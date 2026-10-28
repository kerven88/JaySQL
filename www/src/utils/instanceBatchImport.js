/*
 * 批量导入允许的数据库类型。
 *
 * 判定口径与「新增实例」表单的 DB_TYPE_CONFIG 保持一致：只要后端能做到
 * ①表单 oneof 白名单 ②连接测试 ③元数据同步，就应当允许批量导入，
 * 否则同一个系统里"单条能建、批量不能导"会让人以为类型不支持。
 * SQLServer 三项均已具备；MongoDB 仍排除（DAS 执行链路无 Mongo 分支）。
 */
export const ALLOWED_DB_TYPES = new Set(['MySQL', 'PostgreSQL', 'SQLServer'])

export const DB_TYPE_ALIASES = {
  mysql: 'MySQL',
  mysqld: 'MySQL',
  mariadb: 'MySQL',
  tidb: 'MySQL',
  postgresql: 'PostgreSQL',
  postgres: 'PostgreSQL',
  pg: 'PostgreSQL',
  sqlserver: 'SQLServer',
  'sql server': 'SQLServer',
  mssql: 'SQLServer',
  'ms-sql': 'SQLServer',
}

export const INSTANCE_BATCH_HEADERS_CN = [
  '数据源名称',
  '环境',
  '数据库类型',
  '连接地址',
  '登录账号',
  '密码',
  '默认数据库',
  'SSL模式',
]

export const HEADER_CN_TO_KEY = {
  数据源名称: 'remark',
  remark: 'remark',
  name: 'remark',
  名称: 'remark',
  环境: 'environment',
  environment: 'environment',
  环境名称: 'environment',
  env: 'environment',
  数据库类型: 'db_type',
  db_type: 'db_type',
  dbtype: 'db_type',
  类型: 'db_type',
  连接地址: 'connection_address',
  connection_address: 'connection_address',
  address: 'connection_address',
  地址: 'connection_address',
  hostport: 'connection_address',
  登录账号: 'user',
  user: 'user',
  username: 'user',
  账号: 'user',
  用户名: 'user',
  密码: 'password',
  password: 'password',
  passwd: 'password',
  pwd: 'password',
  默认数据库: 'database',
  database: 'database',
  db: 'database',
  'SSL模式': 'sslmode',
  ssl_mode: 'sslmode',
  sslmode: 'sslmode',
  ssl: 'sslmode',
  主机: 'hostname',
  hostname: 'hostname',
  host: 'hostname',
  主机名: 'hostname',
  ip: 'hostname',
  端口: 'port',
  port: 'port',
}

export const TEMPLATE_CSV = `# =============================================================================
# 实例批量导入 — CSV 模板（请按以下要求填写；以 # 开头的整行会自动忽略）
# =============================================================================
# 【列顺序无要求】，按列名匹配即可（列名支持中英文两种别名：数据源名称/remark、环境/environment ...）
# 【必填列 6 个】数据源名称 / 环境 / 数据库类型 / 连接地址 / 登录账号 / 密码
# 【可选列 2 个】默认数据库 / SSL模式（留空时 PostgreSQL 取 postgres、SQLServer 取 master、MySQL 不指定；SSL模式仅 PostgreSQL 生效）
# 【数据库类型】支持 MySQL / PostgreSQL / SQLServer；可识别别名：mysqld/mariadb/tidb→MySQL；pg/postgres→PostgreSQL；mssql→SQLServer
# 【环境】填写环境名称（大小写不敏感但必须精确匹配，示例：开发/测试/预发/生产）；也可直接填环境ID
# 【连接地址】推荐写法：主机:端口（例 192.168.1.1:5432 或 db.example.com:3306）；也可拆为 "主机 + 端口" 两列
# 【单次上限】最多 500 行；文件大小 ≤ 2MB；仅支持 UTF-8 编码
# 【密码安全】CSV 密码为明文，请确保文件安全；导入成功后系统会加密存储，任何导出文件都不会包含密码
# =============================================================================
${INSTANCE_BATCH_HEADERS_CN.join(',')}
订单库主库,生产,PostgreSQL,192.168.53.76:5432,app_user,YourPassword1!,orders,require
用户库-预发,预发,MySQL,192.168.53.77:3306,app_user,YourPassword2!,,
`

