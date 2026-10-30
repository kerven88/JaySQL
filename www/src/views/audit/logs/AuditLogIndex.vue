<template>
  <div class="gi-page-shell audit-log-page">
    <a-card class="gi-page-section" :bordered="false">
      <a-space wrap>
        <a-button v-for="preset in presets" :key="preset.key"
          :type="activePreset === preset.key ? 'primary' : 'default'" @click="selectPreset(preset.key)">
          {{ preset.label }}
        </a-button>
        <a-range-picker v-model:value="filters.timeRange" show-time
          value-format="YYYY-MM-DD HH:mm:ss" format="YYYY-MM-DD HH:mm:ss" @change="activePreset = ''" />
        <a-button type="primary" @click="search">查询</a-button>
        <a-button @click="reset">重置</a-button>
        <a-button type="text" @click="moreExpanded = !moreExpanded">{{ moreExpanded ? '收起' : '展开' }}</a-button>
      </a-space>
      <a-space v-if="moreExpanded" wrap class="more-filters">
        <a-input v-model:value="filters.operatorUsername" allow-clear placeholder="操作用户" />
        <a-select v-model:value="filters.auditLevel" allow-clear placeholder="审计等级" :options="levels" />
        <a-select v-model:value="filters.moduleCode" allow-clear placeholder="所属模块" :options="modules" />
        <a-select v-model:value="filters.actionResult" allow-clear placeholder="操作结果" :options="results" />
        <a-input v-model:value="filters.keyword" allow-clear placeholder="搜索操作类型、摘要、资源名称" @press-enter="search" />
      </a-space>
    </a-card>
    <a-card class="gi-page-section" :bordered="false">
      <a-table row-key="audit_id" :columns="columns" :data-source="rows" :loading="loading"
        :pagination="pagination" :scroll="{ x: 1000 }" :locale="{ emptyText: '暂无符合条件的审计日志' }"
        @change="changePage">
        <template #bodyCell="{ column, record, text }">
          <template v-if="column.key === 'audit_level'">{{ levelLabel(record.audit_level) }}</template>
          <template v-else-if="column.key === 'action_result'">{{ resultLabel(record.action_result) }}</template>
          <a-button v-else-if="column.key === 'actions'" type="link" size="small" @click="openDetail(record)">查看详情</a-button>
          <template v-else>{{ text || '-' }}</template>
        </template>
      </a-table>
    </a-card>
    <a-drawer v-model:open="detail.open" title="审计详情" :width="760" destroy-on-close>
      <a-spin :spinning="detail.loading">
        <a-space v-if="detail.record" direction="vertical" class="detail-layout" :size="16">
          <a-card size="small" title="基本信息">
            <a-descriptions :column="2" size="small" bordered>
              <a-descriptions-item v-for="field in basicFields" :key="field.key" :label="field.label">
                {{ field.key === 'audit_level' ? levelLabel(detail.record[field.key]) : field.key === 'action_result' ? resultLabel(detail.record[field.key]) : detail.record[field.key] || '-' }}
              </a-descriptions-item>
            </a-descriptions>
          </a-card>
          <a-card size="small" title="对象信息">
            <a-descriptions :column="2" size="small" bordered>
              <a-descriptions-item v-for="field in resourceFields" :key="field.key" :label="field.label">{{ detail.record[field.key] || '-' }}</a-descriptions-item>
            </a-descriptions>
          </a-card>
          <a-card size="small" title="操作详情">
            <div v-for="field in detailFields" :key="field.key" class="detail-block">
              <h4>{{ field.label }}</h4>
              <pre>{{ formatValue(detail.record[field.key]) }}</pre>
            </div>
          </a-card>
        </a-space>
      </a-spin>
    </a-drawer>
  </div>
</template>

<script setup>
// Reconstructed from the official demo's published audit page and the local API contract.
import { computed, onMounted, reactive, ref } from 'vue'
import { getAuditLogsApi, getAuditLogDetailApi } from '@/api/audit'

