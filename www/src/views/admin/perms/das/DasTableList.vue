<template>
  <a-card title="配置表访问权限">
    <!-- 卡片右上角的新增按钮 -->
    <template #extra>
      <a-button type="primary" :disabled="!canManageDas" @click="handleAdd">
        <PlusOutlined />新增表权限
      </a-button>
    </template>

    <!-- 搜索区域 -->
    <PageToolbar>
      <a-input-search
        v-model:value="uiData.searchValue"
        placeholder="搜索环境名..."
        class="gi-toolbar-search"
        @search="handleSearch"
      />
    </PageToolbar>

    <!-- 表格 -->
    <PageTableSection>
      <a-table
        size="middle"
        :columns="uiData.tableColumns"
        :row-key="(record) => record.id"
        :data-source="uiData.tableData"
        :pagination="pagination"
        :loading="uiState.loading"
        @change="handleTableChange"
        :scroll="{ x: 1100 }"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'rule'">
            <span class="gi-table-text">{{ record.rule }}</span>
          </template>
          <template v-else-if="column.key === 'principal_name'">
            <span class="gi-table-text">{{ record.principal_name }}</span>
          </template>
          <template v-else-if="column.key === 'principal_type'">
            <span class="gi-table-text">{{ record.principal_type === 'role' ? '角色' : '用户' }}</span>
          </template>
          <template v-else-if="column.key === 'schema'">
            <span class="gi-table-text">{{ resolveSchemaDisplay(record) }}</span>
          </template>
          <template v-else-if="column.key === 'table'">
            <span class="gi-table-text">{{ record.table }}</span>
          </template>
          <template v-else-if="column.key === 'created_at'">
            <span class="gi-table-text">{{ formatDateTimeText(record.created_at) }}</span>
          </template>
          <template v-else-if="column.key === 'updated_at'">
            <span class="gi-table-text">{{ formatDateTimeText(record.updated_at || record.created_at) }}</span>
          </template>
          <template v-else-if="column.key === 'action'">
            <a-popconfirm v-if="canManageDas" title="确认删除吗？" ok-text="是" cancel-text="否" @confirm="handleDelete(record)">
              <a>
                <DeleteOutlined /> 删除
              </a>
            </a-popconfirm>
          </template>
        </template>
      </a-table>
    </PageTableSection>
  </a-card>

  <!-- 新增/编辑弹窗 -->
  <DasTableFormModal
    :open="uiState.isModalOpen"
    v-model:modelValue="formState"
    :tables="uiData.tableList"
    :can-manage="canManageDas"
    title="新增表权限"
    @update:open="uiState.isModalOpen = $event"
    @submit="onSubmit"
  />
</template>

<script setup>
import {
  createDasTablesGrantApi,
  getDasDataGrantsApi,
  getDasTablesListApi,
  revokeDasDataGrantApi,
} from '@/api/admin'
import PageTableSection from '@/components/patterns/PageTableSection.vue'
import PageToolbar from '@/components/patterns/PageToolbar.vue'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons-vue'
import { useThrottleFn } from '@vueuse/core'
import { message } from 'ant-design-vue'
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { getPureSchemaDisplayName, isPostgresSchemaDbType } from '@/utils/schemaDisplay'
import { useUserStore } from '@/store/user'
import DasTableFormModal from './DasTableFormModal.vue'
import { normalizeDataGrantRows } from './dataGrantAdapter'

const userStore = useUserStore()
const canManageDas = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.das.manage'),
)

// 路由参数
const route = useRoute()
const routeParams = route.params
const routeQuery = route.query

// 🔴 PIN 住进入这个页面时 URL 带回来的 earliest_created_at（来自详情页的 pinned 值），
//    防止在本页里把所有表级记录删光后再新建时，创建时间被刷新。
const firstAtRaw =
  routeQuery.earliest_created_at || routeQuery.earliestCreatedAt || ''
const pinnedFirstGrantedAt = ref(
  firstAtRaw
    ? (Array.isArray(firstAtRaw) ? firstAtRaw[0] : firstAtRaw)
    : ''
)

const resolveSchemaDisplay = (record) => {
  const dbType = record?.db_type || routeQuery.db_type
  const displayName = record?.display_name || routeQuery.display_name
  if (isPostgresSchemaDbType(dbType)) {
    return getPureSchemaDisplayName({
      dbType,
      schema: record?.schema || routeParams.schema,
      displayName,
      schemaName: record?.schema_name,
      databaseName: record?.database_name,
      parentPath: record?.parent_path,
    }) || displayName || record?.schema || routeParams.schema || ''
  }
  return displayName || record?.schema || routeParams.schema || ''
}

