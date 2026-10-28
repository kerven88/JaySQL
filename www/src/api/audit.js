import { get } from '@/utils/request'

export const getAuditLogsApi = (params) => get('/api/v1/audit/logs', params)

export const getAuditLogDetailApi = (auditId) => get(`/api/v1/audit/logs/${auditId}`)
