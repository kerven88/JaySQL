<template>
  <div class="gi-page-shell alert-center-page" :class="{ 'is-fs': isFullscreen, 'tab-overview': activeTab === 'overview', 'tab-history': activeTab === 'history' }">
    <PageHeader title="告警中心">
      <template #right>
      <a-space wrap align="center" class="page-actions">
        <template v-if="isOverview">
          <span class="refresh-hint" :class="{ bad: overviewError }">{{ refreshHint }}</span>
          <!-- 纯图标控制区：勾选框悬停出轻量提示（需求 3） -->
          <a-tooltip title="勾选后每 30 秒自动刷新概览数据" placement="bottom">
            <a-checkbox v-model:checked="autoRefresh" aria-label="自动刷新概览" />
          </a-tooltip>
        </template>
        <a-tooltip title="刷新当前数据" placement="bottom">
          <a-button class="icon-btn" :loading="activeLoading" @click="reloadCurrent">
            <template #icon><SyncOutlined /></template>
          </a-button>
        </a-tooltip>
        <a-tooltip :title="isFullscreen ? '退出全屏' : '全屏显示'" placement="bottom">
          <a-button class="icon-btn fullscreen-btn" :aria-label="isFullscreen ? '退出全屏' : '全屏显示'" @click="toggleFullscreen">
            <template #icon>
              <FullscreenExitOutlined v-if="isFullscreen" />
              <FullscreenOutlined v-else />
            </template>
          </a-button>
        </a-tooltip>
      </a-space>
      </template>
    </PageHeader>

    <a-card class="section-card alert-center-card" :bordered="false">
      <a-tabs v-model:activeKey="activeTab" @change="onTabChange">
      <!-- 筛选栏上移与 tab 同行（仅历史 tab 展示）；空间不足时内部横向滚动，不挤压 tab -->
      <template #rightExtra>
        <div v-show="activeTab === 'history'" class="tab-toolbar">
          <div class="filter-toolbar-left">
            <a-space :wrap="false" :size="8">
              <a-input
                v-model:value="filters.keyword"
                allow-clear
                class="filter-id-input"
                placeholder="搜索告警ID / IP / 实例名称"
                @press-enter="onHistoryFilterChange"
                @change="onKeywordClear"
              />
              <a-select
                v-model:value="filters.status"
                allow-clear
                class="filter-select"
                placeholder="全部状态"
                :options="statusOptions"
                @change="onHistoryFilterChange"
              />
              <!-- 时间范围：预设下拉 + 常驻区间选择器（精确到秒、→ 分隔、日历后缀）。
                   切预设时选择器同步展示该档起止；手动改选择器即视为自定义区间
                   （跨度上限 15 天，超限在确认时拦截提示；show-time 确认按钮模式下
                   calendarChange 不触发，动态置灰锚点不可用，故只保留未来日期置灰） -->
              <a-select
                v-model:value="filters.rangeKey"
                class="filter-select"
                :options="rangeOptions"
                @change="onRangeKeyChange"
              />
              <a-range-picker
                v-model:value="filters.customRange"
                show-time
                value-format="YYYY-MM-DD HH:mm:ss"
                format="YYYY-MM-DD HH:mm:ss"
                :placeholder="['开始时间', '结束时间']"
                :disabled-date="disabledRangeDate"
                class="custom-range-picker"
                @change="onCustomRangeChange"
              />
              <a-button type="primary" :loading="historyLoading" @click="onHistoryFilterChange">查询</a-button>
            </a-space>
          </div>
        </div>
      </template>
        <!-- ============================ Tab1 告警概览 ============================ -->
        <a-tab-pane key="overview" tab="告警概览">
          <!-- cache-bust: 20260921-1452 -->
          <!-- 全屏态下这一层负责把高度分给「图表区撑满 + 明细面板封顶」 -->
          <div class="ov-fill" :class="{ 'detail-open': !topCollapsed }">
          <!-- 计数卡：口径见后端 summary / stats 注释，前端绝不现算百分比以外的数字 -->
          <div class="ov-cards">
            <OverviewCard
              label="总告警数"
              :value="ovTiles.total"
              tone="bad"
              @click="openDrill('unresolved')"
            />
            <OverviewCard
              label="未认领告警"
              :value="ovTiles.unclaimed"
              tone="warn"
              @click="openDrill('active')"
            />
            <OverviewCard
              label="处理中告警"
              :value="ovTiles.acknowledged"
              tone="warn"
              @click="openDrill('acknowledged')"
            />
            <OverviewCard
              label="今日已恢复告警"
              :value="ovTiles.todayRecovered"
              tone="good"
              @click="openDrill('recovered_today')"
            />
            <OverviewCard
              label="告警实例总数"
              :value="ovTiles.instances"
              tone="p2"
              @click="openInstanceDrill"
            />
          </div>

          <div class="ov-charts">
            <!-- 当前告警分布：当前未恢复。默认按 P0–P3 级别分组，可切换为按实例分组 -->
            <div class="ov-panel">
              <!-- 标题行：左标题 + 右分组维度下拉 -->
              <div class="ov-panel-titlebar">
                <div class="ov-panel-title">当前告警分布</div>
                <a-select
                  v-model:value="pieDimension"
                  class="pie-dim-select"
                  size="small"
                  :options="PIE_DIM_OPTIONS"
                  aria-label="分组维度"
                />
              </div>
              <div v-if="pieHasData" class="pie-wrap">
                <!-- 圆饼图：实心扇区 + 中心总数 + 右侧图例 -->
                <svg class="pie-svg" viewBox="0 0 240 240" role="img">
                  <g transform="translate(120, 120)">
                    <path
                      v-for="seg in pieSegments"
                      :key="seg.key"
                      class="pie-seg"
                      :d="seg.path"
                      :fill="seg.color"
                      @click="onPieLegendClick(seg)"
                    >
                      <title>{{ seg.label }}：{{ seg.value }}（{{ seg.pct }}）</title>
                    </path>
                  </g>
                  <!-- 中心总数：垫一层容器色圆底，保证数字在彩色扇区上仍然清晰。
                       原「未恢复」文案已移除，数字用 dominant-baseline 做严格垂直居中。
                       中心整体（圆底 + 数字）可点击 → 下钻到该数字对应的全部告警明细。
                       事件冲突处理：
                         1) 中心元素绘制在扇区 path 之后，天然位于上层，扇区不会抢走点击；
                         2) @click.stop 阻断冒泡，避免事件扩散到外层容器；
                         3) 圆底 fill 不透明，指针落在圆内任意位置均由中心捕获，
                            扇区的 :hover 高亮不会在中心区域触发。 -->
                  <g
                    class="pie-center-hit"
                    role="button"
                    tabindex="0"
                    :aria-label="`共 ${pieTotal} 条告警，点击查看全部明细`"
                    @click.stop="onPieCenterClick"
                    @keydown.enter.prevent="onPieCenterClick"
                    @keydown.space.prevent="onPieCenterClick"
                  >
                    <title>点击查看全部告警明细（共 {{ pieTotal }} 条）</title>
                    <circle class="pie-center-bg" cx="120" cy="120" r="38" />
                    <text class="pie-center-num" x="120" y="120" dominant-baseline="central">{{ pieTotal }}</text>
                  </g>
                </svg>
                <div class="pie-legend">
                  <!-- 「其他」分组的 tooltip 展示被合并实例的明细；其余分组沿用「点击下钻」提示 -->
                  <a-tooltip v-for="seg in pieLegend" :key="seg.key" placement="right">
                    <template #title>
                      <template v-if="seg.detail && seg.detail.length">
                        <div class="pie-tip-title">其他（共 {{ seg.value }} 条）</div>
                        <div v-for="d in seg.detail" :key="d.label" class="pie-tip-row">
                          <span>{{ d.label }}</span><b>{{ d.value }}</b>
                        </div>
                      </template>
                      <span v-else>点击查看 {{ seg.label }} 告警明细</span>
                    </template>
                    <div class="pie-legend-row pie-legend-row-link" @click="onPieLegendClick(seg)">
                      <span class="legend-dot" :style="{ background: seg.color }"></span>
                      <span class="legend-label">{{ seg.label }}</span>
                      <span class="legend-value">{{ seg.value }}</span>
                      <span class="legend-pct">{{ seg.pct }}</span>
                    </div>
                  </a-tooltip>
                </div>
              </div>
              <a-empty v-else class="chart-empty" description="当前没有告警" />
            </div>

            <!-- 近 N 天趋势：右上角时间搜索框（精度到日），默认最近 7 天、最长 30 天 -->
            <div class="ov-panel">
              <div class="ov-panel-titlebar">
                <div class="ov-panel-title">近 {{ trendDays }} 天告警数量趋势</div>
                <a-range-picker
                  v-model:value="trendRange"
                  class="trend-range"
                  size="small"
                  value-format="YYYY-MM-DD"
                  :allow-clear="false"
                  @change="onTrendRangeChange"
                />
              </div>
              <div v-if="trendHasData" ref="trendWrapRef" class="trend-wrap" @mouseleave="trendHover = -1">
                <svg class="trend-svg" :viewBox="trendViewBox" @mousemove="onTrendMove">
                  <line
                    v-for="g in trendChart.grid"
                    :key="'g' + g.v"
                    class="trend-grid"
                    :x1="trendChart.padL"
                    :y1="g.y"
                    :x2="trendChart.padL + trendChart.w"
                    :y2="g.y"
                  />
                  <text
                    v-for="g in trendChart.grid"
                    :key="'gt' + g.v"
                    class="trend-ylabel"
                    :x="trendChart.padL - 8"
                    :y="g.y + 4"
                  >
                    {{ g.v }}
                  </text>
                  <text
                    v-for="(p, i) in trendChart.points"
                    v-show="p.showLabel"
                    :key="'x' + i"
                    class="trend-xlabel"
                    :x="p.x"
                    :y="trendChart.padT + trendChart.h + 22"
                  >
                    {{ trend[i]?.date }}
                  </text>
                  <polyline class="trend-line" :points="trendChart.polyline" />
                  <circle
                    v-for="(p, i) in trendChart.points"
                    :key="'d' + i"
                    class="trend-dot"
                    :cx="p.x"
                    :cy="p.y"
                    :class="{ hot: i === trendHover }"
                  />
                  <g v-if="trendHover >= 0">
                    <line
                      class="trend-cross"
                      :x1="trendChart.points[trendHover].x"
                      :y1="trendChart.padT"
                      :x2="trendChart.points[trendHover].x"
                      :y2="trendChart.padT + trendChart.h"
                    />
                  </g>
                </svg>
                <div
                  v-if="trendHover >= 0"
                  class="trend-tip"
                  :style="trendTipStyle"
                >
                  <div class="trend-tip-date">{{ trend[trendHover]?.key }}</div>
                  <div class="trend-tip-row">
                    触发 <b>{{ trend[trendHover]?.total }}</b>
                  </div>
                  <div v-for="seg in trendTipRows" :key="seg.key" class="trend-tip-row">
                    <span class="legend-dot" :style="{ background: seg.color }"></span>
                    {{ seg.label }} {{ seg.value }}
                  </div>
                </div>
              </div>
              <a-empty v-else class="chart-empty" :description="`近 ${trendDays} 天没有触发过告警`" />
            </div>
          </div>

          <!-- 当前告警信息明细：默认展开，展示全部未恢复告警（触发时间倒序） -->
          <div class="ov-panel ov-top-panel" :class="{ collapsed: topCollapsed }">
            <div class="ov-top-head">
              <div class="ov-panel-title">当前告警信息明细</div>
              <a-button
                type="link"
                size="small"
                class="ov-top-toggle"
                @click="topCollapsed = !topCollapsed"
              >
                <span>{{ topCollapsed ? '展开' : '收起' }}</span>
                <DownOutlined v-if="topCollapsed" />
                <UpOutlined v-else />
              </a-button>
            </div>

            <div v-show="!topCollapsed" class="ov-top-body">
              <a-table
                :columns="topColumns"
                :data-source="topItems"
                row-key="row_key"
                :loading="overviewLoading"
                :pagination="false"
                table-layout="fixed"
              >
                <template #bodyCell="{ column, record }">
                  <template v-if="column.key === 'severity'">
                    <!-- 级别文字用默认文本色（不再按 P0-P3 着色），与其他列保持一致的阅读节奏 -->
                    <span class="level-text">{{ levelMeta(record.severity).label }}</span>
                  </template>
                  <template v-else-if="column.key === 'instance'">
                    <!-- 两行展示：第一行数据源名称（无链接色），第二行 ip:port（淡灰） -->
                    <a class="cell-instance" :title="instanceTitleOf(record)" @click="openInstance(record)">
                      <span class="cell-main">{{ instanceNameOf(record) }}</span>
                      <span class="cell-ip">{{ topIpOf(record) }}</span>
                    </a>
                  </template>
                  <template v-else-if="column.key === 'name'">
                    <span>{{ operatorTextInText(record.name) }}</span>
                  </template>
                  <template v-else-if="column.key === 'triggered_at'">
                    {{ record.triggered_at || '—' }}
                  </template>
                  <template v-else-if="column.key === 'actions'">
                    <a-button type="link" size="small" class="ov-detail-btn" @click="openInstance(record)">
                      查看详情
                    </a-button>
                  </template>
                </template>
              </a-table>
            </div>
          </div>

          <div v-if="!canManage" class="tab-hint">
            当前账号只有查看权限（monitor.view），认领 / 忽略等处置操作需要 monitor.manage 权限。
          </div>
          </div>
        </a-tab-pane>

        <!-- ============================ Tab2 告警历史 ============================ -->
        <a-tab-pane key="history" tab="告警历史">
          <a-table
            ref="historyTableRef"
            :columns="columns"
            :data-source="historyItems"
            row-key="row_key"
            :loading="historyLoading"
            table-layout="fixed"
            :scroll="{ x: 1548, y: historyTableScrollY }"
            :pagination="pagination"
            @change="onTableChange"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'alert_id'">
                <span class="alert-id-text" :title="record.alert_id || ''">
                  {{ record.alert_id || '—' }}
                </span>
                <a-tooltip title="复制告警ID">
                  <a-button
                    class="copy-id-btn"
                    type="text"
                    size="small"
                    @click="copyAlertId(record.alert_id)"
                  >
                    <template #icon><CopyOutlined /></template>
                  </a-button>
                </a-tooltip>
              </template>
              <template v-else-if="column.key === 'status'">
                <!-- 无框纯色文字区分状态；忽略信息第二行小字淡灰，悬浮看完整忽略人/时间 -->
                <div class="st-cell">
                  <span class="st-text" :class="`st-${record.status}`">{{ record.status_text || record.status }}</span>
                  <a-tooltip v-if="record.ignored" :title="ignoredTipOf(record)">
                    <div class="st-ignored-line">已忽略 · {{ ignoredBriefOf(record) }}</div>
                  </a-tooltip>
                </div>
              </template>
              <template v-else-if="column.key === 'instance'">
                <!-- 第一行数据源名称，第二行 ip:port（端口缺失时只显示 IP），均使用默认文本色 -->
                <a class="cell-instance" :title="instanceTitleOf(record)" @click="openInstance(record)">
                  <span class="cell-main">{{ instanceNameOf(record) }}</span>
                  <span class="cell-ip">{{ instanceIpOf(record) }}</span>
                </a>
              </template>
              <template v-else-if="column.key === 'name'">
                <span>{{ historyAlertNameOf(record) }}</span>
              </template>
              <template v-else-if="column.key === 'actions'">
                <a-button class="action-link" type="link" size="small" @click="openDetail(record)">详情</a-button>
              </template>
            </template>
          </a-table>
        </a-tab-pane>
      </a-tabs>
    </a-card>

    <!-- 告警详情（单条） -->
    <a-drawer
      :open="detail.open"
      :title="operatorTextInText(detail.record?.name) || '告警详情'"
      width="620"
      @close="detail.open = false"
    >
      <template v-if="detail.record">
        <a-descriptions :column="1" bordered size="small">
          <a-descriptions-item label="告警ID">
            <span class="alert-id-text">{{ detail.record.alert_id || '—' }}</span>
          </a-descriptions-item>
          <a-descriptions-item label="实例">{{ detail.record.instance }}</a-descriptions-item>
          <a-descriptions-item label="数据库类型">{{ detail.record.db_type || '—' }}</a-descriptions-item>
          <a-descriptions-item label="业务">{{ detail.record.business || '—' }}</a-descriptions-item>
          <a-descriptions-item label="告警名称">{{ operatorTextInText(detail.record.name) }}</a-descriptions-item>
          <a-descriptions-item label="指标">
            {{ detail.record.metric_name }}（{{ detail.record.metric_key }}）
          </a-descriptions-item>
          <a-descriptions-item label="级别">{{ levelMeta(detail.record.severity).label }}</a-descriptions-item>
          <a-descriptions-item label="状态">{{ detail.record.status_text }}</a-descriptions-item>
          <a-descriptions-item label="阈值 / 观测值 / 峰值">
            {{ fmtMetricNum(detail.record.threshold) || '—' }} / {{ fmtMetricNum(detail.record.observed_value) || '—' }} / {{ fmtMetricNum(detail.record.peak_value) || '—' }}
            {{ detail.record.unit || '' }}
          </a-descriptions-item>
          <a-descriptions-item label="累计命中">{{ detail.record.occurrences }} 次</a-descriptions-item>
          <a-descriptions-item label="触发时间">{{ detail.record.triggered_at || '—' }}</a-descriptions-item>
          <a-descriptions-item label="恢复时间">{{ detail.record.recovered_at || '—' }}</a-descriptions-item>
          <a-descriptions-item label="持续时长">{{ durationText(detail.record.duration_sec) }}</a-descriptions-item>
          <a-descriptions-item label="认领人">{{ detail.record.acked_by || '—' }}</a-descriptions-item>
          <a-descriptions-item label="忽略人">{{ detail.record.ignored_by || '—' }}</a-descriptions-item>
        </a-descriptions>

        <div class="detail-section">
          <div class="detail-title">告警内容</div>
          <div class="detail-body">{{ operatorTextInText(detail.record.message) || '—' }}</div>
        </div>
        <div class="detail-section">
          <div class="detail-title">处置建议</div>
          <div class="detail-body">{{ detail.record.advice || '—' }}</div>
        </div>
      </template>
    </a-drawer>

    <!-- 数字下钻弹窗：概览计数卡（未恢复/高危/今日已恢复）+ 级别分布图例（P0–P3）。
         与监控大屏「新增告警」共用 AlertDrillModal 组件，格式/交互完全一致 -->
    <AlertDrillModal
      v-model:open="drill.open"
      :title="drillTitle"
      :title-prefix="drillTitlePrefix"
      :params="drillParams"
      :selectable="drillSelectable"
      :can-manage="canManage"
      @view-detail="openDetail"
      @view-instance="openInstance"
      @acknowledged="onDrillAcknowledged"
    />

    <!-- 「告警实例总数」下钻弹窗：与监控大屏共用 InstanceDrillModal 组件
         （数据来自 /monitor/alerts/instances，与计数卡数字同一套过滤口径）；
         行内"查看该实例告警明细"复用下方告警下钻弹窗并按实例过滤 -->
    <InstanceDrillModal
      v-model:open="instDrillOpen"
      scope="alert"
      @view-detail="onInstViewDetail"
    />

    <!-- 实例视角（两个 Tab 共用） -->
    <InstanceAlertDrawer
      ref="instanceDrawerRef"
      v-model:open="instanceDrawer.open"
      :instance="instanceDrawer.record"
      :can-manage="canManage"
      @action="onInstanceAction"
      @detail="onInstanceDetail"
    />
  </div>
