package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/ai/adapters"
	aiForms "github.com/simajiong/jaysql/internal/ai/forms"
	aiModels "github.com/simajiong/jaysql/internal/ai/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
)

// ============================================================
// 凭证三级校验
//
//   L1 格式校验    纯本地、零网络：URL 合法性、必填字段、鉴权方式
//   L2 连通性校验  最小代价探测；失败区分 DNS/TCP/TLS/HTTP/鉴权
//   L3 能力探测    流式 / 工具调用 / JSON mode → 写入 capabilities
//
// 「先测后存」：TestLlmProviderConnection 不落库，供接入向导第三步使用
// ============================================================

// buildRuntimeConfig 组装适配器运行时配置（解密凭证）
func buildRuntimeConfig(p *aiModels.InsightLlmProviders, tpl *aiModels.InsightLlmVendorTemplates) (adapters.ProviderConfig, error) {
	apiKey := ""
	if strings.TrimSpace(p.APIKeyEnc) != "" {
		plain, err := secret.Decrypt(p.APIKeyEnc)
		if err != nil {
			return adapters.ProviderConfig{}, fmt.Errorf("凭证解密失败（可能 SECRET_KEY 已变更）: %w", err)
		}
		apiKey = plain
	}

	extra := decodeExtraConfig(p.ExtraConfig)
	// 允许在 extra 中显式提供 api_key（私有化服务常用）
	if apiKey == "" {
		if v, ok := extra["api_key"]; ok && v != nil {
			apiKey = strings.TrimSpace(fmt.Sprintf("%v", v))
		}
	}

	cfg := adapters.ProviderConfig{
		Vendor:         p.Vendor,
		BaseURL:        p.BaseURL,
		AuthMode:       p.AuthMode,
		APIKey:         apiKey,
		Extra:          extra,
		TimeoutSeconds: p.TimeoutSeconds,
	}
	if tpl != nil {
		cfg.AuthHeaderName = tpl.AuthHeaderName
		cfg.PathChat = tpl.PathChat
		cfg.PathModels = tpl.PathModels
	}
	return cfg, nil
}

// validateProviderFormat L1 格式校验（本地，无网络）
func validateProviderFormat(cfg adapters.ProviderConfig, tpl *aiModels.InsightLlmVendorTemplates) error {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("端点地址（Base URL）不能为空")
	}
	base := strings.TrimSpace(cfg.BaseURL)
	// 占位符未替换的情况交给 L2 报错（提示更精确），此处只校验基本结构
	probe := strings.ReplaceAll(base, "{resource}", "placeholder")
	parsed, err := url.Parse(probe)
	if err != nil {
		return fmt.Errorf("端点地址不是合法的 URL: %v", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("端点地址必须以 http:// 或 https:// 开头")
	}
	if parsed.Host == "" {
		return errors.New("端点地址缺少主机名")
	}

	switch strings.TrimSpace(cfg.AuthMode) {
	case "", "bearer", "api_key_header", "query_param", "signature":
	default:
		return fmt.Errorf("不支持的鉴权方式 `%s`", cfg.AuthMode)
	}

	// 模板声明的必填字段（除 base_url / api_key 外的差异化字段）
	if tpl != nil {
		var required []string
		if err := json.Unmarshal(tpl.RequiredFields, &required); err == nil {
			for _, field := range required {
				key := strings.TrimSpace(field)
				switch key {
				case "", "base_url", "api_key":
					continue
				}
				if strings.TrimSpace(adapters.ExtraString(cfg.Extra, key)) == "" {
					return fmt.Errorf("缺少必填参数：%s", key)
				}
			}
		}
		if templateRequiresAPIKey(tpl) && strings.TrimSpace(cfg.APIKey) == "" {
			return errors.New("该厂商需要 API Key，请填写")
		}
	}

	// 强制 HTTPS（私有化内网地址可显式放行）
	if !allowInsecureHTTP() && parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return errors.New("出于安全考虑，非回环地址必须使用 https；内网私有化服务如需 http，请在配置中开启 ai.allow_insecure_http")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return strings.HasPrefix(host, "127.")
}

// allowInsecureHTTP 是否允许非 HTTPS 端点（默认不允许；私有化内网可显式开启）
func allowInsecureHTTP() bool {
	if global.App.ConfigViper == nil {
		return false
	}
	value := strings.TrimSpace(global.App.ConfigViper.GetString("ai.allow_insecure_http"))
	return strings.EqualFold(value, "true")
}

