package services

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"github.com/simajiong/jaysql/pkg/notifier"
	"gorm.io/datatypes"
)

// ============================================================================
// 通知策略服务：把「什么告警」路由到「哪些渠道 + 哪些接收人」
//
// 匹配语义：一台实例的告警要同时满足策略里所有**非空**条件才算命中（AND）；
// 空数组 = 该维度不限制。多条策略可以同时命中：告警会分别投递给每条策略的渠道集合
// （例如「P0 → 钉钉群 + 值班邮件」「全部 → 邮件归档」可以并存）。
// ============================================================================

// NotifyPolicyForm 策略保存表单。
//
// Name 刻意不加 binding:"required"：同一个表单结构被「预览匹配范围」复用，
// 预览时不该因为名称还没填就被 400 拦掉（保存路径里有显式的空名校验，语义没有放松）。
type NotifyPolicyForm struct {
	ID               uint64                        `json:"id"`
	Name             string                        `json:"name"`
	Enabled          *bool                         `json:"enabled"`
	Priority         int                           `json:"priority"`
	MatchLevels      []string                      `json:"match_levels"`
	MatchDbTypes     []string                      `json:"match_db_types"`
	MatchBusiness    []string                      `json:"match_business"`
	MatchEnvs        []string                      `json:"match_envs"`
	MatchInstances   []string                      `json:"match_instances"`
	MatchLayers      []string                      `json:"match_layers"`
	MatchTemplateIDs []uint64                      `json:"match_template_ids"`
	ChannelIDs       []uint64                      `json:"channel_ids"`
	Subjects         []monitorModels.NotifySubject `json:"subjects"`
	// NotifyIntervalMinutes 统一通知间隔：>0 = 告警每 N 分钟最多打扰一次
	// （未恢复未认领的持续告警按 N 重发，同指纹新事件 N 分钟内去重）；0 = 仅发送一次。
	// 落库时同步写 silence_minutes / repeat_minutes 两列（=N），投递链路无需感知合并。
	NotifyIntervalMinutes  int    `json:"notify_interval_minutes"`
	AggregateWindowSeconds int    `json:"aggregate_window_seconds"`
	EscalateAfterMinutes   int    `json:"escalate_after_minutes"`
	EscalatePolicyID       uint64 `json:"escalate_policy_id"`
	StopOnAck              *bool  `json:"stop_on_ack"`
	NotifyRecovery         *bool  `json:"notify_recovery"`
}

// NotifyPolicyItem 策略视图。
type NotifyPolicyItem struct {
	ID                     uint64                        `json:"id"`
	Name                   string                        `json:"name"`
	Enabled                bool                          `json:"enabled"`
	Priority               int                           `json:"priority"`
	MatchLevels            []string                      `json:"match_levels"`
	MatchDbTypes           []string                      `json:"match_db_types"`
	MatchBusiness          []string                      `json:"match_business"`
	MatchEnvs              []string                      `json:"match_envs"`
	MatchInstances         []string                      `json:"match_instances"`
	MatchLayers            []string                      `json:"match_layers"`
	MatchTemplateIDs       []uint64                      `json:"match_template_ids"`
	ChannelIDs             []uint64                      `json:"channel_ids"`
	Channels               []map[string]any              `json:"channels"`
	Subjects               []monitorModels.NotifySubject `json:"subjects"`
	SubjectSummary         []string                      `json:"subject_summary"`
	NotifyIntervalMinutes  int                           `json:"notify_interval_minutes"`
	AggregateWindowSeconds int                           `json:"aggregate_window_seconds"`
	EscalateAfterMinutes   int                           `json:"escalate_after_minutes"`
	EscalatePolicyID       uint64                        `json:"escalate_policy_id"`
	StopOnAck              bool                          `json:"stop_on_ack"`
	NotifyRecovery         bool                          `json:"notify_recovery"`
	MatchedInstances       int                           `json:"matched_instances"`
	CreatedAt              string                        `json:"created_at"`
	CreatedBy              string                        `json:"created_by"`
}

