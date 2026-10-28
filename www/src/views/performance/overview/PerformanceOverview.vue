<template>
  <div class="gi-page-shell perf-overview">
    <!-- 顶部：标题 + 筛选 + 刷新 -->
    <div class="po-header">
      <div class="po-title">全局数据库性能概览</div>
      <a-space :size="10" wrap>
        <a-select
          v-model:value="filters.dbType"
          class="po-filter"
          :options="dbTypeOptions"
          @change="loadData"
        />
        <a-select
          v-model:value="filters.business"
          class="po-filter"
          :options="businessOptions"
          show-search
          option-filter-prop="label"
          @change="loadData"
        />
        <a-tooltip title="刷新">
          <a-button :loading="loading" aria-label="刷新" @click="loadData">
            <SyncOutlined />
          </a-button>
        </a-tooltip>
      </a-space>
    </div>

    <a-spin :spinning="loading">
      <!-- 趋势图 + 资源瓶颈 -->
      <div class="po-mid-row">
        <div class="po-card po-card--trend">
          <div class="po-card__head">
            <span class="po-card__title">慢SQL 数量趋势</span>
            <a-segmented
              v-model:value="filters.rangeDays"
              :options="rangeOptions"
              @change="loadData"
            />
          </div>
          <div v-if="trendMax > 0" class="po-chart">
            <div class="po-chart__bars">
              <div v-for="point in overview.slow_trend" :key="point.date" class="po-chart__col is-clickable" @click="goSlowSQL(point)">
                <div class="po-chart__bar-stack">
                  <span class="po-chart__value">{{ formatNumber(point.count) }}</span>
                  <div
                    class="po-chart__bar"
                    :style="{ height: barHeight(point.count) }"
                    :title="`${point.date}：${formatNumber(point.count)} 条，点击查看当日慢SQL`"
                  />
                </div>
              </div>
            </div>
            <div class="po-chart__labels">
              <span v-for="point in overview.slow_trend" :key="point.date" class="po-chart__label">
                {{ point.date }}
              </span>
            </div>
          </div>
          <a-empty v-else :image="Empty.PRESENTED_IMAGE_SIMPLE" description="所选时间范围内暂无慢SQL 数据" />
          <div class="po-chart__legend">
            <span class="po-chart__dot" />慢SQL 数量（条）
          </div>
        </div>

        <div class="po-card po-card--top">
          <div class="po-card__head">
            <span class="po-card__title">资源瓶颈 Top 5 实例</span>
            <a-select
              v-model:value="topMetricKey"
              class="po-top__select"
              size="small"
              :options="topMetricOptions"
            />
          </div>
          <template v-if="topRows.length > 0">
            <div v-for="row in topRows" :key="row.instance_id" class="po-top__item">
              <div class="po-top__row">
                <span class="po-top__name" :title="`${row.name || ''}（${row.target || ''}）`">
                  <span class="po-top__name-text">{{ clipText(row.name, 15) }}</span>
                  <span class="po-top__addr">（{{ clipText(row.target, 20) }}）</span>
                </span>
                <span class="po-top__pct" :style="{ color: topBarColor(row) }">{{ topValueText(row) }}</span>
              </div>
              <div class="po-top__track">
                <div
                  class="po-top__fill"
                  :style="{ width: `${topBarPercent(row)}%`, background: topBarColor(row) }"
                />
              </div>
            </div>
          </template>
          <a-empty v-else :image="Empty.PRESENTED_IMAGE_SIMPLE" description="暂无资源数据（依赖监控采集）" />
        </div>
      </div>

      <!-- 实例性能明细 -->
      <div class="po-card">
        <div class="po-card__head">
          <span class="po-card__title">实例性能明细（按性能水位排序）</span>
          <span class="po-card__hint">共 {{ overview.detail_instances.length }} 条</span>
        </div>
        <a-table
          :columns="detailColumns"
          :data-source="overview.detail_instances"
          :loading="loading"
          :pagination="false"
          row-key="instance_id"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'instance'">
              <div class="po-cell__name" :title="record.name">{{ clipText(record.name, 15) }}</div>
              <div class="po-cell__addr" :title="record.target">{{ clipText(record.target, 20) }}</div>
            </template>
            <template v-else-if="column.key === 'db_type'">{{ record.db_type }}</template>
            <template v-else-if="column.key === 'qps'">{{ formatNumber(record.qps) }}</template>
            <template v-else-if="column.key === 'tps'">{{ formatNumber(record.tps) }}</template>
            <template v-else-if="column.key === 'cpu'">
              <span :style="{ color: cpuTextColor(record.cpu) }">{{ Math.round(record.cpu) }}%</span>
            </template>
            <template v-else-if="column.key === 'memory'">{{ Math.round(record.memory) }}%</template>
            <template v-else-if="column.key === 'slow'">{{ formatNumber(record.slow_sql_24h) }}</template>
            <template v-else-if="column.key === 'level'">
              <span :style="{ color: levelMeta(record.level).color }">{{ levelMeta(record.level).text }}</span>
            </template>
            <template v-else-if="column.key === 'action'">
              <a-button type="link" size="small" class="po-action-btn" @click="goDiagnose(record)">诊断</a-button>
            </template>
          </template>
          <template #emptyText>
            <a-empty :image="Empty.PRESENTED_IMAGE_SIMPLE" description="暂无已纳入性能分析的实例" />
          </template>
        </a-table>
      </div>
    </a-spin>
  </div>
