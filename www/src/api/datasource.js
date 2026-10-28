/**
 * 数据源管理模块 API
 *
 * 数据源总览（管理侧）：一次取回实例清单 + 探活状态 + 监控/备份/SQL 规范覆盖标记，
 * 避免前端拼 4 个域的接口（权限口径由后端 RequireAnyPermission 统一收敛）。
 */
import { del, get, post } from '@/utils/request'

export const getDatasourceOverviewApi = (params) => get('/api/v1/datasource/overview', params)

// ---------------------------------------------------------------------------
// 主机登录
//
// 三个接口分工：
//   precheck   —— 开窗口后自检（凭据齐备 + 主机可连 + 返回主机密钥指纹），不建交互式会话
//   create     —— 自检通过后签发一次性令牌（60 秒、单次使用），真正的 SSH 在 WS 握手时才建
//   close      —— 主动断开，幂等
// ---------------------------------------------------------------------------
export const hostLoginPrecheckApi = (params) => post('/api/v1/datasource/host-login/precheck', params)

export const createHostLoginSessionApi = (params) => post('/api/v1/datasource/host-login/sessions', params)

export const closeHostLoginSessionApi = (sessionId) => del(`/api/v1/datasource/host-login/sessions/${sessionId}`)

export const getHostLoginSessionsApi = () => get('/api/v1/datasource/host-login/sessions')

// WS 地址：ws(s)://<当前域名>/api/v1/datasource/host-login/ws/<session_id>?token=<一次性令牌>
// 令牌必须走 query —— 浏览器 WebSocket API 不允许自定义握手头，带不了 Authorization，
// 后端这条路由也就不挂 JWT 中间件（与工单模块 /ws/:channel 同一处理）。
export const buildHostLoginWsUrl = (sessionId, token) => {
  const scheme = window.location.protocol === 'https:' ? 'wss://' : 'ws://'
  return `${scheme}${window.location.host}/api/v1/datasource/host-login/ws/${sessionId}?token=${encodeURIComponent(token)}`
}