// ListNotifyPolicies 策略列表。
func ListNotifyPolicies() ([]NotifyPolicyItem, error) {
	if global.App.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	var rows []monitorModels.InsightMonitorNotifyPolicies
	if err := global.App.DB.Order("priority asc, id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	channelIndex := notifyChannelIndex()
	items := make([]NotifyPolicyItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, buildNotifyPolicyItem(row, channelIndex))
	}
	return items, nil
}

// SaveNotifyPolicy 新建或更新策略。
func SaveNotifyPolicy(form NotifyPolicyForm, operator string) (uint64, error) {
	if global.App.DB == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	name := strings.TrimSpace(form.Name)
	if name == "" {
		return 0, fmt.Errorf("策略名称不能为空")
	}
	if len(form.ChannelIDs) == 0 {
		return 0, fmt.Errorf("至少选择一个通知渠道")
	}
	if len(form.Subjects) == 0 && notifyPolicyNeedsSubjects(form.ChannelIDs) {
		// 群渠道没有"个人地址"概念（发到群即可），可以不配接收人；
		// 但只要选中了任意一个"只能发给个人"的渠道（邮件/短信/电话），就必须有人，
		// 否则会在投递期空投失败，连续失败还会把这个渠道自动熔断。
		return 0, fmt.Errorf("所选渠道中包含必须指定接收人的渠道（如邮件），请至少选择一个接收人（用户 / 用户组）")
	}

	// 渠道必须存在且已注册
	var channelCount int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyChannels{}).
		Where("id IN ?", form.ChannelIDs).Count(&channelCount).Error; err != nil {
		return 0, err
	}
	if int(channelCount) != len(uniqueUint64(form.ChannelIDs)) {
		return 0, fmt.Errorf("所选渠道中有实例已被删除，请刷新后重试")
	}
	if err := validateNotifySubjects(form.Subjects); err != nil {
		return 0, err
	}
	// 升级链必须在保存时就排除环路：运行期虽然有深度上限与 visited 兜底，
	// 但那只能保证"不刷屏"，配置是错的这件事没人告诉运维 —— 保存前拦下才有意义。
	if err := validateEscalationChain(form.ID, form.EscalatePolicyID); err != nil {
		return 0, err
	}

	levels := normalizeNotifyLevels(form.MatchLevels)
	// 统一通知间隔：一个 N 驱动两套底层机制 —— silence（新事件 N 分钟去重）
	// 与 repeat（持续未恢复每 N 分钟重发）同步落 N；N=0 即"只发一次"。
	// repeat 路径刻意绕过静默判断（见 dispatch 注释），两值相等时语义恰好收敛为
	// "每 N 分钟最多打扰一次"，不会互相压制。
	interval := form.NotifyIntervalMinutes
	if interval < 0 {
		interval = 0
	}
	priority := form.Priority
	if priority <= 0 {
		priority = 100
	}

	payload := map[string]any{
		"name":                     name,
		"enabled":                  decodeBoolDefault(form.Enabled, true),
		"priority":                 priority,
		"match_levels":             notifyEncodeJSON(levels),
		"match_db_types":           notifyEncodeJSON(form.MatchDbTypes),
		"match_business":           notifyEncodeJSON(form.MatchBusiness),
		"match_envs":               notifyEncodeJSON(form.MatchEnvs),
		"match_instances":          notifyEncodeJSON(form.MatchInstances),
		"match_layers":             notifyEncodeJSON(form.MatchLayers),
		"match_template_ids":       notifyEncodeJSON(form.MatchTemplateIDs),
		"channel_ids":              notifyEncodeJSON(form.ChannelIDs),
		"subjects":                 notifyEncodeJSON(form.Subjects),
		"silence_minutes":          interval,
		"repeat_minutes":           interval,
		"aggregate_window_seconds": form.AggregateWindowSeconds,
		"escalate_after_minutes":   form.EscalateAfterMinutes,
		"escalate_policy_id":       form.EscalatePolicyID,
		"stop_on_ack":              decodeBoolDefault(form.StopOnAck, true),
		"notify_recovery":          decodeBoolDefault(form.NotifyRecovery, true),
		"updated_at":               notifyNowLocal(),
	}

	if form.ID > 0 {
		var existing monitorModels.InsightMonitorNotifyPolicies
		if err := global.App.DB.Where("id = ?", form.ID).Take(&existing).Error; err != nil {
			return 0, fmt.Errorf("策略不存在")
		}
		// 存量策略可能早于 created_by 字段存在，编辑时顺手补记当前操作人（与渠道保存同口径）
		if strings.TrimSpace(existing.CreatedBy) == "" && strings.TrimSpace(operator) != "" {
			payload["created_by"] = operator
		}
		if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyPolicies{}).
			Where("id = ?", form.ID).Updates(payload).Error; err != nil {
			return 0, err
		}
		return form.ID, nil
	}

	row := monitorModels.InsightMonitorNotifyPolicies{
		Name:                   name,
		Enabled:                decodeBoolDefault(form.Enabled, true),
		Priority:               priority,
		MatchLevels:            toJSON(levels),
		MatchDbTypes:           toJSON(form.MatchDbTypes),
		MatchBusiness:          toJSON(form.MatchBusiness),
		MatchEnvs:              toJSON(form.MatchEnvs),
		MatchInstances:         toJSON(form.MatchInstances),
		MatchLayers:            toJSON(form.MatchLayers),
		MatchTemplateIDs:       toJSON(form.MatchTemplateIDs),
		ChannelIDs:             toJSON(form.ChannelIDs),
		Subjects:               toJSON(form.Subjects),
		SilenceMinutes:         interval,
		RepeatMinutes:          interval,
		AggregateWindowSeconds: form.AggregateWindowSeconds,
		EscalateAfterMinutes:   form.EscalateAfterMinutes,
		EscalatePolicyID:       form.EscalatePolicyID,
		StopOnAck:              decodeBoolDefault(form.StopOnAck, true),
		NotifyRecovery:         decodeBoolDefault(form.NotifyRecovery, true),
		CreatedBy:              operator,
	}
	if err := global.App.DB.Create(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

// DeleteNotifyPolicy 删除策略。
func DeleteNotifyPolicy(id uint64) error {
	if global.App.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}
	// 作为升级目标被引用时不允许删除，避免升级链断裂
	var referrers int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyPolicies{}).
		Where("escalate_policy_id = ?", id).Count(&referrers).Error; err == nil && referrers > 0 {
		return fmt.Errorf("该策略被 %d 条策略作为升级目标引用，请先解除引用", referrers)
	}
	result := global.App.DB.Where("id = ?", id).Delete(&monitorModels.InsightMonitorNotifyPolicies{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("策略不存在")
	}
	return nil
}

