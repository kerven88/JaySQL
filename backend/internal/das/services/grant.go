package services

import (
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/internal/das/forms"

	"github.com/gin-gonic/gin"
)

type GetUserGrantsService struct {
	*forms.UserGrantsForm
	C        *gin.Context
	Username string
}

func (s *GetUserGrantsService) filter(data []map[string]string) (result []map[string]string) {
	var hasAllowedRule bool = false
	for _, i := range data {
		if i["rule"] == "allow" {
			hasAllowedRule = true
			break
		}
	}
	// 如果有allow规则，就移除deny规则
	if hasAllowedRule {
		for _, i := range data {
			if i["rule"] == "allow" {
				result = append(result, i)
			}
		}
	} else {
		// 如果没有allow规则，就保留deny规则
		return data
	}
	return result
}

func (s *GetUserGrantsService) format(schema, resourceType, displayName, tables string) *map[string]interface{} {
	var returnData map[string]interface{} = map[string]interface{}{}
	returnData["schema"] = schema
	returnData["resource_type"] = resourceType
	returnData["display_name"] = displayName
	if tables == "" {
		returnData["tables"] = "*"
	} else {
		var tmpTables []map[string]string = []map[string]string{}
		for _, i := range strings.Split(tables, ",") {
			val := strings.Split(i, ":")
			tmpTables = append(tmpTables, map[string]string{"table": val[0], "rule": val[1]})
		}
		returnData["tables"] = s.filter(tmpTables)
	}
	return &returnData
}

