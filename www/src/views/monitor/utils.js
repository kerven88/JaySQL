// 大屏图表语义色（方案 A）的唯一真源。这里的展示映射全部从它派生，
// 保证圆饼扇区 / 图例圆点 / 状态色条 / 明细页四分类圆点永远是同一组颜色。
import { ALERT_STATUS_KEY_BY_BACKEND, INSTANCE_STATUS_COLORS } from './chartTokens'

export const createDefaultDashboard = () => ({
  generated_at: '',
  summary: {
    total_instances: 0,
    healthy_instances: 0,
    degraded_instances: 0,
    active_alerts: 0,
    prometheus_covered: 0,
    direct_collected: 0,
  },
  prometheus: {
    configured: false,
    available: false,
    base_url: '',
    message: '',
  },
  collect: {
    ok: 0,
    partial: 0,
    failed: 0,
    stale_or_never: 0,
    unsupported: 0,
    fresh_seconds: 0,
    notes: [],
  },
  filters: {
    environments: [],
    db_types: [],
    businesses: [],
    engines: [],
  },
  instances: [],
  alerts: [],
  screen: createDefaultScreen(),
})

export const createDefaultDetail = () => ({
  generated_at: '',
  filters: {
    environments: [],
    db_types: [],
    engines: [],
  },
  instances: [],
  selected: null,
})

// 明细页"当前生效的指标模板"：后端一定下发，这里给出前端兜底结构，
// 避免某个字段缺失时模板渲染直接炸掉
export const createDefaultInstanceTemplate = () => ({
  name: '',
  display_name: '',
  db_type: '',
  is_default: false,
  builtin: false,
  metric_count: 0,
  bound_name: '',
  fallback: false,
  state: '',
  message: '',
})

export const createDefaultStatus = () => ({
  generated_at: '',
  prometheus: { configured: false, available: false, base_url: '', message: '' },
  collect: { ok: 0, partial: 0, failed: 0, stale_or_never: 0, unsupported: 0, fresh_seconds: 0, notes: [] },
  engines: [],
  failures: [],
  runner: {
    collect_interval_seconds: 10,
    alert_interval_seconds: 60,
    workers: 12,
    latest_collect_at: '',
    average_collect_ms: 0,
    slowest_collect_ms: 0,
    on_demand_limit: 20,
  },
})

// 指标模板列表 /monitor/templates
export const createDefaultTemplates = () => ({
  generated_at: '',
  db_types: [],
  engines: [],
  host_policy: '',
  templates: [],
})

// 指标模板详情 /monitor/templates/:id
export const createDefaultTemplateDetail = () => ({
  generated_at: '',
  template: null,
  metrics: [],
  applicable: [],
  host_metrics: [],
  host_policy: '',
})

export const sourceText = (value) => {
  if (value === 'prometheus') return 'Prometheus'
  if (value === 'direct') return '直连采集'
  if (value === 'none') return '已关闭'
  return '暂无数据'
}

export const statusColor = (value) => {
  if (value === 'healthy') return 'green'
  if (value === 'warning') return 'orange'
  return 'red'
}

export const statusText = (value) => {
  if (value === 'healthy') return '健康'
  if (value === 'warning') return '告警'
  return '降级'
}

// ---------------------------------------------------------------------------
// 数据质量（quality 五态）
//
// 这是本次改造最重要的一条前端硬规则：quality !== 'ok' 时一律显示 "—"，
// 绝不显示 0。历史上 3 个 PG 实例 QPS 恒为 0、5 个未接 Prometheus 的实例
// CPU/内存/磁盘恒为 0，页面把 0 当真实值展示 —— 这是最危险的一类错觉。
// ---------------------------------------------------------------------------

export const QUALITY_TEXT = {
  ok: '正常',
  unsupported: '不适用',
  stale: '数据滞后',
  error: '采集失败',
  no_data: '无数据',
  partial: '部分可用',
  // disabled 是"配置上就没采"，与"试图采但没拿到"完全不同的语义：
  // 混用会让运维无法判断缺失是忘了勾选模板，还是真的出故障。
  disabled: '未启用',
}

