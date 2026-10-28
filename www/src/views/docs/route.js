import lazyImport from '@/utils/lazyImport'

/**
 * 文档中心
 *
 * 有意不设 meta.permission —— 与「首页」一致，对所有登录用户开放。
 * 若日后需要按角色收敛，改这里加 permission 即可，但必须同步后端播种该权限点，
 * 否则非超管会直接看不到菜单（utils/permission.js 的 hasPermission 只认权限编码）。
 */
const route = {
  name: 'docs',
  path: '/docs',
  component: lazyImport(() => import('./index.vue')),
  meta: { title: '文档中心', icon: 'ReadOutlined', keepAlive: true },
}

export default route
