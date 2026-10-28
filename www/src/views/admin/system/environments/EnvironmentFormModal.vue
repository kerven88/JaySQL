<template>
  <a-modal :open="props.open" :title="props.title" :width="560" @cancel="handleCancel">
    <template #footer>
      <a-button @click="handleCancel">取消</a-button>
      <a-button type="primary" :loading="uiState.loading" :disabled="!props.canManage" @click="onSubmit">确定</a-button>
    </template>

    <a-form ref="formRef" :model="formData" :rules="rules" :disabled="!props.canManage" layout="vertical" class="modal-form">
      <a-alert
        v-if="isDirtyStandard"
        type="warning"
        show-icon
        message="当前绑定的规范不可用"
        description="该规范已停用、无有效规则或不再被提交工单流程支持，请重新选择"
        class="standard-dirty-alert"
        :closable="false"
      />
      <a-row :gutter="16">
        <a-col :span="14">
          <a-form-item label="环境名" name="name" has-feedback>
            <a-input v-model:value="formData.name" placeholder="请输入环境名" allow-clear />
          </a-form-item>
        </a-col>
        <a-col :span="10">
          <a-form-item label="环境颜色" name="color">
            <div class="color-picker-field">
              <input v-model="formData.color" class="native-color-input" type="color" />
              <a-input v-model:value="formData.color" placeholder="#1677ff" maxlength="7" />
            </div>
            <div class="color-presets">
              <button
                v-for="item in colorPresets"
                :key="item.value"
                type="button"
                class="color-preset"
                :title="item.label"
                :style="{ backgroundColor: item.value }"
                @click="applyPresetColor(item.value)"
              />
            </div>
          </a-form-item>
        </a-col>
      </a-row>
      <a-row :gutter="16">
        <a-col :span="12">
          <a-form-item label="环境类型" name="env_type">
            <a-select
              v-model:value="formData.env_type"
              placeholder="请选择环境类型"
              :options="envTypeOptions"
              allow-clear
            />
            <div class="env-type-tip">
              {{ envTypeTip }}
            </div>
          </a-form-item>
        </a-col>
        <a-col :span="12">
          <a-form-item label="SQL 开发规范" name="sql_standard_id">
            <a-select
              v-model:value="formData.sql_standard_id"
              placeholder="请选择SQL开发规范"
              :options="sqlStandardOptions"
              :not-found-content="'暂无可用于提交工单的规范'"
              allow-clear
            />
            <div class="env-type-tip">仅显示已启用且被提交工单流程支持的规范；环境内所有实例统一继承该规范</div>
          </a-form-item>
        </a-col>
      </a-row>
    </a-form>
  </a-modal>
</template>

<script setup>
import { getSQLStandardsApi } from '@/api/admin'
import { computed, ref, reactive, watch } from 'vue'

// 定义props和emits
const props = defineProps({
  open: Boolean,
  title: String,
  canManage: {
    type: Boolean,
    default: true,
  },
  mode: {
    type: String,
    default: 'create',
  },
})
const emit = defineEmits(['update:open', 'submit'])

// 表单数据
const formData = defineModel('modelValue', {
  type: Object,
  required: true,
})

// 表单引用
const formRef = ref()

// 状态
const uiState = reactive({
  loading: false,
})

const colorPresets = [
  { label: '生产', value: '#ff4d4f' },
  { label: '测试', value: '#1677ff' },
  { label: '开发', value: '#52c41a' },
  { label: '预发', value: '#faad14' },
  { label: '离线', value: '#722ed1' },
]

// 环境类型与 SQL 开发规范：开发环境对应开发规范、生产环境对应生产规范，
// 规范按环境类型自动推荐，但允许在弹窗内手动选择；环境内所有实例统一跟随，实例不单独配置
const envTypeOptions = [
  { label: '开发环境', value: 'dev' },
  { label: '生产环境', value: 'prod' },
]

const envTypeTip = computed(() => {
  const map = {
    dev: '开发环境：自动推荐开发环境 SQL 规范',
    prod: '生产环境：自动推荐生产环境 SQL 规范',
  }
  return map[formData.value?.env_type] || '选择环境类型后将自动推荐对应类型的 SQL 规范'
})

const standards = ref([])

const loadStandards = async () => {
  const res = await getSQLStandardsApi({ is_page: false, page: 1, page_size: 200 }).catch(() => null)
  standards.value = Array.isArray(res?.data) ? res.data : []
}

// 仅显示「已启用」且「被提交工单流程支持」的规范
const supportedStandards = computed(() => {
  return (standards.value || []).filter((item) => item.enabled !== false && item.supported === true)
})

const supportedStandardIds = computed(() => {
  return new Set(supportedStandards.value.map((item) => item.id))
})

const envTypeTag = (envType) => {
  const map = { dev: '开发', prod: '生产', custom: '自定义' }
  return map[envType] || envType || ''
}

const sqlStandardOptions = computed(() => {
  return supportedStandards.value.map((item) => ({
    label: item.name + (item.env_type ? `（${envTypeTag(item.env_type)}）` : ''),
    value: item.id,
  }))
})

// 当前显示值是否属于历史脏数据（规范被停用、无有效规则，或已删除）
const isDirtyStandard = computed(() => {
  const currentId = formData.value?.sql_standard_id
  if (!currentId) return false
  return !supportedStandardIds.value.has(currentId)
})