export const qualityText = (value) => QUALITY_TEXT[value] || value || '未知'

export const qualityColor = (value) => {
  if (value === 'ok') return 'green'
  if (value === 'partial') return 'blue'
  if (value === 'stale') return 'orange'
  if (value === 'unsupported') return 'default'
  if (value === 'no_data') return 'default'
  if (value === 'disabled') return 'default'
  return 'red'
}

/** 未启用（不在模板内）的额外说明，用于 tooltip；非 disabled 返回空串。 */
export const qualityHint = (value) => {
  if (value === 'disabled') return '该指标不在当前指标模板内，本轮未采集；如需采集请在「指标模板」中勾选'
  if (value === 'unsupported') return '当前数据库引擎或权限不支持该指标'
  return ''
}

export const collectStatusText = (value) => {
  if (value === 'success') return '成功'
  if (value === 'failed') return '失败'
  if (value === 'partial') return '部分成功'
  return '未采集'
}

export const collectStatusColor = (value) => {
  if (value === 'success') return 'green'
  if (value === 'partial') return 'orange'
  if (value === 'failed') return 'red'
  return 'default'
}

export const deployModeText = (value) => {
  if (value === 'master_slave') return '主从'
  if (value === 'cluster') return '分布式'
  if (value === 'standalone') return '单机'
  return value || '单机'
}

export const categoryLabel = (value) => {
  if (value === 'availability') return '可用性'
  if (value === 'capacity') return '容量'
  if (value === 'performance') return '性能'
  if (value === 'risk') return '风险'
  if (value === 'change') return '变更'
  return value || '其他'
}

export const categoryColor = (value) => {
  if (value === 'availability') return 'red'
  if (value === 'capacity') return 'orange'
  if (value === 'performance') return 'blue'
  if (value === 'risk') return 'purple'
  return 'default'
}

// kernel 层在不同引擎下指代不同的内核对象，标题必须跟着引擎走：
//   MySQL / Doris → InnoDB 引擎计数器；TiDB → TiKV / PD 集群组件；
//   PostgreSQL → autovacuum / lwlock 等内核视图。
// 调用方传 dbType 时给准确文案，不传时用通用表述。
export const layerLabel = (value, dbType = '') => {
  if (value === 'host') return '主机层'
  if (value === 'engine') return '数据库层'
  if (value === 'kernel') {
    const engine = String(dbType || '').toLowerCase()
    if (engine === 'tidb') return '集群指标'
    if (engine === 'postgresql') return '内核指标'
    if (engine === 'mysql' || engine === 'doris') return 'InnoDB 引擎指标'
    return '引擎内核指标'
  }
  return value || '其他'
}

export const layerColor = (value) => {
  if (value === 'host') return 'blue'
  if (value === 'engine') return 'orange'
  if (value === 'kernel') return 'purple'
  return 'default'
}

export const layerDesc = (value) => {
  if (value === 'host') return 'Prometheus · node_exporter'
  if (value === 'engine') return '跨库通用语义 · 直连采集'
  if (value === 'kernel') return '数据库方言专有 · 直连采集'
  return ''
}

/** 指标键前缀 → 层级。与后端 metricLayer 同一口径，前端要按层分组展示时用它。 */
export const metricLayerOf = (key) => {
  const value = String(key || '')
  if (value.startsWith('res.')) return 'host'
  if (value.startsWith('kern.')) return 'kernel'
  return 'engine'
}

export const levelColor = (value) => {
  if (value === '高' || value === 'P1') return 'red'
  if (value === '中' || value === 'P2') return 'orange'
  if (value === '低' || value === 'P3') return 'gold'
  return 'default'
}

export const formatCount = (value) => {
  const number = Number(value || 0)
  if (Math.abs(number) >= 1000) {
    return number.toLocaleString('zh-CN', { maximumFractionDigits: 2 })
  }
  return number.toFixed(number >= 100 ? 0 : 2).replace(/\.00$/, '')
}

export const formatBytes = (value) => {
  const number = Number(value || 0)
  if (number <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = number
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index += 1
  }
  return `${size.toFixed(size >= 100 ? 0 : 2).replace(/\.00$/, '')} ${units[index]}`
}

