/**
 * 应用入口瞬间的原始 URL query。
 *
 * 为什么需要它：permission.js 首次跳转用的是 `{ path: to.fullPath }`，而 vue-router 对「对象形式」
 * 的 location 只认 path 字段，query 会被丢掉；等路由守卫跑完，`route.query` 与 `location.search`
 * 里都已经没有它了（冷启动直链 `/docs?doc=xxx` 会静默落到默认内容）。
 * 所以要在任何导航发生前先抓一份 —— 本模块在 main.js 顶部以副作用方式引入，只求值一次。
 *
 * 用法：`getEntryParam('doc')` 做冷启动深链兜底；SPA 内导航请直接用 `route.query`，那里的值才是活的。
 */
const params = (() => {
  try {
    return new URLSearchParams(window.location.search)
  } catch {
    return new URLSearchParams()
  }
})()

export const getEntryParam = (key) => params.get(key) || ''
