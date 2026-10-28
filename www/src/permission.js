/**
 * 路由权限控制
 * 参考 ant-design-vue-pro 的设计模式，适配 Vue3 + Pinia
 */
import { prepareForNextPageRequest } from '@/utils/request'
import { usePermissionStore } from '@/store/permission'
import { useUserStore } from '@/store/user'
import NProgress from 'nprogress'
import 'nprogress/nprogress.css'
import router from './router'

NProgress.configure({ showSpinner: false })

// 免登录白名单
const WHITE_LIST = ['/login', '/403']
// 默认路由
const DEFAULT_ROUTE = '/home'

const isSignificantRouteChange = (from, to) => {
  if (!from || !from.name || from.name === 'NotFound') {
    return false
  }
  if (WHITE_LIST.includes(to.path) && WHITE_LIST.includes(from.path)) {
    return false
  }
  return from.fullPath !== to.fullPath
}

const BOOTSTRAP_GUARD_TIMEOUT_MS = 90000
const WATCHDOG_FORCE_NEXT_MS = 60000

const withBootstrapTimeout = async (promise, userStore, permissionStore, to) => {
  let timeoutHandle = null
  const timeoutPromise = new Promise((_, reject) => {
    timeoutHandle = window.setTimeout(() => {
      const err = new Error('BootstrapTimeout')
      err.code = 'BOOTSTRAP_TIMEOUT'
      reject(err)
    }, BOOTSTRAP_GUARD_TIMEOUT_MS)
  })
  try {
    const result = await Promise.race([promise, timeoutPromise])
    return result
  } finally {
    if (timeoutHandle) {
      try { window.clearTimeout(timeoutHandle) } catch (_) {}
    }
  }
}

const isLongIdle = () => {
  try {
    const v = sessionStorage.getItem('__gi_last_activity__')
    if (!v) return false
    return Date.now() - (parseInt(v, 10) || 0) >= 2 * 60 * 1000
  } catch (_) {
    return false
  }
}

const areDynamicRoutesActuallyRegistered = () => {
  try {
    const routes = router.getRoutes()
    if (!routes || !routes.length) return false
    const pathSet = new Set(routes.map((r) => String(r.path || '')))
    const probePaths = [
      '/admin/roles',
      '/admin/users',
      '/orders',
      '/orders/workbench',
      '/das/query',
      '/backup/policies',
      '/backup/storage_targets',
    ]
    let hits = 0
    for (const p of probePaths) {
      if (pathSet.has(p)) hits += 1
    }
    return hits >= 2
  } catch (_) {
    return false
  }
}

const KNOWN_STATIC_ROUTE_NAMES = new Set([
  'Login',
  'Forbidden',
  'NotFound',
  'Root',
  'Home',
  'HomeTodos',
  'HomeRisks',
  'DasRoot',
  'DasQuery',
  'AccountBasic',
  'AccountNotifications',
  'AccountAPI',
  'OrdersWorkbench',
  'OrdersList',
  'TicketsWorkbench',
  'Tickets',
])

const isTargetRouteKnownByMatcher = (to) => {
  try {
    if (!to || (!to.path && !to.name)) return true
    if (to.name && typeof to.name === 'string' && KNOWN_STATIC_ROUTE_NAMES.has(to.name)) return true
    if (to.path) {
      if (WHITE_LIST.includes(to.path)) return true
      if (to.path === '/login' || to.path === '/' || to.path === '/403') return true
    }
    const resolved = router.resolve(to)
    if (!resolved) return false
    const resolvedName = resolved.name && typeof resolved.name === 'string' ? resolved.name : ''
    const resolvedPath = String(resolved.path || '')
    if (resolvedName === 'NotFound') return false
    if (/^\/:pathMatch|pathMatch\(\.\*\)/.test(resolvedPath)) return false
    if (to.name && typeof to.name === 'string') {
      if (resolvedName !== to.name) return false
    }
    if (to.path) {
      const cleanExpected = String(to.path).split('?')[0].split('#')[0]
      const cleanActual = resolvedPath.split('?')[0].split('#')[0]
      if (cleanExpected && cleanExpected !== '/' && cleanActual !== cleanExpected) return false
    }
    return true
  } catch (_) {
    return true
  }
}

const runBootstrapOnce = async (userStore, permissionStore) => {
  const task = (async () => {
    await userStore.getInfo()
    await permissionStore.GenerateRoutes(userStore.is_superuser, userStore.permissions)
  })()
  await withBootstrapTimeout(task, userStore, permissionStore, null)
}

const isSessionFatalError = (err) => {
  if (!err) return false
  const resp = err.response || err
  if (resp && typeof resp.status === 'number') {
    return [401, 403, 422].includes(resp.status)
  }
  const msg = (err && (err.message || err.code || err.name)) ? String(err.message || err.code || err.name) : ''
  return /^SessionExpired|UNAUTHORIZED|session expired/i.test(msg)
}

const isUserCancelledError = (err) => {
  if (!err) return false
  if (err.code === 'ERR_CANCELED' || err.name === 'CanceledError' || err.name === 'AbortError') return true
  const reason = err && (err.reason || err.signal && err.signal.reason)
  if (reason && typeof reason === 'string' && /ROUTE_CHANGE_CANCEL|IDLE_WAKEUP_CANCEL|FORCE_NEW_CONNECTION/.test(reason)) return true
  const msg = String(err && (err.message || '') || '')
  return /abort|cancel|network error|timeout of \d+ms exceeded/i.test(msg)
}

/**
 * 路由前置守卫
 * 处理登录验证、动态路由生成和权限控制
 */
