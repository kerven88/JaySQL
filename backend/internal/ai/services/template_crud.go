package services

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/simajiong/jaysql/internal/ai/adapters"
	aiForms "github.com/simajiong/jaysql/internal/ai/forms"
	aiModels "github.com/simajiong/jaysql/internal/ai/models"
	"github.com/simajiong/jaysql/internal/global"

	"gorm.io/gorm"
)

// ============================================================
// 厂商模板 CRUD（落库可维护 → 新增厂商不发版）
//
// 内置模板：仅允许调整 is_enabled / sort_order / label（运营侧可调项），
//           定义字段由播种按 definition_hash 维护，且禁止删除。
// 自定义模板：全字段可编辑，可删除；可「复制内置模板」生成。
//
// 模板本身的校验（落库意味着运营可能填错）：
//   - vendor 命名规范
//   - adapter 必须是已注册的适配器
//   - extra_fields 字段类型必须在白名单内
//   - required_fields 引用的 key 必须真实存在
//   - default_base_url 必须是合法 URL（允许占位符）
// ============================================================

// 动态表单字段类型白名单
var allowedFieldTypes = map[string]struct{}{
	"string":   {},
	"password": {},
	"number":   {},
	"boolean":  {},
	"select":   {},
}

var vendorNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// ListLlmVendorTemplates 模板列表
func ListLlmVendorTemplates(onlyEnabled bool) ([]aiModels.InsightLlmVendorTemplates, error) {
	query := global.App.DB.Model(&aiModels.InsightLlmVendorTemplates{}).
		Order("sort_order asc, id asc")
	if onlyEnabled {
		query = query.Where("is_enabled = ?", true)
	}
	var rows []aiModels.InsightLlmVendorTemplates
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetLlmVendorTemplate 取单个模板
func GetLlmVendorTemplate(vendor string) (*aiModels.InsightLlmVendorTemplates, error) {
	name := strings.TrimSpace(vendor)
	if name == "" {
		return nil, errors.New("厂商标识不能为空")
	}
	var row aiModels.InsightLlmVendorTemplates
	if err := global.App.DB.Where("vendor = ?", name).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("厂商模板不存在")
		}
		return nil, err
	}
	return &row, nil
}

// validateLlmVendorTemplateForm 模板 schema 校验
func validateLlmVendorTemplateForm(form *aiForms.LlmVendorTemplateForm, isCustom bool) error {
	vendor := strings.TrimSpace(form.Vendor)
	if !vendorNamePattern.MatchString(vendor) {
		return errors.New("厂商标识只能包含小写字母、数字与下划线，且以字母开头（2-32 位）")
	}
	if strings.TrimSpace(form.Label) == "" {
		return errors.New("显示名不能为空")
	}

	adapter := strings.TrimSpace(form.Adapter)
	if adapter == "" {
		return errors.New("必须指定适配器")
	}
	if _, err := adapters.Resolve(adapter); err != nil {
		return fmt.Errorf("适配器 `%s` 未注册，可选：%s", adapter, strings.Join(adapters.RegisteredAdapters(), ", "))
	}

	// 端点：允许 {resource} 之类的占位符，替换为示例值后校验
	if base := strings.TrimSpace(form.DefaultBaseURL); base != "" {
		probe := strings.ReplaceAll(base, "{resource}", "example")
		parsed, err := url.Parse(probe)
		if err != nil {
			return fmt.Errorf("默认端点不是合法 URL: %v", err)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return errors.New("默认端点必须以 http:// 或 https:// 开头")
		}
	}

	// 路径模板必须以 / 开头
	for name, value := range map[string]string{"对话路径": form.PathChat, "模型列表路径": form.PathModels} {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		if !strings.HasPrefix(v, "/") {
			return fmt.Errorf("%s 必须以 / 开头", name)
		}
	}

	// 差异化字段校验
	extraKeys := map[string]struct{}{}
	for index, field := range form.ExtraFields {
		key, _ := field["key"].(string)
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("第 %d 个差异化字段缺少 key", index+1)
		}
		fieldType, _ := field["type"].(string)
		fieldType = strings.TrimSpace(fieldType)
		if fieldType == "" {
			return fmt.Errorf("差异化字段 `%s` 必须指定类型", key)
		}
		if _, ok := allowedFieldTypes[fieldType]; !ok {
			return fmt.Errorf("差异化字段 `%s` 的类型 `%s` 不被支持（可选：string / password / number / boolean / select）", key, fieldType)
		}
		if fieldType == "select" {
			options, _ := field["options"].([]any)
			if len(options) == 0 {
				return fmt.Errorf("差异化字段 `%s` 为下拉类型，必须提供选项列表", key)
			}
		}
		extraKeys[key] = struct{}{}
	}

	// required_fields 引用的 key 必须真实存在
	baseFields := map[string]struct{}{
		"base_url": {}, "api_key": {}, "auth_mode": {}, "auth_header_name": {},
	}
	for _, item := range form.RequiredFields {
		key := strings.TrimSpace(item)
		if key == "" {
			continue
		}
		if _, ok := baseFields[key]; ok {
			continue
		}
		if _, ok := extraKeys[key]; !ok {
			return fmt.Errorf("必填字段 `%s` 未在差异化字段中定义", key)
		}
	}

	if !isCustom {
		// 内置模板的 vendor 不可改，由播种维护
		var existing aiModels.InsightLlmVendorTemplates
		if err := global.App.DB.Where("vendor = ?", vendor).First(&existing).Error; err == nil && !existing.IsBuiltin {
			return errors.New("该厂商标识已被自定义模板占用")
		}
	}
	return nil
}

