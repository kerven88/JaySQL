<template>
  <div v-if="!uiState.bindingManagerOpen" class="gi-page-shell">
    <!-- 页面标题在内容卡片之外；标题与操作组件（新增环境）恒为同一行、任何宽度都不换行 -->
    <div class="gi-page-toolbar gi-toolbar-single-line">
      <div class="gi-page-title">环境管理</div>
      <div class="gi-toolbar-actions">
        <a-button type="primary" :disabled="!canManageEnvironment" @click="handleAdd"><PlusOutlined />新增环境</a-button>
      </div>
    </div>

    <a-card>
      <PageTableSection>
      <a-table
        size="small"
        :columns="uiData.tableColumns"
        :row-key="(record) => record.id"
        :data-source="uiData.tableData"
        :pagination="pagination"
        :loading="uiState.loading"
        @change="handleTableChange"
        :scroll="{ x: 'max-content' }"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'name'">
            <div class="env-name-cell">
              <span class="env-color-dot" :style="{ backgroundColor: resolveEnvironmentColor(record.color) }" />
              <span>{{ record.name }}</span>
            </div>
          </template>
          <template v-else-if="column.key === 'data_source_count'">
            <span class="count-label count-label--plain">{{ record.data_source_count || 0 }} 个数据源</span>
          </template>
          <template v-else-if="column.key === 'sql_standard_name'">
            <span :class="{ 'standard-empty': !record.sql_standard_name }">{{ record.sql_standard_name || '-' }}</span>
          </template>
          <template v-else-if="column.key === 'created_by'">
            {{ record.created_by || '-' }}
          </template>
          <template v-if="column.key === 'action'">
            <a-space>
              <a v-if="canManageEnvironment" class="env-action-link" @click="handleEdit(record)"> <EditOutlined /> 编辑 </a>
              <a v-if="canManageEnvironment" class="env-action-link" @click="handleManageDataSources(record)">管理数据源</a>
              <a-popconfirm
                v-if="canManageEnvironment"
                title="确认删除吗？"
                ok-text="是"
                cancel-text="否"
                @confirm="handleDelete(record)"
              >
                <a class="env-action-link"><DeleteOutlined /> 删除</a>
              </a-popconfirm>
            </a-space>
          </template>
        </template>
      </a-table>
      </PageTableSection>
    </a-card>
  </div>

  <EnvironmentDataSourceManager
    v-else
    :environment="uiState.activeEnvironment"
    :can-manage="canManageEnvironment"
    @back="handleBackToList"
    @saved="handleBindingSaved"
  />

  <!-- 新增/编辑弹窗 -->
  <EnvironmentFormModal
    :open="uiState.isModalOpen"
    v-model:modelValue="formState"
    :can-manage="canManageEnvironment"
    :title="uiState.isEditMode ? '编辑环境' : '新增环境'"
    :mode="uiState.isEditMode ? 'edit' : 'create'"
    @update:open="uiState.isModalOpen = $event"
    @submit="onSubmit"
  />
</template>

<script setup>
import {
  createEnvironmentsApi,
  deleteEnvironmentsApi,
  getEnvironmentsApi,
  updateEnvironmentsApi,
} from '@/api/admin'
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons-vue'
import { useThrottleFn } from '@vueuse/core'
import { message } from 'ant-design-vue'
import { computed, onMounted, reactive, ref } from 'vue'
import PageTableSection from '@/components/patterns/PageTableSection.vue'
import EnvironmentDataSourceManager from './EnvironmentDataSourceManager.vue'
import EnvironmentFormModal from './EnvironmentFormModal.vue'
import { useUserStore } from '@/store/user'

const userStore = useUserStore()
const canManageEnvironment = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.environment.manage'),
)

// 状态
const uiState = reactive({
  loading: false,
  isEditMode: false,
  isModalOpen: false,
  bindingManagerOpen: false,
  activeEnvironment: null,
})

