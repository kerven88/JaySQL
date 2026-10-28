<!--
  右缘浮动入口组 —— 挂在全局 Layout，一次接入全页面生效。
    ┌ 联系方式（微信扫码）：悬停即展开二维码卡片
    ├ 公众号关注（纯图标，与另两个入口同风格）：悬停即展开公众号二维码卡片，引导词在卡片标题
    └ 问题反馈：点击打开反馈弹窗
  三个入口共用同一容器（也是拖动的「最小单元」），拖动时整组一起移动，相对位置固定。

  位置锚点：以「反馈按钮中心落在视口 80% 高度处」为准（保持反馈入口的既有位置不变），
  联系方式 / 公众号依次叠加在它上方 —— 因此容器用 bottom 锚定，而不是对整组做 translateY(-50%)
  （后者会把反馈按钮挤到 84.6%，把既有位置带偏）。新增入口只会让整组向上生长，反馈位置不动。

  交互约定：
  - 三个入口均为「系统主色实底 + 白色线条图标」，图标规格完全一致（24 viewBox / 1.8 圆头描边 / 14px / 同圆角）。
  - 无收起/展开功能：入口组恒为展开态。
  - 联系方式 / 公众号入口悬停即展开二维码卡片（鼠标移出入口与卡片后自动关闭），不显示悬停提示词；
    公众号卡片的标题即引导词「关注公众号」，入口上不放文字与图片 logo（保证三个图标风格统一）。
  - 反馈入口保留悬停提示「问题反馈」，且「立即无延迟出现」：不用原生 title（浏览器 tooltip 有 ~1s 延迟），也不用 opacity 过渡。
