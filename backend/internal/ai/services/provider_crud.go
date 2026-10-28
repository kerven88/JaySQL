package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	aiForms "github.com/simajiong/jaysql/internal/ai/forms"
	aiModels "github.com/simajiong/jaysql/internal/ai/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"

	"github.com/appleboy/gin-jwt/v2"
	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ============================================================
// Provider CRUD
//
// 安全约束：
//   - APIKey 一律 secret.Encrypt 后入库（AES-256-GCM，密钥来自 app.secret_key）
//   - 对外只返回掩码（APIKeyMask）+ HasAPIKey 标记，密文与明文永不出参
//   - 更新时 APIKey 留空 = 不修改
// ============================================================

// ProviderView 对外视图
type ProviderView struct {
	ProviderID   string `json:"provider_id"`
	Vendor       string `json:"vendor"`
	VendorLabel  string `json:"vendor_label"`
	VendorIcon   string `json:"vendor_icon"`
	Adapter      string `json:"adapter"`
	DisplayName  string `json:"display_name"`
	BaseURL      string `json:"base_url"`
	AuthMode     string `json:"auth_mode"`

	APIKeyMask string `json:"api_key_mask"`
	HasAPIKey  bool   `json:"has_api_key"`

	ExtraConfig      datatypes.JSON `json:"extra_config"`
	DefaultModelID   string         `json:"default_model_id"`
	DefaultModelName string         `json:"default_model_name"`

	TimeoutSeconds int `json:"timeout_seconds"`
	MaxRetries     int `json:"max_retries"`

	Status    string `json:"status"`
	IsDefault bool   `json:"is_default"`
	IsShared  bool   `json:"is_shared"`
	Owner     string `json:"owner"`

	LastValidatedAt      commonModels.LocalTime `json:"last_validated_at"`
	LastValidationStatus string                 `json:"last_validation_status"`
	LastValidationMsg    string                 `json:"last_validation_msg"`

	HealthStatus    string                 `json:"health_status"`
	HealthCheckedAt commonModels.LocalTime `json:"health_checked_at"`
	LastLatencyMs   int                    `json:"last_latency_ms"`

	ModelCount        int64 `json:"model_count"`
	EnabledModelCount int64 `json:"enabled_model_count"`

	Remark    string                 `json:"remark"`
	CreatedAt commonModels.LocalTime `json:"created_at"`
	UpdatedAt commonModels.LocalTime `json:"updated_at"`
}

// providerModelCounts 模型计数（分组查询，避免 N+1）
type providerModelCounts struct {
	Total   int64
	Enabled int64
}

func loadProviderModelCounts() map[string]providerModelCounts {
	counts := make(map[string]providerModelCounts)
	type row struct {
		ProviderID string
		Total      int64
	}
	var totalRows []row
	if err := global.App.DB.Table("insight_llm_models").
		Select("provider_id, COUNT(*) AS total").Group("provider_id").Scan(&totalRows).Error; err == nil {
		for _, item := range totalRows {
			entry := counts[item.ProviderID]
			entry.Total = item.Total
			counts[item.ProviderID] = entry
		}
	}
	var enabledRows []row
	if err := global.App.DB.Table("insight_llm_models").
		Select("provider_id, COUNT(*) AS total").Where("is_enabled = ?", true).
		Group("provider_id").Scan(&enabledRows).Error; err == nil {
		for _, item := range enabledRows {
			entry := counts[item.ProviderID]
			entry.Enabled = item.Total
			counts[item.ProviderID] = entry
		}
	}
	return counts
}

func loadVendorTemplateMap() map[string]aiModels.InsightLlmVendorTemplates {
	result := make(map[string]aiModels.InsightLlmVendorTemplates)
	var rows []aiModels.InsightLlmVendorTemplates
	if err := global.App.DB.Find(&rows).Error; err != nil {
		return result
	}
	for _, row := range rows {
		result[row.Vendor] = row
	}
	return result
}

