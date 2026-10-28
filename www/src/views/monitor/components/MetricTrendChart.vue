<template>
  <div
    class="mtc"
    :class="`tone-${tone}`"
    :style="{ minHeight: `${height}px`, height: fill ? '100%' : `${height}px` }"
  >
    <svg
      v-if="chart"
      ref="svgEl"
      class="mtc-svg"
      :class="{ 'mtc-svg-selectable': selectable }"
      :viewBox="`0 0 ${chart.width} ${chart.height}`"
      preserveAspectRatio="none"
      @mousemove="onMove"
      @mouseleave="onLeave"
      @mousedown="onDown"
      @dblclick="onDblClick"
    >
      <rect class="mtc-axis" :x="chart.padL" :y="chart.padT" :width="chart.w" :height="chart.h" />
      <line
        v-for="(g, i) in chart.hGrid"
        :key="'g' + i"
        class="mtc-grid"
        :x1="g.x1"
        :y1="g.y"
        :x2="g.x2"
        :y2="g.y"
      />
      <g v-for="(s, i) in chart.series" :key="`s${i}`">
        <!-- 只画线，不画面积：多序列叠加时面积会互相遮挡，且浓淡块会干扰读数 -->
        <path
          class="mtc-line"
          :d="s.linePath"
          :style="s.color !== 'currentColor' ? { stroke: s.color } : undefined"
        />
      </g>
      <g v-if="hover">
        <line
          class="mtc-cross"
          :x1="hover.cx"
          :y1="chart.padT"
          :x2="hover.cx"
          :y2="chart.padT + chart.h"
        />
        <circle
          v-for="(row, i) in hover.rows"
          :key="`dot${i}`"
          class="mtc-hover-dot"
          :cx="row.coord?.[0] ?? hover.cx"
          :cy="row.coord?.[1] ?? hover.cy"
          r="3"
          :style="row.color !== 'currentColor' ? { fill: row.color, stroke: row.color } : undefined"
        />
      </g>
      <!-- 时间区间拖拽选区：半透明主色矩形 + 两条边界线，拖拽中与松手后共用 -->
      <g v-if="chart && selRect">
        <rect
          class="mtc-sel"
          :x="selRect.x0"
          :y="chart.padT"
          :width="Math.max(1, selRect.x1 - selRect.x0)"
          :height="chart.h"
        />
        <line class="mtc-sel-edge" :x1="selRect.x0" :y1="chart.padT" :x2="selRect.x0" :y2="chart.padT + chart.h" />
        <line class="mtc-sel-edge" :x1="selRect.x1" :y1="chart.padT" :x2="selRect.x1" :y2="chart.padT + chart.h" />
      </g>
      <text
        v-for="(label, i) in chart.yLabels"
        :key="'y' + i"
        :x="chart.padL - 6"
        :y="label.y"
        text-anchor="end"
      >
        {{ label.text }}
      </text>
      <!-- 右轴（双轴时出现）：刻度贴右轴轴线外侧 -->
      <text
        v-for="(label, i) in chart.yLabelsRight"
        :key="'yr' + i"
        :x="chart.width - chart.padR + 6"
        :y="label.y"
        text-anchor="start"
      >
        {{ label.text }}
      </text>
      <text
        v-for="(label, i) in chart.xLabels"
        :key="'x' + i"
        :x="label.x"
        :y="chart.height - 4"
        text-anchor="middle"
      >
        {{ label.text }}
      </text>
    </svg>
    <div
      v-if="chart && hover"
      class="mtc-tip"
      :style="tipStyle"
    >
      <span class="tip-time">{{ timeText }}</span>
      <div class="tip-rows">
        <div v-for="(row, i) in tipRows" :key="`r${i}`" class="tip-row">
          <span class="tip-dot" :style="row.color !== 'currentColor' ? { background: row.color } : undefined"></span>
          <span class="tip-text">{{ row.text }}</span>
        </div>
      </div>
    </div>
    <!-- 拖拽选区的时间区间提示：拖拽中实时跟随，松手后保留到数据刷新 -->
    <div v-if="selRect" class="mtc-sel-tip" :style="selTipStyle">
      <span class="tip-time">
        {{ fmtFullTime(selRect.startTs) }} ~ {{ fmtFullTime(selRect.endTs) }}
        <template v-if="selRect.spanText">（{{ selRect.spanText }}）</template>
      </span>
    </div>
    <span v-if="!chart" class="mtc-empty">暂无趋势数据</span>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import {
  fmtAxisTick,
  fmtDimValue,
  unitDim,
  unitToBytes,
} from './chartUnits'
import { curveColorAt } from '../detail/detailTheme'

// ---------------------------------------------------------------------------
// 指标趋势图（手写 SVG，不依赖任何图表库）
//
// 之所以拆成独立子组件：明细页一个组件里挂着 30~40 张图，悬停是高频交互，
// 若把 hover 状态放在父组件，一次 mousemove 会让整个页面重渲染；
// 状态内聚到子组件后，重渲染范围收敛到"当前这一张图"。
//
// 顺滑相关的三件事都在这里处理：
//  1. 数据更新走 rAF 补间（点数不变时逐点插值 320ms cubicOut），而不是直接换路径；
//  2. hover 用 rAF 节流 + transform 定位，避免高频重排；
//  3. y 轴刻度用 nice-number + 迟滞，数据小幅波动时刻度不跳。
// ---------------------------------------------------------------------------

