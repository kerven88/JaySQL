<template>
  <div class="docs-page">
    <header class="docs-header">
      <div class="docs-header-text">
        <h1 class="docs-title">
          <ReadOutlined class="docs-title-icon" />
          <span>文档中心</span>
        </h1>
      </div>
      <a-input-search
        v-model:value="keyword"
        class="docs-search"
        placeholder="搜索标题、摘要、正文或关键词"
        allow-clear
      />
    </header>

    <div class="docs-body">
      <aside v-if="!isNarrow" class="docs-side">
        <!-- 两级目录：一级是文档（默认收起），展开后是它的 h2/h3 小节 -->
        <nav class="docs-nav">
          <div
            v-for="doc in filteredDocs"
            :key="doc.slug"
            class="docs-nav-node"
            :class="{ 'is-open': isOpen(doc.slug) }"
          >
            <div class="docs-nav-row">
              <button
                type="button"
                class="docs-nav-caret"
                :aria-label="isOpen(doc.slug) ? '收起目录' : '展开目录'"
                @click="toggleNode(doc.slug)"
              >
                <RightOutlined class="docs-nav-caret-icon" />
              </button>
              <button
                type="button"
                class="docs-nav-item"
                :class="{ 'is-active': doc.slug === currentSlug }"
                @click="onDocClick(doc.slug)"
              >
                <span class="docs-nav-text">{{ doc.title }}</span>
              </button>
            </div>

            <ul v-if="isOpen(doc.slug)" class="docs-nav-sub">
              <li v-for="item in outlineTrees[doc.slug]" :key="item.id">
                <div class="docs-nav-row docs-nav-row-sub">
                  <button
                    type="button"
                    class="docs-nav-caret docs-nav-caret-sub"
                    :class="{ 'is-empty': !item.children.length }"
                    :disabled="!item.children.length"
                    :aria-hidden="!item.children.length ? 'true' : undefined"
                    :aria-label="item.children.length ? (isSectionOpen(doc.slug, item.id) ? '收起小节' : '展开小节') : undefined"
                    @click="toggleSection(doc.slug, item.id)"
                  >
                    <RightOutlined
                      class="docs-nav-caret-icon"
                      :class="{ 'is-expanded': !!item.children.length && isSectionOpen(doc.slug, item.id) }"
                    />
                  </button>
                  <button
                    type="button"
                    class="docs-nav-subitem"
                    :class="{ 'is-active': isSectionActive(doc, item) }"
                    :title="item.text"
                    @click="selectDoc(doc.slug, item.id)"
                  >
                    {{ item.text }}
                  </button>
                </div>
                <ul
                  v-if="item.children.length && isSectionOpen(doc.slug, item.id)"
                  class="docs-nav-sub docs-nav-sub-nested"
                >
                  <li v-for="child in item.children" :key="child.id">
                    <button
                      type="button"
                      class="docs-nav-subitem is-nested"
                      :class="{ 'is-active': doc.slug === currentSlug && child.id === activeAnchorId }"
                      :title="child.text"
                      @click="selectDoc(doc.slug, child.id)"
                    >
                      {{ child.text }}
                    </button>
                  </li>
                </ul>
              </li>
            </ul>
          </div>
          <p v-if="!filteredDocs.length" class="docs-nav-empty">没有匹配的文档</p>
        </nav>
      </aside>

      <section ref="mainRef" class="docs-main">
        <nav v-if="isNarrow" class="docs-chips">
          <button
            v-for="doc in filteredDocs"
            :key="doc.slug"
            type="button"
            class="docs-chip"
            :class="{ 'is-active': doc.slug === currentSlug }"
            @click="selectDoc(doc.slug)"
          >
            {{ doc.title }}
          </button>
        </nav>

        <article v-if="currentDoc" ref="cardRef" class="docs-card">
          <div class="docs-article-head">
            <h2 class="docs-article-title">{{ currentDoc.title }}</h2>
            <div class="docs-meta">
              <span>
                <CalendarOutlined />
                {{ currentDoc.updated }}
              </span>
              <span>
                <ClockCircleOutlined />
                约 {{ currentDoc.reading }}
              </span>
              <span class="docs-tags">
                <a-tag v-for="tag in currentDoc.tags" :key="tag">{{ tag }}</a-tag>
              </span>
            </div>
          </div>

          <!-- 正文由 www/scripts/build-docs.mjs 在构建期从 docs/manual/**/*.md 转成 HTML 内联进来 -->
          <div class="docs-content" @click="onContentClick">
            <div v-if="currentDoc.html" class="docs-markdown" v-html="currentDoc.html"></div>
            <a-empty v-else description="该文档暂无正文" />
          </div>

          <footer class="docs-foot">
            <span class="docs-foot-source">
              源文件：<code>docs/manual/{{ currentDoc.slug }}.md</code>
            </span>
            <div class="docs-foot-nav">
              <a-button v-if="prevDoc" size="small" @click="selectDoc(prevDoc.slug)">
                <LeftOutlined />
                {{ prevDoc.title }}
              </a-button>
              <a-button v-if="nextDoc" size="small" @click="selectDoc(nextDoc.slug)">
                {{ nextDoc.title }}
                <RightOutlined />
              </a-button>
            </div>
          </footer>
        </article>

        <a-empty v-else class="docs-empty" description="没有可显示的文档，换个关键词试试" />
      </section>
    </div>
  </div>
