import { del, get, post, put } from '@/utils/request'

export const getMonitorDashboardApi = (params) => get('/api/v1/monitor/dashboard', params)
// 风险总览：全部风险实例 + 每实例的判定原因（大屏"风险总览"入口与下钻共用）
export const getMonitorRisksApi = (params) => get('/api/v1/monitor/risks', params)
export const getMonitorDetailApi = (params) => get('/api/v1/monitor/detail', params)
// 指标历史：明细页趋势图按时间范围取数（range: 15m / 1h / 6h / 24h / 7d / custom）
export const getMonitorMetricHistoryApi = (params) => get('/api/v1/monitor/metrics/history', params)
export const getMonitorTargetConfigsApi = (params) => get('/api/v1/monitor/config/targets', params)
export const updateMonitorTargetConfigApi = (instanceID, params) => put(`/api/v1/monitor/config/targets/${instanceID}`, params)
export const getMonitorRulesApi = (params) => get('/api/v1/monitor/config/rules', params)
export const createMonitorRuleApi = (params) => post('/api/v1/monitor/config/rules', params)
export const updateMonitorRuleApi = (id, params) => put(`/api/v1/monitor/config/rules/${id}`, params)
export const deleteMonitorRuleApi = (id) => del(`/api/v1/monitor/config/rules/${id}`)

// --- 采集健康度：回答"数据到底采到没有、为什么没有" ---
export const getMonitorStatusApi = () => get('/api/v1/monitor/status')

// --- 立即采集：新接入实例不必干等下一个采集周期 ---
export const collectMonitorInstanceApi = (instanceID) => post(`/api/v1/monitor/instances/${instanceID}/collect`)

// --- 明细弹窗刷新：同步采集一次并返回最新指标值（monitor.view 即可调用） ---
export const refreshMonitorDetailApi = (instanceID) => post('/api/v1/monitor/detail/refresh', { instance_id: instanceID })

// --- 采集频率：三处周期（Prometheus 抓取 / 落库 / PromQL 窗口与聚合）的唯一配置源 ---
export const getMonitorScrapeConfigApi = () => get('/api/v1/monitor/config/scrape')
export const updateMonitorScrapeConfigApi = (params) => put('/api/v1/monitor/config/scrape', params)

// --- 健康分级：实例健康/风险/故障的判定口径（等级 + 结果项 + 规则），平台级单行配置 ---
export const getMonitorHealthGradeApi = () => get('/api/v1/monitor/config/health-grade')
export const updateMonitorHealthGradeApi = (params) => put('/api/v1/monitor/config/health-grade', params)
export const resetMonitorHealthGradeApi = () => post('/api/v1/monitor/config/health-grade/reset')

// --- 内置指标清单：指标定义固定在后端代码里，只供指标模板勾选弹窗读取 ---
// （原「指标字典」模块已下线：PUT 写入接口随之移除，不再允许运行时改写指标元信息）
export const getMonitorMetricCatalogApi = (params) => get('/api/v1/monitor/metrics/catalog', params)

// --- 告警中心：告警事件检索（含已恢复与被忽略的），看告警只需 monitor.view ---
export const getMonitorAlertsApi = (params) => get('/api/v1/monitor/alerts', params)

// --- 告警概览统计：今日已恢复 + 近 7 天趋势（概览 Tab 专用）---
export const getMonitorAlertStatsApi = (params) => get('/api/v1/monitor/alerts/stats', params)

// --- 告警概览「告警实例总数」下钻：触发告警的 DB 实例列表（与列表同口径）---
export const getMonitorAlertInstancesApi = (params) => get('/api/v1/monitor/alerts/instances', params)

// --- 大屏「近 7 天告警趋势」卡：预设窗口内每段新增告警数（时间与页面顶部范围解耦）---
export const getMonitorAlertTrendApi = (params) => get('/api/v1/monitor/alerts/trend', params)

// --- 大屏告警人工处置：认领工单（ack）/ 忽略（ignore）/ 取消忽略（unignore）---
export const updateMonitorAlertActionApi = (params) => put('/api/v1/monitor/alerts/action', params)

// --- 指标模板：按数据库类型组织的采集指标集合；实例通过 metrics_template 绑定 ---
export const getMonitorTemplatesApi = (params) => get('/api/v1/monitor/templates', params)
export const getMonitorTemplateDetailApi = (id) => get(`/api/v1/monitor/templates/${id}`)
export const createMonitorTemplateApi = (params) => post('/api/v1/monitor/templates', params)
export const updateMonitorTemplateApi = (id, params) => put(`/api/v1/monitor/templates/${id}`, params)
export const deleteMonitorTemplateApi = (id) => del(`/api/v1/monitor/templates/${id}`)
export const bindMonitorTemplateInstancesApi = (id, params) => put(`/api/v1/monitor/templates/${id}/instances`, params)

