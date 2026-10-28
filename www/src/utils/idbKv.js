const DB_NAME = 'goinsight_das_drafts'
const DB_VERSION = 1
const STORE_NAME = 'kv'

let dbPromise = null

const openDb = () => {
  if (dbPromise) return dbPromise
  dbPromise = new Promise((resolve, reject) => {
    if (!('indexedDB' in window)) {
      reject(new Error('indexedDB_not_supported'))
      return
    }
    const request = window.indexedDB.open(DB_NAME, DB_VERSION)
    request.onupgradeneeded = (event) => {
      const db = event.target.result
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME, { keyPath: 'k' })
      }
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error || new Error('indexedDB_open_failed'))
  })
  return dbPromise
}

export const idbGet = async (key) => {
  const db = await openDb()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readonly')
    const store = tx.objectStore(STORE_NAME)
    const req = store.get(key)
    req.onsuccess = () => resolve(req.result?.v ?? null)
    req.onerror = () => reject(req.error || new Error('indexedDB_get_failed'))
  })
}

export const idbSet = async (key, value) => {
  const db = await openDb()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite')
    const store = tx.objectStore(STORE_NAME)
    const req = store.put({ k: key, v: String(value ?? '') })
    req.onsuccess = () => resolve(true)
    req.onerror = () => reject(req.error || new Error('indexedDB_set_failed'))
  })
}

export const idbDel = async (key) => {
  const db = await openDb()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite')
    const store = tx.objectStore(STORE_NAME)
    const req = store.delete(key)
    req.onsuccess = () => resolve(true)
    req.onerror = () => reject(req.error || new Error('indexedDB_delete_failed'))
  })
}

