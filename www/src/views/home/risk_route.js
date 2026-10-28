import lazyImport from '@/utils/lazyImport'

const route = {
  name: 'home.risks',
  path: '/home/risks',
  component: lazyImport(() => import('./RiskDetail.vue')),
  meta: { title: '风险详情', keepAlive: true, hidden: true },
}

export default route

