package services

import (
	"errors"
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/das/parser"

	"gorm.io/gorm"
)

type CheckUserPerm struct {
	UserName   string
	InstanceID string
	Tables     []parser.Table
}

func (p CheckUserPerm) getScope() (*dasActorScope, error) {
	return resolveDASActorScope(p.UserName)
}

func (p CheckUserPerm) resolveResourceType(schemaPath string) string {
	type schemaResource struct {
		ResourceType string
	}
	var result schemaResource
	err := global.App.DB.Table("`insight_instance_schemas`").
		Select("resource_type").
		Where("instance_id=? and `schema`=? and is_deleted=0", p.InstanceID, schemaPath).
		Take(&result).Error
	if err == nil && result.ResourceType != "" {
		return result.ResourceType
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		global.App.Log.Error(err.Error())
	}
	if strings.Contains(schemaPath, ".") {
		return "schema"
	}
	return "database"
}

func isBuiltinSchema(schemaPath string) bool {
	lowerPath := strings.ToLower(schemaPath)
	if lowerPath == "information_schema" {
		return true
	}
	return strings.HasSuffix(lowerPath, ".information_schema")
}

func shouldSkipSchemaPermCheck(schemaPath string) bool {
	trimmed := strings.TrimSpace(schemaPath)
	if trimmed == "" {
		return true
	}
	return isBuiltinSchema(trimmed)
}

func (p CheckUserPerm) checkHasAllTablePerms(scope *dasActorScope) bool {
	// 表das_user_table_permissions中没有找到username=? and instance_id=? and `schema`=?的记录
	for _, t := range p.Tables {
		resourceType := p.resolveResourceType(t.Schema)
		type countResult struct {
			Count int
		}
		var result countResult
		tx := global.App.DB.Table("`insight_das_table_perms` t").
			Select("count(*) as count").
			Where("t.instance_id=? and t.`schema`=? and t.resource_type=?", p.InstanceID, t.Schema, resourceType)
		applyPrincipalFilter(tx, "t", scope).Scan(&result)
		if result.Count == 0 {
			return true
		}
	}
	return false
}

func (p CheckUserPerm) checkOnlyHasAllowTablePerms(scope *dasActorScope) []string {
	// 仅有allow规则的记录
	var errMsg []string
	for _, t := range p.Tables {
		resourceType := p.resolveResourceType(t.Schema)
		type ruleResult struct {
			Table string
		}
		var result []ruleResult
		tx := global.App.DB.Table("`insight_das_table_perms` t").
			Select("*").
			Where("t.instance_id=? and t.`schema`=? and t.resource_type=? and t.rule='allow'", p.InstanceID, t.Schema, resourceType)
		applyPrincipalFilter(tx, "t", scope).Scan(&result)
		if len(result) > 0 {
			// 结果集大于0，表示有allow规则的记录
			var tmpTables []string
			for _, r := range result {
				tmpTables = append(tmpTables, r.Table)
			}
			// 判断提取的表名t1是否在匹配出来的表集合里面["work_days", "t1"]
			if !utils.IsContain(tmpTables, t.Table) {
				errMsg = append(errMsg, fmt.Sprintf("您没有表`%s`.`%s`权限", t.Schema, t.Table))
			}
		}
	}
	return errMsg
}

func (p CheckUserPerm) checkOnlyHasDenyTablePerms(scope *dasActorScope) []string {
	var errMsg []string
	for _, t := range p.Tables {
		resourceType := p.resolveResourceType(t.Schema)
		type ruleResult struct {
			Table string
		}
		var result []ruleResult
		tx := global.App.DB.Table("`insight_das_table_perms` t").
			Select("*").
			Where("t.instance_id=? and t.`schema`=? and t.resource_type=? and t.rule='deny'", p.InstanceID, t.Schema, resourceType)
		tx = applyPrincipalFilter(tx, "t", scope)
		tx = tx.Where("not exists (?)",
			applyPrincipalFilter(
				global.App.DB.Table("insight_das_table_perms ta").Select("1").
					Where("ta.instance_id = t.instance_id and ta.schema = t.schema and ta.resource_type = t.resource_type and ta.rule = 'allow'"),
				"ta",
				scope,
			),
		)
		tx.Scan(&result)
		if len(result) > 0 {
			// 结果集大于0，表示仅有deny规则的记录
			var tmpTables []string
			for _, r := range result {
				tmpTables = append(tmpTables, r.Table)
			}
			// 判断提取的表名t1是否在匹配出来的表集合里面["work_days", "t1"]
			if utils.IsContain(tmpTables, t.Table) {
				errMsg = append(errMsg, fmt.Sprintf("您没有表`%s`.`%s`权限", t.Schema, t.Table))
			}
		}
	}
	return errMsg
}

