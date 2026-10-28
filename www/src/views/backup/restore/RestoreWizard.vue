<template>
  <div class="gi-page-shell restore-wizard-page">
    <a-card class="wizard-steps-card" :bordered="false">
      <a-steps :current="currentStep" size="small">
        <a-step title="选择备份集" />
        <a-step title="配置恢复目标" />
        <a-step title="恢复预检查" />
        <a-step title="确认并执行" />
      </a-steps>
    </a-card>

    <a-card class="wizard-main-card" :bordered="false" :loading="uiState.pageLoading">
      <RestoreWizardStepSource
        v-show="currentStep === 0"
        :sets="sets"
        :sets-loading="uiState.setsLoading"
        :objects-info="objectsInfo"
        :selected-set="selectedSet"
        :is-physical="isPhysical"
        :is-full="isFullBackup"
        @change-set="handleSetChange"
      />

      <RestoreWizardStepTarget
        v-show="currentStep === 1"
        ref="targetStepRef"
        :objects-info="objectsInfo"
        :instances="instances"
        :instances-loading="uiState.instancesLoading"
        :nodes="nodes"
        :nodes-loading="uiState.nodesLoading"
        :selected-set="selectedSet"
        :is-physical="isPhysical"
        :is-mysql-physical="isMySQLPhysical"
        @change="invalidatePrecheck"
        @change-instance="handleInstanceChange"
      />

      <RestoreWizardStepPrecheck
        v-show="currentStep === 2"
        :precheck-loading="uiState.precheckLoading"
        :precheck-result="precheck.result"
        :precheck-error="precheck.errorMessage"
      />

      <RestoreWizardStepConfirm v-show="currentStep === 3" :summary-items="summaryItems" :is-physical="isPhysical" />
    </a-card>

    <div class="wizard-footer">
      <a-space>
        <a-button @click="handleCancel">取消</a-button>
        <a-button v-if="currentStep > 0" :disabled="uiState.precheckLoading || uiState.submitting" @click="handlePrev">
          上一步
        </a-button>
        <a-button v-if="currentStep < 3" type="primary" :disabled="!canGoNext" @click="handleNext">下一步</a-button>
        <a-button v-else type="primary" :loading="uiState.submitting" :disabled="!canSubmit" @click="handleSubmit">
          启动恢复
        </a-button>
      </a-space>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, provide, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { message } from 'ant-design-vue'

import {
  createPhysicalRestoreRunApi,
  createRestoreRunApi,
  getBackupInstancesApi,
  getBackupNodesApi,
  getBackupSetObjectsApi,
  getBackupSetsApi,
  precheckRestoreRunApi,
} from '@/api/backup'

import RestoreWizardStepSource from './RestoreWizardStepSource.vue'
import RestoreWizardStepTarget from './RestoreWizardStepTarget.vue'
import RestoreWizardStepPrecheck from './RestoreWizardStepPrecheck.vue'
import RestoreWizardStepConfirm from './RestoreWizardStepConfirm.vue'
import { deriveRestoreMode, restoreModeLabel, formatBytes, formatDateTime } from './constants'

defineOptions({ name: 'RestoreWizardView' })

const route = useRoute()
const router = useRouter()

const uiState = reactive({
  pageLoading: false,
  setsLoading: false,
  instancesLoading: false,
  nodesLoading: false,
  precheckLoading: false,
  submitting: false,
})

const currentStep = ref(0)
const sets = ref([])
const instances = ref([])
const nodes = ref([])
const targetStepRef = ref()

const objectsInfo = reactive({
  restore_kind: '',
  backup_type: '',
  is_full: true,
  supports_object_select: false,
  objects: [],
  chain: [],
  hint: '',
  artifact_format: '',
})

const form = reactive({
  name: '',
  backup_set_id: null,
  target_instance_id: null,
  target_db_name: '',
  // 逻辑
  object_scope: 'ALL',
  selected_objects: [],
  mapping: {},
  restore_schema: true,
  restore_data: true,
  schema_conflict_policy: 'ABORT',
  data_conflict_policy: 'ABORT',
  // 物理
  restore_mode: 'NEW_INSTANCE',
  target_node_id: null,
  target_data_dir: '',
  target_port: 5433,
  auto_start: true,
  confirm_host_port: '',
  confirm_text: '',
  // 提交
  execute_confirmed: false,
})

const precheck = reactive({ result: null, errorMessage: '' })