</template>

<script setup>
import { getPerformanceOverviewApi } from '@/api/performance'
import { Empty } from 'ant-design-vue'
import { SyncOutlined } from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import dayjs from 'dayjs'
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { clipText, formatNumber, SUPPORTED_DB_TYPES } from '../utils'

defineOptions({ name: 'PerformanceOverviewView' })

const router = useRouter()
const loading = ref(false)
const filters = reactive({
  dbType: '',
  business: '',
  rangeDays: '7',
})

const overview = reactive({
  business_options: [],
  slow_trend: [],
  top_resource: [],
  detail_instances: [],
})

const dbTypeOptions = [
  { label: '全部数据库类型', value: '' },
  ...SUPPORTED_DB_TYPES.map((item) => ({ label: item, value: item })),
]

const businessOptions = computed(() => ([
  { label: '全部业务', value: '' },
  ...overview.business_options.map((item) => ({ label: item, value: item })),
]))

const rangeOptions = [
  { label: '近3天', value: '3' },
  { label: '近7天', value: '7' },
  { label: '近14天', value: '14' },
  { label: '近30天', value: '30' },
]

const detailColumns = [
  { title: '数据源', key: 'instance', width: 220 },
  { title: '类型', key: 'db_type', width: 100 },
  { title: 'QPS', key: 'qps', width: 90, align: 'right' },
  { title: 'TPS', key: 'tps', width: 90, align: 'right' },
  { title: 'CPU', key: 'cpu', width: 90, align: 'right' },
  { title: '内存', key: 'memory', width: 90, align: 'right' },
  { title: '慢SQL (24h)', key: 'slow', width: 110, align: 'right' },
  { title: '性能水位', key: 'level', width: 100, align: 'center' },
  { title: '操作', key: 'action', width: 80, align: 'center' },
]

const trendMax = computed(() => Math.max(0, ...overview.slow_trend.map((item) => Number(item.count || 0))))

const barHeight = (count) => {
  if (trendMax.value <= 0 || count <= 0) return '4px'
  return `${Math.max((count / trendMax.value) * 100, 4)}%`
}

// 进度条配色：≥80 红、≥50 橙、其余蓝
const cpuColor = (value) => {
  if (value >= 80) return '#ef4444'
  if (value >= 50) return '#f59e0b'
  return '#3b82f6'
}

// 表格 CPU 文字配色：≥85 红、≥70 橙
const cpuTextColor = (value) => {
  if (value >= 85) return '#ef4444'
  if (value >= 70) return '#f59e0b'
  return '#1f2329'
}

const levelMeta = (level) => {
  switch (level) {
    case 'danger':
      return { text: '危险', color: '#ef4444' }
    case 'warn':
      return { text: '预警', color: '#f59e0b' }
    default:
      return { text: '正常', color: '#16a34a' }
  }
}

const goDiagnose = (record) => {
  router.push({ path: '/performance/slow-sql', query: { instance_id: record.instance_id } })
}

// 点击趋势柱 → 慢SQL页，并把该页时间筛选锁定为所点柱状图对应日期（当天全天）。
// 后端 slow_trend.date 只返回 MM-DD（无年份），在当前所选范围（近 N 天）内映射回完整日期；
// 不在窗口内（跨年等极端情况）按当前年兜底。
const resolveTrendDate = (mmdd) => {
  const days = Number(filters.rangeDays) || 7
  for (let offset = 0; offset < days; offset++) {
    const day = dayjs().subtract(offset, 'day')
    if (day.format('MM-DD') === mmdd) return day.format('YYYY-MM-DD')
  }
  return `${dayjs().format('YYYY')}-${mmdd}`
}

// 无论柱子数量是否为 0 都可点击：跳转后由慢SQL页展示该日期的列表（无数据走既有空态）。
const goSlowSQL = (point) => {
  router.push({ path: '/performance/slow-sql', query: { focus_date: resolveTrendDate(point.date) } })
}

