package process

// WHERE 条件类规则的表达式采集测试（2026-09-29 新增实现）。
// 只验证「从 WHERE 表达式里采集到的特征是否正确」，告警与开关由 logics 层负责。

import (
	"strings"
	"testing"

	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	_ "github.com/pingcap/tidb/pkg/parser/test_driver"
)

func parseWhereExpr(t *testing.T, where string) ast.ExprNode {
	t.Helper()
	p := parser.New()
	stmts, _, err := p.Parse("UPDATE t SET c = 1 WHERE "+where, "", "")
	if err != nil {
		t.Fatalf("解析失败 %s: %v", where, err)
	}
	stmt, ok := stmts[0].(*ast.UpdateStmt)
	if !ok || stmt.Where == nil {
		t.Fatalf("未取到 WHERE: %s", where)
	}
	return stmt.Where
}

// whereTestMeta 固定表元数据：id 主键(索引)、name 普通索引(字符)、code 唯一键(字符)、status 无索引(数值)
func whereTestMeta() TableColumnMeta {
	return TableColumnMeta{
		TableName:  "t",
		IndexCols:  map[string]struct{}{"id": {}, "name": {}, "code": {}},
		UniqueCols: map[string]struct{}{"code": {}},
		ColTypes: map[string]byte{
			"id":     mysql.TypeLonglong,
			"name":   mysql.TypeVarchar,
			"code":   mysql.TypeVarchar,
			"status": mysql.TypeTiny,
		},
	}
}

func TestCollectWhereConditionsSyntax(t *testing.T) {
	cases := []struct {
		name           string
		where          string
		wantLeading    []string
		wantNoWildcard []string
		wantNotIn      bool
		wantNotLike    bool
		wantOr         bool
	}{
		{name: "前通配符", where: "name LIKE '%a'", wantLeading: []string{"%a"}},
		{name: "下划线前通配符", where: "name LIKE '_a'", wantLeading: []string{"_a"}},
		{name: "后缀通配符不报前通配", where: "name LIKE 'a%'"},
		{name: "无通配符LIKE", where: "name LIKE 'a'", wantNoWildcard: []string{"a"}},
		{name: "NOT IN", where: "id NOT IN (1,2)", wantNotIn: true},
		{name: "IN不算", where: "id IN (1,2)"},
		{name: "NOT LIKE", where: "name NOT LIKE 'a%'", wantNotLike: true},
		{name: "LIKE不算NOT LIKE", where: "name LIKE 'a%'"},
		{name: "OR连接", where: "name = 'a' OR code = 'c1'", wantOr: true},
		{name: "AND不算OR", where: "name = 'a' AND code = 'c1'"},
		{name: "常量OR不算", where: "1 = 1 OR 2 = 2"},
		// 「列条件 OR 恒真常量」是最危险的写法（等价于没有 WHERE），必须命中
		{name: "列OR恒真常量", where: "id = 1 OR 1 = 1", wantOr: true},
		{name: "恒真常量OR列", where: "1 = 1 OR id = 1", wantOr: true},
		// LIKE 通配符口径：只有 `%` 算有效通配符，`_` 在业务 SQL 里多为字面下划线
		{name: "含下划线视为无通配符", where: "name LIKE 'User_1'", wantNoWildcard: []string{"User_1"}},
		{name: "转义下划线视为字面量", where: "name LIKE 'User\\_1'", wantNoWildcard: []string{"User\\_1"}},
		{name: "含百分号不报无通配符", where: "name LIKE 'User%1'"},
		{name: "转义百分号视为字面量", where: "name LIKE 'User\\%1'", wantNoWildcard: []string{"User\\%1"}},
	}

	for _, c := range cases {
		info := CollectWhereConditions(parseWhereExpr(t, c.where), whereTestMeta())
		if !equalList(info.LeadingWildcards, c.wantLeading) {
			t.Errorf("%s: 前通配符 got=%v want=%v", c.name, info.LeadingWildcards, c.wantLeading)
		}
		if !equalList(info.NoWildcardLikes, c.wantNoWildcard) {
			t.Errorf("%s: 无通配符LIKE got=%v want=%v", c.name, info.NoWildcardLikes, c.wantNoWildcard)
		}
		if (len(info.NotInExprs) > 0) != c.wantNotIn {
			t.Errorf("%s: NOT IN got=%v want=%v", c.name, info.NotInExprs, c.wantNotIn)
		}
		if (len(info.NotLikeExprs) > 0) != c.wantNotLike {
			t.Errorf("%s: NOT LIKE got=%v want=%v", c.name, info.NotLikeExprs, c.wantNotLike)
		}
		if (len(info.OrConditions) > 0) != c.wantOr {
			t.Errorf("%s: OR got=%v want=%v", c.name, info.OrConditions, c.wantOr)
		}
	}
}

