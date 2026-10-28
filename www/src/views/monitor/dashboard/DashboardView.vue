<template>
  <div class="gi-page-shell monitor-page" :class="{ 'is-fullscreen': isFullscreen }">
    <!-- 顶部条：标题 + 时间区间 + 业务线/机房 + 生成时间 + 实时状态（统一用 PageHeader） -->
    <PageHeader title="监控大屏">
      <template #left>
        <!-- 时间范围：下拉框（最近5分钟/30分钟/1小时/6小时/1天/7天/自定义）；自定义时展开起止时间选择器 -->
        <a-select
          v-model:value="rangeMode"
          size="small"
          class="range-select"
          :options="rangeOptions"
          @change="handleRangeModeChange"
        />
        <a-range-picker
          v-if="rangeMode === 'custom'"
          v-model:value="customRange"
          size="small"
          class="range-picker"
          show-time
          format="YYYY-MM-DD HH:mm"
          value-format="YYYY-MM-DD HH:mm:ss"
          :allow-clear="false"
          :disabled-date="disableFutureDate"
          :placeholder="['开始时间', '结束时间']"
          @change="handleCustomRangeChange"
        />
        <a-select
          v-model:value="filterBusiness"
          allow-clear
          size="small"
          class="filter-select"
          placeholder="全部业务线"
          :options="businessOptions"
          @change="reload"
        />
        <a-select
          v-model:value="filterEnvironment"
          allow-clear
          size="small"
          class="filter-select"
          placeholder="全部机房"
          :options="environmentOptions"
          @change="reload"
        />
      </template>
      <template #right>
        <span class="screen-clock">{{ dashboard.generated_at || '未获取' }}</span>
        <span class="live-badge" :class="liveState"><span class="live-dot"></span>实时状态</span>
        <a-tooltip :title="isFullscreen ? '退出全屏 (Esc)' : '大屏全屏'">
          <a-button
            size="small"
            type="text"
            class="fullscreen-btn"
            @click="toggleDashboardFullscreen"
          >
            <FullscreenExitOutlined v-if="isFullscreen" />
            <FullscreenOutlined v-else />
          </a-button>
        </a-tooltip>
      </template>
    </PageHeader>

    <!-- 汇总行：环形图 + 4 张指标卡 -->
    <div class="summary-row">
      <div class="screen-card donut-card">
        <div class="donut-wrap">
          <svg viewBox="0 0 120 120" class="donut-svg">
            <circle
              v-for="(seg, index) in donutSegments"
              :key="index"
              cx="60"
              cy="60"
              r="48"
              fill="none"
              :stroke="seg.color"
              stroke-width="16"
              :stroke-dasharray="seg.dasharray"
              :stroke-dashoffset="seg.dashoffset"
              transform="rotate(-90 60 60)"
            />
          </svg>
          <div class="donut-center">
            <div class="donut-total stat-value-link" title="点击查看实例列表" @click="openInstanceDrill('all')">{{ dashboard.summary.total_instances }}</div>
            <div class="donut-label">总实例</div>
          </div>
        </div>
        <a-tooltip :title="prevDeltaTooltip">
          <!-- 图例四类与右侧指标卡同交互：数字点击下钻到该分类的实例列表
               （scope 与健康分级引擎四分类一一对应，含 uncollected） -->
          <ul class="donut-legend">
            <li class="legend-link" @click="openInstanceDrill('healthy')">
              <span class="dot dot-healthy"></span>
              <span class="legend-text">
                <span class="legend-label">健康</span>
                <i class="legend-value">{{ screen.status.healthy }}</i>
              </span>
            </li>
            <li class="legend-link" @click="openInstanceDrill('risk')">
              <span class="dot dot-risk"></span>
              <span class="legend-text">
                <span class="legend-label">风险</span>
                <i class="legend-value">{{ screen.status.risk }}</i>
              </span>
            </li>
            <li class="legend-link" @click="openInstanceDrill('fault')">
              <span class="dot dot-fault"></span>
              <span class="legend-text">
                <span class="legend-label">故障</span>
                <i class="legend-value">{{ screen.status.fault }}</i>
              </span>
            </li>
            <li class="legend-link" @click="openInstanceDrill('uncollected')">
              <span class="dot dot-uncollected"></span>
              <span class="legend-text">
                <span class="legend-label">未采集</span>
                <i class="legend-value">{{ screen.status.uncollected }}</i>
              </span>
            </li>
          </ul>
        </a-tooltip>
      </div>

      <div class="screen-card stat-card tone-green">
        <div class="stat-title">健康实例</div>
        <div class="stat-value stat-value-link" title="点击查看实例列表" @click="openInstanceDrill('healthy')">{{ screen.status.healthy }}</div>
      </div>

      <div class="screen-card stat-card tone-orange">
        <div class="stat-title">风险实例</div>
        <div class="stat-value stat-value-link" title="点击查看实例列表" @click="openInstanceDrill('risk')">{{ screen.status.risk }}</div>
      </div>

      <div class="screen-card stat-card tone-red">
        <div class="stat-title">
          故障实例
          <!-- 口径口径说明：点击问号圆圈弹出故障判定含义 -->
          <a-tooltip trigger="click" title="数据库无法连接或宕机">
            <QuestionCircleOutlined class="title-help-icon" />
          </a-tooltip>
        </div>
        <div class="stat-value stat-value-link" title="点击查看实例列表" @click="openInstanceDrill('fault')">{{ screen.status.fault }}</div>
      </div>

      <!-- 告警数：环形图卡，与首卡同构 —— 圆心=当前未恢复告警数（待处理+处理中，实时快照）；
           环按状态分三段，其中待处理/处理中为实时快照、已解决为当前时间范围内的数据（口径见各行 title）；
           圆心与图例三行均可点击下钻到对应告警列表 -->
      <div class="screen-card donut-card alert-donut-card">
        <div class="donut-wrap">
          <svg viewBox="0 0 120 120" class="donut-svg">
            <circle
              v-for="(seg, index) in alertDonutSegments"
              :key="index"
              cx="60"
              cy="60"
              r="48"
              fill="none"
              :stroke="seg.color"
              stroke-width="16"
              :stroke-dasharray="seg.dasharray"
              :stroke-dashoffset="seg.dashoffset"
              transform="rotate(-90 60 60)"
            />
          </svg>
          <div class="donut-center">
            <div class="donut-total stat-value-link" title="点击查看当前告警（待认领+处理中）" @click="openRangeDrill()">{{ alertPendingTotal }}</div>
            <div class="donut-label">告警数</div>
          </div>
        </div>
        <!-- 图例：三类各自的统计口径写在每行 title 里（悬停可见），卡面不再占用额外行 -->
        <div class="donut-legend-wrap">
          <ul class="donut-legend">
            <li class="legend-link" title="待认领：实时快照（与当前时间范围无关）" @click="openRangeDrill('active')">
              <span class="dot dot-pending"></span>
              <span class="legend-text">
                <span class="legend-label">待认领</span>
                <i class="legend-value">{{ alertCounts.pending }}</i>
              </span>
            </li>
            <li class="legend-link" title="处理中：实时快照（与当前时间范围无关）" @click="openRangeDrill('acknowledged')">
              <span class="dot dot-doing"></span>
              <span class="legend-text">
                <span class="legend-label">处理中</span>
                <i class="legend-value">{{ alertCounts.processing }}</i>
              </span>
            </li>
            <li class="legend-link" :title="`已恢复：${rangeLabelText}内恢复的告警`" @click="openRangeDrill('recovered')">
              <span class="dot dot-resolved"></span>
              <span class="legend-text">
                <span class="legend-label">已恢复</span>
                <i class="legend-value">{{ alertCounts.resolved }}</i>
              </span>
            </li>
          </ul>
        </div>
      </div>
    </div>

    <!-- 中部 + 底部：左 2/3 三块，右 1/3 告警事件中心跨两行 -->
    <div class="screen-grid">
      <!-- 近 7 天告警趋势：时间刻意与页面顶部范围解耦（全局管实时、本卡管中长期）。
           柱 = 每段新增告警数（按 first_seen_at/triggered_at 落桶，口径与告警数卡一致），
           柱内只按两种状态着色：未恢复（紫）/ 已恢复（绿），不引入其他分类维度；
           色值取 chartTokens 的 --chart-alert-pending / --chart-alert-resolved，
           与右侧告警圆饼、告警卡状态条同源（全站同一套"未闭合/已闭合"语义色）；
           点柱子下钻该段告警列表（所有状态合并一张表 + 列内筛选/排序，口径自洽）。 -->
      <div class="screen-card trend-card">
        <div class="card-header">
          <span class="card-title">{{ trendTitle }}</span>
          <!-- 图例：与标题同行，放在时间切换 chips 左侧（同一右端组）。
               颜色是"此刻回看"的状态归类，会随恢复进度变化（同一天今天绿、下周可能更绿） -->
          <div v-if="trendPoints.length" class="trend-legend" title="颜色为此刻回看的告警状态，会随恢复进度变化">
            <span class="trend-legend-item"><i class="trend-dot trend-dot-unresolved"></i>未恢复</span>
            <span class="trend-legend-item"><i class="trend-dot trend-dot-resolved"></i>已恢复</span>
          </div>
          <div class="trend-chips">
            <button
              v-for="p in TREND_PRESETS"
              :key="p.key"
              type="button"
              class="trend-chip"
              :class="{ active: trendPreset === p.key }"
              @click="setTrendPreset(p.key)"
            >{{ p.label }}</button>
          </div>
        </div>
        <div v-if="trendLoading && !trendPoints.length" class="placeholder-box">加载中...</div>
        <div v-else-if="trendPoints.length" class="trend-chart">
          <div
            v-for="(pt, i) in trendPoints"
            :key="pt.start"
            class="trend-col"
            :class="{ empty: pt.count === 0 }"
            :title="trendColTitle(pt)"
            @click="openTrendDrill(pt)"
          >
            <span class="trend-count" :class="{ 'has-unresolved': pt.unresolved_count > 0 }" @click.stop="openTrendDrill(pt)">{{ pt.count > 0 ? pt.count : '0' }}</span>
            <div class="trend-bar-area" @click.stop="openTrendDrill(pt)">
              <!-- 柱内只按两种状态着色：上段=未恢复（紫），下段=已恢复（绿），总高=该段新增总数。
                   颜色取 chartTokens 的 --chart-alert-pending / --chart-alert-resolved，
                   与右侧告警圆饼、告警卡状态条同源，全站同一套语义色。
                   未恢复段 min-height 4px 保底，避免 1/50 这类小占比被压到肉眼不可见。 -->
              <div class="trend-bar" :style="{ height: trendBarHeight(pt) }" @click.stop="openTrendDrill(pt)">
                <div
                  v-if="pt.unresolved_count > 0"
                  class="trend-seg trend-seg-unresolved"
                  :style="{ height: trendUnresolvedPct(pt) }"
                ></div>
                <div class="trend-seg trend-seg-resolved"></div>
              </div>
            </div>
            <span v-if="trendLabelVisible(i)" class="trend-label">{{ pt.label }}</span>
            <span v-else class="trend-label">&nbsp;</span>
          </div>
        </div>
        <div v-else class="placeholder-box">暂无数据</div>
      </div>

      <div class="screen-card alert-center-card">
        <div class="card-header">
          <span class="card-title">告警事件中心</span>
          <span class="card-note">{{ rangeLabelText }}</span>
        </div>
        <a-tabs v-model:activeKey="alertTab" size="small" class="alert-tabs">
          <a-tab-pane key="firing">
            <template #tab><span class="tab-label tab-label-firing">待认领({{ firingAlerts.length }})</span></template>
            <div class="alert-list-wrapper">
              <div class="alert-list">
                <div v-for="alert in pagedFiringAlerts" :key="alert.row_key" class="alert-item">
                  <div class="alert-line">
                    <span class="alert-time">{{ alertTime(alert) }}</span>
                    <a-tag :color="levelColor(alert.level || alert.severity)">{{ alert.severity || alert.level }}</a-tag>
                  </div>
                  <!-- 数据源行：回答"这条告警来自哪个库" —— 名称 + 地址 + 环境（类型图标已按要求移除）
                       实例名称最多显示 15 个字符，超出用 ... 截断，鼠标悬停显示全部 -->
                  <div class="ds-row">
                    <span class="ds-name"><DataSourceName :name="dsNameOf(alert)" :max="15" /></span>
                    <span class="ds-addr">{{ dsAddrOf(alert) }}</span>
                    <span v-if="alert.environment_name" class="ds-env">{{ alert.environment_name }}</span>
                  </div>
                  <div class="alert-name">{{ operatorTextInText(alert.name) }}</div>
                  <div class="alert-meta">
                    <span class="alert-duration">{{ alertDurationText(alert.duration_sec) }}</span>
                  </div>
                  <div class="alert-actions">
                    <a-button size="small" :loading="actionLoading === alert.row_key" @click="handleAlertAction(alert, 'ignore')">忽略</a-button>
                    <a-button size="small" type="primary" :loading="actionLoading === alert.row_key" @click="handleAlertAction(alert, 'ack')">认领工单</a-button>
                  </div>
                </div>
                <div v-if="!firingAlerts.length" class="placeholder-box">暂无待认领告警</div>
              </div>
              <div class="alert-list-footer">
                <span>共 {{ firingAlerts.length }} 条，当前第 {{ firingPage }} / {{ firingTotalPages }} 页</span>
                <a-space>
                  <a-button size="small" :disabled="firingPage <= 1" @click="firingPage -= 1">上一页</a-button>
                  <a-button size="small" :disabled="firingPage >= firingTotalPages" @click="firingPage += 1">下一页</a-button>
                </a-space>
              </div>
            </div>
          </a-tab-pane>
          <a-tab-pane key="acked">
            <template #tab><span class="tab-label tab-label-acked">处理中({{ ackedAlerts.length }})</span></template>
            <div class="alert-list-wrapper">
              <div class="alert-list">
                <div v-for="alert in pagedAckedAlerts" :key="alert.row_key" class="alert-item is-acked">
                  <div class="alert-line">
                    <span class="alert-time">{{ alertTime(alert) }}</span>
                    <a-tag color="blue">处理中</a-tag>
                  </div>
                  <div class="ds-row">
                    <span class="ds-name"><DataSourceName :name="dsNameOf(alert)" :max="15" /></span>
                    <span class="ds-addr">{{ dsAddrOf(alert) }}</span>
                    <span v-if="alert.environment_name" class="ds-env">{{ alert.environment_name }}</span>
                  </div>
                  <div class="alert-name">{{ operatorTextInText(alert.name) }}</div>
                  <div class="alert-meta">
                    <span class="alert-duration">{{ alert.acked_by ? `认领人: ${alert.acked_by}` : '认领人: 未获取' }}</span>
                  </div>
                  <div class="alert-actions">
                    <a-button size="small" :loading="actionLoading === alert.row_key" @click="handleAlertAction(alert, 'ignore')">忽略</a-button>
                  </div>
                </div>
                <div v-if="!ackedAlerts.length" class="placeholder-box">暂无处理中告警</div>
              </div>
              <div class="alert-list-footer">
                <span>共 {{ ackedAlerts.length }} 条，当前第 {{ ackedPage }} / {{ ackedTotalPages }} 页</span>
                <a-space>
                  <a-button size="small" :disabled="ackedPage <= 1" @click="ackedPage -= 1">上一页</a-button>
                  <a-button size="small" :disabled="ackedPage >= ackedTotalPages" @click="ackedPage += 1">下一页</a-button>
                </a-space>
              </div>
            </div>
          </a-tab-pane>
          <a-tab-pane key="recovered" :tab="`已恢复(${recoveredAlerts.length})`">
            <div class="alert-list-wrapper">
              <div class="alert-list">
                <div v-for="alert in pagedRecoveredAlerts" :key="alert.row_key" class="alert-item is-recovered">
                  <div class="alert-line">
                    <span class="alert-time">{{ alertTime(alert) }}</span>
                    <a-tag color="green">已恢复</a-tag>
                  </div>
                  <div class="ds-row">
                    <span class="ds-name"><DataSourceName :name="dsNameOf(alert)" :max="15" /></span>
                    <span class="ds-addr">{{ dsAddrOf(alert) }}</span>
                    <span v-if="alert.environment_name" class="ds-env">{{ alert.environment_name }}</span>
                  </div>
                  <div class="alert-name">{{ operatorTextInText(alert.name) }}</div>
                  <div class="alert-meta">
                    <span class="alert-duration">{{ alert.recovered_at || '恢复时间未获取' }}</span>
                  </div>
                </div>
                <div v-if="!recoveredAlerts.length" class="placeholder-box">{{ rangeLabelText }}内无恢复记录</div>
              </div>
              <div class="alert-list-footer">
                <span>共 {{ recoveredAlerts.length }} 条，当前第 {{ recoveredPage }} / {{ recoveredTotalPages }} 页</span>
                <a-space>
                  <a-button size="small" :disabled="recoveredPage <= 1" @click="recoveredPage -= 1">上一页</a-button>
                  <a-button size="small" :disabled="recoveredPage >= recoveredTotalPages" @click="recoveredPage += 1">下一页</a-button>
                </a-space>
              </div>
            </div>
          </a-tab-pane>
        </a-tabs>
      </div>

      <div class="screen-card table-card">
        <div ref="tableWrapRef" class="table-body-wrap" @scroll.passive="updateScrollHint">
            <a-table
              row-key="instance_id"
              size="small"
              :columns="tableColumns"
              :data-source="filteredInstances"
              :pagination="false"
              :loading="loading"
              class="instance-table"
              :custom-row="customRow"
            >
            <!-- 列表只保留风险/故障实例，全部被过滤时给贴合语义的空态 -->
            <template #emptyText>
              <div class="table-empty">暂无风险或故障实例</div>
            </template>
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'status'">
                <!-- 状态列：去掉 tag 圆角底，纯文字颜色区分，色值与第一张圆饼四分类同源（chartTokens） -->
                <span class="status-text" :style="{ color: statusTextColor(record.screen_class) }">{{ screenClassText(record.screen_class) }}</span>
              </template>
              <template v-else-if="column.key === 'issue'">
                <!-- 文字保持正文色，颜色只落在前导小圆点上（fault 红 / risk 橙 / 其它灰），
                     避免整列满屏红橙字带来的视觉噪音 -->
                <span v-if="issueOf(record)" class="issue-text">
                  <span class="issue-dot" :class="issueDotClass(record)"></span>{{ issueOf(record) }}
                </span>
                <span v-else class="is-na">—</span>
              </template>
              <template v-else-if="column.key === 'health_score'">
                <!-- 健康分由后端按分级评估结果派生；uncollected 等不可信场景为 null，显示 — -->
                <!-- 健康分由后端按分级评估结果派生；uncollected 等不可信场景为 null，显示 —。
                     着色与「状态」列统一：同一行的健康分与状态用同一语义色（chartTokens 四分类）。 -->
                <span v-if="record.health_score != null" class="score-text" :style="{ color: statusTextColor(record.screen_class) }">{{ healthScoreText(record.health_score) }}</span>
                <span v-else class="is-na">—</span>
              </template>
              <template v-else-if="column.key === 'env'">{{ record.environment_name || '未获取' }}</template>
              <template v-else-if="column.key === 'name'">
                <!-- 数据库类型 logo(20px，与备份/数据源页同口径) + 两行文字：
                     上层数据源名称（最多 15 字符，超出 ... 截断，悬停看全名），下层小字淡灰 IP:port -->
                <div class="cell-name">
                  <img class="db-logo" :src="dbTypeLogo(record.db_type)" :alt="record.db_type || 'db'" />
                  <div class="cell-name-text">
                    <span class="name-main"><DataSourceName :name="record.remark || `${record.hostname}:${record.port}`" :max="15" /></span>
                    <span class="name-sub">{{ record.hostname }}:{{ record.port }}</span>
                  </div>
                </div>
              </template>
            </template>
            </a-table>
          </div>
        <!-- 表体还有未展示内容时给底部渐隐提示：覆盖式滚动条不占位也不常显，
             否则末行被裁一半会被误读成"内容截断且不能滚"。滚到底部自动消失。 -->
        <div v-if="showScrollHint" class="table-scroll-hint"></div>
      </div>
    </div>

    <!-- 风险总览：全系统全部风险实例及其判定原因（数据来自 /monitor/risks） -->
    <a-drawer v-model:open="riskOverviewVisible" title="风险总览" width="600" class="risk-drawer">
      <template #extra>
        <a-space>
          <a-button size="small" :loading="riskLoading" @click="loadRiskOverview()">刷新</a-button>
          <a-button size="small" type="primary" :disabled="!riskOverview.generated_at" @click="riskOverviewVisible = false">关闭</a-button>
        </a-space>
      </template>
      <div v-if="riskLoading && !riskOverview.generated_at" class="placeholder-box">加载中...</div>
      <template v-else-if="riskOverview.generated_at">
        <div class="risk-overview-summary">
          共 <b>{{ riskOverview.total }}</b> 个风险实例、<b>{{ riskOverview.reason_total }}</b> 条风险原因
          <span class="sev-summary">
            <template v-if="riskOverview.by_severity">｜P1 {{ riskOverview.by_severity.P1 ?? 0 }} · P2 {{ riskOverview.by_severity.P2 ?? 0 }} · P3 {{ riskOverview.by_severity.P3 ?? 0 }}</template>
          </span>
          <span class="gen-time">生成于 {{ riskOverview.generated_at || '未获取' }}</span>
        </div>
        <a-collapse v-if="riskOverview.instances.length" class="risk-collapse">
          <a-collapse-panel v-for="item in riskOverview.instances" :key="item.instance_id">
            <template #header>
              <span class="risk-item-header">
                <a-tag :color="levelColor(item.max_severity || '')">{{ item.max_severity || '采集' }}</a-tag>
                <b class="risk-item-name">{{ item.remark || item.target }}</b>
                <span class="risk-item-meta">{{ item.db_type || '未获取' }} · {{ item.reason_count }} 条原因</span>
              </span>
            </template>
            <div class="risk-instance-meta">
              {{ item.target }} ｜ {{ item.environment_name || '未获取' }} ｜
              业务线: {{ item.business || '未获取' }}
            </div>
            <ul class="reason-list">
              <li v-for="(reason, idx) in item.reasons" :key="idx" class="reason-item">
                <div class="reason-line">
                  <a-tag :color="reason.kind === 'alert' ? 'red' : 'orange'" class="reason-kind">
                    {{ reason.kind === 'alert' ? '告警' : '采集' }}
                  </a-tag>
                  <span class="reason-message">{{ operatorTextInText(reason.message) || '未获取' }}</span>
                </div>
                <div class="reason-basis">
                  <template v-if="reason.metric_name">指标: {{ reason.metric_name }}｜</template>
                  判定依据: {{ operatorTextInText(reason.basis) || '未获取' }}
                  <template v-if="reason.kind === 'alert'">｜阈值 {{ reasonValueText(reason, 'threshold') }}｜观测值 {{ reasonValueText(reason, 'observed_value') }}｜已持续 {{ alertDurationText(reason.duration_sec) }}</template>
                </div>
              </li>
            </ul>
            <a-button size="small" type="link" @click="gotoInstanceDetail(item.instance_id)">查看该实例完整监控详情 →</a-button>
          </a-collapse-panel>
        </a-collapse>
        <div v-else class="placeholder-box">当前没有风险实例</div>
      </template>
      <div v-else class="placeholder-box">未获取</div>
    </a-drawer>

    <!-- 「新增告警」下钻：与告警中心共用 AlertDrillModal 组件，格式/交互完全一致；
         按大屏当前时段（后端下发的 range_start/range_end）查触发告警，与卡片数字同口径；
         从图例进入时带 status 参数，只看对应状态的告警（标题随状态联动）。
         时段内可能含已恢复告警：已恢复行禁用勾选（认领只对未恢复有意义） -->
    <AlertDrillModal
      v-model:open="rangeDrill.open"
      :title="rangeDrillTitle"
      :params="rangeDrillParams"
      :selectable="rangeDrill.kind !== 'recovered'"
      :can-manage="canManage"
      disable-recovered
      @view-detail="gotoAlertDetail"
      @view-instance="gotoAlertsOfInstance"
    />

    <!-- 趋势柱下钻：同一弹窗组件，时间用被点柱子自己的起止（口径自洽），
         标题带该段描述（如「9月15日 新增告警列表」）。
         merge-status：窗口内天然含已恢复，所有状态合并一张表展示，
         状态/级别列带下拉筛选、触发时间列带排序（默认降序），不再折叠「已恢复」。
         与告警数卡同规则禁用勾选（已恢复行不可认领） -->
    <AlertDrillModal
      v-model:open="trendDrillState.open"
      :title="trendDrillState.title"
      :params="trendDrillParams"
      selectable
      merge-status
      :can-manage="canManage"
      disable-recovered
      @view-detail="gotoAlertDetail"
      @view-instance="gotoAlertsOfInstance"
    />

    <!-- 风险下钻：复用公共弹窗组件，保证监控大屏与监控明细的交互/样式完全一致 -->
    <InstanceRiskDetailModal
      v-model:visible="riskDetailVisible"
      :risk-detail="riskDetail"
      @goto-detail="gotoInstanceDetail"
    />

    <!-- 实例列表下钻：与告警中心「告警实例总数」共用同一组件/接口，
         scope 圈定实例集合（全部 / 健康 / 风险 / 故障，口径 = 健康分级引擎四分类） -->
    <InstanceDrillModal
      v-model:open="instDrill.open"
      :scope="instDrill.scope"
      @view-detail="gotoInstanceAlerts"
    />
  </div>
