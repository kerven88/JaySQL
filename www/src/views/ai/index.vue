<template>
  <div v-if="loggedIn" class="ai-page" :class="{ 'no-ctx': !ctxOpen, 'no-conv': !convOpen }">
    <!-- ============ 左：会话历史 ============ -->
    <section class="panel conv-panel">
      <div class="conv-head">
        <button type="button" class="btn-new" @click="handleNewConversation">
          <PlusOutlined />
          新建对话
        </button>
        <div class="conv-search">
          <SearchOutlined class="conv-search-icon" />
          <input v-model="keyword" placeholder="搜索历史对话" />
        </div>
      </div>

      <div class="conv-list">
        <template v-if="groupedConversations.length">
          <template v-for="group in groupedConversations" :key="group.label">
            <div class="conv-group-title">{{ group.label }}</div>
            <div
              v-for="conv in group.items"
              :key="conv.id"
              class="conv-item"
              :class="{ 'is-active': conv.id === currentId }"
              @click="handleSelectConversation(conv.id)"
            >
              <div class="conv-title">{{ conv.title }}</div>
              <div class="conv-meta">
                <span>{{ formatConvTime(conv.updatedAt) }}</span>
                <span>{{ conv.messages.length }} 条</span>
              </div>
              <a-dropdown trigger="click" placement="bottomRight">
                <button type="button" class="conv-more" title="重命名 / 删除" @click.stop>
                  <EllipsisOutlined />
                </button>
                <template #overlay>
                  <a-menu @click="(info) => handleConvMenu(info, conv)">
                    <a-menu-item key="rename">
                      <EditOutlined />
                      重命名
                    </a-menu-item>
                    <a-menu-item key="delete" danger>
                      <DeleteOutlined />
                      删除
                    </a-menu-item>
                  </a-menu>
                </template>
              </a-dropdown>
            </div>
          </template>
        </template>
        <div v-else class="conv-empty">
          {{ keyword ? '没有匹配的对话' : '还没有对话，点击上方「新建对话」开始' }}
        </div>
      </div>
    </section>

    <!-- ============ 中：对话区 ============ -->
    <section ref="chatPanelRef" class="panel chat-panel">
      <div class="chat-head">
        <button
          type="button"
          class="icon-btn"
          :title="convOpen ? '收起会话列表' : '展开会话列表'"
          @click="convOpen = !convOpen"
        >
          <MenuFoldOutlined v-if="convOpen" />
          <MenuUnfoldOutlined v-else />
        </button>
        <div class="chat-head-main">
          <div class="chat-head-title">
            <span class="chat-head-text">{{ currentConversation?.title || '新对话' }}</span>
            <span class="beta-tag">Beta</span>
          </div>
        </div>
        <div class="chat-head-right">
          <span class="chip-model" :title="modelChipTitle">
            <span class="model-dot" :class="{ 'is-off': !defaultProvider }" />
            {{ modelChipLabel }}
          </span>
          <button
            v-if="!ctxOpen"
            type="button"
            class="icon-btn"
            title="展开右侧面板"
            @click="ctxOpen = true"
          >
            <DoubleLeftOutlined />
          </button>
          <button
            type="button"
            class="icon-btn"
            :title="isFullscreen ? '退出全屏（Esc）' : '全屏对话'"
            @click="toggleChatFullscreen"
          >
            <FullscreenExitOutlined v-if="isFullscreen" />
            <FullscreenOutlined v-else />
          </button>
          <button
            type="button"
            class="icon-btn"
            title="清空当前对话"
            :disabled="!messages.length"
            @click="handleClear"
          >
            <DeleteOutlined />
          </button>
        </div>
      </div>

      <AIChatThread
        ref="threadRef"
        :messages="messages"
        :loading="loading"
        :user-name="userName"
        :user-avatar="userAvatar"
        :trace-default-open="policy.showToolDetail"
        @send="handleSend"
        @cancel="cancelMessage"
        @copy="handleCopyMessage"
        @delete="handleDeleteMessage"
      />
    </section>

    <!-- ============ 右：上下文 ============ -->
    <aside class="panel ctx-panel">
      <div class="ctx-sec">
        <div class="ctx-title">
          <DatabaseOutlined />
          当前引用的实例
          <button
            type="button"
            class="icon-btn ctx-collapse"
            title="收起右侧面板"
            @click="ctxOpen = false"
          >
            <DoubleRightOutlined />
          </button>
        </div>
        <div v-if="referencedInstance" class="ctx-card">
          <div class="ctx-inst-name">
            <DatabaseOutlined class="ctx-inst-icon" />
            {{ referencedInstance.label }}
          </div>
          <div class="ctx-inst-meta">{{ referencedInstance.endpoint }}</div>
          <div class="ctx-tags">
            <span v-if="referencedInstance.dbType" class="tag">{{ referencedInstance.dbType }}</span>
            <span class="tag tag-grey">{{ referencedInstance.origin }}</span>
          </div>
        </div>
        <div v-else class="ctx-hint">本会话尚未引用实例。说出主机地址或实例名，我会自动解析。</div>
      </div>

      <div class="ctx-sec">
        <div class="ctx-title">
          <ThunderboltOutlined />
          本会话的工具调用
        </div>
        <template v-if="toolStats.rows.length">
          <div v-for="row in toolStats.rows" :key="row.tool" class="kv">
            <span class="kv-k">{{ row.tool }}</span>
            <span class="kv-v">{{ row.count }} 次</span>
          </div>
          <div class="kv kv-total">
            <span class="kv-k">累计耗时</span>
            <span class="kv-v">{{ (toolStats.total / 1000).toFixed(2) }} s</span>
          </div>
        </template>
        <div v-else class="ctx-hint">暂无工具调用。</div>
      </div>

      <div class="ctx-sec">
        <div class="ctx-title">
          <SafetyOutlined />
          安全策略
        </div>
        <div class="switch-row">
          <div class="switch-texts">
            <div class="switch-label">写操作二次确认</div>
            <div class="switch-desc">创建/修改/删除类动作必须点确认才执行</div>
          </div>
          <button
            type="button"
            class="switch is-on is-locked"
            title="策略由系统统一管控，不可关闭"
          />
        </div>
        <div class="switch-row">
          <div class="switch-texts">
            <div class="switch-label">只读查询免确认</div>
            <div class="switch-desc">查状态、查实例、生成 SQL 等直接返回</div>
          </div>
          <button
            type="button"
            class="switch"
            :class="{ 'is-on': policy.readOnlyNoConfirm }"
            @click="togglePolicy('readOnlyNoConfirm')"
          />
        </div>
        <div class="switch-row">
          <div class="switch-texts">
            <div class="switch-label">显示工具调用明细</div>
            <div class="switch-desc">在回复中默认展开每步调用的参数与耗时</div>
          </div>
          <button
            type="button"
            class="switch"
            :class="{ 'is-on': policy.showToolDetail }"
            @click="togglePolicy('showToolDetail')"
          />
        </div>
      </div>

      <div class="ctx-sec">
        <button type="button" class="ctx-title ctx-title-toggle" @click="betaOpen = !betaOpen">
          <InfoCircleOutlined />
          <span>当前版本（Beta）</span>
          <span class="ctx-toggle-text">{{ betaOpen ? '收起' : '展开' }}</span>
        </button>
        <div v-show="betaOpen" class="beta-note">
          <div class="beta-note-row">
            <span class="dot-ok" />
            <span><b>已真实可用：</b>对话由真实大模型驱动；模型可调用平台工具查询真实数据（实例、工单、备份执行结果、监控告警、慢查询），回答会附上真实的工具调用与耗时。</span>
          </div>
          <div class="beta-note-row">
            <span class="dot-warn" />
            <span><b>写操作需人工确认：</b>创建工单等写操作只生成待确认草稿，你点确认后才跳转到工单创建页提交，AI 不会自行落库。</span>
          </div>
          <div class="beta-note-row">
            <span class="dot-off" />
            <span><b>模型不可用即报错：</b>未接入服务商或调用失败时，不会用演示数据兜底，而是返回失败原因并给出排查入口。</span>
          </div>
        </div>
      </div>
    </aside>

    <a-modal
      v-model:open="renameState.open"
      title="重命名对话"
      :width="420"
      ok-text="保存"
      cancel-text="取消"
      @ok="confirmRename"
    >
      <a-input
        v-model:value="renameState.title"
        :maxlength="60"
        placeholder="请输入对话名称"
        @press-enter="confirmRename"
      />
    </a-modal>
  </div>

  <div v-else class="ai-locked">
    <!-- 未登录（含 token 失效、路由守卫看门狗强制放行的异常路径）不渲染助手主体，
         避免出现无数据、接口 401 的错乱界面；此处仅作过渡，随即跳转登录页 -->
    <div class="ai-locked-box">
      <a-spin size="large" />
      <div class="ai-locked-title">AI 助手需要登录后使用</div>
      <div class="ai-locked-desc">正在跳转到登录页…</div>
    </div>
  </div>
