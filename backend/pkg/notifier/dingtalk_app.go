package notifier

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/dingtalk"
)

// ============================================================================
// 钉钉企业内部应用渠道
//
// 与已有的 "dingtalk"（群机器人）渠道的关键区别：
//   - 群机器人 Personalizable=false —— 只能发到群，人靠群内 @ 触达，
//     因此「只通知某个审批人」在群机器人上做不到（全群都能看到）。
//   - 企业内部应用用 AppKey/AppSecret 换 accessToken，可按 userid 定向投递给个人，
//     且支持互动卡片（按钮回调），这才是在钉钉里完成「通过/转交」的前提。
//
// 本适配器负责通用通知（监控告警、工单状态变更等）；
// 工单「审批卡片」因为需要追踪 outTrackId 与一次性令牌，由 orders 服务层
// 直接调用 pkg/dingtalk 完成，两者共用同一份渠道实例配置。
// ============================================================================

// DingTalkAppChannel 钉钉企业内部应用渠道。
type DingTalkAppChannel struct{}

// DingTalkAppKind 渠道类型标识。
const DingTalkAppKind = "dingtalk_app"

func (d *DingTalkAppChannel) Kind() string { return DingTalkAppKind }

func (d *DingTalkAppChannel) Capabilities() Capabilities {
	return Capabilities{
		SupportsMarkdown: true,
		// 个人定向投递，不需要 @ 语法
		SupportsMention: false,
		SupportsBatch:   true,
		// 关键：能发给个人（群机器人为 false）
		Personalizable: true,
		// 需要接收人预先绑定钉钉 userid（可由手机号自动匹配得到）
		RequiresBinding: true,
	}
}

func (d *DingTalkAppChannel) Validate(cfg map[string]any) error {
	if cfgString(cfg, "app_key") == "" {
		return fmt.Errorf("钉钉 AppKey（ClientId）不能为空")
	}
	if cfgString(cfg, "app_secret") == "" {
		return fmt.Errorf("钉钉 AppSecret 不能为空")
	}
	// 卡片模板是可选项：填了就走互动卡片，没填就退化为工作通知文本。
	// 这里不做「必须有模板」的强校验，否则首次接入时无法先跑通通知链路。
	return nil
}

func (d *DingTalkAppChannel) Send(ctx context.Context, cfg map[string]any, msg Message) (Result, error) {
	client := dingtalk.NewClient(dingtalkConfigFrom(cfg))

	userIDs := make([]string, 0, len(msg.Recipients))
	for _, recipient := range msg.Recipients {
		if id := strings.TrimSpace(recipient.Address); id != "" {
			userIDs = append(userIDs, id)
		}
	}
	if len(userIDs) == 0 {
		return Result{}, fmt.Errorf("钉钉企业应用渠道需要接收人的钉钉账号（userid），未指定无法投递")
	}

	title := firstNonEmpty(msg.Title, "JaySQL 通知")
	body := msg.Markdown
	if body == "" {
		body = msg.PlainText
	}

	// 配了卡片模板 ⇒ 优先用互动卡片（支持按钮回调，也是工单审批的主路径）
	if client.Config().CardTemplateID != "" {
		accepted := 0
		rejected := make([]string, 0)
		params := map[string]string{
			"title":   title,
			"content": body,
			"status":  "",
			"link":    msg.URL,
		}
		for _, userID := range userIDs {
			outTrackID := fmt.Sprintf("gi-notify-%s", strings.ReplaceAll(uuid.NewString(), "-", ""))
			if _, err := client.DeliverCardToUser(ctx, dingtalk.CardDeliverRequest{
				OutTrackID: outTrackID,
				UserID:     userID,
				Params:     params,
			}); err != nil {
				rejected = append(rejected, userID)
				global.App.Log.Warnf("[notify] 钉钉卡片投放失败 user=%s: %v", maskID(userID), err)
				continue
			}
			accepted++
		}
		if accepted == 0 {
			return Result{Rejected: rejected}, fmt.Errorf("钉钉卡片全部投放失败")
		}
		return Result{Accepted: accepted, Rejected: rejected, Code: "card_ok"}, nil
	}

	// 未配模板 ⇒ 降级为工作通知（仍能定向到个人，只是没有可点按钮）
	if err := client.SendWorkNotice(ctx, userIDs, title, body, msg.URL); err != nil {
		return Result{}, err
	}
	return Result{Accepted: len(userIDs), Code: "work_notice_ok"}, nil
}

