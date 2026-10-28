<template>
  <!-- 告警列表下钻弹窗（告警中心计数卡/级别图例 与 监控大屏「新增告警」共用）。
       数据来自 /monitor/alerts，查询参数由父页面通过 params 传入（分页参数组件内部接管）；
       行内「详情」「实例名称」通过 view-detail / view-instance 事件交给父页面决定跳转方式。 -->
  <a-modal
    :open="open"
    wrap-class-name="alert-drill-modal"
    centered
    :title="modalTitle"
    width="1020px"
    :mask-closable="false"
    :footer="null"
    @cancel="emit('update:open', false)"
  >
    <!-- 主列表：mergeStatus（合并模式）下所有状态一张表 + 前端筛选/排序/分页；
         非合并模式行为不变（服务端分页，单口径列表）。
         分页在下方 .drill-footer 独立行，与批量确认按钮同行对齐 -->
    <a-table
      :columns="tableColumns"
      :data-source="mergedMode ? pagedRows : state.items"
      row-key="row_key"
      :loading="state.loading"
      :pagination="false"
      :row-selection="rowSelection"
      size="middle"
      table-layout="fixed"
      :scroll="{ x: 1300 }"
      @change="onTableChangeClient"
    >
      <template #emptyText>
        <a-empty
          v-if="mergedMode"
          :description="emptyText"
          class="drill-empty"
        />
      </template>
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'alert_id'">
          <span class="drill-alert-id">{{ record.alert_id || '—' }}</span>
          <a-tooltip title="复制告警ID">
            <a-button
              class="drill-copy-btn"
              type="text"
              size="small"
              @click="copyAlertId(record.alert_id)"
            >
              <template #icon><CopyOutlined /></template>
            </a-button>
          </a-tooltip>
        </template>
        <template v-else-if="column.key === 'severity'">
          <span>{{ alertLevelMeta(record.severity).label }}</span>
        </template>
        <template v-else-if="column.key === 'instance'">
          <!-- 两行：数据源名称（默认文本色）+ ip:port（小一号、淡灰）；
               名称 20 字符 / ip:port 20 字符截断（产品统一口径），完整值见 title 悬浮提示 -->
          <a class="drill-instance" :title="instanceTitleOf(record)" @click="emit('view-instance', record)">
            <span class="drill-inst-name">{{ clipText(instanceNameOf(record), 20) }}</span>
            <span class="drill-inst-ip">{{ clipText(instanceIpOf(record), 20) }}</span>
          </a>
        </template>
        <template v-else-if="column.key === 'name'">
          <span class="drill-name">{{ cleanSeverityInName(operatorTextInText(record.name)) }}</span>
        </template>
        <template v-else-if="column.key === 'status'">
          <span class="drill-status" :class="`drill-status-${alertStatusColor(record.status)}`">{{ record.status_text || record.status }}</span>
        </template>
        <template v-else-if="column.key === 'triggered_at'">
          <!-- 单行完整展示：nowrap + 不省略（见 .drill-time） -->
          <span class="drill-time">{{ record.triggered_at || '—' }}</span>
        </template>
        <template v-else-if="column.key === 'actions'">
          <a-button class="drill-detail-btn" type="link" size="small" @click="emit('view-detail', record)">
            详情
          </a-button>
        </template>
        <template v-else-if="column.key === 'acked_by'">
          <!-- 处理人：取认领动作写入的 acked_by；未认领/未获取显示 - -->
          <span>{{ record.acked_by || '-' }}</span>
        </template>
      </template>
    </a-table>

    <!-- 底部行：批量确认（左）与主列表分页（右）同行两端对齐。
         分页语义与原表格内置分页一致：分组模式前端切片翻页（换页清勾选），
         非分组模式走服务端分页（onTableChange 内 load()） -->
    <div class="drill-footer">
      <a-tooltip v-if="batchable" title="批量认领所选告警">
        <a-button
          type="primary"
          :disabled="!state.selected.length"
          :loading="state.confirming"
          @click="onBatchAck"
        >
          批量确认
        </a-button>
      </a-tooltip>
      <span v-else aria-hidden="true"></span>
      <a-pagination
        :current="state.page"
        :page-size="state.pageSize"
        :total="mergedMode ? sortedRows.length : state.total"
        :show-size-changer="false"
        :show-total="(t) => `共 ${Number(t).toLocaleString()} 条`"
        @change="onPageChange"
      />
    </div>

