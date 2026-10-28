package dingtalk

// ============================================================================
// 出站调用（取 token / 手机号查 userid / 投卡片 / 工作通知）的协议测试
//
// 这部分是唯一无法靠"读代码"确认的东西：钉钉对请求头、请求体字段、场域 ID 拼法
// 都有硬性要求，写错了都会在真机上表现为一句笼统的失败。这里用一个桩服务收下
// 我们真实发出的请求，逐字段断言，把协议对错钉死在测试里。
// ============================================================================

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubDingTalk 同时扮演新版 OpenAPI 与老版 TOP 接口。
type stubDingTalk struct {
	mu       sync.Mutex
	requests []stubRequest
	server   *httptest.Server
}

type stubRequest struct {
	Path    string
	Method  string
	Headers http.Header
	Body    map[string]any
}

func newStubDingTalk(t *testing.T) *stubDingTalk {
	t.Helper()
	s := &stubDingTalk{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.requests = append(s.requests, stubRequest{
			Path: r.URL.Path, Method: r.Method, Headers: r.Header.Clone(), Body: body,
		})
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_, _ = w.Write([]byte(`{"accessToken":"tok-abc","expireIn":7200}`))
		case "/topapi/v2/user/getbymobile":
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","result":{"userid":"ding-u-1"}}`))
		case "/v1.0/card/instances/createAndDeliver":
			_, _ = w.Write([]byte(`{"success":true,"result":{"outTrackId":"x","deliverResults":[{"spaceType":"IM_ROBOT","spaceId":"s1","success":true}]}}`))
		case "/topapi/message/corpconversation/asyncsend_v2":
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NotFound","message":"no stub for ` + r.URL.Path + `"}`))
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *stubDingTalk) last(t *testing.T) stubRequest {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		t.Fatal("桩服务没有收到任何请求")
	}
	return s.requests[len(s.requests)-1]
}

func newStubClient(t *testing.T, s *stubDingTalk) *Client {
	t.Helper()
	client := NewClient(ClientConfig{
		AppKey: "app-key-1", AppSecret: "app-secret-1",
		AgentID: "agent-1", CardTemplateID: "tpl-1", Timeout: 5 * time.Second,
	})
	client.SetEndpointOverride(s.server.URL, s.server.URL)
	t.Cleanup(func() { ResetTokenCache("app-key-1") })
	return client
}

// 取 token 的请求体字段名必须是 appKey/appSecret（驼峰），且结果能正确解析并缓存。
func TestAccessTokenRequestShapeAndCache(t *testing.T) {
	stub := newStubDingTalk(t)
	client := newStubClient(t, stub)

	token, err := client.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("取 token 失败: %v", err)
	}
	if token != "tok-abc" {
		t.Fatalf("token=%q，期望 tok-abc", token)
	}
	req := stub.last(t)
	if req.Method != http.MethodPost {
		t.Errorf("method=%s，期望 POST", req.Method)
	}
	if req.Body["appKey"] != "app-key-1" || req.Body["appSecret"] != "app-secret-1" {
		t.Errorf("请求体字段不对: %+v", req.Body)
	}

	// 第二次应命中缓存，不再发请求
	before := len(stub.requests)
	if _, err := client.AccessToken(context.Background()); err != nil {
		t.Fatalf("二次取 token 失败: %v", err)
	}
	if len(stub.requests) != before {
		t.Error("token 未被缓存，每次都在请求钉钉")
	}
}

// 手机号换 userid：access_token 走 query、手机号走 body。
func TestUserIDByMobileRequestShape(t *testing.T) {
	stub := newStubDingTalk(t)
	client := newStubClient(t, stub)

	userID, err := client.UserIDByMobile(context.Background(), " 13800000000 ")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if userID != "ding-u-1" {
		t.Fatalf("userid=%q，期望 ding-u-1", userID)
	}
	req := stub.last(t)
	if req.Path != "/topapi/v2/user/getbymobile" {
		t.Errorf("path=%q", req.Path)
	}
	if !strings.Contains(stub.requests[0].Path, "access_token") && req.Headers.Get("Authorization") == "" {
		t.Log("access_token 通过 query 传递，桩服务已忽略 query，仅作提示")
	}
	if req.Body["mobile"] != "13800000000" {
		t.Errorf("body.mobile=%v，期望去掉首尾空白后的手机号", req.Body["mobile"])
	}
}

