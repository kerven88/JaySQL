<template>
  <div ref="pageRef" class="gi-page-shell todo-detail-page">
    <div class="gi-page-toolbar todo-detail-toolbar">
      <div class="toolbar-left">
        <div class="title">待办详情</div>
      </div>
      <div class="toolbar-right">
        <a-space wrap>
          <a-button :loading="uiState.loading" @click="reloadActive">刷新</a-button>
          <a-button type="primary" @click="go('/home')">返回工作台</a-button>
        </a-space>
      </div>
    </div>

    <a-card class="todo-detail-card" :bordered="false">
      <a-tabs v-model:activeKey="uiData.activeKey" :key="visibleTabKeys.join('-')" destroyInactiveTabPane @change="handleTabChange">
        <a-tab-pane v-if="visibleTabKeys.includes(tabKeys.orderSubmit)" :key="tabKeys.orderSubmit">
          <template #tab>待提交工单 ({{ Number(tabs.orderSubmit.total || 0) }})</template>
          <a-table
            class="todo-table"
            size="middle"
            row-key="order_id"
            :columns="orderSubmitColumns"
            :data-source="tabs.orderSubmit.rows"
            :loading="tabs.orderSubmit.loading"
            :pagination="false"
            :scroll="{ y: tableBodyHeight }"
            @change="() => {}"
            :custom-row="orderRow"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'instance_name'">
                <div class="instance-cell">
                  <div class="instance-name-line">
                    <DataSourceName v-if="instanceNameOf(record)" :name="instanceNameOf(record)" :max="25" />
                  </div>
                  <div v-if="hostPortOf(record)" class="instance-host-line">{{ hostPortOf(record) }}</div>
                </div>
              </template>
              <template v-else-if="column.key === 'actions'">
                <a-button type="link" class="inline-link todo-op-link" @click.stop="closeTodo('todo_order_submit', record)">关闭</a-button>
              </template>
              <template v-else-if="column.key === 'order_id'">
                <div class="task-cell">
                  <div class="task-id-row">
                    <router-link class="task-id-link" :to="{ path: `/orders/${record.order_id}` }">
                      {{ record.display_id || record.order_id }}
                    </router-link>
                    <a-tooltip title="复制工单ID">
                      <button
                        type="button"
                        class="task-id-copy-btn"
                        aria-label="复制工单ID"
                        @click.stop="copyOrderId(record.display_id || record.order_id)"
                      >
                        <CopyOutlined />
                      </button>
                    </a-tooltip>
                  </div>
                  <div
                    class="task-title"
                    :class="{ 'order-title-truncated': record.title && [...record.title].length > 18 }"
                  >
                    {{
                      record.title && [...record.title].length > 18
                        ? [...record.title].slice(0, 18).join('') + '...'
                        : (record.title || '')
                    }}
                  </div>
                </div>
              </template>
            </template>
          </a-table>
          <div class="table-pagination">
            <a-pagination
              v-model:current="tabs.orderSubmit.page"
              v-model:pageSize="tabs.orderSubmit.pageSize"
              :total="tabs.orderSubmit.total"
              :show-total="(t) => `共 ${t} 条`"
              :show-size-changer="true"
              :page-size-options="['10', '20', '50', '100']"
              @change="loadOrderSubmit"
              @showSizeChange="handleOrderSubmitSizeChange"
            />
          </div>
        </a-tab-pane>

        <a-tab-pane v-if="visibleTabKeys.includes(tabKeys.orderApprove)" :key="tabKeys.orderApprove">
          <template #tab>待审批工单 ({{ Number(tabs.orderApprove.total || 0) }})</template>
          <a-table
            class="todo-table"
            size="middle"
            row-key="order_id"
            :columns="orderApproveColumns"
            :data-source="tabs.orderApprove.rows"
            :loading="tabs.orderApprove.loading"
            :pagination="false"
            :scroll="{ y: tableBodyHeight }"
            :custom-row="orderRow"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'instance_name'">
                <div class="instance-cell">
                  <div class="instance-name-line">
                    <DataSourceName v-if="instanceNameOf(record)" :name="instanceNameOf(record)" :max="25" />
                  </div>
                  <div v-if="hostPortOf(record)" class="instance-host-line">{{ hostPortOf(record) }}</div>
                </div>
              </template>
              <template v-else-if="column.key === 'actions'">
                <a-button type="link" class="inline-link todo-op-link" @click.stop="closeTodo('todo_order_approve', record)">关闭</a-button>
              </template>
              <template v-else-if="column.key === 'order_id'">
                <div class="task-cell">
                  <div class="task-id-row">
                    <router-link class="task-id-link" :to="{ path: `/orders/${record.order_id}` }">
                      {{ record.display_id || record.order_id }}
                    </router-link>
                    <a-tooltip title="复制工单ID">
                      <button
                        type="button"
                        class="task-id-copy-btn"
                        aria-label="复制工单ID"
                        @click.stop="copyOrderId(record.display_id || record.order_id)"
                      >
                        <CopyOutlined />
                      </button>
                    </a-tooltip>
                  </div>
                  <div
                    class="task-title"
                    :class="{ 'order-title-truncated': record.title && [...record.title].length > 18 }"
                  >
                    {{
                      record.title && [...record.title].length > 18
                        ? [...record.title].slice(0, 18).join('') + '...'
                        : (record.title || '')
                    }}
                  </div>
                </div>
              </template>
            </template>
          </a-table>
          <div class="table-pagination">
            <a-pagination
              v-model:current="tabs.orderApprove.page"
              v-model:pageSize="tabs.orderApprove.pageSize"
              :total="tabs.orderApprove.total"
              :show-total="(t) => `共 ${t} 条`"
              :show-size-changer="true"
              :page-size-options="['10', '20', '50', '100']"
              @change="loadOrderApprove"
              @showSizeChange="handleOrderApproveSizeChange"
            />
          </div>
        </a-tab-pane>

        <a-tab-pane v-if="visibleTabKeys.includes(tabKeys.orderExecute)" :key="tabKeys.orderExecute">
          <template #tab>待执行工单 ({{ Number(tabs.orderExecute.total || 0) }})</template>
          <a-table
            class="todo-table"
            size="middle"
            row-key="order_id"
            :columns="orderExecuteColumns"
            :data-source="tabs.orderExecute.rows"
            :loading="tabs.orderExecute.loading"
            :pagination="false"
            :scroll="{ y: tableBodyHeight }"
            :custom-row="orderRow"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'instance_name'">
                <div class="instance-cell">
                  <div class="instance-name-line">
                    <DataSourceName v-if="instanceNameOf(record)" :name="instanceNameOf(record)" :max="25" />
                  </div>
                  <div v-if="hostPortOf(record)" class="instance-host-line">{{ hostPortOf(record) }}</div>
                </div>
              </template>
              <template v-else-if="column.key === 'actions'">
                <a-button type="link" class="inline-link todo-op-link" @click.stop="closeTodo('todo_order_execute', record)">关闭</a-button>
              </template>
              <template v-else-if="column.key === 'order_id'">
                <div class="task-cell">
                  <div class="task-id-row">
                    <router-link class="task-id-link" :to="{ path: `/orders/${record.order_id}` }">
                      {{ record.display_id || record.order_id }}
                    </router-link>
                    <a-tooltip title="复制工单ID">
                      <button
                        type="button"
                        class="task-id-copy-btn"
                        aria-label="复制工单ID"
                        @click.stop="copyOrderId(record.display_id || record.order_id)"
                      >
                        <CopyOutlined />
                      </button>
                    </a-tooltip>
                  </div>
                  <div
                    class="task-title"
                    :class="{ 'order-title-truncated': record.title && [...record.title].length > 18 }"
                  >
                    {{
                      record.title && [...record.title].length > 18
                        ? [...record.title].slice(0, 18).join('') + '...'
                        : (record.title || '')
                    }}
                  </div>
                </div>
              </template>
            </template>
          </a-table>
          <div class="table-pagination">
            <a-pagination
              v-model:current="tabs.orderExecute.page"
              v-model:pageSize="tabs.orderExecute.pageSize"
              :total="tabs.orderExecute.total"
              :show-total="(t) => `共 ${t} 条`"
              :show-size-changer="true"
              :page-size-options="['10', '20', '50', '100']"
              @change="loadOrderExecute"
              @showSizeChange="handleOrderExecuteSizeChange"
            />
          </div>
        </a-tab-pane>

        <a-tab-pane v-if="visibleTabKeys.includes(tabKeys.backupEnable)" :key="tabKeys.backupEnable">
          <template #tab>备份策略待启用 ({{ Number(tabs.backupEnable.total || 0) }})</template>
          <a-table
            class="todo-table"
            size="middle"
            row-key="id"
            :columns="backupTodoColumns"
            :data-source="tabs.backupEnable.rows"
            :loading="tabs.backupEnable.loading"
            :pagination="false"
            :scroll="{ y: tableBodyHeight }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'actions'">
                <a-button type="link" class="inline-link todo-op-link" @click.stop="closeTodo('todo_backup_enable', record)">关闭</a-button>
              </template>
              <template v-else-if="column.key === 'task_id'">
                <div class="task-cell">
                  <div class="task-id-row">
                    <router-link class="task-id-link task-id-link--primary" :to="{ path: `/backup/task/${record.id}/edit` }">
                      {{ record.display_id || record.id }}
                    </router-link>
                    <a-tooltip title="复制任务ID">
                      <button
                        type="button"
                        class="task-id-copy-btn"
                        aria-label="复制任务ID"
                        @click.stop="copyTaskId(record.display_id || record.id)"
                      >
                        <CopyOutlined />
                      </button>
                    </a-tooltip>
                  </div>
                  <div class="task-title">{{ record.name || '' }}</div>
                </div>
              </template>
              <template v-else-if="column.key === 'instance'">
                <div class="instance-cell">
                  <div class="instance-name-line">
                    <DataSourceName v-if="instanceNameOf(record)" :name="instanceNameOf(record)" :max="25" />
                  </div>
                  <div v-if="hostPortOf(record)" class="instance-host-line">{{ hostPortOf(record) }}</div>
                </div>
              </template>
              <template v-else-if="column.key === 'schedule'">
                <span>{{ scheduleSummary(record.schedule_days, record.schedule_time) }}</span>
              </template>
              <template v-else-if="column.key === 'status'">
                <span class="status-text">未启用</span>
              </template>
            </template>
          </a-table>
          <div class="table-pagination">
            <a-pagination
              v-model:current="tabs.backupEnable.page"
              v-model:pageSize="tabs.backupEnable.pageSize"
              :total="tabs.backupEnable.total"
              :show-total="(t) => `共 ${t} 条`"
              :show-size-changer="true"
              :page-size-options="['10', '20', '50', '100']"
              @change="sliceBackupEnable"
              @showSizeChange="handleBackupEnableSizeChange"
            />
          </div>
        </a-tab-pane>
      </a-tabs>
    </a-card>
  </div>