</template>

<script setup>
import { getMonitorAlertTrendApi, getMonitorDashboardApi, getMonitorRisksApi, updateMonitorAlertActionApi } from '@/api/monitor'
import { message } from 'ant-design-vue'
import { computed, inject, nextTick, onActivated, onDeactivated, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { FullscreenExitOutlined, FullscreenOutlined, QuestionCircleOutlined } from '@ant-design/icons-vue'
import { useRouter } from 'vue-router'
import { useUserStore } from '@/store/user'
import InstanceRiskDetailModal from '../components/InstanceRiskDetailModal.vue'
// 实例名称统一走该组件：最多 15 个字符，超出 ... 截断，悬停显示全名
import DataSourceName from '@/components/patterns/DataSourceName.vue'
import { dbTypeLogo } from '@/utils/dbTypeLogo'
import InstanceDrillModal from '../components/InstanceDrillModal.vue'
import AlertDrillModal from '../components/AlertDrillModal.vue'
import PageHeader from '../components/PageHeader.vue'
// 方案 A 语义色：圆饼分段颜色一律走 Token，不在本页写死十六进制
import { chartColor, CHART_EMPTY_COLOR, ALERT_STATUS_KEY_BY_BACKEND } from '../chartTokens'
import {
  alertDurationText,
  createDefaultDashboard,
  createDefaultRiskOverview,
  levelColor,
  operatorTextInText,
  reasonValueText,
  screenClassText,
} from '../utils'

defineOptions({ name: 'MonitorDashboardView' })

const router = useRouter()

// 大屏页面固定为一屏、不出现整页滚动条：
// 通过 Layout 预留的 setLayoutContentNoScroll 信号，把内容区切到 overflow:hidden。
// 大屏被 keep-alive 缓存，进出必须走 activated/deactivated，否则离开后其他页面会残留禁滚状态。
const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)
onActivated(() => setLayoutContentNoScroll?.(true))
onDeactivated(() => setLayoutContentNoScroll?.(false))
onMounted(() => setLayoutContentNoScroll?.(true))
onUnmounted(() => setLayoutContentNoScroll?.(false))

