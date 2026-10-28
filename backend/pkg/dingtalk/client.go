package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simajiong/jaysql/internal/global"
)

// ============================================================================
// 钉钉开放平台客户端（企业内部应用）
//
// 只覆盖「工单审批」需要的四件事，不做通用 SDK：
//  1. 取企业内部应用 accessToken（按 appKey 维度做进程内缓存，提前 5 分钟过期）
//  2. 手机号换 userid —— 审批人钉钉身份自动匹配的唯一入口
//  3. 创建并投放互动卡片到「IM 机器人单聊」场域 —— 定向发到某个人的钉钉
//  4. 更新卡片数据 —— 审批完成后把卡片改成结果态，避免残留可点的按钮
//
// 为什么不引入钉钉官方 SDK：本项目是非 git 仓库，go.mod 变更无法回滚，
// 而这里需要的接口只有四个、协议稳定，自己实现更可控。
// Stream 长连接同理，见 stream.go。
// ============================================================================

const (
	// OpenAPIHost 新版 OpenAPI（卡片、accessToken 走这里）
	OpenAPIHost = "https://api.dingtalk.com"
	// LegacyAPIHost 老版 TOP 接口（手机号查 userid、工作通知走这里）
	LegacyAPIHost = "https://oapi.dingtalk.com"

	// CallbackTypeStream 卡片回调走 Stream 长连接（无需公网 IP）
	CallbackTypeStream = "STREAM"

	// SpaceTypeIMRobot 投放场域：IM 机器人单聊
	SpaceTypeIMRobot = "IM_ROBOT"

	// CardInstanceCallbackTopic 卡片互动回调的 Stream topic
	CardInstanceCallbackTopic = "/v1.0/card/instances/callback"
)

// ClientConfig 企业内部应用凭据。
type ClientConfig struct {
	AppKey         string
	AppSecret      string
	AgentID        string
	RobotCode      string
	CardTemplateID string
	Timeout        time.Duration
}

// Normalize 去掉各字段首尾空白，并补默认值。
func (c ClientConfig) Normalize() ClientConfig {
	c.AppKey = strings.TrimSpace(c.AppKey)
	c.AppSecret = strings.TrimSpace(c.AppSecret)
	c.AgentID = strings.TrimSpace(c.AgentID)
	c.RobotCode = strings.TrimSpace(c.RobotCode)
	c.CardTemplateID = strings.TrimSpace(c.CardTemplateID)
	if c.Timeout <= 0 {
		c.Timeout = 8 * time.Second
	}
	return c
}

// Client 钉钉开放平台调用客户端。无状态（除 token 缓存），可并发使用。
type Client struct {
	cfg ClientConfig
	// openAPIHost/legacyAPIHost 允许测试替换为 httptest 服务地址。
	openAPIHost   string
	legacyAPIHost string
	httpClient    *http.Client
}

// 端点环境变量覆盖：私有化部署若只能经企业网关/正向代理访问钉钉，
// 或需要把请求引到本地的桩服务做联调，可在这里改基址。
// 只在显式设置且是合法绝对 URL 时生效，默认仍直连钉钉官方域名。
//
// 注意：这两项只影响**出站** HTTP 调用（取 token、查 userid、投卡片、工作通知）。
// Stream 长连接的 endpoint 由钉钉在换 ticket 时下发，无法在这里改。
const (
	EnvOpenAPIBase = "DINGTALK_OPENAPI_BASE"
	EnvLegacyBase  = "DINGTALK_LEGACY_BASE"
)

// hostOverride 读取环境变量里的基址覆盖值，非法值一律忽略并回落到官方域名。
func hostOverride(envKey, fallback string) string {
	raw := strings.TrimSpace(os.Getenv(envKey))
	if raw == "" {
		return fallback
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		// 用 nil 安全的 logw：校验路径绝不能因为"日志还没初始化"而崩掉
		logw("忽略非法的 %s=%q（须为 http/https 绝对地址），改用默认域名", envKey, raw)
		return fallback
	}
	return strings.TrimRight(raw, "/")
}

// NewClient 构造客户端。
func NewClient(cfg ClientConfig) *Client {
	cfg = cfg.Normalize()
	return &Client{
		cfg:           cfg,
		openAPIHost:   hostOverride(EnvOpenAPIBase, OpenAPIHost),
		legacyAPIHost: hostOverride(EnvLegacyBase, LegacyAPIHost),
		httpClient:    &http.Client{Timeout: cfg.Timeout},
	}
}

// Config 返回规范化后的配置（只读用途）。
func (c *Client) Config() ClientConfig { return c.cfg }

