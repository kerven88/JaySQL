import router from '@/router'
import { useUserStore } from '@/store/user'
import { message } from 'ant-design-vue'
import axios from 'axios'
import Nprogress from 'nprogress'

// 请求头
axios.defaults.headers.post['Content-Type'] = 'application/json;charset=UTF-8'
axios.defaults.headers.put['Content-Type'] = 'application/json;charset=UTF-8'

const AUTH_WHITELIST = ['/api/v1/user/login', '/api/v1/user/register', '/api/v1/user/refresh_token']

let refreshTokenPromise = null
let redirectingForSessionExpired = false
let sessionExpiredNotified = false

let last403WarningAt = 0

const pendingAbortControllers = new Set()
const ABORT_REASON_ROUTE_CHANGE = 'ROUTE_CHANGE_CANCEL'
const ABORT_REASON_IDLE_WAKEUP = 'IDLE_WAKEUP_CANCEL'
const ABORT_REASON_FORCE_NEW_CONN = 'FORCE_NEW_CONNECTION'

const LAST_ACTIVITY_KEY = '__gi_last_activity__'
const IDLE_WAKEUP_THRESHOLD_MS = 5 * 60 * 1000
const FORCE_NEW_CONN_REQUESTS = 5

let forceNewConnRemaining = 0

const markLastActivity = () => {
  try { sessionStorage.setItem(LAST_ACTIVITY_KEY, String(Date.now())) } catch (_) {}
}

const getLastActivity = () => {
  try {
    const v = sessionStorage.getItem(LAST_ACTIVITY_KEY)
    return v ? parseInt(v, 10) || 0 : 0
  } catch (_) {
    return 0
  }
}

export function prepareForNextPageRequest() {
  if (!pendingAbortControllers.size) return
  for (const ctrl of pendingAbortControllers) {
    try {
      if (ctrl && typeof ctrl.abort === 'function' && !ctrl.signal?.aborted) {
        ctrl.abort(ABORT_REASON_ROUTE_CHANGE)
      }
    } catch (_) {
      /* ignore individual abort failures */
    }
  }
  pendingAbortControllers.clear()
}

export function forceMarkIdleWakeup() {
  forceNewConnRemaining = FORCE_NEW_CONN_REQUESTS
  if (pendingAbortControllers.size) {
    for (const ctrl of pendingAbortControllers) {
      try {
        if (ctrl && typeof ctrl.abort === 'function' && !ctrl.signal?.aborted) {
          ctrl.abort(ABORT_REASON_FORCE_NEW_CONN)
        }
      } catch (_) {}
    }
    pendingAbortControllers.clear()
  }
  markLastActivity()
}

const handleIdleWakeup = () => {
  if (document.visibilityState !== 'visible') return
  const last = getLastActivity()
  const now = Date.now()
  if (last && now - last >= IDLE_WAKEUP_THRESHOLD_MS) {
    forceMarkIdleWakeup()
  } else {
    markLastActivity()
  }
}

if (typeof document !== 'undefined') {
  try {
    document.addEventListener('visibilitychange', handleIdleWakeup)
    markLastActivity()
  } catch (_) {}
}
if (typeof window !== 'undefined') {
  try {
    window.addEventListener('focus', markLastActivity)
    window.addEventListener('mousedown', markLastActivity, { passive: true })
    window.addEventListener('keydown', markLastActivity, { passive: true })
  } catch (_) {}
}

const isAuthRequest = (url = '') => AUTH_WHITELIST.some((path) => url.includes(path))

// 跳转登录页
const toLogin = (extraQuery = {}) => {
  const currentRoute = router.currentRoute?.value
  const query = {
    ...extraQuery,
  }

  if (currentRoute?.path && currentRoute.path !== '/login') {
    query.redirect = currentRoute.fullPath || '/'
  }

  const navigation = currentRoute?.path === '/login' ? router.replace : router.push
  return navigation({
    path: '/login',
    query,
  })
}

