<template>
  <div class="split-wrapper" :class="{ collapsed: isCollapsed }">
    <!-- 左侧收起后显示拖拽把手，可拖回展开 -->
    <div v-show="isCollapsed" class="collapsed-handle" @mousedown="startDrag">
      <i></i><i></i>
      <button
        v-if="showToggleButton"
        type="button"
        class="split-toggle-button split-toggle-button--collapsed"
        :aria-label="expandAriaLabel"
        :title="expandAriaLabel"
        @mousedown.stop
        @click.stop="expandPanel"
      >
        <RightOutlined />
      </button>
    </div>

    <div v-show="!isCollapsed" class="scalable" :style="{ width: normalizeWidth(leftWidthPx) }">
      <div class="left-content">
        <slot name="left-content"> 左边内容区 </slot>
      </div>
      <div ref="separator" class="separator" @mousedown="startDrag">
        <i></i><i></i>
        <button
          v-if="showToggleButton"
          type="button"
          class="split-toggle-button split-toggle-button--expanded"
          :aria-label="collapseAriaLabel"
          :title="collapseAriaLabel"
          @mousedown.stop
          @click.stop="collapsePanel"
        >
          <LeftOutlined />
        </button>
      </div>
    </div>
    <div class="right-content">
      <slot name="right-content"> 右边内容区 </slot>
    </div>
  </div>
</template>

<script>
export default {
  name: 'SplitPanel',
}
</script>

<script setup>
import { LeftOutlined, RightOutlined } from '@ant-design/icons-vue'
import { throttle } from 'lodash-es'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps({
  leftWidth: {
    type: String,
    default: '',
  },
  // 可拖到最左侧收起
  collapsible: {
    type: Boolean,
    default: true,
  },
  // 左侧最小宽度（未收起状态）；允许为 0，表示可一直缩放到 0
  minLeftWidth: {
    type: Number,
    default: 260,
  },
  // 左侧最大宽度（未收起状态；实际取 min(maxLeftWidth, 50vw)）
  maxLeftWidth: {
    type: Number,
    default: 600,
  },
  // 左侧默认宽度（px 固定值）。> 0 时优先于 defaultWidthRatioOfViewport：
  // 用于「侧边栏宽度与屏幕大小无关」的场景，窗口缩放时保持不变。
  // 用户手动拖拽过之后，仍按视口比例记忆并恢复响应式伸缩。
  defaultLeftWidth: {
    type: Number,
    default: 0,
  },
  // 默认宽度占视口宽度的比例（当没有持久化用户拖拽值时使用）
  defaultWidthRatioOfViewport: {
    type: Number,
    default: 0.22,
  },
  // 拖拽到 <= 该阈值则自动收起。0 也是合法值（表示只有拖到 0 才收起），
  // 传负数或非数字才回退到默认 60。
  collapseThreshold: {
    type: Number,
    default: 60,
  },
  storageKey: {
    type: String,
    default: '',
  },
  // 用户未主动拖拽过时，自动按屏幕宽度响应式伸缩
  autoResponsive: {
    type: Boolean,
    default: true,
  },
  showToggleButton: {
    type: Boolean,
    default: false,
  },
  collapseAriaLabel: {
    type: String,
    default: '收起侧边栏',
  },
  expandAriaLabel: {
    type: String,
    default: '展开侧边栏',
  },
})

const DEFAULT_MIN_LEFT_WIDTH = 260
const DEFAULT_MAX_LEFT_WIDTH_RATIO = 0.5
const DEFAULT_COLLAPSE_THRESHOLD = 60

/* 0 是合法取值（最小值可下调到 0），因此这里一律不能用 `|| 常量` 兜底，
   否则传 0 会被当成「未设置」而回退到默认值。统一走下面的解析器。 */
const resolveMinWidth = () => {
  const n = Number(props.minLeftWidth)
  return Number.isFinite(n) && n >= 0 ? n : DEFAULT_MIN_LEFT_WIDTH
}