-->
<template>
  <div
    ref="dockEl"
    class="edge-dock"
    :class="{ 'is-dragging': dragging }"
    :style="dockStyle"
    @pointerdown="onPointerDown"
  >
    <!-- ① 联系方式入口 + 二维码卡片（悬停展开） -->
    <div
      ref="ctWrapEl"
      class="ct-wrap"
      :class="{ 'is-open': qrOpen }"
      @mouseenter="openQr"
      @mouseleave="closeQr"
    >
      <button
        class="dock-tab ct-tab"
        :class="{ 'is-open': qrOpen }"
        type="button"
        aria-label="扫码加微信"
        :aria-expanded="qrOpen ? 'true' : 'false'"
        @click="onQrTabClick"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <path d="M4 8.5V5.6c0-.9.7-1.6 1.6-1.6h2.9" />
          <path d="M15.5 4h2.9c.9 0 1.6.7 1.6 1.6v2.9" />
          <path d="M20 15.5v2.9c0 .9-.7 1.6-1.6 1.6h-2.9" />
          <path d="M8.5 20H5.6c-.9 0-1.6-.7-1.6-1.6v-2.9" />
          <rect x="8.6" y="8.6" width="6.8" height="6.8" rx="1.6" />
        </svg>
      </button>

      <!-- 二维码卡片：从入口左侧弹出，位置由 JS 收束在视口内（入口在 80% 高度，卡片必然会被往下挤）
           pointerdown.stop：卡片内按下鼠标不触发整组拖动 -->
      <div
        ref="qrCardEl"
        class="qr-card"
        role="dialog"
        aria-label="微信二维码"
        @pointerdown.stop
      >
        <div class="qr-head">
          <span class="qr-brand">
            <svg viewBox="0 0 24 24" fill="currentColor">
              <path d="M9.3 3.4c-3.9 0-7.1 2.6-7.1 5.7 0 1.8 1 3.4 2.5 4.5l-.6 2 2.3-1.2c.8.2 1.6.4 2.5.4h.5c-.1-.4-.2-.9-.2-1.3 0-3 2.9-5.4 6.4-5.4h.6c-.7-2.7-3.5-4.7-6.9-4.7Zm-2.4 3.5a.94.94 0 1 1 0 1.9.94.94 0 0 1 0-1.9Zm4.8 0a.94.94 0 1 1 0 1.9.94.94 0 0 1 0-1.9Z" />
              <path d="M22 13.5c0-2.8-2.8-5-6.2-5s-6.2 2.2-6.2 5 2.8 5 6.2 5c.7 0 1.4-.1 2.1-.3l1.9 1-.5-1.7c1.6-.9 2.7-2.4 2.7-4Zm-8.2-1.4a.85.85 0 1 1 0-1.7.85.85 0 0 1 0 1.7Zm4.1 0a.85.85 0 1 1 0-1.7.85.85 0 0 1 0 1.7Z" />
            </svg>
          </span>
          <span class="qr-title">微信扫码联系</span>
          <button class="qr-close" type="button" aria-label="关闭二维码" @click.stop="closeQr">&times;</button>
        </div>

        <div class="qr-code">
          <img :src="qrImage" alt="微信二维码" />
        </div>
      </div>
    </div>

    <!-- ② 公众号关注入口 + 二维码卡片（悬停展开）
         入口为纯线条 SVG 图标（与另两个入口同规格：24 viewBox / 1.8 描边 / 14px / 主色实底白图标）；
         引导词「关注公众号」呈现在悬停卡片的标题上，入口本身不再放文字与图片 logo -->
    <div
      ref="gzhWrapEl"
      class="gzh-wrap"
      :class="{ 'is-open': gzhOpen }"
      @mouseenter="openGzh"
      @mouseleave="closeGzh"
    >
      <button
        class="dock-tab gzh-tab"
        :class="{ 'is-open': gzhOpen }"
        type="button"
        aria-label="关注公众号"
        :aria-expanded="gzhOpen ? 'true' : 'false'"
        @click="onGzhTabClick"
      >
        <!-- 公众号图标：双气泡线稿，描边参数与另两个入口完全一致（1.8 圆头描边 + 实心眼点） -->
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <path d="M9.6 4.3c-3.7 0-6.7 2.5-6.7 5.6 0 1.8.9 3.3 2.4 4.4l-.7 2.5 2.8-1.5c.6.2 1.3.3 2 .3h.4" />
          <path d="M21 14.5c0-2.7-2.6-4.9-5.8-4.9s-5.8 2.2-5.8 4.9 2.6 4.9 5.8 4.9c.7 0 1.3-.1 1.9-.3l2.5 1.3-.6-2.2c1.2-.9 2-2.2 2-3.7Z" />
          <circle cx="7.6" cy="9.2" r="0.5" fill="currentColor" stroke="none" />
          <circle cx="11.6" cy="9.2" r="0.5" fill="currentColor" stroke="none" />
        </svg>
      </button>

      <div
        ref="gzhCardEl"
        class="qr-card"
        role="dialog"
        aria-label="公众号二维码"
        @pointerdown.stop
      >
        <div class="qr-head">
          <span class="qr-brand gzh-brand">
            <img :src="gzhLogo" alt="" />
          </span>
          <span class="qr-title">关注公众号</span>
          <button class="qr-close" type="button" aria-label="关闭二维码" @click.stop="closeGzh">&times;</button>
        </div>

        <div class="qr-code">
          <img :src="gzhQrcode" alt="公众号二维码" />
        </div>
      </div>
    </div>

    <!-- ③ 问题反馈入口 -->
    <div class="fb-group">
      <span class="edge-tip" aria-hidden="true">问题反馈</span>
      <button class="dock-tab fb-tab" type="button" aria-label="问题反馈" @click="onOpen">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z" />
        </svg>
      </button>
    </div>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

// 微信二维码图片（真图，876×880）；如需更换，替换 src/assets/wx-qrcode.jpg 即可
import qrImage from '@/assets/wx-qrcode.jpg'
// 公众号头像（入口 logo）与公众号二维码（暂用 logo 占位，真二维码就绪后替换同名文件即可）
import gzhLogo from '@/assets/gzh-logo.png'
import gzhQrcode from '@/assets/gzh-qrcode.png'

defineOptions({ name: 'FeedbackEntry' })
const emit = defineEmits(['open'])

const dragging = ref(false)
const dockEl = ref(null)
const ctWrapEl = ref(null)
const qrCardEl = ref(null)
const gzhWrapEl = ref(null)
const gzhCardEl = ref(null)

/* ---------------- 定位：bottom 锚定（反馈按钮中心 = 视口 80%） ----------------
   CSS 默认 bottom: calc(20% - 16px)（16px = 反馈按钮半高）；
   拖动后改为像素值，窗口缩放时重新收束。 */
const dockPos = ref('')
const dockStyle = computed(() => (dockPos.value ? { bottom: dockPos.value } : {}))

const viewportH = () => window.innerHeight || document.documentElement.clientHeight || 0

const currentBottom = () => {
  const el = dockEl.value
  if (!el) return 0
  return viewportH() - el.getBoundingClientRect().bottom
}

const clampBottom = (bottom) => {
  const el = dockEl.value
  const h = el?.offsetHeight || 32
  const vh = viewportH()
  const min = 8
  const max = Math.max(min, vh - h - 8)
  return Math.min(Math.max(bottom, min), max)
}

