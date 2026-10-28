<template>
  <div class="gi-page-shell restore-detail-page">
    <RestoreRunDetailPanel
      :run="run"
      :items="items"
      :log-excerpt="logState.content"
      :loading="uiState.loading"
      :loading-action="uiState.actionLoading"
      :refresh-seconds="POLL_INTERVAL_MS / 1000"
      close-text="返回列表"
      @close="handleBack"
      @refresh="loadDetail"
      @view-log="openLog(0)"
      @stop="handleStop"
      @download-log="handleDownloadLog"
    />

    <a-drawer
      :open="logState.open"
      width="900"
      title="执行日志"
      placement="right"
      @close="logState.open = false"
    >
      <template #extra>
        <a-space>
          <a-button size="small" @click="refreshLog">刷新</a-button>
          <a-button size="small" @click="copyLog">复制</a-button>
          <a-button v-if="!isPhysical" size="small" @click="handleDownloadLog">下载</a-button>
        </a-space>
      </template>
      <div ref="logScrollRef" class="log-content">
        <pre class="log-content-inner">{{ logState.content || '暂无日志内容' }}</pre>
      </div>
    </a-drawer>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { message } from 'ant-design-vue'

import {
  downloadRestoreRunLogApi,
  getPhysicalRestoreRunDetailApi,
  getPhysicalRestoreRunLogApi,
  getRestoreRunDetailApi,
  getRestoreRunLogApi,
  stopPhysicalRestoreRunApi,
  stopRestoreRunApi,
} from '@/api/backup'

import RestoreRunDetailPanel from './RestoreRunDetailPanel.vue'
import { isActiveStatus } from './constants'

defineOptions({ name: 'RestoreDetailView' })

const route = useRoute()
const router = useRouter()

const POLL_INTERVAL_MS = 3000

const runID = computed(() => Number(route.params.id || route.query.detail_id || 0) || 0)
const isPhysical = computed(() => String(route.query.kind || '').toLowerCase() === 'physical')

const uiState = reactive({ loading: false, actionLoading: false })
const run = ref(null)
const items = ref([])
const logState = reactive({ open: false, loading: false, content: '', itemID: 0 })
const logScrollRef = ref()
let pollTimer = null

// ==================== 数据 ====================
const loadDetail = async () => {
  if (!runID.value) return
  try {
    const res = isPhysical.value
      ? await getPhysicalRestoreRunDetailApi(runID.value)
      : await getRestoreRunDetailApi(runID.value)
    const payload = res?.data || {}
    run.value = payload.run || payload
    items.value = Array.isArray(payload.items) ? payload.items : []
  } catch (err) {
    message.error(err?.response?.data?.message || '恢复任务详情加载失败')
  }
}

const startPolling = () => {
  stopPolling()
  pollTimer = window.setInterval(async () => {
    await loadDetail()
    if (logState.open) await refreshLog()
    if (!isActiveStatus(run.value?.status)) stopPolling()
  }, POLL_INTERVAL_MS)
}

const stopPolling = () => {
  if (pollTimer) {
    window.clearInterval(pollTimer)
    pollTimer = null
  }
}

const refreshLog = async () => {
  if (!runID.value) return
  logState.loading = true
  try {
    const res = isPhysical.value
      ? await getPhysicalRestoreRunLogApi(runID.value, { item_id: logState.itemID })
      : await getRestoreRunLogApi(runID.value, { item_id: logState.itemID })
    const payload = res?.data || {}
    logState.content = payload.log_excerpt || payload.content || ''
  } catch (err) {
    logState.content = `日志读取失败：${err?.response?.data?.message || err?.message || '未知错误'}`
  } finally {
    logState.loading = false
  }
}

const openLog = async (itemID = 0) => {
  // 防御：@click 直绑时 Vue 会把 MouseEvent 当首参传入，Number(event) 为 NaN，
  // 曾导致请求 item_id=NaN 被后端 ParseUint 拒绝（「日志读取失败」）。
  const safeID = typeof itemID === 'number' && Number.isFinite(itemID) ? itemID : 0
  logState.itemID = Number(safeID || 0)
  logState.open = true
  await refreshLog()
}

const copyLog = async () => {
  try {
    await navigator.clipboard.writeText(logState.content || '')
    message.success('日志已复制')
  } catch {
    message.warning('当前浏览器不支持自动复制，请手动选择文本')
  }
}

const handleDownloadLog = async () => {
  try {
    await downloadRestoreRunLogApi(runID.value, { item_id: logState.itemID })
    message.success('已开始下载日志')
  } catch (err) {
    message.error(err?.response?.data?.message || '日志下载失败')
  }
}

// ==================== 操作 ====================
const handleStop = async () => {
  uiState.actionLoading = true
  try {
    const api = isPhysical.value ? stopPhysicalRestoreRunApi : stopRestoreRunApi
    await api(runID.value)
    message.success('已提交终止请求')
    await loadDetail()
  } catch (err) {
    message.error(err?.response?.data?.message || '终止失败')
  } finally {
    uiState.actionLoading = false
  }
}

const handleBack = () => router.push({ path: '/backup/restore' })

onMounted(async () => {
  uiState.loading = true
  await loadDetail()
  uiState.loading = false
  if (isActiveStatus(run.value?.status)) startPolling()
})

onBeforeUnmount(stopPolling)
</script>

<style scoped lang="scss">
.restore-detail-page {
  // 面板不再自带滚动（与弹窗保持一致，滚动条统一在外层），
  // 这里提供定高外壳 + 纵向滚动，面板 header/footer 靠 sticky 吸顶吸底。
  height: calc(100vh - 120px);
  min-height: 520px;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-gutter: stable;
}

.log-content {
  max-height: calc(100vh - 180px);
  overflow: auto;
}

.log-content-inner {
  margin: 0;
  padding: var(--gi-spacing-sm, 8px);
  background: #f6f8f9;
  border-radius: var(--gi-radius-sm, 6px);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: var(--gi-font-size-caption, 12px);
  white-space: pre-wrap;
  word-break: break-all;
}

@media (max-width: 768px) {
  :deep(.ant-space) {
    flex-wrap: wrap;
    row-gap: 8px;
  }
}
</style>