func (s *GetUserGrantsService) Run() (*map[string]interface{}, error) {
	scope, err := resolveDASActorScope(s.Username)
	if err != nil {
		return nil, err
	}
	instanceID := strings.TrimSpace(s.InstanceID)
	if instanceID == "" {
		return nil, fmt.Errorf("instance id is empty")
	}
	instanceGranted, err := hasDASInstanceGrant(scope, instanceID)
	if err != nil {
		return nil, err
	}
	type schemaGrant struct {
		Schema       string
		ResourceType string
		ParentPath   string
		DBType       string
		DatabaseName string
		SchemaName   string
		DisplayName  string
	}
	var current schemaGrant
	// 【2026-10-15 收口】原「超管直接拉全量」的旁路分支已移除：
	// 超管与普通人一样，要么命中实例级授权，要么逐库走显式授权判定。
	if instanceGranted {
		tx := global.App.DB.Table("insight_instance_schemas m").
			Select("m.schema, m.resource_type, m.parent_path, i.db_type, ifnull(m.display_name, m.schema) as display_name").
			Joins("join insight_instances i on m.instance_id = i.instance_id").
			Where("m.instance_id=? and m.`schema`=? and m.is_deleted=0", s.InstanceID, s.Schema)
		if s.ResourceType != "" {
			tx = tx.Where("m.resource_type=?", s.ResourceType)
		}
		if err := tx.Take(&current).Error; err != nil {
			return s.format("", "", "", ""), nil
		}
		if IsPostgresFamily(current.DBType) || strings.EqualFold(current.DBType, "sqlserver") {
			current.DatabaseName, current.SchemaName, current.DisplayName = deriveDatabaseAndSchemaNames(current.Schema, current.ParentPath, current.DisplayName)
		}
		return s.format(current.Schema, current.ResourceType, current.DisplayName, ""), nil
	}

	// 【2026-10-15 改造】新授权模型下，明细直接来自 insight_das_data_grants：
	// 先判断该库是否有显式授权（实例级在上面的 instanceGranted 分支已返回），
	// 再从新表取该库下的表级 allow / deny 明细。
	if dataGrantEnabled() {
		granted, err := HasDataGrant(scope, s.InstanceID, s.Schema, "", DataActionSelect)
		if err != nil {
			return nil, err
		}
		if !granted {
			// 只授了库内某张表时，库本身仍要可见（否则树里连这个库都没有，
			// 用户被授予的表根本点不到）；下面的表级明细会把范围收敛到已授权表。
			granted, err = hasTableLevelGrantInSchema(scope, s.InstanceID, s.Schema, DataActionSelect)
			if err != nil {
				return nil, err
			}
		}
		if !granted {
			// 未授权：与旧逻辑一致，返回空（前端不展示该库/表）
			return s.format("", "", "", ""), nil
		}
		tx := global.App.DB.Table("insight_instance_schemas m").
			Select("m.schema, m.resource_type, m.parent_path, i.db_type, ifnull(m.display_name, m.schema) as display_name").
			Joins("join insight_instances i on m.instance_id = i.instance_id").
			Where("m.instance_id=? and m.`schema`=? and m.is_deleted=0", s.InstanceID, s.Schema)
		if s.ResourceType != "" {
			tx = tx.Where("m.resource_type=?", s.ResourceType)
		}
		if err := tx.Take(&current).Error; err != nil {
			return s.format("", "", "", ""), nil
		}
		if IsPostgresFamily(current.DBType) || strings.EqualFold(current.DBType, "sqlserver") {
			current.DatabaseName, current.SchemaName, current.DisplayName = deriveDatabaseAndSchemaNames(current.Schema, current.ParentPath, current.DisplayName)
		}

		type tableGrant struct {
			Table     string
			Rule      string
			TableMode string
		}
		var tableGrants []tableGrant
		now := time.Now()
		tableTx := global.App.DB.Table("insight_das_data_grants g").
			Select("g.table_name as `table`, g.effect as rule, ifnull(g.table_mode, 'exact') as table_mode").
			Where("g.instance_id = ? and g.schema_name = ? and g.resource_level = ?", s.InstanceID, s.Schema, DataResourceLevelTable).
			Where("g.status = ?", DataGrantStatusActive).
			Where("(g.effective_at IS NULL OR g.effective_at <= ?)", now).
			Where("(g.expire_at IS NULL OR g.expire_at > ?)", now).
			Order("g.table_name asc")
		tableTx = applyDataGrantPrincipalFilter(tableTx, "g", scope)
		if err := tableTx.Scan(&tableGrants).Error; err != nil {
			return nil, err
		}
		if len(tableGrants) == 0 {
			return s.format(current.Schema, current.ResourceType, current.DisplayName, ""), nil
		}
		// 2026-10-23：表级明细改为结构化输出（table_grants，带 table_mode），
		// 前端据此做 exact / wildcard / except 三种口径的匹配。
		// 旧 tables 字符串（"name:rule" 逗号拼接）只放 exact 行：except 行的
		// table_name 是逗号拼接的排除清单，混进旧格式会把 format 的切分逻辑打穿
		// （无冒号段取 val[1] 直接 panic），通配行也匹配不上任何真实表名。
		detail := make([]map[string]string, 0, len(tableGrants))
		exactMaps := make([]map[string]string, 0, len(tableGrants))
		for _, item := range tableGrants {
			mode := strings.ToLower(strings.TrimSpace(item.TableMode))
			if mode == "" {
				mode = "exact"
			}
			detail = append(detail, map[string]string{
				"table":      item.Table,
				"rule":       item.Rule,
				"table_mode": mode,
			})
			if mode == "exact" {
				exactMaps = append(exactMaps, map[string]string{"table": item.Table, "rule": item.Rule})
			}
		}
		result := s.format(current.Schema, current.ResourceType, current.DisplayName, "")
		(*result)["table_grants"] = detail
		(*result)["tables"] = s.filter(exactMaps)
		return result, nil
	}

	tx := global.App.DB.Table("insight_das_schema_perms s").
		Select("s.schema, s.resource_type, m.parent_path, i.db_type, ifnull(m.display_name, s.schema) as display_name").
		Joins("join insight_instances i on s.instance_id = i.instance_id").
		Joins("left join insight_instance_schemas m on s.instance_id = m.instance_id and s.schema = m.schema and s.resource_type = m.resource_type").
		Where("s.instance_id=? and s.`schema`=?", s.InstanceID, s.Schema)
	if s.ResourceType != "" {
		tx = tx.Where("s.resource_type=?", s.ResourceType)
	}
	tx = applyPrincipalFilter(tx, "s", scope)
	if err := tx.Take(&current).Error; err != nil {
		return s.format("", "", "", ""), nil
	}
	if IsPostgresFamily(current.DBType) || strings.EqualFold(current.DBType, "sqlserver") {
		current.DatabaseName, current.SchemaName, current.DisplayName = deriveDatabaseAndSchemaNames(current.Schema, current.ParentPath, current.DisplayName)
	}

	type tableGrant struct {
		Table string
		Rule  string
	}
	var tableGrants []tableGrant
	tableTx := global.App.DB.Table("insight_das_table_perms t").
		Select("t.table, t.rule").
		Where("t.instance_id=? and t.`schema`=?", s.InstanceID, s.Schema).
		Order("t.table asc")
	if current.ResourceType != "" {
		tableTx = tableTx.Where("t.resource_type = ?", current.ResourceType)
	}
	tableTx = applyPrincipalFilter(tableTx, "t", scope)
	if err := tableTx.Scan(&tableGrants).Error; err != nil {
		return nil, err
	}
	if len(tableGrants) == 0 {
		return s.format(current.Schema, current.ResourceType, current.DisplayName, ""), nil
	}
	parts := make([]string, 0, len(tableGrants))
	for _, item := range tableGrants {
		parts = append(parts, item.Table+":"+item.Rule)
	}
	return s.format(current.Schema, current.ResourceType, current.DisplayName, strings.Join(parts, ",")), nil
}
