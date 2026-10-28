<template>
  <div v-if="compact" class="order-info-card" :class="{ 'is-collapsed': !moreOpen }">
    <!-- ====== 第 1 行：标题 + 工单ID + 复制icon + 状态 tag ====== -->
    <div class="header-title-row">
      <div class="title-left-group">
        <AppTooltip
          :title="orderDetail.title || ''"
          placement="bottomLeft"
          overlay-class-name="order-detail-title-tip gi-style"
        >
          <span class="detail-title-text detail-title-text--ellipsis">
            {{ displayTitleShort }}
          </span>
        </AppTooltip>
        <div v-if="orderDetail.order_id" class="order-id-block">
          <span class="detail-order-id">{{ orderDetail.order_id }}</span>
          <button
            type="button"
            class="copy-order-id-btn"
            :aria-label="'复制工单ID'"
            :title="'复制工单ID'"
            @click.stop="copyOrderId"
          >
            <CopyOutlined />
          </button>
          <a-tag
            v-if="orderDetail.progress"
            :color="getOrderStatusMeta(orderDetail.progress).color"
            class="detail-status-tag detail-status-tag-inline"
          >
            {{ getOrderStatusMeta(orderDetail.progress).text }}
          </a-tag>
        </div>
      </div>
      <a-tag
        v-if="orderDetail.progress && !orderDetail.order_id"
        :color="getOrderStatusMeta(orderDetail.progress).color"
        class="detail-status-tag"
      >
        {{ getOrderStatusMeta(orderDetail.progress).text }}
      </a-tag>
    </div>

    <!-- ====== 第 2 行：左侧创建者信息 + 中间灰胶囊组（数据源/数据库/Schema）+ 右侧执行人 + 【更多/收起】按钮 ====== -->
    <div class="header-content-row">
      <!-- 2.1 左侧：申请人 + 创建时间 -->
      <div class="creator-info">
        <span class="creator-name">{{ orderDetail.applicant || '-' }}</span>
        <span class="creator-sep">创建于</span>
        <span class="creator-time">{{ orderDetail.created_at || '-' }}</span>
      </div>

      <!-- 2.2 中间：标签胶囊组（数据源 + 数据库 + Schema） -->
      <div class="tag-group">
        <span class="info-chip">
          <span class="chip-content">
            <span class="chip-label chip-label--inline">数据源</span>
            <!-- 数据源格式「实例名称 IP:Port」：名称>15 字符、IP>30 字符分别 ... 截断，完整内容见 tooltip -->
            <AppTooltip :title="displayInstanceFullText">
              <span class="ds-full-text"
                ><template v-if="displayInstanceName">{{ displayInstanceNameShort }}&nbsp;</template
                >{{ displayHostShort }}</span
              >
            </AppTooltip>
          </span>
        </span>
        <span class="info-chip">
          <AppTooltip :title="displayDatabaseFull">
            <span class="chip-content"><DatabaseFilled class="chip-icon chip-icon--db" />{{ displayDatabaseShort }}</span>
          </AppTooltip>
        </span>
        <span v-if="hasDualLevelStructure" class="info-chip">
          <AppTooltip :title="displaySchemaFull">
            <span class="chip-content"><PartitionOutlined class="chip-icon chip-icon--schema" />{{ displaySchemaShort }}</span>
          </AppTooltip>
        </span>
      </div>

      <!-- 2.3 右侧：执行人 + 【更多/收起】按钮（带图标 ∨ / ∧） -->
      <div class="right-side-group">
        <div class="executor-inline">
          <span class="info-label">执行人</span>
          <span class="label-colon">：</span>
          <AppTooltip :title="displayExecutor">
            <span class="info-value">{{ displayExecutor }}</span>
          </AppTooltip>
        </div>
        <span class="info-toggle" role="button" tabindex="0"
              @click.prevent="toggleMore"
              @keydown.enter.prevent="toggleMore"
              @keydown.space.prevent="toggleMore">
          <template v-if="moreOpen">
            收起<UpOutlined class="toggle-icon" />
          </template>
          <template v-else>
            更多<DownOutlined class="toggle-icon" />
          </template>
        </span>
      </div>
    </div>

    <!-- ====== 第 3 行：点击【更多】展开的 5 项信息（3列 2 行布局，与参考图一致）====== -->
    <div v-show="moreOpen" class="extra-info-grid">
      <div class="info-group-left">
        <div class="info-item info-item--horizontal">
          <span class="info-label">预估影响行数</span>
          <span class="label-colon">：</span>
          <AppTooltip :title="String(orderDetail.estimated_affected_rows ?? 0)">
            <span class="info-value">{{ orderDetail.estimated_affected_rows ?? 0 }}</span>
          </AppTooltip>
        </div>
        <div class="info-item info-item--horizontal">
          <span class="info-label">执行时间</span>
          <span class="label-colon">：</span>
          <AppTooltip :title="execTimeText">
            <span class="info-value">{{ execTimeText }}</span>
          </AppTooltip>
        </div>
      </div>
      <div class="info-group-middle">
        <div class="info-item info-item--horizontal">
          <span class="info-label">错误处理</span>
          <span class="label-colon">：</span>
          <AppTooltip :title="displayErrorPolicy">
            <span class="info-value">{{ displayErrorPolicy }}</span>
          </AppTooltip>
        </div>
        <div class="info-item info-item--horizontal">
          <span class="info-label">备注</span>
          <span class="label-colon">：</span>
          <AppTooltip :title="orderDetail.remark || '-'">
            <span class="info-value">{{ orderDetail.remark || '-' }}</span>
          </AppTooltip>
        </div>
      </div>
      <div class="info-group-right">
        <div class="info-item info-item--horizontal">
          <span class="info-label">备份失败处理</span>
          <span class="label-colon">：</span>
          <AppTooltip :title="displayBackupPolicy">
            <span class="info-value">{{ displayBackupPolicy }}</span>
          </AppTooltip>
        </div>
      </div>
    </div>
  </div>

  <a-descriptions v-else size="small" :column="2" class="header-descriptions">
    <a-descriptions-item label="任务名称">{{ orderDetail.title || '-' }}</a-descriptions-item>
    <a-descriptions-item label="创建人">{{ orderDetail.applicant || '-' }}</a-descriptions-item>
    <a-descriptions-item label="数据源类型">{{ orderDetail.db_type || '-' }}</a-descriptions-item>
    <a-descriptions-item label="数据源实例">
      <span
        class="instance-description"
      >
        <AppTooltip :title="displayInstanceFullText">
          <span class="instance-name"><DataSourceName :name="displayInstanceName" :max="14" /></span>
        </AppTooltip>
        <span class="instance-sep" v-if="displayInstanceName && displayInstanceHost">｜</span>
        <AppTooltip :title="displayInstanceHost">
          <span class="instance-host"><DataSourceName :name="displayInstanceHost" :max="14" /></span>
        </AppTooltip>
      </span>
    </a-descriptions-item>
    <a-descriptions-item label="数据库">{{ orderDetail.database || '-' }}</a-descriptions-item>
    <a-descriptions-item v-if="orderDetail.schema" label="Schema">{{ displaySchema }}</a-descriptions-item>
    <a-descriptions-item label="执行人">{{ displayExecutor }}</a-descriptions-item>
    <a-descriptions-item label="当前审批人">{{ orderDetail.current_approver || '-' }}</a-descriptions-item>
    <a-descriptions-item label="预估影响行数">{{ orderDetail.estimated_affected_rows ?? 0 }}</a-descriptions-item>
    <a-descriptions-item label="提交时间">{{ orderDetail.created_at || '-' }}</a-descriptions-item>
    <a-descriptions-item :span="2" label="备注">{{ orderDetail.remark || '-' }}</a-descriptions-item>
  </a-descriptions>
