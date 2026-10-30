<template>
  <section class="message-template-editor">
    <p class="template-help">为当前渠道设置告警消息正文；留空使用默认正文。支持标题、加粗、斜体、列表、链接和代码块。</p>
    <a-space wrap class="template-actions">
      <a-button size="small" :disabled="!defaultTemplate" @click="useExample">填入示例</a-button>
      <a-button size="small" @click="value = ''">恢复默认正文</a-button>
      <a-select v-model:value="trigger" size="small" aria-label="预览告警阶段" :options="triggerOptions" />
      <a-button size="small" type="primary" :loading="loading" @click="previewTemplate">预览</a-button>
    </a-space>
    <a-textarea ref="editor" v-model:value="value" :rows="8" :maxlength="16384"
      aria-label="Markdown 告警消息模板" placeholder="留空使用默认告警正文；点击变量可插入，例如 {{instance_name}}"
      @select="rememberSelection" @keyup="rememberSelection" @click="rememberSelection" />
    <p class="template-help">点击插入变量（样例预览不会发送消息）：</p>
    <a-space wrap :size="[4, 6]" class="template-variables">
      <a-button v-for="variable in variables" :key="variable.key" size="small"
        :title="`插入 {{${variable.key}}}`" @click="insertVariable(variable.key)">{{ variable.label }}</a-button>
    </a-space>
    <a-alert v-if="kind === 'telegram'" type="info" show-icon class="template-note"
      message="Telegram 使用适配后的富文本；表格、原始 HTML 和图片不作为富文本发送。单条消息最多 4096 字符。" />
    <a-alert v-if="previewError" type="error" show-icon class="template-note" :message="previewError" />
    <div v-if="preview" class="template-preview">
      <a-tabs size="small">
        <a-tab-pane key="formatted" tab="格式预览">
          <div class="template-markdown" v-html="previewHTML" />
        </a-tab-pane>
        <a-tab-pane key="plain" tab="纯文本">
          <pre>{{ preview.plain_text }}</pre>
        </a-tab-pane>
      </a-tabs>
    </div>
    <p v-else class="template-help">内容修改后点击“预览”，检查变量与排版。实际消息使用真实告警数据。</p>
  </section>
</template>

<script setup>
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import MarkdownIt from 'markdown-it'
import { previewNotifyMessageTemplateApi } from '@/api/monitor'

const props = defineProps({ kind: { type: String, default: '' } })
const value = defineModel({ type: String, default: '' })
const editor = ref(null)
const trigger = ref('firing')
const triggerOptions = [{ label: '触发告警', value: 'firing' }, { label: '恢复通知', value: 'recovered' }, { label: '认领通知', value: 'acknowledged' }]
const variables = ref([])
const defaultTemplate = ref('')
const preview = ref(null)
const previewError = ref('')
const loading = ref(false)
const selection = ref(null)
let requestVersion = 0
const md = new MarkdownIt({ html: false, breaks: true, linkify: true })
const defaultLinkOpen = md.renderer.rules.link_open || ((tokens, idx, options, env, self) => self.renderToken(tokens, idx, options))
md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  tokens[idx].attrSet('target', '_blank')
  tokens[idx].attrSet('rel', 'noopener noreferrer')
  return defaultLinkOpen(tokens, idx, options, env, self)
}
// Telegram HTML comes from the backend's allowlisted renderer, never from raw user HTML.
const previewHTML = computed(() => props.kind === 'telegram' ? preview.value?.telegram_html || '' : md.render(preview.value?.markdown || ''))
watch([value, trigger], () => { requestVersion++; preview.value = null; previewError.value = ''; loading.value = false })
function rememberSelection(event) { selection.value = [event.target.selectionStart, event.target.selectionEnd] }
async function insertVariable(key) {
  const text = value.value || ''
  const [start, end] = selection.value || [text.length, text.length]
  const token = `{{${key}}}`
  value.value = text.slice(0, start) + token + text.slice(end)
  selection.value = [start + token.length, start + token.length]
  await nextTick()
  editor.value?.focus()
  const element = editor.value?.$el
  const textarea = element?.tagName === 'TEXTAREA' ? element : element?.querySelector?.('textarea')
  textarea?.setSelectionRange(...selection.value)
}
function useExample() { value.value = defaultTemplate.value; selection.value = null }
async function previewTemplate() {
  const version = ++requestVersion
  loading.value = true
  previewError.value = ''
  try {
    const response = await previewNotifyMessageTemplateApi({ template: value.value || '', trigger: trigger.value })
    if (version !== requestVersion) return
    preview.value = response?.data || null
    variables.value = preview.value?.variables || []
    defaultTemplate.value = preview.value?.default_template || ''
  } catch {
    if (version === requestVersion) { preview.value = null; previewError.value = '预览失败，请检查变量格式或网络后重试。' }
  } finally {
    if (version === requestVersion) loading.value = false
  }
}
onMounted(async () => {
  // Load supported variables even if a saved template is invalid after an upgrade.
  try {
    const response = await previewNotifyMessageTemplateApi({ template: '', trigger: 'firing' })
    variables.value = response?.data?.variables || []
    defaultTemplate.value = response?.data?.default_template || ''
  } catch { /* Shared request handling displays the error; preview allows retry. */ }
  previewTemplate()
})
</script>

<style scoped>
.template-help { color: var(--gi-text-secondary, #595959); margin: 8px 0; font-size: 12px; }
.template-actions { margin-bottom: 10px; }
.template-variables { margin-bottom: 10px; }
.template-note { margin-top: 10px; }
.template-preview { margin-top: 12px; padding: 4px 12px 12px; background: #f7faf9; border: 1px solid #d9e5e2; border-radius: 8px; }
.template-preview pre { white-space: pre-wrap; overflow-wrap: anywhere; }
.template-markdown { overflow-wrap: anywhere; }
.template-markdown :deep(pre) { white-space: pre-wrap; padding: 10px; background: #edf2f0; }
.template-markdown :deep(img) { max-width: 100%; }
.template-markdown :deep(p) { margin: 8px 0; }
/* Telegram HTML uses newlines rather than HTML paragraphs. */
.template-markdown { white-space: pre-wrap; }
</style>
