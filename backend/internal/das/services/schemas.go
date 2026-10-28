package services

import (
	"strings"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
)

// filterSystemSchemaResults 按系统库可见性规则过滤库列表结果（原地压缩）。
func filterSystemSchemaResults(gate *systemSchemaGate, items []schemaResult) []schemaResult {
	if gate == nil || len(items) == 0 {
		return items
	}
	kept := items[:0]
	for i := range items {
		if gate.hidden(items[i].InstanceID, items[i].DbType, items[i].Schema) {
			continue
		}
		kept = append(kept, items[i])
	}
	return kept
}

type schemaResult struct {
	InstanceID   string `json:"instance_id"`
	Schema       string `json:"schema"`
	ParentPath   string `json:"parent_path"`
	ResourceType string `json:"resource_type"`
	DatabaseName string `json:"database_name"`
	SchemaName   string `json:"schema_name"`
	DisplayName  string `json:"display_name"`
	DbType       string `json:"db_type"`
	Hostname     string `json:"hostname"`
	Port         int    `json:"port"`
	IsDeleted    bool   `json:"is_deleted"`
	Remark       string `json:"remark"`
}

type GetSchemasService struct {
	C        *gin.Context
	Username string
}

// 获取用户授权的库
func (s *GetSchemasService) Run() ([]schemaResult, error) {
	scope, err := resolveDASActorScope(s.Username)
	if err != nil {
		return nil, err
	}
	var results []schemaResult
	baseSelect := "a.`instance_id`, b.`db_type`, a.`schema`, c.parent_path, a.`resource_type`, ifnull(c.display_name, a.`schema`) as display_name, b.`hostname`, b.`port`, c.`is_deleted`, b.`remark`"
	tx := global.App.DB.Table("`insight_das_schema_perms` a").
		Select(baseSelect).
		Joins("join `insight_instances` b on a.instance_id = b.instance_id").
		Joins("join `insight_instance_schemas` c on b.instance_id = c.instance_id and a.`schema` = c.`schema` and a.resource_type = c.resource_type").
		Where(`
			(
				(b.db_type in ? and c.resource_type = 'database')
				or (b.db_type in ? and c.resource_type = 'schema')
				or (b.db_type in ? and c.resource_type in ('database','schema'))
			)
		`, []string{"MySQL", "TiDB", "ClickHouse", "Doris", "OceanBase"}, []string{"PostgreSQL", "SQLServer"}, []string{"Oracle", "OceanBase"})
	// 【2026-10-15 收口】不再按超管身份切换成「拉全量 schema」的查询：
	// 无论谁查，都统一走主体可见性过滤（新模型下即 insight_das_data_grants 的 EXISTS）。
	tx = applyPrincipalFilter(tx, "a", scope)
	tx.Group("a.`instance_id`, b.`db_type`, a.`schema`, c.parent_path, a.`resource_type`, c.display_name, b.`hostname`, b.`port`, c.is_deleted, b.`remark`").
		Scan(&results)
	// 【2026-10-15】全实例授权 + 非 admin：剔除引擎自带系统库（见 system_schema.go）。
	// 与资源树共用同一个 gate，保证两个出口判定口径完全一致。
	if len(results) > 0 {
		results = filterSystemSchemaResults(newSystemSchemaGate(scope), results)
	}
	for i := range results {
		if IsPostgresFamily(results[i].DbType) || strings.EqualFold(results[i].DbType, "sqlserver") {
			databaseName, schemaName, displayName := deriveDatabaseAndSchemaNames(results[i].Schema, results[i].ParentPath, results[i].DisplayName)
			results[i].DatabaseName = databaseName
			results[i].SchemaName = schemaName
			if results[i].ResourceType == "schema" {
				results[i].DisplayName = displayName
			}
		}
	}
	return results, nil
}
