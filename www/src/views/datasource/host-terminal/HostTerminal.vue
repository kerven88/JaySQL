<template>
  <div class="host-terminal">
    <div class="term-head">
      <div class="term-head-left">
        <span class="term-title">{{ ctx.name || '主机终端' }}</span>
        <span v-if="targetLabel" class="term-target">{{ targetLabel }}</span>
        <a-tooltip v-if="fingerprint" :title="fingerprint" :mouse-enter-delay="0.1">
          <span class="term-fp">指纹 {{ shortFingerprint }}</span>
        </a-tooltip>
      </div>
      <div class="term-head-right">
        <span class="term-status" :class="`is-${phase}`">{{ statusText }}</span>
        <a-button size="small" danger :disabled="phase !== 'connected'" @click="disconnect">断开</a-button>
      </div>
    </div>

    <div v-if="banner.text" class="term-banner" :class="`is-${banner.type}`">
      <span class="term-banner-text">{{ banner.text }}</span>
      <a v-if="banner.action === 'reconnect'" class="term-banner-link" @click="start">重连</a>
      <a v-else-if="banner.action === 'configure'" class="term-banner-link" @click="goConfigure">去配置</a>
      <a v-else-if="banner.action === 'forget'" class="term-banner-link" @click="forgetFingerprint">清除指纹并重连</a>
    </div>

    <div ref="termRef" class="term-body"></div>
  </div>
</template>

<script setup>
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  buildHostLoginWsUrl,
  closeHostLoginSessionApi,
  createHostLoginSessionApi,
  getHostLoginSessionsApi,
  hostLoginPrecheckApi,
} from '@/api/datasource'

// ---------------------------------------------------------------------------
// 主机终端（数据源总览 → 操作列「主机登录」→ 新窗口）
//
// 三段式建连：预检（凭据齐备 + 主机可连 + 拿指纹）→ 签一次性令牌 → WS 握手时才真连 SSH。
// 之所以不一步到位：令牌 60 秒可作废，用户迟迟不连就不会留下半开的 SSH 会话；
// 且 WS 握手必须在拿到令牌后立刻发起（令牌单次使用）。
// ---------------------------------------------------------------------------

const route = useRoute()
const router = useRouter()

const CTX_KEY_PREFIX = 'goinsight#datasource#host-terminal#'
const FP_KEY_PREFIX = 'goinsight#datasource#host-key#'

// 阶段：init → connecting → connected → failed / closed
const phase = ref('init')
const targetLabel = ref('')
const fingerprint = ref('')
const ctx = reactive({ instance_id: '', name: '' })
const banner = reactive({ type: 'error', text: '', action: '' })

const termRef = ref(null)

// xterm 与 WebSocket 都不能进 reactive：Vue 的 Proxy 会破坏它们的内部状态
let term = null
let fitAddon = null
let ws = null
let sessionId = ''
let heartbeatTimer = null
let resizeObserver = null
// 无 PTY 的降级会话：远端没有窗口尺寸的概念，resize 帧发了也没人接
let noPty = false

// 远端退出/连接失败码 → 展示文案与可执行的动作。
// 凭据类问题给「去配置」，网络类给「重连」，指纹变更给「清除指纹并重连」（需用户显式确认）。
const EXIT_INFO = {
  LOGOUT: { type: 'info', text: '远程 shell 已退出。', action: 'reconnect' },
  CLOSED: { type: 'info', text: '会话已断开。', action: 'reconnect' },
  INTERRUPTED: { type: 'error', text: '连接已中断，可能是网络波动或页面被关闭。', action: 'reconnect' },
  IDLE_TIMEOUT: { type: 'warning', text: '会话因连续 30 分钟无操作已自动断开。', action: 'reconnect' },
  SESSION_EXPIRED: { type: 'warning', text: '会话已达最长时长（2 小时），请重新连接。', action: 'reconnect' },
  AUTH_FAILED: { type: 'error', text: '', action: 'configure' },
  KEY_INVALID: { type: 'error', text: '', action: 'configure' },
  AUTH_METHOD_UNAVAILABLE: { type: 'error', text: '', action: 'configure' },
  ALGORITHM_MISMATCH: { type: 'error', text: '', action: 'configure' },
  CREDENTIAL_INCOMPLETE: { type: 'error', text: '', action: 'configure' },
  NO_CREDENTIAL: { type: 'error', text: '', action: 'configure' },
  ENGINE_UNSUPPORTED: { type: 'error', text: '', action: '' },
  CLOUD_MANAGED: { type: 'error', text: '', action: '' },
  SESSION_LIMIT: { type: 'warning', text: '', action: '' },
  HOST_KEY_CHANGED: { type: 'error', text: '', action: 'forget' },
}

