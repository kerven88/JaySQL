// ---------------------------------------------------------------------------
// 健康分扣分规则：前端唯一取数点
// ---------------------------------------------------------------------------
// 数据来源就是「告警配置 → 健康判定」那一份（GET /api/v1/monitor/config/health-grade
// → services.HealthStateConfig.deductions），后端 evaluateInstanceState 算健康分时
// 用的也是同一份扣分表。任何需要展示"这条告警扣几分"的地方都必须走这里，
// 不允许在组件里另写一套默认值 —— 否则页面上会出现两套分数。
//
// 口径对齐要点（与后端 monitor_health_state.go 一致）：
//   - 未知/缺失的 severity 统一按 P3 计（后端 healthStateSeverityOf 同逻辑）；
//   - 扣分表取不到或取失败时不静默猜数字，返回 null → 展示「—」。
import { getMonitorHealthGradeApi } from '@/api/monitor'

// 内置等级序：与后端 healthStateLevels 保持一致
const LEVELS = ['P0', 'P1', 'P2', 'P3']
const FALLBACK_LEVEL = 'P3'

// 模块级缓存：弹窗反复开关 / 多个入口同时用时不重复打接口。
// TTL 与后端配置缓存（10s）同量级，保存后重新打开弹窗即可看到新扣分。
const CACHE_TTL_MS = 10 * 1000
let cache = null
let cacheAt = 0
let inflight = null

/** 归一化 severity → P0~P3；未知值按 P3（与后端 healthStateSeverityOf 同口径）。 */
export function normalizeSeverity(severity) {
  const code = String(severity || '').trim().toUpperCase()
  return LEVELS.includes(code) ? code : FALLBACK_LEVEL
}

/**
 * 取当前生效的扣分表 { P0: 40, P1: 10, ... }。
 * force=true 时强制刷新（保存配置后立刻生效的场景用）。
 * 接口不可用返回 null，由调用方降级展示「—」，绝不回退成硬编码数字。
 */
export async function loadHealthDeductions({ force = false } = {}) {
  const fresh = cache && Date.now() - cacheAt < CACHE_TTL_MS
  if (!force && fresh) return cache
  if (inflight) return inflight
  inflight = (async () => {
    try {
      const res = await getMonitorHealthGradeApi()
      const raw = res?.data?.deductions || {}
      const table = {}
      for (const level of LEVELS) {
        const value = Number(raw?.[level])
        if (Number.isFinite(value) && value >= 0) table[level] = value
      }
      cache = Object.keys(table).length ? table : null
      cacheAt = Date.now()
      return cache
    } catch (error) {
      // 取不到规则不能让弹窗开天窗：返回 null，单元格展示「—」
      cache = null
      cacheAt = Date.now()
      return cache
    } finally {
      inflight = null
    }
  })()
  return inflight
}

/** 单条告警的扣分：命中规则给数字，没有对应规则给 null（展示「—」）。 */
export function deductionOf(table, severity) {
  if (!table) return null
  const value = table[normalizeSeverity(severity)]
  return Number.isFinite(value) ? value : null
}

/** 单元格文案：有规则显示扣分值，无规则显示「—」。 */
export function deductionText(table, severity) {
  const value = deductionOf(table, severity)
  return value == null ? '—' : String(value)
}
