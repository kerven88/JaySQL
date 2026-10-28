<template>
  <div ref="pageRef" class="gi-page-shell user-page">
    <!-- 满高布局：滚动条只出现在表格表体内部，表头与底部分页固定不动（详见 script 中 updateScrollY） -->
    <!-- 页面标题在内容卡片之外；标题与全部操作组件（搜索框/下拉框/按钮）恒为同一行，任何宽度都不换行 -->
    <div class="gi-page-toolbar gi-toolbar-single-line">
      <div class="gi-page-title">用户管理</div>
      <div class="gi-toolbar-actions">
        <a-input-search
          v-model:value="uiData.filters.search"
          placeholder="搜索用户名 / 邮箱 / 手机号"
          class="gi-toolbar-search"
          style="width: 320px;"
          size="middle"
          allow-clear
          @search="handleSearch"
          @change="(e) => { if (!e.target.value) { uiData.filters.search = ''; pagination.current = 1; fetchData(); } }"
        />
        <a-select
          v-model:value="uiData.filters.is_active_filter"
          :options="statusFilterOptions"
          style="width: 140px;"
          placeholder="状态"
          size="middle"
          allow-clear
          @change="handleFilterChange"
        />
        <a-select
          v-model:value="uiData.filters.role_scope"
          :options="roleScopeFilterOptions"
          style="width: 160px;"
          placeholder="角色类型"
          size="middle"
          allow-clear
          @change="handleFilterChange"
        />
        <a-popconfirm
          title="确认批量删除选中的用户吗？"
          ok-text="是"
          cancel-text="否"
          @confirm="handleBatchDelete"
        >
          <a-button danger :disabled="!canManageUser || !selectedRowKeys.length">批量删除</a-button>
        </a-popconfirm>
        <a-button type="primary" :disabled="!canManageUser" @click="handleAdd"> <PlusOutlined />新增用户 </a-button>
      </div>
    </div>

    <a-card>
      <!-- 表格 -->
      <PageTableSection>
        <a-table
          size="middle"
          :columns="uiData.tableColumns"
          :row-key="(record) => record.uid"
          :data-source="uiData.tableData"
          :pagination="pagination"
          :loading="uiState.loading"
          :row-selection="canManageUser ? rowSelection : undefined"
          @change="handleTableChange"
          :scroll="{ x: 1120, y: tableBodyHeight }"
          :table-layout="'fixed'"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'is_active'">
              {{ record.is_active && !record.is_deleted ? '启用' : '停用' }}
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
                      <a-menu-item key="1">
                        <a @click="handleDetail(record)"> <EyeOutlined /> 详情 </a>
                      </a-menu-item>
                      <a-menu-item v-if="canManageUser" key="2">
                        <a @click="handleEdit(record)"> <EditOutlined /> 编辑 </a>
                      </a-menu-item>
                      <a-menu-item v-if="canManageUser" key="3" :disabled="isCurrentUser(record)">
                        <a-popconfirm
                          :title="isCurrentUser(record) ? '不能删除当前登录用户。' : '确认删除该用户吗？'"
                          :disabled="isCurrentUser(record)"
                          ok-text="是"
                          cancel-text="否"
                          @confirm="handleDelete(record)"
                        >
                          <a :class="{ 'gi-delete-disabled': isCurrentUser(record) }"> <DeleteOutlined /> 删除 </a>
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

  <!-- 新增/编辑弹窗 -->
  <UserFormModal
    :open="uiState.userModalOpen"
    v-model:modelValue="formState"
    :can-manage="canManageUser"
    :roles="uiData.roles"
    :permissions="uiData.permissions"
    :title="uiState.isEditMode ? '编辑用户' : '新增用户'"
    @update:open="uiState.userModalOpen = $event"
    @submit="onSubmit"
  />

  <!-- 用户详情弹窗 -->
  <UserDetailModal
    :open="uiState.detailModalOpen"
    :user="currentUser"
    @update:open="uiState.detailModalOpen = $event"
  />
</template>