const resolveMaxWidth = () => {
  const n = Number(props.maxLeftWidth)
  return Number.isFinite(n) && n > 0 ? n : 600
}

const resolveCollapseThreshold = () => {
  const n = Number(props.collapseThreshold)
  return Number.isFinite(n) && n >= 0 ? n : DEFAULT_COLLAPSE_THRESHOLD
}

/** 固定默认宽度（px）；未配置（<=0）时返回 0，交由视口比例兜底。 */
const resolveDefaultFixedWidth = () => {
  const n = Number(props.defaultLeftWidth)
  return Number.isFinite(n) && n > 0 ? n : 0
}

const hasWindow = typeof window !== 'undefined'

/** 宽度必须带单位：calcResponsiveWidth/widthFromRatio 历史上返回的是纯数字字符串
    （如 "280"），CSS 里 `width: 280` 是非法值，浏览器会整条丢弃 style，
    结果侧边栏宽度退化成内容自然宽度（实测 313px），默认宽度永远不生效。 */
const normalizeWidth = (value) => {
  const raw = String(value ?? '').trim()
  if (!raw) return '0px'
  if (/px$/i.test(raw)) return raw
  const num = Number.parseFloat(raw)
  return `${Number.isFinite(num) ? num : 0}px`
}

const getHardMaxWidth = () => (hasWindow ? Math.floor(window.innerWidth * DEFAULT_MAX_LEFT_WIDTH_RATIO) : Infinity)

const clampWidth = (value, minW, maxW) => {
  const hardMax = getHardMaxWidth()
  const effectiveMax = Math.min(Number.isFinite(maxW) ? maxW : Infinity, hardMax)
  const effectiveMin = Math.min(minW, effectiveMax)
  return Math.min(Math.max(effectiveMin, value || 0), effectiveMax)
}

const calcResponsiveWidth = () => {
  if (!hasWindow) return normalizeWidth(resolveMinWidth())
  // 固定默认宽度优先：与屏幕宽度无关，1420px 屏和 1920px 屏都是同一个值
  const fixed = resolveDefaultFixedWidth()
  if (fixed > 0) return normalizeWidth(clampWidth(fixed, resolveMinWidth(), resolveMaxWidth()))
  const ratio = Number(props.defaultWidthRatioOfViewport) || 0.22
  const ideal = Math.round(window.innerWidth * Math.min(Math.max(ratio, 0.05), 0.8))
  return normalizeWidth(clampWidth(ideal, resolveMinWidth(), resolveMaxWidth()))
}

const computedLeftWidth = computed(() => props.leftWidth || calcResponsiveWidth())
const getDefaultExpandedWidth = () => computedLeftWidth.value

const safeRatio = (r) => Math.min(0.8, Math.max(0.05, Number.isFinite(r) ? r : (Number(props.defaultWidthRatioOfViewport) || 0.22)))

const persistedState = {
  value: null,
  ratioOfViewport: Number(props.defaultWidthRatioOfViewport) || 0.22,
  ratioDirty: true,
}

const widthFromRatio = () => {
  // 用户尚未拖拽过（ratioDirty）：固定默认宽度优先，窗口缩放时也保持不变，
  // 避免「没拖过却因为换显示器/缩放窗口导致侧边栏宽度跳变」。
  const fixed = resolveDefaultFixedWidth()
  if (persistedState.ratioDirty && fixed > 0) {
    return normalizeWidth(clampWidth(fixed, resolveMinWidth(), resolveMaxWidth()))
  }
  if (!hasWindow) return normalizeWidth(resolveMinWidth())
  const ratio = safeRatio(persistedState.ratioOfViewport)
  const ideal = Math.round(window.innerWidth * ratio)
  return normalizeWidth(clampWidth(ideal, resolveMinWidth(), resolveMaxWidth()))
}

const ratioFromWidth = (widthPx) => {
  if (!hasWindow || !window.innerWidth) return Number(props.defaultWidthRatioOfViewport) || 0.22
  const w = parseInt(widthPx, 10)
  if (!Number.isFinite(w) || w <= 0) return Number(props.defaultWidthRatioOfViewport) || 0.22
  return safeRatio(w / window.innerWidth)
}

