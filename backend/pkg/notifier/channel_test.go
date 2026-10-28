package notifier

import (
	"context"

	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/simajiong/jaysql/pkg/httpx"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// allowLoopbackForTest 允许渠道测试访问 httptest 的回环地址（用完还原）。
func allowLoopbackForTest(t *testing.T) {
	t.Helper()
	previous := httpx.SetURLGuardEnabled(false)
	t.Cleanup(func() { httpx.SetURLGuardEnabled(previous) })
}

// 钉钉加签：stringToSign = timestamp + "\n" + secret，密钥为 secret。
func TestSignDingTalkURL(t *testing.T) {
	signed, err := signDingTalkURL("https://oapi.dingtalk.com/robot/send?access_token=abc", "SEC123")
	if err != nil {
		t.Fatalf("加签失败：%v", err)
	}
	parsed, err := url.Parse(signed)
	if err != nil {
		t.Fatalf("加签后的地址不合法：%v", err)
	}
	query := parsed.Query()
	if query.Get("access_token") != "abc" {
		t.Fatalf("原有查询参数被破坏：%s", signed)
	}
	timestamp := query.Get("timestamp")
	if _, err := strconv.ParseInt(timestamp, 10, 64); err != nil {
		t.Fatalf("timestamp 应为毫秒时间戳，实际 %s", timestamp)
	}
	mac := hmac.New(sha256.New, []byte("SEC123"))
	mac.Write([]byte(timestamp + "\n" + "SEC123"))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if query.Get("sign") != want {
		t.Fatalf("签名不符：want=%s got=%s", want, query.Get("sign"))
	}
}

// 飞书签名：以 (timestamp + "\n" + secret) 为密钥对空内容做 HMAC。
func TestSignFeishu(t *testing.T) {
	timestamp, sign, err := signFeishu("SEC456")
	if err != nil {
		t.Fatalf("飞书签名失败：%v", err)
	}
	if _, err := strconv.ParseInt(timestamp, 10, 64); err != nil {
		t.Fatalf("飞书 timestamp 应为秒级时间戳，实际 %s", timestamp)
	}
	mac := hmac.New(sha256.New, []byte(timestamp+"\n"+"SEC456"))
	mac.Write(nil)
	if want := base64.StdEncoding.EncodeToString(mac.Sum(nil)); sign != want {
		t.Fatalf("飞书签名不符：want=%s got=%s", want, sign)
	}
}

// 钉钉：HTTP 200 但 errcode != 0 必须判为失败。
// 这是改造前最严重的缺陷——原实现只看状态码，把"发送失败"记成了成功。
func TestDingTalkSendFailsOnBusinessError(t *testing.T) {
	allowLoopbackForTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":310000,"errmsg":"keywords not in content"}`))
	}))
	defer server.Close()

	channel := &DingTalkChannel{}
	_, err := channel.Send(context.Background(), map[string]any{"webhook": server.URL},
		Message{Title: "t", Markdown: "hello"})
	if err == nil {
		t.Fatal("业务错误码非 0 时必须返回错误")
	}
	if !strings.Contains(err.Error(), "310000") {
		t.Fatalf("错误信息应包含业务码，实际 %v", err)
	}
}

func TestDingTalkSendSuccessAndMentions(t *testing.T) {
	allowLoopbackForTest(t)
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	channel := &DingTalkChannel{}
	result, err := channel.Send(context.Background(), map[string]any{
		"webhook":    server.URL,
		"keywords":   "JaySQL",
		"at_mobiles": true,
	}, Message{
		Title:    "title",
		Markdown: "body",
		Mentions: Mentions{Mobiles: []string{"13800000001"}},
	})
	if err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if result.Accepted != 1 {
		t.Fatalf("成功时应记 1 个接收方，实际 %+v", result)
	}
	at, _ := payload["at"].(map[string]any)
	mobiles, _ := at["atMobiles"].([]any)
	if len(mobiles) != 1 {
		t.Fatalf("@ 名单未带上，实际 %+v", payload["at"])
	}
	markdown, _ := payload["markdown"].(map[string]any)
	if !strings.Contains(markdown["text"].(string), "JaySQL") {
		t.Fatal("配置了关键字时应追加到正文（钉钉安全设置要求）")
	}
	if markdown["title"] != "title" {
		t.Fatalf("markdown 标题未传递，实际 %+v", markdown)
	}
}