</a-modal>
</template>

<script setup>
import { computed, reactive, watch } from 'vue'
import { CopyOutlined } from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { getMonitorAlertsApi, updateMonitorAlertActionApi } from '@/api/monitor'
import { alertLevelMeta, alertStatusColor, cleanSeverityInName, clipText, operatorTextInText } from '../utils'
import { ALERT_DRILL_COLUMNS } from './alertDrillColumns'

const props = defineProps({
  open: { type: Boolean, default: false },
  // 标题主体（如「未恢复告警列表」「新增告警列表」），组件自动拼接「（共 N 条）」
  title: { type: String, default: '告警列表' },
  // 标题前缀（如实例名 / 级别 P1 / "其他实例"），超出 15 字符自动截断加"..."
  titlePrefix: { type: String, default: '' },
  // /monitor/alerts 查询参数（不含分页）；父页面保证与卡片数字同一口径
  params: { type: Object, default: () => ({}) },
  // 是否出现勾选框与批量确认（语义上可认领的列表才传 true）
  selectable: { type: Boolean, default: false },
  canManage: { type: Boolean, default: false },
  // 已恢复行不可勾选（认领只对未恢复告警有意义，如大屏「新增告警」含已恢复）
  disableRecovered: { type: Boolean, default: false },
  // 合并模式：所有状态一张表展示（不做「已恢复」折叠），状态/级别列加下拉筛选、
  // 触发时间列加排序（默认降序），数据一次拉取后由前端完成筛选/排序/分页。
  // 仅柱状图下钻等「时间窗口内天然含已恢复」的场景开启；快照类弹窗保持服务端口径。
  mergeStatus: { type: Boolean, default: false },
})

const emit = defineEmits(['update:open', 'view-detail', 'view-instance', 'acknowledged'])

const state = reactive({
  loading: false,
  items: [],
  total: 0,
  page: 1,
  pageSize: 10,
  selected: [],
  confirming: false,
  // 合并模式的列筛选/排序状态（受控传给 a-table，实际过滤排序在 computed 管道里做）
  severityFilters: null,
  statusFilters: null,
  timeOrder: 'descend',
})

// 列宽与 ALERT_DRILL_COLUMNS 一致：固定列合计 1000（instance 已加宽到 324 以完整显示 20 个字符），
// 超出弹窗 body 可用宽度 ~972，因此表格 :scroll x 设为 1300 并出现横向滚动条。
const columns = ALERT_DRILL_COLUMNS

const TITLE_PREFIX_MAX = 15

// 标题前缀截断：超过 15 个字符保留前 15 个并加"..."，保证标题整体结构不被破坏
const titlePrefixText = computed(() => {
  const p = props.titlePrefix?.trim()
  if (!p) return ''
  if (p.length <= TITLE_PREFIX_MAX) return p
  return p.slice(0, TITLE_PREFIX_MAX) + '...'
})

const modalTitle = computed(() => {
  const prefix = titlePrefixText.value
  return `${prefix}${props.title}（共 ${Number(state.total || 0).toLocaleString()} 条）`
})

// 勾选/批量确认仅对有 monitor.manage 权限的用户开放；
// disableRecovered 时已恢复行禁用勾选（认领无意义）。
const batchable = computed(() => props.selectable && props.canManage)
const rowSelection = computed(() => {
  if (!batchable.value) return undefined
  return {
    selectedRowKeys: state.selected,
    onChange: (keys) => (state.selected = keys),
    getCheckboxProps: (record) => ({
      disabled: props.disableRecovered && record.status === 'recovered',
    }),
  }
})

// 合并模式（props.mergeStatus）：所有状态一张表，前端完成筛选 → 排序 → 分页。
// 非合并模式：保持服务端分页的单口径列表（params 里已带状态/口径过滤）。
const mergedMode = computed(() => Boolean(props.mergeStatus))

