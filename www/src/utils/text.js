// 超长文本按字符数截断，超出部分用 ... 表示。
// 调用方一般用 :title / title 属性保留完整值，避免截断后信息丢失。
export const clipText = (value, max) => {
  const text = String(value ?? '').trim()
  const limit = Number(max)
  if (!text || !Number.isFinite(limit) || limit <= 0) return text
  return text.length > limit ? `${text.slice(0, limit)}...` : text
}

// 实例名 / 地址的展示上限：全站统一口径，避免同屏各处截得不一样长
export const INSTANCE_NAME_CLIP = 15
export const INSTANCE_ADDR_CLIP = 20

// 实例名 + ip:port 的常用组合截断（返回 { name, addr }，便于模板直接取值）
export const clipInstanceLabel = (name, addr) => ({
  name: clipText(name, INSTANCE_NAME_CLIP),
  addr: clipText(addr, INSTANCE_ADDR_CLIP),
})