</template>

<script setup>
import { computed, inject, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import { getOrderListApi } from '@/api/order'
import { getBackupTasksApi } from '@/api/backup'
import { CopyOutlined } from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import DataSourceName from '@/components/patterns/DataSourceName.vue'
import { useUserStore } from '@/store/user'
import {
  buildRiskKey,
  dismissItem,
  getDismissedCountSync,
  getWorkbenchUsername,
  isDismissedSync,
  loadAllDismissedKeys,
} from '@/utils/homeWorkbench'

defineOptions({ name: 'HomeTodoDetailView' })

const router = useRouter()
const route = useRoute()
const userStore = useUserStore()
const username = ref(getWorkbenchUsername())

const canManageBackup = computed(() => userStore.hasPermission('backup.task.manage'))
const visibleTabKeys = computed(() => {
  const list = [tabKeys.orderSubmit, tabKeys.orderApprove, tabKeys.orderExecute]
  if (canManageBackup.value) list.push(tabKeys.backupEnable)
  return list
})

const tabKeys = Object.freeze({
  orderSubmit: 'order_submit',
  orderApprove: 'order_approve',
  orderExecute: 'order_execute',
  backupEnable: 'backup_enable',
})

const uiState = reactive({
  loading: false,
})

const uiData = reactive({
  activeKey: tabKeys.orderSubmit,
})

const switchActiveToFirstVisible = () => {
  if (!visibleTabKeys.value.includes(uiData.activeKey)) {
    uiData.activeKey = visibleTabKeys.value[0] || tabKeys.orderSubmit
  }
}
switchActiveToFirstVisible()

const tabs = reactive({
  orderSubmit: { loading: false, rows: [], page: 1, pageSize: 10, total: 0 },
  orderApprove: { loading: false, rows: [], page: 1, pageSize: 10, total: 0 },
  orderExecute: { loading: false, rows: [], page: 1, pageSize: 10, total: 0 },
  backupEnable: { loading: false, allRows: [], rows: [], page: 1, pageSize: 10, total: 0 },
})

const tabRefByKey = {
  [tabKeys.orderSubmit]: tabs.orderSubmit,
  [tabKeys.orderApprove]: tabs.orderApprove,
  [tabKeys.orderExecute]: tabs.orderExecute,
  [tabKeys.backupEnable]: tabs.backupEnable,
}

// 从首页「当前待办」入口进入时（?focus=max）定位目标 tab：
// 1) 先按问题数量取最大值；2) 并列最多时，在显示顺序（visibleTabKeys，与模板中 a-tab-pane 顺序一致）
// 中从右到左取第一个命中的 tab。
const pickMostIssuesTabKey = () => {
  const order = visibleTabKeys.value
  if (!order.length) return tabKeys.orderSubmit
  const countOf = (key) => Math.max(0, Number(tabRefByKey[key]?.total || 0))
  const maxCount = Math.max(...order.map(countOf))
  for (let i = order.length - 1; i >= 0; i -= 1) {
    if (countOf(order[i]) === maxCount) return order[i]
  }
  return order[order.length - 1]
}

// 关闭页面主内容区的滚动条，避免出现「页面 + 表格」双层滚动。
const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)

