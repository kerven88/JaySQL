<template>
  <div class="gi-page-shell backup-sets-page">
    <div class="gi-page-toolbar">
      <div class="gi-page-title">备份集</div>
      <a-space>
        <!-- 数据源筛选：默认「全部」，按实例过滤（后端 instance_id 口径，分页仍在服务端生效） -->
        <DataSourceFilter
          v-model:value="query.instance_id"
          :instances="instanceOptions"
          :loading="uiState.instancesLoading"
          @change="handleDataSourceChange"
        />
        <a-input-search
          v-model:value="query.search"
          placeholder="搜索备份集ID/任务ID/数据源"
          class="search-input"
          @search="handleSearch"
        />
        <!-- 批量删除：仅在勾选数据后显示，样式与【数据备份】页保持一致（danger 描边按钮 + 数量） -->
        <a-button
          v-if="canManageBackupSet && selectedRowKeys.length > 0"
          danger
          :disabled="selectedRowKeys.length === 0"
          :loading="uiState.batchDeleting"
          @click="confirmBatchDelete"
        >
          批量删除 ({{ selectedRowKeys.length }})
        </a-button>
      </a-space>
    </div>

    <a-card :bordered="false" class="gi-plain-card">
      <a-table
        :columns="columns"
        :data-source="tableData"
        :loading="uiState.loading"
        :pagination="pagination"
        row-key="id"
        :scroll="{ x: 1381, y: 'calc(100vh - 300px)' }"
        :row-selection="rowSelection"
        @change="handleTableChange"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'backup_sets_id'">
            <div class="id-cell">
              <a-button
                type="link"
                size="small"
                class="backup-set-id-link"
                @click="openDetail(record)"
              >
                {{ record.backup_sets_id || '-' }}
              </a-button>
              <CopyOutlined class="copy-icon" @click.stop="copyText(record.backup_sets_id, '备份集ID已复制')" />
            </div>
          </template>

          <template v-else-if="column.key === 'task_id'">
            <div class="id-cell">
              <a-button
                type="link"
                size="small"
                class="task-id-link"
                @click="handleOpenTask(record)"
              >
                {{ record.task_id || '-' }}
              </a-button>
              <CopyOutlined class="copy-icon" @click.stop="copyText(record.task_id, '任务ID已复制')" />
            </div>
          </template>

          <template v-else-if="column.key === 'source'">
            <!-- 数据库类型图标 + 实例名称/地址，图标展现方式与【数据备份】页数据源列一致 -->
            <div class="data-source-cell">
              <img
                class="db-logo"
                :src="dbTypeLogo(record.db_type)"
                :alt="record.db_type || 'db'"
              />
              <div class="data-source-text">
                <!-- 实例名称最多显示 15 个字符，超出用 … 截断，鼠标悬停显示全部 -->
                <DataSourceName :name="record.instance_remark" :max="15" />
                <div class="source-secondary">{{ formatHostPort(record.instance_host, record.instance_port) }}</div>
              </div>
            </div>
          </template>

          <template v-else-if="column.key === 'size'">
            {{ formatBytes(record.size_bytes) }}
          </template>

          <template v-else-if="column.key === 'storage_target'">
            <a-tooltip v-if="record.storage_target_id" :title="storageTargetTooltip(record.storage_target_id)">
              <span class="storage-target-name">{{ storageTargetName(record.storage_target_id) }}</span>
            </a-tooltip>
            <span v-else class="storage-target-empty">-</span>
          </template>

          <template v-else-if="column.key === 'start_at'">
            {{ formatDateTime(record.start_at || record.created_at) }}
          </template>

          <template v-else-if="column.key === 'status'">
            <span :class="['status-text', `is-${String(record.status || '').toLowerCase()}`]">
              {{ statusLabel(record.status) }}
            </span>
          </template>

          <template v-else-if="column.key === 'actions'">
            <div class="action-cell">
              <template v-if="record.status === 'RUNNING'">
                <a-button
                  v-if="canManageBackupSet"
                  type="link"
                  size="small"
                  danger
                  class="action-link"
                  @click="handleStop(record)"
                >
                  终止
                </a-button>
                <a-popconfirm
                  v-if="canManageBackupSet"
                  title="确认删除该备份集吗？"
                  ok-text="确认"
                  cancel-text="取消"
                  @confirm="handleDelete(record)"
                >
                  <a-button type="link" size="small" danger class="action-link danger-link">删除</a-button>
                </a-popconfirm>
              </template>

              <template v-else-if="record.status === 'COMPLETED'">
                <a-button
                  v-if="canExecuteRestore"
                  type="link"
                  size="small"
                  class="action-link"
                  @click="handleRestore(record)"
                >
                  恢复
                </a-button>
                <a-dropdown
                  v-if="canViewDetail || canManageBackupSet"
                  placement="bottomRight"
                  trigger="click"
                >
                  <a-button type="link" size="small" class="more-action-btn">更多</a-button>
                  <template #overlay>
                    <a-menu>
                      <a-menu-item v-if="canViewDetail" @click="openDetail(record)">详情</a-menu-item>
                      <a-menu-item v-if="canManageBackupSet" danger @click="confirmDelete(record)">删除</a-menu-item>
                    </a-menu>
                  </template>
                </a-dropdown>
              </template>

              <template v-else>
                <a-popconfirm
                  v-if="canManageBackupSet"
                  title="确认删除该备份集吗？"
                  ok-text="确认"
                  cancel-text="取消"
                  @confirm="handleDelete(record)"
                >
                  <a-button type="link" size="small" danger class="action-link danger-link">删除</a-button>
                </a-popconfirm>
              </template>

              <span v-if="!hasVisibleAction(record)" class="muted-text">只读可见</span>
            </div>
          </template>
        </template>
      </a-table>
    </a-card>

    <a-modal
      :open="detailState.open"
      title="备份集详情"
      width="760px"
      centered
      :footer="null"
      :body-style="{ maxHeight: 'calc(100vh - 220px)', overflowY: 'auto' }"
      @cancel="closeDetail"
    >
      <a-skeleton v-if="detailState.loading && !detailState.record" active :paragraph="{ rows: 8 }" />
      <template v-else-if="detailState.record">
        <div class="detail-status-row">
          <span :class="['status-pill', `is-${String(detailState.record.status || '').toLowerCase()}`]">
            {{ statusLabel(detailState.record.status) }}
          </span>
          <span class="detail-id">{{ detailState.record.backup_sets_id || '-' }}</span>
        </div>

        <a-descriptions :column="2" size="small" bordered class="detail-descriptions">
          <a-descriptions-item label="备份集ID">{{ detailState.record.backup_sets_id || '-' }}</a-descriptions-item>
          <a-descriptions-item label="任务ID">{{ detailState.record.task_id || '-' }}</a-descriptions-item>
          <a-descriptions-item label="数据源IP">
            <span :title="formatHostPort(detailState.record.instance_host, detailState.record.instance_port)">
              {{ clipText(formatHostPort(detailState.record.instance_host, detailState.record.instance_port)) || '-' }}
            </span>
          </a-descriptions-item>
          <a-descriptions-item label="数据源名称">
            <span :title="detailState.record.instance_remark || ''">
              {{ clipText(detailState.record.instance_remark) || '-' }}
            </span>
          </a-descriptions-item>
          <a-descriptions-item label="备份集大小">{{ formatBytes(detailState.record.size_bytes) }}</a-descriptions-item>
          <a-descriptions-item label="创建人">{{ detailState.record.created_by || '-' }}</a-descriptions-item>
          <a-descriptions-item label="备份开始时间">{{ formatDateTime(detailState.record.start_at || detailState.record.created_at) }}</a-descriptions-item>
          <a-descriptions-item label="结束时间">{{ formatDateTime(detailState.record.end_at) }}</a-descriptions-item>
          <a-descriptions-item label="备份方式">{{ detailState.record.driver_type || '-' }}</a-descriptions-item>
          <a-descriptions-item label="备份类型">{{ detailState.record.backup_type || '-' }}</a-descriptions-item>
          <a-descriptions-item label="产物文件" :span="2">{{ detailState.record.file_name || '-' }}</a-descriptions-item>
          <a-descriptions-item label="产物路径" :span="2">
            <span class="detail-path-text">{{ formatPathWithTarget(detailState.record.file_path, detailState.record.storage_target_id) }}</span>
          </a-descriptions-item>
          <a-descriptions-item label="日志路径" :span="2">
            <span class="detail-path-text">{{ formatPathWithTarget(detailState.record.log_path, detailState.record.storage_target_id) }}</span>
          </a-descriptions-item>
          <a-descriptions-item label="对象信息" :span="2">{{ detailState.record.object_summary || '-' }}</a-descriptions-item>
          <a-descriptions-item label="输出目录" :span="2">{{ detailState.record.output_dir || '-' }}</a-descriptions-item>
          <a-descriptions-item label="错误信息" :span="2">
            <div class="detail-error-text">{{ detailState.record.error_message || '-' }}</div>
          </a-descriptions-item>
        </a-descriptions>
      </template>
    </a-modal>
  </div>
