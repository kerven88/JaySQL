package models

import (
	"github.com/simajiong/jaysql/internal/common/models"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/datatypes"
)

func BcryptPW(password string) string {
	cost := 10
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(password), cost)
	return string(hashedPassword)
}

type InsightUsers struct {
	Uid uint64 `gorm:"type:bigint;primaryKey;autoIncrement;comment:用户ID" json:"uid"`
	// 用户唯一性只由 uid（自增主键）决定：username/email/mobile 不设唯一索引。
	// 注意：AutoMigrate 不会删除存量库里已有的 uniq_username/uniq_email/uniq_mobile 索引，
	// 存量环境须执行 backend/sql/20260925_users_uniq_uid_only.sql。
	Username    string           `gorm:"type:varchar(32);not null;comment:用户名" json:"username"`
	Password    string           `gorm:"type:varchar(128);not null;comment:密码" json:"password"`
	Email       string           `gorm:"type:varchar(254);not null;comment:email" json:"email"`
	Mobile      string           `gorm:"type:varchar(11);not null;comment:手机号" json:"mobile"`
	AvatarFile  string           `gorm:"type:varchar(254);null;comment:头像文件地址" json:"avatar_file"`
	IsSuperuser bool             `gorm:"type:boolean;default:false;comment:是否为超级管理员" json:"is_superuser"`
	IsActive    bool             `gorm:"type:boolean;default:true;comment:是否激活" json:"is_active"`
	IsStaff     bool             `gorm:"type:boolean;default:false;comment:是否为员工" json:"is_staff"`
	LastLogin   models.LocalTime `gorm:"comment:最后一次登录" json:"last_login"`
	IsDeleted   bool             `gorm:"type:boolean;default:false;comment:是否软删除" json:"is_deleted"`
	// DingTalkUserID 钉钉企业内部应用的 userid，用于把工单审批通知定向投递给本人。
	// 默认由手机号自动匹配后回填（钉钉「根据手机号获取userid」接口）；管理员也可在
	// 用户管理页手工填写/覆盖——通讯录里手机号缺失或不一致的情况确实存在。
	//
	// 注意列名：GORM 默认命名策略把 DingTalkUserID 落成 `ding_talk_user_id`（与既有的
	// insight_notify_settings.ding_talk_* 一致）。而 GORM 对 Updates(map[string]any{...})
	// 的 key **不做命名转换、直接当列名用** —— 用 map 更新这里时必须写 `ding_talk_user_id`，
	// 写成 dingtalk_user_id 会静默失败（只留一条 Warn，功能看起来正常）。
	DingTalkUserID string `gorm:"type:varchar(64);not null;default:'';comment:钉钉userid" json:"dingtalk_userid"`
	// DingTalkBoundAt 钉钉身份的最近一次确认时间（自动匹配成功或管理员填写时更新）。
	// 仅作可观测性：界面据此区分「从未匹配过」与「匹配过但现在失效」。
	DingTalkBoundAt models.LocalTime `gorm:"comment:钉钉账号绑定时间" json:"dingtalk_bound_at"`
	DateJoined      models.LocalTime `gorm:"index:date_joined;autoCreateTime;comment:加入时间" json:"date_joined"`
	UpdatedAt       models.LocalTime `gorm:"index:idx_updated_at;autoUpdateTime;comment:更新时间" json:"updated_at"`
}

func (InsightUsers) TableName() string {
	return "insight_users"
}

// 角色表
type InsightRoles struct {
	*models.Model
	Code        string         `gorm:"type:varchar(64);not null;uniqueIndex:uniq_code;comment:角色编码" json:"code"`
	Name        string         `gorm:"type:varchar(32);not null;uniqueIndex:uniq_name;comment:角色名" json:"name"`
	Description string         `gorm:"type:varchar(256);null;comment:角色描述" json:"description"`
	Permissions datatypes.JSON `gorm:"type:json;null;comment:权限列表" json:"permissions"`
	IsBuiltin   bool           `gorm:"type:boolean;default:false;comment:是否内置角色" json:"is_builtin"`
	IsDeleted   bool           `gorm:"type:boolean;default:false;comment:是否软删除" json:"is_deleted"`
}

func (InsightRoles) TableName() string {
	return "insight_roles"
}

// key全局唯一
// ParentID+Name组成唯一索引,表示同一级别下节点名不能重复
type InsightOrgs struct {
	ID          uint64           `gorm:"primaryKey" json:"id"`
	Name        string           `gorm:"type:varchar(32);not null;uniqueIndex:uniq_name;comment:节点名" json:"name"`
	ParentID    uint64           `gorm:"not null;default 0;uniqueIndex:uniq_name;comment:父节点ID,0值表示根节点" json:"parent_id"`
	Key         string           `gorm:"type:varchar(256);null;uniqueIndex:uniq_key;comment:搜索路径" json:"key"`
	Level       uint64           `gorm:"not null;default 1;comment:当前节点到根节点的距离或者层级,父节点起始值为1" json:"level"`
	Path        datatypes.JSON   `gorm:"type:json;null;comment:绝对路径" json:"path"`
	Description string           `gorm:"type:varchar(256);null;comment:组织描述" json:"description"`
	Creator     string           `gorm:"type:varchar(64);null;comment:创建人" json:"creator"`
	Updater     string           `gorm:"type:varchar(64);null;comment:更新人" json:"updater"`
	CreatedAt   models.LocalTime `gorm:"index:idx_created_at;autoCreateTime;comment:创建时间" json:"created_at"`
	UpdatedAt   models.LocalTime `gorm:"index:idx_updated_at;autoUpdateTime;comment:更新时间" json:"updated_at"`
}

func (InsightOrgs) TableName() string {
	return "insight_orgs"
}

type InsightOrgUsers struct {
	*models.Model
	Uid             uint64 `gorm:"type:bigint;not null;uniqueIndex:uniq_uid_org;comment:用户ID" json:"uid"`
	OrganizationKey string `gorm:"type:varchar(256);not null;uniqueIndex:uniq_uid_org;index:organization_key;comment:搜索路径" json:"organization_key"`
	RoleID          uint64 `gorm:"type:bigint;null;comment:角色ID" json:"role_id"`
	IsActive        bool   `gorm:"type:boolean;default:true;comment:是否激活" json:"is_active"`
}

func (InsightOrgUsers) TableName() string {
	return "insight_org_users"
}
