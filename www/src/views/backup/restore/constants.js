// 恢复模块共享常量与文案。
// 集中一处的原因：向导、列表、详情页三处都要展示同一套状态/阶段/预检语义，
// 散落各写一份必然出现「详情页叫预检查失败、列表叫失败」的不一致。

// 执行阶段时间轴，顺序与后端 restore_observability.go 的 restoreStage* 常量一致。
export const RESTORE_STAGES = [
  { key: 'PENDING', label: '排队中' },
  { key: 'PRECHECK', label: '预检查' },
  { key: 'PREPARE_TARGET', label: '准备目标' },
  { key: 'PREPARE_SOURCE', label: '读取备份集' },
  { key: 'RESTORE', label: '恢复执行' },
  { key: 'VERIFY', label: '校验' },
  { key: 'FINALIZE', label: '收尾' },
  { key: 'FINISHED', label: '完成' },
]

export const stageLabel = (stage) => {
  const key = String(stage || '').trim().toUpperCase()
  return RESTORE_STAGES.find((item) => item.key === key)?.label || (stage ? String(stage) : '-')
}

// 阶段在整条链路中的序号，用于时间轴当前节点判定。
export const stageIndex = (stage) => {
  const key = String(stage || '').trim().toUpperCase()
  const index = RESTORE_STAGES.findIndex((item) => item.key === key)
  return index < 0 ? -1 : index
}

export const RESTORE_STATUS_LABELS = {
  PENDING: '等待中',
  RUNNING: '运行中',
  SUCCESS: '成功',
  FAILED: '失败',
  STOP_REQUESTED: '终止中',
  STOPPED: '已终止',
}

export const statusLabel = (status, stage = '') => {
  const text = String(status || '').trim().toUpperCase()
  const currentStage = String(stage || '').trim().toUpperCase()
  if (text === 'FAILED' && currentStage === 'PRECHECK') return '预检查失败'
  return RESTORE_STATUS_LABELS[text] || (status ? String(status) : '未知')
}

export const statusColor = (status) => {
  const text = String(status || '').trim().toUpperCase()
  if (text === 'SUCCESS') return 'green'
  if (text === 'FAILED') return 'red'
  if (text === 'RUNNING' || text === 'STOP_REQUESTED') return 'blue'
  if (text === 'STOPPED') return 'default'
  return 'orange'
}

export const isActiveStatus = (status) => ['PENDING', 'RUNNING', 'STOP_REQUESTED'].includes(String(status || '').toUpperCase())

// 预检查三档：与后端 items[].status（PASS/WARN/BLOCK）一一对应。
export const precheckToken = (item) => {
  const status = String(item?.status || '').trim().toUpperCase()
  const level = String(item?.level || '').trim().toUpperCase()
  if (status === 'BLOCK' || level === 'BLOCKER') return 'blocked'
  if (status === 'WARN' || level === 'WARNING') return 'warning'
  return 'passed'
}

export const precheckTokenLabel = (token) => ({ blocked: '阻断', warning: '警告', passed: '通过' }[token] || '通过')

// 预检查不通过时的修复指引：用户看到「只读状态 BLOCK」不该自己去猜怎么办。
export const PRECHECK_FIX_HINTS = {
  execute_confirmed: '在「确认并执行」步骤勾选执行确认后重新检查。',
  read_only: '目标实例为只读（read_only=ON 或处于备库），请先放开写入。',
  backup_file: '备份文件不可访问，确认存储目标挂载正常后重试。',
  metadata: '备份产物缺少 metadata.json，通常来自旧版本备份，建议重做一次备份再恢复。',
  checksum: '备份产物校验和不匹配，产物可能已损坏，请勿继续恢复。',
  target_exists: '目标对象已存在：改选「跳过」或「清空后导入」，或更换目标名称。',
  privilege: '恢复账号权限不足，补齐 CREATE/INSERT/DROP 等权限后重试。',
}

export const fixHintOf = (item) =>
  PRECHECK_FIX_HINTS[String(item?.item_key || item?.key || '').trim()] || ''

// 冲突策略 → 底层恢复模式，必须与后端 deriveRestoreModeFromPolicies 保持一致。
export const deriveRestoreMode = (schemaPolicy, dataPolicy) => {
  if (String(dataPolicy || '').toUpperCase() === 'TRUNCATE') return 'OVERWRITE'
  if (String(schemaPolicy || '').toUpperCase() === 'ABORT') return 'NEW'
  return 'APPEND'
}

export const RESTORE_MODE_LABELS = {
  NEW: '新建恢复',
  OVERWRITE: '覆盖恢复',
  APPEND: '追加恢复',
  NEW_INSTANCE: '新建实例',
  EXISTING_INSTANCE: '覆盖已有实例',
}

export const restoreModeLabel = (mode) =>
  RESTORE_MODE_LABELS[String(mode || '').toUpperCase()] || (mode ? String(mode) : '-')

export const derivedModeHint = (schemaPolicy, dataPolicy) => {
  const mode = deriveRestoreMode(schemaPolicy, dataPolicy)
  if (mode === 'OVERWRITE') return '覆盖恢复：目标表先清空再导入，原有数据不可恢复。'
  if (mode === 'NEW') return '新建恢复：目标对象已存在即中断，适合恢复成一份新副本。'
  return '追加恢复：已存在对象跳过并继续导入。'
}

export const DRIVER_LABELS = {
  LOGICAL: '逻辑备份',
  XTRABACKUP: 'XtraBackup',
  PG_BACKREST: 'pgBackRest',
}

export const RISK_LABELS = { high: '高危', medium: '中风险', low: '低风险' }
export const RISK_COLORS = { high: 'red', medium: 'orange', low: 'green' }

// 目标库名合法性：MySQL 允许字母/数字/下划线/$，PG 额外允许小写与连字符（带引号场景不含）。
export const validateObjectName = (name, dbType) => {
  const value = String(name || '').trim()
  if (!value) return ''
  if (value.length > 64) return '名称不能超过 64 个字符'
  const pattern = String(dbType || '').toUpperCase() === 'POSTGRESQL' ? /^[A-Za-z_][A-Za-z0-9_-]*$/ : /^[A-Za-z0-9_$]+$/
  return pattern.test(value) ? '' : '仅允许字母、数字、下划线（MySQL 另允许 $）'
}

// 后端 LocalTime 序列化可能是「2026-09-20 02:00:00」或带时区的「2026-09-20T02:00:00+08:00」，
// 统一裁到秒级，避免摘要里出现 T / 时区尾巴。
export const formatDateTime = (value) => {
  const raw = String(value || '').trim()
  if (!raw || raw === '0001-01-01 00:00:00') return '-'
  return raw.replace('T', ' ').slice(0, 19)
}

export const formatBytes = (value) => {
  const num = Number(value || 0)
  if (!num) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let index = 0
  let size = num
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index += 1
  }
  return `${size.toFixed(size >= 100 || index === 0 ? 0 : 1)} ${units[index]}`
}
