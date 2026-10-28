package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

func TestDeriveRestoreModeFromPolicies(t *testing.T) {
	cases := []struct {
		name         string
		schemaPolicy string
		dataPolicy   string
		expect       string
	}{
		{"双忽略冲突->追加", restoreSchemaConflictSkip, restoreDataConflictSkip, logicalRestoreModeAppend},
		{"结构已存在则停止->新建", restoreSchemaConflictAbort, restoreDataConflictSkip, logicalRestoreModeNew},
		{"先删后写->覆盖", restoreSchemaConflictSkip, restoreDataConflictTruncate, logicalRestoreModeOverwrite},
		{"先删后写优先于结构策略", restoreSchemaConflictAbort, restoreDataConflictTruncate, logicalRestoreModeOverwrite},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got := deriveRestoreModeFromPolicies(item.schemaPolicy, item.dataPolicy)
			if got != item.expect {
				t.Fatalf("期望 %s，实际 %s", item.expect, got)
			}
		})
	}
}

func TestNormalizeLogicalRestoreOptionsWizardFields(t *testing.T) {
	input := logicalRestorePlanInput{
		Mode:                 "execute",
		RestoreMode:          "",
		SchemaConflictPolicy: "skip",
		DataConflictPolicy:   "truncate",
		DBMappings: []logicalRestoreDBMapping{
			{SourceDB: " game3 ", TargetDB: " game3_new "},
			{SourceDB: "game3", TargetDB: "dup"},
			{SourceDB: "", TargetDB: "ignored"},
		},
	}
	options := normalizeLogicalRestoreOptions(input)
	if options.RestoreMode != logicalRestoreModeOverwrite {
		t.Fatalf("数据冲突 TRUNCATE 应推导为 OVERWRITE，实际 %s", options.RestoreMode)
	}
	if !options.RestoreSchema || !options.RestoreData {
		t.Fatalf("恢复类型默认应为结构与数据都恢复，实际 schema=%v data=%v", options.RestoreSchema, options.RestoreData)
	}
	if options.ObjectScope != restoreObjectScopeCustom {
		t.Fatalf("存在库映射时应推断为 CUSTOM，实际 %s", options.ObjectScope)
	}
	if len(options.DBMappings) != 1 || options.DBMappings[0].SourceDB != "game3" || options.DBMappings[0].TargetDB != "game3_new" {
		t.Fatalf("库映射归一化失败: %+v", options.DBMappings)
	}
}

func TestNormalizeLogicalRestoreOptionsBackwardCompatible(t *testing.T) {
	// 旧调用方只传 restore_mode，新字段缺省时行为必须保持不变。
	options := normalizeLogicalRestoreOptions(logicalRestorePlanInput{RestoreMode: "new"})
	if options.RestoreMode != logicalRestoreModeNew {
		t.Fatalf("显式 restore_mode 应保留，实际 %s", options.RestoreMode)
	}
	if options.SchemaConflictPolicy != restoreSchemaConflictAbort || options.DataConflictPolicy != restoreDataConflictSkip {
		t.Fatalf("应由 restore_mode 反推策略，实际 %s/%s", options.SchemaConflictPolicy, options.DataConflictPolicy)
	}
	if !options.RestoreSchema || !options.RestoreData {
		t.Fatal("旧调用方默认应恢复结构与数据")
	}
	if options.ObjectScope != restoreObjectScopeAll {
		t.Fatalf("未选对象时应为 ALL，实际 %s", options.ObjectScope)
	}
}

// TestNormalizeLogicalRestoreOptionsAlwaysResolvesRestoreMode 是回归防线。
// 背景：向导只提交冲突策略字段（不带 restore_mode），底层模式由这里推导；
// prepareLogicalRestorePlan 必须在目标校验之前调用本函数并回写结果，
// 否则会出现「restore_mode 不能为空」把向导第 4 步预检查直接卡死。
func TestNormalizeLogicalRestoreOptionsAlwaysResolvesRestoreMode(t *testing.T) {
	cases := []struct {
		name         string
		schemaPolicy string
		dataPolicy   string
		expect       string
	}{
		{"策略全缺省->追加", "", "", logicalRestoreModeAppend},
		{"双忽略->追加", restoreSchemaConflictSkip, restoreDataConflictSkip, logicalRestoreModeAppend},
		{"结构中止->新建", restoreSchemaConflictAbort, restoreDataConflictSkip, logicalRestoreModeNew},
		{"数据清空->覆盖", restoreSchemaConflictSkip, restoreDataConflictTruncate, logicalRestoreModeOverwrite},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			options := normalizeLogicalRestoreOptions(logicalRestorePlanInput{
				SchemaConflictPolicy: item.schemaPolicy,
				DataConflictPolicy:   item.dataPolicy,
			})
			if strings.TrimSpace(options.RestoreMode) == "" {
				t.Fatal("归一化后 restore_mode 仍为空，目标校验会直接失败")
			}
			if options.RestoreMode != item.expect {
				t.Fatalf("期望 %s，实际 %s", item.expect, options.RestoreMode)
			}
		})
	}
}

