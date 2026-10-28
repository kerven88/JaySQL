// AI 助手会话记录的存储契约 —— 全项目唯一出口
//
// 为什么单独一个文件：
//   这些键既要被 useAIConversations（进页面时读写）使用，也要在「退出登录」时
//   被 store/user.js 清除。两处各写一份常量与键格式，迟早漂移（v1 就吃过这个亏）。
//
// 存储口径：
//   1. 仅登录可用：未登录既不复原历史、也不落盘。避免在登录页读到同一浏览器里
//      他人留下的会话。
//   2. 按账号隔离：键带 uid（缺失时退化为 username），换账号登录互不可见。
//   3. 登录会话级：退出登录（含 token 失效、鉴权失败跳登录）即清除该账号记录 ——
//      重新登录必须是「全新界面」；同一次登录内的切页/刷新保留，这才是持久化的价值。
//   4. 版本号：存储结构变更时 STORAGE_VERSION +1，旧数据整包丢弃（不做迁移）。
//   5. 规模上限：localStorage 单域名约 5MB，写满会连累整站所有写入失败，
//      故落盘前先裁剪（保留最近 MAX_CONVERSATIONS 个会话、每会话最近
//      MAX_MESSAGES_PER_CONV 条消息）。
//   6. 登出闸门：见下方 suspend 注释 —— 不关闸会把刚清掉的记录在整页刷新时写回。

export const STORAGE_VERSION = 2
export const MAX_CONVERSATIONS = 50
export const MAX_MESSAGES_PER_CONV = 300
export const PERSIST_DEBOUNCE_MS = 250

// v1 及更早的「全局无版本键」：新版改为按账号 + 版本分键，残留键一次性清扫
export const LEGACY_STORAGE_KEYS = [
  'jaysql_ai_conversations',
  'jaysql_ai_current_conversation',
  'jaysql_ai_conversations_v1',
]

const KEY_PREFIX = 'jaysql_ai_conversations_v'

// 账号标识：useAIConversations 与 store/user.js 共用同一取值口径，保证「写的键」
// 与「登出时清的键」永远一致
export const aiUserKeyOf = (user) => {
  if (!user) return ''
  return String(user.uid || user.username || '').trim()
}

// 某账号在当前版本下的存储键
export const aiConversationsKey = (userKey) =>
  `${KEY_PREFIX}${STORAGE_VERSION}_${String(userKey || '').trim()}`

/* ---------------- 登出闸门 ----------------
   退出登录走的是整页刷新（Layout.vue 里 location.reload）。刷新会触发 AI 页
   beforeunload 的兜底落盘，而那个闭包持有的是「登出前那个账号」的键与内存里的
   会话数组 —— 不在清键之前先把闸门关上，刚清掉的记录会被原样写回去，等于没清。
   闸门是进程内状态，登出后必然整页重载，所以不需要持久化。 */
let persistSuspended = false

export const suspendAIConversationPersist = () => {
  persistSuspended = true
}

// 重新开闸：新的一次进入 AI 页 = 新的一次登录会话开始。
// 这一步必须有 —— 401 会话过期走的是前端路由跳转（不整页刷新），若不重开闸，
// 重新登录后本进程内的落盘会一直失效。
export const resumeAIConversationPersist = () => {
  persistSuspended = false
}

export const isAIConversationPersistSuspended = () => persistSuspended

/* ---------------- 清除 ----------------
   清掉「本账号」的会话键，外加无主的遗留键（旧全局键、v1 键，无法归属到任何账号）。
   只清本账号：同浏览器上其他账号的数据由各自的键隔离，登出不该误伤旁人。 */
export const clearAIConversationStorage = (userKey = '') => {
  const removed = []
  const wanted = String(userKey || '').trim()
  try {
    Object.keys(localStorage).forEach((key) => {
      if (!key.startsWith(KEY_PREFIX)) return
      const matched = /^(\d+)_(.*)$/.exec(key.slice(KEY_PREFIX.length))
      // 不符合「版本_账号」结构的历史残留键，一并清掉
      if (!matched || matched[2] === wanted) {
        localStorage.removeItem(key)
        removed.push(key)
      }
    })
    LEGACY_STORAGE_KEYS.forEach((key) => {
      if (localStorage.getItem(key) !== null) {
        localStorage.removeItem(key)
        removed.push(key)
      }
    })
  } catch (_) {
    /* 隐私模式 / 存储被禁用：静默降级，不影响登出主流程 */
  }
  return removed
}