</template>

<script setup>
defineOptions({ name: 'DocsCenter' })

import manifest from '@/views/docs/manifest.json'
import { docsCurrentSlug, docsExpandedSections, docsOpenSlugs, docsPendingAnchor } from './state'
import { getEntryParam } from '@/utils/entryQuery'
import { useWindowSize } from '@vueuse/core'
import {
  CalendarOutlined,
  ClockCircleOutlined,
  LeftOutlined,
  ReadOutlined,
  RightOutlined,
} from '@ant-design/icons-vue'
import {
  computed,
  inject,
  nextTick,
  onActivated,
  onBeforeUnmount,
  onDeactivated,
  onMounted,
  ref,
  watch,
} from 'vue'
import { useRoute, useRouter } from 'vue-router'

// 元数据与正文都来自构建产物（docs/manual/**/*.md → manifest.json），运行时零接口依赖
const docsIndex = manifest.docs
const findDocBySlug = (slug) => docsIndex.find((doc) => doc.slug === slug) || null

const NARROW_BREAKPOINT = 1024 // 与 components/layout/layoutConfig.js 的 mobileBreakpoint 对齐

const route = useRoute()
const router = useRouter()
const { width } = useWindowSize()

const isNarrow = computed(() => width.value < NARROW_BREAKPOINT)

const keyword = ref('')
const matchKeyword = (doc, kw) => {
  if (!kw) return true
  const haystack = [doc.title, doc.desc, doc.text, ...(doc.keywords || []), ...(doc.tags || [])]
    .join(' ')
    .toLowerCase()
  return haystack.includes(kw)
}

const filteredDocs = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  return docsIndex.filter((doc) => matchKeyword(doc, kw))
})

// 独家 key，仅用于「记住上次读到的文档」，不做业务索引
const LAST_DOC_KEY = '__gi_docs_last_doc__'

const readStoredSlug = () => {
  try {
    return localStorage.getItem(LAST_DOC_KEY) || ''
  } catch {
    return ''
  }
}

const resolveInitialSlug = () => {
  // 冷启动直链兜底：守卫重导航后 route.query 会丢，用入口处预留的原始 query 顶上
  const fromQuery = typeof route.query.doc === 'string' ? route.query.doc : getEntryParam('doc')
  return findDocBySlug(fromQuery)?.slug || findDocBySlug(readStoredSlug())?.slug || docsIndex[0]?.slug || ''
}

