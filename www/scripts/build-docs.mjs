#!/usr/bin/env node
/**
 * 文档中心正文构建：docs/manual/**\/*.md → www/src/views/docs/manifest.json
 *
 * 为什么要构建期转：浏览器不引 markdown 库，运行时零解析、零接口依赖，产物可控。
 * 执行顺序：本脚本必须先于 vite build（已挂在 package.json 的 predev / prebuild 上）。
 *
 * 产出 manifest.json 结构（与 docs/plans/2026-10-09-*.md §3.4 对齐）：
 *   { generatedAt, groups:[{id,title,order}], docs:[{slug,title,group,order,icon,
 *     desc,updated,reading,tags,keywords,anchors:[{id,text,level}],html,text}] }
 *
 * 转换过程中的两个重写（否则产物在 SPA 里点不动 / 图裂）：
 *   1. 站内 .md 链接  → href="#" + data-doc-slug（由页面接管成路由内切换）
 *      目标在 docs/manual 内但不在导航里（如 ../README.md）→ 降级为纯文本并告警
 *   2. 站内图片       → 内联成 data URI（docs/ 不在 frontend 静态目录里，无法直接引用）
 *
 * 校验失败即 throw，让构建停在半路，不留「页面能开但内容不对」的中间态。
 */
import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

import matter from 'gray-matter'
import MarkdownIt from 'markdown-it'
import container from 'markdown-it-container'

// highlight.js 走 CJS 的 lib/common 子集：只含常用语言，避免把全量语言包打进构建依赖
const require = createRequire(import.meta.url)
const hljs = require('highlight.js/lib/common')

const here = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(here, '../..')
const manualRoot = path.resolve(repoRoot, 'docs/manual')
const outFile = path.resolve(here, '../src/views/docs/manifest.json')
const navFile = path.join(manualRoot, 'nav.json')

/** :::info|tip|warn|danger 容器 */
const CALLOUT_TYPES = ['info', 'tip', 'warn', 'danger']
const IMAGE_MIME = {
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.gif': 'image/gif',
  '.webp': 'image/webp',
}
/** 单张图超过这个体量就不内联，避免 manifest 无脑膨胀 */
const INLINE_IMAGE_MAX = 512 * 1024
/** 必填 front-matter 字段，缺一个就中断构建（编写规范见 docs/manual/README.md） */
const REQUIRED_FIELDS = ['title', 'group', 'order', 'desc', 'keywords']

const warnings = []
const warn = (msg) => warnings.push(msg)

const toPosix = (p) => p.split(path.sep).join('/')
const normDate = (v) => (v instanceof Date ? v.toISOString().slice(0, 10) : v)
/** 产物体积（不用 Buffer，省得给构建脚本引 node globals） */
const byteLength = (str) => new TextEncoder().encode(str).length
const kb = (bytes) => `${(bytes / 1024).toFixed(1)}KB`

