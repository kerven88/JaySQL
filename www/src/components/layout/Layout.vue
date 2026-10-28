<template>
  <a-layout class="layout" :style="layoutVars" :class="{ 'is-workspace-fullscreen': uiState.workspaceFullscreen, 'is-page-immersive': pageImmersiveSignal }">
    <a-layout-header class="layout-header">
      <div class="header-left">
        <router-link class="brand" to="/home" :aria-label="brandNameText">
          <img class="brand-icon" :src="sidebarLogoSrc" :alt="brandNameText" @error="sidebarLogoBroken = true" />
          <span v-if="!uiState.isMobile" class="brand-text">{{ brandNameText }}</span>
        </router-link>
      </div>

      <div class="header-right">
        <a-dropdown placement="bottomRight">
          <span class="user-info">
            <a-avatar v-if="userStore.avatar" :src="userStore.avatar" />
            <a-avatar v-else src="/avatar.png" />
            <span v-if="!uiState.isMobile" class="nickname">{{ userStore.displayName }}</span>
          </span>
          <template #overlay>
            <a-menu>
              <a-menu-item @click="userCenter">
                <UserOutlined />
                个人中心
              </a-menu-item>
              <a-menu-item @click="uiState.openPasswordModal = true">
                <SafetyOutlined />
                修改密码
              </a-menu-item>
              <a-menu-item @click="Logout">
                <LogoutOutlined />
                注销登录
              </a-menu-item>
            </a-menu>
          </template>
        </a-dropdown>
      </div>
    </a-layout-header>

    <!-- 页面公告条：header 下方独立一行，水平居中占 50% 宽。
         无生效公告 / 全部被当前会话关闭时组件自身不渲染，不占位。
         height-change 用来把实际高度回写给内容区的 min-height 计算。 -->
    <PageAnnouncementBar @height-change="handleAnnouncementHeight" />

    <a-layout class="layout-body">
      <a-layout-sider
        class="layout-sider"
        :class="{ 'is-collapsed': uiState.collapsed }"
        :width="layoutConfig.sidebarExpandedWidth"
        :collapsed-width="uiState.isMobile ? 0 : layoutConfig.sidebarCollapsedWidth"
        breakpoint="lg"
        v-model:collapsed="uiState.collapsed"
        :trigger="null"
        collapsible
        @breakpoint="handleBreakpoint"
      >
        <div class="layout-menu-scroll">
          <a-menu
            class="layout-menu"
            theme="dark"
            mode="inline"
            :openKeys="uiData.openKeys"
            :selectedKeys="[route.path]"
            :items="menuItems"
            @select="select"
            @openChange="openChange"
          />
        </div>

        <button
          type="button"
          class="layout-sider-trigger"
          :aria-label="uiState.collapsed ? '展开侧边栏' : '收起侧边栏'"
          :title="uiState.collapsed ? '展开侧边栏' : '收起侧边栏'"
          @click="toggleCollapse"
        >
          <MenuUnfoldOutlined v-if="uiState.collapsed" />
          <MenuFoldOutlined v-else />
        </button>
      </a-layout-sider>

      <a-layout class="layout-main">
        <a-layout-content class="layout-content" :class="{ 'is-sql-query-page': isSqlQueryPage, 'is-order-create-page': isOrderCreatePage, 'is-docs-page': isDocsPage, 'is-monitor-detail-page': isMonitorDetailPage, 'is-monitor-dashboard-page': isMonitorDashboardPage, 'is-no-scroll': layoutContentNoScrollSignal }">
          <div class="layout-content-inner" :class="{ 'is-sql-query-page': isSqlQueryPage, 'is-order-create-page': isOrderCreatePage, 'is-docs-page': isDocsPage, 'is-monitor-detail-page': isMonitorDetailPage, 'is-monitor-dashboard-page': isMonitorDashboardPage, 'is-no-scroll': layoutContentNoScrollSignal }">
            <router-view v-slot="{ Component, route }">
              <component :is="Component" :key="renderedRouteKey" />
            </router-view>
          </div>
        </a-layout-content>
      </a-layout>
    </a-layout>
  </a-layout>

  <UserPassword :open="uiState.openPasswordModal" @update:open="uiState.openPasswordModal = $event" />
  <FeedbackWidget />
</template>

<script setup>
defineOptions({ name: 'GoLayout' })