const props = defineProps({
  // 兼容旧写法：单序列 points
  points: { type: Array, default: () => [] },
  // 单序列时的指标名称与单位（ Tooltip 中显示为 "名称：值 单位"）
  name: { type: String, default: '' },
  unit: { type: String, default: '' },
  // 多序列扩展：每个序列独立名称/颜色/数据点；传入后将忽略 points/name/unit
  // 格式：[{ name, color, points: [{ ts, value }] }]
  series: { type: Array, default: () => [] },
  // viewBox 宽度：由调用方按实测容器宽度传入，保证与像素 1:1（否则文字会被拉伸）
  width: { type: Number, default: 300 },
  height: { type: Number, default: 86 },
  pad: { type: Object, default: () => ({ left: 42, right: 18, top: 6, bottom: 18 }) },
  // 是否撑满父容器高度（卡片图撑满，放大弹窗固定高）
  fill: { type: Boolean, default: true },
  tone: { type: String, default: 'default' }, // default | alert | error | muted
  // x 轴刻度格式：time = HH:MM，date = MM-DD HH:MM；Tooltip 时间始终完整
  timeMode: { type: String, default: 'time' },
  // 是否启用时间区间拖拽选取（仅放大弹窗大图开启；卡片小图关闭，避免误触）
  selectable: { type: Boolean, default: false },
})

const emit = defineEmits(['select-range'])

const TWEEN_MS = 320

// 刻度标签精度跟随步长：step=20 就 0 位小数，step=0.25 就 2 位小数
// 避免出现"0.13"这种被四舍五入掉的刻度，也不会出现"20.00000"这种冗余
// （刻度格式化统一走 chartUnits.fmtAxisTick，此处仅保留步长精度推断的兜底）

function fmtTick(ts, mode) {
  const text = String(ts || '')
  if (!text) return ''
  return mode === 'date' ? text.slice(5, 16) : text.slice(11, 16)
}

// Tooltip 时间始终完整：yyyy-mm-dd hh24:mi:ss
function fmtFullTime(ts) {
  const text = String(ts || '')
  if (!text) return ''
  // 后端返回格式已经是 "2026-09-17 15:52:00"，直接取前 19 位
  if (/^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}/.test(text)) return text.slice(0, 19).replace('T', ' ')
  const d = new Date(text)
  if (Number.isNaN(d.getTime())) return text
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

// 序列颜色的兜底取色：只在调用方没传 color 时生效（正常路径由明细页逐条下发）。
// 与明细页共用同一份 token 引用 —— 同一色相的不同明度档位，避免两边调色板各写一套。

// 把 props.points / props.series 统一成内部多序列结构（unit 随序列下发，
// 双轴分配与 Tooltip/图例的单位格式化都依赖它）
const normalizedSeries = computed(() => {
  if (props.series && props.series.length) {
    return props.series.map((s, i) => ({
      name: s.name || `指标${i + 1}`,
      color: s.color || curveColorAt(i),
      points: s.points || [],
      unit: s.unit || '',
    }))
  }
  if (props.points && props.points.length) {
    return [{
      name: props.name || '当前指标',
      color: 'currentColor', // 使用 CSS 当前 tone 下的线条色
      points: props.points,
      unit: props.unit || '',
    }]
  }
  return []
})

// ---------------------------------------------------------------------------
// Y 轴刻度：1/2/5×10^n 取整 + 按指标类型分区间/粒度
//
// 旧实现的问题：ticks 被硬编码成 [max, (max+min)/2, min] 三条，与 step 完全脱钩。
// 数据波动小时三条网格线挤在一起，折线被压成一条直线，标签互相重叠。
//
// 新实现参考 Grafana / Datadog / Prometheus 的 nice-scale 做法：
//  1. 步长收敛到 1/2/5×10^n（floorNice / nextNice），刻度值天然是"整数感"的；
//  2. 上下界对齐到步长的整数倍，不强制从 0 起；
//  3. 百分比固定 0~100 全量程，按 20%（小图 25%）一档均分，5~6 条刻度，
//     标签统一带 %，不随数据动态缩放，避免小幅百分比波动被视觉放大；
//  4. 刻度数量按绘图区高度取 4~6 个，保证标签不重叠；
//  5. 计数类步长强制为整数，避免"2.5 个连接"这种无意义的刻度；
//  6. 突刺用 P95/P05 裁剪，避免单点极值把主体压成一条直线；
//  7. 恒定值 / 全零单独兜底，保证折线居中而不是贴在画布边缘；
//  8. 非百分比指标增加"最小相对步长"兜底：当数据跨度相对于当前量级过小时，
//     强制刻度步长 ≥ 量级的 0.05%，避免 55.02 GB 附近 0.01 GB 的抖动被放大成剧烈 V 型。
// ---------------------------------------------------------------------------

// 指标轴类型：由 unit 推断，统一走 chartUnits.unitDim
//   bytes → 字节（自动 KB/MB/GB）  percent → 百分比  duration → 时长
//   rate → 速率  count → 计数  gauge → 其余
// 双轴分配规则：只看「有数据的序列」的量纲，量纲不同才启用双轴。

const NICE_CANDS = [1, 2, 5, 10]

// 下一个 1/2/5×10^n 步长（用于刻度过多时放大间隔）
function nextNice(step) {
  const value = Math.abs(step)
  if (!Number.isFinite(value) || value === 0) return 1
  const exp = Math.floor(Math.log10(value))
  const base = Math.pow(10, exp)
  const frac = value / base
  for (let i = 0; i < NICE_CANDS.length; i += 1) {
    if (NICE_CANDS[i] > frac + 1e-9) return NICE_CANDS[i] * base
  }
  return 10 * base
}

// 不超过 v 的最大 1/2/5×10^n 步长。
// 用"向下取"而不是"就近取"：就近取会让步长偏大、刻度偏少（这正是旧实现刻度被压成 3 条的原因）。
function floorNice(v) {
  const value = Math.abs(v)
  if (!Number.isFinite(value) || value === 0) return 1e-6
  const exp = Math.floor(Math.log10(value))
  const base = Math.pow(10, exp)
  const frac = value / base
  let nice = 1
  for (let i = 0; i < NICE_CANDS.length; i += 1) {
    if (NICE_CANDS[i] <= frac + 1e-9) nice = NICE_CANDS[i]
  }
  return nice * base
}

