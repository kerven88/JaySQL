// ---------------------------------------------------------------------------
// 数据访问申请：列表 / 申请页 / 详情页共用的展示口径
//
// 三处页面都要展示「状态」「申请权限」「流转事件」，各写一份必然漂移
// （最典型的是新增 wait_grant 后只改了一处，另一处显示成原始英文）。
// 状态词只允许来自这里；新增状态一律先加 ACCESS_STATUS_META。
// ---------------------------------------------------------------------------

// 状态：pending 待审批 / forwarded 已转交 / wait_grant 待执行授权（审批已通过、权限还没落）
// approved 已通过 / rejected 已驳回 / withdrawn 已撤回 / terminated 已终止 / expired 已过期 / revoked 已回收
export const ACCESS_STATUS_META = {
  pending: { text: '待审批', color: 'blue' },
  forwarded: { text: '已转交', color: 'cyan' },
  wait_grant: { text: '待执行授权', color: 'gold' },
  approved: { text: '已通过', color: 'green' },
  rejected: { text: '已驳回', color: 'red' },
  withdrawn: { text: '已撤回', color: 'default' },
  terminated: { text: '已终止', color: 'default' },
  expired: { text: '已过期', color: 'orange' },
  revoked: { text: '已回收', color: 'default' },
}

export const accessStatusText = (value) => ACCESS_STATUS_META[value]?.text || value || '-'
export const accessStatusColor = (value) => ACCESS_STATUS_META[value]?.color || 'default'

// 审批中的状态集合：转交后仍属审批中，撤回 / 终止 / 审批按钮都按这个口径判断
export const ACCESS_IN_APPROVAL_STATUS = ['pending', 'forwarded']
export const accessInApproval = (status) => ACCESS_IN_APPROVAL_STATUS.includes(status)

// 操作维度。申请入口只受理 select：变更 / 导出由 SQL 工单自身的审批流程覆盖，
// 再单独申请一次等于同一件事审两遍（服务层由 ActionNeedsAccessGrant 拦截）。
export const ACCESS_ACTION_META = {
  select: { text: '查询', color: 'blue' },
  export: { text: '导出', color: 'gold' },
  dml: { text: '变更', color: 'volcano' },
}

export const accessActionText = (value) =>
  String(value || '')
    .split(',')
    .filter(Boolean)
    .map((item) => ACCESS_ACTION_META[item]?.text || item)
    .join(' / ') || '-'
export const accessActionColor = (value) => ACCESS_ACTION_META[value]?.color || 'default'
export const splitAccessActions = (value) => String(value || '').split(',').filter(Boolean)

// 申请范围（资源层级）。schema=模式，只有 PostgreSQL / SQL Server 这类
// 「库 → 模式 → 表」三层结构的数据源才会申请到这一级。
export const ACCESS_RESOURCE_LEVEL_TEXT = {
  instance: '整个实例',
  database: '库',
  schema: '模式',
  table: '表',
}
export const accessResourceLevelText = (value) => ACCESS_RESOURCE_LEVEL_TEXT[value] || value || '-'

export const ACCESS_EVENT_META = {
  create: { text: '发起申请', color: 'blue' },
  approve: { text: '审批通过', color: 'green' },
  reject: { text: '审批驳回', color: 'red' },
  forward: { text: '转交审批人', color: 'cyan' },
  withdraw: { text: '申请人撤回', color: 'gray' },
  terminate: { text: '终止申请', color: 'gray' },
  grant: { text: '执行授权', color: 'green' },
  expire: { text: '授权到期', color: 'orange' },
  revoke: { text: '授权回收', color: 'gray' },
}
export const accessEventText = (value) => ACCESS_EVENT_META[value]?.text || value || '-'
export const accessEventColor = (value) => ACCESS_EVENT_META[value]?.color || 'blue'

// 有效期档位：只有这五档，长期 = -1（后端 accessRequestExpireForever，
// 落库时 requested_expire_at / approved_expire_at / 授权 expire_at 全部为 NULL，
// 数据权限判定把 NULL 当永久有效）。
// 申请页与审批弹窗共用同一份，避免「申请能选长期、审批只能选到 90 天」这种断档。
export const ACCESS_EXPIRE_FOREVER = -1
export const ACCESS_EXPIRE_DEFAULT = 180 // 6 个月

export const ACCESS_EXPIRE_OPTIONS = [
  { label: '1 天', value: 1 },
  { label: '7 天', value: 7 },
  { label: '1 个月', value: 30 },
  { label: '6 个月', value: 180 },
  { label: '长期', value: ACCESS_EXPIRE_FOREVER },
]

export const accessExpireLabel = (value) => {
  const days = Number(value)
  return ACCESS_EXPIRE_OPTIONS.find((item) => item.value === days)?.label || (days > 0 ? `${days} 天` : '长期')
}

// 时间字段展示：为空的时点字段（requested / approved / expire_at）只有一个来源，
// 就是申请人或审批人选了「长期」，所以空值一律显示「长期」而不是 '-'。
export const accessExpireText = (value) => (value ? String(value) : '长期')

// 审批时有效期的默认值：沿用申请人填的期望有效期，并吸附到最近的档位。
// 不要硬编码 —— 申请人选长期却被审批人一打开改成 6 个月等于静默收窄。
export const accessRequestedExpireDays = (record) => {
  const raw = record?.requested_expire_at
  if (!raw) return ACCESS_EXPIRE_FOREVER
  const target = new Date(String(raw).replace(/-/g, '/'))
  if (Number.isNaN(target.getTime())) return ACCESS_EXPIRE_DEFAULT
  const days = Math.ceil((target.getTime() - Date.now()) / 86400000)
  if (!Number.isFinite(days) || days <= 0) return 1
  return ACCESS_EXPIRE_OPTIONS.filter((item) => item.value > 0).reduce(
    (best, item) => (Math.abs(item.value - days) < Math.abs(best.value - days) ? item : best),
  ).value
}

