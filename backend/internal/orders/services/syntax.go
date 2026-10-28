package services

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/simajiong/jaysql/internal/global"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/inspect/checker"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	inspectServices "github.com/simajiong/jaysql/internal/inspect/services"
	"github.com/simajiong/jaysql/internal/orders/forms"
	"github.com/simajiong/jaysql/pkg/secret"

	"github.com/gin-gonic/gin"
)

type InspectOrderSyntaxService struct {
	*forms.InspectOrderSyntaxForm
	C        *gin.Context
	Username string
}

// 获取实例配置
func (s *InspectOrderSyntaxService) getInstanceConfig() (commonModels.InsightInstances, error) {
	// 获取实例配置
	var config commonModels.InsightInstances
	tx := global.App.DB.Table("`insight_instances`").
		Where("instance_id=?", s.InstanceID).
		First(&config)
	if tx.RowsAffected == 0 {
		return config, fmt.Errorf("未找到实例ID为%s的记录", s.InstanceID)
	}
	return config, nil
}

// 审核SQL
func (s *InspectOrderSyntaxService) inspectSQL(instanceCfg commonModels.InsightInstances) ([]checker.ReturnData, error) {
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}
	connDB, inspectSchema := inspectConnTarget(string(s.DBType), s.Database, s.Schema)
	inspect := checker.SyntaxInspectService{
		C:             s.C,
		InstanceID:    instanceCfg.InstanceID,
		DbUser:        instanceCfg.User,
		DbPassword:    plainPassword,
		DbHost:        instanceCfg.Hostname,
		DbPort:        instanceCfg.Port,
		DbType:        string(s.DBType),
		DBSchema:      connDB,
		InspectSchema: inspectSchema,
		// Oracle 元信息探测需要服务名（extra_config.service_name）；其它库类型不消费该字段
		ServiceName: oracleServiceName(instanceCfg.ExtraConfig),
		Username:    s.Username,
		SqlText:     s.Content,
	}
	returnData, err := inspect.Run()
	if err != nil {
		// 语法错误不再让整个审核失败：转成一条可定位、会阻断的结构化问题项。
		// 其余错误（连不上库、权限不足等）仍按真实系统错误上抛，前端据此提示重试。
		if data, ok := syntaxErrorToReturnData(err, s.Content); ok {
			return data, nil
		}
		return nil, err
	}
	return returnData, nil
}

// syntaxErrorToReturnData 把语法错误转成一条结构化审核结果。
//
// 语法错误过去以裸 error 上抛，预审接口整体失败，前端只能显示「规范预审快照读取失败」——
// 把用户的 SQL 错误说成了系统故障，且看不到出错位置。这里改走结构化通道：
// 语法错误作为一条 must 级问题项进入预审结果，可定位、可阻断、有修复建议。
func syntaxErrorToReturnData(err error, sqlText string) ([]checker.ReturnData, bool) {
	var syntaxErr *checker.SyntaxError
	if !errors.As(err, &syntaxErr) {
		return nil, false
	}
	return syntaxErr.ToReturnData(sqlText), true
}

// inspectDatabaseLevelDDL 库层级 DDL（CREATE DATABASE / ALTER DATABASE）的静态审核。
//
// 库层级 SQL 拿不到目标库，常规审核链路（要连库取 DB 变量）无法执行，
// 这里改用不依赖连接的静态审核，保证「限制库的字符集」等库级规则不会整体失效。
// PG 类语法 TiDB parser 解析不了，直接跳过，维持原有行为。
func inspectDatabaseLevelDDL(content string, instanceID string, dbType string) ([]checker.ReturnData, error) {
	if isPgLikeDB(dbType) {
		return nil, nil
	}
	return checker.InspectDatabaseLevelDDL(content, instanceID)
}

