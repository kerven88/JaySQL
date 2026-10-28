package services

import (
	"errors"
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/pagination"

	"github.com/simajiong/jaysql/internal/common/forms"
	"github.com/simajiong/jaysql/internal/common/models"
	inspectModels "github.com/simajiong/jaysql/internal/inspect/models"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

type AdminGetEnvironmentServices struct {
	*forms.AdminGetEnvironmentForm
	C *gin.Context
}

func (s *AdminGetEnvironmentServices) Run() (responseData interface{}, total int64, err error) {
	type environmentItem struct {
		models.InsightInstanceEnvironments
		DataSourceCount uint64   `json:"data_source_count"`
		DataSourceIDs   []uint64 `json:"data_source_ids"`
		SQLStandardName string   `json:"sql_standard_name"`
	}
	var environments []environmentItem
	tx := global.App.DB.
		Table("insight_instance_environments a").
		Select("a.id, a.name, a.color, a.env_type, a.sql_standard_id, a.created_by, a.created_at, a.updated_at, COUNT(i.id) as data_source_count").
		Joins("left join insight_instances i on i.environment = a.id").
		Group("a.id, a.name, a.color, a.env_type, a.sql_standard_id, a.created_by, a.created_at, a.updated_at").
		Order("a.created_at desc, a.id desc")
	// 搜索
	if s.Search != "" {
		tx = tx.Where("a.`name` like ? or a.created_by like ?", "%"+s.Search+"%", "%"+s.Search+"%")
	}
	total = pagination.Pager(&s.PaginationQ, tx, &environments)
	if len(environments) == 0 {
		return &environments, total, nil
	}
	ids := make([]uint64, 0, len(environments))
	for _, item := range environments {
		ids = append(ids, item.ID)
	}
	type binding struct {
		EnvironmentID uint64
		InstanceID    uint64
	}
	var bindings []binding
	if err := global.App.DB.Table("insight_instances").
		Select("environment as environment_id, id as instance_id").
		Where("environment in ?", ids).
		Order("id asc").
		Scan(&bindings).Error; err != nil {
		return nil, total, err
	}
	bindingMap := make(map[uint64][]uint64, len(environments))
	for _, item := range bindings {
		bindingMap[item.EnvironmentID] = append(bindingMap[item.EnvironmentID], item.InstanceID)
	}
	// 批量取规范名称（用于列表展示 SQL 开发规范列）
	standardIDs := make([]uint64, 0, len(environments))
	for _, item := range environments {
		if item.SQLStandardID > 0 {
			standardIDs = append(standardIDs, item.SQLStandardID)
		}
	}
	standardNameMap := make(map[uint64]string)
	if len(standardIDs) > 0 {
		type standardInfo struct {
			ID   uint64
			Name string
		}
		var standards []standardInfo
		global.App.DB.Model(&inspectModels.InsightSQLStandard{}).
			Select("id, name").
			Where("id in ?", standardIDs).
			Scan(&standards)
		for _, s := range standards {
			standardNameMap[s.ID] = s.Name
		}
	}

	for idx := range environments {
		environments[idx].DataSourceIDs = bindingMap[environments[idx].ID]
		environments[idx].SQLStandardName = standardNameMap[environments[idx].SQLStandardID]
		if strings.TrimSpace(environments[idx].Color) == "" {
			environments[idx].Color = "#1677ff"
		}
	}
	return &environments, total, nil
}

type AdminCreateEnvironmentService struct {
	*forms.AdminCreateEnvironmentForm
	C *gin.Context
}

func (s *AdminCreateEnvironmentService) Run() error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		// 创建环境必须显式指定继承的 SQL 开发规范：
		// 环境是「数据源 → 规范」的唯一中间层，缺规范会让该环境下的数据源无规范可继承，
		// 因此这里硬校验，不再回退默认规范（前端仍会按环境类型自动推荐并允许改选）。
		if s.SQLStandardID == nil || *s.SQLStandardID == 0 {
			return fmt.Errorf("请选择SQL开发规范：创建环境时必须为环境指定继承的SQL开发规范")
		}
		standard, err := validateSQLStandardForOrder(tx, *s.SQLStandardID)
		if err != nil {
			return err
		}
		standardID := standard.ID
		db := models.InsightInstanceEnvironments{
			Name:          s.Name,
			Color:         normalizeEnvironmentColor(s.Color),
			EnvType:       s.EnvType,
			SQLStandardID: standardID,
			CreatedBy:     currentAuditUsername(s.C),
		}
		result := tx.Model(&models.InsightInstanceEnvironments{}).Create(&db)
		if result.Error != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(result.Error, &mysqlErr) {
				switch mysqlErr.Number {
				case 1062:
					return fmt.Errorf("记录`%s`已存在", s.Name)
				}
			}
			return result.Error
		}
		if err := syncEnvironmentBindings(tx, db.ID, s.DataSourceIDs); err != nil {
			return err
		}
		// 新环境绑定的数据源统一继承环境级 SQL 规范
		return syncEnvironmentStandardBindings(tx, db.ID, standardID)
	})
}

