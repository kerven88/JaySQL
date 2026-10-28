<template>
  <div class="template-list">
    <div class="toolbar">
      <a-space>
        <a-button @click="loadTemplates">
          <template #icon><ReloadOutlined /></template>
          刷新
        </a-button>
        <a-button type="primary" @click="openCreate">
          <template #icon><PlusOutlined /></template>
          新增自定义厂商
        </a-button>
      </a-space>
    </div>

    <a-table
      :columns="columns"
      :data-source="templates"
      :loading="loading"
      :pagination="false"
      row-key="vendor"
      size="middle"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'vendor'">
          <div class="cell-name">
            <span class="name-text">{{ record.label }}</span>
            <a-tag v-if="record.is_builtin" color="blue" style="margin-left: 6px">内置</a-tag>
          </div>
          <div class="cell-sub-text mono">{{ record.vendor }}</div>
        </template>

        <template v-else-if="column.key === 'category'">
          <a-tag :color="categoryMeta(record.category).color">
            {{ categoryMeta(record.category).text }}
          </a-tag>
        </template>

        <template v-else-if="column.key === 'adapter'">
          <span class="mono">{{ record.adapter }}</span>
        </template>

        <template v-else-if="column.key === 'auth'">
          <div>{{ authModeLabel(record.auth_mode) }}</div>
          <div class="cell-sub-text">{{ record.auth_header_name || '—' }}</div>
        </template>

        <template v-else-if="column.key === 'fields'">
          <span class="cell-sub-text">
            必填 {{ asArray(record.required_fields).length }} 项 / 扩展 {{ asArray(record.extra_fields).length }} 项
          </span>
        </template>

        <template v-else-if="column.key === 'enabled'">
          <a-switch
            :checked="record.is_enabled"
            size="small"
            @change="(checked) => handleToggle(record, checked)"
          />
        </template>

        <template v-else-if="column.key === 'action'">
          <a-space size="small">
            <a-button type="link" size="small" @click="openEdit(record)">编辑</a-button>
            <a-button type="link" size="small" @click="openCopy(record)">复制为新模板</a-button>
            <a-popconfirm
              v-if="!record.is_builtin"
              title="确认删除该模板？"
              ok-text="删除"
              cancel-text="取消"
              @confirm="handleDelete(record)"
            >
              <a-button type="link" size="small" danger>删除</a-button>
            </a-popconfirm>
          </a-space>
        </template>
      </template>
    </a-table>

    <a-modal
      v-model:open="modalOpen"
      :title="modalTitle"
      width="760"
      :confirm-loading="saving"
      @ok="handleSubmit"
    >
      <a-alert
        v-if="editing?.is_builtin"
        type="warning"
        show-icon
        message="内置模板只允许调整名称、排序与启停"
        description="定义字段（端点、路径、字段结构）由平台内置定义维护，避免与代码定义冲突。如需修改定义，请「复制为新模板」。"
        style="margin-bottom: 16px"
      />

      <a-form layout="vertical" :model="form">
        <a-row :gutter="16">
          <a-col :span="12">
            <a-form-item label="显示名" required>
              <a-input v-model:value="form.label" :maxlength="64" />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="厂商标识" required>
              <a-input
                v-model:value="form.vendor"
                :maxlength="32"
                :disabled="isVendorLocked"
                placeholder="小写字母/数字/下划线，如 my_gateway"
              />
            </a-form-item>
          </a-col>
        </a-row>

        <template v-if="!editing?.is_builtin">
          <a-row :gutter="16">
            <a-col :span="12">
              <a-form-item label="分类">
                <a-select v-model:value="form.category" :options="categoryOptions" />
              </a-form-item>
            </a-col>
            <a-col :span="12">
              <a-form-item label="适配器" required>
                <a-select v-model:value="form.adapter" :options="adapterOptions" />
              </a-form-item>
            </a-col>
          </a-row>

          <a-form-item label="默认端点">
            <a-input v-model:value="form.default_base_url" placeholder="https://api.example.com/v1" />
          </a-form-item>

          <a-row :gutter="16">
            <a-col :span="12">
              <a-form-item label="对话路径">
                <a-input v-model:value="form.path_chat" placeholder="/chat/completions" />
              </a-form-item>
            </a-col>
            <a-col :span="12">
              <a-form-item label="模型列表路径">
                <a-input v-model:value="form.path_models" placeholder="/models" />
              </a-form-item>
            </a-col>
          </a-row>

          <a-row :gutter="16">
            <a-col :span="12">
              <a-form-item label="鉴权方式">
                <a-select v-model:value="form.auth_mode" :options="authModeOptions" />
              </a-form-item>
            </a-col>
            <a-col :span="12">
              <a-form-item label="鉴权 Header 名">
                <a-input v-model:value="form.auth_header_name" placeholder="如 api-key / x-api-key" />
              </a-form-item>
            </a-col>
          </a-row>

          <a-form-item label="差异化字段（驱动接入表单）">
            <div class="field-editor">
              <div v-for="(field, index) in form.extra_fields" :key="index" class="field-row">
                <a-input v-model:value="field.key" placeholder="key" style="width: 130px" />
                <a-input v-model:value="field.label" placeholder="显示名" style="width: 130px" />
                <a-select v-model:value="field.type" :options="fieldTypeOptions" style="width: 120px" />
                <a-checkbox v-model:checked="field.required">必填</a-checkbox>
                <a-input v-model:value="field.default" placeholder="默认值" style="width: 120px" />
                <a-input
                  v-if="field.type === 'select'"
                  v-model:value="field.optionText"
                  placeholder="选项，逗号分隔"
                  style="width: 160px"
                />
                <a-button type="link" danger size="small" @click="removeField(index)">移除</a-button>
              </div>
              <a-button type="dashed" block size="small" @click="addField">
                <template #icon><PlusOutlined /></template>
                添加字段
              </a-button>
            </div>
          </a-form-item>

          <a-form-item label="必填基础字段">
            <a-select
              v-model:value="form.required_fields"
              mode="multiple"
              :options="requiredFieldOptions"
              placeholder="选择该厂商接入时必须填写的基础字段"
            />
          </a-form-item>

          <a-form-item label="凭证获取指引链接">
            <a-input v-model:value="form.docs_url" placeholder="https://..." />
          </a-form-item>
        </template>

        <a-row :gutter="16">
          <a-col :span="12">
            <a-form-item label="排序（越小越靠前）">
              <a-input-number v-model:value="form.sort_order" :min="1" :max="9999" style="width: 100%" />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="启用">
              <a-switch v-model:checked="form.is_enabled" />
            </a-form-item>
          </a-col>
        </a-row>

        <a-form-item label="备注">
          <a-input v-model:value="form.remark" :maxlength="256" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup>
