// Package sqlserverdsn 统一构建 go-mssqldb 的 SQL Server 连接串。
//
// 为什么统一带上 tlsmin=1.0：SQL Server 2012 等老版本默认只协商 TLS 1.0
// （TLS 协议版本号 301），而 Go 客户端自 1.18 起默认最低 TLS 1.2，握手直接失败，
// 报「连接失败：TLS Handshake failed: tls: server selected unsupported protocol version 301」。
// tlsmin 只放宽客户端可接受的最低版本——新版本服务器仍会协商到 TLS 1.2+，行为不变；
// 驱动在未显式传 encrypt 参数时默认不强制加密、信任服务器证书，与历史行为一致。
package sqlserverdsn

import (
	"fmt"
	"net/url"
)

// DSN 构建 SQL Server 连接串；database 为空时省略该参数（驱动默认 master）。
func DSN(user, password, hostname string, port int, database string) string {
	query := url.Values{}
	if database != "" {
		query.Set("database", database)
	}
	query.Set("tlsmin", "1.0")
	return (&url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(user, password),
		Host:     fmt.Sprintf("%s:%d", hostname, port),
		RawQuery: query.Encode(),
	}).String()
}
