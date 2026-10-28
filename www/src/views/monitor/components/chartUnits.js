// ============================================================================
// 监控图表单位格式化 —— MetricTrendChart 与 DetailView 共用
//
// 职责：
//   1. 由单位字符串推断量纲（bytes / percent / duration / rate / count / gauge）；
//   2. 字节量纲统一折算成「原始字节」后再按大小自动换算 B/KB/MB/GB 展示；
//   3. 双 Y 轴的轴分配依据就是这里的量纲：只有量纲不同才启用双轴。
//
// 字节折算约定：后端下发的 series.value 已经乘过目录里的 Scale（进制换算），
// 前端再乘「单位对应的字节因子」恰好还原成原始字节数。例如网络流量
// （采集 B/s，Scale=1/1024，Unit=KB）：value × 1024 = 原始 B/s。
//
// ⚠️ 只有轴极力计算的助记：图表内部把每条序列都折算回「原始字节」再定刻度，
// 因此凡是用到 Number.isFinite 数值的地方，必须自己走 toBytes 折算 ——
// 直接把已折过的值交给 fmtByteAuto，会得到「数值小了 1024^n 倍」的假象。
// ============================================================================

const BYTE_UNITS = { b: 1, kb: 1024, mb: 1024 ** 2, gb: 1024 ** 3 }

// 速率尾缀：B/s、KB/s、MB/s 这类「字节 + 时间」单位与纯字节同量纲，
// 共用一套自动换算，只在文本上保留 /s 尾缀。
const RATE_SUFFIX_RE = /\/(s|sec|second)$/i
// 裸单位：把单位末尾的 /s 去掉后的部分（B/s → b）
const bareUnit = (unit) => String(unit || '').trim().toLowerCase().replace(RATE_SUFFIX_RE, '')

// 量纲推断：与图表 Y 轴类型判定共用一套口径
export function unitDim(unit) {
  const bare = bareUnit(unit)
  if (bare in BYTE_UNITS || bare === 'byte' || bare === 'bytes') return 'bytes'
  const u = String(unit || '').trim().toLowerCase()
  if (u === '%' || u === 'pct' || u === 'percent') return 'percent'
  if (u === 'ms' || u === 's' || u === 'us' || u === 'min') return 'duration'
  if (/\/(s|sec|second)$/.test(u)) return 'rate' // req/s 等非字节速率
  if (u === 'count' || u === 'conn' || u === '个' || u === '次') return 'count'
  return 'gauge'
}

// 单位对应的字节因子；非字节量纲返回 null
export function unitToBytes(unit) {
  return BYTE_UNITS[bareUnit(unit)] ?? null
}

// 展示值 → 原始字节。非字节量纲原样返回（调用方需自行判断量纲）。
// 例：5.24 KB → 5365.76；512 B → 512。
export function toBytes(value, unit) {
  const num = Number(value)
  if (!Number.isFinite(num)) return null
  const factor = unitToBytes(unit)
  return factor == null ? num : num * factor
}

// 单位里的时间尾缀（"/s"、"/sec"），纯字节返回空串
export function unitRateSuffix(unit) {
  const m = String(unit || '').trim().match(RATE_SUFFIX_RE)
  return m ? `/${m[1]}` : ''
}

function trimNum(num) {
  return String(Number(num.toFixed(2)))
}

// 字节值 → 自动换算的可读文本（"127.87 KB" / "3 MB" / "512 B" / "2 TB"）
export function fmtByteAuto(bytes) {
  const num = Number(bytes)
  if (!Number.isFinite(num)) return '—'
  const abs = Math.abs(num)
  if (abs >= 1024 ** 4) return `${trimNum(num / 1024 ** 4)} TB`
  if (abs >= 1024 ** 3) return `${trimNum(num / 1024 ** 3)} GB`
  if (abs >= 1024 ** 2) return `${trimNum(num / 1024 ** 2)} MB`
  if (abs >= 1024) return `${trimNum(num / 1024)} KB`
  return `${trimNum(num)} B`
}

// 通用数值（Tooltip / 图例用）：千分位 + 按量级取小数位
export function fmtNumber(value) {
  const num = Number(value)
  if (!Number.isFinite(num)) return '—'
  const abs = Math.abs(num)
  const digits = abs >= 100 ? 0 : abs >= 1 ? 2 : 4
  return num.toLocaleString('zh-CN', { maximumFractionDigits: digits })
}

