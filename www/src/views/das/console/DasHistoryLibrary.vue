<template>
  <div ref="panelRef" class="library-panel">
    <div ref="toolbarRef" class="library-toolbar">
      <a-range-picker
        v-model:value="uiData.dateRange"
        class="library-date-range"
        size="small"
        format="YYYY-MM-DD"
        :allow-clear="false"
        @change="handleDateRangeChange"
      />
      <a-input-search
        v-model:value="uiData.searchValue"
        allow-clear
        class="library-search"
        placeholder="搜索 SQL 历史"
        @search="handleSearch"
      />
    </div>

    <a-table
      size="small"
      :columns="uiData.tableColumns"
      :row-key="(record) => record.id || record.key"
      :data-source="uiData.tableData"
      :pagination="pagination"
      :loading="uiState.loading"
      :scroll="{ x: 1120, y: uiState.tableScrollY }"
      @change="handleTableChange"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'instance_id'">
          <div class="instance-cell">
            <div class="instance-name">{{ getInstanceDisplayName(record) }}</div>
            <div class="instance-address">{{ getInstanceAddress(record) }}</div>
          </div>
        </template>
        <template v-else-if="column.key === 'sqltext'">
          <a class="view-link" @click="showSqlDetail(record)">查看</a>
          <span class="sql-preview">{{ record.sqltext }}</span>
        </template>
        <template v-else-if="column.key === 'created_at'">
          {{ formatCreatedAt(record.created_at) }}
        </template>
        <template v-else-if="column.key === 'duration'">
          <span class="duration-tag" :class="getDurationClass(record.duration)">
            {{ record.duration }} ms
          </span>
        </template>
        <template v-else-if="column.key === 'error_msg'">
          <span class="status-tag" :class="record.error_msg ? 'error' : 'success'">
            {{ record.error_msg ? '执行失败' : '执行成功' }}
          </span>
        </template>
        <template v-else-if="column.key === 'action'">
          <a-space size="small" wrap>
            <a-dropdown :trigger="['click']">
              <a class="action-link" @click.prevent>
                插入
                <DownOutlined />
              </a>
              <template #overlay>
                <a-menu @click="({ key }) => emitInsertAction(record, key)">
                  <a-menu-item key="current">插入到当前</a-menu-item>
                  <a-menu-item key="new_console">在新 Console 插入</a-menu-item>
                </a-menu>
              </template>
            </a-dropdown>
            <a class="action-link" @click="copyRecord(record.sqltext)">复制</a>
          </a-space>
        </template>
      </template>
    </a-table>

    <a-modal
      v-model:open="uiState.detailOpen"
      :wrap-class-name="['history-sqlview-modal-wrap', uiState.isSqlFullscreen ? 'history-sqlview-modal-wrap--fullscreen' : ''].filter(Boolean).join(' ')"
      :class="[{ 'history-sqlview-modal--fullscreen': uiState.isSqlFullscreen }]"
      :width="uiState.isSqlFullscreen ? '100vw' : 'min(88vw, 1120px)'"
      :centered="!uiState.isSqlFullscreen"
      :transition-name="uiState.isSqlFullscreen ? '' : undefined"
      :mask-transition-name="uiState.isSqlFullscreen ? '' : undefined"
      :footer="null"
      :closable="false"
      :mask-closable="false"
      :destroy-on-close="false"
      @cancel="handleCancel"
      @ok="handleCancel"
    >
      <template #title>
        <div class="sqlview-title-bar">
          <span class="sqlview-title-text">SQL语句</span>
          <span class="sqlview-title-actions">
            <a-tooltip :title="uiState.isSqlFullscreen ? '退出全屏 (Esc)' : '全屏'" placement="bottom">
              <a-button
                type="text"
                size="small"
                class="sqlview-action-button"
                @click.stop="toggleSqlFullscreen"
              >
                <template #icon>
                  <FullscreenExitOutlined v-if="uiState.isSqlFullscreen" />
                  <FullscreenOutlined v-else />
                </template>
              </a-button>
            </a-tooltip>
            <a-tooltip title="关闭" placement="bottom">
              <a-button
                type="text"
                size="small"
                class="sqlview-action-button"
                @click.stop="handleCancel"
              >
                <template #icon><CloseOutlined /></template>
              </a-button>
            </a-tooltip>
          </span>
        </div>
      </template>
      <div class="sqlview-body" :class="{ 'is-fullscreen': uiState.isSqlFullscreen }">
        <highlightjs language="sql" :code="uiData.detail.sqltext" />
      </div>
    </a-modal>
  </div>