</template>

<script setup>
import { computed, h, inject, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { CopyOutlined } from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import { batchDeleteBackupSetsApi, deleteBackupSetApi, getBackupInstancesApi, getBackupSetDetailApi, getBackupSetsApi, getStorageTargetsApi, stopBackupSetApi } from '@/api/backup'
import DataSourceName from '@/components/patterns/DataSourceName.vue'
import DataSourceFilter from '@/components/patterns/DataSourceFilter.vue'
import { dbTypeLogo } from '@/utils/dbTypeLogo'
import { extractStorageTargetId, formatStoragePath } from '@/utils/storagePath'
import { useUserStore } from '@/store/user'
import { useRoute, useRouter } from 'vue-router'

defineOptions({ name: 'BackupSetsView' })

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()

const hasPermission = (permission) => {
  if (userStore?.is_superuser === true) {
    return true
  }
  if (!permission) {
    return true
  }
  const permissions = Array.isArray(userStore?.permissions) ? userStore.permissions : []
  return permissions.includes(permission)
}

const canViewDetail = computed(() => hasPermission('backup.set.view'))
const canManageBackupSet = computed(() => hasPermission('backup.task.manage'))
const canExecuteRestore = computed(() => hasPermission('backup.restore.execute'))

const uiState = reactive({
  loading: false,
  batchDeleting: false,
  instancesLoading: false,
})

const detailState = reactive({
  open: false,
  loading: false,
  record: null,
})

const query = reactive({
  search: '',
  // 数据源筛选：0 表示「全部」，与后端「InstanceID != 0 才过滤」口径一致
  instance_id: 0,
  page: 1,
  page_size: 10,
  is_page: true,
})

// 数据源下拉候选项：与【数据备份】页取同一份实例接口（MySQL/PostgreSQL）
const instanceOptions = ref([])

const loadInstances = async () => {
  uiState.instancesLoading = true
  try {
    const res = await getBackupInstancesApi(
      { is_page: false },
      { skipGlobalMessageError: true, silentHttpError: true },
    ).catch(() => null)
    instanceOptions.value = res?.data || []
  } finally {
    uiState.instancesLoading = false
  }
}

const tableData = ref([])
const total = ref(0)
const stopSetRequestInFlight = ref(false)
const selectedRowKeys = ref([])
let refreshTimer = null
const copyToastState = reactive({
  text: '',
  at: 0,
})

const columns = [
  { title: '备份集 ID', dataIndex: 'backup_sets_id', key: 'backup_sets_id', width: 188 },
  { title: '任务 ID', dataIndex: 'task_id', key: 'task_id', width: 171 },
  // 数据源列带类型图标（原创建人列删除后其宽度补给此列）
  { title: '数据源', key: 'source', width: 280 },
  { title: '备份集大小', key: 'size', width: 137 },
  { title: '存储目标', key: 'storage_target', width: 190 },
  { title: '备份开始时间', key: 'start_at', width: 178 },
  // 状态列表头与表体内容水平居中，与「数据恢复」页面保持一致
  { title: '状态', key: 'status', width: 117, fixed: 'right', align: 'center' },
  // 操作列表头与表体内容水平居中，与「数据备份」「数据恢复」页面保持一致
  { title: '操作', key: 'actions', width: 120, fixed: 'right', align: 'center' },
]

const rowSelection = computed(() => ({
  selectedRowKeys: selectedRowKeys.value,
  onChange: (keys) => {
    selectedRowKeys.value = keys
  },
}))

const pagination = computed(() => ({
  total: total.value,
  current: query.page,
  pageSize: query.page_size,
  pageSizeOptions: ['10', '20', '50', '100'],
  showSizeChanger: true,
  showTotal: (t) => `共 ${t} 条`,
}))

const storageTargets = ref([])
const storageTargetMap = computed(() => {
  const map = new Map()
  storageTargets.value.forEach((t) => {
    map.set(t.id, t)
  })
  return map
})
const storageTargetById = (id) => {
  const sid = Number(id) || 0
  if (!sid) return null
  return storageTargetMap.value.get(sid) || null
}

// 仅显示存储目标名称；未关联（无 storage_target_id）由模板显示 "-"
const storageTargetName = (id) => {
  const sid = Number(id) || 0
  const t = storageTargetById(id)
  if (!t) return `存储目标 #${sid}`
  return `${t.name || `存储目标 #${sid}`}`
}

// 悬停提示仅展示关联地址（IP/Endpoint）
const storageTargetTooltip = (id) => {
  const t = storageTargetById(id)
  if (!t) return ''
  const type = String(t.type || '').toUpperCase()
  if (type === 'LOCAL_SERVER') {
    const host = String(t.host || '').trim()
    const port = Number(t.port) || 0
    if (!host) return ''
    return port > 0 ? `${host}:${port}` : host
  }
  const endpoint = String(t.s3_endpoint || '').trim()
  const bucket = String(t.s3_bucket || '').trim()
  return [endpoint, bucket].filter(Boolean).join(' / ')
}
const loadStorageTargets = async () => {
  const res = await getStorageTargetsApi({ is_page: false }, { skipGlobalMessageError: true, silentHttpError: true }).catch(() => null)
  storageTargets.value = res?.data || []
}

// 与【数据备份】【数据恢复】两页一致：本页自己管滚动（页面容器 height:100% + overflow:hidden），
// 向公共布局申请「内容区不滚动」，保证三页内容区留白与标题左上角起点完全一致。
const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)

