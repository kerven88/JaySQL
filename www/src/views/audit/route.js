import lazyImport from '@/utils/lazyImport'

const route = {
  name: 'audit',
  path: '/audit',
  redirect: '/audit/logs',
  component: lazyImport(() => import('./index.vue')),
  meta: { title: '安全审计', icon: 'SafetyCertificateOutlined', keepAlive: true, permission: ['audit.log.view', 'audit.policy.view'] },
  children: [
    {
      name: 'audit.logs',
      path: '/audit/logs',
      component: lazyImport(() => import('./logs/AuditLogIndex.vue')),
      meta: { title: '操作日志', icon: 'FileSearchOutlined', keepAlive: true, permission: 'audit.log.view' },
    },
    {
      name: 'audit.policy',
      path: '/audit/policy',
      component: lazyImport(() => import('./policy/AuditPolicyConfig.vue')),
      meta: { title: '策略配置', icon: 'SettingOutlined', keepAlive: true, permission: 'audit.policy.view' },
    },
  ],
}

export default route