</template>

<script setup>
import { GetHistoryApi } from '@/api/das'
import {
  CloseOutlined,
  DownOutlined,
  FullscreenExitOutlined,
  FullscreenOutlined,
} from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import dayjs from 'dayjs'
import { inject, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import useClipboard from 'vue-clipboard3'

const emit = defineEmits(['insert-sql', 'close'])
const consoleToast = inject('consoleToast', null)

const toast = (text, tone = 'info') => {
  if (typeof consoleToast === 'function') {
    consoleToast(text, tone)
    return
  }
  if (tone === 'success') message.success(text)
  else if (tone === 'error') message.error(text)
  else message.warning(text)
}
const { toClipboard } = useClipboard()

const uiState = reactive({
  loading: false,
  detailOpen: false,
  tableScrollY: 420,
  isSqlFullscreen: false,
})

const uiData = reactive({
  searchValue: '',
  dateRange: [dayjs().subtract(6, 'day'), dayjs()],
  tableData: [],
  detail: {
    sqltext: '',
    rewrite_sqltext: '',
    params: '',
  },
  tableColumns: [
    {
      title: '实例',
      dataIndex: 'instance_id',
      key: 'instance_id',
      width: 250,
      fixed: 'left',
    },
    {
      title: 'SQL语句',
      dataIndex: 'sqltext',
      key: 'sqltext',
      ellipsis: true,
      width: 360,
      fixed: 'left',
    },
    {
      title: '返回行数',
      dataIndex: 'return_rows',
      key: 'return_rows',
      width: 96,
    },
    {
      title: '耗时',
      dataIndex: 'duration',
      key: 'duration',
      width: 90,
    },
    {
      title: '执行状态',
      dataIndex: 'error_msg',
      key: 'error_msg',
      width: 100,
    },
    {
      title: '执行时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 168,
    },
    {
      title: '操作',
      dataIndex: 'action',
      key: 'action',
      width: 100,
      fixed: 'right',
    },
  ],
})

const pagination = reactive({
  current: 1,
  pageSize: 15,
  total: 0,
  pageSizeOptions: ['10', '15', '30', '50'],
  showSizeChanger: true,
  showTotal: (total) => `共 ${total} 条记录`,
  hideOnSinglePage: false,
  position: ['bottomRight'],
})

const fetchData = async () => {
  uiState.loading = true
  const params = {
    page_size: pagination.pageSize,
    page: pagination.current,
    is_page: true,
    search: uiData.searchValue,
  }
  if (Array.isArray(uiData.dateRange) && uiData.dateRange[0] && uiData.dateRange[1]) {
    params.start_date = uiData.dateRange[0].format('YYYY-MM-DD')
    params.end_date = uiData.dateRange[1].format('YYYY-MM-DD')
  }
  const res = await GetHistoryApi(params).catch(() => {})
  if (res) {
    pagination.total = res.total
    uiData.tableData = Array.isArray(res.data) ? res.data : []
  }
  uiState.loading = false
  updateTableScrollY()
}

const handleSearch = (value) => {
  uiData.searchValue = value
  pagination.current = 1
  fetchData()
}

const handleDateRangeChange = (value) => {
  if (!Array.isArray(value) || value.length !== 2) return
  uiData.dateRange = value
  pagination.current = 1
  fetchData()
}

const handleTableChange = (pager) => {
  const prevPageSize = pagination.pageSize
  pagination.current = pager.current
  pagination.pageSize = pager.pageSize
  if (pager.pageSize !== prevPageSize) {
    pagination.current = 1
  }
  fetchData()
  nextTick(() => {
    const bodyEl = panelRef.value?.querySelector?.('.ant-table-body')
    if (bodyEl) bodyEl.scrollTop = 0
  })
}

const getInstanceDisplayName = (record) => {
  return String(record.instance_name || record.schema || '-')
}

const getInstanceAddress = (record) => {
  const host = String(record.hostname || '').trim()
  const port = record.port ?? ''
  return host ? `${host}:${port}` : ''
}

const getDurationClass = (duration) => {
  if (duration > 1000) return 'slow'
  if (duration > 100) return 'medium'
  return 'fast'
}

const formatCreatedAt = (value) => {
  const raw = String(value || '').trim()
  if (!raw) return ''
  const parsed = dayjs(raw)
  if (!parsed.isValid()) return raw
  return parsed.format('YYYY-MM-DD HH:mm:ss')
}

const showSqlDetail = (record) => {
  uiState.isSqlFullscreen = false
  uiData.detail.sqltext = record.sqltext || ''
  uiData.detail.rewrite_sqltext = record.rewrite_sqltext || ''
  uiData.detail.params = JSON.stringify(record.params ?? {})
  uiState.detailOpen = true
}

const handleCancel = () => {
  uiState.isSqlFullscreen = false
  uiState.detailOpen = false
}

const toggleSqlFullscreen = () => {
  uiState.isSqlFullscreen = !uiState.isSqlFullscreen
}

const handleDetailEscKey = (e) => {
  if (e.key === 'Escape' && uiState.detailOpen && uiState.isSqlFullscreen) {
    uiState.isSqlFullscreen = false
    e.preventDefault()
    e.stopPropagation()
  }
}

const emitInsertAction = (record, actionKey) => {
  emit('insert-sql', {
    mode: actionKey,
    sqltext: record?.sqltext || '',
    record,
  })
  emit('close')
}

const copyRecord = async (value) => {
  try {
    await toClipboard(value || '')
    emit('close')
  } catch {
    toast('复制失败', 'error')
  }
}

const panelRef = ref()
const toolbarRef = ref()
let resizeObserver = null

const updateTableScrollY = async () => {
  await nextTick()
  const panelHeight = panelRef.value?.getBoundingClientRect?.().height || 0
  const toolbarHeight = toolbarRef.value?.getBoundingClientRect?.().height || 0
  const paginationEl = panelRef.value?.querySelector?.('.ant-pagination')
  const paginationHeight = paginationEl?.getBoundingClientRect?.().height || 40
  const reserved = 12
  const available = Math.floor(panelHeight - toolbarHeight - paginationHeight - reserved)
  uiState.tableScrollY = available > 200 ? available : 200
}

onMounted(() => {
  fetchData()
  updateTableScrollY()
  if (typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(() => updateTableScrollY())
    if (panelRef.value) resizeObserver.observe(panelRef.value)
    if (toolbarRef.value) resizeObserver.observe(toolbarRef.value)
  }
  if (typeof window !== 'undefined') {
    window.addEventListener('keydown', handleDetailEscKey, true)
  }
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect?.()
  resizeObserver = null
  if (typeof window !== 'undefined') {
    window.removeEventListener('keydown', handleDetailEscKey, true)
  }
})
</script>

<style scoped>
.library-panel {
  display: flex;
  flex-direction: column;
  gap: 8px;
  height: 100%;
  min-height: 0;
  max-height: 100%;
  overflow: hidden;
}

.library-toolbar {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  padding: 0;
  flex: 0 0 auto;
}

.library-panel :deep(.ant-table-wrapper),
.library-panel :deep(.ant-table) {
  margin: 0;
}

.library-panel :deep(.ant-table-wrapper) {
  flex: 1 1 auto;
  min-height: 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.library-panel :deep(.ant-table) {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.library-panel :deep(.ant-table-container) {
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
}

.library-panel :deep(.ant-spin-nested-loading),
.library-panel :deep(.ant-spin-container) {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.library-panel :deep(.ant-spin-container) {
  flex: 1 1 auto;
  min-height: 0;
}

.library-panel :deep(.ant-table-thead > tr > th) {
  padding-top: 8px;
  padding-bottom: 8px;
}

.library-date-range {
  width: 240px;
}

.library-search {
  width: 280px;
}

.instance-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.instance-name {
  color: #1f2937;
}

.instance-address {
  color: #7b8794;
  font-size: 12px;
}

.view-link,
.action-link {
  white-space: nowrap;
}

.sql-preview {
  margin-left: 8px;
}

.duration-tag.fast {
  color: #0f9f6f;
}

.duration-tag.medium {
  color: #d97706;
}

.duration-tag.slow,
.status-tag.error {
  color: #dc2626;
}

.status-tag.success {
  color: #0f9f6f;
}

.detail-card {
  margin-top: 12px;
}

.library-panel :deep(.ant-pagination) {
  margin: 4px 0 0 !important;
  padding-top: 8px;
  padding-bottom: 4px;
  border-top: 1px solid #f0f1f3;
  flex: 0 0 auto;
}

.library-panel :deep(.ant-pagination .ant-pagination-total-text) {
  color: #86909c;
  font-size: 12px;
}

.library-panel :deep(.ant-pagination .ant-pagination-item-active) {
  background: rgba(15, 138, 131, 0.08);
  border-color: rgba(15, 138, 131, 0.22);
}

.library-panel :deep(.ant-pagination .ant-pagination-item-active a) {
  color: #0f8a83;
}

.library-panel :deep(.ant-pagination .ant-pagination-item a:hover),
.library-panel :deep(.ant-pagination .ant-pagination-prev:hover .ant-pagination-item-link),
.library-panel :deep(.ant-pagination .ant-pagination-next:hover .ant-pagination-item-link) {
  color: #0b7770;
  border-color: rgba(15, 138, 131, 0.22);
}

.sqlview-title-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  line-height: 1;
  gap: 8px;
}

.sqlview-title-text {
  font-size: 16px;
  font-weight: 600;
  color: #1f2937;
  line-height: 28px;
  letter-spacing: 0.2px;
}

.sqlview-title-actions {
  display: inline-flex;
  align-items: center;
  gap: 0;
}

.sqlview-action-button {
  height: 28px;
  line-height: 28px;
  padding: 0 6px;
  color: #4b5563;
  border-radius: 6px;
  transition: background-color 0.15s ease, color 0.15s ease;
}

.sqlview-action-button:hover {
  color: #0f8a83;
  background-color: rgba(15, 138, 131, 0.06);
}

.sqlview-action-button :deep(.anticon) {
  font-size: 14px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.sqlview-body {
  width: 100%;
  min-height: 100%;
  box-sizing: border-box;
}

.sqlview-body :deep(pre) {
  margin: 0;
  padding: 16px !important;
  background: #1e293b !important;
  border-radius: 8px;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: min(68vh, 720px);
  overflow-y: auto;
  overflow-x: auto;
  line-height: 1.75;
  font-size: 13px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace;
}

.sqlview-body.is-fullscreen :deep(pre) {
  min-height: calc(100vh - 150px);
  max-height: calc(100vh - 150px);
  padding: 18px 22px !important;
  line-height: 1.85;
  font-size: 13.5px;
}

.sqlview-body :deep(pre)::-webkit-scrollbar {
  width: 10px;
  height: 10px;
}

.sqlview-body :deep(pre)::-webkit-scrollbar-thumb {
  background-color: rgba(148, 163, 184, 0.45);
  border-radius: 6px;
  border: 2px solid transparent;
  background-clip: padding-box;
}

.sqlview-body :deep(pre)::-webkit-scrollbar-thumb:hover {
  background-color: rgba(148, 163, 184, 0.75);
  background-clip: padding-box;
  border: 2px solid transparent;
}

.sqlview-body :deep(pre)::-webkit-scrollbar-track {
  background-color: rgba(15, 23, 42, 0.35);
  border-radius: 6px;
}</style>

<style>
.history-sqlview-modal-wrap .ant-modal-title {
  width: 100%;
  line-height: 1;
  padding: 0;
}

.history-sqlview-modal-wrap .ant-modal-close {
  display: none !important;
}

.history-sqlview-modal-wrap .ant-modal-header {
  padding: 14px 20px;
  border-bottom: 1px solid #f0f1f3;
  border-top-left-radius: 10px;
  border-top-right-radius: 10px;
  background: #ffffff;
}

.history-sqlview-modal-wrap .ant-modal-body {
  padding: 18px 20px 22px 20px;
  background: #ffffff;
}

.history-sqlview-modal-wrap .ant-modal-content {
  border-radius: 10px;
  overflow: hidden;
  box-shadow: 0 18px 50px rgba(15, 23, 42, 0.16);
  border: 1px solid rgba(15, 23, 42, 0.06);
}

.history-sqlview-modal-wrap.history-sqlview-modal-wrap--fullscreen {
  backdrop-filter: blur(2px);
}

.history-sqlview-modal-wrap.history-sqlview-modal-wrap--fullscreen .ant-modal-content {
  box-shadow: 0 30px 80px rgba(15, 23, 42, 0.35);
}

.history-sqlview-modal-wrap .ant-modal {
  max-width: 100vw !important;
}

.history-sqlview-modal-wrap--fullscreen + body,
body:has(.history-sqlview-modal-wrap--fullscreen) {
  overflow: hidden;
}

@media (max-width: 768px) {
  /* 日期范围 240px + 搜索 280px = 528px，容器既不换行也没有 overflow，直接被裁掉 */
  .library-toolbar {
    flex-wrap: wrap;
    gap: 8px;
    justify-content: space-between;
  }

  .library-date-range,
  .library-search {
    width: 100%;
    flex: 1 1 100%;
    min-width: 0;
  }

  .library-panel {
    gap: 6px;
  }
}
</style>