func buildProviderView(p aiModels.InsightLlmProviders, tpl *aiModels.InsightLlmVendorTemplates, counts map[string]providerModelCounts) ProviderView {
	view := ProviderView{
		ProviderID:           p.ProviderID,
		Vendor:               p.Vendor,
		DisplayName:          p.DisplayName,
		BaseURL:              p.BaseURL,
		AuthMode:             p.AuthMode,
		APIKeyMask:           p.APIKeyMask,
		HasAPIKey:            strings.TrimSpace(p.APIKeyEnc) != "",
		ExtraConfig:          p.ExtraConfig,
		DefaultModelID:       p.DefaultModelID,
		TimeoutSeconds:       p.TimeoutSeconds,
		MaxRetries:           p.MaxRetries,
		Status:               p.Status,
		IsDefault:            p.IsDefault,
		IsShared:             p.IsShared,
		Owner:                p.Owner,
		LastValidatedAt:      p.LastValidatedAt,
		LastValidationStatus: p.LastValidationStatus,
		LastValidationMsg:    p.LastValidationMsg,
		HealthStatus:         p.HealthStatus,
		HealthCheckedAt:      p.HealthCheckedAt,
		LastLatencyMs:        p.LastLatencyMs,
		Remark:               p.Remark,
		CreatedAt:            p.CreatedAt,
		UpdatedAt:            p.UpdatedAt,
	}
	if tpl != nil {
		view.VendorLabel = tpl.Label
		view.VendorIcon = tpl.Icon
		view.Adapter = tpl.Adapter
	}
	if p.DefaultModelID != "" {
		var model aiModels.InsightLlmModels
		if err := global.App.DB.Where("model_uid = ?", p.DefaultModelID).First(&model).Error; err == nil {
			view.DefaultModelName = model.ModelName
		}
	}
	if entry, ok := counts[p.ProviderID]; ok {
		view.ModelCount = entry.Total
		view.EnabledModelCount = entry.Enabled
	}
	return view
}

// maskAPIKey 生成掩码：保留前 4 位与后 4 位
func maskAPIKey(plain string) string {
	value := strings.TrimSpace(plain)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return strings.Repeat("*", len(value))
	}
	return value[:4] + "***" + value[len(value)-4:]
}

func currentUsername(c *gin.Context) string {
	if c == nil {
		return ""
	}
	claims := jwt.ExtractClaims(c)
	raw, ok := claims["id"]
	if !ok {
		return ""
	}
	username, ok := raw.(string)
	if !ok {
		return ""
	}
	return username
}

// loadVendorTemplate 取厂商模板；不存在则报错（模板是接入的前置条件）
func loadVendorTemplate(vendor string) (*aiModels.InsightLlmVendorTemplates, error) {
	name := strings.TrimSpace(vendor)
	if name == "" {
		return nil, errors.New("厂商标识不能为空")
	}
	var tpl aiModels.InsightLlmVendorTemplates
	if err := global.App.DB.Where("vendor = ?", name).First(&tpl).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("厂商模板 `%s` 不存在，请先在厂商模板页添加或启用", name)
		}
		return nil, err
	}
	return &tpl, nil
}

// decodeExtraConfig 解析 ExtraConfig JSON
func decodeExtraConfig(raw datatypes.JSON) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	result := map[string]any{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return map[string]any{}
	}
	return result
}

// mergeExtraConfigDefaults 用模板的 extra_fields 默认值补齐缺项（缺失的必填项由校验层报错）
func mergeExtraConfigDefaults(extra map[string]any, tpl *aiModels.InsightLlmVendorTemplates) map[string]any {
	if tpl == nil || len(tpl.ExtraFields) == 0 {
		if extra == nil {
			return map[string]any{}
		}
		return extra
	}
	result := map[string]any{}
	for key, value := range extra {
		result[key] = value
	}
	var fields []map[string]any
	if err := json.Unmarshal(tpl.ExtraFields, &fields); err != nil {
		return result
	}
	for _, field := range fields {
		key, _ := field["key"].(string)
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, exists := result[key]; exists {
			continue
		}
		if def, ok := field["default"]; ok && def != nil {
			result[key] = def
		}
	}
	return result
}

