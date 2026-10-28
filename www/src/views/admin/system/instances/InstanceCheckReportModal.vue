<template>
  <a-modal
    :open="open"
    title="连接检测报告"
    :width="760"
    :footer="null"
    @cancel="emit('close')"
  >
    <a-alert
      :type="sshAlertType"
      show-icon
      :message="sshTitle"
      :description="sshDescription"
      style="margin-bottom: 16px"
    />

    <div class="report-section-title">
      采集权限体检
      <span class="report-section-note">{{ privilegeSummary }}</span>
    </div>
    <a-empty v-if="!items.length" description="该数据库类型暂未提供权限自检项" />
    <a-table
      v-else
      :columns="columns"
      :data-source="items"
      :pagination="false"
      size="small"
      row-key="key"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'status'">
          <a-tag :color="statusColor(record.status)">{{ statusText(record.status) }}</a-tag>
        </template>
        <template v-else-if="column.key === 'detail'">
          <div class="report-impact">{{ record.impact }}</div>
          <div v-if="record.reason" class="report-reason">原因：{{ record.reason }}</div>
          <div v-if="record.fix_sql" class="report-fix">
            <span class="report-fix-label">修复语句</span>
            <a-typography-text code copyable :content="record.fix_sql">
              {{ record.fix_sql }}
            </a-typography-text>
          </div>
        </template>
      </template>
    </a-table>

    <a-alert
      v-if="failedCount > 0"
      type="warning"
      show-icon
      style="margin-top: 16px"
      message="上述缺口不会阻断实例保存"
      :description="nonBlockingDescription"
    />
  </a-modal>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  open: { type: Boolean, default: false },
  report: { type: Object, default: () => ({}) },
})
const emit = defineEmits(['close'])

const items = computed(() => props.report?.privileges || [])
const failedCount = computed(() => props.report?.failed_count || 0)

// 用绑定而非字面属性：文案里有成对双引号，写进 HTML 属性会提前闭合。
const nonBlockingDescription =
  '数据库连接本身已通过。缺权限时对应指标会被标记为 unsupported（明确标为「采不到」），而不会返回 0 冒充健康值。'

const columns = [
  { title: '检查项', dataIndex: 'name', key: 'name', width: 180 },
  { title: '结果', dataIndex: 'status', key: 'status', width: 90 },
  { title: '影响与修复', dataIndex: 'detail', key: 'detail' },
]

const privilegeSummary = computed(() => {
  if (!items.value.length) return ''
  const failed = failedCount.value
  const warn = props.report?.warn_count || 0
  const passed = items.value.length - failed - warn
  const parts = []
  if (passed > 0) parts.push(`${passed} 项通过`)
  if (failed > 0) parts.push(`${failed} 项缺失`)
  if (warn > 0) parts.push(`${warn} 项待确认`)
  return parts.join(' · ')
})

const statusText = (status) => ({ pass: '通过', fail: '缺失', warn: '待确认' }[status] || status)
const statusColor = (status) => ({ pass: 'green', fail: 'red', warn: 'orange' }[status] || 'default')

const sshAlertType = computed(() => ({
  ok: 'success',
  not_configured: 'info',
  failed: 'warning',
}[props.report?.ssh_status] || 'info'))

const sshTitle = computed(() => ({
  ok: '主机 SSH 可用',
  not_configured: '未配置主机 SSH',
  failed: '主机 SSH 不可用',
}[props.report?.ssh_status] || '主机 SSH 状态未知'))

const sshDescription = computed(() => {
  const message = String(props.report?.ssh_message || '').trim()
  const suffix = '监控采集不使用 SSH：服务端直连数据库取引擎指标，主机指标由 Prometheus 拉取。'
  return message ? `${message}。${suffix}` : suffix
})
</script>

<style scoped>
.report-section-title {
  font-size: 14px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.86);
  margin-bottom: 10px;
}
.report-section-note {
  margin-left: 8px;
  font-size: 12.5px;
  font-weight: 400;
  color: rgba(0, 0, 0, 0.45);
}
.report-impact {
  color: rgba(0, 0, 0, 0.78);
  line-height: 1.6;
}
.report-reason {
  margin-top: 3px;
  font-size: 12.5px;
  color: rgba(0, 0, 0, 0.45);
  line-height: 1.6;
}
.report-fix {
  margin-top: 6px;
  display: flex;
  align-items: flex-start;
  gap: 6px;
  flex-wrap: wrap;
}
.report-fix-label {
  flex: 0 0 auto;
  font-size: 12.5px;
  color: rgba(0, 0, 0, 0.45);
  line-height: 22px;
}

@media (max-width: 768px) {
  :deep(.ant-table) {
    min-width: 520px;
  }

  :deep(.ant-table-wrapper) {
    overflow-x: auto;
  }
}
</style>