</template>

<script setup>
import dayjs from 'dayjs'
import { getMonitorAlertsApi, getMonitorAlertStatsApi, updateMonitorAlertActionApi } from '@/api/monitor'
import InstanceAlertDrawer from '../components/InstanceAlertDrawer.vue'
import InstanceDrillModal from '../components/InstanceDrillModal.vue'
import OverviewCard from '../components/OverviewCard.vue'
import PageHeader from '../components/PageHeader.vue'
import { ALERT_LEVEL_DEFS as LEVEL_DEFS, alertLevelMeta as levelMeta, fmtMetricNum, operatorTextInText } from '../utils'
import AlertDrillModal from '../components/AlertDrillModal.vue'
import { message } from 'ant-design-vue'
import { computed, inject, nextTick, onDeactivated, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  CopyOutlined,
  DownOutlined,
  FullscreenExitOutlined,
  FullscreenOutlined,
  SyncOutlined,
  UpOutlined,
} from '@ant-design/icons-vue'
import { useUserStore } from '@/store/user'

defineOptions({ name: 'MonitorAlertCenterView' })

const userStore = useUserStore()
// 处置（认领 / 忽略）需要 monitor.manage；查看只需要 monitor.view
const canManage = computed(() => (userStore.permissions || []).includes('monitor.manage'))

const route = useRoute()
const router = useRouter()

// ============================================================================
// 两个 Tab 的分工（严格按评审定稿）：
//   overview —— 态势统计：计数卡 + 级别分布 + 7 天趋势 + 未恢复 TOP5，自动刷新；
//   history  —— 逐条明细：按时间线分页检索，支持告警 ID 精确查询，不自动刷新。
// ============================================================================

// 级别是 P0–P3 四档。颜色约定：P0 深红 / P1 正红 / P2 橙 / P3 柔和紫——
// P3 不用绿色（绿色已被「已恢复」状态占用，同页撞色会干扰判读），
// 也不用 --gi-color-info 蓝（大扇区里过于突兀），紫色与整体暖色阶最协调。
// 展示口径：等级名直接用 P0–P3（不再显示「紧急/严重/警告/通知」）。
// 定义已收敛到 ../utils（ALERT_LEVEL_DEFS / alertLevelMeta / alertStatusColor）——
// 监控大屏下钻弹窗与本页共用同一份，改 utils 即全模块同步。
// （levelMeta / statusColor 由文件头 import 别名提供。）

// 实例列两行展示用的取值：
//   name —— 数据源名称（remark），缺失时退回 instance（ip:port），再兜底 —
//   ip   —— 主机 IP（hostname），缺失时退回 instance
//   title —— 完整文本挂 title，超长时鼠标悬停仍可看到全貌
const instanceNameOf = (record) => record?.remark || record?.instance || '—'
const instanceIpOf = (record) => {
  const host = record?.hostname || record?.instance || ''
  if (!host) return '—'
  const port = record?.port
  return port ? `${host}:${port}` : host
}
const instanceTitleOf = (record) => `${instanceNameOf(record)} ${instanceIpOf(record)}`

// 告警历史 Tab 列表展示用：移除名称里的 P0–P3 优先级标记。
//
// 标记来源（重要）：优先级标记不是前端拼的，而是接口返回的数据本身就有。
// 后端 backend/internal/monitor/services/runtime.go 生成告警名时用的是
//   fmt.Sprintf("%s %s %s %s %s", 类型, 指标名, 级别, 操作符, 阈值)
// 第三个字段即 level.Level（P1/P2…），已随规则名落库并在告警记录里原样返回，
// 例如「MySQL 复制 SQL 线程 P1 不等于 1」。因此只能在展示层过滤，不能改数据。
//
// 匹配三种形态：裸写 P1、方括号【P1】/[P1]、圆括号 (P1)；P0–P3 一视同仁。
// 标记连同其两侧空格一并去掉，避免出现「线程 不等于」这类残留空格，
// 得到目标效果「MySQL 复制 SQL 线程不等于 1」。
const LEVEL_TAG_RE = /\s*(?:[[【(（]\s*)?P[0-3](?:\s*[\]】)）])?\s*/g
const historyAlertNameOf = (record) => {
  const raw = operatorTextInText(record?.name || '')
  if (!raw || raw === '—') return '—'
  return raw.replace(LEVEL_TAG_RE, '').replace(/\s+/g, ' ').trim()
}

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

