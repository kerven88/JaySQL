package tools

// ---------------------------------------------------------------------------
// AI 工具的数据授权收口
//
// AI 助手能「一句话问出全网数据」，是数据授权最容易失守的地方：
// 工具的权限点（order.view / monitor.view …）只校验「能不能进这个功能」，
// 不校验「能不能看这个实例的数据」。于是只要能对话，就能问出未授权实例上的
// 工单内容（含 SQL）、慢查询原文、告警与备份情况 —— RBAC 形同虚设。
//
// 收口方式：所有按实例维度取数的工具，统一收敛到「该账号被授权可见的实例集合」，
// 与工单数据源列表、性能分析页同一口径（dasServices.GetAccessibleInstanceIDsByUsername）。
// ---------------------------------------------------------------------------

import (
	"errors"
	"fmt"
	"strings"

	dasServices "github.com/simajiong/jaysql/internal/das/services"
	"github.com/simajiong/jaysql/internal/global"
	"gorm.io/gorm"
)

// visibleInstanceIDs 取当前对话人可见的实例集合。
// allAllowed=true 表示存在实例级授权，无需按实例收敛。
func visibleInstanceIDs(ctx *Context) ([]string, bool, error) {
	username := ""
	if ctx != nil {
		username = strings.TrimSpace(ctx.Username)
	}
	return dasServices.GetAccessibleInstanceIDsByUsername(username)
}

// applyVisibleInstances 给 AI 工具的查询加上可见实例过滤。
// column 传实际存放业务 instance_id 的列名（多数表就叫 instance_id）。
func applyVisibleInstances(tx *gorm.DB, ctx *Context, column string) (*gorm.DB, error) {
	instanceIDs, allAllowed, err := visibleInstanceIDs(ctx)
	if err != nil {
		return tx, err
	}
	if allAllowed {
		return tx, nil
	}
	if len(instanceIDs) == 0 {
		// 没有任何授权：短路成空结果，不要让 AI 把全表问出去
		return tx.Where("1 = 0"), nil
	}
	return tx.Where(column+" in ?", instanceIDs), nil
}

// applyVisibleInstancesByNumericID 备份任务表里存的是数字实例 ID
// （insight_instances.id），而授权表用业务 ID（instance_id char(15)），
// 这里先做一次映射再过滤，否则会拿业务 ID 去比数字列，过滤恒不命中。
func applyVisibleInstancesByNumericID(tx *gorm.DB, ctx *Context, column string) (*gorm.DB, error) {
	instanceIDs, allAllowed, err := visibleInstanceIDs(ctx)
	if err != nil {
		return tx, err
	}
	if allAllowed {
		return tx, nil
	}
	if len(instanceIDs) == 0 {
		return tx.Where("1 = 0"), nil
	}
	var numericIDs []uint64
	if err := global.App.DB.Table("insight_instances").
		Where("instance_id in ?", instanceIDs).
		Pluck("id", &numericIDs).Error; err != nil {
		return tx, err
	}
	if len(numericIDs) == 0 {
		return tx.Where("1 = 0"), nil
	}
	return tx.Where(column+" in ?", numericIDs), nil
}

// instanceVisible 单点判定：某个实例对当前对话人是否可见。
func instanceVisible(ctx *Context, instanceID string) (bool, error) {
	if strings.TrimSpace(instanceID) == "" {
		return false, nil
	}
	instanceIDs, allAllowed, err := visibleInstanceIDs(ctx)
	if err != nil {
		return false, err
	}
	if allAllowed {
		return true, nil
	}
	for _, id := range instanceIDs {
		if id == instanceID {
			return true, nil
		}
	}
	return false, nil
}

// ---------------------------------------------------------------------------
// SQL 类工具的统一鉴权前置
//
// 与上面「按实例收敛」的差别：SQL 类工具会真正连到目标库取表结构、跑 EXPLAIN、
// 抽样本数据，粒度必须下沉到「库 / 表」。因此在调用任何查询型服务之前，先把
// SQL 涉及的表解析出来，逐张问一次数据授权：
//   - 有任意一张没过 → 整条 SQL 不放行，直接返回统一措辞的友好提示；
//   - 无权限时不调用任何查询工具 —— 绝不「先跑一次再说」。表结构和样本数据
//     一旦进了模型上下文，再补一句「你没权限」已经没有意义。
// ---------------------------------------------------------------------------

