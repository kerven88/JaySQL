<template>
  <div ref="pageRef" class="gi-page-shell risk-detail-page">
    <div class="gi-page-toolbar risk-detail-toolbar">
      <div class="toolbar-left">
        <div class="title">风险详情</div>
      </div>
      <div class="toolbar-right">
        <a-space wrap>
          <a-button :loading="uiState.loading" @click="reloadActive">刷新</a-button>
          <a-button type="primary" @click="go('/home')">返回工作台</a-button>
        </a-space>
      </div>
    </div>

    <a-card class="risk-detail-card" :bordered="false">
      <a-tabs v-model:activeKey="uiData.activeKey" :key="visibleTabKeys.join('-')" destroyInactiveTabPane @change="handleTabChange">
        <a-tab-pane v-if="visibleTabKeys.includes(tabKeys.instanceConn)" :key="tabKeys.instanceConn">
          <template #tab>实例连接异常 ({{ Number(tabs.instanceConn.total || 0) }})</template>
          <a-table
            class="instance-conn-table"
            size="middle"
            row-key="id"
            :columns="instanceColumns"
            :data-source="tabs.instanceConn.rows"
            :loading="tabs.instanceConn.loading"
            :pagination="false"
            :scroll="{ y: tableBodyHeight }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'status'">
                <span class="conn-status-text">异常</span>
              </template>
              <template v-else-if="column.key === 'checked_at'">
                <span>{{ record.last_checked_at ? dayjs(record.last_checked_at).format('YYYY-MM-DD HH:mm') : '-' }}</span>
              </template>
              <template v-else-if="column.key === 'op'">
                <a-button type="link" class="inline-link conn-op-link" @click.stop="closeRisk('instance_conn', record)">关闭</a-button>
              </template>
            </template>
          </a-table>
          <div class="table-pagination">
            <a-pagination
              v-model:current="tabs.instanceConn.page"
              v-model:pageSize="tabs.instanceConn.pageSize"
              :total="tabs.instanceConn.total"
              :show-total="(t) => `共 ${t} 条`"
              :show-size-changer="true"
              :page-size-options="['10', '20', '50', '100']"
              @change="sliceInstanceConn"
              @showSizeChange="handleInstanceConnSizeChange"
            />
          </div>
        </a-tab-pane>

        <a-tab-pane v-if="visibleTabKeys.includes(tabKeys.manualBackupFail)" :key="tabKeys.manualBackupFail">
          <template #tab>手动备份失败 ({{ Number(tabs.manualBackupFail.total || 0) }})</template>
          <a-table
            class="manual-backup-fail-table"
            size="middle"
            row-key="id"
            :columns="backupFailedColumns"
            :data-source="tabs.manualBackupFail.rows"
            :loading="tabs.manualBackupFail.loading"
            :pagination="false"
            :scroll="{ x: true, y: tableBodyHeight }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'time'">
                <span>{{ record.last_start_at ? dayjs(record.last_start_at).format('YYYY-MM-DD HH:mm') : '-' }}</span>
              </template>
              <template v-else-if="column.key === 'reason'">
                <span>{{ record.last_error_message || '备份执行失败' }}</span>
              </template>
              <template v-else-if="column.key === 'op'">
                <a-button type="link" class="inline-link" @click.stop="closeRisk('manual_backup_fail', record)">关闭</a-button>
              </template>
            </template>
          </a-table>
          <div class="table-pagination">
            <a-pagination
              v-model:current="tabs.manualBackupFail.page"
              v-model:pageSize="tabs.manualBackupFail.pageSize"
              :total="tabs.manualBackupFail.total"
              :show-total="(t) => `共 ${t} 条`"
              :show-size-changer="true"
              :page-size-options="['10', '20', '50', '100']"
              @change="sliceManualBackupFail"
              @showSizeChange="handleManualBackupFailSizeChange"
            />
          </div>
        </a-tab-pane>

        <a-tab-pane v-if="visibleTabKeys.includes(tabKeys.restoreFail)" :key="tabKeys.restoreFail">
          <template #tab>恢复失败 ({{ Number(tabs.restoreFail.total || 0) }})</template>
          <a-table
            size="middle"
            row-key="id"
            :columns="restoreColumns"
            :data-source="tabs.restoreFail.rows"
            :loading="tabs.restoreFail.loading"
            :pagination="false"
            :scroll="{ x: true, y: tableBodyHeight }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'time'">
                <span>{{ record.updated_at ? dayjs(record.updated_at).format('YYYY-MM-DD HH:mm') : '-' }}</span>
              </template>
              <template v-else-if="column.key === 'status'">
                <a-tag color="red">失败</a-tag>
              </template>
              <template v-else-if="column.key === 'op'">
                <a-button type="link" class="inline-link" @click.stop="closeRisk('restore_fail', record)">关闭</a-button>
              </template>
            </template>
          </a-table>
          <div class="table-pagination">
            <a-pagination
              v-model:current="tabs.restoreFail.page"
              v-model:pageSize="tabs.restoreFail.pageSize"
              :total="tabs.restoreFail.total"
              :show-total="(t) => `共 ${t} 条`"
              :show-size-changer="true"
              :page-size-options="['10', '20', '50', '100']"
              @change="sliceRestoreFail"
              @showSizeChange="handleRestoreFailSizeChange"
            />
          </div>
        </a-tab-pane>

        <a-tab-pane v-if="visibleTabKeys.includes(tabKeys.nodeOffline)" :key="tabKeys.nodeOffline">
          <template #tab>数据库 Agent 离线 ({{ Number(tabs.nodeOffline.total || 0) }})</template>
          <a-table
            size="middle"
            row-key="id"
            :columns="nodeColumns"
            :data-source="tabs.nodeOffline.rows"
            :loading="tabs.nodeOffline.loading"
            :pagination="false"
            :scroll="{ x: true, y: tableBodyHeight }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'node'">
                <div class="cell-primary">{{ record.name || '-' }}</div>
                <div class="cell-secondary">{{ record.host }}:{{ record.port }}</div>
              </template>
              <template v-else-if="column.key === 'status'">
                <a-tag color="default">离线</a-tag>
              </template>
              <template v-else-if="column.key === 'action'">
                <a-button type="link" class="inline-link" @click="go(`/backup/agent-install/${record.id}`)">修复/安装 Agent</a-button>
              </template>
              <template v-else-if="column.key === 'op'">
                <a-button type="link" class="inline-link" @click.stop="closeRisk('node_offline', record)">关闭</a-button>
              </template>
            </template>
          </a-table>
          <div class="table-pagination">
            <a-pagination
              v-model:current="tabs.nodeOffline.page"
              v-model:pageSize="tabs.nodeOffline.pageSize"
              :total="tabs.nodeOffline.total"
              :show-total="(t) => `共 ${t} 条`"
              :show-size-changer="true"
              :page-size-options="['10', '20', '50', '100']"
              @change="sliceNodeOffline"
              @showSizeChange="handleNodeOfflineSizeChange"
            />
          </div>
        </a-tab-pane>

        <a-tab-pane v-if="visibleTabKeys.includes(tabKeys.instanceNoBackup)" :key="tabKeys.instanceNoBackup">
          <template #tab>实例无有效备份保护 ({{ Number(tabs.instanceNoBackup.total || 0) }})</template>
          <a-table
            class="instance-nobackup-table"
            size="middle"
            row-key="id"
            :columns="backupInstanceColumns"
            :data-source="tabs.instanceNoBackup.rows"
            :loading="tabs.instanceNoBackup.loading"
            :pagination="false"
            :scroll="{ x: true, y: tableBodyHeight }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'action'">
                <a-space :size="8">
                  <a-button
                    type="link"
                    class="inline-link instance-op-link"
                    @click="go('/backup/task/create', { source_instance_id: record.id })"
                  >
                    创建备份任务
                  </a-button>
                  <a-button type="link" class="inline-link instance-op-link" @click.stop="closeRisk('instance_no_backup', record)">关闭</a-button>
                </a-space>
              </template>
            </template>
          </a-table>
          <div class="table-pagination">
            <a-pagination
              v-model:current="tabs.instanceNoBackup.page"
              v-model:pageSize="tabs.instanceNoBackup.pageSize"
              :total="tabs.instanceNoBackup.total"
              :show-total="(t) => `共 ${t} 条`"
              :show-size-changer="true"
              :page-size-options="['10', '20', '50', '100']"
              @change="sliceInstanceNoBackup"
              @showSizeChange="handleInstanceNoBackupSizeChange"
            />
          </div>
        </a-tab-pane>

        <a-tab-pane v-if="visibleTabKeys.includes(tabKeys.dailyBackupMiss)" :key="tabKeys.dailyBackupMiss">
          <template #tab>周期备份当天未成功 ({{ Number(tabs.dailyBackupMiss.total || 0) }})</template>
          <a-table
            class="daily-backup-miss-table"
            size="middle"
            row-key="id"
            :columns="backupDailyColumns"
            :data-source="tabs.dailyBackupMiss.rows"
            :loading="tabs.dailyBackupMiss.loading"
            :pagination="false"
            :scroll="{ x: 980, y: tableBodyHeight }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'schedule'">
                <span>{{ scheduleSummary(record.schedule_days, record.schedule_time) }}</span>
              </template>
              <template v-else-if="column.key === 'last'">
                <span>{{ record.last_end_at ? dayjs(record.last_end_at).format('YYYY-MM-DD HH:mm') : '-' }}</span>
              </template>
              <template v-else-if="column.key === 'status'">
                <span>{{ taskStatusLabel(record.task_status) }}</span>
              </template>
              <template v-else-if="column.key === 'op'">
                <a-button type="link" class="inline-link" @click.stop="closeRisk('daily_backup_miss', record)">关闭</a-button>
              </template>
            </template>
          </a-table>
          <div class="table-pagination">
            <a-pagination
              v-model:current="tabs.dailyBackupMiss.page"
              v-model:pageSize="tabs.dailyBackupMiss.pageSize"
              :total="tabs.dailyBackupMiss.total"
              :show-total="(t) => `共 ${t} 条`"
              :show-size-changer="true"
              :page-size-options="['10', '20', '50', '100']"
              @change="sliceDailyBackupMiss"
              @showSizeChange="handleDailyBackupMissSizeChange"
            />
          </div>
        </a-tab-pane>

        <a-tab-pane v-if="visibleTabKeys.includes(tabKeys.orderFail)" :key="tabKeys.orderFail">
          <template #tab>工单执行失败 ({{ Number(tabs.orderFail.total || 0) }})</template>
          <a-table
            class="order-fail-table"
            size="middle"
            row-key="order_id"
            :columns="orderColumns"
            :data-source="tabs.orderFail.rows"
            :loading="tabs.orderFail.loading"
            :pagination="false"
            :scroll="{ x: true, y: tableBodyHeight }"
            :custom-row="orderRow"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'op'">
                <a-space :size="8">
                  <a-button type="link" class="inline-link order-op-link" @click.stop="go(`/orders/${record.order_id}`)">处理</a-button>
                  <a-button type="link" class="inline-link order-op-link" @click.stop="closeRisk('order_fail', record)">关闭</a-button>
                </a-space>
              </template>
            </template>
          </a-table>
          <div class="table-pagination">
            <a-pagination
              v-model:current="tabs.orderFail.page"
              v-model:pageSize="tabs.orderFail.pageSize"
              :total="tabs.orderFail.total"
              :show-total="(t) => `共 ${t} 条`"
              :show-size-changer="true"
              :page-size-options="['10', '20', '50', '100']"
              @change="sliceOrderFail"
              @showSizeChange="handleOrderFailSizeChange"
            />
          </div>
        </a-tab-pane>
      </a-tabs>
    </a-card>
  </div>
