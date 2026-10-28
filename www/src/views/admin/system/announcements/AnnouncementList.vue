<template>
  <div class="gi-page-shell">
    <!-- 页面标题在内容卡片之外；搜索/筛选/新增恒为同一行、任何宽度都不换行 -->
    <div class="gi-page-toolbar gi-toolbar-single-line">
      <div class="gi-page-title">公告管理</div>
      <div class="gi-toolbar-actions">
        <a-input
          v-model:value="filterState.keyword"
          class="gi-toolbar-search"
          placeholder="按标题 / 内容搜索"
          allow-clear
          @press-enter="handleSearch"
        />
        <a-select
          v-model:value="filterState.status"
          style="width: 150px;"
          placeholder="全部状态"
          :options="statusOptions"
          allow-clear
        />
        <a-button type="primary" @click="handleSearch">查询</a-button>
        <a-button @click="handleReset">重置</a-button>
        <a-button type="primary" :disabled="!canManage" @click="handleAdd"><PlusOutlined />新增公告</a-button>
      </div>
    </div>

    <a-card>
      <!-- 展示设置：全局一份，作用于页面公告条 -->
    <div class="ann-settings">
      <div class="ann-settings__title">展示设置</div>
      <a-form
        :model="settingsState"
        layout="inline"
        :disabled="!canManage"
        class="ann-settings__form"
      >
        <a-form-item label="滚动速度">
          <a-input-number
            v-model:value="settingsState.scroll_speed"
            :min="10"
            :max="300"
            :step="10"
            addon-after="px/s"
            style="width: 160px"
          />
        </a-form-item>
        <a-form-item label="每条停留时长">
          <a-input-number
            v-model:value="settingsState.dwell_ms"
            :min="500"
            :max="20000"
            :step="500"
            addon-after="ms"
            style="width: 160px"
          />
        </a-form-item>
        <a-form-item>
          <a-button type="primary" :loading="settingsSaving" :disabled="!canManage" @click="handleSaveSettings">
            保存设置
          </a-button>
        </a-form-item>
      </a-form>
    </div>

    <PageTableSection>
      <a-table
        size="small"
        :columns="uiData.tableColumns"
        :row-key="(record) => record.id"
        :data-source="uiData.tableData"
        :pagination="pagination"
        :loading="uiState.loading"
        @change="handleTableChange"
        :scroll="{ x: 'max-content' }"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'title'">
            <span class="ann-title-cell" :title="record.title">{{ record.title }}</span>
          </template>
          <template v-else-if="column.key === 'content'">
            <span class="ann-content-cell">{{ record.content }}</span>
          </template>
          <template v-else-if="column.key === 'link'">
            <a v-if="record.link" class="ann-action-link" :href="record.link" target="_blank" rel="noopener noreferrer">
              {{ record.link }}
            </a>
            <span v-else class="ann-empty">-</span>
          </template>
          <template v-else-if="column.key === 'state'">
            {{ stateMeta(record.effective_state) }}
          </template>
          <template v-else-if="column.key === 'range'">
            {{ formatRange(record) }}
          </template>
          <template v-else-if="column.key === 'updated_at'">
            {{ record.updated_at || '-' }}
          </template>
          <template v-else-if="column.key === 'created_by'">
            {{ record.created_by || '-' }}
          </template>
          <template v-else-if="column.key === 'action'">
            <a-space>
              <a v-if="canManage" class="ann-action-link" @click="handleEdit(record)"><EditOutlined /> 编辑</a>
              <a v-if="canManage" class="ann-action-link" @click="handleToggle(record)">
                {{ record.enabled ? '停用' : '启用' }}
              </a>
              <a-popconfirm
                v-if="canManage"
                title="确认删除吗？"
                ok-text="是"
                cancel-text="否"
                @confirm="handleDelete(record)"
              >
                <a class="ann-action-link"><DeleteOutlined /> 删除</a>
              </a-popconfirm>
            </a-space>
          </template>
        </template>
      </a-table>
      </PageTableSection>
    </a-card>
  </div>

  <AnnouncementFormModal
    :open="uiState.isModalOpen"
    v-model:modelValue="formState"
    :can-manage="canManage"
    :title="uiState.isEditMode ? '编辑公告' : '新增公告'"
    @update:open="uiState.isModalOpen = $event"
    @submit="onSubmit"
  />
</template>

<script setup>
defineOptions({ name: 'AnnouncementList' })