// PreviewNotifyPolicy 保存前预览：这条策略会覆盖多少实例、多少条告警规则。
//
// 目的：把"策略写歪"的代价前置。写歪的静默策略会让告警全部失声，
// 写歪的宽松策略会造成告警轰炸——两者在保存前都应该看得见。
func PreviewNotifyPolicy(form NotifyPolicyForm) (map[string]any, error) {
	if global.App.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	instances, rules, err := countNotifyPolicyMatches(form)
	if err != nil {
		return nil, err
	}
	subjects := len(form.Subjects)
	addresses := notifySubjectAddressCount(form.Subjects)
	return map[string]any{
		"matched_instances":       instances,
		"matched_rules":           rules,
		"subject_count":           subjects,
		"address_count":           addresses,
		"notify_interval_minutes": form.NotifyIntervalMinutes,
		"hint":                    previewHintText(instances, rules),
	}, nil
}

// ---------- 匹配引擎 ----------

// notifyMatchContext 一次告警的匹配上下文。
type notifyMatchContext struct {
	Level           string
	Severity        string
	DbType          string
	Business        string
	Environment     string
	InstanceID      string
	Layer           string
	AlertTemplateID uint64
}

// matchNotifyPolicies 找出所有命中的启用策略（按优先级排序）。
// buildPolicyTestEvent 构造一条样例告警事件。
//
// 内嵌的主键模型必须显式初始化：renderAlertPayload 会读 event.ID，
// 内嵌指针为空会直接 panic（真实事件由 DB 装载，不存在这个问题）。
func buildPolicyTestEvent(policyID uint64, stamp int64) monitorModels.InsightMonitorAlertEvents {
	now := notifyNowLocal()
	return monitorModels.InsightMonitorAlertEvents{
		Model:         &commonModels.Model{},
		InstanceID:    "policy-test",
		DBType:        "MySQL",
		Name:          "策略连通性测试",
		MetricKey:     "res.cpu",
		Level:         "P2",
		Status:        "active",
		Threshold:     90,
		ObservedValue: 96.5,
		Message:       "这是一条由「策略测试」生成的样例告警，用于验证该策略的渠道与接收人配置。",
		TriggeredAt:   now,
		FirstSeenAt:   &now,
		LastSeenAt:    &now,
		Occurrences:   3,
		DurationSec:   180,
		PeakValue:     96.5,
		Advice:        "样例告警，无需处置。",
		Fingerprint:   fmt.Sprintf("policy-test-%d-%d", policyID, stamp),
	}
}

