package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateExternalURL(t *testing.T) {
	allowed := []string{
		"https://oapi.dingtalk.com/robot/send?access_token=abc",
		"https://open.feishu.cn/open-apis/bot/v2/hook/xyz",
		"http://10.0.0.5:8080/hook",     // 内网自建网关：允许（企业常见）
		"http://192.168.1.99:9000/hook", // 内网自建网关：允许
	}
	for _, raw := range allowed {
		if err := ValidateExternalURL(raw); err != nil {
			t.Fatalf("应放行的地址被拒：%s (%v)", raw, err)
		}
	}

	blocked := []string{
		"",
		"ftp://example.com/hook",
		"https://",
		"http://localhost/hook",
		"http://127.0.0.1/hook",
		"http://[::1]/hook",
		"http://169.254.169.254/latest/meta-data/", // 云元数据：典型 SSRF 跳板
		"http://0.0.0.0/hook",
		"http://foo.internal/hook",
	}
	for _, raw := range blocked {
		if err := ValidateExternalURL(raw); err == nil {
			t.Fatalf("应拒绝的地址被放行：%s", raw)
		}
	}
}

// 这些用例验证的是重试/超时/响应解析，不是地址闸门本身，
// 因此临时关闭闸门以允许访问 httptest 的回环地址（用完还原）。
func allowLoopbackForTest(t *testing.T) {
	t.Helper()
	previous := SetURLGuardEnabled(false)
	t.Cleanup(func() { SetURLGuardEnabled(previous) })
}

func TestPostJSONSuccess(t *testing.T) {
	allowLoopbackForTest(t)
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") == "" {
			t.Error("缺少 Content-Type 头")
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	resp, err := PostJSON(context.Background(), server.URL, map[string]any{"msgtype": "markdown"}, nil, 2*time.Second)
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(resp.Body), "errcode") {
		t.Fatalf("响应不符：%+v", resp)
	}
	if gotBody["msgtype"] != "markdown" {
		t.Fatalf("请求体不符：%+v", gotBody)
	}
}

// 4xx 不重试：配置错误重试再多次也不会变对，重试只会放大噪音。
func TestPostJSONDoesNotRetryOn4xx(t *testing.T) {
	allowLoopbackForTest(t)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errcode":300001,"errmsg":"invalid token"}`))
	}))
	defer server.Close()

	resp, err := PostJSON(context.Background(), server.URL, map[string]any{}, nil, 2*time.Second)
	if err != nil {
		t.Fatalf("4xx 应正常返回而不报错：%v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码应为 400，实际 %d", resp.StatusCode)
	}
	if attempts != 1 {
		t.Fatalf("4xx 不应重试，实际请求 %d 次", attempts)
	}
}

// 5xx 会重试：对端临时故障是重试能救回来的场景。
func TestPostJSONRetriesOn5xx(t *testing.T) {
	allowLoopbackForTest(t)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer server.Close()

	resp, err := PostJSON(context.Background(), server.URL, map[string]any{}, nil, 2*time.Second)
	if err != nil {
		t.Fatalf("重试后应成功：%v", err)
	}
	if resp.StatusCode != http.StatusOK || attempts != 2 {
		t.Fatalf("应第 2 次成功，实际 attempts=%d status=%d", attempts, resp.StatusCode)
	}
}

func TestPostJSONRejectsBlockedURL(t *testing.T) {
	if _, err := PostJSON(context.Background(), "http://127.0.0.1:1/hook", map[string]any{}, nil, time.Second); err == nil {
		t.Fatal("回环地址应在发出请求前被拒绝")
	}
}
