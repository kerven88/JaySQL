package services

import (
	"encoding/json"
	"testing"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/inspect/checker"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	inspectModels "github.com/simajiong/jaysql/internal/inspect/models"
	inspectServices "github.com/simajiong/jaysql/internal/inspect/services"
	ordersModels "github.com/simajiong/jaysql/internal/orders/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// ============================================================================
// 规范预审「规则版本」相关回归
//
// 目标：撤回工单后的重新提交必须是一次全新校验 ——
// 实时读取当前生效规则、记录所用版本、旧结论全部作废并按全量重跑。
// ============================================================================

func testRule(key string, level string, params string) inspectModels.InsightSQLStandardRule {
	return inspectModels.InsightSQLStandardRule{RuleKey: key, Level: level, Params: json.RawMessage(params)}
}

// 规则版本计算：内容相同 → 版本相同；顺序不影响结果
func TestComputeStandardRuleVersionStable(t *testing.T) {
	rulesA := []inspectModels.InsightSQLStandardRule{
		testRule("mysql.struct.table_must_have_pk", "must", `{}`),
		testRule("mysql.struct.limit_tbl_must_comment", "suggest", `{}`),
	}
	rulesB := []inspectModels.InsightSQLStandardRule{
		testRule("mysql.struct.limit_tbl_must_comment", "suggest", `{}`),
		testRule("mysql.struct.table_must_have_pk", "must", `{}`),
	}
	v1 := inspectServices.ComputeStandardRuleVersion(1, rulesA)
	v2 := inspectServices.ComputeStandardRuleVersion(1, rulesB)
	if v1.Version == "" {
		t.Fatalf("version should not be empty")
	}
	if v1.Version != v2.Version {
		t.Errorf("rule order must not affect version: %s != %s", v1.Version, v2.Version)
	}
	if v1.EnabledCount != 2 {
		t.Errorf("enabled count want 2 got %d", v1.EnabledCount)
	}
}

// 规则变动（改级别 / 改参数 / 换规范）必须让版本变化
func TestComputeStandardRuleVersionChanges(t *testing.T) {
	base := []inspectModels.InsightSQLStandardRule{testRule("mysql.struct.table_must_have_pk", "must", `{}`)}
	baseVersion := inspectServices.ComputeStandardRuleVersion(1, base).Version

	off := []inspectModels.InsightSQLStandardRule{testRule("mysql.struct.table_must_have_pk", "off", `{}`)}
	if got := inspectServices.ComputeStandardRuleVersion(1, off).Version; got == baseVersion {
		t.Errorf("disable rule must change version, both %s", got)
	}

	paramChanged := []inspectModels.InsightSQLStandardRule{testRule("mysql.struct.table_must_have_pk", "must", `{"max_length":128}`)}
	if got := inspectServices.ComputeStandardRuleVersion(1, paramChanged).Version; got == baseVersion {
		t.Errorf("rule params change must change version, both %s", got)
	}

	if got := inspectServices.ComputeStandardRuleVersion(2, base).Version; got == baseVersion {
		t.Errorf("standard change must change version, both %s", got)
	}
}

// 预审结果必须带上「所用规则版本」与「命中的规则条目」
func TestBuildPreInspectResultWithVersion(t *testing.T) {
	ruleLevels := []inspectServices.StandardRuleLevel{
		{RuleKey: "mysql.struct.table_must_have_pk", Name: "表要有主键", Description: "表要有主键", Level: "must"},
		{RuleKey: "mysql.struct.limit_tbl_must_comment", Name: "限制表必须注释", Description: "表备注", Level: "suggest"},
	}
	returnData := []checker.ReturnData{
		{
			Line: 1,
			Summary: []controllers.SummaryItem{
				{Level: "WARN", RuleKey: "mysql.struct.table_must_have_pk", Message: "表`t1`必须定义主键"},
				{Level: "WARN", RuleKey: "mysql.struct.limit_tbl_must_comment", Message: "表`t1`必须要有注释"},
				{Level: "INFO", Message: "语法正确"},
			},
		},
	}
	version := inspectServices.StandardRuleVersion{
		Bound: true, StandardID: 7, Version: "abc123", RuleCount: 2, EnabledCount: 2, CapturedAt: "2026-09-29 15:00:00",
	}
	result := buildPreInspectResultWithVersion(returnData, ruleLevels, "suggest", version)

	if result["rule_version"] != "abc123" {
		t.Errorf("rule_version want abc123 got %v", result["rule_version"])
	}
	if result["status"] != "must_fix" {
		t.Errorf("status want must_fix got %v", result["status"])
	}
	if result["must_count"] != 1 || result["suggest_count"] != 1 {
		t.Errorf("counts want 1/1 got %v/%v", result["must_count"], result["suggest_count"])
	}

	items, ok := result["items"].([]preInspectItem)
	if !ok || len(items) != 2 {
		t.Fatalf("items want 2 got %#v", result["items"])
	}
	if items[0].RuleKey != "mysql.struct.table_must_have_pk" || items[0].RuleName != "表要有主键" {
		t.Errorf("item should carry hit rule, got %+v", items[0])
	}
	if items[0].Level != "must" || items[1].Level != "suggest" {
		t.Errorf("item level wrong: %+v %+v", items[0], items[1])
	}

	hitRules, ok := result["hit_rules"].([]hitRuleItem)
	if !ok || len(hitRules) != 2 {
		t.Fatalf("hit_rules want 2 got %#v", result["hit_rules"])
	}
	if hitRules[0].RuleKey != "mysql.struct.table_must_have_pk" || hitRules[0].Count != 1 {
		t.Errorf("hit_rules content wrong: %+v", hitRules[0])
	}

	// 同一规则命中多次应聚合计数
	dup := []checker.ReturnData{
		{Summary: []controllers.SummaryItem{
			{Level: "WARN", RuleKey: "mysql.struct.table_must_have_pk", Message: "表`t1`必须定义主键"},
			{Level: "WARN", RuleKey: "mysql.struct.table_must_have_pk", Message: "表`t2`必须定义主键"},
		}},
	}
	dupResult := buildPreInspectResultWithVersion(dup, ruleLevels, "suggest", version)
	dupHits, _ := dupResult["hit_rules"].([]hitRuleItem)
	if len(dupHits) != 1 || dupHits[0].Count != 2 {
		t.Errorf("duplicate hits should aggregate, got %#v", dupHits)
	}
}

// 快照版本判定：版本一致才有效；旧快照（无版本记录）在有版本时必须重建
func TestSnapshotRuleVersionIsCurrent(t *testing.T) {
	cases := []struct {
		name     string
		snapshot string
		current  string
		want     bool
	}{
		{"版本一致", "v1", "v1", true},
		{"版本漂移", "v1", "v2", false},
		{"旧快照无版本+已有版本", "", "v2", false},
		{"未绑定规范+旧快照", "", "", true},
	}
	for _, c := range cases {
		snap := &ordersModels.InsightOrderPreInspections{RuleVersion: c.snapshot}
		if got := snapshotRuleVersionIsCurrent(snap, c.current); got != c.want {
			t.Errorf("%s: snapshot=%q current=%q got %v want %v", c.name, c.snapshot, c.current, got, c.want)
		}
	}
	if snapshotRuleVersionIsCurrent(nil, "v1") {
		t.Errorf("nil snapshot must not be current")
	}
}

// 撤回工单作废旧结论：快照行必须被删除，之后只能按当前规则全量重跑
func TestInvalidateOrderPreInspectSnapshot(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:pre_inspect_invalidate?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.AutoMigrate(&ordersModels.InsightOrderPreInspections{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	orderID := "tk-test000001"
	db.Where("order_id = ?", orderID).Delete(&ordersModels.InsightOrderPreInspections{})
	record := &ordersModels.InsightOrderPreInspections{
		Model:       &commonModels.Model{},
		OrderID:     orderID,
		ContentHash: "hash",
		OrderStage:  "WAIT_PRECHECK",
		Status:      "success",
		RuleVersion: "v1",
	}
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("seed snapshot failed: %v", err)
	}

	InvalidateOrderPreInspectSnapshot(orderID, db)

	var count int64
	db.Model(&ordersModels.InsightOrderPreInspections{}).Where("order_id = ?", orderID).Count(&count)
	if count != 0 {
		t.Errorf("snapshot should be invalidated, remaining %d", count)
	}

	// 幂等：重复作废不报错
	InvalidateOrderPreInspectSnapshot(orderID, db)
	InvalidateOrderPreInspectSnapshot("", db)
}

// 预审结果里的规则版本要能被回写逻辑正确取出
func TestPreInspectRuleVersionOf(t *testing.T) {
	if got := preInspectRuleVersionOf(map[string]any{"rule_version": " v9 "}); got != "v9" {
		t.Errorf("want v9 got %q", got)
	}
	if got := preInspectRuleVersionOf(nil); got != "" {
		t.Errorf("want empty got %q", got)
	}
	if got := preInspectRuleVersionOf(map[string]any{}); got != "" {
		t.Errorf("want empty got %q", got)
	}
}