// 关键词要过机器人安全校验，但不能以独立文字出现在消息里：
// 正文有链接时藏进链接 URL 的 fragment；没有链接时用零宽字符空链接兜底。
func TestDingTalkKeywordsHiddenNotVisible(t *testing.T) {
	// 场景 1：正文带「查看实例明细」链接 → 关键词进 URL fragment
	withLink := "### 告警\n- 实例：db-01\n\n[查看实例明细](http://10.0.0.1:8083/monitor/detail?instance_id=ins100)\n@所有人"
	got := embedKeywordsHidden(withLink, "JaySQL")
	if !strings.Contains(got, "instance_id=ins100#JaySQL)") {
		t.Fatalf("关键词应藏在链接 URL fragment 中，实际：\n%s", got)
	}
	if strings.Contains(got, "\nJaySQL") || strings.Contains(got, " JaySQL") {
		t.Fatalf("关键词不应作为可见文字单独出现，实际：\n%s", got)
	}
	if !strings.HasPrefix(got, "### 告警") {
		t.Fatalf("正文结构不应变化，实际：\n%s", got)
	}

	// 场景 2：正文无链接 → 零宽字符空链接兜底，原始文本仍含关键词
	noLink := "### 告警\n- 实例：db-01"
	got2 := embedKeywordsHidden(noLink, "JaySQL")
	if !strings.Contains(got2, "JaySQL") {
		t.Fatalf("无链接兜底时原始文本仍应包含关键词，实际：\n%s", got2)
	}
	if !strings.Contains(got2, "[\u200b](#JaySQL)") {
		t.Fatalf("无链接兜底应为零宽字符空链接，实际：\n%s", got2)
	}

	// 场景 3：多关键词（逗号分隔）全部嵌入
	got3 := embedKeywordsHidden(noLink, "JaySQL,告警")
	for _, kw := range []string{"JaySQL", "告警"} {
		if !strings.Contains(got3, "#"+kw) {
			t.Fatalf("多关键词应全部嵌入，缺 %s，实际：\n%s", kw, got3)
		}
	}

	// 场景 4：未配置关键词 → 原文不动
	if got4 := embedKeywordsHidden("正文", "  "); got4 != "正文" {
		t.Fatalf("空关键词应返回原文，实际：\n%s", got4)
	}
}

// 钉钉 @ 全员：只传 at.isAtAll 不够，正文必须出现「@所有人」关键字，
// 否则群里看不到 @ 标记、也收不到 @ 提醒。
func TestDingTalkAtAllAppendsKeywordToContent(t *testing.T) {
	allowLoopbackForTest(t)
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	channel := &DingTalkChannel{}
	if _, err := channel.Send(context.Background(), map[string]any{
		"webhook": server.URL,
		"at_all":  true,
	}, Message{Title: "t", Markdown: "磁盘空间不足"}); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	at, _ := payload["at"].(map[string]any)
	if at["isAtAll"] != true {
		t.Fatalf("at.isAtAll 应为 true，实际 %+v", at)
	}
	markdown, _ := payload["markdown"].(map[string]any)
	text, _ := markdown["text"].(string)
	if !strings.Contains(text, "@所有人") {
		t.Fatalf("正文未追加 @所有人 关键字，实际 %q", text)
	}
	if !strings.Contains(text, "磁盘空间不足") {
		t.Fatalf("追加 @ 时不应破坏原正文，实际 %q", text)
	}
}

// 未开 @全员 时绝不能往正文塞 @所有人，否则每条告警都在群里 @ 一遍。
func TestDingTalkNoAtAllKeepsContentClean(t *testing.T) {
	allowLoopbackForTest(t)
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	channel := &DingTalkChannel{}
	if _, err := channel.Send(context.Background(), map[string]any{
		"webhook": server.URL,
		"at_all":  false,
	}, Message{Title: "t", Markdown: "body"}); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	markdown, _ := payload["markdown"].(map[string]any)
	if text, _ := markdown["text"].(string); strings.Contains(text, "@所有人") {
		t.Fatalf("未开启 @全员 时正文不应出现 @所有人，实际 %q", text)
	}
}

