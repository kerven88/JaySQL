package process

import (
	"testing"

	"github.com/mitchellh/mapstructure"
)

// TestExplainOutputDecodeIndexColumns EXPLAIN 的 type/key/possible_keys 列必须能被解析，
// 否则「必须使用索引」判定拿不到依据（mapstructure 默认按字段名匹配，
// possible_keys 这类带下划线的列名必须靠 mapstructure tag）。
func TestExplainOutputDecodeIndexColumns(t *testing.T) {
	rows := []map[string]interface{}{
		{
			"table":         "users",
			"type":          "ALL",
			"key":           "NULL",
			"possible_keys": "idx_status",
			"rows":          "1000",
			"Extra":         "Using where",
		},
	}
	var data []ExplainOutput
	if err := mapstructure.WeakDecode(&rows, &data); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(data) != 1 {
		t.Fatalf("decoded rows = %d, want 1", len(data))
	}
	if data[0].Table != "users" {
		t.Errorf("Table = %q, want users", data[0].Table)
	}
	if data[0].Type != "ALL" {
		t.Errorf("Type = %q, want ALL（type 列未解析会导致索引判定失效）", data[0].Type)
	}
	if data[0].Key != "NULL" {
		t.Errorf("Key = %q, want NULL", data[0].Key)
	}
	if data[0].PossibleKeys != "idx_status" {
		t.Errorf("PossibleKeys = %q, want idx_status（possible_keys 需要 mapstructure tag）", data[0].PossibleKeys)
	}
	if data[0].Extra != "Using where" {
		t.Errorf("Extra = %q, want \"Using where\"（MySQL 8 列名为 \"Extra\"，依赖 mapstructure 大小写不敏感匹配）", data[0].Extra)
	}
}

func TestJudgeIndexUsage(t *testing.T) {
	cases := []struct {
		name      string
		data      []ExplainOutput
		wantCheck bool
		wantUsed  bool
		wantTable string
	}{
		{
			name: "全表扫描(ALL)=>未使用索引",
			data: []ExplainOutput{
				{Table: "users", Type: "ALL", Key: "NULL", PossibleKeys: "idx_status"},
			},
			wantCheck: true, wantUsed: false, wantTable: "users",
		},
		{
			// MySQL 8 的 EXPLAIN UPDATE 在没有可用索引时是 type=index + key=PRIMARY，
			// 只看 key 会漏判，这条用例锁住该行为
			name: "全索引扫描(index, key=PRIMARY)=>未使用索引",
			data: []ExplainOutput{
				{Table: "users", Type: "index", Key: "PRIMARY", PossibleKeys: "NULL"},
			},
			wantCheck: true, wantUsed: false, wantTable: "users",
		},
		{
			name: "命中索引(type=range)=>通过",
			data: []ExplainOutput{
				{Table: "users", Type: "range", Key: "idx_status", PossibleKeys: "idx_status"},
			},
			wantCheck: true, wantUsed: true,
		},
		{
			name: "主键等值(type=ref)=>通过",
			data: []ExplainOutput{
				{Table: "users", Type: "ref", Key: "PRIMARY", PossibleKeys: "PRIMARY"},
			},
			wantCheck: true, wantUsed: true,
		},
		{
			name: "多表UPDATE：主表走索引、被驱动表全表扫描=>未使用索引",
			data: []ExplainOutput{
				{Table: "orders", Type: "ref", Key: "idx_uid"},
				{Table: "users", Type: "ALL", Key: "NULL"},
			},
			wantCheck: true, wantUsed: false, wantTable: "users",
		},
		{
			name: "派生表无索引=>跳过，不误报",
			data: []ExplainOutput{
				{Table: "users", Type: "ref", Key: "idx_status"},
				{Table: "<derived2>", Type: "ALL", Key: "NULL"},
			},
			wantCheck: true, wantUsed: true,
		},
		{
			name: "表名NULL(Impossible WHERE)=>跳过",
			data: []ExplainOutput{
				{Table: "NULL", Type: "NULL", Key: "NULL"},
			},
			wantCheck: false, wantUsed: false,
		},
		{
			name: "无type/key列(TiDB形态)=>不判定",
			data: []ExplainOutput{
				{Table: "users", EstRows: "1000"},
			},
			wantCheck: false, wantUsed: false,
		},
		{
			name:      "空结果=>不判定",
			data:      []ExplainOutput{},
			wantCheck: false, wantUsed: false,
		},
	}

	for _, c := range cases {
		got := judgeIndexUsage(c.data)
		if got.Checked != c.wantCheck {
			t.Errorf("%s: Checked = %v, want %v", c.name, got.Checked, c.wantCheck)
		}
		if got.Used != c.wantUsed {
			t.Errorf("%s: Used = %v, want %v", c.name, got.Used, c.wantUsed)
		}
		if c.wantTable != "" && got.Table != c.wantTable {
			t.Errorf("%s: Table = %q, want %q", c.name, got.Table, c.wantTable)
		}
	}
}