const statusText = computed(
  () =>
    ({
      init: '准备中',
      connecting: '连接中',
      connected: '已连接',
      failed: '连接失败',
      closed: '已断开',
    })[phase.value] || '准备中',
)

const shortFingerprint = computed(() => {
  const fp = String(fingerprint.value || '')
  // SHA256:xxxx 只留算法前缀与后 8 位，完整值放 tooltip
  if (fp.length <= 14) return fp
  const [algo, body] = fp.split(':')
  return `${algo}:…${body.slice(-8)}`
})

// ---------------------------------------------------------------------------
// 本地存储
// ---------------------------------------------------------------------------

// 上下文（实例名）按实例各存一条，开窗口时覆盖写。
// 不读完即删 —— 终端页刷新是很常见的操作，删了刷新后标题就没了。
const readCtx = (instanceId) => {
  try {
    const raw = window.localStorage.getItem(`${CTX_KEY_PREFIX}${instanceId}`)
    if (raw) {
      const parsed = JSON.parse(raw)
      return { instance_id: parsed?.instance_id || instanceId, name: parsed?.name || '' }
    }
  } catch {
    /* localStorage 不可用时退化为不带名称 */
  }
  return { instance_id: instanceId, name: '' }
}

// 主机密钥指纹：TOFU。第一次连上记录，之后每次建会话带上，服务端比对不一致就断在 PTY 之前。
// 存浏览器而不是服务端，是为了不为一个临时视图单独建表。
const readFingerprint = (instanceId) => {
  try {
    return window.localStorage.getItem(`${FP_KEY_PREFIX}${instanceId}`) || ''
  } catch {
    return ''
  }
}
const saveFingerprint = (instanceId, fp) => {
  if (!fp) return
  try {
    window.localStorage.setItem(`${FP_KEY_PREFIX}${instanceId}`, fp)
  } catch {
    /* 存不下不影响本次连接，只是下次会当作首次登录再确认一次指纹 */
  }
}
const clearFingerprint = (instanceId) => {
  try {
    window.localStorage.removeItem(`${FP_KEY_PREFIX}${instanceId}`)
  } catch {
    /* ignore */
  }
}

// ---------------------------------------------------------------------------
// 提示条
// ---------------------------------------------------------------------------

function showBanner(code, fallbackText) {
  const info = EXIT_INFO[code] || {}
  banner.type = info.type || 'error'
  banner.text = info.text || fallbackText || '连接失败，请稍后重试。'
  banner.action = info.action || ''
  if (code === 'SESSION_LIMIT') {
    // 并发上限时把「我还有哪些终端」摆出来，否则用户不知道该关哪个
    void getHostLoginSessionsApi()
      .then((res) => {
        const list = res?.data || []
        if (list.length) {
          banner.text = `${banner.text} 您当前有 ${list.length} 个终端：${list.map((s) => s.label).join('、')}`
        }
      })
      .catch(() => {})
  }
}

const clearBanner = () => {
  banner.text = ''
  banner.action = ''
}

// ---------------------------------------------------------------------------
// 终端
// ---------------------------------------------------------------------------

