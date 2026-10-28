<template>
  <div class="das-detail-page">
    <!-- 顶部区域：压缩为单行（返回 + 授权对象标识 + 操作）。
         原有的对象概览标题块与 4 张统计卡（覆盖环境/覆盖实例/授权合计/最近变更）
         整体移除，把纵向空间全部让给下方明细表。 -->
    <div class="detail-topbar">
      <a-button type="link" class="back-link" @click="goBack">
        <template #icon><ArrowLeftOutlined /></template>
        返回列表
      </a-button>

      <div v-if="detailGroup" class="detail-topbar__identity">
        <span class="detail-topbar__name" :class="{ 'is-muted': !!detailGroup.principal_deleted_hint }">
          {{ detailGroup.principal_name }}
        </span>
        <span class="detail-topbar__type">{{ detailGroup.principal_type_label }}</span>
        <a-tag v-if="detailGroup.principal_deleted_hint" class="overview-invalid-tag" color="default">
          {{ detailGroup.principal_deleted_hint }}
        </a-tag>
      </div>

      <div class="detail-topbar__actions">
        <a-button size="small" @click="fetchData">
          <template #icon><ReloadOutlined /></template>刷新
        </a-button>
        <a-button size="small" type="primary" :disabled="!canManageDas" @click="handleAdd">
          <template #icon><PlusOutlined /></template>新增资源访问权限
        </a-button>
      </div>
    </div>

    <a-card class="detail-table-card" :class="{ 'detail-table-card--fullscreen': uiState.isDetailFullscreen }" :body-style="{ padding: 0 }">
      <div class="table-toolbar">
        <div class="table-toolbar__title">
          <span class="title-dot" />
          资源访问授权明细
        </div>
        <div class="table-toolbar__right">
          <a-input-search
            v-model:value="uiData.searchValue"
            placeholder="搜索资源 / 环境 / 实例 / 数据库类型"
            class="detail-search"
            allow-clear
            @search="handleSearch"
            @input="handleSearch($event?.target?.value || '')"
          />
          <a-tooltip :title="uiState.isDetailFullscreen ? '退出全屏 (Esc)' : '全屏'" placement="bottom">
            <a-button
              type="text"
              size="small"
              class="detail-fullscreen-button"
              :class="{ 'is-in-fullscreen': uiState.isDetailFullscreen }"
              @click="toggleDetailFullscreen"
            >
              <template #icon>
                <FullscreenExitOutlined v-if="uiState.isDetailFullscreen" />
                <FullscreenOutlined v-else />
              </template>
            </a-button>
          </a-tooltip>
        </div>
      </div>

      <a-alert
        v-if="!uiState.loading && !detailGroup"
        type="warning"
        show-icon
        style="margin: 0 16px;"
        message="未找到对应授权对象"
        description="该对象可能已被删除，或当前筛选条件下没有可展示的资源访问权限。"
      />

      <div v-else ref="tableWrapRef" class="table-wrap">
        <a-table
          size="middle"
          :columns="uiData.tableColumns"
          :data-source="uiData.tableData"
          :pagination="pagination"
          :loading="uiState.loading"
          :row-key="(record) => record.id"
          :scroll="tableScroll"
          @change="handleTableChange"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'schema'">
              <span v-if="record.resource_type === 'instance'" class="resource-instance-text">整个实例</span>
              <router-link
                v-else
                :to="{
                  name: 'view.admin.das.tables',
                  params: {
                    principal_type: record.principal_type,
                    principal_id: record.principal_id,
                    schema: record.schema,
                    instance_id: record.instance_id,
                  },
                  query: {
                    resource_type: record.resource_type,
                    // 传原始展示名（不带表名后缀），下钻页的「资源」列才能保持干净
                    display_name: record.display_name || record.schema,
                    db_type: record.db_type,
                    principal_name: record.principal_name,
                    earliest_created_at:
                      pinnedFirstGrantedAt ||
                        (detailGroup && detailGroup.earliestCreatedAt
                          ? detailGroup.earliestCreatedAt
                          : ''),
                  },
                }"
                class="resource-link"
              >
                {{ formatSchemaDisplay(record) }}
              </router-link>
            </template>

            <template v-else-if="column.key === 'scope_tag'">
              <span class="scope-plain">{{ resourceTagLabel(record.resource_type) }}</span>
            </template>

            <template v-else-if="column.key === 'grant_actions'">
              <span class="scope-plain">{{ dataGrantActionsLabel(record.actions) }}</span>
            </template>

            <template v-else-if="column.key === 'environment'">
              <span class="env-text">{{ record.environment || '未设置' }}</span>
            </template>

            <template v-else-if="column.key === 'instance_name'">
              <div class="instance-cell">
                <div
                  class="instance-cell__name"
                  :title="String(record.remark || '').trim() || (record.hostname ? `${record.hostname}:${record.port}` : '--')"
                >{{ instanceDisplayName(record) }}</div>
                <div
                  class="instance-cell__conn"
                  :title="`${record.hostname || ''}:${record.port || ''}`"
                >{{ instanceConnText(record) }}</div>
              </div>
            </template>

            <template v-else-if="column.key === 'db_type'">
              <span class="db-type-plain">{{ record.db_type || '--' }}</span>
            </template>

            <template v-else-if="column.key === 'updated_at'">
              <span class="updated-text">{{ record._updated_at || record.updated_at || '--' }}</span>
            </template>

            <template v-else-if="column.key === 'action'">
              <a-popconfirm v-if="canManageDas" title="确认删除吗？" ok-text="是" cancel-text="否" @confirm="handleDelete(record)">
                <a type="link" class="action-link">删除</a>
              </a-popconfirm>
            </template>
          </template>
        </a-table>
      </div>
    </a-card>

    <DasSchemaFormModal
      :users="uiData.users"
      :roles="uiData.roles"
      :environments="uiData.environments"
      :open="uiState.isModalOpen"
      :can-manage="canManageDas"
      :initial-principal-type="route.query.principal_type"
      :initial-principal-id="route.query.principal_id"
      principal-disabled
      title="新增资源权限"
      @update:open="uiState.isModalOpen = $event"
      @submit="onSubmit"
    />
  </div>
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
import { useThrottleFn } from '@vueuse/core'
import { message } from 'ant-design-vue'
import { ArrowLeftOutlined, FullscreenExitOutlined, FullscreenOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons-vue'
import { computed, inject, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getPureSchemaDisplayName, isPostgresSchemaDbType, truncateByRune, MAX_DATASOURCE_NAME_LENGTH } from '@/utils/schemaDisplay'
import { useUserStore } from '@/store/user'
import DasSchemaFormModal from './DasSchemaFormModal.vue'
import { buildGroupRecords, buildPrincipalKey } from './grouping'
import { dataGrantActionsLabel, dataGrantScopeLabel, normalizeDataGrantRows } from './dataGrantAdapter'

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()
const canManageDas = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.das.manage'),
)