/* ---------------- 拖动：整组一起移动 ---------------- */
// 拖动结束的那次 click 需要被吞掉，避免误触触发动作。
// 兜底：某些浏览器拖完不派发 click，标志会滞留吞掉下一次正常点击 —— 用短定时器自愈。
let dragState = { active: false, moved: false, startY: 0, startBottom: 0 }
let movedResetTimer = null

const clearMovedTimer = () => {
  if (movedResetTimer) {
    clearTimeout(movedResetTimer)
    movedResetTimer = null
  }
}

const onPointerDown = (e) => {
  if (e.button !== undefined && e.button !== 0) return
  clearMovedTimer()
  dragState = { active: true, moved: false, startY: e.clientY, startBottom: currentBottom() }
  dragging.value = true
  try {
    e.preventDefault()
  } catch (_) {}
}

const onPointerMove = (e) => {
  if (!dragState.active) return
  const delta = e.clientY - dragState.startY
  if (Math.abs(delta) > 3) dragState.moved = true
  // 鼠标上移（delta<0）→ 容器上移 → 距底部更大
  if (dragState.moved) dockPos.value = `${clampBottom(dragState.startBottom - delta)}px`
}

const endDrag = () => {
  if (!dragState.active) return
  dragState.active = false
  dragging.value = false
  if (dragState.moved) {
    clearMovedTimer()
    movedResetTimer = setTimeout(() => {
      dragState.moved = false
      movedResetTimer = null
    }, 300)
    if (qrOpen.value) positionQrCard()
    if (gzhOpen.value) positionGzhCard()
  }
}

const consumeDragClick = () => {
  if (!dragState.moved) return false
  dragState.moved = false
  clearMovedTimer()
  return true
}

const onOpen = () => {
  if (consumeDragClick()) return // 拖动结束的那次 click 不打开弹窗
  emit('open')
}

/* ---------------- 二维码卡片：悬停展开 ---------------- */
const qrOpen = ref(false)
let repositionTimer = null

// 垂直居中对齐入口，同时收束在视口内；箭头跟随入口中心（限制在卡片圆角内）
const positionQrCard = () => {
  const wrap = ctWrapEl.value
  const card = qrCardEl.value
  if (!wrap || !card) return
  const wrapRect = wrap.getBoundingClientRect()
  const cardH = card.offsetHeight
  const vh = viewportH()
  const centerY = wrapRect.top + wrapRect.height / 2
  const top = Math.max(8, Math.min(centerY - cardH / 2, vh - cardH - 8))
  card.style.top = `${top - wrapRect.top}px`
  card.style.setProperty('--arrow-top', `${Math.min(Math.max(centerY - top, 14), cardH - 14)}px`)
}

const openQr = () => {
  if (consumeDragClick()) return
  if (qrOpen.value) return
  qrOpen.value = true
  requestAnimationFrame(positionQrCard)
  // 按钮 hover 加宽有 0.22s 过渡，过渡结束后再校正一次，避免卡片横向抖动残留
  clearTimeout(repositionTimer)
  repositionTimer = setTimeout(positionQrCard, 260)
}

const closeQr = () => {
  clearTimeout(repositionTimer)
  repositionTimer = null
  qrOpen.value = false
}

// 点击入口：作为鼠标不可用（触屏/键盘）时的兜底，只负责展开
const onQrTabClick = (e) => {
  e?.stopPropagation?.()
  if (consumeDragClick()) return
  openQr()
}

/* ---------------- 公众号二维码卡片：悬停展开（与联系方式卡片同一套定位/收束规则） ---------------- */
const gzhOpen = ref(false)
let gzhRepositionTimer = null

const positionGzhCard = () => {
  const wrap = gzhWrapEl.value
  const card = gzhCardEl.value
  if (!wrap || !card) return
  const wrapRect = wrap.getBoundingClientRect()
  const cardH = card.offsetHeight
  const vh = viewportH()
  const centerY = wrapRect.top + wrapRect.height / 2
  const top = Math.max(8, Math.min(centerY - cardH / 2, vh - cardH - 8))
  card.style.top = `${top - wrapRect.top}px`
  card.style.setProperty('--arrow-top', `${Math.min(Math.max(centerY - top, 14), cardH - 14)}px`)
}

const openGzh = () => {
  if (consumeDragClick()) return
  if (gzhOpen.value) return
  gzhOpen.value = true
  requestAnimationFrame(positionGzhCard)
  // 与联系方式卡片同理：入口 hover 过渡结束后再校正一次，避免卡片横向抖动残留
  clearTimeout(gzhRepositionTimer)
  gzhRepositionTimer = setTimeout(positionGzhCard, 260)
}