func TestValidateRestoreTypeSelection(t *testing.T) {
	if err := validateRestoreTypeSelection(logicalRestoreRunOptions{RestoreSchema: true, RestoreData: false}); err != nil {
		t.Fatalf("仅表结构应通过: %v", err)
	}
	if err := validateRestoreTypeSelection(logicalRestoreRunOptions{RestoreSchema: false, RestoreData: false}); err == nil {
		t.Fatal("两者都不选应报错")
	}
}

func TestLogicalRestoreTargetForSource(t *testing.T) {
	options := logicalRestoreRunOptions{DBMappings: []logicalRestoreDBMapping{{SourceDB: "game3", TargetDB: "game3_new"}}}
	target, ok := logicalRestoreTargetForSource(options, "GAME3")
	if !ok || target != "game3_new" {
		t.Fatalf("映射应忽略大小写命中，实际 ok=%v target=%s", ok, target)
	}
	if _, ok := logicalRestoreTargetForSource(options, "unknown"); ok {
		t.Fatal("未配置的源库不应命中映射")
	}
}

func TestEnsureRestoreHomogeneous(t *testing.T) {
	mysqlSet := &backupModels.InsightBackupSets{DBType: "MySQL"}
	pgInstance := &commonModels.InsightInstances{DbType: "PostgreSQL"}
	if err := ensureRestoreHomogeneous(mysqlSet, pgInstance); err == nil {
		t.Fatal("MySQL 备份集恢复到 PG 实例必须被拒绝")
	}
	mysqlInstance := &commonModels.InsightInstances{DbType: "MySQL"}
	if err := ensureRestoreHomogeneous(mysqlSet, mysqlInstance); err != nil {
		t.Fatalf("同构恢复不应被拒绝: %v", err)
	}
}

func TestBuildMySQLRestoreTypeFilter(t *testing.T) {
	if got := buildMySQLRestoreTypeFilter(true, true); got != "" {
		t.Fatalf("结构与数据都恢复时不应附加过滤器，实际 %s", got)
	}
	schemaOnly := buildMySQLRestoreTypeFilter(true, false)
	if !strings.Contains(schemaOnly, "kd=0") || !strings.Contains(schemaOnly, "ks=1") {
		t.Fatalf("仅表结构过滤器参数错误: %s", schemaOnly)
	}
	dataOnly := buildMySQLRestoreTypeFilter(false, true)
	if !strings.Contains(dataOnly, "kd=1") || !strings.Contains(dataOnly, "ks=0") {
		t.Fatalf("仅数据过滤器参数错误: %s", dataOnly)
	}
}

func TestMySQLGrantsAllowRestore(t *testing.T) {
	allowed, missing := mySQLGrantsAllowRestore([]string{"GRANT ALL PRIVILEGES ON *.* TO 'u'@'%'"})
	if !allowed || len(missing) != 0 {
		t.Fatalf("ALL PRIVILEGES 应判定通过，实际 allowed=%v missing=%v", allowed, missing)
	}
	allowed, missing = mySQLGrantsAllowRestore([]string{"GRANT SELECT, INSERT ON db.* TO 'u'@'%'"})
	if allowed {
		t.Fatal("缺少 CREATE/DROP 权限应判定不足")
	}
	if len(missing) == 0 {
		t.Fatal("应给出缺失权限清单")
	}
	if allowed, _ := mySQLGrantsAllowRestore(nil); allowed {
		t.Fatal("无授权信息应判定不足")
	}
}