const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)

const tableWrapRef = ref(null)
const tableScrollY = ref(360)
const tableScroll = computed(() => ({
  x: 'max-content',
  y: tableScrollY.value > 0 ? tableScrollY.value : undefined,
}))

// DOM 尚未就绪时的兜底高度
const TABLE_THEAD_FALLBACK = 41
const TABLE_PAGER_FALLBACK = 32
const TABLE_PAGER_MARGIN_FALLBACK = 24
const MIN_TABLE_BODY_H = 160

let _ro = null

/**
 * 量出「表体以外」会占掉高度的固定内容：容器上内边距 + 表头 + 分页（含上下外边距）。
 * 这些必须从可用高度里扣掉，否则算出的 y 会把表体撑出容器，
 * 分页会被上层 overflow:hidden 裁掉（表现为「分页看不见 / 整块在滚」）。
 */
const measureTableChrome = () => {
  const el = tableWrapRef.value
  if (!el) {
    return {
      topPadding: 8,
      theadH: TABLE_THEAD_FALLBACK,
      pagerH: TABLE_PAGER_FALLBACK + TABLE_PAGER_MARGIN_FALLBACK,
    }
  }
  const topPadding = parseFloat(window.getComputedStyle(el).paddingTop) || 0
  const thead = el.querySelector('.ant-table-thead')
  const pager = el.querySelector('.ant-pagination')
  const pagerStyle = pager ? window.getComputedStyle(pager) : null
  const pagerMargin = pagerStyle
    ? (parseFloat(pagerStyle.marginTop) || 0) + (parseFloat(pagerStyle.marginBottom) || 0)
    : TABLE_PAGER_MARGIN_FALLBACK
  return {
    topPadding,
    theadH: thead?.offsetHeight || TABLE_THEAD_FALLBACK,
    pagerH: (pager?.offsetHeight || TABLE_PAGER_FALLBACK) + pagerMargin,
  }
}

