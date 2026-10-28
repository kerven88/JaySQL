package services

import (
	"strings"
	"testing"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"gorm.io/datatypes"
)

func jsonConfig(t *testing.T, raw string) datatypes.JSON {
	t.Helper()
	return datatypes.JSON([]byte(raw))
}

func TestNormalizeBackupTriggerSource(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"MANUAL", backupTriggerManual},
		{"manual", backupTriggerManual},
		{" manual ", backupTriggerManual},
		{"SCHEDULED", backupTriggerScheduled},
		{"scheduled", backupTriggerScheduled},
		{"", backupTriggerScheduled},         // 历史数据按定时语义兼容
		{"WHATEVER", backupTriggerScheduled}, // 未知值不引入新分支
	}
	for _, c := range cases {
		if got := normalizeBackupTriggerSource(c.in); got != c.want {
			t.Fatalf("normalizeBackupTriggerSource(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if !isBackupTriggerManual("MANUAL") {
		t.Fatal("isBackupTriggerManual(MANUAL) 应为 true")
	}
	if isBackupTriggerManual("SCHEDULED") {
		t.Fatal("isBackupTriggerManual(SCHEDULED) 应为 false")
	}
}

func TestIsMySQLPhysicalBackupTask(t *testing.T) {
	physical := &backupModels.InsightBackupTasks{DriverType: "XTRABACKUP", DBType: "MySQL"}
	if !isMySQLPhysicalBackupTask(physical) {
		t.Fatal("XTRABACKUP + MySQL 应识别为物理备份任务")
	}
	logical := &backupModels.InsightBackupTasks{DriverType: "LOGICAL", DBType: "MySQL"}
	if isMySQLPhysicalBackupTask(logical) {
		t.Fatal("LOGICAL 不应识别为物理备份任务")
	}
	pgPhysical := &backupModels.InsightBackupTasks{DriverType: "PG_BACKREST", DBType: "PostgreSQL"}
	if isMySQLPhysicalBackupTask(pgPhysical) {
		t.Fatal("PG_BACKREST 不应走 MySQL 物理基线判定")
	}
	if isMySQLPhysicalBackupTask(nil) {
		t.Fatal("nil 任务不应识别为物理备份任务")
	}
}

// TestResolveEffectiveBackupType_ManualForcesFull 手动触发的物理任务必须强制全量，
// 且无论任务自身口径、混合策略、是否存在基线，结果都只能是 FULL。
func TestResolveEffectiveBackupType_ManualForcesFull(t *testing.T) {
	cases := []struct {
		name       string
		taskType   string
		driverConf string
	}{
		{"任务口径为增量", "INCR", `{"strategy_mode":"MIXED","full_backup_days":["SUN","WED"],"other_days_type":"INCR"}`},
		{"任务口径为全量", "FULL", `{"strategy_mode":"SINGLE"}`},
		{"未配置策略", "INCR", ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			task := &backupModels.InsightBackupTasks{
				DriverType:   "XTRABACKUP",
				DBType:       "MySQL",
				BackupType:   commonModels.EnumType(c.taskType),
				DriverConfig: jsonConfig(t, c.driverConf),
			}
			gotType, note, err := resolveEffectiveBackupType(task, string(task.BackupType), backupTriggerManual)
			if err != nil {
				t.Fatalf("手动触发不应返回错误: %v", err)
			}
			if gotType != "FULL" {
				t.Fatalf("手动触发物理备份应强制 FULL，实际 %s（note=%s）", gotType, note)
			}
			if !strings.Contains(note, "手动触发") {
				t.Fatalf("提示信息应说明是手动触发，实际: %s", note)
			}
		})
	}
}

// TestResolveEffectiveBackupType_ScheduledSingleFull 定时触发 + 单策略全量：直接 FULL，不做基线探测。
func TestResolveEffectiveBackupType_ScheduledSingleFull(t *testing.T) {
	task := &backupModels.InsightBackupTasks{
		DriverType:   "XTRABACKUP",
		DBType:       "MySQL",
		BackupType:   commonModels.EnumType("FULL"),
		DriverConfig: jsonConfig(t, `{"strategy_mode":"SINGLE"}`),
	}
	gotType, note, err := resolveEffectiveBackupType(task, "FULL", backupTriggerScheduled)
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if gotType != "FULL" {
		t.Fatalf("单策略全量应返回 FULL，实际 %s", gotType)
	}
	if !strings.Contains(note, "定时触发") {
		t.Fatalf("提示信息应说明是定时触发，实际: %s", note)
	}
}