/**
 * 不可用一律显示 "—"（不是 0）。可用性由 available 决定，
 * 而 available 的充要条件是 quality === 'ok'。
 */
export const formatMetric = (metric) => {
  if (!metric || metric.available !== true) return '—'
  if (metric.unit === '%') return `${Number(metric.value || 0).toFixed(2)}%`
  if (metric.unit === 'B/s') return `${formatBytes(metric.value)}/s`
  if (metric.unit === 'ms') return `${formatCount(metric.value)} ms`
  if (metric.unit === 'count' || metric.unit === '') return formatCount(metric.value)
  return `${formatCount(metric.value)} ${metric.unit}`
}

/** 指标不可用时给用户的解释（用于 tooltip），可用时返回空串。 */
export const metricReason = (metric) => {
  if (!metric || metric.available === true) return ''
  const reason = metric.reason || ''
  const label = qualityText(metric.quality)
  return reason ? `${label}：${reason}` : label
}

/** 采集时间的相对描述，用于"数据是否已经太旧"的直观判断。 */
export const freshnessText = (seconds) => {
  const value = Number(seconds || 0)
  if (!value) return '刚刚'
  if (value < 60) return `${value} 秒前`
  if (value < 3600) return `${Math.floor(value / 60)} 分钟前`
  if (value < 86400) return `${Math.floor(value / 3600)} 小时前`
  return `${Math.floor(value / 86400)} 天前`
}

export const freshnessColor = (seconds, limit = 180) => {
  const value = Number(seconds || 0)
  if (value <= 0) return 'default'
  if (value <= limit) return 'green'
  if (value <= limit * 5) return 'orange'
  return 'red'
}

export const progressStatus = (value) => {
  if (value >= 90) return 'exception'
  if (value >= 80) return 'active'
  return 'normal'
}

export const metricTone = (metric) => {
  if (!metric?.available) return 'muted'
  if (metric.unit === '%' && metric.value >= 90) return 'danger'
  if (metric.unit === '%' && metric.value >= 80) return 'warning'
  return 'normal'
}

export const metricNumber = (metric) => (metric?.available ? Number(metric.value || 0) : 0)

// ---------------------------------------------------------------------------
// 大屏（监控大屏）专用：状态四分类、区间口径、未获取占位。
// 硬规则：任何字段取不到一律渲染「未获取」，绝不填 0 或模拟数 ——
// 0 是合法读数，"没拿到"必须显式可见。
// ---------------------------------------------------------------------------

export const createDefaultScreen = () => ({
  range_minutes: 30,
  range_start: '',
  range_end: '',
  range_label: '',
  range_short: '',
  range_custom: false,
  status: { healthy: 0, risk: 0, fault: 0, uncollected: 0 },
  status_prev: null,
  alerts_in_range: 0,
  // 「告警数」卡：待处理/处理中为实时快照，已解决为当前时间范围内（按恢复时间）
  alert_counts: { pending: 0, processing: 0, resolved: 0 },
  alerts_prev_range: 0,
  acked_alerts: [],
  recovered_alerts: [],
  instance_stats: {},
  risk_top: [],
})

// createDefaultRiskOverview /monitor/risks 风险总览默认结构。
export const createDefaultRiskOverview = () => ({
  generated_at: '',
  total: 0,
  reason_total: 0,
  by_severity: { P1: 0, P2: 0, P3: 0 },
  instances: [],
})

// fmtMetricNum 指标数值的通用展示精度：四舍五入保留至多 2 位小数，
// 消除浮点噪声（如 88.31949500237403 → 88.32、70 → 70）；
// 空/非有限数字原样返回（交由调用方决定 '—' 等兜底文案）。
export const fmtMetricNum = (value) => {
  if (value === null || value === undefined || value === '') return ''
  const num = Number(value)
  if (!Number.isFinite(num)) return String(value)
  return String(Math.round(num * 100) / 100)
}

// clipText 超长文本按字符数截断，超出部分用 ... 表示；
// 调用方一般用 :title 保留完整值（与 performance/utils.js 的同名工具口径一致）。
export const clipText = (value, max) => {
  const text = String(value ?? '').trim()
  const limit = Number(max)
  if (!text || !Number.isFinite(limit) || limit <= 0) return text
  return text.length > limit ? `${text.slice(0, limit)}...` : text
}