onMounted(async () => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(true)
  }
  await loadStorageTargets()
  await loadInstances()
})

const shouldAutoRefresh = computed(() => (tableData.value || []).some((item) => item?.status === 'RUNNING'))

const pad = (n) => (n < 10 ? `0${n}` : `${n}`)

const formatDateTime = (value) => {
  if (!value) return '-'
  const d = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(Number(d))) {
    return String(value || '').replace('T', ' ').slice(0, 16) || '-'
  }
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const formatBytes = (bytes = 0) => {
  const b = Number(bytes) || 0
  if (b < 1024) return `${b} B`
  const kb = b / 1024
  if (kb < 1024) return `${kb.toFixed(1)} KB`
  const mb = kb / 1024
  if (mb < 1024) return `${mb.toFixed(1)} MB`
  const gb = mb / 1024
  if (gb < 1024) return `${gb.toFixed(1)} GB`
  return `${(gb / 1024).toFixed(1)} TB`
}

const formatHostPort = (host, port) => {
  const h = String(host || '').trim()
  const p = Number(port || 0)
  if (!h) return '-'
  return p > 0 ? `${h}:${p}` : h
}

// 详情弹窗字段统一 20 字符截断，悬浮 :title 看全文
const clipText = (value, limit = 20) => {
  const text = String(value || '').trim()
  if (!text) return ''
  return text.length > limit ? `${text.slice(0, limit)}...` : text
}