// CreateLlmVendorTemplate 新增自定义模板（支持从内置模板复制）
func CreateLlmVendorTemplate(form *aiForms.LlmVendorTemplateForm) (*aiModels.InsightLlmVendorTemplates, error) {
	if err := validateLlmVendorTemplateForm(form, true); err != nil {
		return nil, err
	}
	vendor := strings.TrimSpace(form.Vendor)

	var count int64
	global.App.DB.Model(&aiModels.InsightLlmVendorTemplates{}).
		Where("vendor = ?", vendor).Count(&count)
	if count > 0 {
		return nil, fmt.Errorf("厂商标识 `%s` 已存在", vendor)
	}

	enabled := true
	if form.IsEnabled != nil {
		enabled = *form.IsEnabled
	}
	category := strings.TrimSpace(form.Category)
	if category == "" {
		category = aiModels.LlmVendorCategoryCustom
	}

	record := aiModels.InsightLlmVendorTemplates{
		Vendor:              vendor,
		Label:               strings.TrimSpace(form.Label),
		Category:            category,
		Icon:                strings.TrimSpace(form.Icon),
		DefaultBaseURL:      strings.TrimSpace(form.DefaultBaseURL),
		AuthMode:            firstNonEmpty(form.AuthMode, "bearer"),
		AuthHeaderName:      strings.TrimSpace(form.AuthHeaderName),
		PathChat:            firstNonEmpty(form.PathChat, "/chat/completions"),
		PathModels:          firstNonEmpty(form.PathModels, "/models"),
		RequiredFields:      marshalJSONOrNull(form.RequiredFields),
		ExtraFields:         marshalJSONOrNull(form.ExtraFields),
		DefaultCapabilities: marshalJSONOrNull(form.DefaultCaps),
		Adapter:             strings.TrimSpace(form.Adapter),
		IsBuiltin:           false,
		IsEnabled:           enabled,
		SortOrder:           normalizeSortOrder(form.SortOrder),
		DocsURL:             strings.TrimSpace(form.DocsURL),
		Remark:              strings.TrimSpace(form.Remark),
	}
	if err := global.App.DB.Create(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// UpdateLlmVendorTemplate 更新模板
//
// 内置模板只允许改「运营可调项」——定义字段由播种维护，避免与代码定义冲突。
func UpdateLlmVendorTemplate(vendor string, form *aiForms.LlmVendorTemplateForm) (*aiModels.InsightLlmVendorTemplates, error) {
	existing, err := GetLlmVendorTemplate(vendor)
	if err != nil {
		return nil, err
	}

	if existing.IsBuiltin {
		updates := map[string]any{
			"label":      firstNonEmpty(form.Label, existing.Label),
			"sort_order": normalizeSortOrder(form.SortOrder),
			"remark":     strings.TrimSpace(form.Remark),
		}
		if form.IsEnabled != nil {
			updates["is_enabled"] = *form.IsEnabled
		}
		if err := global.App.DB.Model(&aiModels.InsightLlmVendorTemplates{}).
			Where("vendor = ?", existing.Vendor).Updates(updates).Error; err != nil {
			return nil, err
		}
		return GetLlmVendorTemplate(existing.Vendor)
	}

	// 自定义模板：全字段可改
	form.Vendor = existing.Vendor
	if err := validateLlmVendorTemplateForm(form, true); err != nil {
		return nil, err
	}
	updates := map[string]any{
		"label":                strings.TrimSpace(form.Label),
		"category":             firstNonEmpty(form.Category, aiModels.LlmVendorCategoryCustom),
		"icon":                 strings.TrimSpace(form.Icon),
		"default_base_url":     strings.TrimSpace(form.DefaultBaseURL),
		"auth_mode":            firstNonEmpty(form.AuthMode, "bearer"),
		"auth_header_name":     strings.TrimSpace(form.AuthHeaderName),
		"path_chat":            firstNonEmpty(form.PathChat, "/chat/completions"),
		"path_models":          firstNonEmpty(form.PathModels, "/models"),
		"required_fields":      marshalJSONOrNull(form.RequiredFields),
		"extra_fields":         marshalJSONOrNull(form.ExtraFields),
		"default_capabilities": marshalJSONOrNull(form.DefaultCaps),
		"adapter":              strings.TrimSpace(form.Adapter),
		"sort_order":           normalizeSortOrder(form.SortOrder),
		"docs_url":             strings.TrimSpace(form.DocsURL),
		"remark":               strings.TrimSpace(form.Remark),
	}
	if form.IsEnabled != nil {
		updates["is_enabled"] = *form.IsEnabled
	}
	if err := global.App.DB.Model(&aiModels.InsightLlmVendorTemplates{}).
		Where("vendor = ?", existing.Vendor).Updates(updates).Error; err != nil {
		return nil, err
	}
	return GetLlmVendorTemplate(existing.Vendor)
}

// DeleteLlmVendorTemplate 删除自定义模板；内置模板拒绝（只能停用）
func DeleteLlmVendorTemplate(vendor string) error {
	existing, err := GetLlmVendorTemplate(vendor)
	if err != nil {
		return err
	}
	if existing.IsBuiltin {
		return errors.New("内置厂商模板不允许删除，如需隐藏请停用")
	}

	// 已被 Provider 使用时不允许删除，避免存量配置悬空
	var used int64
	global.App.DB.Model(&aiModels.InsightLlmProviders{}).
		Where("vendor = ?", existing.Vendor).Count(&used)
	if used > 0 {
		return fmt.Errorf("已有 %d 个服务商配置使用该模板，请先删除对应配置", used)
	}

	return global.App.DB.Where("vendor = ?", existing.Vendor).
		Delete(&aiModels.InsightLlmVendorTemplates{}).Error
}

func normalizeSortOrder(v int) int {
	if v <= 0 {
		return 100
	}
	if v > 9999 {
		return 9999
	}
	return v
}
