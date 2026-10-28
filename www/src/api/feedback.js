import { del, get, post, put } from '@/utils/request'

// ─────────────────────────────────────────────────────────────
// 用户侧：提交反馈
// multipart/form-data：category / content / contact / page_path / user_agent
//                      + images（可多张，字段名 images）
// 注意：必须显式声明 multipart/form-data，否则 axios 会把 FormData 当 JSON 序列化。
// ─────────────────────────────────────────────────────────────
export const submitFeedbackApi = (formData) =>
  post('/api/v1/feedback', formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
    contentType: false,
    processData: false,
  })

// ─────────────────────────────────────────────────────────────
// 管理侧：反馈管理（系统管理 → 系统配置 → 反馈管理）
// ─────────────────────────────────────────────────────────────
export const getFeedbackListApi = (params, config = {}) => get('/api/v1/admin/feedback', params, config)
// 预留：列表页已不再展示状态统计条；该接口可供「顶栏未处理角标」等场景复用（后端保持可用）
export const getFeedbackStatsApi = (config = {}) => get('/api/v1/admin/feedback/stats', {}, config)
export const getFeedbackDetailApi = (id, config = {}) => get('/api/v1/admin/feedback/detail', { id }, config)
export const updateFeedbackApi = (id, data) => put(`/api/v1/admin/feedback/${id}`, data)
export const deleteFeedbackApi = (id) => del(`/api/v1/admin/feedback/${id}`)