</template>

<script setup>
import {
  DatabaseOutlined,
  DeleteOutlined,
  DoubleLeftOutlined,
  DoubleRightOutlined,
  EditOutlined,
  EllipsisOutlined,
  FullscreenExitOutlined,
  FullscreenOutlined,
  InfoCircleOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  PlusOutlined,
  SafetyOutlined,
  SearchOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons-vue'
import { aiProviderListApi } from '@/api/ai'
import { useUserStore } from '@/store/user'
import { Modal, message } from 'ant-design-vue'
import { computed, inject, nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAIChat, AIChatRole } from './composables/useAIChat'
import { useAIConversations } from './composables/useAIConversations'
import AIChatThread from './components/AIChatThread.vue'

const POLICY_KEY = 'jaysql_ai_policy'
// v2：showToolDetail 默认值 true → false（工具调用明细默认收起），并给存量 localStorage 数据做一次性迁移
const POLICY_VERSION = 2

const userStore = useUserStore()
const router = useRouter()

// 登录门禁：AI 助手仅对已登录用户开放。未登录（含 token 失效、路由守卫看门狗
// 强制放行的异常路径）不渲染助手主体，直接回登录页；登录态下页面与改动前完全一致。
const loggedIn = computed(() => userStore.isLoggedIn)
const gotoLogin = () => {
  router.replace({ path: '/login', query: { redirect: '/ai' } })
}
watch(
  loggedIn,
  (value) => {
    if (!value) gotoLogin()
  },
  { immediate: true },
)

// 本页自己管滚动（内容区切到 overflow:hidden），离开时归还
const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)