const activeTab = ref('overview')
const isOverview = computed(() => activeTab.value !== 'history')

const REFRESH_INTERVAL = 30000

// ---------- Tab1 告警概览 ----------
const overviewLoading = ref(false)
const overviewError = ref(false)
const overviewItems = ref([])
const ovSummary = ref({})
const ovStats = ref({ today_recovered: 0, trend: [] })
const lastRefreshedAt = ref('')
const autoRefresh = ref(true)

const ovTiles = computed(() => {
  const summary = ovSummary.value || {}
  return {
    // 总告警数 = 当前概览过滤条件下的告警总数（当前请求 unresolved=true，即未恢复告警总数）
    total: Number(summary.total || 0),
    // 未认领 = 未恢复且未认领（active + pending 观察期），由概览接口 summary.active 给出；
    // 下钻必须传 unclaimed=true 与之同口径（传 status=active 会漏掉观察期事件）
    unclaimed: Number(summary.active || 0),
    // 处理中 = status === 'acknowledged'，由概览接口 summary 直接给出
    acknowledged: Number(summary.acknowledged || 0),
    todayRecovered: Number(ovStats.value?.today_recovered || 0),
    instances: Number(summary.instances || 0),
  }
})

// ---------- 圆饼图分组维度 ----------
// 级别：P0–P3 全展示（沿用后端 summary 口径）；实例：只取告警数最多的前 3 个，
// 其余合并为「其他」。
const PIE_DIM_OPTIONS = [
  { label: '级别', value: 'level' },
  { label: '实例', value: 'instance' },
]
const pieDimension = ref('level')

const PIE_INSTANCE_TOP_N = 3
const PIE_OTHER_KEY = '__other__'
// 实例维度配色：主题蓝 / 健康绿 / 紫，「其他」用中性灰
const PIE_INSTANCE_PALETTE = ['#2457f5', '#0f9f6f', '#7b61ff']
const PIE_OTHER_COLOR = '#9aa8b8'

// 按实例聚合：直接用概览已加载的告警明细（page_size=200，unresolved=true）。
// 正常场景下告警数远少于 200 条；万一被分页截断（聚合值 < 总告警数），
// 差额并入「其他」，保证饼图总和与「总告警数」卡片数字严格一致。
const pieInstanceGroups = computed(() => {
  const map = new Map()
  ;(overviewItems.value || []).forEach((item) => {
    const id = String(item?.instance_id || '')
    const label = instanceNameOf(item)
    const key = id || label
    if (!map.has(key)) map.set(key, { key, id, label, value: 0, latestAt: 0 })
    const group = map.get(key)
    group.value += 1
    // 记录该实例最近一次告警时间，用于排序第三键
    const at = Date.parse(item?.triggered_at || '') || 0
    if (at > group.latestAt) group.latestAt = at
  })
  return [...map.values()]
})

// 稳定排序，保证「多个实例告警数相同」时结果不抖动：
//   1) 告警数量 DESC
//   2) 名称字典序 ASC（zh-CN）——数量相同时顺序恒定，与接口返回顺序无关
//   3) 最近一次告警时间 DESC——名称也相同时，最近活跃的排在前面
const sortPieGroups = (list) =>
  [...list].sort((a, b) => {
    if (b.value !== a.value) return b.value - a.value
    const byName = String(a.label).localeCompare(String(b.label), 'zh-CN')
    if (byName !== 0) return byName
    return (b.latestAt || 0) - (a.latestAt || 0)
  })

// 圆饼分组单一数据源：扇区与图例都从它派生，避免两处口径漂移。
const pieGroups = computed(() => {
  if (pieDimension.value !== 'instance') {
    const summary = ovSummary.value || {}
    return LEVEL_DEFS.map((def) => ({
      key: def.key,
      label: def.label,
      color: def.color,
      value: Number(summary[def.key.toLowerCase()] || 0),
    }))
  }
  const total = ovTiles.value.total
  const sorted = sortPieGroups(pieInstanceGroups.value).map((group, i) => ({
    ...group,
    color: PIE_INSTANCE_PALETTE[i % PIE_INSTANCE_PALETTE.length],
  }))
  // 实例数未超过 3 个：全部独立展示，不出现「其他」
  if (sorted.length <= PIE_INSTANCE_TOP_N) return sorted
  const top = sorted.slice(0, PIE_INSTANCE_TOP_N)
  const rest = sorted.slice(PIE_INSTANCE_TOP_N)
  const aggregated = sorted.reduce((sum, g) => sum + g.value, 0)
  const otherValue = rest.reduce((sum, g) => sum + g.value, 0) + Math.max(0, total - aggregated)
  if (otherValue <= 0) return top
  return [
    ...top,
    {
      key: PIE_OTHER_KEY,
      label: '其他',
      color: PIE_OTHER_COLOR,
      value: otherValue,
      // 「其他」明细：悬浮/点击时展示被合并的各实例、告警数及其 instance_id，
      // 点击某一行可下钻到该实例的告警列表。
      detail: rest.map((g) => ({ label: g.label, value: g.value, id: g.id })),
    },
  ]
})

// 饼图分母 = 各分组之和。级别维度等于 summary 的 P0–P3 之和（= 未恢复总数），
// 实例维度在截断兜底后同样等于未恢复总数。
const pieTotal = computed(() => pieGroups.value.reduce((sum, g) => sum + Number(g.value || 0), 0))

const pieHasData = computed(() => pieTotal.value > 0)

// 圆饼几何：半径 110，从 12 点钟方向顺时针展开；用 <path> 画实心扇区，
// 并保留 2px 的白色描边做扇区间隔，使各等级区分更清晰。
const PIE_R = 110

const makeSectorPath = (r, startAngle, endAngle) => {
  const sweep = endAngle - startAngle
  // 单一扇区占满圆时，常规 path 会退化，用两条半圆弧拼成完整圆
  if (sweep >= Math.PI * 2 - 0.001) {
    return `M ${r} 0 A ${r} ${r} 0 1 1 -${r} 0 A ${r} ${r} 0 1 1 ${r} 0 Z`
  }
  const x1 = r * Math.cos(startAngle)
  const y1 = r * Math.sin(startAngle)
  const x2 = r * Math.cos(endAngle)
  const y2 = r * Math.sin(endAngle)
  const largeArc = sweep > Math.PI ? 1 : 0
  return `M 0 0 L ${x1.toFixed(2)} ${y1.toFixed(2)} A ${r} ${r} 0 ${largeArc} 1 ${x2.toFixed(2)} ${y2.toFixed(2)} Z`
}

const pieSegments = computed(() => {
  const total = pieTotal.value
  if (total <= 0) return []
  let acc = 0
  return pieGroups.value
    // 数量为 0 的分组不画扇区（级别维度 P0 常为 0，图例仍保留该行）
    .filter((group) => Number(group.value || 0) > 0)
    .map((group) => {
      const value = Number(group.value || 0)
      const frac = value / total
      const startAngle = -Math.PI / 2 + acc * Math.PI * 2
      const endAngle = -Math.PI / 2 + (acc + frac) * Math.PI * 2
      const seg = {
        ...group,
        value,
        frac,
        pct: `${Math.round(frac * 100)}%`,
        path: makeSectorPath(PIE_R, startAngle, endAngle),
      }
      acc += frac
      return seg
    })
})

const pieLegend = computed(() => {
  const total = pieTotal.value
  return pieGroups.value.map((group) => {
    const value = Number(group.value || 0)
    return {
      ...group,
      value,
      pct: total > 0 ? `${Math.round((value / total) * 100)}%` : '0%',
    }
  })
})

// 图例 / 扇区点击下钻：
//   级别维度 → 按 severity 过滤（沿用原 openDrill('level', key)）
//   实例维度 → 按 instance_id 过滤告警；「其他」把被合并的多个 instance_id
//             用逗号拼接传给后端，下钻后只展示这些实例的告警，标题前缀为"其他实例"。
const onPieLegendClick = (seg) => {
  if (!seg) return
  if (pieDimension.value === 'instance') {
    if (seg.key === PIE_OTHER_KEY) {
      const ids = (seg.detail || []).map((d) => d.id).filter(Boolean)
      openDrill('unresolved', '', ids.join(','), '其他实例')
      return
    }
    openDrill('unresolved', '', String(seg.id || seg.key || ''), seg.label || '')
    return
  }
  openDrill('level', seg.key)
}

// 点击圆心总数：数字 = 当前分组下所有扇区之和（= 总告警数），
// 因此下钻到「全部告警」，不带级别/实例过滤，保证弹窗条数与中心数字一致。
// 与扇区点击互斥：扇区走 onPieLegendClick 带过滤条件，中心走本函数不带过滤条件。
const onPieCenterClick = () => {
  if (!pieHasData.value) return
  openDrill('unresolved', '', '', '')
}

const trendHasData = computed(() => (ovStats.value?.trend || []).some((p) => p.total > 0))

// 模板直接引用的日期轴序列（ovStats.trend 的别名，保持模板简洁）
const trend = computed(() => ovStats.value?.trend || [])

// ---------- 趋势时间窗（精度到日） ----------
// 默认最近 7 天；跨度最长 30 天，超出轻度提醒并按所选区间最后 30 天截取。
// 天数随选择联动传给后端 /monitor/alerts/stats?days=N（后端同样 clamp 1-30），
// 轮询刷新（loadOverview）也带同一 days，避免定时刷新把用户选的窗口重置回 7 天。
const TREND_MAX_DAYS = 30
const alertTrendDefaultDays = 7
const todayStr = dayjs().format('YYYY-MM-DD')
const trendRange = ref([dayjs(todayStr).subtract(6, 'day').format('YYYY-MM-DD'), todayStr])
const trendDays = computed(() => {
  const [s, e] = trendRange.value || []
  if (!s || !e) return alertTrendDefaultDays
  const span = dayjs(e).diff(dayjs(s), 'day') + 1
  return span > 0 ? span : alertTrendDefaultDays
})

// 只刷新趋势（含今日恢复数，同一接口顺带返回）；不动 overviewError/lastRefreshedAt
const reloadTrend = async () => {
  try {
    const res = await getMonitorAlertStatsApi({ days: trendDays.value })
    const data = res?.data || {}
    ovStats.value = {
      today_recovered: data.today_recovered ?? ovStats.value.today_recovered,
      trend: data.trend || [],
    }
  } catch (error) {
    if (!isCanceled(error)) message.error(error?.message || '告警趋势加载失败')
  }
}

const onTrendRangeChange = (dates) => {
  if (!Array.isArray(dates) || dates.length !== 2 || !dates[0] || !dates[1]) return
  const [s, e] = dates
  const span = dayjs(e).diff(dayjs(s), 'day') + 1
  if (span > TREND_MAX_DAYS) {
    // 轻度提醒 + 截取：保留用户选的结束日，向前取满 30 天
    message.warning(`告警趋势最多支持查询 ${TREND_MAX_DAYS} 天，已按所选区间最后 ${TREND_MAX_DAYS} 天展示`)
    trendRange.value = [dayjs(e).subtract(TREND_MAX_DAYS - 1, 'day').format('YYYY-MM-DD'), e]
    reloadTrend()
    return
  }
  reloadTrend()
}

// 趋势折线几何：按容器「实测」宽高动态计算。
// 全屏时图表区会被 flex 拉高，若沿用固定 560×232 的 viewBox，SVG 会按旧比例缩放，
// 出现上下留白或文字被拉伸，所以 viewBox 必须跟着容器走。
//
// 关键坑：非全屏下容器高度是由 SVG 的高度反推出来的（.trend-svg 只有 width:100%），
// 若把「实测高度」再写回 viewBox，会形成自锁回路——从全屏退回普通窗口时高度收不回来。
// 因此非全屏高度一律由「宽度 × 原始比例」推导（与改造前的渲染完全一致），
// 只有全屏态才采用实测高度（此时高度由 flex 决定，与 SVG 无关）。
const TREND_BASE = { w: 560, h: 232 }
const TREND_RATIO = TREND_BASE.h / TREND_BASE.w
const trendBox = ref({ ...TREND_BASE })