</template>

<script setup>
import { computed, ref } from 'vue'
import { message } from 'ant-design-vue'
import {
  CopyOutlined,
  DatabaseFilled,
  DownOutlined,
  PartitionOutlined,
  UpOutlined,
} from '@ant-design/icons-vue'
import AppTooltip from '@/components/patterns/AppTooltip.vue'
import DataSourceName from '@/components/patterns/DataSourceName.vue'
import { getPureSchemaDisplayName } from '@/utils/schemaDisplay'
import { getOrderStatusMeta } from '@/views/orders/shared/orderStatusMeta'

const props = defineProps({
  orderDetail: {
    type: Object,
    default: () => ({}),
  },
  compact: {
    type: Boolean,
    default: false,
  },
})

const moreOpen = ref(false)

// 「执行时间」展示口径：
// - 已执行过的工单（执行成功 COMPLETED / 执行失败 FAILED）：必须显示实际时间，
//   executed_at 为空（历史数据）时回落 updated_at（= 状态最后变更时间，近似执行完成时刻）；
// - 尚未执行的工单：统一显示 '-'。
//
// 2026-10-13 追加：定时工单一并把「计划执行时间 + 等待时长」呈现出来。
// 原因：任务结果里的耗时是 SQL 在目标库上的执行时间（常常只有几十毫秒），
// 而定时工单从「计划时间」到「实际触发」还隔着调度等待（历史轮询粒度 1 分钟，实测延迟 55s）。
// 只显示完成时刻 + 只显示毫秒耗时，两边对不上，用户无法判断时间花在哪。
const EXECUTED_PROGRESSES = ['COMPLETED', 'FAILED']
const isValidTimeText = (v) => {
  const s = String(v || '').trim()
  return !!s && !s.startsWith('0001-01-01') && !s.startsWith('1970-01-01') && s !== '{}' && s !== 'null'
}

