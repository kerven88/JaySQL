import lazyImport from '@/utils/lazyImport'

// ---------------------------------------------------------------------------
// 2026-10-20 菜单结构调整：实例配置 / 环境管理 已整体迁入一级菜单
// 「数据源管理」（www/src/views/datasource/route.js），此处不再保留任何入口。
// 路由 name 未变（view.admin.instance / view.admin.environment），
// 按 name 的 router.push 不受影响。
//
// 2026-10-25 二次调整：数据授权（原 2026-10-20 随实例/环境一起迁到
// /datasource/grants）按产品口径回迁到「系统管理 → 权限管理」下（/admin/das），
// 与用户管理 / 角色管理同级。页面组件已同步从 views/admin/system/das/ 移到
// views/admin/perms/das/，路由 name 依旧不变（view.admin.das / view.admin.das.tables）。
// 父级（view.admin / view.admin.perms）的 permission 数组补了 system.das.view，
// 否则只有该权限的用户会被父菜单过滤掉，看不到入口。
// ---------------------------------------------------------------------------

const route = {
  name: 'view.admin',
  path: '/admin',
  component: lazyImport(() => import('./index.vue')),
  redirect: '/admin/users',
  meta: {
    title: '系统管理',
    icon: 'SettingOutlined',
    keepAlive: true,
    // 2026-10-25：补 system.das.view，保证只有数据授权权限的用户也能进「系统管理」
    permission: ['system.access', 'system.das.view'],
  },
  children: [
    {
      name: `view.admin.perms`,
      path: '/admin/perms',
      component: lazyImport(() => import('./perms/index.vue')),
      meta: {
        title: '权限管理',
        icon: 'UserOutlined',
        keepAlive: true,
        permission: ['system.user.view', 'system.role.view', 'system.das.view'],
      },
      children: [
        {
          name: `view.admin.users`,
          path: '/admin/users',
          component: lazyImport(() => import('./perms/users/UserList.vue')),
          meta: { title: '用户管理', icon: 'UserOutlined', keepAlive: true, permission: 'system.user.view' },
        },
        {
          name: `view.admin.roles`,
          path: '/admin/roles',
          component: lazyImport(() => import('./perms/roles/RoleList.vue')),
          meta: { title: '角色管理', icon: 'IdcardOutlined', keepAlive: true, permission: 'system.role.view' },
        },
        {
          // 2026-10-25 回迁：数据授权（2026-10-20 曾随实例/环境迁到 /datasource/grants）
          // 归入权限管理：授权主体是「用户 / 角色」，与用户管理、角色管理同属一套权限体系。
          name: `view.admin.das`,
          path: '/admin/das',
          component: lazyImport(() => import('./perms/das/DasSchemaList.vue')),
          meta: { title: '数据授权', icon: 'SafetyOutlined', keepAlive: true, permission: 'system.das.view' },
        },
        {
          // 授权明细（按主体 + 实例 + 库展开到表粒度），列表页按 name 跳转进来
          name: `view.admin.das.tables`,
          path: '/admin/das/:principal_type/:principal_id/:schema/:instance_id',
          component: lazyImport(() => import('./perms/das/DasTableList.vue')),
          meta: { title: '数据授权', icon: 'CheckCircleOutlined', keepAlive: true, hidden: true, permission: 'system.das.view' },
        },
      ],
    },
    {
      name: `view.admin.system`,
      path: '/admin/system',
      component: lazyImport(() => import('./system/index.vue')),
      meta: { title: '系统配置', icon: 'ToolOutlined', keepAlive: true, permission: 'system.access' },
      children: [
        {
          // 2026-10-21 SQL规范（含详情）已迁入一级菜单「数据源管理」
          // （/datasource/sql-standards），此处不再保留入口；路由 name 未变。
          name: `view.admin.ai`,
          path: '/admin/ai',
          component: lazyImport(() => import('./system/ai/index.vue')),
          meta: { title: 'AI 能力', icon: 'RobotOutlined', keepAlive: true, permission: 'system.ai.view' },
        },
        {
          name: `view.admin.feedback`,
          path: '/admin/feedback',
          component: lazyImport(() => import('./system/feedback/FeedbackList.vue')),
          meta: { title: '反馈管理', icon: 'CommentOutlined', keepAlive: true, permission: 'system.feedback.view' },
        },
        {
          name: `view.admin.announcement`,
          path: '/admin/announcements',
          component: lazyImport(() => import('./system/announcements/AnnouncementList.vue')),
          meta: { title: '公告管理', icon: 'NotificationOutlined', keepAlive: true, permission: 'system.announcement.view' },
        },
        {
          name: `view.admin.brand`,
          path: '/admin/brand',
          component: lazyImport(() => import('./system/brand/BrandSettings.vue')),
          meta: { title: '品牌设置', icon: 'PictureOutlined', keepAlive: true, permission: 'system.access' },
        },
        {
          name: `view.admin.hot_article`,
          path: '/admin/hot-articles',
          component: lazyImport(() => import('./system/hot-articles/HotArticleList.vue')),
          meta: { title: '热文管理', icon: 'FireOutlined', keepAlive: true, permission: 'system.hotarticle.view' },
        },
      ],
    },
  ],
}
export default route