const trendViewBox = computed(() => `0 0 ${trendBox.value.w} ${trendBox.value.h}`)

const applyTrendSize = (width, height) => {
  const w = Math.max(320, Math.round(width))
  const h = isFullscreen.value
    ? Math.max(140, Math.round(height))
    : Math.max(140, Math.round(w * TREND_RATIO))
  if (w !== trendBox.value.w || h !== trendBox.value.h) trendBox.value = { w, h }
}

const trendChart = computed(() => {
  const trend = ovStats.value?.trend || []
  if (!trend.length) return null
  const padL = 42
  const padR = 16
  const padT = 16
  const padB = 30
  const boxW = trendBox.value.w
  const boxH = trendBox.value.h
  const w = Math.max(80, boxW - padL - padR)
  const h = Math.max(40, boxH - padT - padB)
  const rawMax = Math.max(...trend.map((p) => p.total), 0)
  const yMax = Math.max(5, Math.ceil((rawMax * 1.15) / 5) * 5)
  const x = (i) => (trend.length === 1 ? padL + w / 2 : padL + (w * i) / (trend.length - 1))
  const y = (v) => padT + h - (v / yMax) * h
  // X 轴日期标签抽稀：窗口 ≤14 天全部显示；更长窗口按「约 10 个标签」取步长，
  // 末点强制显示（保证用户知道趋势截止到哪天），避免 30 天时标签互相叠字
  const xStep = trend.length <= 14 ? 1 : Math.ceil(trend.length / 10)
  const points = trend.map((p, i) => ({
    x: x(i),
    y: y(p.total),
    showLabel: i % xStep === 0 || i === trend.length - 1,
  }))
  const gridSteps = 4
  const grid = Array.from({ length: gridSteps + 1 }, (_, i) => {
    const v = Math.round((yMax * i) / gridSteps)
    return { v, y: y(v) }
  })
  return {
    padL,
    padT,
    w,
    h,
    grid,
    points,
    polyline: points.map((p) => `${p.x},${p.y}`).join(' '),
  }
})

// 趋势图容器尺寸监听：窗口缩放 / 全屏进出 / 数据到达都会重算几何
let trendObserver = null
const stopTrendObserver = () => {
  trendObserver?.disconnect()
  trendObserver = null
}
const syncTrendSize = () => {
  const el = trendWrapRef.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  if (!rect.width) return
  applyTrendSize(rect.width, rect.height)
}
const bindTrendObserver = () => {
  const el = trendWrapRef.value
  if (!el || typeof ResizeObserver === 'undefined') return
  stopTrendObserver()
  trendObserver = new ResizeObserver((entries) => {
    const rect = entries[0]?.contentRect
    if (!rect || !rect.width) return
    applyTrendSize(rect.width, rect.height)
  })
  trendObserver.observe(el)
}

const trendHover = ref(-1)
const trendWrapRef = ref(null)

const onTrendMove = (evt) => {
  const chart = trendChart.value
  if (!chart) return
  const rect = trendWrapRef.value?.getBoundingClientRect()
  if (!rect || !rect.width) return
  const mx = ((evt.clientX - rect.left) / rect.width) * trendBox.value.w
  const n = chart.points.length
  let idx = 0
  let best = Infinity
  chart.points.forEach((p, i) => {
    const d = Math.abs(p.x - mx)
    if (d < best) {
      best = d
      idx = i
    }
  })
  trendHover.value = idx >= 0 && idx < n ? idx : -1
}

const trendTipStyle = computed(() => {
  if (trendHover.value < 0 || !trendChart.value) return {}
  const p = trendChart.value.points[trendHover.value]
  // 用百分比定位，跟随 viewBox 缩放；靠右时翻到左边避免出界
  const leftPct = (p.x / trendBox.value.w) * 100
  const flip = leftPct > 70
  return {
    left: `${leftPct}%`,
    top: `${(p.y / trendBox.value.h) * 100}%`,
    transform: flip ? 'translate(calc(-100% - 10px), -50%)' : 'translate(10px, -50%)',
  }
})

const trendTipRows = computed(() => {
  const point = (ovStats.value?.trend || [])[trendHover.value]
  if (!point) return []
  return LEVEL_DEFS.map((def) => ({
    ...def,
    value: Number(point[def.key.toLowerCase()] || 0),
  }))
})

// 底部「当前告警信息明细」面板：默认展开；展示全部未恢复告警，
// 按触发时间倒序（后发生的排在前面），支持表头升降序（实例 / 级别 / 告警类型）。
// 原「全部实例 / 全部级别」下拉筛选已按需求移除，这里不再保留筛选状态。
const topCollapsed = ref(false)

// 级别排序权重：未知级别排在最后，避免脏数据跑到最前面
const severityRank = (s) => {
  const i = LEVEL_DEFS.findIndex((d) => d.key === String(s || '').toUpperCase())
  return i < 0 ? LEVEL_DEFS.length : i
}

// 实例列 IP 展示：优先 hostname:port，缺失时退回 instance（本表专用）
const topIpOf = (record) => {
  const host = record?.hostname
  const port = record?.port
  if (host && port) return `${host}:${port}`
  return record?.instance || host || '—'
}

const topItems = computed(() =>
  [...overviewItems.value].sort((a, b) => String(b.triggered_at || '').localeCompare(String(a.triggered_at || ''))),
)

// 列宽固定比例分配（配合 table-layout="fixed" 精确生效）：
//   数据源 20%（第一列）/ 告警级别 10%（第二列）/ 告警内容 25%
//   触发时间与操作未指定宽度，均分剩余 45%（各 22.5%），Σ = 100%
//   操作列表头与表体统一居中（align:center）；触发时间默认倒序
const topColumns = [
  {
    title: '数据源',
    key: 'instance',
    width: '20%',
    align: 'left',
    sorter: (a, b) => instanceNameOf(a).localeCompare(instanceNameOf(b)),
  },
  {
    title: '告警级别',
    key: 'severity',
    width: '10%',
    align: 'left',
    sorter: (a, b) => severityRank(a.severity) - severityRank(b.severity),
  },
  {
    title: '告警内容',
    key: 'name',
    width: '25%',
    align: 'left',
    // 表头不折叠：始终单行显示
    customHeaderCell: () => ({ class: 'col-alert-name' }),
    sorter: (a, b) => String(a.name || '').localeCompare(String(b.name || '')),
  },
  {
    title: '触发时间',
    key: 'triggered_at',
    width: '22.5%',
    align: 'left',
    defaultSortOrder: 'descend',
    sorter: (a, b) => String(a.triggered_at || '').localeCompare(String(b.triggered_at || '')),
  },
  { title: '操作', key: 'actions', width: '22.5%', align: 'center' },
]


const refreshHint = computed(() => {
  if (!lastRefreshedAt.value) return '等待首次刷新'
  return overviewError.value ? `刷新失败 ${lastRefreshedAt.value}` : `${lastRefreshedAt.value} 已更新`
})

// ---------- Tab2 告警历史 ----------
const historyLoading = ref(false)
const historyItems = ref([])
const historyTotal = ref(0)
const page = reactive({ current: 1, pageSize: 10 })

const filters = reactive({
  keyword: '',
  status: undefined,
  // 时间范围键：预设为小时数（字符串），'custom' = 自定义区间（customRange）
  rangeKey: '168',
  // 区间选择器常驻展示值 ["YYYY-MM-DD HH:mm:ss", "YYYY-MM-DD HH:mm:ss"]：
  // 预设档显示该档计算出的起止，手动改动即成为自定义区间
  customRange: null,
})

// 状态筛选：active / pending / acknowledged / recovered 四个真实状态互斥单选
// （每个事件只落一个桶），外加一个聚合项「未恢复」= 所有进行中状态
// （unresolved，后端 status <> 'recovered'）。
// 文案与状态列的渲染词严格一致（待认领/观察中/处理中/已恢复），用户在筛选项与
// 结果列之间零翻译成本；「活跃」已全站统一为「待认领」（2026-09-24 由「待处理」更名）。
// 口径差异须注意：历史是审计视角，「未恢复」不排除已忽略（能查到"未恢复但已忽略"
// 的事实），概览「总告警数」卡则排除已忽略 —— 前者查"发生过什么"，后者算"待办"。
const statusOptions = [
  { label: '未恢复', value: 'unresolved' },
  { label: '待认领', value: 'active' },
  { label: '观察中', value: 'pending' },
  { label: '处理中', value: 'acknowledged' },
  { label: '已恢复', value: 'recovered' },
]
// 自定义区间的跨度上限：按自然日计 15 天。即「9月1日 ~ 9月16日」（16 个自然日）
// 不允许，「9月1日 ~ 9月15日」允许。刻意用日界（startOf day）比较而非毫秒差，
// 避免边界随时刻漂移。
const CUSTOM_RANGE_MAX_DAYS = 15

// 预设档的精确起止（"YYYY-MM-DD HH:mm:ss"）：end=当前时刻、start=往前推 N 小时，
// 与区间选择器常驻展示的内容保持一致（例：最近1天 → 昨天 11:58:20 ~ 今天 11:58:20）
const presetRangeOf = (hours) => {
  const end = dayjs()
  return [end.subtract(Number(hours), 'hour').format('YYYY-MM-DD HH:mm:ss'), end.format('YYYY-MM-DD HH:mm:ss')]
}

// 时间下拉选项（与设计稿一致：纯文案、不带日期后缀，区间细节由常驻选择器展示）。
// 「最近 15 天」已按要求删除；自定义区间直接在选择器里改，无需占位档。
const rangeOptions = [
  { label: '最近1天', value: '24' },
  { label: '最近3天', value: '72' },
  { label: '最近7天', value: '168' },
  { label: '自定义时间', value: 'custom' },
]

const disabledRangeDate = (current) => {
  if (!current) return false
  // 只拦截未来日期；起点锚定的 15 天动态置灰在 show-time 确认按钮模式下不可用
  // （calendarChange 不触发），跨度校验由 onCustomRangeChange 在确认时兜底
  return current.startOf('day').valueOf() > Date.now()
}

const onCustomRangeChange = (value) => {
  if (!value || value.length !== 2) {
    // 清空选择器：恢复当前档的默认展示，不触发查询（避免退化成"全部"）
    filters.customRange = filters.rangeKey === 'custom' ? presetRangeOf(168) : presetRangeOf(Number(filters.rangeKey) || 168)
    return
  }
  // 手动改选择器 = 自定义区间，下拉同步落到「自定义时间」档
  filters.rangeKey = 'custom'
  const [start, end] = value
  // 确认时按自然日兜底校验（面板置灰在 show-time 下不可用，这里是唯一防线）
  if (dayjs(end).startOf('day').diff(dayjs(start).startOf('day'), 'day') >= CUSTOM_RANGE_MAX_DAYS) {
    filters.customRange = presetRangeOf(168)
    filters.rangeKey = '168'
    message.warning('自定义时间跨度不能超过 15 天（例如 9月1日 至 9月16日），请缩小时间范围')
    return
  }
  onHistoryFilterChange()
}

// 切换时间档：预设档同步刷新选择器展示并立即查询；
// 选「自定义时间」时沿用选择器当前区间（等价于当前窗口）直接查询。
const onRangeKeyChange = () => {
  if (filters.rangeKey !== 'custom') {
    filters.customRange = presetRangeOf(Number(filters.rangeKey))
  }
  if (!(Array.isArray(filters.customRange) && filters.customRange.length === 2)) return
  onHistoryFilterChange()
}

