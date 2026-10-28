<template>
  <div class="ai-message" :class="isUser ? 'is-user' : 'is-ai'">
    <div class="msg-avatar" :class="isUser ? 'is-me' : 'is-ai'" aria-hidden="true">
      <!-- 用户头像：有头像且未加载失败时展示头像图，否则回落首字方块 -->
      <img
        v-if="isUser && userAvatar && !avatarImgFailed"
        class="avatar-img"
        :src="userAvatar"
        alt=""
        @error="avatarImgFailed = true"
      />
      <span v-else-if="isUser" class="avatar-text">{{ avatarChar }}</span>
      <RobotOutlined v-else class="avatar-icon" />
    </div>

    <div class="msg-main">
      <div class="msg-bubble">
        <!-- 流式回答：一旦有正文就边收边渲染，只在「还没吐字」时显示等待动画 -->
        <div v-if="message.loading && !message.text" class="typing-dots">
          <span class="dot dot-1" />
          <span class="dot dot-2" />
          <span class="dot dot-3" />
        </div>
        <template v-else>
          <!-- AI 消息：Markdown 渲染；用户消息：纯文本（不解析，防注入） -->
          <div
            v-if="!isUser && message.text"
            class="bubble-text md-render"
            v-html="renderedText"
          />
          <div v-else-if="message.text" class="bubble-text">
            <template v-for="(seg, idx) in textSegments" :key="idx">
              <span v-if="seg.type === 'text'">{{ seg.value }}</span>
              <br v-else />
            </template>
          </div>
        </template>
      </div>

      <template v-if="!message.loading">
        <AIToolTrace
          v-if="traceSteps.length"
          :steps="traceSteps"
          :default-open="traceDefaultOpen"
        />
        <AIChatCodeBlock
          v-if="showCodeBlock"
          :payload="message.sql"
        />
        <AIChatActionCard
          v-if="message.action"
          :payload="message.action"
        />
      </template>

      <div v-if="(!isUser && viaModel) || cancelled" class="msg-meta">
        <span v-if="viaModel" class="via-model">{{ viaModel }}</span>
        <span v-if="cancelled" class="msg-cancelled">已停止生成</span>
      </div>

      <!-- 消息操作：复制本条的正文；删除按「轮次」处理——删掉这条提问连同它的 AI 回答。
           默认低对比常驻，hover 整条消息或键盘聚焦时变实，兼顾可发现性与阅读安静。 -->
      <div v-if="showActions" class="msg-actions">
        <button
          type="button"
          class="msg-action-btn"
          title="复制这条消息"
          aria-label="复制这条消息"
          @click="emit('copy', message)"
        >
          <CopyOutlined />
        </button>
        <button
          type="button"
          class="msg-action-btn is-danger"
          :disabled="busy"
          :title="busy ? '回复生成中，暂不能删除' : '删除该轮对话'"
          aria-label="删除该轮对话"
          @click="emit('delete', message)"
        >
          <DeleteOutlined />
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { CopyOutlined, DeleteOutlined, RobotOutlined } from '@ant-design/icons-vue'
import { AIChatRole } from '../composables/useAIChat'
import { renderMarkdown } from '../composables/useMarkdown'
import AIToolTrace from './AIToolTrace.vue'
import AIChatCodeBlock from './AIChatCodeBlock.vue'
import AIChatActionCard from './AIChatActionCard.vue'

