import lazyImport from '@/utils/lazyImport'

const route = {
  name: 'account',
  path: '/account',
  redirect: '/account/basic',
  component: lazyImport(() => import('./index.vue')),
  meta: { title: '个人中心', icon: 'UserOutlined', keepAlive: true, hidden: true },
  children: [
    {
      name: 'account.basic',
      path: '/account/basic',
      component: lazyImport(() => import('./settings/UserBasic.vue')),
      meta: { title: '基本设置', icon: 'SettingOutlined', keepAlive: true, hidden: true },
    },
  ],
}

export default route
