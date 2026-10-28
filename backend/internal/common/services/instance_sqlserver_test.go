package services

import (
	"encoding/json"
	"testing"
)

// 这组用例钉住 SQL Server 的纳管口径。
//
// 背景：SQLServer 在后端早就有完整通道（forms 的 oneof 白名单、testConnectionMode
// 的 sqlserver 分支、SyncDBMetaForInstance 的 sqlserver 分支、DAS 执行器），
// 但前端「新增实例」的类型下拉里没有它 —— 属于"后端能连、界面选不到"。
// 前端补选项容易，怕的是后端某一路被顺手删掉，所以这里把两条硬约束钉死：
//   1) extra_config 必须落 database，且缺省为 master（SQL Server 登录必须带初始库）
//   2) 连接测试必须认得 sqlserver 这个 db_type（否则测试连接直接报"暂不支持"）
//
// 第 2 条无法在不连库的前提下断言分支被走到，改为断言「不返回暂不支持」：
// 用一个必然连不通的地址，错误信息里必须是连接类错误而不是"暂不支持该数据库类型"。

func TestNormalizeInstanceExtraConfigSQLServer(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
		want string
	}{
		{name: "未填默认库时兜底 master", raw: map[string]any{}, want: "master"},
		{name: "填了业务库则原样保留", raw: map[string]any{"database": "orders"}, want: "orders"},
		{name: "空白字符串也按未填处理", raw: map[string]any{"database": "   "}, want: "master"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeInstanceExtraConfig("SQLServer", tc.raw, "create")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var cfg map[string]any
			if err := json.Unmarshal(got, &cfg); err != nil {
				t.Fatalf("extra_config 不是合法 JSON: %v (%s)", err, string(got))
			}
			if cfg["database"] != tc.want {
				t.Fatalf("database = %v, want %v", cfg["database"], tc.want)
			}
		})
	}
}

func TestTestConnectionModeRecognizesSQLServer(t *testing.T) {
	// 127.0.0.1:1 必然连不上：只关心错误语义，不关心能否连通。
	_, err := testConnectionMode(t.Context(), ProbeModeLightweight, "SQLServer",
		"127.0.0.1", 1, "sa", "whatever", []byte(`{"database":"master"}`))
	if err == nil {
		t.Fatalf("expected connection error, got nil")
	}
	if got := err.Error(); got == "暂不支持该数据库类型的连接测试" {
		t.Fatalf("SQLServer 未进入连接测试分派：%s", got)
	}
}
