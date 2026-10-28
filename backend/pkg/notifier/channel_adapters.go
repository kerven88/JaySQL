package notifier

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/simajiong/jaysql/pkg/httpx"
	"gopkg.in/gomail.v2"
)

// ============================================================================
// 内置渠道适配器：钉钉 / 飞书 / 邮件 / 企业微信
//
// 每个适配器的共同要求：
//  1. 超时与重试统一走 pkg/httpx（8 秒超时、网络层重试）；
//  2. 必须校验业务错误码——这些平台在失败时仍返回 HTTP 200，只把错误写在 body 里。
//     原实现只看 StatusCode，导致"发送失败却记录成功"，这是本次改造修掉的核心缺陷。
// ============================================================================

func init() {
	Register(&DingTalkChannel{})
	Register(&FeishuChannel{})
	Register(&EmailChannel{})
	Register(&WechatChannel{})
}

// ---------- 配置读取小工具 ----------

func cfgString(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	value, ok := cfg[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}

func cfgBool(cfg map[string]any, key string, fallback bool) bool {
	if cfg == nil {
		return fallback
	}
	value, ok := cfg[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "true" || typed == "1"
	case float64:
		return typed != 0
	default:
		return fallback
	}
}

func cfgInt(cfg map[string]any, key string, fallback int) int {
	if cfg == nil {
		return fallback
	}
	value, ok := cfg[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return parsed
		}
	}
	return fallback
}

// ---------- 钉钉 ----------

// DingTalkChannel 钉钉群机器人。
type DingTalkChannel struct{}

func (d *DingTalkChannel) Kind() string { return "dingtalk" }

func (d *DingTalkChannel) Capabilities() Capabilities {
	// 群机器人只能发到群，无法单独投递给某个人的私聊；人靠群内 @ 触达。
	return Capabilities{
		SupportsMarkdown: true,
		SupportsMention:  true,
		SupportsBatch:    true,
		Personalizable:   false,
		RequiresBinding:  false,
	}
}

func (d *DingTalkChannel) Validate(cfg map[string]any) error {
	webhook := cfgString(cfg, "webhook")
	if webhook == "" {
		return fmt.Errorf("钉钉 Webhook 不能为空")
	}
	// 注意：这里只做本地格式与安全校验，不发测试消息（真正的连通性验证走"立即测试"）
	return httpx.ValidateExternalURL(webhook)
}

func (d *DingTalkChannel) Send(ctx context.Context, cfg map[string]any, msg Message) (Result, error) {
	webhook := cfgString(cfg, "webhook")
	if webhook == "" {
		return Result{}, fmt.Errorf("钉钉 Webhook 未配置")
	}
	target := webhook
	if secret := cfgString(cfg, "secret"); secret != "" {
		signed, err := signDingTalkURL(webhook, secret)
		if err != nil {
			return Result{}, err
		}
		target = signed
	}

	content := msg.Markdown
	if content == "" {
		content = msg.PlainText
	}
	// 去掉正文尾部空白/空行，保证消息末尾整洁。
	content = strings.TrimRight(content, "\n \t\u3000")
	content = embedKeywordsHidden(content, cfgString(cfg, "keywords"))
	content = appendDingTalkMentions(content, cfg, msg)

	at := map[string]any{"isAtAll": cfgBool(cfg, "at_all", false)}
	if cfgBool(cfg, "at_mobiles", true) && len(msg.Mentions.Mobiles) > 0 {
		at["atMobiles"] = msg.Mentions.Mobiles
	}
	payload := map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]any{
			"title": msg.Title,
			"text":  content,
		},
		"at": at,
	}

	resp, err := httpx.PostJSON(ctx, target, payload, nil, 0)
	if err != nil {
		return Result{}, err
	}
	code, message := parseBusinessCode(resp.Body, "errcode", "errmsg")
	if resp.StatusCode != 200 {
		return Result{Code: strconv.Itoa(resp.StatusCode), Raw: snippet(resp.Body)},
			fmt.Errorf("钉钉返回 HTTP %d: %s", resp.StatusCode, snippet(resp.Body))
	}
	if code != "" && code != "0" {
		return Result{Code: code, Raw: snippet(resp.Body)},
			fmt.Errorf("钉钉发送失败(code=%s): %s", code, message)
	}
	return Result{Accepted: 1, Code: "0", Raw: snippet(resp.Body)}, nil
}