// 目标刻度数：按绘图区实际高度决定，避免小图塞 6 条标签导致重叠
function targetTickCount(plotHeight) {
  if (!Number.isFinite(plotHeight)) return 5
  if (plotHeight < 70) return 4
  if (plotHeight < 150) return 5
  return 6
}

// 数据区间：如实取真实最小 / 最大值，不做任何裁剪或平滑。
//
// 这里曾经做过「突刺裁剪」：当最大值超过 P95 的 3 倍时把上界压到 P95×1.5，
// 本意是避免单个毛刺把主体压成一条直线。但它不区分指标形态 —— 网络流量、
// 磁盘 IO 这类突发型指标的短时尖峰是业务常态（备份、批量同步、日志上传），
// 会被当成毛刺砍掉，导致 Y 轴上界低于真实峰值、曲线被 clampY 截断在顶部，
// 用户只看到"顶到天花板"却读不出峰值是多少。
//
// 原则：图表上的每个点都如实反映采集到的值，坐标轴去适配数据，而不是反过来。
// 主体被压扁的问题交给「时间范围缩放」去解决，而不是偷偷改数据区间。
// 用循环而不是 Math.min(...values)：点数多时展开成参数列表有栈溢出风险。
function dataRange(values) {
  let min = Infinity
  let max = -Infinity
  for (let i = 0; i < values.length; i += 1) {
    const v = values[i]
    if (!Number.isFinite(v)) continue
    if (v < min) min = v
    if (v > max) max = v
  }
  return { min, max }
}

// 刻度迟滞：上一档刻度还能容纳新数据时就沿用，避免呼吸式抖动。
// 用普通对象而不是 ref —— 它只在 computed 内部读写，做成响应式会引发循环依赖。
// 双轴后左右两侧各自独立记忆，互不干扰。
const lastScales = { left: null, right: null }

// 计算 Y 轴刻度：返回 { min, max, step, ticks }；side 用于隔离双轴迟滞
function resolveScale(rawMin, rawMax, plotHeight, axisType, side = 'left') {
  if (!Number.isFinite(rawMin) || !Number.isFinite(rawMax)) {
    return { min: 0, max: 1, step: 1, ticks: [0, 0.5, 1] }
  }

  // ---- 百分比：固定 0–100，按规整间距均分，不随数据动态缩放 ----
  // 百分比型指标必须给人"占满度"的绝对感知。动态缩放会让 1% 的波动
  // 在图上被放大成剧烈抖动；固定 0–100 + 规整大间隔，小幅波动自然呈现为
  // 接近平线的小幅起伏，符合运维直觉。
  if (axisType === 'percent') {
    // 百分比固定 0–100%，刻度间隔统一 10%（共 11 条刻度线）。
    // 为避免标签拥挤，只在 0/20/40/60/80/100 六个主刻度上显示标签，
    // 其余 10/30/50/70/90 只保留网格线，不标文字。
    const step = 10
    const ticks = []
    const labelTicks = []
    for (let v = 0; v <= 100 + 1e-9; v += step) {
      const n = Number(v.toFixed(6))
      ticks.push(n)
      if (n % 20 === 0) labelTicks.push(n)
    }
    return { min: 0, max: 100, step, ticks, labelTicks }
  }

  // ---- 字节量纲：把计算空间先换算到 KB / MB / GB 档位（1024 进制）----
  // 十进制 nice（1/2/5×10^n）直接套在字节上会算出 0.95 MB / 1.91 MB / 2.86 MB
  // 这种刻度，读的时候还得心算。改成在 KB/MB/GB 档位里做 nice 再乘回字节，
  // 刻度就落在整数档上（0 / 1 MB / 2 MB / 3 MB）。
  let div = 1
  if (axisType === 'bytes') {
    const mag = Math.max(Math.abs(rawMin), Math.abs(rawMax), 1)
    const level = Math.max(1, Math.floor(Math.log(mag) / Math.log(1024)))
    div = Math.pow(1024, level)
  }
  const loD = rawMin / div
  const hiD = rawMax / div

  // ---- 退化场景：恒定值 / 全零 ----
  let min = loD
  let max = hiD
  if (max - min < 1e-9) {
    if (Math.abs(max) < 1e-9) {
      // 全零：给一个能容纳 4 条刻度的区间，折线居中而不是贴边
      min = 0
      max = axisType === 'count' ? 3 : 1
    } else {
      // 恒定值：以该值为中心上下扩展，折线居中而不是贴边。
      // 计数类至少扩 2（保证 4 个整数刻度），其他类型扩 10%（至少 0.5）
      const pad = axisType === 'count' ? 2 : Math.max(Math.abs(max) * 0.1, 0.5)
      min = max - pad
      max = max + pad
    }
  }

  // 只扩不缩的最后一道闸：无论上面走了哪条分支，区间都必须完整容纳真实极值。
  // 后续 nice 取整只会把上界再往上抬，所以在这里兜一次底就够，ticks 与量程始终一致。
  min = Math.min(min, loD)
  max = Math.max(max, hiD)

  const tickCount = targetTickCount(plotHeight)
  const MAX_TICKS = 6
  // 步长向下取：保证刻度数不会少于目标（旧实现用就近取，导致刻度被压到 2~3 条）
  let step = floorNice((max - min) / (tickCount - 1))
  // 计数类：步长必须是整数（不允许"2.5 个连接"）
  if (axisType === 'count') step = Math.max(1, Math.round(step))
  step = Number(step.toPrecision(12))

  // ---- 极小波动防抖（非百分比） ----
  // 当数据跨度相对于当前量级过小时，nice-scale 会把 0.0x GB 的抖动细分成多个
  // 小刻度，而 fmtByteAuto 只保留两位小数，导致所有 Y 轴标签都变成"55.02 GB"，
  // 折线却占满整张图，形成"剧烈 V 型"的误判。
  // 强制步长至少为当前量级的 0.05%，并以数据中心扩展出 5 条可读刻度，
  // 这样小幅波动只会占用少量纵向空间，视觉上呈现为合理的小幅起伏。
  const magnitude = Math.max(Math.abs(min), Math.abs(max), 1e-9)
  const minStep = magnitude * 0.0005
  if (step < minStep) {
    step = Number(nextNice(minStep).toPrecision(12))
    const center = (min + max) / 2
    // 只扩不缩：防抖是为了让刻度标签不重复，绝不能反过来把真实极值挤出去
    min = Math.min(min, center - step * 2)
    max = Math.max(max, center + step * 2)
  }

  // 上下界对齐到步长整数倍后会向外扩张，刻度数可能超出上限；
  // 这时逐级放大步长（1→2→5→10）直到落回 4~6 条，保证标签不重叠。
  let niceMin = 0
  let niceMax = 0
  let ticks = []
  for (let guard = 0; guard < 6; guard += 1) {
    niceMin = Number((Math.floor(min / step) * step).toPrecision(12))
    niceMax = Number((Math.ceil(max / step) * step).toPrecision(12))
    if (niceMax - niceMin < step) niceMax = Number((niceMin + step).toPrecision(12))
    ticks = []
    for (let i = 0; i * step <= niceMax - niceMin + step * 1e-6; i += 1) {
      ticks.push(Number((niceMin + i * step).toPrecision(12)))
    }
    if (ticks.length <= MAX_TICKS) break
    step = Number(nextNice(step).toPrecision(12))
  }

  // 迟滞：旧刻度完全容纳新数据、且步长与刻度数都一致时沿用，
  // 避免数据在刻度边界附近抖动时标签来回跳（1/2/5 离散化已很稳，这里只兜边界情况）
  // 比较统一在字节空间做：prev 已是最终量程，step 需先乘回 div 才可比
  const stepOut = Number((step * div).toPrecision(12))
  const prev = lastScales[side]
  if (
    prev &&
    prev.step === stepOut &&
    prev.ticks.length === ticks.length &&
    rawMin >= prev.min &&
    rawMax <= prev.max
  ) {
    return prev
  }

  const scale = {
    min: Number((niceMin * div).toPrecision(12)),
    max: Number((niceMax * div).toPrecision(12)),
    step: stepOut,
    ticks: ticks.map((t) => Number((t * div).toPrecision(12))),
  }
  lastScales[side] = scale
  return scale
}

