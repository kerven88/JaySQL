import { computed, ref, watch } from 'vue'
import { useUserStore } from '@/store/user'
import {
  LEGACY_STORAGE_KEYS,
  MAX_CONVERSATIONS,
  MAX_MESSAGES_PER_CONV,
  PERSIST_DEBOUNCE_MS,
  STORAGE_VERSION,
  aiConversationsKey,
  aiUserKeyOf,
  isAIConversationPersistSuspended,
  resumeAIConversationPersist,
} from '../aiConversationStorage'

const genConvId = () => `conv_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`
const genMsgId = () => `msg_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`

const startOfDay = (ts) => {
  const d = new Date(ts)
  d.setHours(0, 0, 0, 0)
  return d.getTime()
}

// 会话分组：今天 / 昨天 / 近 7 天 / 更早
export const groupLabelOf = (ts, now = Date.now()) => {
  if (!ts) return '更早'
  const days = Math.round((startOfDay(now) - startOfDay(ts)) / 86400000)
  if (days <= 0) return '今天'
  if (days === 1) return '昨天'
  if (days < 7) return '近 7 天'
  return '更早'
}

/* ---------------------------------------------------------------------------
   会话持久化约定
   键格式、版本号、上限、登出清除与落盘闸门统一收在 ../aiConversationStorage.js
   （store/user.js 登出时也 import 同一份，避免两处常量漂移）。本文件只负责：
   1. 恢复：仅登录时读本账号的落盘数据；结构不合法条目直接丢弃；中断的 loading
      占位复位（含刷新瞬间正在等模型回复的消息），空文本的 assistant 占位不恢复，
      避免恢复后出现「一直转圈」或空气泡。
   2. 落盘：会话/当前会话变化后防抖写入；页面卸载（切页）与窗口关闭前各补一次
      flush，保证最后一次改动不丢；闸门关闭（刚登出）时一律 no-op。
   3. 未登录（含 token 失效、路由守卫看门狗强制放行的异常路径）：既不复原也不落盘，
      保持纯内存，避免在登录页读到同一浏览器里他人留下的会话。
--------------------------------------------------------------------------- */

const MESSAGE_TYPES = ['text', 'sql', 'action_card']

// 模块级单例：beforeunload 只注册一次，避免每次进页面重复挂监听
let unloadHooked = false
let activeFlush = null

// 单条消息清洗：只保留可渲染、可序列化的字段
const sanitizeMessage = (raw) => {
  if (!raw || typeof raw !== 'object') return null
  const text = typeof raw.text === 'string' ? raw.text : ''
  // 中断的 loading 占位（还没拿到回复就切页/刷新）直接丢弃，不留下空气泡
  if (raw.loading && !text) return null
  return {
    id: typeof raw.id === 'string' && raw.id ? raw.id : genMsgId(),
    role: raw.role === 'assistant' ? 'assistant' : 'user',
    type: MESSAGE_TYPES.includes(raw.type) ? raw.type : 'text',
    text,
    sql: raw.sql && typeof raw.sql === 'object' ? raw.sql : null,
    action: raw.action && typeof raw.action === 'object' ? raw.action : null,
    trace: Array.isArray(raw.trace) ? raw.trace.filter((s) => s && typeof s === 'object') : null,
    viaModel: typeof raw.viaModel === 'string' ? raw.viaModel : '',
    loading: false,
    createdAt: Number(raw.createdAt) || Date.now(),
  }
}

const sanitizeConversation = (raw) => {
  if (!raw || typeof raw !== 'object') return null
  const createdAt = Number(raw.createdAt) || Date.now()
  const messages = Array.isArray(raw.messages)
    ? raw.messages
        .map(sanitizeMessage)
        .filter(Boolean)
        .slice(-MAX_MESSAGES_PER_CONV)
    : []
  const title = typeof raw.title === 'string' ? raw.title.trim() : ''
  return {
    id: typeof raw.id === 'string' && raw.id ? raw.id : genConvId(),
    title: title || '新对话',
    createdAt,
    updatedAt: Number(raw.updatedAt) || createdAt,
    messages,
  }
}

