package services

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/ai/adapters"
	aiForms "github.com/simajiong/jaysql/internal/ai/forms"
	aiModels "github.com/simajiong/jaysql/internal/ai/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"gorm.io/gorm"
)

// ============================================================
// 模型表管理与差分同步
//
// 拆表后的核心约束：刷新必须是「差分同步」而不是「清空重建」——
// 用户设过的 is_enabled / 自定义别名 / 默认标记必须保留；
// 手工添加（source=manual）的行永不被同步覆盖；
// 厂商侧消失的模型只置 unavailable，由用户决定是否删除。
// ============================================================

// syncProviderModelsFromProbe 用探测到的模型清单做差分同步。
// models 为空时直接返回 —— 绝不因为「没探测到」而清空已有清单。
func syncProviderModelsFromProbe(providerID string, probed []adapters.ModelInfo) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" || len(probed) == 0 {
		return
	}

	var existing []aiModels.InsightLlmModels
	if err := global.App.DB.Where("provider_id = ?", providerID).Find(&existing).Error; err != nil {
		global.App.Log.Warnf("load llm models for sync failed: %v", err)
		return
	}
	existingMap := make(map[string]aiModels.InsightLlmModels, len(existing))
	for _, item := range existing {
		existingMap[item.ModelName] = item
	}

	now := commonModels.LocalTime(time.Now())
	seen := make(map[string]struct{}, len(probed))
	inserted, updated, missing := 0, 0, 0

	for _, item := range probed {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		seen[name] = struct{}{}

		if row, ok := existingMap[name]; ok {
			// 已存在：只刷新「厂商侧事实」，保留用户配置
			updates := map[string]any{
				"last_seen_at": now,
				"status":       aiModels.LlmModelStatusAvailable,
			}
			if row.Source == aiModels.LlmModelSourceAuto && strings.TrimSpace(row.DisplayName) == "" {
				updates["display_name"] = name
			}
			if len(item.Capabilities) > 0 {
				updates["capabilities"] = marshalJSONOrNull(item.Capabilities)
			}
			if item.ContextWindow > 0 {
				updates["context_window"] = item.ContextWindow
			}
			// 已经被标记下线的模型重新出现 → 恢复 available（status 已在上面覆盖）
			if err := global.App.DB.Model(&aiModels.InsightLlmModels{}).
				Where("id = ?", row.ID).Updates(updates).Error; err != nil {
				global.App.Log.Warnf("sync update llm model `%s` failed: %v", name, err)
				continue
			}
			updated++
			continue
		}

		record := aiModels.InsightLlmModels{
			ProviderID:    providerID,
			ModelName:     name,
			DisplayName:   name,
			ContextWindow: item.ContextWindow,
			Capabilities:  marshalJSONOrNull(item.Capabilities),
			IsEnabled:     true,
			Source:        aiModels.LlmModelSourceAuto,
			Status:        aiModels.LlmModelStatusAvailable,
			LastSeenAt:    now,
		}
		if err := global.App.DB.Create(&record).Error; err != nil {
			global.App.Log.Warnf("sync insert llm model `%s` failed: %v", name, err)
			continue
		}
		inserted++
	}

	// 厂商侧消失：仅自动来源的置为 unavailable，手工添加的完全不动
	for name, row := range existingMap {
		if _, ok := seen[name]; ok {
			continue
		}
		if row.Source == aiModels.LlmModelSourceManual {
			continue
		}
		if row.Status == aiModels.LlmModelStatusUnavailable {
			continue
		}
		if err := global.App.DB.Model(&aiModels.InsightLlmModels{}).
			Where("id = ?", row.ID).
			Update("status", aiModels.LlmModelStatusUnavailable).Error; err != nil {
			global.App.Log.Warnf("mark llm model `%s` unavailable failed: %v", name, err)
			continue
		}
		missing++
	}

	if inserted > 0 || updated > 0 || missing > 0 {
		global.App.Log.Infof("llm model sync provider=%s inserted=%d updated=%d unavailable=%d",
			providerID, inserted, updated, missing)
	}

	ensureProviderDefaultModel(providerID)
}

