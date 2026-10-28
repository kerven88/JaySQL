import MarkdownIt from 'markdown-it'
import hljs from 'highlight.js/lib/common'

// AI 助手回答的 Markdown 渲染器（单例，模块级创建一次）
// 安全边界：html:false 禁止消息里嵌原始 HTML（防 XSS）；链接强制新窗口 + noopener
const md = new MarkdownIt({
  html: false,
  linkify: true,
  breaks: true,
  highlight(str, lang) {
    const langClass = lang && hljs.getLanguage(lang) ? ` language-${lang}` : ''
    let body
    if (lang && hljs.getLanguage(lang)) {
      try {
        body = hljs.highlight(str, { language: lang, ignoreIllegals: true }).value
      } catch (_) {
        body = md.utils.escapeHtml(str)
      }
    } else {
      body = md.utils.escapeHtml(str)
    }
    return `<pre class="md-code"><code class="hljs${langClass}">${body}</code></pre>`
  },
})

const defaultLinkOpen =
  md.renderer.rules.link_open ||
  ((tokens, idx, options, env, self) => self.renderToken(tokens, idx, options))

md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  tokens[idx].attrSet('target', '_blank')
  tokens[idx].attrSet('rel', 'noopener noreferrer')
  return defaultLinkOpen(tokens, idx, options, env, self)
}

export const renderMarkdown = (text) => md.render(String(text || ''))
