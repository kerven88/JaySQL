<template>
  <div class="ai-thread">
    <div ref="bodyRef" class="thread-body">
      <div class="thread-inner">
        <AIChatEmpty v-if="!messages.length" />
        <template v-else>
          <template v-for="(message, idx) in messages" :key="message.id">
            <div v-if="showTimeSeparator(idx)" class="thread-time">
              <span>{{ formatMsgTime(message.createdAt) }}</span>
            </div>
            <AIChatMessage
              :message="message"
              :user-name="userName"
              :user-avatar="userAvatar"
              :trace-default-open="traceDefaultOpen"
              :busy="loading"
              @copy="emit('copy', $event)"
              @delete="emit('delete', $event)"
            />
          </template>
        </template>
      </div>
    </div>

    <div class="thread-foot">
      <div class="thread-foot-inner">
        <AIChatInput
          v-model="draft"
          :loading="loading"
          @send="handleSend"
          @cancel="emit('cancel')"
        />
      </div>
    </div>
  </div>
</template>

<script setup>
import { nextTick, onActivated, onMounted, ref, watch } from 'vue'
import AIChatEmpty from './AIChatEmpty.vue'
import AIChatMessage from './AIChatMessage.vue'
import AIChatInput from './AIChatInput.vue'

const props = defineProps({
  messages: {
    type: Array,
    default: () => [],
  },
  loading: {
    type: Boolean,
    default: false,
  },
  userName: {
    type: String,
    default: '我',
  },
  userAvatar: {
    type: String,
    default: '',
  },
  traceDefaultOpen: {
    type: Boolean,
    default: false,
  },
})

const emit = defineEmits(['send', 'copy', 'delete', 'cancel'])

const bodyRef = ref(null)
const draft = ref('')

// 相邻两条消息间隔超过 90 秒才插入时间分隔（首条作为会话起点也显示一次）
const TIME_GAP_MS = 90 * 1000

const formatMsgTime = (ts) => {
  if (!ts) return ''
  const d = new Date(ts)
  const now = new Date()
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  const sameDay =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate()
  if (sameDay) return `${hh}:${mm}`
  const mo = String(d.getMonth() + 1).padStart(2, '0')
  const da = String(d.getDate()).padStart(2, '0')
  return `${mo}-${da} ${hh}:${mm}`
}

const showTimeSeparator = (idx) => {
  const list = props.messages
  const cur = list[idx]
  if (!cur?.createdAt) return false
  if (idx === 0) return true
  const prevTs = list[idx - 1]?.createdAt
  if (!prevTs) return true
  return cur.createdAt - prevTs > TIME_GAP_MS
}

const scrollToBottom = async () => {
  await nextTick()
  const el = bodyRef.value
  if (!el) return
  el.scrollTop = el.scrollHeight
  // 消息有 0.2s 的入场位移动画，且 Markdown 排版会在下一帧才最终定高，
  // 补一帧再贴一次底，避免停在「动画开始前」的高度而露出半条消息。
  requestAnimationFrame(() => {
    const box = bodyRef.value
    if (box) box.scrollTop = box.scrollHeight
  })
}

// 每次打开/回到对话面板都定位到最新一条。
// watch 只在消息数组「发生变化」时触发，而挂载那一刻 messages 已是最终值，
// 不会再变，所以首屏必须显式滚一次——这正是「打开对话框停在顶部」的成因。
onMounted(scrollToBottom)
// keepAlive 命中时由 onActivated 兜底（未命中则不会触发，无副作用）
onActivated(scrollToBottom)

watch(
  () => [props.messages.length, props.messages[props.messages.length - 1]?.loading],
  () => scrollToBottom(),
  { flush: 'post' },
)

// 切换会话：清空草稿并定位到最新一条
watch(
  () => props.messages,
  () => {
    draft.value = ''
    scrollToBottom()
  },
)

const handleSend = (text) => {
  emit('send', text)
  draft.value = ''
}

defineExpose({ scrollToBottom })
</script>

<style scoped>
.ai-thread {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.thread-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 18px 16px 8px;
}

/* 相邻消息间隔 > 90s 时插入的居中时间分隔 */
.thread-time {
  margin: 2px 0 16px;
  text-align: center;
}

.thread-time span {
  display: inline-block;
  padding: 2px 10px;
  border-radius: 999px;
  background: rgba(15, 23, 32, 0.045);
  font-size: 11px;
  color: rgba(79, 100, 112, 0.75);
}

.thread-foot {
  flex: 0 0 auto;
  padding: 10px 16px 16px;
  border-top: 1px solid rgba(27, 73, 88, 0.08);
  background: #ffffff;
}

@media (max-width: 767px) {
  .thread-body {
    padding: 14px 12px 6px;
  }
  .thread-foot {
    padding: 8px 12px 12px;
  }
}
</style>
