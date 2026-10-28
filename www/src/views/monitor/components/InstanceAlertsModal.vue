<template>
  <!-- 实例告警明细弹窗：监控明细页三处入口共用（页首告警数量、页首健康分、展开态健康分）。
       口径严格一致：unresolved=true（未恢复且未忽略）+ instance_id 过滤，
       默认排序（状态优先 → 严重度 → 最近命中）。三态：loading / empty / error+重试。
       「分数」列取健康判定（健康分扣分规则）的扣分表，与后端算健康分同源。
       wrapClassName 挂 token 作用域，使弹窗（teleport 到 body）内同样取到 --gi-* 主题变量。 -->
  <a-modal
    v-model:open="innerOpen"
    :footer="null"
    width="760px"
    destroy-on-close
    wrap-class-name="monitor-detail-modal-wrap"
  >
    <!-- 标题：实例名超过 15 个字符即截断为「…」，悬停给出完整名称。
         改用 #title 插槽而非字符串 :title —— 字符串形式无法在标题内挂气泡。 -->
    <template #title>
      <span class="inst-alerts-title">
        <span class="inst-alerts-title-label">实例告警明细 · </span>
        <a-tooltip v-if="instanceLabelTruncated" :title="instanceLabelRaw" placement="topLeft">
          <span class="inst-alerts-title-name">{{ instanceLabelText }}</span>
        </a-tooltip>
        <span v-else class="inst-alerts-title-name">{{ instanceLabelText }}</span>
      </span>
    </template>
    <div class="inst-alerts">
      <!-- 触底说明条：健康分被截断到 0 时，把"为什么是 0 分"讲清楚 ——
           0 分可能是"刚好扣满"，也可能是"远超满分"，两者严重程度完全不同 -->
      <div v-if="scoreCapped" class="inst-alerts-floor">
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
          <circle cx="8" cy="8" r="7" stroke="currentColor" stroke-width="1.3" />
          <path d="M8 4.5v4.2" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
          <circle cx="8" cy="11.2" r="0.85" fill="currentColor" />
        </svg>
        <span>
          实际扣分合计 {{ scoreDeductTotal }} 分，已超出满分 100 分（超扣 {{ scoreOverDeduct }} 分），
          健康分按最低分 0 展示。下方列表为该实例全部未恢复告警。
        </span>
      </div>
      <a-alert v-if="state.error" type="error" show-icon class="inst-alerts-error">
        <template #message>
          <span>告警明细加载失败，请稍后重试。</span>
          <a-button type="link" size="small" @click="load">重试</a-button>
        </template>
      </a-alert>
      <a-table
        :columns="columns"
        :data-source="state.items"
        row-key="alert_id"
        :loading="state.loading"
        :pagination="pagination"
        size="small"
        table-layout="fixed"
        :locale="{ emptyText: state.error ? '加载失败，请重试' : '当前实例暂无未恢复告警' }"
        @change="onTableChange"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'name'">
            <!-- 内容列：去等级标识（P0~P3）+ 比较符本地化，其余文案原样保留；
                 完整展示不截断，超宽由 .col-alert-content 换行、行高自适应 -->
            <span class="inst-alerts-name" :title="cleanSeverityInName(operatorTextInText(record.name))">
              {{ cleanSeverityInName(operatorTextInText(record.name)) || '—' }}
            </span>
          </template>
          <template v-else-if="column.key === 'severity'">
            <span class="inst-alerts-plain">{{ alertLevelMeta(record.severity).label }}</span>
          </template>
          <template v-else-if="column.key === 'deduction'">
            <!-- 分数列：扣分值直接取健康判定规则表，无对应规则显示「—」 -->
            <span
              class="inst-alerts-plain"
              :title="deductionTitle(record)"
            >{{ deductionText(deductions, record.severity) }}</span>
          </template>
          <template v-else-if="column.key === 'triggered_at'">
            {{ record.triggered_at || '—' }}
          </template>
          <template v-else-if="column.key === 'duration'">
            {{ durationText(record.duration_sec) }}
          </template>
          <template v-else-if="column.key === 'status'">
            <span class="inst-alerts-plain">{{ record.status_text || record.status }}</span>
          </template>
        </template>
      </a-table>
    </div>
  </a-modal>
</template>