type AdminUpdateEnvironmentService struct {
	*forms.AdminUpdateEnvironmentForm
	C  *gin.Context
	ID uint64
}

func (s *AdminUpdateEnvironmentService) Run() error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		// 先查询当前环境，判断规范是否发生变化
		var env models.InsightInstanceEnvironments
		if err := tx.First(&env, s.ID).Error; err != nil {
			return err
		}

		updates := map[string]interface{}{
			"name":  s.Name,
			"color": normalizeEnvironmentColor(s.Color),
		}
		// env_type 非空时才更新，避免数据源管理器保存时误清空
		envTypeChanged := false
		if strings.TrimSpace(s.EnvType) != "" {
			updates["env_type"] = s.EnvType
			envTypeChanged = s.EnvType != env.EnvType
		}

		standardID := env.SQLStandardID
		standardChanged := false
		if s.SQLStandardID != nil && *s.SQLStandardID > 0 {
			if *s.SQLStandardID != env.SQLStandardID {
				standard, err := validateSQLStandardForOrder(tx, *s.SQLStandardID)
				if err != nil {
					return err
				}
				updates["sql_standard_id"] = standard.ID
				standardID = standard.ID
				standardChanged = true
			}
		} else if envTypeChanged {
			// 仅环境类型变化且未显式指定规范时，按类型自动解析默认规范
			newStandardID, err := resolveDefaultStandardID(tx, s.EnvType)
			if err != nil {
				return err
			}
			updates["sql_standard_id"] = newStandardID
			standardID = newStandardID
			standardChanged = true
		}

		result := tx.Model(&models.InsightInstanceEnvironments{}).Where("id=?", s.ID).Updates(updates)
		if result.Error != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(result.Error, &mysqlErr) {
				switch mysqlErr.Number {
				case 1062:
					return fmt.Errorf("记录`%s`已存在", s.Name)
				}
			}
			return result.Error
		}

		if s.DataSourceIDs != nil {
			if err := syncEnvironmentBindings(tx, s.ID, s.DataSourceIDs); err != nil {
				return err
			}
		}

		// 数据源变化或规范变化时，把该环境下所有实例的规范绑定同步到环境级规范
		if (s.DataSourceIDs != nil || standardChanged) && standardID > 0 {
			if err := syncEnvironmentStandardBindings(tx, s.ID, standardID); err != nil {
				return err
			}
		}
		return nil
	})
}

type AdminDeleteEnvironmentService struct {
	C  *gin.Context
	ID uint64
}

func (s *AdminDeleteEnvironmentService) Run() error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.InsightInstances{}).Where("environment = ?", s.ID).Update("environment", nil).Error; err != nil {
			return err
		}
		result := tx.Where("id=?", s.ID).Delete(&models.InsightInstanceEnvironments{})
		if result.Error != nil {
			return result.Error
		}
		return nil
	})
}

// validateSQLStandardForOrder 校验规范是否可用于提交工单流程。
// 条件：规范存在、已启用，且至少包含一条 level in ('must','suggest') 且 implemented=true 的规则。
func validateSQLStandardForOrder(tx *gorm.DB, standardID uint64) (*inspectModels.InsightSQLStandard, error) {
	if standardID == 0 {
		return nil, fmt.Errorf("请选择SQL开发规范")
	}
	var standard inspectModels.InsightSQLStandard
	if err := tx.First(&standard, standardID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("所选SQL开发规范不存在或已被删除")
		}
		return nil, err
	}
	if !standard.Enabled {
		return nil, fmt.Errorf("所选SQL开发规范(%s)已停用，请选择其他规范", standard.Name)
	}
	var count int64
	if err := tx.Model(&inspectModels.InsightSQLStandardRule{}).
		Where("standard_id = ? and level in (?) and implemented = ?", standardID, []string{"must", "suggest"}, true).
		Count(&count).Error; err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("所选SQL开发规范(%s)未配置可用于提交工单审核的规则，请选择其他规范", standard.Name)
	}
	return &standard, nil
}

// resolveDefaultStandardID 按环境类型解析该环境应继承的 SQL 开发规范ID
// dev -> 开发规范，prod -> 生产规范；取启用的规范中「系统默认优先、排序靠前」的一条
func resolveDefaultStandardID(tx *gorm.DB, envType string) (uint64, error) {
	envType = strings.TrimSpace(envType)
	if envType == "" {
		return 0, fmt.Errorf("环境类型不能为空")
	}
	var standard inspectModels.InsightSQLStandard
	err := tx.Model(&inspectModels.InsightSQLStandard{}).
		Select("id").
		Where("env_type = ? and enabled = ?", envType, true).
		Order("is_default desc, sort_order asc, id asc").
		First(&standard).Error
	if err == nil {
		return standard.ID, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, fmt.Errorf("未找到%s对应的SQL开发规范，请先在「系统管理 -> SQL规范」中创建", envTypeLabel(envType))
	}
	return 0, err
}