// ListLlmProviders 列表（私有 Provider 仅创建人可见；共享型对全部使用者可见）
func ListLlmProviders(c *gin.Context) ([]ProviderView, error) {
	username := currentUsername(c)
	query := global.App.DB.Model(&aiModels.InsightLlmProviders{}).Order("is_default desc, id asc")
	if username != "" {
		query = query.Where("is_shared = ? OR owner = ?", true, username)
	}

	var rows []aiModels.InsightLlmProviders
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}

	templateMap := loadVendorTemplateMap()
	counts := loadProviderModelCounts()

	views := make([]ProviderView, 0, len(rows))
	for _, row := range rows {
		var tpl *aiModels.InsightLlmVendorTemplates
		if item, ok := templateMap[row.Vendor]; ok {
			copied := item
			tpl = &copied
		}
		views = append(views, buildProviderView(row, tpl, counts))
	}
	return views, nil
}

// GetLlmProviderDetail 详情（含最近调用记录）
func GetLlmProviderDetail(providerID string) (*ProviderView, []aiModels.InsightLlmCallLogs, error) {
	row, err := loadProviderByID(providerID)
	if err != nil {
		return nil, nil, err
	}
	templateMap := loadVendorTemplateMap()
	var tpl *aiModels.InsightLlmVendorTemplates
	if item, ok := templateMap[row.Vendor]; ok {
		copied := item
		tpl = &copied
	}
	view := buildProviderView(*row, tpl, loadProviderModelCounts())

	logs := []aiModels.InsightLlmCallLogs{}
	_ = global.App.DB.Where("provider_id = ?", row.ProviderID).
		Order("id desc").Limit(20).Find(&logs).Error
	return &view, logs, nil
}

func loadProviderByID(providerID string) (*aiModels.InsightLlmProviders, error) {
	id := strings.TrimSpace(providerID)
	if id == "" {
		return nil, errors.New("provider_id 不能为空")
	}
	var row aiModels.InsightLlmProviders
	if err := global.App.DB.Where("provider_id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("服务商配置不存在")
		}
		return nil, err
	}
	return &row, nil
}

// CreateLlmProvider 新增（凭证加密入库，初始状态 draft）
func CreateLlmProvider(c *gin.Context, form *aiForms.LlmProviderForm) (*ProviderView, error) {
	tpl, err := loadVendorTemplateByVendor(form.Vendor)
	if err != nil {
		return nil, err
	}

	baseURL := strings.TrimSpace(form.BaseURL)
	if baseURL == "" {
		baseURL = tpl.DefaultBaseURL
	}
	if baseURL == "" {
		return nil, errors.New("端点地址（Base URL）不能为空")
	}

	authMode := strings.TrimSpace(form.AuthMode)
	if authMode == "" {
		authMode = tpl.AuthMode
	}
	if authMode == "" {
		authMode = "bearer"
	}

	extra := mergeExtraConfigDefaults(form.ExtraConfig, tpl)

	record := aiModels.InsightLlmProviders{
		Vendor:         tpl.Vendor,
		DisplayName:    strings.TrimSpace(form.DisplayName),
		BaseURL:        baseURL,
		AuthMode:       authMode,
		ExtraConfig:    marshalJSONOrNull(extra),
		TimeoutSeconds: normalizeTimeout(form.TimeoutSeconds),
		MaxRetries:     normalizeRetries(form.MaxRetries),
		Status:         aiModels.LlmProviderStatusDraft,
		Owner:          currentUsername(c),
		Remark:         strings.TrimSpace(form.Remark),
	}
	if form.IsShared != nil {
		record.IsShared = *form.IsShared
	}

	// 鉴权信息：Key 可选（私有化服务可能不需要），但模板要求则必须提供
	apiKey := strings.TrimSpace(form.APIKey)
	keyRequired := templateRequiresAPIKey(tpl)
	if apiKey == "" && keyRequired {
		return nil, errors.New("该厂商需要 API Key，请填写")
	}
	if apiKey != "" {
		encrypted, err := secret.Encrypt(apiKey)
		if err != nil {
			return nil, fmt.Errorf("凭证加密失败: %w", err)
		}
		record.APIKeyEnc = encrypted
		record.APIKeyMask = maskAPIKey(apiKey)
	}

	if err := global.App.DB.Create(&record).Error; err != nil {
		return nil, err
	}

	view := buildProviderView(record, tpl, nil)
	return &view, nil
}

