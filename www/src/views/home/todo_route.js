import lazyImport from '@/utils/lazyImport'

const route = {
  name: 'home.todos',
  path: '/home/todos',
  component: lazyImport(() => import('./TodoDetail.vue')),
  meta: { title: '待办详情', keepAlive: true, hidden: true },
}

export default route