/**
 * 厂商模板管理（表格 + 弹窗表单）
 *
 * ⚠️ 当前未挂载入口：产品决策上把「厂商模板」从 AI 能力页的顶层 tab 中移除，
 * 以降低使用者的概念负担。组件与后端 CRUD 接口均完整保留，需要时可重新挂载：
 *   - 直接恢复 top tab：在 ./index.vue 的 a-tabs 中加回 <a-tab-pane key="templates">
 *   - 或作为次级入口（服务商接入页右上角按钮 + 抽屉）复用本组件
 * 注意：模板定义是运行时硬依赖（适配器 / 路径 / 鉴权 Header 均由模板提供），
 * 因此模板数据与播种逻辑不可删除，仅管理界面被收起。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { message } from 'ant-design-vue'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons-vue'
import {
  aiVendorListApi,
  aiVendorTemplateCreateApi,
  aiVendorTemplateDeleteApi,
  aiVendorTemplateUpdateApi,
} from '@/api/ai'
import {
  ADAPTER_OPTIONS,
  FIELD_TYPE_OPTIONS,
  VENDOR_CATEGORY_META,
  asArray,
} from './helpers'

const emit = defineEmits(['changed'])

const loading = ref(false)
const templates = ref([])
const modalOpen = ref(false)
const saving = ref(false)
const editing = ref(null)
const copyMode = ref(false)

const emptyForm = () => ({
  vendor: '',
  label: '',
  category: 'custom',
  icon: '',
  default_base_url: '',
  auth_mode: 'bearer',
  auth_header_name: '',
  path_chat: '/chat/completions',
  path_models: '/models',
  adapter: 'openai_compat',
  required_fields: ['base_url', 'api_key'],
  extra_fields: [],
  sort_order: 100,
  docs_url: '',
  remark: '',
  is_enabled: true,
})

const form = reactive(emptyForm())

const columns = [
  { title: '厂商', key: 'vendor', width: 220 },
  { title: '分类', key: 'category', width: 110 },
  { title: '适配器', key: 'adapter', width: 150 },
  { title: '鉴权', key: 'auth', width: 160 },
  { title: '字段', key: 'fields', width: 170 },
  { title: '启用', key: 'enabled', width: 80 },
  { title: '操作', key: 'action', width: 240 },
]

const categoryOptions = [
  { value: 'international', label: '海外模型' },
  { value: 'domestic', label: '国内模型' },
  { value: 'private', label: '私有化 / 本地' },
  { value: 'custom', label: '自定义' },
]
const authModeOptions = [
  { value: 'bearer', label: 'Bearer Token' },
  { value: 'api_key_header', label: '自定义 Header' },
  { value: 'query_param', label: 'URL 参数' },
  { value: 'signature', label: 'HMAC 签名' },
]

const fieldTypeOptions = FIELD_TYPE_OPTIONS
const adapterOptions = ADAPTER_OPTIONS

const requiredFieldOptions = computed(() => {
  const base = [
    { value: 'base_url', label: '端点地址（base_url）' },
    { value: 'api_key', label: 'API Key' },
    { value: 'auth_mode', label: '鉴权方式' },
    { value: 'auth_header_name', label: '鉴权 Header 名' },
  ]
  const extra = (form.extra_fields || [])
    .filter((item) => item.key)
    .map((item) => ({ value: item.key, label: `${item.label || item.key}（${item.key}）` }))
  return [...base, ...extra]
})

const modalTitle = computed(() => {
  if (copyMode.value) return '复制为新厂商模板'
  return editing.value ? '编辑厂商模板' : '新增自定义厂商模板'
})
const isVendorLocked = computed(() => !!editing.value && !copyMode.value)

const categoryMeta = (key) => VENDOR_CATEGORY_META[key] || { text: key || '未分类', color: 'default' }
const authModeLabel = (mode) => {
  const map = { bearer: 'Bearer Token', api_key_header: '自定义 Header', query_param: 'URL 参数', signature: 'HMAC 签名' }
  return map[mode] || mode || '—'
}

const loadTemplates = async () => {
  loading.value = true
  try {
    const res = await aiVendorListApi({ all: true })
    templates.value = res?.data || []
  } catch (err) {
    message.error(err?.message || '加载厂商模板失败')
  } finally {
    loading.value = false
  }
}

const hydrateForm = (record) => {
  Object.assign(form, emptyForm())
  form.vendor = record.vendor
  form.label = record.label
  form.category = record.category
  form.icon = record.icon
  form.default_base_url = record.default_base_url
  form.auth_mode = record.auth_mode
  form.auth_header_name = record.auth_header_name
  form.path_chat = record.path_chat
  form.path_models = record.path_models
  form.adapter = record.adapter
  form.required_fields = asArray(record.required_fields)
  form.extra_fields = asArray(record.extra_fields).map((item) => ({
    ...item,
    optionText: Array.isArray(item.options) ? item.options.join(',') : '',
  }))
  form.sort_order = record.sort_order
  form.docs_url = record.docs_url
  form.remark = record.remark
  form.is_enabled = record.is_enabled
}

const openCreate = () => {
  editing.value = null
  copyMode.value = false
  Object.assign(form, emptyForm())
  modalOpen.value = true
}

const openEdit = (record) => {
  editing.value = record
  copyMode.value = false
  hydrateForm(record)
  modalOpen.value = true
}

const openCopy = (record) => {
  editing.value = record
  copyMode.value = true
  hydrateForm(record)
  form.vendor = ''
  form.label = `${record.label} 副本`
  modalOpen.value = true
}

const addField = () => {
  form.extra_fields.push({ key: '', label: '', type: 'string', required: false, default: '', optionText: '' })
}
const removeField = (index) => {
  form.extra_fields.splice(index, 1)
}

const buildPayload = () => {
  const extraFields = (form.extra_fields || [])
    .filter((item) => item.key)
    .map((item) => {
      const payload = {
        key: item.key,
        label: item.label || item.key,
        type: item.type || 'string',
        required: !!item.required,
      }
      if (item.default !== undefined && item.default !== '') payload.default = item.default
      if (item.type === 'select') {
        payload.options = String(item.optionText || '')
          .split(',')
          .map((text) => text.trim())
          .filter(Boolean)
      }
      return payload
    })

  return {
    vendor: form.vendor,
    label: form.label,
    category: form.category,
    icon: form.icon,
    default_base_url: form.default_base_url,
    auth_mode: form.auth_mode,
    auth_header_name: form.auth_header_name,
    path_chat: form.path_chat,
    path_models: form.path_models,
    adapter: form.adapter,
    required_fields: form.required_fields || [],
    extra_fields: extraFields,
    sort_order: form.sort_order,
    docs_url: form.docs_url,
    remark: form.remark,
    is_enabled: form.is_enabled,
  }
}

const handleSubmit = async () => {
  if (copyMode.value || !editing.value) {
    if (!form.vendor) {
      message.warning('请填写厂商标识')
      return
    }
    if (!form.label) {
      message.warning('请填写显示名')
      return
    }
  }
  saving.value = true
  try {
    const payload = buildPayload()
    if (editing.value && !copyMode.value) {
      await aiVendorTemplateUpdateApi(payload)
      message.success('模板已更新')
    } else {
      await aiVendorTemplateCreateApi(payload)
      message.success('模板已创建')
    }
    modalOpen.value = false
    await loadTemplates()
    emit('changed')
  } catch (err) {
    message.error(err?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

const handleToggle = async (record, checked) => {
  try {
    await aiVendorTemplateUpdateApi({
      vendor: record.vendor,
      label: record.label,
      sort_order: record.sort_order,
      remark: record.remark,
      is_enabled: checked,
    })
    record.is_enabled = checked
    message.success(checked ? '已启用' : '已停用')
    emit('changed')
  } catch (err) {
    message.error(err?.message || '操作失败')
  }
}

const handleDelete = async (record) => {
  try {
    await aiVendorTemplateDeleteApi({ vendor: record.vendor })
    message.success('已删除')
    await loadTemplates()
    emit('changed')
  } catch (err) {
    message.error(err?.message || '删除失败')
  }
}

onMounted(loadTemplates)
defineExpose({ loadTemplates })
</script>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 14px;
}
.cell-name {
  display: flex;
  align-items: center;
}
.name-text {
  font-size: 13px;
  font-weight: 500;
}
.cell-sub-text {
  font-size: 12px;
  color: rgba(0, 0, 0, 0.45);
  margin-top: 2px;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12.5px;
}
.field-editor {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.field-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
</style>