// loadVendorTemplateByVendor 解析并校验厂商模板可用性
func loadVendorTemplateByVendor(vendor string) (*aiModels.InsightLlmVendorTemplates, error) {
	tpl, err := loadVendorTemplate(vendor)
	if err != nil {
		return nil, err
	}
	if !tpl.IsEnabled {
		return nil, fmt.Errorf("厂商模板 `%s` 已停用，请先在厂商模板页启用", tpl.Vendor)
	}
	return tpl, nil
}

// templateRequiresAPIKey 判断模板是否要求必填 API Key
func templateRequiresAPIKey(tpl *aiModels.InsightLlmVendorTemplates) bool {
	if tpl == nil {
		return false
	}
	var required []string
	if err := json.Unmarshal(tpl.RequiredFields, &required); err == nil {
		for _, item := range required {
			if strings.EqualFold(strings.TrimSpace(item), "api_key") {
				return true
			}
		}
	}
	var fields []map[string]any
	if err := json.Unmarshal(tpl.ExtraFields, &fields); err == nil {
		for _, field := range fields {
			key, _ := field["key"].(string)
			if !strings.EqualFold(strings.TrimSpace(key), "api_key") {
				continue
			}
			// 显式声明为可选的 api_key 字段不参与必填
			if requiredFlag, ok := field["required"].(bool); ok && !requiredFlag {
				return false
			}
			if secret, ok := field["secret"].(bool); ok && secret {
				return false
			}
		}
	}
	return false
}

func normalizeTimeout(v int) int {
	if v <= 0 {
		return 60
	}
	if v > 600 {
		return 600
	}
	return v
}

func normalizeRetries(v int) int {
	if v < 0 {
		return 0
	}
	if v > 5 {
		return 5
	}
	return v
}

// UpdateLlmProvider 更新（APIKey 留空 = 不修改）
func UpdateLlmProvider(c *gin.Context, form *aiForms.LlmProviderForm) (*ProviderView, error) {
	row, err := loadProviderByID(form.ProviderID)
	if err != nil {
		return nil, err
	}
	tpl, err := loadVendorTemplateByVendor(row.Vendor)
	if err != nil {
		return nil, err
	}

	updates := map[string]any{
		"display_name": strings.TrimSpace(form.DisplayName),
		"remark":       strings.TrimSpace(form.Remark),
	}
	if v := strings.TrimSpace(form.BaseURL); v != "" {
		updates["base_url"] = v
	}
	if v := strings.TrimSpace(form.AuthMode); v != "" {
		updates["auth_mode"] = v
	}
	if form.ExtraConfig != nil {
		updates["extra_config"] = marshalJSONOrNull(mergeExtraConfigDefaults(form.ExtraConfig, tpl))
	}
	if form.TimeoutSeconds > 0 {
		updates["timeout_seconds"] = normalizeTimeout(form.TimeoutSeconds)
	}
	if form.MaxRetries >= 0 {
		updates["max_retries"] = normalizeRetries(form.MaxRetries)
	}
	if form.IsShared != nil {
		updates["is_shared"] = *form.IsShared
	}

	// 凭证：留空表示不修改
	if apiKey := strings.TrimSpace(form.APIKey); apiKey != "" {
		encrypted, err := secret.Encrypt(apiKey)
		if err != nil {
			return nil, fmt.Errorf("凭证加密失败: %w", err)
		}
		updates["api_key_enc"] = encrypted
		updates["api_key_mask"] = maskAPIKey(apiKey)
		// 凭证变了，之前的验证结论失效
		updates["status"] = aiModels.LlmProviderStatusDraft
		updates["last_validation_status"] = "unknown"
		updates["last_validation_msg"] = ""
	}

	if err := global.App.DB.Model(&aiModels.InsightLlmProviders{}).
		Where("provider_id = ?", row.ProviderID).Updates(updates).Error; err != nil {
		return nil, err
	}

	refreshed, err := loadProviderByID(row.ProviderID)
	if err != nil {
		return nil, err
	}
	view := buildProviderView(*refreshed, tpl, loadProviderModelCounts())
	return &view, nil
}

