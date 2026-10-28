<template>
  <!--
    页面公告条：挂在 Layout 的 header 下方，独立占一行。
    - 水平居中、宽度占页面中间 50%，两侧留白；
    - 单条文字不论长短都在自己的可视区内横向循环滚动，多条时逐条轮播；
    - 关闭图标紧跟文字右侧，只关当前这一条，其余继续按原规则播；
    - 无公告 / 全部被关时整块不渲染，不留空白占位。
  -->
  <div
    v-if="visible"
    ref="rootRef"
    class="gi-page-announcement"
    role="region"
    aria-label="系统公告"
    @mouseenter="pause"
    @mouseleave="resume"
  >
    <div class="gi-page-announcement__inner">
      <div ref="viewportRef" class="gi-page-announcement__viewport">
        <div ref="trackRef" class="gi-page-announcement__track">
          <!--
            轨道里放两份同样的内容 + 一份 48px 间隔：
            平移「一份 + 间隔」正好回到起点，无限循环起来中间不会出现空屏。
            第二份对读屏软件隐藏，避免同一条公告被念两遍。
          -->
          <template v-for="n in 2" :key="n">
            <span
              :ref="(el) => setCopyRef(el, n - 1)"
              class="gi-page-announcement__copy"
              :aria-hidden="n === 2 ? 'true' : undefined"
            >
              <span class="gi-page-announcement__badge">【系统公告】</span>
              <a
                v-if="current.link"
                class="gi-page-announcement__text gi-page-announcement__text--link"
                :href="current.link"
                target="_blank"
                rel="noopener noreferrer"
                :title="hoverText"
                @click.stop
              >{{ displayText }}</a>
              <span v-else class="gi-page-announcement__text" :title="hoverText">{{ displayText }}</span>
            </span>
            <span v-if="n === 1" class="gi-page-announcement__gap" aria-hidden="true"></span>
          </template>
        </div>
      </div>

      <!-- 关闭：只作用于当前正在显示的这一条 -->
      <button
        type="button"
        class="gi-page-announcement__close"
        title="本次登录不再展示"
        aria-label="关闭当前公告"
        @click="closeCurrent"
      >
        <CloseOutlined />
      </button>
    </div>
  </div>
</template>

<script setup>
defineOptions({ name: 'PageAnnouncementBar' })

import { CloseOutlined } from '@ant-design/icons-vue'
import { getActiveAnnouncementsApi } from '@/api/announcement'
import {
  closeFingerprint,
  markAnnouncementClosed,
  readClosedSet,
} from '@/components/announcement/announcementStorage'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

// page-announcement.js —— 页面公告条
//
// 五条硬口径：
//   1. 生效判定完全交给后端（enabled + 生效时间窗），前端只按「会话内关闭状态」过滤；
//   2. 宽度固定 50% 居中：空间不足时被裁的是公告本身，绝不挤压两侧布局；
//   3. 滚动速度与每条停留时长来自后台【公告管理 → 展示设置】，前端不写死；
//   4. 关闭是会话级的，且只关当前条；关掉后剩余条目继续轮播（见 announcementStorage.js）；
//   5. 无公告 / 全被关 → 整块不渲染，不留空白占位。

const emit = defineEmits(['height-change'])

// 后端没配过时的兜底值，与后端 models.DefaultSettings() 保持一致
const FALLBACK_SPEED = 90
const FALLBACK_DWELL = 3000

// 两份内容之间的间隔，必须与 CSS 里 .gi-page-announcement__gap 的宽度一致，
// 否则平移量算错，循环接缝会跳一下
const GAP_PX = 48
// 一轮滚动的时长下限：内容再短也不至于一闪而过
const MIN_SCROLL_MS = 1500

const rootRef = ref(null)
const viewportRef = ref(null)
const trackRef = ref(null)
// 轨道第一份内容的 DOM：拿它测「单条公告的真实宽度」
const copyRef = ref(null)

const items = ref([])
const settings = ref({})
const currentIndex = ref(0)
// sessionStorage 不参与响应式，关闭后用它显式驱动 playList 重算
const closedVersion = ref(0)

let animation = null
let paused = false
let hovered = false
let pendingAdvance = false
let timer = null
let resizeObserver = null
let selfObserver = null

// ---------- 数据 ----------
const speed = computed(() => {
  const v = Number(settings.value.scroll_speed)
  return v > 0 ? v : FALLBACK_SPEED
})

const dwellMs = computed(() => {
  const v = Number(settings.value.dwell_ms)
  return v > 0 ? v : FALLBACK_DWELL
})

