// Package redisreader provides bounded RESP2 reads for the Redis key browser.
// No user-provided command text is accepted, and write/admin commands are absent.
package redisreader

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	maxResponseBytes = 512 << 10
	maxBulkBytes     = 64 << 10
	maxArrayItems    = 4096
	maxArraySlots    = 8192
)

type Value struct {
	Kind    byte
	Text    string
	Integer int64
	Array   []Value
	Nil     bool
}
type Options struct {
	Host               string
	Port               int
	Username, Password string
	TLS                bool
	Database           int
}
type Client struct {
	conn     net.Conn
	response responseReader
	stop     func() bool
}
type responseReader struct {
	reader     *bufio.Reader
	remaining  int
	arraySlots int
}

func (r *responseReader) take(n int) error {
	if n < 0 || n > r.remaining {
		return fmt.Errorf("Redis 响应超过 512 KiB 安全上限，请缩小查询范围")
	}
	r.remaining -= n
	return nil
}
func (r *responseReader) read(depth int) (Value, error) {
	if depth > 8 {
		return Value{}, fmt.Errorf("Redis 响应嵌套过深")
	}
	line, err := r.reader.ReadSlice('\n')
	if err != nil {
		return Value{}, fmt.Errorf("Redis 响应失败或连接超时")
	}
	if err := r.take(len(line)); err != nil {
		return Value{}, err
	}
	if len(line) < 3 || line[len(line)-2] != '\r' {
		return Value{}, fmt.Errorf("Redis 协议响应无效")
	}
	value := Value{Kind: line[0]}
	text := string(line[1 : len(line)-2])
	switch line[0] {
	case '+', '-':
		value.Text = text
		return value, nil
	case ':':
		value.Integer, err = strconv.ParseInt(text, 10, 64)
		return value, err
	case '$', '*':
		size, err := strconv.ParseInt(text, 10, 64)
		if err != nil || size < -1 {
			return Value{}, fmt.Errorf("Redis 响应长度无效")
		}
		if size == -1 {
			value.Nil = true
			return value, nil
		}
		if line[0] == '$' {
			if size > maxBulkBytes {
				return Value{}, fmt.Errorf("Redis 单个返回字段超过 64 KiB 安全上限；大字符串请使用分段预览")
			}
			if err := r.take(int(size) + 2); err != nil {
				return Value{}, err
			}
			data := make([]byte, int(size)+2)
			if _, err := io.ReadFull(r.reader, data); err != nil {
				return Value{}, fmt.Errorf("Redis 响应读取失败或连接超时")
			}
			if data[len(data)-2] != '\r' || data[len(data)-1] != '\n' {
				return Value{}, fmt.Errorf("Redis 数据响应格式无效")
			}
			value.Text = string(data[:len(data)-2])
			return value, nil
		}
		if size > maxArrayItems {
			return Value{}, fmt.Errorf("Redis 返回项目超过 4096 项安全上限，请缩小查询范围")
		}
		// Reserve the aggregate element budget before allocation. Per-array caps
		// alone would allow many small headers to trigger large nested allocations.
		if int(size) > r.arraySlots {
			return Value{}, fmt.Errorf("Redis 响应嵌套项目超过安全上限，请缩小查询范围")
		}
		r.arraySlots -= int(size)
		value.Array = make([]Value, 0, int(size))
		for i := int64(0); i < size; i++ {
			child, err := r.read(depth + 1)
			if err != nil {
				return Value{}, err
			}
			value.Array = append(value.Array, child)
		}
		return value, nil
	default:
		return Value{}, fmt.Errorf("Redis 返回了不支持的协议响应")
	}
}
func Dial(ctx context.Context, opts Options) (*Client, error) {
	if opts.Database < 0 || opts.Database > 65535 || opts.Port < 1 || opts.Port > 65535 || opts.Host == "" {
		return nil, fmt.Errorf("Redis 连接参数不合法")
	}
	address := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))
	dialer := &net.Dialer{Timeout: 4 * time.Second}
	var conn net.Conn
	var err error
	if opts.TLS {
		secure := tls.Dialer{NetDialer: dialer, Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: opts.Host}}
		conn, err = secure.DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, fmt.Errorf("Redis 连接失败，请检查地址、TLS 和网络")
	}
	deadline := time.Now().Add(8 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, fmt.Errorf("Redis 无法设置读取超时")
	}
	client := &Client{conn: conn, response: responseReader{reader: bufio.NewReaderSize(conn, 4096), remaining: maxResponseBytes, arraySlots: maxArraySlots}}
	client.stop = context.AfterFunc(ctx, func() { conn.Close() })
	if opts.Username != "" || opts.Password != "" {
		args := []string{"AUTH", opts.Password}
		if opts.Username != "" {
			args = []string{"AUTH", opts.Username, opts.Password}
		}
		reply, err := client.exchange(args)
		if err != nil || reply.Text != "OK" {
			client.Close()
			return nil, fmt.Errorf("Redis 认证失败，请检查用户名、密码和 ACL 权限")
		}
	}
	if opts.Database > 0 {
		reply, err := client.exchange([]string{"SELECT", strconv.Itoa(opts.Database)})
		if err != nil || reply.Text != "OK" {
			client.Close()
			return nil, fmt.Errorf("Redis DB 不可用或账号无 SELECT 权限；Cluster 仅支持 DB 0")
		}
	}
	return client, nil
}
func (c *Client) Close() error {
	if c.stop != nil {
		c.stop()
	}
	return c.conn.Close()
}

