<template>
  <div class="gi-page-shell">
    <!-- 页面标题在内容卡片之外；标题与右侧操作组件单行不换行（gi-page-toolbar 规范） -->
    <div class="gi-page-toolbar gi-toolbar-single-line">
      <div class="gi-page-title">数据源总览</div>
      <div class="gi-toolbar-actions">
        <a-select
          v-model:value="uiData.gapFilter"
          class="overview-select overview-select--gap"
          :options="GAP_OPTIONS"
        />
        <a-input-search
          v-model:value="uiData.keyword"
          class="overview-search"
          placeholder="搜索实例名称 / 地址"
          allowClear
        />
        <a-button @click="loadData"><ReloadOutlined />刷新</a-button>
      </div>
    </div>

    <a-card>
      <PageTableSection>
        <a-table
          size="small"
          :columns="columns"
          :row-key="(record) => record.instance_id"
          :data-source="filteredRows"
          :loading="uiState.loading"
          :pagination="false"
          :scroll="{ x: 'max-content' }"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'instance'">
              <!-- 名称/地址两行；复制按钮跟在名称后（logo 已移至「数据库类型」列） -->
              <div class="cell-stack">
                <div class="cell-name-row">
                  <a-tooltip :title="record.name" :mouse-enter-delay="0.1">
                    <span>{{ truncateText(record.name, 15) }}</span>
                  </a-tooltip>
                  <a-tooltip title="复制名称" :mouse-enter-delay="0.1">
                    <CopyOutlined class="copy-btn" @click.stop="copyName(record)" />
                  </a-tooltip>
                </div>
                <a-tooltip :title="`${record.hostname}:${record.port}`" :mouse-enter-delay="0.1">
                  <span class="cell-sub">{{ truncateText(`${record.hostname}:${record.port}`, 20) }}</span>
                </a-tooltip>
              </div>
            </template>
            <template v-else-if="column.key === 'dbtype'">
              <div class="cell-dbtype">
                <img class="db-logo" :src="dbTypeLogo(record.db_type)" :alt="record.db_type || 'db'" />
                <span>{{ formatDbType(record) }}</span>
              </div>
            </template>
            <template v-else-if="column.key === 'environment'">
              <span v-if="record.environment_name">{{ record.environment_name }}</span>
              <span v-else class="cell-muted">—</span>
            </template>
            <template v-else-if="column.key === 'probe'">
              <span>{{ formatStatusText(record.last_check_status) }}</span>
            </template>
            <template v-else-if="column.key === 'monitor'">
              <!-- 已接入 → 可点进该实例的监控明细；无 monitor.view 权限时退化为纯文本 -->
              <a v-if="record.monitor_registered && canViewMonitor" class="overview-link" @click="goMonitor(record)">
                已接入
              </a>
              <span v-else-if="record.monitor_registered">已接入</span>
              <span v-else title="未在监控配置中注册">未接入</span>
            </template>
            <template v-else-if="column.key === 'backup'">
              <!-- 已配置 → 可点进备份任务列表（按该实例主键过滤）；无 backup.task.view 权限时退化为纯文本 -->
              <a
                v-if="record.backup_task_count > 0 && canViewBackup"
                class="overview-link"
                @click="goBackup(record)"
              >
                已配置
              </a>
              <span v-else-if="record.backup_task_count > 0">已配置</span>
              <span v-else title="无启用的备份任务（软删不计入）">未配置</span>
            </template>
            <template v-else-if="column.key === 'standard'">
              <!-- 已绑定 → 可点进该规范的详情页；未绑定不可点；无 system.inspect.view 权限时退化为纯文本 -->
              <a v-if="record.std_bound && record.std_id && canViewStandard" class="overview-link" @click="goStandard(record)">
                {{ record.std_name || '已绑定' }}
              </a>
              <span v-else-if="record.std_bound">{{ record.std_name || '已绑定' }}</span>
              <span v-else title="未绑定 SQL 规范，工单预审走默认级别">未绑定</span>
            </template>
            <template v-else-if="column.key === 'action'">
              <a-space :size="12">
                <!-- 主机登录：三态由后端算好（host_login_supported / host_login_ready），
                     不支持的整条置灰并给出原因；支持但没配凭据的点开后引导去配置。
                     无 datasource.host.login 权限时入口直接不出现（与其余列的退化口径一致） -->
                <template v-if="canHostLogin && hasHostLoginMeta(record)">
                  <a-tooltip
                    v-if="!record.host_login_supported"
                    :title="record.host_login_block_note || '该实例不支持主机登录'"
                    :mouse-enter-delay="0.1"
                  >
                    <span class="overview-link is-disabled">主机登录</span>
                  </a-tooltip>
                  <a v-else class="overview-link" @click="openHostTerminal(record)">主机登录</a>
                </template>
                <a class="overview-link" @click="goSqlQuery(record)">
                  <LoadingOutlined v-if="runtime.sqlQueryChecking === record.instance_id" class="overview-loading" />
                  SQL查询
                </a>
                <a class="overview-link" @click="goInstance(record)">查看</a>
              </a-space>
            </template>
          </template>
        </a-table>
      </PageTableSection>
    </a-card>
  </div>