const {
  messages,
  loading,
  setMessages,
  sendMessage,
  cancelMessage,
  clearChat,
} = useAIChat()

const {
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
} = useAIConversations()

const ctxOpen = ref(false)
const convOpen = ref(true)
const betaOpen = ref(false)
const userName = computed(() => userStore.displayName || '我')
// 与顶栏（Layout.vue）同口径：store.avatar 非空即视为已设置头像
const userAvatar = computed(() => String(userStore.avatar || '').trim())

/* ---------------- 会话切换 ---------------- */
const threadRef = ref(null)

const bindCurrentConversation = () => {
  setMessages(currentConversation.value?.messages || [])
}

bindCurrentConversation()
watch(currentId, () => bindCurrentConversation())

/**
 * 点侧栏会话项。除了选中目标会话，还要把它定位到最新一条消息。
 * 重复点「当前已选中」的会话时 currentId 没变化，绑定流程与 thread 的
 * messages 监听都不会触发，视图就会停在用户上次滚到的位置 —— 而这同样
 * 属于「打开这个对话」，预期是回到最新一条，所以这里显式补一次滚动。
 */
const handleSelectConversation = async (id) => {
  selectConversation(id)
  await nextTick()
  threadRef.value?.scrollToBottom()
}

const handleNewConversation = () => {
  createConversation()
  bindCurrentConversation()
}

const handleSend = async (text) => {
  if (!currentConversation.value) {
    // 进入页面未选中任何会话（默认空状态），发送时先建新会话承接
    createConversation()
    bindCurrentConversation()
  }
  const conv = currentConversation.value
  const sending = sendMessage(text)
  // sendMessage 内部同步 push 用户消息，此处即可回写标题（首句自动命名）
  markConversationUpdated(conv)
  await sending
  markConversationUpdated(conv)
}

const handleClear = () => {
  if (!messages.value.length) return
  Modal.confirm({
    title: '清空当前对话',
    content: '将删除本会话的全部消息，操作不可撤销。',
    okText: '清空',
    okType: 'danger',
    cancelText: '取消',
    onOk: () => {
      clearChat()
      const conv = currentConversation.value
      if (conv) {
        conv.title = '新对话'
        conv.updatedAt = Date.now()
      }
    },
  })
}

/* ---------------- 单条消息：复制 / 按轮次删除 ---------------- */

/**
 * 写剪贴板。优先用异步 Clipboard API；内网 http 地址（如 192.168.1.99）属于
 * 非安全上下文，navigator.clipboard 直接不存在，必须回落 execCommand ——
 * 否则会出现「点了复制但没有任何反应」。
 */
