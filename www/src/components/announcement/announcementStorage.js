// 头部公告 —— 会话级关闭状态的唯一出口
//
// 为什么要单独一个文件：关闭状态既要被 PageAnnouncementBar 读写，
// 也要在「退出登录」时被清空（Layout.vue）。两处各写一个字符串常量
// 迟早漂移，所以把 key 与读写逻辑收在这里，两边都 import。
//
// 为什么是 sessionStorage 而不是 localStorage：
//   关闭行为对应「当前登录会话」，误点关闭不应永久屏蔽重要公告。
//   标签页/浏览器关闭后自动失效，重新登录即可按后台最新配置重新展示。

// ANNOUNCEMENT_CLOSED_KEY 关闭记录存储键
export const ANNOUNCEMENT_CLOSED_KEY = 'gi_announcement_closed'

// closeFingerprint 生成「这条公告的这一版内容」的指纹。
//
// 为什么不是只用 id：长期有效（生效窗为空）的公告被关掉后，只要标签页
// 不关就永远不会再出现，连管理员改了内容用户也看不到。带上 updated_at
// 后，公告一经编辑指纹就变，关过的用户会自动重新看到新内容。
// id 前缀保留是为了能在写入时清理同一条公告的历史版本记录。
export function closeFingerprint(id, updatedAt) {
  return `${id}@${updatedAt || ''}`
}

function safeRead() {
  try {
    const raw = sessionStorage.getItem(ANNOUNCEMENT_CLOSED_KEY)
    const arr = raw ? JSON.parse(raw) : []
    return Array.isArray(arr) ? arr.map(String) : []
  } catch (_) {
    // 隐私模式下 sessionStorage 不可用：降级为「每次都展示」，不影响主流程
    return []
  }
}

// readClosedSet 读取已关闭的公告指纹集合
export function readClosedSet() {
  return new Set(safeRead())
}

// markAnnouncementClosed 记录某条公告的当前版本已被关闭。
// 同时清掉同一条公告的历史版本记录，避免长期编辑后集合无限膨胀。
export function markAnnouncementClosed(id, updatedAt) {
  try {
    const fingerprint = closeFingerprint(id, updatedAt)
    const prefix = `${id}@`
    const next = safeRead().filter((v) => v !== String(id) && !v.startsWith(prefix))
    next.push(fingerprint)
    sessionStorage.setItem(ANNOUNCEMENT_CLOSED_KEY, JSON.stringify(next))
  } catch (_) {
    /* 存储不可用时静默降级 */
  }
}

// clearAnnouncementClosed 清空全部关闭记录。
// 退出登录时调用 —— 登出走的是 location.reload()，不清 sessionStorage，
// 不显式清一次的话「重新登录后重新展示」就是句空话。
export function clearAnnouncementClosed() {
  try {
    sessionStorage.removeItem(ANNOUNCEMENT_CLOSED_KEY)
  } catch (_) {
    /* 存储不可用时静默降级 */
  }
}