// --- 采集策略：模板 - 实例 两级配置（"怎么采"：周期/保留/超时/重试/来源 + 指标模板）---
// 与上面的指标模板（"采哪些指标"）是两套：实例挂的是采集策略模板，
// 采集策略模板里的 metric_template 项再指向指标模板。
export const getMonitorStrategyTemplatesApi = (params) => get('/api/v1/monitor/strategy/templates', params)
export const getMonitorStrategyTemplateDetailApi = (id) => get(`/api/v1/monitor/strategy/templates/${id}`)
export const createMonitorStrategyTemplateApi = (params) => post('/api/v1/monitor/strategy/templates', params)
export const updateMonitorStrategyTemplateApi = (id, params) => put(`/api/v1/monitor/strategy/templates/${id}`, params)
export const deleteMonitorStrategyTemplateApi = (id) => del(`/api/v1/monitor/strategy/templates/${id}`)
// 挂载：把一批实例挂到某个模板（切换模板 = 直接挂载到另一个）
export const bindMonitorStrategyInstancesApi = (id, params) => put(`/api/v1/monitor/strategy/templates/${id}/instances`, params)
// 解绑：置空挂载，实例回落该类型的默认模板（不等于停止采集）
export const unbindMonitorStrategyInstancesApi = (params) => put('/api/v1/monitor/strategy/instances/unbind', params)
export const getMonitorInstanceStrategyApi = (instanceID) => get(`/api/v1/monitor/strategy/instances/${instanceID}`)
export const saveMonitorInstanceOverridesApi = (instanceID, params) => put(`/api/v1/monitor/strategy/instances/${instanceID}/overrides`, params)

// ============================================================================
// 告警策略模板：默认模板兜底 + 实例 1:1 挂载 + 规则覆盖式存储
// ============================================================================
export const getMonitorAlertTemplatesApi = (params) => get('/api/v1/monitor/alert/templates', params)
export const getMonitorAlertTemplateDetailApi = (id) => get(`/api/v1/monitor/alert/templates/${id}`)
export const createMonitorAlertTemplateApi = (params) => post('/api/v1/monitor/alert/templates', params)
export const updateMonitorAlertTemplateApi = (id, params) => put(`/api/v1/monitor/alert/templates/${id}`, params)
export const deleteMonitorAlertTemplateApi = (id) => del(`/api/v1/monitor/alert/templates/${id}`)
// 覆盖式保存模板规则集：同键覆盖（rule_id 不变）、缺键删除、新键插入
export const saveMonitorAlertTemplateRulesApi = (id, params) => put(`/api/v1/monitor/alert/templates/${id}/rules`, params)
// 挂载 / 解绑：解绑后实例回落该类型的默认告警模板
export const bindMonitorAlertInstancesApi = (id, params) => put(`/api/v1/monitor/alert/templates/${id}/instances`, params)
export const unbindMonitorAlertInstancesApi = (params) => put('/api/v1/monitor/alert/instances/unbind', params)

// --- 通知渠道：告警消息的出口（钉钉/飞书/邮件/企微），可配多个实例 ---
export const getNotifyChannelsApi = () => get('/api/v1/monitor/notify/channels')
export const getNotifyChannelsBriefApi = () => get('/api/v1/monitor/notify/channels/brief')
export const createNotifyChannelApi = (params) => post('/api/v1/monitor/notify/channels', params)
export const updateNotifyChannelApi = (id, params) => put(`/api/v1/monitor/notify/channels/${id}`, params)
export const deleteNotifyChannelApi = (id) => del(`/api/v1/monitor/notify/channels/${id}`)
export const testNotifyChannelApi = (id) => post(`/api/v1/monitor/notify/channels/${id}/test`)

// 【平台默认通知配置】的两个接口封装（get/save /api/v1/monitor/notify/defaults）已移除：
// 告警配置页的「平台默认」面板删除后前端无处调用；后端接口保留未动，需要时按下面两行加回即可：
//   export const getNotifyDefaultsApi = () => get('/api/v1/monitor/notify/defaults')
//   export const saveNotifyDefaultsApi = (params) => put('/api/v1/monitor/notify/defaults', params)

// --- 通知策略：把「哪些告警」路由到「哪些渠道 + 哪些接收人」 ---
export const getNotifyPoliciesApi = () => get('/api/v1/monitor/notify/policies')
export const createNotifyPolicyApi = (params) => post('/api/v1/monitor/notify/policies', params)
export const updateNotifyPolicyApi = (id, params) => put(`/api/v1/monitor/notify/policies/${id}`, params)
export const deleteNotifyPolicyApi = (id) => del(`/api/v1/monitor/notify/policies/${id}`)
export const previewNotifyPolicyApi = (params) => post('/api/v1/monitor/notify/policies/preview', params)
// 策略连通性测试：用样例告警跑完整派发链路（策略 → 渠道 → 接收人 → 投递）
export const testNotifyPolicyApi = (id) => post(`/api/v1/monitor/notify/policies/${id}/test`)

// --- 接收人选择器数据源（组织树 + 用户）与投递记录 ---
export const getNotifySubjectTreeApi = () => get('/api/v1/monitor/notify/subjects/tree')
export const getNotifyTasksApi = (params) => get('/api/v1/monitor/notify/tasks', params)
export const retryNotifyTaskApi = (id) => post(`/api/v1/monitor/notify/tasks/${id}/retry`)

// --- 负载快照：AWR 式实例负载诊断报告（首页实例列表 / 生成 / 详情 / 删除） ---
export const getLoadSnapshotInstancesApi = (params) => get('/api/v1/monitor/snapshot/instances', params)
export const getLoadSnapshotsApi = (params) => get('/api/v1/monitor/snapshots', params)
export const createLoadSnapshotApi = (params) => post('/api/v1/monitor/snapshots', params)
export const getLoadSnapshotDetailApi = (id) => get(`/api/v1/monitor/snapshots/${id}`)
export const deleteLoadSnapshotApi = (id) => del(`/api/v1/monitor/snapshots/${id}`)

export const previewNotifyMessageTemplateApi = (params) => post('/api/v1/monitor/notify/templates/preview', params)