import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons-vue'
import { useThrottleFn } from '@vueuse/core'
import { message } from 'ant-design-vue'
import { computed, onMounted, reactive, ref } from 'vue'
import PageTableSection from '@/components/patterns/PageTableSection.vue'
import {
  createAnnouncementApi,
  deleteAnnouncementApi,
  getAnnouncementListApi,
  getAnnouncementSettingsApi,
  updateAnnouncementApi,
  updateAnnouncementSettingsApi,
  updateAnnouncementStatusApi,
} from '@/api/announcement'
import { useUserStore } from '@/store/user'
import AnnouncementFormModal from './AnnouncementFormModal.vue'

const userStore = useUserStore()
const canManage = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.announcement.manage'),
)

// 展示态与列表筛选共用同一套取值：状态是后端按「启用 + 生效时间窗」实时算出来的派生值，
// 前端只负责渲染，不另算一遍，避免列表与头部展示口径漂移。
// 状态只渲染文字，不套标签/颜色，避免与页面其它列的视觉噪音叠加
const STATE_MAP = {
  effective: '生效中',
  pending: '未开始',
  expired: '已过期',
  disabled: '已停用',
}

const statusOptions = [
  { label: '生效中', value: 'effective' },
  { label: '未开始', value: 'pending' },
  { label: '已过期', value: 'expired' },
  { label: '已停用', value: 'disabled' },
]

const stateMeta = (key) => STATE_MAP[key] || '-'

const uiState = reactive({
  loading: false,
  isEditMode: false,
  isModalOpen: false,
})

const uiData = reactive({
  tableData: [],
  tableColumns: [
    { title: '公告标题', dataIndex: 'title', key: 'title', width: 200, align: 'left', ellipsis: true },
    { title: '公告内容', dataIndex: 'content', key: 'content', align: 'left', ellipsis: true },
    { title: '跳转链接', dataIndex: 'link', key: 'link', width: 220, align: 'left', ellipsis: true },
    { title: '状态', dataIndex: 'effective_state', key: 'state', width: 100, align: 'center' },
    { title: '生效时间', dataIndex: 'range', key: 'range', width: 300, align: 'left' },
    { title: '创建人', dataIndex: 'created_by', key: 'created_by', width: 120, align: 'left' },
    { title: '更新时间', dataIndex: 'updated_at', key: 'updated_at', width: 180, align: 'left' },
    { title: '操作', dataIndex: 'action', key: 'action', fixed: 'right', width: 200, align: 'center' },
  ],
})

const defaultFilter = {
  keyword: '',
  status: undefined,
}
const filterState = ref({ ...defaultFilter })

const defaultForm = {
  id: undefined,
  title: '',
  content: '',
  link: '',
  enabled: true,
  start_at: undefined,
  end_at: undefined,
}
const formState = ref({ ...defaultForm })

const pagination = reactive({
  current: 1,
  pageSize: 10,
  total: 0,
  pageSizeOptions: ['10', '20', '50', '100'],
  showSizeChanger: true,
})

const formatRange = (record) => {
  const start = record.start_at || '不限'
  const end = record.end_at || '不限'
  if (start === '不限' && end === '不限') return '不限'
  return `${start} ~ ${end}`
}

const buildParams = () => ({
  page: pagination.current,
  page_size: pagination.pageSize,
  keyword: (filterState.value.keyword || '').trim(),
  status: filterState.value.status || undefined,
})

const fetchData = async () => {
  uiState.loading = true
  const res = await getAnnouncementListApi(buildParams()).catch(() => null)
  if (res) {
    uiData.tableData = Array.isArray(res.data) ? res.data : []
    pagination.total = res.total || 0
  }
  uiState.loading = false
}

const handleTableChange = (pager) => {
  pagination.current = pager.current
  pagination.pageSize = pager.pageSize
  fetchData()
}

const handleSearch = () => {
  pagination.current = 1
  fetchData()
}

const handleReset = () => {
  filterState.value = { ...defaultFilter }
  pagination.current = 1
  fetchData()
}

const handleAdd = () => {
  uiState.isEditMode = false
  formState.value = { ...defaultForm }
  uiState.isModalOpen = true
}

const handleEdit = (record) => {
  uiState.isEditMode = true
  formState.value = {
    ...defaultForm,
    ...record,
    // 后端 NULL 序列化成 null，日期控件要 undefined 才是「未选择」状态
    start_at: record.start_at || undefined,
    end_at: record.end_at || undefined,
  }
  uiState.isModalOpen = true
}

