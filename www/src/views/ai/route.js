import lazyImport from '@/utils/lazyImport'

const route = {
  name: 'ai',
  path: '/ai',
  component: lazyImport(() => import('./index.vue')),
  meta: {
    title: 'AI 助手',
    icon: 'RobotOutlined',
    keepAlive: true,
    permission: 'ai.chat.use',
  },
}

export default route