const readPersistedState = () => {
  if (!hasWindow || !props.storageKey) return null
  try {
    const raw = window.localStorage.getItem(props.storageKey)
    if (!raw) return null
    const parsed = JSON.parse(raw)
    let ratio = Number(parsed?.ratioOfViewport)
    if (Number.isFinite(ratio) && ratio > 0) {
      persistedState.ratioOfViewport = safeRatio(ratio)
      persistedState.ratioDirty = false
    } else {
      const width = String(parsed?.width || '').trim()
      const parsedNum = parseInt(width, 10)
      const minW = resolveMinWidth()
      const collapseThreshold = resolveCollapseThreshold()
      const baseWidthOk = Number.isFinite(parsedNum) && parsedNum >= collapseThreshold
      if (baseWidthOk) {
        const clamped = clampWidth(parsedNum, minW, resolveMaxWidth())
        persistedState.ratioOfViewport = ratioFromWidth(clamped)
        persistedState.ratioDirty = false
      } else {
        // 存档里既没有可用比例也没有可用宽度：视为「用户没拖过」，
        // 保持 ratioDirty=true，让固定默认宽度（defaultLeftWidth）生效。
        persistedState.ratioOfViewport = safeRatio(Number(props.defaultWidthRatioOfViewport) || 0.22)
      }
    }
    const responsiveWidth = widthFromRatio()
    const collapsed = Boolean(parsed?.collapsed)
    if (collapsed) {
      return { collapsed: true, width: responsiveWidth }
    }
    const responsiveNum = parseInt(responsiveWidth, 10)
    const minW = resolveMinWidth()
    if (responsiveNum < minW) {
      const minClamped = clampWidth(minW, minW, resolveMaxWidth())
      persistedState.ratioOfViewport = ratioFromWidth(minClamped)
      return { collapsed: false, width: minClamped + 'px' }
    }
    return { collapsed: false, width: responsiveWidth }
  } catch {
    return null
  }
}

const loaded = readPersistedState()
persistedState.value = loaded
const initialWidthPx = loaded ? (loaded.collapsed ? '0px' : loaded.width) : getDefaultExpandedWidth()
const lastExpandedWidth = ref(loaded?.width || getDefaultExpandedWidth())
const leftWidthPx = ref(initialWidthPx)
const isCollapsed = computed(() => leftWidthPx.value === '0px')

watch(
  () => props.leftWidth,
  (v) => {
    if (!isCollapsed.value) {
      const next = v || calcResponsiveWidth()
      leftWidthPx.value = next
      if (v) {
        persistedState.ratioOfViewport = ratioFromWidth(next)
        persistedState.ratioDirty = false
      }
    }
    if (!lastExpandedWidth.value || lastExpandedWidth.value === getDefaultExpandedWidth()) {
      lastExpandedWidth.value = v || calcResponsiveWidth()
    }
  },
)

const writePersistedState = () => {
  if (!hasWindow || !props.storageKey) return
  // 用户从未拖拽/收起过（ratioDirty）时不落盘：否则首屏就会把「默认比例」写进
  // localStorage，下次进来读到它，固定默认宽度（defaultLeftWidth）就永远不生效。
  if (persistedState.ratioDirty) return
  try {
    window.localStorage.setItem(
      props.storageKey,
      JSON.stringify({
        collapsed: isCollapsed.value,
        width: lastExpandedWidth.value,
        ratioOfViewport: safeRatio(persistedState.ratioOfViewport),
      }),
    )
  } catch {
    // ignore
  }
}

watch(
  () => [leftWidthPx.value, lastExpandedWidth.value, props.storageKey],
  () => writePersistedState(),
  { immediate: true },
)

let startX
let startWidth