const cardRef = ref(null)
const mainRef = ref(null)
// 这两个状态挂模块作用域（见 ./state.js）：切文档会让 router-view 重建组件，本地 ref 会丢
const currentSlug = docsCurrentSlug
if (!currentSlug.value) currentSlug.value = resolveInitialSlug()
const currentDoc = computed(() => findDocBySlug(currentSlug.value) || filteredDocs.value[0] || null)
/** 把每篇文档的 h2/h3 组装成两级树（h3 挂到它上面的 h2），供侧边栏逐级展开 */
const buildOutlineTree = (doc) => {
  const roots = []
  let current = null
  for (const item of doc?.anchors || []) {
    if (item.level === 2 || !current) {
      // 孤立的 h3 也兜住，不至于丢条目
      current = { id: item.id, text: item.text, level: item.level, children: [] }
      roots.push(current)
    } else {
      current.children.push({ id: item.id, text: item.text, level: item.level, children: [] })
    }
  }
  return roots
}

const outlineTrees = computed(() => {
  const map = {}
  for (const doc of docsIndex) map[doc.slug] = buildOutlineTree(doc)
  return map
})

// 侧边栏展开状态：默认全部收起只显示一级目录；换文档不重置，记住用户展开过的节点
const openSlugs = docsOpenSlugs
const isOpen = (slug) => openSlugs.value.has(slug)
const toggleNode = (slug) => {
  const next = new Set(openSlugs.value)
  if (next.has(slug)) next.delete(slug)
  else next.add(slug)
  openSlugs.value = next
}
const openNode = (slug) => {
  if (openSlugs.value.has(slug)) return
  const next = new Set(openSlugs.value)
  next.add(slug)
  openSlugs.value = next
}

// 小节级收起/展开：key 带 slug，避免不同文档的同名小节互相影响。
// 默认**收起**（只列 h2 标题），Set 里存「被用户展开的小节」，点 caret 纯切换。
const expandedSections = docsExpandedSections
const sectionKey = (slug, id) => `${slug}#${id}`
const isSectionOpen = (slug, id) => expandedSections.value.has(sectionKey(slug, id))
const toggleSection = (slug, id) => {
  const key = sectionKey(slug, id)
  const next = new Set(expandedSections.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expandedSections.value = next
}
/** 小节自身或它的任一子节是当前锚点：收起状态下父级也要能高亮，否则用户看不出读到哪。
    注意这里在函数体内，ref 不会自动解包，必须显式 .value。 */
const isSectionActive = (doc, item) =>
  doc.slug === currentSlug.value
  && (item.id === activeAnchorId.value || (item.children || []).some((c) => c.id === activeAnchorId.value))

/** 页面滚动容器是 .layout-content，不一定在 window 上，先从元素往上找 */
const findScroller = (el) => {
  let node = el?.parentElement
  while (node) {
    const { overflowY } = window.getComputedStyle(node)
    if (/(auto|scroll|overlay)/.test(overflowY) && node.scrollHeight > node.clientHeight + 1) return node
    node = node.parentElement
  }
  return document.scrollingElement || document.documentElement
}

/** 滚到正文锚点；不传 id 就回到正文顶部（切换文档时用） */
const scrollToAnchor = (id) => {
  const card = cardRef.value
  if (!card) return
  const target = id ? card.querySelector(`#${CSS.escape(id)}`) : card
  if (!target) return
  const scroller = findScroller(card)
  const isDoc = scroller === document.scrollingElement || scroller === document.documentElement
  const originTop = isDoc ? 0 : scroller.getBoundingClientRect().top
  const base = isDoc ? window.scrollY : scroller.scrollTop
  const top = Math.max(target.getBoundingClientRect().top - originTop + base - (id ? 12 : 8), 0)
  scroller.scrollTo({ top, behavior: 'smooth' })
}

// 当前阅读到的小节，由正文滚动反向同步，用于侧边栏高亮
const activeAnchorId = ref('')
let anchorTicking = false

const syncActiveAnchor = () => {
  const main = mainRef.value
  const anchors = currentDoc.value?.anchors || []
  if (!main || !anchors.length) {
    activeAnchorId.value = ''
    return
  }
  const line = main.getBoundingClientRect().top + 96
  let hit = anchors[0].id
  for (const item of anchors) {
    const el = main.querySelector(`[id="${CSS.escape(item.id)}"]`)
    if (!el) continue
    if (el.getBoundingClientRect().top - line <= 0) hit = item.id
    else break
  }
  activeAnchorId.value = hit
}

const onMainScroll = () => {
  if (anchorTicking) return
  anchorTicking = true
  requestAnimationFrame(() => {
    anchorTicking = false
    syncActiveAnchor()
  })
}

const selectDoc = (slug, anchor) => {
  if (!slug) return
  const sameDoc = slug === currentSlug.value
  if (!sameDoc) {
    currentSlug.value = slug
    router.replace({ name: 'docs', query: { doc: slug } })
  }
  if (anchor) activeAnchorId.value = anchor
  nextTick(() => {
    // 换文档会触发 router-view 重建，本实例的滚动会随卸载作废，锚点交给重建后的 onMounted 补做
    if (sameDoc) {
      scrollToAnchor(anchor || null)
      syncActiveAnchor()
    } else if (anchor) {
      docsPendingAnchor.value = anchor
    }
  })
}

/** 点文档标题：选中并把它的小节展开，方便直接接着读 */
const onDocClick = (slug) => {
  openNode(slug)
  selectDoc(slug)
}

/** 正文里的链接：站内文档走路由内切换，同页锚点走平滑滚动，站外链接交给浏览器 */
const onContentClick = (event) => {
  const el = event.target instanceof Element ? event.target.closest('a') : null
  if (!el) return
  if (el.dataset.docPlain) {
    event.preventDefault()
    return
  }
  if (el.dataset.docSlug) {
    event.preventDefault()
    selectDoc(el.dataset.docSlug, el.dataset.docAnchor)
    return
  }
  const href = el.getAttribute('href') || ''
  if (href.startsWith('#') && href.length > 1) {
    event.preventDefault()
    scrollToAnchor(decodeURIComponent(href.slice(1)))
  }
}

// 支持 /docs?doc=<slug> 直达；动态路由注册完毕后 vue-router 会重解析一次，
// 冷启动直链时 query 可能被规范化掉，这里在挂载后按当前选中补写回去，保证「刷新还在这一篇」。
watch(
  () => route.query.doc,
  (doc) => {
    if (typeof doc !== 'string' || !doc) return
    if (findDocBySlug(doc)) currentSlug.value = doc
  },
)

watch(currentSlug, (slug) => {
  try {
    localStorage.setItem(LAST_DOC_KEY, slug)
  } catch {
    /* localStorage 不可用时忽略，不影响阅读 */
  }
})

// 满高布局：让外层 layout-content 放弃滚动，改由右侧正文区自己滚（见第 4 点）
const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)
const applyNoScroll = (value) => {
  if (typeof setLayoutContentNoScroll === 'function') setLayoutContentNoScroll(value)
}