<script setup>
// 实例告警明细弹窗（共用组件）：结构与取数只此一份，
// 监控明细页的三个点击入口（告警数量 / 页首健康分 / 展开态健康分）都复用它，
// 保证列、数据、过滤、排序完全一致，不会各自演化出一套实现。
import { computed, reactive, ref, watch } from 'vue'
import { getMonitorAlertsApi } from '@/api/monitor'
import { alertLevelMeta, cleanSeverityInName, operatorTextInText } from '../utils'
import { deductionOf, deductionText, loadHealthDeductions, normalizeSeverity } from '../healthDeductions'

defineOptions({ name: 'MonitorInstanceAlertsModal' })

const props = defineProps({
  open: { type: Boolean, default: false },
  instanceId: { type: String, default: '' },
  instanceName: { type: String, default: '' },
  // 健康分触底三件套：由明细页把后端下发的口径透传进来，
  // 弹窗不做二次计算 —— 一个页面里同一件事只算一次，避免两处口径漂移。
  scoreCapped: { type: Boolean, default: false },
  scoreOverDeduct: { type: Number, default: 0 },
  scoreDeductTotal: { type: Number, default: 0 },
})
const emit = defineEmits(['update:open'])

// v-model:open 的读写代理：关闭由弹窗内部触发时同步回父级
const innerOpen = computed({
  get: () => props.open,
  set: (value) => emit('update:open', value),
})

// 标题里的实例名：超过 15 个字符即截断为「前 15 字 + ...」，完整名称由悬停气泡给出，
// 标题栏恒定单行、不被长实例名撑成两行。
// 按码点切分（Array.from）而非 substring —— 后者会把 emoji / 代理对切成半个字符。
const INSTANCE_LABEL_MAX = 15
const instanceLabelRaw = computed(() => props.instanceName || props.instanceId || '')
const instanceLabelChars = computed(() => Array.from(instanceLabelRaw.value))
const instanceLabelTruncated = computed(() => instanceLabelChars.value.length > INSTANCE_LABEL_MAX)
const instanceLabelText = computed(() => (
  instanceLabelTruncated.value
    ? `${instanceLabelChars.value.slice(0, INSTANCE_LABEL_MAX).join('')}...`
    : instanceLabelRaw.value
))

// 列宽按「不换行」倒推：级别 76 / 分数 76 / 触发时间 170 / 持续时长 110 / 状态 92，
// 剩余宽度给告警内容；表头经 customHeaderCell 挂 .col-alert-name 保证不折叠。
// 告警内容列刻意不加 ellipsis —— antd 的 ellipsis 会同时引入 nowrap + 省略号，
// 且它是 tableLayout 判定的入参之一；这里改为「表格显式 table-layout: fixed」
// （见模板上的 table-layout 属性）+ 单元格 CSS 换行，既保证内容完整展示、
// 行高自适应，又让其余列的宽度分配与原先完全一致。
const columns = [
  {
    title: '告警内容',
    key: 'name',
    customHeaderCell: () => ({ class: 'col-alert-name' }),
    customCell: () => ({ class: 'col-alert-content' }),
  },
  { title: '级别', key: 'severity', width: 76, align: 'center' },
  { title: '分数', key: 'deduction', width: 76, align: 'center' },
  { title: '触发时间', key: 'triggered_at', width: 170 },
  { title: '持续时长', key: 'duration', width: 110 },
  { title: '状态', key: 'status', width: 92, align: 'center' },
]

const state = reactive({
  loading: false,
  error: false,
  items: [],
  total: 0,
  page: 1,
  pageSize: 10,
})

// 健康判定扣分表：与「告警配置 → 健康判定」同一接口、同一份数据
const deductions = ref(null)

const pagination = computed(() => ({
  current: state.page,
  pageSize: state.pageSize,
  total: state.total,
  size: 'small',
  showTotal: (t) => `共 ${t} 条`,
}))

// 持续时长文案：与告警中心 durationText 同一格式
const durationText = (seconds) => {
  const value = Number(seconds || 0)
  if (value <= 0) return '—'
  const days = Math.floor(value / 86400)
  const hours = Math.floor((value % 86400) / 3600)
  const minutes = Math.floor((value % 3600) / 60)
  if (days > 0) return `${days} 天 ${hours} 小时`
  if (hours > 0) return `${hours} 小时 ${minutes} 分`
  return `${minutes} 分`
}

