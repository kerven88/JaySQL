<template>
  <div class="gi-page-shell">
    <!-- 页面标题在内容卡片之外；搜索框与新增按钮恒为同一行、任何宽度都不换行 -->
    <div class="gi-page-toolbar gi-toolbar-single-line">
      <div class="gi-page-title">SQL 规范</div>
      <div class="gi-toolbar-actions">
        <a-input-search
          v-model:value="uiData.searchValue"
          placeholder="搜索规范名称..."
          class="gi-toolbar-search"
          @search="handleSearch"
        />
        <a-button v-if="canManage" type="primary" @click="handleAdd">
          <PlusOutlined />新建规范
        </a-button>
      </div>
    </div>

    <!-- 表格 -->
    <a-card>
      <PageTableSection>
      <a-table
        size="middle"
        :columns="uiData.tableColumns"
        :row-key="(record) => record.id"
        :scroll="{ x: 1100 }"
        :data-source="uiData.tableData"
        :pagination="pagination"
        :loading="uiState.loading"
        @change="handleTableChange"
      >
        <template #bodyCell="{ column, record }">
          <!-- 规范名称点击进入详情 -->
          <template v-if="column.key === 'name'">
            <a class="standard-name" @click="handleViewDetail(record)">{{ record.name }}</a>
          </template>

          <!-- 管理实例：第二列，点击实例数进入维护弹窗 -->
          <template v-if="column.key === 'instances'">
            <a class="instance-count-link" @click="handleManageInstances(record)">
              {{ record.instance_count || 0 }} 个实例
            </a>
          </template>

          <!-- 描述 -->
          <template v-if="column.key === 'description'">
            <span :class="{ 'cell-empty': !record.description }">{{ record.description || '-' }}</span>
          </template>

          <!-- 创建人 -->
          <template v-if="column.key === 'created_by'">
            <span :class="{ 'cell-empty': !record.created_by }">{{ record.created_by || '-' }}</span>
          </template>

          <!-- 创建时间 -->
          <template v-if="column.key === 'created_at'">
            <span :class="{ 'cell-empty': !record.created_at }">{{ record.created_at || '-' }}</span>
          </template>

          <template v-if="column.key === 'action'">
            <a-space class="gi-table-actions">
              <a-button type="link" size="small" @click="handleViewDetail(record)">编辑</a-button>
              <a-button
                v-if="!record.is_default"
                type="link"
                size="small"
                danger
                :disabled="!canManage"
                @click="handleDelete(record)"
              >删除</a-button>
            </a-space>
          </template>
        </template>
      </a-table>
      </PageTableSection>
    </a-card>
  </div>

  <!-- 新增弹窗（列表已移除【编辑】入口，规范基础信息在详情页维护） -->
  <a-modal
    v-model:open="uiState.isModalOpen"
    title="新建规范"
    :confirm-loading="uiState.submitting"
    @ok="onSubmit"
    @cancel="uiState.isModalOpen = false"
  >
    <a-form :model="formState" layout="vertical">
      <a-form-item label="规范名称" required>
        <a-input v-model:value="formState.name" placeholder="请输入规范名称" />
      </a-form-item>
      <a-form-item label="规范描述">
        <a-textarea v-model:value="formState.description" :rows="3" placeholder="请输入规范描述" />
      </a-form-item>
      <a-form-item label="是否启用">
        <a-switch v-model:checked="formState.enabled" />
      </a-form-item>
    </a-form>
  </a-modal>

  <!-- 管理实例弹窗 -->
  <SQLStandardInstanceModal
    v-model:open="uiState.isInstanceModalOpen"
    :standard="uiState.currentStandard"
    @change="fetchData"
  />
</template>

<script setup>
import {
  createSQLStandardApi,
  deleteSQLStandardApi,
  getSQLStandardsApi,
} from '@/api/admin'
import PageTableSection from '@/components/patterns/PageTableSection.vue'
import { PlusOutlined } from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useUserStore } from '@/store/user'
import SQLStandardInstanceModal from './SQLStandardInstanceModal.vue'

const router = useRouter()
const userStore = useUserStore()

const canManage = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.inspect.manage'),
)

// 状态
const uiState = reactive({
  loading: false,
  submitting: false,
  isModalOpen: false,
  isInstanceModalOpen: false,
  currentStandard: null,
})

// 数据
const uiData = reactive({
  searchValue: '',
  tableData: [],
  tableColumns: [
    {
      title: '规范名称',
      dataIndex: 'name',
      key: 'name',
      fixed: 'left',
      width: '22%',
      ellipsis: true,
    },
    {
      title: '管理实例',
      dataIndex: 'instances',
      key: 'instances',
      width: '12%',
    },
    {
      title: '描述',
      dataIndex: 'description',
      key: 'description',
      width: '24%',
      ellipsis: true,
    },
    {
      title: '创建人',
      dataIndex: 'created_by',
      key: 'created_by',
      width: '12%',
      ellipsis: true,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: '16%',
    },
    {
      title: '操作',
      dataIndex: 'action',
      key: 'action',
      width: '14%',
    },
  ],
})

// form表单
const defaultForm = {
  id: undefined,
  name: '',
  description: '',
  enabled: true,
}
const formState = ref({ ...defaultForm })

// 搜索
const handleSearch = (value) => {
  uiData.searchValue = value
  pagination.current = 1
  fetchData()
}

// 分页
const pagination = reactive({
  current: 1,
  pageSize: 10,
  total: 0,
  pageSizeOptions: ['10', '20', '50', '100'],
  showSizeChanger: true,
})

// 翻页
const handleTableChange = (pager) => {
  pagination.current = pager.current
  pagination.pageSize = pager.pageSize
  fetchData()
}

// 获取列表数据
const fetchData = async () => {
  uiState.loading = true
  const params = {
    page_size: pagination.pageSize,
    page: pagination.current,
    is_page: true,
    search: uiData.searchValue,
  }
  const res = await getSQLStandardsApi(params).catch(() => {})
  if (res) {
    pagination.total = res.total
    uiData.tableData = res.data
  }
  uiState.loading = false
}

// 查看详情
const handleViewDetail = (record) => {
  router.push({ name: 'view.admin.sql_standard_detail', params: { id: record.id } })
}

// 新增
const handleAdd = () => {
  formState.value = { ...defaultForm }
  uiState.isModalOpen = true
}

// 提交
const onSubmit = async () => {
  if (!formState.value.name) {
    message.warning('请输入规范名称')
    return
  }
  uiState.submitting = true
  const res = await createSQLStandardApi({ ...formState.value }).catch(() => {})
  uiState.submitting = false
  if (res) {
    message.success('操作成功')
    uiState.isModalOpen = false
    fetchData()
  }
}

// 管理实例
const handleManageInstances = (record) => {
  uiState.currentStandard = { ...record }
  uiState.isInstanceModalOpen = true
}

// 删除
const handleDelete = async (record) => {
  const res = await deleteSQLStandardApi(record.id).catch(() => {})
  if (res) {
    message.success('删除成功')
    fetchData()
  }
}

// 初始化
onMounted(() => {
  fetchData()
})
</script>

<style scoped>
.standard-name {
  color: var(--ant-color-primary, #0f766e);
}

.instance-count-link {
  color: var(--gi-color-primary, #0f766e);
}

.cell-empty {
  color: rgba(0, 0, 0, 0.35);
}
</style>
