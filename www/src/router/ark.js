import Layout from '@/components/layout/Layout.vue'
import { markRaw } from 'vue'
import lazyImport from '@/utils/lazyImport'

import ACCOUNT from '@/views/account/route'
import ADMIN from '@/views/admin/route'
import AI from '@/views/ai/route'
import AUDIT from '@/views/audit/route'
import BACKUP from '@/views/backup/route'
import DAS from '@/views/das/route'
import DATASOURCE from '@/views/datasource/route'
import DOCS from '@/views/docs/route'
import HOME from '@/views/home/route'
import HOME_RISKS from '@/views/home/risk_route'
import HOME_TODOS from '@/views/home/todo_route'
import MONITOR from '@/views/monitor/route'
import ORDERS from '@/views/orders/route'
import PERFORMANCE from '@/views/performance/route'

export const staticRoutes = [
  {
    path: '/login',
    name: 'Login',
    component: lazyImport(() => import('@/views/login/Login.vue')),
    meta: { title: '用户登录', hidden: true },
  },
  {
    path: '/403',
    name: '403',
    component: lazyImport(() => import('@/views/error/403.vue')),
    meta: { title: '403', hidden: true },
  },
  {
    path: '/backup/storage_targets',
    name: 'backup.storage_targets',
    component: markRaw(Layout),
    redirect: '/backup/storage_targets',
    children: [
      {
        path: '',
        component: lazyImport(() => import('@/views/backup/storage/StorageTargets.vue')),
        meta: { title: '存储目标', icon: 'InboxOutlined', keepAlive: true },
      },
    ],
  },
  {
    // 主机终端：新窗口打开的独立全屏页，刻意不套 Layout。
    //   1) 终端要占满视口，左侧导航与顶栏都是纯占地方；
    //   2) 独立窗口里再挂一套菜单，会让人误以为还能点其它页面（点了也是把终端页换成别的页面）。
    // 登录态与 meta.permission 仍由 permission.js 统一裁决，不因为不走 Layout 而放松。
    path: '/datasource/host-terminal/:instance_id',
    name: 'datasource.host_terminal',
    component: lazyImport(() => import('@/views/datasource/host-terminal/HostTerminal.vue')),
    meta: { title: '主机终端', hidden: true, permission: 'system.instance.manage' },
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'NotFound',
    component: lazyImport(() => import('@/views/error/403.vue')),
    meta: { title: 'Not Found', hidden: true },
  },
]

export const asyncRoutes = [
  {
    path: '/',
    name: 'Root',
    component: markRaw(Layout),
    redirect: '/home',
    children: [
      HOME,
      HOME_TODOS,
      HOME_RISKS,
      AI,
      DAS,
      DATASOURCE,
      ACCOUNT,
      ORDERS,
      // 导航顺序：数据源管理 → 监控告警 → 性能分析 → 备份恢复 → 安全审计（监控提到性能前、备份提到审计前）
      MONITOR,
      PERFORMANCE,
      BACKUP,
      AUDIT,
      ADMIN,
      DOCS,
    ],
  },
]
