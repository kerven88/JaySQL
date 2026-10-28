/*
 * SQL 别名与作用域解析器
 * 输入：SQL 全文 + 光标位置
 * 输出：当前作用域内 alias → table 映射
 */

const JOIN_MARKERS = ['JOIN']
const STOP_KEYWORDS = [
  'WHERE', 'GROUP', 'HAVING', 'ORDER', 'LIMIT', 'UNION', 'INTERSECT', 'EXCEPT',
  'WITH', 'SELECT', 'ON', 'USING', 'INTO', 'UPDATE', 'SET', 'DELETE', 'RETURNING',
]
const JOIN_PREFIXES = ['INNER', 'LEFT', 'RIGHT', 'FULL', 'CROSS', 'OUTER', 'NATURAL']

const isIdChar = (ch) =>
  (ch >= 'a' && ch <= 'z') ||
  (ch >= 'A' && ch <= 'Z') ||
  (ch >= '0' && ch <= '9') ||
  ch === '_' || ch === '$' || ch === '.'

const isIdStartChar = (ch) =>
  (ch >= 'a' && ch <= 'z') ||
  (ch >= 'A' && ch <= 'Z') ||
  ch === '_' || ch === '$'

const isWhitespace = (ch) => ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r' || ch === '\f' || ch === '\v'

const skipQuoted = (text, pos, quote) => {
  const len = text.length
  let i = pos + 1
  while (i < len) {
    const ch = text[i]
    if (ch === quote) {
      if (text[i + 1] === quote) { i += 2; continue }
      return i + 1
    }
    if (ch === '\\') { i += 2; continue }
    i++
  }
  return len
}

const skipIgnored = (text, pos) => {
  const len = text.length
  let i = pos
  while (i < len) {
    const ch = text[i]
    if (isWhitespace(ch)) { i++; continue }
    if (ch === '\'') { i = skipQuoted(text, i, ch); continue }
    if (ch === '-' && text[i + 1] === '-') {
      const nl = text.indexOf('\n', i + 2)
      if (nl < 0) return len
      i = nl + 1
      continue
    }
    if (ch === '/' && text[i + 1] === '*') {
      const close = text.indexOf('*/', i + 2)
      if (close < 0) return len
      i = close + 2
      continue
    }
    break
  }
  return i
}

const statementRangeAt = (text, cursor) => {
  const len = text.length
  if (len === 0) return { start: 0, end: 0 }
  let pos = 0
  let start = 0
  while (pos < len) {
    let i = skipIgnored(text, pos)
    let end = len
    while (i < len) {
      const ch = text[i]
      if (ch === '\'') { i = skipQuoted(text, i, ch); continue }
      if (ch === '-' && text[i + 1] === '-') { const nl = text.indexOf('\n', i + 2); i = nl < 0 ? len : nl + 1; continue }
      if (ch === '/' && text[i + 1] === '*') { const cl = text.indexOf('*/', i + 2); i = cl < 0 ? len : cl + 2; continue }
      if (ch === ';') { end = i + 1; break }
      i++
    }
    if (cursor >= start && cursor <= end) return { start, end }
    pos = end
    start = end
  }
  return { start: Math.max(0, len - 1), end: len }
}

const readIdentifier = (text, pos) => {
  const len = text.length
  let s = skipIgnored(text, pos)
  if (s >= len) return null
  const ch = text[s]
  if (ch === '"' || ch === '`') {
    const end = skipQuoted(text, s, ch)
    const value = text.slice(s + 1, end - 1)
    return { value, end: skipIgnored(text, end) }
  }
  if (ch === '[') {
    const close = text.indexOf(']', s + 1)
    if (close < 0) return null
    const value = text.slice(s + 1, close)
    return { value, end: skipIgnored(text, close + 1) }
  }
  if (!isIdStartChar(ch)) return null
  let e = s
  while (e < len && isIdChar(text[e])) e++
  return { value: text.slice(s, e), end: skipIgnored(text, e) }
}

