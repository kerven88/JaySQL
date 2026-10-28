import { get, getBlob, post } from '@/utils/request'

// ================= 参数管理 =================
// 参数当前值实时查目标实例（SHOW VARIABLES / pg_settings / v$parameter / sys.configurations），不落平台库
export const getInstanceParamsApi = (params) => get('/api/v1/performance/params', params)
// 多实例对比：instance_ids 逗号分隔，2~5 个同家族实例
export const diffInstanceParamsApi = (params) => get('/api/v1/performance/params/diff', params)
// 修改历史（只增不改的审计链）
export const getParamChangesApi = (params) => get('/api/v1/performance/params/changes', params)
// 修改参数：需 performance.param.modify 权限；高危参数须 ack_risk=true 且 reason 必填
export const modifyInstanceParamApi = (data) => post('/api/v1/performance/params/modify', data)
// 回滚到历史值：同样走 modify 链路与审计
export const rollbackParamApi = (data) => post('/api/v1/performance/params/rollback', data)

export const getPerformanceInstancesApi = (params) => get('/api/v1/performance/instances', params)
export const getPerformanceOverviewApi = (params) => get('/api/v1/performance/overview', params)
export const getPerformanceSlowSQLApi = (params) => get('/api/v1/performance/slow-sql', params)
export const getPerformanceSlowSQLInstancesApi = (params) => get('/api/v1/performance/slow-sql/instances', params)
export const getPerformanceSlowSQLAnalysisApi = (params) => get('/api/v1/performance/slow-sql/analysis', params)
export const getPerformanceSlowSQLStatsApi = (params) => get('/api/v1/performance/slow-sql/stats', params)
// 慢SQL导出：按当前查询条件导出全量 CSV
export const exportPerformanceSlowSQLStatsApi = (params) => getBlob('/api/v1/performance/slow-sql/stats/export', params)
export const exportPerformanceSlowSQLApi = (params) => getBlob('/api/v1/performance/slow-sql/export', params)
export const getSlowLogCapabilityApi = (params) => get('/api/v1/performance/slowlog-capability', params)
export const reprobeSlowLogCapabilityApi = (instanceId) => post(`/api/v1/performance/slowlog-capability/${instanceId}/probe`)
// 采集前提条件清单（db_type 为空时返回三类引擎全量清单）
export const getSlowLogPrerequisitesApi = (params) => get('/api/v1/performance/slowlog-prerequisites', params)
// 全部可见实例的采集诊断（含 失败码 → 前提项 关联）
export const getSlowLogDiagnosisApi = () => get('/api/v1/performance/slowlog-diagnosis')
// 单个实例的前提清单 + 它自己的失败关联
export const getSlowLogInstancePrerequisitesApi = (instanceId) => get(`/api/v1/performance/slowlog-prerequisites/${instanceId}`)