// policyTestInstance 样例事件的实例上下文（不查库，保证测试只依赖策略本身）。
func policyTestInstance() notifyInstanceContext {
	return notifyInstanceContext{
		InstanceID:  "policy-test",
		Name:        "策略连通性测试",
		Target:      "127.0.0.1:3306",
		DbType:      "MySQL",
		Environment: "测试",
		Business:    "样例数据",
	}
}

// TestNotifyPolicy 用一条样例告警把整条策略链路跑通：策略 → 渠道 → 接收人解析 →
// 消息渲染 → 落投递任务，随后由 worker 在 15 秒内真实发送。
//
// 与「渠道测试」的分工：渠道测试只回答"这个 webhook 通不通"（固定文案）；
// 策略测试回答"这条策略到底有没有配到能发出去"——渠道是否启用、接收人有没有可用地址、
// 渲染是否正常，全部走生产同一条代码路径。
func TestNotifyPolicy(id uint64) (map[string]any, error) {
	if global.App.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	var policy monitorModels.InsightMonitorNotifyPolicies
	if err := global.App.DB.Where("id = ?", id).Take(&policy).Error; err != nil {
		return nil, fmt.Errorf("通知策略不存在")
	}
	if !policy.Enabled {
		return nil, fmt.Errorf("策略已停用，请先启用后再测试")
	}
	if len(decodeUint64List(policy.ChannelIDs)) == 0 {
		return nil, fmt.Errorf("策略未配置通知渠道")
	}

	stamp := time.Now().UnixNano()
	event := buildPolicyTestEvent(id, stamp)
	instance := policyTestInstance()

	noticeURL := ""
	if value, err := notifier.LoadNoticeURL(); err == nil {
		noticeURL = value
	}
	// dedupScope 让每次测试都能落新任务（样例事件的 ID 恒为 0，不加 scope 会被去重吃掉）
	created := dispatchPolicyTasks(event, instance, policy, "firing", "", noticeURL,
		fmt.Sprintf("policy-test|%d", stamp), nil)
	if created == 0 {
		return nil, fmt.Errorf("未生成任何投递任务：请检查渠道是否启用、「接收人 / 用户组」是否配置了可用地址")
	}
	global.App.Log.Infof("[monitor] 通知策略「%s」(%d) 连通性测试：已生成 %d 条投递任务",
		policy.Name, policy.ID, created)
	return map[string]any{
		"fingerprint": event.Fingerprint,
		"created":     created,
	}, nil
}

func matchNotifyPolicies(ctx notifyMatchContext) []monitorModels.InsightMonitorNotifyPolicies {
	if global.App.DB == nil {
		return nil
	}
	var rows []monitorModels.InsightMonitorNotifyPolicies
	if err := global.App.DB.Where("enabled = ?", true).
		Order("priority asc, id asc").Find(&rows).Error; err != nil {
		global.App.Log.Warnf("[monitor] 加载通知策略失败: %v", err)
		return nil
	}
	matched := make([]monitorModels.InsightMonitorNotifyPolicies, 0, len(rows))
	for _, row := range rows {
		if notifyPolicyMatches(row, ctx) {
			matched = append(matched, row)
		}
	}
	return matched
}

