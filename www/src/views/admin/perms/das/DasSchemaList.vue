<template>
  <DasSchemaDetail v-if="isDetailMode" />

  <div v-else class="gi-page-shell das-page">
    <!-- 页面标题在内容卡片之外；搜索框 / 下拉框 / 新增按钮与标题恒为同一行，任何宽度都不换行 -->
    <div class="gi-page-toolbar gi-toolbar-single-line">
      <div class="gi-page-title">数据访问</div>
      <div class="gi-toolbar-actions">
        <a-input-search
          v-model:value="uiData.searchValue"
          placeholder="搜索用户、角色、实例、环境或资源"
          class="gi-toolbar-search"
          style="width: 320px;"
          size="middle"
          allow-clear
          @search="handleSearch"
          @change="(e) => { if (!e.target.value) { uiData.searchValue = ''; pagination.current = 1; refreshTableData(); } }"
        />
        <a-select
          v-model:value="uiData.filters.principal_type"
          :options="principalTypeFilterOptions"
          style="width: 130px;"
          placeholder="对象类型"
          size="middle"
          allow-clear
          @change="handleFilterChange"
        />
        <a-button type="primary" :disabled="!canManageDas" @click="handleAdd">
          <PlusOutlined />新增资源访问权限
        </a-button>
      </div>
    </div>

    <a-card>
      <PageTableSection>
        <a-table
          size="middle"
          :columns="uiData.tableColumns"
          :row-key="(record) => record.key"
          :data-source="uiData.tableData"
          :pagination="pagination"
          :loading="uiState.loading"
          @change="handleTableChange"
          :scroll="{ x: 820 }"
        >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'principal_type_label'">
            <span class="gi-table-text">
              {{ record.principal_type === 'role' ? '角色' : '用户' }}
            </span>
          </template>

          <template v-else-if="column.key === 'principal_name'">
            <div class="principal-cell">
              <div class="principal-cell__title-row">
                <div class="gi-table-text" :class="{ 'gi-table-text--muted': !!record.principal_deleted_hint }">
                  {{ record.principal_name }}
                </div>
                <span v-if="record.principal_deleted_hint" class="gi-table-text gi-table-text--muted">
                  （{{ record.principal_deleted_hint }}）
                </span>
              </div>
            </div>
          </template>

          <template v-else-if="column.key === 'resource_stat'">
            <span class="gi-table-text">
              {{ record.resourceCount }} 项
            </span>
          </template>

          <template v-else-if="column.key === 'created_at'">
            <span class="gi-table-text">{{ formatDateTimeText(record.earliestCreatedAt) }}</span>
          </template>

          <template v-else-if="column.key === 'updated_at'">
            <span class="gi-table-text">{{ formatDateTimeText(record.latestUpdatedAt || record.earliestCreatedAt) }}</span>
          </template>

          <template v-else-if="column.key === 'action'">
            <div class="action-cell">
              <a-dropdown>
                <a-button type="text" class="more-action-btn">
                  更多
                  <DownOutlined />
                </a-button>
                <template #overlay>
                  <a-menu>
                    <a-menu-item v-if="canManageDas" key="edit" @click="openEdit(record)">
                      <EditOutlined /> 编辑
                    </a-menu-item>
                    <a-menu-divider v-if="canManageDas" />
                    <a-menu-item v-if="canManageDas" key="delete">
                      <a-popconfirm
                        title="确认删除该对象的全部资源访问权限吗？删除后无法恢复。"
                        ok-text="是"
                        cancel-text="否"
                        @confirm="handleDeleteAll(record)"
                      >
                        <span> <DeleteOutlined /> 删除 </span>
                      </a-popconfirm>
                    </a-menu-item>
                  </a-menu>
                </template>
              </a-dropdown>
            </div>
          </template>
        </template>
      </a-table>
      </PageTableSection>
    </a-card>
  </div>

  <DasSchemaFormModal
    :users="uiData.users"
    :roles="uiData.roles"
    :environments="uiData.environments"
    :open="uiState.isModalOpen"
    :can-manage="canManageDas"
    title="新增资源权限"
    @update:open="uiState.isModalOpen = $event"
    @submit="onSubmit"
  />
</template>

