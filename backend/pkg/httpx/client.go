// Package httpx 提供告警通知链路专用的 HTTP 客户端封装。
//
// 为什么单独包一层：改造前 pkg/notifier 直接用裸 http.Post，带来三个真实问题——
//  1. 无超时：钉钉/飞书抖动时会把调用方（采集评估 cron）挂死；
//  2. 无重试：一次网络抖动就丢消息；
//  3. 不看业务错误码：钉钉/飞书返回 HTTP 200 但 body 里 errcode != 0，
//     原实现一律判定成功，消息其实没发出去，页面上却显示"已通知"。
//
// 本包集中解决这三点，并对外配置的 webhook 地址做基础 SSRF 防护。
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// DefaultTimeout 单次请求超时。通知属于"尽力而为 + 可重试"，超时宁短勿长：
	// 8 秒足够跨公网完成一次 webhook 投递，再长只会拖慢投递 worker。
	DefaultTimeout = 8 * time.Second
	// defaultAttempts 网络层重试次数（仅对网络错误与 5xx 重试，业务错误码不重试）
	defaultAttempts = 3
	// maxRespBytes 响应体读取上限，防止对端返回巨量内容打爆内存
	maxRespBytes = 64 * 1024
)

// ErrBlockedURL 表示外部地址被安全策略拒绝（非 http(s)、回环地址、云元数据地址等）。
var ErrBlockedURL = errors.New("目标地址不被允许")

// urlGuardEnabled 地址安全闸门。默认开启（生产必须开启，防 SSRF）；
// 测试与本地联调需要访问 127.0.0.1 上的 mock 服务时，用 SetURLGuardEnabled(false) 临时关闭。
var urlGuardEnabled atomic.Bool

func init() {
	urlGuardEnabled.Store(true)
}

// SetURLGuardEnabled 开关地址安全闸门。仅用于测试与本地联调，生产环境不要关闭。
// 返回上一次的取值，便于 defer 还原。
func SetURLGuardEnabled(enabled bool) bool {
	return urlGuardEnabled.Swap(enabled)
}

var defaultClient = &http.Client{
	Timeout: DefaultTimeout,
	Transport: &http.Transport{
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     60 * time.Second,
	},
}

// Response 精简后的响应结果。
type Response struct {
	StatusCode int
	Body       []byte
}

// PostJSON 以 JSON 发送请求。timeout<=0 时用 DefaultTimeout。
//
// 重试策略：网络错误与 5xx 重试（指数间隔 300ms / 1s），4xx 与业务错误码直接返回。
func PostJSON(ctx context.Context, rawURL string, payload any, headers map[string]string, timeout time.Duration) (*Response, error) {
	if err := ValidateExternalURL(rawURL); err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	var lastErr error
	for attempt := 1; attempt <= defaultAttempts; attempt++ {
		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, rawURL, bytes.NewReader(body))
		if err != nil {
			cancel()
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		for key, value := range headers {
			req.Header.Set(key, value)
		}

		resp, err := defaultClient.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			if attempt < defaultAttempts {
				sleepBackoff(ctx, attempt)
				continue
			}
			return nil, fmt.Errorf("请求失败: %w", err)
		}

		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
		resp.Body.Close()
		cancel()
		if readErr != nil {
			lastErr = readErr
			if attempt < defaultAttempts {
				sleepBackoff(ctx, attempt)
				continue
			}
			return nil, fmt.Errorf("读取响应失败: %w", readErr)
		}

		// 5xx 视为对端故障，值得重试；4xx 是配置问题，重试无意义。
		if resp.StatusCode >= 500 && attempt < defaultAttempts {
			lastErr = fmt.Errorf("服务端返回 %d", resp.StatusCode)
			sleepBackoff(ctx, attempt)
			continue
		}
		return &Response{StatusCode: resp.StatusCode, Body: data}, nil
	}
	if lastErr == nil {
		lastErr = errors.New("未知错误")
	}
	return nil, lastErr
}

func sleepBackoff(ctx context.Context, attempt int) {
	delay := 300 * time.Millisecond
	if attempt > 1 {
		delay = time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// ValidateExternalURL 校验外部可配置地址（webhook 等）。
//
// 防的是把"渠道配置页"变成内网探测入口：入参来自用户在界面填的 URL，
// 服务端会主动发起请求，因此必须拒绝明显不该访问的目标。
//
// 说明：内网网段（10./172.16-31/192.168.）**允许**——不少企业在内网部署
// 钉钉/飞书代理或自建网关，一刀切会误伤；真正禁止的是回环地址与云元数据地址
// （169.254.169.254 这类目标是典型的 SSRF 提权跳板）。
func ValidateExternalURL(raw string) error {
	if !urlGuardEnabled.Load() {
		return nil
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("%w: 地址为空", ErrBlockedURL)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("%w: 地址格式非法", ErrBlockedURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: 仅支持 http/https", ErrBlockedURL)
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("%w: 缺少主机名", ErrBlockedURL)
	}

	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("%w: 不允许回环地址", ErrBlockedURL)
	}
	if strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") {
		return fmt.Errorf("%w: 不允许内网保留域名", ErrBlockedURL)
	}

	if ip := net.ParseIP(lower); ip != nil {
		if ip.IsLoopback() {
			return fmt.Errorf("%w: 不允许回环地址", ErrBlockedURL)
		}
		// 云元数据服务（169.254.0.0/16）与未指定地址
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("%w: 不允许链路本地/未指定地址", ErrBlockedURL)
		}
	}
	return nil
}