// TestExplainIndexIsUsed key 列取值的边界：DAO 层把 SQL NULL 转成字符串 "NULL"
func TestExplainIndexIsUsed(t *testing.T) {
	if explainIndexIsUsed("NULL") {
		t.Error(`key="NULL" 应判为未使用索引`)
	}
	if explainIndexIsUsed("") {
		t.Error(`key="" 应判为未使用索引`)
	}
	if explainIndexIsUsed("  ") {
		t.Error(`key 仅空白应判为未使用索引`)
	}
	if !explainIndexIsUsed("idx_status") {
		t.Error(`key="idx_status" 应判为已使用索引`)
	}
}

// TestExplainUsesIndexTypePrecedence type 优先于 key：
// UPDATE 场景常见的 type=index + key=PRIMARY 必须判为未使用索引（MySQL 8 实测行为）
func TestExplainUsesIndexTypePrecedence(t *testing.T) {
	cases := []struct {
		accessType string
		key        string
		extra      string
		wantUsed   bool
	}{
		{"ALL", "NULL", "", false},
		{"ALL", "", "", false},
		{"index", "PRIMARY", "", false},
		// MySQL 8 EXPLAIN UPDATE 无可用索引时：type=index + key=PRIMARY，无 Using index
		{"index", "PRIMARY", "Using where", false},
		// 覆盖索引扫描：Extra 含 Using index（不回表），属于真用上了索引
		{"index", "uniq_permission_code", "Using where; Using index", true},
		{"index", "idx_a", "Using index", true},
		// 只有 "Using index condition"（ICP，仍需回表）不算覆盖扫描
		{"index", "idx_a", "Using index condition", false},
		{"range", "idx_a", "", true},
		{"ref", "PRIMARY", "", true},
		{"eq_ref", "PRIMARY", "", true},
		{"const", "PRIMARY", "", true},
		{"index_merge", "idx_a,idx_b", "", true},
		{"", "idx_a", "", true}, // 无 type 列时退回按 key 判定
		{"", "NULL", "", false}, // 无索引可选
	}
	for _, c := range cases {
		if got := explainUsesIndex(c.accessType, c.key, c.extra); got != c.wantUsed {
			t.Errorf("explainUsesIndex(type=%q, key=%q, extra=%q) = %v, want %v", c.accessType, c.key, c.extra, got, c.wantUsed)
		}
	}
}

// TestJudgeIndexUsageCoveringIndexScan 多表删除的覆盖索引扫描不应误报：
// 实测 `DELETE rp FROM a rp JOIN b p ...` 的 EXPLAIN 中 p 行
// type=index + key=uniq_permission_code + Extra="Using where; Using index"
func TestJudgeIndexUsageCoveringIndexScan(t *testing.T) {
	got := judgeIndexUsage([]ExplainOutput{
		{Table: "p", Type: "index", Key: "uniq_permission_code", PossibleKeys: "PRIMARY,uniq_permission_code", Extra: "Using where; Using index"},
		{Table: "rp", Type: "eq_ref", Key: "uniq_role_permission", Extra: "Using where"},
	})
	if !got.Checked || !got.Used {
		t.Fatalf("Checked=%v Used=%v, want 全部使用索引（覆盖索引扫描被误报为未使用）", got.Checked, got.Used)
	}
}
