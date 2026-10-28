<template>
  <a-modal
    :open="visible"
    :title="title"
    width="720px"
    :footer="null"
    class="risk-detail-modal"
    @update:open="$emit('update:visible', $event)"
  >
    <div v-if="riskDetail" class="risk-detail-wrapper">
      <div class="risk-detail-scroll">
        <div class="risk-instance-meta">{{ metaLine }}</div>
        <div v-if="riskDetail.status_message" class="risk-instance-status">
          {{ riskDetail.status_message }}
        </div>
        <ul v-if="reasons.length" class="reason-list">
          <li v-for="(reason, idx) in pagedReasons" :key="idx" class="reason-item">
            <div class="reason-line">
              <a-tag :color="reason.kind === 'alert' ? 'red' : 'orange'" class="reason-kind">
                {{ reason.kind === 'alert' ? '告警' : '采集' }}
              </a-tag>
              <a-tag v-if="reason.severity" :color="levelColor(reason.severity)" class="reason-severity">
                {{ reason.severity }}
              </a-tag>
              <span class="reason-text">
                <span class="reason-name">{{ operatorTextInText(reason.message) || reason.metric_name || '未获取' }}</span>
                <span v-if="reason.duration_sec" class="reason-duration">
                  。已持续 {{ alertDurationCompact(reason.duration_sec) }}
                </span>
              </span>
            </div>
          </li>
        </ul>
        <div v-else class="risk-empty-state">
          <div class="risk-empty-score">
            <span class="res-label">健康分</span>
            <span class="res-value" :class="emptyScoreClass">{{ emptyScore }}</span>
            <span class="res-total">/ 100</span>
          </div>
          <div class="risk-empty-row">
            <span class="res-dot"></span>
            当前待认领告警：<b>0</b> 条
          </div>
          <div class="risk-empty-row">
            <span class="res-dot"></span>
            最近检测时间：{{ riskDetail.probe_at || riskDetail.generated_at || '—' }}
          </div>
          <p class="risk-empty-hint">该实例当前没有触发任何告警，暂无风险原因。</p>
        </div>
        <a-button size="small" type="link" @click="$emit('goto-detail', riskDetail.instance_id)">
          查看该实例完整监控详情 →
        </a-button>
      </div>
      <div v-if="total > pageSize" class="risk-detail-footer">
        <a-pagination
          v-model:current="page"
          :total="total"
          :page-size="pageSize"
          :show-size-changer="false"
          :show-less-items="true"
        />
      </div>
    </div>
    <div v-else class="placeholder-box">未获取</div>
  </a-modal>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { alertDurationCompact, levelColor, operatorTextInText } from '../utils'

defineOptions({ name: 'InstanceRiskDetailModal' })

const props = defineProps({
  visible: Boolean,
  riskDetail: Object,
})
const emit = defineEmits(['update:visible', 'goto-detail'])

const pageSize = 10
const page = ref(1)
const reasons = computed(() => props.riskDetail?.reasons || [])
const total = computed(() => reasons.value.length)
const pagedReasons = computed(() => {
  const start = (page.value - 1) * pageSize
  return reasons.value.slice(start, start + pageSize)
})

watch(
  () => [props.visible, props.riskDetail],
  () => {
    page.value = 1
  },
  { immediate: true }
)

const title = computed(() => {
  if (!props.riskDetail) return '风险详情'
  return `风险详情：${props.riskDetail.remark || props.riskDetail.target || '实例'}`
})

const metaLine = computed(() => {
  const d = props.riskDetail
  if (!d) return ''
  const parts = [
    d.target || '—',
    d.db_type || '未获取',
    d.environment_name || '未获取',
    d.business ? `业务线: ${d.business}` : '',
  ]
  return parts.filter(Boolean).join(' ｜ ')
})

const emptyScore = computed(() => {
  const score = props.riskDetail?.health_score
  if (score == null) return '—'
  const num = Number(score)
  if (!Number.isFinite(num)) return '—'
  // 后端算分已做 0 分下限截断，这里再兜一层：负分对运维没有解释力。
  return String(Math.max(0, num))
})

const emptyScoreClass = computed(() => {
  // 颜色与后端状态口径同源，不再前端硬编码 60/80 阈值。
  const key = props.riskDetail?.screen_class
  if (!key) return ''
  return { healthy: 'score-ok', risk: 'score-risk', fault: 'score-fault' }[key] || ''
})
</script>

<style>
/* 弹窗被 a-modal teleport 到 body，scoped 不生效，必须挂全局 */
.risk-detail-modal .ant-modal-body {
  display: flex;
  flex-direction: column;
  max-height: calc(100vh - 180px);
  overflow: hidden !important;
  padding-bottom: 0;
}
.risk-detail-wrapper {
  display: flex;
  flex-direction: column;
  min-height: 0;
  flex: 1;
}
.risk-detail-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-right: 2px;
}
.risk-instance-meta {
  margin-bottom: 8px;
  font-size: 13px;
  color: #6d8290;
}
.risk-instance-status {
  margin-bottom: 12px;
  padding: 8px 12px;
  border-radius: 8px;
  background: #fff7e6;
  color: var(--chart-instance-risk, #ef9f27);
  font-size: 13px;
  line-height: 20px;
}
.risk-detail-modal .reason-list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.risk-detail-modal .reason-item {
  padding: 10px 0;
  border-bottom: 1px solid #f0f0f0;
}
.risk-detail-modal .reason-item:last-child {
  border-bottom: none;
}
.risk-detail-modal .reason-line {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}
.risk-detail-modal .reason-kind,
.risk-detail-modal .reason-severity {
  flex: none;
  margin: 0 !important;
}
.risk-detail-modal .reason-text {
  flex: 1;
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px;
  min-width: 0;
}
.risk-detail-modal .reason-name {
  font-size: 13px;
  color: #163440;
  line-height: 22px;
  word-break: break-all;
}
.risk-detail-modal .reason-duration {
  flex: none;
  font-size: 13px;
  color: #6d8290;
  line-height: 22px;
  white-space: nowrap;
}
.risk-detail-footer {
  flex: none;
  display: flex;
  justify-content: flex-end;
  padding: 12px 0;
  border-top: 1px solid #f0f0f0;
}
.risk-empty-state {
  padding: 24px 0 16px;
  text-align: center;
  color: #6d8290;
}
.risk-empty-score {
  display: inline-flex;
  align-items: baseline;
  gap: 4px;
  margin-bottom: 12px;
  font-size: 14px;
}
.risk-empty-score .res-label {
  color: #6d8290;
}
.risk-empty-score .res-value {
  font-size: 32px;
  font-weight: 600;
  color: #163440;
}
.risk-empty-score .res-value.score-ok {
  color: var(--chart-instance-healthy, #0f9f6f);
}
.risk-empty-score .res-value.score-risk {
  color: var(--chart-instance-risk, #ef9f27);
}
.risk-empty-score .res-value.score-fault {
  color: var(--chart-instance-fault, #b3261e);
}
.risk-empty-score .res-total {
  color: #9aa8b8;
  font-size: 13px;
}
.risk-empty-row {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  margin-bottom: 6px;
  font-size: 13px;
}
.risk-empty-row .res-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--chart-instance-healthy, #0f9f6f);
}
.risk-empty-hint {
  margin: 12px 0 0;
  font-size: 13px;
  color: #9aa8b8;
}
.risk-detail-modal .placeholder-box {
  min-height: 96px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #9aa8b8;
  font-size: 13px;
  border: 1px dashed #d8e0ea;
  border-radius: 14px;
}
</style>