// 实例明细表：表体独立滚动（scroll.y），表头固定、分页不受影响。

// 大屏全屏：两层叠加 ——
//   1) 真·浏览器全屏（Fullscreen API）：隐藏地址栏/标签栏/书签栏，效果等同全屏看图；
//   2) 工作区全屏（Layout 的 workspaceFullscreen 机制，与 SQL 工作台同套路）：隐藏侧栏/顶栏、内容区铺满。
// 原生全屏请求失败（iframe 未授权、权限拒绝等）时降级为仅工作区铺满，按钮功能不失效。
// isFullscreen（图标态）= 原生全屏 || 工作区全屏 任一激活。
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

const exitDashboardFullscreen = async () => {
  if (fullscreenEl()) {
    const fn = document.exitFullscreen || document.webkitExitFullscreen
    try {
      await fn.call(document)
    } catch {
      // 忽略退出异常，下方工作区复位照常执行
    }
  }
  workspaceMode.value = false
  setWorkspaceFullscreen?.(false)
}

const toggleDashboardFullscreen = () => {
  if (isFullscreen.value) {
    exitDashboardFullscreen()
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
// 避免“已退出全屏但侧栏还没回来”的半态。
const onFullscreenChange = () => {
  isNativeFullscreen.value = !!fullscreenEl()
  if (!isNativeFullscreen.value && workspaceMode.value) {
    workspaceMode.value = false
    setWorkspaceFullscreen?.(false)
  }
}
const onGlobalKeydown = (event) => {
  // 原生全屏下浏览器自己处理 Esc；这里只兜底“降级为工作区铺满”的场景
  if (event.key === 'Escape' && !isNativeFullscreen.value) exitDashboardFullscreen()
}
onMounted(() => {
  document.addEventListener('fullscreenchange', onFullscreenChange)
  document.addEventListener('webkitfullscreenchange', onFullscreenChange)
  window.addEventListener('keydown', onGlobalKeydown)
})
onUnmounted(() => {
  document.removeEventListener('fullscreenchange', onFullscreenChange)
  document.removeEventListener('webkitfullscreenchange', onFullscreenChange)
  window.removeEventListener('keydown', onGlobalKeydown)
  exitDashboardFullscreen()
})
onDeactivated(() => exitDashboardFullscreen())

const loading = ref(false)
// 时间范围：值 = 预设窗口的分钟数（'5'/'30'/'60'/'360'/'1440'/'10080'）；
// 'custom' = 自定义起止。统一用分钟表达，后端 range_minutes 也认分钟。
const rangeMode = ref('30')
const customRange = ref([])
const rangeOptions = [
  { value: '5', label: '最近5分钟' },
  { value: '30', label: '最近30分钟' },
  { value: '60', label: '最近1小时' },
  { value: '360', label: '最近6小时' },
  { value: '1440', label: '最近1天' },
  { value: '10080', label: '最近7天' },
  { value: 'custom', label: '自定义' },
]
const filterBusiness = ref(undefined)
const filterEnvironment = ref(undefined)
const alertTab = ref('firing')
const actionLoading = ref('')

// 告警事件中心：三个 tab 各自分页，每页固定 5 条；页码随 tab 独立，切换 tab 回到第 1 页。
const ALERT_PAGE_SIZE = 5
const firingPage = ref(1)
const ackedPage = ref(1)
const recoveredPage = ref(1)

watch(alertTab, () => {
  firingPage.value = 1
  ackedPage.value = 1
  recoveredPage.value = 1
})

const dashboardState = ref(createDefaultDashboard())
const dashboard = computed(() => dashboardState.value || createDefaultDashboard())
const screen = computed(() => dashboard.value.screen || createDefaultDashboard().screen)
const instances = computed(() => dashboard.value.instances || [])

// 区间文案统一取后端返回值（后端按真实窗口生成），前端不再自行拼接，
// 避免"页面写最近1小时、实际按30分钟算"的错位。
const rangeLabelText = computed(() => screen.value.range_label || '未获取')

const businessOptions = computed(() => dashboard.value.filters.businesses || [])
const environmentOptions = computed(() => dashboard.value.filters.environments || [])

// ---------------------------------------------------------------------------
// 顶部条：实时状态灯。口径 = 后台采集无失败且无滞后（Prometheus 未配置不影响实时性，
// 引擎直连采集照常工作）。
// ---------------------------------------------------------------------------
const liveState = computed(() => {
  const collect = dashboard.value.collect || {}
  if ((collect.failed || 0) === 0 && (collect.stale_or_never || 0) === 0) return 'ok'
  return 'warn'
})

// ---------------------------------------------------------------------------
// 环形图：四分类占比来自后端 screen.status（真实状态归类），没有任何一段时画灰色空环。
// ---------------------------------------------------------------------------
const donutSegments = computed(() => {
  const status = screen.value.status || {}
  // 配色取自语义 Token（交通灯语义：健康绿 / 风险琥珀 / 故障深红 / 未采集灰），
  // 与右侧告警圆饼使用不同色相空间，避免相邻两图撞色。
  const colorOf = (key) => chartColor('instance', key)
  const entries = [
    ['healthy', status.healthy || 0],
    ['risk', status.risk || 0],
    ['fault', status.fault || 0],
    ['uncollected', status.uncollected || 0],
  ].filter(([, count]) => count > 0)
  const total = entries.reduce((sum, [, count]) => sum + count, 0)
  const circumference = 2 * Math.PI * 48
  // 相邻两段的拼接处各有一条抗锯齿过渡边，两条半透明边缘叠在一起会呈现
  // 毛边/发丝缝（视觉上像两块颜色拼接错位）。让每段向前多画 OVERLAP
  // （≈1px），下一段的「起笔直边」画在上一段的「收笔」之上，拼接处只剩
  // 一条干净的直切边界；末段的溢出量绕回盖住首段起点，12 点位闭环同样锐利。
  // dash 上限取整圈：单段占满时 length+OVERLAP 会超出，gap 不能为负值。
  const OVERLAP = 1.2
  let offset = 0
  const segments = entries.map(([key, count]) => {
    const length = (count / total) * circumference
    const dash = Math.min(length + OVERLAP, circumference)
    const segment = {
      color: colorOf(key),
      dasharray: `${dash} ${circumference - dash}`,
      dashoffset: -offset,
    }
    offset += length
    return segment
  })
  // 四类都为 0：画灰色空环，明确表达「无数据」而不是照画一段
  if (!segments.length) {
    segments.push({ color: CHART_EMPTY_COLOR, dasharray: `${circumference} 0`, dashoffset: 0 })
  }
  return segments
})

const prevDeltaTooltip = computed(() => {
  const prev = screen.value.status_prev
  if (!prev) return `环比快照未获取：上一时段（${rangeLabelText.value}）的历史状态不可判定`
  const format = (current, before) => {
    const delta = current - before
    return delta > 0 ? `+${delta}` : String(delta)
  }
  const current = screen.value.status
  return `较上一时段（${rangeLabelText.value}）：健康 ${format(current.healthy, prev.healthy)}、风险 ${format(current.risk, prev.risk)}、故障 ${format(current.fault, prev.fault)}`
})

// ---------------------------------------------------------------------------
// 风险展示：TOP5 默认露出（screen.risk_top）+ 风险总览抽屉（/monitor/risks 全量）
// + 单实例下钻弹窗（为什么被判为风险：原因 + 判定依据）。
// 口径与后端 classifyScreenCard 严格一致，前端不做二次归类。
// ---------------------------------------------------------------------------
const riskTop = computed(() => screen.value.risk_top || [])

const riskOverviewVisible = ref(false)
const riskOverview = ref(createDefaultRiskOverview())
const riskLoading = ref(false)
const riskDetailVisible = ref(false)
const riskDetail = ref(null)


const loadRiskOverview = async () => {
  riskLoading.value = true
  try {
    const res = await getMonitorRisksApi()
    // get() 返回 {code, data, message} 信封，真实数据在 res.data
    riskOverview.value = res?.data || createDefaultRiskOverview()
    return riskOverview.value
  } catch {
    // 错误提示由请求层统一弹出；保留旧数据
    return null
  } finally {
    riskLoading.value = false
  }
}

// 风险总览抽屉：卡片上的「风险总览 →」入口已按需求移除，抽屉本体与加载逻辑保留
// （loadRiskOverview 仍被风险 chip 的单实例下钻复用）；将来要重新开放入口，
// 只需把 riskOverviewVisible 置 true 并调用 loadRiskOverview()。

// openRiskDetail 单实例下钻。优先用 dashboard 自带的 risk_top（含完整原因），
// 不在其中再拉风险总览接口补全；任何来源都只是服务端给过的真实数据。
const openRiskDetail = async (instanceId) => {
  riskDetailVisible.value = true
  riskDetail.value = null
  let item = riskTop.value.find((i) => i.instance_id === instanceId)
  if (!item) {
    let overview = riskOverview.value
    if (!overview.generated_at) overview = (await loadRiskOverview()) || overview
    item = (overview.instances || []).find((i) => i.instance_id === instanceId)
  }
  riskDetail.value = item || null
}

const gotoInstanceDetail = (instanceId) => {
  riskDetailVisible.value = false
  riskOverviewVisible.value = false
  router.push({ path: '/monitor/detail', query: { instance_id: instanceId } })
}

// ---------- 实例列表下钻（总实例 / 健康实例 / 风险实例 / 故障实例卡片数字） ----------
// 与告警中心「告警实例总数」共用 InstanceDrillModal 组件与后端接口；
// scope=healthy/risk/fault/all 由后端健康分级引擎圈定实例集合，与大屏四分类同口径。
const instDrill = reactive({ open: false, scope: 'all' })
const openInstanceDrill = (scope) => {
  instDrill.scope = scope
  instDrill.open = true
}
// 操作列「查看该实例告警明细」：跳告警中心历史 Tab 并带实例过滤（router 内部跳转，query 不丢）
const gotoInstanceAlerts = (record) => {
  instDrill.open = false
  router.push({ path: '/monitor/alerts', query: { tab: 'history', instance: record.instance_id } })
}

// ---------- 「告警数」环形图与下钻（共用 AlertDrillModal，格式与告警中心弹窗一致） ----------
// 口径（后端 screen.alert_counts 统一下发，前端不做二次归类）：
//   待处理 / 处理中 —— 实时快照，与当前时间范围无关（同告警事件中心两个 Tab 的来源）；
//   已解决 —— 当前时间范围内（按恢复时间过滤）。
// 圆心总数 = 待处理 + 处理中 = 当前未恢复告警数，因此点击时用 unresolved（快照语义）。
const userStore = useUserStore()
const canManage = computed(() => (userStore.permissions || []).includes('monitor.manage'))
// kind 为空 = 圆心（当前告警，快照）；active/acknowledged/recovered = 图例分状态入口
const rangeDrill = reactive({ open: false, kind: '' })
const alertCounts = computed(() => {
  const raw = screen.value.alert_counts || {}
  return { pending: raw.pending || 0, processing: raw.processing || 0, resolved: raw.resolved || 0 }
})
const alertPendingTotal = computed(() => alertCounts.value.pending + alertCounts.value.processing)
// 环形图分段：与第一张卡同一画法（svg stroke-dasharray）。
// 配色按业务紧迫度走暖色（待认领红 / 处理中橙 / 已恢复绿），色值见 chartTokens 的
// ALERT_STATUS_COLORS —— 圆饼、图例、事件中心 tab、告警卡状态条全站同一真源。
const alertDonutSegments = computed(() => {
  const counts = alertCounts.value
  // 后端三段（active/acknowledged/recovered）先归一到 Token 的 key，再取色
  const colorOf = (backendStatus) => chartColor('alert', ALERT_STATUS_KEY_BY_BACKEND[backendStatus])
  const entries = [
    ['active', counts.pending],
    ['acknowledged', counts.processing],
    ['recovered', counts.resolved],
  ].filter(([, count]) => count > 0)
  const total = entries.reduce((sum, [, count]) => sum + count, 0)
  const circumference = 2 * Math.PI * 48
  // 相邻两段的拼接处各有一条抗锯齿过渡边，两条半透明边缘叠在一起会呈现
  // 毛边/发丝缝（视觉上像两块颜色拼接错位）。让每段向前多画 OVERLAP
  // （≈1px），下一段的「起笔直边」画在上一段的「收笔」之上，拼接处只剩
  // 一条干净的直切边界；末段的溢出量绕回盖住首段起点，12 点位闭环同样锐利。
  // dash 上限取整圈：单段占满时 length+OVERLAP 会超出，gap 不能为负值。
  const OVERLAP = 1.2
  let offset = 0
  const segments = entries.map(([key, count]) => {
    const length = (count / total) * circumference
    const dash = Math.min(length + OVERLAP, circumference)
    const segment = {
      color: colorOf(key),
      dasharray: `${dash} ${circumference - dash}`,
      dashoffset: -offset,
    }
    offset += length
    return segment
  })
  // 三类都为 0：画灰色空环，与第一张卡「无数据」表现一致
  if (!segments.length) {
    segments.push({ color: CHART_EMPTY_COLOR, dasharray: `${circumference} 0`, dashoffset: 0 })
  }
  return segments
})
// 下钻参数按口径分流：
//   圆心 → unresolved=true（未恢复快照 = active + pending + acknowledged，与圆心数字同口径）；
//   待认领 → unclaimed=true（pending 观察期 + active 已转正未认领，与图例数字同口径）；
//   处理中 → status=acknowledged 快照过滤，不带时间边界；
//   已解决 → status=recovered + 页面时间范围（按恢复时间过滤，与图例口径一致）。
const rangeDrillParams = computed(() => {
  const kind = rangeDrill.kind
  // exclude_ignored：与圆饼计数（后端 ignored = false）同口径，否则弹窗会比圆饼多出已忽略的行
  // 待认领用 unclaimed（不是 status=active）：图例数字含观察期 pending，
  // 用 status=active 会被后端精确匹配收窄，出现"图例 3 条、点开 0 条"。
  if (kind === 'active') return { unclaimed: true, exclude_ignored: true }
  if (kind === 'acknowledged') return { status: 'acknowledged', exclude_ignored: true }
  if (kind === 'recovered') {
    const start = screen.value.range_start
    const end = screen.value.range_end
    return start && end ? { status: 'recovered', start_time: start, end_time: end } : { status: 'recovered' }
  }
  return { unresolved: true }
})
// 弹窗标题随入口联动：圆心=当前告警（快照），图例=对应状态
const RANGE_STATUS_TITLES = {
  active: '待认领告警列表',
  acknowledged: '处理中告警列表',
  recovered: '已恢复告警列表',
}
const rangeDrillTitle = computed(() => RANGE_STATUS_TITLES[rangeDrill.kind] || '当前告警列表')
const openRangeDrill = (kind = '') => {
  const target = typeof kind === 'string' ? kind : ''
  // 已解决走时间范围过滤：拿不到时段就不能用错误的窗口出数
  if (target === 'recovered' && (!screen.value.range_start || !screen.value.range_end)) {
    message.warning('时间范围未获取，暂不能查看明细')
    return
  }
  rangeDrill.kind = target
  rangeDrill.open = true
}
// 详情 → 告警中心历史 Tab 按告警ID过滤；实例名 → 历史 Tab 按实例过滤
const gotoAlertDetail = (record) => {
  rangeDrill.open = false
  router.push({ path: '/monitor/alerts', query: { tab: 'history', kw: record.alert_id } })
}
const gotoAlertsOfInstance = (record) => {
  rangeDrill.open = false
  router.push({ path: '/monitor/alerts', query: { tab: 'history', instance: record.instance_id } })
}

// ---------------------------------------------------------------------------
// 近 7 天告警趋势卡：时间刻意与页面顶部范围解耦（全局管实时、本卡管中长期），
// 只接受预设窗口（今日 / 近3天 / 近7天 / 近14天，时间写进卡标题声明归属）。
// 柱 = 每段新增告警数，后端按 COALESCE(first_seen_at, triggered_at) 落桶并做
// 模板门控，口径与告警数卡一致；点柱子下钻该段告警列表（标题自带该段时间）。
// ---------------------------------------------------------------------------
const TREND_PRESETS = [
  { key: 'today', label: '今日' },
  { key: '3d', label: '近3天' },
  { key: '7d', label: '近7天' },
  { key: '14d', label: '近14天' },
]
// 时间写进卡标题，声明口径归属（与顶部全局范围区分）
const TREND_TITLES = { today: '今日告警趋势', '3d': '近3天告警趋势', '7d': '近7天告警趋势', '14d': '近14天告警趋势' }
const trendPreset = ref('7d')
const trendLoading = ref(false)
const trendPoints = ref([])
const trendTitle = computed(() => TREND_TITLES[trendPreset.value] || '近7天告警趋势')
const loadAlertTrend = async () => {
  trendLoading.value = true
  try {
    const res = await getMonitorAlertTrendApi({ preset: trendPreset.value })
    trendPoints.value = res?.data?.points || []
  } catch {
    // 错误提示由请求层统一弹出；保留旧数据
  } finally {
    trendLoading.value = false
  }
}
const setTrendPreset = (key) => {
  if (key === trendPreset.value) return
  trendPreset.value = key
  loadAlertTrend()
}
const trendMax = computed(() => Math.max(1, ...trendPoints.value.map((p) => p.count)))
const trendBarHeight = (pt) => {
  if (pt.count <= 0) return '2px'
  return `${Math.max(6, (pt.count / trendMax.value) * 100)}%`
}
// 二分堆叠：未恢复段占柱体的百分比（分母 = 该段新增总数，保证与总柱高一致）
const trendUnresolvedPct = (pt) => {
  if (!pt.count || !(pt.unresolved_count > 0)) return '0%'
  return `${Math.round((pt.unresolved_count / pt.count) * 10000) / 100}%`
}
// 柱面 tooltip：总数 + 未恢复/已恢复拆分（拆分即柱内两段颜色各自代表的数量）
const trendColTitle = (pt) => {
  if (pt.count <= 0) return `${pt.drill_label} · 新增 0 条，点击可查看空状态`
  const un = pt.unresolved_count || 0
  return `${pt.drill_label} · 新增 ${pt.count} 条（未恢复 ${un} / 已恢复 ${pt.count - un}），点击查看明细`
}
// x 轴标签稀疏化：柱数多（今日按小时 24 柱）时隔 3 根显示一个，末柱必显
const trendLabelVisible = (i) => trendPoints.value.length <= 14 || i % 3 === 0 || i === trendPoints.value.length - 1

// 趋势柱下钻：用该柱自己的时间段过滤（口径自洽），标题带上该段描述
const trendDrillState = reactive({ open: false, start: '', end: '', title: '' })
// params 用 ref 显式维护，避免 computed 在 open 变化时重新回退到 {}。
// openTrendDrill 里一次性写入新对象，保证子组件拿到的是完整参数。
const trendDrillParams = ref({})
const openTrendDrill = async (pt) => {
  trendDrillState.start = pt.start
  trendDrillState.end = pt.end
  trendDrillState.title = `${pt.drill_label} 新增告警列表`
  trendDrillParams.value = {
    start_time: pt.start,
    end_time: pt.end,
    first_seen: true,
  }
  // 必须等 params 同步到子组件后再打开弹窗，
  // 否则 AlertDrillModal 的 watch open 触发 load 时，读到的还是旧的 {}。
  await nextTick()
  trendDrillState.open = true
}

// ---------------------------------------------------------------------------
// 告警事件中心：待认领（含观察中）、处理中（已认领）、已解决（区间内恢复）。
// ---------------------------------------------------------------------------
// 卡片统一按「第一次告警时间」倒序：后发生的排前面，先发生的排后面。
const alertFirstTime = (alert) => String(alert?.first_seen_at || alert?.triggered_at || '')
const sortAlertsByFirstTimeDesc = (list) =>
  [...list].sort((a, b) => alertFirstTime(b).localeCompare(alertFirstTime(a)))

const firingAlerts = computed(() =>
  sortAlertsByFirstTimeDesc(
    (dashboard.value.alerts || []).filter((alert) => {
      // 按原始状态码判断（active=待认领 / pending=观察中），不依赖中文展示词，
      // 避免文案调整时这里漏改导致告警归错 Tab。status 为空视为仍在告警中。
      const key = alert.status_key || alert.status
      return !key || key === 'active' || key === 'pending'
    })
  )
)
const ackedAlerts = computed(() => sortAlertsByFirstTimeDesc(screen.value.acked_alerts || []))
const recoveredAlerts = computed(() => sortAlertsByFirstTimeDesc(screen.value.recovered_alerts || []))

const pagedFiringAlerts = computed(() => {
  const start = (firingPage.value - 1) * ALERT_PAGE_SIZE
  return firingAlerts.value.slice(start, start + ALERT_PAGE_SIZE)
})
const pagedAckedAlerts = computed(() => {
  const start = (ackedPage.value - 1) * ALERT_PAGE_SIZE
  return ackedAlerts.value.slice(start, start + ALERT_PAGE_SIZE)
})
const pagedRecoveredAlerts = computed(() => {
  const start = (recoveredPage.value - 1) * ALERT_PAGE_SIZE
  return recoveredAlerts.value.slice(start, start + ALERT_PAGE_SIZE)
})

const firingTotalPages = computed(() => Math.max(1, Math.ceil(firingAlerts.value.length / ALERT_PAGE_SIZE)))
const ackedTotalPages = computed(() => Math.max(1, Math.ceil(ackedAlerts.value.length / ALERT_PAGE_SIZE)))
const recoveredTotalPages = computed(() => Math.max(1, Math.ceil(recoveredAlerts.value.length / ALERT_PAGE_SIZE)))

// 完整展示到秒：yyyy-mm-dd hh24:mi:ss（后端形如 "2026-09-21 15:30:27"）
const alertTime = (alert) => {
  const raw = alertFirstTime(alert)
  return raw.length >= 19 ? raw.slice(0, 19) : raw || '未获取'
}

// ---------------------------------------------------------------------------
// 数据源展示：告警卡数据源行只保留名称/地址/环境（类型小图标已按需求移除），
// 字段由后端随告警下发（instance_name / instance_target / db_type / environment_name），
// 前端只做展示与降级，绝不在前端二次关联实例列表。
// ---------------------------------------------------------------------------
// 名称优先备注（业务名），没有再退到 hostname:port，最后退到 instance_id
const dsNameOf = (alert) => alert?.instance_name || alert?.instance_target || alert?.instance || '未获取'
const dsAddrOf = (alert) => alert?.instance_target || alert?.instance || ''

const handleAlertAction = async (alert, action) => {
  actionLoading.value = alert.row_key
  try {
    await updateMonitorAlertActionApi({ fingerprint: alert.row_key, action })
    message.success(action === 'ack' ? '已认领，告警转入处理中' : action === 'ignore' ? '已忽略该告警' : '已取消忽略')
    await reload(true)
  } catch {
    // 错误提示由请求层统一弹出
  } finally {
    actionLoading.value = ''
  }
}

// ---------------------------------------------------------------------------
// 实例明细列表：异常信息有告警时统一提示「有 N 个告警，请及时处理。」，
// 无告警时降级到状态说明（如「部分指标不可用」）。
// ---------------------------------------------------------------------------
// 已忽略的告警不再计入，统计时与后端 loadActiveEventMap 口径保持一致。
const alertCountOf = (record) => (record.alerts || []).filter((item) => !item.ignored).length
const issueOf = (record) => {
  // 有告警优先给数量提示（N 为未忽略告警数）；无告警但实例非健康时仍保留状态说明
  const alertTotal = alertCountOf(record)
  if (alertTotal > 0) return `有 ${alertTotal} 个告警，请及时处理。`
  if (record.screen_class !== 'healthy' && (record.status_message || '').trim()) return record.status_message
  return ''
}

// 异常信息前导圆点的配色：只给「非健康」上色，健康/未采集用中性灰。
// 文字本身始终用正文色，颜色只落在这 6px 的小圆点上 —— 既保留风险提示，
// 又不会让整列出现大面积红/橙字。色值与健康四分类 Token 同源。
const issueDotClass = (record) => {
  const cls = String(record?.screen_class || '')
  if (cls === 'fault') return 'dot-fault'
  if (cls === 'risk') return 'dot-risk'
  return 'dot-normal'
}

// 实例明细「状态」列文字色：直接取实例四分类语义色（与第一张圆饼同源，chartColor），
// 未知分类兜底「未采集」灰，保证文字始终可读。
// 「健康分」列复用同一函数 → 两列同行颜色一致（健康绿 / 风险琥珀 / 故障深红 / 未采集灰）。
const statusTextColor = (screenClass) => (
  ['healthy', 'risk', 'fault', 'uncollected'].includes(screenClass)
    ? chartColor('instance', screenClass)
    : '#888780'
)

// 健康分展示兜底：后端算分时已做 0 分下限截断，这里再夹一层。
// 列表是扫读场景，负分没有任何解释力，只会让人怀疑系统算错；
// 扣分详情与触底标记在监控明细页解释，大屏不重复承载。
const healthScoreText = (value) => {
  const num = Number(value)
  if (!Number.isFinite(num)) return '—'
  return String(Math.max(0, num))
}

// 大屏实例明细只呈现需要关注的两类实例：故障（fault）与有风险（risk），
// 健康/未采集不进入列表；数量多时靠表体内部滚动，不再分页。
// 排序：按健康分升序，分数最低（最需要关注的异常实例）排最前，
// 同分保持后端原有顺序（sort 稳定）。仅影响展示顺序。
const filteredInstances = computed(() =>
  [...instances.value]
    .filter((r) => r.screen_class === 'fault' || r.screen_class === 'risk')
    .sort((a, b) => (a.health_score ?? Infinity) - (b.health_score ?? Infinity))
)

// 表体内部滚动提示：实例多时列表在卡内滚动（不分页、不截断），
// 但 macOS / 部分 Chrome 用覆盖式滚动条，不占位也不常显，末行被裁一半会被误判成"不能滚"。
// 这里在卡底叠一条渐隐带，提示下方还有内容；滚到底部或不需要滚动时自动隐藏。
const tableWrapRef = ref(null)
const showScrollHint = ref(false)
const updateScrollHint = () => {
  const el = tableWrapRef.value
  if (!el) return
  showScrollHint.value = el.scrollHeight - el.scrollTop - el.clientHeight > 4
}
watch([instances, loading], () => nextTick(updateScrollHint))
onActivated(() => nextTick(updateScrollHint))
onMounted(() => window.addEventListener('resize', updateScrollHint))
onUnmounted(() => window.removeEventListener('resize', updateScrollHint))

// 五列宽度按内容特征分配：状态/健康分内容短，收窄给纯标签与数字；
// 异常信息是长句占最大份额并允许换行完整展示（见 .issue-text），
// 合计 100% 不产生横向滚动。类型列已删除，数据库类型 logo 内嵌到数据源名称前。
const tableColumns = computed(() => [
  { title: '异常数据源', key: 'name', dataIndex: 'instance_id', width: '26%' },
  { title: '环境', key: 'env', width: '10%' },
  { title: '状态', key: 'status', width: '12%' },
  { title: '健康分', key: 'health_score', width: '10%' },
  { title: '异常信息', key: 'issue', width: '30%' },
])

const customRow = (record) => ({
  class: 'instance-row',
  onClick: () => {
    // 风险行下钻到风险原因明细；其余状态维持原跳转（完整监控详情页）
    if (record.screen_class === 'risk') {
      openRiskDetail(record.instance_id)
      return
    }
    router.push({ path: '/monitor/detail', query: { instance_id: record.instance_id } })
  },
})

// ---------------------------------------------------------------------------
// 时间范围交互：下拉框切换预设 / 自定义；自定义时用起止选择器指定任意区间。
// 自定义区间固定不变（不会随 30s 自刷新漂移），方便"回看某一段"时对着看。
// ---------------------------------------------------------------------------
const pad2 = (value) => String(value).padStart(2, '0')

const formatDateTime = (date) =>
  `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())} ` +
  `${pad2(date.getHours())}:${pad2(date.getMinutes())}:${pad2(date.getSeconds())}`

// 不允许选未来时间：未来没有采集数据，选了只会得到空窗口
const disableFutureDate = (current) => !!current && current.valueOf() > Date.now()

const rangeParams = () => {
  if (rangeMode.value === 'custom') {
    const [start, end] = customRange.value || []
    return { start_time: start || undefined, end_time: end || undefined }
  }
  return { range_minutes: Number(rangeMode.value) || 30 }
}

const handleRangeModeChange = (value) => {
  if (value === 'custom' && (!customRange.value || customRange.value.length !== 2)) {
    // 默认给"最近1小时"，用户可直接改；不是模拟数据，只是选择器初值
    const end = new Date()
    const start = new Date(end.getTime() - 60 * 60 * 1000)
    customRange.value = [formatDateTime(start), formatDateTime(end)]
  }
  reload()
}

const handleCustomRangeChange = (value) => {
  if (value && value.length === 2 && value[0] && value[1]) {
    reload()
  }
}

// ---------------------------------------------------------------------------
// 数据加载：区间 / 业务线 / 机房都是服务端过滤；30s 静默自刷新。
// ---------------------------------------------------------------------------
const reload = async (silent = false) => {
  if (!silent) loading.value = true
  // 趋势卡自带刷新：时间范围与全局解耦（preset 不变，仅数据更新），
  // 跟着同一节奏刷新但消费的是自己的接口
  loadAlertTrend()
  try {
    const res = await getMonitorDashboardApi({
      environment: filterEnvironment.value || undefined,
      business: filterBusiness.value || undefined,
      ...rangeParams(),
    })
    // get() 解析出的是 {code, data, message} 信封，真实数据在 res.data
    dashboardState.value = res?.data || createDefaultDashboard()
  } catch {
    // 错误提示由请求层统一弹出；保留旧数据避免大屏闪空
  } finally {
    loading.value = false
  }
}

let refreshTimer = null
onMounted(() => {
  reload()
  refreshTimer = setInterval(() => reload(true), 30000)
})
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>

<style scoped>
.monitor-page {
  display: flex;
  flex-direction: column;
  /* 与监控告警下其它二级页面统一：头部与内容区间距同为 12px */
  gap: var(--gi-spacing-ssm);
  /* 一屏自适应：占满内容区高度，超出部分交给内部滚动区，页面本身不产生滚动条 */
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

/* 全屏状态（原生 Fullscreen API / 工作区铺满）下，顶部内容贴边显得压抑，
   给 .monitor-page 增加顶部安全留白。非全屏时由 .layout-content 的 padding 提供间距，
   这里保持 0，避免重复。box-sizing 保证 padding 不撑破 100% 高度、不产生滚动条。 */
.monitor-page.is-fullscreen {
  padding-top: var(--gi-spacing-lg, 24px);
  box-sizing: border-box;
}

/* ----- 顶部条 ----- */
/* 标题左侧呼吸空间：全屏态 .monitor-page 无水平内边距，标题会贴住屏幕左缘；
   常规布局下也让标题与下方卡片的内边距观感对齐。只作用于本页（PageHeader 为 5 个页面共享）。 */
.monitor-page :deep(.page-header-title) {
  padding-left: 8px;
}

.range-select {
  width: 130px;
  margin-left: 4px;
}

.range-picker {
  width: 330px;
}

.filter-select {
  width: 150px;
}

.screen-clock {
  font-size: 13px;
  color: #6d8290;
  font-variant-numeric: tabular-nums;
}

.live-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: #6d8290;
}

.live-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #0f9f6f;
  box-shadow: 0 0 0 3px rgba(15, 159, 111, 0.18);
}

.live-badge.warn .live-dot {
  background: #d97706;
  box-shadow: 0 0 0 3px rgba(217, 119, 6, 0.18);
}

/* 全屏切换：轻量化文字按钮，主题色，不做重边框/底色 */
.fullscreen-btn {
  color: var(--gi-color-primary, #2457f5);
}

.fullscreen-btn:hover {
  color: var(--gi-color-primary, #2457f5);
  background: color-mix(in srgb, var(--gi-color-primary, #2457f5) 10%, transparent);
}

/* ----- 卡片基底 ----- */
.screen-card {
  position: relative;
  background: #ffffff;
  border-radius: 22px;
  box-shadow: 0 10px 24px rgba(18, 58, 72, 0.06);
  padding: 16px 18px;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

.card-title {
  font-size: 14px;
  font-weight: 700;
  color: #163440;
  padding-left: 10px;
  /* 蓝色竖线装饰已按要求移除 */
  line-height: 1.2;
}

.card-note {
  font-size: 12px;
  color: #6d8290;
}

.placeholder-box {
  min-height: 96px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #9aa8b8;
  font-size: 13px;
  border: 1px dashed #d8e0ea;
  border-radius: 14px;
}

/* ----- 汇总行 ----- */
/* 5 张卡严格等宽、间距统一：minmax(0, 1fr) 保证每列按 1fr 等分且不被内容 min-content
   撑开，justify-content:center 在列总宽小于容器时把整行水平居中——两侧不偏向任何一边。 */
.summary-row {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  justify-content: center;
  gap: 12px;
}

/* fr 列的默认最小值是 min-content：卡内图例会把列撑宽，破坏"等宽居中"。
   显式 min-width:0 让五列严格等分；内容越界由图例自身收缩兜底。 */
.summary-row > .screen-card {
  min-width: 0;
}

.donut-card {
  display: flex;
  align-items: center;
  /* 环图与图例的间距：14px 紧凑排布（原 18px 偏松，窄卡下挤压图例） */
  gap: 14px;
}

.donut-wrap {
  position: relative;
  /* 汇总行整体压缩：圆环 116 → 100，减少卡内上下留白 */
  width: 100px;
  height: 100px;
  flex: none;
}

.donut-svg {
  width: 100%;
  height: 100%;
}

.donut-center {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}

.donut-total {
  font-size: 23px;
  font-weight: 700;
  color: #163440;
  line-height: 1.1;
}

.donut-label {
  font-size: 12px;
  color: #6d8290;
}

.donut-legend {
  /* 统一图例行内所有间隔：色点↔状态文字、状态文字↔数字，全部使用同一变量。
     这样【总实例】与【告警数】两张卡片的「状态-数字」间距完全一致。 */
  --legend-gap: 6px;
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 7px;
  font-size: 13px;
  color: #44586a;
}

.donut-legend li {
  display: flex;
  align-items: center;
  gap: var(--legend-gap);
  flex-wrap: nowrap;
}

/* 紧凑排版：色点 + 状态文字 + 数字三段紧挨，整行左对齐，数字不再用
   margin-left:auto 推到行尾 —— 窄卡（汇总行五等分列）下不再出现
   「文字左、数字右」中间大片空白，也不会挤压换行。两张卡共用本规则，风格天然统一。 */
.donut-legend .legend-text {
  display: flex;
  align-items: center;
  gap: var(--legend-gap);
  flex: none;
  white-space: nowrap;
}

.donut-legend .legend-label {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.donut-legend .legend-value {
  font-style: normal;
  font-weight: 700;
  color: #163440;
  font-variant-numeric: tabular-nums;
  flex: none;
  white-space: nowrap;
}

/* 图例行可点击下钻：与右侧指标卡数字同语义（悬停变色 + 手型） */
.donut-legend li.legend-link {
  cursor: pointer;
  transition: color 0.2s;
}

.donut-legend li.legend-link:hover,
.donut-legend li.legend-link:hover .legend-value {
  color: #2457f5;
}

.donut-legend .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}

/* ----- 方案 A 语义色：图例圆点 -----
   色值统一取 --chart-*（由 chartTokens.js 在启动时注入到 :root），
   var() 的第二个参数是兜底色，即使注入未生效也不会丢色。
   左侧用实例健康四分类（交通灯语义），右侧用告警处理三态（工单流冷色系），
   两套色相不同，相邻圆饼图不再撞色。 */
.dot-healthy {
  background: var(--chart-instance-healthy, #0f9f6f);
}

.dot-risk {
  background: var(--chart-instance-risk, #ef9f27);
}

.dot-fault {
  background: var(--chart-instance-fault, #b3261e);
}

.dot-uncollected {
  background: var(--chart-instance-uncollected, #888780);
}

.dot-pending {
  background: var(--chart-alert-pending, #b3261e);
}

.dot-doing {
  background: var(--chart-alert-doing, #ef9f27);
}

.dot-resolved {
  background: var(--chart-alert-resolved, #0f9f6f);
}

/* 告警数卡：图例列。完全继承通用 .donut-legend 的间距、字号与单行布局；
   仅保留 flex 列与 min-width:0，避免图例把卡片撑破。 */
.alert-donut-card .donut-legend-wrap {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-width: 0;
}

.alert-donut-card .donut-legend {
  min-width: 0;
}

.stat-card {
  /* 汇总行二次压缩（累计 -40px 量级）：上下内边距 10 → 6，左侧色条留位 22 → 20。
     多出的空间由下方 screen-grid 的 flex:1 自动吃掉。 */
  padding: 6px 16px 6px 20px;
  /* 健康 / 风险 / 故障三张实例卡：标题、数值、说明文字统一水平居中
     （左侧色条是 absolute 定位，不参与文本流，不受影响）。 */
  text-align: center;
  /* 垂直居中：卡片是 grid 项、会被拉伸到整行高度，内容少的卡（健康 / 故障）原本顶在
     上方、底部留白。改 flex 列 + justify-content:center 后，内容块整体在卡内垂直居中；
     内容高度变化（风险卡 chips 增减、说明文字有无）时仍自动保持居中。
     子项沿交叉轴 stretch 撑满卡宽，配合上面的 text-align:center 保持水平居中。 */
  display: flex;
  flex-direction: column;
  justify-content: center;
}

.stat-card::before {
  content: '';
  position: absolute;
  left: 0;
  top: 8px;
  bottom: 8px;
  width: 4px;
  border-radius: 0 4px 4px 0;
}

/* 实例卡左侧色条：与健康四分类 Token 同源（图例圆点 / 圆饼扇区 / 色条三处一致）。 */
.stat-card.tone-green::before {
  background: var(--chart-instance-healthy, #0f9f6f);
}

.stat-card.tone-orange::before {
  background: var(--chart-instance-risk, #ef9f27);
}

.stat-card.tone-red::before {
  background: var(--chart-instance-fault, #b3261e);
}

.stat-card.tone-purple::before {
  background: #7c3aed;
}

/* 三张实例状态卡的数字与状态同色（色值 = 各卡左侧色条 / 图例圆点），
   实现「数字颜色 = 状态颜色」的一一对应；未指定的卡（如告警卡）维持默认深色。 */
.stat-card.tone-green .stat-value {
  color: var(--chart-instance-healthy, #0f9f6f);
}
.stat-card.tone-orange .stat-value {
  color: var(--chart-instance-risk, #ef9f27);
}
.stat-card.tone-red .stat-value {
  color: var(--chart-instance-fault, #b3261e);
}

.stat-title {
  font-size: 13px;
  color: #6d8290;
}

/* 卡片标题内的口径说明图标：点击（也支持悬停）弹出含义说明 */
.title-help-icon {
  margin-left: 6px;
  font-size: 13px;
  color: #9aa8b8;
  cursor: pointer;
  transition: color 0.2s;
}

.title-help-icon:hover {
  color: #2457f5;
}

.stat-value {
  margin-top: 4px;
  font-size: 30px;
  font-weight: 700;
  color: #163440;
  line-height: 1;
  font-variant-numeric: tabular-nums;
}

/* 可下钻数字：与告警中心计数卡的 ov-num-link 同语义（悬停主题色 + 手型） */
.stat-value-link {
  cursor: pointer;
  transition: color 0.2s;
}
.stat-value-link:hover {
  color: #2457f5;
}

/* ----- 中部网格 ----- */
.screen-grid {
  display: grid;
  grid-template-columns: 1fr 1fr 1fr;
  /* 第一行（实例明细列表）目标高度 = 趋势卡原自然高度 257px
     （header 34 + chart min-height 168 + legend 23 + 卡片上下 padding 32）；
     用 minmax(0, 257px) 而不是死写 257px：窗口够高时就是 257px（与趋势卡原高度一致），
     窗口变矮时该行可收缩，由表体内部滚动接纳超出内容，而不是被上层静默裁掉。
     第二行仍为弹性行，由趋势卡吃掉剩余空间。 */
  grid-template-rows: minmax(0, var(--screen-top-row-h, 257px)) minmax(0, 1fr);
  gap: 16px;
  flex: 1;
  min-height: 0;
}

.trend-card {
  /* 与实例明细列表互换位置：趋势卡落到第二行（弹性行），图表随剩余高度自适应拉伸 */
  grid-column: 1 / span 2;
  grid-row: 2;
  /* 纵向 flex：标题行 / 柱状图两段式，图表吃掉剩余高度 */
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.alert-center-card {
  grid-column: 3;
  grid-row: 1 / span 2;
  /* 与 table-card 同样的三段式 flex：标题/tab页签/分页都能固定，
     只有中间列表区滚动，避免分页被挤出可视区。 */
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* 告警事件中心：ant-tabs 占满卡片剩余高度；tab pane 和内容区都 100% 高度，
   这样 .alert-list-wrapper height:100% 才能真正生效。 */
.alert-tabs {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
.alert-tabs :deep(.ant-tabs-content-holder),
.alert-tabs :deep(.ant-tabs-content),
.alert-tabs :deep(.ant-tabs-tabpane) {
  height: 100%;
}
.alert-tabs :deep(.ant-tabs-tabpane) {
  overflow: hidden;
}

/* tab 标签着色：待认领红 / 处理中橙，与右上角告警圆饼、图例圆点同一 token（chartTokens 单一真源）。
   颜色写在内层 span 上（antd 的选中色在 .ant-tabs-tab-btn 上），active/非 active 都保持语义色。 */
.tab-label-firing {
  color: var(--chart-alert-pending, #b3261e);
}

.tab-label-acked {
  color: var(--chart-alert-doing, #ef9f27);
}

.table-card {
  /* 与近7天告警趋势互换位置：实例明细列表占据第一行（趋势卡原位置），
     行高已由 --screen-top-row-h 固定为趋势卡原自然高度，两卡高度严格一致 */
  grid-column: 1 / span 2;
  grid-row: 1;
  /* 卡片改纵向 flex：表头/滚动区/分页三段式，页面本身不动 */
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}

/* 表格滚动容器：占满表头与分页之间的剩余空间；
   自身提供纵向滚动条，而不是依赖 ant-table 内部的 scroll.y，
   避免 tableScrollY 计算失败导致内容被截断。 */
.table-body-wrap {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  /* 实例多时靠这里内部滚动（不分页、不截断）：
     常显细滚动条 + 预留槽位，避免覆盖式滚动条下用户看不出还能滚，
     也避免滚动条出现/消失时列宽跳动。 */
  scrollbar-width: thin;
  scrollbar-color: #c9d5df transparent;
  scrollbar-gutter: stable;
}

.table-body-wrap::-webkit-scrollbar {
  width: 6px;
}

.table-body-wrap::-webkit-scrollbar-thumb {
  background: #c9d5df;
  border-radius: 3px;
}

.table-body-wrap::-webkit-scrollbar-thumb:hover {
  background: #a9bac8;
}

.table-body-wrap::-webkit-scrollbar-track {
  background: transparent;
}

/* 卡头（标题+搜索框）删除后表体整体上移，原先被表头吃掉的高度回补到表体。
   antd 默认行内边距 8px 时行高约 56px，257px 卡高只能容下 3.7 行、末行被裁半行；
   收到 3px 后行高约 46px，可视区（186px）正好容纳 4 整行，实例更多时由内部滚动承接，
   剩余高度仍由 height:100% 链条均摊，不留空白。 */
.table-card :deep(.ant-table.ant-table-small .ant-table-tbody > tr > td) {
  padding-top: 3px;
  padding-bottom: 3px;
}

/* 行数不足时表格铺满卡片：把 ant-table 内部容器链条高度打通到 100%，
   table 元素拿到确定高度后浏览器会把多余高度均摊到各行（行数多时
   table 高度只增不减，超出部分照常走 wrap 的滚动条，互不影响）。 */
.table-body-wrap :deep(.ant-table-wrapper),
.table-body-wrap :deep(.ant-spin-nested-loading),
.table-body-wrap :deep(.ant-spin-container),
.table-body-wrap :deep(.ant-table),
.table-body-wrap :deep(.ant-table-container),
.table-body-wrap :deep(.ant-table-content) {
  height: 100%;
}

.table-body-wrap :deep(.ant-table-content table) {
  height: 100%;
}

/* 预设窗口 chips：点一下就切，时间口径同时写在卡标题里（双保险声明归属） */
.trend-chips {
  display: flex;
  gap: 6px;
}

.trend-chip {
  border: 1px solid #dfe6ee;
  background: #fff;
  /* 时间切换选项文字统一用系统主题色 */
  color: var(--gi-color-primary, #0f766e);
  font-size: 12px;
  line-height: 18px;
  padding: 2px 12px;
  border-radius: 12px;
  cursor: pointer;
  transition: all 0.15s;
}

.trend-chip:hover {
  color: var(--gi-color-primary-hover, #0d6b63);
  border-color: var(--gi-color-primary-hover, #0d6b63);
}

.trend-chip.active {
  background: var(--gi-color-primary, #0f766e);
  border-color: var(--gi-color-primary, #0f766e);
  color: #fff;
}

/* 柱状图：每列 = 计数 / 柱体 / x 轴标签；min-height 撑起 auto 行的固有高度，
   全屏时 flex:1 继续吃掉行内剩余空间 */
.trend-chart {
  flex: 1;
  min-height: 168px;
  display: flex;
  align-items: stretch;
  gap: 8px;
  padding: 8px 6px 4px;
}

.trend-col {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  cursor: pointer;
}

/* 无告警的段也允许点击，弹出空状态弹窗；cursor 保持 pointer 提示可交互。 */
.trend-col.empty {
  cursor: pointer;
}

.trend-count {
  flex: none;
  height: 15px;
  line-height: 15px;
  font-size: 11.5px;
  font-weight: 600;
  /* 柱顶数字（慢SQL数量）统一用系统主题色，不再按是否含未恢复区分 */
  color: var(--gi-color-primary, #0f766e);
  cursor: pointer;
}

.trend-count.has-unresolved {
  color: var(--gi-color-primary, #0f766e);
}

.trend-col.empty .trend-count {
  color: #9aa8b8;
}

.trend-bar-area {
  flex: 1;
  width: 100%;
  max-width: 52px;
  display: flex;
  align-items: flex-end;
  background: #f3f6f9;
  border-radius: 4px;
  overflow: hidden;
  cursor: pointer;
}

/* 柱体容器：内部两段（未恢复紫 / 已恢复绿）铺满，容器只负责高度与圆角裁切。
   颜色走 chartTokens 注入的 --chart-alert-*，与右侧圆饼/告警卡状态条同源 */
.trend-bar {
  width: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 4px 4px 0 0;
  transition: height 0.35s ease;
}

/* 柱内两段：只区分「未恢复 / 已恢复」两种状态，不引入其他分类维度。
   配色理由：
     · 已恢复 = 绿（--chart-alert-resolved），与告警圆饼第三段、告警卡「已恢复」状态条同色，
       "绿=已结束"是全站既有语义，不需要额外解释；
     · 未恢复 = 红（--chart-alert-pending），与顶部数字的红色档、告警圆饼「待认领」段同源，
       "未结束 vs 已结束"的语义对；紫-绿色相距离远，在红绿色盲/蓝黄色盲下都能分辨
       （蓝-绿在蓝色盲下易混，故不选蓝）；同时避开左卡实例四分类已占用的红/橙，
       不让趋势卡出现"警示色"，保证整卡只表达状态、不表达恐慌。
   颜色一律走 chartTokens 注入的 --chart-alert-*，同类图表可互相印证 */
.trend-seg {
  width: 100%;
  flex: none;
}

/* 未恢复段：取左侧圆饼「风险」琥珀色（用户要求与实例语义同色）；
   min-height 保底可见性，避免 1/50 这类小占比被压到肉眼不可见 */
.trend-seg-unresolved {
  min-height: 4px;
  background: var(--chart-instance-risk, #ef9f27);
}

/* 已恢复段（绿）：吃掉剩余高度（未恢复段为 0 时即整根柱） */
.trend-seg-resolved {
  flex: 1;
  background: var(--chart-alert-resolved, #0f9f6f);
}

.trend-col.empty .trend-bar {
  background: #d9e2ea;
}

.trend-col:hover:not(.empty) .trend-seg-unresolved {
  background: #d97706;
}

.trend-col:hover:not(.empty) .trend-seg-resolved {
  background: #0d8a60;
}

.trend-col:hover:not(.empty) .trend-count {
  color: var(--gi-color-primary-active, #0b5f58);
}

.trend-label {
  flex: none;
  height: 14px;
  line-height: 14px;
  font-size: 10.5px;
  color: #9aa8b8;
  white-space: nowrap;
}

/* 图例：与标题同行，margin-left:auto 推到右端、与时间切换 chips 组成右侧一组；
   色点与柱内两段同 token，避免"图例色 ≠ 柱子色" */
.trend-legend {
  flex: none;
  display: flex;
  align-items: center;
  margin-left: auto;
  gap: 12px;
  font-size: 11px;
  color: #9aa8b8;
  cursor: default;
}

.trend-legend-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.trend-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 2px;
}

.trend-dot-unresolved {
  background: var(--chart-instance-risk, #ef9f27);
}

.trend-dot-resolved {
  background: var(--chart-alert-resolved, #0f9f6f);
}

/* 表头吸顶：滚动条挂在 wrap 上（表头+表体一起滚），sticky 把 thead 钉在 wrap 顶部，
   滚动时表头不动、数据行从其下方穿过；显式补背景色避免行内容透出。 */
.table-body-wrap :deep(.ant-table-thead) th {
  position: sticky;
  top: 0;
  z-index: 2;
  background: #fff;
}

/* ----- 告警事件中心 ----- */
.alert-list-wrapper {
  display: flex;
  flex-direction: column;
  height: 100%;
}

/* 列表区占满 wrapper 剩余高度，内容超出时自身出现纵向滚动条；
   不再写死 max-height，避免 tab pane 高度不足时底部被截断。 */
.alert-list {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  overflow-y: auto;
  padding-right: 2px;
}

.alert-list-footer {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding-top: 10px;
  margin-top: 2px;
  font-size: 12px;
  color: #6d8290;
  border-top: 1px solid #edf2f7;
}

/* 左侧 3px 状态色条：一眼分辨卡片属于哪个 tab（红=待处理 / 蓝=处理中 / 绿=已解决）。
   overflow:hidden 让色条跟随圆角；padding-left 给色条让位。 */
.alert-item {
  position: relative;
  overflow: hidden;
  border: 1px solid #e2e8f0;
  border-radius: 14px;
  padding: 10px 14px 10px 16px;
  /* 卡片按完整内容高度渲染，禁止被 flex 容器压缩；
     列表总高度超过容器时由 .alert-list 的 overflow-y:auto 出现滚动条。 */
  flex-shrink: 0;
}

/* 告警卡片左侧状态条：与右侧圆饼 / 图例同源（待认领红 / 处理中橙 / 已恢复绿），
   统一走告警 Token（chartTokens 单一真源）。 */
.alert-item::before {
  content: "";
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 3px;
  background: var(--chart-alert-pending, #b3261e);
}

.alert-item.is-acked::before {
  background: var(--chart-alert-doing, #ef9f27);
}

.alert-item.is-recovered::before {
  background: var(--chart-alert-resolved, #0f9f6f);
}

.alert-item.is-acked {
  border-color: rgba(250, 140, 22, 0.35);
  background: rgba(250, 140, 22, 0.03);
}

.alert-item.is-recovered {
  border-color: rgba(15, 159, 111, 0.35);
  background: rgba(15, 159, 111, 0.03);
}

.alert-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

/* 完整时间戳（yyyy-mm-dd hh24:mi:ss）不折行，等级标签不被挤压 */
.alert-time {
  flex: none;
  white-space: nowrap;
  font-size: 12px;
  color: #6d8290;
  font-variant-numeric: tabular-nums;
}

/* ----- 数据源行：名称 + 地址 + 环境 -----
   所有 flex 子项都写 min-width:0 —— 少了这条，ellipsis 在 flex 容器里不会生效。 */
/* 完整展示数据源信息：取消 ellipsis/nowrap，允许自然换行；
   名称/地址较长时整体折行，保证信息不丢失。 */
.ds-row {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 6px;
  margin-top: 7px;
  min-width: 0;
}

.ds-name {
  flex: 0 1 auto;
  min-width: 0;
  font-size: 13px;
  font-weight: 600;
  color: #163440;
  word-break: break-word;
  white-space: normal;
}

/* 覆盖 DataSourceName 组件默认的小号淡灰（12px / #748893），保持数据源名称原有字号与颜色；
   字重不在这里写，继续继承父级 .ds-name（已解决 tab 为 500，其余为 600）。 */
.ds-name :deep(.gi-data-source-name) {
  font-size: 13px;
  color: #163440;
  word-break: break-word;
  white-space: normal;
}

.ds-addr {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 12px;
  color: #6d8290;
  font-variant-numeric: tabular-nums;
  word-break: break-word;
  white-space: normal;
}

.ds-env {
  flex: none;
  padding: 0 6px;
  border-radius: 3px;
  border: 1px solid rgba(15, 118, 110, 0.22);
  background: rgba(15, 118, 110, 0.08);
  font-size: 11px;
  line-height: 17px;
  color: #0f766e;
}

/* 三个 tab 的差异只写在父级 class 上，结构与字段保持一致，避免后续样式漂移：
   - 处理中：已有人接手，环境退到悬浮提示；
   - 已解决：回溯场景，地址降级为浅灰小字，名称不再加粗抢焦点。 */
.alert-item.is-acked .ds-env,
.alert-item.is-recovered .ds-env {
  display: none;
}

.alert-item.is-recovered .ds-name {
  font-weight: 500;
}

.alert-item.is-recovered .ds-addr {
  flex: 1 1 auto;
  font-size: 11.5px;
  color: #9aa8b8;
  word-break: break-word;
  white-space: normal;
}

/* 窄栏降级（分屏 / 小分辨率）：只隐藏环境标签并略降地址字号，
   名称/地址仍完整换行，不截断。 */
.alert-center-card {
  container-type: inline-size;
}

@container (max-width: 380px) {
  .ds-addr {
    font-size: 11.5px;
  }
  .ds-env {
    display: none;
  }
}

.alert-name {
  margin-top: 5px;
  font-size: 14px;
  font-weight: 700;
  color: #163440;
}

/* meta 行只剩右侧一项（持续时长 / 认领人 / 恢复时间），改为右对齐，
   避免 space-between 把它甩到左边破坏原有视觉重心。 */
.alert-meta {
  margin-top: 6px;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  font-size: 12px;
  min-width: 0;
}

/* 待处理卡片的持续时长：与告警 Token 的「待处理」同色，避免页面上再残留一个红。 */
.alert-duration {
  color: var(--chart-alert-pending, #b3261e);
}

.is-recovered .alert-duration {
  color: var(--chart-alert-resolved, #0f9f6f);
}

.alert-actions {
  margin-top: 10px;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

/* ----- 实例明细 ----- */
.instance-table :deep(.instance-row) {
  cursor: pointer;
}

.instance-table :deep(.instance-row:hover) td {
  background: rgba(36, 87, 245, 0.04) !important;
}

/* 异常信息列：文字统一用正文灰，颜色只落在前导 6px 小圆点上（fault 红 / risk 橙 / 其它灰）。
   此前整句按风险等级染成红/橙，叠加上方圆饼与右侧告警区后视觉噪音过重，
   现在保留「一眼看出有没有异常」的能力，同时让表格整体安静下来。
   长句在单元格内换行完整展示、不截断，撑高的行仍随行背景整体高亮。 */
.issue-text {
  color: #5a6b7a;
  display: block;
  line-height: 20px;
  word-break: break-word;
}

/* 前导小圆点：唯一承载异常信息列颜色的元素 */
.issue-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  margin-right: 6px;
  vertical-align: 1px;
  background: #9aa8b8;
}
.issue-dot.dot-risk {
  background: var(--chart-instance-risk, #ef9f27);
}
.issue-dot.dot-fault {
  background: var(--chart-instance-fault, #b3261e);
}

/* 实例明细列表【异常数据源】列：类型 logo + （名称 / IP:port）两行文字，
   排版与「备份任务」「数据源总览」页一致：logo 居左垂直居中，文字竖排在右。 */
.cell-name {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.cell-name-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
/* 数据库类型 logo：全站统一 20×20（utils/dbTypeLogo.js 口径） */
.cell-name .db-logo {
  width: 20px;
  height: 20px;
  flex: none;
  object-fit: contain;
}
.cell-name .name-main {
  font-size: 13px;
  line-height: 20px;
  color: #163440;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 覆盖 DataSourceName 组件默认的小号淡灰（12px / #748893），与本列主名保持一致 */
.cell-name .name-main :deep(.gi-data-source-name) {
  font-size: 13px;
  line-height: 20px;
  color: #163440;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  word-break: normal;
}
.cell-name .name-sub {
  font-size: 12px;
  line-height: 18px;
  color: #9aa8b8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 健康分着色：内联 style 由 statusTextColor() 提供，与「状态」列同一语义色，
   同一行两列颜色一致（健康绿 / 风险琥珀 / 故障深红）。色值与圆饼四分类 Token 同源。 */
.score-text {
  font-weight: 600;
}

/* 状态列纯文字（tag 已移除）：颜色由内联 style 提供，与第一张圆饼四分类同源 */
.status-text {
  font-size: 12px;
  font-weight: 600;
}

.is-na {
  color: #9aa8b8;
}

/* 表体滚动到底部前的渐隐提示：与卡片左右/下内边距对齐，不吃鼠标事件 */
.table-scroll-hint {
  position: absolute;
  left: 18px;
  right: 18px;
  bottom: 16px;
  height: 22px;
  pointer-events: none;
  background: linear-gradient(to bottom, rgba(255, 255, 255, 0), #ffffff 85%);
}

/* 空态：卡头删除后表体独占整卡，空态文字垂直居中，不留大片空白 */
.table-empty {
  padding: 28px 0;
  font-size: 13px;
  color: #9aa8b8;
  text-align: center;
}

/* ----- 响应式 ----- */
@media (max-width: 1200px) {
  .summary-row {
    grid-template-columns: repeat(2, 1fr);
  }

  .screen-grid {
    grid-template-columns: 1fr;
    grid-template-rows: none;
  }

  .trend-card,
  .alert-center-card,
  .table-card {
    grid-column: auto;
    grid-row: auto;
  }

  /* 单列堆叠时网格行高不再受控，卡片是 auto 高度：
     表格的高度 100% 链会在 auto 卡片里塌成 0（只剩一条边），
     这里给实例明细列表一个下限高度，让表体拿得到高度、靠内部滚动展示全部实例。 */
  .table-card {
    min-height: 220px;
  }
}</style>

<!-- 风险总览抽屉 / 下钻弹窗：内容 teleport 到 body，scoped 管不到，单独全局块 -->
<style>
.risk-overview-summary {
  margin-bottom: 12px;
  font-size: 13px;
  color: #163440;
}

.risk-overview-summary b {
  font-size: 16px;
  color: var(--chart-instance-risk, #ef9f27);
}

.risk-overview-summary .sev-summary {
  color: #6d8290;
}

.risk-overview-summary .gen-time {
  display: block;
  margin-top: 4px;
  font-size: 12px;
  color: #9aa8b8;
}

.risk-item-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.risk-item-name {
  color: #163440;
}

.risk-item-meta {
  font-size: 12px;
  font-weight: 400;
  color: #6d8290;
}

.risk-instance-meta {
  margin-bottom: 8px;
  font-size: 12px;
  color: #6d8290;
}

.risk-instance-status {
  margin-bottom: 12px;
  padding: 8px 12px;
  border-radius: 8px;
  background: rgba(217, 119, 6, 0.08);
  font-size: 13px;
  color: #163440;
}

.reason-list {
  margin: 0 0 12px;
  padding: 0;
  list-style: none;
}

.reason-item {
  padding: 10px 0;
  border-bottom: 1px dashed #e2e8f0;
}

.reason-item:last-child {
  border-bottom: none;
}

.reason-line {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}

.reason-kind {
  flex: none;
}

.reason-severity {
  flex: none;
}

.reason-message {
  font-size: 13px;
  color: #163440;
  line-height: 22px;
  word-break: break-all;
}

.reason-basis {
  margin-top: 4px;
  padding-left: 2px;
  font-size: 12px;
  line-height: 20px;
  color: #6d8290;
}

.risk-drawer .ant-collapse-header {
  align-items: center !important;
}

@media (max-width: 768px) {
  /* 页面壳 height:100% + overflow:hidden 会把内容裁掉，移动端无法滚动查看 */
  .monitor-page {
    height: auto;
    min-height: 100%;
    overflow: visible;
  }

  /* 时间范围选择器 330px、下拉 150px 全部改为占满 */
  .range-select,
  .range-picker,
  .filter-select {
    width: 100% !important;
    max-width: 100%;
    margin-left: 0;
  }

  /* 汇总卡 5 列 → 单列，环形图卡不再被压到 170px 以内 */
  .summary-row {
    grid-template-columns: minmax(0, 1fr);
    gap: 10px;
  }

  .donut-card {
    flex-wrap: wrap;
    gap: 12px;
  }

  .donut-wrap {
    width: 88px;
    height: 88px;
  }

  .alert-donut-card .donut-legend-wrap {
    flex: 1 1 100%;
  }

  .screen-grid {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: none;
  }

  .screen-card {
    padding: 12px 14px;
  }

  .table-body-wrap {
    overflow-x: auto;
    overflow-y: visible;
    max-height: none;
  }

  /* 趋势柱：允许横向滚动，日期标签不再 nowrap 挤压 */
  .trend-chart {
    min-height: 140px;
    overflow-x: auto;
    padding: 8px 2px 4px;
  }

  .trend-col {
    flex: 0 0 44px;
  }

  .trend-label {
    font-size: 9.5px;
    white-space: normal;
    text-align: center;
    line-height: 1.2;
  }

  .trend-legend {
    flex-wrap: wrap;
  }

  .alert-center-card,
  .trend-card,
  .table-card {
    overflow: visible;
  }

  .alert-list {
    overflow-y: visible;
  }

  .card-header {
    flex-wrap: wrap;
    row-gap: 8px;
  }
}
</style>
