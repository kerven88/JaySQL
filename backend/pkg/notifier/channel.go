package notifier

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/simajiong/jaysql/internal/global"
)

// ============================================================================
// 渠道抽象层
//
// 与既有 notifier.go 的关系：
//   - 旧接口 Notifier.SendMessage(subject, users, msg) 及其实现（企微/钉钉/邮件）保留不动，
//     工单、备份模块继续用，零改动。
//   - 本文件新增 Channel 接口 + 注册表，供监控告警使用：支持多实例、超时重试、
//     业务错误码校验、能力矩阵（用于按渠道能力做接收人降级）。
//
// 新增渠道（短信、电话）只需实现 Channel 并调用 Register 注册，
// 派发决策 / 投递队列 / worker / 投递记录四条链路不需要任何改动。
// ============================================================================

// Recipient 投递目标。Address 的形态由渠道决定：
// 邮箱 / 手机号 / open_id / userid / 群标识。
type Recipient struct {
	Address     string `json:"address"`
	DisplayName string `json:"display_name,omitempty"`
	UserID      uint64 `json:"user_id,omitempty"`
}

// Mentions 群消息的 @ 名单。不同渠道吃不同字段：
// 钉钉按手机号，飞书按 open_id，企业微信按 userid/手机号。
type Mentions struct {
	Mobiles    []string `json:"mobiles,omitempty"`
	OpenIDs    []string `json:"open_ids,omitempty"`
	UserIDs    []string `json:"user_ids,omitempty"`
	Names      []string `json:"names,omitempty"`
	MentionAll bool     `json:"mention_all,omitempty"`
}

// Message 与渠道无关的统一消息结构。
type Message struct {
	Title      string            `json:"title"`
	Markdown   string            `json:"markdown"`
	PlainText  string            `json:"plain_text"`
	Level      string            `json:"level"`
	URL        string            `json:"url,omitempty"`
	Recipients []Recipient       `json:"recipients,omitempty"`
	Mentions   Mentions          `json:"mentions,omitempty"`
	Meta       map[string]string `json:"meta,omitempty"`
}

// Capabilities 渠道能力矩阵：派发层据此做接收人寻址与降级决策。
type Capabilities struct {
	// SupportsMarkdown 是否支持富文本（短信/电话不支持，需退化为纯文本）
	SupportsMarkdown bool `json:"supports_markdown"`
	// SupportsMention 是否支持 @ 指定人
	SupportsMention bool `json:"supports_mention"`
	// SupportsBatch 一次请求能否发给多个接收人（邮件可以，短信通常也可以）
	SupportsBatch bool `json:"supports_batch"`
	// Personalizable 能否发给个人（群机器人=false：它只能发到群，人由群内 @ 触达）
	Personalizable bool `json:"personalizable"`
	// RequiresBinding 是否需要接收人预先绑定该渠道身份（飞书 open_id / 钉钉 userid）
	RequiresBinding bool `json:"requires_binding"`
	// MaxLength 正文长度上限（0=不限）。短信 70 字、电话播报稿有上限。
	MaxLength int `json:"max_length"`
	// Costly 是否产生直接费用（短信/电话），策略界面需给出成本提示
	Costly bool `json:"costly"`
}

// Result 一次发送的结果。
type Result struct {
	Accepted int      `json:"accepted"`
	Rejected []string `json:"rejected,omitempty"`
	Code     string   `json:"code,omitempty"`
	Raw      string   `json:"raw,omitempty"`
}

// Channel 渠道适配器接口。
//
// 关键设计：无状态 + 配置按调用传入（Send 的 cfg 参数）。
// 旧实现把配置存在结构体字段里，多个实例并存时会互相串配置；
// 无状态实现天然支持并发、支持同类型多实例、也让单测不必构造全局状态。
type Channel interface {
	Kind() string
	Capabilities() Capabilities
	// Validate 保存渠道实例前的校验：必填项 + 连通性自检（不发真实告警）
	Validate(cfg map[string]any) error
	// Send 发送消息。cfg 为渠道实例配置（敏感字段已解密）。
	Send(ctx context.Context, cfg map[string]any, msg Message) (Result, error)
}