// 数据
const uiData = reactive({
  tableData: [],
  tableColumns: [
    {
      title: '环境名称',
      dataIndex: 'name',
      key: 'name',
      width: 250,
      align: 'left',
    },
    {
      title: '关联数据源数量',
      dataIndex: 'data_source_count',
      key: 'data_source_count',
      width: 200,
      align: 'left',
    },
    {
      title: 'SQL 开发规范',
      dataIndex: 'sql_standard_name',
      key: 'sql_standard_name',
      width: 220,
      align: 'left',
      ellipsis: true,
    },
    {
      title: '创建人',
      dataIndex: 'created_by',
      key: 'created_by',
      width: 120,
      align: 'left',
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 220,
      align: 'left',
    },
    {
      title: '操作',
      dataIndex: 'action',
      key: 'action',
      fixed: 'right',
      width: 270,
      align: 'center',
    },
  ],
})

// form表单
const defaultForm = {
  name: '',
  color: '#1677ff',
  env_type: '',
  sql_standard_id: undefined,
}
const formState = ref({ ...defaultForm })

const resolveEnvironmentColor = (value) => value || '#1677ff'

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

// 新增
const handleAdd = () => {
  uiState.isEditMode = false
  formState.value = { ...defaultForm }
  uiState.isModalOpen = true
}

// 编辑
const handleEdit = (record) => {
  uiState.isEditMode = true
  formState.value = {
    ...defaultForm,
    ...record,
    color: resolveEnvironmentColor(record.color),
  }
  uiState.isModalOpen = true
}

const handleManageDataSources = (record) => {
  uiState.activeEnvironment = { ...record }
  uiState.bindingManagerOpen = true
}

const handleBackToList = () => {
  uiState.bindingManagerOpen = false
  uiState.activeEnvironment = null
}

// 获取列表数据
const fetchData = async () => {
  uiState.loading = true
  const params = {
    page_size: pagination.pageSize,
    page: pagination.current,
    is_page: true,
  }
  const res = await getEnvironmentsApi(params).catch(() => {})
  if (res) {
    pagination.total = res.total
    uiData.tableData = res.data
    if (uiState.activeEnvironment?.id) {
      const latest = (res.data || []).find((item) => item.id === uiState.activeEnvironment.id)
      if (latest) {
        uiState.activeEnvironment = { ...latest }
      }
    }
  }
  uiState.loading = false
}

// 提交（新增或编辑）
const onSubmit = useThrottleFn(async (data) => {
  const payload = {
    ...data,
    data_source_ids: undefined,
  }
  const res = uiState.isEditMode
    ? await updateEnvironmentsApi(payload).catch(() => {})
    : await createEnvironmentsApi(payload).catch(() => {})
  if (res) {
    message.success('操作成功')
    uiState.isModalOpen = false
    fetchData()
  }
})

const handleBindingSaved = async ({ id, data_source_ids }) => {
  await fetchData()
  const latest = uiData.tableData.find((item) => item.id === id)
  if (latest) {
    uiState.activeEnvironment = {
      ...latest,
      data_source_ids: [...data_source_ids],
    }
  }
}

// 删除
const handleDelete = useThrottleFn(async (record) => {
  const res = await deleteEnvironmentsApi(record.id).catch(() => {})
  if (res) {
    message.info('操作成功')
    fetchData()
  }
})

// 初始化
onMounted(() => {
  fetchData()
})
</script>

<style scoped>
.env-name-cell {
  display: flex;
  align-items: center;
  gap: 10px;
}

.env-color-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex: none;
}

.count-label {
  color: rgba(0, 0, 0, 0.65);
  display: inline-block;
  min-width: 88px;
}

.count-label--plain {
  margin-left: 0;
}

.standard-empty {
  color: rgba(0, 0, 0, 0.35);
}

.env-name-cell {
  min-width: 0;
}

:deep(.ant-table-small .ant-table-thead > tr > th) {
  white-space: nowrap;
}

:deep(.ant-table-small .ant-table-tbody > tr > td) {
  vertical-align: middle;
}

:deep(.ant-table-thead > tr > th),
:deep(.ant-table-tbody > tr > td) {
  padding-top: 10px;
  padding-bottom: 10px;
}

.env-action-link {
  color: var(--gi-color-primary, #0f766e);
  cursor: pointer;
  text-decoration: none;
  font-size: 13px;
  &:hover {
    color: var(--gi-color-primary-hover, #0d6b63);
    text-decoration: underline;
  }
  &:active {
    color: var(--gi-color-primary-active, #0b5f58);
  }
}
</style>
