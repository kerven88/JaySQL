/**
 * Vue Router 配置
 * 路由守卫逻辑已移至 @/permission.js
 */
import { createRouter, createWebHistory } from 'vue-router'
import { staticRoutes } from './ark'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: staticRoutes,
})

// ---------------------------------------------------------------------------
// 发布自愈：服务端重新部署后，旧标签页里已加载的 SPA 仍持有旧 chunk 文件名，
// 懒加载路由（如工单详情）时请求旧 hash 的 js → 404 → 动态 import 失败，
// vue-router 会静默中止本次跳转（表现为：接口正常、提示正常、页面不跳转）。
// 这里捕获该类错误，整页跳到目标路由强制加载最新前端。
// 注意：不能用「一次性」标记（用户一整天不关标签页，第二次发版后就永久失效），
// 改为时间窗冷却（10s 内最多整页重载一次），且导航成功后立即清零。
// ---------------------------------------------------------------------------
const CHUNK_RELOAD_COOLDOWN_MS = 10 * 1000

const shouldChunkReload = () => {
  try {
    const last = parseInt(sessionStorage.getItem('__gi_chunk_reload__') || '0', 10) || 0
    return Date.now() - last >= CHUNK_RELOAD_COOLDOWN_MS
  } catch (_) {
    return true
  }
}

const markChunkReload = () => {
  try { sessionStorage.setItem('__gi_chunk_reload__', String(Date.now())) } catch (_) {}
}

const clearChunkReload = () => {
  try { sessionStorage.removeItem('__gi_chunk_reload__') } catch (_) {}
}

router.onError((error, to) => {
  const msg = String(error?.message || error || '')
  const isChunkLoadError =
    /Failed to fetch dynamically imported module|Importing a module script failed|error loading dynamically imported module|Loading chunk \d+ failed|Loading CSS chunk/i.test(msg)
  if (!isChunkLoadError) return
  if (!shouldChunkReload()) return
  markChunkReload()
  const target = to?.fullPath || '/'
  window.location.replace(window.location.origin + target)
})

// 导航成功即清零：下次发版后旧标签页首次跳转仍能自愈
router.afterEach(() => {
  clearChunkReload()
})

export default router
