package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/pkg/secret"
)

// instance_privilege_check.go
//
// 连通性检测的「体检」部分：跑通 SELECT 1 只证明网络可达、账号能登录，
// 证明不了「采得到」。历史上所有"指标假健康"事故都不是连不上，而是：
// 连得上 → 查询成功 → 返回了偏小的值 → 告警阈值永远不触发。
//
// 本文件把"哪些权限决定哪些指标"显式化，让用户在纳管那一刻就看到缺口，
// 而不是等到出事以后再去猜。
//
// 设计约束：
//  1. 只读。体检只允许 SELECT / SHOW / 系统视图查询，禁止任何写操作与 DDL；
//  2. 失败无害。任一检查项失败都不阻断纳管，只降级为 warn/fail 提示；
//     检查表本身不做"是否放行"的决策，由调用方决定；
//  3. 区分「权限不足」与「引擎不适用」。缺 MySQL 的 REPLICATION CLIENT 是权限问题，
//     TiDB/Doris 不支持该语法是能力问题 —— 混为一谈会产生大量假告警。

// 体检项状态。
const (
	PrivilegeStatusPass = "pass"
	PrivilegeStatusFail = "fail"
	PrivilegeStatusWarn = "warn"
)

// SSH 状态取值。
const (
	sshStatusNotConfigured = "not_configured"
	sshStatusOK            = "ok"
	sshStatusFailed        = "failed"
)

// PrivilegeCheckItem 一项最小权限体检结果。
type PrivilegeCheckItem struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Impact   string `json:"impact"`
	Reason   string `json:"reason"`
	FixSQL   string `json:"fix_sql"`
	Required bool   `json:"required"` // true = 该缺口会直接导致已上线指标不可用
}

// ConnectionTestReport 连通性检测的回报：除连接结果本身，还带上 SSH 与权限体检结论。
type ConnectionTestReport struct {
	SSHStatus   string               `json:"ssh_status"`
	SSHMessage  string               `json:"ssh_message"`
	Privileges  []PrivilegeCheckItem `json:"privileges"`
	FailedCount int                  `json:"failed_count"`
	WarnCount   int                  `json:"warn_count"`
}

func newConnectionTestReport() *ConnectionTestReport {
	return &ConnectionTestReport{
		SSHStatus:  sshStatusNotConfigured,
		Privileges: make([]PrivilegeCheckItem, 0, 8),
	}
}

func (r *ConnectionTestReport) add(item PrivilegeCheckItem) {
	switch item.Status {
	case PrivilegeStatusFail:
		r.FailedCount++
	case PrivilegeStatusWarn:
		r.WarnCount++
	}
	r.Privileges = append(r.Privileges, item)
}

