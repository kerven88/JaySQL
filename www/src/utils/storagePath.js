/**
 * 备份「产物路径 / 日志路径」展示口径的唯一入口。
 *
 * 规则（全局一致，任何页面展示这两个字段都必须走这里）：
 * 1) 存储 URI  st:<targetId>:///<rel>  → 解析存储目标 →「地址 + 根路径 + 相对路径」
 * 2) 绝对路径  /xxx/yyy                → 直接作为路径（缺失根路径时补上根路径）
 * 3) 相对路径  xxx/yyy                 → 「根路径 + 相对路径」
 * 4) 地址与路径之间**恰好一个冒号**；地址为空（目标缺失/未配置地址）则只显示路径
 * 5) 空值统一返回 '-'
 *
 * 地址的取值：
 * - LOCAL_SERVER：host（不含 SSH 端口，端口是登录用的，不是路径的一部分）
 * - 对象存储（OSS/COS/OBS/MinIO）：s3_endpoint（去掉 http(s):// 与尾部斜杠）
 *
 * 根路径的取值：base_path 优先；对象存储未配 base_path 时用 bucket。
 */

const ST_URI_RE = /^st:(\d+):\/\/\/(.*)$/
const SCHEME_RE = /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//
const OBJECT_STORAGE_TYPES = ['OSS_ALIYUN', 'S3_MINIO', 'COS_TENCENT', 'OBS_HUAWEI']

export const isObjectStorageTarget = (target) =>
  OBJECT_STORAGE_TYPES.includes(String(target?.type || '').toUpperCase())

/** 从 st:<id>:///... 中取出存储目标 ID；不是存储 URI 时返回 0 */
export const extractStorageTargetId = (value) => {
  const matched = String(value ?? '').trim().match(ST_URI_RE)
  return matched ? Number(matched[1]) || 0 : 0
}

/** 压缩多余斜杠并保证以单个 / 开头、不以 / 结尾（'/' 本身保持） */
const normalizePath = (value) => {
  const text = String(value ?? '').trim()
  if (!text) return ''
  const trimmed = text.replace(/^\/+/, '').replace(/\/+$/, '')
  if (!trimmed) return '/'
  return `/${trimmed}`.replace(/\/{2,}/g, '/')
}

/** 存储目标地址；无法解析时返回空串（调用方据此只显示路径） */
export const storageTargetAddress = (target) => {
  if (!target) return ''
  if (isObjectStorageTarget(target)) {
    const endpoint = String(target.s3_endpoint || '').trim()
    if (!endpoint) return ''
    return endpoint.replace(SCHEME_RE, '').replace(/\/+$/, '')
  }
  return String(target.host || '').trim().replace(/\/+$/, '')
}

/** 存储目标根路径（备份根路径；对象存储未配置时退化为 bucket） */
export const storageTargetRoot = (target) => {
  if (!target) return ''
  const base = String(target.base_path || '').trim()
  if (base) return normalizePath(base)
  if (isObjectStorageTarget(target)) {
    const bucket = String(target.s3_bucket || '').trim().replace(/^\/+|\/+$/g, '')
    return bucket ? `/${bucket}` : ''
  }
  return ''
}

/**
 * 把原始路径值格式化为「地址:路径」的完整路径。
 * @param {string} value  原始值（st: URI / 绝对路径 / 相对路径）
 * @param {object|null} target 存储目标对象（含 type/host/base_path/s3_endpoint/s3_bucket）
 * @returns {string} 完整路径；空值返回 '-'
 */
export const formatStoragePath = (value, target) => {
  const raw = String(value ?? '').trim()
  if (!raw) return '-'

  const address = storageTargetAddress(target)
  const root = storageTargetRoot(target)

  // 已经是「地址:路径」形态（含地址等于整串的情况）→ 原样返回，杜绝重复冒号
  if (address && (raw === address || raw.startsWith(`${address}:/`))) {
    return raw
  }

  const uri = raw.match(ST_URI_RE)
  if (uri) {
    const relative = String(uri[2] || '').trim()
    const path = normalizePath(`${root}/${relative}`)
    if (!path || path === '/') return '-'
    return address ? `${address}:${path}` : path
  }

  let path = ''
  if (raw.startsWith('/')) {
    const absolute = normalizePath(raw)
    // 绝对路径没有带上根路径时才补齐，避免重复拼接
    path =
      root && root !== '/' && absolute !== root && !absolute.startsWith(`${root}/`)
        ? normalizePath(root + absolute)
        : absolute
  } else {
    path = normalizePath(`${root}/${raw}`)
  }

  if (!path || path === '/') return '-'
  return address ? `${address}:${path}` : path
}

export default formatStoragePath
