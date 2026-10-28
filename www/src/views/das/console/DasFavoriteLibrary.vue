<template>
  <div class="library-panel">
    <div class="library-toolbar">
      <a-input-search
        v-model:value="uiData.searchValue"
        allow-clear
        class="library-search"
        placeholder="搜索收藏标题或 SQL"
        @search="handleSearch"
      />
    </div>

    <a-table
      size="small"
      :columns="uiData.tableColumns"
      :row-key="(record) => record.id"
      :data-source="uiData.tableData"
      :pagination="pagination"
      :loading="uiState.loading"
      :scroll="{ x: 980 }"
      @change="handleTableChange"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'sqltext'">
          <a class="view-link" @click="showSqlDetail(record)">查看</a>
          <span class="sql-preview">{{ record.sqltext }}</span>
        </template>
        <template v-else-if="column.key === 'action'">
          <a-space size="small" wrap>
            <a class="action-link" @click="handleInsert(record.sqltext)">插入</a>
            <a class="action-link" @click="copyRecord(record)">复制</a>
            <a class="action-link" @click="handleEdit(record)">编辑</a>
            <a-popconfirm title="确认删除该收藏吗？" ok-text="是" cancel-text="否" @confirm="handleDelete(record)">
              <a class="action-link danger">删除</a>
            </a-popconfirm>
          </a-space>
        </template>
      </template>
    </a-table>

    <a-modal
      v-model:open="uiState.detailOpen"
      :wrap-class-name="['favorite-sqlview-modal-wrap', uiState.isSqlFullscreen ? 'favorite-sqlview-modal-wrap--fullscreen' : ''].filter(Boolean).join(' ')"
      :class="[{ 'favorite-sqlview-modal--fullscreen': uiState.isSqlFullscreen }]"
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
        <highlightjs language="sql" :code="uiData.detailSql" />
      </div>
    </a-modal>

    <DasFavoriteFormModal
      :open="uiState.formOpen"
      v-model:modelValue="formState"
      @update:open="uiState.formOpen = $event"
      @submit="handleSubmit"
    />
  </div>
</template>

<script setup>
import { DeleteFavoritesApi, GetFavoritesApi, UpdateFavoritesApi } from '@/api/das'
import DasFavoriteFormModal from '@/views/das/favorite/DasFavoriteFormModal.vue'
import {
  CloseOutlined,
  FullscreenExitOutlined,
  FullscreenOutlined,
} from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { inject, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
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
  formOpen: false,
  isSqlFullscreen: false,
})

const uiData = reactive({
  searchValue: '',
  detailSql: '',
  tableData: [],
  tableColumns: [
    {
      title: '标题',
      dataIndex: 'title',
      key: 'title',
      ellipsis: true,
      width: 180,
    },
    {
      title: 'SQL语句',
      dataIndex: 'sqltext',
      key: 'sqltext',
      ellipsis: true,
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      key: 'updated_at',
      width: 168,
    },
    {
      title: '操作',
      dataIndex: 'action',
      key: 'action',
      width: 170,
      fixed: 'right',
    },
  ],
})

const defaultForm = {
  id: undefined,
  title: '',
  sqltext: '',
}
const formState = ref({ ...defaultForm })

const pagination = reactive({
  current: 1,
  pageSize: 8,
  total: 0,
  pageSizeOptions: ['8', '12', '20', '50'],
  showSizeChanger: true,
})

const fetchData = async () => {
  uiState.loading = true
  const params = {
    page_size: pagination.pageSize,
    page: pagination.current,
    is_page: true,
    search: uiData.searchValue,
  }
  const res = await GetFavoritesApi(params).catch(() => {})
  if (res) {
    pagination.total = res.total
    uiData.tableData = Array.isArray(res.data) ? res.data : []
  }
  uiState.loading = false
}

const handleSearch = (value) => {
  uiData.searchValue = value
  pagination.current = 1
  fetchData()
}

const handleTableChange = (pager) => {
  pagination.current = pager.current
  pagination.pageSize = pager.pageSize
  fetchData()
}

const showSqlDetail = (record) => {
  uiState.isSqlFullscreen = false
  uiData.detailSql = record.sqltext || ''
  uiState.detailOpen = true
}

const handleCancel = () => {
  uiState.detailOpen = false
  uiState.isSqlFullscreen = false
}

const toggleSqlFullscreen = () => {
  uiState.isSqlFullscreen = !uiState.isSqlFullscreen
}

const handleDetailEscKey = (event) => {
  if (uiState.detailOpen && uiState.isSqlFullscreen && event.key === 'Escape') {
    event.preventDefault()
    uiState.isSqlFullscreen = false
  }
}

onMounted(() => {
  fetchData()
  window.addEventListener('keydown', handleDetailEscKey)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleDetailEscKey)
})

const handleInsert = (sqltext) => {
  emit('insert-sql', sqltext)
  emit('close')
}

