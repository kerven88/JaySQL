<template>
  <div class="rrd">
    <!-- ===================== Header：标题 + 身份标签 + 常驻最近上报 ===================== -->
    <div class="rrd-header">
      <div class="rrd-head-row">
        <h3 class="rrd-title">恢复任务详情</h3>
        <span class="rrd-task-name" :title="run?.name || ''">{{ run?.name || '-' }}</span>
        <span class="pill" :class="'t-' + derivedMeta.tone">
          <component :is="iconOf(derivedMeta.icon)" />
          {{ derivedMeta.label }}
          <span v-if="run?.status === 'RUNNING'" class="dot-live"></span>
        </span>
        <span class="pill t-brand">{{ isPhysical ? '物理恢复' : '逻辑恢复' }}</span>
        <span class="head-spacer"></span>
        <button class="btn btn-sm" :disabled="loading" @click="emit('refresh')">
          <ReloadOutlined />
          刷新<span class="num rrd-head-interval">{{ refreshSeconds }}s</span>
        </button>
      </div>
      <div class="rrd-sub">
        <span>
          任务ID <code>{{ run?.task_id || '-' }}</code>
          <button class="copy-btn" title="复制任务ID" @click="copyText(run?.task_id)">
            <CopyOutlined />
          </button>
        </span>
        <span>{{ run?.db_type || '-' }} · {{ driverLabel }}</span>
        <span>发起人 {{ run?.created_by || '-' }} · 创建于 {{ formatDateTime(run?.created_at) }}</span>
        <span v-if="isActive">
          <ClockCircleFilled />
          最近上报 <span class="num">{{ lastReportAgoText }}</span>
        </span>
      </div>
      <button class="close-x" title="关闭" @click="emit('close')">
        <CloseOutlined />
      </button>
    </div>

    <!-- ===================== 主体：滚动条落在本体上 ===================== -->
    <div class="rrd-body" ref="bodyRef">
      <a-skeleton v-if="loading && !run" active :paragraph="{ rows: 10 }" />
      <a-empty v-else-if="!run" description="未找到该恢复任务" />

      <template v-else>
        <!-- ① 任务概览 -->
        <section class="sec" id="rrd-sec-overview">
          <div class="sec-head">
            <span class="sec-idx">1</span>
            <h4 class="sec-title">任务概览</h4>
            <span class="sec-line"></span>
          </div>
          <div class="card hero">
            <div class="hero-left">
              <!-- 状态 pill 已在头部常驻（终态与「成功」同义），概览区只保留阶段 pill；
                   两者都不展示时整行不渲染，避免留下 10px 的空白条 -->
              <div v-if="showStagePill" class="hero-pills">
                <span class="pill t-brand">{{ stageLabel(run?.current_stage) }}</span>
              </div>
              <!-- 标签与值用「：」直连（无间距）；任务名称/任务ID/目标实例同一行展示 -->
              <div class="kv-list">
                <div class="kv">
                  <span class="kv-k">任务名称</span><span class="kv-colon">：</span>
                  <span class="kv-v" :title="run?.name || ''">{{ run?.name || '-' }}</span>
                </div>
                <div class="kv">
                  <span class="kv-k">任务ID</span><span class="kv-colon">：</span>
                  <span class="kv-v mono">
                    {{ run?.task_id || '-' }}
                    <button class="copy-btn" title="复制任务ID" @click="copyText(run?.task_id)">
                      <CopyOutlined />
                    </button>
                  </span>
                </div>
                <div class="kv">
                  <span class="kv-k">目标实例</span><span class="kv-colon">：</span>
                  <span class="kv-v" :title="`${run?.target_remark || targetHostPort} · ${targetHostPort}`">
                    {{ run?.target_remark || targetHostPort }}
                    <span class="kv-suffix">· {{ targetHostPort }}</span>
                  </span>
                </div>
              </div>
            </div>
          </div>

          <div v-if="isHeartbeatLost" class="alert-strip warning">
            <ExclamationCircleFilled />
            <div class="as-body">
              <div class="as-title">已超过 {{ staleMinutes }} 分钟未收到执行节点心跳</div>
              <div class="as-msg">
                执行进程可能已退出但未上报状态。可点击底部「强制终止」将任务收口为已终止。
              </div>
            </div>
          </div>

          <div v-if="run?.status === 'FAILED'" class="alert-strip danger">
            <CloseCircleFilled />
            <div class="as-body">
              <div class="as-title">任务失败 · {{ stageLabel(run?.current_stage) }}阶段中断</div>
              <div class="as-msg">{{ run?.error_message || '任务失败，请查看日志定位。' }}</div>
            </div>
            <button class="btn btn-sm" @click="showException()">
              <AimOutlined />查看异常明细
            </button>
          </div>
          <div v-else-if="failedCount > 0" class="alert-strip warning">
            <ExclamationCircleFilled />
            <div class="as-body">
              <div class="as-title">
                部分成功 · {{ failedCount }}/{{ totalCount }} 个恢复对象失败（任务整体已结束）
              </div>
              <div class="as-msg">{{ firstFailedItem?.error_message || '' }}</div>
            </div>
            <button class="btn btn-sm" @click="showException()">
              <AimOutlined />查看异常明细
            </button>
          </div>
        </section>

        <!-- ② 恢复配置 -->
        <section class="sec" id="rrd-sec-config">
          <div class="sec-head">
            <span class="sec-idx">2</span>
            <h4 class="sec-title">恢复配置</h4>
            <span class="sec-line"></span>
          </div>
          <div class="rrd-groups">
            <div v-for="group in configGroups" :key="group.key" class="group" :class="{ collapsed: collapsed[group.key] }">
              <div class="group-head" @click="toggleGroup(group.key)">
                <span class="group-caret"><RightOutlined /></span>
                <span class="group-title">{{ group.title }}</span>
              </div>
              <div class="group-body">
                <div class="kv-grid" :class="'cols-' + group.cols">
                  <div v-for="field in group.fields" :key="field.k" class="kv" :class="{ full: field.full }">
                    <span class="kv-k" :title="field.k">{{ field.k }}</span><span class="kv-colon">：</span>
                    <span
                      class="kv-v"
                      :class="{ mono: field.mono }"
                      :title="field.title || field.v"
                    >
                      {{ field.v || '-' }}
                      <span v-if="field.suffix" class="kv-suffix">{{ field.suffix }}</span>
                    </span>
                  </div>
                </div>
                <div v-if="group.hint" class="group-hint">
                  <AimOutlined /><span>{{ group.hint }}</span>
                </div>
              </div>
            </div>
          </div>
        </section>

        <!-- ③ 结果统计 -->
        <section class="sec" id="rrd-sec-result">
          <div class="sec-head">
            <span class="sec-idx">3</span>
            <h4 class="sec-title">结果统计</h4>
            <span class="sec-line"></span>
          </div>
          <div class="card">
            <template v-if="result">
              <table class="tbl">
                <thead>
                  <tr>
                    <th>目标库</th>
                    <th class="right">表</th>
                    <th class="right">视图</th>
                    <th class="right">触发器</th>
                    <th class="right">存储过程/函数</th>
                    <th>字符集 / 排序规则</th>
                    <th>引擎</th>
                    <th>ANALYZE</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="row in postchecks" :key="row.database">
                    <template v-if="row.error">
                      <td>{{ row.database || '-' }}</td>
                      <td colspan="6" class="err-text">{{ row.error }}</td>
                      <td><span class="pill t-danger"><CloseCircleFilled />未通过</span></td>
                    </template>
                    <template v-else>
                      <td>{{ row.database || '-' }}</td>
                      <td class="num right">{{ row.objects.tables_count ?? '-' }}</td>
                      <td class="num right">{{ row.objects.views_count ?? '-' }}</td>
                      <td class="num right">{{ row.objects.triggers_count ?? '-' }}</td>
                      <td class="num right">{{ row.objects.routines_count ?? '-' }}</td>
                      <td>{{ row.objects.charset || '-' }} / {{ row.objects.collation || '-' }}</td>
                      <td>{{ row.objects.engines || '-' }}</td>
                      <!-- ANALYZE 只给纯文本，不用 pill/图标/色块 -->
                      <td>{{ row.analyze ? '已 ANALYZE' : '未执行' }}</td>
                    </template>
                  </tr>
                </tbody>
              </table>
            </template>
            <div v-else class="empty-tip">
              任务尚未完成，暂无结果统计。完成后将展示对象级数据量、还原对象数与后校验结果。
            </div>
          </div>
        </section>

        <!-- ⑤ 日志 / 预检查 / 异常 -->
        <section class="sec" id="rrd-sec-log">
          <div class="sec-head">
            <span class="sec-idx">4</span>
            <h4 class="sec-title">执行日志 / 异常明细</h4>
            <span class="sec-line"></span>
          </div>
          <div class="card">
            <div class="tabs">
              <button class="tab" :class="{ on: activeTab === 'log' }" @click="activeTab = 'log'">执行日志</button>
              <button class="tab" :class="{ on: activeTab === 'precheck' }" @click="activeTab = 'precheck'">
                预检查明细
                <span v-if="pcBlock" class="tab-badge">{{ pcBlock }}</span>
                <span v-else-if="pcWarn" class="tab-badge warn">{{ pcWarn }}</span>
              </button>
              <button class="tab" :class="{ on: activeTab === 'exception' }" @click="activeTab = 'exception'">
                异常明细
                <span v-if="failedCount" class="tab-badge">{{ failedCount }}</span>
              </button>
            </div>

            <div v-show="activeTab === 'log'" class="tab-pane">
              <div class="log-toolbar">
                <label class="chk">
                  <input v-model="onlyError" type="checkbox" />
                  只看错误行
                </label>
                <span class="log-meta">共 {{ logLines.length }} 行 · 错误 {{ errorLineCount }} 行</span>
                <span class="log-spacer"></span>
                <span class="log-path mono">{{ formatStoragePath(run?.log_path, null) }}</span>
                <button class="btn btn-sm" @click="openFullLog">查看完整日志</button>
                <button class="btn btn-sm" @click="copyLog"><CopyOutlined />复制</button>
                <button class="btn btn-sm" @click="emit('download-log', 0)"><DownloadOutlined />下载日志</button>
              </div>
              <div class="log-view">
                <template v-if="visibleLogLines.length">
                  <div v-for="(line, index) in visibleLogLines" :key="index" class="log-line" :class="line.cls">
                    <span class="lm">{{ line.raw }}</span>
                  </div>
                </template>
                <div v-else class="log-empty">
                  {{ logLines.length ? '无匹配日志行' : '暂无日志内容，可点击「查看完整日志」拉取' }}
                </div>
              </div>
            </div>

            <div v-show="activeTab === 'precheck'" class="tab-pane">
              <div class="pc-summary">
                阻断 {{ pcBlock }} 项 · 警告 {{ pcWarn }} 项 · 通过
                {{ Math.max(0, precheckItems.length - pcBlock - pcWarn) }} 项
              </div>
              <template v-if="precheckItems.length">
                <div v-for="item in precheckItems" :key="item.item_key" class="pc-item" :class="precheckToken(item)">
                  <span class="pc-icon">
                    <CheckCircleFilled v-if="precheckToken(item) === 'passed'" class="c-success" />
                    <ExclamationCircleFilled v-else-if="precheckToken(item) === 'warning'" class="c-warning" />
                    <CloseCircleFilled v-else class="c-danger" />
                  </span>
                  <div class="pc-main">
                    <div class="pc-label">
                      {{ item.label || item.item_key || '-' }}
                      <span class="pill" :class="precheckPillClass(item)">{{ precheckTokenLabel(precheckToken(item)) }}</span>
                      <span class="pc-key">{{ item.phase || '-' }} / {{ item.item_key || '-' }}</span>
                    </div>
                    <div class="pc-msg">{{ item.message || '-' }}</div>
                    <!-- 修复指引只对「未通过」的项显示，通过的项给指引等于制造噪音 -->
                    <div v-if="precheckToken(item) !== 'passed' && fixHintOf(item)" class="pc-fix">
                      <AimOutlined /><span>{{ fixHintOf(item) }}</span>
                    </div>
                  </div>
                </div>
              </template>
              <div v-else class="empty-tip">无预检查记录</div>
            </div>

            <div v-show="activeTab === 'exception'" class="tab-pane">
              <template v-if="exceptionItems.length">
                <div
                  v-for="item in exceptionItems"
                  :key="item.id"
                  class="pc-item"
                  :class="item.status === 'FAILED' ? 'blocked' : 'warning'"
                >
                  <span class="pc-icon">
                    <CloseCircleFilled v-if="item.status === 'FAILED'" class="c-danger" />
                    <ExclamationCircleFilled v-else class="c-warning" />
                  </span>
                  <div class="pc-main">
                    <div class="pc-label">
                      {{ item.target_object_name || item.source_object_name || '-' }}
                      <span class="pc-key">item#{{ item.id }}</span>
                    </div>
                    <div class="pc-msg mono err-text">{{ item.error_message }}</div>
                    <div class="pc-fix">
                      <AimOutlined /><span>处理建议：{{ exceptionAdvice(item) }}</span>
                    </div>
                  </div>
                </div>
              </template>
              <div v-else class="empty-tip">无异常明细</div>
            </div>
          </div>
        </section>
      </template>
    </div>

    <!-- ===================== Footer：固定操作区 ===================== -->
    <div class="rrd-footer">
      <span v-if="footerTip" class="footer-tip">{{ footerTip }}</span>
      <span class="footer-spacer"></span>
      <button class="btn" @click="emit('download-log', 0)"><DownloadOutlined />下载日志</button>
      <button
        v-if="canStop"
        class="btn btn-danger"
        :disabled="loadingAction"
        @click="emit('stop')"
      >
        <StopOutlined />{{ run?.status === 'STOP_REQUESTED' ? '强制终止' : '终止任务' }}
      </button>
      <button class="btn btn-primary" @click="emit('close')">{{ closeText }}</button>
    </div>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import dayjs from 'dayjs'