onMounted(() => {
  applyNoScroll(true)
  // 正文区滚动 → 反向同步侧边栏小节高亮
  mainRef.value?.addEventListener('scroll', onMainScroll, { passive: true })
  if (!route.query.doc && currentSlug.value) {
    router.replace({ name: 'docs', query: { doc: currentSlug.value } })
  }
  nextTick(() => {
    if (docsPendingAnchor.value) {
      scrollToAnchor(docsPendingAnchor.value)
      docsPendingAnchor.value = ''
    }
    syncActiveAnchor()
  })
})

// 路由 keepAlive 打开，切回来要重新接管滚动容器并刷新高亮
onActivated(() => {
  applyNoScroll(true)
  syncActiveAnchor()
})
onDeactivated(() => applyNoScroll(false))
onBeforeUnmount(() => {
  applyNoScroll(false)
  mainRef.value?.removeEventListener('scroll', onMainScroll)
})

const flatDocs = computed(() => filteredDocs.value)
const currentPosition = computed(() => flatDocs.value.findIndex((doc) => doc.slug === currentDoc.value?.slug))
const prevDoc = computed(() => (currentPosition.value > 0 ? flatDocs.value[currentPosition.value - 1] : null))
const nextDoc = computed(() =>
  currentPosition.value >= 0 && currentPosition.value < flatDocs.value.length - 1
    ? flatDocs.value[currentPosition.value + 1]
    : null,
)
</script>

<style scoped>
/* 满高布局：外层 layout-content 已关掉滚动（setLayoutContentNoScroll），
   页面自身撑满剩余高度，滚动只发生在右侧正文区 */