router.beforeEach(async (to, from, next) => {
  NProgress.start()

  let nextCalled = false
  const safeNext = (...args) => {
    if (nextCalled) return
    nextCalled = true
    try { next(...args) } catch (_) {}
    try { NProgress.done() } catch (_) {}
  }

  const watchdogHandle = window.setTimeout(() => {
    if (!nextCalled) {
      console.warn('[Permission] 路由守卫看门狗触发，强制放行:', to.fullPath)
      try { prepareForNextPageRequest() } catch (_) {}
      safeNext()
    }
  }, WATCHDOG_FORCE_NEXT_MS)

  const markActivity = () => {
    try { sessionStorage.setItem('__gi_last_activity__', String(Date.now())) } catch (_) {}
  }
  markActivity()

  try {
    if (isSignificantRouteChange(from, to) || isLongIdle()) {
      try { prepareForNextPageRequest() } catch (_) {}
    }
  } catch (_) {}

  try {
    const userStore = useUserStore()
    const permissionStore = usePermissionStore()

    if (WHITE_LIST.includes(to.path)) {
      safeNext()
      return
    }

    if (!userStore.isLoggedIn) {
      safeNext({ path: '/login', query: { redirect: to.fullPath } })
      return
    }

    if (to.path === '/login') {
      safeNext({ path: DEFAULT_ROUTE })
      return
    }

    const targetKnown = isTargetRouteKnownByMatcher(to)
    const matcherDamaged = !targetKnown || (permissionStore.isRoutesGenerated && !areDynamicRoutesActuallyRegistered())

    const storageTargetDirectAccess =
      typeof to.path === 'string' &&
      to.path === '/backup/storage_targets' &&
      (!to.name || to.name === 'NotFound' || !targetKnown)

    const needRoutesRecheck =
      !permissionStore.isRoutesGenerated ||
      matcherDamaged ||
      storageTargetDirectAccess

    if (needRoutesRecheck) {
      const needReset =
      permissionStore.isRoutesGenerated &&
        (!targetKnown || !areDynamicRoutesActuallyRegistered() || storageTargetDirectAccess)
      if (needReset) {
        console.warn('[Permission] 动态路由疑似从Vue Router matcher中丢失，强制重置并重新bootstrap. targetKnown=', targetKnown, 'to=', to.fullPath)
        try { permissionStore.reset() } catch (_) {}
      }
      let bootstrapOK = false
      let lastErr = null
      let fatal = false
      const maxAttempts = isLongIdle() ? 2 : 3
      for (let attempt = 0; attempt < maxAttempts && !bootstrapOK; attempt += 1) {
        try {
          await runBootstrapOnce(userStore, permissionStore)
          bootstrapOK = true
        } catch (err) {
          lastErr = err
          const isTimeout = err && (err.code === 'BOOTSTRAP_TIMEOUT' || /BootstrapTimeout/.test(err && err.message))
          if (isSessionFatalError(err)) {
            fatal = true
            break
          }
          console.warn(`[Permission] bootstrap 第${attempt + 1}次失败:`, isTimeout ? 'timeout' : (err && err.message ? err.message : err))
          if (attempt < maxAttempts - 1) {
            const delayMs = isUserCancelledError(err) ? 250 : 800
            try { await new Promise((r) => window.setTimeout(r, delayMs)) } catch (_) {}
          }
        }
      }
      if (!bootstrapOK) {
        if (fatal || isSessionFatalError(lastErr)) {
          console.error('[Permission] bootstrap 鉴权失败，跳登录')
          try { userStore.clear() } catch (_) {}
          try { permissionStore.reset() } catch (_) {}
          safeNext({
            path: '/login',
            query: {
              redirect: to.fullPath,
            },
          })
          return
        }
        const isTimeout = lastErr && (lastErr.code === 'BOOTSTRAP_TIMEOUT' || /BootstrapTimeout/.test(lastErr && lastErr.message))
        console.warn('[Permission] bootstrap 失败但非鉴权级错误，放行目标页由组件自行加载:', to.fullPath, isTimeout ? 'timeout' : lastErr)
        if (to.name === 'NotFound') {
          // 注意不能写 { path: to.fullPath }：vue-router 4 的对象 location 不解析
          // path 里内嵌的 ？query，会把 edit_order_id 这类上下文参数整段吞掉。
          safeNext({ path: to.path, query: to.query, hash: to.hash, replace: true })
          return
        }
        // 不能展开整个路由对象（{...to}）：matched/redirectedFrom 等字段会干扰解析，
        // 实测会把 query 丢掉 —— 冷加载 /sql-workbench/create?edit_order_id=xxx 时
        // 编辑上下文被静默剥掉，页面退化成空白新建表单。
        safeNext({ path: to.path, query: to.query, hash: to.hash, replace: true })
        return
      }

      if (to.name === 'NotFound') {
        // 同上：path 字段不解析内嵌 query
        safeNext({ path: to.path, query: to.query, hash: to.hash, replace: true })
        return
      }
      safeNext({ path: to.path, query: to.query, hash: to.hash, replace: true })
      return
    }

    try {
      if (typeof to.path === 'string' && to.path === '/backup/storage_targets') {
        safeNext()
        return
      }
      if (to.meta?.permission && !userStore.hasPermission(to.meta.permission)) {
        safeNext({ path: '/403' })
        return
      }
    } catch (permErr) {
      console.warn('[Permission] 权限校验异常，默认放行:', permErr)
    }

    safeNext()
  } catch (fatalErr) {
    console.error('[Permission] 守卫致命异常，强制放行:', fatalErr)
    safeNext()
  } finally {
    try { window.clearTimeout(watchdogHandle) } catch (_) {}
  }
})

/**
 * 路由后置守卫
 */
router.afterEach(() => {
  NProgress.done()
})