/**
 * 计算 a-table 的 scroll.y —— 也就是「只有表体滚动」的那个高度。
 *
 * 容器（.table-wrap）由 flex 拉伸决定高度，与表格内容无关，因此这里不会来回震荡。
 * 表头由 antd 的固定表头机制单独渲染在上方，分页是 .ant-table 的兄弟节点，
 * 两者都不在滚动区内 ⇒ 滚动表体时表头与分页保持不动。
 * 全屏态下容器是 fixed inset:0 的子节点，clientHeight 同样成立，无需分支处理。
 */
const calcTableScrollY = () => {
  const el = tableWrapRef.value
  if (!el) return
  const h = el.clientHeight
  if (h <= 0) return
  const { topPadding, theadH, pagerH } = measureTableChrome()
  // 多留 1px 亚像素余量，避免表体再挤出一条外层滚动条
  const chromeH = topPadding + theadH + pagerH + 1
  tableScrollY.value = Math.max(MIN_TABLE_BODY_H, Math.floor(h - chromeH))
}

// 表体高度重算时机与 UserList.vue 保持一致：等 DOM 打完补丁（分页条渲染完成）再量。
// ⚠️ 该 watch 必须在 uiData 声明之后注册（见文件末尾），否则 getter 立即求值会命中 TDZ。

// 🔴 关键：PIN 住第一次进入详情页时，该主体的「第一次授权时间」
//      即使后面用户把所有授权记录删光了，新建记录时仍然用这个值作为 preserve_first_granted_at，
//      防止「删除所有 → 新建」导致创建时间被刷新。
const pinnedFirstGrantedAt = ref('')

const pinPrincipalEarliestTime = () => {
  if (pinnedFirstGrantedAt.value) return
  const g = uiData.groupedRecords.find((item) => item.key === detailKey.value)
  const val = g && g.earliestCreatedAt ? String(g.earliestCreatedAt).trim() : ''
  if (val && !val.startsWith('0001-') && !val.startsWith('0000-')) {
    pinnedFirstGrantedAt.value = val
  }
}

const formatSchemaDisplay = (record) => {
  const base = (() => {
    if (record?.resource_type === 'schema' && isPostgresSchemaDbType(record?.db_type)) {
      return getPureSchemaDisplayName({
        dbType: record?.db_type,
        schema: record?.schema,
        displayName: record?.display_name,
        schemaName: record?.schema_name,
        databaseName: record?.database_name,
        parentPath: record?.parent_path,
      }) || record?.display_name || record?.schema || ''
    }
    return record?.display_name || record?.schema || ''
  })()
  // 表级授权（新表才有的维度）在这里拼上表名，否则同一张库下多条表级授权
  // 在明细里会显示成一模一样的行，无法分辨撤的是哪一张表
  if (record?.resource_type === 'table' && record?.table) {
    return `${base} / ${record.table}`
  }
  return base
}