// ensureProviderDefaultModel Provider 尚无默认模型时，挑一个可用模型作为默认
func ensureProviderDefaultModel(providerID string) {
	var provider aiModels.InsightLlmProviders
	if err := global.App.DB.Where("provider_id = ?", providerID).First(&provider).Error; err != nil {
		return
	}
	if strings.TrimSpace(provider.DefaultModelID) != "" {
		// 校验引用是否仍然有效
		var count int64
		global.App.DB.Model(&aiModels.InsightLlmModels{}).
			Where("model_uid = ?", provider.DefaultModelID).Count(&count)
		if count > 0 {
			return
		}
	}

	var candidate aiModels.InsightLlmModels
	err := global.App.DB.Where("provider_id = ? AND source = ? AND status = ?",
		providerID, aiModels.LlmModelSourceAuto, aiModels.LlmModelStatusAvailable).
		Order("id asc").First(&candidate).Error
	if err != nil {
		err = global.App.DB.Where("provider_id = ? AND status = ?",
			providerID, aiModels.LlmModelStatusAvailable).Order("id asc").First(&candidate).Error
		if err != nil {
			return
		}
	}
	_ = setProviderDefaultModel(providerID, candidate.ModelUID)
}

func setProviderDefaultModel(providerID, modelUID string) error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&aiModels.InsightLlmModels{}).
			Where("provider_id = ? AND is_default = ?", providerID, true).
			Update("is_default", false).Error; err != nil {
			return err
		}
		if err := tx.Model(&aiModels.InsightLlmModels{}).
			Where("model_uid = ?", modelUID).Update("is_default", true).Error; err != nil {
			return err
		}
		// 同步写回 provider，并把模型名写入 extra.model（供探测与默认调用使用）
		var provider aiModels.InsightLlmProviders
		if err := tx.Where("provider_id = ?", providerID).First(&provider).Error; err != nil {
			return err
		}
		extra := decodeExtraConfig(provider.ExtraConfig)
		var model aiModels.InsightLlmModels
		if err := tx.Where("model_uid = ?", modelUID).First(&model).Error; err == nil {
			extra["model"] = model.ModelName
		}
		return tx.Model(&aiModels.InsightLlmProviders{}).
			Where("provider_id = ?", providerID).
			Updates(map[string]any{
				"default_model_id": modelUID,
				"extra_config":     marshalJSONOrNull(extra),
			}).Error
	})
}

// ------------------------------------------------------------------
// 模型 CRUD
// ------------------------------------------------------------------

// LlmModelView 模型对外视图（附带所属 Provider 信息）
type LlmModelView struct {
	aiModels.InsightLlmModels
	ProviderName string `json:"provider_name"`
	Vendor       string `json:"vendor"`
}

// ListLlmModels 模型列表（分页）
func ListLlmModels(form *aiForms.LlmModelQueryForm) ([]LlmModelView, int64, error) {
	page := form.Page
	if page <= 0 {
		page = 1
	}
	pageSize := form.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 500 {
		pageSize = 500
	}

	query := global.App.DB.Model(&aiModels.InsightLlmModels{})
	if v := strings.TrimSpace(form.ProviderID); v != "" {
		query = query.Where("provider_id = ?", v)
	}
	if keyword := strings.TrimSpace(form.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("model_name LIKE ? OR display_name LIKE ?", like, like)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []aiModels.InsightLlmModels
	if err := query.Order("provider_id asc, is_default desc, id asc").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}

	// Provider 名称映射
	nameMap := map[string]string{}
	vendorMap := map[string]string{}
	var providers []aiModels.InsightLlmProviders
	if err := global.App.DB.Find(&providers).Error; err == nil {
		for _, p := range providers {
			nameMap[p.ProviderID] = p.DisplayName
			vendorMap[p.ProviderID] = p.Vendor
		}
	}

	views := make([]LlmModelView, 0, len(rows))
	for _, row := range rows {
		views = append(views, LlmModelView{
			InsightLlmModels: row,
			ProviderName:     nameMap[row.ProviderID],
			Vendor:           vendorMap[row.ProviderID],
		})
	}
	return views, total, nil
}

// CreateLlmModel 手工添加模型（应对厂商无模型列表接口的私有化场景）
func CreateLlmModel(form *aiForms.LlmModelForm) (*aiModels.InsightLlmModels, error) {
	provider, err := loadProviderByID(form.ProviderID)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(form.ModelName)
	if name == "" {
		return nil, errors.New("模型名不能为空")
	}

	var exists int64
	global.App.DB.Model(&aiModels.InsightLlmModels{}).
		Where("provider_id = ? AND model_name = ?", provider.ProviderID, name).Count(&exists)
	if exists > 0 {
		return nil, fmt.Errorf("模型 `%s` 已存在", name)
	}

	enabled := true
	if form.IsEnabled != nil {
		enabled = *form.IsEnabled
	}
	displayName := strings.TrimSpace(form.DisplayName)
	if displayName == "" {
		displayName = name
	}

	record := aiModels.InsightLlmModels{
		ProviderID:      provider.ProviderID,
		ModelName:       name,
		DisplayName:     displayName,
		ContextWindow:   form.ContextWindow,
		MaxOutputTokens: form.MaxOutputTokens,
		Capabilities:    marshalJSONOrNull(form.Capabilities),
		IsEnabled:       enabled,
		Source:          aiModels.LlmModelSourceManual,
		Status:          aiModels.LlmModelStatusAvailable,
		LastSeenAt:      commonModels.LocalTime(time.Now()),
	}
	if form.InputPrice != nil {
		record.InputPrice = *form.InputPrice
	}
	if form.OutputPrice != nil {
		record.OutputPrice = *form.OutputPrice
	}
	if form.Remark != nil {
		record.Remark = strings.TrimSpace(*form.Remark)
	}
	if err := global.App.DB.Create(&record).Error; err != nil {
		return nil, err
	}
	ensureProviderDefaultModel(provider.ProviderID)
	return &record, nil
}

