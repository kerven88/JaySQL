import { get, post } from '@/utils/request'

// AI 对话：运行时真实模型调用。
// 返回 source=llm（真实模型回答）或 source=rule_engine（后端已降级，前端回落本地规则）。
export const aiChatCompletionApi = (data, config = {}) => post('/api/v1/ai/chat/completions', data, config)

// AI 对话流式（SSE）：直答路由逐字下发（start/delta/done/error 四类事件）。
// 用 fetch 手工消费，走不了 axios —— 它拿不到流式响应体。
export const AI_CHAT_STREAM_PATH = '/api/v1/ai/chat/stream'

// AI 对话：主动取消运行中的一次编排。
// run_id 由前端在发起提问时生成、随 /chat/stream（或 /chat/completions）一起发出，
// 取消时原样回传即可把取消信号投递到服务端仍在跑的那一次 —— 不依赖本地连接是否已被断开。
// 独立于被取消的请求，因此不会被 AbortController 一并掐掉；重复取消是幂等的。
export const aiChatCancelApi = (data, config = {}) =>
  post('/api/v1/ai/chat/cancel', data, {
    silentBusinessError: true,
    silentHttpError: true,
    ...config,
  })

// AI 对话：在工单草稿卡片上点「确认创建」后落库。
// 创建人由后端从当前登录会话取（前端不传，也无权传）；落库即进入规范预审（WAIT_PRECHECK）
// 并自动生成预审快照，用户不需要再做任何提交动作。
export const aiCreateOrderConfirmApi = (data, config = {}) => post('/api/v1/ai/orders/confirm', data, config)

// AI 对话：在备份草稿卡片上点「确认创建」后落库。
// 创建人由后端从当前登录会话取（前端不传，也无权传）；落库即纳入调度。
export const aiCreateBackupConfirmApi = (data, config = {}) => post('/api/v1/ai/backups/confirm', data, config)

// ============================================================
// 大模型配置接入 —— 厂商模板
// ============================================================

// 厂商模板列表；params.all 为真时返回含停用项的全部模板（管理页）
export const aiVendorListApi = (params = {}, config = {}) => get('/api/v1/ai/vendors', params, config)
export const aiVendorTemplateCreateApi = (data, config = {}) => post('/api/v1/ai/vendor-templates', data, config)
export const aiVendorTemplateUpdateApi = (data, config = {}) => post('/api/v1/ai/vendor-templates/update', data, config)
export const aiVendorTemplateDeleteApi = (data, config = {}) => post('/api/v1/ai/vendor-templates/delete', data, config)

// ============================================================
// 大模型配置接入 —— 服务商
// ============================================================

export const aiProviderListApi = (config = {}) => get('/api/v1/ai/providers', {}, config)
export const aiProviderDetailApi = (params = {}, config = {}) => get('/api/v1/ai/providers/detail', params, config)
export const aiProviderCreateApi = (data, config = {}) => post('/api/v1/ai/providers', data, config)
export const aiProviderUpdateApi = (data, config = {}) => post('/api/v1/ai/providers/update', data, config)
export const aiProviderDeleteApi = (data, config = {}) => post('/api/v1/ai/providers/delete', data, config)
export const aiProviderEnableApi = (data, config = {}) => post('/api/v1/ai/providers/enable', data, config)
export const aiProviderDisableApi = (data, config = {}) => post('/api/v1/ai/providers/disable', data, config)
export const aiProviderSetDefaultApi = (data, config = {}) => post('/api/v1/ai/providers/set-default', data, config)

// 先测后存：凭证未落库即可验证（接入向导第三步）
export const aiProviderTestApi = (data, config = {}) => post('/api/v1/ai/providers/test-connection', data, config)
// 对已落库配置重新验证
export const aiProviderValidateApi = (data, config = {}) => post('/api/v1/ai/providers/validate', data, config)

// ============================================================
// 大模型配置接入 —— 模型清单
// ============================================================

export const aiModelListApi = (params = {}, config = {}) => get('/api/v1/ai/models', params, config)
export const aiModelCreateApi = (data, config = {}) => post('/api/v1/ai/models', data, config)
export const aiModelUpdateApi = (data, config = {}) => post('/api/v1/ai/models/update', data, config)
export const aiModelDeleteApi = (data, config = {}) => post('/api/v1/ai/models/delete', data, config)
export const aiModelSetDefaultApi = (data, config = {}) => post('/api/v1/ai/models/set-default', data, config)
