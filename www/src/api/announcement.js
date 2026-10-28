import { del, get, post, put } from '@/utils/request'

// ─────────────────────────────────────────────────────────────
// 管理侧：公告管理（系统管理 → 系统配置 → 公告管理）
// 需要 system.announcement.view / system.announcement.manage 权限
// ─────────────────────────────────────────────────────────────
export const getAnnouncementListApi = (params, config = {}) => get('/api/v1/admin/announcements', params, config)
export const createAnnouncementApi = (data) => post('/api/v1/admin/announcements', data)
export const updateAnnouncementApi = (id, data) => put(`/api/v1/admin/announcements/${id}`, data)
export const updateAnnouncementStatusApi = (id, enabled) =>
  put(`/api/v1/admin/announcements/${id}/status`, { enabled })
export const deleteAnnouncementApi = (id) => del(`/api/v1/admin/announcements/${id}`)

// 展示设置：滚动速度(px/s) 与 每条停留时长(ms)，全局一份
export const getAnnouncementSettingsApi = (config = {}) => get('/api/v1/admin/announcements/settings', {}, config)
export const updateAnnouncementSettingsApi = (data) => put('/api/v1/admin/announcements/settings', data)

// ─────────────────────────────────────────────────────────────
// 用户侧：页面公告条数据源（仅要求登录）
// 返回 { items: 全部生效公告, settings: 展示设置 }；无生效公告时 items 为空数组
// ─────────────────────────────────────────────────────────────
export const getActiveAnnouncementsApi = (config = {}) => get('/api/v1/announcements/active', {}, config)