// probeOptionalSSHForBackup 主机 SSH 探测：备份/恢复需要它，监控采集不需要。
//
// 变更说明：此前 SSH 校验失败会让整次连通性检测失败，导致"只想接入监控"的用户
// 被迫先开通主机 SSH —— 而监控模块全链路不使用 SSH（服务端直连数据库 + Prometheus 拉主机）。
// 备份侧 resolveSSHCredentialsFromInstance 有独立的凭据校验，
// 这里降级为提示不会让备份静默失败。
func probeOptionalSSHForBackup(hostname string, cfg map[string]any) (status string, message string) {
	sshUser := getStringConfig(cfg, "ssh_user", "")
	if strings.TrimSpace(sshUser) == "" {
		return sshStatusNotConfigured, "未配置主机 SSH：不影响监控采集；逻辑备份/恢复与主机侧操作需要补全"
	}
	sshPassword := getStringConfig(cfg, "ssh_password", "")
	sshPrivateKey := getStringConfig(cfg, "ssh_private_key", "")
	// extra_config 里的 SSH 凭据是**密文入库**的（instanceSecretJSONPaths 清单，
	// 保存时 EncryptJSON 加密）。这里必须先解密再拨号，否则会把
	// `enc:v1:aesgcm:...` 整串当密码发给 sshd —— 服务器必然拒绝，表现为
	// "attempted methods [none password], no supported methods remain"。
	// 与备份侧 resolveSSHCredentialsFromInstance 的 decryptCredential 同一套
	// 幂等语义：密文还原明文，历史明文（secret.Decrypt 无前缀时）原样返回，
	// 因此加密上线前的存量数据不受影响。
	if plain, err := secret.Decrypt(strings.TrimSpace(sshPassword)); err == nil {
		sshPassword = plain
	}
	if plain, err := secret.Decrypt(strings.TrimSpace(sshPrivateKey)); err == nil {
		sshPrivateKey = plain
	}
	if strings.TrimSpace(sshPassword) == "" && strings.TrimSpace(sshPrivateKey) == "" {
		return sshStatusFailed, "已配置 ssh_user 但缺少 ssh_password / ssh_private_key：不影响监控采集，备份任务会在此环节报错"
	}
	sshPort := 22
	if portText := getStringConfig(cfg, "ssh_port", ""); portText != "" {
		if parsed, parseErr := parsePositivePort(portText); parseErr == nil {
			sshPort = parsed
		} else {
			return sshStatusFailed, "ssh_port 非法：" + parseErr.Error()
		}
	}
	if err := dialInstanceSSH(hostname, sshPort, sshUser, sshPassword, sshPrivateKey); err != nil {
		return sshStatusFailed, trimErrorText(err.Error(), 200) + "（不影响监控采集，备份/恢复需要此通道）"
	}
	return sshStatusOK, "主机 SSH 可用，备份/恢复通道就绪"
}

// ============================ MySQL / TiDB / Doris ============================

// checkMySQLProtocolPrivileges 体检 MySQL 协议族的最小权限。
//
// 取巧点：information_schema.user_privileges 只列出「当前会话用户自己」的全局权限，
// 因此一条 SQL 即可判定多个权限，无需解析 SHOW GRANTS 文本。
//
// 为什么单独查 PROCESS：MySQL 的 SHOW PROCESSLIST / information_schema.processlist
// 在没有 PROCESS 权限时**不报错**，只返回当前用户自己的行 —— 与 PostgreSQL
// pg_stat_activity 完全同构的"静默偏小"陷阱，只能靠权限声明判定。
func checkMySQLProtocolPrivileges(db dao.DB, user string) []PrivilegeCheckItem {
	items := make([]PrivilegeCheckItem, 0, 3)
	grantSQL := func(privs string) string {
		return fmt.Sprintf("GRANT %s ON *.* TO '%s'@'%%';", privs, strings.TrimSpace(user))
	}

	// 1) 全局状态：官方明确 SHOW GLOBAL STATUS 不需要任何权限，
	//    但 Doris 等派生分支可能有出入，仍然实测一遍，避免纸上谈兵。
	if _, _, err := db.Query("SHOW GLOBAL STATUS"); err != nil {
		items = append(items, PrivilegeCheckItem{
			Key:      "global_status",
			Name:     "读取全局状态变量",
			Status:   PrivilegeStatusFail,
			Impact:   "QPS/TPS/连接数/缓存命中率等绝大多数基础指标不可用",
			Reason:   "SHOW GLOBAL STATUS 执行失败：" + trimErrorText(err.Error(), 160),
			Required: true,
		})
	} else {
		items = append(items, PrivilegeCheckItem{
			Key:    "global_status",
			Name:   "读取全局状态变量",
			Status: PrivilegeStatusPass,
			Impact: "基础指标采集正常",
		})
	}

	// 2) 权限声明查询：一次取回 PROCESS / REPLICATION CLIENT。
	_, rows, err := db.Query(`
		SELECT
			COALESCE(SUM(privilege_type = 'PROCESS'), 0) AS has_process,
			COALESCE(SUM(privilege_type = 'REPLICATION CLIENT'), 0) AS has_repl_client
		FROM information_schema.user_privileges`)
	if err != nil || rows == nil || len(*rows) == 0 {
		// TiDB 早期版本 / Doris 没有这张视图 —— 是能力差异，不是权限问题。
		reason := "权限视图不可用"
		if err != nil {
			reason += "：" + trimErrorText(err.Error(), 160)
		}
		items = append(items, PrivilegeCheckItem{
			Key:    "session_detail",
			Name:   "会话明细与复制状态",
			Status: PrivilegeStatusWarn,
			Impact: "无法自检，请人工核对是否具备 PROCESS 与 REPLICATION CLIENT",
			Reason: reason,
		})
		return items
	}

	cell := (*rows)[0]
	if asBool(cell["has_process"]) {
		items = append(items, PrivilegeCheckItem{
			Key:    "session_detail",
			Name:   "查看全部会话（PROCESS）",
			Status: PrivilegeStatusPass,
			Impact: "会话明细可见",
		})
	} else {
		items = append(items, PrivilegeCheckItem{
			Key:      "session_detail",
			Name:     "查看全部会话（PROCESS）",
			Status:   PrivilegeStatusFail,
			Impact:   "SHOW PROCESSLIST 只会返回本账号自己的连接且**不报错**，会话明细类指标会静默偏小",
			Reason:   "当前账号不具备 PROCESS 权限",
			FixSQL:   grantSQL("PROCESS"),
			Required: false,
		})
	}

	if asBool(cell["has_repl_client"]) {
		items = append(items, PrivilegeCheckItem{
			Key:    "replication",
			Name:   "读取复制状态（REPLICATION CLIENT）",
			Status: PrivilegeStatusPass,
			Impact: "复制指标采集正常",
		})
	} else {
		items = append(items, PrivilegeCheckItem{
			Key:      "replication",
			Name:     "读取复制状态（REPLICATION CLIENT）",
			Status:   PrivilegeStatusFail,
			Impact:   "主从延迟、复制状态等复制类指标不可用",
			Reason:   "当前账号不具备 REPLICATION CLIENT 权限",
			FixSQL:   grantSQL("REPLICATION CLIENT"),
			Required: false,
		})
	}
	return items
}