// 三个步骤组件共用同一份表单：注入而非逐字段 props 传递（见子组件内注释）
provide('restoreWizardForm', form)

// ==================== 派生状态 ====================
const selectedSet = computed(() => sets.value.find((item) => item.id === form.backup_set_id) || null)
// 注意：目标实例必须在向导层自己算一遍。此前这里引用了只在 StepTarget 内定义的
// selectedInstance，导致「确认并执行」步骤的目标实例栏恒为空。
const selectedInstance = computed(() => instances.value.find((item) => item.id === form.target_instance_id) || null)
const isPhysical = computed(() => String(objectsInfo.restore_kind || '').toUpperCase() === 'PHYSICAL')
const isMySQLPhysical = computed(
  () => isPhysical.value && String(selectedSet.value?.db_type || '').toUpperCase() === 'MYSQL',
)
const isFullBackup = computed(() => objectsInfo.is_full !== false)
const canSelectObjects = computed(() => !isPhysical.value && !!objectsInfo.supports_object_select)
const dbLabel = computed(() => (String(selectedSet.value?.db_type || '').toUpperCase() === 'POSTGRESQL' ? '库' : '数据库'))

const mappingRows = computed(() => {
  const source =
    form.object_scope === 'CUSTOM' ? form.selected_objects || [] : (objectsInfo.objects || []).map((item) => item.name)
  return (source || []).map((name) => ({ source_db: name, target_db: form.mapping?.[name] || '' }))
})

// 目标实例「名称」与「地址」拆成两栏：名称取备注/主机名，地址固定 host:port。
const instanceNameLabel = computed(() => {
  const item = selectedInstance.value
  if (!item) return '-'
  return item.remark || item.hostname || `实例 #${item.id}`
})

const instanceAddressLabel = computed(() => {
  const item = selectedInstance.value
  if (!item) return '-'
  return item.port ? `${item.hostname || ''}:${item.port}` : item.hostname || '-'
})

const selectedNode = computed(() => nodes.value.find((item) => item.id === form.target_node_id) || null)

const nodeLabel = computed(() => {
  const item = selectedNode.value
  return item ? item.name || item.hostname || `节点 #${item.id}` : '-'
})

const targetInstanceLabel = computed(() => {
  if (selectedInstance.value) return instanceNameLabel.value
  // PG 物理「拉起新实例」没有目标实例，落到执行节点上
  if (isPhysical.value && !isMySQLPhysical.value) return nodeLabel.value
  return '-'
})

const targetAddressLabel = computed(() => {
  if (selectedInstance.value) return instanceAddressLabel.value
  const node = selectedNode.value
  if (isPhysical.value && !isMySQLPhysical.value && node) {
    const host = node.hostname || node.name || ''
    return form.target_port ? `${host}:${form.target_port}` : host || '-'
  }
  return '-'
})

const targetLocationLabel = computed(() => {
  if (isPhysical.value) {
    return isMySQLPhysical.value ? `实例数据目录（${instanceNameLabel.value}）` : form.target_data_dir || '-'
  }
  if (!canSelectObjects.value) return form.target_db_name || '-'
  if (form.object_scope === 'CUSTOM') return `${(form.selected_objects || []).length} 个${dbLabel.value}`
  return `全部${dbLabel.value}`
})

// 源数据库 = 备份集登记的类型 + 服务端版本（备份那一刻抓到的版本，不随目标变化）。
const sourceDBLabel = computed(() => {
  const set = selectedSet.value
  if (!set) return '-'
  const type = String(set.db_type || '').trim()
  const version = String(set.server_version || '').trim()
  if (!type) return version || '-'
  return version ? `${type} ${version}` : type
})

// 目标数据库版本来自预检阶段实际探测的结果（预检未跑/失败时为空）。
const targetVersionLabel = computed(() => {
  const version = String(precheck.result?.resolved?.target_version || '').trim()
  if (!version) return '-'
  const type = String(selectedSet.value?.db_type || '').trim()
  return type ? `${type} ${version}` : version
})

const backupCreatedAtLabel = computed(() =>
  formatDateTime(selectedSet.value?.created_at || selectedSet.value?.start_at || selectedSet.value?.end_at),
)

const restoreContentLabel = computed(() => {
  if (isPhysical.value) return '整实例数据'
  return [form.restore_schema ? '表结构' : '', form.restore_data ? '全量数据' : ''].filter(Boolean).join(' + ') || '-'
})

