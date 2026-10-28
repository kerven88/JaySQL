import lazyImport from '@/utils/lazyImport'

const route = {
  name: 'sql.workbench',
  path: '/sql-workbench',
  component: lazyImport(() => import('./index.vue')),
  redirect: '/sql-workbench/query',
  meta: {
    title: 'SQL工作台',
    icon: 'CodeOutlined',
    keepAlive: true,
    permission: ['sql.console.access', 'order.submit', 'order.view'],
  },
  children: [
    {
      name: 'das.query',
      path: '/sql-workbench/query',
      component: lazyImport(() => import('./console/ConsoleIndex.vue')),
      meta: { title: 'SQL查询', icon: 'CodeOutlined', keepAlive: true, permission: 'sql.console.access' },
    },
    {
      name: 'orders.create',
      path: '/sql-workbench/create',
      component: lazyImport(() => import('../orders/create/OrderCreate.vue')),
      meta: { title: '新建工单', icon: 'FormOutlined', keepAlive: true, permission: 'order.submit' },
    },
    {
      name: 'orders.list',
      path: '/sql-workbench/orders',
      component: lazyImport(() => import('../orders/list/OrderList.vue')),
      meta: { title: 'SQL工单', icon: 'UnorderedListOutlined', keepAlive: true, permission: 'order.view' },
    },
    {
      name: 'orders.detail',
      path: '/sql-workbench/orders/:order_id',
      component: lazyImport(() => import('../orders/detail/OrderDetail.vue')),
      meta: {
        title: '工单详情',
        keepAlive: true,
        hidden: true,
        permission: 'order.view',
        activeMenu: '/sql-workbench/orders',
      },
    },
    {
      name: 'orders.tasks',
      path: '/sql-workbench/orders/tasks/:order_id',
      component: lazyImport(() => import('../orders/tasks/TaskList.vue')),
      meta: {
        title: '工单任务',
        keepAlive: true,
        hidden: true,
        permission: 'order.view',
        activeMenu: '/sql-workbench/orders',
      },
    },
    {
      name: 'orders.exportfile.download',
      path: '/sql-workbench/orders/tasks/exports/:filename',
      component: lazyImport(() => import('../orders/tasks/ExportfileDownload.vue')),
      meta: { title: '下载导出文件', keepAlive: true, hidden: true },
    },
    {
      name: 'das.favorite',
      path: '/sql-workbench/favorite',
      component: lazyImport(() => import('./favorite/DasFavorite.vue')),
      meta: {
        title: 'SQL收藏库',
        icon: 'FolderOpenOutlined',
        keepAlive: true,
        hidden: true,
        permission: 'sql.favorite.view',
        activeMenu: '/sql-workbench/query',
      },
    },
    {
      name: 'das.history',
      path: '/sql-workbench/history',
      component: lazyImport(() => import('./history/DasHistory.vue')),
      meta: {
        title: 'SQL历史',
        icon: 'HistoryOutlined',
        keepAlive: true,
        hidden: true,
        permission: 'sql.history.view',
        activeMenu: '/sql-workbench/query',
      },
    },
    {
      path: '/das',
      redirect: '/sql-workbench/query',
      meta: { hidden: true, activeMenu: '/sql-workbench/query' },
    },
    {
      path: '/das/query',
      redirect: '/sql-workbench/query',
      meta: { hidden: true, activeMenu: '/sql-workbench/query' },
    },
    {
      path: '/das/favorite',
      redirect: '/sql-workbench/favorite',
      meta: { hidden: true, activeMenu: '/sql-workbench/query' },
    },
    {
      path: '/das/history',
      redirect: '/sql-workbench/history',
      meta: { hidden: true, activeMenu: '/sql-workbench/query' },
    },
  ],
}

export default route