const copyToClipboard = async (value) => {
  try {
    if (window.isSecureContext && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value)
      return true
    }
  } catch (_) {
    /* 权限被拒 → 走下面的兜底 */
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = value
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.top = '-9999px'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    ta.setSelectionRange(0, value.length)
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch (_) {
    return false
  }
}

const handleCopyMessage = async (msg) => {
  const text = String(msg?.text || '')
  if (!text) {
    message.info('这条消息没有可复制的文字')
    return
  }
  const ok = await copyToClipboard(text)
  if (ok) message.success('已复制')
  else message.error('复制失败，请手动选中文本复制')
}

/**
 * 求一条消息所属「对话轮次」的下标区间 [start, end)。
 *
 * 消息模型里没有 turnId，按相邻关系推导（存量数据同样适用）：先向前找最近的
 * 用户消息作为本轮起点，再从起点向后扫到下一条用户消息之前——中间的 assistant
 * 消息（含工具轨迹、代码块、确认卡片）都算这一轮的回答。
 * 删除以轮次为单位，避免删掉提问却留下一条「无头」回答。
 */
const resolveTurnRange = (list, msg) => {
  const idx = list.findIndex((m) => m.id === msg?.id)
  if (idx < 0) return null
  let start = -1
  for (let i = idx; i >= 0; i -= 1) {
    if (list[i]?.role === AIChatRole.USER) {
      start = i
      break
    }
  }
  // 前面没有用户消息（异常数据）：只删自己，不牵连别的
  if (start < 0) return { start: idx, end: idx + 1 }
  let end = list.length
  for (let i = start + 1; i < list.length; i += 1) {
    if (list[i]?.role === AIChatRole.USER) {
      end = i
      break
    }
  }
  return { start, end }
}

const handleDeleteMessage = (msg) => {
  // 发消息时会先塞一条 loading 占位，回复回来靠「改最后一条」回填。
  // 生成期间删掉任何消息都可能让回填落到错误的条目上，故此处一并拦住
  //（按钮侧也置灰，这里只是兜底）。
  if (loading.value) {
    message.info('回复生成中，请稍后再删除')
    return
  }
  const list = messages.value
  const range = resolveTurnRange(list, msg)
  if (!range) return
  const count = range.end - range.start
  Modal.confirm({
    title: count > 1 ? '删除这一轮对话' : '删除这条消息',
    content:
      count > 1
        ? `将同时删除这条提问及其对应的 AI 回答（共 ${count} 条消息）。删除后无法恢复。`
        : '将删除这条消息。删除后无法恢复。',
    okText: '删除',
    okType: 'danger',
    cancelText: '取消',
    onOk: () => {
      list.splice(range.start, count)
      // 删空后标题回到默认，否则侧栏还挂着已被删掉的提问标题
      const conv = currentConversation.value
      if (conv && !list.length) conv.title = '新对话'
    },
  })
}

/* ---------------- 重命名 / 删除 ---------------- */
const renameState = reactive({ open: false, id: '', title: '' })

const handleConvMenu = ({ key }, conv) => {
  if (key === 'rename') {
    renameState.open = true
    renameState.id = conv.id
    renameState.title = conv.title
    return
  }
  if (key === 'delete') {
    Modal.confirm({
      title: '删除对话',
      content: `确认删除「${conv.title}」？该操作不可撤销。`,
      okText: '删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: () => {
        deleteConversation(conv.id)
        bindCurrentConversation()
      },
    })
  }
}

const confirmRename = () => {
  const title = renameState.title.trim()
  if (!title) return
  renameConversation(renameState.id, title)
  renameState.open = false
}

/* ---------------- 默认模型展示 ---------------- */
const providers = ref([])

const loadProviders = async () => {
  try {
    const res = await aiProviderListApi({
      skipGlobalMessageError: true,
      silentHttpError: true,
      silentBusinessError: true,
    })
    providers.value = Array.isArray(res?.data) ? res.data : []
  } catch (_) {
    providers.value = []
  }
}

const defaultProvider = computed(
  () =>
    providers.value.find((p) => p.is_default) ||
    providers.value.find((p) => p.status === 'enabled') ||
    null,
)

const modelChipLabel = computed(() => {
  const provider = defaultProvider.value
  if (!provider) return '未接入模型'
  const name = provider.default_model_name || provider.display_name || provider.vendor_label
  return `默认模型：${name || '未指定'}`
})

