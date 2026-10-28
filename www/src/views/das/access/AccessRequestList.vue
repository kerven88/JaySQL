<template>
  <div class="gi-page-shell access-request-page">
    <a-card class="access-request-card">
      <template #title>
        <div class="card-header">
          <div class="card-title">数据访问申请</div>
          <div class="toolbar-scroll">
            <div class="toolbar">
              <div class="toolbar-left">
                <a-select
                  v-model:value="scope"
                  :options="scopeOptions"
                  class="toolbar-scope-select"
                  @change="handleScopeChange"
                />
                <a-select
                  v-model:value="filters.status"
                  :options="statusOptions"
                  allow-clear
                  placeholder="全部状态"
                  class="toolbar-status-select"
                  @change="handleSearch"
                />
                <a-input-search
                  v-model:value="filters.search"
                  placeholder="搜索单号 / 申请人 / 数据源"
                  allow-clear
                  class="toolbar-search"
                  @search="handleSearch"
                />
                <a-tooltip :title="canApply ? '' : '缺少 das.access.apply 权限，无法发起申请'">
                  <span>
                    <a-button type="primary" class="toolbar-create-button" :disabled="!canApply" @click="openCreate">
                      新申请
                    </a-button>
                  </span>
                </a-tooltip>
              </div>
            </div>
          </div>
        </div>
      </template>

      <div class="table-wrapper">
        <a-table
          size="middle"
          row-key="request_no"
          :columns="columns"
          :data-source="rows"
          :loading="loading"
          :pagination="pagination"
          :scroll="{ x: 946, y: 'calc(100vh - 300px)' }"
          @change="handleTableChange"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'request_no'">
              <div class="request-no-row">
                <a class="mono-text request-no-text" :title="record.request_no" @click="openDetail(record)">
                  {{ record.request_no }}
                </a>
                <a-tooltip title="复制申请单号">
                  <button
                    type="button"
                    class="request-no-copy-btn"
                    aria-label="复制申请单号"
                    @click.stop="copyRequestNo(record.request_no)"
                  >
                    <CopyOutlined />
                  </button>
                </a-tooltip>
              </div>
            </template>

            <template v-else-if="column.key === 'instance'">
              <!-- 数据库类型 logo（20px）+ 名称/地址两行，与「数据备份」「数据恢复」页数据源列同构 -->
              <div class="cell-db">
                <img class="db-logo" :src="dbTypeLogo(record.db_type)" :alt="record.db_type || 'db'" />
                <div class="cell-stack">
                  <span :title="instanceTitle(record)">{{ truncate15(instanceTitle(record)) }}</span>
                  <span class="cell-sub" :title="instanceAddr(record)">{{ truncate20(instanceAddr(record)) }}</span>
                </div>
              </div>
            </template>

            <template v-else-if="column.key === 'status'">
              <!-- 纯文本状态：不带色块与圆角底（此前用 a-tag，视觉权重过高，
                   一列彩色徽章反而干扰扫读；状态语义由文案本身承担） -->
              <span class="status-text">{{ accessStatusText(record.effective_status) }}</span>
            </template>

            <template v-else-if="column.key === 'action'">
              <a-space :size="2" class="action-space">
                <a-button type="link" size="small" @click="openDetail(record)">详情</a-button>
                <a-button
                  v-if="canWithdraw(record)"
                  type="link"
                  size="small"
                  @click="handleWithdraw(record)"
                >
                  撤回
                </a-button>
              </a-space>
            </template>
          </template>

          <template #emptyText>
            <a-empty :description="emptyText" />
          </template>
        </a-table>
      </div>
    </a-card>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { message, Modal } from 'ant-design-vue'
import { CopyOutlined } from '@ant-design/icons-vue'
import { useUserStore } from '@/store/user'
import { dbTypeLogo } from '@/utils/dbTypeLogo'
import { GetAccessRequestsApi, WithdrawAccessRequestApi } from '@/api/das'
import {
  accessInApproval,
  accessStatusText,
} from './accessRequestMeta'

defineOptions({ name: 'DasAccessRequestList' })

const router = useRouter()
const userStore = useUserStore()

const canApply = computed(() => userStore.hasPermission('das.access.apply'))

const scope = ref('related')
const scopeOptions = [
  { label: '与我相关', value: 'related' },
  { label: '我的申请', value: 'mine' },
  { label: '待我审批', value: 'pending' },
  { label: '全部记录', value: 'all' },
]
const loading = ref(false)
const rows = ref([])
const filters = reactive({ status: undefined, search: '' })

const pagination = reactive({
  current: 1,
  pageSize: 10,
  total: 0,
  pageSizeOptions: ['10', '20', '50', '100'],
  showSizeChanger: true,
  showTotal: (total) => `共 ${total} 条`,
})