func (s *InspectOrderSyntaxService) Run() (any, error) {
	inferredSQLType, err := inferOrderSQLType(s.Content, string(s.DBType))
	if err != nil {
		return nil, err
	}
	if s.SQLType, err = reconcileOrderSQLType(string(s.SQLType), inferredSQLType); err != nil {
		return nil, err
	}

	config, err := s.getInstanceConfig()
	if err != nil {
		return nil, err
	}
	if s.DBType == "" {
		s.DBType = config.DbType
	}
	targetMode, err := detectOrderTargetMode(s.Content, string(s.DBType))
	if err != nil {
		return nil, err
	}
	targetResource, err := resolveOrderTargetResource(string(s.DBType), s.Database, s.Schema, targetMode)
	if err != nil {
		return nil, err
	}
	s.Schema = targetResource
	if !isPgLikeDB(string(s.DBType)) && strings.TrimSpace(s.Database) != "" {
		s.Schema = strings.TrimSpace(s.Database)
	}
	err = validateOrderSQLTypeSupport(string(s.DBType), string(s.SQLType))
	if err != nil {
		return nil, err
	}
	if s.SQLType == "EXPORT" {
		// 导出工单仅检查语法是否有效，不审核，CheckSqlType已经判断类型为SELECT了
		return nil, nil
	}
	if targetMode != orderTargetSchema {
		// 库层级 SQL（CREATE DATABASE / CREATE SCHEMA）在提交阶段没有目标库可选，
		// 常规审核链路需要连库取变量，只能整体短路 —— 这会让「限制库的字符集」这类
		// 库级规则变成死代码。库级字符集规则是纯语法判定，这里补跑静态审核。
		returnData, err := inspectDatabaseLevelDDL(s.Content, s.InstanceID, string(s.DBType))
		if err != nil {
			return nil, err
		}
		status := 0
		for _, row := range returnData {
			for _, sum := range row.Summary {
				if sum.Level != "INFO" {
					status = 1
					break
				}
			}
			if status == 1 {
				break
			}
		}
		return map[string]any{"status": status, "data": returnData}, nil
	}
	if !supportsDeepInspect(string(s.DBType)) {
		return nil, nil
	}
	// 审核（不再做数据授权校验：变更 / 导出由工单审批覆盖，
	// 数据授权只约束查询接口，见 das/services/access_action_scope.go）
	returnData, err := s.inspectSQL(config)
	if err != nil {
		return nil, err
	}

	// 检查语法检查是否通过
	// status: 0表示语法检查通过，1表示语法检查不通过
	status := 0
	for _, row := range returnData {
		for _, sum := range row.Summary {
			if sum.Level != "INFO" {
				status = 1
				break
			}
		}
		if status == 1 {
			break
		}
	}

	return map[string]any{"status": status, "data": returnData}, nil
}

// 规则名称中常见的动作前缀，匹配时需要剥离后再与审核消息比对
var ruleNamePrefixes = []string{"限制", "要求", "禁止", "建议", "推荐", "必须", "需要", "不允许"}

// 无实际语义、在匹配时应忽略的停用词/助词/动词（中文）
var matchStopWords = map[string]struct{}{
	"的": {}, "了": {}, "在": {}, "是": {}, "和": {}, "或": {}, "与": {}, "为": {}, "对": {}, "等": {},
	"请": {}, "要": {}, "有": {}, "使用": {}, "建议": {}, "推荐": {}, "必须": {}, "需要": {}, "应该": {},
	"应当": {}, "禁止": {}, "不允许": {}, "不能": {}, "不要": {}, "不得": {}, "应": {}, "须": {}, "需": {},
	"可": {}, "能": {}, "会": {}, "将": {}, "被": {}, "把": {}, "给": {}, "从": {}, "向": {},
	"表": {}, "列": {}, "字段": {}, "库": {}, "数据库": {}, "索引": {}, "键": {}, "名": {}, "名称": {},
}

// normalizeMatchText 归一化文本：去空白、去引号与常见标点，消除 md 规则名与引擎文案的书写差异
func normalizeMatchText(s string) string {
	replacer := strings.NewReplacer(
		" ", "", "\t", "", "　", "",
		"'", "", "\"", "", "`", "",
		"（", "(", "）", ")", "【", "(", "】", ")",
		"：", ":", "，", ",", "。", ".", "-", "", "_", "",
	)
	return strings.ToLower(replacer.Replace(s))
}

// quotedIdentifierRe 匹配审核消息中的库表/字段等标识符（'xxx'、"xxx"、`xxx`）。
// 引擎会把具体对象名拼进消息里，导致与规则名无法直接包含匹配，需先剥离。
var quotedIdentifierRe = regexp.MustCompile("`[^`]*`|'[^']*'|\"[^\"]*\"")

// normalizeMessageText 归一化审核消息：先剥离被引用的标识符，再做通用归一化
func normalizeMessageText(s string) string {
	return normalizeMatchText(quotedIdentifierRe.ReplaceAllString(s, ""))
}

// ruleNameCandidates 生成规则名的匹配候选（原名 + 剥离动作前缀后的形式）
func ruleNameCandidates(name string) []string {
	normalized := normalizeMatchText(name)
	if normalized == "" {
		return nil
	}
	candidates := []string{normalized}
	for _, prefix := range ruleNamePrefixes {
		trimmed := strings.TrimPrefix(normalized, normalizeMatchText(prefix))
		if trimmed != "" && trimmed != normalized {
			candidates = append(candidates, trimmed)
		}
	}
	return candidates
}