const props = defineProps({
  message: {
    type: Object,
    required: true,
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
  /** 是否正在等待/接收回复：生成期间禁用删除，避免删掉正在写入的那一轮 */
  busy: {
    type: Boolean,
    default: false,
  },
})

const emit = defineEmits(['copy', 'delete'])

const isUser = computed(() => props.message?.role === AIChatRole.USER)

// 还没吐字的占位消息没有可复制的内容，也不该被单独删除
const showActions = computed(() => !props.message?.loading)

const avatarChar = computed(() => String(props.userName || '我').trim().charAt(0) || '我')

// 头像图加载失败（链接失效/权限变更）时回落首字方块；头像地址变化时重试
const avatarImgFailed = ref(false)
watch(
  () => props.userAvatar,
  () => {
    avatarImgFailed.value = false
  },
)

const traceSteps = computed(() =>
  Array.isArray(props.message?.trace) ? props.message.trace : [],
)

const showCodeBlock = computed(() => {
  const type = props.message?.type
  return (type === 'sql' || type === 'action_card') && !!props.message?.sql
})

const renderedText = computed(() =>
  isUser.value ? '' : renderMarkdown(props.message?.text),
)

const textSegments = computed(() => {
  const text = props.message?.text || ''
  const result = []
  text.split('\n').forEach((line, idx) => {
    if (idx > 0) result.push({ type: 'br' })
    result.push({ type: 'text', value: line })
  })
  return result
})

const viaModel = computed(() => String(props.message?.viaModel || '').trim())

// 主动取消：正文保留模型已生成的部分，这里只补一个状态提示。
// 用中性的灰、不用告警色 —— 取消是用户的正常选择，不是出错。
const cancelled = computed(() => !!props.message?.cancelled)
</script>

<style scoped>
.ai-message {
  display: flex;
  gap: 12px;
  margin-bottom: 18px;
  animation: msgIn 0.2s ease-out;
}

@keyframes msgIn {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.ai-message.is-user {
  flex-direction: row-reverse;
}

.msg-avatar {
  flex: 0 0 32px;
  width: 32px;
  height: 32px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.msg-avatar.is-ai {
  background: linear-gradient(135deg, rgba(15, 118, 110, 0.14) 0%, rgba(109, 91, 214, 0.14) 100%);
  color: #0f766e;
}

.msg-avatar.is-me {
  background: linear-gradient(135deg, #14313d 0%, #1d4756 100%);
  color: #ffffff;
}

.avatar-icon {
  font-size: 16px;
}

.avatar-text {
  font-size: 12px;
  font-weight: 600;
}

/* 头像图与首字方块同尺寸同圆角，切换时视觉一致 */
.avatar-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  border-radius: 10px;
  display: block;
}

.msg-main {
  min-width: 0;
  max-width: min(680px, calc(100% - 52px));
}

.msg-bubble {
  display: inline-block;
  max-width: 100%;
  padding: 11px 14px;
  border-radius: 14px;
  font-size: 13.5px;
  line-height: 1.75;
  word-break: break-word;
}

.ai-message.is-ai .msg-bubble {
  background: #ffffff;
  border: 1px solid rgba(27, 73, 88, 0.08);
  box-shadow: 0 4px 14px rgba(17, 55, 69, 0.04);
  color: #14313d;
  border-top-left-radius: 6px;
}

.ai-message.is-user .msg-bubble {
  background: linear-gradient(135deg, #0f766e 0%, #0d6b63 100%);
  color: #ffffff;
  border-top-right-radius: 6px;
  box-shadow: 0 6px 18px rgba(15, 118, 110, 0.2);
}

.ai-message.is-user .msg-main {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
}

.bubble-text {
  white-space: pre-wrap;
}

/* ---------------- Markdown 渲染排版 ---------------- */
/* v-html 内容不在 scoped 作用域内，需 :deep() 穿透 */
.md-render {
  white-space: normal;
}

.md-render :deep(p) {
  margin: 0 0 8px;
}

.md-render :deep(> :last-child) {
  margin-bottom: 0;
}

.md-render :deep(h1),
.md-render :deep(h2),
.md-render :deep(h3),
.md-render :deep(h4),
.md-render :deep(h5),
.md-render :deep(h6) {
  margin: 14px 0 8px;
  font-weight: 600;
  line-height: 1.4;
  color: #14313d;
}

.md-render :deep(h1) {
  font-size: 17px;
}

.md-render :deep(h2) {
  font-size: 15.5px;
}

.md-render :deep(h3) {
  font-size: 14.5px;
}

.md-render :deep(h4),
.md-render :deep(h5),
.md-render :deep(h6) {
  font-size: 13.5px;
}

.md-render :deep(ul),
.md-render :deep(ol) {
  margin: 0 0 8px;
  padding-left: 20px;
}

.md-render :deep(li) {
  margin: 3px 0;
}

.md-render :deep(a) {
  color: #0f766e;
  text-decoration: none;
  border-bottom: 1px solid rgba(15, 118, 110, 0.35);
}

.md-render :deep(a:hover) {
  border-bottom-color: #0f766e;
}

.md-render :deep(strong) {
  font-weight: 600;
  color: #0c2b33;
}

.md-render :deep(blockquote) {
  margin: 8px 0;
  padding: 6px 12px;
  border-left: 3px solid rgba(15, 118, 110, 0.35);
  background: rgba(15, 118, 110, 0.04);
  border-radius: 0 8px 8px 0;
  color: rgba(20, 49, 61, 0.82);
}

.md-render :deep(blockquote p) {
  margin: 0;
}

.md-render :deep(code) {
  padding: 1.5px 6px;
  border-radius: 6px;
  background: rgba(15, 118, 110, 0.08);
  color: #0b5d56;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  font-size: 12px;
}

.md-render :deep(hr) {
  margin: 12px 0;
  border: none;
  border-top: 1px solid rgba(27, 73, 88, 0.12);
}

/* 表格：细边框 + 斑马纹，宽表在气泡内横向滚动 */
.md-render :deep(table) {
  margin: 8px 0;
  border-collapse: collapse;
  width: 100%;
  font-size: 12.5px;
  display: block;
  overflow-x: auto;
}

.md-render :deep(th),
.md-render :deep(td) {
  padding: 6px 10px;
  border: 1px solid rgba(27, 73, 88, 0.14);
  text-align: left;
  white-space: nowrap;
}

.md-render :deep(th) {
  background: rgba(15, 118, 110, 0.06);
  font-weight: 600;
  color: #0c2b33;
}

.md-render :deep(tr:nth-child(even) td) {
  background: rgba(27, 73, 88, 0.02);
}

/* Markdown 代码块（区别于行内 code）：深色底 + 高亮，复用全局 atom-one-dark 配色 */
.md-render :deep(pre.md-code) {
  margin: 8px 0;
  padding: 12px 14px;
  border-radius: 10px;
  background: #0b1e25;
  overflow-x: auto;
}

.md-render :deep(pre.md-code code) {
  display: block;
  padding: 0;
  background: transparent;
  color: #e6f4f1;
  font-size: 12.5px;
  line-height: 1.7;
  white-space: pre;
  word-break: break-all;
}

.msg-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
}

/* 复制 / 删除：贴在气泡下方，用户消息靠右、AI 消息靠左（跟随 .msg-main 的对齐方式）。
   默认完全隐藏，鼠标进入这条消息的气泡区域才出现 —— 阅读时不被图标干扰。

   为什么用 opacity + pointer-events 而不是 display/visibility：
     ① 保留占位，出现时不会有布局跳动；
     ② opacity:0 的元素仍然可以被 Tab 聚焦 —— 换成 visibility:hidden 的话按钮
        永远聚焦不到，键盘用户就直接失去这两个操作了。所以这里必须配 :focus-within。

   隐藏时关掉 pointer-events：不可见的按钮不该还留着一个能点到的「盲区」。 */
.msg-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  margin-top: 4px;
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.15s ease;
}

/* 悬停范围取 .msg-main（气泡 + 它下方的操作条），而不是整行 .ai-message：
   整行是 flex 容器、横向撑满，鼠标落在气泡外侧的空白处也会触发，观感上「太灵敏」。
   取 .msg-main 还有个必要作用 —— 鼠标从气泡往下移到图标上时仍在范围内，不会闪烁消失。 */
.msg-main:hover .msg-actions,
.msg-main:focus-within .msg-actions {
  opacity: 1;
  pointer-events: auto;
}

/* 触屏设备没有 hover 指针：按上面这套规则，图标会永远不出现、功能直接不可用。
   因此退回常驻（半透明即可，不抢阅读）。 */
@media (hover: none) {
  .msg-actions {
    opacity: 0.6;
    pointer-events: auto;
  }
}

.msg-action-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: none;
  border-radius: 7px;
  background: transparent;
  color: rgba(79, 100, 112, 0.95);
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;
}

.msg-action-btn:hover:not(:disabled) {
  background: rgba(15, 118, 110, 0.08);
  color: #0f766e;
}

.msg-action-btn.is-danger:hover:not(:disabled) {
  background: rgba(220, 38, 38, 0.08);
  color: #dc2626;
}

.msg-action-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.via-model {
  display: inline-block;
  padding: 1px 7px;
  border-radius: 999px;
  background: rgba(15, 118, 110, 0.08);
  color: #0f766e;
  font-size: 10.5px;
}

.msg-cancelled {
  display: inline-block;
  padding: 1px 7px;
  border-radius: 999px;
  background: rgba(79, 100, 112, 0.09);
  color: rgba(79, 100, 112, 0.9);
  font-size: 10.5px;
}

.typing-dots {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 0;
}

.typing-dots .dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #0f766e;
  opacity: 0.5;
  animation: dotBounce 1.2s infinite ease-in-out;
}

.typing-dots .dot-1 {
  animation-delay: -0.32s;
}

.typing-dots .dot-2 {
  animation-delay: -0.16s;
}

@keyframes dotBounce {
  0%,
  80%,
  100% {
    transform: scale(0.6);
    opacity: 0.4;
  }
  40% {
    transform: scale(1);
    opacity: 0.9;
  }
}

@media (max-width: 767px) {
  .ai-message {
    gap: 8px;
    margin-bottom: 14px;
  }
  .msg-main {
    max-width: calc(100% - 44px);
  }
}

@media (max-width: 768px) {
  /* 消息操作按钮原本 24x24 */
  .msg-action-btn {
    width: 32px;
    height: 32px;
  }
}
</style>
