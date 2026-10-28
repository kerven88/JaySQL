<template>
  <div class="gi-page-shell performance-page" :class="{ 'performance-page--detail': selectedInstanceID }">
    <template v-if="!selectedInstanceID">
      <div class="gi-page-toolbar performance-toolbar">
        <div class="toolbar-left">
          <div class="page-title">慢 SQL</div>
        </div>
        <a-space wrap>
          <a-select
            v-model:value="selectedSummaryQuickRange"
            class="quick-range-select"
            placeholder="快捷范围"
            :options="quickRangeOptions"
            @change="handleSummaryQuickRangeChange"
          />
          <a-range-picker
            v-model:value="summaryFilters.timeRange"
            class="filter-range"
            show-time
            value-format="YYYY-MM-DD HH:mm:ss"
            :disabled-date="disabledSummaryDate"
            @calendarChange="handleSummaryTimeRangeCalendarChange"
            @change="handleSummaryTimeRangeChange"
          />
          <a-input-search
            v-model:value="summaryFilters.search"
            allow-clear
            class="search-input"
            placeholder="搜索实例名、实例ID、主机"
            @search="loadInstanceSummary"
          />
          <a-select
            v-model:value="summaryFilters.status"
            class="status-filter-select"
            placeholder="采集状态"
            :options="collectStatusOptions"
            @change="loadInstanceSummary"
          />
          <div class="view-switch">
            <a-tooltip title="卡片">
              <a-button :type="displayMode === 'card' ? 'primary' : 'default'" @click="displayMode = 'card'">
                <AppstoreOutlined />
              </a-button>
            </a-tooltip>
            <a-tooltip title="列表">
              <a-button :type="displayMode === 'list' ? 'primary' : 'default'" @click="displayMode = 'list'">
                <UnorderedListOutlined />
              </a-button>
            </a-tooltip>
          </div>
          <a-tooltip title="采集前提条件">
            <a-button aria-label="采集前提条件" @click="openPrerequisites(null)">
              <FileSearchOutlined />
            </a-button>
          </a-tooltip>
        </a-space>
      </div>

      <a-empty
        v-if="!summaryLoading && !instanceSummaryRows.length"
        class="instance-empty"
        description="暂无实例"
      >
        <template #description>
          <div>没有符合条件的实例</div>
          <div class="instance-empty__hint">
            {{ excludedHint || '已接入的 MySQL / PostgreSQL / Oracle 实例都会出现在这里；采集失败的会标注原因，不会被隐藏。' }}
          </div>
        </template>
      </a-empty>

      <div v-else-if="displayMode === 'card'" class="instance-card-grid">
        <a-card
          v-for="item in instanceSummaryRows"
          :key="item.instance_id"
          class="instance-card"
          :bordered="false"
          hoverable
          @click="openInstance(item)"
        >
          <div class="instance-card__header">
            <div class="instance-card__identity">
              <div class="instance-card__title" :title="item.remark || item.instance_id">{{ clipText(item.remark || item.instance_id, 20) }}</div>
              <div class="instance-card__subtitle" :title="item.target">{{ clipText(item.target, 20) }}</div>
            </div>
            <DbTypeLogo :db-type="item.db_type" :version="item.db_version" />
          </div>
          <div class="instance-card__body">
            <div class="score-ring" :class="`score-ring--${scoreTone(item.system_score_level)}`">
              {{ item.system_score }}
            </div>
            <div class="score-meta">
              <div class="score-text">系统评分 {{ item.system_score_text }}</div>
            </div>
          </div>
          <div class="instance-card__stats">
            <div class="stat-block">
              <div class="stat-label">数据源信息</div>
              <div class="stat-value">{{ item.environment_name || '未归属环境' }}</div>
            </div>
            <div class="stat-block">
              <div class="stat-label">数据类型</div>
              <div class="stat-value">{{ item.db_type }}</div>
            </div>
            <div class="stat-block">
              <div class="stat-label">慢SQL数量</div>
              <div class="stat-value">{{ formatNumber(item.slow_sql_count) }}</div>
            </div>
            <div class="stat-block">
              <div class="stat-label">全表扫描次数</div>
              <div class="stat-value">{{ formatNumber(item.full_scan_count) }}</div>
            </div>
          </div>
          <div class="instance-card__status" :class="`instance-card__status--${statusOf(item).tone}`">
            <a-tooltip :title="statusOf(item).detail">
              <span>{{ statusOf(item).text }}</span>
            </a-tooltip>
            <a-button
              v-if="statusOf(item).tone !== 'ok'"
              type="link"
              size="small"
              @click.stop="openPrerequisites(item)"
            >
              排查指引
            </a-button>
          </div>
          <div class="instance-card__footer">点击查看详情</div>
        </a-card>
      </div>

      <a-card v-else :bordered="false">
        <a-table
          :columns="instanceColumns"
          :data-source="instanceSummaryRows"
          :loading="summaryLoading"
          :pagination="false"
          row-key="instance_id"
          table-layout="fixed"
          @change="handleInstanceTableChange"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'source'">
              <div class="cell-primary" :title="record.remark || record.instance_id">{{ clipText(record.remark || record.instance_id, 15) }}</div>
              <div class="cell-secondary" :title="record.target">{{ clipText(record.target, 20) }}</div>
            </template>
            <template v-else-if="column.key === 'db_type'">
              <div class="db-type-cell">
                <DbTypeLogo :db-type="record.db_type" :version="record.db_version" />
              </div>
            </template>
            <template v-else-if="column.key === 'status'">
              <div class="status-cell" :class="`status-cell--${statusOf(record).tone}`">
                <a-tooltip :title="statusOf(record).detail" overlay-class-name="slow-status-tooltip">
                  <span class="status-cell__text">{{ statusOf(record).text }}</span>
                </a-tooltip>
                <a-button
                  v-if="statusOf(record).tone !== 'ok'"
                  type="link"
                  size="small"
                  class="status-cell__link"
                  @click="openPrerequisites(record)"
                >
                  排查指引
                </a-button>
              </div>
            </template>
            <template v-else-if="column.key === 'action'">
              <a-button type="link" class="view-detail-action" @click="openInstance(record)">查看详情</a-button>
            </template>
          </template>
        </a-table>
      </a-card>
      <!-- 口径提示：日志里 grep duration 的行数天然多于页面条数，
           不给说明就会被当成「采集漏数据」。 -->
      <div v-if="!selectedInstanceID && instanceSummaryRows.length" class="caliber-hint">
        统计口径：慢SQL数量只计入执行完成记录（statement / execute），扩展协议的 parse / bind 属于同一查询的准备阶段，不计入；
        悬浮「状态」可查看最近一次采集消息（新增条数 / 跳过的协议阶段条数）。
      </div>
    </template>

    <template v-else>
      <div class="detail-header">
        <div class="detail-header__left">
          <a-button @click="backToList">
            <ArrowLeftOutlined />
            返回实例列表
          </a-button>
          <div class="instance-identity">
            <a-tooltip :title="selectedInstanceName">
              <div class="instance-identity__name">{{ clipText(selectedInstanceName, 20) }}</div>
            </a-tooltip>
            <a-tooltip :title="selectedInstanceTarget">
              <div class="instance-identity__target">{{ clipText(selectedInstanceTarget, 20) }}</div>
            </a-tooltip>
          </div>
        </div>
        <a-space wrap>
          <a-select
            v-model:value="selectedQuickRange"
            class="quick-range-select"
            placeholder="快捷范围"
            :options="quickRangeOptions"
            @change="handleQuickRangeChange"
          />
          <a-range-picker
            v-model:value="detailFilters.timeRange"
            class="filter-range"
            show-time
            value-format="YYYY-MM-DD HH:mm:ss"
            :disabled-date="disabledDetailDate"
            @calendarChange="handleTimeRangeCalendarChange"
            @change="handleTimeRangeChange"
          />
          <a-tooltip title="刷新">
            <a-button :loading="detailLoading || analysisLoading" aria-label="刷新" @click="reloadSelectedView">
              <SyncOutlined />
            </a-button>
          </a-tooltip>
        </a-space>
      </div>

      <a-card class="chart-section-card" :bordered="false">
        <div class="summary-metrics">
          <div class="metric-card">
            <div class="metric-label">慢SQL总数</div>
            <div class="metric-value">{{ formatNumber(analysisSummary.slow_sql_count) }}</div>
          </div>
          <div class="metric-card">
            <div class="metric-label">平均响应时间</div>
            <div class="metric-value">{{ formatDuration(analysisSummary.avg_duration_ms) }}</div>
          </div>
          <div class="metric-card">
            <div class="metric-label">最慢SQL</div>
            <div class="metric-value">{{ formatDuration(analysisSummary.max_duration_ms) }}</div>
          </div>
          <div class="metric-card">
            <div class="metric-label">全表扫描占比</div>
            <div class="metric-value">{{ formatPercent(analysisSummary.full_scan_ratio) }}</div>
          </div>
        </div>

        <div class="chart-section-title">慢SQL趋势</div>
        <div v-if="trendSeries.points.length" class="trend-chart" @dblclick="resetTrendZoom">
          <div class="trend-axis-title trend-axis-title--left">慢SQL数量</div>
          <div class="trend-axis-title trend-axis-title--right">平均响应时间(秒)</div>
          <button v-if="trendZoomRange" class="trend-zoom-reset" type="button" @click="resetTrendZoom">
            恢复完整时间范围
          </button>
          <svg
            ref="trendSvgRef"
            :viewBox="`0 0 ${trendSeries.width} ${trendSeries.height}`"
            preserveAspectRatio="none"
            @pointerdown="handleTrendBrushStart"
            @pointermove="handleTrendBrushMove"
            @pointerup="handleTrendBrushEnd"
          >
          <line
            :x1="trendSeries.paddingLeft"
            :x2="trendSeries.paddingLeft"
            :y1="trendSeries.paddingTop"
            :y2="trendSeries.height - trendSeries.paddingBottom"
            class="trend-axis"
          />
          <line
            :x1="trendSeries.width - trendSeries.paddingRight"
            :x2="trendSeries.width - trendSeries.paddingRight"
            :y1="trendSeries.paddingTop"
            :y2="trendSeries.height - trendSeries.paddingBottom"
            class="trend-axis"
          />
          <line
            :x1="trendSeries.paddingLeft"
            :x2="trendSeries.width - trendSeries.paddingRight"
            :y1="trendSeries.height - trendSeries.paddingBottom"
            :y2="trendSeries.height - trendSeries.paddingBottom"
            class="trend-axis"
          />
          <line
            v-for="grid in trendSeries.gridLines"
            :key="`grid-${grid}`"
            :x1="trendSeries.paddingLeft"
            :x2="trendSeries.width - trendSeries.paddingRight"
            :y1="grid"
            :y2="grid"
            class="trend-grid"
          />
          <text
            v-for="tick in trendSeries.leftTicks"
            :key="`left-${tick.y}-${tick.value}`"
            :x="trendSeries.paddingLeft - 10"
            :y="tick.y + 4"
            text-anchor="end"
            class="trend-y-label"
          >
            {{ tick.value }}
          </text>
          <text
            v-for="tick in trendSeries.rightTicks"
            :key="`right-${tick.y}-${tick.value}`"
            :x="trendSeries.width - trendSeries.paddingRight + 10"
            :y="tick.y + 4"
            text-anchor="start"
            class="trend-y-label"
          >
            {{ tick.value }}
          </text>
          <polyline
            v-if="!hiddenTrendSeries.count"
            :points="trendSeries.countPolyline"
            class="trend-line trend-line--count"
          />
          <polyline
            v-if="!hiddenTrendSeries.duration"
            :points="trendSeries.durationPolyline"
            class="trend-line trend-line--duration"
          />
          <template v-if="!hiddenTrendSeries.count">
            <circle
              v-for="point in trendSeries.points"
              :key="`count-${point.label}`"
              :cx="point.x"
              :cy="point.countY"
              r="3.5"
              class="trend-point trend-point--count"
            />
          </template>
          <template v-if="!hiddenTrendSeries.duration">
            <circle
              v-for="point in trendSeries.points"
              :key="`duration-${point.label}`"
              :cx="point.x"
              :cy="point.durationY"
              r="3.5"
              class="trend-point trend-point--duration"
            />
          </template>
          <rect
            v-for="(point, pointIndex) in trendSeries.points"
            :key="`hover-${point.bucketTimeText}`"
            :x="point.hitStartX"
            :y="trendSeries.paddingTop"
            :width="point.hitWidth"
            :height="trendSeries.height - trendSeries.paddingTop - trendSeries.paddingBottom"
            class="trend-hit-area"
            @mouseenter="hoveredTrendIndex = pointIndex"
            @mouseleave="clearTrendHover(pointIndex)"
          />
          <text
            v-for="point in trendSeries.points"
            :key="`label-${point.label}`"
            :x="point.x"
            :y="trendSeries.height - 10"
            text-anchor="middle"
            class="trend-label"
          >
            {{ point.label }}
          </text>
          <rect
            v-if="trendBrushRect"
            :x="trendBrushRect.x"
            :y="trendSeries.paddingTop"
            :width="trendBrushRect.width"
            :height="trendSeries.height - trendSeries.paddingTop - trendSeries.paddingBottom"
            class="trend-brush"
          />
          </svg>
          <div
            v-if="hoveredTrendPoint"
            class="trend-tooltip"
            :style="trendTooltipStyle"
          >{{ hoveredTrendPoint.tooltip }}</div>
          <div class="trend-legend">
            <span
              class="trend-legend-item"
              :class="{ 'trend-legend-item--off': hiddenTrendSeries.count }"
              title="点击隐藏/显示该指标"
              @click="toggleTrendSeries('count')"
            ><i class="legend-dot legend-dot--count" /> 慢SQL数量</span>
            <span
              class="trend-legend-item"
              :class="{ 'trend-legend-item--off': hiddenTrendSeries.duration }"
              title="点击隐藏/显示该指标"
              @click="toggleTrendSeries('duration')"
            ><i class="legend-dot legend-dot--duration" /> 平均响应时间(秒)</span>
          </div>
        </div>
        <a-empty v-else description="当前时间范围暂无趋势数据" />
      </a-card>

      <a-card class="detail-card" :bordered="false">
        <a-tabs class="detail-tabs" v-model:activeKey="activeTab" @change="handleActiveTabChange">
          <template #tabBarExtraContent>
            <a-button
              class="tabs-export-btn"
              :loading="activeTab === 'stats' ? statsExporting : detailExporting"
              @click="activeTab === 'stats' ? handleExportStats() : handleExportDetail()"
            >
              <template #icon><DownloadOutlined /></template>
              导出
            </a-button>
          </template>
          <a-tab-pane key="stats" tab="慢SQL统计">
            <div class="stats-pane">
              <a-table
                :key="statsTableKey"
                class="stats-table"
                :columns="statsColumns"
                :data-source="statsRows"
                :loading="statsLoading"
                :pagination="false"
                row-key="sql_digest"
                :scroll="statsTableScroll"
                @change="handleStatsSortChange"
              >
                <template #bodyCell="{ column, record }">
                  <template v-if="column.key === 'sql_fingerprint'">
                    <div class="sql-preview sql-link" @click="openSQLDrawer(record, 'stats')">
                      {{ sqlPreview(record.sql_fingerprint || record.sql_text, 180) }}
                    </div>
                  </template>
                  <template v-else-if="column.key === 'schema_name'">
                    {{ record.schema_name || '-' }}
                  </template>
                  <template v-else-if="column.key === 'total_duration'">
                    {{ formatSeconds(record.total_duration_ms) }}
                  </template>
                  <template v-else-if="column.key === 'avg_duration'">
                    {{ formatAvgMs(record) }}
                  </template>
                  <template v-else-if="column.key === 'scan_rows'">
                    {{ formatNumber(record.max_rows_examined || record.total_rows_examined) }}
                  </template>
                </template>
              </a-table>
              <div v-if="statsPager.total > 0" class="stats-pagination-wrap">
                <a-pagination
                  :current="statsPager.page"
                  :page-size="statsPager.pageSize"
                  :total="statsPager.total"
                  :show-size-changer="true"
                  :show-total="statsPagination.showTotal"
                  @change="handleStatsPageChange"
                  @showSizeChange="handleStatsPageSizeChange"
                />
              </div>
            </div>
          </a-tab-pane>

          <a-tab-pane key="detail" tab="慢SQL明细">
            <div class="detail-pane">
              <a-table
                :key="detailTableKey"
                class="analysis-section"
                :columns="detailColumns"
                :data-source="detailRows"
                :loading="detailLoading"
                :pagination="detailPagination"
                row-key="id"
                :scroll="{ x: 1680 }"
                @change="handleDetailTableChange"
              >
                <template #bodyCell="{ column, record }">
                  <template v-if="column.key === 'sql_text'">
                    <div class="sql-preview sql-link" @click="openSQLDrawer(record, 'detail')">
                      {{ sqlPreview(record.sql_text, 220) }}
                    </div>
                  </template>
                  <template v-else-if="column.key === 'username'">
                    {{ record.username || '-' }}
                  </template>
                  <template v-else-if="column.key === 'schema'">
                    {{ record.schema || '-' }}
                  </template>
                  <template v-else-if="column.key === 'duration'">
                    {{ formatSeconds(record.duration) }}
                  </template>
                  <template v-else-if="column.key === 'lock_time_ms'">
                    {{ formatSeconds(record.lock_time_ms) }}
                  </template>
                  <template v-else-if="column.key === 'rows_examined'">
                    {{ formatNumber(record.rows_examined) }}
                  </template>
                  <template v-else-if="column.key === 'return_rows'">
                    {{ formatNumber(record.return_rows) }}
                  </template>
                </template>
              </a-table>
            </div>
          </a-tab-pane>
        </a-tabs>
      </a-card>
    </template>

    <a-drawer v-model:open="sqlDrawer.open" :title="sqlDrawer.title" width="1080">
      <template v-if="sqlDrawer.mode === 'fingerprint'">
        <a-table
          class="drawer-table"
          :columns="sqlDrawerColumns"
          :data-source="sqlDrawer.rows"
          :loading="sqlDrawer.loading"
          :pagination="false"
          row-key="id"
          :scroll="{ x: 920 }"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'duration'">
              {{ formatSeconds(record.duration) }}
            </template>
            <template v-else-if="column.key === 'sql_text'">
              <pre class="detail-code detail-code--drawer-row">{{ record.sql_text || '-' }}</pre>
            </template>
          </template>
        </a-table>
        <div v-if="sqlDrawer.total > 0" class="drawer-pagination">
          <a-pagination
            :current="sqlDrawer.page"
            :page-size="sqlDrawer.pageSize"
            :total="sqlDrawer.total"
            :show-size-changer="true"
            :show-total="(total) => `共 ${formatNumber(total)} 条明细，按时间从近到远排序`"
            @change="handleSQLDrawerPageChange"
            @showSizeChange="handleSQLDrawerPageSizeChange"
          />
        </div>
      </template>
      <template v-else>
        <pre class="detail-code">{{ sqlDrawer.record.sql_text || '-' }}</pre>
      </template>
    </a-drawer>

    <SlowLogPrerequisitesDrawer
      v-model:open="prerequisitesDrawer.open"
      :default-family="prerequisitesDrawer.family"
      :highlight-keys="prerequisitesDrawer.highlightKeys"
    />
  </div>
