/**
 * 工单语句分组：把「同一条建表语句及其伴生语句」在展示层归为一组
 *
 * 背景：Oracle 没有列注释内联语法（COMMENT ON 只能是独立语句），一条规范建表
 * 天然膨胀成 1+N 条语句；10 张表就是 60+ 行扁平列表，预审/任务列表一眼扫不出重点。
 * 行业通用做法（Liquibase changeSet / DMS 变更工单）：展示按目标对象聚合，执行仍逐条。
 *
 * 本 util 只做「展示层归组」，不参与后端拆分与执行：
 *   - resolveStatementGroup：解析一条语句归属的目标对象（key + 语句类别）；
 *   - buildStatementGroups：把逐条语句行聚合成组列表（保持首条语句的出现顺序）。
 * 无法识别 / 不归属任何建表对象的语句（DML 等）返回 null，由调用方保持原样渲染。
 */

/* 标识符：支持反引号 / 双引号包裹与 schema 前缀（如 `db`.`t`、hr."Test_User"） */
const IDENT = String.raw`(?:\`[^\`]+\`|"[^"]+"|[\w$]+)`
const QUALIFIED_IDENT = `${IDENT}(?:\\s*\\.\\s*${IDENT})*`

const CREATE_TABLE_RE = new RegExp(
  String.raw`^\s*create\s+(?:(?:global|private|sharded|duplicated|immutable)\s+)*(?:temporary\s+)?table\s+(?:if\s+not\s+exists\s+)?(${QUALIFIED_IDENT})`,
  'i',
)
const ALTER_TABLE_RE = new RegExp(String.raw`^\s*alter\s+table\s+(?:if\s+exists\s+)?(${QUALIFIED_IDENT})\b`, 'i')
const COMMENT_ON_RE = new RegExp(String.raw`^\s*comment\s+on\s+(column|table|materialized\s+view)\s+(.+?)\s+is\b`, 'i')
const CREATE_INDEX_RE = new RegExp(
  String.raw`^\s*create\s+(?:unique\s+|bitmap\s+)*index\s+(?:concurrently\s+)?(?:if\s+not\s+exists\s+)?(${QUALIFIED_IDENT})\s+on\s+(?:only\s+)?(${QUALIFIED_IDENT})`,
  'i',
)
const DROP_TABLE_RE = new RegExp(String.raw`^\s*drop\s+table\s+(?:if\s+exists\s+)?(${QUALIFIED_IDENT})\b`, 'i')
const TRUNCATE_TABLE_RE = new RegExp(String.raw`^\s*truncate\s+table\s+(?:if\s+exists\s+)?(${QUALIFIED_IDENT})\b`, 'i')

/* 标识符片段归一：剥引号 */
const normalizeIdent = (token) => String(token || '').trim().replace(/^[`"]+|[`"]+$/g, '')

/* 从限定标识符（schema.table）取分组键与展示名 */
const parseTableIdentifier = (raw) => {
  const parts = String(raw || '')
    .split('.')
    .map(normalizeIdent)
    .filter(Boolean)
  if (!parts.length) return null
  const name = parts[parts.length - 1]
  if (!name || !/^[\w$]+$/.test(name)) return null
  return { key: parts.join('.').toLowerCase(), name }
}

// COMMENT ON COLUMN 的目标以列结尾：三段 = schema.table.column（表取前两段），
// 两段 = table.column（表取第一段）。列归属表是唯一确定的，段数即可消歧。
const commentOnTarget = (kind, raw) => {
  const parts = String(raw || '')
    .split('.')
    .map(normalizeIdent)
    .filter(Boolean)
  if (!parts.length) return null
  if (kind === 'column') {
    if (parts.length < 2) return null
    const tableParts = parts.length >= 3 ? parts.slice(0, 2) : parts.slice(0, 1)
    return { key: tableParts.join('.').toLowerCase(), name: tableParts[tableParts.length - 1] }
  }
  return parseTableIdentifier(parts.join('.'))
}

/* 解析单条语句的目标对象。返回 { key, name, kind } 或 null。
   kind: create_table / alter / comment / index / drop / truncate */
export const resolveStatementGroup = (sql) => {
  // 剥掉前导注释行与块注释，避免 -- 注释吞掉语句头
  const text = String(sql || '')
    .split('\n')
    .map((line) => line.replace(/--.*$/, ''))
    .join(' ')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  if (!text) return null

  let match = text.match(CREATE_TABLE_RE)
  if (match) {
    const target = parseTableIdentifier(match[1])
    if (target) return { ...target, kind: 'create_table' }
  }
  match = text.match(ALTER_TABLE_RE)
  if (match) {
    const target = parseTableIdentifier(match[1])
    if (target) return { ...target, kind: 'alter' }
  }
  match = text.match(COMMENT_ON_RE)
  if (match) {
    const kind = match[1].toLowerCase() === 'column' ? 'column' : 'table'
    const target = commentOnTarget(kind, match[2])
    if (target) return { ...target, kind: 'comment' }
  }
  match = text.match(CREATE_INDEX_RE)
  if (match) {
    const target = parseTableIdentifier(match[2])
    if (target) return { ...target, kind: 'index' }
  }
  match = text.match(DROP_TABLE_RE)
  if (match) {
    const target = parseTableIdentifier(match[1])
    if (target) return { ...target, kind: 'drop' }
  }
  match = text.match(TRUNCATE_TABLE_RE)
  if (match) {
    const target = parseTableIdentifier(match[1])
    if (target) return { ...target, kind: 'truncate' }
  }
  return null
}

const KIND_LABELS = {
  create_table: '建表',
  alter: '变更',
  comment: '注释',
  index: '索引',
  drop: '删除',
  truncate: '清空',
}

/* 把逐条语句行聚合成组。
   rows: [{ seq, query, ... }]（预审行或任务行，至少要有 seq 与 query）
   返回 Map<groupKey, group>；group = { key, name, seqs, kinds, summary, firstSeq } */
export const buildStatementGroups = (rows) => {
  const groups = new Map()
  const order = []
  ;(rows || []).forEach((row) => {
    const target = resolveStatementGroup(row?.query)
    if (!target) return
    let group = groups.get(target.key)
    if (!group) {
      group = { key: target.key, name: target.name, seqs: [], kinds: new Map(), firstSeq: row.seq }
      groups.set(target.key, group)
      order.push(target.key)
    }
    group.seqs.push(row.seq)
    const kind = KIND_LABELS[target.kind] || '语句'
    group.kinds.set(kind, (group.kinds.get(kind) || 0) + 1)
  })
  order.forEach((key) => {
    const group = groups.get(key)
    group.summary = Array.from(group.kinds.entries())
      .map(([kind, count]) => (count > 1 ? `${kind}×${count}` : kind))
      .join(' + ')
  })
  return groups
}

/* 判断「是否值得启用分组渲染」：
   - 组数 < 语句数（分组压缩了列表长度），或
   - 存在包含 ≥2 条语句的组（聚合了伴生语句）
   两者都不满足时分组没有收益，调用方退回扁平渲染 */
export const groupsAreUseful = (rows, groups) => {
  if (!groups || !groups.size) return false
  const total = rows || []
  if (groups.size < total.length) return true
  return Array.from(groups.values()).some((group) => group.seqs.length > 1)
}
