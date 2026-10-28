package services

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// PG 慢日志解析：log_line_prefix 布局适配
//
// 背景：log_line_prefix 由运维自由配置，平台只能保证「含 %u 和 %d」这一个前提。
// 旧版正则把 `[pid] user@db LOG:` 写死成严格相邻的一整条，于是任何字段顺序
// 不同、分隔符不同、或中间插了额外字段的常见配置都会整行失配 —— 采集器照样
// 报"采集成功：新增 0 条"，页面就是 0 条慢 SQL，没有任何报错。
// 这里把各类布局都钉成用例，防止再退化回去。
// ---------------------------------------------------------------------------

func TestParsePostgresSlowLogChunkPrefixLayouts(t *testing.T) {
	sql := "SELECT * FROM t WHERE id = 1"
	cases := []struct {
		name     string
		prefix   string // log_line_prefix 配置
		rendered string // 渲染出的行首前缀
		wantUser string
		wantDB   string
		wantPID  string
		wantIP   string
	}{
		{
			name:     "标准 %m [%p] %u@%d %h",
			prefix:   "%m [%p] %u@%d %h ",
			rendered: "2026-10-24 10:15:23.456 CST [12345] app@app_db 10.0.0.7",
			wantUser: "app", wantDB: "app_db", wantPID: "12345", wantIP: "10.0.0.7",
		},
		{
			name:     "无 %p（没有方括号）",
			prefix:   "%m %u@%d %h ",
			rendered: "2026-10-24 10:15:23.456 CST app@app_db 10.0.0.7",
			wantUser: "app", wantDB: "app_db", wantPID: "", wantIP: "10.0.0.7",
		},
		{
			name:     "[pid] 与 user@db 之间插了 session id",
			prefix:   "%m [%p] %c %u@%d ",
			rendered: "2026-10-24 10:15:23.456 CST [12345] 68f2a1b4.3039 app@app_db",
			wantUser: "app", wantDB: "app_db", wantPID: "12345", wantIP: "",
		},
		{
			name:     "KV 风格 user= db=",
			prefix:   "%t [%p]: [%l-1] user=%u,db=%d,app=%a,client=%h ",
			rendered: "2026-10-24 10:15:23 CST [12345]: [12-1] user=app,db=app_db,app=psql,client=10.0.0.7",
			wantUser: "app", wantDB: "app_db", wantPID: "12345", wantIP: "10.0.0.7",
		},
		{
			name:     "带 %q 限定的用户名",
			prefix:   "%m [%p] %q%u@%d ",
			rendered: "2026-10-24 10:15:23.456 CST [12345] app@app_db",
			wantUser: "app", wantDB: "app_db", wantPID: "12345", wantIP: "",
		},
		{
			name:     "无时区后缀",
			prefix:   "%m [%p] %u@%d ",
			rendered: "2026-10-24 10:15:23.456 [12345] app@app_db",
			wantUser: "app", wantDB: "app_db", wantPID: "12345", wantIP: "",
		},
		{
			name:     "只有时间戳和 pid（探测会判 UNSUPPORTED，但解析不该崩）",
			prefix:   "%m [%p] ",
			rendered: "2026-10-24 10:15:23.456 CST [12345]",
			wantUser: "", wantDB: "", wantPID: "12345", wantIP: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line := c.rendered + " LOG:  duration: 1234.567 ms  statement: " + sql + "\n"
			events, rest, err := parsePostgresSlowLogChunk(line, 100)
			if err != nil {
				t.Fatalf("解析返回错误：%v", err)
			}
			if len(events) != 1 {
				t.Fatalf("期望解析出 1 条慢 SQL，实际 %d 条", len(events))
			}
			e := events[0]
			if e.QueryTimeMS != 1234 {
				t.Errorf("耗时期望 1234ms，实际 %d", e.QueryTimeMS)
			}
			if !strings.Contains(e.SQLText, sql) {
				t.Errorf("SQL 文本提取错误：%q", e.SQLText)
			}
			if e.Username != c.wantUser {
				t.Errorf("用户名期望 %q，实际 %q", c.wantUser, e.Username)
			}
			if e.SchemaName != c.wantDB {
				t.Errorf("库名期望 %q，实际 %q", c.wantDB, e.SchemaName)
			}
			if e.ThreadID != c.wantPID {
				t.Errorf("进程号期望 %q，实际 %q", c.wantPID, e.ThreadID)
			}
			if e.ClientHost != c.wantIP {
				t.Errorf("客户端地址期望 %q，实际 %q", c.wantIP, e.ClientHost)
			}
			if rest != "" {
				t.Errorf("以换行结尾时不应有残留，实际 %q", rest)
			}
		})
	}
}