const tryReadKeyword = (text, pos, keyword) => {
  const len = text.length
  const s = skipIgnored(text, pos)
  if (s + keyword.length > len) return { ok: false, pos }
  const fragment = text.slice(s, s + keyword.length)
  if (fragment.toUpperCase() !== keyword) return { ok: false, pos }
  const after = s + keyword.length
  if (after < len && isIdChar(text[after])) return { ok: false, pos }
  return { ok: true, pos: skipIgnored(text, after) }
}

const isNextKeyword = (text, pos, keywords) => {
  for (const kw of keywords) {
    const t = tryReadKeyword(text, pos, kw)
    if (t.ok) return true
  }
  return false
}

const isNextStop = (text, pos) => isNextKeyword(text, pos, STOP_KEYWORDS)
const isNextJoinMarker = (text, pos) => {
  const len = text.length
  let s = skipIgnored(text, pos)
  let remaining = s
  while (true) {
    let consumed = false
    for (const prefix of JOIN_PREFIXES) {
      if (remaining + prefix.length > len) continue
      if (text.slice(remaining, remaining + prefix.length).toUpperCase() !== prefix) continue
      const after = remaining + prefix.length
      if (after < len && isIdChar(text[after])) continue
      remaining = skipIgnored(text, after)
      consumed = true
      break
    }
    if (!consumed) break
  }
  for (const marker of JOIN_MARKERS) {
    if (remaining + marker.length > len) continue
    if (text.slice(remaining, remaining + marker.length).toUpperCase() !== marker) continue
    const after = remaining + marker.length
    if (after < len && isIdChar(text[after])) continue
    return true
  }
  return false
}

const skipParen = (text, pos) => {
  const len = text.length
  let depth = 0
  let i = pos
  while (i < len) {
    const ch = text[i]
    if (ch === '"' || ch === '\'' || ch === '`') { i = skipQuoted(text, i, ch); continue }
    if (ch === '-' && text[i + 1] === '-') { const nl = text.indexOf('\n', i + 2); i = nl < 0 ? len : nl + 1; continue }
    if (ch === '/' && text[i + 1] === '*') { const cl = text.indexOf('*/', i + 2); i = cl < 0 ? len : cl + 2; continue }
    if (ch === '(') depth++
    else if (ch === ')') { depth--; if (depth === 0) return skipIgnored(text, i + 1) }
    i++
  }
  return len
}

const extractAliasScope = (text, cursor) => {
  if (!text || typeof text !== 'string') return {}
  const len = text.length
  const pos = Math.min(Math.max(0, cursor), len)
  const { start, end } = statementRangeAt(text, pos)
  const statement = text.slice(start, end)
  return parseStatementAliases(statement)
}

const parseStatementAliases = (statement) => {
  const result = Object.create(null)
  const register = (tableName, alias) => {
    if (!tableName) return
    const tk = tableName.toLowerCase()
    result[tk] = tableName
    if (alias) result[alias.toLowerCase()] = tableName
  }
  const len = statement.length
  let i = 0
  while (i < len) {
    i = skipIgnored(statement, i)
    if (i >= len) break
    const ch = statement[i]
    if (ch === '(') { i = skipParen(statement, i); continue }
    const fromTry = tryReadKeyword(statement, i, 'FROM')
    if (fromTry.ok) {
      i = fromTry.pos
      i = scanTableList(statement, i, register)
      continue
    }
    const joinPos = tryConsumeJoinPrefix(statement, i)
    if (joinPos.ok) {
      i = joinPos.pos
      const entry = readOneTableEntry(statement, i)
      if (entry) {
        register(entry.tableName, entry.alias)
        i = entry.end
      } else {
        i = skipIgnored(statement, i + 1)
      }
      continue
    }
    i++
  }
  return result
}

