<template>
  <div class="gi-page-shell order-list-page">
    <a-card class="order-list-card">
      <template #title>
        <div class="card-header">
          <div class="card-title">SQL工单</div>
          <div class="toolbar-scroll">
            <div class="toolbar">
              <div class="toolbar-left">
          <a-select
            v-model:value="filters.scope"
            :options="scopeOptions"
            class="toolbar-scope-select"
            placeholder="提交范围"
          />

          <a-select
            v-model:value="filters.progress"
            :options="progressOptions"
            allowClear
            class="toolbar-progress-select"
            placeholder="全部状态"
          />

          <a-range-picker
            v-model:value="filters.dateRange"
            :allow-clear="false"
            :disabled-date="disabledDate"
            class="toolbar-date-range"
            format="YYYY-MM-DD"
          />

          <a-input-search
            v-model:value="filters.keyword"
            placeholder="搜索任务名称/ID"
            allowClear
            class="toolbar-search"
            @search="handleSearch"
          />

          <a-button class="toolbar-refresh" @click="refreshPageData">
            <ReloadOutlined />
          </a-button>

          <a-button type="primary" class="toolbar-create-button" @click="handleCreate">
            新建SQL工单
          </a-button>
              </div>
            </div>
          </div>
        </div>
      </template>

      <div class="table-wrapper">
        <a-table
          size="middle"
          :columns="uiData.tableColumns"
          :row-key="(record) => record.order_id"
          :data-source="uiData.tableData"
          :pagination="pagination"
          :loading="uiState.loading"
          :custom-row="customRow"
          @change="handleTableChange"
          :scroll="{ x: 1080, y: 'calc(100vh - 278px)' }"
        >
          <template #headerCell="{ column }">
            <template v-if="column.key === 'instance_name'">
              <a-popover
                v-model:open="columnSearch.instanceName.visible"
                trigger="click"
                placement="bottomLeft"
                overlay-class-name="column-search-popover"
                @open-change="(visible) => onColumnSearchToggle('instanceName', visible)"
              >
                <template #content>
                  <a-input
                    v-if="columnSearch.instanceName.visible"
                    v-model:value="columnSearch.instanceName.input"
                    class="column-search-input"
                    placeholder="搜索数据源"
                    allow-clear
                    autofocus
                    @press-enter="onColumnSearchEnter('instanceName')"
                  />
                </template>
                <div class="th-search-wrap">
                  <span class="th-title">数据源</span>
                  <a-tooltip :title="filters.instanceName ? `已筛选：${filters.instanceName}` : '点击搜索'">
                    <button
                      type="button"
                      class="th-search-btn"
                      :class="{ active: !!filters.instanceName }"
                      aria-label="搜索数据源"
                    >
                      <SearchOutlined />
                    </button>
                  </a-tooltip>
                </div>
              </a-popover>
            </template>
            <template v-else-if="column.key === 'applicant'">
              <a-popover
                v-model:open="columnSearch.applicant.visible"
                trigger="click"
                placement="bottomLeft"
                overlay-class-name="column-search-popover"
                @open-change="(visible) => onColumnSearchToggle('applicant', visible)"
              >
                <template #content>
                  <a-input
                    v-if="columnSearch.applicant.visible"
                    v-model:value="columnSearch.applicant.input"
                    class="column-search-input"
                    placeholder="搜索提交人"
                    allow-clear
                    autofocus
                    @press-enter="onColumnSearchEnter('applicant')"
                  />
                </template>
                <div class="th-search-wrap">
                  <span class="th-title">提交人</span>
                  <a-tooltip :title="filters.applicant ? `已筛选：${filters.applicant}` : '点击搜索'">
                    <button
                      type="button"
                      class="th-search-btn"
                      :class="{ active: !!filters.applicant }"
                      aria-label="搜索提交人"
                    >
                      <SearchOutlined />
                    </button>
                  </a-tooltip>
                </div>
              </a-popover>
            </template>
            <template v-else>{{ column.title }}</template>
          </template>
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'task'">
              <div class="task-cell">
                <div class="task-id-row">
                  <router-link class="task-id-link" :to="{ name: 'orders.detail', params: { order_id: record.order_id } }">
                    {{ record.order_id }}
                  </router-link>
                  <a-tooltip title="复制工单ID">
                    <button
                      type="button"
                      class="task-id-copy-btn"
                      aria-label="复制工单ID"
                      @click.stop="copyOrderId(record.order_id)"
                    >
                      <CopyOutlined />
                    </button>
                  </a-tooltip>
                </div>
                <div class="task-title-row">
                  <AppTooltip
                    :title="record.title || ''"
                    placement="topLeft"
                    overlay-class-name="order-list-title-tip gi-style"
                  >
                    <div
                      class="task-title"
                      :class="{ 'order-title-truncated': record.title && [...record.title].length > 15 }"
                    >
                      {{ record.title && [...record.title].length > 15 ? truncateOrderTitle(record.title, 15) : (record.title || '') }}
                    </div>
                  </AppTooltip>
                  <a-tooltip title="修改工单名称">
                    <button
                      type="button"
                      class="task-title-edit-btn"
                      aria-label="修改工单名称"
                      @click.stop="openEditTitle(record)"
                    >
                      <EditOutlined />
                    </button>
                  </a-tooltip>
                </div>
              </div>
            </template>
            <template v-else-if="column.key === 'instance_name'">
              <a-tooltip :title="`${record.instance_name || '-'} ${record.instance_host || '-'}${record.instance_port ? ':' + record.instance_port : ''}`">
                <!-- logo 为独立元素：垂直居中于两行文本左侧；无对应 logo 时不渲染、保持原样式 -->
                <div class="ds-cell">
                  <img
                    v-if="hasDbTypeLogo(record.db_type)"
                    class="ds-logo"
                    :src="dbTypeLogo(record.db_type)"
                    :alt="record.db_type"
                  />
                  <div class="ds-text-block">
                    <div class="ds-name-line">{{ record.instance_name || record.instance_host || '-' }}</div>
                    <div class="ds-host-line">{{ record.instance_host || '-' }}{{ record.instance_port ? `:${record.instance_port}` : '' }}</div>
                  </div>
                </div>
              </a-tooltip>
            </template>
            <template v-else-if="column.key === 'applicant'">
              <div class="user-cell">
                <span class="user-name">{{ record.applicant_name || record.applicant }}</span>
              </div>
            </template>
            <template v-else-if="column.key === 'progress'">
              <span class="status-cell">
                <a-badge :status="statusBadge(record.progress)" :text="statusText(record.progress)" />
                <a-tooltip v-if="planTag(record)" :title="planTag(record).tip">
                  <a-tag :color="planTag(record).color" class="plan-tag">{{ planTag(record).text }}</a-tag>
                </a-tooltip>
              </span>
            </template>
            <template v-else-if="column.key === 'actions'">
              <span class="actions-cell">
                <a class="link-action" @click.stop="goDetail(record)">详情</a>
              </span>
            </template>
          </template>
        </a-table>
      </div>
    </a-card>

    <a-modal
      v-model:open="titleEdit.visible"
      title="修改工单名称"
      :confirm-loading="titleEdit.saving"
      @ok="saveTitle"
    >
      <a-form layout="vertical">
        <a-form-item label="工单ID">
          <span>{{ titleEdit.orderId }}</span>
        </a-form-item>
        <a-form-item label="工单名称" required>
          <a-input
            v-model:value="titleEdit.title"
            :maxlength="60"
            show-count
            placeholder="请输入新的工单名称"
            @pressEnter="saveTitle"
          />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup>