// 状态管理
const uiState = reactive({
  loading: false,
  isModalOpen: false,
})

// 数据
const uiData = reactive({
  searchValue: '',
  tableList: [],
  tableData: [],
  tableColumns: [
    {
      title: '规则',
      dataIndex: 'rule',
      key: 'rule',
    },
    {
      title: '授权对象',
      dataIndex: 'principal_name',
      key: 'principal_name',
    },
    {
      title: '对象类型',
      dataIndex: 'principal_type',
      key: 'principal_type',
    },
    {
      title: '资源',
      dataIndex: 'schema',
      key: 'schema',
    },
    {
      title: '表名',
      dataIndex: 'table',
      key: 'table',
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      key: 'updated_at',
    },
    {
      title: '操作',
      dataIndex: 'action',
      key: 'action',
      fixed: 'right',
    },
  ]
})

// form表单
const defaultForm = {
  tables: [],
  rule: '',
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

// 新增
const handleAdd = () => {
  formState.value = { ...defaultForm }
  getTables()
  uiState.isModalOpen = true
}

// 获取列表数据
const fetchData = async () => {
  uiState.loading = true
  // 与上游两页同源：读新授权表。旧表 /tables/grant 只能看到手工写进去的行，
  // system / self / migrate 来源的表级授权它会整段漏掉。
  // 新表按 schema_name 下钻（后端 DataGrantListForm 新增的过滤键）。
  const res = await getDasDataGrantsApi({
    page_size: pagination.pageSize,
    page: pagination.current,
    is_page: true,
    search: uiData.searchValue,
    status: 'active',
    principal_type: routeParams.principal_type,
    principal_id: routeParams.principal_id,
    instance_id: routeParams.instance_id,
    schema_name: routeParams.schema,
  }).catch(() => { })
  if (res) {
    pagination.total = res.total
    uiData.tableData = normalizeDataGrantRows(res.data)
  }
  uiState.loading = false
}

// 获取指定schema的表
const getTables = async () => {
  const res = await getDasTablesListApi(routeParams).catch(() => { })
  uiData.tableList = res.data || []
}

// 提交表单
const onSubmit = useThrottleFn(async (data) => {
  const payload = {
    ...data,
    ...routeParams,
    resource_type: routeQuery.resource_type || 'database',
  }
  // 编辑场景：优先用 PIN 住的第一次授权时间（页面进入时锁定），没有再退化为 query 里的 earliest_created_at
  const queryFirst =
    routeQuery.earliest_created_at || routeQuery.earliestCreatedAt || ''
  const queryFirstStr = queryFirst
    ? Array.isArray(queryFirst) ? queryFirst[0] : queryFirst
    : ''
  const firstAt = pinnedFirstGrantedAt.value || queryFirstStr || ''
  if (firstAt) {
    payload.preserve_first_granted_at = firstAt
  }
  const res = await createDasTablesGrantApi(payload).catch(() => { })
  if (res) {
    message.success('操作成功')
    uiState.isModalOpen = false
    fetchData()
  }
})

// 删除
const handleDelete = useThrottleFn(async (record) => {
  if (!record?.grant_no) {
    message.error('该记录缺少授权编号，无法撤销')
    return
  }
  const res = await revokeDasDataGrantApi({
    grant_no: record.grant_no,
    reason: `数据访问管理页删除表级授权：${record.table || ''}`,
  }).catch(() => { })
  if (res) {
    message.info('操作成功')
    fetchData()
  }
})

const formatDateTimeText = (val) => {
  const s = val == null ? '' : String(val).trim()
  if (!s) return '--'
  if (s.startsWith('0001-') || s.startsWith('0000-')) return '--'
  if (s.length >= 19) return s.slice(0, 19).replace('T', ' ')
  return s
}

// 初始化
onMounted(() => {
  fetchData()
})
</script>

<style scoped>
.gi-table-text {
  font-size: 14px;
  line-height: 22px;
  font-weight: 400;
  color: rgba(0, 0, 0, 0.88);
  letter-spacing: 0;
}

.gi-table-text--muted {
  color: rgba(0, 0, 0, 0.45);
}
</style>
