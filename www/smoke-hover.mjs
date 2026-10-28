// 客户端构建 + jsdom 挂载：真实跑一遍 MetricTrendChart 的悬停取值
const WWW = '/Users/hongjie/Documents/AiWork/goInsight-master/goInsight-master/www'
const NODE_MODULES = '/Users/hongjie/.workbuddy/binaries/node/workspace/node_modules'
const OUT = '/tmp/mtc-smoke'

const { JSDOM } = await import(`${NODE_MODULES}/jsdom/lib/api.js`)
const dom = new JSDOM('<!doctype html><html><body><div id="app"></div></body></html>', {
  pretendToBeVisual: true,
})
const { window } = dom
global.window = window
global.document = window.document
global.Element = window.Element
global.SVGElement = window.SVGElement
global.Node = window.Node
global.HTMLElement = window.HTMLElement
global.MouseEvent = window.MouseEvent
global.requestAnimationFrame = window.requestAnimationFrame.bind(window)
global.cancelAnimationFrame = window.cancelAnimationFrame.bind(window)
try {
  Object.defineProperty(global, 'navigator', { value: window.navigator, configurable: true })
} catch (e) {
  /* node 22 已内置 navigator */
}

// jsdom 不做布局：给 SVG 手工伪装 300x86 矩形（与卡片图一致）
const FAKE = { left: 20, top: 50, width: 300, height: 86, right: 320, bottom: 136, x: 20, y: 50 }
window.Element.prototype.getBoundingClientRect = function () {
  const isSvg = String(this.tagName || '').toLowerCase() === 'svg'
  const r = isSvg ? FAKE : { left: 0, top: 0, width: 0, height: 0, right: 0, bottom: 0, x: 0, y: 0 }
  return { ...r, toJSON: () => r }
}

// ---- 用 vite 以客户端模式构建 lib（不用 ssrLoadModule：它会走 SSR 编译分支）----
const { build } = await import(`${WWW}/node_modules/vite/dist/node/index.js`)
const vuePlugin = (await import(`${WWW}/node_modules/@vitejs/plugin-vue/dist/index.mjs`)).default
await build({
  root: WWW,
  configFile: false, // 不加载项目 vite.config：否则 vue 插件会被挂两次，二次 transform 会把已经编译好的 JS 当 SFC 再解析
  logLevel: 'error',
  plugins: [vuePlugin()],
  build: {
    outDir: OUT,
    emptyOutDir: true,
    minify: false,
    lib: { entry: `${WWW}/smoke-entry.js`, formats: ['es'], fileName: () => 'smoke.js' },
  },
})

const { mount } = await import(`${OUT}/smoke.js`)

// ---- 构造 60 个 15 秒采样点 ----
const points = []
const base = Date.now() - 15 * 60 * 1000
const pad = (n) => String(n).padStart(2, '0')
for (let i = 0; i < 60; i++) {
  const d = new Date(base + i * 15000)
  points.push({
    ts: `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`,
    value: Math.round((20 + Math.sin(i / 6) * 10) * 100) / 100,
  })
}

mount(document.getElementById('app'), { points, width: 300, height: 86, fill: false })
await new Promise((r) => requestAnimationFrame(r))

const svg = document.querySelector('svg')
console.log('[1] svg 渲染:', svg ? 'OK' : 'FAIL', '| circle 数（默认态应为 0）:', document.querySelectorAll('circle').length)
if (!svg) process.exit(1)

// 悬停到第 30 个点：padL=42, w=300-42-12=246, step=246/59
const clientX = 20 + 42 + 30 * (246 / 59)
svg.dispatchEvent(new window.MouseEvent('mousemove', { clientX, clientY: 90, bubbles: true }))
await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))

const tip = document.querySelector('.mtc-tip')
const cross = document.querySelector('.mtc-cross')
const expected = points[30].value.toLocaleString('zh-CN', { maximumFractionDigits: 2 })
console.log('[2] 十字线:', cross ? 'OK' : 'MISSING')
console.log('[3] 浮层文本:', tip ? JSON.stringify(tip.textContent.replace(/\s+/g, ' ').trim()) : 'MISSING')
console.log('[4] 期望第30点值 =', expected, '→', tip && tip.textContent.includes(expected) ? 'OK' : 'MISMATCH')
console.log('[5] hover 后 circle 数（应为 0）:', document.querySelectorAll('circle').length)

// 换到另一个点，验证浮层会跟着更新
svg.dispatchEvent(new window.MouseEvent('mousemove', { clientX: 20 + 42 + 50 * (246 / 59), clientY: 90, bubbles: true }))
await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))
const tip2 = document.querySelector('.mtc-tip')
const expected2 = points[50].value.toLocaleString('zh-CN', { maximumFractionDigits: 2 })
console.log('[6] 移动到第50点，期望 =', expected2, '→', tip2 && tip2.textContent.includes(expected2) ? 'OK' : 'MISMATCH')

// 移出后清空
svg.dispatchEvent(new window.MouseEvent('mouseleave', { bubbles: true }))
await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))
console.log('[7] 移出后浮层:', document.querySelector('.mtc-tip') ? 'STILL THERE(FAIL)' : '已清除 OK')

process.exit(0)