// 解析服务端时间文本（"2006-01-02 15:04:05"）为时间戳，解析不了返回 NaN
const parseServerTime = (v) => {
  const s = String(v || '').trim()
  if (!isValidTimeText(s)) return NaN
  return new Date(s.replace(' ', 'T')).getTime()
}

// 秒 → 人类可读时长
const formatDuration = (seconds) => {
  const s = Math.max(0, Math.round(seconds))
  if (s < 60) return `${s}秒`
  const m = Math.floor(s / 60)
  const rest = s % 60
  return rest ? `${m}分${rest}秒` : `${m}分`
}

// 定时等待时长：计划时间 → 开始执行时间（没有 started_at 时退回完成时间）
const scheduleWaitText = computed(() => {
  const d = props.orderDetail || {}
  if (!EXECUTED_PROGRESSES.includes(d.progress)) return ''
  const planned = parseServerTime(d.scheduled_at)
  if (Number.isNaN(planned)) return ''
  const actual = parseServerTime(d.started_at) || parseServerTime(d.executed_at) || parseServerTime(d.updated_at)
  if (Number.isNaN(actual)) return ''
  const wait = (actual - planned) / 1000
  // 早于计划时间触发（立即执行、手动提前触发）不算等待
  if (wait < 1) return ''
  return `计划 ${d.scheduled_at} · 等待 ${formatDuration(wait)}`
})

const execTimeText = computed(() => {
  const d = props.orderDetail || {}
  if (!EXECUTED_PROGRESSES.includes(d.progress)) return '-'
  let base = '-'
  if (isValidTimeText(d.executed_at)) base = d.executed_at
  else if (isValidTimeText(d.updated_at)) base = d.updated_at
  if (base === '-') return '-'
  const wait = scheduleWaitText.value
  return wait ? `${base}（${wait}）` : base
})

const hasDualLevelStructure = computed(() => {
  const dbType = String(props.orderDetail?.db_type || '').toLowerCase()
  if (['postgresql', 'postgres', 'polardb-pg', 'polardb-postgresql', 'polardbpg'].includes(dbType)) return true
  if (dbType === 'sqlserver') return true
  return false
})

const displayExecutor = computed(() => {
  if (!props.orderDetail?.executor) return '提交人执行'
  return props.orderDetail.executor
})

const displayInstanceHost = computed(() => props.orderDetail?.instance || '')

const displayInstanceName = computed(() => {
  const name = props.orderDetail?.instance_name
  if (name) return name
  return ''
})

const truncateN = (val, max) => {
  const s = String(val ?? '')
  if (!s) return ''
  return s.length <= max ? s : s.slice(0, max) + '...'
}

/* 数据源展示：实例名 >15 字符、IP:Port >30 字符分别截断 */
const displayInstanceNameShort = computed(() => truncateN(displayInstanceName.value, 15))
const displayHostShort = computed(() => truncateN(displayInstanceHost.value, 30))

const displayInstanceFullText = computed(() => {
  const parts = [displayInstanceName.value, displayInstanceHost.value].filter(Boolean)
  return parts.join(' ') || (props.orderDetail?.instance || '')
})

const displayDatabaseFull = computed(() => {
  const database = props.orderDetail?.database || '-'
  const schema = props.orderDetail?.schema
  if (hasDualLevelStructure.value) {
    return database
  }
  if (schema) {
    return schema
  }
  return database
})

const truncate15 = (val) => {
  if (val === null || val === undefined) return '-'
  const s = String(val)
  if (!s) return '-'
  if (s.length <= 15) return s
  return s.slice(0, 15) + '...'
}