import { getOrderListApi, updateOrderTitleApi } from '@/api/order'
import { useUserStore } from '@/store/user'
import { CopyOutlined, EditOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons-vue'
import { dbTypeLogo, hasDbTypeLogo } from '@/utils/dbTypeLogo'
import { message } from 'ant-design-vue'
import { useDebounceFn } from '@vueuse/core'
import dayjs from 'dayjs'
import { computed, onMounted, reactive, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppTooltip from '@/components/patterns/AppTooltip.vue'
import { buildOrderQuery } from './orderListModel'

const router = useRouter()
const route = useRoute()
const userStore = useUserStore()

const uiState = reactive({
  loading: false,
})

const uiData = reactive({
  tableData: [],
  tableColumns: [
    {
      title: '工单ID',
      key: 'task',
      width: 200,
      fixed: 'left',
    },
    {
      title: '数据源',
      dataIndex: 'instance_name',
      key: 'instance_name',
      width: 220,
      ellipsis: true,
    },
    {
      title: '库',
      dataIndex: 'database',
      key: 'database',
      width: 160,
      ellipsis: true,
      // 表头与单元格内容统一居中
      align: 'center',
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      ellipsis: true,
    },
    {
      title: '提交人',
      dataIndex: 'applicant',
      key: 'applicant',
      width: 150,
      ellipsis: true,
    },
    {
      title: '状态',
      dataIndex: 'progress',
      key: 'progress',
      width: 160,
    },
    {
      title: '操作',
      key: 'actions',
      width: 110,
      fixed: 'right',
    },
  ],
})

// 只有能看全量的人才有真正意义上的「全部任务」。后端对无 order.manage 的普通用户，
// scope=ALL 会安全退化为「与我相关」，如果仍把选项露出来就会变成「假全量」——标题说全部、
// 结果只看到自己那几条。所以这里按权限收敛，普通用户干脆不展示该项。
const canManageAll = computed(() => {
  if (userStore.is_superuser) return true
  return userStore.hasPermission?.('order.manage') ?? false
})

const scopeOptions = computed(() => {
  const hasApprove = userStore.hasPermission?.('order.approve') ?? userStore.is_superuser
  const hasExecute = userStore.hasPermission?.('order.execute') ?? userStore.is_superuser
  const options = [
    { label: '与我相关', value: 'MY_RELATED' },
    { label: '我提交的', value: 'MY_SUBMITTED' },
    { label: '抄送我的', value: 'MY_CC' },
  ]
  if (hasApprove) {
    options.push(
      { label: '待我审批', value: 'PENDING_MY_APPROVAL' },
      { label: '我已审批', value: 'MY_APPROVED' },
    )
  }
  if (hasExecute) {
    options.push(
      { label: '待我执行', value: 'PENDING_MY_EXECUTION' },
      { label: '我已执行', value: 'MY_EXECUTED' },
    )
  }
  if (canManageAll.value) {
    options.splice(1, 0, { label: '全部任务', value: 'ALL' })
  }
  return options
})

const progressOptions = computed(() => [
  { label: '全部状态', value: '' },
  { label: '待提交', value: 'WAIT_PRECHECK' },
  { label: '审批中', value: 'PENDING' },
  { label: '审批通过', value: 'APPROVED' },
  { label: '执行中', value: 'EXECUTING' },
  { label: '执行成功', value: 'COMPLETED' },
  { label: '执行失败', value: 'FAILED' },
  { label: '审批驳回', value: 'REJECTED' },
  { label: '已终止', value: 'TERMINATED' },
  { label: '已撤回', value: 'WITHDRAWN' },
])

// 首页「我的资源 · SQL工单」卡点击进来带 ?scope=MY_RELATED，默认就落到「与我相关」，
// 保证卡片数字与列表数字一致。只接受白名单内的值，避免 URL 手改出下拉框里不存在的项。
const VALID_SCOPES = [
  'MY_RELATED',
  'ALL',
  'MY_SUBMITTED',
  'MY_CC',
  'PENDING_MY_APPROVAL',
  'MY_APPROVED',
  'PENDING_MY_EXECUTION',
  'MY_EXECUTED',
]
const initialScope = (() => {
  const raw = String(route.query.scope || '').toUpperCase()
  if (!VALID_SCOPES.includes(raw)) return 'MY_RELATED'
  // 手改 URL 也拦一道：没有全量权限的人拿不到「全部任务」，回落到「与我相关」，
  // 避免出现下拉框里没有、列表却按退化口径出数的诡异状态。
  if (raw === 'ALL' && !canManageAll.value) return 'MY_RELATED'
  return raw
})()

// 工单列表默认只看近 7 天，而首页「我的资源 · SQL工单」卡是全量口径，所以卡片点击会带
// range=all 进来，把日期范围清空；从菜单直接进入仍保持「近 7 天」的默认，避免一次拉全部历史。
const isAllRange = String(route.query.range || '').toLowerCase() === 'all'
const defaultDateRange = isAllRange ? [] : [dayjs().subtract(7, 'day'), dayjs()]

const filters = reactive({
  scope: initialScope,
  progress: '',
  dateRange: defaultDateRange,
  keyword: '',
  instanceName: '',
  applicant: '',
})

const columnSearch = reactive({
  instanceName: { visible: false, input: '' },
  applicant: { visible: false, input: '' },
})

const MIN_ALLOWED_DATE = dayjs('2000-01-01')
const MAX_ALLOWED_DATE = dayjs('2099-12-31')

const disabledDate = (current) => {
  if (!current) return false
  return current.isBefore(MIN_ALLOWED_DATE, 'day') || current.isAfter(MAX_ALLOWED_DATE, 'day')
}

const statusText = (value) => {
  const key = String(value || '').toUpperCase()
  if (key === 'WAIT_PRECHECK') return '待提交'
  if (key === 'PENDING') return '审批中'
  if (key === 'APPROVED') return '审批通过'
  if (key === 'EXECUTING') return '执行中'
  if (key === 'COMPLETED') return '执行成功'
  if (key === 'FAILED') return '执行失败'
  if (key === 'REJECTED') return '审批驳回'
  if (key === 'TERMINATED') return '已终止'
  if (key === 'WITHDRAWN') return '已撤回'
  return key || '-'
}

const statusBadge = (value) => {
  const key = String(value || '').toUpperCase()
  if (['WAIT_PRECHECK', 'TERMINATED'].includes(key)) return 'default'
  if (key === 'WITHDRAWN') return 'warning'
  if (key === 'PENDING') return 'processing'
  if (key === 'APPROVED') return 'processing'
  if (key === 'EXECUTING') return 'processing'
  if (key === 'COMPLETED') return 'success'
  if (key === 'FAILED') return 'error'
  return 'default'
}

// 执行方式标签：定时 / 手动模式在「审批通过」阶段不会自动执行，
// 列表里若不标出来，用户会以为工单卡住了。状态列右侧给一个小标签 + 悬浮说明。
const planTag = (record) => {
  const progress = String(record?.progress || '').toUpperCase()
  if (progress !== 'APPROVED' && progress !== 'FAILED') return null
  if (record?.execution_mode === 'SCHEDULED' && record?.scheduled_at) {
    return {
      text: '定时',
      color: 'gold',
      tip: `已排定定时执行：${record.scheduled_at}（到期由平台调度器自动触发）`,
    }
  }
  if (record?.execution_mode === 'MANUAL') {
    return { text: '手动', color: 'blue', tip: '手动执行模式：需在执行任务页逐条点击「单条执行」' }
  }
  return null
}

const copyText = async (text, successMessage = '复制成功') => {
  const value = String(text || '')
  if (!value) return
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
    } else {
      throw new Error('clipboard unavailable')
    }
    message.success({
      content: successMessage,
      key: 'order-list-copy-feedback',
      duration: 1,
    })
  } catch {
    message.error({
      content: '复制失败',
      key: 'order-list-copy-feedback',
      duration: 1.2,
    })
  }
}

