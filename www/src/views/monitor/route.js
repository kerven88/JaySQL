import lazyImport from '@/utils/lazyImport'

const route = {
  name: 'monitor',
  path: '/monitor',
  redirect: '/monitor/dashboard',
  component: lazyImport(() => import('./index.vue')),
  meta: {
    title: '监控告警',
    icon: 'AlertOutlined',
    keepAlive: true,
    permission: ['monitor.view', 'monitor.manage'],
  },
  children: [
    {
      name: 'monitor.dashboard',
      path: '/monitor/dashboard',
      component: lazyImport(() => import('./dashboard/DashboardView.vue')),
      meta: { title: '监控大屏', icon: 'DashboardOutlined', keepAlive: true, permission: 'monitor.view' },
    },
    {
      name: 'monitor.detail',
      path: '/monitor/detail',
      component: lazyImport(() => import('./detail/DetailView.vue')),
      meta: { title: '监控明细', icon: 'FundProjectionScreenOutlined', keepAlive: true, permission: 'monitor.view' },
    },
    {
      name: 'monitor.alerts',
      path: '/monitor/alerts',
      component: lazyImport(() => import('./alerts/AlertCenterView.vue')),
      meta: { title: '告警中心', icon: 'AlertOutlined', keepAlive: true, permission: 'monitor.view' },
    },
    {
      name: 'monitor.config',
      path: '/monitor/config',
      component: lazyImport(() => import('./config/ConfigView.vue')),
      meta: { title: '监控配置', icon: 'SettingOutlined', keepAlive: true, permission: 'monitor.manage' },
    },
    {
      name: 'monitor.alert-config',
      path: '/monitor/alert-config',
      component: lazyImport(() => import('./alert/AlertConfigView.vue')),
      meta: { title: '告警配置', icon: 'BellOutlined', keepAlive: true, permission: 'monitor.manage' },
    },
  ],
}

export default route
