package services

import (
	"errors"
	"strings"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/users/models"
	"gorm.io/gorm"
)

// ErrUserNotFound 按用户名解析不到任何用户行时返回。
var ErrUserNotFound = errors.New("用户不存在")

// CanonicalUserByUsername 按 username 解析出「代表当前登录者的那一行」用户记录。
//
// 背景（2026-10-13 线上故障复盘）：
//
//	insight_users 的 username 上不设唯一索引（见 sql/20260925_users_uniq_uid_only.sql，
//	唯一性只由 uid 决定），软删除（is_deleted=1）的历史行不会被物理清理，因此
//	「删除用户 → 用同名重新新建」会留下多行同名记录。
//
//	此前凡是 `WHERE username = ?` 且不带排序的查询（权限解析、用户信息、登录回写……），
//	在 InnoDB 全表扫描下都会先读到 uid 最小的历史行——通常正是那条已软删、角色绑定
//	已被清空的行；GORM 的 Scan/Take 又只取第一行。于是出现：
//	用户列表里明明绑定了「普通成员」，登录也正常，但权限解析落到已删除的旧 uid，
//	有效权限为空 → 菜单被前端剪掉、接口 403、首页统计全 0。
//
//	判定口径必须与登录链路（middleware/otp.go checkIfUserExist）完全一致：
//	is_deleted asc（未删除优先）→ is_active desc（已激活优先）→ uid asc（最早创建优先）。
//	这样「登录用的是哪一行，权限校验/用户信息读的就是哪一行」，三者不会再错位。
func CanonicalUserByUsername(username string) (models.InsightUsers, error) {
	return ResolveCanonicalUser(global.App.DB, username)
}

// ResolveCanonicalUser 与 CanonicalUserByUsername 同语义，但可注入 DB，便于单测。
func ResolveCanonicalUser(db *gorm.DB, username string) (models.InsightUsers, error) {
	var user models.InsightUsers
	name := strings.TrimSpace(username)
	if name == "" {
		return user, ErrUserNotFound
	}
	res := db.Table("insight_users").
		Where("username = ?", name).
		Order("is_deleted asc, is_active desc, uid asc").
		Take(&user)
	if res.Error != nil {
		if errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return user, ErrUserNotFound
		}
		return user, res.Error
	}
	if res.RowsAffected == 0 || user.Uid == 0 {
		return user, ErrUserNotFound
	}
	return user, nil
}

// IsEffectiveUser 判断解析出的行是否是一个「可用」账号：未被软删且已激活。
// 同名场景下若只剩历史（已删除/未激活）行，则不应再带来任何权限。
func IsEffectiveUser(user models.InsightUsers) bool {
	return !user.IsDeleted && user.IsActive
}

// IsSuperuserByUsername 判断用户名对应的「当前有效账号」是否为超级管理员。
//
// 注意不要退化成 `WHERE username=? AND is_superuser=1` 的裸查：
// 同名软删除历史行同样会被计数，判权结果会跟着历史数据漂移。
func IsSuperuserByUsername(username string) bool {
	user, err := CanonicalUserByUsername(username)
	if err != nil {
		return false
	}
	return IsEffectiveUser(user) && user.IsSuperuser
}
