package services

import (
	"regexp"
	"strings"

	"github.com/simajiong/jaysql/internal/ai/adapters"
)

// ============================================================
// 提问路由：通识问题直答（系统只做转发），平台数据问题才挂工具
//
// 背景：此前每轮对话都无条件下发全部工具清单（11 个 ≈ 7.5KB ≈ 2.5k tokens），
// 对「介绍 mysqldump 实现原理」这类不需要平台数据的问题，这部分是纯开销，
// 且系统提示词里的「先调工具」倾向会让模型绕一圈工具再回答。
//
// 口径（宁可多挂工具，不可漏判平台问题 —— 误判成工具的代价只是多花 2.5k tokens，
// 误判成直答才会答不出数据）：
//   1. 强信号（平台实体名 / IP:端口 / 写意图）→ 工具；
//      但整句是「什么是 X / X 的实现原理 / X 与 Y 的区别」这类通识问法，
//      且不含「我的 / 平台 / 这张表 / 昨天」这类指向词时 → 仍走直答。
//   2. 上下文本体词（库 / 表 / 备份 / 索引 / 慢查询…）+ 指向词或时间状态词 → 工具。
//   3. 其余 → 直答（纯转发）。
//
// 只取「最后一条 user 消息」判定，历史消息不参与 —— 前面聊过实例，
// 后面问「mysqldump 原理」依然是通识问题。
//
// 已知边界（刻意不做关键词堆砌去覆盖）：
//   - 平台问题但一个信号词都不含（如「查一下耗时最长的语句」），会走直答，
//     此时模型按 direct 提示词明确回答「查不到平台数据」并建议换个说法 —— 可恢复，不会编造；
//   - 通识问题里带了指向词（如「我的 MySQL 怎么调优」）会走工具路由，代价只是多花
//     约 2.5k tokens，模型仍会直接作答。两侧都不做二次重试，避免把链路做成多段调用。
// ============================================================

type chatRoute string

const (
	// routeDirect 直答：不带 tools/tool_choice，单次调用把模型回复原样返回
	routeDirect chatRoute = "direct"
	// routeTools Agent 编排循环：带工具清单，允许模型多次调用平台工具
	routeTools chatRoute = "tools"
)

// platformEntitySignals 强信号：平台里的实体/功能名，日常通用技术问答基本不会用到
var platformEntitySignals = []string{
	"实例", "工单", "执行单", "审批", "备份任务", "备份集", "备份计划", "备份策略",
	"物理备份", "逻辑备份", "全量备份", "增量备份", "备份节点", "执行节点", "存储目标",
	"恢复任务", "告警", "慢查询", "慢sql", "慢 sql", "慢日志", "巡检", "监控大屏", "监控指标",
	"库表", "表结构", "建表语句", "建库", "连接数", "会话数", "死锁", "长事务",
	"保留天数", "磁盘容量", "数据源", "执行节点状态",
	"执行记录", "操作记录", "审计日志", "变更记录", "top sql", "topsql",
	// SQL 类工具的场景词：这些说法在通用技术问答里基本不会以「平台取数」的方式出现
	"执行计划", "explain", "索引推荐", "缺失索引", "采样", "抽样", "样例数据", "nl2sql",
}

// platformContextSignals 上下文本体词：既是平台对象也是通用技术词，
// 只有和「指向词 / 时间状态词」共现时才判为平台问题
var platformContextSignals = []string{
	"备份", "恢复", "库", "表", "索引", "字段", "主从", "复制", "延迟", "容量",
	"性能", "sql", "数据库", "mysql", "postgresql", "pg", "参数", "配置",
	"ddl", "dml", "变更", "上线", "投产", "回滚", "语句", "耗时", "排名", "明细", "记录", "日志",
}

// deicticSignals 指向词：把话题锚定到「本平台的具体对象」上
var deicticSignals = []string{
	"我的", "我们", "咱们", "平台", "系统", "jaysql", "这台", "这台机", "该实例",
	"这个实例", "该表", "这张表", "这个库", "该库", "上面", "刚才", "现有", "已配置",
	"当前", "内置", "这里", "列表中", "页面",
}

// timeStateSignals 时间/状态词：问「结果如何」必然是在问平台里的既成事实
var timeStateSignals = []string{
	"昨天", "今天", "前天", "最近", "上次", "这次", "刚才", "几天前", "上个月",
	"状态", "结果", "成功", "失败", "进度", "还在", "有没有跑", "跑完", "触发", "定时",
}

// definitionQuestionPattern 通识问法：解释概念、原理、机制、区别
var definitionQuestionPattern = regexp.MustCompile(`什么是|是什么|原理|实现机制|实现方式|工作机制|机制|区别|差异|优缺点|适用场景|怎么用|如何使用|科普|教程|最佳实践`)

// ipPortPattern 平台实例地址（IP:端口）
var ipPortPattern = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}:\d{2,5}\b`)

// writeIntentPattern 写意图：动词 + 平台对象（建一个 test001 库 / 创建工单 / 删除表）
var writeIntentPattern = regexp.MustCompile(`(创建|新建|建|删除|清空|提交|执行|导入|导出|跑).{0,20}(库|表|工单|备份|恢复|实例|索引|字段)`)

// decideChatRoute 依据最后一条用户消息判定走「直答」还是「工具」
func decideChatRoute(history []adapters.ChatMessage) chatRoute {
	text := lastUserText(history)
	if text == "" {
		return routeDirect
	}
	lower := strings.ToLower(text)

	// 写意图 / 实例地址：一定是平台操作
	if ipPortPattern.MatchString(lower) || writeIntentPattern.MatchString(lower) {
		return routeTools
	}

	anchored := containsAny(lower, deicticSignals) || containsAny(lower, timeStateSignals)
	defLike := definitionQuestionPattern.MatchString(lower)

	// 强信号：除非是「解释某个概念」的通识问法且没有任何平台指向
	if containsAny(lower, platformEntitySignals) {
		if defLike && !anchored {
			return routeDirect
		}
		return routeTools
	}

	// 上下文本体词：必须与指向词/时间状态词共现
	if containsAny(lower, platformContextSignals) && anchored {
		return routeTools
	}

	return routeDirect
}

// lastUserText 取最后一条用户消息的纯文本（本轮提问）
func lastUserText(history []adapters.ChatMessage) string {
	for i := len(history) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(history[i].Role), "user") {
			return strings.TrimSpace(history[i].Content)
		}
	}
	return ""
}

func containsAny(text string, signals []string) bool {
	for _, signal := range signals {
		if signal != "" && strings.Contains(text, signal) {
			return true
		}
	}
	return false
}

// buildChatRequest 按路由组装模型请求：直答不带工具，工具路由带全部工具清单。
// 抽成纯函数便于测试断言「直答路径的 payload 里没有 tools」。
func buildChatRequest(route chatRoute, model string, messages []adapters.ChatMessage) adapters.ChatRequest {
	if route == routeDirect {
		return adapters.ChatRequest{
			System:    chatSystemPromptDirect,
			Model:     model,
			MaxTokens: chatMaxTokens,
			Messages:  messages,
		}
	}
	return adapters.ChatRequest{
		System:     chatSystemPromptWithTools,
		Model:      model,
		MaxTokens:  chatMaxTokens,
		Messages:   messages,
		Tools:      chatToolDefinitions(),
		ToolChoice: "auto",
	}
}
