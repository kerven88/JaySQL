import { del, get, post, put } from '@/utils/request'

export const getUsersApi = (params) => get('/api/v1/admin/users', params)
export const addUsersApi = (data) => post('/api/v1/admin/users', data)
export const updateUsersApi = (data) => put(`/api/v1/admin/users/${data.uid}`, data)
export const deleteUsersApi = (data) => del(`/api/v1/admin/users/${data}`, data)
export const batchDeleteUsersApi = (uids) => post('/api/v1/admin/users/batch-delete', { uids })
export const getUserAccessApi = (uid) => get(`/api/v1/admin/users/${uid}/access`)
export const ResetPasswordApi = (params) => post('/api/v1/admin/users/reset-password', params)
export const getRolesApi = (params) => get('/api/v1/admin/roles', params)
export const createRolesApi = (data) => post('/api/v1/admin/roles', data)
export const updateRolesApi = (data) => put(`/api/v1/admin/roles/${data.id}`, data)
export const deleteRolesApi = (data) => del(`/api/v1/admin/roles/${data}`, data)
export const getPermissionsApi = (params) => get('/api/v1/admin/permissions', params)
// 实例
export const getInstancesApi = (params, config = {}) => get('/api/v1/admin/instances', params, config)
export const createInstancesApi = (data) => post('/api/v1/admin/instances', data)
export const updateInstancesApi = (data) => put(`/api/v1/admin/instances/${data.id}`, data)
// 行内快速改名：只更新 remark，避免走全量更新接口时漏传字段冲掉连接信息
export const updateInstanceRemarkApi = (id, remark) =>
  put(`/api/v1/admin/instances/${id}/remark`, { remark })
// config 可传 { silentBusinessError: true }：数据源删除被拒（仍被备份/恢复任务引用）时，
// 页面要弹带跳转入口的窗口，这里静默掉全局 toast，避免同一次操作弹两个提示。
export const deleteInstancesApi = (data, config = {}) =>
  del(`/api/v1/admin/instances/${data}`, data, config)
export const testInstanceConnectionApi = (id, config = {}) =>
  post(`/api/v1/admin/instances/${id}/test`, {}, config)
export const testInstanceConnectionByPayloadApi = (data, config = {}) =>
  post(`/api/v1/admin/instances/test`, data, config)
export const syncInstanceMetaApi = (id) => post(`/api/v1/admin/instances/${id}/sync-meta`, {})
export const batchDeleteInstancesApi = (ids) => post('/api/v1/admin/instances/batch-delete', { ids })
export const batchImportInstancesApi = (data, config = {}) =>
  post('/api/v1/admin/instances/batch-import', data, config)
// 公有云数据库集成 (阿里云/华为云/腾讯云)
export const validateCloudAccountApi = (data, config = {}) =>
  post('/api/v1/admin/cloud-accounts/validate', data, config)
export const discoverCloudInstancesApi = (data, config = {}) =>
  post('/api/v1/admin/cloud-accounts/discover', data, config)
export const importCloudInstancesApi = (data, config = {}) =>
  post('/api/v1/admin/cloud-accounts/import', data, config)
// 环境
export const getEnvironmentsApi = (params, config = {}) =>
  get('/api/v1/admin/environments', params, { silentHttpError: true, skipGlobalMessageError: true, ...config })
export const createEnvironmentsApi = (data) => post('/api/v1/admin/environment', data)
export const updateEnvironmentsApi = (data) => put(`/api/v1/admin/environment/${data.id}`, data)
export const deleteEnvironmentsApi = (data) => del(`/api/v1/admin/environment/${data}`, data)
// inspect（SQL 开发规范）// SQL 规范
export const getSQLStandardsApi = (params) => get('/api/v1/admin/inspect/sql-standards', params)
export const createSQLStandardApi = (data) => post('/api/v1/admin/inspect/sql-standards', data)
// 根据实例 ID 查询其绑定的 SQL 开发规范（工单详情页「查阅规范」用）
export const getSQLStandardByInstanceApi = (instanceID) => get(`/api/v1/admin/inspect/sql-standards/by-instance/${instanceID}`)
export const getSQLStandardDetailApi = (id) => get(`/api/v1/admin/inspect/sql-standards/${id}`)
export const updateSQLStandardApi = (data) => put(`/api/v1/admin/inspect/sql-standards/${data.id}`, data)
export const deleteSQLStandardApi = (id) => del(`/api/v1/admin/inspect/sql-standards/${id}`)
export const updateSQLStandardRuleApi = (data) => put(`/api/v1/admin/inspect/sql-standards/rules/${data.rule_id}`, data)
export const getSQLStandardInstancesApi = (params) => get(`/api/v1/admin/inspect/sql-standards/${params.standard_id}/instances`, params)
export const bindSQLStandardInstanceApi = (data) => post(`/api/v1/admin/inspect/sql-standards/${data.standard_id}/instances`, data)
export const unbindSQLStandardInstanceApi = (id) => del(`/api/v1/admin/inspect/sql-standards/instances/${id}`)
export const getSQLStandardAvailableInstancesApi = (params) => get(`/api/v1/admin/inspect/sql-standards/${params.standard_id}/available-instances`, params)
// das
//
// 【旧授权表 insight_das_schema_perms / table_perms】只保留「新增」入口。
// 读与撤权一律走下面的 data-grants（新表，判定侧唯一权威）。
// 旧表的 GET / DELETE 已不再被任何页面使用，故不再导出，避免又有人接错数据源。
// 新增落旧表后由后端 ReconcileLegacyGrantsForPrincipal 对账进新表。
export const createDasSchemasGrantApi = (data) => post('/api/v1/admin/das/schemas/grant', data)
export const createDasTablesGrantApi = (data) => post('/api/v1/admin/das/tables/grant', data)
export const getDasInstancesListApi = (params) => get('/api/v1/admin/das/instances/list', params)
export const getDasSchemasListApi = (params) => get('/api/v1/admin/das/schemas/list', params)
export const getDasTablesListApi = (params) => get('/api/v1/admin/das/tables/list', params)

// 细粒度数据授权（新模型 insight_das_data_grants）
//
// 「数据访问」管理页的读与撤权走这一组接口：新表是数据可见性的唯一权威来源
// （HasDataGrant / CheckDataAccess 只认它），旧表 /schemas/grant 仅保留新增入口
// （新增后由后端 ReconcileLegacyGrantsForPrincipal 对账进新表）。
export const getDasDataGrantsApi = (params) => get('/api/v1/admin/das/data-grants', params)
export const createDasDataGrantApi = (data) => post('/api/v1/admin/das/data-grants', data)
export const revokeDasDataGrantApi = (data) => post('/api/v1/admin/das/data-grants/revoke', data)
export const renewDasDataGrantApi = (data) => post('/api/v1/admin/das/data-grants/renew', data)
export const getDasDataGrantAuditsApi = (params) =>
  get('/api/v1/admin/das/data-grants/audits', params)
