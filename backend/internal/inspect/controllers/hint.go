package controllers

import (
	"github.com/simajiong/jaysql/pkg/kv"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
)

// 审核问题的错误类别。
//
// 类别决定两件事：
//  1. 前端的呈现形态（语法错误 / 对象冲突 / 对象缺失 / 规范违规各有专用卡片）；
//  2. 级别的兜底策略 —— 前三类意味着「继续执行必然失败」，即使规范里没有
//     对应规则条目，也不得降级为「建议改进」（见 orders/services 的类别兜底）。
const (
	CategorySyntaxError    = "syntax_error"    // SQL 无法解析
	CategoryObjectConflict = "object_conflict" // 目标对象已存在
	CategoryObjectMissing  = "object_missing"  // 目标对象不存在
	CategoryRuleViolation  = "rule_violation"  // 规范规则违规（默认类别）
	// CategoryForbiddenStatement 语句类型被平台禁止执行（CTAS / CTL …）。
	// 它不是「这条语句写得不够好」，而是「平台不接受这类语句」，因此与规范级别无关：
	// 语义上等价于「提交也不会被执行」，必须按「必须改进」阻断（见 IsFatalCategory）。
	CategoryForbiddenStatement = "forbidden_statement"
)

// IsFatalCategory 判断类别是否属于「执行必然失败」的硬错误。
// 这类错误在未能关联到具体规范规则时，必须按「必须改进」阻断，不能走规范默认级别兜底。
func IsFatalCategory(category string) bool {
	switch category {
	case CategorySyntaxError, CategoryObjectConflict, CategoryObjectMissing, CategoryForbiddenStatement:
		return true
	}
	return false
}

// ForbiddenStatement 构造「该语句类型不允许执行」的结论。
//
// 只由语句准入闸门（checker.statementAdmissionGate）产出：它表达的是语句级判定，
// 不是规则级违规 —— 语句一旦被判定不允许执行，后续基于语句结构的检查（引擎/主键…）
// 都不再有意义（结构来自源表或 SELECT，本就无从判定），因此闸门会终止该语句的规则链。
func ForbiddenStatement(ruleKey, object, advice, message string) SummaryItem {
	return SummaryItem{
		Level:    "WARN",
		RuleKey:  ruleKey,
		Category: CategoryForbiddenStatement,
		Object:   object,
		Advice:   advice,
		Message:  message,
	}
}

// level: INFO/WARN/ERROR
type SummaryItem struct {
	Level   string `json:"level"`              // 级别,INFO/WARN/ERROR
	RuleKey string `json:"rule_key,omitempty"` // 触发该摘要的规范规则 key（为空表示引擎未显式关联）
	Message string `json:"message"`
	// Category 错误类别（见上方常量）。为空按 rule_violation 处理，保证存量规则行为不变。
	Category string `json:"category,omitempty"`
	// Object 出错对象名（表名/列名/索引名/库名），用于前端结构化展示
	Object string `json:"object,omitempty"`
	// Advice 可执行的修复建议
	Advice string `json:"advice,omitempty"`
}

// RuleHint 单条规则的执行上下文与产出。
//
// 这里刻意**没有**「跳过后续检查」这类开关：一条语句上的规则必须全部执行、结果全部收集。
// 历史字段 IsBreak（json: skip_next_step）已删除 —— 它会让前序规则的结论掩盖后续规则的违规
// （典型：UPDATE 缺 WHERE 短路掉必须级的「影响行数上限」，导致全表 UPDATE 免检）。
type RuleHint struct {
	Summary      []SummaryItem `json:"summary"`       // 摘要
	AffectedRows int           `json:"affected_rows"` // 默认为0
	MergeAlter   string        `json:"merge_alter,omitempty"`

	// 内部执行上下文：禁止输出到 JSON（避免泄露/序列化失败）
	DB            *dao.DB               `json:"-"`
	KV            *kv.KVCache           `json:"-"`
	Query         string                `json:"-"` // 原始SQL
	InspectParams *config.InspectParams `json:"-"`

	// CurrentRuleKey 当前正在检查的规则 key，Warn/AddSummary 会把它写入摘要。
	// 逻辑函数在调用 Warn 前设置，即可把审核消息精确关联回规范规则。
	CurrentRuleKey string `json:"-"`
}

func (r *RuleHint) AddSummary(level, message string) {
	if r == nil {
		return
	}
	r.Summary = append(r.Summary, SummaryItem{Message: message, Level: level, RuleKey: r.CurrentRuleKey})
}

// WarnWithRule 显式指定规则 key 输出 WARN 摘要。
// 当同一逻辑函数内包含多条不同规则时，可用本方法替代设置 CurrentRuleKey。
func (r *RuleHint) WarnWithRule(ruleKey, message string) {
	if r == nil {
		return
	}
	r.Summary = append(r.Summary, SummaryItem{Message: message, Level: "WARN", RuleKey: ruleKey})
}

func (r *RuleHint) Info(message string) { r.AddSummary("INFO", message) }
func (r *RuleHint) Warn(message string) { r.AddSummary("WARN", message) }

// WarnFatal 输出「执行必然失败」类的审核问题（语法错误 / 对象已存在 / 对象不存在）。
//
// 与 Warn 的两点关键差异：
//  1. 携带 category / object / advice，前端据此渲染四要素错误卡片（位置·对象·原因·建议），
//     而不是把整条消息当纯文本堆给用户；
//  2. 不阻断后续规则 —— 对象存在性是独立结论，中断检查会掩盖同一条语句上的其它违规
//     （历史上「表已存在」排在规则首位且置 IsBreak，导致主键/字符集/索引等规则全部被跳过）。
func (r *RuleHint) WarnFatal(ruleKey, category, object, advice, message string) {
	if r == nil {
		return
	}
	r.Summary = append(r.Summary, SummaryItem{
		Message:  message,
		Level:    "WARN",
		RuleKey:  ruleKey,
		Category: category,
		Object:   object,
		Advice:   advice,
	})
}