// UpdateLlmModel 修改模型配置
func UpdateLlmModel(form *aiForms.LlmModelForm) (*aiModels.InsightLlmModels, error) {
	uid := strings.TrimSpace(form.ModelUID)
	if uid == "" {
		return nil, errors.New("model_uid 不能为空")
	}
	var row aiModels.InsightLlmModels
	if err := global.App.DB.Where("model_uid = ?", uid).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("模型不存在")
		}
		return nil, err
	}

	// 逐字段判断「本次是否提交」：指针字段仅在被显式提交时才更新，
	// 避免前端只切启停时把价格/备注误清空。
	updates := map[string]any{}
	if v := strings.TrimSpace(form.DisplayName); v != "" {
		updates["display_name"] = v
	}
	// 模型名允许重命名，但需校验同 Provider 内不冲突
	if v := strings.TrimSpace(form.ModelName); v != "" && v != row.ModelName {
		var exists int64
		global.App.DB.Model(&aiModels.InsightLlmModels{}).
			Where("provider_id = ? AND model_name = ? AND model_uid <> ?", row.ProviderID, v, uid).
			Count(&exists)
		if exists > 0 {
			return nil, fmt.Errorf("同一服务商下已存在模型 `%s`", v)
		}
		updates["model_name"] = v
	}
	if form.ContextWindow > 0 {
		updates["context_window"] = form.ContextWindow
	}
	if form.MaxOutputTokens > 0 {
		updates["max_output_tokens"] = form.MaxOutputTokens
	}
	if form.Capabilities != nil {
		updates["capabilities"] = marshalJSONOrNull(form.Capabilities)
	}
	if form.InputPrice != nil {
		updates["input_price"] = *form.InputPrice
	}
	if form.OutputPrice != nil {
		updates["output_price"] = *form.OutputPrice
	}
	if form.Remark != nil {
		updates["remark"] = strings.TrimSpace(*form.Remark)
	}
	if form.IsEnabled != nil {
		updates["is_enabled"] = *form.IsEnabled
	}

	if len(updates) == 0 {
		return &row, nil
	}

	if err := global.App.DB.Model(&aiModels.InsightLlmModels{}).
		Where("model_uid = ?", uid).Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := global.App.DB.Where("model_uid = ?", uid).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// DeleteLlmModel 删除模型（被 default_model_id 引用时拒绝）
func DeleteLlmModel(modelUID string) error {
	uid := strings.TrimSpace(modelUID)
	if uid == "" {
		return errors.New("model_uid 不能为空")
	}
	var row aiModels.InsightLlmModels
	if err := global.App.DB.Where("model_uid = ?", uid).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("模型不存在")
		}
		return err
	}

	var referenced int64
	global.App.DB.Model(&aiModels.InsightLlmProviders{}).
		Where("default_model_id = ?", uid).Count(&referenced)
	if referenced > 0 {
		return errors.New("该模型是所属服务商的默认模型，请先切换默认模型再删除")
	}

	if err := global.App.DB.Where("model_uid = ?", uid).Delete(&aiModels.InsightLlmModels{}).Error; err != nil {
		return err
	}
	return nil
}

// SetDefaultLlmModel 设为所属 Provider 的默认模型
func SetDefaultLlmModel(modelUID string) (*aiModels.InsightLlmModels, error) {
	uid := strings.TrimSpace(modelUID)
	if uid == "" {
		return nil, errors.New("model_uid 不能为空")
	}
	var row aiModels.InsightLlmModels
	if err := global.App.DB.Where("model_uid = ?", uid).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("模型不存在")
		}
		return nil, err
	}
	if row.Status != aiModels.LlmModelStatusAvailable {
		return nil, errors.New("该模型当前在厂商侧不可用，无法设为默认")
	}
	if err := setProviderDefaultModel(row.ProviderID, uid); err != nil {
		return nil, err
	}
	if err := global.App.DB.Where("model_uid = ?", uid).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}