const clearSession = () => {
  const userStore = useUserStore()
  userStore.clear()
}

const notifySessionExpired = () => {
  if (sessionExpiredNotified) return
  sessionExpiredNotified = true
  console.info('[Session] 会话过期，将跳转登录页（提示由登录页显示）')
}

const handleSessionExpired = () => {
  if (redirectingForSessionExpired) return
  redirectingForSessionExpired = true
  clearSession()
  notifySessionExpired()
  const navigateDelay = window.setTimeout(() => {
    Promise.resolve(toLogin({ sessionExpired: '1' }))
      .catch(() => {})
      .finally(() => {
        window.setTimeout(() => {
          redirectingForSessionExpired = false
          window.setTimeout(() => {
            sessionExpiredNotified = false
          }, 800)
        }, 300)
      })
  }, 600)
  try {
    window.addEventListener(
      'beforeunload',
      () => {
        try { window.clearTimeout(navigateDelay) } catch (_) {}
      },
      { once: true },
    )
  } catch (_) {}
}

const refreshAccessToken = async () => {
  const userStore = useUserStore()
  if (!userStore?.token) {
    throw new Error('缺少登录凭证')
  }

  try {
    const response = await service.get('/api/v1/user/refresh_token', {
      headers: {
        Authorization: 'JWT ' + userStore.token,
      },
      silentHttpError: true,
      silentBusinessError: true,
    })

    const refreshedToken = response?.data?.token
    if (!refreshedToken) {
      throw new Error('续期失败')
    }

    userStore.setToken(refreshedToken)
    return refreshedToken
  } catch (refreshErr) {
    const status = refreshErr?.status || refreshErr?.response?.status
    if (status === 401 || status === 422) {
      notifySessionExpired()
    }
    throw refreshErr
  }
}

// 统一错误处理
const errorHandle = (response) => {
  const { status, data } = response
  const msg = data?.message || '请求错误'

  switch (status) {
    case 400:
      // Bad Request
      message.error(msg)
      break
    case 401: {
      const silent = !!response.config?.silentHttpError || !!response.config?.skipGlobalMessageError
      if (!silent) notifySessionExpired()
      break
    }
    case 403: {
      // 首页这类并发计数/统计接口经常用 silentHttpError/skipGlobalMessageError 静默；
      // 另外做 15s 去重，防止并发 5 条 403 直接弹 5 条 "无权限访问"
      const silent = !!response.config?.silentHttpError || !!response.config?.skipGlobalMessageError
      if (!silent) {
        const now = Date.now()
        if (now - last403WarningAt >= 15 * 1000) {
          last403WarningAt = now
          message.warning('无权限访问！')
        }
      }
      break
    }
    case 404:
      message.error('请求的资源不存在!')
      break
    case 422: {
      const silent = !!response.config?.silentHttpError || !!response.config?.skipGlobalMessageError
      if (!silent) notifySessionExpired()
      break
    }
    case 429:
      message.warning('当前访问太过频繁,请稍等再试!')
      break
    case 500:
      message.error('服务端错误,请稍后再试!')
      break
    case 502:
      message.error('网关错误,请稍后再试!')
      break
    case 504:
      message.error('响应超时,请刷新后再试!')
      break
    default:
      message.error(`服务繁忙(${status})`)
  }
}

const DEFAULT_TIMEOUT_MS = 15000
const LONG_RUNNING_URL_PATTERNS = [
  /\/api\/v1\/das\/(execute|query)/i,
  /\/api\/v1\/orders\/(commit|tasks\/result)/i,
  /\/api\/v1\/backup\//i,
]

const resolveTimeout = (config) => {
  if (config?.timeout && config.timeout > 0) return config.timeout
  const url = String(config?.url || '')
  const hit = LONG_RUNNING_URL_PATTERNS.some((p) => p.test(url))
  if (hit) return 600000
  return DEFAULT_TIMEOUT_MS
}