import { message } from 'ant-design-vue'
import {
  AimOutlined,
  CheckCircleFilled,
  ClockCircleFilled,
  CloseCircleFilled,
  CloseOutlined,
  CopyOutlined,
  DownloadOutlined,
  ExclamationCircleFilled,
  MinusCircleFilled,
  ReloadOutlined,
  RightOutlined,
  StopOutlined,
} from '@ant-design/icons-vue'

import {
  fixHintOf,
  formatBytes,
  formatDateTime,
  isActiveStatus,
  precheckToken,
  precheckTokenLabel,
  restoreModeLabel,
  stageLabel,
} from './constants'

import { clipText } from '@/utils/text'
// 日志路径 / 产物路径与备份侧同源口径：能解析到存储目标时补「地址:」，解析不到则只显示路径
import { formatStoragePath } from '@/utils/storagePath'

defineOptions({ name: 'RestoreRunDetailPanel' })

const props = defineProps({
  run: { type: Object, default: null },
  items: { type: Array, default: () => [] },
  statistics: { type: Object, default: () => ({}) },
  // 日志预览：优先取 props，其次取 run.log_excerpt（详情接口已带尾部摘录）
  logExcerpt: { type: String, default: '' },
  loading: { type: Boolean, default: false },
  loadingAction: { type: Boolean, default: false },
  refreshSeconds: { type: Number, default: 5 },
  // 详情页复用时底部主按钮语义不同（返回列表）
  closeText: { type: String, default: '关闭' },
})

