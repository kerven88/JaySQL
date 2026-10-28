<template>
  <a-drawer
    :open="open"
    :width="720"
    :title="title"
    placement="right"
    destroy-on-close
    @close="close"
  >
    <a-spin :spinning="loading">
      <div class="gi-iad-section">
        <div class="gi-iad-section-title">当前告警</div>
        <a-empty
          v-if="!loading && !currentItems.length"
          description="该实例当前没有未恢复的告警"
        />
        <div v-for="item in currentItems" :key="item.row_key" class="gi-iad-alert">
          <div class="gi-iad-alert-head">
            <span class="gi-iad-alert-name" :title="operatorTextInText(item.name)">{{ operatorTextInText(item.name) }}</span>
            <a-tag :color="statusColor(item.status)">{{ item.status_text || item.status }}</a-tag>
            <a-tag v-if="item.ignored" color="default">已忽略</a-tag>
          </div>
          <div class="gi-iad-alert-meta">{{ descText(item) }}</div>
          <div class="gi-iad-alert-actions">
            <a-button
              v-if="item.status === 'active'"
              type="link"
              size="small"
              class="gi-iad-action-btn"
              :disabled="!canManage"
              @click="emitAction(item, 'ack')"
            >
              认领
            </a-button>
            <a-button
              v-if="!item.ignored"
              type="link"
              size="small"
              class="gi-iad-action-btn"
              :disabled="!canManage"
              @click="emitAction(item, 'ignore')"
            >
              忽略
            </a-button>
            <a-button
              v-else
              type="link"
              size="small"
              class="gi-iad-action-btn"
              :disabled="!canManage"
              @click="emitAction(item, 'unignore')"
            >
              取消忽略
            </a-button>
            <a-button type="link" size="small" class="gi-iad-action-btn" @click="$emit('detail', item)">详情</a-button>
          </div>
        </div>
      </div>

      <div class="gi-iad-section">
        <div class="gi-iad-section-title gi-iad-history-title" @click="historyExpanded = !historyExpanded">
          <span>历史记录</span>
          <a-space align="center" :size="8">
            <a-tooltip title="一周内已恢复告警">
              <QuestionCircleOutlined class="gi-iad-help-icon" />
            </a-tooltip>
            <span class="gi-iad-toggle">
              {{ historyExpanded ? '收起' : '展开' }}
              <DownOutlined v-if="!historyExpanded" />
              <UpOutlined v-else />
            </span>
          </a-space>
        </div>
        <div v-show="historyExpanded">
          <a-empty
            v-if="!loading && !historyItems.length"
            description="该实例近期没有已恢复的告警"
          />
          <div v-for="item in historyItems" :key="`${item.row_key}-${item.triggered_at}`" class="gi-iad-node">
            <div class="gi-iad-node-body">
              <div class="gi-iad-node-head">
                <a-tag>{{ item.severity || '—' }}</a-tag>
                <span class="gi-iad-alert-name" :title="operatorTextInText(item.name)">{{ operatorTextInText(item.name) }}</span>
              </div>
              <div class="gi-iad-node-meta">{{ historyMetaText(item) }}</div>
            </div>
          </div>
          <div v-if="historyTotal > historyItems.length" class="gi-iad-more">
            仅展示最近 {{ historyItems.length }} 条，共 {{ historyTotal }} 条，完整记录见「告警历史」
          </div>
        </div>
      </div>
    </a-spin>
  </a-drawer>
</template>

<script setup>
import { getMonitorAlertsApi } from '@/api/monitor'
import { message } from 'ant-design-vue'
import { computed, ref, watch } from 'vue'
import { DownOutlined, QuestionCircleOutlined, UpOutlined } from '@ant-design/icons-vue'
import { fmtMetricNum, operatorTextInText } from '../utils'

// 实例告警抽屉：两个 Tab 共用的下钻视图（当前态 + 历史时间线）。
// 它只负责"某个实例"这一个问题，不承担检索职责——检索在告警历史 Tab。
defineOptions({ name: 'MonitorInstanceAlertDrawer' })