// 表格表体高度：动态测量当前 tab 面板的可用高度（面板高 − 表头 − 底部分页），
// 让表格随窗口自适应铺满，滚动只发生在表体内部，表头与底部分页固定不动。
const pageRef = ref(null)
const scrollY = ref(360)
const tableBodyHeight = computed(() => scrollY.value)

const updateScrollY = () => {
  const root = pageRef.value
  if (!root) return
  const pane = root.querySelector('.ant-tabs-tabpane-active') || root.querySelector('.ant-tabs-tabpane')
  if (!pane) return
  const paneHeight = pane.clientHeight || 0
  if (!paneHeight) return
  const paginationHeight = pane.querySelector('.table-pagination')?.offsetHeight || 0
  const headHeight = pane.querySelector('.ant-table-thead')?.offsetHeight || 39
  const available = paneHeight - paginationHeight - headHeight - 2
  scrollY.value = available > 200 ? Math.round(available) : 200
}

let todoResizeObserver = null
onMounted(() => {
  setLayoutContentNoScroll?.(true)
  nextTick(() => {
    updateScrollY()
    window.addEventListener('resize', updateScrollY)
    if (typeof ResizeObserver !== 'undefined' && pageRef.value) {
      todoResizeObserver = new ResizeObserver(() => updateScrollY())
      todoResizeObserver.observe(pageRef.value)
    }
  })
})
onBeforeUnmount(() => {
  setLayoutContentNoScroll?.(false)
  window.removeEventListener('resize', updateScrollY)
  todoResizeObserver?.disconnect()
  todoResizeObserver = null
})