import { layoutConfig } from '@/components/layout/layoutConfig'
import router from '@/router'
import { usePermissionStore } from '@/store/permission'
import { useBrandStore } from '@/store/brand'
import { useUserStore } from '@/store/user'
// 内置默认图：品牌设置里没配自定义 Logo 时用它，配置被删/文件丢失时也回落它
import logoOtter from '@/assets/jaysql-otter.png'
import UserPassword from '@/views/account/settings/UserPassword.vue'
import FeedbackWidget from '@/components/feedback/FeedbackWidget.vue'
import PageAnnouncementBar from '@/components/announcement/PageAnnouncementBar.vue'
import { clearAnnouncementClosed } from '@/components/announcement/announcementStorage'
import { forceMarkIdleWakeup } from '@/utils/request'
import {
  LogoutOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  SafetyOutlined,
  UserOutlined,
} from '@ant-design/icons-vue'
import { computed, defineAsyncComponent, h, onBeforeUnmount, onMounted, provide, reactive, ref, shallowRef, watch } from 'vue'
import { useRoute } from 'vue-router'

const LAST_ACTIVITY_KEY = '__gi_last_activity__'
const IDLE_MENU_THRESHOLD_MS = 2 * 60 * 1000

const isLongIdle = () => {
  try {
    const v = sessionStorage.getItem(LAST_ACTIVITY_KEY)
    if (!v) return false
    return Date.now() - (parseInt(v, 10) || 0) >= IDLE_MENU_THRESHOLD_MS
  } catch (_) {
    return false
  }
}

const markLastActivity = () => {
  try { sessionStorage.setItem(LAST_ACTIVITY_KEY, String(Date.now())) } catch (_) {}
}

const route = useRoute()
const userStore = useUserStore()
const permissionStore = usePermissionStore()
const brandStore = useBrandStore()

// 左上角 Logo：优先用品牌设置里上传的自定义图，没配就回落内置默认图。
// sidebarBroken 兜底自定义图 404（文件被误删）的情况——此时 img 触发 error，
// 直接换回内置图，页面不至于出现破图。
const sidebarLogoBroken = ref(false)
const sidebarLogoSrc = computed(() =>
  !sidebarLogoBroken.value && brandStore.sidebarLogoUrl ? brandStore.sidebarLogoUrl : logoOtter,
)

// 侧栏品牌文字：优先用品牌设置里的自定义名称，没配就回落内置「JaySQL」
const brandNameText = computed(() => brandStore.brandName || 'JaySQL')

const renderIcon = (iconName) => {
  if (!iconName) return undefined
  const iconComponent = defineAsyncComponent(() =>
    import('@ant-design/icons-vue').then((module) => module[iconName]),
  )
  return h(iconComponent)
}

const uiState = reactive({
  collapsed: false,
  isMobile: false,
  openPasswordModal: false,
  workspaceFullscreen: false,
})

const workspaceFullscreenSignal = ref(false)
const layoutContentNoScrollSignal = ref(false)

watch(workspaceFullscreenSignal, (val) => {
  uiState.workspaceFullscreen = Boolean(val)
})

provide('setWorkspaceFullscreen', (value) => {
  workspaceFullscreenSignal.value = Boolean(value)
})

provide('setLayoutContentNoScroll', (value) => {
  layoutContentNoScrollSignal.value = Boolean(value)
})

// 页面内全屏（沉浸模式）：顶部 App 栏、公告条与左侧导航一起收起，内容独占可视区。
// 与 is-workspace-fullscreen 的区别只在于「谁来触发、退出时归谁复位」。
const pageImmersiveSignal = ref(false)
provide('setPageImmersive', (value) => {
  pageImmersiveSignal.value = Boolean(value)
})

const uiData = reactive({
  openKeys: [],
})

// 页面公告条的实际高度：它在 flex 流里占位，内容区的 min-height 要减掉它，
// 否则有了公告条之后每个页面都会多出一条多余的纵向滚动。
const announcementBarHeight = ref(0)
const handleAnnouncementHeight = (px) => {
  const next = Number(px) || 0
  if (announcementBarHeight.value !== next) announcementBarHeight.value = next
}

const layoutVars = computed(() => ({
  '--gi-layout-header-height': `${layoutConfig.headerHeight}px`,
  '--gi-announcement-bar-height': `${announcementBarHeight.value}px`,
  '--gi-layout-sider-width': `${layoutConfig.sidebarExpandedWidth}px`,
  '--gi-layout-sider-width-collapsed': `${layoutConfig.sidebarCollapsedWidth}px`,
}))