const targetCoords = computed(() => {
  const series = normalizedSeries.value
  if (!series.length) return null
  // 以「点数最多的序列」的时间轴为基准：多序列卡片里第一条未必有数据
  // （例如监控项首行是尚无采集源的指标），用 series[0] 会让整张图被判成空。
  const basePoints = series.reduce(
    (best, s) => ((s.points || []).length > best.length ? s.points || [] : best),
    [],
  )
  if (basePoints.length < 2) return null

  // ---- 量纲与轴分配 ----
  // 每条序列先按单位推断量纲；字节量纲统一折算成原始字节参与缩放，
  // 刻度与取值文本再自动换算 KB/MB/GB（见 chartUnits）。
  const infos = series.map((s) => {
    const dim = unitDim(s.unit)
    const factor = dim === 'bytes' ? unitToBytes(s.unit) || 1 : 1
    return { dim, factor }
  })
  const axisValue = (i, p) => {
    if (p.value == null) return null
    const v = Number(p.value)
    if (!Number.isFinite(v)) return null
    return v * infos[i].factor
  }

  // 坐标轴只由「有数据的序列」决定：无采集源 / 本窗口无数据的行不参与，
  // 否则一个空维度会凭空撑出一条没有任何曲线的轴。
  const drawableIdx = []
  series.forEach((s, i) => {
    const finite = (s.points || []).filter((p) => p.value != null && Number.isFinite(Number(p.value)))
    if (finite.length > 1) drawableIdx.push(i)
  })
  if (!drawableIdx.length) return null

  // 双轴规则：按序列出现顺序收集量纲，出现第二种量纲才启用双轴；
  // 第一种量纲 → 左轴，其余量纲全部归右轴（当前目录下最多两种量纲）。
  const dimOrder = []
  drawableIdx.forEach((i) => {
    if (!dimOrder.includes(infos[i].dim)) dimOrder.push(infos[i].dim)
  })
  const dual = dimOrder.length > 1
  const sideOf = new Map()
  drawableIdx.forEach((i) => {
    sideOf.set(i, dual && infos[i].dim !== dimOrder[0] ? 'right' : 'left')
  })

  // ---- 画布与边距 ----
  const height = props.height
  const pad = { ...props.pad }
  if (dual) pad.right = Math.max(pad.right, 46) // 右轴刻度文本需要竖排空间
  // 字节轴刻度形如 "390.63 KB"，比纯数字宽，左边距要加宽防裁剪
  if (infos[drawableIdx[0]].dim === 'bytes') pad.left = Math.max(pad.left, 58)
  const w = Math.max(10, props.width - pad.left - pad.right)
  const h = Math.max(10, height - pad.top - pad.bottom)

  // ---- 每轴独立求刻度 ----
  const valuesBySide = { left: [], right: [] }
  drawableIdx.forEach((i) => {
    const side = sideOf.get(i)
    ;(series[i].points || []).forEach((p) => {
      const v = axisValue(i, p)
      if (v != null) valuesBySide[side].push(v)
    })
  })
  const resolveFor = (values, dim, side) => {
    if (!values.length) return null
    const { min, max } = dataRange(values)
    return resolveScale(min, max, h, dim, side)
  }
  const leftType = dimOrder[0]
  const rightType = dual ? dimOrder[dimOrder.length - 1] : null
  const scaleL = resolveFor(valuesBySide.left, leftType, 'left')
  const scaleR = dual ? resolveFor(valuesBySide.right, rightType, 'right') : null
  if (!scaleL || (dual && !scaleR)) return null

  const clampY = (raw) => Math.min(pad.top + h, Math.max(pad.top, raw))
  const yOfL = (v) => clampY(pad.top + h - ((v - scaleL.min) / (scaleL.max - scaleL.min || 1)) * h)
  const yOfR = dual ? (v) => clampY(pad.top + h - ((v - scaleR.min) / (scaleR.max - scaleR.min || 1)) * h) : null

  const step = w / Math.max(1, basePoints.length - 1)
  const xAt = (i) => pad.left + i * step

  // 基准序列的坐标（点数最多的那条）：悬停锚点用它，
  // 多序列卡片里 series[0] 可能是一条没有数据的线。
  const baseSeriesIdx = series.reduce(
    (best, s, i) => ((s.points || []).length > (series[best]?.points || []).length ? i : best),
    drawableIdx[0],
  )
  const baseCoords = (series[baseSeriesIdx].points || []).map((p, i) => {
    const side = sideOf.get(baseSeriesIdx) === 'right'
    const yv = axisValue(baseSeriesIdx, p)
    const y = side ? yOfR(yv ?? scaleR.min) : yOfL(yv ?? scaleL.min)
    return [xAt(i), y]
  })

  return {
    series: series.map((s, i) => {
      const side = sideOf.get(i) === 'right' ? 'right' : 'left'
      const yOf = side === 'right' ? yOfR : yOfL
      const fallback = side === 'right' ? scaleR.min : scaleL.min
      return {
        ...s,
        dim: infos[i].dim,
        coords: drawableIdx.includes(i)
          ? (s.points || []).map((p, j) => {
              const yv = axisValue(i, p)
              return [xAt(j), yOf(yv ?? fallback)]
            })
          : [],
      }
    }),
    dual,
    leftType,
    rightType,
    scaleL,
    scaleR,
    baseCoords,
    basePoints,
    w,
    h,
    padL: pad.left,
    padR: pad.right,
    padT: pad.top,
    step,
    height,
    width: props.width,
  }
})

