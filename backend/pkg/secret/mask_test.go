package secret

import (
	"testing"

	"github.com/sirupsen/logrus"
)

func TestMask(t *testing.T) {
	if got := Mask(""); got != "" {
		t.Fatalf("Mask(\"\") = %q, want empty", got)
	}
	if got := Mask("123"); got != DefaultMask {
		t.Fatalf("Mask = %q want %q", got, DefaultMask)
	}
	// 等长星号会泄露长度，掩码必须是固定串
	if Mask("a") != Mask("averyveryverylongpassword") {
		t.Fatalf("mask must not leak length")
	}
}

func TestIsSensitiveKey(t *testing.T) {
	yes := []string{"password", "ssh_password", "MailPassword", "api-key", "access_key_secret", "SSHPrivateKey", "Authorization"}
	no := []string{"username", "host", "port", "created_at", "remark"}
	for _, key := range yes {
		if !IsSensitiveKey(key) {
			t.Fatalf("IsSensitiveKey(%q) should be true", key)
		}
	}
	for _, key := range no {
		if IsSensitiveKey(key) {
			t.Fatalf("IsSensitiveKey(%q) should be false", key)
		}
	}
}

func TestMaskStruct(t *testing.T) {
	type inner struct {
		SSHPassword string
		SSHUser     string
	}
	type payload struct {
		Inner    inner
		Name     string
		Password string
	}
	data := payload{Inner: inner{SSHPassword: "abc", SSHUser: "root"}, Name: "i-1", Password: "secret"}
	MaskStruct(&data)
	if data.Password != DefaultMask || data.Inner.SSHPassword != DefaultMask {
		t.Fatalf("password fields not masked: %+v", data)
	}
	if data.Name != "i-1" || data.Inner.SSHUser != "root" {
		t.Fatalf("non-sensitive fields must be untouched: %+v", data)
	}
}

func TestLogHook(t *testing.T) {
	entry := &logrus.Entry{
		Level: logrus.InfoLevel,
		Data: logrus.Fields{
			"password":      "plain-secret",
			"mail_password": "mail-secret",
			"instance":      "i-1",
			"port":          3306,
		},
	}
	if err := LogHook().Fire(entry); err != nil {
		t.Fatalf("Fire failed: %v", err)
	}
	if entry.Data["password"] != DefaultMask || entry.Data["mail_password"] != DefaultMask {
		t.Fatalf("log fields not masked: %+v", entry.Data)
	}
	if entry.Data["instance"] != "i-1" || entry.Data["port"] != 3306 {
		t.Fatalf("non-sensitive log fields must be untouched: %+v", entry.Data)
	}
}
