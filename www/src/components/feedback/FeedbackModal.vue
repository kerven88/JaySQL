<!--
  反馈弹窗
  - 问题类型（必填，下拉）/ 意见描述（必填 ≤2000 字）/ 图片上传（选填，最多 5 张）/ 联系方式（选填）
  - 提交按钮：仅当「类型 + 描述」均非空才可点（初始置灰）
  - 联系方式若填写，则在提交时做手机号/邮箱宽松校验并标错
  - 提交成功切换成功态，完成后关闭并重置表单
-->
<template>
  <a-modal
    :open="open"
    title="反馈"
    :width="560"
    :mask-closable="true"
    :keyboard="true"
    :footer="null"
    @cancel="handleCancel"
  >
    <!-- 表单态 -->
    <div v-if="!success" class="fb-body">
      <div class="f-item">
        <div class="f-label">问题类型<span class="f-req">*</span></div>
        <a-select
          v-model:value="form.category"
          class="f-select"
          placeholder="请选择问题类型"
          :options="categoryOptions"
        />
      </div>

      <div class="f-item">
        <div class="f-label">意见描述<span class="f-req">*</span></div>
        <a-textarea
          v-model:value="form.content"
          :maxlength="2000"
          :auto-size="{ minRows: 5, maxRows: 8 }"
          show-count
          placeholder="您可以在这里输入使用过程中所遇到的问题或者改进建议"
        />
      </div>

      <div class="f-item">
        <div class="f-label">图片上传</div>
        <div class="f-hint">可上传截图辅助说明，支持 jpg/png 等图片格式，最多 5 张</div>
        <div class="fb-upload">
          <div v-for="img in images" :key="img.id" class="fb-thumb">
            <img :src="img.url" alt="" />
            <button type="button" class="rm" aria-label="删除该图片" @click="removeImage(img.id)">✕</button>
          </div>
          <button
            type="button"
            class="fb-add"
            :class="{ 'is-full': images.length >= MAX_IMAGES }"
            aria-label="上传图片"
            @click="chooseFiles"
          >
            <span class="plus">+</span>
            <span class="up-count">{{ images.length }}/{{ MAX_IMAGES }}</span>
          </button>
        </div>
        <input ref="fileInput" type="file" accept="image/*" multiple hidden @change="onFilesPicked" />
        <div class="f-tip" :class="{ show: !!imgTip }">{{ imgTip }}</div>
      </div>

      <div class="f-item" :class="{ 'has-error': contactError }">
        <div class="f-label">联系方式</div>
        <div class="f-hint">如您需要我们与您联系，可留下您的联系方式，手机号或邮箱</div>
        <a-input v-model:value="form.contact" placeholder="请输入您的邮箱/联系电话" />
        <div class="f-err">请输入正确的手机号或邮箱</div>
      </div>

      <div class="fb-foot">
        <a-button @click="handleCancel">取消</a-button>
        <a-button type="primary" :disabled="!canSubmit" :loading="submitting" @click="handleSubmit">提交</a-button>
      </div>
    </div>

    <!-- 成功态 -->
    <div v-else class="fb-success">
      <div class="ok-ico">
        <svg viewBox="0 0 24 24"><path d="M4 12.5l5.5 5.5L20 7" /></svg>
      </div>
      <div class="ok-t">提交成功</div>
      <div class="ok-d">感谢您的反馈，我们会尽快跟进处理</div>
      <a-button class="ok-btn" @click="handleCancel">完成</a-button>
    </div>
  </a-modal>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { message } from 'ant-design-vue'
import { submitFeedbackApi } from '@/api/feedback'

defineOptions({ name: 'FeedbackModal' })

const props = defineProps({
  open: { type: Boolean, default: false },
})
const emit = defineEmits(['update:open', 'submitted'])

const MAX_IMAGES = 5
const route = useRoute()

const categoryOptions = [
  { label: 'AI助手', value: 'ai_assistant' },
  { label: 'SQL工作台', value: 'sql_workbench' },
  { label: '备份恢复', value: 'backup_restore' },
  { label: '性能分析', value: 'performance' },
  { label: '安全审计', value: 'security_audit' },
  { label: '系统管理', value: 'system_admin' },
  { label: '其他', value: 'other' },
]

