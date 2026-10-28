import { del, get, post, put } from '@/utils/request'

// ─────────────────────────────────────────────────────────────
// 品牌设置（登录页 Logo / 页面左上角 Logo / 品牌文字）
//
// 读取是**公开接口**：登录页在未登录态就要加载自定义 Logo，所以 GET /api/v1/brand
// 不挂鉴权；读写分离，写操作全部走 /api/v1/admin/brand（system.access 权限）。
// ─────────────────────────────────────────────────────────────

// 当前品牌配置。未配置时各字段均为空串，前端回落内置默认图 / 默认文案。
export const getBrandApi = (config = {}) => get('/api/v1/brand', {}, config)

// 上传 / 替换 Logo。type: login | sidebar
// type 同时放进 FormData 与 query：FormData 是主通道，query 只是兜底
// （某些环境 multipart 解析早于字段读取时会漏掉 body 字段）。
export const uploadBrandLogoApi = (type, formData, config = {}) =>
  post(`/api/v1/admin/brand/logo?type=${encodeURIComponent(type)}`, formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
    contentType: false,
    processData: false,
    ...config,
  })

// 恢复内置默认 Logo。type: login | sidebar
export const resetBrandLogoApi = (type) =>
  del(`/api/v1/admin/brand/logo?type=${encodeURIComponent(type)}`)

// 保存品牌文字（品牌名称 / 首页大标题）。字段传空串 = 恢复内置默认文案。
export const updateBrandTextApi = (data) => put('/api/v1/admin/brand/text', data)