// extractRuleText 从规则记录中提取用于匹配的候选文本。
// 优先使用 name + description；同时混入 rule_key 中的中文词与英文缩写（如 pk/engine），
// 弥补规则名与引擎文案用词不一致的问题。
func extractRuleText(rule inspectServices.StandardRuleLevel) string {
	raw := rule.Name + " " + rule.Description
	// rule_key 去掉数据库/分类前缀，保留后半段
	if idx := strings.LastIndex(rule.RuleKey, "."); idx >= 0 && idx+1 < len(rule.RuleKey) {
		keyPart := rule.RuleKey[idx+1:]
		// 去掉常见英文前缀
		keyPart = strings.ReplaceAll(keyPart, "limit_", "")
		keyPart = strings.ReplaceAll(keyPart, "tbl_", "表")
		keyPart = strings.ReplaceAll(keyPart, "col_", "列")
		keyPart = strings.ReplaceAll(keyPart, "idx_", "索引")
		keyPart = strings.ReplaceAll(keyPart, "pk", "主键")
		keyPart = strings.ReplaceAll(keyPart, "engine", "引擎")
		keyPart = strings.ReplaceAll(keyPart, "comment", "注释")
		keyPart = strings.ReplaceAll(keyPart, "default", "默认值")
		keyPart = strings.ReplaceAll(keyPart, "varchar", "varchar")
		keyPart = strings.ReplaceAll(keyPart, "char", "char")
		keyPart = strings.ReplaceAll(keyPart, "json", "json")
		keyPart = strings.ReplaceAll(keyPart, "auto_inc", "自增")
		keyPart = strings.ReplaceAll(keyPart, "auto_increment", "自增")
		raw += " " + keyPart
	}
	return raw
}

// buildCharSet 把文本拆成「有意义字符」集合（中文字符 + 英文/数字序列）。
// 停用词与动作词会被过滤，避免它们拉高误匹配分数。
func buildCharSet(s string) map[string]struct{} {
	norm := normalizeMatchText(s)
	set := make(map[string]struct{})
	// 提取英文/数字单词
	for _, w := range regexp.MustCompile("[a-z0-9]+").FindAllString(norm, -1) {
		if _, stop := matchStopWords[w]; !stop {
			set[w] = struct{}{}
		}
	}
	// 提取中文字符，并同时加入二字组合（bigram）以保留局部语义
	runes := []rune(norm)
	chinese := make([]rune, 0, len(runes))
	for _, r := range runes {
		if r >= 0x4e00 && r <= 0x9fff {
			chinese = append(chinese, r)
		}
	}
	for i, r := range chinese {
		rs := string(r)
		if _, stop := matchStopWords[rs]; !stop {
			set[rs] = struct{}{}
		}
		if i+1 < len(chinese) {
			bigram := string(chinese[i]) + string(chinese[i+1])
			if _, stop := matchStopWords[bigram]; !stop {
				set[bigram] = struct{}{}
			}
		}
	}
	return set
}

// charOverlapScore 计算消息字符集合与规则字符集合的相似度。
// 返回交集数量、Jaccard 相似度、以及交集占比（交集/规则集合大小）。
func charOverlapScore(msgSet, ruleSet map[string]struct{}) (intersection int, jaccard, coverage float64) {
	if len(ruleSet) == 0 {
		return 0, 0, 0
	}
	inter := 0
	for k := range ruleSet {
		if _, ok := msgSet[k]; ok {
			inter++
		}
	}
	union := len(msgSet) + len(ruleSet) - inter
	if union == 0 {
		return 0, 0, 0
	}
	return inter, float64(inter) / float64(union), float64(inter) / float64(len(ruleSet))
}

// matchRuleSeverity 把审核消息关联回具体的规范规则，返回该规则在规范中的处理级别。
// 命中多条规则时取最严格级别（must 优先）。未命中返回空字符串，由调用方走兜底策略。
//
// 匹配策略：
//  1. 先用字符重叠度在 name/description/rule_key 上找最佳匹配；
//  2. 再用子串包含匹配兜底（兼容历史上文案完全一致的规则）。
func matchRuleSeverity(ruleLevels []inspectServices.StandardRuleLevel, message string) string {
	if hit, ok := matchRuleByText(ruleLevels, message); ok {
		return hit.Level
	}
	return ""
}