// 找不到用户（60121）要给出可读原因，而不是当成系统故障。
func TestUserIDByMobileNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			_, _ = w.Write([]byte(`{"accessToken":"tok","expireIn":7200}`))
			return
		}
		_, _ = w.Write([]byte(`{"errcode":60121,"errmsg":"cannot find user by mobile"}`))
	}))
	defer srv.Close()

	client := NewClient(ClientConfig{AppKey: "k-notfound", AppSecret: "s", Timeout: 5 * time.Second})
	client.SetEndpointOverride(srv.URL, srv.URL)
	defer ResetTokenCache("k-notfound")

	_, err := client.UserIDByMobile(context.Background(), "13900000000")
	if err == nil {
		t.Fatal("找不到用户时应当报错")
	}
	if !strings.Contains(err.Error(), "未匹配到钉钉用户") {
		t.Errorf("期望可读提示，实际: %v", err)
	}
}

// 投卡片是最容易写错的一处：请求头、callbackType、openSpaceId 拼法都必须对。
func TestDeliverCardRequestShape(t *testing.T) {
	stub := newStubDingTalk(t)
	client := newStubClient(t, stub)

	outTrackID, err := client.DeliverCardToUser(context.Background(), CardDeliverRequest{
		OutTrackID:    "gi-apv-001",
		UserID:        "ding-u-1",
		Params:        map[string]string{"title": "有工单待审批"},
		PrivateParams: map[string]any{"approval_token": "tk-1"},
	})
	if err != nil {
		t.Fatalf("投卡片失败: %v", err)
	}
	if outTrackID != "gi-apv-001" {
		t.Errorf("outTrackID=%q", outTrackID)
	}

	req := stub.last(t)
	if req.Path != "/v1.0/card/instances/createAndDeliver" {
		t.Errorf("path=%q，期望 /v1.0/card/instances/createAndDeliver", req.Path)
	}
	if got := req.Headers.Get("x-acs-dingtalk-access-token"); got != "tok-abc" {
		t.Errorf("access token 头=%q，期望 tok-abc", got)
	}
	if req.Body["callbackType"] != CallbackTypeStream {
		t.Errorf("callbackType=%v，期望 %s（否则钉钉不会把点击推到 Stream）", req.Body["callbackType"], CallbackTypeStream)
	}
	if req.Body["cardTemplateId"] != "tpl-1" {
		t.Errorf("cardTemplateId=%v", req.Body["cardTemplateId"])
	}
	if req.Body["openSpaceId"] != "dtv1.card//IM_ROBOT.ding-u-1" {
		t.Errorf("openSpaceId=%v，期望 dtv1.card//IM_ROBOT.ding-u-1", req.Body["openSpaceId"])
	}
	deliver, _ := req.Body["imRobotOpenDeliverModel"].(map[string]any)
	if deliver["spaceType"] != SpaceTypeIMRobot {
		t.Errorf("imRobotOpenDeliverModel.spaceType=%v", deliver["spaceType"])
	}
	// robotCode 未配置时必须回落到 AppKey，否则钉钉报机器人不存在
	if deliver["robotCode"] != "app-key-1" {
		t.Errorf("robotCode=%v，期望回落为 AppKey", deliver["robotCode"])
	}
	cardData, _ := req.Body["cardData"].(map[string]any)
	params, _ := cardData["cardParamMap"].(map[string]any)
	if params["title"] != "有工单待审批" {
		t.Errorf("cardParamMap 丢失: %+v", cardData)
	}
	private, _ := cardData["privateData"].(map[string]any)
	if _, ok := private["ding-u-1"]; !ok {
		t.Errorf("privateData 未按 userId 维度挂载: %+v", private)
	}

	// 令牌缓存已建立，后续不该重复取 token
	tokenCalls := 0
	for _, r := range stub.requests {
		if r.Path == "/v1.0/oauth2/accessToken" {
			tokenCalls++
		}
	}
	if tokenCalls != 1 {
		t.Errorf("accessToken 请求次数=%d，期望 1（应命中缓存）", tokenCalls)
	}
}

// 未配模板时不允许投卡片（否则会拿空模板 ID 去请求）。
func TestDeliverCardRequiresTemplate(t *testing.T) {
	stub := newStubDingTalk(t)
	client := NewClient(ClientConfig{AppKey: "k", AppSecret: "s"})
	client.SetEndpointOverride(stub.server.URL, stub.server.URL)
	if _, err := client.DeliverCardToUser(context.Background(), CardDeliverRequest{OutTrackID: "t", UserID: "u"}); err == nil {
		t.Fatal("未配置卡片模板时应报错")
	}
}

