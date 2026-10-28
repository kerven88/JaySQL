import { post } from '@/utils/request'

const DEFAULT_RISK_TYPES = [
  'instance_conn',
  'manual_backup_fail',
  'restore_fail',
  'node_offline',
  'instance_no_backup',
  'daily_backup_miss',
  'order_fail',
  'todo_order_submit',
  'todo_order_approve',
  'todo_order_execute',
  'todo_backup_enable',
]

export const HOME_WORKBENCH_EVENT = 'gi-home-workbench-change'

export const getWorkbenchUsername = () => {
  try {
    const { useUserStore } = require('@/store/user')
    const store = typeof useUserStore === 'function' ? useUserStore() : null
    const u = store?.username || store?.user?.username || store?.profile?.username || ''
    return String(u || '').trim()
  } catch (_) {
    return ''
  }
}

let dismissedDataCache = null
let dismissedDataExpireAt = 0
let loadDismissedPromise = null

const CACHE_TTL_MS = 8 * 1000

const nowMs = () => Date.now()

const postRiskDismissedApi = (action, payload, config = {}) => {
  const url = `/api/v1/home/risk-dismissed/${action}`
  return post(url, payload, config)
}

export const invalidateDismissedCache = () => {
  dismissedDataCache = null
  dismissedDataExpireAt = 0
  loadDismissedPromise = null
  try {
    window.dispatchEvent(new Event(HOME_WORKBENCH_EVENT))
  } catch (_) {
    /* noop */
  }
}

export const loadAllDismissedKeys = async (riskTypes, { force = false, silentError = true } = {}) => {
  const types = Array.isArray(riskTypes) && riskTypes.length > 0
    ? riskTypes.filter(Boolean)
    : DEFAULT_RISK_TYPES
  if (!force && dismissedDataCache && dismissedDataExpireAt > nowMs()) {
    return dismissedDataCache
  }
  if (loadDismissedPromise && !force) {
    return loadDismissedPromise
  }
  loadDismissedPromise = (async () => {
    try {
      const res = await postRiskDismissedApi('batch', { risk_types: types }, {
        skipGlobalMessageError: !!silentError,
        silentBusinessError: !!silentError,
        silentHttpError: !!silentError,
      })
      const raw = (res && typeof res === 'object' && res.data && typeof res.data === 'object')
        ? res.data
        : ((res && typeof res === 'object') ? res : {})
      const sanitized = {}
      if (raw && typeof raw === 'object') {
        for (const k of Object.keys(raw)) {
          const v = raw?.[k]
          if (v && typeof v === 'object') {
            if (Array.isArray(v)) {
              sanitized[k] = new Set(v.map((x) => String(x || '')))
            } else if (v instanceof Set) {
              sanitized[k] = new Set(Array.from(v).map((x) => String(x || '')))
            } else {
              sanitized[k] = new Set(Object.keys(v).map((x) => String(x || '')))
            }
          } else {
            sanitized[k] = new Set()
          }
        }
      }
      dismissedDataCache = sanitized
      dismissedDataExpireAt = nowMs() + CACHE_TTL_MS
      return sanitized
    } finally {
      loadDismissedPromise = null
    }
  })()
  try {
    return await loadDismissedPromise
  } catch (err) {
    loadDismissedPromise = null
    dismissedDataCache = dismissedDataCache || {}
    return dismissedDataCache
  }
}

const getOrEnsureCache = async (riskType, { force = false } = {}) => {
  const cache = await loadAllDismissedKeys([riskType], { force })
  if (!cache[riskType]) {
    cache[riskType] = new Set()
  }
  return cache
}

export const getDismissedKeys = async (username, type) => {
  const cache = await getOrEnsureCache(type)
  return new Set(cache[type] || [])
}

export const getDismissedCount = async (username, type) => {
  const set = await getDismissedKeys(username, type)
  return set.size
}

export const isDismissed = async (username, type, key) => {
  const set = await getDismissedKeys(username, type)
  return set.has(String(key || ''))
}

