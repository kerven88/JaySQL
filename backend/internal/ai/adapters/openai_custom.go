package adapters

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ============================================================
// openai_custom：用户自定义 / 企业网关 / 私有化服务
//
// 用于兜住「协议兼容 OpenAI，但鉴权或路径有企业自定义」的场景：
//   - 自定义鉴权 header 名（extra.auth_header）
//   - 附加请求头（extra.extra_headers，JSON 对象）
//   - HMAC 签名鉴权（auth_mode=signature）
//   - 自定义路径（模板的 path_chat / path_models）
//
// 有了它，接入「自研网关 / 其他厂商」不需要开发介入。
// ============================================================

func init() {
	Register(&openAICustom{})
}

type openAICustom struct {
	openAICompat
}

func (a *openAICustom) Name() string { return AdapterOpenAICustom }

// DefaultBaseURL 自定义场景无默认端点，必须由用户填写
func (a *openAICustom) DefaultBaseURL() string { return "" }

func (a *openAICustom) BuildChatRequest(cfg ProviderConfig, req ChatRequest) (*http.Request, error) {
	endpoint := cfg.PathChat
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "/chat/completions"
	}
	full, err := BuildURL(cfg, endpoint)
	if err != nil {
		return nil, err
	}

	payload, err := buildOpenAIChatPayload(req, true)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest(http.MethodPost, full, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}

	// 自定义鉴权 header 名优先于标准 bearer
	customHeader := ExtraString(cfg.Extra, "auth_header")
	if customHeader != "" && strings.TrimSpace(cfg.AuthMode) != LlmAuthSignatureLocal {
		if cfg.APIKey != "" {
			httpReq.Header.Set(customHeader, cfg.APIKey)
		}
	} else if strings.TrimSpace(cfg.AuthMode) == LlmAuthSignatureLocal {
		a.applySignature(httpReq, cfg, payload)
	} else {
		ApplyAuth(httpReq, cfg)
	}

	applyExtraHeaders(httpReq, cfg)
	return httpReq, nil
}

// LlmAuthSignatureLocal 与 models 包常量保持一致（此处避免包循环引用，单独声明）
const LlmAuthSignatureLocal = "signature"

// applySignature HMAC-SHA256 签名：
//
//	signature = HMAC-SHA256(secret, timestamp + "\n" + body)
//
// 通过 extra 的 signature_header / timestamp_header 指定头名，
// 未指定则使用 X-Signature / X-Timestamp。
func (a *openAICustom) applySignature(req *http.Request, cfg ProviderConfig, body []byte) {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(cfg.APIKey))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	signatureHeader := ExtraString(cfg.Extra, "signature_header")
	if signatureHeader == "" {
		signatureHeader = "X-Signature"
	}
	timestampHeader := ExtraString(cfg.Extra, "timestamp_header")
	if timestampHeader == "" {
		timestampHeader = "X-Timestamp"
	}
	req.Header.Set(signatureHeader, signature)
	req.Header.Set(timestampHeader, timestamp)
}

// MapError 自定义网关的错误体结构未知，做关键词兜底后复用通用分类
func (a *openAICustom) MapError(status int, body []byte) *ProviderError {
	pErr := a.openAICompat.MapError(status, body)
	if pErr.Hint == "" {
		pErr.Hint = "自定义服务请检查端点路径、鉴权 header 名与附加请求头配置"
	}
	return pErr
}
