// 大模型接入页面的公共映射与工具

// Provider 状态机文案（与后端 llm_providers.status 对应）
export const PROVIDER_STATUS_META = {
  draft: { text: '待验证', color: 'default', tip: '尚未通过连通性验证，不可启用' },
  verified: { text: '已验证', color: 'blue', tip: '凭证有效，尚未启用' },
  enabled: { text: '已启用', color: 'success', tip: '已开放给 AI 助手使用' },
  disabled: { text: '已停用', color: 'default', tip: '配置已保留，暂不参与调用' },
  degraded: { text: '降级', color: 'warning', tip: '最近一次检查失败，已从主用链路降级' },
}

// 健康状态
export const HEALTH_META = {
  unknown: { text: '未检查', color: 'default' },
  healthy: { text: '正常', color: 'success' },
  degraded: { text: '降级', color: 'warning' },
  down: { text: '不可用', color: 'error' },
}

// 模型能力标签
export const CAPABILITY_LABELS = {
  chat: '对话',
  stream: '流式',
  tool_call: '工具调用',
  json_mode: 'JSON 模式',
  vision: '视觉',
  embedding: '向量',
}

// 厂商分类
export const VENDOR_CATEGORY_META = {
  international: { text: '海外模型', color: 'blue' },
  domestic: { text: '国内模型', color: 'green' },
  private: { text: '私有化 / 本地', color: 'purple' },
  custom: { text: '自定义', color: 'orange' },
}

// 校验错误分类 → 排查建议（后端 category 字段）
export const VALIDATION_CATEGORY_META = {
  config: { text: '配置错误', color: 'error' },
  dns: { text: '域名解析失败', color: 'error' },
  tcp: { text: '连接失败 / 超时', color: 'error' },
  tls: { text: '证书校验失败', color: 'error' },
  http: { text: '服务返回异常', color: 'error' },
  auth: { text: '鉴权失败', color: 'error' },
  rate_limit: { text: '触发限流', color: 'warning' },
  quota: { text: '额度不足', color: 'warning' },
  model_not_found: { text: '模型不存在', color: 'warning' },
  bad_request: { text: '请求被拒绝', color: 'warning' },
  unknown: { text: '未知错误', color: 'default' },
}

// 动态表单支持的字段类型（与后端白名单保持一致）
export const FIELD_TYPE_OPTIONS = [
  { value: 'string', label: '文本' },
  { value: 'password', label: '密码（掩码显示）' },
  { value: 'number', label: '数字' },
  { value: 'boolean', label: '开关' },
  { value: 'select', label: '下拉选择' },
]

export const ADAPTER_OPTIONS = [
  { value: 'openai_compat', label: 'openai_compat（OpenAI 兼容，覆盖多数厂商）' },
  { value: 'azure_openai', label: 'azure_openai（Azure OpenAI）' },
  { value: 'anthropic', label: 'anthropic（Claude Messages API）' },
  { value: 'openai_custom', label: 'openai_custom（自定义鉴权 / 企业网关）' },
]

// 安全解析 JSON 字段：后端 datatypes.JSON 可能为 null / 数组 / 对象 / 字符串
export const parseJSONField = (value, fallback) => {
  if (value === null || value === undefined || value === '') return fallback
  if (typeof value === 'object') return value
  try {
    const parsed = JSON.parse(value)
    return parsed === null ? fallback : parsed
  } catch {
    return fallback
  }
}

export const asArray = (value) => {
  const parsed = parseJSONField(value, [])
  return Array.isArray(parsed) ? parsed : []
}

// 用模板的 extra_fields 默认值构造 extra_config 初始值
export const buildExtraConfigFromTemplate = (template) => {
  const fields = asArray(template?.extra_fields)
  const result = {}
  fields.forEach((field) => {
    if (!field?.key) return
    if (field.default !== undefined && field.default !== null) {
      result[field.key] = field.default
    }
  })
  return result
}

// 新建向导的初始表单
export const buildProviderFormFromTemplate = (template) => ({
  vendor: template?.vendor || '',
  display_name: template?.label || '',
  base_url: template?.default_base_url || '',
  auth_mode: template?.auth_mode || 'bearer',
  auth_header_name: template?.auth_header_name || '',
  api_key: '',
  timeout_seconds: 60,
  max_retries: 1,
  is_shared: true,
  remark: '',
  extra_config: buildExtraConfigFromTemplate(template),
})

// 模板是否把 api_key 列为必填
export const templateRequiresAPIKey = (template) => {
  const required = asArray(template?.required_fields)
  if (required.includes('api_key')) return true
  const fields = asArray(template?.extra_fields)
  const keyField = fields.find((item) => item?.key === 'api_key')
  if (keyField && keyField.required === true && keyField.secret !== true) return true
  return false
}
