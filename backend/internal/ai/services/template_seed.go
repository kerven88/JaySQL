package services

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"

	aiModels "github.com/simajiong/jaysql/internal/ai/models"
	"github.com/simajiong/jaysql/internal/ai/seeds"
	"github.com/simajiong/jaysql/internal/global"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ============================================================
// 内置厂商模板播种
//
// 关键约束（否则每次重启都会把运营侧的调整洗回默认值）：
//   1. 幂等：按 vendor upsert
//   2. 只有 definition_hash 变化时才更新「定义字段」
//   3. is_enabled / sort_order / label 属于运营侧可调项，永不被播种覆盖
//   4. 已存在的内置模板不会被重新创建，也不会被删除
// ============================================================

// SeedBuiltinVendorTemplates 播种内置厂商模板
func SeedBuiltinVendorTemplates(db *gorm.DB) error {
	if db == nil {
		return errors.New("db is nil")
	}
	if !db.Migrator().HasTable(&aiModels.InsightLlmVendorTemplates{}) {
		return errors.New("insight_llm_vendor_templates 表不存在，跳过模板播种")
	}

	for _, def := range seeds.BuiltinVendorTemplates {
		hash := vendorTemplateDefinitionHash(def)

		var row aiModels.InsightLlmVendorTemplates
		err := db.Where("vendor = ?", def.Vendor).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			record := buildTemplateFromSeed(def, hash)
			if createErr := db.Create(&record).Error; createErr != nil {
				global.App.Log.Warnf("seed llm vendor template `%s` failed: %v", def.Vendor, createErr)
			}
			continue
		}
		if err != nil {
			global.App.Log.Warnf("load llm vendor template `%s` failed: %v", def.Vendor, err)
			continue
		}

		// 定义没变 → 完全不触碰这一行
		if row.DefinitionHash == hash {
			continue
		}

		updates := vendorTemplateDefinitionUpdates(def, hash)
		if len(updates) == 0 {
			continue
		}
		if updateErr := db.Model(&aiModels.InsightLlmVendorTemplates{}).Where("id = ?", row.ID).
			Updates(updates).Error; updateErr != nil {
			global.App.Log.Warnf("update llm vendor template `%s` definition failed: %v", def.Vendor, updateErr)
			continue
		}
		global.App.Log.Infof("init: 已更新内置大模型厂商模板定义 `%s`（保留运营侧的启停与排序）", def.Vendor)
	}
	return nil
}

// vendorTemplateDefinitionHash 计算定义指纹（仅覆盖"定义字段"，不含运营可调项）
func vendorTemplateDefinitionHash(def seeds.VendorTemplate) string {
	payload := map[string]any{
		"vendor":           def.Vendor,
		"label":            def.Label,
		"category":         def.Category,
		"icon":             def.Icon,
		"default_base_url": def.DefaultBaseURL,
		"auth_mode":        def.AuthMode,
		"auth_header_name": def.AuthHeaderName,
		"path_chat":        def.PathChat,
		"path_models":      def.PathModels,
		"required_fields":  def.RequiredFields,
		"extra_fields":     def.ExtraFields,
		"default_caps":     def.DefaultCaps,
		"adapter":          def.Adapter,
		"docs_url":         def.DocsURL,
		"remark":           def.Remark,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

func buildTemplateFromSeed(def seeds.VendorTemplate, hash string) aiModels.InsightLlmVendorTemplates {
	return aiModels.InsightLlmVendorTemplates{
		Vendor:              def.Vendor,
		Label:               def.Label,
		Category:            def.Category,
		Icon:                def.Icon,
		DefaultBaseURL:      def.DefaultBaseURL,
		AuthMode:            def.AuthMode,
		AuthHeaderName:      def.AuthHeaderName,
		PathChat:            def.PathChat,
		PathModels:          def.PathModels,
		RequiredFields:      marshalJSONOrNull(def.RequiredFields),
		ExtraFields:         marshalJSONOrNull(def.ExtraFields),
		DefaultCapabilities: marshalJSONOrNull(def.DefaultCaps),
		Adapter:             def.Adapter,
		IsBuiltin:           true,
		IsEnabled:           true,
		SortOrder:           def.SortOrder,
		DefinitionHash:      hash,
		DocsURL:             def.DocsURL,
		Remark:              def.Remark,
	}
}

// vendorTemplateDefinitionUpdates 定义字段更新集。
// 注意：刻意不包含 is_enabled / sort_order / label —— 这三项由运营侧掌控。
func vendorTemplateDefinitionUpdates(def seeds.VendorTemplate, hash string) map[string]any {
	return map[string]any{
		"category":             def.Category,
		"icon":                 def.Icon,
		"default_base_url":     def.DefaultBaseURL,
		"auth_mode":            def.AuthMode,
		"auth_header_name":     def.AuthHeaderName,
		"path_chat":            def.PathChat,
		"path_models":          def.PathModels,
		"required_fields":      marshalJSONOrNull(def.RequiredFields),
		"extra_fields":         marshalJSONOrNull(def.ExtraFields),
		"default_capabilities": marshalJSONOrNull(def.DefaultCaps),
		"adapter":              def.Adapter,
		"docs_url":             def.DocsURL,
		"remark":               def.Remark,
		"definition_hash":      hash,
	}
}

func marshalJSONOrNull(v any) datatypes.JSON {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return datatypes.JSON(data)
}