// 创建 axios 实例
const service = axios.create({
  timeout: DEFAULT_TIMEOUT_MS,
  baseURL: '',
})

const DB_CONNECTION_ERROR_PATTERNS = [
  /failed\s+to\s+connect/i,
  /dial\s+tcp/i,
  /connection\s+refused/i,
  /connection\s+reset/i,
  /no\s+route\s+to\s+host/i,
  /i\/o\s+timeout|ETIMEDOUT|timed\s*out/i,
  /pgx\.Connect|the\s+database\s+system\s+is\s+starting\s+up/i,
  /password\s+authentication\s+failed/i,
  /Access\s+denied\s+for\s+user/i,
  /Can't\s+connect\s+to\s+MySQL\s+server/i,
  /Unknown\s+database/i,
  /数据库.*不存在/i,
]
const DB_PERMISSION_ERROR_PATTERNS = [
  /Access\s+denied|permission\s+denied|42501|insufficient\s+privilege/i,
  /无权限|没有权限|权限不足/i,
]

const detectDbErrorCategory = (rawMessage) => {
  const text = String(rawMessage || '')
  if (!text) return { category: 'generic', friendly: '请求失败' }
  const primaryConnHit = DB_CONNECTION_ERROR_PATTERNS.some((p) => p.test(text))
  if (primaryConnHit) {
    return {
      category: 'connection',
      friendly: '连接失败：请检查实例是否启动、网络是否可达，或联系管理员',
    }
  }
  if (DB_PERMISSION_ERROR_PATTERNS.some((p) => p.test(text))) {
    return {
      category: 'permission',
      friendly: '权限不足：当前账号无法访问该资源，请联系管理员授权',
    }
  }
  return { category: 'generic', friendly: text || '请求失败' }
}

const buildFriendlyErrorMessage = (response, rawMessage) => {
  const cfg = response?.config || {}
  if (typeof cfg.errorFriendlyText === 'string' && cfg.errorFriendlyText) {
    return cfg.errorFriendlyText
  }
  const detected = detectDbErrorCategory(rawMessage)
  if (cfg.errorCategory && detected.category && cfg.errorCategory !== detected.category) {
    return detected.friendly
  }
  return detected.friendly
}

const normalizeRequestConfig = (configOrHeaders = {}) => {
  if (!configOrHeaders || typeof configOrHeaders !== 'object') {
    return {}
  }
  const axiosConfigKeys = [
    'headers',
    'signal',
    'timeout',
    'responseType',
    'params',
    'baseURL',
    'withCredentials',
    'silentBusinessError',
    'silentHttpError',
    'skipGlobalMessageError',
    'disableNetRetry',
    'errorCategory',
    'errorFriendlyText',
  ]
  const looksLikeAxiosConfig = axiosConfigKeys.some((key) => key in configOrHeaders)
  if (looksLikeAxiosConfig) {
    return { ...configOrHeaders }
  }
  return {
    headers: { ...configOrHeaders },
  }
}

const canRefreshToken = (response) => {
  const status = response?.status
  const originalRequest = response?.config
  return (
    !!originalRequest &&
    !originalRequest._retry &&
    !originalRequest.skipAuthRefresh &&
    !isAuthRequest(originalRequest.url) &&
    (status === 401 || status === 422)
  )
}