// reasonValueText 风险原因的数值展示：null 显示「未获取」，绝不显示 0 冒充。
export const reasonValueText = (reason, field) => {
  const value = reason?.[field]
  if (value === null || value === undefined) return '未获取'
  const unit = reason.unit || ''
  return `${fmtMetricNum(value)}${unit}`
}

export const SCREEN_CLASS_TEXT = {
  healthy: '健康',
  risk: '风险',
  fault: '故障',
  uncollected: '未采集',
}

export const screenClassText = (value) => SCREEN_CLASS_TEXT[value] || '未获取'

export const screenClassColor = (value) => {
  if (value === 'fault') return 'red'
  if (value === 'risk') return 'orange'
  if (value === 'healthy') return 'green'
  if (value === 'uncollected') return 'default'
  return 'default'
}

/** 大屏数值占位：值为 null/undefined 时显示「未获取」。 */
export const screenValue = (value, formatter) => {
  if (value === null || value === undefined) return '未获取'
  return formatter ? formatter(value) : String(value)
}

// 区间文案已改为后端下发（screen.range_label / screen.range_short）——
// 前端不再用小时数自行拼接，避免"显示最近1小时、实际按30分钟算"的错位。

/** 告警持续时长：秒 → 分钟/小时口径，缺失返回「未获取」。 */
export const alertDurationText = (seconds) => {
  if (seconds === null || seconds === undefined) return '未获取'
  if (seconds < 60) return `已持续 ${seconds} 秒`
  if (seconds < 3600) return `已持续 ${Math.floor(seconds / 60)} 分钟`
  return `已持续 ${(seconds / 3600).toFixed(1)} 小时`
}

/**
 * 告警持续时长（紧凑口径，不带「已持续」前缀）：秒 → 1h57m / 57m / 45s / 2d3h。
 * 用于需要把时长拼进整句的场景（如「…阈值 != 1.00。已持续 1h57m」），
 * 与 alertDurationText 的中文口径（1.9 小时）区分开，避免同页两种写法。
 */
export const alertDurationCompact = (seconds) => {
  if (seconds === null || seconds === undefined || seconds <= 0) return ''
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const secs = Math.floor(seconds % 60)
  if (days > 0) return hours > 0 ? `${days}d${hours}h` : `${days}d`
  if (hours > 0) return minutes > 0 ? `${hours}h${minutes}m` : `${hours}h`
  if (minutes > 0) return `${minutes}m`
  return `${secs}s`
}

/** 告警状态 → 大屏页签归属：等级（待认领/观察中）、处理中（已认领）、已解决。 */
export const alertBucket = (alert) => {
  // 按原始状态码判断；status_key 优先，兼容只带展示词的历史调用。
  const key = alert?.status_key || alert?.status
  if (key === 'acknowledged') return 'acked'
  if (key === 'recovered') return 'recovered'
  return 'firing'
}

// ---------------------------------------------------------------------------
// 明细页（按 Demo 还原）使用的展示映射
// 统一放在 utils 里，避免页面各写一套导致文案/配色漂移
// ---------------------------------------------------------------------------

/** 质量标签：直接复用 QUALITY_TEXT，命名对齐明细页用法。 */
export const QUALITY_LABEL = QUALITY_TEXT

/** 质量标签的样式类（对应明细页 .q-* 系列）。 */
export const qualityTagClass = (value) => {
  if (value === 'ok') return 'q-ok'
  if (value === 'partial') return 'q-partial'
  if (value === 'warn') return 'q-warn'
  if (value === 'error') return 'q-error'
  return 'q-off'
}

/** 四分类圆点配色，与页面徽标一致。色值直接取自语义 Token，不在本文件另写一套。 */
export const CLASS_DOT_COLOR = { ...INSTANCE_STATUS_COLORS }

