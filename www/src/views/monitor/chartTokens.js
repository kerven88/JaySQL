// ---------------------------------------------------------------------------
// 大屏图表语义色 Token —— 方案 A 单一数据源
//
// 背景：最初左右两套语义使用不同色相空间（左深红/琥珀，右亮红/橙）以便区分；
// 后按用户要求改为同名状态同色：右侧「待认领」复用左侧「故障」深红、
// 「处理中」复用「风险」琥珀，跨组件看到同一颜色即同一含义。
//
// 本文件是全站唯一真源：
//   · JS 侧（SVG stroke）用 chartColor() 取十六进制；
//   · CSS 侧（图例圆点、实例卡色条、告警卡片状态条）用 var(--chart-*)，
//     由 applyChartTokens() 在启动时注入，值与这里永远一致，不会各写一套而漂移。
// ---------------------------------------------------------------------------

/** 实例健康四分类（左侧圆饼 / 实例卡 / 明细页四分类圆点共用）。 */
export const INSTANCE_STATUS_COLORS = {
  healthy: '#0f9f6f',
  risk: '#ef9f27',
  fault: '#b3261e',
  uncollected: '#888780',
}

/**
 * 告警处理三态（右侧圆饼 / 告警卡片状态条 / 事件中心 tab 标签共用）。
 * key 沿用前端 alert_counts 的字段名：pending / doing / resolved。
 * 配色与左侧实例健康语义对齐（用户要求同名状态同色）：
 * 待认领=故障色（深红，最需要人介入）、处理中=风险色（琥珀，有人接手但未闭环）、
 * 已恢复=绿（闭环）。
 */
export const ALERT_STATUS_COLORS = {
  pending: '#b3261e',
  doing: '#ef9f27',
  resolved: '#0f9f6f',
}

/**
 * 后端告警状态枚举 → 上面的 key。圆饼按后端三段（active/acknowledged/recovered）
 * 分段，取值时经此表归一，避免同一处逻辑里两套命名并存。
 */
export const ALERT_STATUS_KEY_BY_BACKEND = {
  active: 'pending',
  acknowledged: 'doing',
  recovered: 'resolved',
}

/** 空态色：各类计数全为 0 时画灰色空环，与「有数据」明确区分。 */
export const CHART_EMPTY_COLOR = '#e2e8f0'

/** CSS 自定义属性前缀，与 scope/key 拼成 --chart-instance-fault 这类名字。 */
const CSS_VAR_PREFIX = '--chart-'

/** scope → 色板。将来新增语义分类只改这一张表。 */
const CHART_PALETTES = {
  instance: INSTANCE_STATUS_COLORS,
  alert: ALERT_STATUS_COLORS,
}

/** scope + key → CSS 变量名，如 ('instance', 'fault') → '--chart-instance-fault'。 */
export function chartCssVar(scope, key) {
  return `${CSS_VAR_PREFIX}${scope}-${key}`
}

/** 展开后的 token 列表，applyChartTokens() 与自检遍历它即可。 */
export const CHART_TOKENS = Object.keys(CHART_PALETTES).flatMap((scope) =>
  Object.keys(CHART_PALETTES[scope]).map((key) => ({
    scope,
    key,
    value: CHART_PALETTES[scope][key],
    cssVar: chartCssVar(scope, key),
  })),
)

/**
 * 取十六进制色值。
 * 未知 scope / key 一律回落空态灰，保证渲染不出现 undefined 或透明描边。
 */
export function chartColor(scope, key) {
  const palette = CHART_PALETTES[scope]
  return (palette && palette[key]) || CHART_EMPTY_COLOR
}

/**
 * 取 CSS 引用形式：var(--chart-instance-fault, <兜底色>)。
 * 兜底写死同色十六进制，即使 JS 注入未执行（异常/降级）样式也不会丢色。
 */
export function chartCssColor(scope, key) {
  return `var(${chartCssVar(scope, key)}, ${chartColor(scope, key)})`
}

/**
 * 把 token 注入为 CSS 自定义属性，让样式表能消费同一份值。
 * 无 DOM 环境（SSR / 单测）静默返回 false，不抛错。
 */
export function applyChartTokens(root) {
  const target = root || (typeof document !== 'undefined' ? document.documentElement : null)
  if (!target || typeof target.style?.setProperty !== 'function') return false
  CHART_TOKENS.forEach(({ cssVar, value }) => {
    // 空名字或空值跳过，避免写入无效属性把 var() 解析成空
    if (!cssVar || !value) return
    target.style.setProperty(cssVar, value)
  })
  target.style.setProperty(`${CSS_VAR_PREFIX}empty`, CHART_EMPTY_COLOR)
  return true
}
