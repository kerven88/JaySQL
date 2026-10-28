import lazyImport from '@/utils/lazyImport'

const route = {
  name: 'backup',
  path: '/backup',
  redirect: '/backup/task',
  component: lazyImport(() => import('./index.vue')),
  meta: {
    title: '备份恢复',
    icon: 'CloudServerOutlined',
    keepAlive: true,
    permission: ['backup.task.view', 'backup.restore.view', 'backup.set.view'],
  },
  children: [
    {
      name: 'backup.task',
      path: '/backup/task',
      component: lazyImport(() => import('./task/BackupTask.vue')),
      meta: { title: '数据备份', icon: 'UploadOutlined', keepAlive: true, permission: 'backup.task.view' },
    },
    {
      name: 'backup.taskCreate',
      path: '/backup/task/create',
      component: lazyImport(() => import('./task/BackupTaskCreate.vue')),
      meta: { title: '新建备份任务', keepAlive: false, hidden: true, permission: 'backup.task.manage' },
    },
    {
      name: 'backup.taskEdit',
      path: '/backup/task/:id/edit',
      component: lazyImport(() => import('./task/BackupTaskCreate.vue')),
      meta: { title: '编辑备份任务', keepAlive: false, hidden: true, permission: 'backup.task.manage' },
    },
    {
      name: 'backup.agentInstall',
      path: '/backup/agent-install/:id',
      component: lazyImport(() => import('./agent/AgentInstall.vue')),
      meta: { title: '修复/安装 Agent', keepAlive: false, hidden: true, permission: 'backup.task.manage' },
    },
    {
      // 仅移出侧边菜单：路由仍保留注册，保证【数据备份】页右上角入口跳转与直链访问行为不变。
      name: 'backup.storage_targets',
      path: '/backup/storage_targets',
      component: lazyImport(() => import('./storage/StorageTargets.vue')),
      meta: {
        title: '存储目标',
        icon: 'InboxOutlined',
        keepAlive: true,
        hidden: true,
        permission: ['backup.task.view', 'backup.set.view'],
      },
    },
    {
      name: 'backup.restore',
      path: '/backup/restore',
      component: lazyImport(() => import('./restore/BackupRestore.vue')),
      meta: { title: '数据恢复', icon: 'RedoOutlined', keepAlive: true, permission: 'backup.restore.view' },
    },
    {
      name: 'backup.restoreWizard',
      path: '/backup/restore/wizard',
      component: lazyImport(() => import('./restore/RestoreWizard.vue')),
      // 与【创建恢复】按钮同口径：按钮由 hasPermission('backup.restore.execute') 控制，
      // 此处必须用单字符串（hasPermission 的数组语义不适用于叶子路由，详见 store/user.js）
      meta: { title: '新建恢复', keepAlive: false, hidden: true, permission: 'backup.restore.execute' },
    },
    {
      // 执行监控页：阶段时间轴 + 对象级进度 + 日志 + 结果摘要，
      // 权限与列表页保持一致（备份恢复列表与详情同属查看口径）
      name: 'backup.restoreDetail',
      path: '/backup/restore/:id',
      component: lazyImport(() => import('./restore/RestoreDetail.vue')),
      meta: { title: '恢复任务详情', keepAlive: false, hidden: true, permission: 'backup.restore.view' },
    },
    {
      name: 'backup.sets',
      path: '/backup/sets',
      component: lazyImport(() => import('./sets/BackupSets.vue')),
      meta: { title: '备份集', icon: 'DatabaseOutlined', keepAlive: true, permission: 'backup.set.view' },
    },
  ],
}

export default route