// 产物/日志路径展示规则（拼接实现统一收敛在 utils/storagePath.js，与【数据备份】页口径一致）：
// 1) st:<id>:///<rel> 存储URI → 解析为「地址:根路径/相对路径」
//    本地：192.168.1.99:/backup/ds-xxx/...；对象存储：oss-cn-hangzhou.aliyuncs.com:/bucket/ds-xxx/...
// 2) 绝对路径 → 前缀「地址:」（地址按路径自带 st:<id> 或备份集的 storage_target_id 解析）
// 3) 地址为空（目标已删除/未配置地址）→ 只显示路径；空值 → '-'
const formatPathWithTarget = (value, fallbackTargetId) => {
  const sid = extractStorageTargetId(value) || Number(fallbackTargetId) || 0
  return formatStoragePath(value, storageTargetById(sid))
}

const statusLabel = (status) => {
  if (status === 'RUNNING') return '备份中'
  if (status === 'COMPLETED') return '已完成'
  return '备份失败'
}

const copyText = async (text, successMessage = '复制成功') => {
  const value = String(text || '')
  if (!value) {
    message.warning('没有可复制的内容')
    return
  }
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(value)
    } else if (typeof document !== 'undefined') {
      const textarea = document.createElement('textarea')
      textarea.value = value
      textarea.setAttribute('readonly', 'readonly')
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
    }
    const now = Date.now()
    if (copyToastState.text !== value || now - copyToastState.at > 1500) {
      message.success(successMessage)
      copyToastState.text = value
      copyToastState.at = now
    }
  } catch {
    message.error('复制失败')
  }
}