.docs-page {
  display: flex;
  flex-direction: column;
  gap: var(--gi-spacing-md);
  height: 100%;
  min-height: 0;
}

.docs-header {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--gi-spacing-md);
  flex-wrap: wrap;
}

.docs-title {
  display: flex;
  align-items: center;
  gap: var(--gi-spacing-sm);
  margin: 0;
  font-size: var(--gi-font-size-hero);
  font-weight: 600;
  color: var(--gi-color-text-primary);
}

.docs-title-icon {
  color: var(--gi-color-primary);
  font-size: 22px;
}

.docs-search {
  width: 280px;
  max-width: 100%;
}

.docs-body {
  display: flex;
  align-items: stretch;
  gap: var(--gi-spacing-md);
  flex: 1 1 auto;
  min-height: 0;
}

/* 目录固定不滚：内容撑不满时无滚动条，仅视口极矮时兜底 */
.docs-side {
  flex: 0 0 232px;
  overflow-y: auto;
  scrollbar-width: thin;
  padding: var(--gi-spacing-sm);
  border: 1px solid var(--gi-color-border);
  border-radius: var(--gi-radius-card);
  background: var(--gi-color-container-bg);
  box-shadow: var(--gi-shadow-sm);
}

.docs-nav-row {
  display: flex;
  align-items: center;
  gap: 2px;
}

.docs-nav-caret {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 24px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--gi-color-text-tertiary);
  cursor: pointer;
}

.docs-nav-caret:hover {
  color: var(--gi-color-primary);
}

.docs-nav-caret-icon {
  font-size: 10px;
  transition: transform var(--gi-duration-fast) ease;
}

/* 一级文档节点的 caret：只旋转它自己那一个。
   必须写成「直接子级链」——原来写成 .docs-nav-node.is-open .docs-nav-caret-icon
   会穿透命中展开区里所有后代 caret（含二级小节），且特异性 3 个 class 高于
   二级自带的规则，会把二级图标一起钉死在 90°。 */
.docs-nav-node.is-open > .docs-nav-row > .docs-nav-caret .docs-nav-caret-icon {
  transform: rotate(90deg);
}

.docs-nav-item {
  flex: 1 1 auto;
  min-width: 0;
  display: block;
  padding: 7px var(--gi-spacing-sm);
  border: none;
  border-radius: var(--gi-radius-md);
  background: transparent;
  color: var(--gi-color-text-secondary);
  font-size: var(--gi-font-size-body);
  text-align: left;
  cursor: pointer;
  transition: background var(--gi-duration-fast) ease, color var(--gi-duration-fast) ease;
}

.docs-nav-item:hover {
  background: rgba(15, 118, 110, 0.06);
  color: var(--gi-color-primary);
}

.docs-nav-item.is-active {
  background: rgba(15, 118, 110, 0.12);
  color: var(--gi-color-primary);
  font-weight: 600;
}