<script setup>
import {
  createDasSchemasGrantApi,
  getDasDataGrantsApi,
  getEnvironmentsApi,
  getRolesApi,
  getUsersApi,
  revokeDasDataGrantApi,
} from '@/api/admin'
import PageTableSection from '@/components/patterns/PageTableSection.vue'
import {
  DeleteOutlined,
  DownOutlined,
  EditOutlined,
  PlusOutlined,
} from '@ant-design/icons-vue'
import { useThrottleFn } from '@vueuse/core'
import { message } from 'ant-design-vue'
import { computed, onMounted, reactive } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUserStore } from '@/store/user'
import DasSchemaFormModal from './DasSchemaFormModal.vue'
import DasSchemaDetail from './DasSchemaDetail.vue'
import { buildGroupRecords, matchGroup } from './grouping'
import { normalizeDataGrantRows } from './dataGrantAdapter'

const router = useRouter()
const route = useRoute()
const userStore = useUserStore()
const canManageDas = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.das.manage'),
)

const uiState = reactive({
  loading: false,
  isModalOpen: false,
})

const uiData = reactive({
  searchValue: '',
  filters: {
    principal_type: undefined,
  },
  users: [],
  roles: [],
  environments: [],
  rawRecords: [],
  groupedRecords: [],
  tableData: [],
  tableColumns: [
    {
      title: '对象类型',
      dataIndex: 'principal_type_label',
      key: 'principal_type_label',
      fixed: 'left',
      width: 110,
      align: 'left',
    },
    {
      title: '对象名称',
      dataIndex: 'principal_name',
      key: 'principal_name',
      width: 170,
      align: 'left',
    },
    {
      title: '资源数',
      dataIndex: 'resource_stat',
      key: 'resource_stat',
      width: 110,
      align: 'left',
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      align: 'left',
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      key: 'updated_at',
      width: 180,
      align: 'left',
    },
    {
      title: '操作',
      key: 'action',
      fixed: 'right',
      width: 120,
      align: 'center',
    },
  ],
})

const principalTypeFilterOptions = [
  { label: '用户', value: 'user' },
  { label: '角色', value: 'role' },
]

const pagination = reactive({
  current: 1,
  pageSize: 10,
  total: 0,
  pageSizeOptions: ['10', '20', '50', '100'],
  showSizeChanger: true,
})

const isDetailMode = computed(() => !!route.query.principal_type && !!route.query.principal_id)

const matchFilters = (item) => {
  if (!uiData.filters.principal_type) return true
  return item.principal_type === uiData.filters.principal_type
}

const refreshTableData = () => {
  const filtered = uiData.groupedRecords
    .filter((item) => matchGroup(item, uiData.searchValue))
    .filter((item) => matchFilters(item))
  pagination.total = filtered.length
  const start = (pagination.current - 1) * pagination.pageSize
  uiData.tableData = filtered.slice(start, start + pagination.pageSize)
}

const handleSearch = (value) => {
  uiData.searchValue = value
  pagination.current = 1
  refreshTableData()
}

const handleFilterChange = () => {
  pagination.current = 1
  refreshTableData()
}

const handleTableChange = (pager) => {
  pagination.current = pager.current
  pagination.pageSize = pager.pageSize
  syncRouteQuery()
  refreshTableData()
}

const syncRouteQuery = () => {
  router.replace({
    name: 'view.admin.das',
    query: {
      ...(uiData.searchValue ? { search: uiData.searchValue } : {}),
      ...(pagination.current > 1 ? { page: String(pagination.current) } : {}),
      ...(pagination.pageSize !== 10 ? { page_size: String(pagination.pageSize) } : {}),
    },
  })
}

const initStateFromRoute = () => {
  uiData.searchValue = String(route.query.search || '')
  const page = Number(route.query.page || 1)
  const pageSize = Number(route.query.page_size || 10)
  pagination.current = Number.isFinite(page) && page > 0 ? page : 1
  pagination.pageSize = Number.isFinite(pageSize) && pageSize > 0 ? pageSize : 10
}

const fetchData = async () => {
  uiState.loading = true
  // 数据源已切到新授权表（insight_das_data_grants）：它才是数据可见性的唯一权威来源。
  // 旧表 /admin/das/schemas/grant 只列表自身、看不到 system/self 来源的授权，
  // 会出现「页面上没有、用户却能访问」的口径分裂。
  const res = await getDasDataGrantsApi({ is_page: false, status: 'active' }).catch(() => {})
  if (res) {
    uiData.rawRecords = normalizeDataGrantRows(res.data)
    uiData.groupedRecords = buildGroupRecords(uiData.rawRecords)
    refreshTableData()
  }
  uiState.loading = false
}

const handleAdd = () => {
  getPrincipals()
  getEnvironments()
  uiState.isModalOpen = true
}

const openEdit = (record) => {
  router.push({
    name: 'view.admin.das',
    query: {
      ...(uiData.searchValue ? { search: uiData.searchValue } : {}),
      page: String(pagination.current),
      page_size: String(pagination.pageSize),
      principal_type: record.principal_type,
      principal_id: String(record.principal_id),
    },
  })
}

