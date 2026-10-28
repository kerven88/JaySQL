package services

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/performance/forms"
	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
	"github.com/simajiong/jaysql/pkg/pagination"
)

type snapshotParamListItem struct {
	ID                    uint64                `json:"id"`
	Name                  string                `json:"name"`
	DBType                commonModels.EnumType `json:"db_type"`
	ScopeType             string                `json:"scope_type"`
	ScopeValue            string                `json:"scope_value"`
	InstanceRemark        string                `json:"instance_remark"`
	SlowSQLThresholdMS    int                   `json:"slow_sql_threshold_ms"`
	SnapshotWindowMinutes int                   `json:"snapshot_window_minutes"`
	TopSQLLimit           int                   `json:"top_sql_limit"`
	RetentionDays         int                   `json:"retention_days"`
	Enabled               bool                  `json:"enabled"`
	Remark                string                `json:"remark"`
	CreatedAt             string                `json:"created_at"`
	UpdatedAt             string                `json:"updated_at"`
}

type GetSnapshotParamListService struct {
	Form *forms.GetSnapshotParamListForm
	C    *gin.Context
}

func (s *GetSnapshotParamListService) Run() ([]snapshotParamListItem, int64, error) {
	form := s.Form
	if !form.IsPage {
		form.IsPage = true
	}
	if form.PageSize <= 0 {
		form.PageSize = 10
	}
	if form.Page <= 0 {
		form.Page = 1
	}

	var list []snapshotParamListItem
	tx := global.App.DB.Table("insight_performance_snapshot_params p").
		Select(`
			p.id,
			p.name,
			p.db_type,
			p.scope_type,
			p.scope_value,
			ifnull(i.remark, '') as instance_remark,
			p.slow_sql_threshold_ms,
			p.snapshot_window_minutes,
			p.top_sql_limit,
			p.retention_days,
			p.enabled,
			p.remark,
			DATE_FORMAT(p.created_at, '%Y-%m-%d %H:%i:%s') as created_at,
			DATE_FORMAT(p.updated_at, '%Y-%m-%d %H:%i:%s') as updated_at
		`).
		Joins("left join insight_instances i on i.instance_id = p.scope_value").
		Order("p.updated_at desc, p.id desc")

	if keyword := strings.TrimSpace(form.Search); keyword != "" {
		like := "%" + keyword + "%"
		tx = tx.Where("p.name like ? or p.remark like ? or p.scope_value like ? or ifnull(i.remark,'') like ?", like, like, like, like)
	}
	if value := strings.TrimSpace(form.DBType); value != "" {
		tx = tx.Where("p.db_type = ?", value)
	}
	if value := strings.TrimSpace(form.ScopeType); value != "" {
		tx = tx.Where("p.scope_type = ?", value)
	}
	if value := strings.TrimSpace(form.InstanceID); value != "" {
		tx = tx.Where("p.scope_value = ?", value)
	}

	total := pagination.Pager(&form.Pagination, tx, &list)
	return list, total, nil
}

type CreateSnapshotParamService struct {
	Form *forms.SaveSnapshotParamForm
	C    *gin.Context
}

func (s *CreateSnapshotParamService) Run() error {
	model, err := buildSnapshotParamModel(s.Form)
	if err != nil {
		return err
	}
	return global.App.DB.Create(model).Error
}

type UpdateSnapshotParamService struct {
	ID   uint64
	Form *forms.SaveSnapshotParamForm
	C    *gin.Context
}

func (s *UpdateSnapshotParamService) Run() error {
	var existing performanceModels.InsightPerformanceSnapshotParams
	if err := global.App.DB.Where("id = ?", s.ID).Take(&existing).Error; err != nil {
		return err
	}
	model, err := buildSnapshotParamModel(s.Form)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"name":                    model.Name,
		"db_type":                 model.DBType,
		"scope_type":              model.ScopeType,
		"scope_value":             model.ScopeValue,
		"slow_sql_threshold_ms":   model.SlowSQLThresholdMS,
		"snapshot_window_minutes": model.SnapshotWindowMinutes,
		"top_sql_limit":           model.TopSQLLimit,
		"retention_days":          model.RetentionDays,
		"enabled":                 model.Enabled,
		"remark":                  model.Remark,
	}
	return global.App.DB.Model(&existing).Updates(updates).Error
}

type DeleteSnapshotParamService struct {
	ID uint64
	C  *gin.Context
}

func (s *DeleteSnapshotParamService) Run() error {
	return global.App.DB.Delete(&performanceModels.InsightPerformanceSnapshotParams{}, s.ID).Error
}

func buildSnapshotParamModel(form *forms.SaveSnapshotParamForm) (*performanceModels.InsightPerformanceSnapshotParams, error) {
	scopeValue := strings.TrimSpace(form.ScopeValue)
	if form.ScopeType == "instance" {
		if scopeValue == "" {
			return nil, fmt.Errorf("实例级参数必须选择实例")
		}
		var instance struct {
			InstanceID string
			DBType     string
		}
		if err := global.App.DB.Table("insight_instances").
			Select("instance_id, db_type").
			Where("instance_id = ?", scopeValue).
			Take(&instance).Error; err != nil {
			return nil, fmt.Errorf("实例不存在")
		}
		if !isSupportedDBType(instance.DBType) {
			return nil, fmt.Errorf("当前性能分析模块仅支持 MySQL / PostgreSQL")
		}
		if strings.TrimSpace(instance.DBType) != strings.TrimSpace(form.DBType) {
			return nil, fmt.Errorf("参数 DB 类型与实例类型不一致")
		}
	} else {
		scopeValue = ""
	}

	return &performanceModels.InsightPerformanceSnapshotParams{
		Name:                  strings.TrimSpace(form.Name),
		DBType:                commonModels.EnumType(strings.TrimSpace(form.DBType)),
		ScopeType:             strings.TrimSpace(form.ScopeType),
		ScopeValue:            scopeValue,
		SlowSQLThresholdMS:    form.SlowSQLThresholdMS,
		SnapshotWindowMinutes: form.SnapshotWindowMinutes,
		TopSQLLimit:           form.TopSQLLimit,
		RetentionDays:         form.RetentionDays,
		Enabled:               form.Enabled,
		Remark:                strings.TrimSpace(form.Remark),
	}, nil
}