const columns = [
  { title: '申请单号', key: 'request_no', width: 168, fixed: 'left' },
  { title: '数据源', key: 'instance', width: 210 },
  { title: '申请人', dataIndex: 'applicant_username', key: 'applicant', width: 120, align: 'center' },
  { title: '申请时间', dataIndex: 'created_at', key: 'created_at', width: 168 },
  { title: '状态', key: 'status', width: 116, fixed: 'right' },
  { title: '操作', key: 'action', width: 120, fixed: 'right' },
]

// 数据源名称 / IP:port 超过 20 个字符后用 … 截断（按字符数，兼容中文）
const truncate20 = (value) => {
  const chars = [...String(value || '')]
  if (chars.length <= 20) return chars.join('')
  return `${chars.slice(0, 20).join('')}...`
}

// 实例名称（数据源列第一行）单独按 15 个字符截断：该行字号更大，且右侧要留 20px 图标位置
const truncate15 = (value) => {
  const chars = [...String(value || '')]
  if (chars.length <= 15) return chars.join('')
  return `${chars.slice(0, 15).join('')}...`
}

const instanceTitle = (record) => record?.instance_remark || record?.instance_name || '-'
const instanceAddr = (record) => record?.instance_addr || record?.instance_name || '-'

// 与后端状态机一一对应：转交与待执行授权都必须能在筛选里选到，
// 否则「审批通过了但查不到」会被当成丢单
const statusOptions = [
  { label: '待审批', value: 'pending' },
  { label: '已转交', value: 'forwarded' },
  { label: '待执行授权', value: 'wait_grant' },
  { label: '已通过', value: 'approved' },
  { label: '已驳回', value: 'rejected' },
  { label: '已撤回', value: 'withdrawn' },
  { label: '已终止', value: 'terminated' },
  { label: '已过期', value: 'expired' },
  { label: '已回收', value: 'revoked' },
]

const emptyText = computed(() => {
  if (scope.value === 'pending') return '暂无待审批的申请'
  if (scope.value === 'all') return '暂无申请记录'
  if (scope.value === 'related') return '暂无与您相关的申请'
  return '还没有发起过申请'
})

// 复制申请单号：优先剪贴板 API（仅 https/localhost 可用），
// 非安全上下文或权限受限时回落到 execCommand，两条路都失败才提示错误
const copyRequestNo = async (text) => {
  const value = String(text || '')
  if (!value) return
  let ok = false
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value)
      ok = true
    } catch (_) {
      ok = false
    }
  }
  if (!ok) {
    try {
      const input = document.createElement('textarea')
      input.value = value
      input.style.position = 'fixed'
      input.style.opacity = '0'
      document.body.appendChild(input)
      input.select()
      ok = document.execCommand('copy')
      document.body.removeChild(input)
    } catch (_) {
      ok = false
    }
  }
  if (ok) message.success('申请单号已复制')
  else message.error('复制失败，请手动选中复制')
}

// ---------------------------------------------------------------------------
// 列表
// ---------------------------------------------------------------------------
const fetchList = async () => {
  loading.value = true
  try {
    const res = await GetAccessRequestsApi({
      scope: scope.value,
      status: filters.status || '',
      search: filters.search || '',
      page: pagination.current,
      page_size: pagination.pageSize,
      is_page: true,
    }).catch(() => null)
    if (res?.data) {
      rows.value = res.data
      pagination.total = res.total || 0
    } else {
      rows.value = []
      pagination.total = 0
    }
  } finally {
    loading.value = false
  }
}

const handleSearch = () => {
  pagination.current = 1
  fetchList()
}

const handleScopeChange = () => {
  pagination.current = 1
  fetchList()
}

const handleTableChange = (pager) => {
  pagination.current = pager.current
  pagination.pageSize = pager.pageSize
  fetchList()
}

// ---------------------------------------------------------------------------
// 跳转：新建与详情都是独立页，列表不再承载表单与详情抽屉
// ---------------------------------------------------------------------------
const openCreate = () => router.push('/datasource/access/apply')
const openDetail = (record) => {
  if (!record?.request_no) return
  router.push(`/datasource/access/${record.request_no}`)
}

const serverMsg = (res, fallback) => {
  const text = String(res?.message || res?.msg || '').trim()
  return text && text !== 'ok' ? text : fallback
}

// 撤回落在这里而不是详情页：列表上直接撤最顺手。
// 转交后（forwarded）仍属审批中，申请人同样可以撤回。
const canWithdraw = (record) =>
  accessInApproval(record?.status) &&
  String(record?.applicant_username || '') === String(userStore.username || '')

const handleWithdraw = (record) => {
  Modal.confirm({
    title: '撤回申请',
    content: `确定撤回申请单 ${record.request_no} 吗？撤回后需重新发起。`,
    okText: '撤回',
    cancelText: '取消',
    onOk: async () => {
      const res = await WithdrawAccessRequestApi({
        request_no: record.request_no,
        comment: '申请人主动撤回',
      }).catch(() => null)
      if (!res) return
      message.success(serverMsg(res, '已撤回'))
      fetchList()
    },
  })
}

onMounted(fetchList)
</script>

<style scoped>
.access-request-page {
  gap: var(--gi-spacing-sm);
  height: 100%;
  width: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
}