// notifyPolicyMatches 单条策略的匹配判定（纯函数，便于单测覆盖矩阵）。
func notifyPolicyMatches(policy monitorModels.InsightMonitorNotifyPolicies, ctx notifyMatchContext) bool {
	if !policy.Enabled {
		return false
	}
	if levels := normalizeNotifyLevels(decodeStringList(policy.MatchLevels)); len(levels) > 0 {
		// 两侧都归一化：策略列里可能是历史中文等级（高/中/低），事件侧一律是 P 码
		if !stringInList(normalizeNotifyLevel(ctx.Level), levels, true) {
			return false
		}
	}
	if types := decodeStringList(policy.MatchDbTypes); len(types) > 0 {
		if !stringInList(resolveEngine(ctx.DbType), types, true) {
			return false
		}
	}
	if businesses := decodeStringList(policy.MatchBusiness); len(businesses) > 0 {
		if !stringInList(ctx.Business, businesses, false) {
			return false
		}
	}
	if envs := decodeStringList(policy.MatchEnvs); len(envs) > 0 {
		if !stringInList(ctx.Environment, envs, false) {
			return false
		}
	}
	if instances := decodeStringList(policy.MatchInstances); len(instances) > 0 {
		if !stringInList(ctx.InstanceID, instances, false) {
			return false
		}
	}
	if layers := decodeStringList(policy.MatchLayers); len(layers) > 0 {
		if ctx.Layer == "" || !stringInList(ctx.Layer, layers, true) {
			// 层级判定不出来时按不匹配处理：宁可漏发也不误发到不该发的渠道
			return false
		}
	}
	if templates := decodeUint64List(policy.MatchTemplateIDs); len(templates) > 0 {
		if ctx.AlertTemplateID == 0 {
			return false
		}
		found := false
		for _, id := range templates {
			if id == ctx.AlertTemplateID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// countNotifyPolicyMatches 预览用：按条件统计命中的实例数与规则数。
func countNotifyPolicyMatches(form NotifyPolicyForm) (int, int, error) {
	query := global.App.DB.Table("insight_instances AS a").
		Joins("left join insight_instance_environments env on a.environment = env.id").
		Joins("left join insight_monitor_target_configs mt on mt.instance_id = a.instance_id").
		Where("LOWER(a.db_type) IN ?", monitorableEngineKeys())

	if types := form.MatchDbTypes; len(types) > 0 {
		normalized := make([]string, 0, len(types))
		for _, item := range types {
			normalized = append(normalized, resolveEngine(item))
		}
		query = query.Where("LOWER(a.db_type) IN ?", normalized)
	}
	if len(form.MatchBusiness) > 0 {
		query = query.Where("mt.business IN ?", form.MatchBusiness)
	}
	if len(form.MatchEnvs) > 0 {
		query = query.Where("env.name IN ?", form.MatchEnvs)
	}
	if len(form.MatchInstances) > 0 {
		query = query.Where("a.instance_id IN ?", form.MatchInstances)
	}

	var instances int64
	if err := query.Distinct("a.instance_id").Count(&instances).Error; err != nil {
		return 0, 0, err
	}

	// 规则数：命中实例所用告警模板里，等级（与层级）符合条件且启用的规则条数
	ruleQuery := global.App.DB.Table("insight_monitor_alert_rules AS r").
		Joins("left join insight_monitor_target_configs mt on mt.alert_template_id = r.template_id").
		Joins("left join insight_instances a on a.instance_id = mt.instance_id").
		Joins("left join insight_instance_environments env on a.environment = env.id").
		Where("r.enabled = ?", true)
	if levels := normalizeNotifyLevels(form.MatchLevels); len(levels) > 0 {
		ruleQuery = ruleQuery.Where("r.alert_level IN ?", levels)
	}
	if len(form.MatchDbTypes) > 0 {
		normalized := make([]string, 0, len(form.MatchDbTypes))
		for _, item := range form.MatchDbTypes {
			normalized = append(normalized, resolveEngine(item))
		}
		ruleQuery = ruleQuery.Where("LOWER(r.db_type) IN ?", normalized)
	}
	if len(form.MatchBusiness) > 0 {
		ruleQuery = ruleQuery.Where("mt.business IN ?", form.MatchBusiness)
	}
	if len(form.MatchEnvs) > 0 {
		ruleQuery = ruleQuery.Where("env.name IN ?", form.MatchEnvs)
	}
	if len(form.MatchInstances) > 0 {
		ruleQuery = ruleQuery.Where("a.instance_id IN ?", form.MatchInstances)
	}
	var rules int64
	if err := ruleQuery.Distinct("r.id").Count(&rules).Error; err != nil {
		return int(instances), 0, nil
	}
	return int(instances), int(rules), nil
}

// ---------- 内部 ----------

func buildNotifyPolicyItem(row monitorModels.InsightMonitorNotifyPolicies, channelIndex map[uint64]map[string]any) NotifyPolicyItem {
	channelIDs := decodeUint64List(row.ChannelIDs)
	channels := make([]map[string]any, 0, len(channelIDs))
	for _, id := range channelIDs {
		if channel, ok := channelIndex[id]; ok {
			channels = append(channels, channel)
			continue
		}
		// 渠道被删但策略还引用着：显式暴露，避免"看起来配了、实际不会发"
		channels = append(channels, map[string]any{
			"id":      id,
			"name":    "（渠道已删除）",
			"kind":    "",
			"enabled": false,
			"missing": true,
		})
	}
	subjects := decodeSubjects(row.Subjects)
	return NotifyPolicyItem{
		ID:               row.ID,
		Name:             row.Name,
		Enabled:          row.Enabled,
		Priority:         row.Priority,
		MatchLevels:      decodeStringList(row.MatchLevels),
		MatchDbTypes:     decodeStringList(row.MatchDbTypes),
		MatchBusiness:    decodeStringList(row.MatchBusiness),
		MatchEnvs:        decodeStringList(row.MatchEnvs),
		MatchInstances:   decodeStringList(row.MatchInstances),
		MatchLayers:      decodeStringList(row.MatchLayers),
		MatchTemplateIDs: decodeUint64List(row.MatchTemplateIDs),
		ChannelIDs:       channelIDs,
		Channels:         channels,
		Subjects:         subjects,
		SubjectSummary:   notifySubjectSummary(subjects),
		// 存量行映射：统一间隔取 repeat（repeat=0 的旧策略本就不重发，对应"仅发送一次"）
		NotifyIntervalMinutes:  row.RepeatMinutes,
		AggregateWindowSeconds: row.AggregateWindowSeconds,
		EscalateAfterMinutes:   row.EscalateAfterMinutes,
		EscalatePolicyID:       row.EscalatePolicyID,
		StopOnAck:              row.StopOnAck,
		NotifyRecovery:         row.NotifyRecovery,
		// 与渠道列表（NotifyChannelItem）同口径：本地时间字符串，前端直接展示
		CreatedAt: time.Time(row.CreatedAt).Format("2006-01-02 15:04:05"),
		CreatedBy: row.CreatedBy,
	}
}

func notifyChannelIndex() map[uint64]map[string]any {
	index := map[uint64]map[string]any{}
	if global.App.DB == nil {
		return index
	}
	var rows []monitorModels.InsightMonitorNotifyChannels
	if err := global.App.DB.Select("id, name, kind, enabled, fail_streak").Find(&rows).Error; err != nil {
		return index
	}
	for _, row := range rows {
		index[row.ID] = map[string]any{
			"id":          row.ID,
			"name":        row.Name,
			"kind":        row.Kind,
			"enabled":     row.Enabled,
			"fail_streak": row.FailStreak,
		}
	}
	return index
}

// validateNotifySubjects 接收人落库前校验。
//
// 为什么必须校验：不存在的用户 / 组织节点会被原样存进策略 JSON，
// 投递时解析出 0 个地址、任务只能以 skipped 收场，而配置界面看起来"配好了"——
// 这种假成功比直接报错难排查得多。
func validateNotifySubjects(subjects []monitorModels.NotifySubject) error {
	if len(subjects) == 0 || global.App.DB == nil {
		return nil
	}
	userIDs := make([]uint64, 0, len(subjects))
	orgKeys := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		switch strings.ToLower(strings.TrimSpace(subject.Type)) {
		case monitorModels.NotifySubjectUser:
			id, err := strconv.ParseUint(strings.TrimSpace(subject.ID), 10, 64)
			if err != nil || id == 0 {
				return fmt.Errorf("接收人「%s」的用户标识非法", subject.ID)
			}
			userIDs = append(userIDs, id)
		case monitorModels.NotifySubjectOrg:
			key := strings.TrimSpace(subject.ID)
			if key == "" {
				return fmt.Errorf("所选用户组缺少组织标识，请刷新后重选")
			}
			orgKeys = append(orgKeys, key)
		case monitorModels.NotifySubjectRaw:
			address := strings.TrimSpace(subject.ID)
			if !strings.Contains(address, "@") {
				return fmt.Errorf("外部地址「%s」不是合法邮箱", address)
			}
		case monitorModels.NotifySubjectGroup:
			return fmt.Errorf("通知组（值班组）为二期能力，当前版本请选择用户或用户组")
		default:
			return fmt.Errorf("不支持的接收人类型「%s」", subject.Type)
		}
	}
	if len(userIDs) > 0 {
		unique := uniqueUint64(userIDs)
		var found int64
		if err := global.App.DB.Table("insight_users").
			Where("uid IN ? AND is_deleted = ?", unique, false).
			Count(&found).Error; err != nil {
			return err
		}
		if int(found) != len(unique) {
			return fmt.Errorf("所选接收人中有账号已被删除，请刷新后重选")
		}
	}
	if len(orgKeys) > 0 {
		unique := uniqueStrings(orgKeys)
		var found int64
		if err := global.App.DB.Table("insight_orgs").
			Where("`key` IN ?", unique).Count(&found).Error; err != nil {
			return err
		}
		if int(found) != len(unique) {
			return fmt.Errorf("所选用户组中有组织节点已被删除，请刷新后重选")
		}
	}
	return nil
}

// notifyPolicyNeedsSubjects 判断所选渠道里是否存在"只能发给个人"的类型。
//
// 语义比"是否有群渠道"更准确：混选「钉钉群 + 邮件」且不配接收人时，
// 群消息发得出去、邮件发不出去，这种半成功比整体失败更难排查，因此直接在保存前拦下。
func notifyPolicyNeedsSubjects(channelIDs []uint64) bool {
	if len(channelIDs) == 0 || global.App.DB == nil {
		return false
	}
	var kinds []string
	if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyChannels{}).
		Where("id IN ?", channelIDs).Pluck("kind", &kinds).Error; err != nil {
		// 查询失败时不阻塞保存，交由投递阶段暴露
		return false
	}
	for _, kind := range kinds {
		if caps, ok := notifyChannelMetaSafe(kind); ok && caps.Personalizable {
			return true
		}
	}
	return false
}

func uniqueUint64(values []uint64) []uint64 {
	seen := map[uint64]struct{}{}
	result := make([]uint64, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func stringInList(value string, list []string, caseInsensitive bool) bool {
	target := strings.TrimSpace(value)
	for _, item := range list {
		if caseInsensitive {
			if strings.EqualFold(strings.TrimSpace(item), target) {
				return true
			}
			continue
		}
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}

// normalizeNotifyLevel 把级别归一化为 P 码；未知值原样返回（避免脏数据被误判为 P3）。
func normalizeNotifyLevel(level string) string {
	if normalized, ok := normalizeAlertLevel(level); ok {
		return normalized
	}
	return strings.TrimSpace(level)
}

// normalizeNotifyLevels 归一化等级列表；空输入返回 nil（= 不限）。
func normalizeNotifyLevels(levels []string) []string {
	if len(levels) == 0 {
		return nil
	}
	result := make([]string, 0, len(levels))
	seen := map[string]struct{}{}
	for _, level := range levels {
		normalized := normalizeNotifyLevel(level)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return notifyLevelRank(result[i]) < notifyLevelRank(result[j])
	})
	return result
}

func toJSON(value any) datatypes.JSON {
	encoded := notifyEncodeJSON(value)
	return encoded
}

func previewHintText(instances, rules int) string {
	switch {
	case instances == 0:
		return "当前没有任何实例命中该条件，保存后不会产生通知，请检查筛选条件"
	case rules == 0:
		return "命中了实例，但没有任何启用的告警规则符合条件，保存后可能收不到通知"
	default:
		return "条件合理：命中实例与告警规则均为正数"
	}
}
