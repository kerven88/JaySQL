package checker

import (
	"errors"
	"strings"
	"testing"
)

// 语法错误必须携带可定位信息：历史上它以裸 error 上抛，
// 前端只能显示笼统的失败提示，用户看不到出错位置。
func TestNewSyntaxErrorExtractsPosition(t *testing.T) {
	cases := []struct {
		name    string
		errText string
		line    int
		column  int
		near    string
	}{
		{"tidb 标准格式", `line 1 column 15 near "talbe" `, 1, 15, "talbe"},
		{"多行语句", `line 3 column 7 near "FORM"`, 3, 7, "FORM"},
		{"无位置信息", `some unknown parse failure`, 1, 0, ""},
	}
	for _, c := range cases {
		se := newSyntaxError(errors.New(c.errText))
		if se.Line != c.line {
			t.Errorf("%s: Line = %d, want %d", c.name, se.Line, c.line)
		}
		if se.Column != c.column {
			t.Errorf("%s: Column = %d, want %d", c.name, se.Column, c.column)
		}
		if se.Near != c.near {
			t.Errorf("%s: Near = %q, want %q", c.name, se.Near, c.near)
		}
		// 任何情况下都不能丢掉原始错误
		if se.Error() != c.errText {
			t.Errorf("%s: Error() = %q, want %q", c.name, se.Error(), c.errText)
		}
	}
}

func TestSyntaxErrorToReturnData(t *testing.T) {
	se := newSyntaxError(errors.New(`line 2 column 9 near "talbe" `))
	data := se.ToReturnData("create table t1 (id int);\ncreate talbe t2 (id int);")
	if len(data) != 1 {
		t.Fatalf("ToReturnData 应返回 1 条结果，实际 %d", len(data))
	}
	row := data[0]
	if row.Line != 2 {
		t.Errorf("Line = %d, want 2", row.Line)
	}
	// 出错行原文要一并带回，前端据此高亮
	if !strings.Contains(row.Query, "talbe") {
		t.Errorf("Query 应包含出错行原文，实际 %q", row.Query)
	}
	if len(row.Summary) != 1 {
		t.Fatalf("Summary 应有 1 条，实际 %d", len(row.Summary))
	}
	sum := row.Summary[0]
	if sum.Category != "syntax_error" {
		t.Errorf("Category = %q, want syntax_error", sum.Category)
	}
	if !strings.Contains(sum.Message, "第 2 行第 10 列") {
		t.Errorf("Message 应包含可读位置，实际 %q", sum.Message)
	}
	// 必须有修复建议，否则用户只知道错了不知道怎么改
	if sum.Advice == "" {
		t.Error("Advice 不能为空")
	}
}