const displayTitleShort = computed(() => truncate15(props.orderDetail?.title || ''))

const displayDatabaseShort = computed(() => truncate15(displayDatabaseFull.value))

const displaySchemaFull = computed(() => {
  if (!props.orderDetail?.schema) return '-'
  if (!hasDualLevelStructure.value) {
    return props.orderDetail.schema
  }
  return getPureSchemaDisplayName({
    dbType: props.orderDetail?.db_type,
    schema: props.orderDetail?.schema,
    displayName: props.orderDetail?.display_name,
    schemaName: props.orderDetail?.schema_name,
    databaseName: props.orderDetail?.database,
  }) || props.orderDetail.schema
})

const displaySchemaShort = computed(() => truncate15(displaySchemaFull.value))

const displayErrorPolicy = computed(() => {
  const v = props.orderDetail?.error_policy
  if (!v) return '-'
  const map = { CONTINUE: '遇到错误继续', ABORT: '遇到错误中止', '事务中中止': '事务中中止' }
  return map[v] || v
})

const displayBackupPolicy = computed(() => {
  const v = props.orderDetail?.backup_policy
  if (!v) return '-'
  const map = { IGNORE: '忽略继续执行', ABORT: '中止执行' }
  return map[v] || v
})

const displaySchema = displaySchemaFull

const toggleMore = () => {
  moreOpen.value = !moreOpen.value
}

const copyOrderId = async () => {
  const id = props.orderDetail?.order_id
  if (!id) return
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(id)
    } else {
      const ta = document.createElement('textarea')
      ta.value = id
      ta.style.position = 'fixed'
      ta.style.left = '-9999px'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    message.success('工单ID已复制')
  } catch {
    message.error('复制失败，请手动复制')
  }
}
</script>

<style scoped>
.header-descriptions :deep(.ant-descriptions-item-label) {
  width: 120px;
}

.instance-description {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  min-width: 0;
}

.instance-description .instance-name,
.instance-description .instance-sep,
.instance-description .instance-host {
  white-space: nowrap;
}

.instance-description .instance-name {
  display: inline-block;
  vertical-align: middle;
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 0 1 auto;
  min-width: 0;
}

.instance-description .instance-sep,
.instance-description .instance-host {
  flex: 0 0 auto;
}

/* ====================== compact 模式：按参考图 1/2 重构的 HeaderContent ====================== */
.order-info-card {
  display: block;
  position: relative;
  width: 100%;
  min-width: 0;
  box-sizing: border-box;
  padding: 0;
  margin: 0;
  border: none;
  border-radius: 0;
  background: transparent;
  color: #1f2329;
}

/* ========== 第 1 行：标题 + 工单 ID + 复制按钮 + 状态 tag ========== */
.header-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  margin: 0;
  padding: 0;
  column-gap: 16px;
}

.title-left-group {
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  flex: 1 1 auto;
  min-width: 0;
  column-gap: 8px;
}

/* 标题：大字 18px，正常字重（不加粗） */
.detail-title-text {
  font-size: 18px !important;
  font-weight: 400 !important;
  line-height: 24px !important;
  color: #1f2329 !important;
  letter-spacing: 0.2px !important;
  display: inline-flex !important;
  align-items: center !important;
  vertical-align: middle !important;
}
.detail-title-text--ellipsis {
  max-width: none;
  min-width: 0;
  overflow: visible;
  text-overflow: clip;
  white-space: nowrap !important;
  margin: 0;
  padding: 0;
}

