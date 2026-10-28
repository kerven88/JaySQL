import { get, getBlob, post, put } from '@/utils/request'

export const getOrderEnvironmentsApi = (params) => get('/api/v1/orders/environments', params)
export const getOrderDataSourcesApi = (params) => get('/api/v1/orders/data-sources', params)
export const getOrderInstancesApi = (params) => get('/api/v1/orders/instances', params)
export const getOrderSchemasApi = (params) => get('/api/v1/orders/schemas', params)
export const getOrderUsersApi = (params) => get('/api/v1/orders/users', params)

export const inspectOrderSyntaxApi = (data) => post('/api/v1/orders/inspect-syntax', data)

export const preInspectOrderApi = (data, config = {}) => post('/api/v1/orders/pre-inspect', data, config)

// 工单规范预审快照（历史态）：终态工单只读快照，不重新触发实时预审
export const getOrderPreInspectSnapshotApi = (params, config = {}) =>
  get(`/api/v1/orders/${params.order_id}/pre-inspect`, params, config)

export const createOrderApi = (data) => post('/api/v1/orders', data)
export const getOrderListApi = (params, config = {}) => get('/api/v1/orders', params, config)
export const getOrderDetailApi = (params) => get(`/api/v1/orders/${params.order_id}`)

export const getOrderApprovalStatusApi = (params) =>
  get(`/api/v1/orders/approvals/${params.order_id}`)
export const getOrderLogsApi = (params) => get(`/api/v1/orders/logs/${params.order_id}`)

export const submitOrderApprovalApi = (data) => put(`/api/v1/orders/actions/submit-approval`, data)

// 钉钉审批：提交审批弹窗实时预览「这位审批人能否收到钉钉通知」。
// available=false 不是错误，是「当前发不了」的正常结论，故用 silentHttpError 静默失败，
// 由弹窗内的提示行展示原因，不弹全局报错（用户还没提交，不该被报错打断）。
export const previewDingTalkNotifyApi = (params, config = {}) =>
  get('/api/v1/orders/approval/dingtalk/preview', params, { silentHttpError: true, ...config })

// 钉钉审批长连接状态（诊断用：区分「没连上」与「连上但没回调」）
export const getDingTalkStreamStatusApi = (config = {}) =>
  get('/api/v1/orders/approval/dingtalk/status', undefined, { silentHttpError: true, ...config })

export const approvalOrderApi = (data) => put(`/api/v1/orders/actions/approval`, data)
export const revokeOrderApi = (data) => put(`/api/v1/orders/actions/revoke`, data)
// 撤回工单：进入「已撤回」状态，可编辑后重新提交（工单 ID 不变）
export const withdrawOrderApi = (data) => put(`/api/v1/orders/actions/withdraw`, data)
// 编辑已撤回工单内容：保存后回到「待预审」
export const updateOrderContentApi = (data) => put(`/api/v1/orders/actions/update-content`, data)
export const transferOrderApi = (data) => put(`/api/v1/orders/actions/transfer`, data)
export const terminateOrderApi = (data) => put(`/api/v1/orders/actions/terminate`, data)
export const completeOrderApi = (data) => put(`/api/v1/orders/actions/complete`, data)
export const failOrderApi = (data) => put(`/api/v1/orders/actions/fail`, data)
export const reviewOrderApi = (data) => put(`/api/v1/orders/actions/review`, data)
export const updateOrderTitleApi = (data) => put(`/api/v1/orders/actions/update-title`, data)

// 生成tasks
export const generateOrderTasksApi = (data) => post(`/api/v1/orders/tasks`, data)

// 获取tasks
export const getOrderTasksApi = (params) => get(`/api/v1/orders/tasks/${params.order_id}`, params)
export const executeTaskApi = (data) => post(`/api/v1/orders/tasks/execute`, data)
export const executebatchTasksApi = (data) => post(`/api/v1/orders/tasks/execute-batch`, data)

// 下载导出文件
export const downloadExportFileApi = (params) => getBlob(`/api/v1/orders/tasks/exports/${params}`)
