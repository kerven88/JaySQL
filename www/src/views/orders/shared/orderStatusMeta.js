const ORDER_STATUS_META = {
  WAIT_PRECHECK: { text: '待预审', color: 'default' },
  PENDING: { text: '审批中', color: 'gold' },
  APPROVED: { text: '审批通过', color: 'blue' },
  REJECTED: { text: '审批驳回', color: 'red' },
  EXECUTING: { text: '执行中', color: 'orange' },
  FAILED: { text: '执行失败', color: 'volcano' },
  COMPLETED: { text: '执行成功', color: 'green' },
  TERMINATED: { text: '已终止', color: 'default' },
  WITHDRAWN: { text: '已撤回', color: 'orange' },
}

const TASK_STATUS_META = {
  PENDING: { text: '待执行', color: 'default' },
  EXECUTING: { text: '执行中', color: 'orange' },
  PAUSED: { text: '已暂停', color: 'default' },
  FAILED: { text: '已失败', color: 'red' },
  COMPLETED: { text: '已完成', color: 'green' },
}

export const ORDER_PROGRESS_OPTIONS = [
  { value: 'WAIT_PRECHECK', label: '待预审' },
  { value: 'PENDING', label: '审批中' },
  { value: 'APPROVED', label: '审批通过' },
  { value: 'REJECTED', label: '审批驳回' },
  { value: 'EXECUTING', label: '执行中' },
  { value: 'FAILED', label: '执行失败' },
  { value: 'COMPLETED', label: '执行成功' },
  { value: 'TERMINATED', label: '已终止' },
  { value: 'WITHDRAWN', label: '已撤回' },
]

export const TASK_PROGRESS_OPTIONS = [
  { value: 'PENDING', label: '待执行' },
  { value: 'EXECUTING', label: '执行中' },
  { value: 'COMPLETED', label: '已完成' },
  { value: 'FAILED', label: '已失败' },
]

export const getOrderStatusMeta = (status) =>
  ORDER_STATUS_META[status] || { text: status, color: 'default' }

export const getTaskStatusMeta = (status) =>
  TASK_STATUS_META[status] || { text: status, color: 'default' }