const copyOrderId = async (orderId) => {
  await copyText(orderId, '复制成功')
}

const titleEdit = reactive({
  visible: false,
  saving: false,
  orderId: '',
  title: '',
})

// ===== 撤回 =====
// 撤回入口只在工单详情页提供（HeaderExtra），列表操作列不展示撤回动作。

const openEditTitle = (record) => {
  titleEdit.orderId = record.order_id
  titleEdit.title = record.title || ''
  titleEdit.visible = true
}

const saveTitle = async () => {
  const newTitle = String(titleEdit.title || '').trim()
  if (!newTitle) {
    message.warning('工单名称不能为空')
    return
  }
  titleEdit.saving = true
  try {
    await updateOrderTitleApi({ order_id: titleEdit.orderId, title: newTitle })
    const row = uiData.tableData.find((item) => item.order_id === titleEdit.orderId)
    if (row) row.title = newTitle
    titleEdit.visible = false
    message.success('工单名称已更新')
  } finally {
    titleEdit.saving = false
  }
}

const truncateOrderTitle = (title, limit = 15) => {
  const raw = String(title || '')
  const chars = [...raw]
  if (chars.length <= limit) return raw
  return chars.slice(0, limit).join('') + '...'
}

const customRow = (record) => ({
  onClick: () => goDetail(record),
})

