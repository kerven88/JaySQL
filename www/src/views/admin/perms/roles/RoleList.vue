<template>
  <div class="gi-page-shell role-page">
    <!-- 页面标题在内容卡片之外；操作组件与标题同一行，任何宽度都不换行 -->
    <div class="gi-page-toolbar gi-toolbar-single-line">
      <div class="gi-page-title">角色管理</div>
      <div class="gi-toolbar-actions">
        <a-button type="primary" :disabled="!canManageRole" @click="handleCreate">
          <template #icon><PlusOutlined /></template>
          新增角色
        </a-button>
      </div>
    </div>

    <a-card>
      <PageTableSection>
        <a-table
          size="small"
          :columns="uiData.tableColumns"
          :row-key="(record) => record.key"
          :data-source="uiData.tableData"
          :pagination="pagination"
          :loading="uiState.loading"
          @change="handleTableChange"
          :scroll="{ x: 960 }"
          :table-layout="'auto'"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'code_type'">
              {{ record.is_builtin ? '默认' : '自定义' }}
            </template>
            <template v-else-if="column.key === 'permission_count'">
              {{ (record.permission_ids || []).length }}
            </template>
            <template v-else-if="column.key === 'actions'">
              <a-space size="small">
                <template v-if="!canManageRole || isReadonlyBuiltin(record)">
                  <a type="link" class="role-action-link" @click.prevent="handleView(record)">查看</a>
                </template>
                <template v-else>
                  <a type="link" class="role-action-link" @click.prevent="handleEdit(record)">编辑</a>
                </template>
              </a-space>
            </template>
          </template>
        </a-table>
      </PageTableSection>
    </a-card>
  </div>

  <RoleFormModal
    v-model:open="modalState.open"
    v-model:modelValue="modalState.formData"
    :title="modalState.title"
    :can-manage="canManageRole"
    :permissions="modalState.permissions"
    :read-only="modalState.readOnly"
    :is-builtin="modalState.isBuiltin"
    @submit="handleModalSubmit"
    @delete="handleModalDelete"
    @clone-builtin="handleCloneBuiltin"
  />
</template>

<script setup>
import {
  createRolesApi,
  deleteRolesApi,
  getPermissionsApi,
  getRolesApi,
  updateRolesApi,
} from '@/api/admin'
import PageTableSection from '@/components/patterns/PageTableSection.vue'
import { PlusOutlined } from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { computed, reactive, onMounted } from 'vue'
import RoleFormModal from './RoleFormModal.vue'
import { useUserStore } from '@/store/user'

const userStore = useUserStore()
const canManageRole = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.role.manage'),
)

// 『不可编辑的内置角色』——只有系统管理员/全局只读这两类是 System Principal 硬编码入口，
// 代码级有 bypass/升级 migration 依赖 code 等隐含假设，不可编辑不可删除。
// 普通成员 member / 只读 viewer 允许用户在 UI 上直接增删权限 + 改名称/描述，
// 并保存后真正生效（后端 builtin_roles.go 同步策略也会改为只在首次创建时写默认值，不覆盖后续编辑）。
const READONLY_BUILTIN_ROLE_CODES = ['admin', 'global_readonly', 'super_admin']

const isReadonlyBuiltin = (record) => {
  if (!record?.is_builtin) return false
  return READONLY_BUILTIN_ROLE_CODES.includes(String(record?.code || ''))
}

const uiState = reactive({
  loading: false,
})

const uiData = reactive({
  tableData: [],
  tableColumns: [
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
      minWidth: 290,
      align: 'left',
    },
    {
      title: '类型',
      dataIndex: 'code_type',
      key: 'code_type',
      minWidth: 220,
      align: 'left',
    },
    {
      title: '权限数',
      dataIndex: 'permission_count',
      key: 'permission_count',
      minWidth: 220,
      align: 'left',
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      minWidth: 185,
      align: 'left',
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      key: 'updated_at',
      minWidth: 185,
      align: 'left',
    },
    {
      title: '操作',
      key: 'actions',
      width: 130,
      fixed: 'right',
      align: 'center',
    },
  ],
})

