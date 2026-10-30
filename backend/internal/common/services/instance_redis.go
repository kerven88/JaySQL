package services

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"
)

// probeRedisConnection is deliberately limited to RESP2 AUTH/PING. Instance
// registration and health checks never execute user commands or scan Redis data.
func probeRedisConnection(ctx context.Context, hostname string, port int, user, password string, cfg map[string]any) error {
	address := net.JoinHostPort(hostname, fmt.Sprintf("%d", port))
	dialer := &net.Dialer{Timeout: 4 * time.Second}
	var conn net.Conn
	var err error
	useTLS := false
	if value, exists := cfg["redis_tls"]; exists && value != nil {
		parsed, ok := value.(bool)
		if !ok {
			return fmt.Errorf("Redis TLS 参数必须为布尔值")
		}
		useTLS = parsed
	}
	if useTLS {
		secure := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: hostname}}
		conn, err = secure.DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("Redis 连接失败：%w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(4 * time.Second)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("Redis 设置连接超时失败")
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	reader := bufio.NewReaderSize(conn, 4096)
	command := func(args ...string) (string, error) {
		var request strings.Builder
		fmt.Fprintf(&request, "*%d\r\n", len(args))
		for _, arg := range args {
			fmt.Fprintf(&request, "$%d\r\n%s\r\n", len([]byte(arg)), arg)
		}
		if _, err := fmt.Fprint(conn, request.String()); err != nil {
			return "", fmt.Errorf("Redis 请求发送失败")
		}
		line, err := reader.ReadSlice('\n')
		if err != nil {
			return "", fmt.Errorf("Redis 响应失败或连接超时")
		}
		if len(line) < 3 || !strings.HasSuffix(string(line), "\r\n") {
			return "", fmt.Errorf("Redis 返回无效协议响应")
		}
		if line[0] == '-' {
			return "", fmt.Errorf("Redis 拒绝请求，请检查认证、账号权限和服务状态")
		}
		if line[0] != '+' {
			return "", fmt.Errorf("Redis 返回非预期协议响应")
		}
		return string(line[1 : len(line)-2]), nil
	}
	if user != "" || password != "" {
		args := []string{"AUTH", password}
		if user != "" {
			args = []string{"AUTH", user, password}
		}
		reply, err := command(args...)
		if err != nil || reply != "OK" {
			return fmt.Errorf("Redis 认证失败，请检查 ACL 用户名、密码或认证权限")
		}
	}
	reply, err := command("PING")
	if err != nil {
		return err
	}
	if reply != "PONG" {
		return fmt.Errorf("Redis PING 未返回 PONG")
	}
	return nil
}
