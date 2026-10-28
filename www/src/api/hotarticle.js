import { del, get, post, put } from '@/utils/request'

// ─────────────────────────────────────────────────────────────
// 管理侧：热文管理（系统管理 → 系统配置 → 热文管理）
// 需要 system.hotarticle.view / system.hotarticle.manage 权限
// ─────────────────────────────────────────────────────────────
export const getHotArticleListApi = (params, config = {}) => get('/api/v1/admin/hot-articles', params, config)
export const createHotArticleApi = (data) => post('/api/v1/admin/hot-articles', data)
export const updateHotArticleApi = (id, data) => put(`/api/v1/admin/hot-articles/${id}`, data)
export const updateHotArticleStatusApi = (id, enabled) =>
  put(`/api/v1/admin/hot-articles/${id}/status`, { enabled })
export const deleteHotArticleApi = (id) => del(`/api/v1/admin/hot-articles/${id}`)
// 批量排序：items = [{ id, sort_order }]，按传入值重写排序（幂等）。
// 用 POST 而非 PUT：PUT 树下已有 /:id 参数段，gin 不允许静态段与参数段并存。
export const updateHotArticleSortApi = (items) => post('/api/v1/admin/hot-articles/sort', { items })

// ─────────────────────────────────────────────────────────────
// 用户侧：首页「相关热文」数据源（仅要求登录）
// 返回 { articles: [...] }，即「已启用 + 排序前 3」的条目；为空是合法结果
// ─────────────────────────────────────────────────────────────
export const getHomeHotArticlesApi = (config = {}) => get('/api/v1/home/hot-articles', {}, config)