// appendDingTalkMentions 把 @ 关键字拼进消息正文。
//
// 钉钉群机器人要真正在群里显示「@所有人」/「@某人」，光在 at 对象里声明 isAtAll / atMobiles
// 是不够的——钉钉要求 markdown.text 正文里也出现对应的 @ 关键字（@所有人 / @手机号），
// 否则群里只看得到一条普通消息，收不到 @ 提醒、也看不到 @ 标记。
//
// 追加前先判断是否已存在，避免正文里本来就写过（例如模板自带 @人）时重复出现。
func appendDingTalkMentions(content string, cfg map[string]any, msg Message) string {
	tags := make([]string, 0, len(msg.Mentions.Mobiles)+1)
	if cfgBool(cfg, "at_all", false) {
		tags = append(tags, "@所有人 ")
	}
	if cfgBool(cfg, "at_mobiles", true) {
		for _, mobile := range msg.Mentions.Mobiles {
			mobile = strings.TrimSpace(mobile)
			if mobile == "" {
				continue
			}
			tags = append(tags, fmt.Sprintf("@%s ", mobile))
		}
	}
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if strings.Contains(content, trimmed) {
			continue
		}
		// 拼接前先压掉正文尾部空行，避免关键词/@关键字与正文之间出现空行；
		// @ 关键字本身不带尾随空格，消息末尾保持整洁。
		content = fmt.Sprintf("%s\n%s", strings.TrimRight(content, "\n \t\u3000"), trimmed)
	}
	return content
}

// embedKeywordsHidden 让「自定义关键词」通过机器人安全校验，但不显示在消息里。
//
// 钉钉机器人的关键词校验是对消息原始文本（markdown.text）做子串匹配，不要求渲染后可见：
//  1. 正文含 Markdown 链接时，把关键词追加进第一个链接 URL 的 fragment 部分，
//     如 [查看实例明细](http://x/detail?id=1) → (http://x/detail?id=1#JaySQL)，
//     渲染仍只显示链接文字，跳转不受影响；
//  2. 正文没有链接时，追加一个以零宽字符作链接文字的空链接兜底，渲染宽度为零、肉眼不可见。
//
// keywords 支持逗号/空格分隔的多个关键词（钉钉机器人安全设置可配多个，命中其一即可）。
func embedKeywordsHidden(content, keywords string) string {
	trimmed := strings.TrimSpace(keywords)
	if trimmed == "" {
		return content
	}
	kws := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\t'
	})
	if len(kws) == 0 {
		return content
	}
	content = strings.TrimRight(content, "\n \t\u3000")
	if idx := strings.Index(content, "](http"); idx >= 0 {
		if close := strings.IndexByte(content[idx:], ')'); close > 0 {
			insertAt := idx + close
			suffix := make([]byte, 0, 16)
			for _, k := range kws {
				suffix = append(suffix, ('#'))
				suffix = append(suffix, k...)
			}
			return content[:insertAt] + string(suffix) + content[insertAt:]
		}
	}
	fallback := ""
	for _, k := range kws {
		fallback += "\n[\u200b](#" + k + ")"
	}
	return content + fallback
}

// signDingTalkURL 按钉钉加签规则补上 timestamp 与 sign。
// 规则：stringToSign = timestamp + "\n" + secret，用 secret 作 HMAC-SHA256 密钥。
func signDingTalkURL(webhook, secret string) (string, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(timestamp + "\n" + secret)); err != nil {
		return "", err
	}
	sign := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	separator := "?"
	if strings.Contains(webhook, "?") {
		separator = "&"
	}
	return fmt.Sprintf("%s%stimestamp=%s&sign=%s", webhook, separator, timestamp, sign), nil
}

// ---------- 飞书 ----------

// FeishuChannel 飞书群机器人。
type FeishuChannel struct{}

func (f *FeishuChannel) Kind() string { return "feishu" }

func (f *FeishuChannel) Capabilities() Capabilities {
	return Capabilities{
		SupportsMarkdown: true,
		SupportsMention:  true,
		SupportsBatch:    true,
		Personalizable:   false,
		RequiresBinding:  true, // @人需要 open_id
	}
}

func (f *FeishuChannel) Validate(cfg map[string]any) error {
	webhook := cfgString(cfg, "webhook")
	if webhook == "" {
		return fmt.Errorf("飞书 Webhook 不能为空")
	}
	return httpx.ValidateExternalURL(webhook)
}