// 权限变化会改变 a-tabs 的 key 触发重挂载，重挂载后需重新测量表体高度。
watch(visibleTabKeys, () => nextTick(updateScrollY), { flush: 'post' })

const go = (path, query) => {
  router.push({ path, query })
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
      return
    }
    message.success(successMessage)
  } catch {
    message.error('复制失败，请手动选择复制')
  }
}
const copyOrderId = async (orderId) => {
  await copyText(orderId, '工单ID已复制')
}
const copyTaskId = async (taskId) => {
  await copyText(taskId, '任务ID已复制')
}

// 实例列统一两行展示：第一行为实例名称，第二行为 ip:port。
// 名称取 instance_name（工单）/ instance_remark（备份任务）；两者缺失时对应行留空，不展示占位符。
const instanceNameOf = (record) => {
  const name = record?.instance_name ?? record?.instance_remark ?? ''
  return String(name || '').trim()
}

const hostPortOf = (record) => {
  const host = String(record?.instance_host ?? '').trim()
  const rawPort = record?.instance_port
  const port = rawPort == null ? '' : String(rawPort).trim()
  if (!host && !port) return ''
  if (!host) return port
  return port ? `${host}:${port}` : host
}


const orderProgressMeta = {
  WAIT_PRECHECK: { label: '待预审', tone: 'default' },
  PENDING: { label: '审批中', tone: 'processing' },
  APPROVED: { label: '审批通过', tone: 'processing' },
  EXECUTING: { label: '执行中', tone: 'processing' },
  COMPLETED: { label: '执行成功', tone: 'success' },
  FAILED: { label: '执行失败', tone: 'error' },
  REJECTED: { label: '审批驳回', tone: 'warning' },
  TERMINATED: { label: '已终止', tone: 'default' },
  WITHDRAWN: { label: '已撤回', tone: 'warning' },
}