const fetchSets = async () => {
  uiState.loading = true
  try {
    const res = await getBackupSetsApi({ ...query })
    tableData.value = res?.data || []
    total.value = res?.total || 0
    selectedRowKeys.value = []
  } finally {
    uiState.loading = false
  }
}

const handleSearch = () => {
  query.page = 1
  fetchSets()
}

// 数据源切换：回到第一页并重新拉取（与搜索同一处理口径）
const handleDataSourceChange = () => {
  handleSearch()
}

const handleTableChange = (pager) => {
  query.page = pager.current || 1
  query.page_size = pager.pageSize || 10
  fetchSets()
}

const closeDetail = () => {
  detailState.open = false
  detailState.record = null
}

const openDetail = async (record) => {
  detailState.open = true
  detailState.loading = true
  if (!detailState.record || detailState.record.id !== record.id) {
    detailState.record = null
  }
  try {
    const res = await getBackupSetDetailApi(record.id)
    detailState.record = res?.data?.set || null
  } finally {
    detailState.loading = false
  }
}

const handleRestore = (record) => {
  router.push({
    path: '/backup/restore/wizard',
    query: { backup_set_id: String(record.id) },
  })
}

const handleOpenTask = (record) => {
  router.push({
    path: '/backup/task',
    query: {
      search: String(record?.task_id || '').trim(),
    },
  })
}

const buildSetStopConfirmConfig = (record) => {
  const rawStatus = String(record?.raw_status || record?.status || '').toUpperCase()
  if (rawStatus === 'PENDING') {
    return {
      title: '终止备份',
      content: '当前备份仍在排队，确认后会直接终止，不会真正启动备份进程。是否继续？',
      okText: '确认终止',
      okType: 'danger',
    }
  }
  if (rawStatus === 'STOP_REQUESTED') {
    return {
      title: record?.stop_state_label || '终止备份',
      content: record?.stop_state_message || '当前已收到终止请求，请稍后刷新查看结果。',
      okText: '刷新状态',
      okType: record?.stop_state === 'UNABLE' ? 'default' : 'primary',
      alreadyRequested: true,
    }
  }
  if (record?.stop_state === 'UNABLE') {
    return {
      title: record?.stop_state_label || '终止备份',
      content: `${record?.stop_state_message || '当前可能无法真正终止。'}${record?.stop_state_hint ? `\n\n${record?.stop_state_hint}` : ''}\n\n是否仍发送终止请求？`,
      okText: '继续发送终止请求',
      okType: 'danger',
    }
  }
  if (record?.stop_state === 'UNINTERRUPTIBLE') {
    return {
      title: record?.stop_state_label || '终止备份',
      content: `${record?.stop_state_message || '当前阶段暂时不能安全中断。'}${record?.stop_state_hint ? `\n\n${record?.stop_state_hint}` : ''}\n\n是否继续发送终止请求？`,
      okText: '继续发送终止请求',
      okType: 'danger',
    }
  }
  return {
    title: '终止备份',
    content: '终止后会立即中止当前备份，本次不会生成可用备份集。是否继续？',
    okText: '确认终止',
    okType: 'danger',
  }
}

