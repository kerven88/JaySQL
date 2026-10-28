package services

import (
	"strings"
	"sync"

	"gorm.io/gorm"

	"github.com/simajiong/jaysql/internal/inspect/models"
)

// 规则类型（insight_sql_standard_rules.rule_type）是「规则类型」列的唯一权威来源。
//
// 历史教训：规则类型此前没有落库，前端拿规则名称去比对一份从 md 文档导出的映射表
// （ruleTypeMap.json）。规则名称、描述允许运营在页面上随时编辑，名字一改映射就静默失效，
// 整列退化成「待补充」。所以这里的原则是：
//  1. 写入时赋值（种子 / 复制模板两条路径都必须带上 rule_type）；
//  2. 启动时幂等回填存量空值与非法值；
//  3. 页面直接读库里的值，不再做任何名称推断。
//
// 下面的目录与分类器只服务于 1、2 两步（补数据），不参与页面渲染。

// ruleTypeAllowed 规则类型的合法取值（与前端筛选项一致）。
var ruleTypeAllowed = []string{"表", "列", "索引", "库", "对象", "更新", "写入", "删除", "查询"}

// isAllowedRuleType 判断取值是否属于合法规则类型，空值/未知值都视为不可用。
func isAllowedRuleType(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return false
	}
	for _, item := range ruleTypeAllowed {
		if v == item {
			return true
		}
	}
	return false
}

var (
	catalogRuleTypeOnce   sync.Once
	catalogRuleTypeByKey  map[string]string
	catalogRuleTypeByName map[string]string
)

// buildCatalogRuleTypes 用规则目录（defaultStandardRules）现场构建检索索引，
// 避免再维护一份与种子平行的类型清单（两份清单必然漂移）。
func buildCatalogRuleTypes() {
	catalogRuleTypeByKey = make(map[string]string, 160)
	catalogRuleTypeByName = make(map[string]string, 160)
	for _, r := range defaultStandardRules() {
		if !isAllowedRuleType(r.RuleType) {
			continue
		}
		if key := strings.TrimSpace(r.RuleKey); key != "" {
			catalogRuleTypeByKey[key] = r.RuleType
		}
		// 同名规则在 mysql / postgres 下类型一致，按名建索引只为兜住「rule_key 被改过」的存量行
		if name := normalizeRuleName(r.Name); name != "" {
			catalogRuleTypeByName[name] = r.RuleType
		}
	}
}

// normalizeRuleName 去掉全部空白后比较，兼容运营改名时顺带调整空格。
func normalizeRuleName(name string) string {
	return strings.Join(strings.Fields(name), "")
}

// ClassifyRuleTypeByKey 按 rule_key 的命名约定做确定性分类（目录未命中时的兜底）。
//
// rule_key 形如 mysql.query_update.limit_update_delete_affected_rows：
// 前两段是「数据库 + 领域」，真正能表达类型的只有后缀，这里按后缀约定判定。
func ClassifyRuleTypeByKey(ruleKey string) string {
	suffix := strings.TrimSpace(ruleKey)
	// 剥掉「数据库.领域」两段前缀（mysql./postgres./all. 等一律适用，不做白名单）
	for i := 0; i < 2; i++ {
		idx := strings.Index(suffix, ".")
		if idx < 0 {
			break
		}
		suffix = suffix[idx+1:]
	}
	if suffix == "" {
		return ""
	}
	switch {
	// 写入类必须先判：limit_insert_values_row_count 虽以 limit_ 开头，语义仍是写入
	case strings.HasPrefix(suffix, "insert_"), strings.Contains(suffix, "insert"):
		return "写入"
	case strings.HasPrefix(suffix, "delete_"):
		return "删除"
	case strings.HasPrefix(suffix, "update_"),
		strings.HasPrefix(suffix, "update_delete_"),
		strings.HasPrefix(suffix, "limit_update_delete_"):
		return "更新"
	case strings.HasPrefix(suffix, "where_"), strings.HasPrefix(suffix, "limit_where_"):
		return "查询"
	case strings.HasPrefix(suffix, "disable_"):
		return "对象"
	case strings.HasPrefix(suffix, "obj_view_"):
		return "对象"
	case strings.HasPrefix(suffix, "obj_idx_"):
		return "索引"
	case strings.HasPrefix(suffix, "obj_col_"):
		return "列"
	case strings.HasPrefix(suffix, "obj_tbl_"):
		return "表"
	case strings.HasPrefix(suffix, "limit_pk_"):
		return "索引"
	case strings.Contains(suffix, "idx"), strings.Contains(suffix, "index"):
		return "索引"
	case strings.HasPrefix(suffix, "limit_database_"):
		return "库"
	case strings.Contains(suffix, "tbl"), strings.Contains(suffix, "table"):
		return "表"
	case strings.Contains(suffix, "col"), strings.Contains(suffix, "column"):
		return "列"
	}
	return ""
}

// ResolveRuleType 解析一条规则的规则类型：目录 rule_key → 目录规则名 → rule_key 分类。
// 返回空串表示无法判定（调用方保持原值/留空，绝不猜一个写进去）。
func ResolveRuleType(ruleKey, name string) string {
	catalogRuleTypeOnce.Do(buildCatalogRuleTypes)
	if v, ok := catalogRuleTypeByKey[strings.TrimSpace(ruleKey)]; ok {
		return v
	}
	if v, ok := catalogRuleTypeByName[normalizeRuleName(name)]; ok {
		return v
	}
	return ClassifyRuleTypeByKey(ruleKey)
}

// NormalizeRuleType 供写入路径使用：已有合法值原样保留，否则按目录/命名约定补齐。
func NormalizeRuleType(ruleKey, name, current string) string {
	if isAllowedRuleType(current) {
		return strings.TrimSpace(current)
	}
	return ResolveRuleType(ruleKey, name)
}

// BackfillSQLStandardRuleTypes 回填存量规则的类型，幂等且可重复执行。
//
// 处理范围：rule_type 为空，或存了历史试验遗留的非法值（不在 ruleTypeAllowed 里）。
// 已经合法的行一律不动——运营/管理员在库里的取值就是最终值。
// 返回被更新的行数，方便启动日志里核对首轮回填效果。
func BackfillSQLStandardRuleTypes(db *gorm.DB) (int64, error) {
	type ruleRow struct {
		ID      uint64
		RuleKey string
		Name    string
	}
	var rows []ruleRow
	if err := db.Model(&models.InsightSQLStandardRule{}).
		Select("id, rule_key, name").
		Where("rule_type IS NULL OR rule_type = '' OR rule_type NOT IN ?", ruleTypeAllowed).
		Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	// 按目标类型聚合成批更新：252 行通常落成 8 条 UPDATE，避免逐行往返
	groups := make(map[string][]uint64, len(ruleTypeAllowed))
	for _, r := range rows {
		if t := ResolveRuleType(r.RuleKey, r.Name); t != "" {
			groups[t] = append(groups[t], r.ID)
		}
	}

	var affected int64
	err := db.Transaction(func(tx *gorm.DB) error {
		for ruleType, ids := range groups {
			tx2 := tx.Model(&models.InsightSQLStandardRule{}).
				Where("id IN ?", ids).
				Update("rule_type", ruleType)
			if tx2.Error != nil {
				return tx2.Error
			}
			affected += tx2.RowsAffected
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return affected, nil
}