const restoreModeText = computed(() =>
  isPhysical.value
    ? restoreModeLabel(isMySQLPhysical.value ? 'EXISTING_INSTANCE' : form.restore_mode)
    : restoreModeLabel(deriveRestoreMode(form.schema_conflict_policy, form.data_conflict_policy)),
)

const precheckLabel = computed(() => {
  if (uiState.precheckLoading) return '预检查中…'
  if (!precheck.result) return '未执行预检'
  return precheck.result.passed === false ? '⛔ 预检未通过' : '✅ 预检通过'
})

// 摘要固定 14 项：a-descriptions column=2 按行优先排布，即「项目 值 项目 值」两列一组。
const summaryItems = computed(() => [
  { label: '任务名称', value: form.name || '-' },
  {
    label: '备份集 ID',
    value: selectedSet.value ? selectedSet.value.backup_sets_id || `备份集 #${selectedSet.value.id}` : '-',
  },
  { label: '恢复方式', value: isPhysical.value ? '物理恢复' : '逻辑恢复' },
  { label: '备份类型', value: isFullBackup.value ? '全量备份' : '非全量备份' },
  { label: '备份创建时间', value: backupCreatedAtLabel.value },
  { label: '备份集大小', value: formatBytes(selectedSet.value?.size_bytes) },
  { label: '源数据库', value: sourceDBLabel.value },
  { label: '目标实例', value: targetInstanceLabel.value },
  { label: '目标实例地址', value: targetAddressLabel.value },
  { label: '目标数据库版本', value: targetVersionLabel.value },
  { label: '恢复内容', value: restoreContentLabel.value },
  { label: '目标位置', value: targetLocationLabel.value, danger: isPhysical.value },
  {
    label: '恢复模式',
    value: restoreModeText.value,
    danger: isPhysical.value || String(form.data_conflict_policy).toUpperCase() === 'TRUNCATE',
  },
  { label: '预检结果', value: precheckLabel.value, danger: precheck.result?.passed === false },
])

// ==================== 数据加载 ====================
const loadSets = async () => {
  uiState.setsLoading = true
  try {
    const res = await getBackupSetsApi(
      { is_page: false, restorable_only: true },
      { skipGlobalMessageError: true, silentHttpError: true },
    ).catch(() => null)
    sets.value = (res?.data || []).filter((item) =>
      ['LOGICAL', 'PG_BACKREST', 'XTRABACKUP'].includes(String(item.driver_type || '').toUpperCase()),
    )
  } finally {
    uiState.setsLoading = false
  }
}

const loadInstances = async () => {
  uiState.instancesLoading = true
  try {
    const res = await getBackupInstancesApi(
      { is_page: false },
      { skipGlobalMessageError: true, silentHttpError: true },
    ).catch(() => null)
    instances.value = res?.data || []
  } finally {
    uiState.instancesLoading = false
  }
}

const loadNodes = async () => {
  uiState.nodesLoading = true
  try {
    const res = await getBackupNodesApi(
      { is_page: false },
      { skipGlobalMessageError: true, silentHttpError: true },
    ).catch(() => null)
    nodes.value = res?.data || []
  } finally {
    uiState.nodesLoading = false
  }
}

const resetObjectsInfo = () => {
  Object.assign(objectsInfo, {
    restore_kind: '',
    backup_type: '',
    is_full: true,
    supports_object_select: false,
    objects: [],
    chain: [],
    hint: '',
    artifact_format: '',
  })
}

const loadObjects = async (id) => {
  resetObjectsInfo()
  if (!id) return
  try {
    const res = await getBackupSetObjectsApi(id)
    const payload = res?.data || {}
    Object.assign(objectsInfo, {
      restore_kind: payload.restore_kind || '',
      backup_type: payload.backup_type || '',
      is_full: payload.is_full !== false,
      supports_object_select: !!payload.supports_object_select,
      objects: Array.isArray(payload.objects) ? payload.objects : [],
      chain: Array.isArray(payload.chain) ? payload.chain : [],
      hint: payload.hint || '',
      artifact_format: payload.artifact_format || '',
    })
  } catch (err) {
    message.error(err?.response?.data?.message || '备份集对象解析失败')
  }
}