// resolveInspectSeverity 决定单条审核结果的严重程度，优先级从高到低：
//  1. 摘要已携带 rule_key → 直接用该规则在规范中的级别（最精确，支持单条规则单独设为必须改进）
//  2. 摘要无 rule_key → 用字符重叠/子串匹配把消息关联到具体规则，返回其级别
//  3. 仍无法关联，但属于「执行必然失败」的硬错误类别（语法错误/对象已存在/对象不存在）→ 按必须改进阻断
//  4. 仍无法关联 → 用规范中启用规则的默认（占优）级别，并列时取 suggest
//  5. 无规范绑定（规范中不存在该参数）→ 一律按 suggest 处理：
//     没有规范定义作为来源时，不得凭消息文案判定为「必须改进」
//
// 第 3 条是「严重度与阻断强度匹配」原则的落点：语法错误、对象已存在/不存在都会让
// 语句在执行阶段必然失败，不能因为引擎没给它 rule_key 就降级成「建议改进」放行。
func resolveInspectSeverity(
	ruleLevels []inspectServices.StandardRuleLevel,
	defaultSeverity string,
	summaryRuleKey string,
	message string,
) string {
	return resolveInspectSeverityWithCategory(ruleLevels, defaultSeverity, summaryRuleKey, message, "", nil)
}

// resolveInspectSeverityWithCategory 在 resolveInspectSeverity 基础上纳入错误类别判定
func resolveInspectSeverityWithCategory(
	ruleLevels []inspectServices.StandardRuleLevel,
	defaultSeverity string,
	summaryRuleKey string,
	message string,
	category string,
	disabledRules map[string]bool,
) string {
	level, _ := resolveInspectRule(ruleLevels, defaultSeverity, summaryRuleKey, message, category, disabledRules)
	return level
}

// resolveInspectRule 判定单条审核结果的严重程度，并给出它命中的规范规则条目。
//
// 返回值：
//   - level：最终级别（must / suggest）；为空字符串表示该问题已被规范显式关闭，不应报出
//   - rule：命中的规范规则；RuleKey 为空表示未能关联到具体规则（走类别/规范默认级别兜底）
//
// 优先级与 resolveInspectSeverity 完全一致，二者共用同一套判定，保证
// 「展示的级别」与「展示的命中规则」永远来自同一条规则。
func resolveInspectRule(
	ruleLevels []inspectServices.StandardRuleLevel,
	defaultSeverity string,
	summaryRuleKey string,
	message string,
	category string,
	disabledRules map[string]bool,
) (string, inspectServices.StandardRuleLevel) {
	matched := matchRuleByKeyOrText(ruleLevels, defaultSeverity, summaryRuleKey, message, category, disabledRules)
	return matched.Level, matched.Rule
}

// isRuleDisabled 判断规则是否被规范显式关闭（支持全 key 与去掉数据库前缀后的后缀两种写法）
func isRuleDisabled(disabledRules map[string]bool, ruleKey string) bool {
	if ruleKey == "" || len(disabledRules) == 0 {
		return false
	}
	if disabledRules[ruleKey] {
		return true
	}
	if idx := strings.LastIndex(ruleKey, "."); idx >= 0 {
		return disabledRules[ruleKey[idx+1:]]
	}
	return false
}

// matchInspectRuleResult 一次匹配的完整结果
type matchInspectRuleResult struct {
	Level string
	Rule  inspectServices.StandardRuleLevel
}