const modalState = reactive({
  open: false,
  title: '新增角色',
  readOnly: false,
  isBuiltin: false,
  permissions: [],
  formData: {
    id: null,
    name: '',
    code: '',
    description: '',
    permission_ids: [],
  },
})

const pagination = reactive({
  current: 1,
  pageSize: 10,
  total: 0,
  pageSizeOptions: ['10', '20', '50', '100'],
  showSizeChanger: true,
})

const handleTableChange = (pager) => {
  pagination.current = pager.current
  pagination.pageSize = pager.pageSize
  fetchData()
}

const fetchData = async () => {
  uiState.loading = true
  const params = {
    page_size: pagination.pageSize,
    page: pagination.current,
    is_page: true,
  }
  const res = await getRolesApi(params).catch(() => {})
  if (res) {
    pagination.total = res.total
    uiData.tableData = res.data
  }
  uiState.loading = false
}

const fetchPermissions = async () => {
  const res = await getPermissionsApi({ is_page: false }).catch(() => {})
  if (res) {
    modalState.permissions = (res.data || []).map((p) => ({
      label: `${p.name} (${p.code})`,
      value: p.id,
    }))
  }
}

const handleCreate = () => {
  modalState.title = '新增角色'
  modalState.readOnly = false
  modalState.isBuiltin = false
  modalState.formData = {
    id: null,
    name: '',
    code: '',
    description: '',
    permission_ids: [],
  }
  modalState.open = true
}

const handleView = (record) => {
  modalState.title = '查看角色'
  modalState.readOnly = true
  modalState.isBuiltin = true
  modalState.formData = {
    id: record.id,
    name: record.name,
    code: record.code,
    description: record.description,
    permission_ids: [...(record.permission_ids || [])],
  }
  modalState.open = true
}

const handleEdit = (record) => {
  modalState.title = '编辑角色'
  modalState.readOnly = false
  modalState.isBuiltin = !!record.is_builtin
  modalState.formData = {
    id: record.id,
    name: record.name,
    code: record.code,
    description: record.description,
    permission_ids: [...(record.permission_ids || [])],
  }
  modalState.open = true
}

const handleModalSubmit = async (formData) => {
  try {
    let res
    if (formData.id) {
      res = await updateRolesApi(formData)
    } else {
      res = await createRolesApi(formData)
    }
    if (res) {
      message.success(formData.id ? '编辑成功' : '新增成功')
      modalState.open = false
      fetchData()
    }
  } catch (err) {
    message.error(err?.message || err?.msg || (formData.id ? '编辑失败' : '新增失败'))
  }
}

const handleModalDelete = async (formData) => {
  if (!formData?.id) return
  const res = await deleteRolesApi(formData.id).catch((err) => {
    message.error(err?.message || err?.msg || '删除失败：该角色可能存在绑定用户，请先解绑后再删除')
    return null
  })
  if (res) {
    message.success('删除成功')
    modalState.open = false
    fetchData()
  }
}

const handleCloneBuiltin = (clonedPayload) => {
  modalState.open = false
  modalState.title = '新增角色（基于内置角色）'
  modalState.readOnly = false
  modalState.isBuiltin = false
  modalState.formData = {
    id: null,
    name: clonedPayload?.name || '',
    code: clonedPayload?.code || '',
    description: clonedPayload?.description || '',
    permission_ids: Array.isArray(clonedPayload?.permission_ids)
      ? [...clonedPayload.permission_ids]
      : [],
  }
  setTimeout(() => {
    modalState.open = true
  }, 100)
}

onMounted(() => {
  fetchData()
  fetchPermissions()
})
</script>

<style scoped lang="less">
.role-action-link {
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

@media (max-width: 768px) {
  /* 操作链接是裸 <a>，点击区只有 20px 高 */
  .role-action-link {
    display: inline-flex;
    align-items: center;
    min-height: 32px;
    padding-inline: 6px;
  }
}
</style>