// ============================ PostgreSQL ============================

// checkPostgresPrivileges 体检 PostgreSQL 的最小权限。
//
// 核心是 pg_stat_activity 的会话明细可读性。PostgreSQL 官方原文：
//
//	"Ordinary users can only see all the information about their own sessions.
//	 In rows about other sessions, many columns will be null. Note, however,
//	 that the existence of a session and its general properties ... are visible
//	 to all users."
//
// 即「行可见、列被屏蔽」。这比"行都看不到"危险得多：COUNT(*) 看着像正常的连接数，
// 而 COUNT(*) FILTER (WHERE state='active') 这类带列过滤的统计会恒偏小甚至恒为 0，
// 且不报错 —— 表现为"永远很健康"。openGauss / MogDB 系更激进，连 state 列都屏蔽。
//
// 为什么不用「查一行看是不是 NULL」来探测：无法区分"被权限屏蔽"与"本来就是 NULL"
// （没有活跃事务时 xact_start 本来就是 NULL、没有等待时 wait_event 本来就是 NULL）。
// 只有直接问 pg_has_role 才是确定答案。
func checkPostgresPrivileges(ctx context.Context, conn *pgx.Conn, user string) []PrivilegeCheckItem {
	items := make([]PrivilegeCheckItem, 0, 3)
	grantSQL := fmt.Sprintf("GRANT pg_read_all_stats TO %s;", quoteIdent(user))

	var superUser, hasStatsRole, hasMonitorRole bool
	err := conn.QueryRow(ctx, `
		SELECT
			COALESCE((SELECT rolsuper FROM pg_roles WHERE rolname = current_user), false),
			pg_has_role(current_user, 'pg_read_all_stats', 'MEMBER'),
			pg_has_role(current_user, 'pg_monitor', 'MEMBER')`).
		Scan(&superUser, &hasStatsRole, &hasMonitorRole)
	if err != nil {
		// 自检本身失败时按"不可读"处理：项目铁律是拿不到就明说，绝不放行假值。
		items = append(items, PrivilegeCheckItem{
			Key:      "pg_stat_activity",
			Name:     "读取会话明细（pg_stat_activity）",
			Status:   PrivilegeStatusFail,
			Impact:   "活跃连接/长事务/慢查询/空闲事务/等待连接/膨胀点等指标会被屏蔽列而静默偏小",
			Reason:   "权限自检失败，按不可读处理：" + trimErrorText(err.Error(), 160),
			FixSQL:   grantSQL,
			Required: true,
		})
	} else if superUser || hasStatsRole || hasMonitorRole {
		items = append(items, PrivilegeCheckItem{
			Key:    "pg_stat_activity",
			Name:   "读取会话明细（pg_stat_activity）",
			Status: PrivilegeStatusPass,
			Impact: "会话与查询明细完整可见",
		})
	} else {
		items = append(items, PrivilegeCheckItem{
			Key:      "pg_stat_activity",
			Name:     "读取会话明细（pg_stat_activity）",
			Status:   PrivilegeStatusFail,
			Impact:   "活跃连接/长事务/慢查询/空闲事务/等待连接/膨胀点等指标会被屏蔽列而**静默偏小**",
			Reason:   "当前角色既非超级用户，也不具备 pg_read_all_stats / pg_monitor",
			FixSQL:   grantSQL,
			Required: true,
		})
	}

	// pg_stat_statements：QPS 的唯一来源，缺它 QPS 只能标 unsupported。
	var calls float64
	if err := conn.QueryRow(ctx, `SELECT COALESCE(SUM(calls), 0) FROM pg_stat_statements`).Scan(&calls); err != nil {
		reason := "查询 pg_stat_statements 失败：" + trimErrorText(err.Error(), 120)
		var installed bool
		if err2 := conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')`).Scan(&installed); err2 == nil && !installed {
			reason = "未安装扩展 pg_stat_statements"
		}
		items = append(items, PrivilegeCheckItem{
			Key:      "pg_stat_statements",
			Name:     "语句执行统计（pg_stat_statements）",
			Status:   PrivilegeStatusWarn,
			Impact:   "QPS 指标将标记为 unsupported（不会返回 0 冒充健康）",
			Reason:   reason,
			FixSQL:   "CREATE EXTENSION IF NOT EXISTS pg_stat_statements; -- 并在 postgresql.conf 的 shared_preload_libraries 中加入 pg_stat_statements",
			Required: false,
		})
	} else {
		items = append(items, PrivilegeCheckItem{
			Key:    "pg_stat_statements",
			Name:   "语句执行统计（pg_stat_statements）",
			Status: PrivilegeStatusPass,
			Impact: "QPS 可统计",
		})
	}
	return items
}

// ============================ 公共小工具 ============================

// asBool 把 MySQL 驱动的宽泛返回值归约为布尔。
// SUM() 在 MySQL 协议里可能以 []byte / string / int64 / float64 任何一种抵达。
func asBool(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case []byte:
		text := strings.TrimSpace(string(v))
		return text != "" && text != "0" && text != "0.0"
	case string:
		text := strings.TrimSpace(v)
		return text != "" && text != "0" && text != "0.0"
	case int64:
		return v != 0
	case int32:
		return v != 0
	case float64:
		return v != 0
	case bool:
		return v
	default:
		return false
	}
}

// quoteIdent 给 SQL 标识符加双引号并转义，用于拼接授权语句（仅做展示，不执行）。
func quoteIdent(text string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(text), `"`, `""`) + `"`
}

// trimErrorText 截断错误文本，避免把长堆栈塞进 last_check_message 与前端弹窗。
func trimErrorText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}