// 带单位的完整取值文本（Tooltip / 图例）：字节量纲自动换算并自带单位，
// 其他量纲「数值 + 原始单位」；单位为 "-"（如复制线程状态）时不追加。
export function fmtDimValue(value, unit) {
  if (value == null || !Number.isFinite(Number(value))) return '—'
  if (unitDim(unit) === 'bytes') {
    // value 已被 Scale 折过（如 KB），先还原成原始字节再自动换算，
    // 否则 5.24 KB/s 会被当成 5.24 B —— 这正是历史上单位显示小 1024^n 倍的原因
    const bytes = toBytes(value, unit)
    if (bytes == null) return '—'
    return `${fmtByteAuto(bytes)}${unitRateSuffix(unit)}`
  }
  const u = String(unit || '').trim()
  const text = fmtNumber(value)
  return u && u !== '-' ? `${text} ${u}` : text
}

// 字节值 → 按「参考量级」统一换算的可读文本（同一根轴共用一个单位）。
// 逐个刻度自行换算会得到 "0 B / 976.56 KB / 1.91 MB / 2.86 MB" 这种混排：
// 数值都在变、单位也在变，人眼没法直接比较刻度间距。
// 参考量级取该轴的最大刻度值，整根轴就锁在 KB 或 MB 或 GB 上。
// 0 刻度只写 "0"（任何单位下都是 0，带单位纯属噪音）。
// step：Y 轴刻度步长（字节）。用于决定小数位：
//   step ≥ 1 个当前单位 → 整数；step ≥ 0.1 单位 → 1 位小数；否则 2 位小数。
//   这样既保证大数值刻度取整，又避免 55 GB 附近 0.1 GB 步长时所有标签都重复。
export function fmtByteAutoAt(bytes, refBytes, step = null) {
  const num = Number(bytes)
  if (!Number.isFinite(num)) return '—'
  if (num === 0) return '0'
  const ref = Math.abs(Number(refBytes))
  if (!Number.isFinite(ref)) return fmtByteAuto(num)

  let factor = 1
  let unit = 'B'
  if (ref >= 1024 ** 4) { factor = 1024 ** 4; unit = 'TB' }
  else if (ref >= 1024 ** 3) { factor = 1024 ** 3; unit = 'GB' }
  else if (ref >= 1024 ** 2) { factor = 1024 ** 2; unit = 'MB' }
  else if (ref >= 1024) { factor = 1024; unit = 'KB' }

  const v = num / factor
  const stepInUnit = Number.isFinite(step) && step > 0 ? step / factor : null
  let digits = 2
  if (stepInUnit == null || stepInUnit >= 1) digits = 0
  else if (stepInUnit >= 0.1) digits = 1

  return `${v.toFixed(digits)} ${unit}`
}

// Y 轴刻度文本：字节量纲按整轴统一单位换算（"1.91 MB"），其余沿用紧凑缩写（k/M/G）
// refMax：该轴的最大刻度值，用作单位量级基准；不传时退回逐值自动换算。
export function fmtAxisTick(value, unit, step, refMax) {
  const num = Number(value)
  if (!Number.isFinite(num)) return '—'
  if (unitDim(unit) === 'bytes') {
    // 按 step 决定小数位，避免 55 GB 附近小步长时所有刻度标签重复。
    return fmtByteAutoAt(num, refMax == null ? num : refMax, step)
  }
  if (unitDim(unit) === 'percent') {
    const s = String(step)
    let digits = 0
    if (Number.isFinite(step) && step > 0 && !s.includes('e-')) {
      const dot = s.indexOf('.')
      digits = dot === -1 ? 0 : Math.min(6, s.length - dot - 1)
    }
    return `${String(Number(num.toFixed(digits)))}%`
  }
  if (num === 0) return '0'
  const abs = Math.abs(num)
  if (abs >= 1e9) return `${trimNum(num / 1e9)}G`
  if (abs >= 1e6) return `${trimNum(num / 1e6)}M`
  if (abs >= 1e4) return `${trimNum(num / 1e3)}k`
  const s = String(step)
  let digits = 2
  if (Number.isFinite(step) && step > 0 && !s.includes('e-')) {
    const dot = s.indexOf('.')
    digits = dot === -1 ? 0 : Math.min(6, s.length - dot - 1)
  }
  return String(Number(num.toFixed(digits)))
}