// 按环境类型挑选默认规范：优先同类型、is_default、sort_order 靠前、id 靠前
const resolveDefaultStandardForEnv = (envType) => {
  if (!envType) return undefined
  const candidates = supportedStandards.value.filter((item) => item.env_type === envType)
  if (!candidates.length) return supportedStandards.value[0]?.id
  const matched = [...candidates].sort((a, b) => {
    if (!!a.is_default !== !!b.is_default) return a.is_default ? -1 : 1
    if ((a.sort_order || 0) !== (b.sort_order || 0)) return (a.sort_order || 0) - (b.sort_order || 0)
    return a.id - b.id
  })[0]
  return matched?.id
}

const ensureDefaultStandard = () => {
  const currentId = formData.value?.sql_standard_id
  if (currentId && supportedStandardIds.value.has(currentId)) {
    return
  }
  formData.value.sql_standard_id = resolveDefaultStandardForEnv(formData.value?.env_type)
}

// 弹窗打开时拉取规范列表；加载完成后，若当前值不可用则自动回填默认规范
watch(
  () => props.open,
  (open) => {
    if (open) loadStandards()
  },
)

watch(
  () => supportedStandards.value.length,
  () => {
    ensureDefaultStandard()
  },
)

watch(
  () => formData.value?.env_type,
  () => {
    ensureDefaultStandard()
  },
)

// 表单校验规则
const rules = computed(() => ({
  name: [
    {
      required: props.canManage,
      message: '请输入环境名',
      trigger: ['blur', 'change'],
    },
    {
      validator: (_rule, value) => {
        if (!props.canManage) return Promise.resolve()
        const v = (value ?? '').trim()
        if (!v) return Promise.reject('环境名不能为空')
        if (v.length < 2 || v.length > 32) return Promise.reject('长度需为 2~32 个字符')
        if (!/^[a-zA-Z0-9\u4e00-\u9fa5_]+$/.test(v)) {
          return Promise.reject('仅可使用字母、数字、中文或下划线')
        }
        if (/^_/.test(v) || /_$/.test(v)) {
          return Promise.reject('不允许以下划线开头或结尾')
        }
        return Promise.resolve()
      },
      trigger: ['blur', 'change'],
    },
  ],
  color: [
    {
      validator: (_rule, value) => {
        if (!props.canManage) return Promise.resolve()
        const v = String(value || '').trim()
        if (!v) return Promise.reject('请选择环境颜色')
        if (!/^#[0-9a-fA-F]{6}$/.test(v)) {
          return Promise.reject('颜色格式应为 #RRGGBB')
        }
        return Promise.resolve()
      },
      trigger: ['blur', 'change'],
    },
  ],
  env_type: [
    {
      required: props.canManage && (props.mode === 'create' || !formData.value?.env_type),
      message: '请选择环境类型',
      trigger: ['blur', 'change'],
    },
  ],
  sql_standard_id: [
    {
      required: props.canManage,
      message: '请选择SQL开发规范',
      trigger: ['blur', 'change'],
    },
    {
      validator: (_rule, value) => {
        if (!props.canManage) return Promise.resolve()
        if (!value) return Promise.reject('请选择SQL开发规范')
        if (!supportedStandardIds.value.has(value)) {
          return Promise.reject('所选规范已停用或不被提交工单流程支持，请选择其他规范')
        }
        return Promise.resolve()
      },
      trigger: ['blur', 'change'],
    },
  ],
}))

const applyPresetColor = (value) => {
  formData.value.color = value
}

// 取消按钮
const handleCancel = () => {
  emit('update:open', false)
  formRef.value?.resetFields()
}

// 提交表单
const onSubmit = async () => {
  try {
    await formRef.value.validateFields()
    uiState.loading = true
    emit('submit', formData.value)
  } catch {
    // ignore
  } finally {
    uiState.loading = false
  }
}
</script>

<style scoped>
.color-picker-field {
  display: flex;
  align-items: center;
  gap: 12px;
}

.native-color-input {
  width: 52px;
  height: 36px;
  padding: 0;
  border: 1px solid #d9d9d9;
  border-radius: 8px;
  background: #fff;
  cursor: pointer;
}

.color-presets {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 10px;
}

.color-preset {
  width: 22px;
  height: 22px;
  border: 2px solid #fff;
  border-radius: 50%;
  box-shadow: 0 0 0 1px #d9d9d9;
  cursor: pointer;
}

.env-type-tip {
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
  line-height: 18px;
  margin-top: 4px;
}

.sql-standard-preview {
  min-height: 32px;
  display: flex;
  align-items: center;
}

.sql-standard-tag {
  display: inline-block;
  max-width: 100%;
  padding: 1px 8px;
  border: 1px solid color-mix(in srgb, var(--gi-color-primary, #0f766e) 35%, transparent);
  border-radius: 4px;
  background: color-mix(in srgb, var(--gi-color-primary, #0f766e) 10%, transparent);
  color: var(--gi-color-primary, #0f766e);
  font-size: 12px;
  line-height: 20px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sql-standard-placeholder {
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
}

.standard-dirty-alert {
  margin-bottom: 16px;
}

</style>