func TestParsePostgresSlowLogChunkNonDurationLinesSkipped(t *testing.T) {
	// 非 duration 行（普通 LOG / 连接日志 / 错误）必须被跳过，且不产出事件
	chunk := strings.Join([]string{
		"2026-10-24 10:15:23.456 CST [12345] LOG:  database system is ready to accept connections",
		"2026-10-24 10:15:24.456 CST [12346] app@app_db 10.0.0.7 LOG:  connection authorized: user=app database=app_db",
		"2026-10-24 10:15:25.456 CST [12347] app@app_db 10.0.0.7 ERROR:  relation \"nope\" does not exist",
	}, "\n") + "\n"
	events, _, err := parsePostgresSlowLogChunk(chunk, 0)
	if err != nil {
		t.Fatalf("解析返回错误：%v", err)
	}
	if len(events) != 0 {
		t.Errorf("非 duration 行不应产出事件，实际 %d 条", len(events))
	}
}

func TestParsePostgresSlowLogChunkMultiLineSQL(t *testing.T) {
	// 多行 SQL：续行不以日期开头，必须拼到同一条记录上
	chunk := strings.Join([]string{
		"2026-10-24 10:15:23.456 CST [12345] app@app_db 10.0.0.7 LOG:  duration: 1234.567 ms  statement: SELECT *",
		"  FROM t",
		"  WHERE id = 1",
		"2026-10-24 10:15:26.456 CST [12346] app@app_db 10.0.0.7 LOG:  duration: 12.000 ms  statement: SELECT 1",
	}, "\n") + "\n"
	events, _, err := parsePostgresSlowLogChunk(chunk, 0)
	if err != nil {
		t.Fatalf("解析返回错误：%v", err)
	}
	if len(events) != 2 {
		t.Fatalf("期望 2 条，实际 %d 条", len(events))
	}
	if !strings.Contains(events[0].SQLText, "FROM t") || !strings.Contains(events[0].SQLText, "WHERE id = 1") {
		t.Errorf("多行 SQL 未被正确拼接：%q", events[0].SQLText)
	}
	if events[0].QueryTimeMS != 1234 || events[1].QueryTimeMS != 12 {
		t.Errorf("耗时解析错误：%d / %d", events[0].QueryTimeMS, events[1].QueryTimeMS)
	}
}

func TestParsePostgresSlowLogChunkTrailingRest(t *testing.T) {
	// 末尾不以换行结尾 → 最后一行作为 rest 返回，等下一轮拼接
	chunk := "2026-10-24 10:15:23.456 CST [1] a@b 1.1.1.1 LOG:  duration: 5.000 ms  statement: SELECT"
	events, rest, err := parsePostgresSlowLogChunk(chunk, 0)
	if err != nil {
		t.Fatalf("解析返回错误：%v", err)
	}
	if len(events) != 1 {
		t.Fatalf("期望 1 条，实际 %d 条", len(events))
	}
	if !strings.Contains(rest, "SELECT") {
		t.Errorf("末尾未闭合的行应作为 rest 返回，实际 %q", rest)
	}
}
