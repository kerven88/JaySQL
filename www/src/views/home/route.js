import lazyImport from '@/utils/lazyImport'

const route = {
  name: 'home',
  path: '/home',
  component: lazyImport(() => import('./index.vue')),
  meta: { title: '首页', icon: 'DashboardOutlined', keepAlive: true },
}

export default route