.docs-nav-text {
  display: block;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

/* 二级/三级小节：缩进层级靠 padding-left 递进，字号递减 */
.docs-nav-sub {
  margin: 2px 0 4px;
  padding: 0 0 0 26px;
  list-style: none;
}

.docs-nav-sub li {
  list-style: none;
}

/* 三级小节：相对二级**文字**再缩进一级（净 20px）+ 一条极淡的层级引导线，
   让「这组 h3 属于上面那个 h2」一眼可见。
   注意二级行左侧有 18px 的 caret，三级行没有，所以这里要把这 18px 一并补回来，
   否则三级文字会几乎贴住二级文字（只差 7px，看不出层级）。 */
.docs-nav-sub-nested {
  margin: 1px 0 3px 9px;
  padding-left: 28px;
  border-left: 1px solid rgba(15, 118, 110, 0.14);
}

.docs-nav-subitem {
  display: block;
  width: 100%;
  padding: 4px 6px;
  border: none;
  border-radius: var(--gi-radius-sm);
  background: transparent;
  color: var(--gi-color-text-secondary);
  font-size: 13px;
  line-height: 1.5;
  text-align: left;
  cursor: pointer;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  transition: background var(--gi-duration-fast) ease, color var(--gi-duration-fast) ease;
}

.docs-nav-subitem.is-nested {
  font-size: 12.5px;
  color: var(--gi-color-text-tertiary);
}

.docs-nav-subitem:hover {
  color: var(--gi-color-primary);
}

.docs-nav-subitem.is-active {
  background: rgba(15, 118, 110, 0.1);
  color: var(--gi-color-primary);
  font-weight: 600;
}

/* h2 小节行：caret + 标题；无子级的小节 caret 占位隐藏，保证各标题左缘对齐 */
.docs-nav-row-sub {
  display: flex;
  align-items: center;
  gap: 2px;
}

.docs-nav-caret-sub {
  width: 16px;
  height: 22px;
}

.docs-nav-caret-sub.is-empty {
  visibility: hidden;
  pointer-events: none;
}

.docs-nav-row-sub .docs-nav-subitem {
  flex: 1 1 auto;
  min-width: 0;
}

/* 二级小节的 caret：默认指向右（收起），展开时转 90°。
   状态类刻意用 is-expanded 而不是一级的 is-open：两者作用对象不同
   （一级看祖先节点、二级看自身图标），同名会让人误以为是同一套状态。 */
.docs-nav-row-sub .docs-nav-caret-icon {
  transform: rotate(0deg);
}

.docs-nav-row-sub .docs-nav-caret-icon.is-expanded {
  transform: rotate(90deg);
}

.docs-nav-empty,
.docs-empty {
  padding: var(--gi-spacing-lg) 0;
  margin: 0;
  text-align: center;
  font-size: var(--gi-font-size-body);
  color: var(--gi-color-text-tertiary);
}

/* 唯一的滚动容器：滚动条落在正文区右侧，而不是浏览器窗口最右侧 */
.docs-main {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
  scrollbar-width: thin;
  padding-right: 4px;
}

.docs-chips {
  position: sticky;
  top: 0;
  z-index: 1;
  display: flex;
  gap: var(--gi-spacing-sm);
  margin-bottom: var(--gi-spacing-sm);
  padding: var(--gi-spacing-xs) 0;
  background: var(--gi-color-page-bg);
  overflow-x: auto;
  scrollbar-width: thin;
}

.docs-chip {
  flex: 0 0 auto;
  padding: 5px var(--gi-spacing-sm);
  border: 1px solid var(--gi-color-border);
  border-radius: 999px;
  background: var(--gi-color-container-bg);
  color: var(--gi-color-text-secondary);
  font-size: var(--gi-font-size-body);
  white-space: nowrap;
  cursor: pointer;
  transition: background var(--gi-duration-fast) ease, border-color var(--gi-duration-fast) ease,
    color var(--gi-duration-fast) ease;
}

.docs-chip:hover {
  border-color: var(--gi-color-primary);
  color: var(--gi-color-primary);
}

.docs-chip.is-active {
  border-color: var(--gi-color-primary);
  background: var(--gi-color-primary);
  color: #ffffff;
  font-weight: 600;
}

.docs-card {
  padding: var(--gi-spacing-lg);
  border: 1px solid var(--gi-color-border);
  border-radius: var(--gi-radius-card);
  background: var(--gi-color-container-bg);
  box-shadow: var(--gi-shadow-sm);
}

.docs-article-head {
  padding-bottom: var(--gi-spacing-md);
  border-bottom: 1px solid var(--gi-color-border);
}

.docs-article-title {
  margin: 0;
  font-size: var(--gi-font-size-title);
  font-weight: 600;
  color: var(--gi-color-text-primary);
}

.docs-meta {
  display: flex;
  align-items: center;
  gap: var(--gi-spacing-md);
  flex-wrap: wrap;
  margin-top: var(--gi-spacing-sm);
  font-size: var(--gi-font-size-caption);
  color: var(--gi-color-text-tertiary);
}

.docs-meta > span {
  display: inline-flex;
  align-items: center;
  gap: var(--gi-spacing-xs);
}

.docs-tags {
  gap: var(--gi-spacing-xs);
}

/* ---------- 正文排版（构建期产物，无 scoped 属性，统一走 :deep） ---------- */
/* 顶部间距：删掉「本章小节」后，正文直接接在 meta 之后，
   沿用 --gi-spacing-md，避免出现双倍留白或内容贴脸 */
.docs-content {
  margin-top: var(--gi-spacing-md);
  font-size: var(--gi-font-size-body);
  line-height: 1.8;
  color: var(--gi-color-text-primary);
  word-break: break-word;
}

.docs-content :deep(> *:first-child) {
  margin-top: 0;
}

.docs-content :deep(h2) {
  margin: var(--gi-spacing-xl) 0 var(--gi-spacing-sm);
  padding-bottom: var(--gi-spacing-xs);
  border-bottom: 1px solid var(--gi-color-border);
  font-size: var(--gi-font-size-title);
  font-weight: 600;
  color: var(--gi-color-text-primary);
}

.docs-content :deep(h3) {
  margin: var(--gi-spacing-lg) 0 var(--gi-spacing-xs);
  font-size: var(--gi-font-size-body-lg);
  font-weight: 600;
  color: var(--gi-color-text-primary);
}

.docs-content :deep(p) {
  margin: var(--gi-spacing-sm) 0;
}

.docs-content :deep(strong) {
  font-weight: 600;
  color: var(--gi-color-text-primary);
}

.docs-content :deep(a) {
  color: var(--gi-color-primary);
  text-decoration: none;
}

.docs-content :deep(a:hover) {
  text-decoration: underline;
}

/* 文档导航之外的站内链接（如 ../README.md）：按纯文本呈现，不给可点错觉 */
.docs-content :deep(a[data-doc-plain]) {
  color: inherit;
  cursor: text;
}

.docs-content :deep(a[data-doc-plain]:hover) {
  text-decoration: none;
}

.docs-content :deep(ul),
.docs-content :deep(ol) {
  margin: var(--gi-spacing-sm) 0;
  padding-left: 22px;
}

/* antd reset.css 给 ul/ol/li 都钉了 list-style: none，且 li 自身的值会盖掉继承，
   所以列表容器和 li 都要显式补回，否则正文的圆点/序号全丢 */
.docs-content :deep(ul) {
  list-style: disc;
}

.docs-content :deep(ol) {
  list-style: decimal;
}

.docs-content :deep(li) {
  margin: 2px 0;
  list-style: inherit;
}

.docs-content :deep(ul ul) {
  list-style: circle;
}

.docs-content :deep(ol ol),
.docs-content :deep(ul ol) {
  list-style: lower-alpha;
}

.docs-content :deep(code) {
  padding: 1px 5px;
  border-radius: var(--gi-radius-sm);
  background: rgba(15, 23, 32, 0.06);
  font-size: 0.92em;
}

.docs-content :deep(pre) {
  margin: var(--gi-spacing-md) 0;
  padding: var(--gi-spacing-md);
  border-radius: var(--gi-radius-md);
  background: #282c34; /* 与 main.js 引入的 atom-one-dark 主题同底色 */
  color: #abb2bf;
  font-size: 12.5px;
  line-height: 1.65;
  overflow-x: auto;
}

.docs-content :deep(pre code) {
  padding: 0;
  background: transparent;
  color: inherit;
  font-size: inherit;
}

.docs-content :deep(.doc-table-wrap) {
  margin: var(--gi-spacing-md) 0;
  overflow-x: auto;
}

.docs-content :deep(table) {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--gi-font-size-body);
}