// 页面实际展示的状态：审批状态 + 授权真实状态的合成。
// 授权被撤销 / 过期时即便申请单还是 approved，也不能显示成「已通过」。
export const accessEffectiveStatus = (request, grant) => {
  const status = request?.status || ''
  if (status !== 'approved') return status
  if (grant?.status === 'revoked') return 'revoked'
  if (grant?.status === 'expired') return 'expired'
  const expireAt = request?.expire_at
  if (expireAt && new Date(String(expireAt).replace(/-/g, '/')) <= new Date()) return 'expired'
  return 'approved'
}

// ---------------------------------------------------------------------------
// 多资源圈选快照（resource_spec）：多库 / 多模式 / 表维度四种方式
// 服务端口径见 access_request_spec.go；这里只做解析与展示，不做校验。
// ---------------------------------------------------------------------------

export const ACCESS_TABLE_MODE_TEXT = {
  all: '整个对象',
  list: '指定表',
  wildcard: '通配符',
  except: '排除表',
}

export const parseAccessResourceSpec = (raw) => {
  try {
    const value = JSON.parse(String(raw || ''))
    return value && typeof value === 'object' ? value : null
  } catch {
    return null
  }
}

// 路径取最后一段：db.s.t → t；db.t → t
const pathLeafName = (path) => {
  const idx = String(path || '').lastIndexOf('.')
  return idx > 0 ? String(path).slice(idx + 1) : String(path || '')
}

// 路径去库前缀：db.s.t → s.t；db.t → t（多模式 / 多库时表清单的展示口径）
const pathWithoutDatabase = (path) => {
  const idx = String(path || '').indexOf('.')
  return idx > 0 ? String(path).slice(idx + 1) : String(path || '')
}

// 申请范围摘要：库 / 模式 / 表三行展示文案。
// 授权清单单（v2，resource_spec.entries）：每条条目 = 库/模式对象 + 各自的表圈选方式，
// 库名行汇总全部涉及的库；模式行只列「整个模式」条目；带表圈选的条目逐条进表行。
// v1 多资源单读 spec 顶层字段；旧单据（spec 为空）回退到旧三列的单资源口径。
export const accessScopeSummary = (request) => {
  const level = String(request?.resource_level || '')
  if (level === 'instance') return { database: '整个实例', schema: '-', table: '-' }

  const spec = parseAccessResourceSpec(request?.resource_spec)

  if (spec && Array.isArray(spec.entries) && spec.entries.length) {
    const dbs = new Set()
    const schemaLines = []
    const tableLines = []
    for (const item of spec.entries) {
      if (!item || !item.database) continue
      dbs.add(item.database)
      const container = item.kind === 'schema' ? item.schema : item.database
      const mode = String(item.table_mode || '')
      if (!mode) {
        if (item.kind === 'schema' && container) schemaLines.push(container)
        continue
      }
      if (!container) continue
      if (mode === 'wildcard') {
        tableLines.push(`${container}：匹配 ${item.pattern || '-'}`)
      } else if (mode === 'except') {
        tableLines.push(`${container}：除 ${(item.except || []).join('、') || '-'} 外全部表`)
      } else {
        tableLines.push(`${container}：${(item.tables || []).join('、') || '-'}`)
      }
    }
    return {
      database: Array.from(dbs).join('、') || '-',
      schema: schemaLines.join('、') || '-',
      table: tableLines.join('；') || '-',
    }
  }

  if (spec) {
    const databaseText = (spec.databases || []).join('、') || '-'
    const multiDatabase = (spec.databases || []).length > 1
    let schemaText = '-'
    if (level === 'schema' || level === 'table') {
      const schemas = spec.schemas || []
      schemaText =
        schemas.map((p) => (multiDatabase ? p : pathLeafName(p))).join('、') || '-'
    }
    let tableText = '-'
    if (level === 'table') {
      if (spec.table_mode === 'wildcard') {
        tableText = `表名匹配 ${spec.pattern || '-'}`
      } else if (spec.table_mode === 'except') {
        const excludes = (spec.except || []).map((p) =>
          multiDatabase ? pathWithoutDatabase(p) : pathLeafName(p),
        )
        tableText = `除 ${excludes.join('、') || '-'} 外的全部表`
      } else {
        tableText =
          (spec.tables || [])
            .map((p) => (multiDatabase ? pathWithoutDatabase(p) : pathLeafName(p)))
            .join('、') || '-'
      }
    }
    return { database: databaseText, schema: schemaText, table: tableText }
  }

  // 旧单据：schema_name 存授权路径（三层结构为「库.模式」），database 列可能为空
  const raw = String(request?.database || '') || String(request?.schema_name || '')
  const idx = raw.indexOf('.')
  const database = idx > 0 ? raw.slice(0, idx) : raw
  const schemaPath = String(request?.schema_name || '')
  const schemaLeaf = level === 'schema' || level === 'table'
    ? schemaPath.includes('.')
      ? schemaPath.slice(schemaPath.indexOf('.') + 1)
      : schemaPath
    : ''
  return {
    database: database || '-',
    schema: schemaLeaf || '-',
    table: String(request?.table_name || '') || '-',
  }
}