const isSqlQueryPage = computed(() => ['/sql-workbench/query', '/das/query'].includes(route.path))
const isOrderCreatePage = computed(() => route.path === '/sql-workbench/create')
const isDocsPage = computed(() => route.path === '/docs')
// 监控明细：页面顶部（标题 + 搜索筛选行）留白减半，让首屏多露出一行卡片
const isMonitorDetailPage = computed(() => route.path === '/monitor/detail')
// 监控大屏：内容区上下留白各减半，首屏多露一行卡片 / 图表
const isMonitorDashboardPage = computed(() => route.path === '/monitor/dashboard')

const renderSeed = shallowRef(0)
const renderedRouteKey = computed(() => {
  const seed = isLongIdle() && renderSeed.value === 0 ? Date.now() : renderSeed.value
  return `${route.fullPath}__${seed}`
})
const bumpRenderSeed = () => {
  renderSeed.value = Date.now()
}
const select = (value) => {
  clearAllPendingRedirectTimers()
  const originPath = window.location.pathname
  const targetPath = String(value?.key || '')
  if (!targetPath) return
  const targetNorm = normalizePath(targetPath)
  if (targetNorm === normalizePath(originPath)) {
    if (uiState.isMobile) uiState.collapsed = true
    return
  }
  if (isLongIdle()) {
    try { forceMarkIdleWakeup() } catch (_) {}
    bumpRenderSeed()
  }
  markLastActivity()
  const handle = hardRedirectIfStuck(targetPath, originPath)
  router.push({ path: targetPath })
    .then(() => {
      if (handle) {
        try { window.clearTimeout(handle) } catch (_) {}
        pendingRedirectTimers.delete(handle)
      }
      const currentNorm = normalizePath(window.location.pathname)
      if (currentNorm !== targetNorm) {
        console.warn('[Layout] router.push成功但路径未变，疑似动态路由丢失，执行window.location硬跳自愈:', targetNorm)
        try { forceMarkIdleWakeup() } catch (_) {}
        window.setTimeout(() => {
          window.location.assign(targetNorm)
        }, 50)
      }
    })
    .catch(() => {
      if (handle) {
        try { window.clearTimeout(handle) } catch (_) {}
        pendingRedirectTimers.delete(handle)
      }
      const currentNorm = normalizePath(window.location.pathname)
      if (currentNorm !== targetNorm) {
        console.warn('[Layout] router.push失败，执行window.location硬跳自愈:', targetNorm)
        try { forceMarkIdleWakeup() } catch (_) {}
        window.setTimeout(() => {
          window.location.assign(targetNorm)
        }, 50)
      }
    })
  if (uiState.isMobile) {
    uiState.collapsed = true
  }
}

const transformRoutesToMenuItems = (routes, depth = 0) =>
  routes
    .filter((route) => !route.meta?.hidden)
    .map((route) => ({
      key: route.path,
      label: route.meta?.title,
      title: route.meta?.title,
      icon: depth === 0 && route.meta?.icon ? renderIcon(route.meta.icon) : undefined,
      children: route.children?.length ? transformRoutesToMenuItems(route.children, depth + 1) : undefined,
    }))

const menuItems = computed(() => transformRoutesToMenuItems(permissionStore.menuRoutes))

const initializeLayoutData = async () => {
  const storedOpenKeys = sessionStorage.getItem('openKeys')
  if (storedOpenKeys) {
    uiData.openKeys = JSON.parse(storedOpenKeys)
  }
}

const syncViewport = () => {
  const isMobile = window.innerWidth < layoutConfig.mobileBreakpoint
  uiState.isMobile = isMobile
  if (isMobile) {
    uiState.collapsed = true
  }
}

const handleBreakpoint = (broken) => {
  uiState.isMobile = broken
  if (broken) {
    uiState.collapsed = true
  }
}

const ROUTER_HARD_REDIRECT_MS = 3000
const pendingRedirectTimers = new Set()

const clearAllPendingRedirectTimers = () => {
  if (!pendingRedirectTimers.size) return
  for (const t of pendingRedirectTimers) {
    try { window.clearTimeout(t) } catch (_) {}
  }
  pendingRedirectTimers.clear()
}

