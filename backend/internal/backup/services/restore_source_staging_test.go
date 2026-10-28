package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsManagedRestoreTempDir 校验「只允许删除本次恢复创建的临时目录」这条安全底线。
func TestIsManagedRestoreTempDir(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// 合法：本次 mktemp -d 生成的目录
		{"/tmp/goinsight-restore-AbC123", true},
		{"/var/tmp/goinsight-restore-AbC123", true},
		{filepath.Join(os.TempDir(), "goinsight-restore-AbC123"), true},
		// 非法：不是本次目录 / 危险路径 / 相对路径 / 业务数据目录
		{"/tmp", false},
		{"/", false},
		{"", false},
		{"goinsight-restore-AbC123", false},
		{"/tmp/other-dir", false},
		{"/opt/app/goinsight/media/backup/restore/PostgreSQL/34/15/restore-src", false},
		{"/data/mysql", false},
		{"/tmp/goinsight-restore-AbC123/../..", false},
	}
	for _, c := range cases {
		if got := isManagedRestoreTempDir(c.path); got != c.want {
			t.Errorf("isManagedRestoreTempDir(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestMakeTempDirShell 保证临时目录创建脚本产出的路径能被安全校验规则接受。
func TestMakeTempDirShell(t *testing.T) {
	out, err := runLocalCommandCapture(context.Background(), "", "/bin/sh", []string{"-lc", makeTempDirShell()}, nil, nil)
	if err != nil {
		t.Skipf("本机不支持 mktemp，跳过: %v", err)
	}
	dir := strings.TrimSpace(out)
	if dir == "" {
		t.Fatalf("mktemp 未返回目录路径")
	}
	if !isManagedRestoreTempDir(dir) {
		t.Fatalf("mktemp 产出的目录未通过安全校验: %q", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("清理测试目录失败: %v", err)
	}
}

// TestBuildRemoteScriptEnvExport 环境变量必须以 export 形式注入远程脚本，且值经过 shell 转义。
func TestBuildRemoteScriptEnvExport(t *testing.T) {
	script := buildRemoteScript(map[string]string{"PGPASSWORD": "p@ss'word"}, "echo hi\n")
	if !strings.Contains(script, "export PGPASSWORD=") {
		t.Fatalf("远程脚本缺少环境变量导出: %s", script)
	}
	if strings.Contains(script, "p@ss'word") {
		t.Fatalf("环境变量值未做 shell 转义: %s", script)
	}
	if !strings.HasSuffix(script, "echo hi\n") {
		t.Fatalf("远程脚本内容被破坏: %s", script)
	}
}
