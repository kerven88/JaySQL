/**
 * 大模型厂商 → 品牌 Logo 映射
 *
 * 与 utils/dbTypeLogo.js 同一套约定：图标资源放 assets/logo/llm，
 * 尺寸与间距由调用方 CSS 统一控制，避免各页面各写一份。
 *
 * 用法：<img v-if="llmVendorLogo(record)" class="vendor-logo" :src="llmVendorLogo(record)" :alt="record.label" />
 * 未收录的厂商返回空串，由调用方决定是否留白（如 Ollama / vLLM / 自定义服务不展示 Logo）。
 *
 * 键同时兼容模板的 icon 字段与 vendor 字段，两者任一命中即可命中图标。
 */
import openaiLogo from '@/assets/logo/llm/openai.svg'
import geminiLogo from '@/assets/logo/llm/gemini.svg'
import deepseekLogo from '@/assets/logo/llm/deepseek.svg'
import qwenLogo from '@/assets/logo/llm/qwen.svg'
import kimiLogo from '@/assets/logo/llm/kimi.svg'
import glmLogo from '@/assets/logo/llm/glm.svg'

/** 键统一小写；一个品牌可注册多个别名（icon 字段 / vendor 字段） */
const LOGO_BY_KEY = Object.freeze({
  openai: openaiLogo,
  gemini: geminiLogo,
  deepseek: deepseekLogo,
  // 通义千问：icon=qwen，vendor=dashscope
  qwen: qwenLogo,
  dashscope: qwenLogo,
  // Kimi：icon=moonshot，vendor=moonshot
  moonshot: kimiLogo,
  kimi: kimiLogo,
  // 智谱 GLM：icon=zhipu，vendor=zhipu
  zhipu: glmLogo,
  glm: glmLogo,
})

const normalize = (value) =>
  String(value == null ? '' : value)
    .trim()
    .toLowerCase()

/**
 * 根据模板获取品牌 Logo 地址
 * @param {{icon?: string, vendor?: string}} template 厂商模板
 * @returns {string} 图标资源地址；未收录品牌返回空串
 */
export const llmVendorLogo = (template) => {
  const keys = [normalize(template?.icon), normalize(template?.vendor)]
  for (const key of keys) {
    if (key && LOGO_BY_KEY[key]) return LOGO_BY_KEY[key]
  }
  return ''
}

/**
 * 判断是否有品牌 Logo（未收录返回 false，调用方据此决定是否让名称左对齐铺满）
 * @param {{icon?: string, vendor?: string}} template 厂商模板
 * @returns {boolean}
 */
export const hasLlmVendorLogo = (template) => llmVendorLogo(template) !== ''

export default llmVendorLogo
