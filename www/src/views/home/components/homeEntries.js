// 图标只给「快捷入口」用；「我的资源」三张卡不带图标，靠标题区分
import {
  AlertOutlined,
  DashboardOutlined,
  FileAddOutlined,
  LineChartOutlined,
  SafetyCertificateOutlined,
  SafetyOutlined,
} from '@ant-design/icons-vue'

/**
 * 首页下半区内容的数据源。
 *
 * 约定：快捷入口 / 常用文档 / 相关热文三块全部是静态常量，不依赖任何接口 ——
 * 首页是登录后的第一屏，任何后端抖动都不该让它空着。后续若接入接口，只替换这里的导出即可。
 * 「我的资源」是例外：三个数量来自 /home/dashboard 的 resources 字段（真实统计值）。
 */

/**
 * 快捷入口：3 列 × 2 行。
 *
 * chip = 图标底色令牌（色值见 tokens.scss 的 --gi-chip-*）。
 * 当前六个入口统一用 brand（与「新建工单」一致）：这一排是并列的同类入口，
 * 逐个换色反而会让人以为颜色在传达某种分类信息。
 * 若将来要按语义区分，改这里的 chip 即可 —— 取值 brand/data/risk/sec/flow，
 * 但要遵守两条规则：① 同色不相邻；② data 与 sec 禁止相邻
 * （这两个色相在低饱和浅色下 Lab 色差只有 3.6，并排几乎分不出来）。
 */
export const quickActions = [
  { key: 'order-create', title: '新建工单', chip: 'brand', icon: FileAddOutlined, path: '/sql-workbench/create', permission: 'order.submit' },
  { key: 'dashboard', title: '监控大屏', chip: 'brand', icon: DashboardOutlined, path: '/monitor/dashboard', permission: 'monitor.view' },
  { key: 'alerts', title: '告警中心', chip: 'brand', icon: AlertOutlined, path: '/monitor/alerts', permission: 'monitor.view' },
  { key: 'das', title: '数据授权', chip: 'brand', icon: SafetyOutlined, path: '/admin/das', permission: 'system.das.view' },
  { key: 'performance', title: '性能概览', chip: 'brand', icon: LineChartOutlined, path: '/performance/overview', permission: 'performance.view' },
  // 安全审计放第二排最后一个：排在第一排末尾会被当成「高频入口」，它是审计类低频操作
  { key: 'audit', title: '安全审计', chip: 'brand', icon: SafetyCertificateOutlined, path: '/audit/logs', permission: 'audit.log.view' },
]

/**
 * 我的资源：三张卡片的静态元信息。
 * 数量不从接口取，由 MyResources.vue 从 dashboard.resources 里按 key 取；
 * 这里只负责标题、跳转与各列标签文案，改文案不用动组件。
 * 卡片不再带图标（只靠标题区分），所以没有 chip 字段。
 * stats 的 key 必须与后端 /home/dashboard 的 resources.<card.key> 字段同名，否则取不到数。
 *
 * 口径（2026-10-13 统一）：三张卡都是「与我相关 / 我可见」，数字必须等于点进去看到的清单，
 * 所以每张卡的跳转都带上了对应列表的筛选参数：
 *   - 数据源   → 有实例配置/监控页权限时进实例配置页（该页对所有人都是全量）；
 *                只有 SQL 工作台权限时进 SQL 工作台（左栏是「我有权访问的授权数据源」）
 *   - 备份任务 → /backup/task?mine=1（备份中心的「只看我创建的」）
 *   - SQL工单  → /sql-workbench/orders?scope=MY_RELATED（工单列表的「与我相关」）
 * 后端 loadResources 用同一组判定条件，改这里务必同步改后端。
 */
