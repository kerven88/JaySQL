package services

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// PG 慢日志时间戳解析：时区语义回归
//
// 背景：早期 pgTimestampHeadRegexp 只截到毫秒、丢掉时区缩写，parsePGTimestamp
// 又用裸 time.Parse 走「无时区 → UTC」兜底。而 PG 的 %m 输出的是 log_timezone
// 的墙上时钟（如 Asia/Shanghai 渲染成 CST），当 UTC 解析后入库再转本地，
// event_time 整体 +8h、全部落在「未来」，页面 24h 统计窗口
// （event_time <= now）一条都圈不进来——采集器报成功、实例有慢SQL、
// 页面慢SQL数量恒为 0。
// ---------------------------------------------------------------------------

// parsePGTimestamp 的结果无论时区缩写被解析成本机时区还是兜底 0 偏移，
// 墙上时钟都必须与日志文本一致（任何时区解读路径都不该挪动表盘读数）。
func assertPGTimestampWallClock(t *testing.T, got time.Time, wantText string) {
	t.Helper()
	if got.IsZero() {
		t.Fatal("解析失败返回零值")
	}
	rendered := got.Format("2006-01-02 15:04:05.000")
	if rendered != wantText {
		t.Fatalf("墙上时钟被错误换算：got=%s want=%s（UTC instant=%s）", rendered, wantText, got.UTC().Format(time.RFC3339Nano))
	}
}

// 带 CST 缩写：墙上时钟必须原样保留，不得按 UTC 再转本地（+8h）。
func TestParsePGTimestampCSTKeepsWallClock(t *testing.T) {
	got := parsePGTimestamp("2026-10-24 18:58:21.022 CST")
	assertPGTimestampWallClock(t, got, "2026-10-24 18:58:21.022")
}

// 不带时区：必须按本机时区解释（ParseInLocation），而不是 UTC。
// 与 time.Local 逐字段比较，在任意时区的机器上都应通过。
func TestParsePGTimestampWithoutZoneUsesLocal(t *testing.T) {
	got := parsePGTimestamp("2026-10-24 18:58:21.022")
	want := time.Date(2026, 10, 24, 18, 58, 21, int(22*time.Millisecond), time.Local)
	if got.IsZero() {
		t.Fatal("解析失败返回零值")
	}
	if !got.Equal(want) {
		t.Fatalf("无时区时间戳未按本机时区解释：got=%s want=%s",
			got.Format("2006-01-02 15:04:05.000 -0700 MST"), want.Format("2006-01-02 15:04:05.000 -0700 MST"))
	}
}

// 端到端：parsePostgresSlowLogChunk 产出的 EventTime 必须等于日志行里的墙上时钟。
// 这是「慢SQL数量显示 0」事故的直接回归用例。
func TestParsePostgresSlowLogChunkEventTimeWallClock(t *testing.T) {
	line := "2026-10-24 18:58:21.022 CST [1319] postgres@postgres 192.168.1.3 " +
		"LOG:  duration: 1234.567 ms  statement: SELECT 1\n"
	events, _, err := parsePostgresSlowLogChunk(line, 0)
	if err != nil {
		t.Fatalf("解析返回错误：%v", err)
	}
	if len(events) != 1 {
		t.Fatalf("期望 1 条事件，实际 %d 条", len(events))
	}
	assertPGTimestampWallClock(t, events[0].EventTime, "2026-10-24 18:58:21.022")
}

// 无时区后缀的日志行同样不得被挪动（老版本会把 10:15 解释成 18:15）。
func TestParsePostgresSlowLogChunkEventTimeWithoutZone(t *testing.T) {
	line := "2026-10-24 10:15:23.456 [12345] app@app_db 10.0.0.7 " +
		"LOG:  duration: 50.000 ms  statement: SELECT 2\n"
	events, _, err := parsePostgresSlowLogChunk(line, 0)
	if err != nil {
		t.Fatalf("解析返回错误：%v", err)
	}
	if len(events) != 1 {
		t.Fatalf("期望 1 条事件，实际 %d 条", len(events))
	}
	want := time.Date(2026, 10, 24, 10, 15, 23, int(456*time.Millisecond), time.Local)
	if !events[0].EventTime.Equal(want) {
		t.Fatalf("无时区 EventTime 未按本机时区解释：got=%s want=%s",
			events[0].EventTime.Format("2006-01-02 15:04:05.000 -0700 MST"),
			want.Format("2006-01-02 15:04:05.000 -0700 MST"))
	}
}

// ---------------------------------------------------------------------------
// 协议阶段计数：解释「日志 duration 行数 >> 页面慢SQL条数」
//
// 扩展协议下同一条 SQL 会依次产生 parse / bind / execute 三行 duration。
// parse（含计划生成）和 bind 常被 log_min_duration_statement 记下来，
// execute 往往不到阈值反而没有——所以 grep duration 的行数天然远多于真实慢SQL。
// 采集消息里要把跳过的条数报出来，否则用户会认为平台漏采。
// ---------------------------------------------------------------------------

func TestPGProtocolPhaseRegexpCountsOnlyProtocolStages(t *testing.T) {
	chunk := strings.Join([]string{
		"2026-10-24 10:15:23.456 CST [1] app@db 10.0.0.7LOG:  duration: 4.682 ms  parse stmtcache_1: SELECT 1",
		"2026-10-24 10:15:23.460 CST [1] app@db 10.0.0.7LOG:  duration: 3.250 ms  bind stmtcache_1: SELECT 1",
		"2026-10-24 10:15:23.462 CST [1] app@db 10.0.0.7LOG:  duration: 0.900 ms  execute stmtcache_1: SELECT 1",
		"2026-10-24 10:15:24.000 CST [2] app@db 10.0.0.7LOG:  duration: 1234.567 ms  statement: SELECT 2",
	}, "\n") + "\n"

	if got := len(pgProtocolPhaseRegexp.FindAllStringIndex(chunk, -1)); got != 2 {
		t.Fatalf("协议阶段行数应为 2（parse + bind），实际 %d", got)
	}
}

