package models

import (
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"gorm.io/datatypes"
)

// InsightMonitorLoadSnapshots 负载快照（AWR 式）：对单个实例的负载状态拍一份诊断报告。
//
// 存储设计：快照头表 + payload JSON（报告全量内容），与时序 points 表完全隔离 ——
// 快照是低频诊断动作，不进入 points 的清理 / 聚合 / 告警链路。
// 状态机：pending → collecting（基线采样）→ window（等待窗口走完）→ ready | failed。
// 进程崩溃恢复：worker 启动时把滞留在 collecting/window 且 window_end_at 已超时
// 的行标记为 failed（详见 RunLoadSnapshotWorker）。
type InsightMonitorLoadSnapshots struct {
	*commonModels.Model
	InstanceID      string                `gorm:"column:instance_id;type:char(15);not null;index;comment:实例ID" json:"instance_id"`
	DbType          string                `gorm:"column:db_type;type:varchar(32);not null;default:'';comment:数据库类型" json:"db_type"`
	DbVersion       string                `gorm:"column:db_version;type:varchar(64);not null;default:'';comment:数据库版本" json:"db_version"`
	InstanceName    string                `gorm:"column:instance_name;type:varchar(160);not null;default:'';comment:实例名 host:port" json:"instance_name"`
	EnvironmentName string                `gorm:"column:environment_name;type:varchar(64);not null;default:'';comment:环境名" json:"environment_name"`
	Status          string                `gorm:"column:status;type:varchar(16);not null;default:'pending';index;comment:pending/collecting/window/ready/failed" json:"status"`
	Progress        int                   `gorm:"column:progress;type:int;not null;default:0;comment:进度百分比 0-100" json:"progress"`
	WindowMinutes   int                   `gorm:"column:window_minutes;type:int;not null;default:15;comment:观察窗口分钟数" json:"window_minutes"`
	BaselineAt      *commonModels.LocalTime `gorm:"column:baseline_at;comment:基线采样时刻" json:"baseline_at"`
	WindowEndAt     *commonModels.LocalTime `gorm:"column:window_end_at;index;comment:窗口结束时刻（到点后做第二次采样）" json:"window_end_at"`
	ErrorMessage    string                `gorm:"column:error_message;type:varchar(512);not null;default:'';comment:失败原因" json:"error_message"`
	Payload         datatypes.JSON        `gorm:"column:payload;type:json;comment:报告全量内容（分章节 JSON）" json:"payload"`
	CreatedBy       string                `gorm:"column:created_by;type:varchar(64);not null;default:'';comment:生成人" json:"created_by"`
}

func (InsightMonitorLoadSnapshots) TableName() string {
	return "insight_monitor_load_snapshots"
}