// ensureInstanceAdmission 实例准入闸：主体必须对实例持有任一覆盖指定操作的
// 有效 allow 授权（实例/库/表任一层级），否则一律拒绝。灰度保护：
// 授权表为空（未迁移环境）时跳过，沿用旧模型判定。
func (p CheckUserPerm) ensureInstanceAdmission(scope *dasActorScope, action string) error {
	if !dataGrantEnabled() {
		return nil
	}
	ok, err := HasAnyInstanceGrant(scope, p.InstanceID, action)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("您没有数据源`%s`执行%s操作的权限", p.InstanceID, action)
	}
	return nil
}

// requireInstanceAction 实例级授权命中后，再单独校验操作类型。
//
// 实例级授权只说明「能进这个数据源」，并不自动蕴含所有操作——
// actions 是授权时写死的（例如只给了 select），那么即使有实例级授权，
// DML / 导出也必须再判一次。缺了这一步，「授了只读却能改数据」就是必然。
func (p CheckUserPerm) requireInstanceAction(scope *dasActorScope, action string) error {
	if !dataGrantEnabled() {
		return nil
	}
	ok, err := HasDataGrant(scope, p.InstanceID, "", "", action)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("您没有对数据源`%s`执行%s操作的权限", p.InstanceID, action)
	}
	return nil
}

// HasSchemaPerms 检查是否有库访问权限（默认按 select 判定）。
func (p CheckUserPerm) HasSchemaPerms() error {
	return p.HasSchemaPermsForAction(DataActionSelect)
}

// HasSchemaPermsForAction 按指定操作类型检查库访问权限。
//
// 操作维度必须显式传入：DML 工单不能拿 select 授权蒙混过关，
// 导出工单同理——否则「只授了 select」的人依然能提交 UPDATE 或把整表拉走。
func (p CheckUserPerm) HasSchemaPermsForAction(action string) error {
	scope, err := p.getScope()
	if err != nil {
		return err
	}
	// 实例准入闸：SELECT 1 / SHOW ... 这类无表引用语句提取不出库表名，
	// 不会进入下面的逐表循环；未授权主体必须在这里就被拦下，
	// 而不是留一条「没人管」的执行通道。
	if err := p.ensureInstanceAdmission(scope, action); err != nil {
		return err
	}
	instanceGranted, err := hasDASInstanceGrant(scope, p.InstanceID)
	if err != nil {
		return err
	}
	if instanceGranted {
		// 实例级授权放行了「能进这个实例」，但操作类型仍要单独判定：
		// 实例级 grant 的 actions 是授权时就写死的，不能因为能进实例就允许 DML。
		return p.requireInstanceAction(scope, action)
	}
	// 新授权模型：逐库按显式授权判定（库级或表级授权都能覆盖到具体库），
	// 未命中任何授权即拒绝——没有「兜底放行」分支。
	if dataGrantEnabled() {
		for _, t := range p.Tables {
			if shouldSkipSchemaPermCheck(t.Schema) {
				continue
			}
			granted, err := HasDataGrant(scope, p.InstanceID, t.Schema, "", action)
			if err != nil {
				return err
			}
			if !granted {
				// 库级无授权时再看库内表级授权：只授了某张表也必须能进这个库，
				// 否则表级授权在「库级闸门」这一层就被挡住，等于授了也用不了。
				granted, err = hasTableLevelGrantInSchema(scope, p.InstanceID, t.Schema, action)
				if err != nil {
					return err
				}
			}
			if !granted {
				return fmt.Errorf("您没有对资源`%s`执行%s操作的权限", t.Schema, action)
			}
		}
		return nil
	}
	if err := func() error {
		for _, t := range p.Tables {
			// 空上下文和 information_schema 默认不做 schema 权限拦截
			if shouldSkipSchemaPermCheck(t.Schema) {
				continue
			}
			resourceType := p.resolveResourceType(t.Schema)
			// 鉴权
			type countResult struct {
				Count int
			}
			var result countResult
			tx := global.App.DB.Table("`insight_das_schema_perms` p").
				Select("count(*) as count").
				Joins("join `insight_instance_schemas` s on p.instance_id = s.instance_id and p.`schema` = s.`schema`").
				Where("s.is_deleted = 0 and p.instance_id=? and p.`schema`=? and p.resource_type=?", p.InstanceID, t.Schema, resourceType)
			applyPrincipalFilter(tx, "p", scope).Scan(&result)
			if result.Count == 0 {
				return fmt.Errorf("您没有资源`%s`的访问权限", t.Schema)
			}
		}
		return nil
	}(); err != nil {
		return err
	}
	return nil
}