export const TEMPLATE_MD = `# 实例批量导入 — Markdown 模板

> 📘 请按以下要求填写：第 1 个 Markdown 表格会被解析为数据，表格上方的所有说明文字会被自动忽略。

## 一、填写要求

| 项目 | 说明 |
| --- | --- |
| 列顺序 | **无要求**，按列名匹配即可（列名同时支持中文/英文别名：数据源名称/remark、环境/environment…） |
| 必填列 6 个 | 数据源名称 / 环境 / 数据库类型 / 连接地址 / 登录账号 / 密码 |
| 可选列 2 个 | 默认数据库 / SSL模式（留空时 PostgreSQL 取 postgres、SQLServer 取 master、MySQL 不指定；SSL模式仅 PostgreSQL 生效） |
| 数据库类型 | 支持 **MySQL / PostgreSQL / SQLServer**；可识别别名：mysqld/mariadb/tidb → MySQL；pg/postgres → PostgreSQL；mssql → SQLServer |
| 环境 | 填写环境名称（大小写不敏感但必须精确相等，例：开发 / 测试 / 预发 / 生产）；也可填环境 ID |
| 连接地址 | 推荐写法：**主机:端口**（例 192.168.53.76:5432 或 db.example.com:3306）；也可拆为「主机」+「端口」两列 |
| 单次上限 | 最多 500 行；粘贴文本或文件大小均 ≤ 5MB；仅 UTF-8 编码 |
| 密码安全 | 模板中密码为明文，请妥善保管文件；导入成功后系统加密存储，所有导出文件都不会回显真实密码 |

## 二、导入数据（请删除上方示例行后再填写真实连接；SSL 模式可选值 disable/prefer/require）

| 数据源名称 | 环境 | 数据库类型 | 连接地址 | 登录账号 | 密码 | 默认数据库 | SSL模式 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 订单库主库 | 生产 | PostgreSQL | 192.168.53.76:5432 | app_user | YourPassword1! | orders | require |
| 用户库-预发 | 预发 | MySQL | 192.168.53.77:3306 | app_user | YourPassword2! | | |
`

export const MAX_ROWS = 500

const HOST_RE = /^[A-Za-z0-9.-]+$/

export const normalizeDbType = (raw) => {
  const key = String(raw || '').trim().toLowerCase()
  if (!key) return ''
  return DB_TYPE_ALIASES[key] || ''
}

const buildEnvIndex = (environments = []) => {
  const byName = new Map()
  const byID = new Map()
  for (const env of environments || []) {
    if (!env || (env.id === undefined && env.name === undefined)) continue
    const id = env.id ?? env.value ?? env.key
    const name = env.name ?? env.label ?? env.title
    if (name !== undefined && name !== null && String(name).trim() !== '') {
      byName.set(String(name).trim().toLowerCase(), id)
    }
    if (id !== undefined && id !== null && String(id).trim() !== '') {
      byID.set(String(id), id)
    }
  }
  return { byName, byID }
}

export const resolveEnvironment = (raw, environments) => {
  if (raw === undefined || raw === null) return { ok: false, value: undefined, name: '' }
  const { byName, byID } = buildEnvIndex(environments)
  const strVal = String(raw).trim()
  if (!strVal) return { ok: false, value: undefined, name: '' }
  if (byName.has(strVal.toLowerCase())) {
    return { ok: true, value: byName.get(strVal.toLowerCase()), name: strVal }
  }
  const num = Number(strVal)
  if (Number.isFinite(num) && Number.isInteger(num)) {
    if (byID.has(String(num))) {
      return { ok: true, value: num, name: strVal }
    }
  }
  return { ok: false, value: undefined, name: strVal }
}

export const parseCSV = (raw) => {
  const content = String(raw || '').replace(/\r\n/g, '\n').replace(/^\uFEFF/, '').trim()
  if (!content) throw new Error('CSV 内容为空')
  const allLines = content.split('\n').map((line) => line.replace(/\r$/, ''))
  const dataLines = []
  for (const line of allLines) {
    const trim = line.trim()
    if (!trim) continue
    if (trim.startsWith('#')) continue
    dataLines.push(line)
  }
  if (dataLines.length < 2) throw new Error('CSV 至少需要表头 + 1 行数据')
  const headers = splitCSVLine(dataLines[0]).map((h) => h.trim())
  const rows = []
  for (let i = 1; i < dataLines.length; i++) {
    const cells = splitCSVLine(dataLines[i])
    const row = {}
    for (let j = 0; j < headers.length; j++) {
      const key = headers[j]
      if (!key) continue
      row[key] = (j < cells.length ? cells[j] : '').trim()
    }
    rows.push(row)
  }
  if (!rows.length) throw new Error('CSV 无有效数据行')
  return rows
}