const goDetail = (record) => {
  router.push({ name: 'orders.detail', params: { order_id: record.order_id } })
}

const handleCreate = () => {
  router.push({ name: 'orders.create' })
}

const handleTableChange = (pager) => {
  pagination.current = pager.current
  pagination.pageSize = pager.pageSize
  fetchData()
}

const pagination = reactive({
  current: 1,
  pageSize: 10,
  total: 0,
  pageSizeOptions: ['10', '20', '50', '100'],
  showSizeChanger: true,
  showTotal: (total) => `共 ${total} 条`,
})

const fetchData = async () => {
  uiState.loading = true
  try {
    const [startAt, endAt] = filters.dateRange || []
    const params = buildOrderQuery({
      page: pagination.current,
      pageSize: pagination.pageSize,
      scope: filters.scope,
      progress: filters.progress,
      startAt: startAt ? dayjs(startAt).startOf('day').format('YYYY-MM-DD HH:mm:ss') : '',
      endAt: endAt ? dayjs(endAt).endOf('day').format('YYYY-MM-DD HH:mm:ss') : '',
      keyword: filters.keyword,
      instanceName: filters.instanceName,
      applicant: filters.applicant,
    })
    const res = await getOrderListApi(params).catch(() => {})
    if (res?.data) {
      pagination.total = res.total
      uiData.tableData = res.data
    } else {
      pagination.total = 0
      uiData.tableData = []
    }
  } finally {
    uiState.loading = false
  }
}