const props = defineProps({
  open: { type: Boolean, default: false },
  instance: { type: Object, default: null },
  canManage: { type: Boolean, default: false },
})
const emit = defineEmits(['update:open', 'action', 'detail'])

const DRAWER_PAGE_SIZE = 50

const loading = ref(false)
const currentItems = ref([])
const currentTotal = ref(0)
const historyItems = ref([])
const historyTotal = ref(0)

// 历史记录默认收起，展开后只加载最近 1 周已恢复告警
const historyExpanded = ref(false)
const historyLoaded = ref(false)

const instanceID = computed(() => props.instance?.instance_id || props.instance?.id || '')
const instanceLabel = computed(() => {
  const item = props.instance
  if (!item) return ''
  if (item.hostname) return `${item.hostname}:${item.port ?? ''}`
  return item.instance || item.instance_id || ''
})
const title = computed(() => (instanceLabel.value ? `实例告警 · ${instanceLabel.value}` : '实例告警'))

const statusColor = (value) => ({ active: 'red', acknowledged: 'blue', recovered: 'green' }[value] || 'default')

// descText 把零散片段整合为一句完整描述，如：
// 「内存使用率为 88.22 %，超过阈值 70 %，持续 5 天 6 小时，触发时间 2026-09-20 13:33:17。」
// 缺失的片段自动跳过（无阈值省略「超过阈值」、无时长省略「持续」），避免出现「为 —」这类破碎文案。
const descText = (record) => {
  const unit = record.unit ? ` ${record.unit}` : ''
  const metric = record.metric_name || record.metric_key || ''
  const observed = fmtMetricNum(record.observed_value)
  const threshold = fmtMetricNum(record.threshold)
  const parts = []
  if (observed) parts.push(`${metric || '指标'}为 ${observed}${unit}`)
  if (threshold) parts.push(`超过阈值 ${threshold}${unit}`)
  const duration = durationText(record.duration_sec)
  if (duration !== '—') parts.push(`持续 ${duration}`)
  if (record.triggered_at) parts.push(`触发时间 ${record.triggered_at}`)
  return parts.length ? `${parts.join('，')}。` : '—'
}

// historyMetaText 历史时间线的描述句，与当前告警 descText 风格一致：
// 「触发于 2026-09-20 13:33:17，恢复于 2026-09-20 14:00:00，持续 26 分，由 admin 认领。」
const historyMetaText = (record) => {
  const parts = []
  if (record.triggered_at) parts.push(`触发于 ${record.triggered_at}`)
  if (record.recovered_at) parts.push(`恢复于 ${record.recovered_at}`)
  const duration = durationText(record.duration_sec)
  if (duration !== '—') parts.push(`持续 ${duration}`)
  if (record.acked_by) parts.push(`由 ${record.acked_by} 认领`)
  return parts.length ? `${parts.join('，')}。` : '—'
}

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

const close = () => emit('update:open', false)

const emitAction = (record, action) => emit('action', { record, action })

const load = async () => {
  const id = instanceID.value
  if (!id) {
    currentItems.value = []
    historyItems.value = []
    currentTotal.value = 0
    historyTotal.value = 0
    historyExpanded.value = false
    historyLoaded.value = false
    return
  }
  loading.value = true
  try {
    // 当前告警始终加载；历史记录默认收起，仅在展开态加载，且只取最近 1 周（range_hours=168）。
    const currentPromise = getMonitorAlertsApi({ instance_id: id, unresolved: true, page: 1, page_size: DRAWER_PAGE_SIZE })
    const historyPromise = historyExpanded.value
      ? getMonitorAlertsApi({ instance_id: id, status: 'recovered', range_hours: 168, page: 1, page_size: DRAWER_PAGE_SIZE })
      : Promise.resolve(null)
    const [currentRes, historyRes] = await Promise.all([currentPromise, historyPromise])
    currentItems.value = currentRes?.data?.items || []
    currentTotal.value = Number(currentRes?.total || 0)
    if (historyRes) {
      historyItems.value = (historyRes?.data?.items || []).slice().sort((a, b) =>
        String(b.triggered_at || '').localeCompare(String(a.triggered_at || ''))
      )
      historyTotal.value = Number(historyRes?.total || 0)
      historyLoaded.value = true
    }
  } catch (error) {
    // 路由切换引发的请求取消不算失败（请求层会 abort 在途请求），静默即可
    if (!(error && (error.canceled || error.code === 'ERR_CANCELED'))) {
      message.error(error?.message || '实例告警加载失败')
    }
    currentItems.value = []
    historyItems.value = []
    currentTotal.value = 0
    historyTotal.value = 0
    historyLoaded.value = false
  } finally {
    loading.value = false
  }
}