// 列定义：合并模式下给 状态/告警级别 挂下拉筛选、给 触发时间 挂排序。
// filteredValue / sortOrder 受控；不给 onFilter、sorter 恒 0 ——
// 过滤与排序统一在下方 computed 管道完成，避免 antd 内部再对当前页数据二次处理。
// 状态筛选项：不提供「观察中（pending）」——观察期告警尚未正式生效通知，
// 单列出来会让用户在弹窗里误以为存在一类需要单独跟进的状态。
const STATUS_FILTER_OPTIONS = [
  { text: '待认领', value: 'active' },
  { text: '处理中', value: 'acknowledged' },
  { text: '已恢复', value: 'recovered' },
]
const SEVERITY_FILTER_OPTIONS = [
  { text: 'P0', value: 'P0' },
  { text: 'P1', value: 'P1' },
  { text: 'P2', value: 'P2' },
  { text: 'P3', value: 'P3' },
]
const tableColumns = computed(() => {
  if (!mergedMode.value) return columns
  return columns.map((col) => {
    if (col.key === 'severity') {
      return { ...col, filters: SEVERITY_FILTER_OPTIONS, filteredValue: state.severityFilters }
    }
    if (col.key === 'status') {
      return { ...col, filters: STATUS_FILTER_OPTIONS, filteredValue: state.statusFilters }
    }
    if (col.key === 'triggered_at') {
      return {
        ...col,
        sorter: () => 0, // 实际排序在 computed 管道里做，这里只提供表头排序交互
        sortOrder: state.timeOrder,
        sortDirections: ['descend', 'ascend'],
        showSorterTooltip: { title: '按触发时间排序' },
      }
    }
    return col
  })
})

// 筛选 → 排序 → 分页 的展示管道（仅合并模式消费）
const filteredRows = computed(() => {
  const rows = state.items
  const sev = state.severityFilters
  const st = state.statusFilters
  if ((!sev || !sev.length) && (!st || !st.length)) return rows
  return rows.filter((r) => {
    if (sev && sev.length && !sev.includes(r.severity)) return false
    if (st && st.length && !st.includes(String(r.status || '').toLowerCase())) return false
    return true
  })
})

// 触发时间排序：'YYYY-MM-DD HH:mm:ss' 字符串字典序即时间序，无需解析。
// 默认降序（后发生的在前）；取消排序（timeOrder 为空）时保持接口原始顺序。
const sortedRows = computed(() => {
  if (!mergedMode.value) return filteredRows.value
  const order = state.timeOrder
  if (order !== 'ascend' && order !== 'descend') return filteredRows.value
  const time = (r) => String(r.triggered_at || '')
  const sorted = [...filteredRows.value].sort((a, b) => time(a).localeCompare(time(b)))
  return order === 'descend' ? sorted.reverse() : sorted
})

const pagedRows = computed(() => {
  const start = (state.page - 1) * state.pageSize
  return sortedRows.value.slice(start, start + state.pageSize)
})

// 合并模式的空态文案：区分"窗口内本来就没有"与"筛选后无匹配"
const emptyText = computed(() => {
  if (!state.items.length) return '该时间窗口内暂无告警'
  if (!filteredRows.value.length) return '没有符合筛选条件的告警'
  return ''
})

const isCanceled = (error) => Boolean(error && (error.canceled || error.code === 'ERR_CANCELED'))

const load = async () => {
  state.loading = true
  try {
    // 合并模式下一次性拉取时间窗口内全部状态的数据（后端 max page_size = 200），
    // 由前端完成筛选 / 排序 / 分页；非合并模式走服务端分页。
    const pageSize = mergedMode.value ? 200 : state.pageSize
    const page = mergedMode.value ? 1 : state.page
    const res = await getMonitorAlertsApi({ ...props.params, page, page_size: pageSize })
    state.items = res?.data?.items || []
    state.total = Number(res?.total || 0)
    if (mergedMode.value) {
      state.page = 1
    }
  } catch (error) {
    if (!isCanceled(error)) message.error(error?.message || '告警明细加载失败')
  } finally {
    state.loading = false
  }
}