const tryConsumeJoinPrefix = (text, pos) => {
  const len = text.length
  let s = skipIgnored(text, pos)
  let remaining = s
  while (true) {
    let consumed = false
    for (const prefix of JOIN_PREFIXES) {
      if (remaining + prefix.length > len) continue
      if (text.slice(remaining, remaining + prefix.length).toUpperCase() !== prefix) continue
      const after = remaining + prefix.length
      if (after < len && isIdChar(text[after])) continue
      remaining = skipIgnored(text, after)
      consumed = true
      break
    }
    if (!consumed) break
  }
  for (const marker of JOIN_MARKERS) {
    if (remaining + marker.length > len) continue
    if (text.slice(remaining, remaining + marker.length).toUpperCase() !== marker) continue
    const after = remaining + marker.length
    if (after < len && isIdChar(text[after])) return { ok: false, pos }
    return { ok: true, pos: skipIgnored(text, after) }
  }
  return { ok: false, pos }
}

const KEYWORD_TABLE = new Set([
  ...STOP_KEYWORDS,
  ...JOIN_PREFIXES,
  ...JOIN_MARKERS,
  'AND', 'OR', 'NOT', 'NULL', 'IS', 'IN', 'LIKE', 'BETWEEN', 'EXISTS',
  'ASC', 'DESC', 'NULLS', 'FIRST', 'LAST', 'DISTINCT', 'ALL', 'ANY', 'SOME',
  'CASE', 'WHEN', 'THEN', 'ELSE', 'END', 'AS',
])

const isKeywordToken = (value) => {
  if (!value) return false
  return KEYWORD_TABLE.has(String(value).toUpperCase())
}

const readOneTableEntry = (text, pos) => {
  let i = skipIgnored(text, pos)
  const len = text.length
  if (i >= len) return null
  if (text[i] === '(') {
    const afterParen = skipParen(text, i)
    let alias = null
    let cur = afterParen
    const asTry = tryReadKeyword(text, cur, 'AS')
    if (asTry.ok) {
      cur = asTry.pos
      const at = readIdentifier(text, cur)
      if (at && !isKeywordToken(at.value)) { alias = at.value; cur = at.end }
    } else {
      const at = readIdentifier(text, cur)
      if (at && !isKeywordToken(at.value)) {
        alias = at.value
        cur = at.end
      }
    }
    if (alias) return { tableName: alias, alias, end: cur }
    return { tableName: null, alias: null, end: afterParen }
  }
  const segments = []
  let cur = i
  while (true) {
    const id = readIdentifier(text, cur)
    if (!id) break
    if (isKeywordToken(id.value)) break
    segments.push(id.value)
    cur = id.end
    const afterDot = skipIgnored(text, cur)
    if (afterDot < len && text[afterDot] === '.') {
      cur = skipIgnored(text, afterDot + 1)
      continue
    }
    break
  }
  if (!segments.length) return null
  const tableName = segments[segments.length - 1]
  let alias = null
  const asTry = tryReadKeyword(text, cur, 'AS')
  if (asTry.ok) {
    cur = asTry.pos
    const at = readIdentifier(text, cur)
    if (at && !isKeywordToken(at.value)) { alias = at.value; cur = at.end }
  } else {
    const at = readIdentifier(text, cur)
    if (at && !isKeywordToken(at.value)) {
      alias = at.value
      cur = at.end
    }
  }
  return { tableName, alias, end: cur }
}

const isNextComma = (text, pos) => {
  const s = skipIgnored(text, pos)
  return text[s] === ','
}

const scanTableList = (text, pos, register) => {
  let i = skipIgnored(text, pos)
  const len = text.length
  while (i < len) {
    if (isNextStop(text, i) || isNextJoinMarker(text, i)) return i
    const entry = readOneTableEntry(text, i)
    if (!entry || !entry.tableName) {
      i++
      i = skipIgnored(text, i)
      continue
    }
    register(entry.tableName, entry.alias)
    i = entry.end
    i = skipIgnored(text, i)
    if (isNextComma(text, i)) {
      const s = skipIgnored(text, i)
      i = skipIgnored(text, s + 1)
      continue
    }
    if (isNextStop(text, i) || isNextJoinMarker(text, i)) return i
    // fallback 防止死循环
    if (i === skipIgnored(text, i)) {
      const next = readIdentifier(text, i)
      if (!next) break
    }
  }
  return i
}

export {
  extractAliasScope,
  parseStatementAliases,
  statementRangeAt,
  tryReadKeyword,
  readIdentifier,
}