// matchRuleByKeyOrText 按「摘要 rule_key → 文案匹配 → 致命类别兜底 → 规范默认级别」的顺序判定级别与命中规则
func matchRuleByKeyOrText(
	ruleLevels []inspectServices.StandardRuleLevel,
	defaultSeverity string,
	summaryRuleKey string,
	message string,
	category string,
	disabledRules map[string]bool,
) matchInspectRuleResult {
	// 0. 规则被规范显式关闭：用户主动关掉的检查不再报出（含致命类错误）。
	//    这里必须在致命类别兜底之前判定，否则「关闭」对硬错误无效。
	if isRuleDisabled(disabledRules, summaryRuleKey) {
		return matchInspectRuleResult{}
	}

	// 1. 引擎已显式关联规则 key（支持全 key 匹配，也支持去掉数据库前缀后的后缀匹配）
	if summaryRuleKey != "" {
		summarySuffix := summaryRuleKey
		if idx := strings.LastIndex(summaryRuleKey, "."); idx >= 0 {
			summarySuffix = summaryRuleKey[idx+1:]
		}
		for _, rule := range ruleLevels {
			ruleSuffix := rule.RuleKey
			if idx := strings.LastIndex(rule.RuleKey, "."); idx >= 0 {
				ruleSuffix = rule.RuleKey[idx+1:]
			}
			if rule.RuleKey == summaryRuleKey || ruleSuffix == summarySuffix {
				return matchInspectRuleResult{Level: rule.Level, Rule: rule}
			}
		}
		// 走到这里说明规范里没有任何规则能匹配该 key（全名或后缀）：
		// 不再退化到文案相似度猜测。文案匹配会把「列`c`不允许变更数据类型」这类消息
		// 误挂到「限制主键列的数据类型」等名字相近的规则上，展示出错误的规则名。
		// 直接走致命类别 / 规范默认级别兜底，命中规则留空（确实没有对应规则条目）。
		if controllers.IsFatalCategory(category) {
			return matchInspectRuleResult{Level: "must"}
		}
		if defaultSeverity == "must" || defaultSeverity == "suggest" {
			return matchInspectRuleResult{Level: defaultSeverity}
		}
		return matchInspectRuleResult{Level: "suggest"}
	}

	// 2. 通过文案匹配规则：命中即返回该规则（含其级别）
	if hit, ok := matchRuleByText(ruleLevels, message); ok {
		return matchInspectRuleResult{Level: hit.Level, Rule: hit}
	}

	// 3. 致命类别兜底：语法错误 / 对象已存在 / 对象不存在，执行阶段必然失败。
	//    规范里可能还没有对应的规则条目（存量规范未补种），此时不得按默认级别降级放行，
	//    否则会出现「页面显示告警、用户点了提交、执行必失败」的矛盾。
	//    命中规则留空（确实没有对应规则），但级别强制为 must。
	if controllers.IsFatalCategory(category) {
		return matchInspectRuleResult{Level: "must"}
	}

	// 4. 规范默认级别兜底：未能关联到具体规则，命中规则留空，级别仍来自规范
	if defaultSeverity == "must" || defaultSeverity == "suggest" {
		return matchInspectRuleResult{Level: defaultSeverity}
	}

	// 5. 规范中不存在该参数：不做任何额外判断，统一按建议改进处理
	return matchInspectRuleResult{Level: "suggest"}
}

