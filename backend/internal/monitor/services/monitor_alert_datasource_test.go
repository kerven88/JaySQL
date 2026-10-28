package services

import (
	"testing"

	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
)

// 告警卡片要回答「这条告警来自哪个库」，数据源字段必须随告警一起下发 ——
// 事件表只有 instance_id，名称/地址/环境靠实例元信息补齐；元信息缺失时
// 退到 instance_id，绝不能让卡片显示空白。
func TestEventToAlertRecordDatasourceMeta(t *testing.T) {
	meta := alertInstanceMeta{
		InstanceID:      "i-0001",
		InstanceName:    "订单中心主库",
		InstanceTarget:  "10.20.13.11:3306",
		DbType:          "MySQL",
		EnvironmentName: "生产",
	}
	rec := eventToAlertRecord(monitorModels.InsightMonitorAlertEvents{
		InstanceID: "i-0001",
		Name:       "MySQL 连接数使用率过高",
		Level:      "P1",
	}, meta)

	if rec.InstanceID != "i-0001" {
		t.Fatalf("instance_id 未随告警下发: %q", rec.InstanceID)
	}
	if rec.InstanceName != "订单中心主库" {
		t.Fatalf("instance_name 应为备注名: %q", rec.InstanceName)
	}
	if rec.InstanceTarget != "10.20.13.11:3306" || rec.Instance != rec.InstanceTarget {
		t.Fatalf("地址应为 hostname:port 且与 instance 一致: target=%q instance=%q", rec.InstanceTarget, rec.Instance)
	}
	if rec.DbType != "MySQL" || rec.EnvironmentName != "生产" {
		t.Fatalf("类型/环境缺失: db_type=%q env=%q", rec.DbType, rec.EnvironmentName)
	}

	// 元信息缺失（实例已被删除等）：退到事件自带字段，卡片不能空白
	fallback := eventToAlertRecord(monitorModels.InsightMonitorAlertEvents{
		InstanceID: "i-0002",
		DBType:     "PostgreSQL",
		Level:      "P2",
	}, alertInstanceMeta{})
	if fallback.InstanceTarget != "i-0002" || fallback.Instance != "i-0002" {
		t.Fatalf("兜底地址应为 instance_id: %q", fallback.InstanceTarget)
	}
	if fallback.DbType != "PostgreSQL" {
		t.Fatalf("兜底类型应取事件表 db_type: %q", fallback.DbType)
	}
}