// TestLlmProviderConnection 不落库的即测即用（接入向导第三步）
func TestLlmProviderConnection(form *aiForms.LlmProviderTestForm) (*adapters.ValidateResult, error) {
	tpl, err := loadVendorTemplateByVendor(form.Vendor)
	if err != nil {
		return nil, err
	}

	// 用模板默认值补全入参
	extra := mergeExtraConfigDefaults(form.ExtraConfig, tpl)
	if strings.TrimSpace(form.Model) != "" {
		extra["model"] = strings.TrimSpace(form.Model)
	}

	cfg := adapters.ProviderConfig{
		Vendor:         tpl.Vendor,
		BaseURL:        firstNonEmpty(form.BaseURL, tpl.DefaultBaseURL),
		AuthMode:       firstNonEmpty(form.AuthMode, tpl.AuthMode, "bearer"),
		AuthHeaderName: firstNonEmpty(form.AuthHeaderName, tpl.AuthHeaderName),
		APIKey:         strings.TrimSpace(form.APIKey),
		PathChat:       tpl.PathChat,
		PathModels:     tpl.PathModels,
		Extra:          extra,
		TimeoutSeconds: normalizeTimeout(form.TimeoutSeconds),
	}

	// L1 格式校验：失败不发起网络请求
	if err := validateProviderFormat(cfg, tpl); err != nil {
		return &adapters.ValidateResult{
			OK:       false,
			Stage:    "L1",
			Category: adapters.ErrCategoryConfig,
			Message:  err.Error(),
		}, nil
	}

	adapter, err := adapters.Resolve(tpl.Adapter)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds+15)*time.Second)
	defer cancel()

	result, err := adapter.Validate(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ValidateLlmProvider 对已落库的配置重新验证，并刷新状态 / 能力 / 健康度
func ValidateLlmProvider(providerID string) (*adapters.ValidateResult, error) {
	row, err := loadProviderByID(providerID)
	if err != nil {
		return nil, err
	}
	tpl, err := loadVendorTemplate(row.Vendor)
	if err != nil {
		return nil, err
	}

	cfg, err := buildRuntimeConfig(row, tpl)
	if err != nil {
		return nil, err
	}

	// L1
	if err := validateProviderFormat(cfg, tpl); err != nil {
		result := &adapters.ValidateResult{
			OK:       false,
			Stage:    "L1",
			Category: adapters.ErrCategoryConfig,
			Message:  err.Error(),
		}
		persistValidationFailure(row, result)
		return result, nil
	}

	adapter, err := adapters.Resolve(tpl.Adapter)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds+15)*time.Second)
	defer cancel()

	result, err := adapter.Validate(ctx, cfg)
	if err != nil {
		return nil, err
	}

	if result.OK {
		persistValidationSuccess(row, result)
		// 模型清单同步（差分，不覆盖用户配置）
		syncProviderModelsFromProbe(row.ProviderID, result.Models)
	} else {
		persistValidationFailure(row, result)
		writeLlmCallLog(row.ProviderID, "", "validate", "",
			result.LatencyMs, 0, 0, "error", result.Category, result.Message)
	}
	return result, nil
}

// persistValidationSuccess 验证通过：状态机推进 + 能力与健康度刷新
func persistValidationSuccess(row *aiModels.InsightLlmProviders, result *adapters.ValidateResult) {
	updates := map[string]any{
		"last_validated_at":      commonModels.LocalTime(time.Now()),
		"last_validation_status": "success",
		"last_validation_msg":    result.Message,
		"capabilities":           marshalJSONOrNull(result.Capabilities),
		"health_status":          aiModels.LlmHealthHealthy,
		"health_checked_at":      commonModels.LocalTime(time.Now()),
		"last_latency_ms":        result.LatencyMs,
	}
	// 状态机：draft/disabled → verified；enabled/degraded 保持启用语义
	switch row.Status {
	case aiModels.LlmProviderStatusDraft, aiModels.LlmProviderStatusDisabled, "":
		updates["status"] = aiModels.LlmProviderStatusVerified
	case aiModels.LlmProviderStatusEnabled, aiModels.LlmProviderStatusDegraded:
		updates["status"] = aiModels.LlmProviderStatusEnabled
	default:
		updates["status"] = aiModels.LlmProviderStatusVerified
	}
	if err := global.App.DB.Model(&aiModels.InsightLlmProviders{}).
		Where("provider_id = ?", row.ProviderID).Updates(updates).Error; err != nil {
		global.App.Log.Warnf("update llm provider validation result failed: %v", err)
	}
}

// persistValidationFailure 验证失败：已启用的降级，未启用的保持原状态
func persistValidationFailure(row *aiModels.InsightLlmProviders, result *adapters.ValidateResult) {
	status := row.Status
	switch row.Status {
	case aiModels.LlmProviderStatusEnabled, aiModels.LlmProviderStatusDegraded:
		status = aiModels.LlmProviderStatusDegraded
	}
	updates := map[string]any{
		"last_validated_at":      commonModels.LocalTime(time.Now()),
		"last_validation_status": "error",
		"last_validation_msg":    truncate(result.Message, 500),
		"health_status":          aiModels.LlmHealthDown,
		"health_checked_at":      commonModels.LocalTime(time.Now()),
		"status":                 status,
	}
	if err := global.App.DB.Model(&aiModels.InsightLlmProviders{}).
		Where("provider_id = ?", row.ProviderID).Updates(updates).Error; err != nil {
		global.App.Log.Warnf("update llm provider validation failure failed: %v", err)
	}
}

// writeLlmCallLog 写调用日志（错误信息统一截断，避免超长）
func writeLlmCallLog(providerID, modelName, scene, username string,
	latencyMs, promptTokens, completionTokens int, status, errorCode, errorMessage string) {
	entry := aiModels.InsightLlmCallLogs{
		ProviderID:       providerID,
		ModelName:        modelName,
		Scene:            scene,
		Username:         username,
		LatencyMs:        latencyMs,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
		Status:           status,
		ErrorCode:        errorCode,
		ErrorMessage:     truncate(errorMessage, 500),
	}
	if err := global.App.DB.Create(&entry).Error; err != nil {
		global.App.Log.Warnf("write llm call log failed: %v", err)
	}
}

func truncate(value string, max int) string {
	trimmed := strings.TrimSpace(value)
	if max <= 0 || len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
