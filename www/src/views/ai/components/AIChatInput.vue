<template>
  <div class="chat-input-row">
    <div class="input-wrap" :class="{ 'is-focus': focusState }">
      <textarea
        ref="inputRef"
        v-model="localText"
        class="chat-textarea"
        :placeholder="placeholder"
        rows="1"
        :disabled="loading"
        @focus="focusState = true"
        @blur="focusState = false"
        @keydown="handleKeyDown"
        @input="autoResize"
      />
    </div>
    <a-button
      v-if="loading"
      type="primary"
      class="send-btn cancel-btn"
      title="终止本次生成"
      @click="handleCancel"
    >
      取消
    </a-button>
    <a-button
      v-else
      type="primary"
      class="send-btn"
      :disabled="!canSend"
      @click="handleSend"
    >
      发送
    </a-button>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, ref, watch } from 'vue'

const props = defineProps({
  modelValue: {
    type: String,
    default: '',
  },
  loading: {
    type: Boolean,
    default: false,
  },
  placeholder: {
    type: String,
    default: '用一句话描述你要做的事，例如：帮我检查昨晚备份有没有失败',
  },
})

const emit = defineEmits(['update:modelValue', 'send', 'cancel'])

const inputRef = ref(null)
const localText = ref(props.modelValue || '')
const focusState = ref(false)

watch(
  () => props.modelValue,
  (v) => {
    if (v !== localText.value) localText.value = v ?? ''
  },
)

// 外部（空状态能力卡）回填文本时也要重算高度
watch(localText, (v) => {
  emit('update:modelValue', v)
  nextTick(autoResize)
})

// 生成中按钮被「取消」占用，这里只需判断有没有可发送的内容
const canSend = computed(() => !!localText.value.trim())

const autoResize = async () => {
  await nextTick()
  const el = inputRef.value
  if (!el) return
  el.style.height = 'auto'
  const nextHeight = Math.min(el.scrollHeight, 120)
  el.style.height = nextHeight + 'px'
  // 未到最大高度时不出现滚动条（避免取整差 1px 常驻一根滚动条）
  el.style.overflowY = el.scrollHeight > 120 ? 'auto' : 'hidden'
}

const handleSend = () => {
  const text = localText.value.trim()
  if (!text || props.loading) return
  emit('send', text)
  nextTick(autoResize)
}

const handleCancel = () => emit('cancel')

const handleKeyDown = (e) => {
  if (!e) return
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    handleSend()
  }
}

onMounted(() => autoResize())

defineExpose({ focus: () => inputRef.value?.focus?.() })
</script>

<style scoped>
.chat-input-row {
  display: flex;
  align-items: flex-end;
  gap: 10px;
}

.input-wrap {
  position: relative;
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  padding: 0 14px;
  border-radius: 14px;
  background: #ffffff;
  border: 1px solid rgba(27, 73, 88, 0.12);
  transition: border-color 0.2s ease, box-shadow 0.2s ease, background 0.2s ease;
}

.input-wrap.is-focus {
  border-color: #0f766e;
  box-shadow: 0 0 0 3px rgba(15, 118, 110, 0.10);
}

.input-icon {
  position: absolute;
  left: 14px;
  top: 50%;
  transform: translateY(-50%);
  font-size: 17px;
  color: #0f766e;
  opacity: 0.75;
  pointer-events: none;
}

.chat-textarea {
  width: 100%;
  min-height: 24px;
  max-height: 120px;
  padding: 13px 0;
  border: none;
  outline: none;
  resize: none;
  overflow-y: hidden;
  background: transparent;
  font-size: 14px;
  line-height: 1.6;
  color: #14313d;
  font-family: inherit;
}

.chat-textarea::placeholder {
  color: rgba(79, 100, 112, 0.62);
}

.chat-textarea:disabled {
  cursor: not-allowed;
  opacity: 0.7;
}

.send-btn {
  flex-shrink: 0;
  height: 44px;
  min-width: 84px;
  padding: 0 20px;
  border-radius: 14px;
  font-weight: 600;
  font-size: 14px;
  color: #ffffff;
  background: linear-gradient(135deg, #0f766e 0%, #0d6b63 100%);
  border: none;
  box-shadow: 0 6px 18px rgba(15, 118, 110, 0.22);
  transition: transform 0.15s ease, box-shadow 0.15s ease, opacity 0.15s ease, color 0.15s ease;
}

.send-btn :deep(.ant-btn-loading-icon + span),
.send-btn :deep(span) {
  color: #ffffff;
}

.send-btn:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 10px 22px rgba(15, 118, 110, 0.28);
}

.send-btn:disabled {
  background: linear-gradient(135deg, #0f766e 0%, #0d6b63 100%);
  color: rgba(255, 255, 255, 0.85);
  opacity: 0.6;
  box-shadow: none;
}

.send-btn:disabled :deep(span) {
  color: rgba(255, 255, 255, 0.85);
}

/* 生成中：同一颗按钮切换为「取消」。
   刻意保留与「发送」完全相同的尺寸与圆角（按钮位置不跳，避免误触），
   只把底色换成告警色系，让「这是中断操作而不是再发一次」一眼可辨。
   选择器带上 .send-btn 是为了跟上面的绿色悬停规则对齐特异性，
   否则绿色阴影会盖在这颗红色按钮上。 */
.send-btn.cancel-btn {
  background: linear-gradient(135deg, #ef4444 0%, #dc2626 100%);
  box-shadow: 0 6px 18px rgba(220, 38, 38, 0.22);
}

.send-btn.cancel-btn:hover:not(:disabled) {
  box-shadow: 0 10px 22px rgba(220, 38, 38, 0.3);
}

@media (max-width: 767px) {
  .chat-input-row {
    flex-direction: column;
    align-items: stretch;
  }
  .send-btn {
    width: 100%;
  }
}
</style>