const setLeftWidth = (nextWidth) => {
  if (props.collapsible && nextWidth <= resolveCollapseThreshold()) {
    leftWidthPx.value = '0px'
    return
  }
  // 最小值允许为 0：clampWidth 会把结果限制在 [0, max]，不再强制 >= 260
  const clamped = clampWidth(nextWidth, resolveMinWidth(), resolveMaxWidth())
  leftWidthPx.value = `${clamped}px`
  lastExpandedWidth.value = leftWidthPx.value
  persistedState.ratioOfViewport = ratioFromWidth(clamped)
  persistedState.ratioDirty = false
}

const expandPanel = () => {
  const nextFromRatio = parseInt(widthFromRatio(), 10)
  const stored = parseInt(lastExpandedWidth.value, 10)
  const fallback = parseInt(getDefaultExpandedWidth(), 10) || resolveMinWidth()
  const next = nextFromRatio || stored || fallback
  setLeftWidth(next)
}

const collapsePanel = () => {
  if (isCollapsed.value) return
  const current = parseInt(leftWidthPx.value, 10) || 0
  if (current > 0) {
    persistedState.ratioOfViewport = ratioFromWidth(current)
    // 主动收起属于「用户有过操作」，从此开始记忆偏好
    persistedState.ratioDirty = false
  }
  lastExpandedWidth.value = leftWidthPx.value || widthFromRatio()
  leftWidthPx.value = '0px'
}

const onDrag = throttle(function (e) {
  const nextWidth = startWidth + e.clientX - startX
  setLeftWidth(nextWidth)
}, 20)

const handleDragVisibilityChange = () => {
  if (typeof document === 'undefined' || document.visibilityState === 'visible') {
    return
  }
  dragEnd()
}

// 拖拽结束
const dragEnd = () => {
  document.documentElement.style.userSelect = ''
  if (document.documentElement.style.webkitUserSelect) {
    document.documentElement.style.webkitUserSelect = ''
  }
  if (document.body?.style) {
    document.body.style.userSelect = ''
    if (document.body.style.webkitUserSelect) {
      document.body.style.webkitUserSelect = ''
    }
  }
  document.documentElement.removeEventListener('mousemove', onDrag)
  document.documentElement.removeEventListener('mouseup', dragEnd)
  window.removeEventListener('blur', dragEnd)
  document.removeEventListener('visibilitychange', handleDragVisibilityChange)
}

// 鼠标按下
const startDrag = (e) => {
  startX = e.clientX
  // 使用受控宽度作为起始值（收起时为 0）
  startWidth = parseInt(leftWidthPx.value, 10) || 0
  document.documentElement.style.userSelect = 'none'
  if (document.documentElement.style.webkitUserSelect !== undefined) {
    document.documentElement.style.webkitUserSelect = 'none'
  }
  if (document.body?.style) {
    document.body.style.userSelect = 'none'
    if (document.body.style.webkitUserSelect !== undefined) {
      document.body.style.webkitUserSelect = 'none'
    }
  }
  document.documentElement.addEventListener('mousemove', onDrag)
  document.documentElement.addEventListener('mouseup', dragEnd)
  window.addEventListener('blur', dragEnd)
  document.addEventListener('visibilitychange', handleDragVisibilityChange)
}

// 响应式自动伸缩：永远用当前 ratioOfViewport × 新屏幕宽度重算
const onWindowResize = throttle(() => {
  if (!props.autoResponsive || isCollapsed.value) {
    return
  }
  const next = widthFromRatio()
  if (next !== leftWidthPx.value) {
    leftWidthPx.value = next
    lastExpandedWidth.value = next
  }
}, 100)

onMounted(() => {
  if (!hasWindow) return
  window.addEventListener('resize', onWindowResize)
})

onBeforeUnmount(() => {
  dragEnd()
  if (hasWindow) {
    window.removeEventListener('resize', onWindowResize)
  }
})
</script>

<style lang="less">
@import '@/styles/theme.less';

@classNames: split-wrapper, separator, collapsed-handle;
.themeBgColor(@classNames);

