package services

import "strings"

// monitor_pg_activity_priv.go
//
// PostgreSQL「会话明细可读性」闸门。
//
// ============================ 为什么要这道闸门 ============================
//
// PostgreSQL 官方文档（Cumulative Statistics System 章节）原文：
//
//	"Ordinary users can only see all the information about their own sessions
//	 (sessions belonging to a role that they are a member of). In rows about
//	 other sessions, many columns will be null. Note, however, that the
//	 existence of a session and its general properties such as its sessions
//	 user and database are visible to all users. Superusers and roles with
//	 privileges of built-in role pg_read_all_stats can see all the information
//	 about all sessions."
//
// 翻译成本系统的后果：
//   - 行可见：COUNT(*) FROM pg_stat_activity 对所有用户都准 → 连接数可信；
//   - 列被屏蔽：state / query_start / xact_start / state_change / wait_event /
//     backend_xid / backend_xmin 对"别人的会话"会是 NULL
//     （openGauss / MogDB 系更激进，连 state 都屏蔽）。
//
// 致命之处在于：
//	SELECT COUNT(*) FROM pg_stat_activity WHERE state = 'active'
//	SELECT COUNT(*) FROM pg_stat_activity WHERE now() - query_start > interval '5 seconds'
//   这类语句在列被屏蔽时不会报错，NULL 参与比较的结果不是 true，行不计入 COUNT，
//   于是**静默返回偏小的值（常为 0）**。监控上就是"永远很健康"，
//   比直接报错危险得多 —— 这是本系统最忌讳的一类假信号。
//
// ============================ 为什么不用"探测列是不是 NULL" ============================
//
// 无法区分两种 NULL：
//   - 被权限屏蔽；
//   - 业务上本来就是 NULL（没有活跃事务时 xact_start 本来就是 NULL；
//     没有等待事件时 wait_event 本来就是 NULL；query_start 同理）。
// 靠 sampling 猜会有大量误判。只有直接问 pg_has_role() 才是确定答案，
// 而且它是纯元数据查询、零副作用、跨版本稳定。
//
// ============================ 失败如何处置 ============================
//
// 自检本身失败时按「不可读」处理，而不是按「可读」。理由同项目既有约定：
// 拿不到就明说（unsupported + 原因），绝不返回 0 冒充健康。
// 代价是权限自检异常时一批会话类指标会临时 unsupported，
// 这是可以接受的 —— 宁可少给数，不可给错数。

const pgActivityFixSQLHint = "以超级用户执行：GRANT pg_read_all_stats TO \"<监控账号>\";（等价做法：GRANT pg_monitor TO \"<监控账号>\";）"

// pgActivityReadability 会话明细可读性的判定结果。
type pgActivityReadability struct {
	// DetailReadable 是否可以读取"其他会话"的列明细。
	// 为 false 时，任何依赖 state / query_start / xact_start / wait_event /
	// backend_xmin 等列做过滤的统计都必须标 unsupported。
	DetailReadable bool
	// Reason 不可读的具体原因（可读时为空）。
	Reason string
}

// pgActivityReadabilityOf 由主查询带回的三个权限标量子查询构造可读性结论。
//
// 内联在主查询里是有意的：这套采集器在 100 实例规模下"往返次数是要害"，
// 为权限自检单独跑一次 SQL 等于每实例每轮多一次往返。
// pg_has_role() 是纯元数据函数，零副作用，放进聚合 SELECT 列表不影响结果行数。
//
// 判定条件（满足任一即可读）：超级用户 / 具备 pg_read_all_stats / 具备 pg_monitor。
// 用 pg_has_role(..., 'MEMBER') 而非直接查 pg_auth_members，因为后者只看一层，
// 会漏掉角色嵌套（role A → role B → 用户）的间接授权。
func pgActivityReadabilityOf(superUser, hasStatsRole, hasMonitorRole bool) pgActivityReadability {
	if superUser || hasStatsRole || hasMonitorRole {
		return pgActivityReadability{DetailReadable: true}
	}
	return pgActivityReadability{
		DetailReadable: false,
		Reason:         "当前角色不是超级用户，也不具备 pg_read_all_stats / pg_monitor；pg_stat_activity 中其他会话的列会被置为 NULL，带列过滤的统计会静默偏小。" + pgActivityFixSQLHint,
	}
}

// pgActivityDenialReason 生成「因会话明细不可读而停用」的统一原因文案。
// 统一文案有利于前端按关键字归并与用户自查。
//
// 可读时返回空串：函数不该被调用，返回空串比返回文案更容易暴露调用方的门控缺陷。
func pgActivityDenialReason(readability pgActivityReadability) string {
	if readability.DetailReadable {
		return ""
	}
	reason := strings.TrimSpace(readability.Reason)
	if reason == "" {
		reason = "pg_stat_activity 列被权限屏蔽"
	}
	return "会话明细不可读：" + reason
}