defineOptions({ name: 'AuditLogIndexView' })
const presets = [{ key: 'today', label: '今日' }, { key: 'yesterday', label: '昨日' }, { key: 'last7', label: '近 7 天' }, { key: 'last30', label: '近 30 天' }]
const levels = [{ label: '普通操作', value: 'normal' }, { label: '高危操作', value: 'high' }]
const results = [{ label: '成功', value: 'success' }, { label: '失败', value: 'fail' }, { label: '被拒绝', value: 'rejected' }, { label: '已取消', value: 'cancelled' }]
const modules = [
  ['数据源管理', 'datasource'], ['数据授权', 'das_grant'], ['SQL 查询', 'sql_query'],
  ['SQL 收藏', 'sql_favorite'], ['数据库工单', 'db_order'], ['备份节点', 'backup_node'],
  ['备份任务', 'backup_task'], ['备份集', 'backup_set'], ['备份恢复', 'backup_restore'],
].map(([label, value]) => ({ label, value }))
const columns = [
  ['操作时间', 'operated_at'], ['操作用户', 'operator_username'], ['审计等级', 'audit_level'],
  ['所属模块', 'module_name'], ['操作结果', 'action_result'], ['操作摘要', 'summary'], ['操作', 'actions'],
].map(([title, key]) => ({ title, key, dataIndex: key, ellipsis: true, ...(key === 'actions' ? { fixed: 'right', width: 110 } : {}) }))
const basicFields = [
  ['审计 ID', 'audit_id'], ['请求 ID', 'request_id'], ['操作时间', 'operated_at'],
  ['操作用户', 'operator_username'], ['显示名称', 'operator_name'], ['审计等级', 'audit_level'],
  ['操作结果', 'action_result'], ['所属模块', 'module_name'], ['操作类型', 'action_name'],
  ['用户 IP', 'operator_ip'], ['终端信息', 'operator_terminal'],
].map(([label, key]) => ({ label, key }))
const resourceFields = [['对象类型', 'resource_type'], ['对象名称', 'resource_name'], ['对象 ID', 'resource_id'], ['关联工单', 'related_order_id']].map(([label, key]) => ({ label, key }))
const detailFields = [['操作摘要', 'summary'], ['请求参数', 'request_payload'], ['变更前内容', 'before_data'], ['变更后内容', 'after_data'], ['SQL 内容', 'sql_text'], ['错误信息', 'error_message']].map(([label, key]) => ({ label, key }))
const pad = (value) => String(value).padStart(2, '0')
const formatDate = (date) => `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
function timeRange(key) {
  const end = new Date()
  const start = new Date(end)
  if (key === 'yesterday') {
    start.setDate(start.getDate() - 1)
    end.setDate(end.getDate() - 1)
    end.setHours(23, 59, 59, 999)
  } else if (key === 'last7' || key === 'last30') {
    start.setDate(start.getDate() - (key === 'last7' ? 6 : 29))
  }
  start.setHours(0, 0, 0, 0)
  return [formatDate(start), formatDate(end)]
}
const defaultFilters = () => ({ timeRange: timeRange('last7'), operatorUsername: '', auditLevel: undefined, moduleCode: undefined, keyword: '', actionResult: undefined })
const filters = reactive(defaultFilters())
const activePreset = ref('last7')
const moreExpanded = ref(false)
const rows = ref([])
const loading = ref(false)
const page = reactive({ current: 1, pageSize: 10, total: 0 })
const pagination = computed(() => ({ ...page, showSizeChanger: true, pageSizeOptions: ['10', '20', '50'], showTotal: (total) => `共 ${total} 条` }))
const detail = reactive({ open: false, loading: false, record: null })
let listRequest = 0
let detailRequest = 0
async function load() {
  const request = ++listRequest
  loading.value = true
  try {
    const response = await getAuditLogsApi({
      start_time: filters.timeRange?.[0] || '', end_time: filters.timeRange?.[1] || '',
      operator_username: filters.operatorUsername || '', audit_level: filters.auditLevel || '',
      module_code: filters.moduleCode || '', keyword: filters.keyword.trim(), action_result: filters.actionResult || '',
      page: page.current, page_size: page.pageSize, is_page: true,
    })
    if (request !== listRequest) return
    rows.value = Array.isArray(response?.data) ? response.data : []
    page.total = Number(response?.total || 0)
  } catch {
    // The shared request interceptor displays API errors.
    if (request === listRequest) { rows.value = []; page.total = 0 }
  } finally {
    if (request === listRequest) loading.value = false
  }
}
function search() { page.current = 1; load() }
function reset() { Object.assign(filters, defaultFilters()); activePreset.value = 'last7'; search() }
function selectPreset(key) { activePreset.value = key; filters.timeRange = timeRange(key); search() }
function changePage(next) { page.current = next.pageSize !== page.pageSize ? 1 : next.current; page.pageSize = next.pageSize; load() }
async function openDetail(record) {
  const request = ++detailRequest
  detail.open = true; detail.loading = true; detail.record = null
  try {
    const response = await getAuditLogDetailApi(record.audit_id)
    if (request === detailRequest) detail.record = response?.data || null
  } catch {
    // The shared request interceptor displays API errors.
  } finally {
    if (request === detailRequest) detail.loading = false
  }
}
const levelLabel = (value) => levels.find((item) => item.value === value)?.label || value || '-'
const resultLabel = (value) => results.find((item) => item.value === value)?.label || value || '-'
function formatValue(value) {
  if (value == null || value === '') return '-'
  if (typeof value === 'string') {
    try { return JSON.stringify(JSON.parse(value), null, 2) } catch { return value }
  }
  return JSON.stringify(value, null, 2)
}
onMounted(load)
</script>

<style scoped>
.audit-log-page { gap: var(--gi-spacing-md); }
.more-filters { margin-top: 16px; }
.more-filters :deep(.ant-select) { min-width: 160px; }
.detail-layout { width: 100%; }
.detail-block h4 { margin: 12px 0 8px; }
.detail-block pre { white-space: pre-wrap; overflow-wrap: anywhere; background: #f4f7f8; padding: 12px; border-radius: 6px; }
</style>