const emit = defineEmits(['close', 'refresh', 'view-log', 'stop', 'download-log'])

/* ==================== 状态语义：唯一出口 ==================== */
const STATUS_META = {
  PENDING: { tone: 'idle', icon: 'idle', label: '等待中' },
  RUNNING: { tone: 'running', icon: 'run', label: '运行中' },
  STOP_REQUESTED: { tone: 'running', icon: 'run', label: '终止中' },
  STOPPED: { tone: 'idle', icon: 'idle', label: '已终止' },
  SUCCESS: { tone: 'success', icon: 'success', label: '成功' },
  FAILED: { tone: 'danger', icon: 'fail', label: '失败' },
}
const ICONS = {
  success: CheckCircleFilled,
  fail: CloseCircleFilled,
  warn: ExclamationCircleFilled,
  run: ClockCircleFilled,
  idle: MinusCircleFilled,
}
const iconOf = (name) => ICONS[name] || MinusCircleFilled

/* ==================== 派生视图模型（重复字段在此抹平） ==================== */
const parseJSON = (value) => {
  if (!value) return null
  if (typeof value === 'object') return value
  try {
    return JSON.parse(String(value))
  } catch {
    return null
  }
}

const options = computed(() => parseJSON(props.run?.options) || {})
const resolved = computed(() => parseJSON(props.run?.resolved_context) || {})
const precheckSummary = computed(() => parseJSON(props.run?.precheck_summary) || null)
const result = computed(() => parseJSON(props.run?.result_summary) || null)
const precheckItems = computed(() => precheckSummary.value?.items || [])
const postchecks = computed(() => result.value?.postcheck_validations || [])