// 请求拦截器
service.interceptors.request.use(
  (config) => {
    const userStore = useUserStore()
    const token = userStore?.token
    if (token) config.headers.Authorization = 'JWT ' + token

    config.timeout = resolveTimeout(config)

    if (forceNewConnRemaining > 0) {
      try {
        forceNewConnRemaining -= 1
        if (config.headers) {
          config.headers['Connection'] = 'close'
          config.headers['X-GI-New-Conn'] = '1'
        } else {
          config.headers = { Connection: 'close', 'X-GI-New-Conn': '1' }
        }
      } catch (_) {}
    }

    if (!config.signal || !config.__acManaged) {
      try {
        const ac = new AbortController()
        if (config.signal) {
          try {
            const upstream = config.signal
            if (typeof upstream.addEventListener === 'function') {
              upstream.addEventListener('abort', () => ac.abort(upstream.reason), { once: true })
            }
          } catch (_) {}
        }
        config.signal = ac.signal
        config.__acManaged = ac
        pendingAbortControllers.add(ac)
      } catch (_) {
        /* AbortController unavailable in very old runtimes: ignore */
      }
    }

    Nprogress.start()
    return config
  },
  (error) => {
    const cfg = error?.config
    const ac = cfg?.__acManaged
    if (ac) {
      pendingAbortControllers.delete(ac)
      delete cfg.__acManaged
    }
    return Promise.reject(error)
  },
)

const isNetworkLevelError = (error) => {
  if (!error) return false
  if (axios.isCancel(error) || error?.code === 'ERR_CANCELED') return false
  if (error?.response) return false
  const code = String(error?.code || '')
  const msg = String(error?.message || '')
  if (/^(ECONNRESET|ECONNREFUSED|ETIMEDOUT|ENOTFOUND|EAI_AGAIN|ERR_NETWORK|ERR_BAD_RESPONSE)$/i.test(code)) return true
  if (/Network Error/i.test(msg)) return true
  if (/timeout.*exceeded|timed?\s*out/i.test(msg)) return true
  return false
}