.split-wrapper {
  --split-handle-bg: var(--ant-colorFillSecondary, #f5f7fa);
  --split-handle-border: var(--ant-colorBorder, #d9d9d9);
  --split-handle-accent: #52c41a;
  --split-panel-gap: 14px;
  --split-separator-width: 12px;
  position: relative;
  display: flex;
  width: 100%;
  height: 100%;

  .scalable {
    position: relative;
    align-self: stretch;
    /* flex-shrink 必须为 0：否则右侧内容（编辑器/结果表格）撑开时会把左栏压窄，
       内联的 280px 只是「建议宽度」，不收缩才能真正固定住。 */
    flex: 0 0 auto;
    min-width: 0;
    min-height: 0;
    max-width: 50vw;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    height: 100%;

    .left-content {
      display: flex;
      flex: 1;
      min-height: 0;
      overflow: hidden;
      padding: 10px calc(var(--split-panel-gap) + var(--split-separator-width)) 10px 10px;

      > * {
        flex: 1 1 auto;
        min-width: 0;
        min-height: 0;
      }
    }

    .separator {
      display: flex;
      position: absolute;
      top: 10px;
      right: 0;
      bottom: 10px;
      align-items: center;
      justify-content: center;
      width: var(--split-separator-width);
      height: auto;
      border: 1px solid var(--split-handle-border);
      border-radius: 999px;
      background-color: var(--split-handle-bg);
      box-shadow: none;
      cursor: col-resize;
      transition: border-color 0.2s ease, background-color 0.2s ease;

      i {
        width: 2px;
        height: 18px;
        margin: 0 1px;
        border-radius: 1px;
        background-color: rgba(82, 196, 26, 0.38);
      }

      &:hover {
        border-color: var(--split-handle-accent);
        background-color: rgba(82, 196, 26, 0.12);

        i {
          background-color: rgba(82, 196, 26, 0.58);
        }
      }

      .split-toggle-button {
        position: absolute;
        top: 50%;
        left: 50%;
        transform: translate(calc(-50% - 4px), -50%);
      }
    }
  }

  .right-content {
    background-color: #ffffff;
    display: flex;
    flex: 1;
    min-width: 0;
    min-height: 0;
    padding: 10px 0 10px var(--split-panel-gap);
    box-sizing: border-box;

    > * {
      flex: 1 1 auto;
      min-width: 0;
      min-height: 0;
    }
  }

  .collapsed-handle {
    display: flex;
    position: absolute;
    top: 0;
    left: 0;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 100%;
    box-sizing: border-box;
    padding: 0 2px;
    border-right: 1px solid var(--split-handle-border);
    background-color: var(--split-handle-bg);
    cursor: col-resize;
    z-index: 3;
    transition: border-color 0.2s ease, background-color 0.2s ease;

    i {
      width: 2px;
      height: 18px;
      margin: 0 1px;
      border-radius: 1px;
      background-color: rgba(82, 196, 26, 0.38);
      opacity: 0.7;
    }

    &:hover {
      border-color: var(--split-handle-accent);
      background-color: rgba(82, 196, 26, 0.12);

      i {
        opacity: 1;
        background-color: rgba(82, 196, 26, 0.58);
      }
    }

    .split-toggle-button {
      position: absolute;
      top: 50%;
      left: 50%;
      transform: translate(-50%, -50%);
    }
  }

  &.collapsed {
    .right-content {
      /* 预留把手宽度，避免遮挡右侧内容（按钮/表格等） */
      padding-left: 22px;
      box-sizing: border-box;
    }
  }

  .right-content {
    overflow: auto;
  }

  .split-toggle-button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 56px;
    padding: 0;
    border: 1px solid var(--split-handle-border);
    border-radius: 999px;
    background: #ffffff;
    color: rgba(82, 196, 26, 0.78);
    cursor: pointer;
    z-index: 4;
    transition: background-color 0.2s ease, border-color 0.2s ease, color 0.2s ease, box-shadow 0.2s ease;

    &:hover {
      border-color: var(--split-handle-accent);
      background: rgba(82, 196, 26, 0.10);
      color: var(--split-handle-accent);
      box-shadow: 0 4px 12px rgba(82, 196, 26, 0.12);
    }
  }
}
</style>