const handleStop = (record) => {
  if (!record?.id || stopSetRequestInFlight.value) return
  const stopConfig = buildSetStopConfirmConfig(record)
  const confirmed = Modal.confirm({
    title: stopConfig.title,
    content: stopConfig.content,
    okText: stopConfig.okText,
    okType: stopConfig.okType,
    cancelText: '取消',
    async onOk() {
      if (stopConfig.alreadyRequested) {
        try { await fetchSets() } catch (_) {}
        try { confirmed.destroy?.() } catch (_) {}
        try { Modal.destroyAll() } catch (_) {}
        return
      }
      stopSetRequestInFlight.value = true
      try {
        const res = await stopBackupSetApi(record.id)
        const result = res?.data || {}
        const rawStatus = String(record?.raw_status || record?.status || '').toUpperCase()
        if (result.status === 'STOPPED') try { message.success(result.stop_state_message || '已终止当前备份') } catch (_) {}
        else if (result.stop_state === 'UNABLE') try { message.warning(result.stop_state_message || '终止请求已记录，但当前可能无法真正终止') } catch (_) {}
        else if (result.stop_state === 'UNINTERRUPTIBLE') try { message.warning(result.stop_state_message || '已记录终止请求，当前阶段不可立即中断') } catch (_) {}
        else try { message.success(result.stop_state_message || (rawStatus === 'PENDING' ? '已终止排队中的备份任务' : '已发送终止请求，正在终止当前备份')) } catch (_) {}
        try { await fetchSets() } catch (_) {}
      } catch (err) {
        stopSetRequestInFlight.value = false
        try { message.error(err?.msg || err?.message || '终止失败，请稍后重试') } catch (_) {}
        try { confirmed.destroy?.() } catch (_) {}
        try { Modal.destroyAll() } catch (_) {}
        return Promise.reject(err)
      } finally { stopSetRequestInFlight.value = false }
      try { confirmed.destroy?.() } catch (_) {}
      try { Modal.destroyAll() } catch (_) {}
    },
  })
}

const handleDelete = async (record) => {
  if (record.status === 'RUNNING') {
    const stopRes = await stopBackupSetApi(record.id)
    const stopStatus = String(stopRes?.data?.status || '').toUpperCase()
    if (stopStatus !== 'STOPPED') {
      message.warning('当前备份正在终止，请待任务结束后再删除。')
      await fetchSets()
      return
    }
  }
  await deleteBackupSetApi(record.id)
  message.success('删除成功')
  if (detailState.record?.id === record.id) {
    closeDetail()
  }
  await fetchSets()
}

const confirmDelete = (record) => {
  Modal.confirm({
    title: '确认删除该备份集吗？',
    content: '删除后无法恢复，同时会清理该备份集在存储目标上的备份文件、日志与参数配置文件。',
    okText: '确认',
    cancelText: '取消',
    okType: 'danger',
    onOk: () => handleDelete(record),
  })
}

const confirmBatchDelete = () => {
  if (selectedRowKeys.value.length === 0) {
    message.warning('请先选择要删除的备份集')
    return
  }
  Modal.confirm({
    title: `确认批量删除 ${selectedRowKeys.value.length} 个备份集吗？`,
    content: '删除后无法恢复，同时会清理这些备份集在存储目标上的备份文件、日志与参数配置文件，是否继续？',
    okText: '确认删除',
    cancelText: '取消',
    okType: 'danger',
    async onOk() {
      await handleBatchDelete()
    },
  })
}

