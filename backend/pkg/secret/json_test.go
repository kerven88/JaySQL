package secret

import (
	"testing"

	"github.com/simajiong/jaysql/internal/global"
)

func TestEncryptJSONKeepsOtherFieldsAndOrder(t *testing.T) {
	global.App.Config.App.SECRET_KEY = "0123456789abcdef0123456789abcdef"
	if err := LoadKey(); err != nil {
		t.Fatalf("LoadKey failed: %v", err)
	}
	raw := []byte(`{"ssh_user":"root","ssh_port":22,"ssh_password":"p@ss w0rd","ssh_private_key":"-----BEGIN KEY-----\nabc\n-----END KEY-----"}`)
	paths := []string{"$.ssh_password", "$.ssh_private_key"}

	encrypted, err := EncryptJSON(raw, paths)
	if err != nil {
		t.Fatalf("EncryptJSON failed: %v", err)
	}
	text := string(encrypted)
	// key 顺序与无关字段必须保持原样
	if !contains(text, `"ssh_user":"root"`, `"ssh_port":22`) {
		t.Fatalf("unrelated fields changed: %s", text)
	}
	if !contains(text, `"ssh_password":"`+PrefixV1, `"ssh_private_key":"`+PrefixV1) {
		t.Fatalf("sensitive fields not encrypted: %s", text)
	}
	// 数字不能被转成科学计数法或精度丢失
	if !contains(text, `"ssh_port":22`) {
		t.Fatalf("number precision lost: %s", text)
	}

	decrypted, err := DecryptJSON(encrypted, paths)
	if err != nil {
		t.Fatalf("DecryptJSON failed: %v", err)
	}
	want := `{"ssh_user":"root","ssh_port":22,"ssh_password":"p@ss w0rd","ssh_private_key":"-----BEGIN KEY-----\nabc\n-----END KEY-----"}`
	if string(decrypted) != want {
		t.Fatalf("round trip mismatch:\n got %s\nwant %s", decrypted, want)
	}
}

func TestEncryptJSONIdempotent(t *testing.T) {
	global.App.Config.App.SECRET_KEY = "0123456789abcdef0123456789abcdef"
	if err := LoadKey(); err != nil {
		t.Fatalf("LoadKey failed: %v", err)
	}
	raw := []byte(`{"ssh_password":"abc"}`)
	paths := []string{"$.ssh_password"}
	first, err := EncryptJSON(raw, paths)
	if err != nil {
		t.Fatalf("EncryptJSON failed: %v", err)
	}
	second, err := EncryptJSON(first, paths)
	if err != nil {
		t.Fatalf("EncryptJSON(again) failed: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("EncryptJSON must be idempotent:\n%s\n%s", first, second)
	}
}

func TestDecryptJSONPassThroughPlain(t *testing.T) {
	global.App.Config.App.SECRET_KEY = "0123456789abcdef0123456789abcdef"
	if err := LoadKey(); err != nil {
		t.Fatalf("LoadKey failed: %v", err)
	}
	// 存量明文：解密必须原样保留
	raw := []byte(`{"ssh_password":"plain-in-history"}`)
	out, err := DecryptJSON(raw, []string{"$.ssh_password"})
	if err != nil {
		t.Fatalf("DecryptJSON failed: %v", err)
	}
	if string(out) != string(raw) {
		t.Fatalf("plain value should pass through: %s", out)
	}
}

func TestJSONArrayWildcardPath(t *testing.T) {
	global.App.Config.App.SECRET_KEY = "0123456789abcdef0123456789abcdef"
	if err := LoadKey(); err != nil {
		t.Fatalf("LoadKey failed: %v", err)
	}
	raw := []byte(`{"channels":[{"name":"a","secret":"s1"},{"name":"b","secret":"s2"}],"webhook":"https://x"}`)
	paths := []string{"$.channels[*].secret"}
	encrypted, err := EncryptJSON(raw, paths)
	if err != nil {
		t.Fatalf("EncryptJSON failed: %v", err)
	}
	if !contains(string(encrypted), `"webhook":"https://x"`) {
		t.Fatalf("unrelated field changed: %s", encrypted)
	}
	decrypted, err := DecryptJSON(encrypted, paths)
	if err != nil {
		t.Fatalf("DecryptJSON failed: %v", err)
	}
	if string(decrypted) != string(raw) {
		t.Fatalf("array path round trip mismatch:\n got %s\nwant %s", decrypted, raw)
	}
}

func TestNeedsEncryption(t *testing.T) {
	global.App.Config.App.SECRET_KEY = "0123456789abcdef0123456789abcdef"
	if err := LoadKey(); err != nil {
		t.Fatalf("LoadKey failed: %v", err)
	}
	paths := []string{"$.ssh_password", "$.ssh_private_key"}

	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"明文", `{"ssh_user":"root","ssh_password":"abc"}`, true},
		{"空串值不需要加密", `{"ssh_user":"root","ssh_password":""}`, false},
		{"无敏感字段", `{"database":"db1","ssh_port":22}`, false},
		{"空输入", ``, false},
	}
	for _, c := range cases {
		got, err := NeedsEncryption([]byte(c.raw), paths)
		if err != nil {
			t.Fatalf("%s: NeedsEncryption error: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s: NeedsEncryption = %v want %v", c.name, got, c.want)
		}
	}

	// 已加密：应判为不需要再加密（幂等，避免迁移工具重复处理）
	encrypted, err := EncryptJSON([]byte(`{"ssh_password":"abc"}`), paths)
	if err != nil {
		t.Fatalf("EncryptJSON failed: %v", err)
	}
	got, err := NeedsEncryption(encrypted, paths)
	if err != nil || got {
		t.Fatalf("encrypted value should not need encryption again: %v / %v", got, err)
	}
}

func TestEncryptJSONEmptyInput(t *testing.T) {
	out, err := EncryptJSON(nil, []string{"$.ssh_password"})
	if err != nil || len(out) != 0 {
		t.Fatalf("empty input should return empty output, got (%q, %v)", out, err)
	}
}

func TestEncryptJSONInvalidInput(t *testing.T) {
	if _, err := EncryptJSON([]byte("{not-json"), []string{"$.a"}); err == nil {
		t.Fatalf("invalid JSON should return error")
	}
}

func TestParsePath(t *testing.T) {
	cases := map[string][]string{
		"$.ssh_password":       {"ssh_password"},
		"ssh_password":         {"ssh_password"},
		"$.channels[*].secret": {"channels", "*", "secret"},
		"$.a.b":                {"a", "b"},
		"$":                    nil,
	}
	for input, want := range cases {
		got, err := parsePath(input)
		if err != nil {
			t.Fatalf("parsePath(%q) error: %v", input, err)
		}
		if len(got) != len(want) {
			t.Fatalf("parsePath(%q) = %v want %v", input, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("parsePath(%q) = %v want %v", input, got, want)
			}
		}
	}
}

func contains(text string, parts ...string) bool {
	for _, p := range parts {
		found := false
		for i := 0; i+len(p) <= len(text); i++ {
			if text[i:i+len(p)] == p {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