// ---------------------------------------------------------------------------
// 比较符中文化（展示层唯一映射源）
//
// 契约：operator 的枚举值（>= <= > < = !=）是接口参数与比较逻辑的一部分，
// 数据库列、表单提交值、后端 binding 校验全部保持原样，**任何地方都不得改动枚举值**。
// 这里只负责「渲染成人话」，因此历史数据无需迁移也能正确显示中文。
// ---------------------------------------------------------------------------

/** 比较符 → 中文。新增操作符时只改这一张表，全模块文案即同步。 */
const OPERATOR_TEXT = {
  '>=': '大于等于',
  '<=': '小于等于',
  '!=': '不等于',
  '>': '大于',
  '<': '小于',
  '=': '等于',
}

/**
 * 纯操作符字段 → 中文（用于「比较符」这类只放符号的列/标签）。
 * 空值给「—」；未知取值原样返回，保证不吞信息、便于发现新枚举。
 */
export const operatorText = (operator) => {
  const key = String(operator ?? '').trim()
  if (!key) return '—'
  return OPERATOR_TEXT[key] || key
}

/**
 * 自由文本里的比较符 → 中文（用于后端拼好的整句：告警正文、规则名、判定依据等）。
 * 只替换多字符符号 >= <= !=：单字符 > < = 不替换 ——
 * 指标名里有「空闲事务数(>1s)」「state=active」这类原文，替换会扭曲原意。
 */
export const operatorTextInText = (text) => {
  if (text === null || text === undefined || text === '') return text
  return String(text)
    .replace(/>=/g, '大于等于')
    .replace(/<=/g, '小于等于')
    .replace(/!=/g, '不等于')
}

// ---------------------------------------------------------------------------
// 告警中心展示映射（告警中心各表格与监控大屏下钻弹窗共用，唯一映射源）
// ---------------------------------------------------------------------------

/** 告警级别 P0–P3：label 直接用等级名；颜色用系统语义 token，P0 用 color-mix 压深。
 *  P0 深红 → P1 红 → P2 橙（--gi-color-warning）为暖色紧迫度阶；
 *  P3 用柔和紫 #8c6fd9——原 --gi-color-info 蓝在图表大扇区里过于突兀，
 *  且绿色已被「已恢复」状态占用，紫色与整页告警配色最协调且无撞色。 */
export const ALERT_LEVEL_DEFS = [
  { key: 'P0', label: 'P0', color: 'color-mix(in srgb, var(--gi-color-error), #000 25%)', tagColor: 'red' },
  { key: 'P1', label: 'P1', color: 'var(--gi-color-error)', tagColor: 'red' },
  { key: 'P2', label: 'P2', color: 'var(--gi-color-warning)', tagColor: 'orange' },
  { key: 'P3', label: 'P3', color: '#8c6fd9', tagColor: 'purple' },
]

/** 级别 → 展示元数据；未知值回落 P2（与历史口径一致）。 */
export const alertLevelMeta = (severity) =>
  ALERT_LEVEL_DEFS.find((d) => d.key === String(severity || '').toUpperCase()) ||
  ALERT_LEVEL_DEFS[2]

/** 告警状态 → 状态色 key。返回值是 chartTokens 的告警 token key
 *  （pending/doing/resolved），CSS 侧用 var(--chart-alert-<key>) 消费，
 *  与右上角告警圆饼扇区同一真源（ALERT_STATUS_COLORS / applyChartTokens 注入），
 *  未知状态回落 'default'（继承默认文本色）。 */
export const alertStatusColor = (value) =>
  ALERT_STATUS_KEY_BY_BACKEND[value] || 'default'

/**
 * 告警名称中混入的等级标识（P0/P1/P2/P3）剔除，并清理多余空格：
 *   - 匹配独立出现的 P0–P3（含前后空白），移除后用单个空格填充；
 *   - 将连续多个空白折叠为单个空格；
 *   - 首尾 trim；
 *   - 如果名称中本就不含等级标识，原样返回。
 * 注意：只处理展示文本，不改变接口原始数据。
 */
const SEVERITY_TOKEN_RE = /\s*\bP[0-3]\b\s*/gi
export const cleanSeverityInName = (text) => {
  if (text === null || text === undefined || text === '') return text
  return String(text)
    .replace(SEVERITY_TOKEN_RE, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}
