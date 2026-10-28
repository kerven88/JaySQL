package process

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/pkg/kv"
	"github.com/pingcap/tidb/pkg/parser/mysql"
)

// 规则「限制 CHAR 类型字段长度」的告警文案必须回显配置的上限值：
// 使用者需要从预审消息中直接看到 max 是多少，否则无法确认配置是否生效。
func TestCheckColumnCharToVarcharMessageEchoesLimit(t *testing.T) {
	col := &ColOptions{
		InspectParams: &config.InspectParams{COLUMN_MAX_CHAR_LENGTH: 64},
		Flen:          128,
		Tp:            mysql.TypeString,
		Column:        "email",
		Table:         "t_user",
	}
	err := col.CheckColumnCharToVarchar()
	if err == nil {
		t.Fatal("char(128) 超过上限 64，应当触发告警")
	}
	msg := err.Error()
	if !strings.Contains(msg, "max = 64") {
		t.Fatalf("告警文案必须回显配置上限 64，实际：%s", msg)
	}
	if !strings.Contains(msg, "128") {
		t.Fatalf("告警文案必须包含当前定义长度 128，实际：%s", msg)
	}
}

func TestCheckColumnCharToVarcharEdgeCases(t *testing.T) {
	// 恰好等于上限：不告警（max 表示最大允许长度）
	col := &ColOptions{
		InspectParams: &config.InspectParams{COLUMN_MAX_CHAR_LENGTH: 64},
		Flen:          64,
		Tp:            mysql.TypeString,
	}
	if err := col.CheckColumnCharToVarchar(); err != nil {
		t.Fatalf("char(64) 等于上限不应告警，实际：%v", err)
	}

	// 规则关闭（上限为 0）：不告警
	col = &ColOptions{
		InspectParams: &config.InspectParams{COLUMN_MAX_CHAR_LENGTH: 0},
		Flen:          128,
		Tp:            mysql.TypeString,
	}
	if err := col.CheckColumnCharToVarchar(); err != nil {
		t.Fatalf("规则关闭不应告警，实际：%v", err)
	}

	// varchar 不在 CHAR 规则的判定范围
	col = &ColOptions{
		InspectParams: &config.InspectParams{COLUMN_MAX_CHAR_LENGTH: 64},
		Flen:          128,
		Tp:            mysql.TypeVarchar,
	}
	if err := col.CheckColumnCharToVarchar(); err != nil {
		t.Fatalf("varchar 不应触发 CHAR 长度规则，实际：%v", err)
	}
}

// 「限制字段名大小写」的引擎判定：默认小写，可配置大写；文案回显当前要求
func TestCheckColumnIdentiferNameCase(t *testing.T) {
	// 默认（小写）：小写通过，大写报错且回显「小写」
	col := &ColOptions{
		InspectParams: &config.InspectParams{CHECK_COLUMN_IDENTIFIER: true},
		Column:        "email",
		Table:         "t_user",
	}
	if err := col.CheckColumnIdentifer(); err != nil {
		t.Fatalf("小写列名应通过默认检查，实际：%v", err)
	}
	col.Column = "Email"
	err := col.CheckColumnIdentifer()
	if err == nil {
		t.Fatal("默认小写要求下，混合大小写列名应告警")
	}
	if !strings.Contains(err.Error(), "小写") {
		t.Fatalf("告警文案必须回显大小写要求，实际：%s", err.Error())
	}

	// 配置为大写：全大写通过，小写报错且回显「大写」
	col = &ColOptions{
		InspectParams: &config.InspectParams{CHECK_COLUMN_IDENTIFIER: true, COLUMN_NAME_CASE: "upper"},
		Column:        "EMAIL",
		Table:         "t_user",
	}
	if err := col.CheckColumnIdentifer(); err != nil {
		t.Fatalf("大写列名应通过大写要求检查，实际：%v", err)
	}
	col.Column = "email"
	err = col.CheckColumnIdentifer()
	if err == nil {
		t.Fatal("大写要求下，小写列名应告警")
	}
	if !strings.Contains(err.Error(), "大写") {
		t.Fatalf("告警文案必须回显大小写要求，实际：%s", err.Error())
	}

	// 检查关闭：不告警
	col = &ColOptions{
		InspectParams: &config.InspectParams{CHECK_COLUMN_IDENTIFIER: false, COLUMN_NAME_CASE: "upper"},
		Column:        "email",
	}
	if err := col.CheckColumnIdentifer(); err != nil {
		t.Fatalf("检查关闭不应告警，实际：%v", err)
	}
}

// 回归（2026-10-22）：BLOB/TEXT 族扩容应纳入类型变更兼容白名单。
// text→mediumtext→longtext 与 int→bigint 同为安全扩容，此前被一刀切拒绝，
// 导致元库 content 列 text→mediumtext 的运维工单无法通过预审（Error 1406 的修复路径被堵死）。
func TestCheckColsTypeChangedBlobFamily(t *testing.T) {
	kvc := &kv.KVCache{}
	kvc.Put("dbVersion", "mysql-8.0.36")
	params := &config.InspectParams{ENABLE_COLUMN_TYPE_CHANGE: false, ENABLE_COLUMN_TYPE_CHANGE_COMPATIBLE: true}

	cases := []struct {
		name    string
		colTp   byte // 语句中的新类型
		vColTp  byte // 库中的旧类型
		wantErr bool
	}{
		{"text扩容mediumtext放行", mysql.TypeMediumBlob, mysql.TypeBlob, false},
		{"text扩容longtext放行", mysql.TypeLongBlob, mysql.TypeBlob, false},
		{"mediumtext扩容longtext放行", mysql.TypeLongBlob, mysql.TypeMediumBlob, false},
		{"tinytext扩容text放行", mysql.TypeBlob, mysql.TypeTinyBlob, false},
		{"mediumtext缩回text拒绝", mysql.TypeBlob, mysql.TypeMediumBlob, true},
		{"longtext缩回text拒绝", mysql.TypeBlob, mysql.TypeLongBlob, true},
		{"text跨族到bigint拒绝", mysql.TypeLonglong, mysql.TypeBlob, true},
		{"bigint缩到int拒绝", mysql.TypeLong, mysql.TypeLonglong, true},
		{"int扩到bigint放行", mysql.TypeLonglong, mysql.TypeLong, false},
		{"varchar扩到text拒绝(跨族)", mysql.TypeBlob, mysql.TypeVarchar, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			col := ColOptions{Column: "content", Tp: tc.colTp}
			vCol := ColOptions{Column: "content", Tp: tc.vColTp}
			err := CheckColsTypeChanged(col, vCol, params, kvc, "modify", "t1")
			if tc.wantErr && err == nil {
				t.Fatalf("期望拒绝，实际放行")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望放行，实际拒绝：%v", err)
			}
		})
	}
}