</template>

<script setup>
import { CopyOutlined, LoadingOutlined, ReloadOutlined } from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import { computed, createVNode, onMounted, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { getDatasourceOverviewApi } from '@/api/datasource'
import { GetInstancesApi } from '@/api/das'
import { dbTypeLogo } from '@/utils/dbTypeLogo'
import { useUserStore } from '@/store/user'
import PageTableSection from '@/components/patterns/PageTableSection.vue'

const router = useRouter()
const userStore = useUserStore()

const GAP_OPTIONS = [
  { label: '全部数据源', value: 'all' },
  { label: '仅看有缺口', value: 'gap' },
]

const uiState = reactive({ loading: false })
// sqlQueryChecking：正在做「SQL查询」跳转前权限校验的实例 ID（给按钮 loading 反馈）
const runtime = reactive({ sqlQueryChecking: '' })
const uiData = reactive({
  tableData: [],
  gapFilter: 'all',
  keyword: '',
})

// 列定义：环境 / 探活状态 / 监控 / 备份 / SQL 规范 五列带表头筛选；
// 环境、SQL 规范的候选值来自当前数据（动态生成）
const columns = computed(() => {
  const envFilters = [...new Set(uiData.tableData.map((row) => row.environment_name).filter(Boolean))]
    .map((name) => ({ text: name, value: name }))
  const stdFilters = [
    ...[...new Set(uiData.tableData.filter((row) => row.std_bound && row.std_name).map((row) => row.std_name))]
      .map((name) => ({ text: name, value: name })),
    { text: '未绑定', value: '__none__' },
  ]
  return [
    { title: '数据源', key: 'instance', width: 260 },
    {
      title: '环境',
      key: 'environment',
      width: 120,
      // 表头与表体统一靠左（antd 的 column.align 同时作用于 th 与 td；
      // 显式写 left 而不是依赖默认值，避免全局 th 样式变化后悄悄跑偏）
      align: 'left',
      filters: envFilters,
      onFilter: (value, record) => record.environment_name === value,
    },
    { title: '数据库类型', key: 'dbtype', width: 180 },
    {
      title: '探活状态',
      key: 'probe',
      width: 110,
      filters: [
        { text: '可用', value: 'success' },
        { text: '不可用', value: 'error' },
        { text: '待检测', value: 'unknown' },
      ],
      onFilter: (value, record) => normalizeStatus(record.last_check_status) === value,
    },
    {
      title: '监控',
      key: 'monitor',
      width: 110,
      filters: [
        { text: '已接入', value: 'yes' },
        { text: '未接入', value: 'no' },
      ],
      onFilter: (value, record) => (value === 'yes') === !!record.monitor_registered,
    },
    {
      title: '备份',
      key: 'backup',
      width: 110,
      filters: [
        { text: '已配置', value: 'yes' },
        { text: '未配置', value: 'no' },
      ],
      onFilter: (value, record) => (value === 'yes') === (record.backup_task_count > 0),
    },
    {
      title: 'SQL 规范',
      key: 'standard',
      width: 160,
      filters: stdFilters,
      onFilter: (value, record) => (value === '__none__' ? !record.std_bound : record.std_name === value),
    },
    // 三个链接：主机登录 / SQL查询 / 查看
    { title: '操作', key: 'action', width: 240 },
  ]
})

// 探活状态口径与「实例管理」页一致：success / error / 其余归 unknown（待检测）
const normalizeStatus = (value) => {
  const normalized = String(value || '').trim().toLowerCase()
  if (normalized === 'success') return 'success'
  if (normalized === 'error') return 'error'
  return 'unknown'
}
const formatStatusText = (value) => {
  const normalized = normalizeStatus(value)
  if (normalized === 'success') return '可用'
  if (normalized === 'error') return '不可用'
  return '待检测'
}

// 数据库类型 + 版本号（如 MySQL 8.0.43）；版本缺失或无法识别时只显示类型名
const formatDbType = (record) => {
  const type = String(record.db_type || '').trim()
  const version = String(record.db_version || '').trim()
  if (!version || /^(unknown|n\/a|null|none|\?)$/i.test(version)) return type
  return `${type} ${version}`
}

// 列宽有限，实例名称 / 地址超长按字符截断展示（悬浮 title 看全量）
const truncateText = (value, max) => {
  const chars = [...String(value || '')]
  if (chars.length <= max) return chars.join('')
  return `${chars.slice(0, max).join('')}...`
}

// 覆盖缺口 = 监控未接入 / 备份未配置 / 规范未绑定 任一（探活状态不是覆盖口径，只影响状态列）
const rowHasGap = (record) => !record.monitor_registered || record.backup_task_count <= 0 || !record.std_bound

const filteredRows = computed(() => {
  const kw = String(uiData.keyword || '').trim().toLowerCase()
  return uiData.tableData.filter((row) => {
    if (uiData.gapFilter === 'gap' && !rowHasGap(row)) return false
    if (!kw) return true
    return (
      String(row.name || '').toLowerCase().includes(kw) ||
      String(row.hostname || '').toLowerCase().includes(kw) ||
      `${row.hostname}:${row.port}`.includes(kw)
    )
  })
})

const goInstance = (record) => {
  router.push({ path: '/datasource/instance', query: { edit_instance_id: record.instance_id } })
}

// ---- 状态列跳转 ----
// 三列状态都可点，但目标页各自有权限点：总览页的准入权限（system.instance.view /
// monitor.view / backup.task.view / system.das.view）不覆盖 SQL 规范（system.inspect.view），
// 直接 push 会被路由守卫拦成空白页。因此按权限决定渲染链接还是纯文本，绝不给出死链。
const canViewMonitor = computed(() => userStore.hasPermission('monitor.view'))
const canViewBackup = computed(() => userStore.hasPermission('backup.task.view'))
const canViewStandard = computed(() => userStore.hasPermission('system.inspect.view'))
// 主机登录＝拿操作系统 shell，与「能改实例配置（含 SSH 凭据）」同一信任级别，
// 全局只读只有 system.instance.view，拿不到这个入口
const canHostLogin = computed(() => userStore.hasPermission('system.instance.manage'))
// 后端没下发判定字段（旧版本 / 接口异常）时直接不显示入口，
// 而不是显示成「不支持」——那会让人以为是实例的问题
const hasHostLoginMeta = (record) => typeof record.host_login_supported === 'boolean'

// 监控明细：DetailView 读 route.query.instance_id（业务号 instance_id，不是主键 id）
const goMonitor = (record) => {
  router.push({ path: '/monitor/detail', query: { instance_id: record.instance_id } })
}

// 备份任务：BackupTask 读 route.query.instance_id，且**按主键 source_instance_id 过滤**，
// 所以这里必须传 record.id（实例主键），传业务号会过滤不到任何任务。
const goBackup = (record) => {
  router.push({ path: '/backup/task', query: { instance_id: record.id } })
}

// SQL 规范详情：路由为 /datasource/sql-standards/:id，id 是规范主键（std_id）
const goStandard = (record) => {
  if (!record.std_id) return
  router.push({ path: `/datasource/sql-standards/${record.std_id}` })
}

// ---- 主机登录 ----
// 终端页在新窗口打开（工单式弹窗塞不下终端，且要能随手拖动/缩放/复制）。
// 上下文传递走 localStorage：新窗口与列表页同源，刷新终端页也不会丢；
// 键带上 instance_id 是为了同时开多个终端互不覆盖。
const HOST_TERM_CTX_KEY_PREFIX = 'goinsight#datasource#host-terminal#'

const openHostTerminal = (record) => {
  // 支持但凭据不全：不弹新窗口（新窗口会先白屏再报错），直接在当前页说清楚并给出去配置的入口
  if (!record.host_login_ready) {
    Modal.confirm({
      title: '暂时无法登录该主机',
      content: createVNode('div', {}, [
        createVNode('p', { style: 'margin: 0 0 4px;' }, record.host_login_block_note || '该实例尚未配置可用的主机 SSH 凭据。'),
        createVNode(
          'p',
          { style: 'margin: 0; color: rgba(0,0,0,0.45);' },
          '请到实例配置的「主机 SSH」区块填写用户名与密码/私钥后再试。',
        ),
      ]),
      okText: '去配置',
      cancelText: '关闭',
      centered: true,
      onOk: () => {
        router.push({ path: '/datasource/instance', query: { edit_instance_id: record.instance_id } })
      },
    })
    return
  }
  try {
    window.localStorage.setItem(
      `${HOST_TERM_CTX_KEY_PREFIX}${record.instance_id}`,
      JSON.stringify({ instance_id: record.instance_id, name: record.name || '' }),
    )
  } catch {
    // 存不下不影响开窗口，终端页会退化成只显示 user@host:port
  }
  // 🔴 window.open 必须在用户手势的同步栈里调用：先 await 预检再开会被浏览器弹窗拦截器拦掉。
  // 因此预检放到终端页自己做，这里只负责开窗口。
  const win = window.open(router.resolve({ path: `/datasource/host-terminal/${record.instance_id}` }).href, '_blank')
  if (!win) {
    Modal.warning({
      title: '新窗口被拦截',
      content: '浏览器拦截了弹出窗口，请允许本站弹出窗口后重试。',
      okText: '知道了',
      centered: true,
    })
  }
}

// 复制实例名称：优先 Clipboard API；99 等内网 HTTP 环境（非 localhost）下不可用，
// 降级 execCommand 兜底，保证两条路径都能出成功提示。
const copyName = async (record) => {
  const name = String(record?.name || '').trim()
  if (!name) return
  try {
    await navigator.clipboard.writeText(name)
    message.success('名称已复制')
  } catch {
    const input = document.createElement('textarea')
    input.value = name
    input.style.position = 'fixed'
    input.style.opacity = '0'
    document.body.appendChild(input)
    input.select()
    try {
      document.execCommand('copy')
      message.success('名称已复制')
    } catch {
      message.error('复制失败，请手动复制')
    }
    document.body.removeChild(input)
  }
}

// 跳转 SQL 查询页的实例上下文传递：SQL 查询页（keepAlive）挂载/激活时消费该 key。
// key 与 ConsoleRight.vue 的 PREFILL_INSTANCE_STORAGE_KEY 保持一致。
const SQL_PREFILL_STORAGE_KEY = 'goinsight#das#prefill-instance'

const showNoPermissionModal = (title, content) => {
  Modal.warning({
    title,
    content,
    okText: '知道了',
    centered: true,
  })
}

const goSqlQuery = async (record) => {
  // 1. 功能权限：SQL 查询页路由要求 sql.console.access，缺了跳过去也是 403
  if (!userStore.hasPermission('sql.console.access')) {
    showNoPermissionModal(
      '暂无 SQL 查询权限',
      '您尚未被授予「SQL查询」功能权限，请联系管理员在角色管理中开通（权限项：SQL查询）。',
    )
    return
  }
  // 2. 实例权限：/api/v1/das/instances 返回的就是当前用户可访问的实例集合，
  //    目标实例不在集合内说明未做数据授权
  runtime.sqlQueryChecking = record.instance_id
  let accessible = []
  try {
    const res = await GetInstancesApi().catch(() => null)
    accessible = res?.data || []
  } finally {
    runtime.sqlQueryChecking = ''
  }
  const hit = accessible.some((item) => String(item.instance_id) === String(record.instance_id))
  if (!hit) {
    Modal.warning({
      title: '暂无该实例的查询权限',
      content: createVNode('div', {}, [
        createVNode('p', { style: 'margin: 0 0 4px;' }, `实例「${record.name}」尚未授权给您，请先申请数据访问权限。`),
        createVNode(
          'a',
          {
            style: 'font-size: 13px;',
            onClick: () => {
              Modal.destroyAll()
              router.push('/datasource/access')
            },
          },
          '前往申请权限 →',
        ),
      ]),
      okText: '知道了',
      centered: true,
    })
    return
  }
  // 3. 双重校验通过：带入实例上下文并跳转
  try {
    window.localStorage.setItem(SQL_PREFILL_STORAGE_KEY, JSON.stringify({ instance_id: record.instance_id }))
  } catch {
    // localStorage 不可用时仍跳转，只是不预选实例
  }
  router.push('/sql-workbench/query')
}

const loadData = async () => {
  uiState.loading = true
  try {
    const res = await getDatasourceOverviewApi()
    // request 工具返回完整响应体 { code, data, ... }，兼容数组形态
    const list = Array.isArray(res) ? res : res?.data || []
    uiData.tableData = list
  } finally {
    uiState.loading = false
  }
}

onMounted(() => {
  loadData()
})
</script>

<style scoped lang="scss">
/* 列表与上方工具栏的间距减半（全局规范是 12px，本页收紧到 6px） */
.gi-page-shell {
  gap: 6px;
}

/* 表格距卡片容器上边减半：antd card body 默认 padding-top 24px，本页收紧到 12px。
   只动上边——表格是卡片里唯一的主体，上方留白最显空；下方/左右维持全局规范。 */
:deep(.ant-card-body) {
  padding-top: 12px;
}

/* 表头筛选器紧贴列名文字：antd 默认 space-between 把漏斗推到单元格最右侧，
   且 trigger 带 -16px 的右外边距，这里都收回 */
:deep(.ant-table-filter-column) {
  justify-content: flex-start;
}

:deep(.ant-table-filter-trigger) {
  margin-inline-end: 0;
}

.overview-select {
  min-width: 128px;
}

.overview-select--gap {
  min-width: 118px;
}

.overview-search {
  width: 210px;
}

.overview-link {
  font-size: 12.5px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  /* 操作列链接统一走系统主题色（tokens.scss --gi-color-primary） */
  color: var(--gi-color-primary, #0f766e);

  &:hover {
    color: var(--gi-color-primary-hover, #0d6b63);
  }
}

/* 置灰的不可用入口：保留位置与文案，让人知道「有这个功能，只是这台用不了」，
   同时用 tooltip 说明原因，避免用户以为是自己没权限 */
.overview-link.is-disabled {
  color: var(--gi-color-text-disabled, rgba(0, 0, 0, 0.25));
  cursor: not-allowed;

  &:hover {
    color: var(--gi-color-text-disabled, rgba(0, 0, 0, 0.25));
  }
}

.overview-loading {
  font-size: 12px;
}

/* 数据库类型列：20px 品牌 logo + 类型/版本文字 */
.cell-dbtype {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;

  span {
    white-space: nowrap;
  }
}

.db-logo {
  width: 20px;
  height: 20px;
  flex: none;
  object-fit: contain;
}

.cell-stack {
  min-width: 0;
}

.cell-name-row {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;

  span:first-child {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 200px;
  }
}

.copy-btn {
  font-size: 12px;
  flex: none;
  color: var(--gi-color-text-tertiary, rgba(79, 100, 112, 0.8));
  cursor: pointer;
  transition: color 0.2s;

  &:hover {
    color: var(--gi-color-primary, #0f766e);
  }
}

.cell-sub {
  display: block;
  font-size: 11.5px;
  font-weight: 400;
  color: var(--gi-color-text-tertiary, rgba(79, 100, 112, 0.8));
  white-space: nowrap;
}

.cell-muted {
  color: var(--gi-color-text-tertiary, rgba(79, 100, 112, 0.8));
}
</style>