const isPhysical = computed(
  () =>
    String(props.run?.restore_kind || '').toUpperCase() === 'PHYSICAL' ||
    String(props.run?.driver_type || '').toUpperCase() === 'PHYSICAL',
)

const totalCount = computed(() => Number(props.statistics?.total || 0) || props.items.length)
const failedCount = computed(() => props.items.filter((item) => item.status === 'FAILED').length)
const firstFailedItem = computed(() => props.items.find((item) => item.status === 'FAILED') || null)
const exceptionItems = computed(() => props.items.filter((item) => item.error_message))

const status = computed(() => String(props.run?.status || '').toUpperCase())
const isActive = computed(() => isActiveStatus(status.value))
// 「部分成功」是派生状态：底层 status 仍为 SUCCESS，但存在失败对象
const derivedStatus = computed(() => (status.value === 'SUCCESS' && failedCount.value > 0 ? 'PARTIAL' : status.value))
const derivedMeta = computed(() => {
  if (derivedStatus.value === 'PARTIAL') return { tone: 'warning', icon: 'warn', label: '部分成功' }
  if (status.value === 'FAILED' && String(props.run?.current_stage || '').toUpperCase() === 'PRECHECK') {
    return { tone: 'danger', icon: 'fail', label: '预检查失败' }
  }
  return STATUS_META[status.value] || STATUS_META.PENDING
})
// FINISHED 的阶段文案是「完成」，与状态 pill 语义重复，收口态不再单独展示阶段
const showStagePill = computed(() => String(props.run?.current_stage || '').toUpperCase() !== 'FINISHED')

const pcBlock = computed(() => precheckItems.value.filter((item) => precheckToken(item) === 'blocked').length)
const pcWarn = computed(() => precheckItems.value.filter((item) => precheckToken(item) === 'warning').length)
const canStop = computed(() => ['PENDING', 'RUNNING', 'STOP_REQUESTED'].includes(status.value))
const FOOTER_TIPS = {
  RUNNING: '任务执行中，可随时终止；终止后已写入数据需人工确认是否清理。',
  SUCCESS: '',
  FAILED: '失败后可先查看异常明细与日志，处理根因再重试。',
  PARTIAL: '存在失败对象，重试会重跑整批对象，请确认是否先清理目标残留数据。',
  PENDING: '任务排队中，尚未开始执行；可终止后调整配置再发起。',
  STOP_REQUESTED: '已下发终止请求，等待执行节点收口；长时间无响应可强制终止。',
  // 重试入口已从详情弹窗移除，终态不再提示「重试将…」，避免出现无对应按钮的说明
  STOPPED: '',
}
const footerTip = computed(() => FOOTER_TIPS[derivedStatus.value] || '')

/* ==================== 时间：活跃任务每秒自走 ==================== */
const nowTick = ref(Date.now())
let ticker = null
const stopTicker = () => {
  if (ticker) {
    window.clearInterval(ticker)
    ticker = null
  }
}
const startTicker = () => {
  stopTicker()
  ticker = window.setInterval(() => {
    nowTick.value = Date.now()
  }, 1000)
}
watch(isActive, (active) => (active ? startTicker() : stopTicker()), { immediate: true })
onBeforeUnmount(stopTicker)

const parseTime = (value) => {
  const text = String(value || '').trim()
  if (!text || text.startsWith('0001-01-01')) return null
  const parsed = dayjs(text.replace(' ', 'T'))
  return parsed.isValid() ? parsed : null
}

const HEARTBEAT_STALE_MINUTES = 2

const lastReportMinutes = computed(() => {
  const parsed = parseTime(props.run?.last_report_at || props.run?.updated_at)
  if (!parsed) return 0
  return Math.max(0, dayjs(nowTick.value).diff(parsed, 'minute'))
})
const staleMinutes = computed(() => lastReportMinutes.value)
// 心跳失联：把「永远转圈」翻译成人话，并指向强制终止出口
const isHeartbeatLost = computed(
  () => isActive.value && lastReportMinutes.value >= HEARTBEAT_STALE_MINUTES,
)

const lastReportAgoText = computed(() => {
  const value = props.run?.last_report_at || props.run?.updated_at
  const parsed = parseTime(value)
  if (!parsed) return '-'
  const seconds = Math.max(0, dayjs(nowTick.value).diff(parsed, 'second'))
  if (seconds <= 1) return '刚刚'
  if (seconds < 60) return `${seconds} 秒前`
  return `${Math.floor(seconds / 60)} 分钟前`
})