var readCommands = map[string]bool{"SCAN": true, "TYPE": true, "TTL": true, "STRLEN": true, "GETRANGE": true, "HLEN": true, "HSCAN": true, "LLEN": true, "LRANGE": true, "SCARD": true, "SSCAN": true, "ZCARD": true, "ZSCAN": true, "XLEN": true, "XRANGE": true}

func encodeCommand(args []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(arg), arg)
	}
	return b.String()
}
func (c *Client) exchange(args []string) (Value, error) {
	if _, err := io.WriteString(c.conn, encodeCommand(args)); err != nil {
		return Value{}, fmt.Errorf("Redis 请求发送失败")
	}
	return c.reply()
}
func (c *Client) reply() (Value, error) {
	value, err := c.response.read(0)
	if err != nil {
		return Value{}, err
	}
	if value.Kind == '-' {
		code := strings.Fields(value.Text)
		if len(code) > 0 {
			switch code[0] {
			case "NOAUTH", "WRONGPASS", "NOPERM", "WRONGTYPE", "LOADING", "CLUSTERDOWN":
				return Value{}, fmt.Errorf("Redis 返回 %s，请检查账号权限、Key 类型和服务状态", code[0])
			}
		}
		return Value{}, fmt.Errorf("Redis 拒绝只读请求，请检查服务状态和权限")
	}
	return value, nil
}
func (c *Client) Read(command string, args ...string) (Value, error) {
	if !readCommands[command] {
		return Value{}, fmt.Errorf("不支持的 Redis 只读操作")
	}
	return c.exchange(append([]string{command}, args...))
}

// ReadMany pipelines metadata reads without one network round trip per key.
func (c *Client) ReadMany(commands [][]string) ([]Value, error) {
	var request strings.Builder
	for _, args := range commands {
		if len(args) == 0 || !readCommands[args[0]] {
			return nil, fmt.Errorf("不支持的 Redis 只读操作")
		}
		request.WriteString(encodeCommand(args))
	}
	if _, err := io.WriteString(c.conn, request.String()); err != nil {
		return nil, fmt.Errorf("Redis 请求发送失败")
	}
	replies := make([]Value, 0, len(commands))
	for range commands {
		reply, err := c.reply()
		if err != nil {
			return nil, err
		}
		replies = append(replies, reply)
	}
	return replies, nil
}