export const resourceCards = [
  {
    key: 'instances',
    title: '数据源',
    scopeHint: '我能访问的数据源',
    // 与后端 loadResources 的实例卡判定保持同一组权限点（二选一）
    pathByPermission: {
      codes: ['system.instance.view', 'system.instance.manage', 'monitor.view', 'monitor.manage'],
      path: '/datasource/instance',
      fallbackPath: '/sql-workbench/query',
    },
    // 首页选了具体环境时，实例卡只统计该环境 → 点进实例配置页要把环境一起带过去，否则数字对不上
    supportsEnvironment: true,
    stats: [
      { key: 'normal', label: '正常' },
      { key: 'abnormal', label: '异常' },
      { key: 'total', label: '总数' },
    ],
  },
  {
    key: 'backup',
    title: '备份任务',
    scopeHint: '我创建的备份任务',
    path: '/backup/task',
    query: { mine: '1' },
    stats: [
      // 成功 = 该任务「最近一次运行」为 SUCCESS（后端 loadResources 口径），不是进行中的任务数
      { key: 'success', label: '成功' },
      { key: 'abnormal', label: '异常' },
      { key: 'total', label: '总数' },
    ],
  },
  {
    key: 'orders',
    title: 'SQL工单',
    scopeHint: '与我相关的工单',
    path: '/sql-workbench/orders',
    // range=all：工单列表默认只看近 7 天，而卡片是全量，必须一起带过去才能数字一致
    query: { scope: 'MY_RELATED', range: 'all' },
    stats: [
      // 「待处理」= 与我相关且处于草稿/审批中/待执行（后端 progress IN WAIT_PRECHECK/PENDING/APPROVED）
      { key: 'pending', label: '待处理' },
      // 第二列取「审批驳回」：数量来自 resources.orders.rejected（后端按 progress=REJECTED 统计）
      { key: 'rejected', label: '审批驳回' },
      { key: 'failed', label: '失败' },
      { key: 'total', label: '总数' },
    ],
  },
]

/**
 * 常用文档。
 * slug 必须命中 docs/manual 下的文档（构建产物 www/src/views/docs/manifest.json 的 docs[].slug），
 * 改这里之前先确认 slug，否则点了会落到文档中心的默认第一篇。
 */
export const commonDocs = [
  { key: 'quick-start', title: '快速开始', slug: 'start/quick-start', group: '入门', reading: '8 分钟' },
  { key: 'backup-restore', title: '备份恢复', slug: 'guide/backup-restore', group: '功能指南', reading: '10 分钟' },
  { key: 'sql-workbench', title: 'SQL工作台', slug: 'guide/sql-workbench', group: '功能指南', reading: '7 分钟' },
  { key: 'monitor-alert', title: '监控告警', slug: 'guide/monitor-alert', group: '功能指南', reading: '9 分钟' },
  { key: 'performance', title: '性能分析', slug: 'guide/performance', group: '功能指南', reading: '6 分钟' },
  { key: 'ai-assistant', title: 'AI 助手', slug: 'guide/ai-assistant', group: '功能指南', reading: '7 分钟' },
]

/**
 * 相关热文（静态兜底数据）。
 *
 * 线上数据来自 /api/v1/home/hot-articles（管理员在【系统管理 → 系统配置 → 热文管理】维护），
 * 这里的内容只在接口异常时作为兜底展示，保证首页第一屏不空；
 * 接口正常返回空数组时如实显示空态（管理员主动清空的配置要看得见）。
 *
 * url 为空串表示该条还没配外链：组件会渲染成不可点的普通条目（不跳空白页）。
 */
export const weeklyHotArticles = [
  { key: 'h1', title: '从 gray 到 crash recovery：MySQL 崩溃恢复的两阶段', source: '程序猿读历史', heat: '2.1k', tag: 'MySQL', url: '' },
  { key: 'h2', title: '备份为什么要校验：一次备份集无法恢复的复盘', source: '程序猿读历史', heat: '1.7k', tag: '备份恢复', url: '' },
  { key: 'h3', title: '慢 SQL 治理的三板斧：采集、归因、闭环', source: '程序猿读历史', heat: '1.4k', tag: '性能', url: '' },
  { key: 'h4', title: 'PostgreSQL 的 MVCC 与 vacuum，到底在忙什么', source: '程序猿读历史', heat: '1.1k', tag: 'PostgreSQL', url: '' },
]