const copyRecord = async (record) => {
  try {
    await toClipboard(record.sqltext || '')
    emit('close')
  } catch {
    toast('复制失败', 'error')
  }
}

const handleEdit = (record) => {
  formState.value = { ...defaultForm, ...record }
  uiState.formOpen = true
}

const handleSubmit = async (data) => {
  const res = await UpdateFavoritesApi(data).catch(() => {})
  if (res) {
    toast('更新成功', 'success')
    uiState.formOpen = false
    fetchData()
  }
}

const handleDelete = async (record) => {
  const res = await DeleteFavoritesApi(record).catch(() => {})
  if (res) {
    toast('删除成功', 'success')
    fetchData()
  }
}
</script>

<style scoped>
.library-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.library-toolbar {
  display: flex;
  justify-content: flex-end;
}

.library-search {
  width: 280px;
}

.view-link,
.action-link {
  white-space: nowrap;
}

.action-link.danger {
  color: #dc2626;
}

.sql-preview {
  margin-left: 8px;
}

.sqlview-title-bar {
  display: flex;
  align-items: center;
  width: 100%;
  justify-content: space-between;
  box-sizing: border-box;
  line-height: 1;
}

.sqlview-title-text {
  font-size: 16px;
  font-weight: 600;
  color: #1F2937;
  line-height: 28px;
}

.sqlview-title-actions {
  display: inline-flex;
  align-items: center;
  gap: 0px;
  line-height: 1;
}

.sqlview-action-button {
  color: rgba(0, 0, 0, 0.45);
  padding: 0 6px;
  height: 28px;
  line-height: 28px;
}

.sqlview-action-button:hover {
  color: rgba(0, 0, 0, 0.75);
  background: rgba(0, 0, 0, 0.04);
}

.sqlview-fullscreen-button {
  color: rgba(0, 0, 0, 0.45);
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
}

.sqlview-body.is-fullscreen :deep(pre) {
  min-height: calc(100vh - 128px);
}</style>

<style>
.favorite-sqlview-modal-wrap .ant-modal-content {
  max-height: 82vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.favorite-sqlview-modal-wrap .ant-modal-header {
  flex: 0 0 auto;
  padding: 14px 20px;
  margin: 0;
}

.favorite-sqlview-modal-wrap .ant-modal-title {
  width: 100%;
  line-height: 1;
}

.favorite-sqlview-modal-wrap .ant-modal-close {
  display: none;
}

.favorite-sqlview-modal-wrap .ant-modal-body {
  flex: 1 1 auto;
  min-height: 0;
  max-height: calc(82vh - 56px);
  padding: 16px 24px 24px;
  overflow-y: auto;
  overflow-x: hidden;
  box-sizing: border-box;
}

.favorite-sqlview-modal-wrap.favorite-sqlview-modal-wrap--fullscreen,
.favorite-sqlview-modal-wrap:has(.favorite-sqlview-modal--fullscreen) {
  top: 0;
  left: 0;
  width: 100vw !important;
  max-width: 100vw !important;
  padding: 0 !important;
  margin: 0 !important;
  transform: none !important;
}

.favorite-sqlview-modal-wrap.favorite-sqlview-modal-wrap--fullscreen .ant-modal,
.favorite-sqlview-modal-wrap:has(.favorite-sqlview-modal--fullscreen) .ant-modal {
  top: 0 !important;
  left: 0 !important;
  width: 100vw !important;
  height: 100vh !important;
  max-width: 100vw !important;
  padding: 0 !important;
  margin: 0 !important;
  border-radius: 0 !important;
  transform: none !important;
}

.favorite-sqlview-modal-wrap.favorite-sqlview-modal-wrap--fullscreen .ant-modal-content,
.favorite-sqlview-modal-wrap:has(.favorite-sqlview-modal--fullscreen) .ant-modal-content {
  height: 100vh;
  max-height: 100vh;
  min-height: 100vh;
  border-radius: 0 !important;
  width: 100vw;
  overflow: hidden;
  box-shadow: none !important;
}

.favorite-sqlview-modal-wrap.favorite-sqlview-modal-wrap--fullscreen .ant-modal-header,
.favorite-sqlview-modal-wrap:has(.favorite-sqlview-modal--fullscreen) .ant-modal-header {
  padding: 14px 20px;
}

.favorite-sqlview-modal-wrap.favorite-sqlview-modal-wrap--fullscreen .ant-modal-body,
.favorite-sqlview-modal-wrap:has(.favorite-sqlview-modal--fullscreen) .ant-modal-body {
  height: calc(100vh - 56px);
  max-height: calc(100vh - 56px);
  min-height: calc(100vh - 56px);
  padding: 24px;
  box-sizing: border-box;
  overflow-y: auto;
}

@media (max-width: 768px) {
  .library-toolbar {
    flex-wrap: wrap;
    gap: 8px;
    justify-content: space-between;
  }

  .library-search {
    width: 100%;
    flex: 1 1 100%;
  }
}
</style>
