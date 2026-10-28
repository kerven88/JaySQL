<template>
  <a-modal
    :open="props.open"
    title="收藏SQL语句"
    :width="modalWidth"
    :centered="true"
    :wrap-class-name="['favorite-edit-modal-wrap']"
    :mask-closable="false"
    @cancel="handleCancel"
  >
    <template #footer>
      <a-button @click="handleCancel">取消</a-button>
      <a-button type="primary" :loading="uiState.loading" @click="onSubmit">确定</a-button>
    </template>

    <a-form
      ref="formRef"
      :model="formData"
      :rules="rules"
      :label-col="{ span: 4 }"
      :wrapper-col="{ span: 20 }"
      class="modal-form"
    >
      <a-form-item label="标题" name="title">
        <a-input v-model:value="formData.title" placeholder="为这条SQL起个名字" allow-clear />
      </a-form-item>
      <a-form-item label="SQL内容" name="sqltext">
        <a-textarea
          v-model:value="formData.sqltext"
          :auto-size="{ minRows: 8, maxRows: textareaMaxRows }"
          placeholder="请输入SQL"
          allow-clear
          class="favorite-edit-sql-textarea"
        />
      </a-form-item>
    </a-form>
  </a-modal>
</template>

<script setup>
import { computed, ref, reactive } from 'vue'

const props = defineProps({
  open: Boolean,
  title: String,
})
const emit = defineEmits(['update:open', 'submit'])

const formData = defineModel('modelValue', {
  type: Object,
  required: true,
})

const formRef = ref()

const uiState = reactive({
  loading: false,
})

const modalWidth = computed(() => {
  if (typeof window === 'undefined') return 'min(88vw, 960px)'
  const w = window.innerWidth || 1440
  if (w >= 1680) return 'min(70vw, 1200px)'
  if (w >= 1280) return 'min(78vw, 1000px)'
  if (w >= 992) return 'min(84vw, 880px)'
  return 'min(94vw, 760px)'
})

const textareaMaxRows = computed(() => {
  if (typeof window === 'undefined') return 14
  const h = window.innerHeight || 900
  if (h >= 1080) return 20
  if (h >= 900) return 16
  if (h >= 720) return 12
  return 10
})

const rules = {
  title: [
    {
      required: true,
      message: '请输入SQL标题',
      trigger: 'blur',
    },
    {
      min: 3,
      max: 100,
      message: '标题长度应为3-100个字符',
      trigger: 'blur',
    },
  ],
  sqltext: [
    {
      required: true,
      message: '请输入SQL语句',
      trigger: 'blur',
    },
    {
      min: 10,
      message: 'SQL语句至少需要10个字符',
      trigger: 'blur',
    },
  ],
}

const handleCancel = () => {
  emit('update:open', false)
  formRef.value?.resetFields()
}

const onSubmit = async () => {
  try {
    await formRef.value.validateFields()
    uiState.loading = true
    emit('submit', formData.value)
  } catch {
  } finally {
    uiState.loading = false
  }
}
</script>

<style scoped>
.modal-form {
  width: 100%;
}

.favorite-edit-sql-textarea {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace;
  font-size: 13px;
  line-height: 1.6;
}
</style>

<style>
.favorite-edit-modal-wrap .ant-modal-body {
  max-height: min(80vh, 860px);
  overflow-y: auto;
  overflow-x: hidden;
  padding: 20px 24px 16px;
}

.favorite-edit-modal-wrap .ant-modal-header {
  padding: 14px 24px;
}

.favorite-edit-modal-wrap .ant-modal-footer {
  padding: 12px 24px;
  border-top: 1px solid #f0f1f3;
  margin: 0;
}

.favorite-edit-modal-wrap .ant-modal-content {
  border-radius: 10px;
  overflow: hidden;
}
</style>
