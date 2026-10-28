<template>
  <div class="gi-page-shell home-page-v3">
    <div class="gi-page-toolbar home-toolbar">
      <div class="toolbar-left">
          <div class="title">{{ homeTitleText }}</div>
          <div class="subtitle">DataBase • SQL • AI</div>
      </div>
      <div class="toolbar-right">
          <a-space :size="10" align="center">
          <a-select
            v-model:value="uiData.environment"
            class="env-select"
            placeholder="全部环境"
            :options="environmentOptions"
            allowClear
            @change="reload"
          />
          <a-button :loading="uiState.loading" @click="reload">刷新</a-button>
        </a-space>
      </div>
    </div>

    <a-card class="intro-card" :loading="uiState.loading" :bordered="false">
      <div class="intro-panel">
        <div class="intro-main">
          <h2>{{ heroHeadline }}</h2>
          <p>{{ heroSubtitle }}</p>
          <!-- 两个按钮同 type / 同尺寸：此前 AI 助手是描边按钮、SQL 工作台是主色按钮，
               并排放着像一主一从，实际两者都是主行动点 -->
          <div class="intro-actions">
            <a-button class="intro-btn" @click="go('/ai')">进入AI助手</a-button>
            <a-button class="intro-btn" @click="go('/sql-workbench/query')">进入SQL工作台</a-button>
          </div>
        </div>
        <div class="intro-stats">
          <div class="intro-stat-card is-clickable" @click="go('/home/todos', { focus: 'max' })">
            <div class="intro-stat-label">当前待办</div>
            <div class="intro-stat-value">{{ uiData.todoCount }}</div>
            <div class="intro-stat-desc">{{ todoStatMeta }}</div>
          </div>
          <div class="intro-stat-card is-risk is-clickable" @click="go('/home/risks', { focus: 'max' })">
            <div class="intro-stat-label">风险提醒</div>
            <div class="intro-stat-value">{{ uiData.riskCount }}</div>
            <div class="intro-stat-desc">{{ riskStatMeta }}</div>
          </div>
        </div>
      </div>
    </a-card>

    <div class="home-entry-row">
      <div class="entry-main">
        <MyResources :resources="dashboard.resources" :environment="uiData.environment" />

        <a-card title="快捷入口" class="home-panel-card entry-card" :bordered="false">
          <QuickEntries />
        </a-card>
      </div>

      <div class="entry-side">
        <CommonDocs />
        <WeeklyHotArticles />
      </div>
    </div>
  </div>
</template>

<script setup>
import CommonDocs from './components/CommonDocs.vue'
import MyResources from './components/MyResources.vue'
import QuickEntries from './components/QuickEntries.vue'
import WeeklyHotArticles from './components/WeeklyHotArticles.vue'
import { getHomeDashboardApi } from '@/api/home'
import { useBrandStore } from '@/store/brand'
import { useUserStore } from '@/store/user'
import { HOME_WORKBENCH_EVENT } from '@/utils/homeWorkbench'
import { message } from 'ant-design-vue'
import { computed, onBeforeUnmount, onMounted, reactive } from 'vue'
import { useRouter } from 'vue-router'

const router = useRouter()
const userStore = useUserStore()
const brandStore = useBrandStore()

// 首页大标题：优先用品牌设置里的自定义标题，没配就回落内置「JaySQL DevOps」。
// store 在 Layout 挂载时已经拉过一次（loaded 后不重复请求），这里兜底再调一次防直连。
brandStore.fetchBrand()
const homeTitleText = computed(() => brandStore.homeTitle || 'JaySQL DevOps')

