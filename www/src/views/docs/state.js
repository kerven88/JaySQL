import { ref } from 'vue'

/**
 * 文档中心的跨实例状态。
 *
 * 为什么必须放模块作用域：`Layout.vue` 里 `<component :is="Component" :key="renderedRouteKey" />`，
 * 而 `renderedRouteKey = route.fullPath + seed` —— 只要 query 变了（切换文档就是 `?doc=` 变化），
 * 组件实例会被整体销毁重建，写在 `index.vue` setup 里的 ref 全部归零。
 * 侧边栏的展开状态、当前选中的文档、待跳转的锚点都必须活过这次重建，所以挂在这里。
 */

/** 当前选中的文档 slug */
export const docsCurrentSlug = ref('')

/** 侧边栏已展开的文档 slug（默认空 = 全部收起，只显示一级目录） */
export const docsOpenSlugs = ref(new Set())

/**
 * 已展开的小节（key = `${slug}#${锚点id}`，带 slug 避免不同文档的同名小节串状态）。
 * 语义：不在这个集合里的小节一律**收起**——一级目录展开后只列 h2 标题，
 * 带 h3 子节的小节由用户点 caret 主动展开，避免长文档一进来就铺满侧栏。
 */
export const docsExpandedSections = ref(new Set())

/** 切换文档后要滚动到的锚点（重建完成后由新实例消费） */
export const docsPendingAnchor = ref('')