const refreshPageData = async () => {
  await fetchData()
}

const handleSearch = () => {
  pagination.current = 1
  fetchData()
}

const onColumnSearchToggle = (field, visible) => {
  // 打开搜索框时用当前已生效的筛选值回填，方便在原有关键词上继续修改
  if (visible) columnSearch[field].input = filters[field]
}

const commitColumnSearch = (field) => {
  const value = String(columnSearch[field].input || '').trim()
  if (value === String(filters[field] || '')) return
  filters[field] = value
  pagination.current = 1
  fetchData()
}

const onColumnSearchEnter = (field) => {
  commitColumnSearch(field)
  columnSearch[field].visible = false
}

// 输入即搜，300ms 防抖；两个字段各自独立计时，互不影响
const columnSearchDebounce = {
  instanceName: useDebounceFn(() => commitColumnSearch('instanceName'), 300),
  applicant: useDebounceFn(() => commitColumnSearch('applicant'), 300),
}

const triggerFetch = useDebounceFn(() => {
  pagination.current = 1
  fetchData()
}, 300)

onMounted(async () => {
  // 用 defaultDateRange 而不是写死近 7 天：首页卡片（全量口径）带 range=all 进来时不能被重置回 7 天
  filters.dateRange = defaultDateRange
  await refreshPageData()
})