<script setup>
import {
  addUsersApi,
  batchDeleteUsersApi,
  deleteUsersApi,
  getPermissionsApi,
  getRolesApi,
  getUserAccessApi,
  getUsersApi,
  updateUsersApi,
} from '@/api/admin'
import {
  DeleteOutlined,
  DownOutlined,
  EditOutlined,
  EyeOutlined,
  PlusOutlined,
} from '@ant-design/icons-vue'
import { useThrottleFn } from '@vueuse/core'
import { message } from 'ant-design-vue'
import { computed, inject, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import PageTableSection from '@/components/patterns/PageTableSection.vue'
import UserDetailModal from './UserDetailModal.vue'
import UserFormModal from './UserFormModal.vue'
import { useUserStore } from '@/store/user'

const userStore = useUserStore()

const canManageUser = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.user.manage'),
)
const selectedRowKeys = ref([])

// 状态管理
const uiState = reactive({
  loading: false,
  isEditMode: false,
  userModalOpen: false,
  detailModalOpen: false,
})

// 数据
const uiData = reactive({
  filters: {
    search: '',
    is_active_filter: undefined,
    role_scope: undefined,
  },
  roles: [],
  permissions: [],
  tableData: [],
  // 各列平均分配宽度：统一 width/minWidth，配合 table-layout: fixed 等分，
  // 窄屏由 scroll.x 触发横向滚动，不做挤压变形。
  tableColumns: [
    {
      title: '用户',
      dataIndex: 'username',
      key: 'username',
      fixed: 'left',
      width: 160,
      minWidth: 140,
      ellipsis: true,
      align: 'left',
    },
    {
      title: '状态',
      dataIndex: 'is_active',
      key: 'is_active',
      width: 160,
      minWidth: 140,
      align: 'left',
    },
    {
      title: '邮箱',
      dataIndex: 'email',
      key: 'email',
      width: 160,
      minWidth: 140,
      ellipsis: true,
      align: 'left',
    },
    {
      title: '手机号',
      dataIndex: 'mobile',
      key: 'mobile',
      width: 160,
      minWidth: 140,
      ellipsis: true,
      align: 'left',
    },
    {
      title: '角色',
      dataIndex: 'role',
      key: 'role',
      width: 160,
      minWidth: 140,
      ellipsis: true,
      align: 'left',
    },
    {
      title: '创建时间',
      dataIndex: 'date_joined',
      key: 'date_joined',
      width: 160,
      minWidth: 140,
      align: 'left',
    },
    {
      title: '操作',
      key: 'action',
      fixed: 'right',
      width: 160,
      minWidth: 140,
      align: 'center',
    },
  ],
})

const statusFilterOptions = [
  { label: '启用', value: '1' },
  { label: '停用', value: '0' },
]

const roleScopeFilterOptions = [
  { label: '默认角色', value: 'builtin' },
  { label: '自定义角色', value: 'custom' },
  { label: '未分配角色', value: 'none' },
]

// 当前选中的用户
const currentUser = ref(null)
const rowSelection = computed(() => ({
  selectedRowKeys: selectedRowKeys.value,
  onChange: (keys) => {
    selectedRowKeys.value = keys
  },
  getCheckboxProps: (record) => ({
    disabled: isCurrentUser(record),
  }),
}))

// form表单
const defaultUserForm = {
  username: '',
  password: '',
  email: '',
  mobile: '',
  primary_role_id: undefined,
  addon_role_ids: [],
  role_ids: [],
  permission_ids: [],
  direct_permission_ids: [],
  is_active: true,
}
const formState = ref({ ...defaultUserForm })

// 搜索
const handleSearch = (value) => {
  uiData.filters.search = value
  pagination.current = 1
  fetchData()
}