const deductionTitle = (record) => {
  const value = deductionOf(deductions.value, record.severity)
  if (value == null) return '健康判定未配置该级别的扣分'
  return `${normalizeSeverity(record.severity)}：每条未恢复告警扣 ${value} 分（取自健康判定）`
}

async function load() {
  if (!props.instanceId) return
  state.loading = true
  state.error = false
  try {
    const [alertRes, table] = await Promise.all([
      getMonitorAlertsApi({
        instance_id: props.instanceId,
        unresolved: true,
        page: state.page,
        page_size: state.pageSize,
      }),
      // 扣分表与告警列表并行取；取不到时 table 为 null，分数列降级显示「—」
      loadHealthDeductions(),
    ])
    state.items = alertRes?.data?.items || []
    state.total = Number(alertRes?.total || 0)
    deductions.value = table || null
  } catch (error) {
    // 请求层路由切换 abort 抛 { canceled: true }，属正常翻页竞态，不当错误处理
    if (error && (error.canceled || error.code === 'ERR_CANCELED')) return
    state.error = true
    state.items = []
    state.total = 0
  } finally {
    state.loading = false
  }
}

function onTableChange(pag) {
  state.page = pag.current || 1
  load()
}

// 每次打开都从第 1 页重新取数：弹窗是 destroy-on-close，
// 不复位的话二次打开会停在上一页，且数据可能已经变化。
watch(
  () => props.open,
  (open) => {
    if (!open) return
    state.page = 1
    load()
  },
)
</script>

<style>
/* 弹窗被 teleport 到 body，样式不能用 scoped（否则取不到 antd 内部 DOM），
   统一挂在 .inst-alerts 前缀下，作用域可控。 */
/* 标题行：实例名截断条不在 .inst-alerts 之内（它在弹窗 header），
   故用 wrap-class-name 对应的 .monitor-detail-modal-wrap 限定作用域。
   截断本身由 JS 按 15 字符完成，这里只保证「恒定单行、不溢出」——
   名称含空格或超长中文时标题栏不会被折成两行、也不会撑破 header。 */
.monitor-detail-modal-wrap .inst-alerts-title {
  display: inline-block;
  max-width: 100%;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: bottom;
}
/* 正文：全部单元格统一为普通正文（无加粗、无彩色），
   表头保持 antd 原有强调样式，正文与表头形成明显层级区分。
   这里同时覆盖 hover 态，避免行悬浮时文字颜色被 antd 默认规则改写。 */
.inst-alerts .ant-table-tbody > tr > td,
.inst-alerts .ant-table-tbody > tr:hover > td,
.inst-alerts .ant-table-tbody > tr.ant-table-row-hover > td {
  font-weight: 400;
  color: var(--gi-text-1, #163440);
}
.inst-alerts .inst-alerts-name,
.inst-alerts .inst-alerts-plain {
  font-weight: 400;
  color: inherit;
}
/* 告警内容表头不折叠：始终单行，不换行也不省略号 */
.inst-alerts .col-alert-name {
  white-space: nowrap;
  overflow: visible;
  text-overflow: clip;
}
/* 告警内容单元格：完整展示，禁止截断与省略号。
   table-layout 为 fixed，本列吃剩余宽度；内容超宽时在词内/中英文间自动换行，
   行高随内容自适应。同时清掉 antd 可能残留的 ellipsis 类样式（双保险）。 */
.inst-alerts .col-alert-content {
  white-space: normal;
  overflow: visible;
  text-overflow: clip;
  word-break: break-word;
  overflow-wrap: anywhere;
}
.inst-alerts .col-alert-content .inst-alerts-name {
  white-space: normal;
  word-break: break-word;
  overflow-wrap: anywhere;
}
.inst-alerts .inst-alerts-error {
  margin-bottom: 12px;
}
/* 触底说明条：红色 info 条，与 error 条区分开 —— 它不是"加载失败"，
   而是"分数被下限截断了"这个事实的解释。 */
.inst-alerts .inst-alerts-floor {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-bottom: 12px;
  padding: 8px 12px;
  font-weight: 400;
  font-size: 12px;
  line-height: 1.6;
  color: #a8071a;
  background: #fff2f0;
  border: 1px solid #ffccc7;
  border-radius: 6px;
}
.inst-alerts .inst-alerts-floor svg {
  flex: none;
  margin-top: 2px;
}
</style>