// 轮播列表：后端返回的生效公告，剔除本会话已关闭的版本
const playList = computed(() => {
  void closedVersion.value
  const closed = readClosedSet()
  return items.value.filter((it) => {
    const text = (it?.content || '').trim()
    return text && !closed.has(closeFingerprint(it.id, it.updated_at))
  })
})

const visible = computed(() => playList.value.length > 0)
const current = computed(() => playList.value[currentIndex.value] || {})
const displayText = computed(() => (current.value.content || '').replace(/\s+/g, ' ').trim())
const hoverText = computed(() => {
  const title = current.value.title || ''
  const content = current.value.content || ''
  return title ? `${title}：${content}` : content
})

// ---------- 播放 ----------
function clearPlayback() {
  if (animation) {
    animation.cancel()
    animation = null
  }
  if (timer) {
    clearTimeout(timer)
    timer = null
  }
  paused = false
  pendingAdvance = false
}

// 轨道里两份内容，只需要第一份的引用来测宽
function setCopyRef(el, index) {
  if (index === 0) copyRef.value = el
}

async function playCurrent() {
  clearPlayback()
  await nextTick()
  const viewport = viewportRef.value
  const track = trackRef.value
  const copy = copyRef.value
  if (!viewport || !track || !copy || !visible.value) return

  // 可视区宽度跟着「单条内容宽度」走：内容短时收到内容宽，关闭图标才贴得住文字；
  // 内容超宽时由 flex 收缩到可用空间，超出的部分照旧被裁掉。
  const copyWidth = copy.offsetWidth
  viewport.style.maxWidth = copyWidth > 0 ? `${copyWidth}px` : ''
  const viewportWidth = viewport.clientWidth
  if (!viewportWidth || !copyWidth) {
    scheduleAdvance(dwellMs.value)
    return
  }

  // 不看内容长不长，一律滚动：轨道是两份内容首尾相接，
  // 平移「一份 + 间隔」后画面和起点完全一样，所以可以无缝无限循环。
  const unit = copyWidth + GAP_PX
  const duration = Math.max(MIN_SCROLL_MS, Math.round((unit / speed.value) * 1000))
  animation = track.animate(
    [
      { transform: 'translateX(0)' },
      { transform: `translateX(${-unit}px)` },
    ],
    { duration, easing: 'linear', iterations: Infinity },
  )
  // 滚完一轮再按后台配置的停留时长继续展示，之后才切下一条
  scheduleAdvance(duration + dwellMs.value)
}

function scheduleAdvance(delayMs) {
  const delay = Number(delayMs) > 0 ? Number(delayMs) : dwellMs.value
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => {
    timer = null
    // 悬停期间不切条，等移出后再补上（否则鼠标一放上去条就跳了）
    if (hovered) {
      pendingAdvance = true
      return
    }
    advance()
  }, delay)
}

function advance() {
  const total = playList.value.length
  if (!total) return
  currentIndex.value = (currentIndex.value + 1) % total
  nextTick(() => {
    if (!hovered) playCurrent()
  })
}

function pause() {
  hovered = true
  if (animation && animation.playState === 'running') {
    animation.pause()
    paused = true
  }
}

function resume() {
  hovered = false
  if (paused && animation) {
    animation.play()
    paused = false
    return
  }
  // 停留在两条之间的间隙时被悬停打断过，移出后补切
  if (pendingAdvance) {
    pendingAdvance = false
    advance()
  }
}

// ---------- 关闭 ----------
function closeCurrent() {
  const item = current.value
  if (!item || !item.id) return
  markAnnouncementClosed(item.id, item.updated_at)
  closedVersion.value += 1
}

// 关掉当前条后：修正下标，剩余条目继续播。
// 点关闭按钮时鼠标必然还悬停在公告条上（hovered=true），若沿用「悬停就不播」，
// 新条目会一直停在旧画面上不出现。所以这里无条件重启播放，再补一次 pause ——
// 新内容立刻可见，同时仍然尊重「悬停时不滚」。
watch(
  () => playList.value.length,
  (len) => {
    if (currentIndex.value >= len) currentIndex.value = 0
    nextTick(async () => {
      await playCurrent()
      if (hovered) pause()
    })
  },
)