// SetEndpointOverride 仅供测试：把请求打向本地 mock 服务。
func (c *Client) SetEndpointOverride(openAPI, legacyAPI string) {
	if strings.TrimSpace(openAPI) != "" {
		c.openAPIHost = strings.TrimRight(strings.TrimSpace(openAPI), "/")
	}
	if strings.TrimSpace(legacyAPI) != "" {
		c.legacyAPIHost = strings.TrimRight(strings.TrimSpace(legacyAPI), "/")
	}
}

// ---------- accessToken ----------

type tokenEntry struct {
	token     string
	expiresAt time.Time
}

var (
	tokenMu    sync.Mutex
	tokenCache = map[string]tokenEntry{}
)

// tokenExpireSkew 提前量：钉钉返回 expireIn=7200s，留 5 分钟余量避免临界失效。
const tokenExpireSkew = 5 * time.Minute

// AccessToken 取企业内部应用 accessToken，带进程内缓存。
func (c *Client) AccessToken(ctx context.Context) (string, error) {
	if c.cfg.AppKey == "" || c.cfg.AppSecret == "" {
		return "", fmt.Errorf("钉钉应用 AppKey / AppSecret 未配置")
	}
	now := time.Now()
	tokenMu.Lock()
	if entry, ok := tokenCache[c.cfg.AppKey]; ok && now.Before(entry.expiresAt) {
		tokenMu.Unlock()
		return entry.token, nil
	}
	tokenMu.Unlock()

	payload := map[string]any{
		"appKey":    c.cfg.AppKey,
		"appSecret": c.cfg.AppSecret,
	}
	var resp struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int    `json:"expireIn"`
		Code        string `json:"code"`
		Message     string `json:"message"`
	}
	if err := c.doJSON(ctx, http.MethodPost, c.openAPIHost+"/v1.0/oauth2/accessToken", nil, payload, &resp); err != nil {
		return "", fmt.Errorf("获取钉钉 accessToken 失败: %w", err)
	}
	if resp.AccessToken == "" {
		return "", fmt.Errorf("获取钉钉 accessToken 失败: code=%s message=%s", resp.Code, resp.Message)
	}
	ttl := time.Duration(resp.ExpireIn) * time.Second
	if ttl <= tokenExpireSkew {
		ttl = time.Hour
	}
	tokenMu.Lock()
	tokenCache[c.cfg.AppKey] = tokenEntry{token: resp.AccessToken, expiresAt: now.Add(ttl - tokenExpireSkew)}
	tokenMu.Unlock()
	return resp.AccessToken, nil
}

// ResetTokenCache 清空缓存（渠道配置变更后调用，避免继续用旧应用的身份）。
func ResetTokenCache(appKey string) {
	tokenMu.Lock()
	defer tokenMu.Unlock()
	if strings.TrimSpace(appKey) == "" {
		tokenCache = map[string]tokenEntry{}
		return
	}
	delete(tokenCache, appKey)
}

// ---------- 手机号 → userid ----------