const normalizePath = (p) => {
  if (!p) return ''
  let s = String(p)
  const qi = s.indexOf('?')
  if (qi >= 0) s = s.substring(0, qi)
  const hi = s.indexOf('#')
  if (hi >= 0) s = s.substring(0, hi)
  if (s.length > 1 && s.endsWith('/')) s = s.slice(0, -1)
  return s
}

const hardRedirectIfStuck = (expectedPath, originPath) => {
  const expectedNorm = normalizePath(expectedPath)
  const originNorm = normalizePath(originPath || window.location.pathname)
  if (!expectedNorm || expectedNorm === originNorm) return null
  const handle = window.setTimeout(() => {
    try {
      pendingRedirectTimers.delete(handle)
      const currentNorm = normalizePath(window.location.pathname)
      if (!currentNorm) return
      if (currentNorm === expectedNorm) return
      if (currentNorm !== originNorm) return
      console.warn('[Layout] 路由软跳转疑似卡死，执行window.location硬跳:', expectedNorm)
      window.location.assign(expectedNorm)
    } catch (_) {}
  }, ROUTER_HARD_REDIRECT_MS)
  pendingRedirectTimers.add(handle)
  return handle
}

const openChange = (openKeys) => {
  uiData.openKeys = openKeys || []
  sessionStorage.setItem('openKeys', JSON.stringify(uiData.openKeys))
}

const toggleCollapse = () => {
  uiState.collapsed = !uiState.collapsed
}

const userCenter = () => {
  clearAllPendingRedirectTimers()
  const originPath = window.location.pathname
  if (isLongIdle()) {
    try { forceMarkIdleWakeup() } catch (_) {}
    bumpRenderSeed()
  }
  markLastActivity()
  const routeSpec = { name: 'account.basic', query: { type: 'info' } }
  const targetPath = router.resolve(routeSpec).path
  const handle = hardRedirectIfStuck(targetPath, originPath)
  router.push(routeSpec)
    .then(() => {
      if (handle) {
        try { window.clearTimeout(handle) } catch (_) {}
        pendingRedirectTimers.delete(handle)
      }
    })
    .catch(() => {
      if (handle) {
        try { window.clearTimeout(handle) } catch (_) {}
        pendingRedirectTimers.delete(handle)
      }
    })
}

const Logout = () => {
  const permissionStore = usePermissionStore()
  userStore.clear()
  permissionStore.reset()
  // 登出走的是 location.reload()，不清 sessionStorage。不显式清一次，
  // 用户重登后仍看不到被自己关掉的公告（与「会话级关闭」的口径不符）。
  clearAnnouncementClosed()
  router.push({ name: 'Login' }).then(() => {
    window.location.reload()
  })
}

onMounted(async () => {
  // 品牌配置不 await：Logo 是装饰元素，不能因为它把布局初始化挡住。
  // 接口回来后 store 变更会自动把左上角 Logo 换成自定义图。
  brandStore.fetchBrand()
  await initializeLayoutData()
  syncViewport()
  markLastActivity()
  window.addEventListener('resize', syncViewport)
  window.addEventListener('focus', markLastActivity)
  window.addEventListener('mousedown', markLastActivity, { passive: true })
  window.addEventListener('keydown', markLastActivity, { passive: true })
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') {
      markLastActivity()
    }
  })
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', syncViewport)
  window.removeEventListener('focus', markLastActivity)
  window.removeEventListener('mousedown', markLastActivity)
  window.removeEventListener('keydown', markLastActivity)
  clearAllPendingRedirectTimers()
})
</script>

<style scoped>
.layout {
  height: 100vh;
  background: var(--gi-color-page-bg);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  overscroll-behavior: none;
}

.layout-body {
  flex: 1;
  min-height: 0;
}

.layout-sider {
  height: 100%;
  overflow: hidden;
  box-shadow: none;
  border-right: 1px solid rgba(5, 17, 25, 0.08);
}

.layout-sider :deep(.ant-layout-sider-children) {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.layout-menu-scroll {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
}

.layout-menu {
  padding-top: var(--gi-spacing-sm);
}

.layout-menu :deep(.ant-menu-submenu .ant-menu-submenu-content .ant-menu-item),
.layout-menu :deep(.ant-menu-submenu .ant-menu-submenu-content .ant-menu-submenu-title) {
  padding-inline-start: 30px !important;
}

.layout-sider-trigger {
  flex: 0 0 auto;
  align-self: flex-start;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  margin: 10px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.06);
  color: rgba(255, 255, 255, 0.86);
  cursor: pointer;
  transition: background 0.15s ease, border-color 0.15s ease;
}

