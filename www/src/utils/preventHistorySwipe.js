const HORIZONTAL_DELTA_THRESHOLD = 8
const SCROLL_TOLERANCE = 1

function isHorizontalScrollable(element) {
  if (!(element instanceof HTMLElement)) {
    return false
  }

  const style = window.getComputedStyle(element)
  const overflowX = style.overflowX
  const canScrollByStyle = ['auto', 'scroll', 'overlay'].includes(overflowX)

  return canScrollByStyle && element.scrollWidth - element.clientWidth > SCROLL_TOLERANCE
}

function canScrollInDirection(element, deltaX) {
  if (!isHorizontalScrollable(element)) {
    return false
  }

  if (deltaX < 0) {
    return element.scrollLeft > SCROLL_TOLERANCE
  }

  const maxScrollLeft = element.scrollWidth - element.clientWidth
  return maxScrollLeft - element.scrollLeft > SCROLL_TOLERANCE
}

function hasScrollableAncestor(target, deltaX) {
  let current = target instanceof HTMLElement ? target : null

  while (current && current !== document.body) {
    if (canScrollInDirection(current, deltaX)) {
      return true
    }
    current = current.parentElement
  }

  return false
}

function shouldBlockHistorySwipe(event) {
  if (event.defaultPrevented || event.ctrlKey || event.metaKey) {
    return false
  }

  const { deltaX, deltaY } = event
  const absDeltaX = Math.abs(deltaX)
  const absDeltaY = Math.abs(deltaY)

  if (absDeltaX < HORIZONTAL_DELTA_THRESHOLD || absDeltaX <= absDeltaY) {
    return false
  }

  return !hasScrollableAncestor(event.target, deltaX)
}

function handleWheel(event) {
  if (shouldBlockHistorySwipe(event)) {
    event.preventDefault()
  }
}

export function setupPreventHistorySwipe() {
  window.addEventListener('wheel', handleWheel, {
    passive: false,
    capture: true,
  })
}