export const getDismissedKeysSync = (username, type) => {
  const data = dismissedDataCache && dismissedDataCache[type]
  if (data && data instanceof Set) {
    return data
  }
  if (data && Array.isArray(data)) {
    return new Set(data.map((x) => String(x || '')))
  }
  if (data && typeof data === 'object') {
    return new Set(Object.keys(data).map((x) => String(x || '')))
  }
  return new Set()
}

export const getDismissedCountSync = (username, type) => getDismissedKeysSync(username, type).size

export const isDismissedSync = (username, type, key) => {
  const set = getDismissedKeysSync(username, type)
  const k = String(key || '')
  if (set.has(k)) return true
  if (type === 'manual_backup_fail') {
    const parts = k.split(':')
    if (parts.length === 3) {
      const legacy = `${parts[0]}:${parts[1]}`
      if (set.has(legacy)) return true
    } else if (parts.length === 2) {
      const prefix = `${k}:`
      for (const x of set) {
        if (String(x).startsWith(prefix)) return true
      }
    }
  }
  return false
}

export const dismissItem = async (username, type, key, remark = '') => {
  const riskType = String(type || '').trim()
  const riskKey = String(key || '').trim()
  if (!riskType || !riskKey) return
  try {
    await postRiskDismissedApi('dismiss', { risk_type: riskType, risk_key: riskKey, remark: String(remark || '') })
  } finally {
    try {
      const cache = dismissedDataCache || {}
      if (!cache[riskType]) {
        cache[riskType] = new Set()
      } else if (!(cache[riskType] instanceof Set)) {
        cache[riskType] = new Set(Array.isArray(cache[riskType]) ? cache[riskType] : Object.keys(cache[riskType] || {}))
      }
      cache[riskType].add(riskKey)
      dismissedDataCache = cache
    } catch (_) {
      /* ignore local-cache add failure */
    }
    try {
      window.dispatchEvent(new Event(HOME_WORKBENCH_EVENT))
    } catch (_) {
      /* ignore */
    }
  }
}

export const undismissItem = async (username, type, key) => {
  const riskType = String(type || '').trim()
  const riskKey = String(key || '').trim()
  if (!riskType || !riskKey) return
  try {
    await postRiskDismissedApi('undismiss', { risk_type: riskType, risk_key: riskKey })
  } finally {
    try {
      const cache = dismissedDataCache || {}
      if (cache[riskType] instanceof Set) {
        cache[riskType].delete(riskKey)
      }
    } catch (_) {
      /* noop */
    }
    try {
      window.dispatchEvent(new Event(HOME_WORKBENCH_EVENT))
    } catch (_) {
      /* noop */
    }
  }
}

export const buildRiskKey = (type, record, context) => {
  const t = String(type || '')
  const r = record || {}
  if (t === 'instance_conn') {
    return `instance_conn:${r.id}`
  }
  if (t === 'manual_backup_fail') {
    const tid = String(r.id ?? '').trim()
    if (r.last_run_id != null && r.last_run_id !== '') {
      return `manual_backup_fail:${tid}:${r.last_run_id}`
    }
    return `manual_backup_fail:${tid}`
  }
  if (t === 'restore_fail') {
    return `restore_fail:${r.id}`
  }
  if (t === 'node_offline') {
    return `node_offline:${r.id}`
  }
  if (t === 'instance_no_backup') {
    return `instance_no_backup:${r.id}`
  }
  if (t === 'daily_backup_miss') {
    const day = context?.day ? String(context.day) : ''
    return `daily_backup_miss:${r.id}:${day}`
  }
  if (t === 'order_fail') {
    return `order_fail:${r.order_id || r.id || ''}`
  }
  if (t === 'todo_backup_enable') {
    return `todo_backup_enable:${r.id}`
  }
  if (t === 'todo_order_submit' || t === 'todo_order_approve' || t === 'todo_order_execute') {
    return `${t}:${r.order_id || r.id || ''}`
  }
  return `${t}:${r.id || r.order_id || ''}`
}