const closeGzh = () => {
  clearTimeout(gzhRepositionTimer)
  gzhRepositionTimer = null
  gzhOpen.value = false
}

// 点击入口：作为鼠标不可用（触屏/键盘）时的兜底，只负责展开
const onGzhTabClick = (e) => {
  e?.stopPropagation?.()
  if (consumeDragClick()) return
  openGzh()
}

/* ---------------- 全局监听 ---------------- */
const onDocumentKeydown = (e) => {
  if (e.key === 'Escape') {
    if (qrOpen.value) closeQr()
    if (gzhOpen.value) closeGzh()
  }
}

const onWindowResize = () => {
  const el = dockEl.value
  if (!el) return
  // 仅当拖动过（像素定位）时才需要重新收束；否则 CSS 的百分比锚点自动适配
  if (dockPos.value) dockPos.value = `${clampBottom(viewportH() - el.getBoundingClientRect().bottom)}px`
  if (qrOpen.value) positionQrCard()
  if (gzhOpen.value) positionGzhCard()
}

onMounted(() => {
  // 移动/松开挂在 document 上：指针移出控件仍可继续拖动。
  // 刻意不用 setPointerCapture —— 捕获会把 click 目标重定向，子按钮收不到 click。
  document.addEventListener('pointermove', onPointerMove)
  document.addEventListener('pointerup', endDrag)
  document.addEventListener('pointercancel', endDrag)
  document.addEventListener('keydown', onDocumentKeydown)
  window.addEventListener('resize', onWindowResize)
})

onBeforeUnmount(() => {
  document.removeEventListener('pointermove', onPointerMove)
  document.removeEventListener('pointerup', endDrag)
  document.removeEventListener('pointercancel', endDrag)
  document.removeEventListener('keydown', onDocumentKeydown)
  window.removeEventListener('resize', onWindowResize)
  clearMovedTimer()
  clearTimeout(repositionTimer)
  clearTimeout(gzhRepositionTimer)
})
</script>

<style scoped>
/* ===================== 容器：右缘竖排，右对齐 ===================== */
.edge-dock {
  position: fixed;
  right: 0;
  /* 锚点：反馈按钮中心 = 视口 80% 高度（16px = 反馈按钮半高），联系方式叠在它上方 */
  bottom: calc(20% - 16px);
  z-index: 900;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 10px;
  touch-action: none;
  user-select: none;
}
.edge-dock.is-dragging,
.edge-dock.is-dragging .dock-tab {
  cursor: grabbing;
}

/* ===================== 入口按钮（两个入口共用，参数完全一致） =====================
   统一底色：系统主色实底 + 白色图标；悬停加深（主色 active）并加宽到 36px。 */
