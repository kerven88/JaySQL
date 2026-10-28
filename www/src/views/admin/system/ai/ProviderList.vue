<template>
  <div class="provider-list">
    <a-table
      :columns="columns"
      :data-source="filteredProviders"
      :loading="loading"
      :pagination="false"
      row-key="provider_id"
      size="middle"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'name'">
          <div class="cell-name">
            <span class="name-text">{{ record.display_name }}</span>
            <a-tag v-if="record.is_default" color="gold" style="margin-left: 6px">默认</a-tag>
          </div>
        </template>

        <template v-else-if="column.key === 'endpoint'">
          <div class="mono">{{ record.base_url }}</div>
        </template>

        <template v-else-if="column.key === 'owner'">
          <div>{{ record.owner || '—' }}</div>
        </template>

        <template v-else-if="column.key === 'status'">
          <span>{{ healthMeta(record.health_status).text }}</span>
        </template>

        <template v-else-if="column.key === 'validated'">
          <div>{{ record.last_validated_at || '未验证' }}</div>
        </template>

        <template v-else-if="column.key === 'action'">
          <a-space size="small">
            <a-button
              type="link"
              size="small"
              :loading="actingId === record.provider_id"
              @click="handleValidate(record)"
            >
              测试连接
            </a-button>
            <a-dropdown>
              <a-button type="link" size="small">
                更多 <DownOutlined class="more-caret" />
              </a-button>
              <template #overlay>
                <a-menu @click="({ key }) => handleAction(key, record)">
                  <a-menu-item key="toggle">
                    {{ record.status === 'enabled' ? '停用' : '启用' }}
                  </a-menu-item>
                  <a-menu-item key="default" :disabled="record.is_default">设为默认</a-menu-item>
                  <a-menu-item key="edit">编辑</a-menu-item>
                  <a-menu-item key="detail">详情</a-menu-item>
                  <a-menu-divider />
                  <a-menu-item key="delete" danger>删除</a-menu-item>
                </a-menu>
              </template>
            </a-dropdown>
          </a-space>
        </template>
      </template>
    </a-table>

    <!-- 接入向导 -->
    <ProviderWizard
      v-model:open="wizardOpen"
      :templates="templates"
      @success="loadProviders"
    />

    <!-- 编辑 -->
    <a-drawer
      v-model:open="editOpen"
      :width="680"
      title="编辑服务商配置"
      :body-style="{ paddingBottom: '72px' }"
    >
      <a-alert
        type="warning"
        show-icon
        message="厂商模板的后续变更不会回写本配置"
        description="本配置使用的是保存时的端点与差异化参数；如需同步模板最新默认值，请手动修改。"
        style="margin-bottom: 16px"
      />
      <DynamicProviderForm
        ref="editFormRef"
        :template="currentTemplate"
        :form="editForm"
        :existing-mask="editing?.api_key_mask || ''"
      />
      <div class="drawer-footer">
        <a-button @click="editOpen = false">取消</a-button>
        <a-button type="primary" :loading="saving" @click="handleUpdate">保存</a-button>
      </div>
    </a-drawer>

    <!-- 详情 -->
    <a-drawer v-model:open="detailOpen" :width="620" title="服务商详情">
      <a-descriptions v-if="detail" bordered size="small" :column="1">
        <a-descriptions-item label="服务名称">{{ detail.display_name }}</a-descriptions-item>
        <a-descriptions-item label="厂商">
          {{ detail.vendor_label || detail.vendor }}（{{ detail.adapter }}）
        </a-descriptions-item>
        <a-descriptions-item label="端点">{{ detail.base_url }}</a-descriptions-item>
        <a-descriptions-item label="API Key">{{ detail.api_key_mask || '未配置' }}</a-descriptions-item>
        <a-descriptions-item label="默认模型">{{ detail.default_model_name || '未设置' }}</a-descriptions-item>
        <a-descriptions-item label="状态">
          <a-tag :color="statusMeta(detail.status).color">{{ statusMeta(detail.status).text }}</a-tag>
        </a-descriptions-item>
        <a-descriptions-item label="最近验证">
          {{ detail.last_validated_at || '—' }}
          <span v-if="detail.last_validation_msg" class="cell-sub-text">
            · {{ detail.last_validation_msg }}
          </span>
        </a-descriptions-item>
        <a-descriptions-item label="备注">{{ detail.remark || '—' }}</a-descriptions-item>
      </a-descriptions>

      <div class="detail-section-title">最近调用记录</div>
      <a-table
        :columns="logColumns"
        :data-source="logs"
        :pagination="false"
        size="small"
        row-key="id"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'status'">
            <a-tag :color="record.status === 'success' ? 'success' : 'error'">
              {{ record.status === 'success' ? '成功' : '失败' }}
            </a-tag>
          </template>
          <template v-else-if="column.key === 'error'">
            <span class="cell-sub-text">{{ record.error_message || '—' }}</span>
          </template>
        </template>
      </a-table>
    </a-drawer>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { message, Modal } from 'ant-design-vue'