// 各 Tab 表格的列宽由 scoped CSS 的 .todo-table 统一等分，此处不再声明固定 width。
// 对齐方式由各列的 align 决定（工单ID / 备份任务列左对齐，其余居中）；
// CSS 中的 text-align: center 只作为未显式声明 align 时的兜底，不带 !important，
// 以免覆盖列定义上的 align（AntD 会以 inline style 应用）。
const baseOrderColumns = [
  {
    title: '工单ID',
    key: 'order_id',
    dataIndex: 'order_id',
    align: 'left',
  },
  {
    title: '实例',
    key: 'instance_name',
    dataIndex: 'instance_name',
    align: 'center',
  },
  {
    title: '状态',
    key: 'progress',
    dataIndex: 'progress',
    align: 'center',
    customRender: ({ text }) => {
      const meta = orderProgressMeta[String(text || '').toUpperCase()] || { label: String(text || '-'), tone: 'default' }
      return meta.label
    },
  },
  {
    title: '创建时间',
    key: 'created_at',
    dataIndex: 'created_at',
    align: 'center',
    // 三个工单 Tab 共用同一份列定义，时间格式只需在此维护一处。
    customRender: ({ text }) => (text ? dayjs(text).format('YYYY-MM-DD HH:mm:ss') : '-'),
  },
]

const buildTodoActionColumn = () => ({
  title: '操作',
  key: 'actions',
  align: 'center',
})

const orderSubmitColumns = computed(() => [...baseOrderColumns, buildTodoActionColumn()])
const orderApproveColumns = computed(() => [...baseOrderColumns, buildTodoActionColumn()])
const orderExecuteColumns = computed(() => [...baseOrderColumns, buildTodoActionColumn()])

const orderRow = (record) => ({
  onClick: () => {
    if (record?.order_id) {
      go(`/orders/${record.order_id}`)
    }
  },
})

const buildOrderParams = (tab, extra) => ({
  page: tab.page,
  page_size: tab.pageSize,
  is_page: true,
  ...extra,
})

const loadOrderSubmit = async () => {
  tabs.orderSubmit.loading = true
  try {
    const res = await getOrderListApi(buildOrderParams(tabs.orderSubmit, { progress: 'WAIT_PRECHECK' })).catch(() => null)
    const all = Array.isArray(res?.data) ? res.data : []
    const filtered = all.filter(
      (row) => !isDismissedSync(username.value, 'todo_order_submit', buildRiskKey('todo_order_submit', row)),
    )
    tabs.orderSubmit.rows = filtered
    // 后端 total 与已关闭记录口径可能不一致（例如工单关联实例失效后关闭记录仍在），
    // 用当前页过滤后的实际行数兜底，避免页签数量被错误减到 0 而列表仍有数据。
    tabs.orderSubmit.total = Math.max(
      Number(res?.total || filtered.length) - getDismissedCountSync(username.value, 'todo_order_submit'),
      filtered.length,
      0,
    )
  } finally {
    tabs.orderSubmit.loading = false
  }
}