export function useAIConversations() {
  // 新的一次进入 = 新的一次登录会话开始：重开落盘闸门
  // （登出时 store/user.js 会关闸，401 过期路径不整页刷新，必须在这里补开）
  resumeAIConversationPersist()

  const userKey = (() => {
    try {
      const userStore = useUserStore()
      if (!userStore?.isLoggedIn) return ''
      return aiUserKeyOf(userStore)
    } catch (_) {
      return ''
    }
  })()

  // 旧版本残留键清扫（只做一次，不影响新键）
  try {
    LEGACY_STORAGE_KEYS.forEach((key) => localStorage.removeItem(key))
  } catch (_) {
    /* 隐私模式等，忽略 */
  }

  // —— 恢复：仅在已登录时读取本账号的落盘数据 ——
  const restored = (() => {
    if (!userKey) return null
    try {
      const raw = localStorage.getItem(aiConversationsKey(userKey))
      if (!raw) return null
      const parsed = JSON.parse(raw)
      if (!parsed || parsed.version !== STORAGE_VERSION) return null
      const conversations = (Array.isArray(parsed.conversations) ? parsed.conversations : [])
        .map(sanitizeConversation)
        .filter(Boolean)
      return {
        conversations,
        currentId: typeof parsed.currentId === 'string' ? parsed.currentId : '',
      }
    } catch (_) {
      return null
    }
  })()

  // 回归 / 登出后重登（登出已清键）时为 null ⇒ 走下面的全新空会话，界面即「全新」
  const conversations = ref(restored?.conversations?.length ? restored.conversations : [])
  const currentId = ref('')
  const keyword = ref('')

  // 选中会话：优先恢复上次选中的；该会话已不存在时退回最近更新的一条
  if (conversations.value.length) {
    const wanted = restored?.currentId
    const exists = wanted && conversations.value.some((c) => c.id === wanted)
    currentId.value = exists
      ? wanted
      : [...conversations.value].sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0))[0].id
  }

  const currentConversation = computed(
    () => conversations.value.find((c) => c.id === currentId.value) || null,
  )

  const filteredConversations = computed(() => {
    const kw = keyword.value.trim().toLowerCase()
    const sorted = [...conversations.value].sort(
      (a, b) => (b.updatedAt || 0) - (a.updatedAt || 0),
    )
    if (!kw) return sorted
    return sorted.filter((c) => String(c.title || '').toLowerCase().includes(kw))
  })

  const groupedConversations = computed(() => {
    const buckets = []
    const index = new Map()
    filteredConversations.value.forEach((conv) => {
      const label = groupLabelOf(conv.updatedAt || conv.createdAt)
      if (!index.has(label)) {
        const bucket = { label, items: [] }
        index.set(label, bucket)
        buckets.push(bucket)
      }
      index.get(label).items.push(conv)
    })
    return buckets
  })

  /* ---------------- 落盘 ---------------- */
  let persistTimer = null

  // 落盘前裁剪：按最近更新排序取前 N 个会话，单会话只保留最后 M 条消息
  const buildPayload = () => {
    const ordered = [...conversations.value].sort(
      (a, b) => (b.updatedAt || 0) - (a.updatedAt || 0),
    )
    return {
      version: STORAGE_VERSION,
      savedAt: Date.now(),
      currentId: currentId.value,
      conversations: ordered.slice(0, MAX_CONVERSATIONS).map((conv) => ({
        id: conv.id,
        title: conv.title,
        createdAt: conv.createdAt,
        updatedAt: conv.updatedAt,
        messages: (conv.messages || [])
          .slice(-MAX_MESSAGES_PER_CONV)
          .map(sanitizeMessage)
          .filter(Boolean),
      })),
    }
  }

  const writePayload = (key, payload) => {
    try {
      localStorage.setItem(key, JSON.stringify(payload))
      return true
    } catch (_) {
      return false
    }
  }

  // 落盘是否可用：未登录（无键）或已登出（闸门关闭）时一律不写
  const persistBlocked = () => !userKey || isAIConversationPersistSuspended()

  // 立即落盘（跳过防抖）：供页面卸载/窗口关闭时兜底调用
  const flushPersist = () => {
    if (persistTimer) {
      clearTimeout(persistTimer)
      persistTimer = null
    }
    if (persistBlocked()) return
    const key = aiConversationsKey(userKey)
    const payload = buildPayload()
    if (!payload.conversations.length) {
      // 一个会话都不剩（用户手动删光）：清掉存储，不留空壳
      try {
        localStorage.removeItem(key)
      } catch (_) {
        /* 忽略 */
      }
      return
    }
    if (writePayload(key, payload)) return
    // 配额超限：逐级丢弃最旧会话后重试，最少保留当前这一条
    let list = payload.conversations
    while (list.length > 1) {
      list = list.slice(0, -1)
      if (writePayload(key, { ...payload, conversations: list })) return
    }
    // 仍然失败：放弃本次落盘，内存态不受影响
  }

  const schedulePersist = () => {
    if (persistBlocked()) return
    if (persistTimer) clearTimeout(persistTimer)
    persistTimer = setTimeout(() => {
      persistTimer = null
      flushPersist()
    }, PERSIST_DEBOUNCE_MS)
  }

  const selectConversation = (id) => {
    if (!id || !conversations.value.some((c) => c.id === id)) return
    currentId.value = id
  }

  const createConversation = () => {
    const now = Date.now()
    const conv = {
      id: genConvId(),
      title: '新对话',
      createdAt: now,
      updatedAt: now,
      messages: [],
    }
    conversations.value.unshift(conv)
    currentId.value = conv.id
    return conv
  }

  const renameConversation = (id, title) => {
    const conv = conversations.value.find((c) => c.id === id)
    if (!conv) return
    const next = String(title || '').trim()
    conv.title = next || conv.title
    conv.updatedAt = Date.now()
  }

  const deleteConversation = (id) => {
    const idx = conversations.value.findIndex((c) => c.id === id)
    if (idx < 0) return
    conversations.value.splice(idx, 1)
    if (currentId.value === id) {
      // 删除当前会话后回到「新对话」空状态，不自动跳到其他会话
      currentId.value = ''
    }
    if (!conversations.value.length) {
      createConversation()
    }
  }

  // 发送后回写：更新时间；首句自动命名（标题仍是默认值时）
  const markConversationUpdated = (conv) => {
    if (!conv) return
    conv.updatedAt = Date.now()
    const firstUserMessage = (conv.messages || []).find(
      (m) => m.role === 'user' && m.text,
    )
    if (firstUserMessage && (!conv.title || conv.title === '新对话')) {
      const text = String(firstUserMessage.text).trim()
      conv.title = text.length > 24 ? `${text.slice(0, 24)}…` : text
    }
  }

  // 首次进入且无任何历史：备好一个空会话，对话区即「新对话」空状态
  if (!conversations.value.length) {
    createConversation()
  }

  // 会话内容或选中项变化即防抖落盘
  watch([conversations, currentId], schedulePersist, { deep: true })

  // 窗口关闭 / 刷新前的兜底落盘：模块级只注册一次监听，指向当前活跃实例
  if (!unloadHooked) {
    unloadHooked = true
    window.addEventListener('beforeunload', () => {
      try {
        activeFlush?.()
      } catch (_) {
        /* 忽略 */
      }
    })
  }
  activeFlush = flushPersist

  return {
    conversations,
    currentId,
    keyword,
    currentConversation,
    groupedConversations,
    selectConversation,
    createConversation,
    renameConversation,
    deleteConversation,
    markConversationUpdated,
    flushPersist,
  }
}
