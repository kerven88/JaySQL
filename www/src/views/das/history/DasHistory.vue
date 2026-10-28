<template>
  <div class="gi-page-shell das-history-page">
    <PageToolbar>
      <a-input-search
        v-model:value="uiData.searchValue"
        placeholder="输入要查询的SQL内容"
        class="gi-toolbar-search"
        @search="handleSearch"
      />
    </PageToolbar>
    <PageTableSection>
      <a-table
        size="middle"
        :columns="uiData.tableColumns"
        :row-key="(record) => record.key"
        :data-source="uiData.tableData"
        :pagination="pagination"
        :loading="uiState.loading"
        @change="handleTableChange"
        :scroll="{ x: 980 }"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'username'">
            {{ record.username }}
          </template>
          <template v-else-if="column.key === 'instance_id'">
            <div class="instance-cell">
              <div class="instance-name" :title="getInstanceDisplayName(record)">
                {{ getInstanceDisplayName(record) }}
              </div>
              <div class="instance-address">{{ getInstanceAddress(record) }}</div>
            </div>
          </template>
          <template v-else-if="column.key === 'sqltext'">
            <a @click="showSqlDetail(record)" title="查看完整SQL">
              <EyeOutlined />
            </a>
            {{ record.sqltext }}
          </template>
          <template v-else-if="column.key === 'duration'">
            <span class="duration-tag" :class="getDurationClass(record.duration)">
              <ClockCircleOutlined />
              {{ record.duration }} ms
            </span>
          </template>
          <template v-else-if="column.key === 'error_msg'">
            <span class="status-tag" :class="record.error_msg ? 'error' : 'success'">
              <CheckSquareOutlined v-if="!record.error_msg" />
              <CloseCircleOutlined v-else />
              <a-tooltip>
                <template v-if="record.error_msg" #title>{{ record.error_msg }}</template>
                {{ record.error_msg ? '执行失败' : '执行成功' }}
              </a-tooltip>
            </span>
          </template>
          <template v-else-if="column.key === 'action'">
            <a-space wrap>
              <a-tooltip title="拷贝SQL语句">
                <a-button type="link" block shape="circle" :icon="h(CopyOutlined)"
                  @click="copyRecord(record.sqltext)" />
              </a-tooltip>
            </a-space>
          </template>
        </template>
      </a-table>
    </PageTableSection>

    <a-modal
      v-model:open="uiState.open"
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
        <a-card title="原始SQL" size="small" :bordered="false">
          <highlightjs language="sql" :code="uiData.sqltext" />
        </a-card>
        <a-card title="重写SQL" class="detail-card" size="small" :bordered="false">
          <highlightjs language="sql" :code="uiData.rewrite_sqltext" />
        </a-card>
        <a-card title="参数" class="detail-card" size="small" :bordered="false">
          <highlightjs language="sql" :code="uiData.params" />
        </a-card>
      </div>
    </a-modal>
  </div>
</template>
<script setup>
import { GetHistoryApi } from '@/api/das'
import PageTableSection from '@/components/patterns/PageTableSection.vue'
import PageToolbar from '@/components/patterns/PageToolbar.vue'
import {
  CheckSquareOutlined,
  ClockCircleOutlined,
  CloseCircleOutlined,
  CloseOutlined,
  CopyOutlined,
  EyeOutlined,
  FullscreenExitOutlined,
  FullscreenOutlined,
} from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { h, onBeforeUnmount, onMounted, reactive } from 'vue'
import useClipboard from 'vue-clipboard3'

const { toClipboard } = useClipboard()

// 状态
const uiState = reactive({
  open: false,
  loading: false,
  isSqlFullscreen: false,
})

// 数据
const uiData = reactive({
  searchValue: '',
  sqltext: '',
  rewrite_sqltext: '',
  params: '',
  tableData: [],
  tableColumns: [
    {
      title: '用户名',
      dataIndex: 'username',
      key: 'username',
      fixed: 'left',
      width: 100,
    },
    {
      title: '实例ID',
      dataIndex: 'instance_id',
      key: 'instance_id',
      width: 250,
    },
    {
      title: 'SQL语句',
      dataIndex: 'sqltext',
      key: 'sqltext',
      ellipsis: true,
      width: 360,
    },
    {
      title: '返回行数',
      dataIndex: 'return_rows',
      key: 'return_rows',
      width: 90,
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
      width: 110,
    },
    {
      title: '执行时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 170,
    },
    {
      title: '操作',
      dataIndex: 'action',
      key: 'action',
      fixed: 'right',
      width: 72,
    },
  ],
})

// 搜索
const handleSearch = (value) => {
  uiData.searchValue = value
  pagination.current = 1
  fetchData()
}

// 分页
const pagination = reactive({
  current: 1,
  pageSize: 10,
  total: 0,
  pageSizeOptions: ['10', '20', '50', '100'],
  showSizeChanger: true,
})

// 翻页
const handleTableChange = (pager) => {
  pagination.current = pager.current
  pagination.pageSize = pager.pageSize
  fetchData()
}

// 获取列表数据
const fetchData = async () => {
  uiState.loading = true
  const params = {
    page_size: pagination.pageSize,
    page: pagination.current,
    is_page: true,
    search: uiData.searchValue,
  }
  const res = await GetHistoryApi(params).catch(() => { })
  if (res) {
    pagination.total = res.total
    uiData.tableData = res.data
  }
  uiState.loading = false
}

