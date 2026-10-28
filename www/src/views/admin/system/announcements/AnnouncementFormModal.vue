<template>
  <a-modal :open="props.open" :title="props.title" :width="620" @cancel="handleCancel">
    <template #footer>
      <a-button @click="handleCancel">取消</a-button>
      <a-button type="primary" :loading="uiState.loading" :disabled="!props.canManage" @click="onSubmit">确定</a-button>
    </template>

    <a-form ref="formRef" :model="formData" :rules="rules" :disabled="!props.canManage" layout="vertical" class="modal-form">
      <a-form-item label="公告标题" name="title" has-feedback>
        <a-input
          v-model:value="formData.title"
          placeholder="请输入公告标题"
          :maxlength="128"
          show-count
          allow-clear
        />
      </a-form-item>

      <a-form-item label="公告内容" name="content">
        <a-textarea
          v-model:value="formData.content"
          placeholder="请输入公告内容"
          :rows="4"
          :maxlength="2000"
          show-count
          allow-clear
        />
      </a-form-item>

      <a-form-item label="公告链接" name="link">
        <a-input v-model:value="formData.link" placeholder="选填，填写后公告可点击跳转（需以 http:// 或 https:// 开头）" allow-clear />
      </a-form-item>

      <a-row :gutter="16">
        <a-col :span="12">
          <a-form-item label="生效开始时间" name="start_at">
            <a-date-picker
              v-model:value="formData.start_at"
              class="ann-time-picker"
              placeholder="选填，留空表示不限"
              show-time
              format="YYYY-MM-DD HH:mm:ss"
              value-format="YYYY-MM-DD HH:mm:ss"
              allow-clear
            />
          </a-form-item>
        </a-col>
        <a-col :span="12">
          <a-form-item label="生效结束时间" name="end_at">
            <a-date-picker
              v-model:value="formData.end_at"
              class="ann-time-picker"
              placeholder="选填，留空表示不限"
              show-time
              format="YYYY-MM-DD HH:mm:ss"
              value-format="YYYY-MM-DD HH:mm:ss"
              allow-clear
            />
          </a-form-item>
        </a-col>
      </a-row>

      <a-form-item label="启用状态" name="enabled">
        <a-switch v-model:checked="formData.enabled" checked-children="启用" un-checked-children="停用" />
        <span class="ann-field-tip">停用后公告立即从头部隐藏，但不删除记录</span>
      </a-form-item>
    </a-form>
  </a-modal>
</template>

<script setup>
import { computed, reactive, ref } from 'vue'

const props = defineProps({
  open: Boolean,
  title: String,
  canManage: {
    type: Boolean,
    default: true,
  },
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

// 时间比较：后端落库格式是 'YYYY-MM-DD HH:mm:ss'，字典序即时间序，直接比字符串即可
const rules = computed(() => ({
  title: [
    {
      required: props.canManage,
      message: '请输入公告标题',
      trigger: ['blur', 'change'],
    },
    {
      validator: (_rule, value) => {
        if (!props.canManage) return Promise.resolve()
        const v = (value ?? '').trim()
        if (!v) return Promise.reject('公告标题不能为空')
        if (v.length > 128) return Promise.reject('标题长度不能超过 128 个字符')
        return Promise.resolve()
      },
      trigger: ['blur', 'change'],
    },
  ],
  content: [
    {
      required: props.canManage,
      message: '请输入公告内容',
      trigger: ['blur', 'change'],
    },
    {
      validator: (_rule, value) => {
        if (!props.canManage) return Promise.resolve()
        const v = (value ?? '').trim()
        if (!v) return Promise.reject('公告内容不能为空')
        if (v.length > 2000) return Promise.reject('公告内容长度不能超过 2000 个字符')
        return Promise.resolve()
      },
      trigger: ['blur', 'change'],
    },
  ],
  link: [
    {
      validator: (_rule, value) => {
        if (!props.canManage) return Promise.resolve()
        const v = (value ?? '').trim()
        if (!v) return Promise.resolve()
        if (!/^https?:\/\/\S+$/.test(v)) {
          return Promise.reject('链接需以 http:// 或 https:// 开头')
        }
        return Promise.resolve()
      },
      trigger: ['blur', 'change'],
    },
  ],
  end_at: [
    {
      validator: (_rule, value) => {
        if (!props.canManage) return Promise.resolve()
        const start = formData.value?.start_at
        const end = value
        if (start && end && String(end) <= String(start)) {
          return Promise.reject('结束时间必须晚于开始时间')
        }
        return Promise.resolve()
      },
      trigger: ['blur', 'change'],
    },
  ],
}))

const handleCancel = () => {
  emit('update:open', false)
  formRef.value?.clearValidate()
}

const onSubmit = async () => {
  try {
    await formRef.value.validateFields()
    uiState.loading = true
    emit('submit', formData.value)
  } catch {
    // 校验失败：保持弹窗打开，由表单自行展示错误
  } finally {
    uiState.loading = false
  }
}
</script>

<style scoped>
.ann-time-picker {
  width: 100%;
}

.ann-field-tip {
  margin-left: 10px;
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
}
</style>
