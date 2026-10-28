package services

import (
	"errors"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"gorm.io/gorm"
)

type RiskDismissedService struct {
	C        interface{}
	Username string
}

type DismissedRiskRow struct {
	RiskType string `json:"risk_type"`
	RiskKey  string `json:"risk_key"`
}

func (s *RiskDismissedService) normalizeRiskKey(riskType, riskKey string) (string, string) {
	return strings.TrimSpace(riskType), strings.TrimSpace(riskKey)
}

func (s *RiskDismissedService) validRiskType(riskType string) bool {
	riskType = strings.TrimSpace(riskType)
	switch riskType {
	case "instance_conn", "manual_backup_fail", "restore_fail", "node_offline",
		"instance_no_backup", "daily_backup_miss", "order_fail",
		"todo_order_submit", "todo_order_approve", "todo_order_execute", "todo_backup_enable":
		return true
	}
	return false
}

func (s *RiskDismissedService) GetDismissedKeys(riskTypes []string) (map[string]map[string]struct{}, error) {
	typeSet := make(map[string]struct{}, len(riskTypes))
	wanted := make([]string, 0, len(riskTypes))
	for _, t := range riskTypes {
		t = strings.TrimSpace(t)
		if t == "" || !s.validRiskType(t) {
			continue
		}
		if _, ok := typeSet[t]; ok {
			continue
		}
		typeSet[t] = struct{}{}
		wanted = append(wanted, t)
	}
	result := make(map[string]map[string]struct{})
	if len(wanted) == 0 {
		return result, nil
	}
	var rows []DismissedRiskRow
	username := strings.TrimSpace(s.Username)
	if err := global.App.DB.Model(&commonModels.InsightRiskDismissed{}).
		Select("risk_type, risk_key").
		Where("risk_type IN ? AND deleted_at IS NULL AND created_by = ?", wanted, username).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		rt := strings.TrimSpace(row.RiskType)
		rk := strings.TrimSpace(row.RiskKey)
		if rt == "" || rk == "" {
			continue
		}
		if len(rk) < 6 {
			continue
		}
		if _, ok := result[rt]; !ok {
			result[rt] = make(map[string]struct{})
		}
		result[rt][rk] = struct{}{}
	}
	return result, nil
}

func (s *RiskDismissedService) Dismiss(riskType, riskKey, remark string) error {
	riskType, riskKey = s.normalizeRiskKey(riskType, riskKey)
	if riskType == "" || riskKey == "" {
		return errors.New("risk_type 和 risk_key 不能为空")
	}
	if !s.validRiskType(riskType) {
		return errors.New("不支持的风险类型")
	}
	username := strings.TrimSpace(s.Username)
	if username == "" {
		return errors.New("缺少登录身份")
	}
	now := commonModels.LocalTime(time.Now())
	var existing commonModels.InsightRiskDismissed
	err := global.App.DB.Unscoped().
		Where("risk_type = ? AND risk_key = ?", riskType, riskKey).
		First(&existing).Error
	if err == nil {
		if existing.DeletedAt.Valid {
			return global.App.DB.Unscoped().
				Model(&existing).
				Updates(map[string]interface{}{
					"deleted_at": nil,
					"updated_by": username,
					"updated_at": now,
				}).Error
		}
		updates := map[string]interface{}{
			"updated_by": username,
			"updated_at": now,
		}
		if strings.TrimSpace(remark) != "" {
			updates["remark"] = strings.TrimSpace(remark)
		}
		return global.App.DB.Model(&existing).Updates(updates).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	row := commonModels.InsightRiskDismissed{
		CreatedAt: now,
		UpdatedAt: now,
		RiskType:  riskType,
		RiskKey:   riskKey,
		CreatedBy: username,
		UpdatedBy: username,
		Remark:    strings.TrimSpace(remark),
	}
	return global.App.DB.Create(&row).Error
}

func (s *RiskDismissedService) Undismiss(riskType, riskKey string) error {
	riskType, riskKey = s.normalizeRiskKey(riskType, riskKey)
	if riskType == "" || riskKey == "" {
		return errors.New("risk_type 和 risk_key 不能为空")
	}
	if !s.validRiskType(riskType) {
		return errors.New("不支持的风险类型")
	}
	username := strings.TrimSpace(s.Username)
	if username == "" {
		return errors.New("缺少登录身份")
	}
	res := global.App.DB.Model(&commonModels.InsightRiskDismissed{}).
		Where("risk_type = ? AND risk_key = ?", riskType, riskKey).
		Delete(&commonModels.InsightRiskDismissed{})
	if res.Error != nil {
		return res.Error
	}
	return nil
}

type DismissedKeysResponse struct {
	RiskTypes []string                               `json:"risk_types"`
	Data      map[string]map[string]struct{}         `json:"data"`
}