/* 工单 ID：小号灰色 */
.order-id-block {
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  flex: 0 0 auto;
  min-width: 0;
  column-gap: 2px;
}
.detail-order-id {
  font-size: 14px !important;
  color: #86909c !important;
  line-height: 20px !important;
  font-weight: 400 !important;
  letter-spacing: 0.2px !important;
  display: inline-flex !important;
  align-items: center !important;
  margin: 0;
  padding: 0;
  white-space: nowrap !important;
}
.copy-order-id-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  flex: 0 0 auto;
  margin: 0;
  padding: 0;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: #86909c;
  cursor: pointer;
  user-select: none;
  outline: 0 !important;
  box-shadow: none !important;
  transition: color 0.15s ease, background 0.15s ease;
}
.copy-order-id-btn:hover {
  color: var(--gi-color-primary, #0f766e);
  background: #f2f3f5;
}
.copy-order-id-btn:active { color: var(--gi-color-primary-active, #0b5f58); }
.copy-order-id-btn svg {
  width: 14px; height: 14px;
}

/* 状态 tag：与参考图一致（灰/彩色胶囊），大小收紧 */
.detail-status-tag {
  flex: 0 0 auto;
  margin: 0;
  padding: 0 10px;
  height: 26px;
  line-height: 24px;
  font-size: 13px !important;
  font-weight: 500 !important;
  border-radius: 4px !important;
}
.detail-status-tag-inline {
  margin-left: 5px;
}

/* ========== 第 2 行：创建者信息 + 标签胶囊组 + 执行人 + 更多/收起 ========== */
.header-content-row {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  width: 100%;
  margin-top: 8px;   /* 标题行(24px)与 meta 行(32px)之间收拢到 8px，紧凑不贴死 */
  padding: 0;
  column-gap: 10px;
  min-height: 32px;
}
.right-side-group {
  margin-left: 0;
}

/* 2.1 左侧：头像 + 申请人 + 创建于 + 时间 */
.creator-info {
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  flex: 0 0 auto;
  min-width: 0;
  column-gap: 8px;
}
.creator-avatar {
  flex: 0 0 auto;
  background: linear-gradient(135deg, #f59e0b 0%, #7c3aed 100%);
  border: 0;
  color: #fff;
  font-weight: 600 !important;
}
.creator-name {
  font-size: 14px !important;
  font-weight: 400 !important;
  color: #86909c !important;
  line-height: 20px !important;
}
.creator-sep {
  font-size: 14px !important;
  font-weight: 400 !important;
  color: #86909c !important;
  line-height: 20px !important;
}
.creator-time {
  font-size: 14px !important;
  font-weight: 400 !important;
  color: #86909c !important;
  line-height: 20px !important;
  white-space: nowrap;
}

/* 2.2 中间：数据源/数据库/Schema 一组（去掉灰色胶囊底，纯文本展示） */
.tag-group {
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  flex: 0 0 auto;
  min-width: 0;
  width: auto;
  max-width: 100%;
  column-gap: 4px;
  background: transparent;
  border-radius: 4px;
  padding: 0;
}
.info-chip {
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  flex: 0 0 auto;
  min-width: 0;
  column-gap: 6px;
  padding: 0 8px;
  height: 28px;
  border-radius: 4px;
}
/* 数据源 chip 与数据库、Schema 使用完全一致的样式，不再有分隔/标签段特殊渲染 */
.info-chip:first-child {
  flex: 0 0 auto;
  column-gap: 6px;
  padding: 0 8px;
  overflow: visible;
  background: transparent;
}
.chip-label--inline {
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  font-size: 13px !important;
  font-weight: 400 !important;
  color: #86909c !important;
  margin-right: 2px;
  flex: 0 0 auto;
}
.info-chip:first-child .chip-content {
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  height: auto;
  padding: 0;
  flex: 0 0 auto;
  min-width: 0;
  background: transparent;
}

.chip-content {
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  min-width: 0;
  column-gap: 6px;
}

.chip-icon {
  flex: 0 0 auto;
  width: 16px;
  height: 16px;
  line-height: 1;
  display: inline-flex !important;
  align-items: center;
  justify-content: center;
}
/* 图标配色按参考图：数据源蓝、数据库紫、Schema 橙 */
.chip-icon--instance  { color: #165dff !important; }
.chip-icon--db        { color: #722ed1 !important; }
.chip-icon--schema    { color: #fa8c16 !important; }

.chip-content,
.chip-content * {
  font-size: 14px !important;
  font-weight: 400 !important;
  line-height: 20px !important;
  color: #86909c !important;
  white-space: nowrap !important;
}

/* 数据源 chip：单行展示，截断由 JS（15/30 字符）完成，不换行 */
.chip-content .ds-full-text {
  white-space: nowrap !important;
}

.chip-content :deep(app-tooltip),
.chip-content :deep(.gi-app-tooltip) {
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  min-width: 0;
  max-width: 100%;
}

/* 2.3 右侧：执行人 inline + 更多/收起 按钮 */
.right-side-group {
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  flex: 0 0 auto;
  min-width: 0;
  column-gap: 6px;
  margin-left: 0;
}

.executor-inline {
  display: inline-grid;
  grid-template-columns: auto auto auto;
  column-gap: 2px;
  align-items: center;
  margin: 0;
  min-width: 0;
}
.executor-inline .info-label,
.executor-inline .label-colon,
.executor-inline .info-value {
  font-size: 14px !important;
  font-weight: 400 !important;
  line-height: 20px !important;
  height: 20px !important;
  color: #86909c !important;
  display: inline-flex !important;
  align-items: center !important;
  white-space: nowrap !important;
  overflow: hidden;
}
.executor-inline .info-value {
  color: #86909c !important;
  font-weight: 400 !important;
  column-gap: 4px;
}

/* 【更多/收起】按钮：纯文字链接 + ∨ / ∧ 图标，主题青绿 */
.info-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  flex: 0 0 auto;
  min-width: auto;
  padding: 0;
  margin: 0;
  column-gap: 3px;
  color: var(--gi-color-primary, var(--ant-colorPrimary, #0f766e));
  font-size: 14px !important;
  font-weight: 500 !important;
  line-height: 20px !important;
  cursor: pointer;
  user-select: none;
  white-space: nowrap;
  border: 0;
  border-radius: 0;
  background: transparent !important;
  box-shadow: none !important;
  outline: 0 !important;
  transition: color 0.15s ease;
  text-decoration: none;
}
.info-toggle:hover {
  color: var(--gi-color-primary-hover, #0d6b63);
  text-decoration: underline;
  background: transparent !important;
}
.toggle-icon {
  display: inline-flex !important;
  align-items: center;
  width: 14px;
  height: 14px;
  line-height: 1;
}

/* ========== 第 3 行：展开更多后的 3 列 × 2 行信息矩阵 ========== */
.extra-info-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  column-gap: 16px;
  row-gap: 12px;
  width: 100%;
  margin-top: 14px;
  align-items: start;
}
.info-group-left,
.info-group-middle,
.info-group-right {
  display: flex;
  flex-direction: column;
  flex-wrap: nowrap;
  align-items: stretch;
  justify-content: flex-start;
  row-gap: 12px;
}

.info-item--horizontal {
  display: inline-grid;
  grid-template-columns: auto auto 1fr;
  column-gap: 2px;                /* label → 冒号 → value 严格 2px */
  align-items: center;
  justify-self: start;
  justify-content: flex-start;
  flex: 0 0 auto;
  min-width: 0;
  max-width: 100%;
}
.info-item--horizontal .info-label,
.info-item--horizontal .label-colon,
.info-item--horizontal .info-value {
  font-size: 14px !important;
  line-height: 20px !important;
  height: 20px !important;
  display: inline-flex !important;
  align-items: center !important;
  white-space: nowrap !important;
  overflow: hidden;
  text-overflow: ellipsis;
}
.info-item--horizontal .info-label { color: #86909c !important; font-weight: 400 !important; }
.info-item--horizontal .label-colon { color: #86909c !important; font-weight: 400 !important; }
.info-item--horizontal .info-value { color: #86909c !important; font-weight: 400 !important; min-width: 0; }

/* ====== 窄屏 / 中屏适配（让 3 列信息矩阵不会在窄屏被挤爆）====== */
@media screen and (max-width: 992px) {
  .header-content-row {
    flex-wrap: wrap;
    row-gap: 10px;
  }
  .tag-group { flex: 1 1 100%; order: 3; }
  .extra-info-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media screen and (max-width: 768px) {
  .header-content-row {
    flex-direction: column;
    align-items: flex-start;
    row-gap: 10px;
  }
  .right-side-group { align-self: flex-end; }
  .extra-info-grid { grid-template-columns: 1fr; }
  .header-title-row { flex-wrap: wrap; row-gap: 8px; }
}

@media (max-width: 768px) {
  .header-descriptions :deep(.ant-descriptions-item-label) {
    width: 84px;
  }

  .instance-description .instance-name {
    max-width: 120px;
  }

  /* 三组 chip 全部 nowrap，窄屏会撑破卡片，改为可横向滑动 */
  .tag-group {
    width: 100%;
    overflow-x: auto;
    scrollbar-width: none;
  }

  .tag-group::-webkit-scrollbar {
    display: none;
  }

  .detail-title-text {
    font-size: 16px !important;
    line-height: 22px !important;
  }

  .detail-title-text--ellipsis {
    max-width: 150px;
  }

  .copy-order-id-btn {
    width: 32px;
    height: 32px;
  }
}
</style>