const columns = [
  { title: '告警ID', dataIndex: 'alert_id', key: 'alert_id', width: 158 },
  // 实例列改为两行（数据源名称 + IP），宽度减半：200 → 100
  { title: '实例名称', key: 'instance', width: 100 },
  // 告警名称表头不折叠（customHeaderCell 挂 .col-alert-name → white-space: nowrap）
  { title: '告警内容', key: 'name', width: 260, customHeaderCell: () => ({ class: 'col-alert-name' }) },
  { title: '触发时间', dataIndex: 'triggered_at', key: 'triggered_at', width: 165 },
  { title: '恢复时间', dataIndex: 'recovered_at', key: 'recovered_at', width: 165 },
  // 状态：宽度 150 → 98（减 35%），与操作列一起右固定
  { title: '状态', key: 'status', width: 98, fixed: 'right' },
  { title: '操作', key: 'actions', width: 90, fixed: 'right' },
]

const pagination = computed(() => ({
  current: page.current,
  pageSize: page.pageSize,
  total: historyTotal.value,
  showSizeChanger: true,
  showTotal: () => `共 ${Number(historyTotal.value).toLocaleString()} 条告警记录`,
}))

// 历史表格的 tbody 区内部滚动（表头固定）。y 值按「视口高度 - 表格顶部 - 底部预留」
// 实测计算，随窗口尺寸 / 全屏切换 / Tab 切换 / 数据加载后重算。
// 目的：页面整体不产生滚动条，滚动只发生在表格内容区内，分页器贴近页面底部。
const historyTableRef = ref(null)
// 初始值给一个常规可用高度，避免首帧（量算前）表格塌陷；挂载后由实测值覆盖
const historyTableScrollY = ref(420)
const updateHistoryTableScrollY = () => {
  const el = historyTableRef.value?.$el
  if (!el || !el.isConnected) return
  // 先把祖先滚动容器回顶：带着滚动位置量算，getBoundingClientRect 的视口坐标
  // 比真实文档位置偏高/偏低，直接造成「分页下方大片空白」或页面滚动条。
  let scroller = el.parentElement
  while (scroller && scroller !== document.body) {
    const oy = getComputedStyle(scroller).overflowY
    if ((oy === 'auto' || oy === 'scroll') && scroller.scrollHeight > scroller.clientHeight) {
      scroller.scrollTop = 0
      break
    }
    scroller = scroller.parentElement
  }
  // 回顶后视口坐标即文档坐标。表格顶部只取决于上方的工具栏（固定高度），
  // 与行数无关——数据是否加载完不影响量算结果，天然规避竞态。
  const top = el.getBoundingClientRect().top
  // 底部预留 = 内容区留白(24) + 分页上边距(16) + 分页器实测高度 + 表头实测高度；
  // 按实测算而非写死，主题密度 / 字号变化都不会把分页器顶出视口。
  const theadH = el.querySelector('.ant-table-thead')?.offsetHeight || 39
  const pagH = el.querySelector('.ant-pagination')?.offsetHeight || 40
  const reserve = 24 + 16 + pagH + theadH
  const y = Math.floor(window.innerHeight - top - reserve)
  historyTableScrollY.value = y > 200 ? y : 200
}

// statusColor（告警状态 → tag 颜色）已收敛到 ../utils，见文件头 import 别名。
// 状态列改为无框纯色文字后，忽略信息第二行展示：摘要用 MM-DD HH:mm，悬浮提示带完整时间。
const ignoredBriefOf = (record) => {
  if (!record.ignored_at) return record.ignored_by || ''
  const m = String(record.ignored_at).match(/^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})/)
  const time = m ? `${m[2]}-${m[3]} ${m[4]}:${m[5]}` : record.ignored_at
  return [record.ignored_by, time].filter(Boolean).join(' ')
}
const ignoredTipOf = (record) => `${record.ignored_by || '—'} 于 ${record.ignored_at || '—'} 忽略`

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

const nowText = () => {
  const now = new Date()
  const pad = (n) => String(n).padStart(2, '0')
  return `${pad(now.getHours())}:${pad(now.getMinutes())}:${pad(now.getSeconds())}`
}

// ---------- 数据加载 ----------
// 请求层在路由切换时会 abort 在途请求并抛 { canceled: true }。轮询页面里这是常态，
// 绝不能当成失败：既不弹错误提示，也不把概览标成"刷新失败"。
const isCanceled = (error) => Boolean(error && (error.canceled || error.code === 'ERR_CANCELED'))

const loadOverview = async ({ silent = false } = {}) => {
  if (!silent) overviewLoading.value = true
  try {
    const [listRes, statsRes] = await Promise.all([
      getMonitorAlertsApi({ page: 1, page_size: 200, unresolved: true }),
      // stats 带当前趋势窗口天数：自动轮询时保持用户选的时间范围，不被重置回默认 7 天
      getMonitorAlertStatsApi({ days: trendDays.value }),
    ])
    overviewItems.value = listRes?.data?.items || []
    ovSummary.value = listRes?.data?.summary || {}
    ovStats.value = statsRes?.data || { today_recovered: 0, trend: [] }
    overviewError.value = false
    lastRefreshedAt.value = nowText()
  } catch (error) {
    if (isCanceled(error)) return
    // 轮询失败保留旧数据，只标记"刷新失败"——白屏比过期数据更危险
    overviewError.value = true
    lastRefreshedAt.value = nowText()
    if (!silent) message.error(error?.message || '告警概览加载失败')
  } finally {
    overviewLoading.value = false
  }
}

const buildHistoryParams = () => {
  // sort_by=triggered_at：历史是流水账视图，默认按触发时间倒序（后发生在前），
  // 与概览/下钻的"状态优先"排序口径区分开（后端按该参数切换排序子句）。
  const params = { page: page.current, page_size: page.pageSize, sort_by: 'triggered_at' }
  if (filters.keyword.trim()) params.keyword = filters.keyword.trim()
  if (filters.status) params.status = filters.status
  if (filters.rangeKey === 'custom') {
    // 自定义区间：选择器值已是完整时间戳（YYYY-MM-DD HH:mm:ss），直接按触发时间过滤；
    // 边界展开为整日（00:00:00 / 23:59:59），保证所选自然日全天覆盖
    if (Array.isArray(filters.customRange) && filters.customRange.length === 2) {
      params.start_time = `${filters.customRange[0].slice(0, 10)} 00:00:00`
      params.end_time = `${filters.customRange[1].slice(0, 10)} 23:59:59`
    }
  } else {
    params.range_hours = Number(filters.rangeKey)
  }
  return params
}

const loadHistory = async () => {
  historyLoading.value = true
  try {
    const res = await getMonitorAlertsApi(buildHistoryParams())
    historyItems.value = res?.data?.items || []
    historyTotal.value = Number(res?.total || 0)
  } catch (error) {
    if (!isCanceled(error)) message.error(error?.message || '告警事件加载失败')
  } finally {
    historyLoading.value = false
    // 数据落定后重量算：表头/分页器的实际高度要等 DOM 稳定，避免首帧偏差留白
    nextTick(updateHistoryTableScrollY)
  }
}

const activeLoading = computed(() => (isOverview.value ? overviewLoading.value : historyLoading.value))
const reloadCurrent = () => (isOverview.value ? loadOverview() : loadHistory())

// ---------- 自动刷新（只给概览）----------
// 历史态绝不能自动刷新：用户正在看第 3 页，一次静默刷新就会把内容和分页顶走。
let refreshTimer = null
const stopTimer = () => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
}
const startTimer = () => {
  stopTimer()
  if (!autoRefresh.value) return
  refreshTimer = setInterval(() => {
    if (document.hidden || !isOverview.value) return
    loadOverview({ silent: true })
  }, REFRESH_INTERVAL)
}
watch(autoRefresh, () => startTimer())

// 从后台标签页切回来时立即补一次，避免盯着一屏过期数字值班
const onVisibilityChange = () => {
  if (!document.hidden && isOverview.value) loadOverview({ silent: true })
}

// ---------- 交互 ----------
const onHistoryFilterChange = () => {
  page.current = 1
  syncQuery()
  loadHistory()
}

const onKeywordClear = (evt) => {
  // antd 的 change 在清空时触发；仅在值被清空时重查，输入过程不打扰
  if (!evt?.target?.value) onHistoryFilterChange()
}

const onTabChange = (key) => {
  activeTab.value = key === 'history' ? 'history' : 'overview'
  syncQuery()
  if (isOverview.value) {
    loadOverview()
    startTimer()
    document.addEventListener('visibilitychange', onVisibilityChange)
  } else {
    stopTimer()
    document.removeEventListener('visibilitychange', onVisibilityChange)
    loadHistory()
  }
}

const onTableChange = (pag) => {
  page.current = pag.current || 1
  page.pageSize = pag.pageSize || 10
  syncQuery()
  loadHistory()
}

const act = async (record, action) => {
  const text = { ack: '认领', ignore: '忽略', unignore: '取消忽略' }[action] || '操作'
  try {
    await updateMonitorAlertActionApi({ fingerprint: record.fingerprint, action })
    message.success(`已${text}`)
    await reloadCurrent()
    instanceDrawerRef.value?.reload()
  } catch (error) {
    message.error(error?.message || `${text}失败`)
  }
}

const detail = reactive({ open: false, record: null })
const openDetail = (record) => {
  detail.record = record
  detail.open = true
}

const instanceDrawer = reactive({ open: false, record: null })
const instanceDrawerRef = ref(null)
const openInstance = (record) => {
  instanceDrawer.record = record
  instanceDrawer.open = true
}
const onInstanceDetail = (record) => openDetail(record)
const onInstanceAction = async ({ record, action }) => {
  await act(record, action)
  instanceDrawerRef.value?.reload()
}

// ---------- 数字下钻弹窗（概览计数卡 + 级别分布图例） ----------
// 口径与数字本身严格一致：
//   unresolved      → unresolved=true（未恢复 ＝ status <> recovered 且未忽略；
//                     概览/下钻是待办口径，故排除已忽略 —— 与历史筛选的
//                     status=unresolved 聚合项只差"是否排除已忽略"这一点）
//   active          → unclaimed=true（未认领 = pending 观察期 + active 已转正未认领。
//                     此前传 status=active 会被后端精确匹配收窄，观察期事件查不出来，
//                     与卡片数字（summary.active 含 pending）对不上：
//                     "未认领告警 3 条，点开 0 条"就是这么来的）
//   acknowledged    → status=acknowledged（处理中 = 已认领未恢复）
//   critical        → unresolved=true + severity=P0,P1（后端支持逗号多级别）
//   recovered_today → recovered_today=true（按恢复时间过滤，与 today_recovered 同口径）
//   level           → unresolved=true + severity=单级别
const drill = reactive({
  open: false,
  kind: 'unresolved',
  severity: '',
  // instanceId 非空时表示从「告警实例列表」下钻而来：列表按该实例过滤。
  instanceId: '',
  // instanceLabel 用于弹窗标题前缀（实例名 / "其他实例" / 级别 P1），空时不显示前缀。
  instanceLabel: '',
})

// 弹窗标题主体：计数卡/图例点击后，标题与对应口径保持一致
const drillTitle = computed(() => {
  if (drill.kind === 'active') return '未认领告警列表'
  return '总告警数列表'
})

// 标题前缀动态生成：
//   level 维度   → P0/P1/P2/P3
//   普通实例     → 实例名称（超过 15 字符在 AlertDrillModal 内截断加"..."）
//   其他聚合     → "其他实例"
//   未认领计数卡 → 空（直接显示"未认领告警列表"）
//   其他计数卡   → 空（直接显示"总告警数列表"）
const drillTitlePrefix = computed(() => {
  if (drill.kind === 'level') return drill.severity || ''
  if (drill.kind === 'unresolved' && drill.instanceId) return drill.instanceLabel || ''
  if (drill.kind === 'unresolved' && drill.instanceLabel === '其他实例') return '其他实例'
  return ''
})

const openDrill = (kind, severity = '', instanceId = '', instanceLabel = '') => {
  drill.kind = kind
  drill.severity = severity
  drill.instanceId = instanceId || ''
  drill.instanceLabel = instanceLabel || ''
  drill.open = true
}