// TestResolveEffectiveBackupType_ScheduledNoBaseNoFallback 定时触发要走增量但基线不可用，
// 且任务关闭了自动降级时必须返回错误（而不是悄悄跑一个会失败的增量）。
func TestResolveEffectiveBackupType_ScheduledNoBaseNoFallback(t *testing.T) {
	task := &backupModels.InsightBackupTasks{
		DriverType:      "XTRABACKUP",
		DBType:          "MySQL",
		BackupType:      commonModels.EnumType("INCR"),
		StorageTargetID: 0, // 未绑定存储目标 → 基线探测必然不可用
		DriverConfig:    jsonConfig(t, `{"strategy_mode":"SINGLE","fallback_to_full_when_missing":false}`),
	}
	_, _, err := resolveEffectiveBackupType(task, "INCR", backupTriggerScheduled)
	if err == nil {
		t.Fatal("基线不可用且关闭自动降级时应返回错误")
	}
	if !strings.Contains(err.Error(), "基线") {
		t.Fatalf("错误信息应说明基线原因，实际: %v", err)
	}
}

// TestResolveEffectiveBackupType_ScheduledNoBaseWithFallback 同上但开启自动降级 → 应降级为 FULL。
func TestResolveEffectiveBackupType_ScheduledNoBaseWithFallback(t *testing.T) {
	task := &backupModels.InsightBackupTasks{
		DriverType:      "XTRABACKUP",
		DBType:          "MySQL",
		BackupType:      commonModels.EnumType("INCR"),
		StorageTargetID: 0,
		DriverConfig:    jsonConfig(t, `{"strategy_mode":"SINGLE","fallback_to_full_when_missing":true}`),
	}
	gotType, note, err := resolveEffectiveBackupType(task, "INCR", backupTriggerScheduled)
	if err != nil {
		t.Fatalf("开启自动降级时不应返回错误: %v", err)
	}
	if gotType != "FULL" {
		t.Fatalf("基线缺失且开启降级时应返回 FULL，实际 %s", gotType)
	}
	if !strings.Contains(note, "降级") {
		t.Fatalf("提示信息应说明已降级，实际: %s", note)
	}
}

func TestDescribeMissingPhysicalBase(t *testing.T) {
	cases := []struct {
		name string
		base *mysqlPhysicalBackupBase
		want string
	}{
		{"nil 基线", nil, "未找到任何物理基线"},
		{"目录里没有 manifest", &mysqlPhysicalBackupBase{ScannedCount: 0}, "首次执行或基线已被清理"},
		{"manifest 全损坏", &mysqlPhysicalBackupBase{ScannedCount: 3, InvalidCount: 3}, "全部无效"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := describeMissingPhysicalBase(c.base)
			if !strings.Contains(got, c.want) {
				t.Fatalf("describeMissingPhysicalBase() = %q, 应包含 %q", got, c.want)
			}
		})
	}
}

func TestIsPhysicalBaseExpired(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		createdAt string
		retention int
		want      bool
	}{
		{"未过期", now.AddDate(0, 0, -3).Format(time.RFC3339), 30, false},
		{"刚好在阈值内", now.AddDate(0, 0, -29).Format(time.RFC3339), 30, false},
		{"已过期", now.AddDate(0, 0, -40).Format(time.RFC3339), 30, true},
		{"时间缺失不判过期", "", 30, false},
		{"时间格式异常不判过期", "not-a-time", 30, false},
		{"保留天数非法时用默认值", now.AddDate(0, 0, -45).Format(time.RFC3339), 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := &mysqlPhysicalBackupBase{CreatedAt: c.createdAt, ToLSN: "1000"}
			expired, _ := isPhysicalBaseExpired(base, c.retention)
			if expired != c.want {
				t.Fatalf("isPhysicalBaseExpired() = %v, want %v", expired, c.want)
			}
		})
	}
}

func TestTruncateRunNote(t *testing.T) {
	long := strings.Repeat("基", 3000)
	got := truncateRunNote(long, 1900)
	if len([]rune(got)) > 1901 {
		t.Fatalf("truncateRunNote 未生效，长度 %d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncateRunNote 缺少截断标记: %q", got[len(got)-20:])
	}
	if truncateRunNote("  短文本  ", 1900) != "短文本" {
		t.Fatalf("truncateRunNote 应去掉首尾空白")
	}
}
