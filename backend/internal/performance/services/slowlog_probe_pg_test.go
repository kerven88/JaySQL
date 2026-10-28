package services

import "testing"

// TestJoinPGLogPath 固定 pg_current_logfile() 的返回值形态与拼接规则。
//
// 历史事故：返回值在默认配置下是 `log/postgresql-2026-10-23.log`（相对 data_directory），
// 却被当成绝对路径写进 log_path，SSH 端 `test -f` 必然失败 →
// 采集报 PG_LOG_FILE_UNREADABLE → 前端显示「慢日志文件缺失或不可读」，
// 但服务端其实天天在写这个文件。
func TestJoinPGLogPath(t *testing.T) {
	cases := []struct {
		name     string
		dataDir  string
		logDir   string
		current  string
		expected string
	}{
		{
			name:     "PG 默认：返回值已含 log_directory，只补 data_directory",
			dataDir:  "/var/lib/pgsql/16/data",
			logDir:   "log",
			current:  "log/postgresql-2026-10-23_000000.log",
			expected: "/var/lib/pgsql/16/data/log/postgresql-2026-10-23_000000.log",
		},
		{
			name:     "只返回文件名时补 log_directory",
			dataDir:  "/var/lib/pgsql/16/data",
			logDir:   "log",
			current:  "postgresql-2026-10-23.log",
			expected: "/var/lib/pgsql/16/data/log/postgresql-2026-10-23.log",
		},
		{
			name:     "不能拼出 log/log 双层目录",
			dataDir:  "/data/pgdata",
			logDir:   "log",
			current:  "log/postgresql.log",
			expected: "/data/pgdata/log/postgresql.log",
		},
		{
			name:     "log_directory 为绝对路径时返回值也是绝对路径",
			dataDir:  "/var/lib/pgsql/16/data",
			logDir:   "/data/pglog",
			current:  "/data/pglog/postgresql-2026-10-23_000000.log",
			expected: "/data/pglog/postgresql-2026-10-23_000000.log",
		},
		{
			name:     "只有日志目录、没有文件名时返回空（不拿目录当文件）",
			dataDir:  "/var/lib/pgsql/16/data",
			logDir:   "log",
			current:  "",
			expected: "",
		},
		{
			name:     "data_directory 缺失时保留原相对路径（无法展开，但不拼错）",
			dataDir:  "",
			logDir:   "log",
			current:  "log/postgresql.log",
			expected: "log/postgresql.log",
		},
		{
			name:     "什么都拿不到",
			dataDir:  "",
			logDir:   "",
			current:  "",
			expected: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := joinPGLogPath(c.dataDir, c.logDir, c.current); got != c.expected {
				t.Fatalf("joinPGLogPath(%q, %q, %q) = %q, want %q",
					c.dataDir, c.logDir, c.current, got, c.expected)
			}
		})
	}
}

// TestResolvePGSlowLogPathPrefersCurrentFile 确认探测结果走的是拼接后的绝对路径。
func TestResolvePGSlowLogPathPrefersCurrentFile(t *testing.T) {
	r := &postgresProbeResult{
		CurrentLogFile: "log/postgresql-2026-10-23.log",
		LogDir:         "log",
		DataDir:        "/var/lib/pgsql/16/data",
	}
	want := "/var/lib/pgsql/16/data/log/postgresql-2026-10-23.log"
	if got := resolvePGSlowLogPath(r); got != want {
		t.Fatalf("resolvePGSlowLogPath = %q, want %q", got, want)
	}
	if got := resolvePGSlowLogPath(nil); got != "" {
		t.Fatalf("resolvePGSlowLogPath(nil) = %q, want empty", got)
	}
}
