<template>
  <div class="model-list">
    <a-table
      :columns="columns"
      :data-source="models"
      :loading="loading"
      :pagination="pagination"
      row-key="model_uid"
      size="middle"
      @change="handleTableChange"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'model'">
          <div class="cell-name">
            <span class="mono">{{ record.model_name }}</span>
            <a-tag v-if="record.is_default" color="gold" style="margin-left: 6px">默认</a-tag>
          </div>
        </template>

        <template v-else-if="column.key === 'capabilities'">
          <a-tag v-for="cap in asArray(record.capabilities)" :key="cap" color="blue" style="margin-bottom: 4px">
            {{ CAPABILITY_LABELS[cap] || cap }}
          </a-tag>
          <span v-if="!asArray(record.capabilities).length" class="cell-sub-text">未探测</span>
        </template>

        <template v-else-if="column.key === 'context'">
          <span>{{ record.context_window ? formatNumber(record.context_window) : '—' }}</span>
        </template>

        <template v-else-if="column.key === 'status'">
          <span>{{ statusText(record.status) }}</span>
        </template>

        <template v-else-if="column.key === 'enabled'">
          <a-switch
            :checked="record.is_enabled"
            size="small"
            @change="(checked) => handleToggleEnabled(record, checked)"
          />
        </template>

        <template v-else-if="column.key === 'action'">
          <a-dropdown>
            <a-button type="link" size="small">
              更多 <DownOutlined class="more-caret" />
            </a-button>
            <template #overlay>
              <a-menu @click="({ key }) => handleAction(key, record)">
                <a-menu-item key="default" :disabled="record.is_default || record.status !== 'available'">
                  设为默认
                </a-menu-item>
                <a-menu-item key="edit">编辑</a-menu-item>
                <a-menu-divider />
                <a-menu-item key="delete" danger>删除</a-menu-item>
              </a-menu>
            </template>
          </a-dropdown>
        </template>
      </template>
    </a-table>

    <!-- 编辑（模型由服务商验证时自动同步，手工新增入口已移除） -->
    <a-modal
      v-model:open="modalOpen"
      title="编辑模型"
      :confirm-loading="saving"
      @ok="handleSubmit"
    >
      <a-form layout="vertical" :model="form">
        <a-form-item label="所属服务商" required>
          <a-select
            v-model:value="form.provider_id"
            :options="providerOptions"
            :disabled="!!editing"
            placeholder="请选择"
          />
        </a-form-item>
        <a-form-item label="模型名（厂商侧标识）" required>
          <a-input v-model:value="form.model_name" :disabled="!!editing" placeholder="例如：qwen2.5:14b" />
        </a-form-item>
        <a-form-item label="显示名 / 别名">
          <a-input v-model:value="form.display_name" placeholder="留空则与模型名一致" />
        </a-form-item>
        <a-row :gutter="16">
          <a-col :span="12">
            <a-form-item label="上下文窗口">
              <a-input-number v-model:value="form.context_window" :min="0" style="width: 100%" />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="最大输出 token">
              <a-input-number v-model:value="form.max_output_tokens" :min="0" style="width: 100%" />
            </a-form-item>
          </a-col>
        </a-row>
        <a-form-item label="能力标签">
          <a-select v-model:value="form.capabilities" mode="multiple" :options="capabilityOptions" />
        </a-form-item>
        <a-form-item label="启用">
          <a-switch v-model:checked="form.is_enabled" />
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="form.remark" :maxlength="256" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { message, Modal } from 'ant-design-vue'
import { DownOutlined } from '@ant-design/icons-vue'
import {
  aiModelDeleteApi,
  aiModelListApi,
  aiModelSetDefaultApi,
  aiModelUpdateApi,
} from '@/api/ai'
import { CAPABILITY_LABELS, asArray, parseJSONField } from './helpers'

const props = defineProps({
  // 服务商下拉选项与筛选条件由页面层持有（工具栏与 tab 同行）
  providerOptions: { type: Array, default: () => [] },
  providerId: { type: [String, Number], default: undefined },
  keyword: { type: String, default: '' },
})

const loading = ref(false)
const models = ref([])
const pagination = reactive({ current: 1, pageSize: 50, total: 0, showSizeChanger: true })