/* ==================== 配置分组（字段去重后的唯一出口） ==================== */
const SCHEMA_POLICY_LABELS = { SKIP: '跳过已存在', ABORT: '存在即中断', REPLACE: '替换' }
const DATA_POLICY_LABELS = { APPEND: '追加', TRUNCATE: '清空后导入', ABORT: '存在即中断' }
const hostPort = (host, port) => {
  const left = String(host || '').trim()
  const right = Number(port || 0)
  if (!left && !right) return '-'
  return `${left || '-'}:${right || '-'}`
}
const targetHostPort = computed(() => hostPort(props.run?.target_host, props.run?.target_port))
// 源实例取自备份集登记的来源实例（后端 join insight_backup_sets.source_instance_id）
const SOURCE_CLIP = 20
const sourceHostPort = computed(() => hostPort(props.run?.source_host, props.run?.source_port))
const sourceAddr = computed(() => clipText(sourceHostPort.value, SOURCE_CLIP))
const sourceFull = computed(() => {
  const name = String(props.run?.source_remark || '').trim()
  return name ? `${name} · ${sourceHostPort.value}` : sourceHostPort.value
})
const driverLabel = computed(() => {
  const driver = String(props.run?.driver_type || '').toUpperCase()
  if (driver === 'LOGICAL') return '逻辑备份'
  if (driver === 'XTRABACKUP') return 'XtraBackup'
  if (driver === 'PG_BACKREST') return 'pgBackRest'
  return props.run?.driver_type || '-'
})

const configGroups = computed(() => {
  const run = props.run || {}
  const rc = resolved.value
  const o = options.value
  const targetLevel =
    run.target_level ||
    (String(run.object_mode || '').toUpperCase() === 'ALL' ? '实例级（全部对象）' : '自定义对象')
  return [
    {
      key: 'src',
      title: '备份集来源',
      cols: 3,
      fields: [
        // 展示备份集的业务 ID（bks-xxxx）；老数据没有该字段时退化到数字主键
        {
          k: '备份集ID',
          v: run.backup_sets_id || (run.backup_set_id ? String(run.backup_set_id) : '-'),
          title: run.set_name ? `备份集名称：${run.set_name}` : '',
          mono: true,
        },
        // 与「备份集ID」同一行（三列栅格），完整路径放 title
        // rc.backup_file 是绝对路径（可能来自存储目标）；set_file_name 只是文件名，不做路径拼接
        { k: '产物路径', v: rc.backup_file ? formatStoragePath(rc.backup_file, null) : run.set_file_name || '-', mono: true },
        { k: '源库对象', v: run.object_summary || run.source_db_name || '-' },
        {
          k: '对象范围',
          v: String(run.object_mode || '').toUpperCase() === 'ALL' ? '全部对象' : '自定义选择',
        },
        { k: '产物大小', v: formatBytes(rc.backup_size_bytes) },
        { k: '解压预估', v: formatBytes(rc.estimated_size_bytes) },
        { k: '版本', v: `${rc.source_version || '-'} → ${rc.target_version || '-'}` },
        // 源实例取备份集登记的来源实例（后端 join insight_backup_sets.source_instance_id），
        // 名称与地址各截断 20 字符，与「版本」同一行，完整值放 title
        {
          k: '源实例',
          v: clipText(run.source_remark, SOURCE_CLIP) || sourceAddr.value,
          suffix: run.source_remark ? sourceAddr.value : '',
          title: sourceFull.value,
        },
      ],
    },
    {
      key: 'tgt',
      title: '目标实例与对象',
      cols: 3,
      fields: [
        // 实例主键对使用者无意义，只展示实例名称/地址
        { k: '目标实例', v: run.target_remark || targetHostPort.value },
        { k: '连接地址', v: targetHostPort.value, mono: true },
        { k: '目标层级', v: targetLevel },
        { k: '目标库', v: run.target_db_name || '-' },
        { k: '恢复后对象', v: run.target_display || '-' },
        { k: '目标版本', v: rc.target_version || '-' },
      ],
    },
    {
      key: 'policy',
      title: '恢复策略',
      cols: 2,
      fields: [
        // 底层枚举（NEW/OVERWRITE…）只对排障有用，使用者侧只保留中文语义
        { k: '底层恢复模式', v: restoreModeLabel(run.restore_mode) },
        {
          k: '恢复内容',
          v: `${o.restore_schema ? '表结构 ' : ''}${o.restore_data ? '＋ 全量数据' : '（仅结构）'}`.trim() || '-',
        },
        {
          k: '结构冲突策略',
          v:
            SCHEMA_POLICY_LABELS[String(o.schema_conflict_policy || '').toUpperCase()] ||
            o.schema_conflict_policy ||
            '-',
        },
        {
          k: '数据冲突策略',
          v:
            DATA_POLICY_LABELS[String(o.data_conflict_policy || '').toUpperCase()] ||
            o.data_conflict_policy ||
            '-',
        },
      ],
    },
    {
      key: 'time',
      title: '时间信息',
      cols: 4,
      fields: [
        { k: '创建时间', v: formatDateTime(run.created_at), mono: true },
        { k: '开始时间', v: formatDateTime(run.start_at), mono: true },
        { k: '结束时间', v: formatDateTime(run.end_at), mono: true },
        {
          k: '最近上报',
          v: formatDateTime(run.last_report_at || run.updated_at) + (isActive.value ? '（持续刷新）' : ''),
          mono: true,
        },
      ],
    },
  ]
})

const collapsed = ref({ src: false, tgt: false, policy: false, time: true })
const toggleGroup = (key) => {
  collapsed.value = { ...collapsed.value, [key]: !collapsed.value[key] }
}

/* ==================== 日志 ==================== */
const activeTab = ref('log')
const onlyError = ref(false)