const handleSetChange = async (id) => {
  form.backup_set_id = id
  form.target_instance_id = null
  form.target_node_id = null
  form.target_db_name = ''
  form.confirm_host_port = ''
  form.selected_objects = []
  form.mapping = {}
  invalidatePrecheck()
  await loadObjects(id)
  if (isPhysical.value && !isMySQLPhysical.value && !nodes.value.length) {
    await loadNodes()
  }
  applyObjectDefaults()
}

const handleInstanceChange = () => {
  invalidatePrecheck()
}

const applyObjectDefaults = () => {
  if (!canSelectObjects.value) return
  form.selected_objects = (objectsInfo.objects || []).map((item) => item.name)
  const mapping = {}
  ;(objectsInfo.objects || []).forEach((item) => {
    mapping[item.name] = item.name
  })
  form.mapping = mapping
}

// ==================== 预检查 ====================
const invalidatePrecheck = () => {
  precheck.result = null
  precheck.errorMessage = ''
}

const buildPayload = () => {
  const base = { name: form.name || undefined, backup_set_id: form.backup_set_id }
  if (isPhysical.value) {
    const physical = {
      ...base,
      restore_mode: isMySQLPhysical.value ? 'EXISTING_INSTANCE' : form.restore_mode || 'NEW_INSTANCE',
      auto_start: form.auto_start,
    }
    if (isMySQLPhysical.value) {
      return { ...physical, target_instance_id: form.target_instance_id }
    }
    return {
      ...physical,
      target_instance_id: form.target_instance_id || undefined,
      target_node_id: form.target_node_id,
      target_data_dir: String(form.target_data_dir || '').trim(),
      target_port: form.target_port,
    }
  }
  return {
    ...base,
    target_instance_id: form.target_instance_id,
    target_db_name: canSelectObjects.value ? undefined : String(form.target_db_name || '').trim(),
    object_scope: form.object_scope,
    selected_databases: form.object_scope === 'CUSTOM' ? [...(form.selected_objects || [])] : [],
    db_mappings: mappingRows.value
      .filter((row) => String(row.target_db || '').trim())
      .map((row) => ({ source_db: row.source_db, target_db: String(row.target_db).trim() })),
    restore_schema: form.restore_schema,
    restore_data: form.restore_data,
    schema_conflict_policy: form.schema_conflict_policy,
    data_conflict_policy: form.data_conflict_policy,
  }
}

const runPrecheck = async () => {
  if (!form.backup_set_id) return
  uiState.precheckLoading = true
  invalidatePrecheck()
  try {
    const res = await precheckRestoreRunApi(buildPayload())
    precheck.result = res?.data || null
  } catch (err) {
    precheck.errorMessage = err?.response?.data?.message || err?.message || '预检查失败'
    precheck.result = null
  } finally {
    uiState.precheckLoading = false
  }
}

// 预检结果只在「第 3 步 + 表单未变」时有效：任何关键配置改动都作废。
watch(
  () => [
    form.backup_set_id,
    form.target_instance_id,
    form.target_node_id,
    form.target_db_name,
    form.target_data_dir,
    form.target_port,
    form.restore_mode,
    form.object_scope,
    form.schema_conflict_policy,
    form.data_conflict_policy,
    form.restore_schema,
    form.restore_data,
    form.selected_objects.join(','),
    JSON.stringify(form.mapping || {}),
  ],
  () => invalidatePrecheck(),
)

// ==================== 步骤校验 ====================
const canGoNext = computed(() => {
  if (currentStep.value === 0) return !!form.backup_set_id
  if (currentStep.value === 1) {
    if (isPhysical.value) {
      if (isMySQLPhysical.value) {
        return !!form.target_instance_id && !!targetStepRef.value?.hostPortMatched
      }
      return !!form.restore_mode && !!form.target_node_id && !!String(form.target_data_dir || '').trim() && !!form.target_port
    }
    if (!form.target_instance_id) return false
    if (!canSelectObjects.value && !String(form.target_db_name || '').trim()) return false
    if (!form.restore_schema && !form.restore_data) return false
    if (form.object_scope === 'CUSTOM' && !(form.selected_objects || []).length) return false
    return true
  }
  // 预检查步骤：必须等结果回来且无阻断项，才允许进入「确认并执行」。
  if (currentStep.value === 2) {
    return !uiState.precheckLoading && !!precheck.result && precheck.result.passed !== false
  }
  return true
})

const canSubmit = computed(() => {
  if (!precheck.result || precheck.result.passed === false) return false
  if (!form.execute_confirmed) return false
  if (isPhysical.value && String(form.confirm_text || '').trim() !== 'RECOVER') return false
  return true
})