.dock-tab {
  width: 32px;
  flex: 0 0 auto;
  padding: 9px 0;
  border: none;
  background: var(--gi-color-primary, #0f766e);
  color: #fff;
  filter: drop-shadow(-2px 3px 10px rgba(16, 42, 50, 0.18));
  cursor: grab;
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.16s ease, color 0.16s ease, width 0.22s ease, opacity 0.16s ease, filter 0.18s ease;
}
.dock-tab:hover,
.dock-tab.is-open {
  width: 36px;
  background: var(--gi-color-primary-active, #0b5f58);
  color: #fff;
  filter: drop-shadow(-3px 5px 14px rgba(16, 42, 50, 0.24));
}
.dock-tab:active {
  filter: brightness(0.95);
}
.dock-tab svg {
  width: 14px;
  height: 14px;
  flex: 0 0 auto;
  display: block;
  pointer-events: none;
}
/* 联系方式入口独立成块（左圆角）；反馈入口同为独立块，两块间距 10px */
.ct-tab {
  border-radius: 8px 0 0 8px;
}
.fb-tab {
  border-radius: 8px 0 0 8px;
}
.gzh-tab {
  border-radius: 8px 0 0 8px;
}

/* ===================== 反馈组 ===================== */
.fb-group {
  position: relative;
  display: flex;
  align-items: stretch;
}

/* ===================== 悬停提示（仅反馈入口）：立即显示，无任何延迟 =====================
   刻意不用原生 title（浏览器 tooltip 有约 1s 延迟），
   也不用 opacity 过渡（会引入淡入时间）—— transition: none = 进入即完整出现。 */
.edge-tip {
  position: absolute;
  right: calc(100% + 10px);
  top: 50%;
  transform: translateY(-50%);
  white-space: nowrap;
  pointer-events: none;
  background: #0f1720;
  color: #fff;
  font-size: 12px;
  line-height: 1;
  padding: 7px 10px;
  border-radius: 6px;
  opacity: 0;
  visibility: hidden;
  transition: none;
}
.edge-tip::after {
  content: '';
  position: absolute;
  left: 100%;
  top: 50%;
  transform: translateY(-50%);
  border: 5px solid transparent;
  border-left-color: #0f1720;
}
.fb-group:hover .edge-tip {
  opacity: 1;
  visibility: visible;
}
.edge-dock.is-dragging .edge-tip {
  opacity: 0;
  visibility: hidden;
}

/* ===================== 公众号关注入口 =====================
   入口与联系方式/问题反馈完全同规格（.dock-tab 默认样式，无任何特例），
   仅图标不同（公众号双气泡线稿）；引导词「关注公众号」呈现在悬停卡片标题上。 */
.gzh-wrap {
  position: relative;
  display: flex;
}
/* 展开态卡片：与 .ct-wrap.is-open 同规则（卡片 DOM 复用 .qr-card） */
.gzh-wrap.is-open .qr-card {
  opacity: 1;
  visibility: visible;
  pointer-events: auto;
  transform: translateX(0);
}
/* 卡片头部的公众号头像徽标：复用 .qr-brand 外框，内放 logo 图 */
.gzh-brand img {
  width: 12px;
  height: 12px;
  border-radius: 3px;
  display: block;
}

/* ===================== 二维码卡片 ===================== */
.ct-wrap {
  position: relative;
  display: flex;
}
.qr-card {
  position: absolute;
  right: calc(100% + 12px);
  width: 236px;
  max-width: calc(100vw - 56px);
  padding: 14px 16px 16px;
  background: #fff;
  border-radius: 12px;
  border: 1px solid rgba(15, 118, 110, 0.16);
  box-shadow: 0 10px 30px rgba(16, 42, 50, 0.14), 0 2px 8px rgba(16, 42, 50, 0.06);
  opacity: 0;
  visibility: hidden;
  pointer-events: none;
  transform: translateX(6px);
  transition: opacity 0.16s ease, transform 0.16s ease, visibility 0.16s;
}
.ct-wrap.is-open .qr-card {
  opacity: 1;
  visibility: visible;
  pointer-events: auto;
  transform: translateX(0);
}
/* 拖动过程中隐藏卡片，避免卡片跟不上移动产生拖影；拖动结束会重新定位 */
.edge-dock.is-dragging .qr-card {
  opacity: 0;
  visibility: hidden;
  transition: none;
}
/* 指向入口的小箭头：纵向位置由 JS 写入 --arrow-top，跟随入口中心 */
.qr-card::after {
  content: '';
  position: absolute;
  right: -6px;
  top: var(--arrow-top, 50%);
  width: 11px;
  height: 11px;
  background: #fff;
  border-right: 1px solid rgba(15, 118, 110, 0.16);
  border-top: 1px solid rgba(15, 118, 110, 0.16);
  transform: translateY(-50%) rotate(45deg);
}

.qr-head {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-bottom: 12px;
}
.qr-brand {
  width: 18px;
  height: 18px;
  flex: 0 0 18px;
  border-radius: 5px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #e8f8ef;
  color: #07c160;
}
.qr-brand svg {
  width: 12px;
  height: 12px;
  display: block;
}
.qr-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--gi-color-text-primary, #0f1720);
}
.qr-close {
  margin-left: auto;
  width: 22px;
  height: 22px;
  flex: 0 0 22px;
  border: none;
  border-radius: 6px;
  background: transparent;
  cursor: pointer;
  color: rgba(36, 57, 71, 0.5);
  font-size: 16px;
  line-height: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.15s ease, color 0.15s ease;
}
.qr-close:hover {
  background: #f2f5f7;
  color: var(--gi-color-text-primary, #0f1720);
}

/* 二维码：底部文字全部移除后，二维码即卡片末元素 —— 由卡片 padding 收口，无残留空白 */
.qr-code {
  width: 176px;
  height: 176px;
  margin: 0 auto;
  padding: 8px;
  border-radius: 10px;
  background: #fff;
  border: 1px solid #e6edf0;
  display: flex;
  align-items: center;
  justify-content: center;
}
.qr-code img {
  width: 100%;
  height: 100%;
  display: block;
  object-fit: contain;
}
</style>