</template>

<script setup>
import {
  exportPerformanceSlowSQLApi,
  exportPerformanceSlowSQLStatsApi,
  getPerformanceSlowSQLAnalysisApi,
  getPerformanceSlowSQLApi,
  getPerformanceSlowSQLInstancesApi,
  getPerformanceSlowSQLStatsApi,
  getSlowLogDiagnosisApi,
} from '@/api/performance'
import DbTypeLogo from './components/DbTypeLogo.vue'
import SlowLogPrerequisitesDrawer from './components/SlowLogPrerequisitesDrawer.vue'
import {
  AppstoreOutlined,
  ArrowLeftOutlined,
  DownloadOutlined,
  FileSearchOutlined,
  SyncOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import dayjs from 'dayjs'
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { clipText, formatDuration, formatNumber, sqlPreview } from '../utils'

defineOptions({ name: 'SlowSQLAnalysisView' })

const router = useRouter()
const route = useRoute()

const displayMode = ref('list')
const summaryLoading = ref(false)
const analysisLoading = ref(false)
const statsLoading = ref(false)
const detailLoading = ref(false)

const instanceSummaryRawRows = ref([])
// 后端会剔除「未开启慢日志 / 仅表格方式 / 无法探测 / 文件不可读」的实例，
// 这里保留它给出的一句话说明，避免用户以为实例"凭空消失"。
const excludedHint = ref('')
const statsRows = ref([])
const detailRows = ref([])
const activeTab = ref('stats')

const summaryFilters = reactive({
  search: '',
  // 采集状态筛选：全部 / 正常 / 采集失败 / 未采集。
  // 注意这是"想看哪一类"，不是"允许哪一类出现"——失败条目始终在列表里。
  status: 'all',
  timeRange: [dayjs().subtract(1, 'day').format('YYYY-MM-DD HH:mm:ss'), dayjs().format('YYYY-MM-DD HH:mm:ss')],
})

const collectStatusOptions = [
  { label: '全部状态', value: 'all' },
  { label: '采集正常', value: 'ok' },
  { label: '采集失败', value: 'failed' },
  { label: '未具备采集条件', value: 'not_ready' },
]

const detailFilters = reactive({
  timeRange: [dayjs().subtract(1, 'day').format('YYYY-MM-DD HH:mm:ss'), dayjs().format('YYYY-MM-DD HH:mm:ss')],
})

const maxTimeRangeDays = 7
const maxTimeRangeMs = maxTimeRangeDays * 24 * 60 * 60 * 1000
const quickRangeOptions = [
  { label: '最近5分钟', value: '5m' },
  { label: '最近1小时', value: '1h' },
  { label: '最近12小时', value: '12h' },
  { label: '最近1天', value: '1d' },
  { label: '最近7天', value: '7d' },
]

const analysisData = reactive({
  instance: {},
  summary: {},
  trend: [],
})

const statsPager = reactive({
  page: 1,
  pageSize: 10,
  total: 0,
  rawTotal: 0,
})

const detailPager = reactive({
  page: 1,
  pageSize: 10,
  total: 0,
})

const sqlDrawer = reactive({
  open: false,
  title: 'SQL详情',
  mode: 'single',
  loading: false,
  record: {},
  rows: [],
  page: 1,
  pageSize: 10,
  total: 0,
})

const selectedSummaryQuickRange = ref('1d')
const selectedQuickRange = ref('1d')
const timeRangeAnchor = ref(null)
const lastValidTimeRange = ref([...detailFilters.timeRange])
const summaryTimeRangeAnchor = ref(null)
const lastValidSummaryTimeRange = ref([...summaryFilters.timeRange])
const instanceSorter = reactive({
  field: '',
  order: null,
})

// 列宽固定比例分配（配合 table-layout="fixed" 精确生效）：
//   数据源 20% —— 两行（实例名 + 地址），内容最长，给最宽
//   状态   20% —— 异常时要放「库类型 + 原因」，不能太窄
//   数据库类型 / 慢SQL数量 / 全表扫描次数 各 16%
//   操作   12% —— 只有一个按钮，够用即可
const instanceColumns = computed(() => [
  { title: '数据源', key: 'source', width: '20%' },
  { title: '数据库类型', key: 'db_type', width: '16%', align: 'center' },
  {
    title: '慢SQL数量',
    dataIndex: 'slow_sql_count',
    key: 'slow_sql_count',
    width: '16%',
    align: 'center',
    sorter: true,
    sortOrder: instanceSorter.field === 'slow_sql_count' ? instanceSorter.order : null,
    sortDirections: ['descend', 'ascend'],
    showSorterTooltip: false,
  },
  { title: '全表扫描次数', dataIndex: 'full_scan_count', key: 'full_scan_count', width: '16%', align: 'center' },
  { title: '状态', key: 'status', width: '20%' },
  { title: '操作', key: 'action', width: '12%', align: 'center' },
])

// 服务端排序状态：与后端 sort_field/sort_order 白名单对应；默认统计按时间、明细按采集时间。
// 表格用非受控排序（defaultSortOrder），切换 tab 时重置状态并自增 key 强制重挂载，
// 避免 antd 内部排序状态与数据不一致。
const statsSorter = reactive({ field: 'latest_time', order: 'descend' })
const detailSorter = reactive({ field: 'created_at', order: 'descend' })
const statsTableKey = ref(0)
const detailTableKey = ref(0)

const statsColumns = [
  { title: '时间', dataIndex: 'latest_time', key: 'latest_time', width: 180, sorter: true, defaultSortOrder: 'descend' },
  { title: 'SQL语句', key: 'sql_fingerprint', minWidth: 520 },
  { title: '数据库名', dataIndex: 'schema_name', key: 'schema_name', width: 180 },
  { title: '执行次数', dataIndex: 'exec_count', key: 'exec_count', width: 130, sorter: true, sortDirections: ['descend', 'ascend'] },
  { title: '总耗时(S)', key: 'total_duration', width: 150, sorter: true, sortDirections: ['descend', 'ascend'] },
  { title: '平均(MS)', key: 'avg_duration', width: 140, sorter: true, sortDirections: ['descend', 'ascend'] },
  { title: '扫描行数', key: 'scan_rows', width: 140, sorter: true, sortDirections: ['descend', 'ascend'] },
]

const detailColumns = [
  { title: '慢日志采集时间', dataIndex: 'created_at', key: 'created_at', width: 180, sorter: true, defaultSortOrder: 'descend' },
  { title: 'SQL语句', key: 'sql_text', minWidth: 520 },
  { title: '客户端IP', dataIndex: 'client_host', key: 'client_host', width: 220 },
  { title: '用户', dataIndex: 'username', key: 'username', width: 140 },
  { title: '数据库名', dataIndex: 'schema', key: 'schema', width: 180 },
  { title: '执行时长（秒）', key: 'duration', width: 140, sorter: true, sortDirections: ['descend', 'ascend'] },
  { title: '锁定时长(秒)', key: 'lock_time_ms', width: 140, sorter: true, sortDirections: ['descend', 'ascend'] },
  { title: '解析行数', key: 'rows_examined', width: 120, sorter: true, sortDirections: ['descend', 'ascend'] },
  { title: '返回行数', key: 'return_rows', width: 120, sorter: true, sortDirections: ['descend', 'ascend'] },
]

const sqlDrawerColumns = [
  { title: '时间', dataIndex: 'created_at', key: 'created_at', width: 180 },
  { title: '执行时长(S)', key: 'duration', width: 140 },
  { title: '客户端IP', dataIndex: 'client_host', key: 'client_host', width: 180 },
  { title: 'SQL明细', key: 'sql_text', minWidth: 520 },
]

const selectedInstanceID = computed(() => String(route.query.instance_id || ''))
const selectedInstanceName = computed(() => analysisData.instance?.remark || analysisData.instance?.instance_id || '-')
const selectedInstanceTarget = computed(() => {
  if (analysisData.instance?.target) return analysisData.instance.target
  if (analysisData.instance?.hostname && analysisData.instance?.port) {
    return `${analysisData.instance.hostname}:${analysisData.instance.port}`
  }
  return '-'
})
const analysisSummary = computed(() => analysisData.summary || {})
const instanceSummaryRows = computed(() => {
  const rows = [...instanceSummaryRawRows.value]
  if (!instanceSorter.field || !instanceSorter.order) {
    return rows
  }

  const factor = instanceSorter.order === 'ascend' ? 1 : -1
  return rows.sort((a, b) => {
    const left = Number(a?.[instanceSorter.field] || 0)
    const right = Number(b?.[instanceSorter.field] || 0)
    if (left === right) {
      return String(a?.remark || a?.instance_id || '').localeCompare(String(b?.remark || b?.instance_id || ''))
    }
    return (left - right) * factor
  })
})

const statsPagination = computed(() => ({
  current: statsPager.page,
  pageSize: statsPager.pageSize,
  total: statsPager.total,
  showSizeChanger: true,
  showTotal: (total) => `共 ${formatNumber(statsPager.rawTotal || 0)} 条慢日志，${formatNumber(total)} 条SQL语句`,
}))

const detailPagination = computed(() => ({
  current: detailPager.page,
  pageSize: detailPager.pageSize,
  total: detailPager.total,
  showSizeChanger: true,
  showTotal: (total) => `共 ${formatNumber(total)} 条`,
}))

const statsTableScroll = { x: 1440 }

// 趋势图图例开关：全部默认显示，点击图例切换对应指标显隐
const hiddenTrendSeries = reactive({ count: false, duration: false })
const toggleTrendSeries = (key) => {
  hiddenTrendSeries[key] = !hiddenTrendSeries[key]
}

// 趋势图时间范围框选缩放（类 Grafana）：拖动选区后仅在所选时间范围内渲染，
// 双击图表或点击「恢复完整时间范围」还原
const trendSvgRef = ref(null)
const trendZoomRange = ref(null)
const trendBrush = ref(null)
const trendDisplayItems = computed(() => {
  const all = analysisData.trend || []
  const range = trendZoomRange.value
  if (!range) return all
  return all.slice(range.start, range.end + 1)
})
const trendSeries = computed(() => buildTrendSeries(trendDisplayItems.value))
const trendBrushRect = computed(() => {
  if (!trendBrush.value) return null
  const { startX, endX } = trendBrush.value
  const left = Math.min(startX, endX)
  const width = Math.abs(endX - startX)
  if (width < 2) return null
  return { x: left, width }
})
const resetTrendZoom = () => {
  trendZoomRange.value = null
  trendBrush.value = null
}
watch(
  () => analysisData.trend,
  () => {
    // 数据源变化（切换实例/时间范围/刷新）后索引失效，还原完整范围
    trendZoomRange.value = null
    trendBrush.value = null
  },
)

const trendViewBoxXFromEvent = (event) => {
  const rect = trendSvgRef.value?.getBoundingClientRect()
  if (!rect || !rect.width) return null
  return ((event.clientX - rect.left) / rect.width) * trendSeries.value.width
}
const trendIndexFromViewBoxX = (viewBoxX) => {
  const series = trendSeries.value
  const items = trendDisplayItems.value
  if (!items.length) return 0
  const chartWidth = series.width - series.paddingLeft - series.paddingRight
  const ratio = (viewBoxX - series.paddingLeft) / chartWidth
  return Math.min(Math.max(Math.round(ratio * (items.length - 1)), 0), items.length - 1)
}
const handleTrendBrushStart = (event) => {
  if (event.button !== 0) return
  const viewBoxX = trendViewBoxXFromEvent(event)
  if (viewBoxX === null) return
  trendSvgRef.value?.setPointerCapture?.(event.pointerId)
  trendBrush.value = { startX: viewBoxX, endX: viewBoxX }
}
const handleTrendBrushMove = (event) => {
  if (!trendBrush.value) return
  const viewBoxX = trendViewBoxXFromEvent(event)
  if (viewBoxX === null) return
  trendBrush.value = { ...trendBrush.value, endX: viewBoxX }
}
const handleTrendBrushEnd = () => {
  const brush = trendBrush.value
  trendBrush.value = null
  if (!brush) return
  // 位移过小视为普通点击（保留悬停提示），不触发缩放
  if (Math.abs(brush.endX - brush.startX) < 8) return
  const items = trendDisplayItems.value
  if (items.length < 3) return
  const startIndex = trendIndexFromViewBoxX(Math.min(brush.startX, brush.endX))
  const endIndex = trendIndexFromViewBoxX(Math.max(brush.startX, brush.endX))
  if (endIndex - startIndex < 1) return
  // 已缩放状态下再次框选：在当前范围基础上继续下钻
  const base = trendZoomRange.value ? trendZoomRange.value.start : 0
  trendZoomRange.value = { start: base + startIndex, end: base + endIndex }
}


// 趋势图悬停提示：mouseenter 即时渲染（替代浏览器原生 <title> 的延迟提示）
const hoveredTrendIndex = ref(null)
const hoveredTrendPoint = computed(() => {
  if (hoveredTrendIndex.value === null) return null
  return trendSeries.value.points[hoveredTrendIndex.value] || null
})
const trendTooltipStyle = computed(() => {
  const point = hoveredTrendPoint.value
  if (!point) return {}
  const series = trendSeries.value
  // SVG 使用 preserveAspectRatio="none"，viewBox 坐标按百分比映射到实际渲染尺寸
  const left = Math.min(Math.max((point.x / series.width) * 100, 10), 90)
  // 气泡锚点跟随可见系列（被隐藏的系列不参与定位）
  let anchorY = point.countY
  if (hiddenTrendSeries.count && !hiddenTrendSeries.duration) {
    anchorY = point.durationY
  } else if (!hiddenTrendSeries.count && hiddenTrendSeries.duration) {
    anchorY = point.countY
  } else {
    anchorY = Math.min(point.countY, point.durationY)
  }
  const top = (anchorY / series.height) * 100
  return { left: `${left}%`, top: `${top}%` }
})
const clearTrendHover = (pointIndex) => {
  if (hoveredTrendIndex.value === pointIndex) {
    hoveredTrendIndex.value = null
  }
}

const loadInstanceSummary = async () => {
  // 取数前把「最近 N」窗口滑到当前时刻，否则窗口右端停在页面打开的那一刻
  syncRollingSummaryRange()
  summaryLoading.value = true
  try {
    const res = await getPerformanceSlowSQLInstancesApi({
      search: summaryFilters.search,
      score_level: summaryFilters.scoreLevel,
      status_filter: summaryFilters.status,
      start_at: summaryFilters.timeRange?.[0],
      end_at: summaryFilters.timeRange?.[1],
    })
    instanceSummaryRawRows.value = res?.data?.instances || []
    excludedHint.value = res?.data?.excluded_hint || ''
    // 诊断与列表并行拉取：列表先渲染，诊断到达后补上状态列文案
    await loadSlowLogDiagnosis()
  } catch (error) {
    message.error(error?.message || '加载实例列表失败')
  } finally {
    summaryLoading.value = false
  }
}

const handleInstanceTableChange = (_pagination, _filters, sorter) => {
  const currentSorter = Array.isArray(sorter) ? sorter[0] : sorter
  instanceSorter.field = currentSorter?.field || currentSorter?.columnKey || ''
  instanceSorter.order = currentSorter?.order || null
}

const buildDetailParams = () => {
  // 详情页三个请求共用，同样按当前时刻重算「最近 N」窗口
  syncRollingDetailRange()
  return {
    instance_id: selectedInstanceID.value,
    start_at: detailFilters.timeRange?.[0],
    end_at: detailFilters.timeRange?.[1],
  }
}

// 详情页三个请求（趋势 / 统计 / 明细）是并发发的，同一个根因会让三个请求同时失败。
// 用固定 key 让 antd 覆盖同一条提示，否则一次「查看详情」会连弹三条一模一样的错误。
const DETAIL_LOAD_ERROR_KEY = 'slow-sql-detail-load-error'
const notifyDetailLoadError = (error, fallback) => {
  message.error({ content: error?.message || fallback, key: DETAIL_LOAD_ERROR_KEY })
}

const loadAnalysis = async () => {
  if (!selectedInstanceID.value) return
  analysisLoading.value = true
  try {
    const res = await getPerformanceSlowSQLAnalysisApi(buildDetailParams())
    analysisData.instance = res?.data?.instance || {}
    analysisData.summary = res?.data?.summary || {}
    analysisData.trend = res?.data?.trend || []
  } catch (error) {
    notifyDetailLoadError(error, '加载慢SQL统计失败')
  } finally {
    analysisLoading.value = false
  }
}

const loadStatsRows = async () => {
  if (!selectedInstanceID.value) return
  statsLoading.value = true
  try {
    const res = await getPerformanceSlowSQLStatsApi({
      ...buildDetailParams(),
      page: statsPager.page,
      page_size: statsPager.pageSize,
      is_page: true,
      min_duration_ms: 0,
      instance_id: selectedInstanceID.value,
      sort_field: statsSorter.field,
      sort_order: statsSorter.order === 'ascend' ? 'asc' : 'desc',
    })
    statsRows.value = res?.data?.rows || []
    statsPager.total = Number(res?.data?.group_total || 0)
    statsPager.rawTotal = Number(res?.data?.raw_total || 0)
  } catch (error) {
    notifyDetailLoadError(error, '加载慢SQL统计失败')
  } finally {
    statsLoading.value = false
  }
}

const loadDetailRows = async () => {
  if (!selectedInstanceID.value) return
  detailLoading.value = true
  try {
    const res = await getPerformanceSlowSQLApi({
      ...buildDetailParams(),
      page: detailPager.page,
      page_size: detailPager.pageSize,
      is_page: true,
      min_duration_ms: 0,
      instance_id: selectedInstanceID.value,
      sort_field: detailSorter.field,
      sort_order: detailSorter.order === 'ascend' ? 'asc' : 'desc',
    })
    detailRows.value = res?.data || []
    detailPager.total = Number(res?.total || 0)
  } catch (error) {
    notifyDetailLoadError(error, '加载慢SQL明细失败')
  } finally {
    detailLoading.value = false
  }
}

// 导出：与列表同条件（实例/时间范围/搜索/排序），后端按 is_page=false 全量导出 CSV
const statsExporting = ref(false)
const detailExporting = ref(false)

const saveBlobFile = (blob, filename) => {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
}

// 导出确认：确认后才执行；取消/关闭不产生任何请求
const handleExportStats = () => {
  if (!selectedInstanceID.value) return
  Modal.confirm({
    title: '确认导出「慢SQL统计」？',
    class: 'export-confirm-modal',
    okText: '确认',
    cancelText: '取消',
    onOk: () => doExportStats(),
  })
}

const handleExportDetail = () => {
  if (!selectedInstanceID.value) return
  Modal.confirm({
    title: '确认导出「慢SQL明细」？',
    class: 'export-confirm-modal',
    okText: '确认',
    cancelText: '取消',
    onOk: () => doExportDetail(),
  })
}

const doExportStats = async () => {
  statsExporting.value = true
  try {
    const res = await exportPerformanceSlowSQLStatsApi({
      ...buildDetailParams(),
      is_page: false,
      min_duration_ms: 0,
      instance_id: selectedInstanceID.value,
      sort_field: statsSorter.field,
      sort_order: statsSorter.order === 'ascend' ? 'asc' : 'desc',
    })
    saveBlobFile(new Blob([res.data], { type: 'text/csv;charset=utf-8' }), `慢SQL统计_${dayjs().format('YYYYMMDD-HHmmss')}.csv`)
    message.success('导出成功')
  } catch (error) {
    message.error(error?.message || '导出失败')
  } finally {
    statsExporting.value = false
  }
}

const doExportDetail = async () => {
  detailExporting.value = true
  try {
    const res = await exportPerformanceSlowSQLApi({
      ...buildDetailParams(),
      is_page: false,
      min_duration_ms: 0,
      instance_id: selectedInstanceID.value,
      sort_field: detailSorter.field,
      sort_order: detailSorter.order === 'ascend' ? 'asc' : 'desc',
    })
    saveBlobFile(new Blob([res.data], { type: 'text/csv;charset=utf-8' }), `慢SQL明细_${dayjs().format('YYYYMMDD-HHmmss')}.csv`)
    message.success('导出成功')
  } catch (error) {
    message.error(error?.message || '导出失败')
  } finally {
    detailExporting.value = false
  }
}

const loadSelectedView = async () => {
  if (!selectedInstanceID.value) return
  await loadAnalysis()
  if (activeTab.value === 'stats') {
    await loadStatsRows()
    return
  }
  await loadDetailRows()
}

const reloadSelectedView = async () => {
  statsPager.page = 1
  detailPager.page = 1
  await loadSelectedView()
}

const openInstance = (record) => {
  router.replace({
    query: {
      ...route.query,
      instance_id: record.instance_id,
      tab: 'stats',
    },
  })
}

const backToList = () => {
  const query = { ...route.query }
  delete query.instance_id
  delete query.tab
  router.replace({ query })
}

const handleActiveTabChange = async (key) => {
  activeTab.value = key
  resetTableSorters()
  await router.replace({
    query: {
      ...route.query,
      instance_id: selectedInstanceID.value,
      tab: key,
    },
  })
  await loadSelectedView()
}

const handleStatsPageChange = (page, pageSize) => {
  statsPager.page = page || 1
  statsPager.pageSize = pageSize || 10
  loadStatsRows()
}

const handleStatsPageSizeChange = (_current, size) => {
  statsPager.page = 1
  statsPager.pageSize = size || 10
  loadStatsRows()
}

// 统计表排序（服务端）：点击表头触发 change；取消排序时重置回默认并重挂载表格刷新指示器
const applyStatsSort = (field, order) => {
  if (field === statsSorter.field && order === statsSorter.order) return
  statsSorter.field = field
  statsSorter.order = order
  statsPager.page = 1
  loadStatsRows()
}
const handleStatsSortChange = (_pagination, _filters, sorter) => {
  const current = Array.isArray(sorter) ? sorter[0] : sorter
  const field = current?.column?.key || current?.field
  if (field && current?.order) {
    applyStatsSort(field, current.order)
    return
  }
  if (statsSorter.field === 'latest_time' && statsSorter.order === 'descend') return
  statsSorter.field = 'latest_time'
  statsSorter.order = 'descend'
  statsTableKey.value++
  statsPager.page = 1
  loadStatsRows()
}

const handleDetailTableChange = (pager, _filters, sorter) => {
  const currentSorter = Array.isArray(sorter) ? sorter[0] : sorter
  const field = currentSorter?.column?.key || currentSorter?.field
  if (field && currentSorter?.order) {
    if (field === detailSorter.field && currentSorter.order === detailSorter.order) {
      // 仅翻页
    } else {
      detailSorter.field = field
      detailSorter.order = currentSorter.order
      detailPager.page = 1
      loadDetailRows()
      return
    }
  } else if (!currentSorter?.order && detailSorter.field !== 'created_at') {
    detailSorter.field = 'created_at'
    detailSorter.order = 'descend'
    detailTableKey.value++
    detailPager.page = 1
    loadDetailRows()
    return
  }
  detailPager.page = pager.current || 1
  detailPager.pageSize = pager.pageSize || 10
  loadDetailRows()
}

// 切换 tab：排序状态重置为默认并重挂载两张表，保持指示器与数据一致
const resetTableSorters = () => {
  statsSorter.field = 'latest_time'
  statsSorter.order = 'descend'
  detailSorter.field = 'created_at'
  detailSorter.order = 'descend'
  statsTableKey.value++
  detailTableKey.value++
}

const buildSQLDrawerParams = () => ({
  ...buildDetailParams(),
  page: sqlDrawer.page,
  page_size: sqlDrawer.pageSize,
  is_page: true,
  min_duration_ms: 0,
  instance_id: selectedInstanceID.value,
  schema_name: sqlDrawer.record.schema_name || sqlDrawer.record.schema,
  sql_digest: sqlDrawer.record.sql_digest,
})

const loadSQLDrawerRows = async () => {
  if (!selectedInstanceID.value || !sqlDrawer.record.sql_digest) return
  sqlDrawer.loading = true
  try {
    const res = await getPerformanceSlowSQLApi(buildSQLDrawerParams())
    sqlDrawer.rows = res?.data || []
    sqlDrawer.total = Number(res?.total || 0)
  } catch (error) {
    message.error(error?.message || '加载SQL语句明细失败')
  } finally {
    sqlDrawer.loading = false
  }
}

const handleSQLDrawerPageChange = (page, pageSize) => {
  sqlDrawer.page = page || 1
  sqlDrawer.pageSize = pageSize || 10
  loadSQLDrawerRows()
}

const handleSQLDrawerPageSizeChange = (_current, size) => {
  sqlDrawer.page = 1
  sqlDrawer.pageSize = size || 10
  loadSQLDrawerRows()
}

const openSQLDrawer = async (record, mode) => {
  sqlDrawer.record = { ...record }
  sqlDrawer.open = true

  if (mode === 'stats') {
    sqlDrawer.title = 'SQL语句明细'
    sqlDrawer.mode = 'fingerprint'
    sqlDrawer.page = 1
    sqlDrawer.pageSize = 10
    sqlDrawer.total = 0
    sqlDrawer.rows = []
    await loadSQLDrawerRows()
    return
  }

  sqlDrawer.title = '慢SQL明细详情'
  sqlDrawer.mode = 'single'
  sqlDrawer.loading = false
  sqlDrawer.rows = []
  sqlDrawer.total = 0
}

const getQuickRangeTimeRange = (value) => {
  const now = dayjs()
  switch (value) {
    case '5m':
      return [now.subtract(5, 'minute').format('YYYY-MM-DD HH:mm:ss'), now.format('YYYY-MM-DD HH:mm:ss')]
    case '1h':
      return [now.subtract(1, 'hour').format('YYYY-MM-DD HH:mm:ss'), now.format('YYYY-MM-DD HH:mm:ss')]
    case '12h':
      return [now.subtract(12, 'hour').format('YYYY-MM-DD HH:mm:ss'), now.format('YYYY-MM-DD HH:mm:ss')]
    case '1d':
      return [now.subtract(1, 'day').format('YYYY-MM-DD HH:mm:ss'), now.format('YYYY-MM-DD HH:mm:ss')]
    case '7d':
      return [now.subtract(7, 'day').format('YYYY-MM-DD HH:mm:ss'), now.format('YYYY-MM-DD HH:mm:ss')]
    default:
      return null
  }
}

const isRangeExceeded = (startAt, endAt) => {
  if (!startAt || !endAt) return false
  return Math.abs(dayjs(endAt).valueOf() - dayjs(startAt).valueOf()) > maxTimeRangeMs
}

// 快捷范围必须是**滚动窗口**：如果只在初始化时算一次区间，
// 「最近1天」就会随页面停留越来越旧——页面开着 20 分钟，窗口右端就停在
// 20 分钟前，此后新入库的慢SQL永远落在窗口外，表现为
// 「实例上的慢SQL一直在涨，列表里的数量一直不动」。
// 因此每次取数前按当前时刻重算；手动选了绝对区间的（快捷范围已清空）不受影响。
const syncRollingSummaryRange = () => {
  if (!selectedSummaryQuickRange.value) return
  const range = getQuickRangeTimeRange(selectedSummaryQuickRange.value)
  if (!range) return
  summaryFilters.timeRange = range
  lastValidSummaryTimeRange.value = [...range]
}

const syncRollingDetailRange = () => {
  if (!selectedQuickRange.value) return
  const range = getQuickRangeTimeRange(selectedQuickRange.value)
  if (!range) return
  detailFilters.timeRange = range
  lastValidTimeRange.value = [...range]
}

const disabledDetailDate = (current) => {
  if (!current || !timeRangeAnchor.value) return false
  return Math.abs(current.startOf('day').valueOf() - timeRangeAnchor.value.startOf('day').valueOf()) > maxTimeRangeMs
}

const handleQuickRangeChange = async (value) => {
  const timeRange = getQuickRangeTimeRange(value)
  if (!timeRange) return
  detailFilters.timeRange = timeRange
  lastValidTimeRange.value = [...timeRange]
  timeRangeAnchor.value = null
  await reloadSelectedView()
}

const handleSummaryQuickRangeChange = async (value) => {
  const timeRange = getQuickRangeTimeRange(value)
  if (!timeRange) return
  summaryFilters.timeRange = timeRange
  lastValidSummaryTimeRange.value = [...timeRange]
  summaryTimeRangeAnchor.value = null
  await loadInstanceSummary()
}

const disabledSummaryDate = (current) => {
  if (!current || !summaryTimeRangeAnchor.value) return false
  return Math.abs(current.startOf('day').valueOf() - summaryTimeRangeAnchor.value.startOf('day').valueOf()) > maxTimeRangeMs
}

const handleSummaryTimeRangeCalendarChange = (dates) => {
  const validDates = (dates || []).filter(Boolean)
  if (validDates.length === 1) {
    summaryTimeRangeAnchor.value = dayjs(validDates[0])
    return
  }
  summaryTimeRangeAnchor.value = null
}

const handleSummaryTimeRangeChange = async (_dates, dateStrings) => {
  const [startAt, endAt] = dateStrings || []
  if (!startAt || !endAt) {
    return
  }
  if (isRangeExceeded(startAt, endAt)) {
    message.warning(`时间跨度最多只能选择${maxTimeRangeDays}天`)
    summaryFilters.timeRange = [...lastValidSummaryTimeRange.value]
    summaryTimeRangeAnchor.value = null
    return
  }
  selectedSummaryQuickRange.value = undefined
  lastValidSummaryTimeRange.value = [startAt, endAt]
  summaryTimeRangeAnchor.value = null
  await loadInstanceSummary()
}

const handleTimeRangeCalendarChange = (dates) => {
  const validDates = (dates || []).filter(Boolean)
  if (validDates.length === 1) {
    timeRangeAnchor.value = dayjs(validDates[0])
    return
  }
  timeRangeAnchor.value = null
}

const handleTimeRangeChange = async (_dates, dateStrings) => {
  const [startAt, endAt] = dateStrings || []
  if (!startAt || !endAt) return
  if (isRangeExceeded(startAt, endAt)) {
    message.warning('时间跨度最多只能选择7天')
    detailFilters.timeRange = [...lastValidTimeRange.value]
    timeRangeAnchor.value = null
    return
  }
  selectedQuickRange.value = undefined
  lastValidTimeRange.value = [startAt, endAt]
  timeRangeAnchor.value = null
  await reloadSelectedView()
}

const scoreTone = (level) => {
  switch (level) {
    case 'excellent':
      return 'excellent'
    case 'good':
      return 'good'
    case 'medium':
      return 'medium'
    default:
      return 'poor'
  }
}

// ---------- 采集状态（要求 2：异常要给出「哪个库 + 什么原因」）----------
//
// 数据来源：GET /api/v1/performance/slowlog-diagnosis
// 后端把 capability_state（能力结论）与 failure_code（失败原因码）一起下发，
// 前端只负责翻译成人话，不在前端维护任何「状态 → 文案」的映射表——
// 映射表放前端的后果是：后端新增一种失败码，所有页面都得跟着发版。
const diagnosisRows = ref([])
const diagnosisLoading = ref(false)

const diagnosisByInstance = computed(() => {
  const map = new Map()
  for (const row of diagnosisRows.value) {
    map.set(String(row?.instance_id || ''), row)
  }
  return map
})

// 兜底文案：后端没给 cause 时用状态本身拼一句话，
// 保证状态列永远不是空白，也不会退化成一个无意义的「失败」。
const capabilityFallbackText = {
  DISABLED: '服务端未开启慢日志',
  ENABLED_TABLE: '慢日志仅记录到表，未落文件',
  PROBE_FAILED: '无法探测服务端慢日志配置',
  FILE_MISSING: '慢日志文件不可读',
  UNSUPPORTED: '当前引擎暂不支持慢 SQL 采集',
  UNKNOWN: '尚未探测',
}

// 采集状态 → 展示标识。取值与后端 slowlog_error_norm.go 的 collect_status 一致，
// 后端是唯一事实来源，这里只负责翻译：把「状态」和「原因」拼成一句话，
// 让用户不用点开就知道是"采不了"还是"没采成"。
const collectStatusMeta = {
  OK: { tone: 'ok', label: '正常', prefix: '' },
  FAILED: { tone: 'error', label: '采集失败', prefix: '采集失败' },
  NOT_READY: { tone: 'warning', label: '未采集', prefix: '未采集' },
}

const statusOf = (record) => {
  const status = String(record?.collect_status || '').trim()
  // 后端尚未发布统一错误口径时（字段缺失），退回诊断接口的老逻辑，
  // 保证新旧版本都能渲染，不会出现空白状态列。
  if (!status) {
    return legacyStatusOf(record)
  }

  const meta = collectStatusMeta[status] || collectStatusMeta.FAILED
  const message = String(record?.error_message || '').trim()
  const rawDetail = String(record?.error_detail || '').trim()
  const code = String(record?.error_code || '').trim()
  const diagnosis = diagnosisByInstance.value.get(String(record?.instance_id || ''))
  const advice = String(diagnosis?.advice || '').trim()

  if (status === 'OK') {
    // 采集消息里带「新增 N 条 / 跳过 M 条协议阶段记录」，是解释
    // 「日志 duration 涨了几百条、页面只多几条」的关键信息，必须露出来。
    const collectMessage = String(record?.last_collect_message || '').trim()
    const caliberNote = '统计口径：只计入执行完成记录（statement / execute）；扩展协议的 parse/bind 是同一查询的准备阶段，不计入慢SQL数量。'
    const source = String(record?.source || '').trim()
    const baseDetail = rawDetail || (source === 'V$SQL'
      ? '采集正常：数据源 V$SQL 定时采样'
      : '采集正常：数据源 慢日志文件（增量读取）')
    return {
      tone: 'ok',
      text: '正常',
      detail: [baseDetail, collectMessage, caliberNote].filter(Boolean).join('\n\n'),
    }
  }

  // 失败/未采集：主文案 = 「状态标识：原因」，tooltip 里再给原始错误与修复建议
  const head = message || meta.label
  return {
    tone: meta.tone,
    text: meta.prefix ? `${meta.prefix}：${head}` : head,
    detail: [code ? `错误码 ${code}` : '', message, rawDetail, advice].filter(Boolean).join('\n\n'),
  }
}

// legacyStatusOf 老口径（无统一错误字段时）：沿用诊断接口的状态与文案。
// 诊断接口不可用时退化成「正常」，避免拿不到诊断数据就把整列标红、制造假告警。
const legacyStatusOf = (record) => {
  const diagnosis = diagnosisByInstance.value.get(String(record?.instance_id || ''))
  if (!diagnosis) {
    // 诊断接口不可用（权限不足 / 网络异常）时退化为「正常」，
    // 避免因为拿不到诊断数据把整列标红、制造假告警。
    return { tone: 'ok', text: '正常', detail: '' }
  }

  const state = String(diagnosis.capability_state || '').trim()
  const collectStatus = String(diagnosis.collect_status || '').trim()
  const isError = state !== 'ENABLED_FILE' || collectStatus === 'ERROR'

  if (!isError) {
    const source = String(diagnosis.source || '').trim()
    const detail = source === 'V$SQL'
      ? `采集正常：数据源 V$SQL 定时采样`
      : `采集正常：数据源 慢日志文件（增量读取）`
    return { tone: 'ok', text: '正常', detail }
  }

  const rawType = String(diagnosis.db_type || record?.db_type || '').trim()
  const version = String(diagnosis.db_version || record?.db_version || '').trim()
  const dbLabel = `${rawType}${version ? ' ' + version : ''}`.trim() || '未知类型'
  const cause = String(diagnosis.cause || '').trim() || capabilityFallbackText[state] || '采集异常'
  const advice = String(diagnosis.advice || '').trim()
  const errorText = String(diagnosis.error_text || '').trim()

  return {
    tone: 'error',
    // 关键：把「失败的库类型」和「失败原因」一起给出，而不是只写一个「失败」
    text: `${dbLabel}：${cause}`,
    detail: [cause, errorText, advice].filter(Boolean).join('\n\n'),
  }
}

// 打开前提条件抽屉：传了实例则定位到它所属引擎并高亮它命中的前提项
const prerequisitesDrawer = reactive({
  open: false,
  family: 'mysql',
  highlightKeys: [],
})

const openPrerequisites = (record) => {
  const diagnosis = record
    ? diagnosisByInstance.value.get(String(record.instance_id || ''))
    : null
  prerequisitesDrawer.family = String(
    diagnosis?.db_family || (record ? record.db_type : '') || 'mysql'
  ).toLowerCase()
  prerequisitesDrawer.highlightKeys = Array.isArray(diagnosis?.prerequisite_keys)
    ? diagnosis.prerequisite_keys
    : []
  prerequisitesDrawer.open = true
}

const loadSlowLogDiagnosis = async () => {
  diagnosisLoading.value = true
  try {
    const res = await getSlowLogDiagnosisApi()
    diagnosisRows.value = res?.data?.instances || []
  } catch {
    // 诊断是增强信息，失败时静默降级为「正常」，不打扰主流程
    diagnosisRows.value = []
  } finally {
    diagnosisLoading.value = false
  }
}

const formatSeconds = (ms) => {
  const value = Number(ms || 0) / 1000
  if (!Number.isFinite(value) || value <= 0) return '0'
  return value >= 100 ? value.toFixed(1) : value.toFixed(value >= 10 ? 2 : 3).replace(/\.?0+$/, '')
}

const formatAvgMs = (record) => {
  const execCount = Number(record.exec_count || 0)
  const total = Number(record.total_duration_ms || 0)
  if (!execCount) return '0'
  return Math.round(total / execCount)
}

const formatPercent = (value) => {
  const num = Number(value || 0)
  if (!Number.isFinite(num) || num <= 0) return '0%'
  return `${num.toFixed(1).replace(/\.0$/, '')}%`
}

const formatAxisValue = (value) => {
  const num = Number(value || 0)
  if (!Number.isFinite(num) || num <= 0) return '0'
  if (num >= 1000) return Math.round(num).toLocaleString()
  if (num >= 100) return Math.round(num).toString()
  if (num >= 10) return num.toFixed(1).replace(/\.0$/, '')
  return num.toFixed(2).replace(/\.?0+$/, '')
}

const formatAxisInteger = (value) => {
  const num = Number(value || 0)
  if (!Number.isFinite(num) || num <= 0) return '0'
  return String(Math.round(num))
}

const buildNiceIntegerAxis = (maxValue, tickCount = 5) => {
  const safeMax = Math.max(Number(maxValue || 0), 1)
  const intervals = Math.max(tickCount - 1, 1)
  const rawStep = safeMax / intervals
  const magnitude = 10 ** Math.floor(Math.log10(rawStep))
  const normalized = rawStep / magnitude

  let step = magnitude
  if (normalized <= 1) {
    step = magnitude
  } else if (normalized <= 2) {
    step = 2 * magnitude
  } else if (normalized <= 5) {
    step = 5 * magnitude
  } else {
    step = 10 * magnitude
  }

  const axisMax = Math.max(step * intervals, Math.ceil(safeMax / step) * step)
  const ticks = Array.from({ length: tickCount }, (_, index) => {
    const value = step * (intervals - index)
    const ratio = value / axisMax
    return {
      ratio,
      value: formatAxisInteger(value),
    }
  })

  return {
    axisMax,
    ticks,
  }
}

const formatTrendTooltip = (point) => {
  const durationText = formatSeconds(point.avg_duration_ms || 0)
  return `${point.bucket_time_text}\n慢SQL数量: ${formatNumber(point.slow_sql_count || 0)}\n平均响应时间: ${durationText} 秒`
}

const buildTrendSeries = (items) => {
  const width = 1040
  const height = 300
  const paddingLeft = 84
  const paddingRight = 84
  const paddingTop = 32
  const paddingBottom = 42
  const points = Array.isArray(items) ? items : []
  if (!points.length) {
    return {
      width,
      height,
      paddingLeft,
      paddingRight,
      paddingTop,
      paddingBottom,
      countPolyline: '',
      durationPolyline: '',
      gridLines: [],
      leftTicks: [],
      rightTicks: [],
      points: [],
    }
  }
  const chartWidth = width - paddingLeft - paddingRight
  const chartHeight = height - paddingTop - paddingBottom
  const maxCount = Math.max(...points.map((item) => Number(item.slow_sql_count || 0)), 1)
  const durationValuesInSeconds = points.map((item) => Number(item.avg_duration_ms || 0) / 1000)
  const durationAxis = buildNiceIntegerAxis(Math.max(...durationValuesInSeconds, 0))
  const maxDurationSeconds = durationAxis.axisMax
  const xStep = points.length > 1 ? chartWidth / (points.length - 1) : 0
  const normalized = points.map((item, index) => {
    const x = paddingLeft + xStep * index
    const countY = paddingTop + chartHeight - (Number(item.slow_sql_count || 0) / maxCount) * chartHeight
    const durationSeconds = Number(item.avg_duration_ms || 0) / 1000
    const durationY = paddingTop + chartHeight - (durationSeconds / maxDurationSeconds) * chartHeight
    const hitStartX = points.length > 1 ? (index === 0 ? paddingLeft : x - xStep / 2) : paddingLeft
    const hitEndX = points.length > 1 ? (index === points.length - 1 ? width - paddingRight : x + xStep / 2) : width - paddingRight
    return {
      label: item.time_label,
      bucketTimeText: item.bucket_time_text || item.time_label,
      x,
      countY,
      durationY,
      hitStartX,
      hitWidth: Math.max(hitEndX - hitStartX, 0),
      tooltip: formatTrendTooltip(item),
    }
  })
  return {
    width,
    height,
    paddingLeft,
    paddingRight,
    paddingTop,
    paddingBottom,
    countPolyline: normalized.map((item) => `${item.x},${item.countY}`).join(' '),
    durationPolyline: normalized.map((item) => `${item.x},${item.durationY}`).join(' '),
    gridLines: [0, 0.25, 0.5, 0.75, 1].map((ratio) => paddingTop + chartHeight * ratio),
    leftTicks: [1, 0.75, 0.5, 0.25, 0].map((ratio) => ({
      y: paddingTop + chartHeight * (1 - ratio),
      value: formatAxisValue(maxCount * ratio),
    })),
    rightTicks: durationAxis.ticks.map((tick) => ({
      y: paddingTop + chartHeight * (1 - tick.ratio),
      value: tick.value,
    })),
    points: normalized,
  }
}

// 性能概览页趋势柱点击跳转过来时带 focus_date=YYYY-MM-DD：
// 列表与实例详情的时间筛选都锁定为该日全天，快捷范围清空（与手动改时间的行为一致）。
// 仅在进入页面 / query 变化时应用一次，之后用户可自由改筛选。
const applyFocusDateFromQuery = () => {
  const raw = String(route.query.focus_date || '')
  if (!/^\d{4}-\d{2}-\d{2}$/.test(raw)) return false
  const day = dayjs(raw)
  if (!day.isValid()) return false
  const startAt = day.startOf('day').format('YYYY-MM-DD HH:mm:ss')
  const endAt = day.endOf('day').format('YYYY-MM-DD HH:mm:ss')
  summaryFilters.timeRange = [startAt, endAt]
  detailFilters.timeRange = [startAt, endAt]
  lastValidSummaryTimeRange.value = [startAt, endAt]
  lastValidTimeRange.value = [startAt, endAt]
  selectedSummaryQuickRange.value = undefined
  selectedQuickRange.value = undefined
  return true
}

watch(
  () => [route.query.instance_id, route.query.tab, route.query.focus_date],
  async () => {
    activeTab.value = String(route.query.tab || 'stats')
    if (route.query.focus_date) {
      applyFocusDateFromQuery()
    }
    if (selectedInstanceID.value) {
      await loadSelectedView()
    }
  },
)

onMounted(async () => {
  activeTab.value = String(route.query.tab || 'stats')
  applyFocusDateFromQuery()
  await loadInstanceSummary()
  if (selectedInstanceID.value) {
    await loadSelectedView()
  }
})
</script>

<style scoped lang="scss">
.performance-page {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.performance-page--detail {
  height: auto;
  overflow: visible;
}

.search-input {
  width: 280px;
}

.filter-range {
  width: 360px;
}

.quick-range-select {
  width: 140px;
}

.page-title {
  /* 与性能概览页 .po-title 字号对齐（20px） */
  font-size: 20px;
  font-weight: 600;
  color: #1f2d38;
}

.performance-toolbar :deep(.ant-btn),
.performance-toolbar :deep(.ant-input-affix-wrapper),
.performance-toolbar :deep(.ant-picker),
.performance-toolbar :deep(.ant-select-selector) {
  height: 40px;
}

.performance-toolbar :deep(.ant-input-search-button) {
  height: 40px !important;
}

.detail-header :deep(.ant-btn),
.detail-header :deep(.ant-picker),
.detail-header :deep(.ant-select-selector) {
  height: 40px;
}

.detail-header :deep(.ant-btn) {
  display: inline-flex;
  align-items: center;
}

.detail-header :deep(.ant-picker) {
  display: inline-flex;
  align-items: center;
}

.detail-header :deep(.ant-select-selector) {
  align-items: center;
}

.instance-identity {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.instance-identity__name {
  color: #0f172a;
  font-size: 22px;
  font-weight: 600;
  line-height: 1.2;
  max-width: 100%;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.instance-identity__target {
  color: #94a3b8;
  font-size: 13px;
  line-height: 1.2;
  max-width: 100%;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.view-switch {
  display: inline-flex;
  gap: 8px;
}

.view-switch :deep(.ant-btn),
.performance-toolbar :deep(.ant-btn) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.instance-empty {
  padding: 48px 0;
}

.instance-empty__hint {
  margin-top: 6px;
  color: #9ca3af;
  font-size: 12px;
}

/* 统计口径提示：解释「日志行数 > 页面条数」，用户不需要再怀疑采集漏数 */
.caliber-hint {
  margin-top: 12px;
  padding: 0 4px;
  color: #94a3b8;
  font-size: 12px;
  line-height: 1.7;
}

/* 状态 tooltip 里有多段内容（采集消息 + 口径说明），需要保留换行 */
.slow-status-tooltip .ant-tooltip-inner {
  white-space: pre-line;
}

.instance-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 16px;
}

.instance-card {
  border-radius: 16px;
  cursor: pointer;
}

.instance-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.instance-card__identity {
  min-width: 0;
  overflow: hidden;
}

.instance-card__title {
  color: #0f172a;
  font-size: 16px;
  font-weight: 400;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.instance-card__subtitle {
  margin-top: 4px;
  color: #64748b;
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 卡片右上角的类型标签：改为品牌图标 + 文字，与列表列保持一致。
   图标由 DbTypeLogo 组件固定 20px，这里只负责让整块与标题基线对齐。 */
.instance-card__header .db-logo-wrap {
  flex: 0 0 auto;
}

.instance-card__body {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-top: 18px;
  padding-bottom: 16px;
  border-bottom: 1px solid #e5e7eb;
}

.score-ring {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 58px;
  height: 58px;
  border: 5px solid #cbd5e1;
  border-radius: 50%;
  color: #0f172a;
  font-size: 16px;
  font-weight: 400;
}

.score-ring--excellent {
  border-color: #059669;
  color: #059669;
}

.score-ring--good {
  border-color: #0f766e;
  color: #0f766e;
}

.score-ring--medium {
  border-color: #d97706;
  color: #d97706;
}

.score-ring--poor {
  border-color: #dc2626;
  color: #dc2626;
}

.score-text {
  color: #0f172a;
  font-size: 16px;
  font-weight: 400;
}

.instance-card__stats {
  display: grid;
  grid-template-columns: 20% repeat(3, minmax(0, 1fr));
  gap: 14px 12px;
  margin-top: 16px;
}

.stat-label {
  color: #94a3b8;
  font-size: 12px;
}

.stat-value {
  margin-top: 4px;
  color: #0f172a;
  font-size: 16px;
  font-weight: 400;
  word-break: break-word;
}

.instance-card__footer {
  margin-top: 18px;
  color: #64748b;
  text-align: right;
  font-size: 16px;
  font-weight: 400;
}

.cell-primary {
  color: #111827;
}

.cell-secondary {
  color: #9ca3af;
  font-size: 12px;
}

.view-detail-action {
  color: #0f766e;
}

.view-detail-action:hover,
.view-detail-action:focus {
  color: #115e59;
}

/* ---------- 数据库类型列（要求 1）----------
   图标由 DbTypeLogo 固定 20×20 并保持 contain，这里只让它在单元格里居中，
   同时限制最大宽度，避免过长版本号把列撑开。 */
.db-type-cell {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-width: 0;
}

.db-type-cell :deep(.db-logo-wrap) {
  min-width: 0;
}

/* ---------- 状态列（要求 2）----------
   正常只显示「正常」两个字；异常时显示「库类型：原因」，
   超过一行省略并用 tooltip 给出完整说明 + 修复建议。 */
.status-cell {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.status-cell__text {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

/* 「正常」跟随系统主题色：健康的实例是"应该的样子"，用主题色而不是
   语义绿，让异常色（红/琥珀）成为页面上唯一的信号色 */
.status-cell--ok .status-cell__text {
  color: var(--gi-color-primary, #52c41a);
}

.status-cell--error .status-cell__text {
  color: #ff4d4f;
}

/* 未采集（条件不满足）用琥珀色：它不是故障，但必须可见——
   和红色「采集失败」区分开，避免把"没配慢日志"当成"实例挂了"。 */
.status-cell--warning .status-cell__text {
  color: #fa8c16;
}

.status-cell__link {
  flex: 0 0 auto;
  padding-inline: 2px;
  font-size: 12px;
}

.instance-card__status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 12px;
  padding: 6px 10px;
  font-size: 13px;
  background: #f7fafb;
  border: 1px solid var(--gi-border, #d9e1e6);
  border-radius: 8px;
}

.instance-card__status > span {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

/* 卡片视图的「正常」与列表列同源：文字主题色 + 主题色 8% 淡化底，
   主题切换时两处一起变 */
.instance-card__status--ok {
  color: var(--gi-color-primary, #52c41a);
  background: color-mix(in srgb, var(--gi-color-primary, #52c41a) 8%, transparent);
}

.instance-card__status--error {
  color: #ff4d4f;
  background: rgba(255, 77, 79, 0.07);
  border-color: #ffccc7;
}

.instance-card__status--warning {
  color: #fa8c16;
  background: rgba(250, 140, 22, 0.08);
  border-color: #ffd591;
}

.detail-header {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.detail-header__left {
  display: flex;
  align-items: center;
  gap: 12px;
}

.detail-subtitle {
  margin-top: 4px;
  color: #64748b;
}

.summary-metrics {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
  margin-bottom: 12px;
}

.metric-card {
  padding: 18px 20px;
  border: 1px solid #e5e7eb;
  border-radius: 16px;
  background: #fff;
}

.metric-label {
  color: #94a3b8;
  font-size: 13px;
}

.metric-value {
  margin-top: 8px;
  color: #0f172a;
  font-size: 24px;
  font-weight: 700;
}

.analysis-section {
  margin-top: 16px;
}

.chart-section-card {
  flex: 0 0 auto;
}

/* 压缩图表区与下方 tab 栏之间的空白，使布局更紧凑 */
.chart-section-card :deep(.ant-card-body) {
  padding-bottom: 6px;
}

.chart-section-title {
  margin: 12px 0 6px;
  color: #0f172a;
  font-size: 15px;
  font-weight: 600;
  line-height: 1.4;
  text-align: center;
}

.detail-card {
  flex: 0 0 auto;
  overflow: visible;
}

.detail-card :deep(.ant-card-body) {
  padding-top: 8px;
}

.detail-card :deep(.ant-tabs-nav) {
  margin-bottom: 12px;
}

/* tab 行右侧「导出」按钮：与 tab 标题同行、垂直居中，图标风格与页头刷新按钮一致 */
.tabs-export-btn {
  display: inline-flex;
  align-items: center;
  height: 30px;
  margin-right: 8px;
}

.tabs-export-btn :deep(.anticon) {
  font-size: 13px;
  color: #475569;
}

.tabs-export-btn :deep(.anticon + span) {
  margin-inline-start: 5px;
}

.detail-pane {
  padding-right: 2px;
}

.stats-pagination-wrap {
  margin-top: 8px;
  display: flex;
  justify-content: flex-end;
  position: relative;
  z-index: 1;
}

/* 慢SQL统计表：表头文字单行显示，不换行 */
.stats-table :deep(.ant-table-thead > tr > th) {
  white-space: nowrap;
}

.trend-chart {
  position: relative;
  width: 100%;
}

.trend-chart svg {
  width: 100%;
  height: 300px;
  overflow: visible;
  cursor: crosshair;
}

.trend-brush {
  fill: rgba(37, 99, 235, 0.12);
  stroke: rgba(37, 99, 235, 0.45);
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
  pointer-events: none;
}

.trend-zoom-reset {
  position: absolute;
  top: -4px;
  left: 50%;
  transform: translateX(-50%);
  padding: 2px 10px;
  border: 1px solid #bfdbfe;
  border-radius: 4px;
  background: #eff6ff;
  color: #2563eb;
  font-size: 12px;
  line-height: 1.6;
  cursor: pointer;
  z-index: 21;
}

.trend-zoom-reset:hover {
  background: #dbeafe;
}

.trend-axis-title {
  position: absolute;
  top: 0;
  color: #475569;
  font-size: 12px;
  font-weight: 600;
  line-height: 1;
}

.trend-axis-title--left {
  left: 8px;
}

.trend-axis-title--right {
  right: 8px;
}

.trend-axis {
  stroke: #cbd5e1;
  stroke-width: 1;
}

.trend-grid {
  stroke: #e5e7eb;
  stroke-width: 1;
}

.trend-line {
  fill: none;
  stroke-width: 3;
}

.trend-line--count {
  stroke: #2563eb;
}

.trend-line--duration {
  stroke: #d97706;
}

.trend-point {
  stroke-width: 2;
  fill: #fff;
}

.trend-point--count {
  stroke: #2563eb;
}

.trend-point--duration {
  stroke: #d97706;
}

.trend-hit-area {
  fill: transparent;
  pointer-events: all;
}

.trend-tooltip {
  position: absolute;
  z-index: 20;
  transform: translate(-50%, calc(-100% - 12px));
  padding: 8px 12px;
  background: rgba(15, 23, 42, 0.92);
  color: #fff;
  font-size: 12px;
  line-height: 1.7;
  border-radius: 6px;
  white-space: pre-line;
  pointer-events: none;
  box-shadow: 0 4px 12px rgba(15, 23, 42, 0.24);
}

.trend-label {
  fill: #94a3b8;
  font-size: 11px;
}

.trend-y-label {
  fill: #475569;
  font-size: 12px;
  font-weight: 600;
}

.trend-legend {
  display: flex;
  justify-content: center;
  gap: 24px;
  margin-top: 8px;
  color: #64748b;
  font-size: 12px;
}

.trend-legend-item {
  cursor: pointer;
  user-select: none;
  transition: opacity 0.2s;
}

.trend-legend-item:hover {
  color: #334155;
}

.trend-legend-item--off {
  opacity: 0.38;
}

.trend-legend-item--off .legend-dot--count,
.trend-legend-item--off .legend-dot--duration {
  background: #cbd5e1;
}

.legend-dot {
  display: inline-block;
  width: 10px;
  height: 10px;
  margin-right: 6px;
  border-radius: 50%;
}

.legend-dot--count {
  background: #2563eb;
}

.legend-dot--duration {
  background: #d97706;
}

.sql-preview {
  color: #374151;
  word-break: break-all;
  white-space: normal;
}

.sql-link {
  cursor: pointer;
}

.sql-link:hover {
  color: #0f766e;
}

.diagnosis-pill {
  display: inline-flex;
  align-items: center;
  padding: 4px 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
}

.diagnosis-pill--danger {
  background: rgba(220, 38, 38, 0.12);
  color: #dc2626;
}

.diagnosis-pill--warn {
  background: rgba(217, 119, 6, 0.12);
  color: #d97706;
}

.diagnosis-pill--info {
  background: rgba(37, 99, 235, 0.12);
  color: #2563eb;
}

.diagnosis-pill--neutral {
  background: rgba(15, 118, 110, 0.12);
  color: #0f766e;
}

.drawer-table {
  margin-top: 0;
}

.drawer-pagination {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}

.detail-code {
  margin: 0;
  padding: 12px;
  overflow: auto;
  border-radius: 8px;
  background: #0f172a;
  color: #e5e7eb;
  white-space: pre-wrap;
  word-break: break-word;
}

.detail-code--drawer-row {
  padding: 10px;
  font-size: 12px;
  line-height: 1.6;
}

@media (max-width: 1200px) {
  .summary-metrics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}</style>

<style lang="scss">
/* 导出确认弹窗（teleport 到 body，需非 scoped）：仅标题无正文，收掉空 content 容器的空白 */
.ant-modal.export-confirm-modal .ant-modal-confirm-content {
  display: none;
}

.ant-modal.export-confirm-modal .ant-modal-confirm-title {
  font-size: 15px;
}

@media (max-width: 768px) {
  /* 时间范围 360px、搜索 280px 在 375 屏直接溢出 */
  .search-input,
  .filter-range,
  .quick-range-select {
    width: 100% !important;
    max-width: 100%;
  }

  .performance-toolbar :deep(.ant-space-item),
  .detail-header :deep(.ant-space-item) {
    flex: 1 1 100%;
    max-width: 100%;
  }

  /* auto-fill + 320px 下限在 375 下会溢出 */
  .instance-card-grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 12px;
  }

  .summary-metrics {
    grid-template-columns: minmax(0, 1fr);
    gap: 10px;
  }

  /* 工具控件统一到 44px 触控高度 */
  .performance-toolbar :deep(.ant-btn),
  .performance-toolbar :deep(.ant-input-affix-wrapper),
  .performance-toolbar :deep(.ant-picker),
  .performance-toolbar :deep(.ant-select-selector) {
    height: 44px;
  }

  .instance-identity__name,
  .instance-identity__target {
    white-space: normal;
    overflow: visible;
    text-overflow: clip;
    word-break: break-word;
  }
}
</style>
