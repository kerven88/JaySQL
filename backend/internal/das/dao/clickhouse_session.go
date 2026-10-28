package dao

// ============================================================================
// ClickHouse「会话」
//
// 与 MySQLSession 同源的动机：ClickhouseDB.Query 的语义是
// 「一次查询 = clickhouse.Open + Ping + Query + conn.Close」，
// 而监控采集一轮要对同一个实例跑 2 条 SQL —— 于是"采集一次"
// 变成"建连两次 + Ping 两次"，每条连接都是一次完整的 TCP + 认证握手。
//
// 本文件提供的是**新增能力，不是替换 ClickhouseDB.Query**：
//   - ClickhouseDB.Query 的行为一字未改（只补了 rows.Close，见 scanClickHouseRows），
//     10 处既有调用方零影响；
//   - 只有需要"一轮多次查询"的调用方（监控采集）显式开会话，用完 Close。
//
// 不做全局池缓存的理由与 MySQLSession 相同：那会在业务库上留下常驻连接，
// 反而与"降低采集冲击"的目标相悖。
// ============================================================================

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// defaultClickHouseSessionDialTimeout 拨号超时，与 ClickhouseDB.connect 保持一致。
const defaultClickHouseSessionDialTimeout = 3 * time.Second

// ClickHouseSessionConfig 建立一个 ClickHouse 会话所需的全部参数。
type ClickHouseSessionConfig struct {
	User     string
	Password string
	Host     string
	Port     int
	Database string
	Settings map[string]interface{}
	// Ctx 会话内所有查询共用的上下文（调用方负责设置总超时）。
	Ctx context.Context
	// DialTimeout 为 0 时回落默认 3 秒。
	DialTimeout time.Duration
}

// ClickHouseSession 一个「一轮采集 / 一批查询」生命周期内的 ClickHouse 会话。
//
// 使用纪律同 MySQLSession：单协程串行使用、用完 Close、不跨轮复用。
type ClickHouseSession struct {
	conn driver.Conn
	ctx  context.Context

	mu sync.Mutex
	// brokenErr 一旦置上，后续 Query 直接短路返回（见 MySQLSession 的同名注释）。
	brokenErr error
	closed    bool
}

// OpenClickHouseSession 打开一个会话。
//
// 这里保留一次 Ping：clickhouse-go 的 Open 是惰性的，Ping 才真正建连。
// 与 MySQLSession 不同（那里刻意不 Ping），CK 一轮只有 2 条查询，
// 一次 Ping 换来"连不上就在开会话时报出来"，让错误归因更干净：
// 主机不可达 / 认证失败不会再伪装成"第一条查询返回空结果"。
func OpenClickHouseSession(cfg ClickHouseSessionConfig) (*ClickHouseSession, error) {
	ctx := cfg.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	dialTimeout := cfg.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = defaultClickHouseSessionDialTimeout
	}

	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.User,
			Password: cfg.Password,
		},
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
		DialTimeout: dialTimeout,
		// 与 MySQLSession 一致：会话串行使用，一条物理连接足够，
		// 顺便保证同一轮的所有查询确实落在同一条连接上。
		MaxOpenConns: 1,
		MaxIdleConns: 1,
		Settings:     cfg.Settings,
	})
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(ctx); err != nil {
		// Open 成功但 Ping 失败：必须主动关闭，否则底层连接池留下一条未回收的连接。
		_ = conn.Close()
		return nil, err
	}
	return &ClickHouseSession{conn: conn, ctx: ctx}, nil
}

// Query 在会话上执行一条查询，返回与 ClickhouseDB.Query 完全同形状的结果。
func (s *ClickHouseSession) Query(query string) (*[]string, *[]map[string]interface{}, error) {
	if s == nil || s.conn == nil {
		return nil, nil, errors.New("会话未初始化")
	}
	if broken := s.BrokenErr(); broken != nil {
		return nil, nil, broken
	}
	rows, err := s.conn.Query(s.ctx, query)
	if err != nil {
		if IsConnectionError(err) {
			s.markBroken(err)
		}
		return nil, nil, err
	}
	columns, result, err := scanClickHouseRows(rows)
	if err != nil && IsConnectionError(err) {
		s.markBroken(err)
	}
	if err != nil {
		return nil, nil, err
	}
	return &columns, &result, nil
}

// Close 关闭会话。幂等，采集侧无条件 defer 即可。
func (s *ClickHouseSession) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	return s.conn.Close()
}

// BrokenErr 返回导致会话失效的错误（未失效返回 nil）。
func (s *ClickHouseSession) BrokenErr() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.brokenErr
}

func (s *ClickHouseSession) markBroken(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.brokenErr == nil {
		s.brokenErr = err
	}
}