watch(
  () => [filters.scope, filters.progress, filters.dateRange?.[0], filters.dateRange?.[1]],
  () => {
    triggerFetch()
  },
)

watch(
  () => filters.keyword,
  () => {
    triggerFetch()
  },
)

watch(
  () => columnSearch.instanceName.input,
  () => {
    columnSearchDebounce.instanceName()
  },
)

watch(
  () => columnSearch.applicant.input,
  () => {
    columnSearchDebounce.applicant()
  },
)
</script>

<style scoped>
.order-list-page {
  gap: var(--gi-spacing-sm);
  height: 100%;
  width: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
}

.order-list-card {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  overflow: hidden;
}

.order-list-card :deep(.ant-card-head) {
  flex-shrink: 0;
  width: 100%;
  min-width: 0;
  display: flex;
  align-items: center;
}

.card-header {
  display: flex;
  align-items: stretch;
  gap: 16px;
  width: 100%;
  min-height: 40px;
}

.card-title {
  flex: 0 0 auto;
  font-weight: 600;
  white-space: nowrap;
  display: inline-flex;
  align-items: center;
}

.toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  min-width: 900px;
  gap: 8px;
  font-size: 13px;
  padding: 2px 0;
}

.toolbar-scroll {
  flex: 1 1 auto;
  overflow-x: auto;
  overflow-y: clip;
  padding: 2px 0;
  min-height: 36px;
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
}

.toolbar-left {
  display: flex;
  align-items: center;
  flex-wrap: nowrap;
  gap: 8px;
  margin-left: auto;
}

.toolbar-scope-select {
  width: 110px;
  margin-left: -5px;
}

.toolbar-progress-select {
  width: 104px;
}

.toolbar-date-range {
  width: 225px;
}

.toolbar-search {
  width: 200px;
}

.toolbar-refresh {
  width: 32px;
  min-width: 32px;
  padding: 0;
}

.toolbar-create-button {
  min-width: 114px;
  padding-inline: 12px;
  display: inline-flex !important;
  align-items: center;
  justify-content: center;
  text-align: center;
  line-height: 1.2;
}

.toolbar :deep(.ant-select-selector),
.toolbar :deep(.ant-input-affix-wrapper),
.toolbar :deep(.ant-input),
.toolbar :deep(.ant-btn),
.toolbar :deep(.ant-picker),
.toolbar :deep(.ant-select-selection-item),
.toolbar :deep(.ant-select-selection-placeholder),
.toolbar :deep(.ant-picker-input > input),
.toolbar :deep(.ant-btn-icon) {
  font-size: 13px;
}

.toolbar :deep(.ant-select-selector),
.toolbar :deep(.ant-input-affix-wrapper),
.toolbar :deep(.ant-picker),
.toolbar :deep(.ant-btn) {
  height: 32px;
  border-radius: 6px;
}

.toolbar :deep(.ant-select-selection-item),
.toolbar :deep(.ant-select-selection-placeholder),
.toolbar :deep(.ant-picker-input > input),
.toolbar :deep(.ant-input) {
  line-height: 30px;
}

.task-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.task-id-row {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
}

.task-id-link {
  color: var(--gi-color-primary);
}

.task-id-copy-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: none;
  background: transparent;
  color: #b6c0c8;
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
  flex: 0 0 auto;
  transition: color 0.2s ease;
}

.task-id-copy-btn:hover {
  color: #71818c;
}