const uiState = reactive({
  loading: false,
  isModalOpen: false,
  isDetailFullscreen: false,
})

const uiData = reactive({
  searchValue: '',
  users: [],
  roles: [],
  environments: [],
  rawRecords: [],
  groupedRecords: [],
  tableData: [],
  tableColumns: [
    {
      title: '资源',
      dataIndex: 'schema',
      key: 'schema',
      width: 220,
      align: 'left',
    },
    {
      title: '授权范围',
      dataIndex: 'resource_type',
      key: 'scope_tag',
      width: 110,
      align: 'left',
    },
    {
      // 新授权表带操作维度（select / export / dml），旧页面没有这一列，
      // 导致「撤销时不知道到底授权了什么」。这里补上，让明细可自证。
      title: '授权操作',
      dataIndex: 'actions',
      key: 'grant_actions',
      width: 180,
      align: 'left',
    },
    {
      title: '环境',
      dataIndex: 'environment',
      key: 'environment',
      width: 100,
      align: 'left',
    },
    {
      title: '数据源名称',
      dataIndex: 'instance_name',
      key: 'instance_name',
      width: 220,
      align: 'left',
    },
    {
      title: '数据库类型',
      dataIndex: 'db_type',
      key: 'db_type',
      width: 140,
      align: 'left',
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      key: 'updated_at',
      width: 170,
      align: 'left',
    },
    {
      title: '操作',
      dataIndex: 'action',
      key: 'action',
      fixed: 'right',
      width: 100,
      align: 'center',
    },
  ],
})

const resourceTagLabel = (type) => dataGrantScopeLabel(type)

// 数据源名称列：第一行名称按 15 字截断（沿用全站 MAX_DATASOURCE_NAME_LENGTH 标准），第二行 ip:port 按 20 字截断。
// 实例记录已不存在的悬空授权行（hostname 为空）显示 '--'，避免出现 ":0" 这类残缺拼接。
const INSTANCE_CONN_MAX_LENGTH = 20
const instanceDisplayName = (record) => {
  const name = String(record?.remark || '').trim() || (record?.hostname ? `${record.hostname}:${record.port}` : '')
  return name ? truncateByRune(name, MAX_DATASOURCE_NAME_LENGTH) : '--'
}
const instanceConnText = (record) =>
  record?.hostname ? truncateByRune(`${record.hostname}:${record.port}`, INSTANCE_CONN_MAX_LENGTH) : '--'

const pagination = reactive({
  current: 1,
  pageSize: 10,
  total: 0,
  pageSizeOptions: ['10', '20', '50'],
  showSizeChanger: true,
})

const detailKey = computed(() => buildPrincipalKey(route.query.principal_type, route.query.principal_id))

const detailGroup = computed(() => uiData.groupedRecords.find((item) => item.key === detailKey.value) || null)

const filterRecord = (item, keyword) => {
  if (!keyword) return true
  const lowerKeyword = keyword.trim().toLowerCase()
  if (!lowerKeyword) return true
  return [
    item.display_name,
    item.schema,
    item.table,
    item.resource_type,
    item.environment,
    item.instance_name,
    item.hostname,
    item.db_type,
    item.actions,
    dataGrantActionsLabel(item.actions),
  ].some((value) => String(value || '').toLowerCase().includes(lowerKeyword))
}

const refreshTableData = () => {
  const records = detailGroup.value?.records || []
  const filtered = records.filter((item) => filterRecord(item, uiData.searchValue))
  pagination.total = filtered.length
  const start = (pagination.current - 1) * pagination.pageSize
  uiData.tableData = filtered.slice(start, start + pagination.pageSize)
}

const handleSearch = (value) => {
  uiData.searchValue = value
  pagination.current = 1
  refreshTableData()
}

const handleTableChange = (pager) => {
  pagination.current = pager.current
  pagination.pageSize = pager.pageSize
  refreshTableData()
}

