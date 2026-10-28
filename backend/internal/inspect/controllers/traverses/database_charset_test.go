package traverses

import (
	"testing"

	"github.com/pingcap/tidb/pkg/parser"
	_ "github.com/pingcap/tidb/pkg/types/parser_driver"
)

// TestTraverseDatabaseCharset 覆盖库字符集提取：utf8 / utf8mb4 / latin1 / 大写 / ALTER DATABASE。
// 这是「限制库的字符集」规则的输入端，抽取失败会直接导致规则静默放行。
func TestTraverseDatabaseCharset(t *testing.T) {
	cases := []struct {
		name    string
		sql     string
		dbName  string
		charset string
		collate string
	}{
		{"CREATE-CHARACTER SET utf8", "CREATE DATABASE testdb CHARACTER SET utf8", "testdb", "utf8", ""},
		{"CREATE-DEFAULT CHARACTER SET utf8", "CREATE DATABASE testdb DEFAULT CHARACTER SET utf8", "testdb", "utf8", ""},
		{"CREATE-DEFAULT CHARSET utf8mb4", "CREATE DATABASE testdb DEFAULT CHARSET utf8mb4", "testdb", "utf8mb4", ""},
		{"CREATE-CHARSET=latin1", "CREATE DATABASE testdb CHARACTER SET latin1 COLLATE latin1_swedish_ci", "testdb", "latin1", "latin1_swedish_ci"},
		{"CREATE-IF NOT EXISTS-等号写法", "CREATE DATABASE IF NOT EXISTS testdb CHARACTER SET = utf8", "testdb", "utf8", ""},
		// 注意：TiDB parser 会把字符集名统一折叠为小写，因此期望值也是小写
		{"CREATE-大写字符集", "CREATE DATABASE testdb CHARACTER SET UTF8MB4", "testdb", "utf8mb4", ""},
		{"ALTER-CHARACTER SET utf8", "ALTER DATABASE testdb CHARACTER SET utf8", "testdb", "utf8", ""},
		{"ALTER-DEFAULT CHARACTER SET latin1", "ALTER DATABASE testdb DEFAULT CHARACTER SET latin1", "testdb", "latin1", ""},
		{"ALTER-无库名(改默认库)", "ALTER DATABASE CHARACTER SET utf8", "", "utf8", ""},
		{"CREATE-未指定字符集", "CREATE DATABASE testdb", "testdb", "", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stmts, _, err := parser.New().Parse(c.sql, "", "")
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if len(stmts) != 1 {
				t.Fatalf("期望解析出 1 条语句，实际 %d 条", len(stmts))
			}
			v := &TraverseDatabaseCharset{}
			stmts[0].Accept(v)
			if v.Name != c.dbName {
				t.Errorf("库名不符: 期望 %q 实际 %q", c.dbName, v.Name)
			}
			if v.Charset != c.charset {
				t.Errorf("字符集不符: 期望 %q 实际 %q", c.charset, v.Charset)
			}
			if v.Collate != c.collate {
				t.Errorf("排序规则不符: 期望 %q 实际 %q", c.collate, v.Collate)
			}
		})
	}
}