// ---------- 生命周期 ----------
onMounted(async () => {
  // 公告是辅助信息，拉取失败静默降级为「不展示」，不能挡住登录后的主流程
  const res = await getActiveAnnouncementsApi().catch(() => null)
  const data = res?.data || {}
  items.value = Array.isArray(data.items) ? data.items : []
  settings.value = data.settings || {}
  await playCurrent()

  // 可视区宽度变化（窗口缩放、侧边栏折叠）直接影响「滚多远」
  if (viewportRef.value && typeof ResizeObserver !== 'undefined') {
    // observe 会立刻回调一次，首帧已经起过播，跳过它避免动画被无谓重启
    let skipFirst = true
    resizeObserver = new ResizeObserver(() => {
      if (skipFirst) {
        skipFirst = false
        return
      }
      if (!hovered) playCurrent()
    })
    resizeObserver.observe(viewportRef.value)
  }

  // 把自身实际高度告诉 Layout：公告条在 flex 流里占位，
  // 内容区的 min-height 需要减掉它，否则页面会多出一条多余滚动。
  if (rootRef.value && typeof ResizeObserver !== 'undefined') {
    selfObserver = new ResizeObserver(() => {
      emit('height-change', rootRef.value?.offsetHeight || 0)
    })
    selfObserver.observe(rootRef.value)
  }
})

// 可见性切换（首次加载出公告 / 全部关完）同步高度
watch(visible, async () => {
  await nextTick()
  emit('height-change', rootRef.value?.offsetHeight || 0)
})

onBeforeUnmount(() => {
  clearPlayback()
  if (resizeObserver) {
    resizeObserver.disconnect()
    resizeObserver = null
  }
  if (selfObserver) {
    selfObserver.disconnect()
    selfObserver = null
  }
  emit('height-change', 0)
})
</script>

<style scoped>
/* 配色走系统令牌：--gi-chip-brand-bg 是项目里既有的「品牌浅底」，
   亮/暗色模式都有对应值，不需要再写一套。 */
.gi-page-announcement {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 36px;
  padding: 6px var(--gi-spacing-md);
  background: var(--gi-chip-brand-bg, #e7f6f4);
  border-bottom: 1px solid var(--gi-color-border, #d9e1e6);
  overflow: hidden;
}

/* 宽度只占页面中间 50%，两侧留白由父级的 justify-content:center 完成 */
.gi-page-announcement__inner {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 50%;
  max-width: 100%;
  min-width: 0;
  justify-content: center;
}

/* flex: 0 1 auto 是「关闭图标紧跟文字」的关键：
   可视区按内容宽度取，放不下才收缩并裁切；文字短时它和内容一样宽，
   关闭按钮自然贴在文字后面，不会跑到 50% 区域的最右端。 */
.gi-page-announcement__viewport {
  flex: 0 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
  overflow: hidden;
}

/* 轨道不设 gap：间距交给 __copy 内部和 __gap，
   这样「平移一份 + 间隔」的距离计算才干净 */
.gi-page-announcement__track {
  display: inline-flex;
  align-items: center;
  white-space: nowrap;
  will-change: transform;
}

.gi-page-announcement__copy {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

/* 两份内容之间的留白，宽度必须和脚本里的 GAP_PX 一致 */
.gi-page-announcement__gap {
  flex: 0 0 48px;
  width: 48px;
}

.gi-page-announcement__badge {
  flex: 0 0 auto;
  display: inline-block;
  padding: 0 6px;
  border-radius: var(--gi-radius-sm, 6px);
  background: var(--gi-color-primary, #0f766e);
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  line-height: 18px;
  letter-spacing: 0.5px;
}

.gi-page-announcement__text {
  color: var(--gi-color-text-primary, rgba(15, 23, 32, 0.9));
  font-size: 13px;
  line-height: 1.5;
  text-decoration: none;
}

.gi-page-announcement__text--link {
  color: var(--gi-color-primary, #0f766e);
  font-weight: 500;
  cursor: pointer;
  border-bottom: 1px solid transparent;
}

.gi-page-announcement__text--link:hover {
  border-bottom-color: var(--gi-color-primary, #0f766e);
}

.gi-page-announcement__close {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  border-radius: var(--gi-radius-sm, 6px);
  background: transparent;
  color: var(--gi-color-text-tertiary, rgba(79, 100, 112, 0.8));
  font-size: 12px;
  cursor: pointer;
  transition: color var(--gi-duration-fast, 120ms) ease, background var(--gi-duration-fast, 120ms) ease;
}

.gi-page-announcement__close:hover {
  color: var(--gi-color-primary, #0f766e);
  background: rgba(15, 118, 110, 0.1);
}

/* 窄屏：50% 会窄到读不成句，放宽到 92% */
@media (max-width: 992px) {
  .gi-page-announcement__inner {
    width: 92%;
  }
}

@media (max-width: 768px) {
  .gi-page-announcement__inner {
    width: calc(100% - 24px);
  }

  /* 关闭按钮原本 20x20，移动端容易点空 */
  .gi-page-announcement__close {
    width: 32px;
    height: 32px;
  }
}
</style>
