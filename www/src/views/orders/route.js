import lazyImport from '@/utils/lazyImport'

const route = {
  name: 'orders.legacy',
  path: '/orders',
  redirect: '/sql-workbench/orders',
  component: lazyImport(() => import('./index.vue')),
  meta: { hidden: true, activeMenu: '/sql-workbench/orders' },
  children: [
    {
      path: '/orders/create',
      redirect: '/sql-workbench/create',
      meta: { hidden: true, activeMenu: '/sql-workbench/create' },
    },
    {
      path: '/orders',
      redirect: '/sql-workbench/orders',
      meta: { hidden: true, activeMenu: '/sql-workbench/orders' },
    },
    {
      path: '/orders/:order_id',
      redirect: (to) => `/sql-workbench/orders/${to.params.order_id}`,
      meta: { hidden: true, activeMenu: '/sql-workbench/orders' },
    },
    {
      path: '/orders/tasks/:order_id',
      redirect: (to) => `/sql-workbench/orders/tasks/${to.params.order_id}`,
      meta: { hidden: true, activeMenu: '/sql-workbench/orders' },
    },
    {
      path: '/orders/tasks/exports/:filename',
      redirect: (to) => `/sql-workbench/orders/tasks/exports/${to.params.filename}`,
      meta: { hidden: true, activeMenu: '/sql-workbench/orders' },
    },
  ],
}

export default route
