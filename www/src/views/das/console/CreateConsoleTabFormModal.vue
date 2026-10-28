<template>
  <a-modal :open="props.open" :title="props.title || '创建新查询'" :width="640" @cancel="handleCancel">
    <template #footer>
      <a-button @click="handleCancel">取消</a-button>
      <a-button type="primary" :loading="uiState.loading" @click="onSubmit">确定</a-button>
    </template>

    <a-alert
      v-if="presetHint"
      type="info"
      show-icon
      :message="presetHint"
      class="create-tab-modal-alert"
    />

    <a-form
      ref="formRef"
      :model="formData"
      :rules="rules"
      layout="vertical"
      class="modal-form create-console-tab-form"
    >
      <a-form-item label="数据源" name="instance_id" required>
        <a-select
          v-model:value="formData.instance_id"
          class="instance-select"
          size="small"
          show-search
          allow-clear
          placeholder="请选择数据源"
          option-label-prop="displayLabel"
          dropdown-class-name="instance-select-dropdown--two-line"
          :options="instanceSelectOptions"
          :filter-option="filterInstanceOption"
          @change="handleInstanceChange"
        >
          <template #option="{ ellipsisRemark, hostPort, fullRemark, longLabel, environmentName, environmentColor }">
            <div class="instance-select-option">
              <div class="instance-select-option__head">
                <div class="instance-select-option__name" :title="fullRemark || longLabel">
                  {{ ellipsisRemark }}
                </div>
                <span
                  v-if="environmentName"
                  class="instance-select-option__env"
                  :style="buildEnvironmentTagStyle(environmentColor)"
                >{{ environmentName }}</span>
              </div>
              <div class="instance-select-option__addr" :title="hostPort">
                {{ hostPort }}
              </div>
            </div>
          </template>
        </a-select>
      </a-form-item>

      <div class="quick-picker-wrap">
        <a-tabs v-model:activeKey="quickActiveKey" size="small" class="quick-picker-tabs">
          <a-tab-pane key="recent" tab="最近访问">
            <a-empty v-if="!recentInstances.length" description="暂无记录" class="quick-empty" />
            <a-row v-else :gutter="12" class="quick-card-grid">
              <a-col
                v-for="item in recentInstances"
                :key="String(item.instance_id)"
                :span="12"
                class="quick-card-col"
              >
                <div
                  class="quick-instance-card"
                  :class="{ 'is-selected': String(formData.instance_id) === String(item.instance_id) }"
                  @click="pickInstance(item.instance_id)"
                >
                  <div class="quick-card-main">
                    <div class="quick-card-icon">
                      <ConsoleDbIcon :type="item.db_type || 'mysql'" />
                    </div>
                    <div class="quick-card-texts">
                      <AppTooltip :title="`${item.hostname || ''}:${item.port || ''}`" placement="topLeft">
                        <span class="quick-card-conn">{{ item.hostname }}:{{ item.port }}</span>
                      </AppTooltip>
                      <div class="quick-card-name-row">
                        <DataSourceName :name="item.remark || item.instance_name || item.name" />
                      </div>
                    </div>
                    <StarFilled v-if="isFavoritedInstance(item.instance_id)" class="quick-card-fav" />
                  </div>
                  <a-tag v-if="item.environment_name" class="quick-card-env-tag" :color="item.environment_color || undefined">
                    {{ item.environment_name }}
                  </a-tag>
                </div>
              </a-col>
            </a-row>
          </a-tab-pane>
          <a-tab-pane key="favorite" tab="我的收藏">
            <a-empty v-if="!favoritedInstances.length" description="暂无收藏" class="quick-empty" />
            <a-row v-else :gutter="12" class="quick-card-grid">
              <a-col
                v-for="item in favoritedInstances"
                :key="String(item.instance_id)"
                :span="12"
                class="quick-card-col"
              >
                <div
                  class="quick-instance-card"
                  :class="{ 'is-selected': String(formData.instance_id) === String(item.instance_id) }"
                  @click="pickInstance(item.instance_id)"
                >
                  <div class="quick-card-main">
                    <div class="quick-card-icon">
                      <ConsoleDbIcon :type="item.db_type || 'mysql'" />
                    </div>
                    <div class="quick-card-texts">
                      <AppTooltip :title="`${item.hostname || ''}:${item.port || ''}`" placement="topLeft">
                        <span class="quick-card-conn">{{ item.hostname }}:{{ item.port }}</span>
                      </AppTooltip>
                      <div class="quick-card-name-row">
                        <DataSourceName :name="item.remark || item.instance_name || item.name" />
                      </div>
                    </div>
                    <StarFilled class="quick-card-fav" />
                  </div>
                  <a-tag v-if="item.environment_name" class="quick-card-env-tag" :color="item.environment_color || undefined">
                    {{ item.environment_name }}
                  </a-tag>
                </div>
              </a-col>
            </a-row>
          </a-tab-pane>
        </a-tabs>
      </div>
    </a-form>
  </a-modal>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { StarFilled } from '@ant-design/icons-vue'