const logLines = computed(() => {
  const raw = String(props.logExcerpt || props.run?.log_excerpt || '').trim()
  if (!raw) return []
  return raw
    .split('\n')
    .filter((line) => String(line).trim())
    .map((line) => {
      const text = String(line)
      let level = 'INFO'
      if (/error|错误|失败|fatal/i.test(text)) level = 'ERROR'
      else if (/warn|警告/i.test(text)) level = 'WARN'
      else if (/\bOK\b|成功|complete/i.test(text)) level = 'OK'
      const cls = level === 'ERROR' ? 'error' : level === 'WARN' ? 'warn' : level === 'OK' ? 'ok' : ''
      return { raw: text, level, cls }
    })
})
const errorLineCount = computed(() => logLines.value.filter((line) => line.level === 'ERROR').length)
const visibleLogLines = computed(() =>
  onlyError.value ? logLines.value.filter((line) => line.level === 'ERROR') : logLines.value,
)

const copyText = async (text) => {
  const value = String(text || '').trim()
  if (!value) {
    message.warning('没有可复制的内容')
    return
  }
  try {
    await navigator.clipboard.writeText(value)
    message.success('已复制')
  } catch {
    message.warning('当前浏览器不支持自动复制，请手动选择文本')
  }
}
const copyLog = () => copyText(logLines.value.map((line) => line.raw).join('\n'))
const openFullLog = () => emit('view-log', 0)

/* ==================== 定位 / 跳转 ==================== */
const bodyRef = ref()
const scrollTo = (selector, block) => {
  const el = typeof selector === 'string' ? document.querySelector(selector) : selector
  if (!el) return
  if (typeof el.scrollIntoView === 'function') el.scrollIntoView({ block: block || 'center', behavior: 'smooth' })
}
// 滚动容器已上提到弹窗 body（或详情页外壳），切换任务时要连外层一起归零
const scrollHostOf = () => {
  let node = bodyRef.value?.parentElement
  while (node && node !== document.body) {
    const overflowY = window.getComputedStyle(node).overflowY
    if (overflowY === 'auto' || overflowY === 'scroll') return node
    node = node.parentElement
  }
  return null
}
const resetScrollTop = () => {
  if (bodyRef.value) bodyRef.value.scrollTop = 0
  const host = scrollHostOf()
  if (host) host.scrollTop = 0
}
// 对象级明细表已移除，失败跳转改为切到「异常明细」Tab 并滚到日志区块
const showException = () => {
  activeTab.value = 'exception'
  scrollTo('#rrd-sec-log', 'start')
}

const exceptionAdvice = (item) => {
  const text = String(item?.error_message || '')
  if (/duplicate/i.test(text)) return '确认目标对象残留数据后按 data_conflict_policy=TRUNCATE 重跑该对象。'
  if (/no space|disk/i.test(text)) return '清理目标主机磁盘至解压体积的 1.5 倍以上，并适当下调 parallel 后重试。'
  if (/permission|denied|privilege/i.test(text)) return '补齐恢复账号的 CREATE/INSERT/DROP 权限后重试。'
  return '确认目标对象残留数据与冲突策略后重跑该对象，或查看完整日志定位根因。'
}

/* ==================== 预检查 pill ==================== */
const precheckPillClass = (item) => {
  const token = precheckToken(item)
  return token === 'passed' ? 't-success' : token === 'warning' ? 't-warning' : 't-danger'
}

// 切换任务时重置交互状态，避免上一个任务的 Tab/过滤/折叠残留
watch(
  () => props.run?.id,
  () => {
    activeTab.value = 'log'
    onlyError.value = false
    collapsed.value = { src: false, tgt: false, policy: false, time: true }
    resetScrollTop()
  },
)
</script>

<style scoped lang="scss">
/* ==========================================================================
   设计令牌：4px 基线栅格；主色沿用 teal(#0f766e)；状态色语义全局唯一
   ========================================================================== */
.rrd {
  --brand: #0f766e;
  --brand-weak: #e6f4f1;
  --success: #16a34a;
  --success-weak: #eaf7ee;
  --running: #1677ff;
  --running-weak: #e8f2ff;
  --warning: #fa8c16;
  --warning-weak: #fff5e8;
  --danger: #cf1322;
  --danger-weak: #fdecee;
  --idle: #8c8c8c;
  --idle-weak: #f5f5f5;
  --text-1: #1f2329;
  --text-2: #4e5969;
  --text-3: #86909c;
  --border: #e5e6eb;
  --border-light: #f0f0f0;
  --bg-soft: #fafbfc;
  --radius: 8px;

  display: flex;
  flex-direction: column;
  /* 高度由内容决定：滚动交给外层弹窗 body，面板只负责 sticky 的头尾 */
  min-height: 0;
  background: #fff;
  color: var(--text-1);
}

.num {
  font-variant-numeric: tabular-nums;
  font-feature-settings: 'tnum';
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12.5px;
}

/* ===================== Header ===================== */
.rrd-header {
  flex-shrink: 0;
  padding: 16px 24px 12px;
  border-bottom: 1px solid var(--border-light);
  position: sticky;
  top: 0;
  z-index: 3;
  background: #fff;
}
.rrd-head-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-right: 32px;
}
.rrd-title {
  font-size: 16px;
  font-weight: 600;
  margin: 0;
}
.rrd-task-name {
  font-size: 13px;
  color: var(--text-2);
  border-left: 1px solid var(--border);
  padding-left: 10px;
  max-width: 420px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.head-spacer {
  flex: 1;
}
.rrd-head-interval {
  margin-left: 2px;
  color: var(--text-3);
}
.rrd-sub {
  margin-top: 6px;
  color: var(--text-3);
  font-size: 12.5px;
  display: flex;
  gap: 14px;
  flex-wrap: wrap;
  align-items: center;
}
.rrd-sub code {
  background: var(--idle-weak);
  padding: 1px 5px;
  border-radius: 4px;
  font-size: 12px;
}
.close-x {
  position: absolute;
  top: 12px;
  right: 16px;
  width: 28px;
  height: 28px;
  border: 0;
  background: transparent;
  border-radius: 6px;
  cursor: pointer;
  color: var(--text-3);
  display: flex;
  align-items: center;
  justify-content: center;
}
.close-x:hover {
  background: var(--idle-weak);
  color: var(--text-1);
}

/* ===================== 主体：不自带滚动条 =====================
   滚动条由外层弹窗 body（或详情页外壳）承载，贴容器右边缘；
   这里若再开 overflow 会出现「里层容器在滚」的错位观感。 */
.rrd-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow: visible;
  padding: 16px 24px 8px;
}