</template>

<script setup>
import { computed, h, inject, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import { Tooltip, message } from 'ant-design-vue'
import { CopyOutlined } from '@ant-design/icons-vue'
import { getHomeDashboardApi } from '@/api/home'
import { getInstancesApi } from '@/api/admin'
import { getOrderListApi } from '@/api/order'
import { getBackupInstancesApi, getBackupNodesApi, getBackupTasksApi, getRestoreRunsApi } from '@/api/backup'
import { buildRiskKey, dismissItem, getWorkbenchUsername, isDismissedSync, loadAllDismissedKeys } from '@/utils/homeWorkbench'
import { useUserStore } from '@/store/user'

defineOptions({ name: 'HomeRiskDetailView' })

const router = useRouter()
const route = useRoute()
const userStore = useUserStore()
const username = computed(() => getWorkbenchUsername())

const canManageInstance = computed(() => userStore.hasPermission('system.instance.manage') || userStore.hasPermission('monitor.manage'))
const canManageBackup = computed(() => userStore.hasPermission('backup.task.manage'))
const canExecuteRestore = computed(() => userStore.hasPermission('backup.restore.execute'))
const canViewOrder = computed(() => userStore.hasPermission('order.view'))

// 全量拉取单页容量：风险项数量可控，一次性取回后在前端按关闭状态过滤并分页，
// 避免「后端分页 + 前端过滤」导致的每页条数被已关闭记录打散。
const FULL_PAGE_SIZE = 2000

const tabKeys = Object.freeze({
  instanceConn: 'instance_conn',
  manualBackupFail: 'manual_backup_fail',
  restoreFail: 'restore_fail',
  nodeOffline: 'node_offline',
  instanceNoBackup: 'instance_no_backup',
  dailyBackupMiss: 'daily_backup_miss',
  orderFail: 'order_fail',
})

const visibleTabKeys = computed(() => {
  const list = []
  if (canManageInstance.value) list.push(tabKeys.instanceConn)
  if (canManageBackup.value) {
    list.push(tabKeys.manualBackupFail)
    list.push(tabKeys.nodeOffline)
    list.push(tabKeys.instanceNoBackup)
    list.push(tabKeys.dailyBackupMiss)
  }
  if (canExecuteRestore.value) list.push(tabKeys.restoreFail)
  if (canViewOrder.value) list.push(tabKeys.orderFail)
  return list
})

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

let riskResizeObserver = null
onMounted(() => {
  setLayoutContentNoScroll?.(true)
  nextTick(() => {
    updateScrollY()
    window.addEventListener('resize', updateScrollY)
    if (typeof ResizeObserver !== 'undefined' && pageRef.value) {
      riskResizeObserver = new ResizeObserver(() => updateScrollY())
      riskResizeObserver.observe(pageRef.value)
    }
  })
})
onBeforeUnmount(() => {
  setLayoutContentNoScroll?.(false)
  window.removeEventListener('resize', updateScrollY)
  riskResizeObserver?.disconnect()
  riskResizeObserver = null
})

// 权限变化会改变 a-tabs 的 key 触发重挂载，重挂载后需重新测量表体高度。
watch(visibleTabKeys, () => nextTick(updateScrollY), { flush: 'post' })

const renderBackupTaskCell = (taskId, taskName, options = {}) => {
  const idFull = String(taskId ?? '').trim() || '-'
  const nameFull = String(taskName ?? '').trim() || '-'
  const copyable = options.copyable === true
  const idMaxLen = options.idMaxLen ?? Number.POSITIVE_INFINITY
  const idChars = Array.from(idFull)
  const idShort = idChars.length > idMaxLen ? idChars.slice(0, idMaxLen).join('') + '…' : idFull
  const nameChars = Array.from(nameFull)
  const nameShort = nameChars.length > 15 ? nameChars.slice(0, 15).join('') + '…' : nameFull
  const needTip = nameChars.length > 15
  const idStyle = { fontSize: '13px', color: '#17313d', fontWeight: 500 }
  const nameStyle = { marginTop: '4px', fontSize: '12px', color: '#8aa0ab', lineHeight: '1.4', maxWidth: '100%' }
  const copyBtn = copyable
    ? h(Tooltip, { title: '复制任务ID', placement: 'top' }, {
        default: () =>
          h(CopyOutlined, {
            class: 'cell-copy-icon',
            style: { marginLeft: '6px', cursor: 'pointer', color: '#8aa0ab', fontSize: '12px' },
            onClick: (e) => {
              e?.stopPropagation?.()
              copyValue(idFull, '任务ID已复制')
            },
          }),
      })
    : null
  const idLine = h('div', { class: 'cell-primary', style: idStyle }, [
    h(
      'span',
      {
        class: 'inline-link task-id-link',
        style: { cursor: 'pointer' },
        onClick: () => go('/backup/task'),
      },
      idShort,
    ),
    copyBtn,
  ])
  const nameLine = needTip
    ? h(Tooltip, { title: nameFull, placement: 'top' }, {
        default: () => h('div', { class: 'cell-secondary remark-line', style: nameStyle }, nameShort),
      })
    : h('div', { class: 'cell-secondary remark-line', style: nameStyle }, nameShort)
  return h('div', { class: 'instance-cell-block' }, [idLine, nameLine])
}

const truncateByRunes = (text, maxLen) => {
  const arr = Array.from(String(text ?? ''))
  return arr.length > maxLen ? arr.slice(0, maxLen).join('') + '…' : String(text ?? '')
}

// 风险详情页所有实例列统一按 25 个字符截断（超出显示 … 并悬浮提示完整内容）。
const renderInstanceCell = (primary, secondary, options = {}) => {
  const maxLen = options.maxLen ?? 25
  const primaryFull = String(primary ?? '').trim() || '-'
  const primaryText = truncateByRunes(primaryFull, maxLen)
  const secondaryText = String(secondary ?? '').trim()
  const secondaryFull = secondaryText || '-'
  const secondaryShort = secondaryText ? truncateByRunes(secondaryFull, maxLen) : ''
  const primaryNeedTip = Array.from(primaryFull).length > maxLen
  const secondaryNeedTip = secondaryText && Array.from(secondaryFull).length > maxLen
  const secondaryStyle = {
    marginTop: '4px',
    fontSize: '12px',
    color: '#8aa0ab',
    lineHeight: '1.4',
    maxWidth: '100%',
  }
  const children = [
    primaryNeedTip
      ? h(Tooltip, { title: primaryFull, placement: 'top' }, {
          default: () =>
            h(
              'div',
              {
                class: 'cell-primary ip-line has-tip',
                style: {
                  fontSize: '13px',
                  color: '#17313d',
                  fontWeight: 500,
                },
              },
              primaryText,
            ),
        })
      : h(
          'div',
          {
            class: 'cell-primary ip-line',
            style: {
              fontSize: '13px',
              color: '#17313d',
              fontWeight: 500,
            },
          },
          primaryText,
        ),
  ]
  if (secondaryText) {
    children.push(
      secondaryNeedTip
        ? h(Tooltip, { title: secondaryFull, placement: 'top' }, {
            default: () =>
              h(
                'div',
                {
                  class: 'cell-secondary remark-line has-tip',
                  style: secondaryStyle,
                },
                secondaryShort,
              ),
          })
        : h(
            'div',
            {
              class: 'cell-secondary remark-line',
              style: secondaryStyle,
            },
            secondaryShort,
          ),
    )
  }
  return h('div', { class: 'instance-cell-block' }, children)
}

const copyValue = async (value, tip = '复制成功') => {
  const text = String(value || '')
  if (!text) return
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
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
    message.success({ content: tip, key: 'risk-detail-copy-feedback', duration: 1 })
  } catch {
    message.error({ content: '复制失败', key: 'risk-detail-copy-feedback', duration: 1.2 })
  }
}