const modelChipTitle = computed(() => {
  const provider = defaultProvider.value
  if (!provider) return '尚未接入大模型服务商，当前对话走本地规则匹配'
  return `${provider.vendor_label || provider.vendor} · ${provider.base_url}`
})

/* ---------------- 右栏：引用实例 / 工具统计 ---------------- */
const referencedInstance = computed(() => {
  const list = messages.value
  for (let i = list.length - 1; i >= 0; i -= 1) {
    const msg = list[i]
    const suggested = msg?.sql?.suggestedInstance
    if (suggested) {
      return {
        label: suggested,
        endpoint: msg?.sql?.suggestedDatabase ? `建议库：${msg.sql.suggestedDatabase}` : '来自 SQL 建议',
        dbType: '',
        origin: 'SQL 建议',
      }
    }
  }
  return null
})

const toolStats = computed(() => {
  const map = new Map()
  let total = 0
  messages.value.forEach((msg) => {
    ;(msg.trace || []).forEach((step) => {
      const ms = parseInt(step?.ms, 10) || 0
      total += ms
      const row = map.get(step.tool) || { tool: step.tool, count: 0 }
      row.count += 1
      map.set(step.tool, row)
    })
  })
  return { rows: [...map.values()], total }
})

/* ---------------- 安全策略（本地偏好） ---------------- */
// showToolDetail 默认收起：工具调用明细默认折叠成「已调用 N 个工具」一行，
// 需要看的用户可在右栏打开「显示工具调用明细」。
const policy = reactive({ readOnlyNoConfirm: true, showToolDetail: false })

const loadPolicy = () => {
  try {
    const saved = JSON.parse(localStorage.getItem(POLICY_KEY) || '{}')
    // v1 时代 showToolDetail 默认 true，且切换任一开关都会把整个 policy 连带写入。
    // 存量数据里的 true 无法区分「用户刻意开启」和「旧默认值被连带持久化」，
    // 故 v2 起默认收起，旧数据一次性重置为新默认，之后以用户在开关上的实际操作为准。
    const legacy = saved.v !== POLICY_VERSION
    if (typeof saved.readOnlyNoConfirm === 'boolean') policy.readOnlyNoConfirm = saved.readOnlyNoConfirm
    if (!legacy && typeof saved.showToolDetail === 'boolean') policy.showToolDetail = saved.showToolDetail
  } catch (_) {
    /* 忽略脏数据 */
  }
}

const persistPolicy = () => {
  try {
    localStorage.setItem(POLICY_KEY, JSON.stringify({ v: POLICY_VERSION, ...policy }))
  } catch (_) {
    /* 忽略 */
  }
}

const togglePolicy = (key) => {
  policy[key] = !policy[key]
  persistPolicy()
}

