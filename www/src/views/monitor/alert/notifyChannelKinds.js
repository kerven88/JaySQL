// 通知渠道类型的唯一公共定义：告警配置下所有涉及「渠道类型」的场景
// （新建/编辑下拉、类型列回显、类型筛选）都从这里取名称与顺序，
// 保证四处展示的选项名称、顺序、取值完全一致。
//
// 注意：这里是展示口径，不是能力口径——各渠道的表单字段、发送逻辑
// 仍由后端 NotifyChannelKinds() 元数据驱动，新增渠道类型时：
//   1. 后端 meta 注册（fields/description 正常下发）；
//   2. 在 NOTIFY_CHANNEL_KINDS 末尾追加一项即可获得统一展示。
export const NOTIFY_CHANNEL_KINDS = [
  { kind: 'dingtalk', label: '钉钉' },
  // 钉钉企业应用：与上面的「钉钉」（群机器人）不是同一个东西。
  // 群机器人只能把消息发到群里，无法定向发给个人；企业应用拿得到 userid，
  // 可以把互动卡片直接投给审批人本人，因此工单审批走的是这个类型。
  // 两者字段与能力不同（由后端 meta 驱动），故在列表里各占一行、并存不互斥。
  { kind: 'dingtalk_app', label: '钉钉企业应用' },
  { kind: 'feishu', label: '飞书' },
  { kind: 'wechat', label: '企微' },
  { kind: 'email', label: '邮箱' },
]

// UI 侧隐藏的渠道类型：仅前端展示层收起（下拉/筛选/列表/策略渠道选择），
// 后端能力与枚举完整保留。重新启用时把对应项从本数组移除即可。
export const NOTIFY_CHANNEL_KINDS_UI_HIDDEN = ['email']

export const NOTIFY_CHANNEL_KINDS_VISIBLE = NOTIFY_CHANNEL_KINDS.filter(
  (item) => !NOTIFY_CHANNEL_KINDS_UI_HIDDEN.includes(item.kind)
)

// 渠道类型是否在 UI 可见：隐藏类型已有数据（如历史渠道行）也不在界面展示。
export function notifyChannelKindVisible(kind) {
  return !NOTIFY_CHANNEL_KINDS_UI_HIDDEN.includes(kind)
}

// kind → 展示名 的映射表（O(1) 回显）。
export const NOTIFY_CHANNEL_KIND_LABELS = Object.fromEntries(
  NOTIFY_CHANNEL_KINDS.map((item) => [item.kind, item.label])
)

// 渠道类型展示名：公共常量优先；未收录的新类型回落到后端 meta.label，
// 再兜底原始 kind，保证不会因为漏登记而显示成空白。
export function notifyChannelKindLabel(kind, backendLabel) {
  return NOTIFY_CHANNEL_KIND_LABELS[kind] || backendLabel || kind
}