import ConsoleDbIcon from './ConsoleDbIcon.vue'
import AppTooltip from '@/components/patterns/AppTooltip.vue'
import DataSourceName from '@/components/patterns/DataSourceName.vue'
import { buildEnvironmentTagStyle, normalizeHexColor } from '@/utils/color'

const props = defineProps({
  open: Boolean,
  title: String,
  instances: {
    type: Array,
    default: () => [],
  },
  boundInstanceIds: {
    type: Array,
    default: () => [],
  },
  favoritedInstanceIds: {
    type: Array,
    default: () => [],
  },
  presetHint: {
    type: String,
    default: '',
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
const quickActiveKey = ref('recent')

const rules = {
  instance_id: [
    {
      required: true,
      message: '请选择数据源',
      trigger: ['change', 'blur'],
    },
  ],
}

const normalizeSearchText = (value) => String(value || '').trim().toLowerCase()

const formatInstanceShortLabel = (instance) => {
  if (!instance) return ''
  const remark = String(instance?.remark || '').trim()
  if (remark) return remark
  const hostname = String(instance?.hostname || '').trim()
  const port = String(instance?.port || '').trim()
  const hostPort = hostname ? `${hostname}${port ? `:${port}` : ''}` : ''
  return hostPort || String(instance?.instance_name || instance?.name || '')
}

const formatInstanceLabel = (instance) => {
  if (!instance) return ''
  const remark = String(instance?.remark || '').trim()
  const hostname = String(instance?.hostname || '').trim()
  const port = String(instance?.port || '').trim()
  const hostPort = hostname ? `${hostname}${port ? `:${port}` : ''}` : ''
  if (!remark) return hostPort
  if (!hostPort) return remark
  return `${remark} | ${hostPort}`
}

const instanceSelectOptions = computed(() => {
  return (props.instances || []).map((item) => {
    const hostname = String(item?.hostname || '').trim()
    const port = String(item?.port || '').trim()
    const hostPort = hostname ? (port ? `${hostname}:${port}` : hostname) : ''
    const remark = String(item?.remark || '').trim()
    const displayRemark = remark || hostPort
    const ellipsisRemark = displayRemark.length > 15 ? displayRemark.slice(0, 15).trimEnd() + '...' : displayRemark
    const displayLabel = ellipsisRemark || String(item?.instance_name || item?.name || '').trim() || String(item?.instance_id || '').slice(0, 15)
    const fullLabel = remark && hostPort ? `${remark} | ${hostPort}` : (remark || hostPort)
    const environmentName = String(item?.environment_name || item?.environmentName || '').trim()
    const environmentColor = normalizeHexColor(item?.environment_color || item?.environmentColor || '') || ''
    return {
      value: String(item.instance_id || ''),
      label: displayLabel,
      displayLabel,
      longLabel: fullLabel || displayLabel,
      ellipsisRemark,
      hostPort,
      fullRemark: remark,
      environmentName,
      environmentColor,
      db_type: item.db_type || '',
      _instance: item,
    }
  })
})

const filterInstanceOption = (input, option) => {
  const keyword = normalizeSearchText(input)
  if (!keyword) return true
  const label = normalizeSearchText(option?.longLabel || option?.label || '')
  const host = normalizeSearchText(option?._instance?.hostname || '')
  const remark = normalizeSearchText(option?._instance?.remark || '')
  const dbType = normalizeSearchText(option?._instance?.db_type || '')
  const hostPort = normalizeSearchText(option?.hostPort || '')
  const fullRemark = normalizeSearchText(option?.fullRemark || '')
  const ellipsis = normalizeSearchText(option?.ellipsisRemark || '')
  const instId = normalizeSearchText(String(option?.value || ''))
  return (
    label.includes(keyword) ||
    host.includes(keyword) ||
    remark.includes(keyword) ||
    dbType.includes(keyword) ||
    hostPort.includes(keyword) ||
    fullRemark.includes(keyword) ||
    ellipsis.includes(keyword) ||
    instId.includes(keyword)
  )
}

const getInstanceById = (instanceId) =>
  (props.instances || []).find((item) => String(item.instance_id) === String(instanceId))

const recentInstances = computed(() => {
  const seen = new Set()
  const list = []
  for (const id of props.boundInstanceIds || []) {
    const key = String(id)
    if (!key || seen.has(key)) continue
    const meta = getInstanceById(key)
    if (meta) {
      list.push(meta)
      seen.add(key)
    }
    if (list.length >= 2) break
  }
  if (list.length < 2) {
    for (const item of props.instances || []) {
      const key = String(item.instance_id)
      if (seen.has(key)) continue
      list.push(item)
      seen.add(key)
      if (list.length >= 2) break
    }
  }
  return list
})

const isFavoritedInstance = (instanceId) =>
  (props.favoritedInstanceIds || []).some((id) => String(id) === String(instanceId))

const favoritedInstances = computed(() => {
  const ids = props.favoritedInstanceIds || []
  const list = []
  for (const id of ids) {
    const meta = getInstanceById(id)
    if (meta) list.push(meta)
    if (list.length >= 2) break
  }
  return list
})

const pickInstance = (instanceId) => {
  formData.value.instance_id = String(instanceId || '')
}

const handleInstanceChange = () => {
  // 预留：父组件如需联动可以通过 watch formData.instance_id 处理
}

const handleCancel = () => {
  emit('update:open', false)
  formRef.value?.resetFields()
}

const onSubmit = async () => {
  try {
    await formRef.value.validateFields()
    uiState.loading = true
    emit('submit', { ...formData.value })
  } catch {
    // ignore
  } finally {
    uiState.loading = false
  }
}

watch(
  () => props.open,
  (val) => {
    if (!val) {
      formRef.value?.resetFields()
    } else {
      quickActiveKey.value = 'recent'
    }
  }
)
</script>

<style scoped>
.create-tab-modal-alert {
  margin-bottom: 16px;
}

.create-console-tab-form .instance-select {
  width: 100%;
}

.create-console-tab-form :deep(.instance-select .ant-select-selector) {
  min-height: 40px !important;
  padding: 4px 12px !important;
}

.create-console-tab-form :deep(.instance-select .ant-select-selection-search-input) {
  height: 30px !important;
  line-height: 30px !important;
}

.quick-picker-wrap {
  margin-top: 12px;
}

.quick-picker-tabs {
  width: 100%;
}

.quick-empty {
  padding: 12px 0;
}

.quick-card-grid {
  margin-top: 4px;
}

.quick-card-col {
  margin-bottom: 12px;
}

.quick-instance-card {
  border: 1px solid var(--ant-colorBorder, #d9d9d9);
  background: #ffffff;
  border-radius: 8px;
  padding: 10px 12px;
  cursor: pointer;
  transition: border-color 0.2s ease, background-color 0.2s ease, box-shadow 0.2s ease;
}

.quick-instance-card:hover {
  border-color: var(--ant-colorPrimary, #1677ff);
  background-color: rgb(22 119 255 / 6%);
}

.quick-instance-card.is-selected {
  border-color: var(--ant-colorPrimary, #1677ff);
  background-color: rgb(22 119 255 / 10%);
  box-shadow: 0 0 0 1px var(--ant-colorPrimary, #1677ff);
}

.quick-card-main {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.quick-card-icon {
  width: 20px;
  height: 20px;
  flex: 0 0 20px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 14px;
}

.quick-card-texts {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.quick-card-conn {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 13px;
  font-weight: 500;
  color: #1F2937;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  line-height: 20px;
  vertical-align: middle;
}

.quick-card-name-row {
  max-width: 100%;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  line-height: 1.4;
}

.quick-card-fav {
  flex: 0 0 auto;
  color: #fadb14;
  font-size: 14px;
}

.quick-card-env-tag {
  margin-top: 6px;
  font-size: 11px;
  line-height: 18px;
  padding: 0 6px;
  height: 18px;
  display: inline-flex;
  align-items: center;
}

.select-option-label {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.instance-select-option {
  display: flex;
  flex-direction: column;
  justify-content: center;
  line-height: 1.3;
  padding: 2px 0;
}

.instance-select-option__name {
  color: #1f2329;
  font-size: 14px;
  font-weight: 500;
  line-height: 20px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.instance-select-option__addr {
  color: #8c8c8c;
  font-size: 12px;
  line-height: 18px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.instance-select-option__head {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.instance-select-option__head .instance-select-option__name {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 13px;
}

.instance-select-option__env {
  flex: 0 0 auto;
  max-width: 96px;
  padding: 0 6px;
  border: 1px solid transparent;
  border-radius: 4px;
  font-size: 11px;
  line-height: 16px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

:deep(.instance-select-dropdown--two-line .ant-select-item-option-content) {
  padding-top: 4px;
  padding-bottom: 4px;
  height: auto;
  line-height: 1.3;
}

:deep(.instance-select-dropdown--two-line .ant-select-item-option) {
  height: auto;
  line-height: 1.3;
  padding-top: 4px;
  padding-bottom: 4px;
}

@media (max-width: 768px) {
  .quick-card-col {
    width: 100% !important;
    flex: 0 0 100% !important;
    max-width: 100% !important;
    margin-bottom: 8px;
  }

  .quick-instance-card {
    padding: 8px 10px;
  }
}
</style>