const modalOpen = ref(false)
const saving = ref(false)
const editing = ref(null)
const form = reactive({
  provider_id: undefined,
  model_name: '',
  display_name: '',
  context_window: 0,
  max_output_tokens: 0,
  capabilities: [],
  is_enabled: true,
  remark: '',
})

const columns = [
  { title: '模型名称', key: 'model', width: 260 },
  { title: '能力', key: 'capabilities', width: 200 },
  { title: '上下文', key: 'context', width: 110 },
  { title: '状态', key: 'status', width: 100 },
  { title: '启用', key: 'enabled', width: 80 },
  { title: '操作', key: 'action', width: 90, align: 'right' },
]

const capabilityOptions = Object.entries(CAPABILITY_LABELS).map(([value, label]) => ({ value, label }))

const loadModels = async () => {
  loading.value = true
  try {
    const res = await aiModelListApi({
      provider_id: props.providerId || '',
      keyword: props.keyword || '',
      page: pagination.current,
      page_size: pagination.pageSize,
    })
    models.value = (res?.data || []).map((item) => ({
      ...item,
      capabilities: parseJSONField(item.capabilities, []),
    }))
    pagination.total = res?.total || 0
  } catch (err) {
    message.error(err?.message || '加载模型列表失败')
  } finally {
    loading.value = false
  }
}

const handleTableChange = (page) => {
  pagination.current = page.current
  pagination.pageSize = page.pageSize
  loadModels()
}

const openEdit = (record) => {
  editing.value = record
  Object.assign(form, {
    provider_id: record.provider_id,
    model_uid: record.model_uid,
    model_name: record.model_name,
    display_name: record.display_name,
    context_window: record.context_window,
    max_output_tokens: record.max_output_tokens,
    capabilities: asArray(record.capabilities),
    is_enabled: record.is_enabled,
    remark: record.remark,
  })
  modalOpen.value = true
}

const handleSubmit = async () => {
  if (!form.provider_id) {
    message.warning('请选择所属服务商')
    return
  }
  if (!form.model_name) {
    message.warning('请填写模型名')
    return
  }
  saving.value = true
  try {
    await aiModelUpdateApi({ ...form, model_uid: editing.value.model_uid })
    message.success('模型已更新')
    modalOpen.value = false
    await loadModels()
  } catch (err) {
    message.error(err?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

const handleToggleEnabled = async (record, checked) => {
  try {
    await aiModelUpdateApi({
      model_uid: record.model_uid,
      model_name: record.model_name,
      is_enabled: checked,
    })
    record.is_enabled = checked
    message.success(checked ? '已启用' : '已停用')
  } catch (err) {
    message.error(err?.message || '操作失败')
  }
}

const handleSetDefault = async (record) => {
  try {
    await aiModelSetDefaultApi({ model_uid: record.model_uid })
    message.success('已设为默认模型')
    await loadModels()
  } catch (err) {
    message.error(err?.message || '设置失败')
  }
}

const handleDelete = async (record) => {
  try {
    await aiModelDeleteApi({ model_uid: record.model_uid })
    message.success('已删除')
    await loadModels()
  } catch (err) {
    message.error(err?.message || '删除失败')
  }
}

// 「更多」菜单统一入口；删除用确认弹窗替代原 popconfirm
const handleAction = (key, record) => {
  if (key === 'default') handleSetDefault(record)
  else if (key === 'edit') openEdit(record)
  else if (key === 'delete') {
    Modal.confirm({
      title: '删除模型',
      content: '确认删除该模型？',
      okText: '删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: () => handleDelete(record),
    })
  }
}

const statusText = (status) => {
  const map = { available: '可用', unavailable: '已下线', deprecated: '已废弃' }
  return map[status] || status
}
const formatNumber = (value) => Number(value || 0).toLocaleString()

onMounted(loadModels)

// 工具栏已上移到页面层，这里只暴露页面层需要的入口
defineExpose({ loadModels, loading })
</script>

<style scoped>
.cell-name {
  display: flex;
  align-items: center;
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
.more-caret {
  font-size: 10px;
  margin-left: 2px;
}

@media (max-width: 768px) {
  .model-list :deep(.ant-table) {
    min-width: 840px;
  }

  .model-list :deep(.ant-table-wrapper) {
    overflow-x: auto;
  }
}
</style>