function ensureTerminal() {
  if (term) return
  term = new Terminal({
    cursorBlink: true,
    fontSize: 13,
    fontFamily: 'Menlo, Monaco, Consolas, "Courier New", monospace',
    scrollback: 5000,
    convertEol: false,
    // 终端本身就是一块画布，这里用常规的深色终端配色，与页面其它区域的浅色无关
    theme: {
      background: '#0b0e14',
      foreground: '#d3d8e0',
      cursor: '#4ec9b0',
      selectionBackground: '#2b4a55',
      black: '#0b0e14',
      red: '#e06c75',
      green: '#98c379',
      yellow: '#e5c07b',
      blue: '#61afef',
      magenta: '#c678dd',
      cyan: '#56b6c2',
      white: '#d3d8e0',
      brightBlack: '#5c6370',
      brightRed: '#e06c75',
      brightGreen: '#98c379',
      brightYellow: '#e5c07b',
      brightBlue: '#61afef',
      brightMagenta: '#c678dd',
      brightCyan: '#56b6c2',
      brightWhite: '#ffffff',
    },
  })
  fitAddon = new FitAddon()
  term.loadAddon(fitAddon)
  term.open(termRef.value)
  // 尺寸变化（用户拖窗口）→ 先 fit 再把新的行列数告诉远端
  term.onResize(({ cols, rows }) => sendResize(cols, rows))
  term.onData((data) => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    ws.send(JSON.stringify({ t: 'in', d: data }))
  })
  fitAndSync()
  // 容器尺寸变化（拖窗口）→ 重新 fit。随终端一起重建，重连后才不会失效
  if (typeof ResizeObserver !== 'undefined' && termRef.value && !resizeObserver) {
    resizeObserver = new ResizeObserver(() => fitAndSync())
    resizeObserver.observe(termRef.value)
  }
}

function fitAndSync() {
  if (!fitAddon || !term) return
  try {
    fitAddon.fit()
  } catch {
    // 容器尺寸为 0（页面尚未布局完成）时 FitAddon 会抛错，忽略即可
    return
  }
  sendResize(term.cols, term.rows)
}

function sendResize(cols, rows) {
  if (noPty) return
  if (!ws || ws.readyState !== WebSocket.OPEN) return
  ws.send(JSON.stringify({ t: 'resize', cols, rows }))
}

function destroyTerminal() {
  if (resizeObserver) {
    resizeObserver.disconnect()
    resizeObserver = null
  }
  if (term) {
    try {
      term.dispose()
    } catch {
      /* ignore */
    }
    term = null
    fitAddon = null
  }
}

// ---------------------------------------------------------------------------
// 建连
// ---------------------------------------------------------------------------

async function start() {
  const instanceId = String(route.params.instance_id || '').trim()
  if (!instanceId) {
    phase.value = 'failed'
    showBanner('', '缺少实例参数，请从「数据源总览」重新进入。')
    return
  }

  clearBanner()
  noPty = false
  destroyTerminal()
  phase.value = 'connecting'

  // 1. 预检：不建立交互式会话。顺带拿到主机密钥指纹
  let ticket = null
  try {
    const res = await hostLoginPrecheckApi({ instance_id: instanceId })
    const data = res?.data || {}
    if (data.ssh_user) {
      targetLabel.value = `${data.ssh_user}@${data.ssh_host}:${data.ssh_port}`
    }
    if (!data.ok) {
      phase.value = 'failed'
      showBanner(data.code, data.message)
      return
    }
    // 2. 签发一次性令牌
    const created = await createHostLoginSessionApi({
      instance_id: instanceId,
      host_key_fingerprint: readFingerprint(instanceId),
    })
    ticket = created?.data || {}
    if (ticket.ssh_user) {
      targetLabel.value = `${ticket.ssh_user}@${ticket.ssh_host}:${ticket.ssh_port}`
    }
    if (!ticket.ok) {
      phase.value = 'failed'
      showBanner(ticket.code, ticket.message)
      return
    }
  } catch {
    // 请求层已经弹过全局错误提示，这里只负责把页面切到失败态
    phase.value = 'failed'
    showBanner('', '无法建立终端会话，请稍后重试。')
    return
  }

  sessionId = ticket.session_id
  ensureTerminal()
  connect(ticket)
}