func (f *FeishuChannel) Send(ctx context.Context, cfg map[string]any, msg Message) (Result, error) {
	webhook := cfgString(cfg, "webhook")
	if webhook == "" {
		return Result{}, fmt.Errorf("飞书 Webhook 未配置")
	}

	content := msg.PlainText
	if content == "" {
		content = msg.Markdown
	}
	// 飞书文本消息的 @ 语法：<at user_id="ou_xxx"></at>；未绑定 open_id 的人不 @，但仍发到群。
	if !cfgBool(cfg, "at_all", false) {
		for _, openID := range msg.Mentions.OpenIDs {
			content = fmt.Sprintf("<at user_id=\"%s\"></at> %s", openID, content)
		}
	}
	// 飞书与钉钉不同，text 消息里没有「隐藏关键词」的载体。
	// 若机器人开启「自定义关键词」安全设置，必须让消息文本包含至少一个关键词，否则返回 19024。
	// 这里把关键词追加到末尾（可见），满足校验；若介意噪音，建议改用「签名校验」或在机器人端关闭关键词校验。
	content = appendFeishuKeywords(content, cfgString(cfg, "keywords"))

	payload := map[string]any{
		"msg_type": "text",
		"content":  map[string]any{"text": content},
	}
	if cfgBool(cfg, "at_all", false) {
		payload["msg_type"] = "text"
	}
	if secret := cfgString(cfg, "secret"); secret != "" {
		timestamp, sign, err := signFeishu(secret)
		if err != nil {
			return Result{}, err
		}
		payload["timestamp"] = timestamp
		payload["sign"] = sign
	}

	resp, err := httpx.PostJSON(ctx, webhook, payload, nil, 0)
	if err != nil {
		return Result{}, err
	}
	code, message := parseBusinessCode(resp.Body, "code", "msg")
	if code == "" {
		// 部分版本用 StatusCode / StatusMessage
		code, message = parseBusinessCode(resp.Body, "StatusCode", "StatusMessage")
	}
	if resp.StatusCode != 200 {
		return Result{Code: strconv.Itoa(resp.StatusCode), Raw: snippet(resp.Body)},
			fmt.Errorf("飞书返回 HTTP %d: %s", resp.StatusCode, snippet(resp.Body))
	}
	if code != "" && code != "0" {
		return Result{Code: code, Raw: snippet(resp.Body)},
			fmt.Errorf("飞书发送失败(code=%s): %s", code, message)
	}
	return Result{Accepted: 1, Code: "0", Raw: snippet(resp.Body)}, nil
}

// signFeishu 飞书签名规则：以 (timestamp + "\n" + secret) 作为密钥，对空内容做 HMAC-SHA256。
// 注意与钉钉不同——钉钉的密钥是 secret、消息是 timestamp+"\n"+secret；飞书正好相反。
// appendFeishuKeywords 把配置的关键词追加到文本末尾，用于通过飞书「自定义关键词」安全校验。
// 多个关键词按中英逗号/空白拆分后全部追加，确保消息中至少包含所有关键词（飞书只要求命中其一，全量追加最稳）。
func appendFeishuKeywords(content, keywords string) string {
	trimmed := strings.TrimSpace(keywords)
	if trimmed == "" {
		return content
	}
	kws := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\t'
	})
	if len(kws) == 0 {
		return content
	}
	content = strings.TrimRight(content, "\n \t\u3000")
	if content != "" {
		content += "\n\n"
	}
	return content + strings.Join(kws, " ")
}

func signFeishu(secret string) (string, string, error) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	stringToSign := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(stringToSign))
	if _, err := mac.Write(nil); err != nil {
		return "", "", err
	}
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return timestamp, sign, nil
}

// ---------- 邮件 ----------

// EmailChannel SMTP 邮件。
type EmailChannel struct{}

func (e *EmailChannel) Kind() string { return "email" }

func (e *EmailChannel) Capabilities() Capabilities {
	return Capabilities{
		SupportsMarkdown: false,
		SupportsMention:  false,
		SupportsBatch:    true,
		Personalizable:   true,
		RequiresBinding:  false,
	}
}

func (e *EmailChannel) Validate(cfg map[string]any) error {
	if cfgString(cfg, "host") == "" {
		return fmt.Errorf("SMTP 服务器不能为空（若已配置平台默认通知凭据，可留空继承）")
	}
	if cfgString(cfg, "username") == "" {
		return fmt.Errorf("发件账号不能为空（若已配置平台默认通知凭据，可留空继承）")
	}
	port := cfgInt(cfg, "port", 465)
	if port <= 0 || port > 65535 {
		return fmt.Errorf("SMTP 端口 %d 非法", port)
	}
	return nil
}