const onTableChange = (pag) => {
  state.page = pag.current || 1
  state.selected = []
  load()
}

// a-table @change：合并模式下承接表头筛选（状态/级别）与排序（触发时间）。
// 筛选或排序变化后回到第 1 页并清空勾选，避免"勾选了被过滤掉的行"参与批量确认。
const onTableChangeClient = (_pag, filters, sorter) => {
  if (!mergedMode.value) return
  state.severityFilters = filters?.severity || null
  state.statusFilters = filters?.status || null
  state.timeOrder = sorter?.order || ''
  state.page = 1
  state.selected = []
}

// 翻页入口在 .drill-footer 的 a-pagination 上：
//   合并模式 —— sortedRows 的前端切片，换页只改页码并清空勾选；
//   非合并模式 —— 复用 onTableChange 走服务端分页（内部 load()）。
const onPageChange = (page) => {
  if (!mergedMode.value) {
    onTableChange({ current: page })
    return
  }
  state.page = page
  state.selected = []
}

// 实例列两行展示用的取值：
//   name —— 数据源名称（remark），缺失时退回 instance（ip:port），再兜底 —
//   ip   —— 主机 IP（hostname），缺失时退回 instance
const instanceNameOf = (record) => record?.remark || record?.instance || '—'
const instanceIpOf = (record) => {
  const host = record?.hostname || record?.instance || ''
  if (!host) return '—'
  const port = record?.port
  return port ? `${host}:${port}` : host
}
const instanceTitleOf = (record) => `${instanceNameOf(record)} ${instanceIpOf(record)}`

const copyAlertId = async (alertId) => {
  if (!alertId) return
  try {
    await navigator.clipboard.writeText(alertId)
    message.success('已复制告警ID')
  } catch {
    // 降级：fallback 到临时 textarea
    const ta = document.createElement('textarea')
    ta.value = alertId
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    document.body.removeChild(ta)
    message.success('已复制告警ID')
  }
}

const onBatchAck = async () => {
  const rows = state.items.filter((r) => state.selected.includes(r.row_key))
  if (!rows.length) return
  state.confirming = true
  let ok = 0
  let firstFail = ''
  for (const row of rows) {
    try {
      await updateMonitorAlertActionApi({ fingerprint: row.fingerprint, action: 'ack' })
      ok++
    } catch (error) {
      if (!firstFail) firstFail = error?.message || row.alert_id
    }
  }
  state.confirming = false
  state.selected = []
  if (ok) message.success(`已认领 ${ok} 条告警`)
  if (firstFail) message.error(`部分认领失败：${firstFail}`)
  // 弹窗内列表刷新到最新状态；数字是否变化由父页面决定（emit 出去）
  load()
  emit('acknowledged')
}

// 打开时或查询参数变化时拉数据。
// 父页面先改 params 再打开弹窗，但 Vue 的 props 同步到子组件是 nextTick 级别的，
// 只靠 watch open 可能在 load 时读到旧 params，因此把 params 也纳入监听。
watch(
  () => [props.open, props.params],
  () => {
    if (props.open) {
      state.page = 1
      state.selected = []
      // 合并模式的筛选/排序随每次打开重置：列上受控值回到「未筛选 + 默认降序」
      state.severityFilters = null
      state.statusFilters = null
      state.timeOrder = 'descend'
      load()
    }
  },
  { deep: true },
)
</script>

<style>
/* 下钻弹窗（a-modal）teleport 到 body，scoped 样式不生效；
   用 wrap class 前缀 .alert-drill-modal 限定作用域，避免污染其他页面。 */
.alert-drill-modal .drill-alert-id {
  font-family: var(--font-mono, monospace);
  font-size: 12px;
  color: var(--gi-color-text-secondary, rgba(0, 0, 0, 0.65));
}

.alert-drill-modal .drill-level {
  font-weight: 600;
}

/* 告警ID 后的轻量复制按钮（与「告警历史」表里的 .copy-id-btn 同规格，
   因弹窗 teleport 到 body，必须在本非 scoped 块内用 .alert-drill-modal 前缀） */
