/**
 * 颜色处理工具（环境色/语义色派生）
 * 统一从这里导出，避免各组件重复实现，也保证环境标签与页签的取色口径一致。
 */

// 归一化为 #rrggbb，非法值返回 ''
export const normalizeHexColor = (value) => {
  const color = String(value || '').trim().toLowerCase()
  if (/^#[0-9a-f]{6}$/.test(color)) {
    return color
  }
  if (/^#[0-9a-f]{3}$/.test(color)) {
    return `#${color.slice(1).split('').map((char) => `${char}${char}`).join('')}`
  }
  return ''
}

// 追加 16 进制 alpha（如 '14' 表示约 8% 透明）
export const appendHexAlpha = (color, alphaHex) => {
  const normalized = normalizeHexColor(color)
  return normalized ? `${normalized}${alphaHex}` : ''
}

// 依据背景色亮度返回可读的前景色
export const getContrastTextColor = (color) => {
  const normalized = normalizeHexColor(color)
  if (!normalized) return '#ffffff'
  const red = Number.parseInt(normalized.slice(1, 3), 16)
  const green = Number.parseInt(normalized.slice(3, 5), 16)
  const blue = Number.parseInt(normalized.slice(5, 7), 16)
  const brightness = (red * 299 + green * 587 + blue * 114) / 1000
  return brightness >= 160 ? '#1f2937' : '#ffffff'
}

// 环境标签内联样式：淡背景 + 同色描边 + 同色文字
export const buildEnvironmentTagStyle = (color) => {
  const softBg = appendHexAlpha(color, '14')
  const softBorder = appendHexAlpha(color, '40')
  if (!softBg && !softBorder) return {}
  return {
    color: color || undefined,
    background: softBg || 'rgba(0, 0, 0, 0.04)',
    borderColor: softBorder || 'rgba(0, 0, 0, 0.1)',
  }
}
