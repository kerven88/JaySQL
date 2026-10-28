<template>
  <div class="gi-page-shell das-favorite-page">
    <PageToolbar>
      <a-input-search
        v-model:value="uiData.searchValue"
        placeholder="输入SQL内容"
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
        :scroll="{ x: 1100 }"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'sqltext'">
            <a @click="showSqlDetail(record)" title="查看完整SQL">
              <EyeOutlined />
            </a>
            {{ record.sqltext }}
          </template>
          <template v-if="column.key === 'action'">
            <a-dropdown>
              <EllipsisOutlined />
              <template #overlay>
                <a-menu>
                  <a-menu-item key="1">
                    <a @click="copyRecord(record)">
                      <CopyOutlined /> 拷贝
                    </a>
                  </a-menu-item>
                  <a-menu-item key="2">
                    <a @click="handleEdit(record)">
                      <EditOutlined /> 编辑
                    </a>
                  </a-menu-item>
                  <a-menu-item key="3">
                    <a-popconfirm title="确认删除吗？" ok-text="是" cancel-text="否" @confirm="handleDelete(record)">
                      <a>
                        <DeleteOutlined /> 删除
                      </a>
                    </a-popconfirm>
                  </a-menu-item>
                </a-menu>
              </template>
            </a-dropdown>
          </template>
        </template>
      </a-table>
    </PageTableSection>
    <!-- 查看SQL -->
    <a-modal
      v-model:open="uiState.open"
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
        <highlightjs language="sql" :code="uiData.sqltext" />
      </div>
    </a-modal>
    <!-- 更新收藏SQL -->
    <DasFavoriteFormModal
      :open="uiState.isFavoritesOpen"
      v-model:modelValue="formState"
      @update:open="uiState.isFavoritesOpen = $event"
      @submit="onSubmit"
    />
  </div>
</template>

<script setup>
import { DeleteFavoritesApi, GetFavoritesApi, UpdateFavoritesApi } from '@/api/das'
import PageTableSection from '@/components/patterns/PageTableSection.vue'
import PageToolbar from '@/components/patterns/PageToolbar.vue'
import DasFavoriteFormModal from '@/views/das/favorite/DasFavoriteFormModal.vue'
import {
  CopyOutlined,
  DeleteOutlined,
  EditOutlined,
  CloseOutlined,
  EllipsisOutlined,
  EyeOutlined,
  FullscreenExitOutlined,
  FullscreenOutlined,
} from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import useClipboard from 'vue-clipboard3'

const { toClipboard } = useClipboard()

// 状态
const uiState = reactive({
  loading: false,
  open: false,
  isSqlFullscreen: false,
  isFavoritesOpen: false,
})

// 数据
const uiData = reactive({
  searchValue: '',
  sqltext: '',
  tableData: [],
  tableColumns: [
    {
      title: '标题',
      dataIndex: 'title',
      key: 'title',
      fixed: 'left',
      ellipsis: true,
    },
    {
      title: 'SQL语句',
      dataIndex: 'sqltext',
      key: 'sqltext',
      ellipsis: true,
      width: '35%',
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      key: 'updated_at',
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
    },
    {
      title: '操作',
      dataIndex: 'action',
      key: 'action',
      fixed: 'right',
    },
  ],
})

// form表单
const defaultForm = {
  title: '',
  sqltext: '',
}
const formState = ref({ ...defaultForm })

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
  const res = await GetFavoritesApi(params).catch(() => { })
  if (res) {
    pagination.total = res.total
    uiData.tableData = res.data
  }
  uiState.loading = false
}
// 查看SQL
const showSqlDetail = (record) => {
  uiState.isSqlFullscreen = false
  uiState.open = true
  uiData.sqltext = record.sqltext
}

// 关闭查看SQL弹窗
const handleCancel = () => {
  uiState.open = false
  uiState.isSqlFullscreen = false
}

const toggleSqlFullscreen = () => {
  uiState.isSqlFullscreen = !uiState.isSqlFullscreen
}

const handleDetailEscKey = (event) => {
  if (uiState.open && uiState.isSqlFullscreen && event.key === 'Escape') {
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

// 复制SQL
const copyRecord = async (value) => {
  try {
    await toClipboard(value.sqltext)
    message.success('已拷贝到剪贴板')
  } catch {
    message.error('拷贝失败')
  }
}

// 编辑
const handleEdit = (record) => {
  formState.value = { ...record }
  uiState.isFavoritesOpen = true
}

// 提交
const onSubmit = async (data) => {
  const res = await UpdateFavoritesApi(data).catch(() => { })
  if (res) {
    message.success('更新成功')
    uiState.isFavoritesOpen = false
    fetchData()
  }
}

// 删除
const handleDelete = async (record) => {
  const res = await DeleteFavoritesApi(record).catch(() => { })
  if (res) {
    message.info('操作成功')
    fetchData()
  }
}

onMounted(() => {
  fetchData()
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

.schema-tag {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  background: #f0fdf4;
  color: #166534;
  padding: 0.25rem 0.5rem;
  border-radius: 0.375rem;
  font-size: 0.75rem;
  font-weight: 500;
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
  .time-cell {
    font-size: 0.8125rem;
  }

  .error-message {
    padding: 0.5rem;
    font-size: 0.8125rem;
  }

  .sqlview-title-text {
    font-size: 15px;
  }
}
</style>