// ConfigField 渠道的配置字段元数据：前端「新建渠道」表单由它动态渲染，
// 新增渠道时前端不用改代码。
type ConfigField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // string / password / number / bool / select / textarea
	Required    bool     `json:"required"`
	Secret      bool     `json:"secret"`
	Placeholder string   `json:"placeholder,omitempty"`
	Default     any      `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"`
	Help        string   `json:"help,omitempty"`
}

// Meta 渠道类型的元数据（供前端渲染表单与说明）。
type Meta struct {
	Kind         string        `json:"kind"`
	Label        string        `json:"label"`
	Description  string        `json:"description"`
	Capabilities Capabilities  `json:"capabilities"`
	Fields       []ConfigField `json:"fields"`
}

// 内置渠道的静态元数据（与适配器一一对应）。
var channelMetas = map[string]Meta{
	"dingtalk": {
		Kind:  "dingtalk",
		Label: "钉钉群机器人",
		Fields: []ConfigField{
			{Key: "webhook", Label: "Webhook 地址", Type: "string", Required: true, Placeholder: "https://oapi.dingtalk.com/robot/send?access_token=..."},
			{Key: "secret", Label: "加签密钥", Type: "password", Secret: true, Help: "机器人安全设置选择「加签」时填写，留空表示未启用加签"},
			{Key: "keywords", Label: "自定义关键字", Type: "string", Help: "机器人开启「自定义关键词」安全设置时填写；仅用于通过校验，不会显示在消息里"},
			{Key: "at_all", Label: "@所有人", Type: "bool", Default: false},
		},
	},
	"feishu": {
		Kind:        "feishu",
		Label:       "飞书群机器人",
		Description: "",
		Fields: []ConfigField{
			{Key: "webhook", Label: "Webhook 地址", Type: "string", Required: true, Placeholder: "https://open.feishu.cn/open-apis/bot/v2/hook/..."},
			{Key: "keywords", Label: "自定义关键词", Type: "string", Help: "若机器人开启「自定义关键词」安全设置，飞书会把关键词追加到消息末尾（可见）以通过校验；建议改用「签名校验」避免噪音。钉钉仍可通过 Markdown 隐藏校验"},
			{Key: "secret", Label: "签名密钥", Type: "password", Secret: true, Help: "机器人开启「签名校验」时填写"},
			{Key: "at_all", Label: "@所有人", Type: "bool", Default: false},
		},
	},
	"email": {
		Kind:        "email",
		Label:       "邮件",
		Description: "通过 SMTP 发送给个人邮箱；未填写 SMTP 时继承平台默认通知凭据",
		Fields: []ConfigField{
			{Key: "username", Label: "发件账号", Type: "string", Placeholder: "留空则用平台默认"},
			{Key: "password", Label: "发件密码/授权码", Type: "password", Secret: true, Placeholder: "留空则用平台默认"},
			{Key: "host", Label: "SMTP 服务器", Type: "string", Placeholder: "留空则用平台默认"},
			{Key: "port", Label: "SMTP 端口", Type: "number", Default: 465, Placeholder: "465 / 587"},
			{Key: "from", Label: "发件人显示名", Type: "string", Placeholder: "如：JaySQL 告警"},
			{Key: "subject_prefix", Label: "主题前缀", Type: "string", Placeholder: "如：[数据库告警]"},
		},
	},
	"wechat": {
		Kind:        "wechat",
		Label:       "企业微信群机器人",
		Description: "通过企业微信 Webhook 发到群（markdown），支持 @ 成员",
		Fields: []ConfigField{
			{Key: "webhook", Label: "Webhook 地址", Type: "string", Required: true, Placeholder: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..."},
			{Key: "at_all", Label: "@所有人", Type: "bool", Default: false},
		},
	},
}

var (
	channelRegistryMu sync.RWMutex
	channelRegistry   = map[string]Channel{}
)

// Register 注册渠道实现。同名重复注册会覆盖（便于测试替换）。
func Register(c Channel) {
	if c == nil {
		return
	}
	channelRegistryMu.Lock()
	defer channelRegistryMu.Unlock()
	channelRegistry[c.Kind()] = c
}

// Get 按类型取渠道实现。
func Get(kind string) (Channel, bool) {
	channelRegistryMu.RLock()
	defer channelRegistryMu.RUnlock()
	c, ok := channelRegistry[strings.ToLower(strings.TrimSpace(kind))]
	return c, ok
}

// Kinds 列出已注册的渠道类型（排序后，便于前端稳定展示）。
func Kinds() []string {
	channelRegistryMu.RLock()
	defer channelRegistryMu.RUnlock()
	kinds := make([]string, 0, len(channelRegistry))
	for kind := range channelRegistry {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// ============================================================================
// 渠道配置变更通知
//
// 有些渠道的凭据在启动时被"固化"进了长连接（例如钉钉企业应用：Stream 长连接
// 用 AppKey/AppSecret 换 ticket 建连，凭据一改旧连接就永久收不到回调）。
// 这类渠道需要在配置保存/删除后重连，否则必须重启服务才生效。
//
// 但"渠道配置存在哪张表、怎么读"属于 internal 层，pkg 不能反向依赖它；
// 于是这里只提供注册口子，由真正需要重连的模块（internal/orders/services）
// 在 init 时挂上自己的处理逻辑，保存渠道的模块负责 Notify 一声。
// 这样 monitor 与 orders 之间也不需要互相 import。
// ============================================================================

var (
	channelChangeMu    sync.RWMutex
	channelChangeHooks []func(kind string)
)

// OnChannelChanged 注册渠道配置变更回调。kind 为渠道类型（已小写）。
func OnChannelChanged(fn func(kind string)) {
	if fn == nil {
		return
	}
	channelChangeMu.Lock()
	defer channelChangeMu.Unlock()
	channelChangeHooks = append(channelChangeHooks, fn)
}

// NotifyChannelChanged 通知各回调「某类型渠道的配置已变更」。
// 回调内部出问题不得影响保存渠道这件事本身，故逐个 recover。
func NotifyChannelChanged(kind string) {
	normalized := strings.ToLower(strings.TrimSpace(kind))
	if normalized == "" {
		return
	}
	channelChangeMu.RLock()
	hooks := make([]func(kind string), len(channelChangeHooks))
	copy(hooks, channelChangeHooks)
	channelChangeMu.RUnlock()

	for _, hook := range hooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// 回调失败只记录，不影响渠道保存结果
					if global.App != nil && global.App.Log != nil {
						global.App.Log.Warnf("[notify] 渠道变更回调 panic kind=%s: %v", normalized, r)
					}
				}
			}()
			hook(normalized)
		}()
	}
}