const loadOrderApprove = async () => {
  tabs.orderApprove.loading = true
  try {
    const res = await getOrderListApi(buildOrderParams(tabs.orderApprove, { scope: 'PENDING_MY_APPROVAL' })).catch(() => null)
    const all = Array.isArray(res?.data) ? res.data : []
    const filtered = all.filter(
      (row) => !isDismissedSync(username.value, 'todo_order_approve', buildRiskKey('todo_order_approve', row)),
    )
    tabs.orderApprove.rows = filtered
    // 后端 total 与已关闭记录口径可能不一致，用当前页过滤后的实际行数兜底。
    tabs.orderApprove.total = Math.max(
      Number(res?.total || filtered.length) - getDismissedCountSync(username.value, 'todo_order_approve'),
      filtered.length,
      0,
    )
  } finally {
    tabs.orderApprove.loading = false
  }
}

const loadOrderExecute = async () => {
  tabs.orderExecute.loading = true
  try {
    const res = await getOrderListApi(buildOrderParams(tabs.orderExecute, { scope: 'PENDING_MY_EXECUTION' })).catch(() => null)
    const all = Array.isArray(res?.data) ? res.data : []
    const filtered = all.filter(
      (row) => !isDismissedSync(username.value, 'todo_order_execute', buildRiskKey('todo_order_execute', row)),
    )
    tabs.orderExecute.rows = filtered
    // 后端 total 与已关闭记录口径可能不一致，用当前页过滤后的实际行数兜底。
    tabs.orderExecute.total = Math.max(
      Number(res?.total || filtered.length) - getDismissedCountSync(username.value, 'todo_order_execute'),
      filtered.length,
      0,
    )
  } finally {
    tabs.orderExecute.loading = false
  }
}

// 后端 schedule_days 存的是 MON/TUE/.../SUN，展示时统一转成中文星期。
const weekdayLabelMap = {
  MON: '周一',
  TUE: '周二',
  WED: '周三',
  THU: '周四',
  FRI: '周五',
  SAT: '周六',
  SUN: '周日',
}
const weekdayOrder = ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN']

const scheduleSummary = (daysJson, time) => {
  const timeLabel = String(time || '').trim()
  let days = []
  try {
    if (Array.isArray(daysJson)) {
      days = daysJson
    } else if (typeof daysJson === 'string' && daysJson.trim()) {
      days = JSON.parse(daysJson)
    } else if (daysJson && typeof daysJson === 'object') {
      days = JSON.parse(JSON.stringify(daysJson))
    }
  } catch {
    days = []
  }
  if (!timeLabel && (!Array.isArray(days) || days.length === 0)) return '未设置周期'
  const dayKeys = Array.isArray(days)
    ? [...new Set(days.map((item) => String(item || '').trim().toUpperCase()))].filter((key) => weekdayLabelMap[key])
    : []
  dayKeys.sort((a, b) => weekdayOrder.indexOf(a) - weekdayOrder.indexOf(b))
  let dayLabel = '每天'
  if (dayKeys.length === weekdayOrder.length) {
    dayLabel = '每天'
  } else if (dayKeys.length) {
    dayLabel = dayKeys.map((key) => weekdayLabelMap[key]).join('、')
  }
  return `${dayLabel}${timeLabel ? ` ${timeLabel}` : ''}`.trim()
}

// 「备份任务」列合并了原「任务ID」与「名称」两列：第一行为任务 ID（可复制），第二行为任务名称。
const backupTodoColumns = [
  { title: '备份任务', key: 'task_id', dataIndex: 'id', align: 'left' },
  { title: '实例', key: 'instance', align: 'center' },
  { title: '调度周期', key: 'schedule', align: 'center' },
  { title: '状态', key: 'status', align: 'center' },
  { title: '操作', key: 'actions', align: 'center' },
]