// 飞书：无 open_id 绑定时应只发群消息而不报错（@ 人属于可选增强）。
func TestFeishuSendWithoutBinding(t *testing.T) {
	allowLoopbackForTest(t)
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_, _ = w.Write([]byte(`{"code":0,"msg":"success"}`))
	}))
	defer server.Close()

	channel := &FeishuChannel{}
	if _, err := channel.Send(context.Background(), map[string]any{"webhook": server.URL},
		Message{Title: "t", PlainText: "hello"}); err != nil {
		t.Fatalf("飞书发送失败：%v", err)
	}
	content, _ := payload["content"].(map[string]any)
	if !strings.Contains(content["text"].(string), "hello") {
		t.Fatalf("正文未传递，实际 %+v", payload)
	}
}

// 飞书：配置 keywords 后，消息文本必须包含这些关键词，否则会触发 19024。
func TestFeishuSendAppendsKeywords(t *testing.T) {
	allowLoopbackForTest(t)
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_, _ = w.Write([]byte(`{"code":0,"msg":"success"}`))
	}))
	defer server.Close()

	channel := &FeishuChannel{}
	if _, err := channel.Send(context.Background(), map[string]any{
		"webhook":  server.URL,
		"keywords": "告警, 数据库",
	}, Message{Title: "t", PlainText: "hello"}); err != nil {
		t.Fatalf("飞书发送失败：%v", err)
	}
	content, _ := payload["content"].(map[string]any)
	text := content["text"].(string)
	if !strings.Contains(text, "hello") {
		t.Fatalf("正文未传递，实际 %q", text)
	}
	if !strings.Contains(text, "告警") || !strings.Contains(text, "数据库") {
		t.Fatalf("关键词未追加到消息文本，实际 %q", text)
	}
	if strings.Contains(text, "，") {
		t.Fatalf("关键词分隔符不应保留中文逗号，实际 %q", text)
	}
}

// 群渠道没有个人地址，不能因为"收件人为空"报错——它的目标是群本身。
func TestChannelValidateRequiredFields(t *testing.T) {
	dingtalk := &DingTalkChannel{}
	if err := dingtalk.Validate(map[string]any{}); err == nil {
		t.Fatal("钉钉缺少 webhook 时应校验失败")
	}
	if err := dingtalk.Validate(map[string]any{"webhook": "https://oapi.dingtalk.com/robot/send?access_token=x"}); err != nil {
		t.Fatalf("合法 webhook 应通过校验：%v", err)
	}

	email := &EmailChannel{}
	if err := email.Validate(map[string]any{}); err == nil {
		t.Fatal("邮件缺少 SMTP 配置时应校验失败（除非继承平台默认凭据）")
	}
	if err := email.Validate(map[string]any{"host": "smtp.example.com", "username": "a@b.com", "port": 465}); err != nil {
		t.Fatalf("完整 SMTP 配置应通过校验：%v", err)
	}
	if err := email.Validate(map[string]any{"host": "smtp.example.com", "username": "a@b.com", "port": 70000}); err == nil {
		t.Fatal("非法端口应校验失败")
	}
}

func TestAllMetasCoverRegisteredKinds(t *testing.T) {
	metas := AllMetas()
	if len(metas) != len(Kinds()) {
		t.Fatalf("元数据数量应与注册渠道一致：metas=%d kinds=%d", len(metas), len(Kinds()))
	}
	for _, meta := range metas {
		if len(meta.Fields) == 0 {
			t.Fatalf("渠道 %s 未声明配置字段，前端无法渲染表单", meta.Kind)
		}
		if _, ok := Get(meta.Kind); !ok {
			t.Fatalf("元数据里的渠道 %s 没有对应实现", meta.Kind)
		}
	}
}
