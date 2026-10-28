const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

const isNetError = (err) => {
  if (!err) return false
  if (typeof err === 'object') {
    const msg = (err.message || String(err)).toString()
    return (
      /Failed to fetch/i.test(msg) ||
      /TypeError:\s*Failed\s+to\s+fetch/i.test(msg) ||
      /Loading\s+Chunk\s+failed/i.test(msg) ||
      /ERR_CONNECTION/i.test(msg) ||
      /NetworkError/i.test(msg) ||
      /Loading CSS chunk/i.test(msg) ||
      /Loading chunk/i.test(msg) ||
      /error loading dynamically imported module/i.test(msg) ||
      /dynamically imported module/i.test(msg)
    )
  }
  return false
}

/**
 * 路由懒加载包装器：给动态 import 的 chunk 增加 3 次指数退避重试
 *  - 重试仅在"网络级失败（连接断开 / 加载失败 / 动态导入模块失败）"时触发，对于超时/断连问题
 */
export const lazyImport = (importFn, options = {}) => {
  const maxRetries = options.maxRetries != null ? options.maxRetries : 3
  const baseDelay = options.baseDelay || 300

  return async () => {
    let lastError = null
    for (let attempt = 0; attempt <= maxRetries; attempt++) {
      try {
        return await importFn()
      } catch (err) {
        lastError = err
        const retryable = attempt < maxRetries && isNetError(err)
        if (!retryable) {
          throw err
        }
        // 指数退避：300ms → 600ms → 1200ms
        const delay = baseDelay * 2 ** attempt
        await sleep(delay)
        continue
      }
    }
    throw lastError
  }
}

export default lazyImport