const paginate = (rows, page, pageSize) => {
  const p = Math.max(1, Number(page || 1))
  const s = Math.max(1, Number(pageSize || 10))
  const start = (p - 1) * s
  return rows.slice(start, start + s)
}

const loadBackupEnable = async () => {
  tabs.backupEnable.loading = true
  try {
    const res = await getBackupTasksApi({ page: 1, page_size: 2000, is_page: true }).catch(() => null)
    const all = Array.isArray(res?.data) ? res.data : []
    const filtered = all
      .filter((item) => !item?.enabled)
      .filter((row) => !isDismissedSync(username.value, 'todo_backup_enable', buildRiskKey('todo_backup_enable', row)))
    tabs.backupEnable.allRows = filtered
    tabs.backupEnable.total = filtered.length
    sliceBackupEnable()
  } finally {
    tabs.backupEnable.loading = false
  }
}

const sliceBackupEnable = () => {
  tabs.backupEnable.rows = paginate(tabs.backupEnable.allRows || [], tabs.backupEnable.page, tabs.backupEnable.pageSize)
}

const handleOrderSubmitSizeChange = (_current, size) => {
  tabs.orderSubmit.page = 1
  tabs.orderSubmit.pageSize = size
  loadOrderSubmit()
}

const handleOrderApproveSizeChange = (_current, size) => {
  tabs.orderApprove.page = 1
  tabs.orderApprove.pageSize = size
  loadOrderApprove()
}

const handleOrderExecuteSizeChange = (_current, size) => {
  tabs.orderExecute.page = 1
  tabs.orderExecute.pageSize = size
  loadOrderExecute()
}

const handleBackupEnableSizeChange = (_current, size) => {
  tabs.backupEnable.page = 1
  tabs.backupEnable.pageSize = size
  sliceBackupEnable()
}

const handleTabChange = async (key) => {
  uiData.activeKey = key
  nextTick(updateScrollY)
  await reloadActive()
}

const reloadActive = async () => {
  if (uiState.loading) return
  uiState.loading = true
  try {
    if (uiData.activeKey === tabKeys.orderSubmit) {
      await loadOrderSubmit()
      return
    }
    if (uiData.activeKey === tabKeys.orderApprove) {
      await loadOrderApprove()
      return
    }
    if (uiData.activeKey === tabKeys.orderExecute) {
      await loadOrderExecute()
      return
    }
    if (uiData.activeKey === tabKeys.backupEnable) {
      await loadBackupEnable()
    }
  } finally {
    uiState.loading = false
  }
}

onMounted(async () => {
  try {
    await loadAllDismissedKeys(
      ['todo_order_submit', 'todo_order_approve', 'todo_order_execute', 'todo_backup_enable'],
      { silentError: true },
    )
  } catch {
    /* ignore */
  }
  await Promise.all([
    loadOrderSubmit(),
    loadOrderApprove(),
    loadOrderExecute(),
    loadBackupEnable(),
  ])

  // 首页入口带 focus=max 时，落在此处按最终数量定位 tab（数据已加载完，总数即页面显示值）。
  if (String(route.query?.focus || '') === 'max') {
    uiData.activeKey = pickMostIssuesTabKey()
  }
  nextTick(updateScrollY)
})

const closeTodo = async (type, record) => {
  const t = String(type || '')
  const key = buildRiskKey(t, record)
  if (!key) return
  try {
    await dismissItem(username.value, t, key)
  } catch (e) {
    message.error('关闭失败：' + (e?.message || e || '未知错误'))
    return
  }

  if (t === 'todo_order_submit') {
    tabs.orderSubmit.rows = (tabs.orderSubmit.rows || []).filter(
      (row) => buildRiskKey('todo_order_submit', row) !== key,
    )
    tabs.orderSubmit.total = Math.max(0, Number(tabs.orderSubmit.total || 0) - 1)
  } else if (t === 'todo_order_approve') {
    tabs.orderApprove.rows = (tabs.orderApprove.rows || []).filter(
      (row) => buildRiskKey('todo_order_approve', row) !== key,
    )
    tabs.orderApprove.total = Math.max(0, Number(tabs.orderApprove.total || 0) - 1)
  } else if (t === 'todo_order_execute') {
    tabs.orderExecute.rows = (tabs.orderExecute.rows || []).filter(
      (row) => buildRiskKey('todo_order_execute', row) !== key,
    )
    tabs.orderExecute.total = Math.max(0, Number(tabs.orderExecute.total || 0) - 1)
  } else if (t === 'todo_backup_enable') {
    tabs.backupEnable.allRows = (tabs.backupEnable.allRows || []).filter(
      (row) => buildRiskKey('todo_backup_enable', row) !== key,
    )
    tabs.backupEnable.total = Math.max(0, Number(tabs.backupEnable.total || 0) - 1)
    sliceBackupEnable()
  }

  message.success('已关闭')
}
</script>