.docs-content :deep(th),
.docs-content :deep(td) {
  padding: 8px 12px;
  border: 1px solid var(--gi-color-border);
  text-align: left;
  vertical-align: top;
}

.docs-content :deep(thead th) {
  background: rgba(15, 23, 32, 0.03);
  font-weight: 600;
  color: var(--gi-color-text-primary);
  white-space: nowrap;
}

.docs-content :deep(img) {
  max-width: 100%;
  height: auto;
  margin: var(--gi-spacing-md) 0;
  border: 1px solid var(--gi-color-border);
  border-radius: var(--gi-radius-md);
}

.docs-content :deep(.doc-callout) {
  margin: var(--gi-spacing-md) 0;
  padding: var(--gi-spacing-sm) var(--gi-spacing-md);
  border: 1px solid transparent;
  border-left-width: 3px;
  border-radius: var(--gi-radius-md);
}

.docs-content :deep(.doc-callout > *:first-child) {
  margin-top: 0;
}

.docs-content :deep(.doc-callout > *:last-child) {
  margin-bottom: 0;
}

.docs-content :deep(.doc-callout-title) {
  margin: 0 0 var(--gi-spacing-xs);
  font-weight: 600;
}

.docs-content :deep(.doc-callout--info) {
  border-color: rgba(22, 119, 255, 0.25);
  border-left-color: var(--gi-color-info);
  background: rgba(22, 119, 255, 0.06);
}