// 查询参数（不含分页，分页由 AlertDrillModal 内部接管）。
const drillParams = computed(() => {
  const base = {}
  if (drill.instanceId) base.instance_id = drill.instanceId
  if (drill.kind === 'unresolved') return { ...base, unresolved: true }
  if (drill.kind === 'active') return { ...base, unclaimed: true }
  if (drill.kind === 'acknowledged') return { ...base, status: 'acknowledged' }
  if (drill.kind === 'critical') return { ...base, unresolved: true, severity: 'P0,P1' }
  if (drill.kind === 'recovered_today') return { ...base, recovered_today: true }
  return { ...base, unresolved: true, severity: drill.severity }
})

// 已恢复 / 处理中 列表没有批量认领语义，不出现勾选框；
// 勾选/批量确认仅对「未恢复 / 未认领 / 级别」列表开放（权限在组件内按 canManage 判断）。
const drillSelectable = computed(() => drill.kind !== 'recovered_today' && drill.kind !== 'acknowledged')

// 批量认领完成：弹窗列表由组件自行刷新，这里同步概览数字，避免"数字与明细对不上"
const onDrillAcknowledged = () => {
  if (isOverview.value) loadOverview({ silent: true })
}

// ---------- 「告警实例总数」下钻弹窗（共享组件 InstanceDrillModal） ----------
// 实例集合口径 alert：与概览计数卡同参数（unresolved=true），实例数与卡片数字严格同口径。
const instDrillOpen = ref(false)
const openInstanceDrill = () => {
  instDrillOpen.value = true
}
// 操作列：关闭实例弹窗，打开既有告警下钻弹窗并按实例过滤。
const onInstViewDetail = (record) => {
  instDrillOpen.value = false
  openDrill('unresolved', '', record.instance_id)
}

// ---------- URL 状态（可分享 / 可收藏 / 可前进后退）----------
// 用有序签名比对，避免"回写 URL → 触发 watch → 再加载一次"的重复请求。
const signature = (query) =>
  Object.keys(query)
    .sort()
    .map((key) => `${key}=${query[key]}`)
    .join('&')

let lastSynced = ''

const buildQuery = () => {
  const query = { ...route.query, tab: activeTab.value }
  const set = (key, value) => {
    if (value === undefined || value === null || value === '') delete query[key]
    else query[key] = value
  }
  // 级别/实例筛选已下线：无论当前 tab 是什么，都清掉旧深链残留参数
  delete query.severity
  delete query.instance
  if (isOverview.value) {
    delete query.status
    delete query.kw
    delete query.range
    delete query.page
  } else {
    set('kw', filters.keyword.trim() || '')
    set('status', filters.status)
    set('range', filters.rangeKey === '168' ? '' : filters.rangeKey)
    if (filters.rangeKey === 'custom' && Array.isArray(filters.customRange) && filters.customRange.length === 2) {
      set('start', filters.customRange[0])
      set('end', filters.customRange[1])
    } else {
      set('start', '')
      set('end', '')
    }
    set('page', page.current > 1 ? page.current : '')
  }
  return query
}

const syncQuery = () => {
  const query = buildQuery()
  const next = signature(query)
  if (next === lastSynced) return
  lastSynced = next
  router.replace({ query })
}

const applyQuery = () => {
  const query = route.query
  // 兼容旧深链 tab=active（上一版「当前告警」）→ 概览
  activeTab.value = query.tab === 'history' ? 'history' : 'overview'
  filters.keyword = query.kw ? String(query.kw) : ''
  filters.status = query.status ? String(query.status) : undefined
  // 时间档解析：预设为小时数字符串；旧深链的 720（30天）/ 0（全部）/ 360（最近15天，已删）
  // 统一降级到「最近7天」；custom 需同时恢复 start / end
  const legacyRangeMap = { 720: '168', 0: '168', 360: '168' }
  const rangeRaw = query.range !== undefined && query.range !== '' ? String(query.range) : '168'
  filters.rangeKey = legacyRangeMap[rangeRaw] ?? (['24', '72', '168', 'custom'].includes(rangeRaw) ? rangeRaw : '168')
  if (filters.rangeKey === 'custom' && query.start && query.end) {
    filters.customRange = [String(query.start), String(query.end)]
  } else {
    // 预设档：选择器常驻展示该档的精确起止
    filters.customRange = presetRangeOf(Number(filters.rangeKey))
  }
  page.current = query.page ? Math.max(1, Number(query.page)) : 1
  lastSynced = signature(buildQuery())
}

watch(
  () => signature(route.query),
  (value) => {
    if (value === lastSynced) return
    // 外部跳转（浏览器前进后退、从其它页面带参进入）：以 URL 为准重建状态
    applyQuery()
    if (isOverview.value) loadOverview()
    else loadHistory()
  }
)

// ---------- 全屏 ----------
// 与监控大屏同一套「双层全屏」实现：
//   1) 真·浏览器全屏（Fullscreen API，含 webkit 前缀兼容）：隐藏地址栏/标签栏；
//   2) 工作区全屏（Layout 的 workspaceFullscreen 机制）：隐藏侧栏/顶栏、内容区铺满。
// 只做 2) 是"页内放大"（截图反馈的问题）；只做 1) 则侧栏顶栏仍然占位。
// 两层叠加且原生请求失败（iframe 未授权、权限拒绝等）时降级为仅工作区铺满，按钮功能不失效。
// isFullscreen（图标态）= 原生全屏 || 工作区全屏 任一激活。
//
// 全屏时同时向公共布局申请「内容区不滚动」(setLayoutContentNoScroll)：
// 拿到 height:100% 后本页才能用 flex 把图表区撑满，否则页面高度仍是内容高度、
// 底部照样空。退出全屏/离开页面必须还原，避免影响其他页面。
const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)
const setWorkspaceFullscreen = inject('setWorkspaceFullscreen', null)
const isNativeFullscreen = ref(false)
const workspaceMode = ref(false)
const isFullscreen = computed(() => isNativeFullscreen.value || workspaceMode.value)
const fullscreenEl = () => document.fullscreenElement || document.webkitFullscreenElement || null

const requestNativeFullscreen = async () => {
  const el = document.documentElement
  const fn = el.requestFullscreen || el.webkitRequestFullscreen
  if (!fn) return false
  try {
    await fn.call(el)
    // 全屏成功与否以 fullscreenchange 事件为准；部分浏览器 await 后状态已就绪，这里兜底同步
    isNativeFullscreen.value = !!fullscreenEl()
    return isNativeFullscreen.value
  } catch {
    return false
  }
}

const exitFullscreen = async () => {
  if (fullscreenEl()) {
    const fn = document.exitFullscreen || document.webkitExitFullscreen
    try {
      await fn?.call(document)
    } catch {
      // 忽略退出异常，下方工作区复位照常执行
    }
  }
  workspaceMode.value = false
  setWorkspaceFullscreen?.(false)
}

const toggleFullscreen = () => {
  if (isFullscreen.value) {
    exitFullscreen()
    return
  }
  workspaceMode.value = true
  setWorkspaceFullscreen?.(true)
  requestNativeFullscreen().then((ok) => {
    // 原生全屏没成（降级为工作区铺满）不打扰用户；图标态已由 isFullscreen 计算正确
    if (!ok) isNativeFullscreen.value = false
  })
}

// 用户按 Esc / 系统手势退出原生全屏时，fullscreenchange 兜底联动退出工作区模式，
// 避免"已退出全屏但侧栏还没回来"的半态。
const onFullscreenChange = () => {
  isNativeFullscreen.value = !!fullscreenEl()
  if (!isNativeFullscreen.value && workspaceMode.value) {
    workspaceMode.value = false
    setWorkspaceFullscreen?.(false)
  }
}
const onGlobalKeydown = (event) => {
  // 原生全屏下浏览器自己处理 Esc；这里只兜底"降级为工作区铺满"的场景
  if (event.key === 'Escape' && !isNativeFullscreen.value) exitFullscreen()
}

watch(isFullscreen, async (val) => {
  setLayoutContentNoScroll?.(val)
  // 布局高度变化后趋势图容器尺寸会变：等 DOM 更新完重新量一次并重算几何
  await nextTick()
  syncTrendSize()
  bindTrendObserver()
  // 全屏切换后视口高度变了，历史表格的可用高度要重算
  updateHistoryTableScrollY()
})

// 趋势图容器是 v-if 出来的，数据到达后才会存在，挂载完再绑 ResizeObserver
watch(trendHasData, async (has) => {
  if (!has) {
    stopTrendObserver()
    trendBox.value = { ...TREND_BASE }
    return
  }
  await nextTick()
  syncTrendSize()
  bindTrendObserver()
})

// ---------- 启动 ----------
onMounted(() => {
  applyQuery()
  document.addEventListener('fullscreenchange', onFullscreenChange)
  document.addEventListener('webkitfullscreenchange', onFullscreenChange)
  window.addEventListener('keydown', onGlobalKeydown)
  // 直链进入时可能已处于全屏（如刷新后浏览器仍持有 fullscreenElement）
  isNativeFullscreen.value = !!fullscreenEl()
  setLayoutContentNoScroll?.(isFullscreen.value)
  if (isNativeFullscreen.value) {
    // 刷新后浏览器仍持有原生全屏：工作区全屏状态已随刷新丢失，补齐避免半态
    workspaceMode.value = true
    setWorkspaceFullscreen?.(true)
  }
  if (isOverview.value) {
    loadOverview()
    startTimer()
    document.addEventListener('visibilitychange', onVisibilityChange)
  } else {
    loadHistory()
  }
  window.addEventListener('resize', updateHistoryTableScrollY)
  // 首屏等 DOM 渲染完再量，否则拿到的表格 top 是 0
  nextTick(updateHistoryTableScrollY)
})

// 卸载必须清定时器与全局监听：否则离开页面后轮询仍在打接口
onUnmounted(() => {
  stopTimer()
  stopTrendObserver()
  // 还原公共布局的内容区滚动与工作区全屏，否则会一直影响后续页面
  setLayoutContentNoScroll?.(false)
  exitFullscreen()
  document.removeEventListener('visibilitychange', onVisibilityChange)
  document.removeEventListener('fullscreenchange', onFullscreenChange)
  document.removeEventListener('webkitfullscreenchange', onFullscreenChange)
  window.removeEventListener('keydown', onGlobalKeydown)
  window.removeEventListener('resize', updateHistoryTableScrollY)
})

// keepAlive 路由：切走时页面不卸载，必须主动退出全屏并还原布局
onDeactivated(() => {
  exitFullscreen()
  setLayoutContentNoScroll?.(false)
})

watch(isOverview, (overviewMode) => {
  if (overviewMode) document.addEventListener('visibilitychange', onVisibilityChange)
  else document.removeEventListener('visibilitychange', onVisibilityChange)
  // 切到历史 Tab 时表格才挂载，需要重新量一次可用高度
  if (!overviewMode) nextTick(updateHistoryTableScrollY)
})
</script>

<style scoped>
.alert-center-page {
  display: flex;
  flex-direction: column;
  gap: var(--gi-spacing-ssm);
  /* 顶部留白收紧：外层 .layout-content 有全局 24px 内边距（不动公共布局），
     本页整体上提 8px，让标题/右上角刷新控件更贴近容器顶部（24 → 16）。 */
  margin-top: calc(var(--gi-spacing-sm) * -1);
}

/* ===================== 告警历史 Tab：铺满可视区、无页面滚动条 =====================
   仅 .tab-history（告警历史）生效，概览 Tab 不受影响。
   表格 tbody 高度由 updateHistoryTableScrollY 实测给定，让「工具栏 + 表格 + 分页」
   恰好填满内容区；这里再把分页器下方的默认留白清零——antd 分页自带 16px 下边距、
   卡片自带 24px 底部内边距，叠加在量算预留之外，会把内容整体顶出可视区
   ~40px，产生右侧纵向滚动条。清零后分页器紧贴内容区底部（保留内容区自身的
   24px 页面留白，与其他页面观感一致）。 */