const fetchData = async () => {
  uiState.loading = true
  // 与列表页同源：读新授权表。旧表只有手工授权行，system / self / migrate 来源的
  // 授权在旧表里根本不存在，用它做明细会漏掉真正生效的那部分。
  const res = await getDasDataGrantsApi({
    is_page: false,
    status: 'active',
    principal_type: route.query.principal_type,
    principal_id: route.query.principal_id,
  }).catch(() => {})
  if (res) {
    uiData.rawRecords = normalizeDataGrantRows(res.data)
    uiData.groupedRecords = buildGroupRecords(uiData.rawRecords)
    refreshTableData()
  }
  uiState.loading = false
}

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

const handleAdd = async () => {
  await Promise.all([getPrincipals(), getEnvironments()])
  uiState.isModalOpen = true
}

const onSubmit = useThrottleFn(async (data) => {
  const payload = data && typeof data === 'object' ? { ...data } : {}
  // 新增仍走旧表接口（理由见 DasSchemaList.vue 的 onSubmit 注释）：
  // grant_items 的多规则语义由后端对账展开进新表，前端只负责把「第一次授权时间」带过去。
  // 编辑场景：优先使用 PIN 住的第一次授权时间（记录全被删了也能用），
  // 否则再退回到当前分组的 earliestCreatedAt
  const firstAt =
    pinnedFirstGrantedAt.value ||
    (detailGroup.value && detailGroup.value.earliestCreatedAt) ||
    ''
  if (firstAt) {
    payload.preserve_first_granted_at = firstAt
  }
  const res = await createDasSchemasGrantApi(payload).catch(() => {})
  if (res) {
    message.success('操作成功')
    uiState.isModalOpen = false
    fetchData()
  }
})

const handleDelete = useThrottleFn(async (record) => {
  if (!record?.grant_no) {
    message.error('该记录缺少授权编号，无法撤销')
    return
  }
  const res = await revokeDasDataGrantApi({
    grant_no: record.grant_no,
    reason: '数据访问管理页删除',
  }).catch(() => {})
  if (res) {
    message.success('删除成功')
    fetchData()
  }
})

const goBack = () => {
  router.push({
    name: 'view.admin.das',
    query: {
      ...(route.query.search ? { search: route.query.search } : {}),
      ...(route.query.page ? { page: route.query.page } : {}),
      ...(route.query.page_size ? { page_size: route.query.page_size } : {}),
    },
  })
}

const toggleDetailFullscreen = () => {
  uiState.isDetailFullscreen = !uiState.isDetailFullscreen
  nextTick(() => calcTableScrollY())
}

const handleDetailEscKey = (event) => {
  if (uiState.isDetailFullscreen && event.key === 'Escape') {
    event.preventDefault()
    uiState.isDetailFullscreen = false
    nextTick(() => calcTableScrollY())
  }
}

// ===================== watch 注册（必须放在全部声明之后）=====================
// setup 期 TDZ 陷阱：`watch(source, cb)` 即使不传 immediate，也会**同步执行一次**
// source getter 去收集依赖（旧值初始化）。因此任何引用 uiData / uiState / detailKey
// 的 watch 都不能写在它们的 `const` 声明之前，否则整个组件 setup 抛
// "Cannot access 'uiData' before initialization"，表现为整页白屏、URL 不变、无报错提示。
// 这类错误 SFC 编译与 go build 都发现不了，只有真浏览器能复现。

// 表体高度重算时机与 UserList.vue 保持一致：等 DOM 打完补丁（分页条渲染完成）再量。
watch(
  () => uiData.tableData.length,
  () => calcTableScrollY(),
  { flush: 'post' }
)

// 首次进入时 PIN 住该主体的「第一次授权时间」，后续增删都不再刷新它。
watch(
  () => uiData.groupedRecords,
  () => pinPrincipalEarliestTime(),
  { immediate: true, deep: false }
)