const createDashboardDefault = () => ({
  environments: [],
  hero: {
    greeting: '你好',
    headline: '',
    subtitle: '',
  },
  summary: {
    my_pending_orders: 0,
    executing_orders: 0,
    sql_queries_today: 0,
    backup_runs_today: 0,
    failed_jobs: 0,
    risk_alerts: 0,
  },
  todo_items: [],
  risks: {
    failed_backups_24h: 0,
    failed_restores_24h: 0,
    offline_nodes: 0,
    enabled_tasks: 0,
  },
  risk_counts: {
    instance_conn: 0,
    manual_backup_fail: 0,
    restore_fail: 0,
    node_offline: 0,
    instance_no_backup: 0,
    daily_backup_miss: 0,
    order_fail: 0,
    total: 0,
  },
  todo_counts: {
    order_submit: 0,
    order_approve: 0,
    order_execute: 0,
    backup_enable: 0,
    total: 0,
  },
  // 「我的资源」三张卡片的数量，由后端 /home/dashboard 聚合返回
  resources: {
    instances: { normal: 0, abnormal: 0, total: 0 },
    backup: { success: 0, abnormal: 0, total: 0 },
    orders: { pending: 0, success: 0, failed: 0, total: 0 },
  },
})

const uiState = reactive({
  loading: false,
})

const uiData = reactive({
  environment: undefined,
  dashboard: createDashboardDefault(),
  todoCount: 0,
  riskCount: 0,
})

const dashboard = computed(() => uiData.dashboard)
const displayName = computed(() => userStore.displayName || '数据库同学')
const heroHeadline = computed(
  () => dashboard.value.hero.headline || `${dashboard.value.hero.greeting || '你好'}，${displayName.value}`,
)
const heroSubtitle = computed(
  () =>
    `现有 ${uiData.riskCount} 项风险待处理、${uiData.todoCount} 条待办事项需关注，请优先处置风险及待办任务，再前往功能区操作。`,
)

const todoStatMeta = computed(() => '点击查看待办详情')

const riskStatMeta = computed(() => '点击查看风险详情')

const environmentOptions = computed(() =>
  (dashboard.value.environments || []).map((item) => ({
    value: item.id,
    label: item.name,
  })),
)

const syncWorkbenchCountsFromDashboard = () => {
  const d = uiData.dashboard || {}
  const rc = d.risk_counts || {}
  const tc = d.todo_counts || {}
  uiData.riskCount = Math.max(0, Number(rc.total || 0))
  uiData.todoCount = Math.max(0, Number(tc.total || 0))
}

let workbenchCountTimer = null
let workbenchCountRefreshing = false

const refreshWorkbenchCounts = async () => {
  if (workbenchCountRefreshing) return
  workbenchCountRefreshing = true
  try {
    const params = {}
    if (uiData.environment) {
      params.environment = uiData.environment
    }
    const res = await getHomeDashboardApi(params, {
      skipGlobalMessageError: true,
      silentHttpError: true,
      silentBusinessError: true,
    }).catch(() => null)
    if (res && typeof res === 'object' && res.code === '0000' && res.data && typeof res.data === 'object') {
      uiData.dashboard = { ...createDashboardDefault(), ...res.data }
      syncWorkbenchCountsFromDashboard()
    }
  } finally {
    workbenchCountRefreshing = false
  }
}

const reload = async () => {
  uiState.loading = true
  try {
    const params = {}
    if (uiData.environment) {
      params.environment = uiData.environment
    }
    const res = await getHomeDashboardApi(params, {
      skipGlobalMessageError: true,
      silentHttpError: true,
    }).catch(() => null)
    if (res && typeof res === 'object' && res.code === '0000' && res.data && typeof res.data === 'object') {
      uiData.dashboard = { ...createDashboardDefault(), ...res.data }
    } else {
      uiData.dashboard = createDashboardDefault()
    }
    syncWorkbenchCountsFromDashboard()
  } catch {
    message.error('工作台加载失败，请稍后重试')
  } finally {
    uiState.loading = false
  }
}

onMounted(async () => {
  await reload()
  window.addEventListener(HOME_WORKBENCH_EVENT, refreshWorkbenchCounts)
  workbenchCountTimer = window.setInterval(refreshWorkbenchCounts, 30 * 1000)
})

onBeforeUnmount(() => {
  window.removeEventListener(HOME_WORKBENCH_EVENT, refreshWorkbenchCounts)
  if (workbenchCountTimer) {
    window.clearInterval(workbenchCountTimer)
    workbenchCountTimer = null
  }
})

const go = (path, query) => {
  router.push({ path, query })
}

</script>

<style>
@import './components/homeCards.css';
</style>