.alert-center-page.tab-history .alert-center-card :deep(.ant-card-body) {
  padding-bottom: 0;
}

.alert-center-page.tab-history :deep(.ant-tabs-tabpane-active .ant-table-wrapper .ant-pagination) {
  margin-bottom: 0;
}

/* ===================== 全屏态：A 弹性撑满 + B 整体放大 =====================
   仅在 .is-fs 下生效，普通窗口布局完全不变。
   A：申请「内容区不滚动」拿到 height:100% 后，把高度沿 flex 链传给图表区，
      图表区吃掉剩余高度（趋势图按容器实测尺寸重绘、饼图垂直居中），
      明细面板自然高度、展开时封顶 46% 并在表体内部滚动 → 全屏不再有底部空白。
   B：全屏下同步放大标题/计数卡/图表与图例尺寸，投屏可读性更好。 */
.alert-center-page.is-fs {
  height: 100%;
  min-height: 0;
  /* 全屏下不再上提 8px，否则 100% 高度会溢出 */
  margin-top: 0;
  /* 全屏时内容区脱离 .layout-content 的 padding，顶部贴边显得压抑——
     与监控大屏（.monitor-page.is-fullscreen）同方案：顶部安全留白 + border-box 不撑破高度 */
  padding-top: var(--gi-spacing-lg, 24px);
  box-sizing: border-box;
}

/* 标题左侧呼吸空间：全屏态 .alert-center-page 无水平内边距，标题会贴住屏幕左缘；
   常规布局下也让标题与下方卡片的内边距观感对齐（与监控大屏同方案）。
   只作用于本页（PageHeader 为 5 个页面共享）。 */
.alert-center-page :deep(.page-header-title) {
  padding-left: 8px;
}

.alert-center-page.is-fs .page-header :deep(.page-header-title) {
  font-size: 22px;
}

/* --- A：高度链（卡片行固定 → 图表区弹性 → 明细面板封顶） ---
   只对「告警概览」生效：历史面板是分页表格，全屏下走页面内滚动，
   固定高度 + overflow hidden 会把分页器裁掉。 */
.alert-center-page.is-fs.tab-overview .alert-center-card {
  flex: 1;
  min-height: 0;
}

.alert-center-page.is-fs.tab-overview .alert-center-card :deep(.ant-card-body) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.alert-center-page.is-fs.tab-overview :deep(.ant-tabs) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.alert-center-page.is-fs.tab-overview :deep(.ant-tabs-content-holder) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.alert-center-page.is-fs.tab-overview :deep(.ant-tabs-content) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.alert-center-page.is-fs.tab-overview :deep(.ant-tabs-tabpane-active) {
  height: 100%;
  min-height: 0;
}

/* 全屏 + 历史面板：滚动只发生在表格 tbody 内（表格 y 由脚本按视口实测），
   父容器不再产生页面级滚动条，避免双层滚动。 */
.alert-center-page.is-fs:not(.tab-overview) {
  overflow: hidden;
}

.alert-center-page.is-fs:not(.tab-overview) .alert-center-card {
  flex: 0 0 auto;
}