.layout-sider-trigger:hover {
  background: rgba(255, 255, 255, 0.1);
  border-color: rgba(255, 255, 255, 0.22);
}

.layout-sider-trigger :deep(svg) {
  font-size: 16px;
}

.layout-main {
  min-width: 0;
  height: 100%;
  display: flex;
  flex-direction: column;
}

.layout-header {
  height: var(--gi-layout-header-height);
  line-height: var(--gi-layout-header-height);
  padding-inline: var(--gi-spacing-md);
  border-bottom: 1px solid rgba(5, 17, 25, 0.08);
  background: #ffffff;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--gi-spacing-sm);
  position: sticky;
  top: 0;
  z-index: 30;
}

.header-left,
.header-right {
  display: flex;
  align-items: center;
  /* 左右两块都不参与收缩：空间不足时各自内容被裁，不会互相挤压变形。 */
  flex: 0 0 auto;
}

.header-left {
  flex: 0 0 auto;
  gap: 10px;
}

.brand {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  padding: 6px 10px;
  border-radius: 12px;
  color: var(--gi-color-text-primary);
  transition: background var(--gi-duration-fast) ease;
}

.brand:hover {
  background: rgba(5, 17, 25, 0.04);
}

.brand-icon {
  width: 34px;
  height: 34px;
  object-fit: contain;
}

.brand-text {
  font-size: 22px;
  font-weight: 700;
  letter-spacing: 0.2px;
}

.layout-breadcrumb {
  min-width: 0;
}