import { DownOutlined } from '@ant-design/icons-vue'
import {
  aiProviderDeleteApi,
  aiProviderDetailApi,
  aiProviderDisableApi,
  aiProviderEnableApi,
  aiProviderListApi,
  aiProviderSetDefaultApi,
  aiProviderUpdateApi,
  aiProviderValidateApi,
} from '@/api/ai'
import DynamicProviderForm from './components/DynamicProviderForm.vue'
import ProviderWizard from './ProviderWizard.vue'
import {
  HEALTH_META,
  PROVIDER_STATUS_META,
  buildProviderFormFromTemplate,
  parseJSONField,
} from './helpers'

const props = defineProps({
  templates: { type: Array, default: () => [] },
  // 搜索关键字由页面层持有（工具栏与 tab 同行）
  keyword: { type: String, default: '' },
})

const emit = defineEmits(['changed'])

const loading = ref(false)
const providers = ref([])
const wizardOpen = ref(false)
const actingId = ref('')

const editOpen = ref(false)
const editFormRef = ref()
const editForm = ref(buildProviderFormFromTemplate({}))
const editing = ref(null)
const saving = ref(false)

const detailOpen = ref(false)
const detail = ref(null)
const logs = ref([])

const columns = [
  { title: '服务名称', key: 'name', width: 200 },
  { title: 'API地址', key: 'endpoint', width: 230 },
  { title: '创建人', key: 'owner', width: 150 },
  { title: '状态', key: 'status', width: 110 },
  { title: '最近验证', key: 'validated', width: 150 },
  { title: '操作', key: 'action', width: 170 },
]

const logColumns = [
  { title: '时间', dataIndex: 'created_at', width: 160 },
  { title: '场景', dataIndex: 'scene', width: 90 },
  { title: '模型', dataIndex: 'model_name', width: 140 },
  { title: '耗时', dataIndex: 'latency_ms', width: 80 },
  { title: '结果', key: 'status', width: 80 },
  { title: '错误', key: 'error' },
]

const statusMeta = (key) => PROVIDER_STATUS_META[key] || { text: key || '未知', color: 'default', tip: '' }
const healthMeta = (key) => HEALTH_META[key] || HEALTH_META.unknown

const currentTemplate = computed(() => {
  if (!editing.value) return {}
  return props.templates.find((item) => item.vendor === editing.value.vendor) || {}
})

const filteredProviders = computed(() => {
  const text = String(props.keyword || '').trim().toLowerCase()
  if (!text) return providers.value
  return providers.value.filter((item) => {
    return [item.display_name, item.vendor, item.vendor_label, item.base_url]
      .filter(Boolean)
      .some((value) => String(value).toLowerCase().includes(text))
  })
})

const loadProviders = async () => {
  loading.value = true
  try {
    const res = await aiProviderListApi()
    providers.value = res?.data || []
    emit('changed', providers.value)
  } catch (err) {
    message.error(err?.message || '加载服务商列表失败')
  } finally {
    loading.value = false
  }
}