// UserIDByMobile 通过手机号查企业内 userid —— 审批人身份自动匹配的唯一入口。
//
// 查不到时返回空串 + 可读原因（而不是 error），因为「手机号不在企业通讯录」
// 是常见的配置问题而非程序故障，调用方需要把它变成界面提示。
func (c *Client) UserIDByMobile(ctx context.Context, mobile string) (string, error) {
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return "", fmt.Errorf("手机号为空，无法匹配钉钉账号")
	}
	token, err := c.AccessToken(ctx)
	if err != nil {
		return "", err
	}
	target := fmt.Sprintf("%s/topapi/v2/user/getbymobile?access_token=%s", c.legacyAPIHost, token)
	var resp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		Result  struct {
			UserID string `json:"userid"`
		} `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, target, nil, map[string]any{"mobile": mobile}, &resp); err != nil {
		return "", fmt.Errorf("查询钉钉用户失败: %w", err)
	}
	if resp.ErrCode != 0 {
		// 60121 = 找不到该用户；此时不是故障，是通讯录里没有这个人
		return "", fmt.Errorf("该手机号未匹配到钉钉用户（errcode=%d %s）", resp.ErrCode, resp.ErrMsg)
	}
	return strings.TrimSpace(resp.Result.UserID), nil
}

// ---------- 互动卡片 ----------

// CardDeliverRequest 创建并投放一张卡片到指定人的机器人单聊。
type CardDeliverRequest struct {
	OutTrackID string
	UserID     string
	Params     map[string]string
	// PrivateParams 按 userId 归属的私有数据（钉钉侧按钮回调时回传，用于携带一次性令牌）
	PrivateParams map[string]any
}

// DeliverCardToUser 创建卡片实例并投放到「IM 机器人单聊」场域。
//
// openSpaceId 拼接规则：dtv1.card//IM_ROBOT.{userId}；
// cardData.cardParamMap 的属性值只能是 String，非字符串会被钉钉忽略。
func (c *Client) DeliverCardToUser(ctx context.Context, req CardDeliverRequest) (string, error) {
	if strings.TrimSpace(c.cfg.CardTemplateID) == "" {
		return "", fmt.Errorf("钉钉卡片模板 ID（card_template_id）未配置")
	}
	outTrackID := strings.TrimSpace(req.OutTrackID)
	userID := strings.TrimSpace(req.UserID)
	if outTrackID == "" || userID == "" {
		return "", fmt.Errorf("卡片 outTrackId 与 userId 均不能为空")
	}
	token, err := c.AccessToken(ctx)
	if err != nil {
		return "", err
	}

	params := map[string]string{}
	for key, value := range req.Params {
		params[key] = value
	}
	cardData := map[string]any{"cardParamMap": params}
	if len(req.PrivateParams) > 0 {
		cardData["privateData"] = map[string]any{userID: req.PrivateParams}
	}

	robotCode := c.cfg.RobotCode
	if robotCode == "" {
		// 企业内部机器人未单独配置 robotCode 时，钉钉接受 AppKey 作为机器人编码
		robotCode = c.cfg.AppKey
	}
	payload := map[string]any{
		"cardTemplateId": c.cfg.CardTemplateID,
		"outTrackId":     outTrackID,
		"callbackType":   CallbackTypeStream,
		"cardData":       cardData,
		"openSpaceId":    fmt.Sprintf("dtv1.card//%s.%s", SpaceTypeIMRobot, userID),
		"imRobotOpenSpaceModel": map[string]any{
			"supportForward": false,
		},
		"imRobotOpenDeliverModel": map[string]any{
			"spaceType": SpaceTypeIMRobot,
			"robotCode": robotCode,
		},
	}

	// 钉钉「创建卡片实例」的响应有两种形态（2026-10 真机实测均出现过）：
	//   A. {"result":{"outTrackId":"x","deliverResults":[...]}} —— 老文档形态
	//   B. {"result":"gi-apv-xxx","success":true} —— result 直接是卡片实例 ID 字符串，
	//      且没有 deliverResults（投放随创建一步完成）
	// 用 RawMessage 兼容两者：能解出对象就用对象里的送达明细，解不出就当字符串 ID（视为成功）。
	var resp struct {
		Success bool            `json:"success"`
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}
	headers := map[string]string{"x-acs-dingtalk-access-token": token}
	// 注意：必须是 createAndDeliver（创建并投放）。/v1.0/card/instances 是「只创建」
	// 接口——真机验证它会返回 success 但不做任何投放（无 deliverResults），卡片永远
	// 不会出现在钉钉里，这是 2026-10-13 真机联调踩过的坑。
	if err := c.doJSON(ctx, http.MethodPost, c.openAPIHost+"/v1.0/card/instances/createAndDeliver", headers, payload, &resp); err != nil {
		return "", fmt.Errorf("投放钉钉卡片失败: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("投放钉钉卡片失败: code=%s message=%s", resp.Code, resp.Message)
	}
	var resultObj struct {
		OutTrackID     string `json:"outTrackId"`
		DeliverResults []struct {
			SpaceType string `json:"spaceType"`
			SpaceID   string `json:"spaceId"`
			Success   bool   `json:"success"`
			ErrorMsg  string `json:"errorMsg"`
		} `json:"deliverResults"`
	}
	if err := json.Unmarshal(resp.Result, &resultObj); err == nil {
		for _, item := range resultObj.DeliverResults {
			if !item.Success {
				return "", fmt.Errorf("钉钉卡片未送达（%s %s）: %s", item.SpaceType, item.SpaceID, item.ErrorMsg)
			}
		}
	}
	// result 是字符串（形态 B）或解不出 deliverResults 时，创建成功即视为已投放
	return outTrackID, nil
}

// UpdateCardRequest 更新卡片内容。
type UpdateCardRequest struct {
	OutTrackID    string
	Params        map[string]string
	PrivateParams map[string]any
	UserID        string
}

// UpdateCard 更新卡片数据（审批完成/转交后把卡片改成终态，避免按钮残留可点）。
func (c *Client) UpdateCard(ctx context.Context, req UpdateCardRequest) error {
	outTrackID := strings.TrimSpace(req.OutTrackID)
	if outTrackID == "" {
		return fmt.Errorf("卡片 outTrackId 不能为空")
	}
	token, err := c.AccessToken(ctx)
	if err != nil {
		return err
	}
	params := map[string]string{}
	for key, value := range req.Params {
		params[key] = value
	}
	cardData := map[string]any{"cardParamMap": params}
	if len(req.PrivateParams) > 0 && strings.TrimSpace(req.UserID) != "" {
		cardData["privateData"] = map[string]any{req.UserID: req.PrivateParams}
	}
	payload := map[string]any{
		"outTrackId": outTrackID,
		"cardData":   cardData,
		"cardUpdateOptions": map[string]any{
			// 按 key 局部更新：只覆盖本次传的变量，模板里其它变量保持原值
			"updateCardDataByKey":    true,
			"updatePrivateDataByKey": true,
		},
	}
	headers := map[string]string{"x-acs-dingtalk-access-token": token}
	var resp struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := c.doJSON(ctx, http.MethodPut, c.openAPIHost+"/v1.0/card/instances", headers, payload, &resp); err != nil {
		return fmt.Errorf("更新钉钉卡片失败: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("更新钉钉卡片失败: code=%s message=%s", resp.Code, resp.Message)
	}
	return nil
}

// ---------- 工作通知（卡片不可用时的文本降级） ----------

// SendWorkNotice 通过企业内部应用给指定 userid 发工作通知（纯文本/markdown）。
//
// 用途：卡片模板尚未创建、或卡片投放失败时，仍能把「有工单待审批」这件事
// 送到审批人钉钉里，附上平台审批页链接，保证流程不被卡住。
func (c *Client) SendWorkNotice(ctx context.Context, userIDs []string, title, markdown, linkURL string) error {
	if strings.TrimSpace(c.cfg.AgentID) == "" {
		return fmt.Errorf("钉钉 AgentId 未配置，无法发送工作通知")
	}
	cleaned := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		if id = strings.TrimSpace(id); id != "" {
			cleaned = append(cleaned, id)
		}
	}
	if len(cleaned) == 0 {
		return fmt.Errorf("没有可投递的钉钉用户")
	}
	token, err := c.AccessToken(ctx)
	if err != nil {
		return err
	}
	text := markdown
	if strings.TrimSpace(linkURL) != "" {
		text = fmt.Sprintf("%s\n\n[查看工单](%s)", strings.TrimRight(text, "\n"), linkURL)
	}
	msg := map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]any{
			"title": title,
			"text":  text,
		},
	}
	// 注意：asyncsend_v2 要求 msg 是 JSON **对象**，不是字符串化 JSON。
	// 传字符串钉钉会报 errcode=41 Invalid arguments:msg（msg 里拿不到 msgtype）。
	payload := map[string]any{
		"agent_id":    c.cfg.AgentID,
		"userid_list": strings.Join(cleaned, ","),
		"msg":         msg,
	}
	target := fmt.Sprintf("%s/topapi/message/corpconversation/asyncsend_v2?access_token=%s", c.legacyAPIHost, token)
	var resp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := c.doJSON(ctx, http.MethodPost, target, nil, payload, &resp); err != nil {
		return fmt.Errorf("发送钉钉工作通知失败: %w", err)
	}
	if resp.ErrCode != 0 {
		return fmt.Errorf("发送钉钉工作通知失败: errcode=%d %s", resp.ErrCode, resp.ErrMsg)
	}
	return nil
}

// ---------- 底层 HTTP ----------

func (c *Client) doJSON(ctx context.Context, method, rawURL string, headers map[string]string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("钉钉接口返回 HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("解析钉钉响应失败: %w（原始响应：%s）", err, snippet(raw))
	}
	return nil
}

func snippet(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	runes := []rune(text)
	if len(runes) > 300 {
		return string(runes[:300])
	}
	return text
}

// 便于日志里区分「哪个应用」出问题
func (c *Client) String() string {
	return fmt.Sprintf("dingtalk{appKey=%s agentId=%s card=%s}",
		maskSecret(c.cfg.AppKey), maskSecret(c.cfg.AgentID), maskSecret(c.cfg.CardTemplateID))
}

// maskSecret 掩码：日志里绝不出现完整凭据。
func maskSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	runes := []rune(value)
	if len(runes) <= 8 {
		return "***"
	}
	return string(runes[:4]) + "***" + string(runes[len(runes)-4:])
}

// logf 统一日志出口（带前缀，便于在 app.log 里筛）。
func logf(format string, args ...any) {
	if global.App != nil && global.App.Log != nil {
		global.App.Log.Infof("[dingtalk] "+format, args...)
	}
}

// logw 统一告警出口。
func logw(format string, args ...any) {
	if global.App != nil && global.App.Log != nil {
		global.App.Log.Warnf("[dingtalk] "+format, args...)
	}
}

// parseUnixMillis 解析毫秒时间戳字符串（Stream 帧头里的 time 字段）。
func parseUnixMillis(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return value
}
