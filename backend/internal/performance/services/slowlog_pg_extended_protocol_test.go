package services

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// PG 慢日志解析：真实世界里的两个致命细节
//
// ① log_line_prefix 末尾不写空格时，`LOG:` 会紧贴前一个字段：
//
//	2026-10-24 13:31:04.846 CST [1319] postgres@postgres,app=[unknown],client=192.168.1.3LOG:  duration: 3.479 ms  statement: -- ping
//
//  用 `\s+LOG:` 会把这台实例**每一行**都判为失配。
//
// ② 走扩展协议（pgx / JDBC / Npgsql 的 prepared statement）时，PG 输出的是
// `execute <name>:` 而非 `statement:`，且语句正文常换行到下一行：
//
//	2026-10-24 13:31:07.027 CST [1322] ...client=192.168.1.19LOG:  duration: 3.099 ms  execute stmtcache_378:
//			SELECT ...
//
//  只认 `statement:`、或要求首行 SQL 非空，都会漏掉这类慢 SQL。
//  实测某实例 27 条 duration 行里 26 条是 `execute`、其中 22 条正文在续行。
// ---------------------------------------------------------------------------

// TestPostgresLogWithoutTrailingSpace LOG: 与前缀紧贴也能解析。
func TestPostgresLogWithoutTrailingSpace(t *testing.T) {
	content := "2026-10-24 13:31:04.846 CST [1319] postgres@postgres,app=[unknown],client=192.168.1.3LOG:  duration: 3.479 ms  statement: -- ping\n"

	events, _, err := parsePostgresSlowLogChunk(content, 0)
	if err != nil {
		t.Fatalf("解析报错: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("期望 1 条事件，实际 %d 条", len(events))
	}
	e := events[0]
	if e.QueryTimeMS != 3 {
		t.Errorf("耗时错误: %d", e.QueryTimeMS)
	}
	if !strings.Contains(e.SQLText, "-- ping") {
		t.Errorf("SQL 错误: %q", e.SQLText)
	}
	if e.Username != "postgres" {
		t.Errorf("用户名错误: %q", e.Username)
	}
	// 库名必须在 `,app=` 处截断，不能吃成 `postgres,app=`
	if e.SchemaName != "postgres" {
		t.Errorf("库名错误: %q（应截断到逗号）", e.SchemaName)
	}
	if e.ThreadID != "1319" {
		t.Errorf("pid 错误: %q", e.ThreadID)
	}
	if e.ClientHost != "192.168.1.3" {
		t.Errorf("客户端地址错误: %q", e.ClientHost)
	}
}

// TestPostgresExtendedProtocolExecute 扩展协议的 execute 行，正文在续行。
func TestPostgresExtendedProtocolExecute(t *testing.T) {
	content := strings.Join([]string{
		"2026-10-24 13:31:07.027 CST [1322] postgres@postgres,app=[unknown],client=192.168.1.19LOG:  duration: 230.099 ms  execute stmtcache_378: ",
		"\t\t\tSELECT",
		"\t\t\t\tEXTRACT(EPOCH FROM now() - pg_postmaster_start_time()) AS uptime_seconds,",
		"\t\t\t\tCOALESCE(SUM(xact_commit), 0) AS commit_total",
		"\t\t\tFROM pg_stat_database",
	}, "\n") + "\n"

	events, _, err := parsePostgresSlowLogChunk(content, 0)
	if err != nil {
		t.Fatalf("解析报错: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("期望 1 条事件，实际 %d 条（首行 SQL 为空时不能直接丢弃）", len(events))
	}
	e := events[0]
	if e.QueryTimeMS != 230 {
		t.Errorf("耗时错误: %d", e.QueryTimeMS)
	}
	for _, frag := range []string{"EXTRACT(EPOCH", "commit_total", "FROM pg_stat_database"} {
		if !strings.Contains(e.SQLText, frag) {
			t.Errorf("多行 SQL 拼接不完整，缺 %q：%q", frag, e.SQLText)
		}
	}
}

// TestPostgresExtendedProtocolIgnoresParseAndBind parse/bind 行是准备阶段，
// 不是执行耗时，必须忽略；execute 行照收。两者交错出现时不能互相吞并。
func TestPostgresExtendedProtocolIgnoresParseAndBind(t *testing.T) {
	content := strings.Join([]string{
		"2026-10-24 13:31:16.890 CST [1334] postgres@postgres,client=10.0.0.9LOG:  duration: 1.054 ms  parse stmtcache_5: SELECT 1",
		"2026-10-24 13:31:16.900 CST [1334] postgres@postgres,client=10.0.0.9LOG:  duration: 0.211 ms  bind stmtcache_5: SELECT 1",
		"2026-10-24 13:31:16.944 CST [1334] postgres@postgres,client=10.0.0.9LOG:  duration: 43.151 ms  execute stmtcache_5: SELECT 1",
	}, "\n") + "\n"

	events, _, err := parsePostgresSlowLogChunk(content, 0)
	if err != nil {
		t.Fatalf("解析报错: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("期望只收 execute 一条，实际 %d 条", len(events))
	}
	if events[0].QueryTimeMS != 43 {
		t.Errorf("耗时错误: %d（应取 execute 的 43ms，而不是 parse/bind）", events[0].QueryTimeMS)
	}
	if !strings.Contains(events[0].SQLText, "SELECT 1") {
		t.Errorf("SQL 错误: %q", events[0].SQLText)
	}
}

// TestPostgresStatementAndExecuteMixed statement 与 execute 混排都要收。
func TestPostgresStatementAndExecuteMixed(t *testing.T) {
	content := strings.Join([]string{
		"2026-10-24 13:31:04.000 CST [100] app@db,app=psql,client=1.2.3.4LOG:  duration: 1200.000 ms  statement: SELECT * FROM a",
		"2026-10-24 13:31:05.000 CST [101] app@db,app=psql,client=1.2.3.4LOG:  duration: 800.500 ms  execute stmt_1: SELECT * FROM b",
	}, "\n") + "\n"

	events, _, err := parsePostgresSlowLogChunk(content, 0)
	if err != nil {
		t.Fatalf("解析报错: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("期望 2 条事件，实际 %d 条", len(events))
	}
	if events[0].QueryTimeMS != 1200 || events[1].QueryTimeMS != 800 {
		t.Errorf("耗时错误: %d / %d", events[0].QueryTimeMS, events[1].QueryTimeMS)
	}
	for _, e := range events {
		if e.Username != "app" || e.SchemaName != "db" {
			t.Errorf("前缀提取错误: user=%q db=%q", e.Username, e.SchemaName)
		}
	}
}
