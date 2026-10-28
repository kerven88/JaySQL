import { defineStore } from 'pinia'
import {
  GetObjectsFlatIndexApi,
  SearchObjectsApi,
  ReportObjectSearchAuditApi,
} from '@/api/das'

const DEFAULT_MAX_ROWS = 20000
const DEFAULT_SEARCH_LIMIT = 200

const createEmptyEntry = () => ({
  loaded: false,
  tooLarge: false,
  loading: null,
  totalCount: 0,
  items: [],
  error: null,
  abortController: null,
  keywordCache: new Map(),
})

const normalizeKeyword = (kw) => String(kw || '').trim().toLowerCase()

const objectMatchesKeyword = (obj, keywordLower) => {
  if (!keywordLower) return false
  const dbName = String(obj.database_name || '').toLowerCase()
  const scName = String(obj.schema_name || '').toLowerCase()
  const tbName = String(obj.table_name || '').toLowerCase()
  const dispName = String(obj.display_name || '').toLowerCase()
  return (
    (dbName && dbName.includes(keywordLower)) ||
    (scName && scName.includes(keywordLower)) ||
    (tbName && tbName.includes(keywordLower)) ||
    (dispName && dispName !== dbName && dispName !== scName && dispName !== tbName && dispName.includes(keywordLower))
  )
}

export const useDasObjectSearchStore = defineStore('dasObjectSearch', {
  state: () => ({
    cache: Object.create(null),
    _auditDedup: new Map(),
  }),

  getters: {
    isReady: (state) => (instanceId) => {
      const id = String(instanceId || '')
      if (!id) return false
      const entry = state.cache[id]
      return !!(entry && (entry.loaded || entry.tooLarge))
    },
    mode: (state) => (instanceId) => {
      const id = String(instanceId || '')
      if (!id) return 'disabled'
      const entry = state.cache[id]
      if (!entry) return 'loading'
      if (entry.loading) return 'loading'
      if (entry.error) return 'error'
      if (entry.tooLarge) return 'server'
      if (entry.loaded) return 'client'
      return 'loading'
    },
  },

  actions: {
    _getEntry(instanceId) {
      const id = String(instanceId || '')
      if (!id) return null
      if (!this.cache[id]) this.cache[id] = createEmptyEntry()
      return this.cache[id]
    },

    invalidate(instanceId = null) {
      if (!instanceId) {
        Object.values(this.cache).forEach((e) => {
          e.abortController?.abort?.()
        })
        this.cache = Object.create(null)
        return
      }
      const id = String(instanceId)
      const e = this.cache[id]
      if (e) {
        e.abortController?.abort?.()
        this.cache[id] = createEmptyEntry()
      }
    },

    async ensureIndex(instanceId, opts = {}) {
      const { force = false, maxRows = DEFAULT_MAX_ROWS } = opts
      const id = String(instanceId || '')
      if (!id) return null
      const entry = this._getEntry(id)
      if (!force && (entry.loaded || entry.tooLarge) && !entry.error) {
        return entry
      }
      if (!force && entry.loading) {
        return entry.loading
      }
      const prevAbort = entry.abortController
      prevAbort?.abort?.()
      const ac = new AbortController()
      entry.abortController = ac
      entry.error = null
      entry.keywordCache = new Map()

      const runPromise = (async () => {
        try {
          const res = await GetObjectsFlatIndexApi(
            {
              instance_id: id,
              max_rows: maxRows,
            },
            { signal: ac.signal }
          )
          const payload = res?.data || { too_large: false, total_count: 0, items: [] }
          entry.tooLarge = !!payload.too_large
          entry.totalCount = Number(payload.total_count) || 0
          entry.items = entry.tooLarge ? [] : Array.isArray(payload.items) ? payload.items : []
          entry.loaded = true
          entry.loading = null
          entry.abortController = null
          return entry
        } catch (err) {
          if (err?.name === 'CanceledError' || err?.name === 'AbortError') {
            entry.loading = null
            return entry
          }
          entry.error = err?.message || 'unknown error'
          entry.loaded = false
          entry.loading = null
          entry.abortController = null
          throw err
        }
      })()

      entry.loading = runPromise
      return runPromise
    },

    searchClient(instanceId, keyword, opts = {}) {
      const limit = Number(opts.limit) || DEFAULT_SEARCH_LIMIT
      const id = String(instanceId || '')
      const kw = normalizeKeyword(keyword)
      const empty = { total: 0, items: [] }
      if (!id) return empty
      const entry = this._getEntry(id)
      if (kw.length < 2) return empty
      if (entry.tooLarge) return empty
      if (entry.keywordCache.has(kw)) {
        const cached = entry.keywordCache.get(kw)
        if (limit >= cached.items.length) return cached
        return { total: cached.total, items: cached.items.slice(0, limit) }
      }
      if (!entry.loaded) return empty
      const hits = []
      let total = 0
      for (let i = 0; i < entry.items.length; i++) {
        const item = entry.items[i]
        if (objectMatchesKeyword(item, kw)) {
          total++
          if (hits.length < limit) hits.push(item)
        }
      }
      const result = { total, items: hits }
      entry.keywordCache.set(kw, { total, items: hits.slice() })
      return result
    },

    async searchServer(instanceId, keyword, opts = {}) {
      const limit = Number(opts.limit) || DEFAULT_SEARCH_LIMIT
      const id = String(instanceId || '')
      const kw = String(keyword || '').trim()
      const empty = { total: 0, items: [] }
      if (!id || kw.length < 2) return empty
      const ac = (opts && opts.signal) ? null : new AbortController()
      try {
        const res = await SearchObjectsApi(
          { instance_id: id, q: kw, limit },
          { signal: (opts && opts.signal) || (ac && ac.signal) }
        )
        const data = res?.data || empty
        return {
          total: Number(data.total) || 0,
          items: Array.isArray(data.items) ? data.items : [],
        }
      } catch (err) {
        if (err?.name === 'CanceledError' || err?.name === 'AbortError') return empty
        throw err
      } finally {
        ac && ac.abort && ac.abort()
      }
    },

    reportAudit(instanceId, keyword) {
      const id = String(instanceId || '')
      const kw = normalizeKeyword(keyword)
      if (!id || kw.length < 4) return
      const key = `${id}::${kw}`
      const now = Date.now()
      const last = this._auditDedup.get(key) || 0
      if (now - last < 3000) return
      this._auditDedup.set(key, now)
      ReportObjectSearchAuditApi({ instance_id: id, keyword: kw.slice(0, 64) }).catch(() => {})
    },
  },
})