onMounted(() => {
  if (setLayoutContentNoScroll) setLayoutContentNoScroll(true)
  window.addEventListener('keydown', handleDetailEscKey)
  fetchData().then(() => {
    nextTick(() => {
      calcTableScrollY()
      if (tableWrapRef.value && typeof ResizeObserver !== 'undefined') {
        try {
          _ro = new ResizeObserver(() => calcTableScrollY())
          _ro.observe(tableWrapRef.value)
        } catch { /* ignore ResizeObserver 初始化失败 */ }
      }
    })
  })
})

onBeforeUnmount(() => {
  if (setLayoutContentNoScroll) setLayoutContentNoScroll(false)
  window.removeEventListener('keydown', handleDetailEscKey)
  if (_ro) {
    try { _ro.disconnect() } catch { /* ignore disconnect 失败 */ }
    _ro = null
  }
})
</script>

<style scoped>
/* ===================== 满高布局 flex 链 =====================
   外层 layout-content 已关滚动（setLayoutContentNoScroll），高度自上而下传导：
   页面壳 → 顶部单行条 → 表格卡片 → 卡片体 → .table-wrap，
   最终只有表格表体（.ant-table-body）可滚动，表头与分页固定可见。 */
.das-detail-page {
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-sizing: border-box;
  gap: 12px;
  padding: 4px 0;
}

/* 顶部区域：压缩为单行 —— 返回 / 授权对象标识 / 操作按钮。
   原有的对象概览标题块与 4 张统计卡已整体移除。 */
.detail-topbar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 44px;
  padding: 0 16px;
  background: #FFFFFF;
  border: 1px solid #F0F1F3;
  border-radius: 8px;
  box-sizing: border-box;
}

.detail-topbar__identity {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  overflow: hidden;
}

.detail-topbar__name {
  color: rgba(0, 0, 0, 0.88);
  font-size: 15px;
  font-weight: 600;
  line-height: 1.4;
  max-width: 320px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.detail-topbar__name.is-muted {
  color: rgba(0, 0, 0, 0.45);
  text-decoration: line-through;
  text-decoration-color: rgba(0, 0, 0, 0.35);
}

.detail-topbar__actions {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 8px;
}

.detail-table-card {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  border-radius: 8px;
  margin: 0;
}

.table-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 14px 24px 12px;
  border-bottom: 1px solid #F0F1F3;
}

.table-toolbar__title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 15px;
  font-weight: 600;
  color: #1F2937;
}

.title-dot {
  width: 3px;
  height: 14px;
  background: #0F766E;
  border-radius: 2px;
  display: inline-block;
}

.table-toolbar__right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.detail-table-card :deep(.ant-card-body) {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  padding: 0;
}

.detail-table-card :deep(.ant-card-body > .ant-alert) {
  margin: 16px 16px 0;
  flex-shrink: 0;
}

.table-wrap {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: 8px 16px 0 16px;
  box-sizing: border-box;
}

.detail-table-card :deep(.ant-spin-nested-loading) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.detail-table-card :deep(.ant-table-wrapper) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.detail-table-card :deep(.ant-spin-container) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.detail-table-card :deep(.ant-table-wrapper .ant-table) {
  flex: initial;
}

.detail-table-card :deep(.ant-table-thead > tr > th) {
  background: #FAFBFC;
  color: #475569;
  font-weight: 500;
  font-size: 13px;
  padding: 11px 12px;
}

.detail-table-card :deep(.ant-table-tbody > tr > td) {
  padding: 11px 12px;
}

.detail-table-card :deep(.ant-table-container) {
  overflow: visible;
}

/* 分页固定表体下方：它是 .ant-table 的兄弟节点，不在滚动区内，故滚动时不动。
   margin 用 !important 覆盖 antd 默认 16px，这里测量代码会按实际值扣高。 */
.detail-table-card :deep(.ant-pagination) {
  flex-shrink: 0;
  margin: 12px 0 !important;
}