const splitCSVLine = (line) => {
  const out = []
  let cur = ''
  let inQuote = false
  for (let i = 0; i < line.length; i++) {
    const ch = line[i]
    if (ch === '"') {
      if (inQuote && line[i + 1] === '"') {
        cur += '"'
        i++
        continue
      }
      inQuote = !inQuote
      continue
    }
    if (ch === ',' && !inQuote) {
      out.push(cur)
      cur = ''
      continue
    }
    cur += ch
  }
  out.push(cur)
  return out
}

const REQUIRED_HEADERS_CN = ['数据源名称', '环境', '数据库类型', '连接地址', '登录账号', '密码']

const isLikelySeparatorRow = (cells) => {
  if (!cells || !cells.length) return false
  let dashCount = 0
  for (const c of cells) {
    const s = String(c || '').trim()
    if (s === '') continue
    if (/^:?-{3,}:?$/.test(s.replace(/\s+/g, ''))) dashCount++
  }
  return dashCount >= Math.ceil(cells.length / 2)
}

const pickDataTable = (tableBlocks) => {
  if (!tableBlocks.length) return null
  for (const block of tableBlocks) {
    if (block.length < 3) continue
    const headerCells = block[0].split('|').map((h) => h.trim())
    const sepCells = block[1].split('|').map((h) => h.trim())
    if (!isLikelySeparatorRow(sepCells)) continue
    const hit = REQUIRED_HEADERS_CN.filter((h) => headerCells.includes(h)).length
    if (hit >= 4) return block
    const headerSet = new Set(headerCells.map((h) => String(h).toLowerCase()))
    const aliasHits = [
      ['数据源名称', 'remark', 'name', '名称'],
      ['环境', 'environment', 'env', '环境名称'],
      ['数据库类型', 'db_type', 'dbtype', '类型'],
      ['连接地址', 'connection_address', 'address', '地址', 'hostport'],
      ['登录账号', 'user', 'username', '账号', '用户名'],
      ['密码', 'password', 'passwd', 'pwd'],
    ].filter((list) => list.some((k) => headerSet.has(k.toLowerCase()))).length
    if (aliasHits >= 5) return block
  }
  return tableBlocks[tableBlocks.length - 1]
}

const splitPipe = (line) => String(line || '').replace(/^\|/, '').replace(/\|$/, '')

export const parseMarkdown = (raw) => {
  const content = String(raw || '').replace(/\r\n/g, '\n').trim()
  if (!content) throw new Error('Markdown 内容为空')
  const lines = content.split('\n')
  const tableBlocks = []
  let buf = null
  for (const line of lines) {
    const trim = line.trim()
    if (trim.startsWith('|')) {
      if (!buf) buf = []
      buf.push(splitPipe(trim))
    } else {
      if (buf && buf.length >= 3) tableBlocks.push(buf)
      buf = null
    }
  }
  if (buf && buf.length >= 3) tableBlocks.push(buf)
  const block = pickDataTable(tableBlocks)
  if (!block || block.length < 3) {
    throw new Error('Markdown 表格解析失败：未找到含（数据源名称/环境/数据库类型/连接地址/登录账号/密码）列的数据表格，请参考 MD 模板的第二节【导入数据】表格格式。')
  }
  const headers = block[0].split('|').map((h) => h.trim())
  const separator = block[1].split('|').map((h) => h.trim())
  if (!isLikelySeparatorRow(separator)) {
    throw new Error('Markdown 表头分隔线格式错误，下一行应为 | --- | --- | （可含冒号左右对齐）')
  }
  if (headers.length !== separator.length && headers.length > separator.length) {
    throw new Error('Markdown 表头与分隔线列数不一致')
  }
  const rows = []
  for (let i = 2; i < block.length; i++) {
    const cells = block[i].split('|').map((c) => c.trim())
    if (cells.every((c) => !c)) continue
    const row = {}
    for (let j = 0; j < headers.length; j++) {
      const key = headers[j]
      if (!key) continue
      row[key] = j < cells.length ? cells[j] : ''
    }
    rows.push(row)
  }
  if (!rows.length) throw new Error('Markdown 表格无有效数据行')
  return rows
}

export const detectFormat = ({ fileName = '', content = '' } = {}) => {
  const file = String(fileName || '').toLowerCase()
  if (file.endsWith('.csv')) return 'csv'
  if (file.endsWith('.md') || file.endsWith('.markdown')) return 'markdown'
  const raw = String(content || '').trim()
  if (raw.startsWith('|') || raw.includes('\n|')) {
    const firstPipeIdx = raw.indexOf('|')
    const windowSlice = raw.slice(firstPipeIdx, firstPipeIdx + 220)
    if (/---/.test(windowSlice)) return 'markdown'
  }
  return 'csv'
}