// matchRuleByText 用字符重叠度 + 子串包含把审核消息关联回具体规则
func matchRuleByText(ruleLevels []inspectServices.StandardRuleLevel, message string) (inspectServices.StandardRuleLevel, bool) {
	normMsg := normalizeMessageText(message)
	if normMsg == "" || len(ruleLevels) == 0 {
		return inspectServices.StandardRuleLevel{}, false
	}
	msgSet := buildCharSet(message)
	if len(msgSet) == 0 {
		return inspectServices.StandardRuleLevel{}, false
	}

	type scoredRule struct {
		level inspectServices.StandardRuleLevel
		inter int
		score float64 // Jaccard 相似度
	}
	var matches []scoredRule
	for _, rule := range ruleLevels {
		ruleSet := buildCharSet(extractRuleText(rule))
		if len(ruleSet) == 0 {
			continue
		}
		inter, jaccard, _ := charOverlapScore(msgSet, ruleSet)
		// 命中阈值：至少共享 2 个特征，且 Jaccard >= 0.08（宽松）
		if inter >= 2 && jaccard >= 0.08 {
			matches = append(matches, scoredRule{rule, inter, jaccard})
		}
	}
	if len(matches) > 0 {
		sort.Slice(matches, func(i, j int) bool {
			if matches[i].score != matches[j].score {
				return matches[i].score > matches[j].score
			}
			// must 优先
			return matches[i].level.Level == "must" && matches[j].level.Level != "must"
		})
		return matches[0].level, true
	}

	// 兜底：子串包含匹配（保留原兼容逻辑）
	var fallback inspectServices.StandardRuleLevel
	found := false
	severity := ""
	for _, rule := range ruleLevels {
		hit := false
		for _, candidate := range ruleNameCandidates(rule.Name) {
			if len(candidate) >= 4 && strings.Contains(normMsg, candidate) {
				hit = true
				break
			}
			if len(normMsg) >= 8 && strings.Contains(candidate, normMsg) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		if rule.Level == "must" {
			return rule, true
		}
		if !found || severity == "" {
			fallback = rule
			severity = "suggest"
			found = true
		}
	}
	return fallback, found
}

// 将 checker 原始结果转换为规范预审结构化结果。
// ruleLevels 为实例绑定规范的规则级别明细，defaultSeverity 为规范默认级别。
// preInspectItem 单条预审问题项；RuleKey/RuleName 为它命中的规范规则条目。
//
// Category/Object/Reason/Advice 支撑「四要素」结构化展示：位置 · 对象 · 原因 · 建议。
// 前端据此渲染专用错误卡片，而不是把 message 当纯文本堆给用户 ——
// 三类错误（语法错误 / 对象已存在 / 对象不存在）各有不同的修复路径，
// 混在一条纯文本里用户既看不出错在哪，也不知道怎么改。
type preInspectItem struct {
	Line     int    `json:"line"`
	Level    string `json:"level"`
	Message  string `json:"message"`
	Query    string `json:"query"`
	RuleKey  string `json:"rule_key"`
	RuleName string `json:"rule_name"`
	// Category 错误类别：syntax_error / object_conflict / object_missing / rule_violation
	Category string `json:"category,omitempty"`
	// Position 出错位置（语法错误为「第 N 行第 M 列」）
	Position string `json:"position,omitempty"`
	// Object 出错对象名（表/列/索引/视图）
	Object string `json:"object,omitempty"`
	// Reason 失败原因（含执行后果）
	Reason string `json:"reason,omitempty"`
	// Advice 可执行的修复建议
	Advice string `json:"advice,omitempty"`
}

// buildPreInspectReason 生成「原因」：在事实陈述之外补上执行后果，
// 让用户明白这不是一条可忽略的风格建议，而是会导致执行失败的问题。
func buildPreInspectReason(category, message string) string {
	switch category {
	case controllers.CategoryObjectConflict:
		return message + "；目标对象已存在，继续执行将失败"
	case controllers.CategoryObjectMissing:
		return message + "；目标对象不存在，继续执行将失败"
	case controllers.CategorySyntaxError:
		return message
	case controllers.CategoryForbiddenStatement:
		// 语句级准入结论：不是「写得不好」，而是「平台不接受这类语句」——
		// 补一句后果说明，用户才知道为什么不能提交（而不是继续按规范细节去改）。
		return message + "；该语句类型在平台上不允许执行，提交将被阻断"
	default:
		return message
	}
}

// hitRuleItem 命中的规范规则条目（去重后的汇总，便于调用方一次性拿到全部命中规则）
type hitRuleItem struct {
	RuleKey string `json:"rule_key"`
	Name    string `json:"name"`
	Level   string `json:"level"`
	Count   int    `json:"count"`
}

func buildPreInspectResult(
	returnData []checker.ReturnData,
	ruleLevels []inspectServices.StandardRuleLevel,
	defaultSeverity string,
) map[string]any {
	return buildPreInspectResultWithVersion(returnData, ruleLevels, defaultSeverity, inspectServices.StandardRuleVersion{})
}

// buildPreInspectResultWithSnapshot 用完整快照构建预审结果（生产链路入口）。
//
// 必须传快照而不是只传规则级别：快照里的 DisabledRules 决定「被规范关闭的检查」不再报出，
// 缺少它时致命类错误（对象已存在/不存在）会永远强制开启，规范里的关闭开关形同虚设。
func buildPreInspectResultWithSnapshot(
	returnData []checker.ReturnData,
	snapshot inspectServices.InstanceStandardSnapshot,
) map[string]any {
	return buildPreInspectResultFull(returnData, snapshot.Rules, snapshot.DefaultSeverity, snapshot.Version, snapshot.DisabledRules)
}

// buildPreInspectResultWithVersion 在 buildPreInspectResult 基础上记录本次预审所用的规则版本。
//
// version 必须来自「与本次审核执行同源」的那次规则加载（见 runInspectWithVersionGuard），
// 这样结果回写时记录的规则版本，与真正参与校验的规则严格一致。
// buildPreInspectResultWithVersion 在 buildPreInspectResult 基础上记录本次预审所用的规则版本。
//
// version 必须来自「与本次审核执行同源」的那次规则加载（见 runInspectWithVersionGuard），
// 这样结果回写时记录的规则版本，与真正参与校验的规则严格一致。
// 不含被关闭规则集合（历史签名，供测试与无快照场景使用）；生产链路请用
// buildPreInspectResultWithSnapshot，否则被规范关闭的检查无法真正关闭。
func buildPreInspectResultWithVersion(
	returnData []checker.ReturnData,
	ruleLevels []inspectServices.StandardRuleLevel,
	defaultSeverity string,
	version inspectServices.StandardRuleVersion,
) map[string]any {
	return buildPreInspectResultFull(returnData, ruleLevels, defaultSeverity, version, nil)
}

// buildPreInspectResultFull 构建预审结果全量版本（含被关闭规则集合）
func buildPreInspectResultFull(
	returnData []checker.ReturnData,
	ruleLevels []inspectServices.StandardRuleLevel,
	defaultSeverity string,
	version inspectServices.StandardRuleVersion,
	disabledRules map[string]bool,
) map[string]any {
	var items []preInspectItem
	hitRules := make([]hitRuleItem, 0)
	hitIndex := map[string]int{}
	mustCount := 0
	suggestCount := 0
	hasSyntaxError := false
	for _, row := range returnData {
		for _, sum := range row.Summary {
			if sum.Level == "INFO" {
				continue
			}
			category := strings.TrimSpace(sum.Category)
			if category == "" {
				category = controllers.CategoryRuleViolation
			}
			if category == controllers.CategorySyntaxError {
				hasSyntaxError = true
			}
			level, rule := resolveInspectRule(ruleLevels, defaultSeverity, sum.RuleKey, sum.Message, category, disabledRules)
			// 规则被规范显式关闭：该检查不再报出，也不参与计数/阻断
			if level == "" {
				continue
			}
			if level == "must" {
				mustCount++
			} else {
				suggestCount++
			}
			items = append(items, preInspectItem{
				Line:     row.Line,
				Level:    level,
				Message:  sum.Message,
				Query:    row.Query,
				RuleKey:  rule.RuleKey,
				RuleName: rule.Name,
				Category: category,
				Position: sum.Object,
				Object:   sum.Object,
				Reason:   buildPreInspectReason(category, sum.Message),
				Advice:   sum.Advice,
			})
			if rule.RuleKey != "" {
				key := rule.RuleKey + "|" + level
				if idx, ok := hitIndex[key]; ok {
					hitRules[idx].Count++
					continue
				}
				hitIndex[key] = len(hitRules)
				hitRules = append(hitRules, hitRuleItem{
					RuleKey: rule.RuleKey,
					Name:    rule.Name,
					Level:   level,
					Count:   1,
				})
			}
		}
	}

	status := "passed"
	if mustCount > 0 {
		status = "must_fix"
	} else if suggestCount > 0 {
		status = "suggest"
	}

	return map[string]any{
		"status":        status,
		"must_count":    mustCount,
		"suggest_count": suggestCount,
		"items":         items,
		"hit_rules":     hitRules,
		// 存在语法错误：前端据此显示「SQL 语法错误」而不是笼统的失败提示。
		// 语法错误已按 must 计入 mustCount（会阻断提交审批），这里单独标记只为区分文案与呈现。
		"has_syntax_error": hasSyntaxError,
		// 本次预审所用的规则版本：结果落库后据此判断快照是否仍对应当前生效规则
		"rule_version": version.Version,
		"standard_id":  version.StandardID,
		"rule_bound":   version.Bound,
		"rule_count":   version.RuleCount,
		"rule_enabled": version.EnabledCount,
		"inspected_at": version.CapturedAt,
	}
}

// 判断 checker 结果中是否存在必须修改的违规项。
// 分级口径与 buildPreInspectResultFull 完全一致，保证「预审放行」与「提交拦截」结论相同。
func hasMustLevelViolations(
	returnData []checker.ReturnData,
	ruleLevels []inspectServices.StandardRuleLevel,
	defaultSeverity string,
) bool {
	for _, row := range returnData {
		for _, sum := range row.Summary {
			if sum.Level == "INFO" {
				continue
			}
			if resolveInspectSeverityWithCategory(ruleLevels, defaultSeverity, sum.RuleKey, sum.Message, sum.Category, nil) == "must" {
				return true
			}
		}
	}
	return false
}

// 提交前规范预审服务：返回带行号、级别、描述的结构化审核结果。
type PreInspectOrderService struct {
	*forms.InspectOrderSyntaxForm
	C        *gin.Context
	Username string
}

func (s *PreInspectOrderService) Run() (any, error) {
	// 逐条语句定型：既给出整单类型（DDL / DML / MIXED），也供后续冲突分析使用
	plan, err := BuildOrderPlan(s.Content, string(s.DBType))
	if err != nil {
		return nil, err
	}
	if s.SQLType, err = reconcileOrderSQLType(string(s.SQLType), commonModels.EnumType(plan.SQLType)); err != nil {
		return nil, err
	}

	var config commonModels.InsightInstances
	tx := global.App.DB.Table("`insight_instances`").
		Where("instance_id=?", s.InstanceID).
		First(&config)
	if tx.RowsAffected == 0 {
		return nil, fmt.Errorf("未找到实例ID为%s的记录", s.InstanceID)
	}
	if s.DBType == "" {
		s.DBType = config.DbType
	}
	// 规范预审的严重程度由实例绑定的 SQL 规范决定，而非审核消息文案。
	// 规则级别、兜底级别、规则版本一次性加载（详见 runInspectWithVersionGuard），
	// 保证「读取规则 → 执行审核 → 判级回写」落在同一版本上：
	// 支持把单条规则单独设为「必须改进」，其余规则仍按「建议改进」处理。
	targetMode, err := detectOrderTargetMode(s.Content, string(s.DBType))
	if err != nil {
		return nil, err
	}
	targetResource, err := resolveOrderTargetResource(string(s.DBType), s.Database, s.Schema, targetMode)
	if err != nil {
		return nil, err
	}
	s.Schema = targetResource
	if !isPgLikeDB(string(s.DBType)) && strings.TrimSpace(s.Database) != "" {
		s.Schema = strings.TrimSpace(s.Database)
	}
	if err := validateOrderSQLTypeSupport(string(s.DBType), string(s.SQLType)); err != nil {
		return nil, err
	}

	returnData, snapshot, err := s.runInspectWithVersionGuard(config, targetMode)
	if err != nil {
		return nil, err
	}

	result := buildPreInspectResultWithSnapshot(returnData, snapshot)
	// 混合工单（DDL + DML 同单）的静态冲突结论并入同一份预审结果：
	// 「DROP TABLE 后又 INSERT 同表」这类必然失败的写法在规范规则链里查不出来，
	// 只能靠语句计划的对象时序分析补上，否则会一路放行到执行阶段才报错。
	appendOrderPlanFindings(result, plan)
	return result, nil
}

// runInspectWithVersionGuard 在「同一规则版本」下完成一次完整预审：
//  1. 审核前读取一次规则（级别 + 兜底级别 + 版本）；
//  2. 按该版本执行审核；
//  3. 复核版本：若规则在审核执行期间被修改（版本漂移），以最新版本重跑一次审核，
//     并改用最新版本判级 —— 保证返回的结论与记录的规则版本严格对应。
//
// 每次预审都是全量重跑：不读取、不复用任何历史预审结果。
func (s *PreInspectOrderService) runInspectWithVersionGuard(
	config commonModels.InsightInstances,
	targetMode orderTargetMode,
) ([]checker.ReturnData, inspectServices.InstanceStandardSnapshot, error) {
	snapshot := inspectServices.LoadInstanceStandardSnapshot(s.InstanceID)
	returnData, err := s.runInspectOnce(config, targetMode)
	if err != nil {
		return nil, snapshot, err
	}
	latest := inspectServices.LoadInstanceStandardSnapshot(s.InstanceID)
	if latest.Version.Version != snapshot.Version.Version {
		global.App.Log.Warnf(
			"order pre-inspect rule version drift: %s -> %s, instance: %s, order sql re-inspected with latest rules",
			snapshot.Version.Version, latest.Version.Version, s.InstanceID,
		)
		snapshot = latest
		if returnData, err = s.runInspectOnce(config, targetMode); err != nil {
			return nil, snapshot, err
		}
	}
	return returnData, snapshot, nil
}

// runInspectOnce 执行一次审核（不做版本复核），按工单目标层级分派到对应审核链路
func (s *PreInspectOrderService) runInspectOnce(
	config commonModels.InsightInstances,
	targetMode orderTargetMode,
) ([]checker.ReturnData, error) {
	if s.SQLType == "EXPORT" {
		return nil, nil
	}
	if targetMode != orderTargetSchema {
		// 库层级 SQL（CREATE DATABASE / CREATE SCHEMA）没有目标库，常规审核链路无法执行；
		// 补跑不依赖连接的库级静态审核，保证库级规则（如库字符集）不会整体失效。
		return inspectDatabaseLevelDDL(s.Content, s.InstanceID, string(s.DBType))
	}
	if !supportsDeepInspect(string(s.DBType)) {
		return nil, nil
	}
	plainPassword, err := secret.Decrypt(config.Password)
	if err != nil {
		return nil, err
	}
	connDB, inspectSchema := inspectConnTarget(string(s.DBType), s.Database, s.Schema)
	inspect := checker.SyntaxInspectService{
		C:             s.C,
		InstanceID:    config.InstanceID,
		DbUser:        config.User,
		DbPassword:    plainPassword,
		DbHost:        config.Hostname,
		DbPort:        config.Port,
		DbType:        string(s.DBType),
		DBSchema:      connDB,
		InspectSchema: inspectSchema,
		// Oracle 元信息探测需要服务名（extra_config.service_name）；其它库类型不消费该字段
		ServiceName: oracleServiceName(config.ExtraConfig),
		Username:    s.Username,
		SqlText:     s.Content,
	}
	returnData, err := inspect.Run()
	if err != nil {
		// 语法错误不再让整个审核失败：转成一条可定位、会阻断的结构化问题项。
		// 其余错误（连不上库、权限不足等）仍按真实系统错误上抛，前端据此提示重试。
		if data, ok := syntaxErrorToReturnData(err, s.Content); ok {
			return data, nil
		}
		return nil, err
	}
	return returnData, nil
}
