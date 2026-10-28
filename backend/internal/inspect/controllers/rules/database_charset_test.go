package rules

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	_ "github.com/pingcap/tidb/pkg/types/parser_driver"
)

// runDatabaseCharsetRule 用给定的允许字符集列表跑一遍库字符集规则，返回 WARN 摘要
func runDatabaseCharsetRule(t *testing.T, sql string, allowed []string) []controllers.SummaryItem {
	t.Helper()
	stmts, _, err := parser.New().Parse(sql, "", "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	support := make([]map[string]string, 0, len(allowed))
	for _, a := range allowed {
		support = append(support, map[string]string{"charset": a})
	}
	r := &Rule{
		Hint: "DatabaseDDL#检查库字符集",
		RuleHint: &controllers.RuleHint{
			InspectParams: &config.InspectParams{
				CHECK_DATABASE_CHARSET:   true,
				DATABASE_SUPPORT_CHARSET: support,
			},
		},
		CheckFunc: (*Rule).RuleCheckDatabaseCharset,
	}
	var node ast.StmtNode = stmts[0]
	r.CheckFunc(r, &node)
	return r.RuleHint.Summary
}

// TestRuleCheckDatabaseCharset 覆盖 utf8 / utf8mb4 / latin1 / 大小写 / ALTER DATABASE / 未指定字符集。
func TestRuleCheckDatabaseCharset(t *testing.T) {
	cases := []struct {
		name       string
		sql        string
		allowed    []string
		wantWarn   bool
		wantSubstr string
	}{
		{"utf8不在允许列表(仅utf8mb4)", "CREATE DATABASE testdb CHARACTER SET utf8", []string{"utf8mb4"}, true, "utf8`不符合要求"},
		{"utf8mb4命中允许列表", "CREATE DATABASE testdb DEFAULT CHARSET utf8mb4", []string{"utf8mb4"}, false, ""},
		{"latin1不在允许列表", "CREATE DATABASE testdb CHARACTER SET latin1", []string{"utf8mb4"}, true, "latin1`不符合要求"},
		{"大写UTF8MB4应大小写不敏感放行", "CREATE DATABASE testdb CHARACTER SET UTF8MB4", []string{"utf8mb4"}, false, ""},
		{"大小写混合Utf8Mb4", "CREATE DATABASE testdb CHARACTER SET Utf8Mb4", []string{"utf8mb4"}, false, ""},
		{"ALTER DATABASE-utf8应拦截", "ALTER DATABASE testdb CHARACTER SET utf8", []string{"utf8mb4"}, true, "utf8`不符合要求"},
		{"ALTER DATABASE-latin1应拦截", "ALTER DATABASE testdb DEFAULT CHARACTER SET latin1", []string{"utf8mb4", "utf8"}, true, "latin1`不符合要求"},
		{"ALTER DATABASE-utf8在允许列表放行", "ALTER DATABASE testdb CHARACTER SET utf8", []string{"utf8mb4", "utf8"}, false, ""},
		{"未指定字符集应提示必须指定", "CREATE DATABASE testdb", []string{"utf8mb4"}, true, "必须指定字符集"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runDatabaseCharsetRule(t, c.sql, c.allowed)
			if c.wantWarn {
				if len(got) == 0 {
					t.Fatalf("期望产生告警，实际无摘要")
				}
				found := false
				for _, s := range got {
					if s.Level != "WARN" {
						continue
					}
					if strings.Contains(s.Message, c.wantSubstr) {
						found = true
					}
					// 摘要必须携带 rule_key，否则规范里配置的「必须改进」级别无法精确命中
					if s.RuleKey != "limit_database_charset" {
						t.Errorf("摘要缺少 rule_key: 期望 limit_库的charset 实际 %q", s.RuleKey)
					}
				}
				if !found {
					t.Errorf("告警文案不包含 %q: %+v", c.wantSubstr, got)
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("期望无告警，实际: %+v", got)
			}
		})
	}
}

// TestRuleCheckDatabaseCharsetDisabled 规则关闭时不产生任何摘要
func TestRuleCheckDatabaseCharsetDisabled(t *testing.T) {
	stmts, _, err := parser.New().Parse("CREATE DATABASE testdb CHARACTER SET latin1", "", "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	r := &Rule{
		RuleHint: &controllers.RuleHint{
			InspectParams: &config.InspectParams{
				CHECK_DATABASE_CHARSET:   false,
				DATABASE_SUPPORT_CHARSET: []map[string]string{{"charset": "utf8mb4"}},
			},
		},
		CheckFunc: (*Rule).RuleCheckDatabaseCharset,
	}
	var node ast.StmtNode = stmts[0]
	r.CheckFunc(r, &node)
	if len(r.RuleHint.Summary) != 0 {
		t.Fatalf("规则关闭时不应产生摘要，实际: %+v", r.RuleHint.Summary)
	}
}
