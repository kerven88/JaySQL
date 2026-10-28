package forms

import "github.com/simajiong/jaysql/pkg/pagination"

type GetSlowSQLInstanceSummaryForm struct {
	Search     string `form:"search" json:"search"`
	ScoreLevel string `form:"score_level" json:"score_level"`
	StartAt    string `form:"start_at" json:"start_at"`
	EndAt      string `form:"end_at" json:"end_at"`
	// IncludeUncollectable 已废弃：列表现在恒返回全部三类引擎实例，
	// 采集失败的照常展示（状态列给出原因），不再需要这个开关。
	// 保留字段只为兼容旧调用方传参，服务端不再读取。
	IncludeUncollectable bool `form:"include_uncollectable" json:"include_uncollectable"`
	// StatusFilter 状态筛选：空 / all = 全部；ok = 采集正常；
	// failed = 采集失败；not_ready = 不具备采集条件（未开启 / 参数不兼容 / 版本不支持 / 未探测）。
	// 注意：筛选的是"用户此刻想看哪一类"，不是"平台允许哪一类出现"。
	StatusFilter string `form:"status_filter" json:"status_filter"`
}

type GetInstanceOptionsForm struct {
	Search        string `form:"search" json:"search"`
	SupportedOnly bool   `form:"supported_only" json:"supported_only"`
	// CollectableOnly 只返回可采集（ENABLED_FILE）实例，供采集类页面复用
	CollectableOnly bool `form:"collectable_only" json:"collectable_only"`
}

type GetOverviewForm struct {
	Search string `form:"search" json:"search"`
	// DBType / Business 顶部筛选：空=全部；RangeDays 慢SQL趋势窗口（3/7/14/30，默认 7）
	DBType    string `form:"db_type" json:"db_type"`
	Business  string `form:"business" json:"business"`
	RangeDays int    `form:"range_days" json:"range_days"`
}

type GetSlowSQLForm struct {
	pagination.Pagination
	Search        string `form:"search" json:"search"`
	DBType        string `form:"db_type" json:"db_type"`
	InstanceID    string `form:"instance_id" json:"instance_id"`
	SchemaName    string `form:"schema_name" json:"schema_name"`
	SQLDigest     string `form:"sql_digest" json:"sql_digest"`
	MinDurationMS int    `form:"min_duration_ms" json:"min_duration_ms"`
	StartAt       string `form:"start_at" json:"start_at"`
	EndAt         string `form:"end_at" json:"end_at"`
	// SortField/SortOrder 服务端排序（白名单校验）：空 = 默认按采集时间倒序
	SortField string `form:"sort_field" json:"sort_field"`
	SortOrder string `form:"sort_order" json:"sort_order"`
}

type GetSlowSQLStatsForm struct {
	pagination.Pagination
	Search        string `form:"search" json:"search"`
	DBType        string `form:"db_type" json:"db_type"`
	InstanceID    string `form:"instance_id" json:"instance_id"`
	MinDurationMS int    `form:"min_duration_ms" json:"min_duration_ms"`
	StartAt       string `form:"start_at" json:"start_at"`
	EndAt         string `form:"end_at" json:"end_at"`
	// SortField/SortOrder 服务端排序（白名单校验）：空 = 默认按最新时间倒序
	SortField string `form:"sort_field" json:"sort_field"`
	SortOrder string `form:"sort_order" json:"sort_order"`
}

type GetSlowSQLAnalysisForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required"`
	StartAt    string `form:"start_at" json:"start_at"`
	EndAt      string `form:"end_at" json:"end_at"`
}

type GetSnapshotParamListForm struct {
	pagination.Pagination
	Search     string `form:"search" json:"search"`
	DBType     string `form:"db_type" json:"db_type"`
	ScopeType  string `form:"scope_type" json:"scope_type"`
	InstanceID string `form:"instance_id" json:"instance_id"`
}

type SaveSnapshotParamForm struct {
	Name                  string `json:"name" binding:"required,min=2,max=128"`
	DBType                string `json:"db_type" binding:"required,oneof=MySQL PostgreSQL"`
	ScopeType             string `json:"scope_type" binding:"required,oneof=global instance"`
	ScopeValue            string `json:"scope_value" binding:"max=15"`
	SlowSQLThresholdMS    int    `json:"slow_sql_threshold_ms" binding:"required,min=100,max=3600000"`
	SnapshotWindowMinutes int    `json:"snapshot_window_minutes" binding:"required,min=1,max=1440"`
	TopSQLLimit           int    `json:"top_sql_limit" binding:"required,min=1,max=200"`
	RetentionDays         int    `json:"retention_days" binding:"required,min=1,max=365"`
	Enabled               bool   `json:"enabled"`
	Remark                string `json:"remark" binding:"max=255"`
}