const handleValidate = async (record) => {
  actingId.value = record.provider_id
  try {
    const res = await aiProviderValidateApi({ provider_id: record.provider_id })
    const result = res?.data
    if (result?.ok) {
      message.success(`验证通过，耗时 ${result.latency_ms} ms`)
    } else {
      message.warning(result?.message || '验证未通过')
    }
    await loadProviders()
  } catch (err) {
    message.error(err?.message || '验证失败')
  } finally {
    actingId.value = ''
  }
}

const handleToggle = async (record, enable) => {
  actingId.value = record.provider_id
  try {
    if (enable) {
      await aiProviderEnableApi({ provider_id: record.provider_id })
      message.success('已启用')
    } else {
      await aiProviderDisableApi({ provider_id: record.provider_id })
      message.success('已停用')
    }
    await loadProviders()
  } catch (err) {
    message.error(err?.message || '操作失败')
  } finally {
    actingId.value = ''
  }
}

const handleSetDefault = async (record) => {
  try {
    await aiProviderSetDefaultApi({ provider_id: record.provider_id })
    message.success('已设为默认服务商')
    await loadProviders()
  } catch (err) {
    message.error(err?.message || '设置失败')
  }
}

// 「更多」菜单统一入口；删除为不可逆操作，用确认弹窗替代原 popconfirm
const handleAction = (key, record) => {
  if (key === 'toggle') handleToggle(record, record.status !== 'enabled')
  else if (key === 'default') handleSetDefault(record)
  else if (key === 'edit') openEdit(record)
  else if (key === 'detail') openDetail(record)
  else if (key === 'delete') {
    Modal.confirm({
      title: '删除服务商',
      content: '删除后该服务商的模型清单将一并清理，确认删除？',
      okText: '删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: () => handleDelete(record),
    })
  }
}

const openEdit = (record) => {
  editing.value = record
  const template = props.templates.find((item) => item.vendor === record.vendor) || {}
  editForm.value = {
    ...buildProviderFormFromTemplate(template),
    provider_id: record.provider_id,
    vendor: record.vendor,
    display_name: record.display_name,
    base_url: record.base_url,
    auth_mode: record.auth_mode,
    api_key: '',
    timeout_seconds: record.timeout_seconds || 60,
    max_retries: record.max_retries ?? 1,
    is_shared: !!record.is_shared,
    remark: record.remark || '',
    extra_config: parseJSONField(record.extra_config, {}),
  }
  editOpen.value = true
}

const handleUpdate = async () => {
  const errors = editFormRef.value?.validate?.() || []
  if (errors.length) {
    message.warning(errors[0])
    return
  }
  saving.value = true
  try {
    await aiProviderUpdateApi(editForm.value)
    message.success('已保存')
    editOpen.value = false
    await loadProviders()
  } catch (err) {
    message.error(err?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

const handleDelete = async (record) => {
  try {
    await aiProviderDeleteApi({ provider_id: record.provider_id })
    message.success('已删除')
    await loadProviders()
  } catch (err) {
    message.error(err?.message || '删除失败')
  }
}

const openDetail = async (record) => {
  detailOpen.value = true
  detail.value = record
  logs.value = []
  try {
    const res = await aiProviderDetailApi({ provider_id: record.provider_id })
    detail.value = res?.data?.provider || record
    logs.value = res?.data?.recent_logs || []
  } catch (err) {
    message.error(err?.message || '加载详情失败')
  }
}

onMounted(loadProviders)
// 工具栏已上移到页面层，这里只暴露页面层需要的入口
defineExpose({
  loadProviders,
  loading,
  openWizard: () => {
    wizardOpen.value = true
  },
})
</script>

<style scoped>
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
  margin-left: 4px;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  word-break: break-all;
}
.more-caret {
  font-size: 10px;
  margin-left: 2px;
}
.drawer-footer {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  padding: 12px 24px;
  background: #fff;
  border-top: 1px solid #e6e4dd;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.detail-section-title {
  font-size: 13px;
  font-weight: 500;
  margin: 20px 0 10px;
}

@media (max-width: 768px) {
  .provider-list :deep(.ant-table) {
    min-width: 1010px;
  }

  .provider-list :deep(.ant-table-wrapper) {
    overflow-x: auto;
  }
}
</style>