/* ---------------- 时间格式 ---------------- */
const formatConvTime = (ts) => {
  if (!ts) return ''
  const d = new Date(ts)
  const now = new Date()
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  const startOfDay = (x) => new Date(x).setHours(0, 0, 0, 0)
  const days = Math.round((startOfDay(now) - startOfDay(d)) / 86400000)
  if (days <= 0) return `${hh}:${mm}`
  if (days === 1) return '昨天'
  if (days < 7) return `${days} 天前`
  return `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

/* ---------------- 生命周期 ---------------- */
const applyNoScroll = (value) => {
  if (typeof setLayoutContentNoScroll === 'function') setLayoutContentNoScroll(value)
}

/* ---------------- 全屏对话（浏览器 Fullscreen API） ----------------
   对「中间对话面板」本身请求全屏：进入后只留对话区铺满整块屏幕，左右两栏被遮挡，
   适合长时间专注对话。退出方式有两种，状态都要能回同步图标：
   面板上的按钮，以及 Esc / 浏览器手势（由 fullscreenchange 事件捕获）。 */
const chatPanelRef = ref(null)
const isFullscreen = ref(false)

const syncFullscreenState = () => {
  // 用 fullscreenElement 反查，而不是自己 toggle 一个布尔量：
  // 用户按 Esc 或浏览器手势退出时也要能把图标切回「进入全屏」
  isFullscreen.value = !!chatPanelRef.value && document.fullscreenElement === chatPanelRef.value
}

const exitChatFullscreen = () => {
  // 切页/卸载时若还停在全屏，界面会像卡住：主动退出
  if (document.fullscreenElement) {
    document.exitFullscreen().catch(() => {})
  }
  isFullscreen.value = false
}

const toggleChatFullscreen = async () => {
  const el = chatPanelRef.value
  if (!el) return
  try {
    if (document.fullscreenElement) {
      await document.exitFullscreen()
      return
    }
    // 只能由用户手势触发；被浏览器策略拒绝（如嵌入 iframe 未授权）时明确提示而不是静默失败
    await el.requestFullscreen()
  } catch (err) {
    message.error('当前浏览器不允许进入全屏，请检查浏览器权限设置')
  }
}

onMounted(() => {
  if (!loggedIn.value) return
  applyNoScroll(true)
  loadPolicy()
  loadProviders()
  document.addEventListener('fullscreenchange', syncFullscreenState)
})

onActivated(() => {
  if (loggedIn.value) applyNoScroll(true)
})
onDeactivated(() => {
  applyNoScroll(false)
  exitChatFullscreen()
})

// 页面卸载（切页）前：先复位中断的 loading 态，再立即落盘 ——
// 跳过防抖窗口，保证切页/刷新后回来记录完整不丢。
onBeforeUnmount(() => {
  messages.value.forEach((msg) => {
    if (msg.loading) msg.loading = false
  })
  flushPersist()
  applyNoScroll(false)
  document.removeEventListener('fullscreenchange', syncFullscreenState)
  exitChatFullscreen()
})
</script>

<style scoped>
/* 未登录过渡态：不渲染助手主体，仅提示并等待跳转登录页 */
.ai-locked {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
}

.ai-locked-box {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  text-align: center;
}

.ai-locked-title {
  font-size: 15px;
  font-weight: 500;
  color: #1f2d3d;
}

.ai-locked-desc {
  font-size: 13px;
  color: rgba(79, 100, 112, 0.72);
}

.ai-page {
  display: grid;
  grid-template-columns: 264px minmax(0, 1fr) 296px;
  gap: 16px;
  height: 100%;
  min-height: 0;
  transition: grid-template-columns 0.22s ease;
}

.ai-page.no-ctx {
  grid-template-columns: 264px minmax(0, 1fr);
}

.ai-page.no-conv {
  grid-template-columns: minmax(0, 1fr) 296px;
}

.ai-page.no-conv.no-ctx {
  grid-template-columns: minmax(0, 1fr);
}

.ai-page.no-ctx .ctx-panel {
  display: none;
}

.ai-page.no-conv .conv-panel {
  display: none;
}

.panel {
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: #ffffff;
  border: 1px solid rgba(27, 73, 88, 0.08);
  border-radius: 22px;
  box-shadow: 0 14px 32px rgba(18, 58, 72, 0.08);
}

/* ---------- 左：会话历史 ---------- */
.conv-head {
  flex: 0 0 auto;
  padding: 16px 16px 10px;
}

.btn-new {
  width: 100%;
  height: 40px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: none;
  border-radius: 10px;
  font-size: 14px;
  font-weight: 600;
  font-family: inherit;
  color: #ffffff;
  background: linear-gradient(135deg, #0f766e 0%, #0d6b63 100%);
  box-shadow: 0 6px 18px rgba(15, 118, 110, 0.22);
  cursor: pointer;
  transition: transform 0.15s ease, box-shadow 0.15s ease;
}

.btn-new:hover {
  transform: translateY(-1px);
  box-shadow: 0 10px 22px rgba(15, 118, 110, 0.28);
}

.conv-search {
  position: relative;
  margin-top: 12px;
}

.conv-search-icon {
  position: absolute;
  left: 11px;
  top: 50%;
  transform: translateY(-50%);
  font-size: 13px;
  color: rgba(79, 100, 112, 0.8);
  pointer-events: none;
}

.conv-search input {
  width: 100%;
  height: 34px;
  padding: 0 12px 0 34px;
  border-radius: 10px;
  border: 1px solid rgba(27, 73, 88, 0.08);
  background: #f7fafb;
  font-size: 13px;
  font-family: inherit;
  color: #14313d;
  outline: none;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, background 0.2s ease;
}

.conv-search input::placeholder {
  color: rgba(79, 100, 112, 0.6);
}

.conv-search input:focus {
  border-color: #0f766e;
  background: #ffffff;
  box-shadow: 0 0 0 3px rgba(15, 118, 110, 0.1);
}

.conv-list {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 6px 10px 12px;
}

.conv-group-title {
  padding: 10px 8px 6px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.06em;
  color: rgba(79, 100, 112, 0.8);
}

.conv-item {
  position: relative;
  padding: 9px 10px 10px;
  border-radius: 10px;
  cursor: pointer;
  transition: background 0.12s ease;
}

.conv-item:hover {
  background: rgba(15, 118, 110, 0.05);
}

.conv-item.is-active {
  background: rgba(15, 118, 110, 0.09);
}

.conv-item.is-active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 12px;
  bottom: 12px;
  width: 3px;
  border-radius: 3px;
  background: #0f766e;
}

.conv-title {
  padding-right: 22px;
  font-size: 13px;
  font-weight: 500;
  line-height: 1.5;
  color: #14313d;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.conv-meta {
  display: flex;
  gap: 8px;
  margin-top: 3px;
  font-size: 11.5px;
  color: #67808d;
}

.conv-more {
  position: absolute;
  right: 6px;
  top: 6px;
  width: 22px;
  height: 22px;
  display: none;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: #67808d;
  cursor: pointer;
}

.conv-item:hover .conv-more {
  display: flex;
}

.conv-more:hover {
  background: rgba(15, 23, 32, 0.06);
}

.conv-empty {
  padding: 24px 10px;
  text-align: center;
  font-size: 12.5px;
  line-height: 1.7;
  color: #67808d;
}

/* ---------- 中：对话区 ---------- */
.chat-panel {
  min-width: 0;
}

/* 浏览器全屏：面板被提升到 top layer，父级 grid 不再参与布局，
   这里把它铺满整屏并去掉卡片观感（圆角 / 边框 / 阴影）。
   对话区内部本就是 flex + 滚动容器，高度放开后自动铺满，无需额外改动。 */
.chat-panel:fullscreen,
.chat-panel:-webkit-full-screen {
  width: 100vw;
  height: 100vh;
  max-height: none;
  border: none;
  border-radius: 0;
  box-shadow: none;
}

.chat-head {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 13px 16px;
  border-bottom: 1px solid rgba(27, 73, 88, 0.08);
}

.chat-head-main {
  min-width: 0;
}

.chat-head-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.chat-head-text {
  font-size: 15px;
  font-weight: 600;
  color: #14313d;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.beta-tag {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  padding: 2px 9px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: #147866;
  background: rgba(24, 165, 143, 0.08);
  border: 1px solid rgba(24, 165, 143, 0.14);
}

.chat-head-right {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 8px;
}

.chip-model {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 26px;
  padding: 0 10px;
  border-radius: 999px;
  font-size: 12px;
  color: #147866;
  background: rgba(24, 165, 143, 0.08);
  border: 1px solid rgba(24, 165, 143, 0.16);
  white-space: nowrap;
}

.model-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #18a58f;
}

.model-dot.is-off {
  background: rgba(79, 100, 112, 0.45);
}

.icon-btn {
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: #67808d;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.12s ease, color 0.12s ease;
}

.icon-btn:hover:not(:disabled) {
  background: rgba(15, 118, 110, 0.07);
  color: #0f766e;
}

.icon-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

/* ---------- 右：上下文 ---------- */
.ctx-panel {
  overflow: auto;
}

.ctx-sec {
  padding: 14px 16px;
  border-bottom: 1px solid rgba(27, 73, 88, 0.08);
}

.ctx-sec:last-child {
  border-bottom: none;
}

.ctx-title {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.05em;
  color: rgba(79, 100, 112, 0.8);
}

/* 右栏内部标题行右侧的收起按钮 */
.ctx-collapse {
  margin-left: auto;
  width: 22px;
  height: 22px;
  font-size: 12px;
}

/* 可折叠区块的标题：整体可点，右侧带展开/收起提示 */
.ctx-title-toggle {
  width: 100%;
  padding: 0;
  border: none;
  background: transparent;
  font-family: inherit;
  text-align: left;
  cursor: pointer;
  transition: color 0.12s ease;
}

.ctx-title-toggle:hover {
  color: #0f766e;
}

.ctx-toggle-text {
  margin-left: auto;
  font-size: 11.5px;
  font-weight: 500;
  letter-spacing: normal;
  color: #0f766e;
}

.ctx-card {
  margin-top: 10px;
  padding: 11px 12px;
  border-radius: 10px;
  background: linear-gradient(180deg, #f8fcfc 0%, #f2f8f8 100%);
  border: 1px solid rgba(25, 72, 89, 0.07);
}

.ctx-inst-name {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 13px;
  font-weight: 600;
  color: #14313d;
  word-break: break-all;
}

.ctx-inst-icon {
  flex: 0 0 auto;
  color: #0f766e;
}

.ctx-inst-meta {
  margin-top: 4px;
  font-size: 12px;
  color: #67808d;
}

.ctx-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.tag {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 999px;
  background: rgba(15, 118, 110, 0.08);
  color: #147866;
  border: 1px solid rgba(15, 118, 110, 0.14);
}

.tag-grey {
  background: rgba(15, 23, 32, 0.05);
  color: rgba(36, 57, 71, 0.72);
  border-color: rgba(15, 23, 32, 0.08);
}

.ctx-hint {
  margin-top: 10px;
  font-size: 12px;
  line-height: 1.7;
  color: #67808d;
}

.kv {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  padding: 5px 0;
  font-size: 12.5px;
}

.kv-k {
  color: #67808d;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  word-break: break-all;
}

.kv-v {
  flex: 0 0 auto;
  color: #14313d;
  font-weight: 500;
}

.kv-total {
  margin-top: 4px;
  padding-top: 8px;
  border-top: 1px dashed rgba(27, 73, 88, 0.1);
}

.kv-total .kv-k {
  font-family: inherit;
}

.switch-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-top: 12px;
}

.switch-texts {
  min-width: 0;
}

.switch-label {
  font-size: 12.5px;
  color: #14313d;
}

.switch-desc {
  margin-top: 2px;
  font-size: 11.5px;
  line-height: 1.5;
  color: #67808d;
}

.switch {
  flex: 0 0 auto;
  position: relative;
  width: 36px;
  height: 20px;
  border: none;
  border-radius: 999px;
  background: rgba(15, 23, 32, 0.16);
  cursor: pointer;
  transition: background 0.16s ease;
}

.switch.is-on {
  background: #0f766e;
}

.switch::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #ffffff;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.2);
  transition: transform 0.16s ease;
}

.switch.is-on::after {
  transform: translateX(16px);
}

.switch.is-locked {
  opacity: 0.55;
  cursor: not-allowed;
}

.beta-note {
  margin-top: 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.beta-note-row {
  display: flex;
  gap: 8px;
  font-size: 11.5px;
  line-height: 1.65;
  color: #67808d;
}

.beta-note-row b {
  color: #14313d;
}

.dot-ok,
.dot-warn,
.dot-off {
  flex: 0 0 auto;
  width: 7px;
  height: 7px;
  margin-top: 5px;
  border-radius: 50%;
}

.dot-ok {
  background: #18a58f;
}

.dot-warn {
  background: #faad14;
}

.dot-off {
  background: rgba(79, 100, 112, 0.45);
}

/* ---------- 响应式 ---------- */
@media (max-width: 1440px) {
  .ai-page {
    grid-template-columns: 240px minmax(0, 1fr) 272px;
  }
  .ai-page.no-ctx {
    grid-template-columns: 240px minmax(0, 1fr);
  }
  .ai-page.no-conv {
    grid-template-columns: minmax(0, 1fr) 272px;
  }
  .ai-page.no-conv.no-ctx {
    grid-template-columns: minmax(0, 1fr);
  }
}

@media (max-width: 1180px) {
  .ai-page,
  .ai-page.no-ctx,
  .ai-page.no-conv {
    grid-template-columns: 232px minmax(0, 1fr);
  }
  .ctx-panel {
    display: none;
  }
}

@media (max-width: 900px) {
  .ai-page,
  .ai-page.no-ctx,
  .ai-page.no-conv {
    grid-template-columns: minmax(0, 1fr);
  }
  .conv-panel {
    display: none;
  }
}

@media (max-width: 767px) {
  .ai-page {
    gap: 8px;
    min-height: 0;
  }
  .chat-head {
    padding: 10px 12px;
  }
  .chip-model {
    display: none;
  }
}

@media (max-width: 768px) {
  /* 900 断点把会话列表 panel 整块隐藏了，移动端完全没有切换/新建会话的入口。
     这里恢复显示：单列布局下它占据顶部一行，作为可滚动的会话条使用。 */
  .conv-panel {
    display: block !important;
    max-height: 30vh;
    overflow-y: auto;
  }

  .ai-page {
    gap: 6px;
  }

  .chat-head {
    padding: 8px 10px;
  }
}
</style>