<style scoped>
/* 区块间距跟视口联动：首屏要放完「工具栏 + 概览 + 入口区」，窄屏必须收紧 */
.home-page-v3 {
  display: flex;
  flex-direction: column;
  gap: clamp(6px, 0.7vw, 12px);
}

.home-toolbar {
  align-items: flex-start;
  padding-top: 0;
  margin-top: -12px;
}

.home-toolbar .toolbar-right {
  padding-top: 0;
  margin-top: 0;
}

.home-toolbar .toolbar-left {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  margin-top: 6px;
}

.home-toolbar .title {
  font-size: 17px;
  font-weight: 700;
  color: #14313d;
}

.home-toolbar .subtitle {
  font-size: 11px;
  color: #6b8090;
}

.env-select {
  width: 168px;
}

.intro-card {
  overflow: hidden;
  border-radius: 22px;
  background:
    radial-gradient(circle at top left, rgba(58, 184, 155, 0.12), transparent 32%),
    radial-gradient(circle at right center, rgba(66, 122, 255, 0.1), transparent 28%),
    linear-gradient(180deg, #ffffff 0%, #f6fbfc 100%);
  border: 1px solid rgba(27, 73, 88, 0.08);
  box-shadow: 0 14px 32px rgba(18, 58, 72, 0.08);
}

.intro-card :deep(.ant-card-body) {
  padding: 0;
}

.intro-panel {
  display: grid;
  grid-template-columns: minmax(0, 1.6fr) minmax(260px, 0.9fr);
  gap: 14px;
  padding: 18px 20px;
}

.intro-main {
  min-width: 0;
}

.intro-kicker {
  display: inline-flex;
  padding: 5px 10px;
  border-radius: 999px;
  background: rgba(24, 165, 143, 0.08);
  border: 1px solid rgba(24, 165, 143, 0.14);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.08em;
  color: #147866;
}

.intro-main h2 {
  margin: 10px 0 6px;
  font-size: 28px;
  line-height: 1.22;
  color: #14313d;
}

.intro-main p {
  max-width: 720px;
  margin: 0;
  font-size: 14px;
  line-height: 1.7;
  color: #67808d;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.intro-actions {
  display: flex;
  gap: 12px;
  margin-top: 14px;
}

/* banner 按钮：与卡片图标同一套「低饱和浅底 + 同色系深字」（--gi-chip-brand-*）。
   之前用主色实心，在整页浅色卡片里是一块孤立的深色，抢走了概览数字的重心；
   换成浅底后按钮仍然存在感清晰，但不再压过内容。 */
.intro-actions :deep(.intro-btn) {
  height: 40px;
  padding: 0 18px;
  border-radius: 10px;
  background: var(--gi-chip-brand-bg);
  color: var(--gi-chip-brand-fg);
  border: 1px solid color-mix(in srgb, var(--gi-chip-brand-fg) 20%, transparent);
  font-weight: 600;
  box-shadow: none;
  transition: background-color var(--gi-duration-fast) ease,
    border-color var(--gi-duration-fast) ease;
}

.intro-actions :deep(.intro-btn:hover),
.intro-actions :deep(.intro-btn:focus) {
  background: color-mix(
    in srgb,
    var(--gi-chip-brand-bg) calc(100% - var(--gi-chip-hover-mix)),
    var(--gi-chip-brand-fg)
  );
  color: var(--gi-chip-brand-fg);
  border-color: color-mix(in srgb, var(--gi-chip-brand-fg) 34%, transparent);
}

.intro-actions :deep(.intro-btn:active) {
  background: color-mix(
    in srgb,
    var(--gi-chip-brand-bg) calc(100% - var(--gi-chip-active-mix)),
    var(--gi-chip-brand-fg)
  );
  color: var(--gi-chip-brand-fg);
}

.intro-stats {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  align-self: stretch;
}

.intro-stat-card {
  min-height: 106px;
  padding: 14px 14px 12px;
  border-radius: 18px;
  background: rgba(255, 255, 255, 0.84);
  border: 1px solid rgba(21, 62, 77, 0.08);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.55);
}