/** 标题 → 锚点 id，与目录（本章小节）共用同一套规则，保证点得中 */
const slugifyHeading = (text) =>
  text
    .trim()
    .toLowerCase()
    .replace(/[*`]/g, '')
    .replace(/\s+/g, '-')
    .replace(/[^\w一-龥-]/g, '')

/** 从 inline token 取纯文本（含行内代码、图片 alt），用于锚点 id 与目录 */
const inlineText = (token) => {
  if (!token) return ''
  if (!token.children) return token.content || ''
  return token.children
    .map((child) => {
      if (child.type === 'text' || child.type === 'code_inline') return child.content
      if (child.type === 'softbreak' || child.type === 'hardbreak') return ' '
      return inlineText(child)
    })
    .join('')
}

const md = new MarkdownIt({ html: false, linkify: true, breaks: false })

// 标题 id + 目录：核心规则必须排在最后，此时 inline 子 token 已经生成
md.core.ruler.push('docs_anchors', (state) => {
  const seen = new Map()
  const anchors = []
  const { tokens } = state
  for (let i = 0; i < tokens.length; i += 1) {
    const open = tokens[i]
    if (open.type !== 'heading_open') continue
    const level = Number(open.tag.slice(1))
    const text = inlineText(tokens[i + 1])
    const base = slugifyHeading(text) || `section-${i}`
    const hit = seen.get(base) || 0
    seen.set(base, hit + 1)
    const id = hit ? `${base}-${hit + 1}` : base
    open.attrSet('id', id)
    if (level === 2 || level === 3) anchors.push({ id, text, level })
  }
  state.env.anchors = anchors
  return true
})

for (const type of CALLOUT_TYPES) {
  md.use(container, type, {
    render(tokens, idx) {
      const token = tokens[idx]
      if (token.nesting !== 1) return '</div>\n'
      const title = token.info.trim().slice(type.length).trim()
      const head = title ? `<p class="doc-callout-title">${md.utils.escapeHtml(title)}</p>` : ''
      return `<div class="doc-callout doc-callout--${type}">${head}\n`
    },
  })
}

// <!-- screenshot: 描述 --> → 截图占位块（html:false 下必须自己兜，否则会显示成一行字面量）
md.block.ruler.before('html_block', 'docs_shot', (state, startLine, endLine, silent) => {
  const pos = state.bMarks[startLine] + state.tShift[startLine]
  const line = state.src.slice(pos, state.eMarks[startLine])
  const m = /^<!--\s*screenshot:\s*(.+?)\s*-->/.exec(line)
  if (!m) return false
  if (silent) return true
  const token = state.push('docs_shot', '', 0)
  token.content = m[1]
  token.map = [startLine, startLine + 1]
  state.line = startLine + 1
  return true
})
md.renderer.rules.docs_shot = (tokens, idx) =>
  `<figure class="doc-shot"><span class="doc-shot-badge">截图占位</span>` +
  `<figcaption>${md.utils.escapeHtml(tokens[idx].content)}</figcaption></figure>\n`

// 代码块：交给全局已引入的 atom-one-dark 主题上色（main.js 里的 highlight.js 样式）
md.options.highlight = (code, lang) => {
  if (!lang || !hljs.getLanguage(lang)) return ''
  const { value } = hljs.highlight(code, { language: lang, ignoreIllegals: true })
  return `<pre class="hljs"><code>${value}</code></pre>`
}

// 宽表格要有自己的横向滚动容器，否则会把整页撑出横向滚动条
md.renderer.rules.table_open = () => '<div class="doc-table-wrap">\n<table>\n'
md.renderer.rules.table_close = () => '</table>\n</div>\n'

/** .md 内链 → SPA 内切换；解析不到的降级为纯文本 */
md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  const token = tokens[idx]
  const href = token.attrGet('href') || ''

  if (/^(?:[a-z][a-z0-9+.-]*:)?\/\//i.test(href) || /^mailto:/i.test(href)) {
    token.attrSet('target', '_blank')
    token.attrSet('rel', 'noopener noreferrer')
    return self.renderToken(tokens, idx, options)
  }

  if (href.startsWith('#')) return self.renderToken(tokens, idx, options)

  const [file, hash] = href.split('#')
  if (!/\.md$/i.test(file)) return self.renderToken(tokens, idx, options)

  const abs = path.resolve(env.docDir, file)
  const rel = toPosix(path.relative(manualRoot, abs)).replace(/\.md$/i, '')
  if (rel.startsWith('..') || !env.slugs.has(rel)) {
    // 指向仓库里的非导航文件（如 ../README.md）：标成哑链（CSS 去链接样式 + 点击不跳）
    warn(`${env.slug}: 站内链接 ${href} 不在文档导航内，已按纯文本样式处理`)
    token.attrSet('href', '#')
    token.attrSet('data-doc-plain', '1')
    return self.renderToken(tokens, idx, options)
  }
  token.attrSet('href', '#')
  token.attrSet('data-doc-slug', rel)
  if (hash) token.attrSet('data-doc-anchor', hash)
  return self.renderToken(tokens, idx, options)
}

/** 站内图片 → data URI */
md.renderer.rules.image = (tokens, idx, options, env, self) => {
  const token = tokens[idx]
  const src = token.attrGet('src') || ''
  if (/^(?:[a-z][a-z0-9+.-]*:)?\/\//i.test(src) || src.startsWith('data:')) {
    return self.renderToken(tokens, idx, options)
  }
  const abs = path.resolve(env.docDir, src)
  const ext = path.extname(abs).toLowerCase()
  const inside = toPosix(path.relative(manualRoot, abs))
  if (!inside.startsWith('..') && fs.existsSync(abs)) {
    const size = fs.statSync(abs).size
    if (size > INLINE_IMAGE_MAX) {
      warn(`${env.slug}: 图片 ${src} 体积 ${(size / 1024).toFixed(0)}KB 超过内联上限，已保留原路径`)
    } else {
      const base64 = fs.readFileSync(abs).toString('base64')
      token.attrSet('src', `data:${IMAGE_MIME[ext] || 'application/octet-stream'};base64,${base64}`)
    }
    return self.renderToken(tokens, idx, options)
  }
  warn(`${env.slug}: 图片 ${src} 找不到（解析为 ${abs}），已保留原路径`)
  return self.renderToken(tokens, idx, options)
}

/** HTML → 纯文本（供页面检索与后续 AI 检索复用） */
const toPlainText = (html) =>
  html
    .replace(/<(script|style)[\s\S]*?<\/\1>/gi, ' ')
    .replace(/<[^>]+>/g, ' ')
    .replace(/&nbsp;/g, ' ')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, '&')
    .replace(/\s+/g, ' ')
    .trim()

const readNav = () => {
  if (!fs.existsSync(navFile)) throw new Error(`缺少导航配置：${navFile}`)
  const groups = JSON.parse(fs.readFileSync(navFile, 'utf8'))
  if (!Array.isArray(groups) || !groups.length) throw new Error('nav.json 必须是「非空数组」的分组列表')
  return groups
    .map((g) => ({ id: String(g.id), title: String(g.title ?? g.id), order: Number(g.order ?? 999) }))
    .sort((a, b) => a.order - b.order)
}

const main = () => {
  const groups = readNav()

  // 第一遍：扫文件 + 解 front-matter（链接重写需要先拿到全部 slug）
  const pending = []
  const slugs = new Set()
  for (const group of groups) {
    const dir = path.join(manualRoot, group.id)
    if (!fs.existsSync(dir)) {
      warn(`分组 ${group.id} 在 docs/manual 下没有对应目录，已跳过`)
      continue
    }
    for (const file of fs.readdirSync(dir).filter((f) => f.endsWith('.md')).sort()) {
      const filePath = path.join(dir, file)
      const parsed = matter(fs.readFileSync(filePath, 'utf8'))
      const fm = {}
      for (const [k, v] of Object.entries(parsed.data)) fm[k] = normDate(v)

      const missing = REQUIRED_FIELDS.filter((k) => fm[k] === undefined || fm[k] === '')
      if (missing.length) {
        throw new Error(`${toPosix(path.relative(manualRoot, filePath))} 缺少必填 front-matter：${missing.join(', ')}`)
      }
      if (fm.group !== group.id) {
        throw new Error(`docs/manual/${group.id}/${file} 的 front-matter group=${fm.group} 与目录不一致`)
      }

      const slug = `${group.id}/${file.replace(/\.md$/i, '')}`
      slugs.add(slug)
      pending.push({ slug, filePath, fm, group, body: parsed.content })
    }
  }

  // 第二遍：渲染
  const docs = pending.map(({ slug, filePath, fm, group, body }) => {
    const env = { docDir: path.dirname(filePath), slug, slugs, anchors: [] }
    const html = md.render(body, env)
    return {
      slug,
      title: String(fm.title),
      group: group.id,
      order: Number(fm.order),
      icon: fm.icon ? String(fm.icon) : 'file-text',
      desc: String(fm.desc),
      updated: fm.updated ? String(fm.updated) : '',
      reading: fm.reading ? String(fm.reading) : '',
      tags: Array.isArray(fm.tags) ? fm.tags.map(String) : [],
      keywords: Array.isArray(fm.keywords) ? fm.keywords.map(String) : [],
      anchors: env.anchors,
      html,
      text: toPlainText(html),
    }
  })

  const groupOrder = new Map(groups.map((g, i) => [g.id, i]))
  docs.sort(
    (a, b) => groupOrder.get(a.group) - groupOrder.get(b.group) || a.order - b.order || a.slug.localeCompare(b.slug),
  )

  const manifest = {
    generatedAt: new Date().toISOString(),
    groups,
    docs,
  }
  fs.mkdirSync(path.dirname(outFile), { recursive: true })
  fs.writeFileSync(outFile, `${JSON.stringify(manifest)}\n`, 'utf8')

  const total = fs.statSync(outFile).size
  console.log(
    `docs: ${docs.length} 篇 / ${manifest.groups.length} 分组 → ${toPosix(path.relative(repoRoot, outFile))} (${kb(total)})`,
  )
  for (const doc of docs) {
    console.log(
      `  ${doc.slug.padEnd(20)} html ${kb(byteLength(doc.html)).padStart(8)}  anchors ${String(doc.anchors.length).padStart(2)}  text ${kb(byteLength(doc.text)).padStart(8)}`,
    )
  }
  if (warnings.length) {
    console.log(`\nwarning: ${warnings.length} 条（不中断构建，但请确认）`)
    for (const w of warnings) console.log(`  - ${w}`)
  }
}

main()