function connect(ticket) {
  let socket
  try {
    socket = new WebSocket(buildHostLoginWsUrl(ticket.session_id, ticket.token))
  } catch {
    phase.value = 'failed'
    showBanner('', '创建 WebSocket 失败，请检查浏览器是否允许 ws 连接。')
    return
  }
  ws = socket

  socket.onopen = () => {
    // 首帧尺寸必须在 ready 之前发，否则远端 PTY 会用默认的 120x32
    fitAndSync()
    // 心跳从握手成功就开始：WS 可能要等服务端拨完 SSH 才 ready，
    // 这段空窗期正是网关最容易掐掉连接的时候
    startHeartbeat()
  }

  socket.onmessage = (event) => {
    let frame = null
    try {
      frame = JSON.parse(event.data)
    } catch {
      return
    }
    if (!frame || !frame.t) return
    if (frame.t === 'out') {
      if (term) term.write(frame.d || '')
      return
    }
    if (frame.t === 'ready') {
      phase.value = 'connected'
      clearBanner()
      if (frame.fingerprint) {
        fingerprint.value = frame.fingerprint
        saveFingerprint(String(route.params.instance_id || ''), frame.fingerprint)
      }
      noPty = !!frame.no_pty
      applyCapabilityNotice(frame)
      fitAndSync()
      if (term) term.focus()
      return
    }
    if (frame.t === 'exit') {
      onExit(frame.code, frame.reason)
      return
    }
    // pong：心跳回应，无需处理
  }

  socket.onerror = () => {
    // 握手阶段的 401（令牌作废）在浏览器侧只会表现为一次无码关闭，统一由 onclose 收敛
  }

  socket.onclose = (event) => {
    if (ws === socket) ws = null
    stopHeartbeat()
    // exit 帧与主动断开都已经收过尾（phase 已是 closed），这里不要再覆盖成失败提示
    if (phase.value === 'closed') return
    const wasConnected = phase.value === 'connected'
    phase.value = 'failed'
    if (wasConnected) {
      showBanner('INTERRUPTED', '连接已中断。')
    } else {
      // 连都没连上：多半是令牌已作废或服务端拒绝了握手
      showBanner('', `未能建立终端连接（${event?.code || '无状态码'}）。请从「数据源总览」重新进入。`)
    }
  }
}

// 能力退化提示：Windows / 无 PTY 的会话有几件事做不到，不说清楚用户只会觉得「终端坏了」
// （表现是拖窗口远端不跟着变、中文变成方块）。提示只出现在 ready 之后一次，不打断正常输入。
function applyCapabilityNotice(frame) {
  const notes = []
  if (frame.no_pty) {
    notes.push('该主机不支持伪终端，已按普通命令行连接：窗口大小不会同步到远端，vi / top 等全屏程序不可用。')
  }
  if (frame.os_hint === 'windows') {
    notes.push('Windows 主机：中文若显示为乱码，在终端内执行 chcp 65001 切换到 UTF-8 代码页。')
  }
  if (!notes.length) return
  banner.type = 'warning'
  banner.text = notes.join(' ')
  banner.action = ''
}

function onExit(code, reason) {
  // 主动断开时会先切 closed，服务端随后补发的 exit 帧不该把提示改回「连接已中断」
  if (phase.value === 'closed') return
  phase.value = 'closed'
  const info = EXIT_INFO[code]
  if (info) {
    banner.type = info.type
    // 服务端给的 reason 更具体（带地址、端口），优先展示它
    banner.text = reason || info.text
    banner.action = info.action || ''
  } else {
    banner.type = 'error'
    banner.text = reason || '会话已结束。'
    banner.action = 'reconnect'
  }
  stopHeartbeat()
  if (ws) {
    try {
      ws.close()
    } catch {
      /* ignore */
    }
    ws = null
  }
}

// ---------------------------------------------------------------------------
// 断开 / 心跳
// ---------------------------------------------------------------------------