// MetaOf 返回渠道类型元数据；未登记元数据的自定义渠道退化为"仅类型名"。
func MetaOf(kind string) Meta {
	normalized := strings.ToLower(strings.TrimSpace(kind))
	if meta, ok := channelMetas[normalized]; ok {
		if c, ok := Get(normalized); ok {
			meta.Capabilities = c.Capabilities()
		}
		return meta
	}
	meta := Meta{Kind: normalized, Label: normalized, Description: "自定义渠道"}
	if c, ok := Get(normalized); ok {
		meta.Capabilities = c.Capabilities()
	}
	return meta
}

// AllMetas 返回所有已注册渠道的元数据（前端「新建渠道」下拉的数据源）。
func AllMetas() []Meta {
	kinds := Kinds()
	metas := make([]Meta, 0, len(kinds))
	for _, kind := range kinds {
		metas = append(metas, MetaOf(kind))
	}
	return metas
}

// SecretFieldsOf 返回某渠道类型需脱敏的配置键（保存与回显时使用）。
func SecretFieldsOf(kind string) []string {
	meta, ok := channelMetas[strings.ToLower(strings.TrimSpace(kind))]
	if !ok {
		return nil
	}
	fields := make([]string, 0, 2)
	for _, field := range meta.Fields {
		if field.Secret {
			fields = append(fields, field.Key)
		}
	}
	return fields
}

// String 便于日志输出。
func (r Result) String() string {
	return fmt.Sprintf("accepted=%d code=%s", r.Accepted, r.Code)
}

// truncate 按渠道长度上限截断正文（短信等场景），按 rune 计数避免切坏中文。
func truncate(text string, max int) string {
	if max <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max])
}