/* ===================== Footer ===================== */
.rrd-footer {
  flex-shrink: 0;
  position: sticky;
  bottom: 0;
  z-index: 3;
  padding: 12px 24px 16px;
  border-top: 1px solid var(--border-light);
  display: flex;
  align-items: center;
  gap: 8px;
  background: #fff;
}
.footer-spacer {
  flex: 1;
}
.footer-tip {
  font-size: 12px;
  color: var(--text-3);
}

/* ===================== 按钮 ===================== */
.btn {
  height: 32px;
  padding: 0 14px;
  border-radius: 6px;
  border: 1px solid var(--border);
  background: #fff;
  color: var(--text-2);
  cursor: pointer;
  font-size: 13px;
  font-family: inherit;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  transition: all 0.15s;
}
.btn:hover:not(:disabled) {
  color: var(--brand);
  border-color: var(--brand);
}
.btn:disabled {
  background: var(--idle-weak);
  color: #bfbfbf;
  border-color: var(--border);
  cursor: not-allowed;
}
.btn-primary {
  background: var(--brand);
  border-color: var(--brand);
  color: #fff;
}
.btn-primary:hover:not(:disabled) {
  background: #115e59;
  border-color: #115e59;
  color: #fff;
}
.btn-danger {
  color: var(--danger);
  border-color: #f5b5bb;
}
.btn-danger:hover:not(:disabled) {
  color: #fff;
  background: var(--danger);
  border-color: var(--danger);
}
.btn-sm {
  height: 26px;
  padding: 0 10px;
  font-size: 12.5px;
}

/* ===================== 状态语义 ===================== */
.pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 22px;
  padding: 0 8px;
  border-radius: 11px;
  font-size: 12px;
  font-weight: 500;
  line-height: 1;
  white-space: nowrap;
}
.t-success {
  background: var(--success-weak);
  color: var(--success);
}
.t-running {
  background: var(--running-weak);
  color: var(--running);
}
.t-warning {
  background: var(--warning-weak);
  color: #d46b08;
}
.t-danger {
  background: var(--danger-weak);
  color: var(--danger);
}
.t-idle {
  background: var(--idle-weak);
  color: var(--idle);
}
.t-brand {
  background: var(--brand-weak);
  color: var(--brand);
}
.dot-live {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  animation: rrd-pulse 1.4s infinite;
}
@keyframes rrd-pulse {
  0% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.35;
    transform: scale(0.75);
  }
  100% {
    opacity: 1;
    transform: scale(1);
  }
}

/* ===================== 分层区块 ===================== */
.sec {
  margin-bottom: 16px;
}
.sec-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}
.sec-idx {
  width: 18px;
  height: 18px;
  border-radius: 5px;
  background: var(--brand-weak);
  color: var(--brand);
  font-size: 11px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
  flex: none;
}
.sec-title {
  font-size: 14px;
  font-weight: 600;
  margin: 0;
}
.sec-hint {
  font-size: 12px;
  color: var(--text-3);
}
.sec-line {
  flex: 1;
  height: 1px;
  background: var(--border-light);
}
.card {
  background: #fff;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
}
.empty-tip {
  padding: 22px;
  text-align: center;
  color: var(--text-3);
  font-size: 13px;
}

