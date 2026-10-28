package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"github.com/simajiong/jaysql/pkg/notifier"
	"gorm.io/datatypes"
)

// ============================================================================
// 通知渠道实例服务
//
// 职责：渠道实例的增删改查 + 配置加解密 + 脱敏回显 + 测试发送 + 健康度。
// 配置里的敏感字段（webhook 加签密钥、SMTP 密码）加密存储，接口只回 ******。
// ============================================================================

// MaskSentinel 密钥回显占位符。加解密与脱敏的唯一实现在 pkg/notifier
// （工单通知要读同一张渠道表，复制一份迟早会漂移），这里保留别名。
const MaskSentinel = notifier.MaskSentinel

// NotifyChannelForm 渠道实例的保存表单。
type NotifyChannelForm struct {
	ID                     uint64         `json:"id"`
	Name                   string         `json:"name" binding:"required"`
	Kind                   string         `json:"kind" binding:"required"`
	Enabled                *bool          `json:"enabled"`
	Config                 map[string]any `json:"config"`
	ScopeType              string         `json:"scope_type"`
	ScopeValue             string         `json:"scope_value"`
	TimeoutSeconds         int            `json:"timeout_seconds"`
	MaxAttempt             int            `json:"max_attempt"`
	RateLimitPerMin        int            `json:"rate_limit_per_min"`
	AggregateWindowSeconds int            `json:"aggregate_window_seconds"`
}

// NotifyChannelItem 渠道实例的返回结构（密钥已脱敏）。
type NotifyChannelItem struct {
	ID                     uint64         `json:"id"`
	Name                   string         `json:"name"`
	Kind                   string         `json:"kind"`
	KindLabel              string         `json:"kind_label"`
	Enabled                bool           `json:"enabled"`
	Config                 map[string]any `json:"config"`
	SecretConfigured       []string       `json:"secret_configured"`
	ScopeType              string         `json:"scope_type"`
	ScopeValue             string         `json:"scope_value"`
	TimeoutSeconds         int            `json:"timeout_seconds"`
	MaxAttempt             int            `json:"max_attempt"`
	RateLimitPerMin        int            `json:"rate_limit_per_min"`
	AggregateWindowSeconds int            `json:"aggregate_window_seconds"`
	LastTestAt             string         `json:"last_test_at"`
	LastTestOK             bool           `json:"last_test_ok"`
	LastTestMessage        string         `json:"last_test_message"`
	FailStreak             int            `json:"fail_streak"`
	LastError              string         `json:"last_error"`
	Capabilities           any            `json:"capabilities"`
	// PolicyRefs 引用该渠道的策略名列表：删除前提示用
	PolicyRefs []string `json:"policy_refs"`
	CreatedAt  string   `json:"created_at"`
	CreatedBy  string   `json:"created_by"`
}

// ListNotifyChannels 渠道实例列表（配置已脱敏）。
func ListNotifyChannels() ([]NotifyChannelItem, error) {
	if global.App.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	var rows []monitorModels.InsightMonitorNotifyChannels
	if err := global.App.DB.Order("kind asc, id asc").Find(&rows).Error; err != nil {
		return nil, err
	}

	// 引用关系：删除保护与界面提示都要用
	refs := notifyChannelPolicyRefs()

	items := make([]NotifyChannelItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, buildNotifyChannelItem(row, refs[row.ID]))
	}
	return items, nil
}

// NotifyChannelKinds 渠道类型元数据（前端动态渲染表单）。
func NotifyChannelKinds() []notifier.Meta {
	return notifier.AllMetas()
}