const copyOrderId = async (orderId) => copyValue(orderId, '复制成功')

const uiState = reactive({
  loading: false,
})

const uiData = reactive({
  activeKey: tabKeys.orderFail,
})

const switchActiveToFirstVisible = () => {
  if (!visibleTabKeys.value.includes(uiData.activeKey)) {
    uiData.activeKey = visibleTabKeys.value[0] || tabKeys.orderFail
  }
}
switchActiveToFirstVisible()
watch(visibleTabKeys, switchActiveToFirstVisible, { flush: 'post' })

// 统一采用「全量拉取 → 过滤已关闭 → 前端分页」的模式：
// 后端分页是基于未剔除已关闭记录的全集做的，若先分页再在前端过滤，
// 每页实际渲染条数会被已关闭记录打散（例如 16 条中关闭 5 条，会变成第一页 5 条、第二页 6 条）。
const tabs = reactive({
  instanceConn: { loading: false, allRows: [], rows: [], page: 1, pageSize: 10, total: 0 },
  manualBackupFail: { loading: false, allRows: [], rows: [], page: 1, pageSize: 10, total: 0 },
  restoreFail: { loading: false, allRows: [], rows: [], page: 1, pageSize: 10, total: 0 },
  nodeOffline: { loading: false, allRows: [], rows: [], page: 1, pageSize: 10, total: 0 },
  instanceNoBackup: { loading: false, allRows: [], rows: [], page: 1, pageSize: 10, total: 0 },
  dailyBackupMiss: { loading: false, allRows: [], rows: [], page: 1, pageSize: 10, total: 0 },
  orderFail: { loading: false, allRows: [], rows: [], page: 1, pageSize: 10, total: 0 },
})

// tab 的显示顺序 = 模板中 a-tab-pane 的先后顺序，决定「位置靠右」的判定依据。
const tabDisplayOrder = [
  tabKeys.instanceConn,
  tabKeys.manualBackupFail,
  tabKeys.restoreFail,
  tabKeys.nodeOffline,
  tabKeys.instanceNoBackup,
  tabKeys.dailyBackupMiss,
  tabKeys.orderFail,
]

const tabRefByKey = {
  [tabKeys.instanceConn]: tabs.instanceConn,
  [tabKeys.manualBackupFail]: tabs.manualBackupFail,
  [tabKeys.restoreFail]: tabs.restoreFail,
  [tabKeys.nodeOffline]: tabs.nodeOffline,
  [tabKeys.instanceNoBackup]: tabs.instanceNoBackup,
  [tabKeys.dailyBackupMiss]: tabs.dailyBackupMiss,
  [tabKeys.orderFail]: tabs.orderFail,
}

// 从首页「风险详情」入口进入时（?focus=max）定位目标 tab：
// 1) 先按问题数量取最大值；2) 并列最多时，在显示顺序中从右到左取第一个命中的 tab。
// 只在当前用户可见的 tab（visibleTabKeys）范围内比较。
const pickMostIssuesTabKey = () => {
  const order = tabDisplayOrder.filter((key) => visibleTabKeys.value.includes(key))
  if (!order.length) return tabKeys.orderFail
  const countOf = (key) => Math.max(0, Number(tabRefByKey[key]?.total || 0))
  const maxCount = Math.max(...order.map(countOf))
  for (let i = order.length - 1; i >= 0; i -= 1) {
    if (countOf(order[i]) === maxCount) return order[i]
  }
  return order[order.length - 1]
}

const cache = reactive({
  backupTasksLoading: false,
  backupTasksLoaded: false,
  backupTasks: [],
  backupInstancesLoading: false,
  backupInstancesLoaded: false,
  backupInstances: [],
})

