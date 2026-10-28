package middleware

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/spf13/viper"
)

// TestResolveLogDir 锁定「日志目录不随进程 CWD 漂移」这一不变量。
func TestResolveLogDir(t *testing.T) {
	// 绝对路径原样返回
	if got := resolveLogDir("/opt/app/goinsight/logs"); got != "/opt/app/goinsight/logs" {
		t.Fatalf("absolute dir: got %q", got)
	}

	// 相对路径应以**配置文件所在目录**为基准
	confDir := t.TempDir()
	cfg := filepath.Join(confDir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("log:\n  root_dir: \"./logs\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := viper.New()
	v.SetConfigFile(cfg)

	oldViper := global.App.ConfigViper
	global.App.ConfigViper = v
	defer func() { global.App.ConfigViper = oldViper }()

	want := filepath.Join(confDir, "logs")
	if got := resolveLogDir("./logs"); got != want {
		t.Fatalf("relative dir: got %q want %q", got, want)
	}
	if got := resolveLogDir(""); got != want {
		t.Fatalf("empty dir: got %q want %q", got, want)
	}

	// 关键断言：换一个工作目录，结果必须不变
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(os.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := resolveLogDir("./logs"); got != want {
		t.Fatalf("cwd-dependent drift: got %q want %q", got, want)
	}

	// 没有 viper 时退回 CWD，且必须是绝对路径
	global.App.ConfigViper = nil
	if got := resolveLogDir("logs"); !filepath.IsAbs(got) {
		t.Fatalf("fallback should be absolute, got %q", got)
	}
}

func TestSanitizeRequestHeaders(t *testing.T) {
	headers := http.Header{
		"Authorization": []string{"JWT abc"},
		"Cookie":        []string{"jwt=abc"},
		"X-Trace-Id":    []string{"trace-1"},
	}

	got := sanitizeRequestHeaders(headers)
	if got["Authorization"][0] != "***" {
		t.Fatalf("authorization should be masked, got %q", got["Authorization"][0])
	}
	if got["Cookie"][0] != "***" {
		t.Fatalf("cookie should be masked, got %q", got["Cookie"][0])
	}
	if got["X-Trace-Id"][0] != "trace-1" {
		t.Fatalf("non-sensitive header should keep value, got %q", got["X-Trace-Id"][0])
	}
}

func TestSanitizeRequestURI(t *testing.T) {
	u, err := url.Parse("/api/v1/user/profile?username=alice&password=secret&token=t1&old_password=abc&new_password=xyz")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	got := sanitizeRequestURI(u)
	if got == "" {
		t.Fatal("sanitized URI should not be empty")
	}
	if got == "/api/v1/user/profile?username=alice&password=secret&token=t1&old_password=abc&new_password=xyz" {
		t.Fatal("sensitive query fields should be masked")
	}
	if want := "username=alice"; !strings.Contains(got, want) {
		t.Fatalf("expected non-sensitive query to remain, got %q", got)
	}
	if want := "password=%2A%2A%2A"; !strings.Contains(got, want) {
		t.Fatalf("expected password to be masked, got %q", got)
	}
	if want := "token=%2A%2A%2A"; !strings.Contains(got, want) {
		t.Fatalf("expected token to be masked, got %q", got)
	}
	if want := "old_password=%2A%2A%2A"; !strings.Contains(got, want) {
		t.Fatalf("expected old_password to be masked, got %q", got)
	}
	if want := "new_password=%2A%2A%2A"; !strings.Contains(got, want) {
		t.Fatalf("expected new_password to be masked, got %q", got)
	}
}
