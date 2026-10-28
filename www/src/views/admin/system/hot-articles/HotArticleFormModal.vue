<template>
  <a-modal :open="props.open" :title="props.title" :width="620" @cancel="handleCancel">
    <template #footer>
      <a-button @click="handleCancel">取消</a-button>
      <a-button type="primary" :loading="uiState.loading" :disabled="!props.canManage" @click="onSubmit">确定</a-button>
    </template>

    <a-form ref="formRef" :model="formData" :rules="rules" :disabled="!props.canManage" layout="vertical" class="modal-form">
      <a-form-item label="标题" name="title" has-feedback>
        <a-input
          v-model:value="formData.title"
          placeholder="请输入文章标题"
          :maxlength="128"
          show-count
          allow-clear
        />
      </a-form-item>

      <a-row :gutter="16">
        <a-col :span="12">
          <a-form-item label="来源" name="source">
            <a-input v-model:value="formData.source" placeholder="默认「程序猿读历史」" :maxlength="64" allow-clear />
          </a-form-item>
        </a-col>
        <a-col :span="12">
          <a-form-item label="标签" name="tag">
            <a-auto-complete
              v-model:value="formData.tag"
              class="hot-tag-select"
              placeholder="选填，可从预设中选或自行填写"
              :options="tagOptions"
              allow-clear
            />
          </a-form-item>
        </a-col>
      </a-row>

      <a-row :gutter="16">
        <a-col :span="12">
          <a-form-item label="热度（阅读量）" name="heat">
            <a-input-number
              v-model:value="formData.heat"
              class="hot-number-input"
              placeholder="选填，如 2100 展示为 2.1k"
              :min="0"
              :max="100000000"
              :precision="0"
            />
          </a-form-item>
        </a-col>
        <a-col :span="12">
          <a-form-item label="排序值" name="sort_order">
            <a-input-number
              v-model:value="formData.sort_order"
              class="hot-number-input"
              placeholder="选填，留空自动排到末尾"
              :min="0"
              :precision="0"
            />
          </a-form-item>
        </a-col>
      </a-row>

      <a-form-item label="链接" name="url">
        <a-input
          v-model:value="formData.url"
          placeholder="选填，留空则首页仅展示、不可点击（需以 http:// 或 https:// 开头）"
          :maxlength="512"
          allow-clear
        />
      </a-form-item>

      <a-form-item label="启用状态" name="enabled">
        <a-switch v-model:checked="formData.enabled" checked-children="启用" un-checked-children="停用" />
        <span class="hot-field-tip">停用后不参与首页展示，记录保留</span>
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

// 预设标签（与后端 models.TagOptions() 一致）；auto-complete 允许自行填写其它值
const tagOptions = ['MySQL', 'PostgreSQL', '备份恢复', '性能', '架构', '其他'].map((value) => ({ value }))

const rules = computed(() => ({
  title: [
    {
      required: props.canManage,
      message: '请输入标题',
      trigger: ['blur', 'change'],
    },
    {
      validator: (_rule, value) => {
        if (!props.canManage) return Promise.resolve()
        const v = (value ?? '').trim()
        if (!v) return Promise.reject('标题不能为空')
        if (v.length > 128) return Promise.reject('标题长度不能超过 128 个字符')
        return Promise.resolve()
      },
      trigger: ['blur', 'change'],
    },
  ],
  url: [
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
.hot-tag-select,
.hot-number-input {
  width: 100%;
}

.hot-field-tip {
  margin-left: 10px;
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
}
</style>