func TestLocatePGBackRestBackupLabel(t *testing.T) {
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	stanzas := []pgBackRestInfoStanza{{
		Name: "pg_1_5432_bk1",
		Backup: []pgBackRestInfoBackup{
			{Label: "20260930-110000F", Type: "full", Timestamp: struct {
				Start int64 `json:"start"`
				Stop  int64 `json:"stop"`
			}{Start: base.Add(-time.Hour).Unix()}},
			{Label: "20260930-120000F", Type: "full", Timestamp: struct {
				Start int64 `json:"start"`
				Stop  int64 `json:"stop"`
			}{Start: base.Unix()}},
			{Label: "20260930-123000I", Type: "incr", Timestamp: struct {
				Start int64 `json:"start"`
				Stop  int64 `json:"stop"`
			}{Start: base.Add(30 * time.Minute).Unix()}},
		},
	}}

	fullSet := &backupModels.InsightBackupSets{BackupType: "FULL", Model: &commonModels.Model{}}
	fullSet.CreatedAt = commonModels.LocalTime(base)
	label, labelType, count, ok := locatePGBackRestBackupLabel(fullSet, "pg_1_5432_bk1", stanzas)
	if !ok || label != "20260930-120000F" || labelType != "full" || count != 3 {
		t.Fatalf("全量备份定位失败: label=%s type=%s count=%d ok=%v", label, labelType, count, ok)
	}

	incrSet := &backupModels.InsightBackupSets{BackupType: "INCR", Model: &commonModels.Model{}}
	incrSet.CreatedAt = commonModels.LocalTime(base.Add(31 * time.Minute))
	label, labelType, _, ok = locatePGBackRestBackupLabel(incrSet, "pg_1_5432_bk1", stanzas)
	if !ok || label != "20260930-123000I" || labelType != "incr" {
		t.Fatalf("非全量备份定位失败: label=%s type=%s ok=%v", label, labelType, ok)
	}

	if _, _, _, ok := locatePGBackRestBackupLabel(fullSet, "pg_1_5432_bk1", nil); ok {
		t.Fatal("仓库无备份时不应定位成功")
	}
}

func TestSanitizePGBackRestLabel(t *testing.T) {
	if got := sanitizePGBackRestLabel(" 20260930-120000F "); got != "20260930-120000F" {
		t.Fatalf("合法 label 应保留，实际 %q", got)
	}
	for _, bad := range []string{"", "2026;rm -rf /", "label`whoami`", "a b"} {
		if got := sanitizePGBackRestLabel(bad); got != "" {
			t.Fatalf("非法 label %q 应被拒绝，实际 %q", bad, got)
		}
	}
}

func TestExtractMySQLDumpDatabasesAndSQLMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jaysql.sql")
	content := "/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;\n" +
		"/*!40101 SET NAMES utf8mb4 */;\n" +
		"SET @@SESSION.SQL_MODE='ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES';\n" +
		"CREATE DATABASE /*!32312 IF NOT EXISTS*/ `jaysql`;\n" +
		"USE `jaysql`;\n" +
		"CREATE TABLE `t1` (`id` int);\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入临时 dump 失败: %v", err)
	}
	names := extractMySQLDumpDatabases(path)
	if len(names) != 1 || names[0] != "jaysql" {
		t.Fatalf("库名解析失败: %v", names)
	}
	mode := extractMySQLDumpSQLMode(path)
	if !strings.Contains(mode, "ONLY_FULL_GROUP_BY") {
		t.Fatalf("SQL_MODE 解析失败: %q", mode)
	}
	if charset := extractMySQLDumpCharset(path); charset != "utf8mb4" {
		t.Fatalf("字符集解析失败: %q", charset)
	}
	// 非 SQL 产物不应 panic / 不应误报
	if got := extractMySQLDumpDatabases(filepath.Join(dir, "not-exists.sql")); len(got) != 0 {
		t.Fatalf("文件不存在时应返回空: %v", got)
	}
}

func TestDecodeLogicalRestoreOptionsJSONLegacyCompat(t *testing.T) {
	// 历史任务 options（无恢复类型 / 无策略字段）应回退为「结构与数据都恢复 + 由 restore_mode 反推策略」。
	raw := []byte(`{"execution_mode":"EXECUTE","restore_mode":"OVERWRITE","parallel":4,"timeout":"4h"}`)
	options := decodeLogicalRestoreOptionsJSON(raw)
	if !options.RestoreSchema || !options.RestoreData {
		t.Fatalf("历史任务应视为结构与数据都恢复，实际 schema=%v data=%v", options.RestoreSchema, options.RestoreData)
	}
	if options.DataConflictPolicy != restoreDataConflictTruncate {
		t.Fatalf("OVERWRITE 应反推为 TRUNCATE，实际 %s", options.DataConflictPolicy)
	}
	if options.ObjectScope != restoreObjectScopeAll {
		t.Fatalf("历史任务对象范围应为 ALL，实际 %s", options.ObjectScope)
	}
}
