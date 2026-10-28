<template>
  <div class="gi-page-shell">
    <!-- 页面标题在内容卡片之外；搜索/筛选/新增恒为同一行、任何宽度都不换行 -->
    <div class="gi-page-toolbar gi-toolbar-single-line">
      <div class="gi-page-title">热文管理</div>
      <div class="gi-toolbar-actions">
        <a-input
          v-model:value="filterState.keyword"
          class="gi-toolbar-search"
          placeholder="按标题 / 来源搜索"
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
        <a-button type="primary" :disabled="!canManage" @click="handleAdd"><PlusOutlined />新增热文</a-button>
      </div>
    </div>

    <a-card>
      <div class="hot-tip">
        <InfoCircleOutlined class="hot-tip__icon" />
        <span>
          首页【相关热文】展示<strong>排序前 3 且已启用</strong>的条目；链接留空的条目仅展示、不可点击。
          调整排序或启停后立即生效，无需重启。
        </span>
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
      >
        <template #bodyCell="{ column, record, index }">
          <template v-if="column.key === 'order'">
            <div class="hot-order-cell">
              <span class="hot-order-no" :class="{ 'is-home': isOnHome(record) }">
                {{ globalIndex(index) }}
              </span>
              <div class="hot-order-move">
                <a-tooltip title="上移">
                  <button
                    type="button"
                    class="hot-move-btn"
                    :disabled="!canManage || isFirstRow(index)"
                    @click="handleMove(record, -1)"
                  >▲</button>
                </a-tooltip>
                <a-tooltip title="下移">
                  <button
                    type="button"
                    class="hot-move-btn"
                    :disabled="!canManage || isLastRow(index)"
                    @click="handleMove(record, 1)"
                  >▼</button>
                </a-tooltip>
              </div>
            </div>
          </template>
          <template v-else-if="column.key === 'title'">
            <div class="hot-title-cell" :title="record.title">
              <span class="hot-title-text">{{ record.title }}</span>
              <a-tag v-if="isOnHome(record)" color="green" class="hot-home-tag">首页展示</a-tag>
              <a-tooltip v-if="record.url" :title="record.url">
                <LinkOutlined class="hot-link-icon" />
              </a-tooltip>
              <span v-else class="hot-no-link">未配链接</span>
            </div>
          </template>
          <template v-else-if="column.key === 'tag'">
            <a-tag v-if="record.tag" class="hot-tag-pill">{{ record.tag }}</a-tag>
            <span v-else class="hot-empty">-</span>
          </template>
          <template v-else-if="column.key === 'heat'">
            {{ formatNumber(record.heat) }}
          </template>
          <template v-else-if="column.key === 'enabled'">
            <a-switch
              :checked="record.enabled"
              :disabled="!canManage"
              size="small"
              @change="handleToggle(record)"
            />
          </template>
          <template v-else-if="column.key === 'updated_at'">
            {{ record.updated_at || '-' }}
          </template>
          <template v-else-if="column.key === 'action'">
            <a-space>
              <a v-if="canManage" class="hot-action-link" @click="handleEdit(record)"><EditOutlined /> 编辑</a>
              <a-popconfirm
                v-if="canManage"
                title="确认删除吗？"
                ok-text="是"
                cancel-text="否"
                @confirm="handleDelete(record)"
              >
                <a class="hot-action-link"><DeleteOutlined /> 删除</a>
              </a-popconfirm>
            </a-space>
          </template>
        </template>
      </a-table>
      </PageTableSection>
    </a-card>
  </div>

  <HotArticleFormModal
    :open="uiState.isModalOpen"
    v-model:modelValue="formState"
    :can-manage="canManage"
    :title="uiState.isEditMode ? '编辑热文' : '新增热文'"
    @update:open="uiState.isModalOpen = $event"
    @submit="onSubmit"
  />
</template>

<script setup>
defineOptions({ name: 'HotArticleList' })

import { DeleteOutlined, EditOutlined, InfoCircleOutlined, LinkOutlined, PlusOutlined } from '@ant-design/icons-vue'
import { useThrottleFn } from '@vueuse/core'
import { message } from 'ant-design-vue'
import { computed, onMounted, reactive, ref } from 'vue'
import PageTableSection from '@/components/patterns/PageTableSection.vue'
import {
  createHotArticleApi,
  deleteHotArticleApi,
  getHomeHotArticlesApi,
  getHotArticleListApi,
  updateHotArticleApi,
  updateHotArticleSortApi,
  updateHotArticleStatusApi,
} from '@/api/hotarticle'
import { useUserStore } from '@/store/user'
import HotArticleFormModal from './HotArticleFormModal.vue'