const formatDateTimeText = (val) => {
  const s = val == null ? '' : String(val).trim()
  if (!s) return '--'
  if (s.startsWith('0001-') || s.startsWith('0000-')) return '--'
  if (s.length >= 19) return s.slice(0, 19).replace('T', ' ')
  return s
}

const onSubmit = useThrottleFn(async (data) => {
  const payload = data && typeof data === 'object' ? { ...data } : {}
  // 新增仍走旧表接口（/admin/das/schemas/grant），这是全页最后一处旧表写入口。
  // 原因：弹窗提交的是 grant_items 语义（实例级 / 库级 / 逐表 allow-deny / 通配规则），
  // 而新表是「一条记录 = 一个资源 + 一组操作」的扁平结构，展开逻辑在后端由
  // ReconcileLegacyGrantsForPrincipal 统一完成：写旧表 → 对账 → 落新表。
  // 读（列表 / 明细 / 已授权提示）与撤权已全部切到新表。
  //
  // 如果弹窗选中的「授权对象」在列表里已经有历史记录，把列表里记住的「第一次授权时间」传给后端，
  // 防止用户在弹窗里直接新增（没进详情页）时，创建时间被刷新
  const pType = payload.principal_type
  const pId = payload.principal_id
  if (pType && pId != null) {
    if (!payload.preserve_first_granted_at) {
      const key = `${pType}:${pId}`
      const existingGroup = uiData.groupedRecords.find((g) => g.key === key)
      const earliest =
        existingGroup && existingGroup.earliestCreatedAt
          ? String(existingGroup.earliestCreatedAt).trim()
          : ''
      if (earliest && !earliest.startsWith('0001-') && !earliest.startsWith('0000-')) {
        payload.preserve_first_granted_at = earliest
      }
    }
  }
  const res = await createDasSchemasGrantApi(payload).catch(() => {})
  if (res) {
    message.success('操作成功')
    uiState.isModalOpen = false
    fetchData()
  }
})

const handleDeleteAll = useThrottleFn(async (record) => {
  try {
    const records = record.records || []
    if (records.length === 0) {
      message.info('暂无待删除的授权')
      return
    }
    // 撤权走新表 revoke：它按 grant_no 定位，能覆盖 system / self / migrate
    // 等所有来源。旧表 delete 只能删自己写的行，system 来源的授权它看不见也删不掉
    // ——那类行正是「页面无记录但实际能访问」的来源。
    const results = await Promise.all(
      records.map((r) =>
        revokeDasDataGrantApi({
          grant_no: r.grant_no,
          reason: '数据访问管理页批量删除',
        }).catch(() => null)
      )
    )
    const failed = results.filter((item) => !item).length
    if (failed > 0) {
      message.warning(`删除完成，其中 ${failed} 条失败，请刷新后重试`)
    } else {
      message.success('删除成功')
    }
    fetchData()
  } catch (err) {
    message.error(err?.message || err?.msg || '删除失败')
  }
})

const getPrincipals = async () => {
  const [userRes, roleRes] = await Promise.all([
    getUsersApi({ is_page: false, is_active_filter: '1' }).catch(() => {}),
    getRolesApi({ is_page: false }).catch(() => {}),
  ])
  uiData.users = userRes?.data || []
  uiData.roles = roleRes?.data || []
}

const getEnvironments = async () => {
  const res = await getEnvironmentsApi().catch(() => {})
  uiData.environments = res?.data || []
}

onMounted(() => {
  initStateFromRoute()
  fetchData()
})
</script>

<style scoped>
/* 工具栏布局由公共规范 .gi-toolbar-single-line + .gi-toolbar-actions 提供
   （components/patterns/pagePatterns.scss），本页不再重复定义。 */
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

.principal-cell__title-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.action-cell {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 2px;
}

.more-action-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: rgba(0, 0, 0, 0.65);
  padding: 0;
  border: 0;
  box-shadow: none;
  background: transparent;
}

.more-action-btn:hover,
.more-action-btn:focus,
.more-action-btn:active {
  border-color: transparent;
  background: transparent;
  box-shadow: none;
  color: rgba(0, 0, 0, 0.88);
}

@media (max-width: 768px) {
  .gi-toolbar-actions > .gi-toolbar-search {
    width: 100% !important;
    flex: 1 1 120px !important;
  }

  .gi-toolbar-actions > .ant-select {
    width: 120px !important;
    flex: 0 1 120px !important;
  }
}
</style>