// 显示SQL详情
const showSqlDetail = (record) => {
  uiState.isSqlFullscreen = false
  uiState.open = true
  uiData.sqltext = record.sqltext
  uiData.rewrite_sqltext = record.rewrite_sqltext
  uiData.params = JSON.stringify(record.params)
}

const handleCancel = () => {
  uiState.isSqlFullscreen = false
  uiState.open = false
}

const toggleSqlFullscreen = () => {
  uiState.isSqlFullscreen = !uiState.isSqlFullscreen
}

const handleDetailEscKey = (e) => {
  if (e.key === 'Escape' && uiState.open && uiState.isSqlFullscreen) {
    uiState.isSqlFullscreen = false
    e.preventDefault()
    e.stopPropagation()
  }
}

// 确认按钮处理函数
const handleOk = () => {
  handleCancel()
}

// 获取耗时标签样式
const getDurationClass = (duration) => {
  if (duration > 1000) return 'slow'
  if (duration > 100) return 'medium'
  return 'fast'
}

const getInstanceDisplayName = (record) => {
  return String(record.instance_name || record.schema || '-')
}

const getInstanceAddress = (record) => {
  const host = String(record.hostname || '').trim()
  const port = record.port ?? ''
  if (!host) return ''
  return `${host}:${port}`
}

// 复制到剪贴板
const copyRecord = async (value) => {
  try {
    await toClipboard(value)
    message.success('已拷贝到剪贴板')
  } catch {
    message.error('拷贝失败')
  }
}

onMounted(() => {
  fetchData()
  if (typeof window !== 'undefined') {
    window.addEventListener('keydown', handleDetailEscKey, true)
  }
})

onBeforeUnmount(() => {
  if (typeof window !== 'undefined') {
    window.removeEventListener('keydown', handleDetailEscKey, true)
  }
})
</script>

<style scoped>
.user-cell {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.user-avatar {
  width: 2rem;
  height: 2rem;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  font-weight: 600;
  font-size: 0.875rem;
}

.instance-cell {
  min-width: 0;
}

.instance-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #1f2329;
  font-size: 13px;
  font-weight: 500;
}

.instance-address {
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #8c8c8c;
  font-size: 12px;
  line-height: 1.2;
}

.duration-tag {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.25rem 0.5rem;
  border-radius: 0.375rem;
  font-size: 0.75rem;
  font-weight: 500;
}

.duration-tag.fast {
  background: #f0fdf4;
  color: #166534;
}

.duration-tag.medium {
  background: #fffbeb;
  color: #92400e;
}

.duration-tag.slow {
  background: #fef2f2;
  color: #991b1b;
}

.time-cell {
  font-size: 0.875rem;
}

.time-main {
  font-weight: 500;
  color: #1e293b;
}

.time-sub {
  color: #6b7280;
  font-size: 0.75rem;
}

.status-tag {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.25rem 0.5rem;
  border-radius: 0.375rem;
  font-size: 0.75rem;
  font-weight: 500;
}

.status-tag.success {
  background: #f0fdf4;
  color: #166534;
}

.status-tag.error {
  background: #fef2f2;
  color: #991b1b;
}

.error-message {
  background: #fef2f2;
  border: 1px solid #fecaca;
  border-radius: 0.375rem;
  padding: 0.75rem;
  font-family: 'Monaco', 'Menlo', monospace;
  font-size: 0.875rem;
  color: #991b1b;
}

.detail-card {
  margin-top: 10px;
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
  max-height: min(68vh, 720px);
  overflow-y: auto;
  overflow-x: hidden;
}

.sqlview-body.is-fullscreen {
  max-height: calc(100vh - 150px);
}

.sqlview-body :deep(.ant-card) {
  background: #ffffff;
  border: 1px solid #eaecf0;
  border-radius: 8px;
}

.sqlview-body :deep(.ant-card + .ant-card) {
  margin-top: 12px;
}

.sqlview-body :deep(.ant-card-head) {
  background: #f7f9fb;
  border-bottom: 1px solid #eaecf0;
  padding: 0 14px;
  min-height: 38px;
  border-top-left-radius: 8px;
  border-top-right-radius: 8px;
}

.sqlview-body :deep(.ant-card-head-title) {
  font-size: 13px;
  font-weight: 600;
  color: #1f2937;
  padding: 10px 0;
}

.sqlview-body :deep(.ant-card-body) {
  padding: 0;
  border-bottom-left-radius: 8px;
  border-bottom-right-radius: 8px;
  overflow: hidden;
}

.sqlview-body :deep(pre) {
  margin: 0;
  padding: 14px 16px !important;
  background: #1e293b !important;
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.75;
  font-size: 13px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace;
}

.sqlview-body.is-fullscreen :deep(pre) {
  padding: 16px 20px !important;
  line-height: 1.85;
  font-size: 13.5px;
}

.sqlview-body::-webkit-scrollbar {
  width: 10px;
  height: 10px;
}

.sqlview-body::-webkit-scrollbar-thumb {
  background-color: #cbd5e1;
  border-radius: 6px;
  border: 2px solid transparent;
  background-clip: padding-box;
}

.sqlview-body::-webkit-scrollbar-thumb:hover {
  background-color: #94a3b8;
  background-clip: padding-box;
  border: 2px solid transparent;
}

.sqlview-body::-webkit-scrollbar-track {
  background-color: #f1f5f9;
  border-radius: 6px;
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
  /* 报错信息原文通常很长，补断行兜底 */
  .error-message {
    padding: 0.5rem;
    font-size: 0.75rem;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .sqlview-body {
    max-height: 60vh;
  }
}
</style>