.layout-breadcrumb :deep(.ant-breadcrumb) {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.trigger {
  font-size: 18px;
  cursor: pointer;
  color: var(--gi-color-text-secondary);
  transition: color var(--gi-duration-fast) ease;
}

.trigger:hover {
  color: var(--gi-color-primary);
}

.user-info {
  display: inline-flex;
  align-items: center;
  gap: var(--gi-spacing-sm);
  cursor: pointer;
}

.nickname {
  max-width: 180px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  color: var(--gi-color-text-primary);
}

.layout-content {
  flex: 1;
  min-height: 0;
  padding: var(--gi-spacing-lg);
  background: var(--gi-color-page-bg);
  overflow: auto;
}

.layout-content.is-sql-query-page {
  padding: 11px 24px 12px 24px;
  overflow: hidden;
}

.layout-content.is-order-create-page {
  padding: 22px 24px 24px 24px;
  overflow: hidden;
  overscroll-behavior: none;
}

.layout-content.is-no-scroll {
  overflow: hidden;
  overscroll-behavior: none;
}

/* 文档中心：顶部/底部留白减半（24px → 12px），左右保持 24px 不变。
   必须放在 .is-no-scroll 之后，同特异性下靠源码顺序覆盖 */
.layout-content.is-docs-page {
  padding: var(--gi-spacing-ssm) var(--gi-spacing-lg);
}

/* 监控明细：顶部与底部留白在原基础上各缩减 50%。
   顶部 24px → 12px（历史调整）→ 6px，底部 24px → 12px；左右保持 24px 不变，
   避免与其它二级页面的横向对齐发生偏差。
   放在 .is-docs-page 之后，保证与 is-no-scroll 叠加时 padding 不被覆盖回落。 */
.layout-content.is-monitor-detail-page {
  padding-top: calc(var(--gi-spacing-lg) / 4);
  padding-bottom: calc(var(--gi-spacing-lg) / 2);
}

/* 监控大屏：顶部与底部留白在原基础上各缩减 50%（24px → 12px），左右保持 24px 不变。
   本页通过 setLayoutContentNoScroll 把内容区切成 overflow:hidden，窄屏由
   .is-no-scroll 压到 6px，对应的减半值写在下方两个媒体查询里（3px）。 */
.layout-content.is-monitor-dashboard-page {
  padding-top: calc(var(--gi-spacing-lg) / 2);
  padding-bottom: calc(var(--gi-spacing-lg) / 2);
}

.layout-content-inner {
  min-height: calc(
    100vh - var(--gi-layout-header-height) - var(--gi-announcement-bar-height, 0px) - var(--gi-spacing-lg) - var(--gi-spacing-lg)
  );
}

/* 监控明细：撑高公式必须与自身的上下留白（顶 6px + 底 12px = 18px）对齐。
   收起态由 .is-no-scroll 的 height:100% 接管，这条只在展开态（关闭 is-no-scroll 后
   min-height 重新生效）起作用；不对齐会在页面底部凭空多出一段空白。 */
.layout-content-inner.is-monitor-detail-page {
  min-height: calc(
    100vh - var(--gi-layout-header-height) - var(--gi-announcement-bar-height, 0px) - var(--gi-spacing-lg) * 3 / 4
  );
}

/* 监控大屏：撑高公式与上下留白（顶 12px + 底 12px = 24px）对齐。
   本页常驻 .is-no-scroll（height:100%），这条只在离开/失活等 no-scroll 关闭的时刻兜底。 */
.layout-content-inner.is-monitor-dashboard-page {
  min-height: calc(
    100vh - var(--gi-layout-header-height) - var(--gi-announcement-bar-height, 0px) - var(--gi-spacing-lg)
  );
}

.layout-content-inner.is-sql-query-page {
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

.layout-content-inner.is-order-create-page {
  height: 100%;
  min-height: 0;
  overflow: hidden;
  overscroll-behavior: none;
}

.layout-content-inner.is-no-scroll {
  height: 100%;
  min-height: 0;
  overflow: hidden;
  overscroll-behavior: none;
}

@media (max-width: 1023px) {
  .layout-header {
    padding-inline: var(--gi-spacing-sm);
  }

  .layout-content {
    padding: var(--gi-spacing-md);
  }

  .layout-content.is-sql-query-page,
  .layout-content.is-order-create-page,
  .layout-content.is-no-scroll {
    padding: 6px;
    overflow: hidden;
  }

  /* 监控明细：窄屏下 .is-no-scroll 把四边压到 6px，顶/底再各减半为 3px；
     左右沿用 6px 不动。写在 .is-no-scroll 之后，同特异性靠源码顺序生效。 */
  .layout-content.is-monitor-detail-page {
    padding-top: 3px;
    padding-bottom: 3px;
  }

  /* 监控大屏：窄屏同样顶/底各减半（6px → 3px），左右沿用 6px */
  .layout-content.is-monitor-dashboard-page {
    padding-top: 3px;
    padding-bottom: 3px;
  }

  /* 文档中心：窄屏顶/底减半（16px → 8px） */
  .layout-content.is-docs-page {
    padding: var(--gi-spacing-sm) var(--gi-spacing-md);
  }

  .layout-content-inner {
    min-height: calc(
      100vh - var(--gi-layout-header-height) - var(--gi-announcement-bar-height, 0px) - var(--gi-spacing-md) - var(--gi-spacing-md)
    );
  }

  /* 监控明细：与窄屏上下留白（3px + 3px = 6px）对齐 */
  .layout-content-inner.is-monitor-detail-page {
    min-height: calc(
      100vh - var(--gi-layout-header-height) - var(--gi-announcement-bar-height, 0px) - var(--gi-spacing-lg) / 4
    );
  }

  /* 监控大屏：窄屏上下留白 3px + 3px = 6px */
  .layout-content-inner.is-monitor-dashboard-page {
    min-height: calc(
      100vh - var(--gi-layout-header-height) - var(--gi-announcement-bar-height, 0px) - var(--gi-spacing-lg) / 4
    );
  }

  .layout-content-inner.is-sql-query-page,
  .layout-content-inner.is-order-create-page,
  .layout-content-inner.is-no-scroll {
    height: 100%;
    min-height: 0;
    overflow: hidden;
  }
}

@media (max-width: 767px) {
  .layout-content {
    padding: var(--gi-spacing-ssm);
  }

  .layout-content.is-sql-query-page,
  .layout-content.is-order-create-page,
  .layout-content.is-no-scroll {
    padding: 6px;
    overflow: hidden;
  }

  /* 监控明细：手机端同样顶/底各减半（6px → 3px），左右沿用 6px */
  .layout-content.is-monitor-detail-page {
    padding-top: 3px;
    padding-bottom: 3px;
  }

  /* 监控大屏：手机端同样顶/底各减半（6px → 3px），左右沿用 6px */
  .layout-content.is-monitor-dashboard-page {
    padding-top: 3px;
    padding-bottom: 3px;
  }

  /* 文档中心：窄屏顶/底减半（12px → 6px） */
  .layout-content.is-docs-page {
    padding: 6px var(--gi-spacing-ssm);
  }

  .layout-content-inner {
    min-height: calc(
      100vh - var(--gi-layout-header-height) - var(--gi-announcement-bar-height, 0px) - var(--gi-spacing-ssm) - var(--gi-spacing-ssm)
    );
  }

  /* 监控明细：与手机端上下留白（3px + 3px = 6px）对齐 */
  .layout-content-inner.is-monitor-detail-page {
    min-height: calc(
      100vh - var(--gi-layout-header-height) - var(--gi-announcement-bar-height, 0px) - var(--gi-spacing-lg) / 4
    );
  }

  /* 监控大屏：手机端上下留白 3px + 3px = 6px */
  .layout-content-inner.is-monitor-dashboard-page {
    min-height: calc(
      100vh - var(--gi-layout-header-height) - var(--gi-announcement-bar-height, 0px) - var(--gi-spacing-lg) / 4
    );
  }

  .layout-content-inner.is-sql-query-page,
  .layout-content-inner.is-order-create-page,
  .layout-content-inner.is-no-scroll {
    height: 100%;
    min-height: 0;
    overflow: hidden;
  }
}

.layout.is-workspace-fullscreen {
  --gi-layout-header-height: 0px;
}

/* ---------------- 页面内全屏（沉浸模式，页面级「全屏」按钮用） ----------------
   顶部 App 栏、公告条、左侧导航全部退场，内容区独占整个浏览器可视区；
   浏览器自身的地址栏/标签栏不动（不调用原生 Fullscreen API）。
   高度全部由变量派生（--gi-layout-header-height / --gi-announcement-bar-height），
   所以把变量归零，内容区 min-height / 100% 链会自动跟着算，无需另写高度。
   注意与上面 is-workspace-fullscreen 的区别：那个是监控大屏专供，本类由其复用同一套收纳规则。 */
.layout.is-page-immersive {
  --gi-layout-header-height: 0px;
  --gi-announcement-bar-height: 0px;
}

.layout.is-page-immersive .layout-header {
  display: none !important;
  height: 0 !important;
  min-height: 0 !important;
  padding: 0 !important;
  border-bottom: none;
}

.layout.is-page-immersive .gi-page-announcement {
  display: none !important;
}

/* 侧栏收掉，把整屏宽度让给内容区（退出后自动恢复原宽度与折叠状态） */
.layout.is-page-immersive .layout-sider {
  width: 0 !important;
  min-width: 0 !important;
  max-width: 0 !important;
  flex: 0 0 0 !important;
  overflow: hidden !important;
  border-right: none;
}

.layout.is-page-immersive .layout-sider-trigger {
  display: none !important;
}

.layout.is-workspace-fullscreen .layout-header {
  display: none !important;
  height: 0 !important;
  min-height: 0 !important;
  padding: 0 !important;
  border-bottom: none;
}

/* 全屏工作台：公告条跟着 header 一起让位。
   注意组件仍会占位并回写高度，所以这里用 display:none 而不是靠父级变量——
   offsetHeight 归零后 height-change 也会回 0，内容区计算不会错位。 */
.layout.is-workspace-fullscreen .gi-page-announcement {
  display: none !important;
}

.layout.is-workspace-fullscreen .layout-sider {
  width: 0 !important;
  min-width: 0 !important;
  max-width: 0 !important;
  flex: 0 0 0 !important;
  overflow: hidden !important;
  border-right: none;
}

.layout.is-workspace-fullscreen .layout-sider-trigger {
  display: none !important;
}

.layout.is-workspace-fullscreen .layout-content {
  padding: 0 !important;
  overflow: hidden !important;
}

.layout.is-workspace-fullscreen .layout-content-inner {
  min-height: 100vh;
  height: 100vh;
}

.layout.is-workspace-fullscreen .layout-content-inner.is-sql-query-page,
.layout.is-workspace-fullscreen .layout-content-inner.is-order-create-page {
  height: 100vh;
  min-height: 100vh;
  overflow: hidden;
}
</style>

<style>
.ant-menu-submenu-popup.ant-menu-dark .ant-menu-item,
.ant-menu-submenu-popup.ant-menu-dark .ant-menu-submenu-title {
  padding-inline-start: 18px !important;
  padding-inline-end: 18px !important;
}
</style>