.task-id {
  display: block;
  font-size: 14px;
  font-weight: 600;
  color: #1f2d38;
  letter-spacing: 0.2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-desc {
  margin-top: 2px;
  display: block;
  font-size: 12px;
  color: #9aa6ae;
  line-height: 1.5;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.muted {
  color: #9aa6ae;
}

/* 表头列内搜索：数据源 / 提交人 */
.th-search-wrap {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  cursor: pointer;
  user-select: none;
}

.th-title {
  font-weight: 500;
}

.th-search-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  padding: 0;
  border: none;
  border-radius: 3px;
  background: transparent;
  color: #b6c0c8;
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
  transition: color 0.2s ease, background-color 0.2s ease;
}

.th-search-btn:hover {
  color: var(--gi-color-primary);
  background-color: rgba(15, 118, 110, 0.08);
}

.th-search-btn.active {
  color: var(--gi-color-primary);
  background-color: rgba(15, 118, 110, 0.12);
}

:global(.column-search-popover .ant-popover-inner-content) {
  padding: 10px 12px;
}

.column-search-input {
  width: 184px;
}

/* 数据源列：logo 独立居左、垂直居中于两行文本；无 logo 时保持原两行样式 */
.ds-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.ds-logo {
  flex: none;
  /* 全站统一：数据库类型 logo 一律 20px */
  width: 20px;
  height: 20px;
  object-fit: contain;
}

.ds-text-block {
  flex: 1;
  min-width: 0;
}

/* 第一行实例名称（正常字号、默认色），第二行 ip:port（淡灰、小一号） */
.ds-name-line {
  display: block;
  font-size: 14px;
  color: #1f2d38;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ds-host-line {
  display: block;
  margin-top: 2px;
  font-size: 12px;
  color: #9aa6ae;
  line-height: 1.5;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 工单名称行 + 编辑按钮 */
.task-title-row {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
}

.task-title-edit-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: none;
  background: transparent;
  color: #b6c0c8;
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  flex: 0 0 auto;
  transition: color 0.2s ease;
}

.task-title-edit-btn:hover {
  color: var(--gi-color-primary, #0f766e);
}

.task-title {
  color: var(--gi-color-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  line-height: 1.5;
}
.order-title-truncated {
  max-width: 100%;
  display: inline-block;
  vertical-align: middle;
  cursor: default;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  pointer-events: auto;
}

:global(.gi-app-tooltip.order-list-title-tip .ant-tooltip-inner) {
  padding: 8px 12px !important;
  background: rgba(29, 35, 41, 0.9) !important;
  color: #f7f8fa !important;
  border-radius: 8px !important;
  box-shadow: 0 8px 24px rgba(29,35,41,.18) !important;
  max-width: 640px !important;
}
:global(.gi-app-tooltip.order-list-title-tip .gi-app-tooltip__content) {
  font-size: 12px !important;
  line-height: 1.55 !important;
  color: #f7f8fa !important;
  white-space: normal !important;
  word-break: break-word !important;
}
:global(.gi-app-tooltip.order-list-title-tip .ant-tooltip-arrow-content) {
  background: rgba(29, 35, 41, 0.9) !important;
}

.user-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}

.user-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.link-action {
  color: var(--gi-color-primary);
}

.link-action:hover {
  color: var(--gi-color-primary);
}

/* 操作列动作间距（详情） */
.actions-cell .link-action + .link-action {
  margin-left: 12px;
}

.order-list-card :deep(.ant-card-body) {
  padding: var(--gi-spacing-ssm) var(--gi-spacing-lg) var(--gi-spacing-ssm) var(--gi-spacing-lg);
  flex: 1 1 auto;
  min-height: 0;
  width: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-sizing: border-box;
}

.table-wrapper :deep(.ant-table-wrapper .ant-table-pagination.ant-pagination) {
  margin: 0 !important;
  padding: var(--gi-spacing-ssm) 0 4px 0;
  min-height: 36px;
}

.table-wrapper :deep(.ant-table-wrapper .ant-spin-container) {
  padding-bottom: 0 !important;
}

.table-wrapper :deep(.ant-table-wrapper .ant-table) {
  padding-bottom: 0;
}

.checkbox-container {
  height: 40px;
  padding: 0 14px;
  display: inline-flex;
  align-items: center;
  background: var(--ant-colorBgContainer, #ffffff);
  border: 1px solid var(--ant-colorBorder, #d9d9d9);
  border-radius: var(--gi-radius-lg);
  transition: all 0.2s ease;
  cursor: pointer;
}

.checkbox-container:hover {
  border-color: var(--ant-colorPrimaryBorderHover, #4096ff);
}

.checkbox-container:has(.ant-checkbox-checked) {
  border-color: var(--ant-colorPrimary, #1677ff);
  background: var(--ant-colorBgContainer, #ffffff);
}

.overview-row {
  margin-top: var(--gi-spacing-ssm);
  display: grid;
  grid-template-columns: repeat(4, minmax(120px, 1fr));
  gap: var(--gi-spacing-sm);
}

.overview-item {
  padding: 10px var(--gi-spacing-ssm);
  border-radius: var(--gi-radius-lg);
  border: 1px solid #e5edf3;
  background: #fafcfd;
  display: flex;
  align-items: baseline;
  justify-content: space-between;
}

.overview-item strong {
  font-size: 18px;
  color: #1f2d38;
}

.overview-label {
  font-size: 12px;
  color: #6d7f8b;
}

.overview-item.pending {
  border-color: #f5deb1;
  background: #fff9ed;
}

.overview-item.running {
  border-color: #ffd7b5;
  background: #fff4eb;
}

.overview-item.failed {
  border-color: #ffc7c7;
  background: #fff1f1;
}

.table-wrapper {
  margin-top: var(--gi-spacing-ssm);
  flex: 1 1 auto;
  min-height: 0;
  width: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-sizing: border-box;
}

.table-wrapper :deep(.ant-table-wrapper),
.table-wrapper :deep(.ant-spin-nested-loading),
.table-wrapper :deep(.ant-spin-container) {
  width: 100%;
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  overflow: hidden;
}

.table-wrapper :deep(.ant-table) {
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
}

.table-wrapper :deep(.ant-table-container) {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.title-link {
  color: #0d5f8c;
  font-weight: 500;
}

.progress-tag {
  border-radius: 999px;
  padding-inline: 10px;
}

:deep(.order-row:hover > td) {
  background: #f7fafc !important;
}

/* 状态列：状态点 + 执行方式标签（定时/手动） */
.status-cell {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.status-cell .plan-tag {
  margin-inline-end: 0;
  padding-inline: 6px;
  font-size: 12px;
  line-height: 18px;
}

@media (max-width: 860px) {
  .overview-row {
    grid-template-columns: repeat(2, minmax(120px, 1fr));
  }
}

@media (max-width: 600px) {
  .overview-row {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 768px) {
  /* 工具栏写死了 min-width: 900px，窄屏会把卡片头撑出横向滚动且主按钮不可见 */
  .card-header {
    flex-direction: column;
    align-items: stretch;
    gap: 8px;
  }

  .toolbar-scroll {
    width: 100%;
    min-height: 0;
    justify-content: flex-start;
  }

  .toolbar {
    min-width: 0;
    width: 100%;
    justify-content: flex-start;
  }

  .toolbar-left {
    flex-wrap: wrap;
    row-gap: 8px;
    margin-left: 0;
    width: 100%;
  }

  .toolbar-scope-select,
  .toolbar-progress-select {
    width: calc(50% - 4px);
  }

  .toolbar-date-range {
    width: 100%;
  }

  .toolbar-search {
    flex: 1 1 auto;
    width: auto;
    min-width: 110px;
  }

  .toolbar-create-button {
    width: 100%;
    min-width: 0;
  }

  /* 复制 / 编辑图标按钮原本约 20px */
  .task-id-copy-btn,
  .task-title-edit-btn {
    width: 32px;
    height: 32px;
  }

  .th-search-btn {
    width: 28px;
    height: 28px;
  }
}
</style>