/* 返回按钮：单行条内垂直居中，去掉 antd 按钮的默认高度/内边距 */
.back-link {
  flex: 0 0 auto;
  padding: 0;
  height: auto;
  line-height: 1.6;
  color: #0F766E;
}

.overview-invalid-tag {
  font-size: 12px;
  line-height: 20px;
  padding: 0 8px;
  color: rgba(0, 0, 0, 0.55);
  background: rgba(0, 0, 0, 0.06);
  border: 1px solid rgba(0, 0, 0, 0.1);
}

/* 授权对象类型：无框淡灰小字，紧跟名称 */
.detail-topbar__type {
  flex: 0 0 auto;
  color: rgba(0, 0, 0, 0.35);
  font-size: 12px;
  line-height: 1.4;
}

.detail-search {
  width: 320px;
}

.instance-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  line-height: 1.4;
  min-width: 0;
}

.instance-cell__name {
  color: #1F2937;
  font-weight: 500;
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.instance-cell__conn {
  color: #9AA5B1;
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.principal-cell__title {
  color: rgba(0, 0, 0, 0.88);
  font-weight: 500;
}

.principal-cell__sub {
  margin-top: 4px;
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
}

.action-link {
  padding-left: 0;
}

.resource-link {
  color: #0F766E;
  font-weight: 500;
}

.resource-instance-text {
  color: #1F2937;
  font-weight: 500;
}

.scope-plain,
.db-type-plain {
  color: #1F2937;
  font-size: 13px;
}

.env-text {
  color: #1F2937;
  font-size: 13px;
}

.updated-text {
  color: #475569;
  font-size: 13px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.detail-table-card--fullscreen {
  position: fixed !important;
  inset: 0 !important;
  z-index: 1050 !important;
  margin: 0 !important;
  border-radius: 0 !important;
  background: #FFFFFF;
  display: flex !important;
  flex-direction: column !important;
  min-height: 0 !important;
}

.detail-table-card--fullscreen .ant-card-body,
.detail-table-card--fullscreen :deep(.ant-card-body) {
  flex: 1 !important;
  min-height: 0 !important;
  height: 0 !important;
  overflow: hidden !important;
  display: flex !important;
  flex-direction: column !important;
  padding: 0 !important;
  box-sizing: border-box;
}

.detail-table-card--fullscreen .table-wrap {
  flex: 1 !important;
  min-height: 0 !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
  padding: 8px 24px 0 24px;
}

.detail-table-card--fullscreen :deep(.ant-spin-nested-loading),
.detail-table-card--fullscreen :deep(.ant-table-wrapper) {
  flex: 1 !important;
  min-height: 0 !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
}

.detail-table-card--fullscreen :deep(.ant-spin-container) {
  flex: 1 1 auto !important;
  min-height: 0 !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
}

.detail-table-card--fullscreen :deep(.ant-table-wrapper .ant-table) {
  flex: 1 1 auto;
  min-height: 0 !important;
}

.detail-table-card--fullscreen :deep(.ant-pagination) {
  flex-shrink: 0 !important;
  margin: 12px 24px !important;
}

.detail-fullscreen-button {
  color: rgba(0, 0, 0, 0.45);
}

.detail-fullscreen-button.is-in-fullscreen {
  color: rgba(0, 0, 0, 0.78);
}

@media (max-width: 768px) {
  .detail-topbar {
    flex-wrap: wrap;
    padding: 8px 12px;
    gap: 8px;
    min-height: 0;
  }

  .detail-topbar__identity {
    flex: 1 1 100%;
  }

  .detail-topbar__name {
    max-width: 100%;
  }

  .detail-topbar__actions {
    flex: 1 1 100%;
    justify-content: flex-end;
  }

  .table-toolbar {
    flex-wrap: wrap;
    padding: 12px 12px 10px;
    gap: 8px;
  }

  .table-toolbar__right {
    width: 100%;
  }

  .detail-search {
    width: 100% !important;
  }

  .table-wrap {
    padding: 8px 8px 0;
  }
}
</style>