async function disconnect() {
  if (sessionId) {
    // 服务端收到后主动回收会话；失败也不影响本地关闭（WS 断开本身就会触发回收）
    try {
      await closeHostLoginSessionApi(sessionId)
    } catch {
      /* ignore */
    }
  }
  // 先把阶段切走再关 WS：onclose 会看 phase 决定是否收尾，
  // 顺序反了会把「已主动断开」覆盖成「连接已中断」
  phase.value = 'closed'
  banner.type = 'info'
  banner.text = '已主动断开连接。'
  banner.action = 'reconnect'
  stopHeartbeat()
  if (ws) {
    try {
      ws.close()
    } catch {
      /* ignore */
    }
    ws = null
  }
}

function startHeartbeat() {
  stopHeartbeat()
  // 30 秒一次：Nginx / 网关常见的 60 秒空闲断连靠它躲过去
  heartbeatTimer = window.setInterval(() => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    ws.send(JSON.stringify({ t: 'ping' }))
  }, 30000)
}

function stopHeartbeat() {
  if (heartbeatTimer) {
    window.clearInterval(heartbeatTimer)
    heartbeatTimer = null
  }
}

// ---------------------------------------------------------------------------
// 动作
// ---------------------------------------------------------------------------

function goConfigure() {
  const instanceId = String(route.params.instance_id || '')
  router.push({ path: '/datasource/instance', query: { edit_instance_id: instanceId } })
}

function forgetFingerprint() {
  clearFingerprint(String(route.params.instance_id || ''))
  start()
}

onMounted(() => {
  const instanceId = String(route.params.instance_id || '')
  const loaded = readCtx(instanceId)
  ctx.instance_id = loaded.instance_id
  ctx.name = loaded.name
  start()
})

onBeforeUnmount(() => {
  stopHeartbeat()
  if (ws) {
    try {
      ws.close()
    } catch {
      /* ignore */
    }
    ws = null
  }
  destroyTerminal()
})
</script>

<style scoped lang="scss">
/* 独立路由、不套 Layout（见 router/ark.js）：父级 #app 已经是 100vh，
   这里直接撑满即可。不要再减顶栏 / 公告条 / 布局留白——这个页面里没有它们。 */
.host-terminal {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 320px;
  background: var(--gi-color-bg-container, #fff);
  overflow: hidden;
}

.term-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--gi-color-border, rgba(5, 23, 34, 0.1));
  flex: none;
}

.term-head-left {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.term-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--gi-color-text, rgba(0, 0, 0, 0.88));
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 260px;
}

.term-target,
.term-fp {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, rgba(79, 100, 112, 0.8));
  font-family: Menlo, Monaco, Consolas, 'Courier New', monospace;
  white-space: nowrap;
}

.term-head-right {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: none;
}

.term-status {
  font-size: 12px;
  padding: 1px 8px;
  border-radius: 10px;
  background: rgba(0, 0, 0, 0.04);
  color: var(--gi-color-text-secondary, rgba(0, 0, 0, 0.65));

  &.is-connected {
    background: rgba(82, 196, 26, 0.12);
    color: #389e0d;
  }

  &.is-connecting {
    background: rgba(22, 119, 255, 0.12);
    color: #0958d9;
  }

  &.is-failed {
    background: rgba(255, 77, 79, 0.12);
    color: #cf1322;
  }
}

.term-banner {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 12px;
  font-size: 12.5px;
  flex: none;
  border-bottom: 1px solid transparent;

  &.is-error {
    background: rgba(255, 77, 79, 0.08);
    color: #cf1322;
  }

  &.is-warning {
    background: rgba(250, 173, 20, 0.1);
    color: #ad6800;
  }

  &.is-info {
    background: rgba(0, 0, 0, 0.03);
    color: var(--gi-color-text-secondary, rgba(0, 0, 0, 0.65));
  }
}

.term-banner-text {
  min-width: 0;
}

.term-banner-link {
  flex: none;
  color: var(--gi-color-primary, #0f766e);
  cursor: pointer;

  &:hover {
    color: var(--gi-color-primary-hover, #0d6b63);
  }
}

.term-body {
  flex: 1 1 auto;
  min-height: 0;
  padding: 6px 8px;
  background: #0b0e14;
}
</style>