// 打开时加载；已打开状态下实例被换（从另一条告警点进来）也要重新加载。
watch(
  () => [props.open, instanceID.value],
  ([isOpen]) => {
    if (isOpen) {
      load()
    } else {
      // 关闭后重置展开状态，下次打开仍默认收起
      historyExpanded.value = false
      historyLoaded.value = false
    }
  },
  { immediate: true }
)

// 历史记录展开时懒加载，收起时不清空以避免再次展开时闪烁
watch(historyExpanded, (expanded) => {
  if (expanded && !historyLoaded.value) load()
})

// 父级处置成功后回灌：抽屉自己刷新一次即可，不必让父级再操作抽屉内部状态。
defineExpose({ reload: load })
</script>

<!--
  注意：a-drawer 内容 teleport 到 body，scoped 样式不会命中，
  因此本组件的样式统一放在非 scoped 块里，并加 gi-iad- 前缀避免污染全局。
-->
<style>
/* 删除顶部实例信息区后，用相邻兄弟选择器给非首个 section 加间距，
   避免 drawer body 顶部出现双重留白。 */
.gi-iad-section + .gi-iad-section {
  margin-top: 18px;
}

.gi-iad-section-title {
  margin-bottom: 10px;
  font-size: 13px;
  font-weight: 500;
  color: var(--gi-color-text-primary, rgba(0, 0, 0, 0.85));
}

/* 历史记录标题：整行可点击展开/收起，右侧放问号提示与展开状态 */
.gi-iad-history-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  cursor: pointer;
  user-select: none;
}

.gi-iad-help-icon {
  font-size: 13px;
  color: var(--gi-color-text-tertiary, #8a94a6);
  cursor: help;
}

.gi-iad-toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  font-weight: 400;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.gi-iad-alert {
  padding: 10px 12px;
  margin-bottom: 8px;
  background: var(--gi-color-page-bg, rgba(0, 0, 0, 0.02));
  border: 1px solid var(--gi-color-border-soft, rgba(0, 0, 0, 0.06));
  border-radius: 8px;
}

.gi-iad-alert-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.gi-iad-alert-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: 13px;
  font-weight: 500;
  color: var(--gi-color-text-primary, rgba(0, 0, 0, 0.85));
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 告警描述为一句完整文案（descText），不再按片段分列，普通段落排版即可 */
.gi-iad-alert-meta {
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--gi-color-text-secondary, rgba(0, 0, 0, 0.55));
}

.gi-iad-alert-actions {
  display: flex;
  gap: 4px;
  margin-top: 4px;
}

/* 认领 / 忽略 / 详情等操作文字统一使用默认文字色，保持原有交互与禁用逻辑 */
.gi-iad-action-btn {
  color: var(--gi-color-text-primary, rgba(0, 0, 0, 0.85));
}
.gi-iad-action-btn:disabled {
  color: var(--gi-color-text-disabled, rgba(0, 0, 0, 0.25));
}
.gi-iad-action-btn:hover {
  color: var(--gi-color-text-primary, rgba(0, 0, 0, 0.85));
}

.gi-iad-node {
  position: relative;
  display: flex;
  gap: 10px;
  padding: 0 0 14px 4px;
}

.gi-iad-node-body {
  flex: 1;
  min-width: 0;
}

.gi-iad-node-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.gi-iad-node-meta {
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--gi-color-text-primary, rgba(0, 0, 0, 0.85));
}

.gi-iad-more {
  padding: 8px 10px;
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #8a94a6);
  background: var(--gi-color-page-bg, rgba(0, 0, 0, 0.02));
  border-radius: 8px;
}
</style>