const form = reactive({ category: undefined, content: '', contact: '' })
const images = ref([]) // [{ id, file, url }]
const success = ref(false)
const submitting = ref(false)
const attempted = ref(false)
const imgTip = ref('')
const fileInput = ref(null)

let uid = 0
let tipTimer = null

const canSubmit = computed(() => !!form.category && !!form.content.trim())

const validContact = (v) => {
  if (!v) return true // 选填
  return /^1[3-9]\d{9}$/.test(v) || /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v)
}
const contactError = computed(() => attempted.value && !validContact(form.contact.trim()))

const showImgTip = (text) => {
  imgTip.value = text
  if (tipTimer) clearTimeout(tipTimer)
  tipTimer = setTimeout(() => {
    imgTip.value = ''
  }, 3200)
}

const chooseFiles = () => {
  if (images.value.length >= MAX_IMAGES) {
    showImgTip(`最多上传 ${MAX_IMAGES} 张图片，请先删除已上传的图片`)
    return
  }
  fileInput.value?.click()
}

const onFilesPicked = (e) => {
  const list = Array.from(e.target?.files || [])
  if (fileInput.value) fileInput.value.value = '' // 允许重复选择同一文件
  if (!list.length) return

  const incoming = list.filter((f) => !f.type || f.type.indexOf('image/') === 0)
  if (!incoming.length) {
    showImgTip('仅支持上传图片文件')
    return
  }
  const room = MAX_IMAGES - images.value.length
  if (room <= 0) {
    showImgTip(`最多上传 ${MAX_IMAGES} 张图片，请先删除已上传的图片`)
    return
  }
  const accepted = incoming.slice(0, room)
  const overflow = incoming.length - accepted.length
  accepted.forEach((file) => {
    uid += 1
    images.value.push({ id: uid, file, url: URL.createObjectURL(file) })
  })
  if (overflow > 0) {
    showImgTip(`最多上传 ${MAX_IMAGES} 张图片，已自动忽略多余的 ${overflow} 张`)
  }
}

const removeImage = (id) => {
  const idx = images.value.findIndex((i) => i.id === id)
  if (idx === -1) return
  try {
    URL.revokeObjectURL(images.value[idx].url)
  } catch (_) {}
  images.value.splice(idx, 1)
  imgTip.value = ''
}

const resetForm = () => {
  form.category = undefined
  form.content = ''
  form.contact = ''
  attempted.value = false
  imgTip.value = ''
  images.value.forEach((i) => {
    try {
      URL.revokeObjectURL(i.url)
    } catch (_) {}
  })
  images.value = []
  success.value = false
  submitting.value = false
}

const handleCancel = () => {
  emit('update:open', false)
}

const handleSubmit = async () => {
  if (!canSubmit.value) return
  if (!validContact(form.contact.trim())) {
    attempted.value = true
    return
  }
  submitting.value = true
  try {
    const fd = new FormData()
    fd.append('category', form.category)
    fd.append('content', form.content.trim())
    fd.append('contact', form.contact.trim())
    fd.append('page_path', route?.fullPath || '')
    fd.append('user_agent', (navigator?.userAgent || '').slice(0, 512))
    images.value.forEach((img) => fd.append('images', img.file, img.file.name))

    const res = await submitFeedbackApi(fd)
    if (res?.code === '0000') {
      success.value = true
      emit('submitted', res?.data)
    } else {
      message.error(res?.message || '提交失败，请稍后重试')
    }
  } catch (err) {
    message.error(err?.message || err?.data?.message || '提交失败，请稍后重试')
  } finally {
    submitting.value = false
  }
}

// 弹窗关闭后重置（成功态停留时不清空，等关闭再清）
watch(
  () => props.open,
  (val) => {
    if (!val) {
      setTimeout(resetForm, 200)
    }
  },
)
</script>