// —— 资源瓶颈 Top5：右上角排序下拉，切换后按所选指标降序取前 5（默认 CPU使用率） ——
// 顺序按「同族相邻」：CPU/内存（恒有采集，百分比）→ 磁盘使用率/磁盘使用量（可能未采集）→ IOPS
const topMetricOptions = [
  { label: 'CPU使用率', value: 'cpu' },
  { label: '内存使用率', value: 'memory' },
  { label: '磁盘使用率', value: 'disk_usage' },
  { label: '磁盘使用量', value: 'disk_size' },
  { label: 'IOPS', value: 'iops' },
]
const topMetricKey = ref('cpu')

const topMetricValue = (row) => {
  switch (topMetricKey.value) {
    case 'cpu':
      return row.cpu
    case 'memory':
      return row.memory
    case 'disk_usage':
      return row.disk_used_pct
    case 'disk_size':
      return row.disk_used_bytes
    case 'iops':
      return row.iops
    default:
      return 0
  }
}

const topHasValue = (row) => {
  const v = topMetricValue(row)
  return v !== null && v !== undefined && Number.isFinite(Number(v))
}

const topRows = computed(() => (
  [...overview.top_resource]
    .sort((a, b) => (Number(topMetricValue(b)) || 0) - (Number(topMetricValue(a)) || 0))
    .slice(0, 5)
))

const formatDiskSize = (bytes) => {
  const v = Number(bytes)
  if (!Number.isFinite(v) || v <= 0) return '0B'
  if (v >= 1024 ** 4) return `${(v / 1024 ** 4).toFixed(2)}TB`
  if (v >= 1024 ** 3) return `${(v / 1024 ** 3).toFixed(1)}GB`
  if (v >= 1024 ** 2) return `${(v / 1024 ** 2).toFixed(0)}MB`
  return `${Math.round(v)}B`
}

const topValueText = (row) => {
  if (!topHasValue(row)) return '—'
  const v = Number(topMetricValue(row))
  switch (topMetricKey.value) {
    case 'cpu':
    case 'memory':
    case 'disk_usage':
      return `${Math.round(v)}%`
    case 'disk_size':
      return formatDiskSize(v)
    case 'iops':
      return formatNumber(Math.round(v))
    default:
      return String(v)
  }
}

const topBarPercent = (row) => {
  if (!topHasValue(row)) return 0
  const v = Number(topMetricValue(row))
  // 占比类指标（CPU/内存/磁盘使用率）直接用原值；相对量指标以列表内最大值为 100% 基准
  if (topMetricKey.value === 'cpu' || topMetricKey.value === 'memory' || topMetricKey.value === 'disk_usage') {
    return Math.min(v, 100)
  }
  const max = Math.max(...topRows.value.map((item) => Number(topMetricValue(item)) || 0))
  if (max <= 0) return 0
  return Math.max((v / max) * 100, 2)
}

const topBarColor = (row) => {
  if (!topHasValue(row)) return '#c9cdd4'
  const v = Number(topMetricValue(row))
  // CPU 与磁盘使用率同一套配色：85/70 阈值 + 青色系（与磁盘使用量/IOPS 的青色默认一致）；
  // 内存维持原有热力配色（≥80 红 / ≥50 橙 / 其余蓝）
  if (topMetricKey.value === 'memory') return cpuColor(v)
  if (topMetricKey.value === 'cpu' || topMetricKey.value === 'disk_usage') {
    if (v >= 85) return '#ef4444'
    if (v >= 70) return '#f59e0b'
    return '#0f766e'
  }
  return '#0f766e'
}

const loadData = async () => {
  loading.value = true
  try {
    const res = await getPerformanceOverviewApi({
      db_type: filters.dbType || undefined,
      business: filters.business || undefined,
      range_days: Number(filters.rangeDays) || 7,
    })
    overview.business_options = res?.data?.business_options || []
    overview.slow_trend = res?.data?.slow_trend || []
    overview.top_resource = res?.data?.top_resource || []
    overview.detail_instances = res?.data?.detail_instances || []
  } catch (error) {
    message.error(error?.message || '加载性能概览失败')
  } finally {
    loading.value = false
  }
}

onMounted(loadData)
</script>

<style scoped lang="scss">
.perf-overview {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.po-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.po-title {
  color: #1f2329;
  font-size: 20px;
  font-weight: 700;
}

.po-filter {
  width: 240px;
}

/* 中部两卡：趋势 2/3 + Top5 1/3 */
.po-mid-row {
  display: grid;
  grid-template-columns: 2fr 1fr;
  gap: 16px;
}

@media (max-width: 1200px) {
  .po-mid-row {
    grid-template-columns: 1fr;
  }
}

.po-card {
  padding: 18px 20px;
  background: #fff;
  border: 1px solid #e5e8eb;
  border-radius: 8px;
}

.po-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 14px;
}