// SQLActionSelect 查询类操作口径。
// NL2SQL / SQL 解释 / 执行计划 / 索引推荐 / 数据采样读的都是表结构与数据，
// 统一按 select 判定（不是 dml，它们不改数据；也不是 export，不做导出）。
const SQLActionSelect = dasServices.DataActionSelect

// sqlGrantApplyTip 无权限时的申请指引，5 个 SQL 类工具共用同一段措辞
const sqlGrantApplyTip = "请联系平台管理员在「系统管理 → 权限管理」为你授予对应库表的数据授权（操作类型：查询）；" +
	"授权生效后重新提问即可。若你认为自己已被授权，请确认授权主体与当前登录账号一致、且授权仍在有效期内。"

// sqlResource 一条待鉴权的数据资源（Schema 为空表示实例级，Table 为空表示库级）
type sqlResource struct {
	Schema string
	Table  string
}

// describeSQLResource 资源的中文描述（提示语与日志共用同一套写法）
func describeSQLResource(item sqlResource) string {
	switch {
	case item.Schema == "" && item.Table == "":
		return "该实例"
	case item.Table == "":
		return fmt.Sprintf("库 `%s`", item.Schema)
	default:
		return fmt.Sprintf("表 `%s`.`%s`", item.Schema, item.Table)
	}
}

// dataActionLabel 操作类型的中文名（提示语里用）
func dataActionLabel(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case dasServices.DataActionExport:
		return "导出"
	case dasServices.DataActionDML:
		return "变更"
	default:
		return "查询"
	}
}

// checkSQLResources 逐条校验数据授权，返回「未通过授权的资源描述列表」。
//
// 返回空列表 = 全部放行；返回 error = 判定链路本身异常（调用方应直接报错，
// 不能把它糊弄成「无权限」，否则一次数据库抖动会被误报成权限问题）。
func checkSQLResources(ctx *Context, instanceID string, action string, resources []sqlResource) ([]string, error) {
	username := usernameOf(ctx)
	missing := make([]string, 0)
	seen := map[string]struct{}{}
	for _, item := range resources {
		key := item.Schema + "|" + item.Table
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		err := dasServices.CheckDataAccess(dasServices.DataAccessRequest{
			Username:   username,
			InstanceID: instanceID,
			Schema:     item.Schema,
			Table:      item.Table,
			Action:     action,
		})
		if err == nil {
			continue
		}
		// 新模型下只有 ErrNoDataGrant 表示「确实没授权」，其它都是判定链路异常；
		// 旧模型（迁移未完成）的报错没有统一哨兵，一律按「未授权」处理 ——
		// 方向同样是失败关闭，只是提示语走统一文案。
		if !errors.Is(err, dasServices.ErrNoDataGrant) && dasServices.DataGrantTableReady() {
			return nil, fmt.Errorf("权限校验失败：%s", strings.TrimSpace(err.Error()))
		}
		missing = append(missing, describeSQLResource(item))
	}
	return missing, nil
}

// noDataGrantMessage 无权限提示语：5 个 SQL 类工具共用这一段措辞。
// 抽成函数是为了让「整条 SQL 被拒」与「部分表被跳过」说同样的话，
// 避免同一个权限问题在不同工具里冒出不同版本的提示。
func noDataGrantMessage(action string, missing []string) string {
	objects := strings.Join(missing, "、")
	if strings.TrimSpace(objects) == "" {
		objects = "该数据对象"
	}
	return fmt.Sprintf(
		"你没有对 %s 的%s权限，因此本次不执行任何查询，也不会返回该对象的表结构或样本数据。",
		objects, dataActionLabel(action),
	)
}

// noDataGrantResult 无权限时 5 个 SQL 类工具共用的返回体。
//
// 刻意返回「结果」而不是 error：工具报错时编排层回灌的是「执行失败：…」，
// 模型容易理解成「换个工具再试」；这里要的是让模型把这段话原样转述给用户，
// 说清缺哪张表的什么权限、怎么申请，而不是去绕路取数。
func noDataGrantResult(action string, missing []string) map[string]any {
	return map[string]any{
		"allowed":         false,
		"code":            "no_data_grant",
		"action":          action,
		"missing_objects": missing,
		"message":         noDataGrantMessage(action, missing),
		"how_to_apply":    sqlGrantApplyTip,
		"note": "请把上面这段提示原样转述给用户：不要改用其它工具、也不要换一种问法绕开权限取数，" +
			"更不要依据常识猜测这些表的字段或数据内容。",
	}
}
