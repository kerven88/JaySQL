<template>
  <div class="gi-page-shell feedback-page">
    <!-- 顶部工具栏 -->
    <div class="gi-page-toolbar">
      <div class="page-title">反馈管理</div>
      <div class="gi-page-toolbar-right">
        <a-select
          v-model:value="query.status"
          class="filter-select"
          placeholder="处理状态"
          allow-clear
          :options="statusFilterOptions"
          @change="handleSearch"
        />
        <a-select
          v-model:value="query.category"
          class="filter-select"
          placeholder="问题类型"
          allow-clear
          :options="categoryFilterOptions"
          @change="handleSearch"
        />
        <a-input-search
          v-model:value="query.keyword"
          placeholder="搜索描述 / 联系方式 / 提交人 / 编号"
          class="toolbar-search"
          allow-clear
          @search="handleSearch"
        />
        <a-button class="toolbar-refresh" :loading="uiState.loading" @click="loadAll">
          <template #icon><ReloadOutlined /></template>
          刷新
        </a-button>
      </div>
    </div>

    <!-- 列表 -->
    <a-card :bordered="false" class="plain-card">
      <a-table
        :columns="columns"
        :data-source="tableData"
        :loading="uiState.loading"
        :pagination="pagination"
        row-key="id"
        table-layout="fixed"
        @change="handleTableChange"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'feedback_no'">
            <span class="mono-text">{{ record.feedback_no || '-' }}</span>
          </template>

          <template v-else-if="column.key === 'category'">
            <span>{{ categoryLabel(record.category) }}</span>
          </template>

          <template v-else-if="column.key === 'username'">
            <span>{{ record.username || '-' }}</span>
          </template>

          <template v-else-if="column.key === 'content'">
            <span class="content-wrap" :title="record.content">{{ record.content }}</span>
          </template>

          <template v-else-if="column.key === 'images'">
            <span>{{ imageCount(record) ? `${imageCount(record)} 张` : '-' }}</span>
          </template>

          <template v-else-if="column.key === 'status'">
            <a-tag :color="statusColor(record.status)">{{ statusLabel(record.status) }}</a-tag>
          </template>

          <template v-else-if="column.key === 'created_at'">
            <span class="time-text">{{ record.created_at || '-' }}</span>
          </template>

          <template v-else-if="column.key === 'actions'">
            <div class="gi-table-actions">
              <a-button size="small" type="link" @click="openDetail(record)">查看</a-button>
              <a-button v-if="canManage" size="small" type="link" danger @click="handleDelete(record)">删除</a-button>
            </div>
          </template>
        </template>
      </a-table>
    </a-card>

    <!-- 详情抽屉 -->
    <a-drawer
      :open="detail.open"
      title="反馈详情"
      :width="'620'"
      :destroy-on-close="true"
      @close="detail.open = false"
    >
      <div v-if="detail.record" class="detail-body">
        <a-descriptions :column="1" bordered size="small">
          <a-descriptions-item label="编号">
            <span class="mono-text">{{ detail.record.feedback_no }}</span>
          </a-descriptions-item>
          <a-descriptions-item label="问题类型">{{ categoryLabel(detail.record.category) }}</a-descriptions-item>
          <a-descriptions-item label="提交人">{{ detail.record.username || '-' }}</a-descriptions-item>
          <a-descriptions-item label="提交时间">{{ detail.record.created_at || '-' }}</a-descriptions-item>
          <a-descriptions-item label="联系方式">{{ detail.record.contact || '-' }}</a-descriptions-item>
          <a-descriptions-item label="所在页面">
            <span class="mono-text">{{ detail.record.page_path || '-' }}</span>
          </a-descriptions-item>
          <a-descriptions-item label="浏览器">
            <span class="ua-text">{{ detail.record.user_agent || '-' }}</span>
          </a-descriptions-item>
        </a-descriptions>

        <div class="detail-block">
          <div class="block-title">意见描述</div>
          <div class="content-full">{{ detail.record.content }}</div>
        </div>

        <div class="detail-block">
          <div class="block-title">图片（{{ imageList(detail.record).length }}）</div>
          <div v-if="imageList(detail.record).length" class="detail-images">
            <a-image
              v-for="(url, idx) in imageList(detail.record)"
              :key="idx"
              :src="url"
              :width="96"
              :height="96"
              class="detail-image"
              :preview="{ src: url }"
            />
          </div>
          <div v-else class="empty-text">无</div>
        </div>

        <div class="detail-block">
          <div class="block-title">处理</div>
          <a-form layout="vertical" :disabled="!canManage">
            <a-form-item label="处理状态">
              <a-select v-model:value="editForm.status" :options="statusOptions" />
            </a-form-item>
            <a-form-item label="处理备注">
              <a-textarea
                v-model:value="editForm.remark"
                :maxlength="1024"
                :auto-size="{ minRows: 3, maxRows: 6 }"
                show-count
                placeholder="记录处理过程 / 回复内容（可选）"
              />
            </a-form-item>
          </a-form>
          <div v-if="detail.record.handled_by" class="handled-hint">
            最近处理：{{ detail.record.handled_by }} · {{ detail.record.handled_at || '-' }}
          </div>
        </div>
      </div>

      <template #footer>
        <div class="drawer-footer">
          <a-button v-if="canManage" danger :loading="uiState.deleting" @click="handleDelete(detail.record)">删除</a-button>
          <span class="footer-spacer"></span>
          <a-button @click="detail.open = false">关闭</a-button>
          <a-button v-if="canManage" type="primary" :loading="uiState.saving" @click="handleSave">保存</a-button>
        </div>
      </template>
    </a-drawer>
  </div>