export const parseByFormat = ({ format, fileName, content }) => {
  const fmt = (format || 'auto') === 'auto' ? detectFormat({ fileName, content }) : format
  if (fmt === 'csv') return parseCSV(content)
  if (fmt === 'markdown') return parseMarkdown(content)
  throw new Error(`不支持的格式: ${fmt}`)
}

const firstNonEmpty = (row, keys) => {
  for (const k of keys) {
    const val = row?.[k] ?? undefined
    if (val === undefined || val === null) continue
    const str = String(val).trim()
    if (str !== '') return str
  }
  return ''
}

const asInt = (v) => {
  if (v === undefined || v === null) return NaN
  if (typeof v === 'number') return Number.isFinite(v) ? v : NaN
  const s = String(v).trim()
  if (!s) return NaN
  const n = Number(s)
  return Number.isFinite(n) && Number.isInteger(n) ? n : NaN
}

export const normalizeRow = (rawRow, environments) => {
  const row = {}
  for (const k of Object.keys(rawRow || {})) {
    row[String(k).trim()] = rawRow[k]
  }
  const remark = firstNonEmpty(row, ['数据源名称', 'remark', 'name', '名称'])
  const envRaw = firstNonEmpty(row, ['环境', 'environment', '环境名称', 'env'])
  const dbTypeRaw = firstNonEmpty(row, ['数据库类型', 'db_type', 'dbType', '类型'])
  const connectionAddress = firstNonEmpty(row, ['连接地址', 'connection_address', 'address', '地址', 'hostport'])
  const user = firstNonEmpty(row, ['登录账号', 'user', 'username', '账号', '用户名'])
  const password = firstNonEmpty(row, ['密码', 'password', 'passwd', 'pwd'])
  const database = firstNonEmpty(row, ['默认数据库', 'database', 'db'])
  const sslMode = firstNonEmpty(row, ['SSL模式', 'ssl_mode', 'sslmode', 'ssl'])

  const errors = []
  const dbType = normalizeDbType(dbTypeRaw)
  if (!dbType) errors.push(`数据库类型字段缺失或无法识别（${dbTypeRaw || ''}）`)

  let hostname = ''
  let port = 0
  if (connectionAddress) {
    const idx = String(connectionAddress).trim().lastIndexOf(':')
    if (idx <= 0 || idx === String(connectionAddress).length - 1) {
      errors.push(`连接地址格式错误，应为 host:port（${connectionAddress || ''}）`)
    } else {
      hostname = String(connectionAddress).slice(0, idx).trim()
      const portNum = asInt(String(connectionAddress).slice(idx + 1))
      if (Number.isNaN(portNum) || portNum < 1 || portNum > 65535) {
        errors.push(`连接地址端口非法（${connectionAddress || ''}）`)
      } else {
        port = portNum
      }
    }
  } else {
    hostname = firstNonEmpty(row, ['主机', 'hostname', 'host', '主机名', 'ip'])
    const portStr = firstNonEmpty(row, ['端口', 'port'])
    const portNum = asInt(portStr)
    if (!hostname || Number.isNaN(portNum) || portNum < 1 || portNum > 65535) {
      errors.push('缺少连接地址字段或端口非法')
    } else {
      port = portNum
    }
  }
  if (hostname && !HOST_RE.test(hostname)) {
    errors.push(`主机名或IP格式不合法（${hostname}）`)
  }
  if (!remark) errors.push('数据源名称不能为空')
  else if (remark.length < 3 || remark.length > 256) errors.push('数据源名称长度需在 3 到 256 个字符之间')

  if (!user || user.length > 128) errors.push('登录账号长度需在 1 到 128 个字符之间')
  else if (/[\r\n\t]/.test(user)) errors.push('登录账号不能包含换行或制表符')
  if (!password || password.length > 256) errors.push('密码长度需在 1 到 256 个字符之间')
  else if (/[\r\n\t]/.test(password)) errors.push('密码不能包含换行或制表符')

  const envInfo = resolveEnvironment(envRaw, environments)
  if (!envInfo.ok) {
    if (!envRaw) {
      const envIDStr = String(row?.environment_id ?? row?.environment ?? '').trim()
      const num = Number(envIDStr)
      if (envIDStr && Number.isFinite(num) && Number.isInteger(num)) {
        envInfo.value = num
        envInfo.ok = true
      }
    }
    if (!envInfo.ok) errors.push(`环境「${envRaw || ''}」不存在或未启用`)
  }

  if (dbType && !ALLOWED_DB_TYPES.has(dbType)) {
    errors.push(`当前批量导入仅支持 MySQL / PostgreSQL / SQLServer，不支持「${dbTypeRaw || ''}」`)
  }

  const extra = {}
  if (dbType === 'PostgreSQL') {
    if (database) {
      if (database.length > 128) errors.push('默认数据库不能超过 128 个字符')
      else extra.database = database
    } else {
      extra.database = 'postgres'
    }
    extra.sslmode = sslMode || 'disable'
  } else if (dbType === 'MySQL') {
    if (database) {
      if (database.length > 128) errors.push('默认数据库不能超过 128 个字符')
      else extra.database = database
    }
  } else if (dbType === 'SQLServer') {
    // SQL Server 登录必须有默认库上下文，留空兜底 master；
    // SSL模式 对 SQL Server 无意义（连接串用 encrypt/trustservercertificate），忽略该列。
    if (database) {
      if (database.length > 128) errors.push('默认数据库不能超过 128 个字符')
      else extra.database = database
    } else {
      extra.database = 'master'
    }
  }

  return {
    ok: errors.length === 0,
    errors,
    normalized: {
      remark,
      environment: envInfo.ok ? envInfo.value : undefined,
      environment_name: envInfo.name,
      db_type: dbType,
      hostname,
      port,
      user,
      password,
      database,
      sslmode: sslMode || '',
      extra_config: extra,
    },
  }
}