.alert-drill-modal .drill-copy-btn {
  width: 18px;
  height: 18px;
  margin-left: 4px;
  padding: 0;
  line-height: 1;
  color: var(--gi-color-text-tertiary, #8a94a6);
}
.alert-drill-modal .drill-copy-btn:hover {
  color: var(--gi-color-primary, #0f766e);
}
.alert-drill-modal .drill-copy-btn .anticon {
  font-size: 12px;
}

/* 实例列：两行展示。
   第一行 数据源名称 —— 不再沿用原字体颜色（元素是 <a>，不显式覆盖会继承全局链接蓝
   rgb(64,158,255)），显式取默认文本色；
   第二行 ip:port —— 小一号（12px）+ 淡灰。 */
.alert-drill-modal .drill-instance {
  display: block;
  cursor: pointer;
  color: var(--gi-color-text-primary, rgba(15, 23, 32, 0.9));
}
.alert-drill-modal .drill-instance:hover .drill-inst-name {
  text-decoration: underline;
  text-underline-offset: 3px;
}
.alert-drill-modal .drill-inst-name {
  display: block;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.alert-drill-modal .drill-inst-ip {
  display: block;
  margin-top: 2px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--gi-color-text-tertiary, #8a94a6);
  /* ip:port 单行省略：列宽 324px 足够显示 20 个字符，这里只作极端值的兜底 */
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

/* 触发时间：单行完整展示——nowrap 防换行，且不设 ellipsis/hidden（不截断、不隐藏）；
   配合列宽 182px 容纳「YYYY-MM-DD HH:mm:ss」19 字符 */
.alert-drill-modal .drill-time {
  white-space: nowrap;
  word-break: keep-all;
}

/* 操作列「详情」用主题色 */
.alert-drill-modal .drill-detail-btn {
  padding: 0;
  color: var(--gi-color-primary, #0f766e);
}
.alert-drill-modal .drill-detail-btn:hover {
  color: var(--gi-color-primary-hover, #0d6b63);
}

/* 告警名称：移除等级标识后保持可读；允许在空格处自然折行，超长连续字符仍可 break */
.alert-drill-modal .drill-name {
  white-space: normal;
  word-break: break-word;
  line-height: 1.4;
}

/* 告警名称表头不折叠：始终单行，不换行也不省略号 */
.alert-drill-modal .col-alert-name {
  white-space: nowrap;
  overflow: visible;
  text-overflow: clip;
}

/* 状态列：去掉 a-tag 圆圈，只保留字体颜色。
   颜色与右上角告警圆饼扇区同一真源（chartTokens → --chart-alert-*，
   applyChartTokens 启动时注入；var() 兜底写死同色十六进制防注入失败丢色）：
   active(待认领)=pending 深红 / acknowledged(处理中)=doing 琥珀 / recovered(已恢复)=resolved 绿；
   未知状态继承默认文本色。 */
.alert-drill-modal .drill-status {
  display: inline-block;
  font-size: 14px;
  line-height: 22px;
  vertical-align: middle;
}

.alert-drill-modal .drill-status-pending {
  color: var(--chart-alert-pending, #b3261e);
}

.alert-drill-modal .drill-status-doing {
  color: var(--chart-alert-doing, #ef9f27);
}

.alert-drill-modal .drill-status-resolved {
  color: var(--chart-alert-resolved, #0f9f6f);
}

.alert-drill-modal .drill-status-default {
  color: var(--gi-color-text-secondary, rgba(0, 0, 0, 0.65));
}

/* —— 滚动策略：整窗不滚，滚动只发生在表格正文（tbody）——
   modal 限高后沿 flex 链把剩余高度传给 .ant-table-content（唯一滚动容器）；
   分页在 .ant-table 内、滚动容器之外，天然常驻不被滚走；
   表头用 sticky 吸顶。页脚为空时 :footer=null 整条隐藏，不留白。 */
/* centered 垂直居中默认靠 inline-block + ::before 占位技巧实现，
   modal 被我们改成 flex 后该技巧失效；改为 wrap 真弹性居中，矮视口也不出滚动条 */
.alert-drill-modal.ant-modal-wrap {
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  padding: 16px;
}
.alert-drill-modal.ant-modal-wrap::before {
  display: none;
}

/* 双类名 .ant-modal.ant-modal 提高优先级：antd css-in-js 注入的同权重样式
   会按文档顺序覆盖我们的 display，导致 flex 布局失效 */
.alert-drill-modal .ant-modal.ant-modal {
  display: flex;
  flex-direction: column;
  max-height: 85vh;
}

/* antd 在 .ant-modal 与 header/body/footer 之间还有两层：空 class 的 tabindex
   焦点包裹层 + .ant-modal-content，flex 高度链必须穿过这两层，body 才拿得到受限高度 */
.alert-drill-modal .ant-modal > div,
.alert-drill-modal .ant-modal-content {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.alert-drill-modal .ant-modal-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  position: relative;
}

/* 底部行：批量确认（左）与分页（右）同行两端对齐；flex 链内自动排在
   表格与已恢复折叠区之间，不再用绝对定位，也就不存在遮挡问题 */
.alert-drill-modal .drill-footer {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 0 4px;
}

/* a-table 到滚动容器的 flex 高度链（缺一环表体就拿到不了受限高度） */
.alert-drill-modal .ant-table-wrapper {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.alert-drill-modal .ant-spin-nested-loading,
.alert-drill-modal .ant-spin-container,
.alert-drill-modal .ant-table,
.alert-drill-modal .ant-table-container {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.alert-drill-modal .ant-table-content {
  flex: 1 1 auto;
  min-height: 0;
  /* 关键：必须 !important。antd 在只用 scroll.x（无 scroll.y）时会给 .ant-table-content
     打内联样式 overflow-x:auto / overflow-y:hidden（它默认认为表体不需要纵向滚动）。
     内联优先级高于普通 CSS，不压过它 → 内容超出高度时**既不出纵向滚动条、又把行裁掉**
     （实测：内容 547px 挤在 407px 里，第 8 条直接看不见）。 */
  overflow: auto !important;
  /* macOS 默认 overlay 滚动条且「仅滚动时显示」，用户看不出还能滚。
     自定义 ::-webkit-scrollbar（-webkit-appearance:none）会让浏览器退回常显的经典滚动条。
     注意：**不能同时写 scrollbar-width**（Chrome 121+ 下标准属性优先，会把 ::-webkit-scrollbar
     的宽度设置整个吃掉，退回 overlay、实测占位 0）—— 只保留 WebKit 伪元素这一条路。 */
  scrollbar-gutter: stable;
}

.alert-drill-modal .ant-table-content::-webkit-scrollbar {
  -webkit-appearance: none;
  width: 8px;
  height: 8px;
}
.alert-drill-modal .ant-table-content::-webkit-scrollbar-track {
  background: transparent;
}
.alert-drill-modal .ant-table-content::-webkit-scrollbar-thumb {
  background: rgba(0, 0, 0, 0.25);
  border-radius: 4px;
}
.alert-drill-modal .ant-table-content::-webkit-scrollbar-thumb:hover {
  background: rgba(0, 0, 0, 0.4);
}

/* 关键：表格本体会被上层 flex 容器压缩（flex-shrink 默认为 1），
   表格 box 被压到容器高度后，溢出的行只能被裁掉（表现为"少显示几条"），
   而容器自身检测不到溢出 → 不出滚动条。关掉收缩让它保持自然高度，
   .ant-table-content 的 overflow:auto 才会真正生效 */
.alert-drill-modal .ant-table-content > table {
  flex: 0 0 auto;
}

/* 表头吸顶：sticky 挂在 thead 上（Chrome 里 th 的 sticky 约束在父行内、无法位移，
   thead 才能相对滚动容器吸附）；背景色防透出行内容 */
.alert-drill-modal .ant-table-content thead {
  position: sticky;
  top: 0;
  z-index: 2;
  background: #f8fbfc;
}

/* 主列表空状态：提示文字居中 */
.alert-drill-modal .drill-empty {
  padding: 24px 0;
}
</style>