// SaveNotifyChannel 新建或更新渠道实例。返回实例 ID。
func SaveNotifyChannel(form NotifyChannelForm, operator string) (uint64, error) {
	if global.App.DB == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	kind := strings.ToLower(strings.TrimSpace(form.Kind))
	channel, ok := notifier.Get(kind)
	if !ok {
		return 0, fmt.Errorf("不支持的渠道类型 %s", form.Kind)
	}
	name := strings.TrimSpace(form.Name)
	if name == "" {
		return 0, fmt.Errorf("渠道名称不能为空")
	}

	cfg := form.Config
	if cfg == nil {
		cfg = map[string]any{}
	}

	var existing *monitorModels.InsightMonitorNotifyChannels
	if form.ID > 0 {
		var row monitorModels.InsightMonitorNotifyChannels
		if err := global.App.DB.Where("id = ?", form.ID).Take(&row).Error; err != nil {
			return 0, fmt.Errorf("渠道实例不存在")
		}
		existing = &row
	}

	// 密钥字段：提交 ****** 或空值表示"保持原值不变"，避免前端拿不到明文就把它清空。
	var existingRaw []byte
	if existing != nil {
		existingRaw = existing.Config
	}
	merged, err := notifier.MergeChannelSecrets(kind, cfg, existingRaw)
	if err != nil {
		return 0, err
	}
	// 校验：用合并后的配置（含继承的默认凭据）判断必填与连通性格式
	if err := channel.Validate(withChannelDefaults(kind, merged, false)); err != nil {
		return 0, fmt.Errorf("渠道配置校验失败: %w", err)
	}

	encoded, err := notifier.EncodeChannelConfig(kind, merged)
	if err != nil {
		return 0, err
	}
	secretFields := notifier.SecretFieldsOf(kind)
	secretJSON, _ := json.Marshal(secretFields)

	enabled := true
	if form.Enabled != nil {
		enabled = *form.Enabled
	}
	timeout := form.TimeoutSeconds
	if timeout <= 0 {
		timeout = 8
	}
	maxAttempt := form.MaxAttempt
	if maxAttempt <= 0 {
		maxAttempt = 4
	}

	// Model 必须显式初始化：ID 是嵌入指针的提升字段，`payload.ID = existing.ID`
	// 等价于 payload.Model.ID = ... —— Model 为 nil 时直接空指针 panic（编辑渠道必现）。
	payload := monitorModels.InsightMonitorNotifyChannels{
		Model:                  &commonModels.Model{},
		Name:                   name,
		Kind:                   kind,
		Enabled:                enabled,
		Config:                 datatypes.JSON(encoded),
		SecretFields:           datatypes.JSON(secretJSON),
		ScopeType:              defaultString(strings.TrimSpace(form.ScopeType), "global"),
		ScopeValue:             strings.TrimSpace(form.ScopeValue),
		TimeoutSeconds:         timeout,
		MaxAttempt:             maxAttempt,
		RateLimitPerMin:        form.RateLimitPerMin,
		AggregateWindowSeconds: form.AggregateWindowSeconds,
	}

	if existing != nil {
		payload.ID = existing.ID
		updates := map[string]any{
			"name":                     payload.Name,
			"kind":                     payload.Kind,
			"enabled":                  payload.Enabled,
			"config":                   payload.Config,
			"secret_fields":            payload.SecretFields,
			"scope_type":               payload.ScopeType,
			"scope_value":              payload.ScopeValue,
			"timeout_seconds":          payload.TimeoutSeconds,
			"max_attempt":              payload.MaxAttempt,
			"rate_limit_per_min":       payload.RateLimitPerMin,
			"aggregate_window_seconds": payload.AggregateWindowSeconds,
			// 配置变更后重置熔断计数，否则改对了也发不出去
			"fail_streak": 0,
			"last_error":  "",
			"updated_at":  time.Now(),
		}
		// 字段上线前的旧数据 created_by 为空：编辑时补记当前操作人，
		// 让列表「创建人」列不再显示 —（不覆盖已有创建人）
		if strings.TrimSpace(existing.CreatedBy) == "" && strings.TrimSpace(operator) != "" {
			updates["created_by"] = operator
		}
		if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyChannels{}).
			Where("id = ?", existing.ID).
			Updates(updates).Error; err != nil {
			return 0, err
		}
		// 配置改完了要通知出去：钉钉企业应用的 Stream 长连接是用旧凭据建起来的，
		// 不重连就一直连不上（换了 AppKey 尤其明显），表现为"改完了还是收不到回调"。
		notifier.NotifyChannelChanged(kind)
		return existing.ID, nil
	}

	payload.CreatedBy = operator
	if err := global.App.DB.Create(&payload).Error; err != nil {
		return 0, err
	}
	notifier.NotifyChannelChanged(kind)
	return payload.ID, nil
}