func (p CheckUserPerm) HasTablePerms() error {
	return p.HasTablePermsForAction(DataActionSelect)
}

// HasTablePermsForAction 按指定操作类型检查表访问权限。
func (p CheckUserPerm) HasTablePermsForAction(action string) error {
	scope, err := p.getScope()
	if err != nil {
		return err
	}
	// 实例准入闸：与 HasSchemaPermsForAction 同理，兜住无表引用语句
	if err := p.ensureInstanceAdmission(scope, action); err != nil {
		return err
	}
	instanceGranted, err := hasDASInstanceGrant(scope, p.InstanceID)
	if err != nil {
		return err
	}
	if instanceGranted {
		return p.requireInstanceAction(scope, action)
	}
	// 新授权模型：逐表按显式授权判定。HasDataGrant 会在同一次查询里
	// 匹配 表级 / 库级 / 实例级 三层，任一命中即放行；deny 优先级最高。
	if dataGrantEnabled() {
		for _, t := range p.Tables {
			if shouldSkipSchemaPermCheck(t.Schema) {
				continue
			}
			granted, err := HasDataGrant(scope, p.InstanceID, t.Schema, t.Table, action)
			if err != nil {
				return err
			}
			if !granted {
				return fmt.Errorf("您没有对资源`%s.%s`执行%s操作的权限", t.Schema, t.Table, action)
			}
		}
		return nil
	}
	/*
		优先级：allow > deny
		前提：insight_das_schema_perms表有zhangsan访问test库的授权记录
		Case分析：
			Case1:
				Desc:
					表das_user_table_permissions中用户zhangsan的记录为空
				Result:
					用户zhangsan可以访问当前拥有test库所有的表权限
			Case2：
				Desc:
					表das_user_table_permissions中用户zhangsan有如下记录
					zhangsan | test | work_days | allow
					zhangsan | test | st_week   | allow
					zhangsan | test | st_total  | deny
				Result:
					当allow和deny规则并存时，仅allow规则生效，deny规则不生效
					用户zhangsan可以访问test.work_days、test.st_week表，deny这条规则不生效，且不用添加
					如果不希望访问表st_week，删除这条记录（zhangsan | test | st_week | allow）
			Case3：
				Desc:
					表das_user_table_permissions中用户zhangsan有如下记录
					zhangsan | test | work_days | deny
					zhangsan | test | st_total  | deny
				Result:
					用户zhangsan不可以访问test.work_days、test.st_total，但可以访问test库其他的表
	*/
	// Case1
	if ok := p.checkHasAllTablePerms(scope); ok {
		return nil
	}
	// Case2
	if errMsg := p.checkOnlyHasAllowTablePerms(scope); len(errMsg) > 0 {
		return errors.New(strings.Join(errMsg, ";"))
	}
	// Case3
	if errMsg := p.checkOnlyHasDenyTablePerms(scope); len(errMsg) > 0 {
		return errors.New(strings.Join(errMsg, ";"))
	}
	return nil
}