let pendingBackupTasks = null
let pendingBackupInstances = null

const go = (path, query) => {
  router.push({ path, query })
}

const paginate = (rows, page, pageSize) => {
  const p = Math.max(1, Number(page || 1))
  const s = Math.max(1, Number(pageSize || 10))
  const start = (p - 1) * s
  return rows.slice(start, start + s)
}

const scheduleSummary = (daysJson, time) => {
  const formatTime = (raw) => {
    const t = String(raw || '').trim()
    if (!t) return ''
    const match = t.match(/^(\d{1,2}):(\d{1,2})(?::(\d{1,2}))?$/)
    if (match) {
      const h = match[1].padStart(2, '0')
      const m = match[2].padStart(2, '0')
      return `${h}:${m}`
    }
    return t
  }
  const mapDayName = (n) => {
    if (n === '7' || n === 7 || n === '0' || n === 0) return '日'
    if (typeof n === 'number') return String(n)
    return String(n)
  }
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
  const timeLabel = formatTime(time)
  if (!timeLabel && (!Array.isArray(days) || days.length === 0)) return '未设置周期'
  let dayLabel
  if (Array.isArray(days) && days.length) {
    const set = new Set(days.map((d) => (typeof d === 'string' ? d : String(d))))
    const ordered = []
    ;['1', '2', '3', '4', '5', '6', '7'].forEach((d) => {
      if (set.has(d)) ordered.push(d)
    })
    days.forEach((d) => {
      const s = typeof d === 'string' ? d : String(d)
      if (!['1', '2', '3', '4', '5', '6', '7'].includes(s) && !ordered.includes(s)) ordered.push(s)
    })
    if (ordered.length === 7) {
      dayLabel = '周1/2/3/4/5/6/日'
    } else {
      dayLabel = '周' + ordered.map(mapDayName).join('/')
    }
  } else {
    dayLabel = '每天'
  }
  return `${dayLabel}${timeLabel ? ` ${timeLabel}` : ''}`
}

const taskStatusLabel = (status) => {
  if (status === 'SUCCESS') return '成功'
  if (status === 'RUNNING') return '备份中'
  if (status === 'STOPPED') return '已停止'
  if (status === 'FAILED') return '备份失败'
  return '未启动'
}

const loadAllBackupTasks = async () => {
  if (cache.backupTasksLoaded) return
  if (pendingBackupTasks) return pendingBackupTasks
  pendingBackupTasks = (async () => {
    cache.backupTasksLoading = true
    try {
      const res = await getBackupTasksApi({ page: 1, page_size: 2000, is_page: true }).catch(() => null)
      cache.backupTasks = Array.isArray(res?.data) ? res.data : []
      cache.backupTasksLoaded = true
    } finally {
      cache.backupTasksLoading = false
      pendingBackupTasks = null
    }
  })()
  return pendingBackupTasks
}

const loadAllBackupInstances = async () => {
  if (cache.backupInstancesLoaded) return
  if (pendingBackupInstances) return pendingBackupInstances
  pendingBackupInstances = (async () => {
    cache.backupInstancesLoading = true
    try {
      const res = await getBackupInstancesApi({ page: 1, page_size: 2000, is_page: true }).catch(() => null)
      cache.backupInstances = Array.isArray(res?.data) ? res.data : []
      cache.backupInstancesLoaded = true
    } finally {
      cache.backupInstancesLoading = false
      pendingBackupInstances = null
    }
  })()
  return pendingBackupInstances
}

const instanceColumns = [
  {
    title: '实例',
    key: 'instance',
    width: 170,
    // 第一行实例名称，第二行淡灰小字 ip:port；两者均保留前 25 个字符，超出截断并悬浮提示完整内容。
    customRender: ({ record }) => {
      const name = String(record?.remark || '').trim() || '-'
      const host = record?.hostname
      const port = record?.port
      const hasPort = port != null && String(port) !== ''
      const ipLine = host ? (hasPort ? `${host}:${port}` : String(host)) : ''
      return renderInstanceCell(name, ipLine, { maxLen: 25 })
    },
  },
  { title: '环境', dataIndex: 'environment_name', key: 'environment_name', width: 100, ellipsis: true },
  { title: '状态', key: 'status', width: 80, className: 'conn-status-cell' },
  { title: '最近检测', key: 'checked_at', width: 170 },
  { title: '异常信息', dataIndex: 'last_check_message', key: 'last_check_message', width: 300, ellipsis: true },
  { title: '操作', key: 'op', width: 80 },
]

const loadInstanceConn = async () => {
  if (!canManageInstance.value) {
    tabs.instanceConn.allRows = []
    tabs.instanceConn.rows = []
    tabs.instanceConn.total = 0
    return
  }
  tabs.instanceConn.loading = true
  try {
    const res = await getInstancesApi({
      page: 1,
      page_size: FULL_PAGE_SIZE,
      is_page: true,
      status: 'error',
    }).catch(() => null)
    const rows = Array.isArray(res?.data) ? res.data : []
    const filtered = rows.filter(
      (row) => !isDismissedSync(username.value, 'instance_conn', buildRiskKey('instance_conn', row)),
    )
    tabs.instanceConn.allRows = filtered
    tabs.instanceConn.total = filtered.length
    sliceInstanceConn()
  } finally {
    tabs.instanceConn.loading = false
  }
}

const sliceInstanceConn = () => {
  tabs.instanceConn.rows = paginate(
    tabs.instanceConn.allRows || [],
    tabs.instanceConn.page,
    tabs.instanceConn.pageSize,
  )
}

const handleInstanceConnSizeChange = (_current, size) => {
  tabs.instanceConn.page = 1
  tabs.instanceConn.pageSize = size
  sliceInstanceConn()
}

const backupFailedColumns = [
  {
    title: '备份任务',
    key: 'task_id',
    width: 220,
    // 第一行任务 ID（主题色 + 复制图标，点击跳备份任务页），第二行淡灰小字任务名称
    customRender: ({ record }) =>
      renderBackupTaskCell(record.display_id || record.id, record.name, { copyable: true }),
  },
  {
    title: '实例',
    key: 'instance',
    width: 220,
    // 第一行实例名称，第二行淡灰小字 IP:port
    customRender: ({ record }) =>
      renderInstanceCell(record.instance_remark, `${record.instance_host}:${record.instance_port}`),
  },
  { title: '触发时间', key: 'time', width: 160 },
  { title: '失败原因', key: 'reason', ellipsis: true },
  { title: '操作', key: 'op', width: 90 },
]

const loadManualBackupFail = async () => {
  if (!canManageBackup.value) {
    tabs.manualBackupFail.allRows = []
    tabs.manualBackupFail.rows = []
    tabs.manualBackupFail.total = 0
    return
  }
  tabs.manualBackupFail.loading = true
  try {
    await loadAllBackupTasks()
    const todayStart = dayjs().startOf('day')
    const filtered = (cache.backupTasks || []).filter((item) => {
      if (String(item?.last_trigger_mode || '').toUpperCase() !== 'MANUAL') return false
      if (String(item?.last_status || '').toUpperCase() !== 'FAILED') return false
      if (!item?.last_start_at) return true
      return dayjs(item.last_start_at).isAfter(todayStart.subtract(7, 'day'))
    })
    const filtered2 = filtered.filter(
      (row) => !isDismissedSync(username.value, 'manual_backup_fail', buildRiskKey('manual_backup_fail', row)),
    )
    tabs.manualBackupFail.allRows = filtered2
    tabs.manualBackupFail.total = filtered2.length
    sliceManualBackupFail()
  } finally {
    tabs.manualBackupFail.loading = false
  }
}

const sliceManualBackupFail = () => {
  tabs.manualBackupFail.rows = paginate(
    tabs.manualBackupFail.allRows || [],
    tabs.manualBackupFail.page,
    tabs.manualBackupFail.pageSize,
  )
}