.alert-center-page.is-fs .ov-fill {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.alert-center-page.is-fs .ov-charts {
  /* flex-basis 0 + grow：图表区恒等于「剩余高度」，明细展开时自动让位、不会挤爆；
     下限（视口足够时 300px）只在明细收起时生效——明细一旦展开，优先保证明细可用 */
  flex: 1 1 0;
  min-height: min(300px, 42%);
  /* 明确给一个可收缩的行轨道，保证两个面板纵向填满图表区 */
  grid-template-rows: minmax(0, 1fr);
}

.alert-center-page.is-fs .ov-fill.detail-open .ov-charts {
  min-height: 0;
}

.alert-center-page.is-fs .ov-charts .ov-panel {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

/* 明细面板：收起时按内容高度（一行标题），展开时最多占 42%、表体内部滚动。
   42% 是权衡值：既保证明细至少能看 3–4 行，也给图表区留下足够高度（饼图不被压小）。 */
.alert-center-page.is-fs .ov-top-panel {
  flex: 0 1 auto;
  max-height: 42%;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}

/* 高度链关键一环：展开区包裹层必须继续把高度往下传，
   否则里面的 a-table 拿不到受限高度，表体不会出现内部滚动条。 */
.alert-center-page.is-fs .ov-top-panel .ov-top-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}


.alert-center-page.is-fs .ov-top-panel :deep(.ant-table-wrapper),
.alert-center-page.is-fs .ov-top-panel :deep(.ant-spin-nested-loading),
.alert-center-page.is-fs .ov-top-panel :deep(.ant-spin-container),
.alert-center-page.is-fs .ov-top-panel :deep(.ant-table),
.alert-center-page.is-fs .ov-top-panel :deep(.ant-table-container),
.alert-center-page.is-fs .ov-top-panel :deep(.ant-table-content) {
  min-height: 0;
}

.alert-center-page.is-fs .ov-top-panel :deep(.ant-table-wrapper) {
  flex: 1;
  display: flex;
  flex-direction: column;
}

.alert-center-page.is-fs .ov-top-panel :deep(.ant-spin-nested-loading) {
  flex: 1;
  display: flex;
  flex-direction: column;
}

.alert-center-page.is-fs .ov-top-panel :deep(.ant-spin-container) {
  flex: 1;
  display: flex;
  flex-direction: column;
}

.alert-center-page.is-fs .ov-top-panel :deep(.ant-table) {
  flex: 1;
}

.alert-center-page.is-fs .ov-top-panel :deep(.ant-table-container) {
  height: 100%;
  display: flex;
  flex-direction: column;
}

/* 表体滚动 + 表头吸顶（a-table 未开 scroll.y 时没有独立 body，用 CSS 实现） */
.alert-center-page.is-fs .ov-top-panel :deep(.ant-table-content) {
  flex: 1;
  min-height: 0;
  overflow: auto;
}

.alert-center-page.is-fs .ov-top-panel :deep(.ant-table-thead > tr > th) {
  position: sticky;
  top: 0;
  z-index: 2;
  /* 与 antd Table.headerBg 保持一致，滚动时下方内容不会透出来 */
  background: #f8fbfc;
}

/* 饼图区垂直居中吃满面板高度 */
.alert-center-page.is-fs .ov-charts .pie-wrap {
  flex: 1;
  min-height: 0;
  padding: 0;
}

/* 趋势图：SVG 铺满容器高度，viewBox 由 ResizeObserver 同步成同一尺寸（等比不拉伸） */
.alert-center-page.is-fs .ov-charts .trend-wrap {
  flex: 1;
  min-height: 0;
}

.alert-center-page.is-fs .ov-charts .trend-svg {
  height: 100%;
}

/* 空数据占位在全屏撑满的面板里居中，不要贴在顶部 */
.alert-center-page.is-fs .ov-charts .chart-empty {
  margin: auto;
}

/* --- B：全屏放大（计数卡 / 面板标题 / 饼图 / 图例 / 表体行距） --- */
.alert-center-page.is-fs .ov-cards {
  gap: 14px;
  margin-bottom: 14px;
}

.alert-center-page.is-fs :deep(.ov-card) {
  padding: 18px;
}

.alert-center-page.is-fs :deep(.ov-card-label) {
  font-size: 14px;
}

.alert-center-page.is-fs :deep(.ov-card-value) {
  padding: 8px 0 4px;
  font-size: 34px;
}

.alert-center-page.is-fs :deep(.ov-card-sub) {
  font-size: 12.5px;
}

.alert-center-page.is-fs .ov-panel-title {
  font-size: 15px;
}

.alert-center-page.is-fs .ov-charts .ov-panel {
  padding: 16px 18px;
}

.alert-center-page.is-fs .pie-wrap {
  gap: 40px;
}

.alert-center-page.is-fs .pie-svg {
  /* 高度跟随面板可用高度（上限 250px）：图表区被压缩时饼图不会溢出面板 */
  width: auto;
  height: 100%;
  max-width: 250px;
  max-height: 250px;
}

.alert-center-page.is-fs .pie-legend-row {
  padding-top: 5px;
  padding-bottom: 5px;
}

.alert-center-page.is-fs .ov-top-panel :deep(.ant-table-tbody > tr > td) {
  padding-top: 12px;
  padding-bottom: 12px;
}

.alert-center-page.is-fs .ov-top-panel :deep(.ant-table-thead > tr > th) {
  padding-top: 11px;
  padding-bottom: 11px;
}

/* 页面标题样式统一收口到 components/PageHeader.vue，本页仅保留全屏放大覆盖 */
.refresh-hint {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.refresh-hint.bad {
  color: #cf1322;
}

/* 右上角纯图标控制区：勾选框 / 刷新 / 全屏三个方框统一为 16×16（与旧版勾选框同尺寸），
   仅作用于本页；勾选框保持 antd 默认 16×16，图标按钮同步缩到同尺寸。 */
.page-actions .icon-btn {
  width: 16px;
  height: 16px;
  padding: 0;
  font-size: 12px;
  line-height: 1;
  border-radius: 4px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

/* 全屏图标用主题色（tokens.scss --gi-color-primary），与刷新默认色区分 */
.page-actions .fullscreen-btn {
  color: var(--gi-color-primary, #0f766e);
}

.alert-center-card {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

/* Tab 区与卡片顶部间距收紧：卡片内边距上 24 → 8（与【监控配置】页 .section-card 同一惯例），
   仅作用于本页卡片（scoped + :deep），不影响其他页面。 */
.alert-center-card :deep(.ant-card-body) {
  padding-top: var(--gi-spacing-sm);
  padding-bottom: 12px;
}

/* ----- 概览：计数卡 ----- */
.ov-cards {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 12px;
}

/* ----- 概览：图表区 ----- */
.ov-charts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 12px;
}

.ov-panel {
  padding: 14px 16px;
  background: var(--gi-color-container-bg, #fff);
  border: 1px solid var(--gi-color-border-soft, rgba(0, 0, 0, 0.08));
  border-radius: 8px;
}

.ov-top-panel {
  margin-bottom: 0;
}

/* 收起态：紧凑化标题面板，减少上下占位，避免底部出现大片空白 */
.ov-top-panel.collapsed {
  padding-top: 10px;
  padding-bottom: 10px;
}

/* 当前告警信息明细表的对齐：一律由 topColumns 每列的 align 决定
   （ant-design-vue 4.x 把 column.align 渲染成 th/td 的内联 text-align，
   而不是 class——所以这里**不要**再用 !important 批量覆盖，
   否则会把操作列的 align:'center' 静默压回左对齐）。列宽比例见 topColumns。 */

.ov-panel-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--gi-color-text-primary, rgba(0, 0, 0, 0.85));
}

/* 圆饼图面板标题行：左标题 + 右侧分组维度下拉 */
.ov-panel-titlebar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.pie-dim-select {
  flex: none;
  width: 92px;
}

/* 趋势面板右上角的时间搜索框（精度到日，默认最近 7 天、最长 30 天） */
.trend-range {
  flex: none;
  width: 250px;
}

/* 底部明细面板标题行：左标题 + 右「收起/展开」 */
.ov-top-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.ov-top-toggle {
  flex: none;
  height: auto;
  padding: 0;
  font-size: 12px;
  /* 收起/展开操作文字用系统主题色：动态引用全局 token，切主题自动同步 */
  color: var(--gi-color-primary, #0f766e);
}

.ov-top-toggle:hover {
  color: var(--gi-color-primary-hover, #0d6b63);
}

/* 展开区：原「全部实例 / 全部级别」筛选行已移除，
   这里用 padding-top 顶上原来筛选行的上边距（margin 10px + 下边距 12px 中的上方空隙），
   避免表格紧贴标题行；收起时 .ov-top-body 不渲染，不会在面板底部留下空白。 */
.ov-top-body {
  padding-top: 10px;
}

/* 数据库实例列（仅底部明细表）：名称去掉链接色，IP 为淡灰的 ip:port */
.ov-top-panel .cell-instance {
  color: var(--gi-color-text-primary, rgba(0, 0, 0, 0.85));
  cursor: pointer;
}
.ov-top-panel .cell-instance:hover .cell-main {
  text-decoration: underline;
  text-underline-offset: 3px;
}

/* 操作列「查看详情」用主题色 */
.ov-top-panel :deep(.ov-detail-btn) {
  color: var(--gi-color-primary, #0f766e);
}
.ov-top-panel :deep(.ov-detail-btn:hover) {
  color: var(--gi-color-primary-hover, #0d6b63);
}

/* 可点击数字：计数卡数字 + 级别分布图例行，点击弹出下钻弹窗 */
.ov-num-link {
  cursor: pointer;
}
.ov-num-link:hover {
  text-decoration: underline;
  text-underline-offset: 4px;
}

.pie-legend-row-link {
  cursor: pointer;
  border-radius: 4px;
}
.pie-legend-row-link:hover .legend-value {
  text-decoration: underline;
  text-underline-offset: 3px;
}

.chart-empty {
  padding: 28px 0;
}

/* ----- 圆饼图 ----- */
.pie-wrap {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 28px;
  padding: 20px 0;
}

.pie-svg {
  flex: none;
  width: 220px;
  height: 220px;
  overflow: visible;
}

.pie-seg {
  stroke: var(--gi-color-container-bg, #fff);
  stroke-width: 2;
  stroke-linejoin: round;
  transition: opacity 0.2s ease, filter 0.2s ease;
  cursor: pointer;
}

.pie-seg:hover {
  opacity: 0.92;
  filter: brightness(1.05);
}

.pie-center-bg {
  fill: var(--gi-color-container-bg, #fff);
  /* 透明描边占位：hover/聚焦时才上色，避免常态下圆环轮廓跳动 */
  stroke: transparent;
  stroke-width: 2;
  transition: fill 0.18s ease, stroke 0.18s ease;
}

/* 中心数字可点击：手型光标 + hover 换主题色 + 淡色圆底，给出明确的点击提示 */
.pie-center-hit {
  cursor: pointer;
}

.pie-center-hit:focus {
  outline: none;
}

.pie-center-hit:hover .pie-center-bg,
.pie-center-hit:focus-visible .pie-center-bg {
  fill: var(--gi-color-primary-bg, rgba(15, 118, 110, 0.08));
  stroke: var(--gi-color-primary, #0f766e);
}

.pie-center-num {
  font-size: 34px;
  font-weight: 600;
  fill: var(--gi-color-text-primary, rgba(0, 0, 0, 0.85));
  text-anchor: middle;
  /* 与模板上的 dominant-baseline="central" 双保险：数字在圆环内严格垂直居中 */
  dominant-baseline: central;
  transition: fill 0.18s ease;
}

.pie-center-hit:hover .pie-center-num,
.pie-center-hit:focus-visible .pie-center-num {
  fill: var(--gi-color-primary, #0f766e);
}

/* 「其他」分组的悬浮明细（a-tooltip 内容） */
.pie-tip-title {
  margin-bottom: 4px;
  font-weight: 600;
}

.pie-tip-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
}

.pie-legend {
  display: flex;
  flex: none;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
  padding-left: 4px;
}

.pie-legend-row {
  display: grid;
  grid-template-columns: 10px 1fr auto auto;
  align-items: center;
  gap: 10px;
  font-size: 14px;
  line-height: 1.4;
}

.legend-dot {
  flex: none;
  width: 10px;
  height: 10px;
  border-radius: 50%;
}

.legend-label {
  min-width: 0;
  overflow: hidden;
  color: var(--gi-color-text-secondary, rgba(0, 0, 0, 0.65));
  text-overflow: ellipsis;
  white-space: nowrap;
}

.legend-value {
  font-weight: 500;
  color: var(--gi-color-text-primary, rgba(0, 0, 0, 0.85));
  text-align: right;
}

.legend-pct {
  width: 48px;
  font-size: 13px;
  color: var(--gi-color-text-tertiary, #8a94a6);
  text-align: right;
}

/* ----- 趋势折线 ----- */
.trend-wrap {
  position: relative;
  /* 副标题「每日新增触发数（按触发时间）」已删除，补回标题与图表之间的间距 */
  margin-top: var(--gi-spacing-ssm);
}

.trend-svg {
  display: block;
  width: 100%;
}

.trend-grid {
  stroke: rgba(0, 0, 0, 0.08);
  stroke-width: 0.5;
}

.trend-ylabel,
.trend-xlabel {
  font-size: 11px;
  fill: var(--gi-color-text-tertiary, #8a94a6);
}

.trend-ylabel {
  text-anchor: end;
}

.trend-xlabel {
  text-anchor: middle;
}

/* 趋势折线与 P2 级别同色（--gi-color-warning），与整页告警配色体系统一 */
.trend-line {
  fill: none;
  stroke: var(--gi-color-warning, #faad14);
  stroke-width: 2;
  stroke-linejoin: round;
}

.trend-dot {
  fill: var(--gi-color-warning, #faad14);
  stroke: #fff;
  stroke-width: 1.5;
}

.trend-dot.hot {
  r: 4.5;
}

.trend-cross {
  stroke: rgba(250, 173, 20, 0.45);
  stroke-width: 1;
  stroke-dasharray: 3 3;
}

.trend-tip {
  position: absolute;
  z-index: 5;
  min-width: 130px;
  padding: 8px 10px;
  pointer-events: none;
  background: rgba(255, 255, 255, 0.98);
  border: 1px solid var(--gi-color-border-soft, rgba(0, 0, 0, 0.12));
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
}

.trend-tip-date {
  margin-bottom: 4px;
  font-size: 12px;
  font-weight: 500;
}

.trend-tip-row {
  display: flex;
  gap: 6px;
  align-items: center;
  font-size: 12px;
  color: var(--gi-color-text-secondary, rgba(0, 0, 0, 0.65));
}

/* ----- 历史 Tab ----- */
/* 筛选栏已移入 a-tabs 的 #rightExtra 插槽，与 tab 同行；
   不再需要独立行的 margin/对齐逻辑，只负责水平排布与窄屏内滚动 */
.tab-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: nowrap;
}

/* extra 插槽在 tab 导航行内垂直居中（antd 默认基线对齐会导致控件下沉） */
.alert-center-card :deep(.ant-tabs-extra-content) {
  display: flex;
  align-items: center;
}

.filter-toolbar-left {
  /* 空间不足时自身横向滚动，不挤压左侧 tab 标签 */
  flex: 0 1 auto;
  min-width: 0;
  max-width: min(900px, calc(100vw - 300px));
  overflow-x: auto;
  /* 抵消滚动条占位，避免控件与 tab 文字基线错位 */
  scrollbar-width: none;
}
.filter-toolbar-left::-webkit-scrollbar {
  display: none;
}

.filter-toolbar-left .ant-space {
  flex-wrap: nowrap !important;
  gap: 8px !important;
}

.filter-toolbar-left .ant-space-item {
  flex-shrink: 0;
}

.tab-hint {
  margin-top: 10px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

/* 筛选控件按实际内容收缩宽度：比原先窄，保证一行排开 */
.filter-select {
  width: 110px;
  min-width: 110px;
}

/* 时间区间选择器：常驻展示，show-time 精确到秒。
   高度显式拉到 40px：全局 token controlHeight=40 让输入框/下拉/按钮都是 40，
   但 antd Picker 组件不消费该 token（固定 padding），默认 32px，会出现唯一矮一截。 */
.custom-range-picker {
  width: 360px;
  height: 40px;
}

.custom-range-picker:deep(.ant-picker-input > input) {
  height: 38px;
}

.filter-id-input {
  width: 180px;
  font-family: var(--font-mono, monospace);
}

.alert-id-text {
  font-family: var(--font-mono, monospace);
  font-size: 12px;
  color: var(--gi-color-primary, #0f766e);
}

.copy-id-btn {
  width: 18px;
  height: 18px;
  margin-left: 4px;
  padding: 0;
  line-height: 1;
  color: var(--gi-color-text-tertiary, #8a94a6);
}
.copy-id-btn:hover {
  color: var(--gi-color-primary, #0f766e);
}
.copy-id-btn :deep(.anticon) {
  font-size: 12px;
}

/* 实例列：两行（数据源名称 + ip:port）。
   两行都继承单元格默认文本色（不套主题色），只保留手型光标和 title 兜底。 */
/* 告警内容（原「告警类型」，告警名称）表头不折叠：始终单行，不换行也不省略号 */
:deep(.col-alert-name) {
  white-space: nowrap;
  overflow: visible;
  text-overflow: clip;
}
.cell-instance {
  display: block;
  color: inherit;
  cursor: pointer;
}
.cell-main {
  display: block;
  color: inherit;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.cell-ip {
  display: block;
  margin-top: 2px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

/* 状态列：无框纯色文字（红=未恢复/橙=已认领/绿=恢复），忽略信息第二行小字淡灰 */
.st-cell {
  line-height: 1.35;
}
.st-text {
  font-weight: 500;
}
.st-active {
  color: #f54a45;
}
.st-acknowledged {
  color: #d25f00;
}
.st-recovered {
  color: #23a05c;
}
.st-ignored-line {
  margin-top: 3px;
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #8a94a6);
  cursor: default;
}
.st-ignored-line:hover {
  color: var(--gi-color-primary, #0f766e);
}

/* 操作列链接统一使用系统主题色，覆盖 antd link 默认蓝色 */
.action-link {
  color: var(--gi-color-primary, #0f766e);
}
.action-link:hover {
  color: var(--gi-color-primary-hover, #0d6b63);
}
.action-link:active {
  color: var(--gi-color-primary-active, #0b5f58);
}

.detail-section {
  margin-top: 16px;
}

.detail-title {
  margin-bottom: 6px;
  font-size: 13px;
  font-weight: 500;
}

.detail-body {
  font-size: 13px;
  line-height: 1.7;
  white-space: pre-wrap;
  color: rgba(0, 0, 0, 0.75);
}

/* 窄屏（<1180px）：图表改单列，计数卡两列 */
@media (max-width: 1180px) {
  .ov-cards {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .ov-charts {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 768px) {
  .ov-cards {
    grid-template-columns: minmax(0, 1fr);
  }

  /* 饼图 220px 固定且不换行，图例被挤出容器 */
  .pie-wrap {
    flex-wrap: wrap;
    justify-content: center;
    gap: 16px;
    padding: 12px 0;
  }

  .pie-svg {
    width: 180px;
    height: 180px;
  }

  .pie-legend {
    flex: 1 1 100%;
    padding-left: 0;
  }

  .pie-legend-row {
    grid-template-columns: 10px minmax(0, 1fr) auto;
    font-size: 13px;
    column-gap: 8px;
  }

  .trend-tip {
    min-width: 0;
    max-width: calc(100vw - 40px);
  }

  /* 历史 Tab 的筛选栏：整体折到第二行，控件各占一行 */
  .alert-center-card :deep(.ant-tabs-nav) {
    flex-wrap: wrap;
  }

  .alert-center-card :deep(.ant-tabs-extra-content) {
    flex: 1 1 100%;
    min-width: 0;
  }

  .tab-toolbar {
    flex-wrap: wrap;
    width: 100%;
  }

  .filter-toolbar-left {
    max-width: 100%;
    overflow-x: visible;
    width: 100%;
  }

  .filter-select,
  .custom-range-picker,
  .filter-id-input {
    width: 100% !important;
    min-width: 0;
  }

  .custom-range-picker {
    height: auto;
  }

  /* 复制 ID 按钮原本 18x18 */
  .copy-id-btn {
    width: 32px;
    height: 32px;
    margin-left: 0;
  }

  :deep(.col-alert-name) {
    white-space: normal;
  }

  .ov-panel {
    padding: 12px;
  }
}
</style>