func envTypeLabel(envType string) string {
	switch envType {
	case "dev":
		return "开发环境"
	case "prod":
		return "生产环境"
	default:
		return "该环境类型"
	}
}

// syncEnvironmentStandardBindings 让指定环境下的所有实例统一继承环境级 SQL 开发规范
// 实例不单独配置规范，全部跟随所在环境
func syncEnvironmentStandardBindings(tx *gorm.DB, environmentID uint64, standardID uint64) error {
	if standardID == 0 {
		return nil
	}
	var instanceIDs []string
	if err := tx.Model(&models.InsightInstances{}).
		Where("environment = ?", environmentID).
		Pluck("instance_id", &instanceIDs).Error; err != nil {
		return err
	}
	for _, instanceID := range instanceIDs {
		instanceID = strings.TrimSpace(instanceID)
		if instanceID == "" {
			continue
		}
		binding := inspectModels.InsightInstanceSQLStandardBinding{
			InstanceID: instanceID,
			StandardID: standardID,
		}
		if err := tx.Where("instance_id = ?", instanceID).
			Assign(inspectModels.InsightInstanceSQLStandardBinding{StandardID: standardID}).
			FirstOrCreate(&binding).Error; err != nil {
			return err
		}
	}
	return nil
}

// syncInstanceStandardBinding 按实例当前所属环境，把该实例的 SQL 开发规范绑定对齐到环境级规范。
//
// 规则（唯一口径）：数据源只要归属于某个环境，就必须继承该环境的开发规范；
// 实例不单独配置规范，规范由环境决定。
//   - environmentID <= 0（未归属任何环境）时不做处理，也不清除已有绑定；
//   - 环境未绑定规范（sql_standard_id = 0）时无可继承，跳过；
//   - 其余情况幂等 upsert（instance_id 唯一），保证「新建数据源」「改环境」两种入口都会落库。
func syncInstanceStandardBinding(tx *gorm.DB, instanceID string, environmentID int) error {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" || environmentID <= 0 {
		return nil
	}
	var env models.InsightInstanceEnvironments
	if err := tx.Select("id", "sql_standard_id").First(&env, environmentID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("所属环境不存在或已被删除，请重新选择环境")
		}
		return err
	}
	if env.SQLStandardID == 0 {
		return nil
	}
	binding := inspectModels.InsightInstanceSQLStandardBinding{InstanceID: instanceID}
	return tx.Where("instance_id = ?", instanceID).
		Assign(inspectModels.InsightInstanceSQLStandardBinding{StandardID: env.SQLStandardID}).
		FirstOrCreate(&binding).Error
}

// InitializeDefaultEnvironments 首次启动（环境表为空）时创建「开发」「生产」两个默认环境，
// 并分别绑定对应环境类型的默认 SQL 开发规范。
//
// 幂等：环境表已有任意数据即不再播种；规范缺失时不阻断启动（由调用方记录告警）。
func InitializeDefaultEnvironments(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&models.InsightInstanceEnvironments{}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		presets := []struct {
			Name    string
			Color   string
			EnvType string
		}{
			{Name: "开发", Color: "#52c41a", EnvType: "dev"},
			{Name: "生产", Color: "#ff4d4f", EnvType: "prod"},
		}
		for _, preset := range presets {
			standardID, err := resolveDefaultStandardID(tx, preset.EnvType)
			if err != nil {
				return err
			}
			env := models.InsightInstanceEnvironments{
				Name:          preset.Name,
				Color:         normalizeEnvironmentColor(preset.Color),
				EnvType:       preset.EnvType,
				SQLStandardID: standardID,
				CreatedBy:     "system",
			}
			if err := tx.Create(&env).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func normalizeEnvironmentColor(color string) string {
	color = strings.TrimSpace(color)
	if color == "" {
		return "#1677ff"
	}
	if !strings.HasPrefix(color, "#") {
		color = "#" + color
	}
	if len(color) != 7 {
		return "#1677ff"
	}
	return strings.ToLower(color)
}

func syncEnvironmentBindings(tx *gorm.DB, environmentID uint64, dataSourceIDs []uint64) error {
	if err := tx.Model(&models.InsightInstances{}).
		Where("environment = ?", environmentID).
		Update("environment", nil).Error; err != nil {
		return err
	}
	if len(dataSourceIDs) == 0 {
		return nil
	}
	result := tx.Model(&models.InsightInstances{}).
		Where("id in ?", dataSourceIDs).
		Update("environment", environmentID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(uniqueUint64s(dataSourceIDs))) {
		return fmt.Errorf("部分数据源不存在，绑定失败")
	}
	return nil
}

func uniqueUint64s(values []uint64) []uint64 {
	result := make([]uint64, 0, len(values))
	seen := make(map[uint64]struct{}, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