.access-request-card {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  overflow: hidden;
}

.access-request-card :deep(.ant-card-body) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding-top: 4px;
}

.access-request-card :deep(.ant-card-head) {
  flex-shrink: 0;
}

/* 头部行结构与控件样式完全对齐 SQL 工单页（views/orders/list/OrderList.vue）：
   控件统一 32px 高、13px 字号、6px 圆角，工具组随卡片标题行拉伸垂直居中 */
.card-header {
  display: flex;
  align-items: stretch;
  gap: 16px;
  width: 100%;
  min-height: 40px;
}

.card-title {
  flex: 0 0 auto;
  font-weight: 600;
  white-space: nowrap;
  display: inline-flex;
  align-items: center;
}

.toolbar-scroll {
  flex: 1 1 auto;
  overflow-x: auto;
  overflow-y: clip;
  padding: 2px 0;
  min-height: 36px;
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
}

.toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  font-size: 13px;
  padding: 2px 0;
}

.toolbar-left {
  display: flex;
  align-items: center;
  flex-wrap: nowrap;
  gap: 8px;
  margin-left: auto;
}

.toolbar-scope-select {
  width: 110px;
}

.toolbar-status-select {
  width: 120px;
}

.toolbar-search {
  width: 200px;
}

.toolbar-create-button {
  min-width: 114px;
  padding-inline: 12px;
  display: inline-flex !important;
  align-items: center;
  justify-content: center;
  text-align: center;
  line-height: 1.2;
}

.toolbar :deep(.ant-select-selector),
.toolbar :deep(.ant-input-affix-wrapper),
.toolbar :deep(.ant-input),
.toolbar :deep(.ant-btn),
.toolbar :deep(.ant-select-selection-item),
.toolbar :deep(.ant-select-selection-placeholder),
.toolbar :deep(.ant-input) {
  font-size: 13px;
}

.toolbar :deep(.ant-select-selector),
.toolbar :deep(.ant-input-affix-wrapper),
.toolbar :deep(.ant-btn) {
  height: 32px;
  border-radius: 6px;
}

.toolbar :deep(.ant-select-selection-item),
.toolbar :deep(.ant-select-selection-placeholder),
.toolbar :deep(.ant-input) {
  line-height: 30px;
}

.table-wrapper {
  flex: 1 1 auto;
  min-height: 0;
}

.cell-db {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

/* 全站统一：数据库类型 logo 一律 20px */
.db-logo {
  width: 20px;
  height: 20px;
  flex: none;
  object-fit: contain;
}

.cell-stack {
  display: flex;
  flex-direction: column;
  line-height: 1.4;
  min-width: 0;
}

/* 名称行：JS 截断 15 字 + CSS 省略双保险，容器不足时不折行 */
.cell-stack > span:first-child {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cell-sub {
  font-size: 12px;
  color: var(--gi-color-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mono-text {
  font-family: var(--gi-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 12px;
}

/* 状态列：纯文本，颜色与普通单元格一致，不再用彩色徽章 */
.status-text {
  color: inherit;
}

/* 操作列：显式跟随系统主题色，不依赖 antd colorLink 默认值，
   主题调整时与申请单号列（--gi-color-primary）保持同一来源 */
.action-space :deep(.ant-btn-link) {
  color: var(--gi-color-primary);
}

.action-space :deep(.ant-btn-link:not(:disabled):hover) {
  color: var(--gi-color-primary);
  opacity: 0.82;
}

/* 申请单号：与 SQL 工单页「工单ID」列同色，附带复制按钮 */
.request-no-row {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
}

.request-no-text {
  color: var(--gi-color-primary);
  cursor: pointer;
}

.request-no-text:hover {
  text-decoration: underline;
}

.request-no-copy-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: none;
  background: transparent;
  color: #b6c0c8;
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
  flex: 0 0 auto;
  transition: color 0.2s ease;
}

.request-no-copy-btn:hover {
  color: #71818c;
}

@media (max-width: 768px) {
  /* 5 个控件 + 标题挤一行，主按钮会被推到屏幕外 */
  .card-header {
    flex-direction: column;
    align-items: stretch;
    gap: 8px;
  }

  .toolbar-scroll {
    width: 100%;
    min-height: 0;
    justify-content: flex-start;
  }

  .toolbar-left {
    flex-wrap: wrap;
    row-gap: 8px;
    margin-left: 0;
    width: 100%;
  }

  .toolbar-scope-select,
  .toolbar-status-select {
    width: calc(50% - 4px);
  }

  .toolbar-search {
    flex: 1 1 auto;
    width: auto;
    min-width: 120px;
  }

  .toolbar-create-button {
    width: 100%;
    min-width: 0;
  }

  /* 左右固定列合计 404px > 375px，中间列完全没有可视空间 */
  :deep(.ant-table-cell-fix-left),
  :deep(.ant-table-cell-fix-right) {
    position: static !important;
  }

  .table-wrapper {
    overflow-x: auto;
  }
}
</style>