export const buildDuplicateIndex = (rows = []) => {
  const set = new Set()
  for (const r of rows || []) {
    const h = String(r?.hostname || r?.host || '').trim().toLowerCase()
    const p = Number(r?.port)
    if (!h || !Number.isFinite(p)) continue
    set.add(`${h}:${p}`)
  }
  return set
}

export const buildPayloadFromNormalized = (n) => ({
  remark: n.remark,
  environment: n.environment,
  db_type: n.db_type,
  hostname: n.hostname,
  port: n.port,
  user: n.user,
  password: n.password,
  extra_config: n.extra_config || {},
})

export const triggerDownload = (filename, content, mime = 'text/plain;charset=utf-8') => {
  const blob = new Blob(['\uFEFF' + content], { type: mime })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export const downloadTemplate = (format = 'csv') => {
  if (format === 'markdown') {
    triggerDownload('instance-import-template.md', TEMPLATE_MD, 'text/markdown;charset=utf-8')
  } else {
    triggerDownload('instance-import-template.csv', TEMPLATE_CSV, 'text/csv;charset=utf-8')
  }
}

const toCSVCell = (val) => {
  const str = String(val === null || val === undefined ? '' : val)
  if (/[",\n\r]/.test(str)) return `"${str.replace(/"/g, '""')}"`
  return str
}

export const buildFailedRowsCSV = (rows = []) => {
  const headers = [...INSTANCE_BATCH_HEADERS_CN, '失败原因']
  const lines = [headers.join(',')]
  for (const r of rows || []) {
    const payload = r?.payload || {}
    const cells = []
    cells.push(toCSVCell(payload['数据源名称'] ?? payload.remark ?? r.remark ?? ''))
    cells.push(toCSVCell(payload['环境'] ?? payload.environment ?? r.key ?? ''))
    cells.push(toCSVCell(payload['数据库类型'] ?? payload.db_type ?? ''))
    const conn =
      payload['连接地址'] ||
      payload.connection_address ||
      (payload.hostname && payload.port ? `${payload.hostname}:${payload.port}` : '')
    cells.push(toCSVCell(conn))
    cells.push(toCSVCell(payload['登录账号'] ?? payload.user ?? ''))
    cells.push('******')
    cells.push(toCSVCell(payload['默认数据库'] ?? payload.database ?? ''))
    cells.push(toCSVCell(payload['SSL模式'] ?? payload.sslmode ?? ''))
    cells.push(toCSVCell(r.reason || ''))
    lines.push(cells.join(','))
  }
  return lines.join('\n')
}

export const generateBatchID = () => {
  const now = new Date()
  const pad = (n) => String(n).padStart(2, '0')
  const t = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
  const rand = Math.floor(Math.random() * 1000)
    .toString()
    .padStart(3, '0')
  return `BI-${t}-${rand}`
}

export const HEADER_MAP = HEADER_CN_TO_KEY