const handleManualBackupFailSizeChange = (_current, size) => {
  tabs.manualBackupFail.page = 1
  tabs.manualBackupFail.pageSize = size
  sliceManualBackupFail()
}

const restoreColumns = [
  { title: '备份集ID', dataIndex: 'backup_set_id', key: 'backup_set_id', width: 180, ellipsis: true },
  {
    title: '目标实例',
    key: 'target',
    width: 220,
    // 第一行展示实例名称，第二行展示淡灰色 IP:port
    customRender: ({ record }) => renderInstanceCell(record.target_remark, `${record.target_host}:${record.target_port}`),
  },
  { title: '目标', dataIndex: 'target_display', key: 'target_display', width: 180, ellipsis: true },
  { title: '状态', key: 'status', width: 90 },
  // 仅恢复失败 tab 的时间列禁止换行
  { title: '更新时间', key: 'time', width: 160, className: 'restore-time-cell' },
  { title: '异常信息', dataIndex: 'error_message', key: 'error_message', ellipsis: true },
  { title: '操作', key: 'op', width: 90 },
]

const loadRestoreFail = async () => {
  if (!canExecuteRestore.value) {
    tabs.restoreFail.allRows = []
    tabs.restoreFail.rows = []
    tabs.restoreFail.total = 0
    return
  }
  tabs.restoreFail.loading = true
  try {
    const res = await getRestoreRunsApi({
      page: 1,
      page_size: FULL_PAGE_SIZE,
      is_page: true,
      status: 'FAILED',
    }).catch(() => null)
    const rows = Array.isArray(res?.data) ? res.data : []
    const filtered = rows.filter((row) => !isDismissedSync(username.value, 'restore_fail', buildRiskKey('restore_fail', row)))
    tabs.restoreFail.allRows = filtered
    tabs.restoreFail.total = filtered.length
    sliceRestoreFail()
  } finally {
    tabs.restoreFail.loading = false
  }
}

const sliceRestoreFail = () => {
  tabs.restoreFail.rows = paginate(
    tabs.restoreFail.allRows || [],
    tabs.restoreFail.page,
    tabs.restoreFail.pageSize,
  )
}

const handleRestoreFailSizeChange = (_current, size) => {
  tabs.restoreFail.page = 1
  tabs.restoreFail.pageSize = size
  sliceRestoreFail()
}

const nodeColumns = [
  { title: '节点', key: 'node' },
  { title: '状态', key: 'status', width: 90 },
  { title: '备注', dataIndex: 'remark', key: 'remark', ellipsis: true },
  { title: '操作', key: 'action', width: 140 },
  { title: '关闭', key: 'op', width: 90 },
]

const loadNodeOffline = async () => {
  if (!canManageBackup.value) {
    tabs.nodeOffline.allRows = []
    tabs.nodeOffline.rows = []
    tabs.nodeOffline.total = 0
    return
  }
  tabs.nodeOffline.loading = true
  try {
    const res = await getBackupNodesApi({
      page: 1,
      page_size: FULL_PAGE_SIZE,
      is_page: true,
      status: 'OFFLINE',
    }).catch(() => null)
    const rows = Array.isArray(res?.data) ? res.data : []
    const filtered = rows.filter((row) => !isDismissedSync(username.value, 'node_offline', buildRiskKey('node_offline', row)))
    tabs.nodeOffline.allRows = filtered
    tabs.nodeOffline.total = filtered.length
    sliceNodeOffline()
  } finally {
    tabs.nodeOffline.loading = false
  }
}

const sliceNodeOffline = () => {
  tabs.nodeOffline.rows = paginate(
    tabs.nodeOffline.allRows || [],
    tabs.nodeOffline.page,
    tabs.nodeOffline.pageSize,
  )
}

const handleNodeOfflineSizeChange = (_current, size) => {
  tabs.nodeOffline.page = 1
  tabs.nodeOffline.pageSize = size
  sliceNodeOffline()
}

const backupInstanceColumns = [
  {
    title: '实例',
    key: 'instance',
    width: 220,
    // 第一行实例名称，第二行淡灰小字 IP:port；两行均超过 25 个字符才截断并悬浮显示完整内容。
    customRender: ({ record }) =>
      renderInstanceCell(record.remark, `${record.hostname}:${record.port}`, { maxLen: 25 }),
  },
  { title: '类型', dataIndex: 'db_type', key: 'db_type', width: 120 },
  { title: '操作', key: 'action', width: 180, align: 'center' },
]

const loadInstanceNoBackup = async () => {
  if (!canManageBackup.value) {
    tabs.instanceNoBackup.allRows = []
    tabs.instanceNoBackup.rows = []
    tabs.instanceNoBackup.total = 0
    return
  }
  tabs.instanceNoBackup.loading = true
  try {
    await Promise.all([loadAllBackupInstances(), loadAllBackupTasks()])
    const protectedIds = new Set(
      (cache.backupTasks || []).filter((item) => !!item?.enabled).map((item) => Number(item.source_instance_id)),
    )
    const filtered = (cache.backupInstances || [])
      .filter((item) => !protectedIds.has(Number(item.id)))
      .filter((row) => !isDismissedSync(username.value, 'instance_no_backup', buildRiskKey('instance_no_backup', row)))
    tabs.instanceNoBackup.allRows = filtered
    tabs.instanceNoBackup.total = filtered.length
    sliceInstanceNoBackup()
  } finally {
    tabs.instanceNoBackup.loading = false
  }
}

const sliceInstanceNoBackup = () => {
  tabs.instanceNoBackup.rows = paginate(
    tabs.instanceNoBackup.allRows || [],
    tabs.instanceNoBackup.page,
    tabs.instanceNoBackup.pageSize,
  )
}

const handleInstanceNoBackupSizeChange = (_current, size) => {
  tabs.instanceNoBackup.page = 1
  tabs.instanceNoBackup.pageSize = size
  sliceInstanceNoBackup()
}

const backupDailyColumns = [
  {
    title: '备份任务',
    key: 'task_id',
    width: 240,
    customRender: ({ record }) => renderBackupTaskCell(record.display_id || record.id, record.name),
  },
  {
    title: '实例',
    key: 'instance',
    width: 220,
    customRender: ({ record }) => renderInstanceCell(`${record.instance_host}:${record.instance_port}`, record.instance_remark),
  },
  { title: '调度周期', key: 'schedule', width: 340 },
  { title: '最近结束', key: 'last', width: 160 },
  { title: '状态', key: 'status', width: 110 },
  { title: '操作', key: 'op', width: 90 },
]

const parseScheduleDays = (value) => {
  try {
    if (Array.isArray(value)) return value
    if (typeof value === 'string' && value.trim()) return JSON.parse(value)
    if (value && typeof value === 'object') return JSON.parse(JSON.stringify(value))
  } catch {
    return []
  }
  return []
}

const isPeriodicTask = (task) => {
  const days = parseScheduleDays(task?.schedule_days)
  const time = String(task?.schedule_time || '').trim()
  return time !== '' || (Array.isArray(days) && days.length > 0)
}