// ---------------------------------------------------------------------------
// 数据更新补间：点数不变时逐点插值，点数变化（切时间范围）则直接切换。
// 双轴后每条序列都有自己的坐标数组，补间按序列逐条插值（数组与 geo.series
// 对齐，无数据的序列为 null，不参与插值）。
// ---------------------------------------------------------------------------
const tweenCoords = ref(null)
let rafTween = 0

function runTween(from, to) {
  cancelAnimationFrame(rafTween)
  const start = performance.now()
  const tick = (now) => {
    const t = Math.min(1, (now - start) / TWEEN_MS)
    const eased = 1 - Math.pow(1 - t, 3) // cubicOut
    tweenCoords.value = to.map((tc, i) => {
      const fc = from[i]
      if (!tc || !fc || fc.length !== tc.length) return null
      return tc.map((p, j) => [
        fc[j][0] + (p[0] - fc[j][0]) * eased,
        fc[j][1] + (p[1] - fc[j][1]) * eased,
      ])
    })
    if (t < 1) rafTween = requestAnimationFrame(tick)
    else tweenCoords.value = null
  }
  rafTween = requestAnimationFrame(tick)
}

onBeforeUnmount(() => cancelAnimationFrame(rafTween))

const chart = computed(() => {
  const geo = targetCoords.value
  if (!geo) return null
  const basePoints = geo.basePoints

  // 点数不足 2 的序列（无采集源 / 本窗口没采到）不画路径，
  // 必须在入口就剔除，否则单点的线会被画成一个孤立的圆点、语义不清。
  const drawable = []
  geo.series.forEach((s, idx) => {
    if (s.coords && s.coords.length > 1) drawable.push({ s, idx })
  })
  if (!drawable.length) return null

  // 补间按序列逐条套用（长度不一致时退回目标坐标，直接切换）
  const tweenArr = tweenCoords.value
  const seriesWithPaths = drawable.map(({ s, idx }) => {
    const tw = tweenArr?.[idx]
    const coords = tw && tw.length === s.coords.length ? tw : s.coords
    const linePath = coords.map((p, i) => `${i ? 'L' : 'M'}${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(' ')
    return { ...s, coords, linePath }
  })

  const yOfL = (v) =>
    geo.padT + geo.h - ((v - geo.scaleL.min) / (geo.scaleL.max - geo.scaleL.min || 1)) * geo.h
  const yOfR = (v) =>
    geo.padT + geo.h - ((v - geo.scaleR.min) / (geo.scaleR.max - geo.scaleR.min || 1)) * geo.h

  return {
    width: geo.width,
    height: geo.height,
    padL: geo.padL,
    padR: geo.padR,
    padT: geo.padT,
    w: geo.w,
    h: geo.h,
    step: geo.step,
    series: seriesWithPaths,
    dual: geo.dual,
    basePoints: geo.basePoints,
    // 补间基准帧：与 geo.series 对齐的坐标数组（作为下一次数据更新的插值起点）
    tweenFrame: geo.series.map((s) => s.coords),
    // 网格线跟随左轴（左右轴刻度对齐时共用，不对齐时以左轴为准避免杂线）
    hGrid: geo.scaleL.ticks.map((v) => ({ y: yOfL(v), x1: geo.padL, x2: geo.width - geo.padR })),
    // 单位量级以「本轴最大刻度」为基准整轴统一，避免 KB / MB 混排。
    // 百分比轴用 labelTicks：每 10% 一条刻度线，但只在 20% 整数倍处显示标签。
    yLabels: (geo.scaleL.labelTicks || geo.scaleL.ticks).map((v) => ({
      y: yOfL(v) + 3,
      text: fmtAxisTick(v, geo.leftType, geo.scaleL.step, geo.scaleL.max),
    })),
    yLabelsRight: geo.dual
      ? (geo.scaleR.labelTicks || geo.scaleR.ticks).map((v) => ({
          y: yOfR(v) + 3,
          text: fmtAxisTick(v, geo.rightType, geo.scaleR.step, geo.scaleR.max),
        }))
      : [],
    xLabels: [0, Math.floor((basePoints.length - 1) / 2), basePoints.length - 1].map((i) => ({
      x: seriesWithPaths[0]?.coords[i]?.[0] ?? geo.padL + i * geo.step,
      text: fmtTick(basePoints[i] && basePoints[i].ts, props.timeMode),
    })),
  }
})

// ---------------------------------------------------------------------------
// 数据更新补间：记录"当前画在屏上的那一帧"作为起点，
// flush: 'post' 保证拿到已完成渲染的那一帧（含补间中间帧），衔接不断层。
// 只有"同一张图、每条序列点数都没变"才补间：切时间范围/首次加载直接切换。
// ---------------------------------------------------------------------------
let lastRendered = null
watch(
  chart,
  (current) => {
    lastRendered = current ? current.tweenFrame : null
  },
  { flush: 'post', immediate: true },
)

watch(
  () => [props.points, props.series],
  () => {
    const target = targetCoords.value
    const from = lastRendered
    if (!target || !from || from.length !== target.series.length) {
      tweenCoords.value = null
      return
    }
    for (let i = 0; i < from.length; i += 1) {
      const tc = target.series[i]?.coords || []
      if ((from[i] || []).length !== tc.length) {
        tweenCoords.value = null
        return
      }
    }
    runTween(from, target.series.map((s) => s.coords))
  },
)

// ---------------------------------------------------------------------------
// 悬停取值：rAF 节流 + transform 定位
// ---------------------------------------------------------------------------
const hover = ref(null)
let rafHover = 0
let pendingEvent = null

// applyHover 收到的是"已经抽干"的普通对象：DOM 事件的 currentTarget 只在派发期间有效，
// 一旦把 event 存起来留到 rAF 里再读，currentTarget 已经是 null（这也是 hover 曾经完全失效的原因）。
function applyHover(sample) {
  const geo = chart.value
  if (!geo) return
  const rect = sample.rect
  if (!rect || !rect.width || !rect.height) return
  // viewBox 与像素不是 1:1 时（卡片被 flex 拉高）必须分别换算横纵比例
  const scaleX = rect.width / geo.width
  const scaleY = rect.height / geo.height
  const svgX = (sample.clientX - rect.left) / scaleX
  // 悬停锚点用基准时间轴（点数最多的序列），多序列卡片里首行可能没有数据
  const maxIndex = Math.max(0, geo.basePoints.length - 1)
  const index = Math.min(maxIndex, Math.max(0, Math.round((svgX - geo.padL) / geo.step)))
  // 同一个数据点内移动不重复触发渲染
  if (hover.value && hover.value.index === index) return

  // 收集所有序列在当前时间点的值，保持传入顺序；单位随序列下发
  const rows = geo.series
    .map((s) => {
      const pt = s.points?.[index]
      const coord = s.coords?.[index]
      return {
        name: s.name,
        color: s.color,
        unit: s.unit,
        value: pt?.value,
        coord,
      }
    })
    .filter(Boolean)
  if (!rows.length) return

  const cx = geo.padL + index * geo.step
  const anchor = rows.find((row) => row.coord) || {}
  const cy = anchor.coord?.[1] ?? geo.padT + geo.h / 2
  hover.value = {
    index,
    cx,
    cy,
    px: cx * scaleX,
    py: cy * scaleY,
    xPct: (cx / geo.width) * 100,
    yPct: (cy / geo.height) * 100,
    ts: geo.basePoints?.[index]?.ts,
    rows,
  }
}

function onMove(event) {
  const el = event.currentTarget
  if (!el) return
  // 拖拽选区进行中：悬停取值让位，避免十字线与选区提示互相干扰
  if (dragCtx) return
  // 同步阶段就把几何信息取出来，只把普通数据留给下一帧
  pendingEvent = { rect: el.getBoundingClientRect(), clientX: event.clientX }
  if (rafHover) return
  rafHover = requestAnimationFrame(() => {
    rafHover = 0
    const sample = pendingEvent
    pendingEvent = null
    if (sample) applyHover(sample)
  })
}

function onLeave() {
  hover.value = null
  pendingEvent = null
  if (rafHover) {
    cancelAnimationFrame(rafHover)
    rafHover = 0
  }
}

onBeforeUnmount(() => {
  if (rafHover) cancelAnimationFrame(rafHover)
})

const tipStyle = computed(() => {
  const item = hover.value
  if (!item) return {}
  // 用 translate3d 定位而非 left/top：避免每次移动触发重排
  // x 方向：靠右时 tooltip 向左展开，避免超出画布右边界
  const x = item.xPct > 72 ? `calc(${item.px}px - 100% - 8px)` : `calc(${item.px}px + 8px)`
  // y 方向：靠上时 tooltip 向下展开，否则向上展开，避免遮挡数据点
  const y = item.yPct < 35 ? `calc(${item.py}px + 10px)` : `calc(${item.py}px - 100% - 10px)`
  return { transform: `translate3d(${x}, ${y}, 0)` }
})

const timeText = computed(() => (hover.value ? fmtFullTime(hover.value.ts) : ''))
// Tooltip 每行带各自序列的单位：字节量纲自动换算 KB/MB/GB，其他量纲原样追加
const tipRows = computed(() => {
  if (!hover.value) return []
  return hover.value.rows.map((row) => ({
    ...row,
    text: `${row.name}：${fmtDimValue(row.value, row.unit)}`,
  }))
})

// ---------------------------------------------------------------------------
// 时间区间拖拽选取（仅 props.selectable 启用，放大弹窗大图用）
//
// 交互约定：
//   1. 按下鼠标开始选区，物理拖拽距离经 viewBox 比例换算成绘图区坐标；
//   2. 拖拽方向任意（左右均可），内部按 min/max 归一化；
//   3. 拖出图表边界由 window 级监听接管，坐标钳制在绘图区内（钳制=贴边延展，
//      不会截断到图表外的时间）；
//   4. 松手时两端吸附到最近数据点的时间戳 —— 时间区间来自真实采样点而不是
//      像素线性外推，任何时间粒度（15s 桶 / 1h 桶 / 7d）下映射都准确；
//   5. 位移过小（<4px 或两端吸附到同一数据点）视为普通点击，不产生选区，
//      悬停交互不受影响；
//   6. 拖拽期间悬停十字线与 tooltip 抑制；Esc 取消；双击清除已确定的选区。
// ---------------------------------------------------------------------------
const svgEl = ref(null)
const selDrag = ref(null) // 拖拽中的选区
const selDone = ref(null) // 松手后保留的高亮（数据刷新即清除）
let dragCtx = null // 非响应式拖拽上下文（锚点坐标等，不需要触发渲染）

const selRect = computed(() => selDrag.value || selDone.value)

const selTipStyle = computed(() => {
  const sel = selRect.value
  if (!sel) return {}
  // 与 hover tooltip 同款定位思路：x 靠右时提示框向左展开
  const flip = sel.xPct > 72
  const x = flip ? `calc(${sel.pxMid}px - 100% - 8px)` : `calc(${sel.pxMid}px + 8px)`
  return { transform: `translate3d(${x}, ${sel.pyTop + 8}px, 0)` }
})

function toSvgX(clientX) {
  const geo = chart.value
  const el = svgEl.value
  if (!geo || !el) return null
  const rect = el.getBoundingClientRect()
  if (!rect.width) return null
  return (clientX - rect.left) * (geo.width / rect.width)
}

function clampPlotX(x) {
  const geo = chart.value
  return Math.min(geo.padL + geo.w, Math.max(geo.padL, x))
}

// 像素 → 数据点索引：与悬停取值同一条换算路径，保证选区与十字线指向一致
function indexAtX(x) {
  const geo = chart.value
  return Math.min(geo.basePoints.length - 1, Math.max(0, Math.round((x - geo.padL) / geo.step)))
}

// 时间跨度的人话格式：2分10秒 / 1小时5分 / 3天2小时
function spanTextOf(startTs, endTs) {
  const da = new Date(String(startTs).replace(' ', 'T'))
  const db = new Date(String(endTs).replace(' ', 'T'))
  const sec = Math.round((db.getTime() - da.getTime()) / 1000)
  if (!Number.isFinite(sec) || sec < 0) return ''
  const days = Math.floor(sec / 86400)
  const hours = Math.floor((sec % 86400) / 3600)
  const minutes = Math.floor((sec % 3600) / 60)
  const seconds = sec % 60
  const parts = []
  if (days) parts.push(`${days}天`)
  if (hours) parts.push(`${hours}小时`)
  if (minutes) parts.push(`${minutes}分`)
  if (seconds || !parts.length) parts.push(`${seconds}秒`)
  return parts.join('')
}

// 由两端 x 坐标构造选区对象（svg 坐标 + 吸附后的时间戳 + 提示框定位）
function makeSel(x0, x1, rect) {
  const geo = chart.value
  const left = Math.min(x0, x1)
  const right = Math.max(x0, x1)
  const i0 = indexAtX(left)
  const i1 = indexAtX(right)
  const scaleX = rect && rect.width ? rect.width / geo.width : 1
  const scaleY = rect && rect.height ? rect.height / geo.height : 1
  return {
    x0: left,
    x1: right,
    i0,
    i1,
    startTs: geo.basePoints[i0]?.ts || '',
    endTs: geo.basePoints[i1]?.ts || '',
    pxMid: ((left + right) / 2) * scaleX,
    xPct: ((left + right) / 2 / geo.width) * 100,
    pyTop: geo.padT * scaleY,
    spanText: '',
  }
}

// 时间戳要在 makeSel 返回后补 spanText（依赖 startTs/endTs，放外面单独算一次）
function withSpan(sel) {
  sel.spanText = spanTextOf(sel.startTs, sel.endTs)
  return sel
}

function onDown(event) {
  if (!props.selectable || !chart.value || event.button !== 0) return
  const el = svgEl.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const x = clampPlotX(toSvgX(event.clientX))
  dragCtx = { anchorX: x, rect }
  hover.value = null // 拖拽期间抑制悬停十字线
  selDrag.value = withSpan(makeSel(x, x, rect))
  window.addEventListener('mousemove', onDragMove)
  window.addEventListener('mouseup', onDragUp)
  window.addEventListener('keydown', onDragKey)
  // 阻止默认行为：避免拖拽时选中页面文本
  event.preventDefault()
}

function onDragMove(event) {
  if (!dragCtx || !chart.value) return
  const el = svgEl.value
  const rect = el ? el.getBoundingClientRect() : dragCtx.rect
  // 拖出图表边界：toSvgX 会给出超出值，clampPlotX 钳回绘图区
  const x = clampPlotX(toSvgX(event.clientX) ?? dragCtx.anchorX)
  selDrag.value = withSpan(makeSel(dragCtx.anchorX, x, rect))
}

function onDragUp(event) {
  if (!dragCtx) return
  const ctx = dragCtx
  const geo = chart.value
  const el = svgEl.value
  const rect = el ? el.getBoundingClientRect() : ctx.rect
  const x = clampPlotX(toSvgX(event.clientX) ?? ctx.anchorX)
  detachDrag()
  selDrag.value = null
  const sel = withSpan(makeSel(ctx.anchorX, x, rect))
  // 位移过小或吸附后两端是同一个数据点：视为点击，不产生选区
  if (!geo || sel.i0 === sel.i1 || (sel.x1 - sel.x0) * (rect.width / geo.width) < 4) {
    return
  }
  selDone.value = sel
  emit('select-range', { startTs: sel.startTs, endTs: sel.endTs, spanText: sel.spanText })
}

function onDragKey(event) {
  if (event.key !== 'Escape') return
  detachDrag()
  selDrag.value = null
}

function detachDrag() {
  window.removeEventListener('mousemove', onDragMove)
  window.removeEventListener('mouseup', onDragUp)
  window.removeEventListener('keydown', onDragKey)
  dragCtx = null
}

function onDblClick() {
  selDone.value = null
}

onBeforeUnmount(detachDrag)

// 数据刷新（父组件按新时间范围重查）后，旧选区高亮失去意义，自动清除
watch(
  () => [props.points, props.series],
  () => {
    selDone.value = null
  },
)
</script>

<style scoped>
.mtc {
  position: relative;
  width: 100%;
}

.mtc-svg {
  display: block;
  width: 100%;
  height: 100%;
}

/* 启用拖拽选区的大图：十字光标提示可框选 */
.mtc-svg-selectable {
  cursor: crosshair;
}

/* 选区：主色半透明填充 + 两侧实线边界，拖拽中与松手后共用 */
.mtc-sel {
  fill: var(--gi-primary);
  fill-opacity: 0.14;
  pointer-events: none;
}

.mtc-sel-edge {
  stroke: var(--gi-primary);
  stroke-width: 1.5;
  pointer-events: none;
}

/* 选区时间提示：与悬停 tooltip 同观感，跟随选区中点 */
.mtc-sel-tip {
  position: absolute;
  top: 0;
  left: 0;
  padding: 6px 10px;
  border-radius: 8px;
  background: var(--gi-color-container-bg);
  color: var(--gi-text-1);
  font-size: 12px;
  line-height: 1.4;
  pointer-events: none;
  box-shadow: var(--gi-shadow-md);
  border: 1px solid var(--gi-color-border);
  z-index: 6;
  white-space: nowrap;
  will-change: transform;
}

/* 坐标轴文字：卡片图与放大弹窗共用同一套字体、字号、颜色与字距 */
.mtc text {
  font-size: 10px;
  font-weight: 400;
  line-height: 1;
  fill: var(--gi-text-3);
  /* 字间距回归 0：10px 字号下额外正间距会让标签显得松散、高宽比失衡 */
  letter-spacing: 0;
  /* 与页面右上角时间框等控件保持一致：使用系统默认无衬线字体 */
  font-family: 'PingFang SC', 'Noto Sans SC', 'Segoe UI', 'Microsoft YaHei UI', sans-serif;
  /* 保留等宽数字特性，避免 Y 轴数值因字宽不同而错位 */
  font-variant-numeric: tabular-nums;
}

.mtc-axis {
  fill: none;
  stroke: var(--gi-color-border-soft);
  stroke-width: 1;
  shape-rendering: crispEdges;
}

.mtc-grid {
  fill: none;
  stroke: var(--gi-color-border-soft);
  stroke-width: 1;
  stroke-dasharray: 2 2;
  stroke-opacity: 0.5;
}

/* 曲线颜色来自明细页主题 token（单序列 = 系统主色；多序列 = 淡色系多色相），
   这里只写变量名，色值在 CSS 层现算，换主题时曲线 / 图例自动跟随。
   纯线条呈现，不使用面积填充。 */
.mtc-line {
  fill: none;
  stroke: var(--gi-primary);
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}

/* 档位色：告警 / 采集失败用主色调的更深明度档，而不是跳到第二个色相 */
.mtc.tone-alert .mtc-line {
  stroke: var(--gi-primary-tone-alert);
}
.mtc.tone-error .mtc-line {
  stroke: var(--gi-primary-tone-error);
}
.mtc.tone-muted .mtc-line {
  stroke: var(--gi-muted);
}

.mtc-cross {
  stroke: var(--gi-text-3);
  stroke-width: 1;
  stroke-dasharray: 3 3;
  pointer-events: none;
}

.mtc-hover-dot {
  fill: var(--gi-color-container-bg);
  stroke: var(--gi-primary);
  stroke-width: 2;
  pointer-events: none;
}
/* 档位色的悬停圆点与曲线同色，避免"线一个色、点另一个色" */
.mtc.tone-alert .mtc-hover-dot {
  stroke: var(--gi-primary-tone-alert);
}
.mtc.tone-error .mtc-hover-dot {
  stroke: var(--gi-primary-tone-error);
}
.mtc.tone-muted .mtc-hover-dot {
  stroke: var(--gi-muted);
}

.mtc-tip {
  position: absolute;
  top: 0;
  left: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px 12px;
  border-radius: 8px;
  background: var(--gi-color-container-bg);
  color: var(--gi-text-1);
  font-size: 12px;
  line-height: 1.4;
  pointer-events: none;
  box-shadow: var(--gi-shadow-md);
  border: 1px solid var(--gi-color-border);
  z-index: 5;
  will-change: transform;
  min-width: 120px;
}

.tip-time {
  font-size: 12px;
  color: var(--gi-text-2);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.tip-rows {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.tip-row {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}

.tip-dot {
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--gi-primary);
}
/* 与曲线 / 悬停圆点同档位取色 */
.mtc.tone-alert .tip-dot {
  background: var(--gi-primary-tone-alert);
}
.mtc.tone-error .tip-dot {
  background: var(--gi-primary-tone-error);
}
.mtc.tone-muted .tip-dot {
  background: var(--gi-muted);
}

.tip-text {
  font-variant-numeric: tabular-nums;
}

.mtc-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  font-size: 11px;
  color: var(--gi-muted);
}
</style>