// ==================== 流程控制 ====================
const handlePrev = () => {
  if (currentStep.value > 0) currentStep.value -= 1
}

const handleNext = async () => {
  if (!canGoNext.value) return
  currentStep.value += 1
  // 进入「恢复预检查」步骤即触发预检查；从后续步骤返回时若结果已失效也会重跑。
  if (currentStep.value === 2) {
    await runPrecheck()
  }
}

const handleCancel = () => {
  router.push({ path: '/backup/restore' })
}

const handleSubmit = async () => {
  if (!canSubmit.value) return
  uiState.submitting = true
  try {
    if (isPhysical.value) {
      const res = await createPhysicalRestoreRunApi({ ...buildPayload(), confirm_text: 'RECOVER' })
      const runID = res?.data?.id || res?.id || null
      message.success('物理恢复任务已提交')
      await router.push(runID ? { path: `/backup/restore/${runID}`, query: { kind: 'physical' } } : { path: '/backup/restore' })
      return
    }
    const res = await createRestoreRunApi({
      ...buildPayload(),
      mode: 'execute',
      execute_confirmed: true,
    })
    const runID = res?.data?.id || res?.data?.ID || null
    message.success('恢复任务已启动')
    await router.push(runID ? { path: `/backup/restore/${runID}`, query: { kind: 'logical' } } : { path: '/backup/restore' })
  } catch (err) {
    message.error(err?.response?.data?.message || err?.message || '恢复任务提交失败')
  } finally {
    uiState.submitting = false
  }
}

onMounted(async () => {
  uiState.pageLoading = true
  try {
    await Promise.allSettled([loadSets(), loadInstances(), loadNodes()])
    const presetID = Number(route.query.backup_set_id || 0) || null
    if (presetID) {
      form.backup_set_id = presetID
      await loadObjects(presetID)
      applyObjectDefaults()
    }
  } finally {
    uiState.pageLoading = false
  }
})
</script>

<style scoped lang="scss">
.restore-wizard-page {
  display: flex;
  flex-direction: column;
  gap: var(--gi-spacing-md, 16px);
  height: 100%;
  min-height: 0;
}

.wizard-steps-card {
  flex: 0 0 auto;
  border-radius: var(--gi-radius-card, 12px);
  box-shadow: var(--gi-shadow-sm);
}

.wizard-main-card {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
  border-radius: var(--gi-radius-card, 12px);
  box-shadow: var(--gi-shadow-sm);
}

.wizard-main-card :deep(.ant-card-body) {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
}

.wizard-footer {
  flex: 0 0 auto;
  display: flex;
  justify-content: flex-end;
  padding: var(--gi-spacing-md, 16px);
  background: var(--gi-color-container-bg, #fff);
  border-radius: var(--gi-radius-card, 12px);
  box-shadow: var(--gi-shadow-sm);
}

@media (max-width: 768px) {
  /* 4 步水平步骤条在窄屏挤压：改为纵向排布（纯 CSS，不动模板） */
  :deep(.ant-steps-horizontal) {
    display: block;
  }

  :deep(.ant-steps-horizontal .ant-steps-item) {
    display: block;
    overflow: visible;
    min-width: 0;
  }

  :deep(.ant-steps-horizontal .ant-steps-item-container) {
    display: block;
    align-items: flex-start;
  }

  :deep(.ant-steps-horizontal .ant-steps-item-icon) {
    float: left;
    margin-right: 10px;
  }

  :deep(.ant-steps-horizontal .ant-steps-item-content) {
    display: block;
    min-height: 44px;
  }

  :deep(.ant-steps-horizontal .ant-steps-item-title) {
    font-size: 13px;
    line-height: 22px;
    white-space: normal;
    padding-right: 0;
  }

  :deep(.ant-steps-horizontal .ant-steps-item::after),
  :deep(.ant-steps-horizontal .ant-steps-item-tail) {
    display: none;
  }

  /* 底部按钮平分一行 */
  .wizard-footer {
    padding: 12px;
  }

  .wizard-footer :deep(.ant-space) {
    width: 100%;
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(88px, 1fr));
    gap: 8px;
  }

  .wizard-footer :deep(.ant-space-item),
  .wizard-footer :deep(.ant-btn) {
    width: 100%;
  }

  .restore-wizard-page {
    height: auto;
    min-height: 100%;
  }
}
</style>