const handleFilterChange = () => {
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

// 获取角色和权限
const getRolesAndPermissions = async () => {
  const [rolesRes, permsRes] = await Promise.all([
    getRolesApi({ is_page: false }).catch(() => {}),
    getPermissionsApi({ is_page: false }).catch(() => {}),
  ])
  uiData.roles = rolesRes?.data || []
  uiData.permissions = permsRes?.data || []
}

// 获取列表数据
const fetchData = async () => {
  uiState.loading = true
  const params = {
    page_size: pagination.pageSize,
    page: pagination.current,
    is_page: true,
    search: uiData.filters.search || undefined,
    is_active_filter: uiData.filters.is_active_filter || undefined,
    role_scope: uiData.filters.role_scope || undefined,
  }
  const res = await getUsersApi(params).catch(() => {})
  if (res) {
    pagination.total = res.total
    uiData.tableData = res.data
  }
  uiState.loading = false
}

// 新增
const handleAdd = async () => {
  uiState.isEditMode = false
  formState.value = { ...defaultUserForm }
  await getRolesAndPermissions()
  uiState.userModalOpen = true
}

// 查看详情
const handleDetail = (record) => {
  currentUser.value = { ...record }
  uiState.detailModalOpen = true
}

// 编辑
const handleEdit = async (record) => {
  uiState.isEditMode = true
  const [, accessRes] = await Promise.all([
    getRolesAndPermissions(),
    getUserAccessApi(record.uid).catch(() => {}),
  ])
  const effectiveIsActive = !record.is_deleted && !!record.is_active
  formState.value = {
    ...defaultUserForm,
    ...record,
    is_active: effectiveIsActive,
    primary_role_id: accessRes?.data?.primary_role_id,
    addon_role_ids: accessRes?.data?.addon_role_ids || [],
    role_ids: accessRes?.data?.role_ids || [],
    direct_permission_ids: accessRes?.data?.direct_permission_ids || [],
    permission_ids: accessRes?.data?.direct_permission_ids || [],
  }
  uiState.userModalOpen = true
}

// 提交
const onSubmit = useThrottleFn(async (data) => {
  const payload = { ...data }
  const res = uiState.isEditMode
    ? await updateUsersApi(payload).catch(() => {})
    : await addUsersApi(payload).catch(() => {})
  if (res) {
    message.success('操作成功')
    uiState.userModalOpen = false
    fetchData()
    // 【权限变更即时生效】改的是「当前登录账号自己」时，动态路由/菜单是按上一次
    // bootstrap 的权限快照生成的，不重新生成就会出现「权限已经加上了、菜单却还在/没了」。
    // 这里对自身账号做一次整页刷新，让 permissionStore 按新权限重新生成路由。
    if (uiState.isEditMode && String(payload?.username || '') === String(userStore?.username || '')) {
      window.setTimeout(() => {
        try {
          window.location.reload()
        } catch (_) {}
      }, 800)
    }
  }
})

// 删除
const isCurrentUser = (record) => String(record?.uid || '') === String(userStore?.uid || '')

const handleDelete = useThrottleFn(async (record) => {
  try {
    const res = await deleteUsersApi(record.uid)
    if (res) {
      message.info('操作成功')
      selectedRowKeys.value = selectedRowKeys.value.filter((uid) => uid !== record.uid)
      uiData.tableData = uiData.tableData.filter((row) => row.uid !== record.uid)
      pagination.total = Math.max(0, pagination.total - 1)
      if (uiData.tableData.length === 0 && pagination.current > 1) {
        pagination.current = Math.max(1, pagination.current - 1)
      }
      fetchData()
    }
  } catch (err) {
    message.error(err?.message || err?.msg || '删除失败')
  }
})

const handleBatchDelete = useThrottleFn(async () => {
  if (!selectedRowKeys.value.length) return
  try {
    const res = await batchDeleteUsersApi([...selectedRowKeys.value])
    if (res) {
      message.success(`批量删除完成${res?.data?.count ? `（${res.data.count} 个用户）` : ''}`)
      selectedRowKeys.value = []
      await fetchData()
    }
  } catch (err) {
    message.error(err?.message || err?.msg || '批量删除失败')
  }
}, 800)

// 初始化
onMounted(() => {
  fetchData()
})

// ===================== 满高布局：滚动只发生在表格表体内部 =====================
// 通过公共布局的 setLayoutContentNoScroll 关掉内容区滚动，离开页面时恢复。
const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)