.docs-content :deep(.doc-callout--info .doc-callout-title) {
  color: var(--gi-color-info);
}

.docs-content :deep(.doc-callout--tip) {
  border-color: rgba(15, 118, 110, 0.25);
  border-left-color: var(--gi-color-primary);
  background: rgba(15, 118, 110, 0.06);
}

.docs-content :deep(.doc-callout--tip .doc-callout-title) {
  color: var(--gi-color-primary);
}

.docs-content :deep(.doc-callout--warn) {
  border-color: rgba(250, 173, 20, 0.3);
  border-left-color: var(--gi-color-warning);
  background: rgba(250, 173, 20, 0.08);
}

.docs-content :deep(.doc-callout--warn .doc-callout-title) {
  color: #ad6800;
}

.docs-content :deep(.doc-callout--danger) {
  border-color: rgba(255, 77, 79, 0.25);
  border-left-color: var(--gi-color-error);
  background: rgba(255, 77, 79, 0.06);
}

.docs-content :deep(.doc-callout--danger .doc-callout-title) {
  color: var(--gi-color-error);
}

.docs-content :deep(.doc-shot) {
  margin: var(--gi-spacing-md) 0;
  padding: var(--gi-spacing-lg) var(--gi-spacing-md);
  border: 1px dashed var(--gi-color-border);
  border-radius: var(--gi-radius-md);
  background: rgba(15, 23, 32, 0.02);
  text-align: center;
  color: var(--gi-color-text-tertiary);
}

.docs-content :deep(.doc-shot figcaption) {
  font-size: var(--gi-font-size-caption);
}

.docs-content :deep(.doc-shot-badge) {
  display: inline-block;
  margin-bottom: var(--gi-spacing-xs);
  padding: 1px 8px;
  border-radius: 999px;
  background: rgba(15, 118, 110, 0.1);
  color: var(--gi-color-primary);
  font-size: var(--gi-font-size-caption);
}

.docs-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--gi-spacing-md);
  flex-wrap: wrap;
  margin-top: var(--gi-spacing-lg);
  padding-top: var(--gi-spacing-md);
  border-top: 1px solid var(--gi-color-border);
  font-size: var(--gi-font-size-caption);
  color: var(--gi-color-text-tertiary);
}

.docs-foot-nav {
  display: flex;
  gap: var(--gi-spacing-sm);
  flex-wrap: wrap;
}

.docs-foot code {
  padding: 1px 5px;
  border-radius: var(--gi-radius-sm);
  background: rgba(15, 23, 32, 0.06);
  font-size: var(--gi-font-size-caption);
}

@media (max-width: 1023px) {
  .docs-header {
    align-items: stretch;
    flex-direction: column;
  }

  .docs-search {
    width: 100%;
  }
}

@media (max-width: 767px) {
  .docs-card {
    padding: var(--gi-spacing-md);
  }

  .docs-foot {
    flex-direction: column;
    align-items: flex-start;
  }
}

@media (max-width: 768px) {
  .docs-card {
    padding: var(--gi-spacing-md);
  }

  .docs-foot {
    flex-direction: column;
    align-items: flex-start;
  }

  .docs-nav-caret {
    width: 28px;
    height: 32px;
  }

  .docs-nav-subitem {
    font-size: 14px;
    padding: 8px 6px;
  }
}
</style>
