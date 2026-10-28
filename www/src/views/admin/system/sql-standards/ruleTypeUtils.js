/**
 * 规则类型解析（RuleTable / SQLStandardDetail 共用）
 *
 * 唯一权威来源是数据库列 insight_sql_standard_rules.rule_type：
 * 种子播种与「新建规范复制模板」都会写入该列，服务启动时还会把存量空值/非法值幂等回填，
 * 页面直接展示库里的值即可。
 *
 * 为什么不按规则名称去文档映射表里匹配：规则名称与描述允许运营在页面上随时编辑，
 * 名称一改任何「按名匹配」的映射都会静默失效（此前的整列「待补充」就是这么来的）。
 *
 * 兜底仅用于过渡期：库里该列为空（历史数据尚未回填、或人工插入了规则行）时，
 * 按 rule_key 的命名约定做一次确定性推断；仍取不到才显示「待补充」。
 */

// 合法规则类型（与后端 ruleTypeAllowed 保持一致）
const ALLOWED_TYPES = new Set(['表', '列', '索引', '库', '对象', '更新', '写入', '删除', '查询'])

const isAllowedType = (value) => ALLOWED_TYPES.has(String(value || '').trim())

/**
 * 按 rule_key 命名约定分类，作为库值为空时的兜底。
 * rule_key 形如 mysql.query_update.limit_update_delete_affected_rows，
 * 前两段是「数据库 + 领域」，能表达类型的只有后缀。
 */
const classifyRuleTypeByKey = (ruleKey) => {
  let suffix = String(ruleKey || '').trim()
  // 剥掉「数据库.领域」两段前缀（mysql./postgres./all. 一律适用，不做白名单）
  for (let i = 0; i < 2; i += 1) {
    const idx = suffix.indexOf('.')
    if (idx < 0) break
    suffix = suffix.slice(idx + 1)
  }
  if (!suffix) return ''
  // 写入类必须先判：limit_insert_values_row_count 虽以 limit_ 开头，语义仍是写入
  if (suffix.startsWith('insert_') || suffix.includes('insert')) return '写入'
  if (suffix.startsWith('delete_')) return '删除'
  if (/^(update_|update_delete_|limit_update_delete_)/.test(suffix)) return '更新'
  if (/^(where_|limit_where_)/.test(suffix)) return '查询'
  if (suffix.startsWith('disable_')) return '对象'
  if (suffix.startsWith('obj_view_')) return '对象'
  if (suffix.startsWith('obj_idx_')) return '索引'
  if (suffix.startsWith('obj_col_')) return '列'
  if (suffix.startsWith('obj_tbl_')) return '表'
  if (suffix.startsWith('limit_pk_')) return '索引'
  if (/(idx|index)/.test(suffix)) return '索引'
  if (suffix.startsWith('limit_database_')) return '库'
  if (/(tbl|table)/.test(suffix)) return '表'
  if (/(col|column)/.test(suffix)) return '列'
  return ''
}

/**
 * 取一条规则的规则类型：优先库里存的值（唯一权威），仅在为空时兜底推断。
 * @param {object} rule 规则对象，需包含 rule_type 与 rule_key
 * @returns {string} 规则类型，无法判定时返回「待补充」
 */
export const resolveRuleType = (rule) => {
  if (!rule) return '待补充'
  const stored = String(rule.rule_type || '').trim()
  if (isAllowedType(stored)) return stored
  return classifyRuleTypeByKey(rule.rule_key) || '待补充'
}