<style scoped>
.fb-body {
  padding-top: 4px;
}
.f-item {
  margin-bottom: 18px;
}
.f-label {
  font-size: 14px;
  font-weight: 600;
  color: var(--gi-color-text-primary, #0f1720);
  display: flex;
  align-items: center;
  gap: 4px;
  margin-bottom: 8px;
}
.f-req {
  color: var(--gi-color-error, #ff4d4f);
  font-weight: 400;
}
.f-select {
  width: 100%;
}
.f-hint {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #4f6470);
  margin-bottom: 8px;
}

/* 图片上传 */
.fb-upload {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}
.fb-thumb {
  position: relative;
  width: 74px;
  height: 74px;
  border-radius: var(--gi-radius-md, 8px);
  overflow: hidden;
  border: 1px solid var(--gi-color-border, #d9e1e6);
  background: #eef3f4;
}
.fb-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.fb-thumb .rm {
  position: absolute;
  top: 3px;
  right: 3px;
  width: 18px;
  height: 18px;
  border: none;
  border-radius: 50%;
  background: rgba(15, 23, 32, 0.6);
  color: #fff;
  font-size: 11px;
  line-height: 1;
  cursor: pointer;
  padding: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}
.fb-thumb .rm:hover {
  background: rgba(15, 23, 32, 0.85);
}
.fb-add {
  width: 74px;
  height: 74px;
  border-radius: var(--gi-radius-md, 8px);
  cursor: pointer;
  padding: 0;
  border: 1px dashed #bfccd3;
  background: #fbfcfd;
  font-family: inherit;
  color: #8b9aa3;
  font-size: 11px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 3px;
  transition: border-color 0.15s ease, color 0.15s ease, background 0.15s ease;
}
.fb-add:hover {
  border-color: var(--gi-color-primary, #0f766e);
  color: var(--gi-color-primary, #0f766e);
  background: rgba(15, 118, 110, 0.04);
}
.fb-add .plus {
  font-size: 20px;
  line-height: 1;
  font-weight: 300;
}
.fb-add.is-full {
  cursor: not-allowed;
  color: #c3d0d7;
  border-color: #dde5e9;
  background: #f7f9fa;
}
.fb-add.is-full:hover {
  border-color: #dde5e9;
  color: #c3d0d7;
  background: #f7f9fa;
}
.f-tip {
  display: none;
  font-size: 12px;
  color: var(--gi-color-error, #ff4d4f);
  margin-top: 8px;
}
.f-tip.show {
  display: block;
}

.f-err {
  display: none;
  font-size: 12px;
  color: var(--gi-color-error, #ff4d4f);
  margin-top: 7px;
}
.f-item.has-error .f-err {
  display: block;
}

.fb-foot {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding-top: 6px;
  border-top: 1px solid rgba(217, 225, 230, 0.6);
  margin-top: 4px;
  padding-top: 16px;
}

/* 成功态 */
.fb-success {
  text-align: center;
  padding: 26px 0 10px;
}
.ok-ico {
  width: 62px;
  height: 62px;
  margin: 0 auto;
  border-radius: 50%;
  background: rgba(15, 118, 110, 0.1);
  display: flex;
  align-items: center;
  justify-content: center;
}
.ok-ico svg {
  width: 32px;
  height: 32px;
}
.ok-ico path {
  stroke: var(--gi-color-primary, #0f766e);
  stroke-width: 3;
  fill: none;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-dasharray: 40;
  stroke-dashoffset: 40;
  animation: fb-draw 0.45s ease 0.1s forwards;
}
@keyframes fb-draw {
  to {
    stroke-dashoffset: 0;
  }
}
.ok-t {
  font-size: 17px;
  font-weight: 600;
  margin-top: 16px;
}
.ok-d {
  font-size: 13px;
  color: var(--gi-color-text-tertiary, #4f6470);
  margin-top: 8px;
}
.ok-btn {
  margin-top: 22px;
}

@media (max-width: 768px) {
  /* 删除图片按钮原本 18x18 */
  .fb-thumb .rm {
    width: 28px;
    height: 28px;
    font-size: 14px;
  }

  .fb-foot {
    flex-direction: column-reverse;
  }

  .fb-foot :deep(.ant-btn) {
    width: 100%;
  }
}
</style>