const handleBatchDelete = async () => {
  if (selectedRowKeys.value.length === 0) return
  const ids = selectedRowKeys.value.map((key) => Number(key)).filter((id) => Number.isFinite(id) && id > 0)
  if (ids.length === 0) return
  uiState.batchDeleting = true
  try {
    const res = await batchDeleteBackupSetsApi({ ids })
    const result = res?.data || {}
    const deleted = Number(result.deleted_count) || 0
    const failed = Number(result.failed_count) || 0
    const failures = Array.isArray(result.failures) ? result.failures : []
    if (failed === 0) {
      message.success(`批量删除成功，共删除 ${deleted} 个备份集`)
    } else if (deleted === 0) {
      message.error(`批量删除失败：${failures[0]?.message || '请稍后重试'}`)
    } else {
      message.warning(`部分删除成功：成功 ${deleted} 个，失败 ${failed} 个`)
      Modal.info({
        title: '部分备份集删除失败',
        width: 560,
        content: () =>
          h(
            'div',
            { class: 'batch-delete-failures' },
            failures.map((item, index) =>
              h('div', { key: index, class: 'batch-delete-failure-item' }, `${item.backup_sets_id || `#${item.id}`}：${item.message || '删除失败'}`),
            ),
          ),
      })
    }
    if (detailState.record && ids.includes(Number(detailState.record.id))) {
      closeDetail()
    }
  } catch (err) {
    message.error(err?.message || '批量删除失败，请稍后重试')
  } finally {
    uiState.batchDeleting = false
    await fetchSets()
  }
}

const hasVisibleAction = (record) => {
  if (record.status === 'RUNNING') {
    return canManageBackupSet.value
  }
  if (record.status === 'COMPLETED') {
    return canExecuteRestore.value || canViewDetail.value || canManageBackupSet.value
  }
  return canManageBackupSet.value
}

const setupRefreshTimer = () => {
  if (refreshTimer) {
    window.clearInterval(refreshTimer)
    refreshTimer = null
  }
  refreshTimer = window.setInterval(() => {
    if (!shouldAutoRefresh.value) return
    fetchSets()
  }, 5000)
}

watch(
  () => route.query.search,
  () => {
    query.search = String(route.query.search || '').trim()
    query.page = 1
    fetchSets()
  },
  { immediate: true },
)

watch(shouldAutoRefresh, () => {
  setupRefreshTimer()
})

onBeforeUnmount(() => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(false)
  }
  if (refreshTimer) {
    window.clearInterval(refreshTimer)
    refreshTimer = null
  }
})
</script>

<style scoped>
.backup-sets-page {
  height: 100%;
  display: flex;
  flex-direction: column;
  /* 与【数据备份】【数据恢复】两页一致：标题到卡片统一 --gi-spacing-ssm(12px)。
     页面四周留白统一交给 Layout 内容区（.layout-content, 24px），此处不再叠加自己的 padding，
     否则标题左上角起点会比另两页多出 10px(上)/16px(左)，三页标题无法对齐。 */
  gap: var(--gi-spacing-ssm);
  box-sizing: border-box;
  overflow: hidden;
}

/* 页面主标题：统一使用公共样式 .gi-page-title，不再重复定义。 */

.search-input {
  width: 320px;
}

.gi-plain-card {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 12px;
}