// DeleteLlmProvider 删除（有模型时一并清理；默认 Provider 需先取消默认）
func DeleteLlmProvider(providerID string) error {
	row, err := loadProviderByID(providerID)
	if err != nil {
		return err
	}
	if row.IsDefault {
		return errors.New("该服务商是默认服务商，请先切换到其他服务商再删除")
	}
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("provider_id = ?", row.ProviderID).Delete(&aiModels.InsightLlmModels{}).Error; err != nil {
			return err
		}
		return tx.Where("provider_id = ?", row.ProviderID).Delete(&aiModels.InsightLlmProviders{}).Error
	})
}

// SetLlmProviderEnabled 启停。启用前强制要求已验证通过（状态机约束）。
func SetLlmProviderEnabled(providerID string, enabled bool) (*ProviderView, error) {
	row, err := loadProviderByID(providerID)
	if err != nil {
		return nil, err
	}

	status := row.Status
	if enabled {
		if row.Status != aiModels.LlmProviderStatusVerified && row.Status != aiModels.LlmProviderStatusEnabled &&
			row.Status != aiModels.LlmProviderStatusDegraded {
			return nil, errors.New("该配置尚未验证通过，请先点击「测试连接」验证可用后再启用")
		}
		status = aiModels.LlmProviderStatusEnabled
	} else {
		status = aiModels.LlmProviderStatusDisabled
	}

	if err := global.App.DB.Model(&aiModels.InsightLlmProviders{}).
		Where("provider_id = ?", row.ProviderID).
		Update("status", status).Error; err != nil {
		return nil, err
	}

	refreshed, err := loadProviderByID(row.ProviderID)
	if err != nil {
		return nil, err
	}
	templateMap := loadVendorTemplateMap()
	var tpl *aiModels.InsightLlmVendorTemplates
	if item, ok := templateMap[refreshed.Vendor]; ok {
		copied := item
		tpl = &copied
	}
	view := buildProviderView(*refreshed, tpl, loadProviderModelCounts())
	return &view, nil
}

// SetLlmProviderDefault 设为场景默认（全局唯一）
func SetLlmProviderDefault(providerID string) error {
	row, err := loadProviderByID(providerID)
	if err != nil {
		return err
	}
	if row.Status != aiModels.LlmProviderStatusEnabled && row.Status != aiModels.LlmProviderStatusDegraded {
		return errors.New("只有已启用的服务商才能设为默认")
	}
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&aiModels.InsightLlmProviders{}).
			Where("is_default = ?", true).Update("is_default", false).Error; err != nil {
			return err
		}
		return tx.Model(&aiModels.InsightLlmProviders{}).
			Where("provider_id = ?", row.ProviderID).Update("is_default", true).Error
	})
}
