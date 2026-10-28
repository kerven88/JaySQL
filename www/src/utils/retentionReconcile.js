import { h } from 'vue'
import { Modal } from 'ant-design-vue'
import { ExclamationCircleOutlined } from '@ant-design/icons-vue'

// 备份任务「保存时间对齐」二次确认。
//
// 保留期在创建执行记录（insight_backup_runs.retention_days）与备份集
// （insight_backup_sets.expire_at）时被快照写入，历史上修改任务不会回溯。后端在保存
// 时会把这些历史记录对齐到任务当前保留期；若对齐后有备份立刻超出保留期，会返回
// requires_confirm + 影响明细（run_count / set_count / oldest_at ...），由这里弹确认框，
// 用户确认后带 confirm_retention_reconcile=true 重试一次。

const CANCELED_FLAG = '__retentionReconcileCanceled'

// 弹窗被 teleport 到 body，CSS 变量可能取不到，统一带十六进制兜底
const LEAD_STYLE = { fontSize: '13px', lineHeight: '1.9', color: 'var(--gi-text-primary, #2c2c2a)' }
const TEXT_STYLE = { fontSize: '13px', lineHeight: '1.9', color: 'var(--gi-text-secondary, #5f5e5a)' }
const WARN_STYLE = { marginTop: '6px', fontSize: '13px', lineHeight: '1.9', color: 'var(--gi-error, #a32d2d)' }
const TIP_STYLE = { marginTop: '10px', fontSize: '12px', lineHeight: '1.8', color: 'var(--gi-text-tertiary, #888780)' }

const formatSize = (bytes) => {
  const value = Number(bytes || 0)
  if (!Number.isFinite(value) || value <= 0) return ''
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let size = value
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index += 1
  }
  const text = index === 0 ? String(Math.round(size)) : size >= 100 ? size.toFixed(0) : size.toFixed(1)
  return `${text} ${units[index]}`
}

// request.js 在业务失败时 reject 的是响应体 { request_id, code, data, message }，
// 网络层错误则可能是原始 error，这里两种形态都兜住。
export const retentionReconcileImpact = (error) => {
  if (!error || typeof error !== 'object') return null
  for (const item of [error.data, error]) {
    if (item && typeof item === 'object' && item.requires_confirm === true) return item
  }
  return null
}

export const isRetentionReconcileCanceled = (error) => !!(error && error[CANCELED_FLAG])

const buildContent = (impact) => {
  const blocks = []
  // 任务保留期可能本就等于目标值（历史快照偏大），这时重点说明「对齐」而不是「改动」
  const unchanged = Number(impact.current_days) === Number(impact.target_days)
  blocks.push(
    h(
      'div',
      { style: LEAD_STYLE },
      unchanged
        ? `保存时间：${impact.target_days} 天，按该保留期对齐历史备份`
        : `保存时间：${impact.current_days} 天 → ${impact.target_days} 天`,
    ),
  )

  const parts = []
  if (Number(impact.set_count) > 0) parts.push(`备份集 ${impact.set_count} 个`)
  if (Number(impact.run_count) > 0) parts.push(`执行记录 ${impact.run_count} 条`)
  if (parts.length) {
    blocks.push(h('div', { style: WARN_STYLE }, `保存后立即清理：${parts.join('、')}`))
  }

  if (impact.oldest_at) {
    const range =
      impact.latest_at && impact.latest_at !== impact.oldest_at
        ? `${impact.oldest_at} ~ ${impact.latest_at}`
        : impact.oldest_at
    blocks.push(h('div', { style: TEXT_STYLE }, `备份时间范围：${range}`))
  }

  const sizeText = formatSize(impact.size_bytes)
  if (sizeText) blocks.push(h('div', { style: TEXT_STYLE }, `连带释放空间：约 ${sizeText}`))

  const samples = Array.isArray(impact.sample_names) ? impact.sample_names.filter(Boolean) : []
  if (samples.length) {
    const more = Number(impact.set_count) > samples.length ? ' 等' : ''
    blocks.push(h('div', { style: TEXT_STYLE }, `包含：${samples.join('、')}${more}`))
  }

  blocks.push(
    h('div', { style: TIP_STYLE }, '备份文件、日志与参数文件会一并从存储目标上删除，删除后无法恢复。'),
  )
  if (Number(impact.running_run_count) > 0) {
    blocks.push(
      h('div', { style: TIP_STYLE }, `另有 ${impact.running_run_count} 条执行记录正在执行，不受本次调整影响。`),
    )
  }
  return h('div', null, blocks)
}

export const confirmRetentionReconcile = (impact) =>
  new Promise((resolve) => {
    let settled = false
    const finish = (value) => {
      if (settled) return
      settled = true
      resolve(value)
    }
    Modal.confirm({
      title: '确认缩短保存时间？',
      icon: () => h(ExclamationCircleOutlined, { style: { color: 'var(--gi-warning, #faad14)' } }),
      width: 520,
      content: () => buildContent(impact),
      okText: '确认并清理',
      okType: 'danger',
      cancelText: '取消',
      onOk: () => finish(true),
      onCancel: () => finish(false),
    })
  })

// 提交任务更新；后端要求确认保留期影响时弹确认框并带确认标记重试一次。
// 用户取消时抛出带 CANCELED 标记的错误，调用方静默 return 即可（不要弹报错）。
export const updateBackupTaskWithRetentionConfirm = async (updateApi, payload, config = {}) => {
  try {
    return await updateApi(payload, config)
  } catch (error) {
    const impact = retentionReconcileImpact(error)
    if (!impact) throw error
    const confirmed = await confirmRetentionReconcile(impact)
    if (!confirmed) {
      const canceled = new Error('已取消保存时间调整')
      canceled[CANCELED_FLAG] = true
      throw canceled
    }
    return await updateApi({ ...payload, confirm_retention_reconcile: true }, config)
  }
}