// dingtalkConfigFrom 把渠道实例配置映射成钉钉客户端配置。
func dingtalkConfigFrom(cfg map[string]any) dingtalk.ClientConfig {
	timeout := 8 * time.Second
	if seconds := cfgInt(cfg, "timeout_seconds", 0); seconds > 0 {
		timeout = time.Duration(seconds) * time.Second
	}
	return dingtalk.ClientConfig{
		AppKey:         cfgString(cfg, "app_key"),
		AppSecret:      cfgString(cfg, "app_secret"),
		AgentID:        cfgString(cfg, "agent_id"),
		RobotCode:      cfgString(cfg, "robot_code"),
		CardTemplateID: cfgString(cfg, "card_template_id"),
		Timeout:        timeout,
	}
}

// DingTalkAppClientFromConfig 供工单模块复用：用同一份渠道配置构造钉钉客户端。
func DingTalkAppClientFromConfig(cfg map[string]any) *dingtalk.Client {
	return dingtalk.NewClient(dingtalkConfigFrom(cfg))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// maskID 日志里对 userid 做掩码，避免把员工标识写进日志。
func maskID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 6 {
		return "***"
	}
	return value[:3] + "***" + value[len(value)-3:]
}

func init() {
	Register(&DingTalkAppChannel{})

	channelMetas[DingTalkAppKind] = Meta{
		Kind:  DingTalkAppKind,
		Label: "钉钉企业应用",
		Description: "通过企业内部应用的 AppKey/AppSecret 使用 accessToken 调用钉钉接口，" +
			"可把消息或互动卡片定向发送到指定员工的钉钉。支持卡片按钮回调（Stream 长连接），" +
			"因此可在钉钉内直接完成工单审批。需要企业管理员在钉钉开放平台创建企业内部应用并开通相应权限。",
		Fields: []ConfigField{
			{Key: "app_key", Label: "AppKey（ClientId）", Type: "string", Required: true,
				Placeholder: "dingxxxxxxxxxxxx", Help: "钉钉开放平台 → 应用开发 → 企业内部应用 → 凭证与基础信息"},
			{Key: "app_secret", Label: "AppSecret", Type: "password", Secret: true, Required: true,
				Help: "与 AppKey 同页；入库前加密存储，接口只回显掩码"},
			{Key: "agent_id", Label: "AgentId", Type: "string",
				Help: "同页可查。仅在发送「工作通知」（卡片模板未配置时的降级路径）时需要"},
			{Key: "robot_code", Label: "机器人编码 RobotCode", Type: "string",
				Help: "留空则使用 AppKey。投放互动卡片时需要"},
			{Key: "card_template_id", Label: "互动卡片模板 ID", Type: "string",
				Placeholder: "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
				Help: "在「开发者后台 → 卡片平台」创建并发布互动卡片模板后获取。填写后工单审批以可交互卡片下发，" +
					"模板需包含这些变量：title（标题）、content（正文）、status（状态）、link（跳转链接）、" +
					"以及两个按钮 actionId 为 approve / transfer"},
			{Key: "notice_url", Label: "审批页地址前缀", Type: "string",
				Placeholder: "https://your-host",
				Help:        "用于卡片「查看详情」链接。留空则取「系统设置 → 通知配置」里的通知链接前缀"},
			{Key: "timeout_seconds", Label: "请求超时（秒）", Type: "number", Default: 8},
		},
	}
}