const loadDailyBackupMiss = async () => {
  if (!canManageBackup.value) {
    tabs.dailyBackupMiss.allRows = []
    tabs.dailyBackupMiss.rows = []
    tabs.dailyBackupMiss.total = 0
    return
  }
  tabs.dailyBackupMiss.loading = true
  try {
    await loadAllBackupTasks()
    const start = dayjs().startOf('day')
    const filtered = (cache.backupTasks || []).filter((item) => {
      if (!item?.enabled) return false
      if (!isPeriodicTask(item)) return false
      const lastEnd = item?.last_end_at ? dayjs(item.last_end_at) : null
      const lastStatus = String(item?.last_status || '').toUpperCase()
      if (lastStatus === 'SUCCESS' && lastEnd && lastEnd.isAfter(start)) return false
      return true
    })
    const day = dayjs().format('YYYY-MM-DD')
    const filtered2 = filtered.filter(
      (row) => !isDismissedSync(username.value, 'daily_backup_miss', buildRiskKey('daily_backup_miss', row, { day })),
    )
    tabs.dailyBackupMiss.allRows = filtered2
    tabs.dailyBackupMiss.total = filtered2.length
    sliceDailyBackupMiss()
  } finally {
    tabs.dailyBackupMiss.loading = false
  }
}

const sliceDailyBackupMiss = () => {
  tabs.dailyBackupMiss.rows = paginate(
    tabs.dailyBackupMiss.allRows || [],
    tabs.dailyBackupMiss.page,
    tabs.dailyBackupMiss.pageSize,
  )
}

const handleDailyBackupMissSizeChange = (_current, size) => {
  tabs.dailyBackupMiss.page = 1
  tabs.dailyBackupMiss.pageSize = size
  sliceDailyBackupMiss()
}

// 工单执行失败 tab 四列等宽，由 scoped CSS 的 .order-fail-table 统一控制每列 25%。
const orderColumns = computed(() => [
  {
    title: '工单',
    key: 'order',
    align: 'left',
    customRender: ({ record }) => {
      const orderId = String(record?.order_id || '').trim() || '-'
      const title = String(record?.title || '').trim() || '-'
      return h(
        'div',
        { class: 'instance-cell-block', style: { alignItems: 'flex-start' } },
        [
          h(
            'div',
            {
              class: 'cell-primary',
              style: {
                display: 'inline-flex',
                alignItems: 'center',
                gap: '6px',
                fontSize: '13px',
                fontWeight: 500,
              },
            },
            [
              h('span', { class: 'order-id-text' }, orderId),
              h(CopyOutlined, {
                style: { cursor: 'pointer', color: '#8aa0ab', fontSize: '12px' },
                onClick: (e) => {
                  e?.stopPropagation?.()
                  copyOrderId(record?.order_id)
                },
              }),
            ],
          ),
          h(
            'div',
            {
              class: 'cell-secondary remark-line',
              style: { marginTop: '4px', fontSize: '12px', color: '#8aa0ab', lineHeight: '1.4' },
            },
            title,
          ),
        ],
      )
    },
  },
  {
    title: '实例',
    key: 'instance',
    align: 'left',
    // 表头与表体统一靠左：除 align 外，scoped CSS 也按列序（nth-child）强制了一次，双保险。
    customRender: ({ record }) => {
      const name = String(record?.instance_name || '').trim() || '-'
      const host = record?.instance_host
      const port = record?.instance_port
      const hasPort = port != null && String(port) !== ''
      const ipLine = host ? (hasPort ? `${host}:${port}` : String(host)) : ''
      // 第一行实例名称，第二行淡灰色小字 IP:port；IP:port 为空时不渲染第二行。
      // 实例名与 IP:port 均保留前 25 个字符，超出截断并悬浮提示完整内容。
      return renderInstanceCell(name, ipLine, { maxLen: 25 })
    },
  },
  {
    title: '创建时间',
    dataIndex: 'created_at',
    key: 'created_at',
    align: 'left',
    className: 'order-created-cell',
    customRender: ({ text }) => (text ? dayjs(text).format('YYYY-MM-DD HH:mm') : '-'),
  },
  { title: '操作', key: 'op', align: 'center' },
])

const orderRow = (record) => ({
  onClick: () => {
    if (record?.order_id) {
      go(`/orders/${record.order_id}`)
    }
  },
})

const loadOrderFail = async () => {
  if (!canViewOrder.value) {
    tabs.orderFail.allRows = []
    tabs.orderFail.rows = []
    tabs.orderFail.total = 0
    return
  }
  tabs.orderFail.loading = true
  try {
    const res = await getOrderListApi({
      page: 1,
      page_size: FULL_PAGE_SIZE,
      is_page: true,
      progress: 'FAILED',
      // 风险详情需要与首页 dashboard 统计口径一致，包含实例已失效的工单。
      no_db_type_filter: true,
    }).catch(() => null)
    const rows = Array.isArray(res?.data) ? res.data : []
    const filtered = rows.filter((row) => !isDismissedSync(username.value, 'order_fail', buildRiskKey('order_fail', row)))
    tabs.orderFail.allRows = filtered
    tabs.orderFail.total = filtered.length
    sliceOrderFail()
  } finally {
    tabs.orderFail.loading = false
  }
}

const sliceOrderFail = () => {
  tabs.orderFail.rows = paginate(
    tabs.orderFail.allRows || [],
    tabs.orderFail.page,
    tabs.orderFail.pageSize,
  )
}

const handleOrderFailSizeChange = (_current, size) => {
  tabs.orderFail.page = 1
  tabs.orderFail.pageSize = size
  sliceOrderFail()
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
    if (uiData.activeKey === tabKeys.instanceConn) {
      await loadInstanceConn()
      return
    }
    if (uiData.activeKey === tabKeys.manualBackupFail) {
      await loadManualBackupFail()
      return
    }
    if (uiData.activeKey === tabKeys.restoreFail) {
      await loadRestoreFail()
      return
    }
    if (uiData.activeKey === tabKeys.nodeOffline) {
      await loadNodeOffline()
      return
    }
    if (uiData.activeKey === tabKeys.instanceNoBackup) {
      await loadInstanceNoBackup()
      return
    }
    if (uiData.activeKey === tabKeys.dailyBackupMiss) {
      await loadDailyBackupMiss()
      return
    }
    if (uiData.activeKey === tabKeys.orderFail) {
      await loadOrderFail()
    }
  } finally {
    uiState.loading = false
  }
}

// 从首页 dashboard 接口读取各风险类型的统计数量，确保风险详情页 tab 数量与首页风险提醒完全一致。
const loadRiskCountsFromDashboard = async () => {
  try {
    const res = await getHomeDashboardApi({}, { skipGlobalMessageError: true, silentHttpError: true })
    const rc = res?.data?.risk_counts
    if (!rc || typeof rc !== 'object') return
    if (canManageInstance.value && typeof rc.instance_conn === 'number') {
      tabs.instanceConn.total = Math.max(0, Number(rc.instance_conn))
    }
    if (canManageBackup.value) {
      if (typeof rc.manual_backup_fail === 'number') {
        tabs.manualBackupFail.total = Math.max(0, Number(rc.manual_backup_fail))
      }
      if (typeof rc.node_offline === 'number') {
        tabs.nodeOffline.total = Math.max(0, Number(rc.node_offline))
      }
      if (typeof rc.instance_no_backup === 'number') {
        tabs.instanceNoBackup.total = Math.max(0, Number(rc.instance_no_backup))
      }
      if (typeof rc.daily_backup_miss === 'number') {
        tabs.dailyBackupMiss.total = Math.max(0, Number(rc.daily_backup_miss))
      }
    }
    if (canExecuteRestore.value && typeof rc.restore_fail === 'number') {
      tabs.restoreFail.total = Math.max(0, Number(rc.restore_fail))
    }
    if (canViewOrder.value && typeof rc.order_fail === 'number') {
      tabs.orderFail.total = Math.max(0, Number(rc.order_fail))
    }
  } catch {
    // 失败时仍由各个 loadXxx 自行计算 total
  }
}

