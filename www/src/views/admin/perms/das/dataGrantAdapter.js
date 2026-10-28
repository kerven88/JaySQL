/**
 * 新授权表（insight_das_data_grants）行 → 「数据访问」管理页既有行形状的适配层。
 *
 * 为什么需要它：
 *   页面（列表 / 明细）与 grouping.js 全部按旧表（insight_das_schema_perms）的
 *   字段名取值（resource_type / schema / display_name / hostname / port / remark …）。
 *   新表把资源维度拆成了 resource_level + schema_name + table_name，并且不存
 *   数据源元信息（那些由后端 join insight_instances / insight_instance_schemas 补出）。
 *
 *   与其把字段名改写散落到模板、grouping、搜索过滤等十几处，不如在这里做一次性收口：
 *   上层继续读老键名，只有这一个文件知道两套命名怎么对。
 *
 * 注意：resource_type 由后端推导（优先取 insight_instance_schemas 的原始
 *   resource_type，取不到才回落到 resource_level），所以这里不做二次判断。
 */

const splitInstanceName = (instanceName) => {
  const text = String(instanceName || '').trim()
  if (!text) return { hostname: '', port: '' }
  const idx = text.lastIndexOf(':')
  if (idx <= 0) return { hostname: text, port: '' }
  return { hostname: text.slice(0, idx), port: text.slice(idx + 1) }
}

export const normalizeDataGrantRow = (raw) => {
  if (!raw || typeof raw !== 'object') return null
  const level = String(raw.resource_level || '')
  const schemaName = String(raw.schema_name || '')
  const tableName = String(raw.table_name || '')
  const instanceName = String(raw.instance_name || '')
  const fromConn = splitInstanceName(instanceName)

  // 资源展示名保持「库 / Schema 的展示名」不变；表级授权要拼表名时由使用方自己拼
  // （明细页资源列需要拼，表级明细页有独立「表名」列，拼了会重复）
  const displayName = String(raw.display_name || '').trim() || schemaName

  return {
    ...raw,
    // —— 定位键 ——
    id: raw.id,
    grant_no: raw.grant_no,
    principal_type: raw.principal_type,
    principal_id: raw.principal_id,
    principal_name: raw.principal_name,
    principal_deleted_hint: raw.principal_deleted_hint || '',

    // —— 资源维度（旧键名）——
    resource_level: level,
    resource_type: raw.resource_type || level,
    schema: schemaName,
    table: tableName,
    display_name: displayName,
    parent_path: raw.parent_path || '',

    // —— 数据源元信息 ——
    instance_id: raw.instance_id,
    instance_name: instanceName,
    hostname: String(raw.hostname || '') || fromConn.hostname,
    port: raw.port || fromConn.port || '',
    remark: raw.remark || '',
    db_type: raw.db_type || '',
    environment: raw.environment || '',

    // —— 授权语义 ——
    actions: raw.actions || '',
    effect: raw.effect || 'allow',
    // 旧表把 allow/deny 放在 rule 列，表级明细页的列定义还按 rule 取
    rule: raw.effect || 'allow',
    status: raw.status || '',
    grant_source: raw.grant_source || '',
    grantor_username: raw.grantor_username || '',
    reason: raw.reason || '',
    effective_at: raw.effective_at || '',
    expire_at: raw.expire_at || '',
    revoked_at: raw.revoked_at || '',

    // —— 时间：grouping.js 会从这些键里择优取「创建时间 / 更新时间」——
    created_at: raw.created_at || '',
    updated_at: raw.updated_at || raw.created_at || '',
  }
}

export const normalizeDataGrantRows = (rows) =>
  (Array.isArray(rows) ? rows.map(normalizeDataGrantRow).filter(Boolean) : [])

/** 授权范围的中文标签（表级授权是新表才有的维度，旧页面没有这个分支） */
export const dataGrantScopeLabel = (resourceType) => {
  switch (String(resourceType || '')) {
    case 'instance': return '实例级'
    case 'database': return '数据库'
    case 'schema': return 'Schema'
    case 'table': return '表'
    default: return '--'
  }
}

/** 权限动作的中文标签，用于明细页摘要展示 */
export const dataGrantActionsLabel = (actions) => {
  const map = { select: '查询', export: '导出', dml: '变更' }
  const list = String(actions || '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
    .map((item) => map[item] || item)
  return list.length ? list.join(' / ') : '--'
}