.intro-stat-card.is-clickable {
  cursor: pointer;
  transition: transform 0.18s ease, box-shadow 0.18s ease, border-color 0.18s ease;
}

.intro-stat-card.is-clickable:hover {
  transform: translateY(-2px);
  border-color: rgba(22, 127, 111, 0.16);
  box-shadow: 0 14px 26px rgba(17, 55, 69, 0.08);
}

.intro-stat-card.is-risk {
  background: linear-gradient(180deg, #fff8f0 0%, #fff2e4 100%);
  border-color: rgba(255, 156, 90, 0.18);
}

.intro-stat-label {
  font-size: 12px;
  color: #6b8090;
}

.intro-stat-value {
  margin-top: 8px;
  font-size: 30px;
  line-height: 1;
  font-weight: 700;
  color: #14313d;
}

.intro-stat-desc {
  margin-top: 6px;
  font-size: 13px;
  line-height: 1.6;
  color: #7b8f9c;
}

/* 左：我的资源 + 快捷入口（上下叠放）；右：常用文档 + 相关热文。
   列宽与间距都跟视口联动：1366 收到 280px，1920 放到 380px，
   两侧都用 minmax(0,…) 兜底，保证任何宽度下都不会挤出横向滚动条。 */
.home-entry-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) clamp(280px, 24vw, 380px);
  gap: clamp(8px, 0.9vw, 14px);
  align-items: stretch;
}

/* 左列两卡左右边缘天然对齐；资源卡按内容定高，快捷入口吸收剩余高度，
   于是左列总高 = 右列总高（由 grid 的 stretch 保证）。 */
.entry-main {
  display: flex;
  flex-direction: column;
  gap: clamp(8px, 0.9vw, 14px);
  min-width: 0;
  min-height: 0;
}

.entry-main > .resource-card {
  flex: 0 0 auto;
}

/* 快捷入口卡片被撑到与左列同高，多余纵向空间让网格整体居中，
   比硬切成半行、露出半截卡片好看 */
.entry-card {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.entry-card :deep(.ant-card-body) {
  flex: 1;
  display: flex;
  align-items: center;
}

.entry-card :deep(.quick-grid) {
  width: 100%;
}

.entry-side {
  display: flex;
  flex-direction: column;
  gap: clamp(8px, 0.9vw, 14px);
  min-width: 0;
}

/* 右侧两卡在左列高度内按内容比例分配：常用文档 6 条比相关热文 3 条长得多，
   若用 flex:1 1 0 等分，第 6 条会被卡片内滚动裁掉。
   flex-basis:auto 让剩余空间在各自内容高度之上再平分，两张列表都完整可见。 */
.entry-side > * {
  flex: 1 1 auto;
  min-height: 0;
}

@media (max-width: 1199px) {
  .intro-panel {
    grid-template-columns: 1fr;
  }

  .intro-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .home-entry-row {
    grid-template-columns: 1fr;
  }

  /* 单列时左列不再被右列拉伸，恢复自然高度 */
  .entry-card {
    flex: 0 0 auto;
  }

  .entry-card :deep(.ant-card-body) {
    display: block;
  }

  /* 单列时右列也按内容定高，避免 flex-basis:0 把两张列表压塌 */
  .entry-side > * {
    flex: 0 0 auto;
  }
}

@media (max-width: 767px) {
  .intro-panel {
    padding: 18px;
  }

  .intro-main h2 {
    font-size: 24px;
  }

  .intro-actions {
    flex-wrap: wrap;
  }

  .intro-stats {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 768px) {
  /* 描述文字整段 nowrap + ellipsis，移动端可读性差 */
  .intro-main p {
    white-space: normal;
    overflow: visible;
    text-overflow: clip;
    max-width: 100%;
    font-size: 13px;
    line-height: 1.6;
  }

  .env-select {
    width: 100%;
    max-width: 100%;
  }

  :deep(.ant-space) {
    flex-wrap: wrap;
    row-gap: 8px;
  }

  .intro-stat-value {
    font-size: 26px;
  }

  .intro-stat-card {
    min-height: 92px;
    padding: 12px;
  }
}
</style>