</template>

<script setup>
defineOptions({ name: 'FeedbackList' })

import { computed, onMounted, reactive, ref } from 'vue'
import { message, Modal } from 'ant-design-vue'
import { ReloadOutlined } from '@ant-design/icons-vue'
import {
  deleteFeedbackApi,
  getFeedbackListApi,
  updateFeedbackApi,
} from '@/api/feedback'
import { useUserStore } from '@/store/user'

const userStore = useUserStore()
const canManage = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.feedback.manage'),
)

const STATUS_MAP = {
  pending: { label: '待处理', color: 'orange' },
  processing: { label: '处理中', color: 'blue' },
  resolved: { label: '已解决', color: 'green' },
  closed: { label: '已关闭', color: 'default' },
}
const CATEGORY_MAP = {
  ai_assistant: 'AI助手',
  sql_workbench: 'SQL工作台',
  backup_restore: '备份恢复',
  performance: '性能分析',
  security_audit: '安全审计',
  system_admin: '系统管理',
  other: '其他',
}

const statusOptions = Object.keys(STATUS_MAP).map((k) => ({ label: STATUS_MAP[k].label, value: k }))
const statusFilterOptions = statusOptions
const categoryFilterOptions = Object.keys(CATEGORY_MAP).map((k) => ({ label: CATEGORY_MAP[k], value: k }))

const columns = [
  { title: '编号', dataIndex: 'feedback_no', key: 'feedback_no' },
  { title: '问题类型', dataIndex: 'category', key: 'category' },
  { title: '提交人', dataIndex: 'username', key: 'username' },
  // 意见描述固定占 15% 列宽，内容超出后换行显示
  { title: '意见描述', dataIndex: 'content', key: 'content', width: '15%' },
  { title: '图片', dataIndex: 'images', key: 'images' },
  { title: '状态', dataIndex: 'status', key: 'status' },
  { title: '提交时间', dataIndex: 'created_at', key: 'created_at' },
  { title: '操作', key: 'actions' },
]

const query = reactive({ status: undefined, category: undefined, keyword: '' })
const uiState = reactive({ loading: false, saving: false, deleting: false })
const tableData = ref([])
const pager = reactive({ current: 1, pageSize: 20, total: 0 })
const detail = reactive({ open: false, record: null })
const editForm = reactive({ status: 'pending', remark: '' })

const pagination = computed(() => ({
  current: pager.current,
  pageSize: pager.pageSize,
  total: pager.total,
  showSizeChanger: true,
  pageSizeOptions: ['10', '20', '50', '100'],
  showTotal: (total) => `共 ${total} 条`,
}))

const categoryLabel = (v) => CATEGORY_MAP[v] || v || '-'
const statusLabel = (v) => STATUS_MAP[v]?.label || v || '-'
const statusColor = (v) => STATUS_MAP[v]?.color || 'default'
const imageList = (record) => (Array.isArray(record?.images) ? record.images.filter(Boolean) : [])
const imageCount = (record) => imageList(record).length

const loadList = async () => {
  uiState.loading = true
  try {
    const res = await getFeedbackListApi({
      status: query.status || '',
      category: query.category || '',
      keyword: query.keyword || '',
      page: pager.current,
      page_size: pager.pageSize,
    })
    tableData.value = res?.data || []
    pager.total = res?.total || 0
  } catch (_) {
    tableData.value = []
    pager.total = 0
  } finally {
    uiState.loading = false
  }
}

const loadAll = () => {
  loadList()
}

const handleSearch = () => {
  pager.current = 1
  loadList()
}