/* ===================== ① 任务概览 ===================== */
.hero {
  display: block;
}
.hero-left {
  padding: 14px 16px;
  background: var(--bg-soft);
}
.hero-pills {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}
/* 概览：任务名称 / 任务ID / 目标实例 同一行等分，放不下时换行 */
.kv-list {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  gap: 8px 28px;
}
.kv-list .kv {
  flex: 1 1 0;
  align-items: center;
}
.kv-list .kv-v {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.alert-strip {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 12px;
  border-radius: var(--radius);
  margin-top: 12px;
  font-size: 13px;
}
.alert-strip.danger {
  background: var(--danger-weak);
  border: 1px solid #f7c1c6;
  color: #a8071a;
}
.alert-strip.warning {
  background: var(--warning-weak);
  border: 1px solid #ffd8a8;
  color: #ad4e00;
}
.alert-strip .as-body {
  flex: 1;
  min-width: 0;
}
.alert-strip .as-title {
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 6px;
}
.alert-strip .as-msg {
  margin-top: 2px;
  word-break: break-all;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12.5px;
}

/* ===================== 键值栅格 ===================== */
.kv-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px 20px;
  padding: 2px 0;
}
.kv-grid.cols-2 {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
.kv-grid.cols-4 {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}
/* 长文本字段（如产物路径）独占整行，单行省略 + title 兜底 */
.kv.full {
  grid-column: 1 / -1;
}
.kv.full .kv-v {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
/* 标签与值用「：」直连，中间不留间距 */
.kv {
  display: flex;
  align-items: flex-start;
  gap: 0;
  min-width: 0;
}
.kv-k {
  flex: none;
  max-width: 50%;
  color: var(--text-3);
  font-size: 13px;
  line-height: 20px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.kv-colon {
  flex: none;
  color: var(--text-3);
  font-size: 13px;
  line-height: 20px;
}
.kv-v {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  color: var(--text-2);
  line-height: 20px;
  word-break: break-all;
}
.kv-suffix {
  color: var(--text-3);
}
.copy-btn {
  border: 0;
  background: transparent;
  padding: 0 2px;
  color: var(--text-3);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
}
.copy-btn:hover {
  color: var(--brand);
}

/* ===================== ② 折叠分组 ===================== */
.rrd-groups {
  display: flex;
  flex-direction: column;
}
.group {
  border: 1px solid var(--border);
  border-radius: var(--radius);
  margin-bottom: 10px;
  overflow: hidden;
  background: #fff;
}
.group:last-child {
  margin-bottom: 0;
}
.group-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 14px;
  background: var(--bg-soft);
  cursor: pointer;
  user-select: none;
}
.group-head:hover {
  background: #f2f3f5;
}
.group-title {
  font-size: 13px;
  font-weight: 600;
}
.group-caret {
  transition: transform 0.18s;
  color: var(--text-3);
  display: flex;
}
.group.collapsed .group-caret {
  transform: rotate(0deg);
}
.group:not(.collapsed) .group-caret {
  transform: rotate(90deg);
}
.group.collapsed .group-body {
  display: none;
}
.group-body {
  padding: 12px 14px;
  border-top: 1px solid var(--border-light);
}
.group-hint {
  margin-top: 10px;
  font-size: 12.5px;
  color: var(--text-3);
  display: flex;
  gap: 6px;
  align-items: flex-start;
}

/* ===================== 表格 ===================== */
.tbl {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.tbl th {
  background: var(--bg-soft);
  color: var(--text-1);
  font-weight: 600;
  text-align: left;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
  white-space: nowrap;
}
.tbl td {
  padding: 9px 12px;
  border-bottom: 1px solid var(--border-light);
  color: var(--text-2);
  vertical-align: middle;
}
.tbl tr:last-child td {
  border-bottom: 0;
}
.tbl tbody tr:hover td {
  background: #fafbfc;
}
.tbl .right {
  text-align: right;
}
.err-text {
  color: #a8071a;
}

/* ===================== ⑤ Tab ===================== */
.tabs {
  display: flex;
  gap: 2px;
  border-bottom: 1px solid var(--border);
  padding: 0 8px;
  background: var(--bg-soft);
}
.tab {
  border: 0;
  background: transparent;
  padding: 9px 14px;
  font-size: 13px;
  color: var(--text-2);
  cursor: pointer;
  font-family: inherit;
  position: relative;
}
.tab:hover {
  color: var(--brand);
}
.tab.on {
  color: var(--brand);
  font-weight: 600;
}
.tab.on::after {
  content: '';
  position: absolute;
  left: 12px;
  right: 12px;
  bottom: -1px;
  height: 2px;
  background: var(--brand);
  border-radius: 2px;
}
.tab-badge {
  margin-left: 5px;
  background: var(--danger);
  color: #fff;
  border-radius: 9px;
  font-size: 11px;
  padding: 0 5px;
  font-weight: 500;
}
.tab-badge.warn {
  background: var(--warning);
}
.tab-pane {
  padding: 14px 16px;
}

.log-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}
.log-meta {
  font-size: 12.5px;
  color: var(--text-3);
}
.log-spacer {
  flex: 1;
}
.log-path {
  color: var(--text-3);
}
.chk {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12.5px;
  color: var(--text-2);
  cursor: pointer;
  user-select: none;
}
.log-view {
  max-height: 248px;
  overflow: auto;
  background: #1e2229;
  border-radius: var(--radius);
  padding: 12px 14px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12.5px;
  line-height: 1.75;
  color: #c9d1d9;
}
.log-view::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}
.log-view::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.22);
  border-radius: 4px;
}
.log-line {
  white-space: pre-wrap;
  word-break: break-all;
}
.log-line.warn .lm {
  color: #ffc36b;
}
.log-line.error .lm {
  color: #ff8b8b;
}
.log-line.ok .lm {
  color: #7ee787;
}
.log-empty {
  color: #7d8590;
}

/* ===================== 预检查 / 异常 ===================== */
.pc-summary {
  margin-bottom: 8px;
  font-size: 12.5px;
  color: var(--text-3);
}
.pc-item {
  display: flex;
  gap: 10px;
  padding: 9px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  margin-bottom: 6px;
  background: #fff;
}
.pc-item.blocked {
  border-color: #f7c1c6;
  background: var(--danger-weak);
}
.pc-item.warning {
  border-color: #ffd8a8;
  background: var(--warning-weak);
}
.pc-item.passed {
  border-color: var(--border-light);
}
.pc-icon {
  flex: none;
  margin-top: 1px;
}
.pc-main {
  flex: 1;
  min-width: 0;
}
.pc-label {
  font-size: 13px;
  color: var(--text-1);
  font-weight: 500;
}
.pc-msg {
  font-size: 12.5px;
  color: var(--text-2);
  margin-top: 2px;
}
.pc-fix {
  font-size: 12.5px;
  color: #ad4e00;
  margin-top: 4px;
  display: flex;
  gap: 5px;
  align-items: flex-start;
}
.pc-key {
  font-size: 11.5px;
  color: var(--text-3);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.c-success {
  color: #16a34a;
}
.c-warning {
  color: #fa8c16;
}
.c-danger {
  color: #cf1322;
}

@media (max-width: 1280px) {
  .kv-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .kv-grid.cols-4 {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 768px) {
  .kv-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