.gi-plain-card :deep(.ant-card-body) {
  flex: 1 1 auto;
  min-height: 0;
  padding: 10px 14px 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.gi-plain-card :deep(.ant-table-wrapper),
.gi-plain-card :deep(.ant-spin-nested-loading),
.gi-plain-card :deep(.ant-spin-container),
.gi-plain-card :deep(.ant-table),
.gi-plain-card :deep(.ant-table-container) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.gi-plain-card :deep(.ant-table-body) {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto !important;
}

/* 数据源列：类型图标 + 名称/地址，参数与【数据备份】页数据源列一致 */
.data-source-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.data-source-text {
  min-width: 0;
}

.db-logo {
  /* 全站统一：数据库类型 logo 一律 20px */
  width: 20px;
  height: 20px;
  flex: none;
  object-fit: contain;
}

/* 覆盖 DataSourceName 组件默认的小号淡灰（12px / #748893），保持本列原有字号与颜色 */
.data-source-text :deep(.gi-data-source-name) {
  color: #1f2d38;
  font-size: 14px;
  line-height: 22px;
}

.id-cell {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.backup-set-id-link {
  padding: 0;
  color: #0f766e;
}

.task-id-text {
  color: #1f2d38;
}

.task-id-link {
  padding: 0;
  color: #1f2d38;
}

.task-id-link:hover,
.task-id-link:focus {
  color: #1f2d38;
}

.backup-set-id-link:hover,
.backup-set-id-link:focus {
  color: #0f766e;
}

.copy-icon {
  flex: none;
  cursor: pointer;
  color: #8ca0ad;
  font-size: 13px;
  transition: color 0.15s ease;
}

.copy-icon:hover {
  color: #0f766e;
}

.source-secondary {
  margin-top: 2px;
  font-size: 12px;
  color: #8a97a1;
}

.muted-text {
  color: #0f766e;
}

.detail-id,
.detail-error-text {
  color: #8a97a1;
}

/* 状态列：纯文字（去掉圆角胶囊底框），仅保留状态色；详情抽屉仍用 .status-pill */
.status-text {
  font-size: 12px;
  line-height: 20px;
}

.status-text.is-running {
  color: #0f766e;
}

.status-text.is-completed {
  color: #166534;
}

.status-text.is-failed {
  color: #b42318;
}

/* 详情抽屉的状态胶囊（非表格状态列，保持原样式） */
.status-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 68px;
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 12px;
  line-height: 20px;
  border: 1px solid transparent;
}

.status-pill.is-running {
  color: #0f766e;
  background: rgba(15, 118, 110, 0.08);
  border-color: rgba(15, 118, 110, 0.2);
}

.status-pill.is-completed {
  color: #166534;
  background: rgba(22, 101, 52, 0.08);
  border-color: rgba(22, 101, 52, 0.18);
}

.status-pill.is-failed {
  color: #b42318;
  background: rgba(180, 35, 24, 0.08);
  border-color: rgba(180, 35, 24, 0.18);
}

/* 操作列内容水平居中（flex 容器需显式居中） */
.action-cell {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
}

.action-link {
  padding: 0 4px;
  color: #0f766e;
}

.action-link:hover,
.action-link:focus {
  color: #0f766e;
}

/* 【更多】按钮字体样式与【数据备份】页操作列保持一致 */
.more-action-btn {
  padding-inline: 4px;
  color: #667784;
  font-size: 12px;
  font-weight: 400;
}

.more-action-btn:hover,
.more-action-btn:focus {
  color: #1f2d38;
}

.danger-link {
  color: #cf1322;
}

.detail-status-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}

.detail-descriptions {
  margin-top: 4px;
}

.detail-error-text {
  white-space: pre-wrap;
  word-break: break-all;
}

.detail-path-text {
  word-break: break-all;
}

.storage-target-name {
  display: inline-block;
  max-width: 100%;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: middle;
}

.storage-target-empty {
  color: #c9cdd4;
}

.batch-delete-failures {
  max-height: 320px;
  overflow-y: auto;
}

.batch-delete-failure-item {
  padding: 4px 0;
  color: #b42318;
  font-size: 13px;
  line-height: 1.6;
  word-break: break-all;
}

@media (max-width: 768px) {
  .backup-sets-page {
    height: auto;
    min-height: 100%;
    overflow: visible;
  }

  :deep(.ant-space) {
    flex-wrap: wrap;
    row-gap: 8px;
  }

  /* 搜索框写死 320px 且无 max-width 兜底 */
  .search-input {
    width: 100%;
    max-width: 100%;
  }

  /* 表格 y 值在脚本里，这里放开容器限高并用 dvh 重算 */
  .gi-plain-card :deep(.ant-table-body) {
    max-height: calc(100dvh - 240px) !important;
    min-height: 220px;
  }
}
</style>
