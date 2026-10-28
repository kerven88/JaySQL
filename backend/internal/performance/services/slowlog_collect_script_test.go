package services

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// assertBashSyntaxOK 用 `bash -n` 做真实语法校验。
//
// 为什么必须有这条：上一版本把 MySQL 兜底块做成「外层 if/then/fi 恒定输出、
// 只有块内语句可选」，PG 分支因此生成了空的 then 块——Go 侧字符串断言全过，
// 但远端 bash 在解析阶段就报 `syntax error near unexpected token 'fi'`，
// 导致慢 SQL 页直接显示「实例不可达」。语法正确性只有真正的解析器说了算。
func assertBashSyntaxOK(t *testing.T, name, script string) {
	t.Helper()
	bashBin, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	file, err := os.CreateTemp(t.TempDir(), "inspect-*.sh")
	if err != nil {
		t.Fatalf("create temp script: %v", err)
	}
	if _, err := file.WriteString(script); err != nil {
		t.Fatalf("write temp script: %v", err)
	}
	_ = file.Close()

	cmd := exec.Command(bashBin, "-n", file.Name())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s 脚本语法错误: %v\n%s\n--- script ---\n%s", name, err, output, script)
	}
}

// TestBuildInspectRemoteScriptSyntax 守住两个分支生成的脚本都能被 bash 解析。
func TestBuildInspectRemoteScriptSyntax(t *testing.T) {
	cases := []struct {
		name     string
		logPath  string
		inode    string
		offset   int64
		postgres bool
	}{
		{"postgres-with-path", "/var/lib/pgsql/16/data/log/postgresql-2026-10-23.log", "12345", 100, true},
		{"postgres-fresh", "/var/lib/pgsql/16/data/log/postgresql-2026-10-23.log", "", 0, true},
		{"mysql-with-path", "/var/log/mysql/slow.log", "777", 1024, false},
		{"mysql-no-inode", "/var/log/mysql/slow.log", "", 0, false},
		{"path-with-space", "/data/pg log/postgresql.log", "1", 0, true},
	}
	for _, tc := range cases {
		script := buildInspectRemoteScript(tc.logPath, tc.inode, tc.offset, tc.postgres)
		assertBashSyntaxOK(t, tc.name, script)
		if strings.Contains(script, "then\nfi") || strings.Contains(script, "then\n fi") {
			t.Fatalf("%s 生成空 then 块：\n%s", tc.name, script)
		}
	}
}

// TestBuildInspectRemoteScriptEngineSplit 守住两件事：
//  1. PG 分支不得出现 mysqld 探测（PG 主机上可能装着 MySQL，误读会串数据）；
//  2. 两个分支都必须在 inode 变化（日志轮转）时把偏移归零，
//     否则会拿旧文件的偏移去截新文件，静默丢数据。
func TestBuildInspectRemoteScriptEngineSplit(t *testing.T) {
	pgScript := buildInspectRemoteScript("/var/lib/pgsql/16/data/log/postgresql-2026-10-23.log", "12345", 100, true)
	if strings.Contains(pgScript, "--datadir") {
		t.Fatal("PG 分支不应包含 mysqld datadir 兜底探测")
	}
	if !strings.Contains(pgScript, `"$INODE" != "$PREV_INODE"`) {
		t.Fatal("缺少 inode 变化时重置偏移的逻辑")
	}
	if !strings.Contains(pgScript, `PREV_INODE="12345"`) {
		t.Fatalf("上一轮 inode 未写进脚本：%s", pgScript)
	}

	mysqlScript := buildInspectRemoteScript("/var/log/mysql/slow.log", "", 0, false)
	if !strings.Contains(mysqlScript, "--datadir") {
		t.Fatal("MySQL 分支应保留 datadir 兜底探测")
	}
	if !strings.Contains(mysqlScript, `"$INODE" != "$PREV_INODE"`) {
		t.Fatal("MySQL 分支同样需要 inode 变化时重置偏移")
	}
}
