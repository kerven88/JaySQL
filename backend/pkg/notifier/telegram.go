package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/simajiong/jaysql/pkg/httpx"
)

type TelegramChannel struct{}

func init()                           { Register(&TelegramChannel{}) }
func (*TelegramChannel) Kind() string { return "telegram" }
func (*TelegramChannel) Capabilities() Capabilities {
	return Capabilities{SupportsMarkdown: true, MaxLength: 4096}
}

var telegramTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
var telegramChatPattern = regexp.MustCompile(`^(?:-?[0-9]+|@[A-Za-z0-9_]+)$`)

func (*TelegramChannel) Validate(cfg map[string]any) error {
	if !telegramTokenPattern.MatchString(cfgString(cfg, "bot_token")) {
		return fmt.Errorf("Telegram Bot Token 格式不正确")
	}
	if !telegramChatPattern.MatchString(cfgString(cfg, "chat_id")) {
		return fmt.Errorf("Telegram Chat ID 必须为数字 ID 或 @频道名")
	}
	if value := cfgString(cfg, "message_thread_id"); value != "" && value != "0" {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("Telegram 群话题 ID 必须为正整数")
		}
	}
	base := cfgString(cfg, "api_url")
	if base == "" {
		base = "https://api.telegram.org"
	}
	if err := httpx.ValidateExternalURL(base); err != nil {
		return fmt.Errorf("Telegram API 地址不合法")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return fmt.Errorf("Telegram API 地址不能包含认证信息、查询参数或片段")
	}
	return nil
}
func (c *TelegramChannel) Send(ctx context.Context, cfg map[string]any, msg Message) (Result, error) {
	if err := c.Validate(cfg); err != nil {
		return Result{}, err
	}
	token := cfgString(cfg, "bot_token")
	content := msg.PlainText
	visible := content
	rich := ""
	if strings.TrimSpace(msg.Markdown) != "" {
		rich, content, visible = renderMessageMarkdown(msg.Markdown)
	}
	if strings.TrimSpace(content) == "" {
		content = msg.Title
		visible = content
	}
	if len(utf16.Encode([]rune(visible))) > 4096 {
		return Result{}, fmt.Errorf("Telegram 消息超过 4096 字符限制，请缩短消息模板")
	}
	payload := map[string]any{"chat_id": cfgString(cfg, "chat_id"), "text": content, "link_preview_options": map[string]bool{"is_disabled": true}}
	if rich != "" {
		payload["text"] = rich
		payload["parse_mode"] = "HTML"
	}
	if thread := cfgString(cfg, "message_thread_id"); thread != "" && thread != "0" {
		id, _ := strconv.ParseInt(thread, 10, 64)
		payload["message_thread_id"] = id
	}
	base := cfgString(cfg, "api_url")
	if base == "" {
		base = "https://api.telegram.org"
	}
	response, err := httpx.PostJSON(ctx, strings.TrimRight(base, "/")+"/bot"+token+"/sendMessage", payload, nil, 0)
	// HTTP errors may contain the full URL, including the token: never surface them verbatim.
	if err != nil {
		return Result{}, fmt.Errorf("Telegram 请求失败，请检查网络、API 地址及超时设置")
	}
	var body struct {
		OK          bool   `json:"ok"`
		Code        int    `json:"error_code"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil {
		return Result{}, fmt.Errorf("Telegram 返回无效响应（HTTP %d）", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !body.OK {
		description := strings.ReplaceAll(body.Description, token, "[REDACTED]")
		if runes := []rune(description); len(runes) > 300 {
			description = string(runes[:300])
		}
		return Result{Code: strconv.Itoa(body.Code)}, fmt.Errorf("Telegram 发送失败（HTTP %d，错误码 %d）：%s", response.StatusCode, body.Code, description)
	}
	return Result{Accepted: 1, Code: "ok"}, nil
}
