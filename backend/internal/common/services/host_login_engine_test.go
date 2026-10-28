package services

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/pkg/secret"
)

// 这组用例钉住「主机登录支持 Oracle / SQL Server」这条链路。
//
// 背景：放开一个新引擎要同时动三处，少任何一处功能都是死的，而且**死得静悄悄**：
//   1) host_login.go 的 hostLoginSupportedDBTypes —— 少了它，总览里入口是灰的；
//   2) instance.go 的 normalizeInstanceExtraConfig —— 少了它，用户在表单里填的
//      ssh_user / ssh_password 保存后被静默丢弃，入口亮着但永远报「未配置凭据」；
//   3) InstanceFormModal 的 showsSSHSection —— 少了它，表单里压根看不到 SSH 区块，
//      用户连填的地方都没有。
// 前两处在这里钉死；第 3 处是前端，只能靠代码注释互相指路。

func TestClassifyHostLoginBlockSupportsOracleAndSQLServer(t *testing.T) {
	cases := []struct {
		name      string
		dbType    string
		wantCode  string
		wantUser  string
		wantReady bool
	}{
		{name: "Oracle 凭据齐备", dbType: "Oracle", wantCode: HostLoginBlockNone, wantUser: "oracle", wantReady: true},
		{name: "SQLServer 凭据齐备", dbType: "SQLServer", wantCode: HostLoginBlockNone, wantUser: "ops", wantReady: true},
		// 大小写都要认：db_type 在库里是驼峰，但导入/同步路径可能出现别的写法
		{name: "小写 oracle", dbType: "oracle", wantCode: HostLoginBlockNone, wantUser: "oracle", wantReady: true},
		{name: "小写 sqlserver", dbType: "sqlserver", wantCode: HostLoginBlockNone, wantUser: "ops", wantReady: true},
		{name: "Oracle 未配凭据", dbType: "Oracle", wantCode: HostLoginBlockNoCredential, wantUser: "", wantReady: false},
		// OceanBase 仍不放开：平台不支持它备份，且 Oracle 模式归族另有口径
		{name: "OceanBase 仍不支持", dbType: "OceanBase", wantCode: HostLoginBlockEngineUnsupported, wantUser: "", wantReady: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{}
			if tc.wantUser != "" || tc.wantReady {
				cfg["ssh_user"] = "enc:v1:aesgcm:x"
				cfg["ssh_password"] = "enc:v1:aesgcm:y"
			}
			code, _, user, ready := ClassifyHostLoginBlock(tc.dbType, "self_built", cfg)
			if code != tc.wantCode {
				t.Fatalf("code = %q, want %q", code, tc.wantCode)
			}
			if ready != tc.wantReady {
				t.Fatalf("ready = %v, want %v", ready, tc.wantReady)
			}
			if ready && user == "" {
				t.Fatalf("ready 为 true 时必须回带 ssh_user")
			}
		})
	}
}

// 钉住 extra_config 落库：白名单放开而落库没放开的话，
// 表现是「保存成功、再打开表单凭据是空的、总览里还一直提示未配置」。
func TestNormalizeInstanceExtraConfigKeepsSSHForOracleAndSQLServer(t *testing.T) {
	cases := []struct {
		name   string
		dbType string
		raw    map[string]any
	}{
		{name: "Oracle", dbType: "Oracle", raw: map[string]any{"service_name": "ORCLPDB1"}},
		{name: "SQLServer", dbType: "SQLServer", raw: map[string]any{"database": "orders"}},
	}
	// SSH 密码入库前要走 pkg/secret 加密，单测进程里没有 config.yaml，先把密钥装进去
	t.Setenv(secret.EnvKey, "N2r#vT8qLw4!Zc1@Kb7^Yd3*Hs6&Gf0$")
	if err := secret.LoadKey(); err != nil {
		t.Fatalf("装载测试密钥失败: %v", err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{}
			for k, v := range tc.raw {
				raw[k] = v
			}
			raw["ssh_user"] = "ops"
			raw["ssh_port"] = "22"
			raw["ssh_password"] = "plain-password"

			got, err := normalizeInstanceExtraConfig(tc.dbType, raw, "create")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var cfg map[string]any
			if err := json.Unmarshal(got, &cfg); err != nil {
				t.Fatalf("extra_config 不是合法 JSON: %v (%s)", err, string(got))
			}
			if cfg["ssh_user"] != "ops" {
				t.Fatalf("ssh_user 未落库: %v", cfg["ssh_user"])
			}
			// 密码必须是密文：明文落库是这个项目的红线
			stored, _ := cfg["ssh_password"].(string)
			if stored == "" || stored == "plain-password" {
				t.Fatalf("ssh_password 未落库或未加密: %v", cfg["ssh_password"])
			}
			if !strings.HasPrefix(stored, "enc:") {
				t.Fatalf("ssh_password 不是密文格式: %v", stored)
			}
		})
	}
}

func TestHostLoginServerLooksWindows(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{version: "SSH-2.0-OpenSSH_for_Windows_8.1", want: true},
		{version: "SSH-2.0-OpenSSH_for_Win32", want: true},
		{version: "SSH-2.0-OpenSSH_8.9p1 Ubuntu-3ubuntu0.4", want: false},
		{version: "", want: false},
	}
	for _, tc := range cases {
		if got := hostLoginServerLooksWindows(tc.version); got != tc.want {
			t.Fatalf("hostLoginServerLooksWindows(%q) = %v, want %v", tc.version, got, tc.want)
		}
	}
}
