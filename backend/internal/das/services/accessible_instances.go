package services

import (
	"github.com/simajiong/jaysql/internal/global"
)

// AccessibleInstanceStats 用户「可访问数据源」的统计结果。
//
// 口径 = SQL 工作台左栏数据源树（GetAccessibleInstancesService）完全一致：
// 只统计 insight_das_data_grants 里「当前有效」的显式授权覆盖到的实例。
// 【2026-10-15 改造】不再包含任何隐式自服务授权（超管旁路、admin 角色全量、
// 实例创建人），admin 也只能看到被显式授权的实例。
type AccessibleInstanceStats struct {
	Total    int64
	Normal   int64
	Abnormal int64
}

// CountAccessibleInstances 统计 username 在数据访问权限下可见的实例数量。
//
// 这是「数据源可见性」的唯一实现入口：首页「我的资源 · 数据源」卡与 SQL 工作台
// 数据源树共用同一套 scope 解析与主体过滤逻辑，避免两处各写一份 SQL 造成口径漂移
// （历史上首页工单卡与工单列表就是因为各写一份 where 而出现 8 vs 3 的不一致）。
//
// environment > 0 时只统计该环境下的实例（对应首页的环境切换）。
func CountAccessibleInstances(username string, environment int) (*AccessibleInstanceStats, error) {
	scope, err := resolveDASActorScope(username)
	if err != nil {
		return nil, err
	}
	// 【2026-10-15 收口】超管旁路已从数据链路移除：admin 同样只在被授权的实例上计数。
	type accessibleRow struct {
		InstanceID      string
		LastCheckStatus string
	}
	var rows []accessibleRow

	tx := global.App.DB.Table("insight_instances i").
		Select("i.instance_id, IFNULL(i.last_check_status, '') AS last_check_status")
	if dataGrantEnabled() {
		// 新模型：可见实例 = 显式授权命中的实例，与列表接口同源（AccessibleInstanceIDsByGrant）
		grantedIDs, err := AccessibleInstanceIDsByGrant(scope)
		if err != nil {
			return nil, err
		}
		if len(grantedIDs) == 0 {
			return &AccessibleInstanceStats{}, nil
		}
		tx = tx.Where("i.instance_id IN ?", grantedIDs)
	} else {
		// 迁移前兼容：旧表显式授权行（不再做隐式补齐）
		ensureSelfServiceGrantsForScope(scope)
		tx = tx.Joins("join insight_das_schema_perms p on p.instance_id = i.instance_id")
		tx = applyPrincipalFilter(tx, "p", scope)
	}
	if environment > 0 {
		tx = tx.Where("i.environment = ?", environment)
	}
	if err := tx.Distinct().Scan(&rows).Error; err != nil {
		return nil, err
	}

	stats := &AccessibleInstanceStats{}
	for _, row := range rows {
		stats.Total++
		if row.LastCheckStatus == "success" {
			stats.Normal++
		}
	}
	stats.Abnormal = stats.Total - stats.Normal
	return stats, nil
}