const userStore = useUserStore()
const canManage = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.hotarticle.manage'),
)

const statusOptions = [
  { label: '已启用', value: 'enabled' },
  { label: '已停用', value: 'disabled' },
]

const uiState = reactive({
  loading: false,
  isEditMode: false,
  isModalOpen: false,
})

// 首页当前实际展示的条目 id 集合（取自首页同一接口，后台标识与用户所见必然一致）
const homeArticleIds = ref([])

const uiData = reactive({
  tableData: [],
  tableColumns: [
    { title: '顺序', key: 'order', width: 96, align: 'left' },
    { title: '标题', dataIndex: 'title', key: 'title', align: 'left', ellipsis: true },
    { title: '来源', dataIndex: 'source', key: 'source', width: 140, align: 'left', ellipsis: true },
    { title: '标签', dataIndex: 'tag', key: 'tag', width: 110, align: 'left' },
    { title: '热度', dataIndex: 'heat', key: 'heat', width: 90, align: 'right' },
    { title: '启用', dataIndex: 'enabled', key: 'enabled', width: 80, align: 'center' },
    { title: '更新时间', dataIndex: 'updated_at', key: 'updated_at', width: 180, align: 'left' },
    { title: '操作', dataIndex: 'action', key: 'action', fixed: 'right', width: 160, align: 'center' },
  ],
})

const defaultFilter = { keyword: '', status: undefined }
const filterState = ref({ ...defaultFilter })

const defaultForm = {
  id: undefined,
  title: '',
  source: '程序猿读历史',
  tag: undefined,
  heat: undefined,
  url: '',
  sort_order: undefined,
  enabled: true,
}
const formState = ref({ ...defaultForm })

const pagination = reactive({
  current: 1,
  pageSize: 10,
  total: 0,
  pageSizeOptions: ['10', '20', '50', '100'],
  showSizeChanger: true,
})

const formatNumber = (v) => Number(v || 0).toLocaleString('en-US')

const isOnHome = (record) => homeArticleIds.value.includes(record.id)
const globalIndex = (index) => (pagination.current - 1) * pagination.pageSize + index + 1
const isFirstRow = (index) => pagination.current === 1 && index === 0
const isLastRow = (index) =>
  index === uiData.tableData.length - 1 && globalIndex(index) >= pagination.total

const buildParams = () => ({
  page: pagination.current,
  page_size: pagination.pageSize,
  keyword: (filterState.value.keyword || '').trim(),
  status: filterState.value.status || undefined,
})

const fetchData = async () => {
  uiState.loading = true
  const res = await getHotArticleListApi(buildParams()).catch(() => null)
  if (res) {
    uiData.tableData = Array.isArray(res.data) ? res.data : []
    pagination.total = res.total || 0
  }
  uiState.loading = false
}

// 首页展示标识：直接问首页同款接口，避免前端再按排序推算一遍导致口径漂移
const fetchHomeIds = async () => {
  const res = await getHomeHotArticlesApi().catch(() => null)
  homeArticleIds.value = (res?.data?.articles || []).map((item) => item.id)
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
    // 标签允许自由填写，空串在控件里要表现为「未填」而不是空选项
    tag: record.tag || undefined,
    heat: record.heat ?? undefined,
  }
  uiState.isModalOpen = true
}

const onSubmit = useThrottleFn(async (data) => {
  const payload = {
    title: (data.title || '').trim(),
    source: (data.source || '').trim(),
    tag: (data.tag || '').trim(),
    heat: Number(data.heat) || 0,
    url: (data.url || '').trim(),
    sort_order: Number(data.sort_order) || 0,
    enabled: !!data.enabled,
  }
  const res = uiState.isEditMode
    ? await updateHotArticleApi(data.id, payload).catch(() => null)
    : await createHotArticleApi(payload).catch(() => null)
  if (res) {
    message.success('操作成功')
    uiState.isModalOpen = false
    refresh()
  }
})

const handleToggle = useThrottleFn(async (record) => {
  const res = await updateHotArticleStatusApi(record.id, !record.enabled).catch(() => null)
  if (res) {
    message.success(record.enabled ? '已停用' : '已启用')
    refresh()
  }
})

const handleDelete = useThrottleFn(async (record) => {
  const res = await deleteHotArticleApi(record.id).catch(() => null)
  if (res) {
    message.success('已删除')
    // 末页最后一条被删后页码会越界，回退一页再拉
    if (uiData.tableData.length === 1 && pagination.current > 1) {
      pagination.current -= 1
    }
    refresh()
  }
})

