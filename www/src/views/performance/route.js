import lazyImport from '@/utils/lazyImport'

const route = {
  name: 'performance',
  path: '/performance',
  redirect: '/performance/overview',
  component: lazyImport(() => import('./index.vue')),
  meta: {
    title: '性能分析',
    icon: 'BarChartOutlined',
    keepAlive: true,
    permission: ['performance.view', 'performance.manage'],
  },
  children: [
    {
      name: 'performance.overview',
      path: '/performance/overview',
      component: lazyImport(() => import('./overview/PerformanceOverview.vue')),
      meta: { title: '性能概览', icon: 'LineChartOutlined', keepAlive: true, permission: 'performance.view' },
    },
    {
      name: 'performance.slow_sql',
      path: '/performance/slow-sql',
      component: lazyImport(() => import('./slow-sql/SlowSQLAnalysis.vue')),
      meta: { title: '慢 SQL', icon: 'SnippetsOutlined', keepAlive: true, permission: 'performance.view' },
    },
    {
      name: 'performance.params',
      path: '/performance/params',
      component: lazyImport(() => import('./params/ParamManager.vue')),
      meta: { title: '参数管理', icon: 'ControlOutlined', keepAlive: true, permission: 'performance.view' },
    },
    {
      name: 'performance.snapshot_report',
      path: '/performance/snapshot-report',
      component: lazyImport(() => import('../monitor/snapshot/SnapshotView.vue')),
      meta: { title: '负载快照', icon: 'DashboardOutlined', keepAlive: true, permission: 'performance.view' },
    },
    {
      name: 'performance.snapshot_report_detail',
      path: '/performance/snapshot-report/:id',
      component: lazyImport(() => import('../monitor/snapshot/SnapshotReportView.vue')),
      meta: { title: '快照报告', icon: 'DashboardOutlined', hidden: true, keepAlive: false, permission: 'performance.view' },
    },
  ],
}

export default route