//  响应拦截器
service.interceptors.response.use(
  (response) => {
    Nprogress.done()
    const ac = response?.config?.__acManaged
    if (ac) {
      pendingAbortControllers.delete(ac)
      delete response.config.__acManaged
    }
    if (response.status === 200) {
      if (response?.data?.code) {
        if (response.data.code === '4001' || response.data.code === '4002') {
          return Promise.resolve(response)
        }
        if (response.data.code !== '0000') {
          const rawMessage = response?.data?.message || '请求失败'
          const detected = detectDbErrorCategory(rawMessage)
          if (response.data && typeof response.data === 'object') {
            try {
              Object.defineProperty(response.data, '__errorCategory', {
                value: detected.category,
                writable: true,
                configurable: true,
                enumerable: false,
              })
              Object.defineProperty(response.data, '__errorRaw', {
                value: rawMessage,
                writable: true,
                configurable: true,
                enumerable: false,
              })
            } catch (e) {
              /* ignore defineProperty failures */
            }
          }
          const skipGlobalToast = !!response.config?.skipGlobalMessageError || !!response.config?.silentBusinessError
          if (!skipGlobalToast) {
            const friendly = buildFriendlyErrorMessage(response, rawMessage)
            message.error(friendly)
            if (typeof console === 'object' && console && typeof console.debug === 'function') {
              try {
                console.debug('[request] business error:', {
                  url: response.config?.url,
                  code: response.data?.code,
                  category: detected.category,
                  raw: rawMessage,
                  friendly,
                })
              } catch (e) {
                /* ignore debug failures */
              }
            }
          }
          if (response.data && typeof response.data === 'object') {
            if (!response.data.category) response.data.category = detected.category
          }
          return Promise.reject(response)
        }
      }
      return Promise.resolve(response)
    } else {
      return Promise.reject(response)
    }
  },
  (error) => {
    Nprogress.done()
    const ac = error?.config?.__acManaged
    if (ac) {
      pendingAbortControllers.delete(ac)
      if (error?.config) delete error.config.__acManaged
    }
    if (axios.isCancel(error) || error?.code === 'ERR_CANCELED') {
      return Promise.reject({
        canceled: true,
        code: 'ERR_CANCELED',
        message: '请求已取消',
      })
    }
    const originalRequest = error?.config
    const { response } = error
    if (!response && isNetworkLevelError(error) && originalRequest && !originalRequest._netRetry && !originalRequest.disableNetRetry) {
      originalRequest._netRetry = true
      try {
        if (originalRequest.headers) {
          originalRequest.headers['X-Force-New-Conn'] = '1'
        }
      } catch (_) {}
      return service(originalRequest).catch((secondErr) => {
        const r2 = secondErr?.response
        if (r2) return Promise.reject(secondErr)
        if (!window.navigator.onLine) {
          message.warn('网络已断开,请检查网络后重试！')
          router.push({ name: 'refresh' }).catch(() => {})
        } else if (!originalRequest?.skipGlobalMessageError) {
          message.error('连接已断开,请刷新页面后重试')
        }
        return Promise.reject(secondErr || error)
      })
    }
    if (response) {
      const isExpired = response.status === 401 || response.status === 422
      const origReq = response.config

      if (isExpired) {
        if (redirectingForSessionExpired) {
          return Promise.reject(response)
        }

        if (refreshTokenPromise && !origReq?._retry && !isAuthRequest(origReq?.url)) {
          return refreshTokenPromise
            .then((token) => {
              try {
                const nextHeaders = { ...(origReq?.headers || {}), Authorization: 'JWT ' + token }
                return service({ ...(origReq || {}), headers: nextHeaders, _retry: true })
              } catch (_) {
                return Promise.reject(response)
              }
            })
            .catch(() => {
              handleSessionExpired()
              return Promise.reject(response)
            })
        }

        if (canRefreshToken(response)) {
          try {
            refreshTokenPromise =
              refreshTokenPromise ||
              refreshAccessToken().finally(() => {
                refreshTokenPromise = null
              })

            return refreshTokenPromise.then((token) => {
              origReq._retry = true
              origReq.headers = {
                ...(origReq.headers || {}),
                Authorization: 'JWT ' + token,
              }
              return service(origReq)
            }).catch(() => {
              handleSessionExpired()
              return Promise.reject(response)
            })
          } catch {
            handleSessionExpired()
            return Promise.reject(response)
          }
        }

        if (!isAuthRequest(origReq?.url)) {
          handleSessionExpired()
        }
        return Promise.reject(response)
      }

      if (!response.config?.silentHttpError) {
        if (response.config?.skipGlobalMessageError) {
          // skip global toast, fallback category only
        } else {
          errorHandle(response)
        }
      }
      return Promise.reject(response)
    }
    if (!window.navigator.onLine) {
      message.warn('网络已断开,请检查网络后重试！')
      router.push({ name: 'refresh' }).catch(() => {})
    } else {
      if (!error?.config?.skipGlobalMessageError) {
        message.error('系统异常')
      }
      return Promise.reject(error)
    }
  },
)

export const get = (url, params, config = {}) => {
  return new Promise((resolve, reject) => {
    service
      .get(url, {
        params: params,
        ...normalizeRequestConfig(config),
      })
      .then((res) => {
        resolve(res.data)
      })
      .catch((err) => {
        reject(err?.data ?? err)
      })
  })
}

export const getBlob = (url, params = {}, config = {}) => {
  return service.get(url, {
    params,
    responseType: 'blob',
    ...config,
  })
}

export function post(url, params, headers = {}) {
  return new Promise((resolve, reject) => {
    service
      .post(url, params, normalizeRequestConfig(headers))
      .then((res) => {
        resolve(res.data)
      })
      .catch((err) => {
        reject(err?.data ?? err)
      })
  })
}

export function del(url, params, config = {}) {
  return new Promise((resolve, reject) => {
    service
      .delete(url, { ...config, data: params })
      .then((res) => {
        resolve(res.data)
      })
      .catch((err) => {
        reject(err?.data ?? err)
      })
  })
}
export function put(url, params, headers = {}) {
  return new Promise((resolve, reject) => {
    service
      .put(url, params, normalizeRequestConfig(headers))
      .then((res) => {
        resolve(res.data)
      })
      .catch((err) => {
        reject(err?.data ?? err)
      })
  })
}

export default service