// 表格表体高度：动态测量列表区域可用高度（区域高 − 表头 − 底部分页），
// 让表格随窗口自适应铺满；纵向滚动只发生在表体内部，表头与分页固定可见。
const pageRef = ref(null)
const scrollY = ref(360)
const tableBodyHeight = computed(() => scrollY.value)

const updateScrollY = () => {
  const root = pageRef.value
  if (!root) return
  const section = root.querySelector('.gi-table-section')
  if (!section) return
  const sectionHeight = section.clientHeight || 0
  if (!sectionHeight) return
  const headHeight = section.querySelector('.ant-table-thead')?.offsetHeight || 39
  const paginationHeight = section.querySelector('.ant-pagination')?.offsetHeight || 0
  // ant-pagination 自带上下 margin（16px×2），额外再留 2px 余量，宁可略短不可溢出
  const available = sectionHeight - headHeight - paginationHeight - 34
  scrollY.value = available > 200 ? Math.round(available) : 200
}

let userResizeObserver = null
onMounted(() => {
  setLayoutContentNoScroll?.(true)
  nextTick(() => {
    updateScrollY()
    window.addEventListener('resize', updateScrollY)
    if (typeof ResizeObserver !== 'undefined' && pageRef.value) {
      userResizeObserver = new ResizeObserver(() => updateScrollY())
      userResizeObserver.observe(pageRef.value)
    }
  })
})
onBeforeUnmount(() => {
  setLayoutContentNoScroll?.(false)
  window.removeEventListener('resize', updateScrollY)
  userResizeObserver?.disconnect()
  userResizeObserver = null
})

// 首次数据加载后分页条才渲染完成（高度从 0 变为实际值），需要重新测量表体高度
watch(
  () => uiData.tableData.length,
  () => nextTick(updateScrollY),
  { flush: 'post' },
)
</script>

<style scoped>
/* ===================== 满高布局 flex 链 =====================
   外层 layout-content 已关滚动（setLayoutContentNoScroll），
   高度自上而下传导：页面壳 → 卡片 → 卡片体 → 列表区域，
   最终只有表格表体（.ant-table-body）可滚动，表头与分页固定可见。 */
.user-page {
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

/* 工具栏（标题 + 搜索/筛选/按钮）固定在顶部，不随表格滚动 */
.user-page > .gi-page-toolbar {
  flex: 0 0 auto;
}

/* 卡片与卡片体逐层向下传导高度 */
.user-page :deep(.ant-card) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.user-page :deep(.ant-card-body) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* 列表区域：占满卡片体剩余高度，内部滚动交给表体 */
.user-page :deep(.gi-table-section) {
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
}

/* 工具栏布局 / 搜索框宽度由公共规范 .gi-toolbar-single-line + .gi-toolbar-actions 提供
   （components/patterns/pagePatterns.scss），本页不再重复定义。 */
.gi-delete-disabled {
  color: rgba(0, 0, 0, 0.25);
  cursor: not-allowed;
  pointer-events: none;
}

.action-cell {
  display: flex;
  align-items: center;
  justify-content: center;
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

/* 统一表格单元格内边距与表头样式，保证行列清爽 */
.user-page :deep(.ant-table-cell) {
  padding: 12px 16px;
}

.user-page :deep(.ant-table-thead .ant-table-cell) {
  font-weight: 600;
  color: rgba(0, 0, 0, 0.85);
  background-color: #fafafa;
}

.user-page :deep(.ant-table-tbody > tr.ant-table-row:hover > td) {
  background-color: #f5f5f5;
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
  /* 模板里搜索框/下拉写的是内联 style="width:320px"，内联优先级高于公共规范，
     这里用 !important 收回，让控件可以收缩 */
  .gi-toolbar-actions > .gi-toolbar-search,
  .gi-toolbar-actions > .ant-select {
    width: 100% !important;
    min-width: 0 !important;
    flex: 1 1 120px !important;
  }

  .user-page {
    height: auto;
    min-height: 0;
    overflow: visible;
  }

  .more-action-btn {
    min-height: 32px;
    padding-inline: 8px;
  }
}
</style>