onMounted(async () => {
  switchActiveToFirstVisible()
  await loadRiskCountsFromDashboard()
  try {
    await loadAllDismissedKeys(
      [
        'instance_conn',
        'manual_backup_fail',
        'restore_fail',
        'node_offline',
        'instance_no_backup',
        'daily_backup_miss',
        'order_fail',
      ],
      { silentError: true },
    )
  } catch {
    /* ignore */
  }
  const tasks = []
  if (canManageInstance.value) tasks.push(loadInstanceConn())
  if (canManageBackup.value) {
    tasks.push(loadManualBackupFail())
    tasks.push(loadNodeOffline())
    tasks.push(loadInstanceNoBackup())
    tasks.push(loadDailyBackupMiss())
  }
  if (canExecuteRestore.value) tasks.push(loadRestoreFail())
  if (canViewOrder.value) tasks.push(loadOrderFail())
  await Promise.all(tasks)

  // 首页入口带 focus=max 时，落在此处按最终数量定位 tab（数据已加载完，总数即页面显示值）。
  if (String(route.query?.focus || '') === 'max') {
    uiData.activeKey = pickMostIssuesTabKey()
  }
  nextTick(updateScrollY)
})

const closeRisk = async (type, record) => {
  const t = String(type || '')
  const day = dayjs().format('YYYY-MM-DD')
  const key = buildRiskKey(t, record, t === 'daily_backup_miss' ? { day } : undefined)
  if (!key) return
  try {
    await dismissItem(username.value, t, key)
  } catch (e) {
    message.error('关闭失败：' + (e?.message || e || '未知错误'))
    return
  }

  if (t === 'instance_conn') {
    tabs.instanceConn.allRows = (tabs.instanceConn.allRows || []).filter(
      (row) => buildRiskKey('instance_conn', row) !== key,
    )
    tabs.instanceConn.total = tabs.instanceConn.allRows.length
    sliceInstanceConn()
  } else if (t === 'restore_fail') {
    tabs.restoreFail.allRows = (tabs.restoreFail.allRows || []).filter((row) => buildRiskKey('restore_fail', row) !== key)
    tabs.restoreFail.total = tabs.restoreFail.allRows.length
    sliceRestoreFail()
  } else if (t === 'node_offline') {
    tabs.nodeOffline.allRows = (tabs.nodeOffline.allRows || []).filter((row) => buildRiskKey('node_offline', row) !== key)
    tabs.nodeOffline.total = tabs.nodeOffline.allRows.length
    sliceNodeOffline()
  } else if (t === 'order_fail') {
    tabs.orderFail.allRows = (tabs.orderFail.allRows || []).filter((row) => buildRiskKey('order_fail', row) !== key)
    tabs.orderFail.total = tabs.orderFail.allRows.length
    sliceOrderFail()
  } else if (t === 'manual_backup_fail') {
    tabs.manualBackupFail.allRows = (tabs.manualBackupFail.allRows || []).filter(
      (row) => buildRiskKey('manual_backup_fail', row) !== key,
    )
    tabs.manualBackupFail.total = Math.max(0, Number(tabs.manualBackupFail.total || 0) - 1)
    sliceManualBackupFail()
  } else if (t === 'instance_no_backup') {
    tabs.instanceNoBackup.allRows = (tabs.instanceNoBackup.allRows || []).filter(
      (row) => buildRiskKey('instance_no_backup', row) !== key,
    )
    tabs.instanceNoBackup.total = Math.max(0, Number(tabs.instanceNoBackup.total || 0) - 1)
    sliceInstanceNoBackup()
  } else if (t === 'daily_backup_miss') {
    tabs.dailyBackupMiss.allRows = (tabs.dailyBackupMiss.allRows || []).filter(
      (row) => buildRiskKey('daily_backup_miss', row, { day }) !== key,
    )
    tabs.dailyBackupMiss.total = Math.max(0, Number(tabs.dailyBackupMiss.total || 0) - 1)
    sliceDailyBackupMiss()
  }

  message.success('已关闭')
}
</script>

<style scoped>
/* 满高布局：页面自身不滚动，滚动只发生在各 tab 表格的表体内部。
   高度经 page → card → card-body → tabs → content → tabpane 逐层 flex 传导。 */