func (e *EmailChannel) Send(ctx context.Context, cfg map[string]any, msg Message) (Result, error) {
	host := cfgString(cfg, "host")
	username := cfgString(cfg, "username")
	password := cfgString(cfg, "password")
	port := cfgInt(cfg, "port", 465)
	if host == "" || username == "" {
		return Result{}, fmt.Errorf("邮件渠道缺少 SMTP 配置，且平台默认通知凭据未启用或未填写")
	}

	addresses := make([]string, 0, len(msg.Recipients))
	rejected := make([]string, 0)
	for _, recipient := range msg.Recipients {
		address := strings.TrimSpace(recipient.Address)
		if address == "" || !strings.Contains(address, "@") {
			rejected = append(rejected, recipient.Address)
			continue
		}
		addresses = append(addresses, address)
	}
	if len(addresses) == 0 {
		return Result{Rejected: rejected}, fmt.Errorf("没有可用的收件人邮箱")
	}

	from := cfgString(cfg, "from")
	subject := strings.TrimSpace(cfgString(cfg, "subject_prefix") + " " + msg.Title)

	mail := gomail.NewMessage()
	if from != "" {
		mail.SetHeader("From", fmt.Sprintf("%s <%s>", from, username))
	} else {
		mail.SetHeader("From", username)
	}
	mail.SetHeader("To", addresses...)
	mail.SetHeader("Subject", subject)

	body := msg.PlainText
	if body == "" {
		body = msg.Markdown
	}
	mail.SetBody("text/plain; charset=UTF-8", body)

	// gomail v2 的 Dialer 没有 Timeout 字段，其内部固定使用 10 秒 TCP 拨号超时
	// （见 gomail smtp.go: netDialTimeout），比 webhook 的 8 秒略宽，符合 SMTP 握手更慢的实际情况。
	dialer := gomail.NewDialer(host, port, username, password)
	if err := dialer.DialAndSend(mail); err != nil {
		return Result{Rejected: rejected}, fmt.Errorf("SMTP 发送失败: %w", err)
	}
	return Result{Accepted: len(addresses), Rejected: rejected, Code: "smtp_ok"}, nil
}

// ---------- 企业微信 ----------

// WechatChannel 企业微信群机器人（沿用既有能力，纳入统一注册表）。
type WechatChannel struct{}

func (w *WechatChannel) Kind() string { return "wechat" }

func (w *WechatChannel) Capabilities() Capabilities {
	return Capabilities{
		SupportsMarkdown: true,
		SupportsMention:  true,
		SupportsBatch:    true,
		Personalizable:   false,
		RequiresBinding:  false,
	}
}

func (w *WechatChannel) Validate(cfg map[string]any) error {
	webhook := cfgString(cfg, "webhook")
	if webhook == "" {
		return fmt.Errorf("企业微信 Webhook 不能为空")
	}
	return httpx.ValidateExternalURL(webhook)
}

func (w *WechatChannel) Send(ctx context.Context, cfg map[string]any, msg Message) (Result, error) {
	webhook := cfgString(cfg, "webhook")
	if webhook == "" {
		return Result{}, fmt.Errorf("企业微信 Webhook 未配置")
	}
	content := msg.Markdown
	if content == "" {
		content = msg.PlainText
	}
	markdown := map[string]any{"content": content}
	mentions := make([]string, 0)
	if cfgBool(cfg, "at_mobiles", true) {
		mentions = append(mentions, msg.Mentions.Mobiles...)
	}
	if cfgBool(cfg, "at_all", false) {
		markdown["mentioned_list"] = []string{"@all"}
	} else if len(mentions) > 0 {
		markdown["mentioned_mobile_list"] = mentions
	}

	resp, err := httpx.PostJSON(ctx, webhook, map[string]any{
		"msgtype":  "markdown",
		"markdown": markdown,
	}, nil, 0)
	if err != nil {
		return Result{}, err
	}
	code, message := parseBusinessCode(resp.Body, "errcode", "errmsg")
	if resp.StatusCode != 200 {
		return Result{Code: strconv.Itoa(resp.StatusCode), Raw: snippet(resp.Body)},
			fmt.Errorf("企业微信返回 HTTP %d: %s", resp.StatusCode, snippet(resp.Body))
	}
	if code != "" && code != "0" {
		return Result{Code: code, Raw: snippet(resp.Body)},
			fmt.Errorf("企业微信发送失败(code=%s): %s", code, message)
	}
	return Result{Accepted: 1, Code: "0", Raw: snippet(resp.Body)}, nil
}

// ---------- 公共工具 ----------

// parseBusinessCode 从响应体里取业务错误码（钉钉/企微用 errcode，飞书用 code）。
// 取不到时返回空串——表示该响应体没有业务码结构，交给调用方按 HTTP 状态码判断。
func parseBusinessCode(body []byte, codeKey, msgKey string) (string, string) {
	if len(body) == 0 {
		return "", ""
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", ""
	}
	code := ""
	if value, ok := decoded[codeKey]; ok && value != nil {
		switch typed := value.(type) {
		case string:
			code = typed
		case float64:
			code = strconv.FormatFloat(typed, 'f', -1, 64)
		default:
			code = fmt.Sprintf("%v", typed)
		}
	}
	message := ""
	if value, ok := decoded[msgKey]; ok && value != nil {
		message = fmt.Sprintf("%v", value)
	}
	return code, message
}

func snippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	runes := []rune(text)
	if len(runes) > 200 {
		return string(runes[:200])
	}
	return text
}