func TestCollectWhereConditionsTableMeta(t *testing.T) {
	cases := []struct {
		name     string
		where    string
		wantCalc bool // 索引列参与运算
		wantCast bool // 隐式类型转换
	}{
		{name: "索引列算术运算", where: "id + 1 = 2", wantCalc: true},
		{name: "索引列函数运算", where: "UPPER(name) = 'A'", wantCalc: true},
		// 函数包裹**不依赖索引元数据**：列是否在索引里、表结构读不读得到都要命中，
		// 否则 `DATE(created_at) = ?` 这类最常见写法会整体漏检
		{name: "非索引列函数运算", where: "DATE(created_at) = '2023-10-01'", wantCalc: true},
		{name: "索引列直接比较", where: "id = 1"},
		{name: "非索引列算术运算不报", where: "status + 1 = 2"},
		{name: "函数只包裹常量", where: "id = ABS(1)"},
		{name: "字符列与数字", where: "name = 1", wantCast: true},
		{name: "数字列与字符串", where: "id = '1'", wantCast: true},
		{name: "字符列与字符串", where: "name = 'a'"},
		{name: "数字列与数字", where: "id = 1"},
		{name: "列与NULL", where: "name IS NULL"},
	}

	for _, c := range cases {
		info := CollectWhereConditions(parseWhereExpr(t, c.where), whereTestMeta())
		if (len(info.IndexColCalcs) > 0) != c.wantCalc {
			t.Errorf("%s: 索引列运算 got=%v want=%v", c.name, info.IndexColCalcs, c.wantCalc)
		}
		if (len(info.ImplicitCasts) > 0) != c.wantCast {
			t.Errorf("%s: 隐式转换 got=%v want=%v", c.name, info.ImplicitCasts, c.wantCast)
		}
	}
}

// TestCollectWhereConditionsNoMeta 表结构不可读时：语法层判定仍生效，依赖表结构的判定落空（不误报）
func TestCollectWhereConditionsNoMeta(t *testing.T) {
	info := CollectWhereConditions(parseWhereExpr(t, "name LIKE '%a' AND id + 1 = 2 AND name = 1"), TableColumnMeta{})
	if len(info.LeadingWildcards) == 0 {
		t.Error("无表结构时前通配符仍应识别")
	}
	if len(info.IndexColCalcs) != 0 || len(info.ImplicitCasts) != 0 {
		t.Errorf("无表结构时不应判定索引列运算/隐式转换: %v / %v", info.IndexColCalcs, info.ImplicitCasts)
	}
}

// TestCollectWhereConditionsNotDescendSubquery 不深入子查询：子查询内部的 LIKE 不算本语句的条件
func TestCollectWhereConditionsNotDescendSubquery(t *testing.T) {
	info := CollectWhereConditions(parseWhereExpr(t, "id IN (SELECT id FROM t2 WHERE name LIKE '%x')"), whereTestMeta())
	if len(info.LeadingWildcards) != 0 {
		t.Errorf("子查询内部的条件不应计入: %v", info.LeadingWildcards)
	}
	if len(info.NotInExprs) != 0 {
		t.Errorf("IN 不应计为 NOT IN: %v", info.NotInExprs)
	}
}

func equalList(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if !strings.EqualFold(got[i], want[i]) {
			return false
		}
	}
	return true
}
