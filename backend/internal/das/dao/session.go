package dao

// ============================================================================
// MySQL 协议「会话」
//
// 背景：DB.Query 的语义是「一次查询 = 一次 sql.Open + 一次完整握手认证 + Close」。
// 这个语义对"构造一次只查一次"的调用方（工单执行、元信息查询、连接体检）完全合适，
// 但监控采集一轮要对同一个实例跑约 10 条 SQL —— 于是"采集一次"变成了"建连十次"，
// 每条连接都要走完整的 TCP + 认证握手，握手成本远大于查询本身，
// 并且会推高业务库的 Connections / Aborted_connects（后者正是我们自己在采集的指标）。
//
// 本文件提供的是**新增能力，不是替换 DB.Query**：
//   - DB.Query 的行为一字未改，25 处既有调用方零影响；
//   - 需要"一轮多次查询"的调用方（监控采集）显式开一个会话，用完 Close。
//
// 为什么不做全局池缓存：那会让 N 个实例在业务库上留下 N 条**常驻**连接，
// 对 max_connections 紧张的小实例反而更糟，还要额外处理口令变更失效与实例回收。
// 60 秒周期下"一轮一连接"已经是每秒零点几次的量级，收益/复杂度比最优。
// ============================================================================

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	// defaultSessionDialTimeout 拨号超时，与 DB.Open 保持一致。
	defaultSessionDialTimeout = 3 * time.Second
	// defaultSessionConnMaxLifetime 池内连接的最大存活时长。
	//
	// 设成 5 分钟的意义：远小于 MySQL 默认 wait_timeout（28800 秒），
	// 保证业务库单方面掐断连接之前，客户端已经主动换掉了它，
	// 不会出现"取到一个服务端已关闭的连接、第一次查询必然失败"的毛刺。
	defaultSessionConnMaxLifetime = 5 * time.Minute
)

// SessionConfig 建立一个会话所需的全部参数。
type SessionConfig struct {
	User     string
	Password string
	Host     string
	Port     int
	Database string
	Params   map[string]string
	// Ctx 会话内所有查询共用的上下文（调用方负责设置总超时）。
	Ctx context.Context
	// DialTimeout 为 0 时回落默认 3 秒。
	DialTimeout time.Duration
	// ConnMaxLifetime 为 0 时回落默认 5 分钟。
	ConnMaxLifetime time.Duration
}

// MySQLSession 一个「一轮采集 / 一批查询」生命周期内的 MySQL 协议会话。
//
// 使用纪律（违反会出事，见包注释）：
//  1. 一个会话只在一个 goroutine 内串行使用，**不跨协程共享**；
//  2. 用完必须 Close（调用方 defer）；
//  3. 不做全局缓存、不跨轮复用。
type MySQLSession struct {
	db  *sql.DB
	ctx context.Context

	mu sync.Mutex
	// brokenErr 一旦被置上，后续 Query 直接短路返回，不再白白等一次超时。
	// 连接已经断了的时候，剩下的 9 条查询只会各自再耗掉一个超时预算 ——
	// 这正是"一轮 8 秒预算被吃光、指标随机缺失"的成因之一。
	brokenErr error
}

// OpenMySQLSession 打开一个会话。
//
// 注意：这里**不做 Ping**。sql.Open 本身不建连，真正的握手发生在第一次查询；
// 提前 Ping 等于凭空多一次建连，正是本次要消灭的开销。连接不可用会在首次查询时暴露。
func OpenMySQLSession(cfg SessionConfig) (*MySQLSession, error) {
	dialTimeout := cfg.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = defaultSessionDialTimeout
	}
	maxLifetime := cfg.ConnMaxLifetime
	if maxLifetime <= 0 {
		maxLifetime = defaultSessionConnMaxLifetime
	}
	params := cfg.Params
	if params == nil {
		params = map[string]string{}
	}

	config := mysql.Config{
		User:                 cfg.User,
		Passwd:               cfg.Password,
		Addr:                 fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Net:                  "tcp",
		DBName:               cfg.Database,
		AllowNativePasswords: true,
		Params:               params,
		Timeout:              dialTimeout,
	}
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return nil, err
	}
	// 只允许一条物理连接：会话是串行使用的，多开没有意义，
	// 还能保证"同一轮内所有查询确实落在同一条连接上"。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(maxLifetime)

	ctx := cfg.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return &MySQLSession{db: db, ctx: ctx}, nil
}

// Query 在会话上执行一条查询，返回与 DB.Query **完全同形状**的结果。
//
// 短路语义：若之前已有查询返回过连接类错误，直接返回缓存的错误，
// 不再向一条已知断开的连接发第 N 次请求。返回值与"每条查询各自失败"时一致，
// 因此采集侧标 QualityError 的文案不会变化。
func (s *MySQLSession) Query(query string) (*[]string, *[]map[string]interface{}, error) {
	if s == nil {
		return nil, nil, errors.New("会话未初始化")
	}
	s.mu.Lock()
	broken := s.brokenErr
	s.mu.Unlock()
	if broken != nil {
		return nil, nil, broken
	}

	rows, err := s.db.QueryContext(s.ctx, query)
	if err != nil {
		if IsConnectionError(err) {
			s.markBroken(err)
		}
		return nil, nil, err
	}
	columns, result, err := scanQueryRows(rows)
	if err != nil && IsConnectionError(err) {
		s.markBroken(err)
	}
	return columns, result, err
}

// Close 关闭会话。可重复调用（幂等），采集侧无条件 defer 即可。
func (s *MySQLSession) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// BrokenErr 返回导致会话失效的错误（未失效返回 nil）。
// 供调用方在"整轮结束"时判断是否需要把本轮标记为整体失败。
func (s *MySQLSession) BrokenErr() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.brokenErr
}

func (s *MySQLSession) markBroken(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.brokenErr == nil {
		s.brokenErr = err
	}
}

// IsConnectionError 判定错误是否属于"连接已经不可用"。
//
// 判定为是的后果是整条会话作废，所以这里只收**确定性的链路/连接类**错误：
// SQL 语法错误、权限不足、表不存在这类都不会误伤 —— 它们只影响单条查询，
// 会话本身仍然是好的，后续查询必须继续跑。
func IsConnectionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		return true
	}
	message := strings.ToLower(err.Error())
	patterns := []string{
		"invalid connection", "bad connection", "broken pipe", "connection reset",
		"connection refused", "i/o timeout", "eof", "unexpected eof",
		"driver: bad connection", "sql: connection is already closed",
		"tls: ", "no such host", "dial tcp", "server closed",
	}
	for _, item := range patterns {
		if strings.Contains(message, item) {
			return true
		}
	}
	return false
}