.po-card__title {
  color: #1f2329;
  font-size: 15px;
  font-weight: 600;
}

.po-card__hint {
  color: #86909c;
  font-size: 12px;
}

/* 纯 CSS 柱状图：不引图表库，与项目依赖保持一致 */
.po-chart {
  display: flex;
  flex-direction: column;
}

.po-chart__bars {
  display: flex;
  align-items: flex-end;
  justify-content: space-around;
  height: 240px;
  padding: 0 8px;
  border-bottom: 1px solid #e5e8eb;
}

.po-chart__col {
  display: flex;
  align-items: flex-end;
  height: 100%;
  flex: 1;
  justify-content: center;
}

/* 趋势柱可点击跳转慢SQL页：整列作为点击热区（含 0 数量的矮柱），hover 微亮反馈 */
.po-chart__col.is-clickable {
  cursor: pointer;
}

.po-chart__col.is-clickable:hover .po-chart__bar {
  filter: brightness(1.1);
}

.po-chart__bar-stack {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: flex-end;
  height: 100%;
}

.po-chart__value {
  margin-bottom: 4px;
  color: var(--gi-color-primary, #0f766e);
  font-size: 12px;
  font-weight: 600;
  line-height: 1;
  white-space: nowrap;
}

.po-chart__bar {
  width: 36px;
  max-width: 60%;
  min-height: 4px;
  /* 慢SQL数量趋势统一橙色（告警/预警语义色） */
  background: #f59e0b;
  border-radius: 4px 4px 0 0;
  transition: height 0.3s ease;
}

.po-chart__labels {
  display: flex;
  justify-content: space-around;
  margin-top: 8px;
}

.po-chart__label {
  flex: 1;
  text-align: center;
  color: #86909c;
  font-size: 12px;
}

.po-chart__legend {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  margin-top: 10px;
  color: #4e5969;
  font-size: 12px;
}

.po-chart__dot {
  width: 10px;
  height: 10px;
  border-radius: 2px;
  /* 图例圆点与趋势柱同色（橙色） */
  background: #f59e0b;
}

/* Top5 进度条 */
.po-top__item {
  padding: 9px 0;
}

/* 排序下拉：与卡片标题行对齐，高度收紧 */
.po-top__select {
  width: 128px;
}

.po-top__select.ant-select-sm .ant-select-selector {
  height: 28px;
  border-radius: 6px;
}

.po-top__select.ant-select-sm .ant-select-selection-item {
  line-height: 26px;
  font-size: 12px;
}

.po-top__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

/* 名称与地址分列：宽度不足时压缩名称（ellipsis），地址尽量完整保留 */
.po-top__name {
  display: flex;
  align-items: baseline;
  gap: 4px;
  min-width: 0;
  color: #1f2329;
  font-size: 13px;
  font-weight: 500;
}

.po-top__name-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.po-top__addr {
  flex: 0 0 auto;
  color: #86909c;
  font-size: 12px;
  font-weight: 400;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.po-top__pct {
  font-size: 13px;
  font-weight: 600;
  flex-shrink: 0;
}

.po-top__track {
  margin-top: 7px;
  height: 6px;
  border-radius: 3px;
  background: #eef0f3;
  overflow: hidden;
}

.po-top__fill {
  height: 100%;
  border-radius: 3px;
  transition: width 0.3s ease;
}

/* 明细表单元格：已按字符数截断，这里再兜一层 nowrap，避免列宽不足时换行导致行高不齐 */
.po-cell__name {
  color: #1f2329;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.po-cell__addr {
  margin-top: 2px;
  color: #86909c;
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 操作列链接按钮：强制走主题色（antd link 默认 colorLink 为蓝色） */
.po-action-btn {
  color: var(--ant-colorPrimary, #0f766e);
}

@media (max-width: 768px) {
  .po-filter,
  .po-top__select {
    width: 100%;
  }

  .po-header,
  .po-card__head {
    row-gap: 8px;
  }

  .po-chart__bars {
    height: 180px;
    padding: 0 2px;
  }

  .po-chart__bar {
    width: 28px;
    max-width: 56%;
  }

  .po-chart__value,
  .po-chart__label {
    font-size: 10.5px;
  }

  .po-top__row {
    flex-wrap: wrap;
    gap: 4px 8px;
  }

  .po-top__name {
    flex: 1 1 100%;
  }

  .po-top__addr {
    flex: 0 1 auto;
    white-space: normal;
  }

  .po-cell__name,
  .po-cell__addr {
    white-space: normal;
    overflow: visible;
    text-overflow: clip;
    word-break: break-word;
  }

  .po-card {
    padding: 14px;
  }
}
</style>