<style scoped>
.todo-detail-page {
  display: flex;
  flex-direction: column;
  gap: var(--gi-spacing-md);
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

.todo-detail-toolbar {
  flex: 0 0 auto;
}

.todo-detail-toolbar .toolbar-left {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.todo-detail-toolbar .title {
  font-size: 18px;
  font-weight: 700;
  color: #14313d;
}

.todo-detail-card {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 22px;
  border: 1px solid rgba(27, 73, 88, 0.06);
  box-shadow: 0 10px 24px rgba(18, 58, 72, 0.05);
}

/* 页面自身不出现滚动条，滚动只发生在表格表体内部 */
.todo-detail-card :deep(.ant-card-body) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.todo-detail-card :deep(.ant-tabs) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.todo-detail-card :deep(.ant-tabs-content-holder) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.todo-detail-card :deep(.ant-tabs-content) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.todo-detail-card :deep(.ant-tabs-tabpane) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.table-pagination {
  display: flex;
  justify-content: flex-end;
  padding-top: 14px;
  flex: 0 0 auto;
}

/* 四个 Tab 的表格：各列等分整行宽度 */
:deep(.todo-table .ant-table-content > table) {
  table-layout: fixed !important;
}

/* 默认居中；列定义上的 align（inline style）优先级更高，可覆盖为左对齐 */
:deep(.todo-table .ant-table-thead > tr > th),
:deep(.todo-table .ant-table-tbody > tr > td) {
  text-align: center;
}

/* 四个 Tab 的操作列统一使用系统主题色（tokens.scss: --gi-color-primary） */
:deep(.todo-table .todo-op-link:not([disabled])) {
  color: var(--gi-color-primary) !important;
}

:deep(.todo-table .todo-op-link:not([disabled]):hover) {
  color: var(--gi-color-primary-hover) !important;
}

:deep(.todo-table .todo-op-link:not([disabled]):active) {
  color: var(--gi-color-primary-active) !important;
}

/* 实例列：第一行为实例名称，第二行为淡灰色小号 ip:port */
.instance-cell {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  line-height: 1.4;
  max-width: 100%;
}

.instance-name-line {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.instance-name-line :deep(.gi-data-source-name) {
  font-size: 13px;
  color: #17313d;
}

.instance-host-line {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: #8aa0ab;
}

.inline-link {
  padding: 0;
  height: auto;
}

.cell-primary {
  font-size: 13px;
  color: #17313d;
}

.cell-secondary {
  margin-top: 4px;
  font-size: 12px;
  color: #748893;
}

.task-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 100%;
}

.task-id-row {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
}

.task-id-link {
  color: var(--gi-color-primary, #0f766e);
  text-decoration: none;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.task-id-link:hover {
  text-decoration: underline;
}
.task-id-link--primary {
  color: var(--gi-color-primary, #0f766e);
  font-weight: 500;
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
  color: var(--gi-color-primary, #0f766e);
}

.task-title {
  color: #6d7f8b;
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
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 768px) {
  :deep(.ant-table-content),
  :deep(.ant-table-body) {
    overflow-x: auto;
  }

  :deep(.ant-table-content) > table {
    min-width: 720px;
  }

  :deep(td),
  :deep(th) {
    white-space: normal;
    word-break: break-word;
  }

  .todo-detail-page {
    height: auto;
    min-height: 100%;
    overflow: visible;
  }
}
</style>