.risk-detail-page {
  display: flex;
  flex-direction: column;
  gap: var(--gi-spacing-md);
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

.risk-detail-toolbar {
  flex: 0 0 auto;
}

.risk-detail-toolbar .toolbar-left {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.risk-detail-toolbar .title {
  font-size: 18px;
  font-weight: 700;
  color: #14313d;
}

.risk-detail-toolbar .subtitle {
  font-size: 12px;
  color: #6b8090;
}

.risk-detail-card {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  border-radius: 22px;
  border: 1px solid rgba(27, 73, 88, 0.06);
  box-shadow: 0 10px 24px rgba(18, 58, 72, 0.05);
  overflow: hidden;
}

/* 卡片体 / Tabs / 内容区 逐层向下传导高度，最终只有表格表体可滚动 */
.risk-detail-card :deep(.ant-card-body) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.risk-detail-card :deep(.ant-tabs) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.risk-detail-card :deep(.ant-tabs-content-holder) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.risk-detail-card :deep(.ant-tabs-content) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.risk-detail-card :deep(.ant-tabs-tabpane) {
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
}

.instance-cell-block {
  display: flex;
  flex-direction: column;
  gap: 4px;
  line-height: 1.4;
}

.instance-cell-block .ip-line {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  font-size: 13px;
  color: #17313d;
  font-weight: 500;
  word-break: break-all;
}

.instance-cell-block .cell-secondary,
.instance-cell-block .remark-line {
  margin-top: 4px;
  font-size: 12px;
  color: #8aa0ab;
  line-height: 1.4;
  max-width: 100%;
}

.instance-cell-block .remark-line.has-tip {
  cursor: help;
  border-bottom: 1px dashed rgba(107, 128, 144, 0.35);
}

:deep(.instance-conn-table .ant-table-content) {
  overflow-x: auto;
}

:deep(.instance-conn-table table) {
  table-layout: fixed !important;
  width: 100% !important;
}

:deep(.instance-conn-table colgroup col:nth-child(1)),
:deep(.instance-conn-table .ant-table-thead > tr > th:nth-child(1)),
:deep(.instance-conn-table .ant-table-tbody > tr > td:nth-child(1)) {
  width: 20% !important;
}

:deep(.instance-conn-table colgroup col:nth-child(2)),
:deep(.instance-conn-table .ant-table-thead > tr > th:nth-child(2)),
:deep(.instance-conn-table .ant-table-tbody > tr > td:nth-child(2)) {
  width: 10% !important;
}

:deep(.instance-conn-table colgroup col:nth-child(3)),
:deep(.instance-conn-table .ant-table-thead > tr > th:nth-child(3)),
:deep(.instance-conn-table .ant-table-tbody > tr > td:nth-child(3)) {
  width: 8% !important;
}

:deep(.instance-conn-table colgroup col:nth-child(4)),
:deep(.instance-conn-table .ant-table-thead > tr > th:nth-child(4)),
:deep(.instance-conn-table .ant-table-tbody > tr > td:nth-child(4)) {
  width: 22% !important;
}

:deep(.instance-conn-table colgroup col:nth-child(5)),
:deep(.instance-conn-table .ant-table-thead > tr > th:nth-child(5)),
:deep(.instance-conn-table .ant-table-tbody > tr > td:nth-child(5)) {
  width: 30% !important;
}

:deep(.instance-conn-table colgroup col:nth-child(6)),
:deep(.instance-conn-table .ant-table-thead > tr > th:nth-child(6)),
:deep(.instance-conn-table .ant-table-tbody > tr > td:nth-child(6)) {
  width: 10% !important;
}

/* 恢复失败 tab 的更新时间列禁止换行 */
:deep(.restore-time-cell) {
  white-space: nowrap;
}

/* 工单执行失败 tab 四列等宽，窄屏下横向滚动 */
:deep(.order-fail-table .ant-table-content > table) {
  table-layout: fixed !important;
}

/* 四列等宽 —— 关键是让表头与表体的列宽严格一致。
   固定表头（scroll.y）时 antd 会在表头额外插入一个隐藏的「滚动条占位列」（表头第 5 个 th）。
   若用 th/td 全量选择器一律写 25%，表头就成 5 × 25% = 125%，浏览器按比例压成每列 20%，
   而表体仍是 25% → 表头与表体列宽错位（实测偏差 58 / 117 / 175px），
   表现为「实例/创建时间表体看着没靠左、操作表头看着没居中」（其实各自都对齐了，是列框错位）。
   正解：宽度只作用于前 4 列，并把占位列归零，使两张表的列宽严格相等（实测偏差 0/0/0/0）。 */
:deep(.order-fail-table colgroup col:nth-child(-n + 4)),
:deep(.order-fail-table .ant-table-thead > tr > th:nth-child(-n + 4)),
:deep(.order-fail-table .ant-table-tbody > tr > td:nth-child(-n + 4)) {
  width: 25% !important;
}

:deep(.order-fail-table colgroup col:nth-child(n + 5)),
:deep(.order-fail-table .ant-table-thead > tr > th.ant-table-cell-scrollbar),
:deep(.order-fail-table .ant-table-tbody > tr > td.ant-table-cell-scrollbar) {
  width: 0 !important;
  padding: 0 !important;
}

/* 工单执行失败 tab 列序固定：1 工单 / 2 实例 / 3 创建时间 / 4 操作。
   列的 align 会渲染成单元格内联 text-align，但为免主题样式或其它规则干扰，
   这里按列序再显式落一次（带 !important），保证表头与表体口径完全一致：
   工单/实例/创建时间 左右都靠左，操作 头尾都居中。 */
:deep(.order-fail-table .ant-table-thead > tr > th:nth-child(1)),
:deep(.order-fail-table .ant-table-thead > tr > th:nth-child(2)),
:deep(.order-fail-table .ant-table-thead > tr > th:nth-child(3)),
:deep(.order-fail-table .ant-table-tbody > tr > td:nth-child(1)),
:deep(.order-fail-table .ant-table-tbody > tr > td:nth-child(2)),
:deep(.order-fail-table .ant-table-tbody > tr > td:nth-child(3)) {
  text-align: left !important;
}

:deep(.order-fail-table .ant-table-thead > tr > th:nth-child(4)),
:deep(.order-fail-table .ant-table-tbody > tr > td:nth-child(4)) {
  text-align: center !important;
}

:deep(.order-fail-table .ant-table-tbody > tr > td .instance-cell-block) {
  gap: 2px;
  align-items: flex-start;
}

:deep(.order-fail-table .ant-table-tbody > tr > td .cell-secondary) {
  margin-top: 2px;
}

/* 实例列：内容左对齐且文字不加粗（renderInstanceCell 内联写了 fontWeight: 500，需 !important 覆盖） */
:deep(.order-fail-table .instance-cell-block .ip-line) {
  font-weight: 400 !important;
}

:deep(.order-fail-table .instance-cell-block .cell-secondary) {
  font-weight: 400;
}

/* 创建时间列：左对齐且文字不加粗 */
:deep(.order-fail-table .order-created-cell) {
  text-align: left;
  font-weight: 400;
}

/* 空数据占位行保持居中，避免与其它列表观感不一致 */
:deep(.order-fail-table .ant-table-placeholder .ant-table-cell) {
  text-align: center;
}

/* 工单 ID 与操作列统一使用系统主题色变量 */
:deep(.order-fail-table .order-id-text) {
  color: var(--gi-color-primary, #0f766e);
  font-weight: 500;
}

:deep(.order-fail-table .order-op-link) {
  color: var(--gi-color-primary, #0f766e) !important;
}

:deep(.order-fail-table .order-op-link:hover) {
  color: var(--gi-color-primary-hover, #0d6b63) !important;
}

/* 实例连接异常 tab：状态去掉标签药丸底，仅保留语义色文字 */
:deep(.instance-conn-table .conn-status-text) {
  color: var(--gi-color-error, #d4380d);
  font-size: 13px;
}

/* 实例连接异常 tab：操作列使用系统主题色 */
:deep(.instance-conn-table .conn-op-link) {
  color: var(--gi-color-primary, #0f766e) !important;
}

:deep(.instance-conn-table .conn-op-link:hover) {
  color: var(--gi-color-primary-hover, #0d6b63) !important;
}

/* 实例无有效备份保护 tab：操作列（创建备份任务 / 关闭）使用系统主题色 */
:deep(.instance-nobackup-table .instance-op-link) {
  color: var(--gi-color-primary, #0f766e) !important;
}

:deep(.instance-nobackup-table .instance-op-link:hover) {
  color: var(--gi-color-primary-hover, #0d6b63) !important;
}

/* 备份任务 ID：统一使用系统主题色变量，hover 后加深 */
:deep(.task-id-link) {
  color: var(--gi-color-primary, #0f766e);
}

:deep(.task-id-link:hover) {
  color: var(--gi-color-primary-hover, #0d6b63);
}

:deep(.cell-copy-icon:hover) {
  color: var(--gi-color-primary, #0f766e) !important;
}

/* 周期备份当天未成功 tab：表体文字统一使用系统主题色 */
:deep(.daily-backup-miss-table .ant-table-tbody > tr > td) {
  color: var(--gi-color-primary, #0f766e);
}

:deep(.daily-backup-miss-table .ant-table-tbody > tr:hover > td) {
  color: var(--gi-color-primary, #0f766e);
}

:deep(.daily-backup-miss-table .ant-table-tbody > tr.ant-table-row-selected > td),
:deep(.daily-backup-miss-table .ant-table-tbody > tr.ant-table-row-selected:hover > td) {
  color: var(--gi-color-primary, #0f766e);
}

/* 覆盖单元格内部硬编码颜色，保证主题色一致 */
:deep(.daily-backup-miss-table .ant-table-tbody > tr > td .cell-primary),
:deep(.daily-backup-miss-table .ant-table-tbody > tr > td .ip-line),
:deep(.daily-backup-miss-table .ant-table-tbody > tr > td .task-id-link),
:deep(.daily-backup-miss-table .ant-table-tbody > tr > td .cell-secondary),
:deep(.daily-backup-miss-table .ant-table-tbody > tr > td .remark-line) {
  color: var(--gi-color-primary, #0f766e) !important;
}

:deep(.daily-backup-miss-table .ant-table-tbody > tr:hover > td .cell-primary),
:deep(.daily-backup-miss-table .ant-table-tbody > tr:hover > td .ip-line),
:deep(.daily-backup-miss-table .ant-table-tbody > tr:hover > td .task-id-link),
:deep(.daily-backup-miss-table .ant-table-tbody > tr:hover > td .cell-secondary),
:deep(.daily-backup-miss-table .ant-table-tbody > tr:hover > td .remark-line) {
  color: var(--gi-color-primary, #0f766e) !important;
}

:deep(.daily-backup-miss-table .ant-table-tbody > tr.ant-table-row-selected > td .cell-primary),
:deep(.daily-backup-miss-table .ant-table-tbody > tr.ant-table-row-selected:hover > td .cell-primary) {
  color: var(--gi-color-primary, #0f766e) !important;
}

/* 链接 hover 仍加深，保持可交互反馈 */
:deep(.daily-backup-miss-table .ant-table-tbody > tr > td .task-id-link:hover) {
  color: var(--gi-color-primary-hover, #0d6b63) !important;
}

@media (max-width: 768px) {
  /* 几张表只有 y 没有 x，窄屏列被压缩 */
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

  .risk-detail-page {
    height: auto;
    min-height: 100%;
    overflow: visible;
  }
}
</style>