const handleTableChange = (pag) => {
  pager.current = pag?.current || 1
  pager.pageSize = pag?.pageSize || 20
  loadList()
}

const openDetail = (record) => {
  detail.record = record
  editForm.status = record.status || 'pending'
  editForm.remark = record.remark || ''
  detail.open = true
}

const handleSave = async () => {
  if (!detail.record) return
  uiState.saving = true
  try {
    const res = await updateFeedbackApi(detail.record.id, {
      status: editForm.status,
      remark: editForm.remark || '',
    })
    message.success('已更新')
    if (res?.data) {
      detail.record = res.data
    }
    loadAll()
  } catch (err) {
    message.error(err?.message || err?.data?.message || '更新失败')
  } finally {
    uiState.saving = false
  }
}

const handleDelete = (record) => {
  if (!record) return
  Modal.confirm({
    title: '删除反馈',
    content: `确认删除反馈「${record.feedback_no || record.id}」？该操作不可恢复。`,
    okText: '删除',
    okType: 'danger',
    cancelText: '取消',
    onOk: async () => {
      uiState.deleting = true
      try {
        await deleteFeedbackApi(record.id)
        message.success('已删除')
        if (detail.record?.id === record.id) {
          detail.open = false
        }
        loadAll()
      } catch (err) {
        message.error(err?.message || err?.data?.message || '删除失败')
      } finally {
        uiState.deleting = false
      }
    },
  })
}

onMounted(() => {
  loadAll()
})
</script>

<style scoped>
.feedback-page {
  padding-bottom: 8px;
}
.page-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--gi-color-text-primary, #0f1720);
}
.gi-page-toolbar-right {
  display: flex;
  flex-wrap: nowrap;
  align-items: center;
  gap: 10px;
  white-space: nowrap;
}
/* 处理状态 / 问题类型 / 搜索框 保持统一尺寸与外观，同处一行且不换行 */
.filter-select {
  width: 140px;
  flex: none;
}
.toolbar-search {
  width: 320px;
  flex: none;
}
/* 筛选控件与搜索框尺寸、圆角保持一致（默认控件高度 32px） */
.toolbar-search :deep(.ant-input-affix-wrapper),
.toolbar-search :deep(.ant-input-search-button) {
  height: 32px;
}
.filter-select :deep(.ant-select-selector) {
  height: 32px;
}
.toolbar-refresh {
  flex: none;
}

.plain-card {
  background: var(--gi-color-container-bg, #fff);
  border-radius: var(--gi-radius-card, 12px);
}
.mono-text {
  font-family: 'SFMono-Regular', Menlo, Consolas, monospace;
  font-size: 12.5px;
}
.time-text {
  font-size: 12.5px;
  color: var(--gi-color-text-secondary, rgba(36, 57, 71, 0.72));
}
/* 意见描述：超出列宽后换行显示 */
.content-wrap {
  display: block;
  white-space: normal;
  word-break: break-word;
  overflow-wrap: anywhere;
  line-height: 1.6;
}

/* 详情抽屉 */
.detail-body {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.detail-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.block-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--gi-color-text-primary, #0f1720);
}
.content-full {
  font-size: 13.5px;
  line-height: 1.75;
  color: var(--gi-color-text-primary, #0f1720);
  white-space: pre-wrap;
  word-break: break-word;
  background: #f7fafb;
  border-radius: var(--gi-radius-md, 8px);
  padding: 12px 14px;
}
.detail-images {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}
.detail-image {
  border-radius: var(--gi-radius-md, 8px);
  overflow: hidden;
  border: 1px solid var(--gi-color-border, #d9e1e6);
}
.empty-text {
  font-size: 13px;
  color: var(--gi-color-text-tertiary, #4f6470);
}
.ua-text {
  font-size: 12px;
  color: var(--gi-color-text-secondary, rgba(36, 57, 71, 0.72));
  word-break: break-all;
}
.handled-hint {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #4f6470);
}
.drawer-footer {
  display: flex;
  align-items: center;
  gap: 10px;
}
.footer-spacer {
  flex: 1;
}

@media (max-width: 768px) {
  /* 工具栏原本 nowrap 且没有 overflow 兜底，窄屏控件会被直接裁掉 */
  .gi-page-toolbar-right {
    flex-wrap: wrap !important;
    white-space: normal !important;
    width: 100%;
  }

  .filter-select,
  .toolbar-search {
    width: 100% !important;
    flex: 1 1 100% !important;
  }

  .toolbar-refresh {
    width: 100%;
  }

  .feedback-page :deep(.ant-table) {
    min-width: 880px;
  }

  .detail-images :deep(.ant-image-img) {
    width: 100% !important;
    height: auto !important;
  }
}
</style>