// DeleteNotifyChannel 删除渠道实例。被策略引用时拒绝删除，避免静默失效。
func DeleteNotifyChannel(id uint64) error {
	if global.App.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}
	refs := notifyChannelPolicyRefs()
	if names, ok := refs[id]; ok && len(names) > 0 {
		return fmt.Errorf("该渠道正被通知策略「%s」引用，请先在策略中移除后再删除", strings.Join(names, "、"))
	}
	// 先取类型：删除后要通知出去（钉钉企业应用的 Stream 长连接得停掉重来，
	// 否则会抱着一个已被删除的渠道实例继续收回调）。
	var row monitorModels.InsightMonitorNotifyChannels
	if err := global.App.DB.Where("id = ?", id).Take(&row).Error; err != nil {
		return fmt.Errorf("渠道实例不存在")
	}
	result := global.App.DB.Where("id = ?", id).Delete(&monitorModels.InsightMonitorNotifyChannels{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("渠道实例不存在")
	}
	notifier.NotifyChannelChanged(row.Kind)
	return nil
}

// TestNotifyChannel 立即测试：发一条样例消息，并把结果写回渠道健康度。
func TestNotifyChannel(id uint64) (notifier.Result, error) {
	if global.App.DB == nil {
		return notifier.Result{}, fmt.Errorf("数据库未初始化")
	}
	row, cfg, err := loadNotifyChannelRuntime(id)
	if err != nil {
		return notifier.Result{}, err
	}
	channel, ok := notifier.Get(row.Kind)
	if !ok {
		return notifier.Result{}, fmt.Errorf("渠道类型 %s 未实现", row.Kind)
	}

	msg := notifier.Message{
		Title:     "JaySQL 通知渠道测试",
		Markdown:  "**JaySQL 通知渠道测试**\n\n这是一条测试消息，收到即代表该渠道配置可用。",
		PlainText: "JaySQL 通知渠道测试：收到即代表该渠道配置可用。",
		Level:     AlertLevelP3,
		Meta:      map[string]string{"test": "1"},
	}
	// 群渠道没有个人地址；邮件渠道用发件账号给自己发一封，验证 SMTP 全链路
	if channel.Capabilities().Personalizable {
		username := notifyCfgString(cfg, "username")
		if username == "" {
			return notifier.Result{}, fmt.Errorf("测试邮件渠道需要填写发件账号，或先配置平台默认通知凭据")
		}
		msg.Recipients = []notifier.Recipient{{Address: username, DisplayName: "渠道测试"}}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(maxInt(row.TimeoutSeconds, 8))*time.Second)
	defer cancel()
	result, sendErr := channel.Send(ctx, cfg, msg)

	updates := map[string]any{
		"last_test_at": commonModels.LocalTime(time.Now()),
		"last_test_ok": sendErr == nil,
	}
	if sendErr != nil {
		updates["last_test_message"] = truncateString(sendErr.Error(), 500)
	} else {
		updates["last_test_message"] = "测试成功"
		updates["fail_streak"] = 0
		updates["last_error"] = ""
	}
	if err := global.App.DB.Model(&monitorModels.InsightMonitorNotifyChannels{}).
		Where("id = ?", id).Updates(updates).Error; err != nil {
		global.App.Log.Warnf("[monitor] 更新渠道测试结果失败: %v", err)
	}
	return result, sendErr
}

// ---------- 内部：加载与编解码 ----------

// loadNotifyChannelRuntime 读取渠道实例并解密配置（供测试发送与投递 worker 使用）。
func loadNotifyChannelRuntime(id uint64) (monitorModels.InsightMonitorNotifyChannels, map[string]any, error) {
	var row monitorModels.InsightMonitorNotifyChannels
	if err := global.App.DB.Where("id = ?", id).Take(&row).Error; err != nil {
		return row, nil, fmt.Errorf("渠道实例不存在")
	}
	cfg, err := notifier.DecodeChannelConfig(row.Config)
	if err != nil {
		return row, nil, fmt.Errorf("渠道配置解析失败: %w", err)
	}
	return row, withChannelDefaults(row.Kind, cfg, true), nil
}

