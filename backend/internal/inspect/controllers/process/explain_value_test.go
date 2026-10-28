package process

import "testing"

// EXPLAIN 输出的数值字段归一：
//   - 多表关联的 EXPLAIN 会出现 rows=NULL（DAO 层把 NULL 转成字符串 "NULL"），
//     这类值必须判为「无值」并跳过，而不是让整条语句的影响行数判成 0；
//   - TiDB 的 estRows 可能是 N/A。
func TestExplainIntValue(t *testing.T) {
	cases := []struct {
		name   string
		input  interface{}
		want   int
		wantOK bool
	}{
		{"NULL 字符串判为无值", "NULL", 0, false},
		{"nil 判为无值", nil, 0, false},
		{"空串判为无值", "  ", 0, false},
		{"N/A 判为无值（TiDB estRows）", "N/A", 0, false},
		{"普通整数字符串", "1000", 1000, true},
		{"带空格整数字符串", " 5000 ", 5000, true},
		{"浮点字符串取整", "1234.9", 1234, true},
		{"int 原样", 42, 42, true},
		{"float64 取整", float64(88), 88, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := explainIntValue(c.input)
			if ok != c.wantOK || got != c.want {
				t.Errorf("explainIntValue(%#v) = (%d, %v)，期望 (%d, %v)", c.input, got, ok, c.want, c.wantOK)
			}
		})
	}
}