const onSubmit = useThrottleFn(async (data) => {
  const payload = {
    title: (data.title || '').trim(),
    content: (data.content || '').trim(),
    link: (data.link || '').trim(),
    enabled: !!data.enabled,
    start_at: data.start_at || null,
    end_at: data.end_at || null,
  }
  const res = uiState.isEditMode
    ? await updateAnnouncementApi(data.id, payload).catch(() => null)
    : await createAnnouncementApi(payload).catch(() => null)
  if (res) {
    message.success('操作成功')
    uiState.isModalOpen = false
    fetchData()
  }
})

const handleToggle = useThrottleFn(async (record) => {
  const res = await updateAnnouncementStatusApi(record.id, !record.enabled).catch(() => null)
  if (res) {
    message.success(record.enabled ? '已停用' : '已启用')
    fetchData()
  }
})

const handleDelete = useThrottleFn(async (record) => {
  const res = await deleteAnnouncementApi(record.id).catch(() => null)
  if (res) {
    message.success('已删除')
    // 末页最后一条被删后页码会越界，回退一页再拉
    if (uiData.tableData.length === 1 && pagination.current > 1) {
      pagination.current -= 1
    }
    fetchData()
  }
})

const settingsSaving = ref(false)
// 缺省值与后端 models.DefaultSettings() 一致，接口不通时页面也不会显示成空框
const settingsState = ref({ scroll_speed: 90, dwell_ms: 3000 })

const fetchSettings = async () => {
  const res = await getAnnouncementSettingsApi().catch(() => null)
  const data = res?.data || {}
  settingsState.value = {
    scroll_speed: Number(data.scroll_speed) || 90,
    dwell_ms: Number(data.dwell_ms) || 3000,
  }
}

const handleSaveSettings = useThrottleFn(async () => {
  settingsSaving.value = true
  const payload = {
    scroll_speed: Number(settingsState.value.scroll_speed) || 90,
    dwell_ms: Number(settingsState.value.dwell_ms) || 3000,
  }
  const res = await updateAnnouncementSettingsApi(payload).catch(() => null)
  settingsSaving.value = false
  if (res) {
    message.success('设置已保存')
    // 后端会把越界值夹回合法区间，以回显为准
    const data = res.data || {}
    settingsState.value = {
      scroll_speed: Number(data.scroll_speed) || payload.scroll_speed,
      dwell_ms: Number(data.dwell_ms) || payload.dwell_ms,
    }
  }
})

onMounted(() => {
  fetchData()
  fetchSettings()
})
</script>

<style scoped>
/* 工具栏（标题 + 搜索/筛选/新增）见公共规范 .gi-toolbar-single-line + .gi-toolbar-actions
   （components/patterns/pagePatterns.scss）；原 .ann-filter-bar 已随筛选区上移到卡片外而移除。 */
.ann-settings {
  margin-bottom: 18px;
  padding: 14px 16px;
  border: 1px solid var(--gi-color-border, #d9e1e6);
  border-radius: var(--gi-radius-md, 8px);
  background: var(--gi-color-page-bg, #f4f7f8);
}

.ann-settings__title {
  margin-bottom: 10px;
  font-size: 14px;
  font-weight: 600;
  color: var(--gi-color-text-primary, rgba(15, 23, 32, 0.9));
}

.ann-settings__form {
  row-gap: 8px;
}

.ann-empty {
  color: rgba(0, 0, 0, 0.35);
}

.ann-content-cell {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}

.ann-action-link {
  color: var(--gi-color-primary, #0f766e);
  cursor: pointer;
  text-decoration: none;
  font-size: 13px;
  &:hover {
    color: var(--gi-color-primary-hover, #0d6b63);
    text-decoration: underline;
  }
  &:active {
    color: var(--gi-color-primary-active, #0b5f58);
  }
}

:deep(.ant-table-small .ant-table-thead > tr > th) {
  white-space: nowrap;
}

:deep(.ant-table-small .ant-table-tbody > tr > td) {
  vertical-align: middle;
}

@media (max-width: 768px) {
  /* inline 表单换行后 addon 会被挤掉 */
  .ann-settings__form :deep(.ant-form-item) {
    margin-inline-end: 0;
    width: 100%;
  }

  .ann-settings__form :deep(.ant-input-number-group-wrapper),
  .ann-settings__form :deep(.ant-input-number) {
    width: 100% !important;
  }

  .ann-settings {
    padding: 12px;
  }

  .gi-toolbar-actions > .ant-select {
    width: 100% !important;
    flex: 1 1 120px !important;
  }
}
</style>