// withChannelDefaults 为邮件渠道补平台默认凭据。
// decrypt=false 用于保存前校验（此时密码字段还是明文或掩码，不需要解密）。
func withChannelDefaults(kind string, cfg map[string]any, _ bool) map[string]any {
	if kind != monitorModels.NotifyKindEmail {
		return cfg
	}
	defaults, err := notifier.LoadRuntimeNotifyConfig()
	if err != nil || defaults == nil {
		return cfg
	}
	merged := map[string]any{}
	for key, value := range cfg {
		merged[key] = value
	}
	if notifyCfgString(merged, "host") == "" {
		merged["host"] = defaults.Mail.Host
	}
	if notifyCfgString(merged, "port") == "" {
		merged["port"] = defaults.Mail.Port
	}
	if notifyCfgString(merged, "username") == "" {
		merged["username"] = defaults.Mail.Username
	}
	if notifyCfgString(merged, "password") == "" {
		merged["password"] = defaults.Mail.Password
	}
	return merged
}

// buildNotifyChannelItem 组装脱敏后的渠道视图。
func buildNotifyChannelItem(row monitorModels.InsightMonitorNotifyChannels, policyRefs []string) NotifyChannelItem {
	cfg, err := notifier.DecodeChannelConfig(row.Config)
	if err != nil {
		cfg = map[string]any{}
		global.App.Log.Warnf("[monitor] 渠道 %d 配置解密失败: %v", row.ID, err)
	}
	cfg, secretConfigured := notifier.MaskChannelConfig(row.Kind, cfg)
	lastTestAt := ""
	if row.LastTestAt != nil {
		lastTestAt = time.Time(*row.LastTestAt).Format("2006-01-02 15:04:05")
	}
	meta := notifier.MetaOf(row.Kind)
	return NotifyChannelItem{
		ID:                     row.ID,
		Name:                   row.Name,
		Kind:                   row.Kind,
		KindLabel:              meta.Label,
		Enabled:                row.Enabled,
		Config:                 cfg,
		SecretConfigured:       secretConfigured,
		ScopeType:              row.ScopeType,
		ScopeValue:             row.ScopeValue,
		TimeoutSeconds:         row.TimeoutSeconds,
		MaxAttempt:             row.MaxAttempt,
		RateLimitPerMin:        row.RateLimitPerMin,
		AggregateWindowSeconds: row.AggregateWindowSeconds,
		LastTestAt:             lastTestAt,
		LastTestOK:             row.LastTestOK,
		LastTestMessage:        row.LastTestMessage,
		FailStreak:             row.FailStreak,
		LastError:              row.LastError,
		Capabilities:           meta.Capabilities,
		PolicyRefs:             policyRefs,
		CreatedAt:              time.Time(row.CreatedAt).Format("2006-01-02 15:04:05"),
		CreatedBy:              row.CreatedBy,
	}
}

// notifyChannelPolicyRefs 汇总"渠道 → 引用它的策略名"，用于删除保护与界面提示。
func notifyChannelPolicyRefs() map[uint64][]string {
	refs := map[uint64][]string{}
	if global.App.DB == nil {
		return refs
	}
	var policies []monitorModels.InsightMonitorNotifyPolicies
	if err := global.App.DB.Select("id, name, channel_ids").Find(&policies).Error; err != nil {
		return refs
	}
	for _, policy := range policies {
		for _, channelID := range decodeUint64List(policy.ChannelIDs) {
			refs[channelID] = append(refs[channelID], policy.Name)
		}
	}
	return refs
}

// ListNotifyChannelsRaw 供策略编辑器选择渠道用（只给必要字段）。
func ListNotifyChannelsRaw() ([]map[string]any, error) {
	if global.App.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	var rows []monitorModels.InsightMonitorNotifyChannels
	if err := global.App.DB.Order("kind asc, id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		meta := notifier.MetaOf(row.Kind)
		items = append(items, map[string]any{
			"id":           row.ID,
			"name":         row.Name,
			"kind":         row.Kind,
			"kind_label":   meta.Label,
			"enabled":      row.Enabled,
			"fail_streak":  row.FailStreak,
			"personalized": meta.Capabilities.Personalizable,
		})
	}
	return items, nil
}