/**
 * 上移 / 下移。
 *
 * 排序值可能重复（历史数据、与新建默认值撞车），直接交换两个 sort_order 在重复值下会「点了没反应」。
 * 因此这里拉取全量已排序列表，在本地交换相邻两条后，按「(序号+1)*10」重排整张表再整体提交 ——
 * 幂等、可预测，也顺手消除了重复值；热文总条数是个位数级别，全量拉取代价可忽略。
 */
const handleMove = useThrottleFn(async (record, direction) => {
  const all = await getHotArticleListApi({ page: 1, page_size: 200 }).catch(() => null)
  const rows = Array.isArray(all?.data) ? all.data : []
  if (!rows.length) return
  const idx = rows.findIndex((item) => item.id === record.id)
  const target = idx + direction
  if (idx < 0 || target < 0 || target >= rows.length) return

  const swapped = [...rows]
  ;[swapped[idx], swapped[target]] = [swapped[target], swapped[idx]]
  const items = swapped.map((item, i) => ({ id: item.id, sort_order: (i + 1) * 10 }))

  const res = await updateHotArticleSortApi(items).catch(() => null)
  if (res) {
    refresh()
  }
})

// 列表 + 首页标识一起刷新：启停/删除/排序都会影响首页展示区
const refresh = () => {
  fetchData()
  fetchHomeIds()
}

onMounted(() => {
  refresh()
})
</script>

<style scoped>
.hot-tip {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-bottom: 14px;
  padding: 10px 14px;
  border: 1px solid var(--gi-color-border, #d9e1e6);
  border-radius: var(--gi-radius-md, 8px);
  background: var(--gi-color-page-bg, #f4f7f8);
  color: rgba(15, 23, 32, 0.75);
  font-size: 13px;
  line-height: 20px;
}

.hot-tip__icon {
  margin-top: 3px;
  color: var(--gi-color-primary, #0f766e);
}

/* 工具栏（标题 + 搜索/筛选/新增）见公共规范 .gi-toolbar-single-line + .gi-toolbar-actions
   （components/patterns/pagePatterns.scss）；原 .hot-filter-bar 已随筛选区上移到卡片外而移除。 */
.hot-order-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}

.hot-order-no {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 22px;
  border-radius: 6px;
  background: rgba(15, 23, 32, 0.06);
  color: rgba(15, 23, 32, 0.55);
  font-size: 12px;
  font-weight: 600;
}

/* 首页展示区（前 3 且启用）的序号用主题色标出，一眼区分展示区与备选区 */
.hot-order-no.is-home {
  background: rgba(15, 118, 110, 0.12);
  color: var(--gi-color-primary, #0f766e);
}

.hot-order-move {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.hot-move-btn {
  width: 18px;
  height: 12px;
  padding: 0;
  border: none;
  border-radius: 3px;
  background: transparent;
  color: rgba(15, 23, 32, 0.4);
  font-size: 9px;
  line-height: 12px;
  cursor: pointer;
}

.hot-move-btn:hover:not(:disabled) {
  background: rgba(15, 118, 110, 0.1);
  color: var(--gi-color-primary, #0f766e);
}

.hot-move-btn:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}

.hot-title-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.hot-title-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.hot-home-tag {
  flex: none;
  margin-inline-end: 0;
}

.hot-no-link {
  flex: none;
  color: rgba(15, 23, 32, 0.35);
  font-size: 12px;
}

/* 已配链接：仅一个图标 + hover 提示完整地址，避免长 URL 撑破标题列 */
.hot-link-icon {
  flex: none;
  color: var(--gi-color-primary, #0f766e);
  font-size: 12px;
}

.hot-tag-pill {
  margin-inline-end: 0;
  background: rgba(240, 138, 11, 0.1);
  border-color: transparent;
  color: #b2650a;
}

.hot-empty {
  color: rgba(0, 0, 0, 0.35);
}

.hot-action-link {
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
  /* 表格没有 scroll.x，补一个最小宽度让它可横向滑动 */
  :deep(.ant-table) {
    min-width: 900px;
  }

  /* 上移/下移按钮原本 18x12，移动端根本点不中 */
  .hot-move-btn {
    width: 32px;
    height: 32px;
    font-size: 12px;
    line-height: 32px;
  }

  .hot-order-move {
    flex-direction: row;
    gap: 4px;
  }

  .hot-order-no {
    width: 28px;
    height: 24px;
  }

  .gi-toolbar-actions > .ant-select {
    width: 100% !important;
    flex: 1 1 120px !important;
  }
}
</style>