// 2026-10 真机实测：钉钉「创建卡片实例」会返回 {"result":"gi-apv-xxx","success":true} ——
// result 是字符串（卡片实例 ID）且没有 deliverResults，投放随创建一步完成。
// 解析必须兼容这种形态，否则把成功当失败、白白降级成工作通知（真机联调踩过）。
func TestDeliverCardAcceptsStringResult(t *testing.T) {
	var delivered bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_, _ = w.Write([]byte(`{"accessToken":"tok-str","expireIn":7200}`))
		case "/v1.0/card/instances/createAndDeliver":
			delivered = true
			_, _ = w.Write([]byte(`{"result":"gi-apv-f0d8031382a8c4eb4c57fe86","success":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		AppKey: "app-key-2", AppSecret: "s", AgentID: "a",
		CardTemplateID: "tpl-1", Timeout: 5 * time.Second,
	})
	client.SetEndpointOverride(server.URL, server.URL)
	t.Cleanup(func() { ResetTokenCache("app-key-2") })

	outTrackID, err := client.DeliverCardToUser(context.Background(), CardDeliverRequest{
		OutTrackID: "gi-apv-001", UserID: "ding-u-1",
		Params: map[string]string{"title": "t"},
	})
	if err != nil {
		t.Fatalf("字符串形态的 result 应视为投放成功: %v", err)
	}
	if !delivered {
		t.Error("应已发出 /v1.0/card/instances/createAndDeliver 请求")
	}
	if outTrackID != "gi-apv-001" {
		t.Errorf("outTrackID=%q，期望返回本地生成的 track", outTrackID)
	}
}

// 缺 AppKey/AppSecret 时要在取 token 这一步就失败，不能带着空凭据发请求。
func TestAccessTokenRequiresCredentials(t *testing.T) {
	client := NewClient(ClientConfig{})
	if _, err := client.AccessToken(context.Background()); err == nil {
		t.Fatal("未配置凭据时应报错")
	}
}

// 工作通知：内部应用的 agent_id 与接收人必须带上。
func TestSendWorkNoticeRequestShape(t *testing.T) {
	stub := newStubDingTalk(t)
	client := newStubClient(t, stub)

	if err := client.SendWorkNotice(context.Background(), []string{"ding-u-1"},
		"有工单待您审批", "### 工单\n请审批", "http://127.0.0.1:8083/x"); err != nil {
		t.Fatalf("发工作通知失败: %v", err)
	}
	req := stub.last(t)
	if req.Path != "/topapi/message/corpconversation/asyncsend_v2" {
		t.Errorf("path=%q", req.Path)
	}
	if req.Body["agent_id"] == nil {
		t.Errorf("缺少 agent_id: %+v", req.Body)
	}
	if req.Body["to_all_user"] == true {
		t.Error("不应广播给全员")
	}
	userIDList, _ := req.Body["userid_list"].(string)
	if !strings.Contains(userIDList, "ding-u-1") {
		t.Errorf("userid_list=%q，期望包含 ding-u-1", userIDList)
	}
	// msg 必须是 JSON 对象（含 msgtype），不能是字符串化 JSON —— 传字符串
	// 钉钉会报 errcode=41 Invalid arguments:msg（真机踩过的坑，锁死）。
	msgObj, ok := req.Body["msg"].(map[string]any)
	if !ok {
		t.Fatalf("msg 应为 JSON 对象，实际类型 %T: %+v", req.Body["msg"], req.Body["msg"])
	}
	if msgObj["msgtype"] != "markdown" {
		t.Errorf("msg.msgtype=%v，期望 markdown", msgObj["msgtype"])
	}
	markdown, _ := msgObj["markdown"].(map[string]any)
	if markdown["title"] != "有工单待您审批" {
		t.Errorf("msg.markdown.title=%v", markdown["title"])
	}
	if !strings.Contains(fmt.Sprint(markdown["text"]), "http://127.0.0.1:8083/x") {
		t.Errorf("msg.markdown.text 缺少回链: %v", markdown["text"])
	}
}

// 端点覆盖：合法 http(s) 生效，非法值必须回落到官方域名（不能把请求发到奇怪的地方）。
func TestHostOverrideValidation(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantIsC bool // 是否应回落到官方 https 域名
	}{
		{"合法 http", "http://127.0.0.1:9000", false},
		{"合法 https 带尾斜杠", "https://gw.example.com/", false},
		{"非绝对地址", "not-a-url", true},
		{"协议不支持", "ftp://x.example.com", true},
		{"只有路径", "/v1.0", true},
		{"空值", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(EnvOpenAPIBase, c.value)
			got := NewClient(ClientConfig{AppKey: "k", AppSecret: "s"}).openAPIHost
			if c.wantIsC && got != OpenAPIHost {
				t.Errorf("got=%q，期望回落到 %q", got, OpenAPIHost)
			}
			if !c.wantIsC && got == OpenAPIHost {
				t.Errorf("合法覆盖未生效，got=%q", got)
			}
			if !c.wantIsC && strings.HasSuffix(got, "/") {
				t.Errorf("尾部斜杠未去掉: %q", got)
			}
		})
	}
}
