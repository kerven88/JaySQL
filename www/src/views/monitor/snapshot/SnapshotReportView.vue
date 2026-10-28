<template>
  <div class="gi-page-shell snapshot-report-page">
    <a-card class="section-card" :bordered="false">
      <!-- 未就绪：进度 / 失败原因 -->
      <template v-if="detail && !isReady">
        <div class="report-pending">
          <a-result
            v-if="detail.status === 'failed'"
            status="error"
            title="快照生成失败"
            :sub-title="detail.error_message || '未知原因'"
          >
            <template #extra>
              <a-button type="primary" @click="goHome">返回列表</a-button>
            </template>
          </a-result>
          <div v-else class="pending-body">
            <a-progress type="circle" :percent="detail.progress || 0" />
            <div class="pending-text">
              <div class="t">{{ statusText(detail.status) }}</div>
              <div class="sub">
                {{ detail.instance_name }} · {{ detail.db_type }} · 观察窗口 {{ detail.window_minutes }} 分钟
              </div>
              <div class="sub" v-if="detail.window_end_at">
                预计 {{ detail.window_end_at }} 完成终点采样
              </div>
            </div>
          </div>
        </div>
      </template>

      <!-- 就绪：报告正文 -->
      <template v-else-if="detail && isReady">
        <div class="rp-head">
          <div class="t">
            {{ detail.instance_name }}
            <span class="rp-tag">快照 #{{ detail.id }} · ready</span>
          </div>
          <div class="m">
            <span v-if="windowInfo">窗口 {{ windowInfo.start }} → {{ windowInfo.end }}（{{ Math.round(windowInfo.seconds / 60) }} 分钟）</span>
            <span>{{ detail.db_type }} {{ detail.db_version }}</span>
            <span>生成人 {{ detail.created_by || '—' }}</span>
          </div>
        </div>

        <div class="rp-nav">
          <a v-for="sec in visibleSections" :key="sec.key" @click="scrollTo(sec.id)">{{ sec.title }}</a>
          <span class="rp-spacer"></span>
          <a class="export" title="导出单文件 HTML 报告（离线可看）" @click="exportHtml">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 15V3"/><path d="m7 8 5-5 5 5"/><path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2"/></svg>
            导出
          </a>
          <a-popconfirm v-if="canManageSnapshot" title="确认删除该快照？" @confirm="removeSnapshot">
            <a class="danger">删除</a>
          </a-popconfirm>
          <a @click="goHome">← 返回列表</a>
        </div>

        <!-- A 基本信息 -->
        <div class="card" id="sec-basics">
          <h2><span class="no">A</span>数据库基本信息</h2>
          <div class="kv" v-if="sectionData.basics">
            <div class="item" v-for="row in basicsItems" :key="row.k">
              <div class="k">{{ row.k }}</div><div class="v">{{ row.v }}</div>
            </div>
          </div>
        </div>

        <!-- B SQL 执行/最耗资源 -->
        <div class="card" id="sec-sql_top">
          <h2><span class="no">B</span>SQL 执行次数 / 最耗资源 Top（窗口差分）</h2>
          <SectionUnsupported v-if="isUnsupported('sql_top')" :reason="unsupportedReason('sql_top')" />
          <template v-else-if="sectionData.sql_top">
            <a-table
              :columns="sqlTopColumns"
              :data-source="sqlTopRows"
              :pagination="false"
              row-key="idx"
              size="small"
              :scroll="{ x: 900 }"
            >
              <template #bodyCell="{ column, record }">
                <template v-if="column.key === 'digest_text'">
                  <code class="sql-code">{{ record.digest_text }}</code>
                </template>
                <template v-else-if="column.key === 'count'">{{ formatCount(record.count) }}</template>
                <template v-else-if="column.key === 'time_sec'">{{ fmtMetricNum(record.time_sec) }} s</template>
                <template v-else-if="column.key === 'avg_ms'">{{ fmtMetricNum(record.avg_ms) }} ms</template>
                <template v-else-if="column.key === 'rows_examined'">
                  {{ record.rows_examined === null ? '—' : formatCount(record.rows_examined) }}
                </template>
              </template>
            </a-table>
          </template>
        </div>

        <!-- C 慢 SQL -->
        <div class="card" id="sec-slow">
          <h2><span class="no">C</span>慢 SQL</h2>
          <SectionUnsupported v-if="isUnsupported('slow')" :reason="unsupportedReason('slow')" />
          <p v-else-if="sectionData.slow?.note" class="foot-note">{{ sectionData.slow.note }}</p>
          <p v-if="sectionData.slow?.slow_total !== undefined && sectionData.slow?.slow_total !== null" class="slow-count">
            窗口内慢查询（Slow_queries 差分）：<b class="num">{{ formatCount(sectionData.slow.slow_total) }}</b> 条
          </p>
        </div>

        <!-- D 锁 -->
        <div class="card" id="sec-locks">
          <h2><span class="no">D</span>锁与阻塞</h2>
          <template v-if="sectionData.locks">
            <div class="lock-stats">
              <div class="item" v-if="sectionData.locks.lock_waits !== null && sectionData.locks.lock_waits !== undefined">
                <div class="k">锁等待次数 / 秒</div><div class="v num">{{ fmtMetricNum(sectionData.locks.lock_waits) }}</div>
              </div>
              <div class="item" v-if="sectionData.locks.lock_time_sec">
                <div class="k">锁等待时间 / 秒</div><div class="v num">{{ fmtMetricNum(sectionData.locks.lock_time_sec) }}</div>
              </div>
              <div class="item" v-if="sectionData.locks.deadlocks !== null && sectionData.locks.deadlocks !== undefined">
                <div class="k">死锁差分</div><div class="v num">{{ fmtMetricNum(sectionData.locks.deadlocks) }}</div>
              </div>
            </div>
            <h3 class="sec" v-if="blockingRows.length">采样时刻阻塞链</h3>
            <a-table
              v-if="blockingRows.length"
              :columns="blockingColumns"
              :data-source="blockingRows"
              :pagination="false"
              row-key="idx"
              size="small"
              :scroll="{ x: 860 }"
            >
              <template #bodyCell="{ column, record }">
                <template v-if="column.key === 'waiting_query' || column.key === 'blocking_query'">
                  <code class="sql-code">{{ record[column.key] || '—' }}</code>
                </template>
              </template>
            </a-table>
            <h3 class="sec" v-if="longTrxRows.length">长事务 / 当前事务</h3>
            <a-table
              v-if="longTrxRows.length"
              :columns="longTrxColumns"
              :data-source="longTrxRows"
              :pagination="false"
              row-key="idx"
              size="small"
              :scroll="{ x: 860 }"
            >
              <template #bodyCell="{ column, record }">
                <template v-if="column.key === 'trx_query'">
                  <code class="sql-code">{{ record.trx_query || '—' }}</code>
                </template>
              </template>
            </a-table>
            <p v-if="sectionData.locks.blocking_error" class="foot-note">{{ sectionData.locks.blocking_error }}</p>
          </template>
        </div>

        <!-- E 内存 -->
        <div class="card" id="sec-memory">
          <h2><span class="no">E</span>内存详细使用情况</h2>
          <div class="kv" v-if="memoryItems.length">
            <div class="item" v-for="row in memoryItems" :key="row.k">
              <div class="k">{{ row.k }}</div><div class="v">{{ row.v }}</div>
            </div>
          </div>
        </div>

        <!-- F 用户 -->
        <div class="card" id="sec-users">
          <h2><span class="no">F</span>用户列表（账号面盘点）</h2>
          <SectionUnsupported v-if="isUnsupported('users')" :reason="unsupportedReason('users')" />
          <template v-else-if="sectionData.users">
            <a-table
              :columns="userColumns"
              :data-source="userRows"
              :pagination="false"
              row-key="idx"
              size="small"
              :scroll="{ x: 760 }"
            />
            <h3 class="sec" v-if="userConnRows.length">窗口时刻连接分布</h3>
            <a-table
              v-if="userConnRows.length"
              :columns="connColumns"
              :data-source="userConnRows"
              :pagination="false"
              row-key="idx"
              size="small"
            />
          </template>
        </div>

        <!-- G 库大小 -->
        <div class="card" id="sec-databases">
          <h2><span class="no">G</span>每个库的大小</h2>
          <SectionUnsupported v-if="isUnsupported('databases')" :reason="unsupportedReason('databases')" />
          <template v-else-if="databaseRows.length">
            <div class="bar-row" v-for="row in databaseRows" :key="row.name">
              <span class="nm">{{ row.name }}</span>
              <div class="bar"><i :style="{ width: row.pct + '%', background: '#0f766e' }"></i></div>
              <span class="num">{{ formatBytes(row.size_bytes) }}</span>
            </div>
          </template>
        </div>

        <!-- H Top 表 -->
        <div class="card" id="sec-tables">
          <h2><span class="no">H</span>最大的 50 张表</h2>
          <SectionUnsupported v-if="isUnsupported('tables')" :reason="unsupportedReason('tables')" />
          <a-table
            v-else-if="tableRows.length"
            :columns="tableColumns"
            :data-source="tableRows"
            :pagination="{ pageSize: 10 }"
            row-key="idx"
            size="small"
            :scroll="{ x: 900 }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'table_name'">
                <code>{{ record.table_schema || record.schemaname }}.{{ record.table_name || record.relname }}</code>
              </template>
              <template v-else-if="column.key === 'size_bytes'">{{ formatBytes(record.size_bytes) }}</template>
              <template v-else-if="column.key === 'rows'">{{ formatCount(record.rows) }}</template>
            </template>
          </a-table>
        </div>

        <!-- I 索引 -->
        <div class="card" id="sec-indexes">
          <h2><span class="no">I</span>索引情况（未被使用）</h2>
          <SectionUnsupported v-if="isUnsupported('indexes')" :reason="unsupportedReason('indexes')" />
          <template v-else-if="indexRows.length">
            <a-table
              :columns="indexColumns"
              :data-source="indexRows"
              :pagination="false"
              row-key="idx"
              size="small"
              :scroll="{ x: 760 }"
            >
              <template #bodyCell="{ column, record }">
                <template v-if="column.key === 'index_name'">
                  <code>{{ record.object_schema || record.schemaname }}.{{ record.object_name || record.relname }}({{ record.index_name || record.indexrelname }})</code>
                </template>
                <template v-else-if="column.key === 'size_bytes'">{{ record.size_bytes ? formatBytes(record.size_bytes) : '—' }}</template>
              </template>
            </a-table>
            <p class="foot-note">MySQL 口径为自启动累计 0 次扫描；PG 为 idx_scan = 0。下线前请结合业务低峰期确认。</p>
          </template>
          <p v-else class="foot-note">未发现自启动以来从未被使用的二级索引。</p>
        </div>

        <!-- J 参数 -->
        <div class="card" id="sec-params">
          <h2><span class="no">J</span>参数值（关键项）</h2>
          <a-table
            v-if="paramRows.length"
            :columns="paramColumns"
            :data-source="paramRows"
            :pagination="{ pageSize: 15 }"
            row-key="name"
            size="small"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'value'"><code>{{ record.value }}</code></template>
            </template>
          </a-table>
        </div>

        <!-- K 二进制日志 / WAL -->
        <div class="card" id="sec-binlog">
          <h2><span class="no">K</span>{{ isPG ? 'WAL 与复制槽' : '二进制日志' }}</h2>
          <div class="kv" v-if="binlogItems.length">
            <div class="item" v-for="row in binlogItems" :key="row.k">
              <div class="k">{{ row.k }}</div><div class="v">{{ row.v }}</div>
            </div>
          </div>
          <a-table
            v-if="binlogSlotRows.length"
            class="mt12"
            :columns="slotColumns"
            :data-source="binlogSlotRows"
            :pagination="false"
            row-key="idx"
            size="small"
          />
        </div>
      </template>
    </a-card>
  </div>
</template>

<script setup>
import { computed, defineComponent, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { message } from 'ant-design-vue'
import { formatBytes, formatCount, fmtMetricNum } from '../utils'
import { useUserStore } from '@/store/user'
import {
  deleteLoadSnapshotApi,
  getLoadSnapshotDetailApi,
} from '@/api/monitor'

defineOptions({ name: 'MonitorSnapshotReportView' })

const userStore = useUserStore()

// 「删除」与生成同属管理操作，口径与列表页/后端守卫一致：performance.manage。
const canManageSnapshot = computed(() => userStore.hasPermission('performance.manage'))

const route = useRoute()
const router = useRouter()
const detail = ref(null)
let pollTimer = null

// 章节降级提示（局部小组件）
const SectionUnsupported = defineComponent({
  name: 'SectionUnsupported',
  props: { reason: { type: String, default: '' } },
  setup(props) {
    return () => h('div', { class: 'unsupported-box' }, [
      h('span', { class: 'tag-n' }, '不支持'),
      h('span', { class: 'reason' }, props.reason || '当前实例不具备该章节的采集条件'),
    ])
  },
})

const SECTIONS = [
  { key: 'basics', id: 'sec-basics', title: '基本信息' },
  { key: 'sql_top', id: 'sec-sql_top', title: 'SQL 执行/最耗资源' },
  { key: 'slow', id: 'sec-slow', title: '慢 SQL' },
  { key: 'locks', id: 'sec-locks', title: '锁' },
  { key: 'memory', id: 'sec-memory', title: '内存使用' },
  { key: 'users', id: 'sec-users', title: '用户' },
  { key: 'databases', id: 'sec-databases', title: '库大小' },
  { key: 'tables', id: 'sec-tables', title: 'Top 表' },
  { key: 'indexes', id: 'sec-indexes', title: '索引' },
  { key: 'params', id: 'sec-params', title: '参数' },
  { key: 'binlog', id: 'sec-binlog', title: '日志' },
]

const isPG = computed(() => String(detail.value?.db_type || '').toLowerCase().includes('postgre'))
const isReady = computed(() => detail.value?.status === 'ready')
const payload = computed(() => {
  if (!detail.value?.payload) return null
  if (typeof detail.value.payload === 'string') {
    try { return JSON.parse(detail.value.payload) } catch { return null }
  }
  return detail.value.payload
})
const sections = computed(() => payload.value?.sections || {})
const windowInfo = computed(() => payload.value?.window || null)
const sectionData = computed(() => {
  // 把每个 section 摊平：ok 的取 data，unsupported 保留状态
  const out = {}
  Object.entries(sections.value).forEach(([key, sec]) => {
    out[key] = sec?.status === 'ok' ? sec.data : { __unsupported: true, reason: sec?.reason }
  })
  return out
})

const visibleSections = computed(() => SECTIONS.filter((sec) => sections.value[sec.key]))

function isUnsupported(key) {
  return !!sectionData.value[key]?.__unsupported
}
function unsupportedReason(key) {
  return sectionData.value[key]?.reason || ''
}

function statusText(status) {
  const map = {
    pending: '任务排队中',
    collecting: '正在连接实例并采集数据',
    window: '基线已采集，等待观察窗口走完',
  }
  return map[status] || status
}

// ---- A 基本信息 ----
const basicsItems = computed(() => {
  const data = sectionData.value.basics
  if (!data || data.__unsupported) return []
  const fmtUptime = (sec) => {
    const n = Number(sec)
    if (!Number.isFinite(n) || n <= 0) return '—'
    const days = Math.floor(n / 86400)
    const hours = Math.floor((n % 86400) / 3600)
    return days > 0 ? `${days} 天 ${hours} 小时` : `${hours} 小时`
  }
  const rows = [
    { k: '版本', v: data.version || '—' },
    { k: '已运行', v: fmtUptime(data.uptime_seconds) },
    { k: '字符集 / 排序规则', v: `${data.charset || '—'} / ${data.collation || '—'}` },
    { k: '时区', v: data.time_zone || '—' },
  ]
  if (data.version_comment) rows.splice(1, 0, { k: '版本说明', v: data.version_comment })
  if (isPG.value && data.start_time) rows.push({ k: '实例启动时间', v: data.start_time })
  if (data.threads_connected !== undefined && data.threads_connected !== null) {
    rows.push({ k: '当前连接 / 活跃', v: `${formatCount(data.threads_connected)} / ${formatCount(data.threads_running)}` })
  }
  if (data.qps !== undefined && data.qps !== null) rows.push({ k: 'QPS（采样时刻）', v: formatCount(data.qps) })
  return rows
})

// ---- B SQL Top ----
const sqlTopColumns = [
  { title: 'SQL 指纹', key: 'digest_text', width: 380 },
  { title: '执行次数', key: 'count', width: 100 },
  { title: '总耗时', key: 'time_sec', width: 100 },
  { title: '平均', key: 'avg_ms', width: 90 },
  { title: '扫描行 / 秒', key: 'rows_examined', width: 110 },
]
const sqlTopRows = computed(() => {
  const items = sectionData.value.sql_top?.items || []
  return items.map((item, index) => ({ ...item, idx: index }))
})

// ---- D 锁 ----
const blockingColumns = [
  { title: '被堵会话', dataIndex: 'waiting_thread', key: 'waiting_thread', width: 110 },
  { title: '被堵语句', key: 'waiting_query', width: 280 },
  { title: '堵塞者', dataIndex: 'blocking_thread', key: 'blocking_thread', width: 110 },
  { title: '堵塞语句', key: 'blocking_query', width: 240 },
  { title: '已等待 / 秒', dataIndex: 'wait_secs', key: 'wait_secs', width: 100 },
]
const blockingRows = computed(() => (sectionData.value.locks?.blocking || []).map((row, index) => ({ ...row, idx: index })))
const longTrxColumns = [
  { title: '事务', dataIndex: 'trx_id', key: 'trx_id', width: 150 },
  { title: '开始时间', dataIndex: 'trx_started', key: 'trx_started', width: 170 },
  { title: '已运行 / 秒', dataIndex: 'run_secs', key: 'run_secs', width: 100 },
  { title: '行锁 / 修改行', key: 'rows', width: 130,
    customRender: ({ record }) => `${record.trx_rows_locked ?? '—'} / ${record.trx_rows_modified ?? '—'}` },
  { title: '语句', key: 'trx_query' },
]
const longTrxRows = computed(() => (sectionData.value.locks?.long_trx || []).map((row, index) => ({ ...row, idx: index })))

// ---- E 内存 ----
const memoryItems = computed(() => {
  const data = sectionData.value.memory
  if (!data || data.__unsupported) return []
  const rows = []
  if (data.hit_ratio !== null && data.hit_ratio !== undefined) rows.push({ k: isPG.value ? '缓存命中率（差分）' : 'Buffer Pool 命中率（差分）', v: `${fmtMetricNum(data.hit_ratio)}%` })
  if (data.buffer_pool_size) rows.push({ k: 'Buffer Pool 大小', v: formatBytes(Number(data.buffer_pool_size)) })
  if (data.shared_buffers) rows.push({ k: 'shared_buffers', v: data.shared_buffers })
  if (data.work_mem) rows.push({ k: 'work_mem', v: data.work_mem })
  if (data.pages_total) rows.push({ k: '缓冲页 总/数据/脏/空闲', v: `${formatCount(data.pages_total)} / ${formatCount(data.pages_data)} / ${formatCount(data.pages_dirty)} / ${formatCount(data.pages_free)}` })
  if (data.tmp_disk_tables !== null && data.tmp_disk_tables !== undefined) rows.push({ k: '临时表落盘（次/秒）', v: fmtMetricNum(data.tmp_disk_tables) })
  if (data.temp_bytes !== null && data.temp_bytes !== undefined) rows.push({ k: '临时文件产生量（差分）', v: formatBytes(data.temp_bytes) })
  return rows
})

// ---- F 用户 ----
const userColumns = [
  { title: '用户', dataIndex: 'user', key: 'user', width: 140 },
  { title: '主机范围', dataIndex: 'host', key: 'host', width: 160 },
  ...isPG.value
    ? [{ title: '超级用户', dataIndex: 'rolsuper', key: 'rolsuper', width: 90 },
       { title: '可复制', dataIndex: 'rolreplication', key: 'rolreplication', width: 90 }]
    : [],
]
const userRows = computed(() => {
  const users = sectionData.value.users?.users || []
  return users.map((row, index) => ({ ...row, idx: index }))
})
const connColumns = [
  { title: '用户', dataIndex: 'user', key: 'user', width: 160 },
  { title: '连接数', dataIndex: 'conns', key: 'conns', width: 100 },
]
const userConnRows = computed(() => (sectionData.value.users?.connections || []).map((row, index) => ({ ...row, idx: index })))

// ---- G 库大小 ----
const databaseRows = computed(() => {
  const items = sectionData.value.databases?.items || []
  const max = Math.max(...items.map((row) => Number(row.size_bytes) || 0), 1)
  return items.map((row) => ({ ...row, pct: (Number(row.size_bytes) / max) * 100 }))
})

// ---- H Top 表 ----
const tableColumns = [
  { title: '表', key: 'table_name', width: 280 },
  { title: '大小', key: 'size_bytes', width: 110 },
  { title: '行数（估算）', key: 'rows', width: 110 },
  { title: '引擎', dataIndex: 'engine', key: 'engine', width: 100 },
]
const tableRows = computed(() => {
  const items = sectionData.value.tables?.items || []
  return items.map((row) => ({
    ...row,
    rows: isPG.value ? row.n_live_tup : row.table_rows,
    idx: `${row.table_schema || row.schemaname}.${row.table_name || row.relname}`,
  }))
})

// ---- I 索引 ----
const indexColumns = [
  { title: '索引', key: 'index_name' },
  { title: '扫描次数', dataIndex: 'idx_scan', key: 'idx_scan', width: 100 },
  { title: '大小', key: 'size_bytes', width: 110 },
]
const indexRows = computed(() => (sectionData.value.indexes?.unused || []).map((row, index) => ({ ...row, idx: index })))

// ---- J 参数 ----
const paramColumns = [
  { title: '参数', dataIndex: 'name', key: 'name', width: 280 },
  { title: '当前值', key: 'value' },
]
const paramRows = computed(() => sectionData.value.params?.items || [])

// ---- K binlog / WAL ----
const binlogItems = computed(() => {
  const data = sectionData.value.binlog
  if (!data || data.__unsupported) return []
  const rows = []
  const push = (k, v) => { if (v !== undefined && v !== null && v !== '') rows.push({ k, v }) }
  if (isPG.value) {
    push('wal_level', data.wal_level)
    push('archive_mode', data.archive_mode)
    if (data.wal_bytes_rate !== undefined && data.wal_bytes_rate !== null) push('WAL 生成速率（差分）', `${formatBytes(data.wal_bytes_rate)}/s`)
  } else {
    push('log_bin', data.log_bin)
    push('binlog_format / row_image', `${data.binlog_format || '—'} / ${data.row_image || '—'}`)
    push('sync_binlog', data.sync_binlog)
    push('过期策略', data.expire)
    push('binlog 总占用', data.total_bytes ? formatBytes(data.total_bytes) : '—')
    if (data.files?.length) push('binlog 文件数', String(data.files.length))
  }
  return rows
})
const slotColumns = [
  { title: '复制槽', dataIndex: 'slot_name', key: 'slot_name', width: 200 },
  { title: '类型', dataIndex: 'slot_type', key: 'slot_type', width: 120 },
  { title: '活跃', dataIndex: 'active', key: 'active', width: 80 },
  { title: '未消费堆积', dataIndex: 'retained_bytes', key: 'retained_bytes', width: 160,
    customRender: ({ text }) => (text ? formatBytes(Number(text)) : '—') },
]
const binlogSlotRows = computed(() => (sectionData.value.binlog?.slots || []).map((row, index) => ({ ...row, idx: index })))

// ---- 行为 ----
async function load() {
  const id = route.params.id
  try {
    const data = await getLoadSnapshotDetailApi(id)
    // 请求层 resolve 的是响应体 {code, data, message}，载荷在 .data 这一层
    detail.value = data?.data || null
    if (detail.value && !['ready', 'failed'].includes(detail.value.status)) {
      pollTimer = setTimeout(load, 5000)
    }
  } catch {
    message.error('快照不存在或已删除')
    router.replace('/performance/snapshot-report')
  }
}

function scrollTo(id) {
  document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function goHome() {
  router.push('/performance/snapshot-report')
}

async function removeSnapshot() {
  try {
    await deleteLoadSnapshotApi(detail.value.id)
    message.success('已删除')
    goHome()
  } catch {
    // 拦截器已 toast
  }
}

// 导出单文件 HTML：payload 直接内联，离线可看、可交给厂商
function exportHtml() {
  if (!payload.value || !detail.value) return
  const meta = {
    instance: detail.value.instance_name,
    dbType: detail.value.db_type,
    dbVersion: detail.value.db_version,
    status: detail.value.status,
    createdBy: detail.value.created_by,
    window: windowInfo.value,
  }
  // 防 script 结束标签提前闭合：JSON 里的 "</" 全部转义（注释里不能写字面闭合标签，否则 SFC 编译会在此截断 script 块）
  const safePayload = JSON.stringify(payload.value).split('</').join('<\\/')
  const html = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8"><title>JaySQL 负载快照报告 · ${detail.value.instance_name}</title>
<style>body{font:14px/1.6 -apple-system,'PingFang SC',sans-serif;color:rgba(0,0,0,.88);max-width:1100px;margin:24px auto;padding:0 16px}
h1{font-size:20px}h2{font-size:16px;border-bottom:1px solid #eee;padding-bottom:6px;margin-top:28px}
table{border-collapse:collapse;width:100%;font-size:12.5px;margin:10px 0}
th,td{border:1px solid #e5e9ef;padding:6px 9px;text-align:left;vertical-align:top}
th{background:#fafbfc}code{background:#f2f5f7;padding:1px 5px;border-radius:4px;font-size:11.5px;word-break:break-all}
.meta{color:rgba(0,0,0,.62);font-size:12.5px}.unsup{color:#d46b08;font-size:12.5px}</style></head><body>
<h1>JaySQL 负载快照报告</h1>
<p class="meta">实例：${meta.instance} · ${meta.dbType} ${meta.dbVersion || ''}<br>
${meta.window ? `窗口：${meta.window.start} → ${meta.window.end}（${Math.round(meta.window.seconds / 60)} 分钟）<br>` : ''}生成人：${meta.createdBy || '—'} · 生成于 ${new Date().toLocaleString()}</p>
<div id="report"></div>
<script>const PAYLOAD = ${safePayload}${'<'}/script>
<script>
(function(){
  var host=document.getElementById('report');var secs=PAYLOAD.sections||{};
  Object.keys(secs).forEach(function(key){
    var sec=secs[key];var h=document.createElement('h2');h.textContent=key;host.appendChild(h);
    if(sec.status!=='ok'){var p=document.createElement('p');p.className='unsup';p.textContent='[不支持] '+(sec.reason||'');host.appendChild(p);return;}
    var data=sec.data||{};var tbl;
    Object.keys(data).forEach(function(k){
      var v=data[k];
      if(Array.isArray(v)&&v.length&&typeof v[0]==='object'){
        var cap=document.createElement('div');cap.textContent=k;host.appendChild(cap);
        tbl=document.createElement('table');var trh=document.createElement('tr');
        Object.keys(v[0]).forEach(function(col){var th=document.createElement('th');th.textContent=col;trh.appendChild(th);});
        tbl.appendChild(trh);
        v.slice(0,50).forEach(function(row){var tr=document.createElement('tr');
          Object.keys(v[0]).forEach(function(col){var td=document.createElement('td');td.textContent=row[col]===null||row[col]===undefined?'—':String(row[col]);tr.appendChild(td);});
          tbl.appendChild(tr);});
        host.appendChild(tbl);
      } else if(v!==null&&v!==undefined&&typeof v!=='object'){
        var line=document.createElement('p');line.innerHTML='<b>'+k+'</b>: '+String(v);host.appendChild(line);
      }
    });
  });
})();
${'<'}/script></body></html>`
  const blob = new Blob([html], { type: 'text/html;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `load-snapshot-${detail.value.instance_name}-${Date.now()}.html`
  link.click()
  URL.revokeObjectURL(url)
  message.success('报告已导出（单文件 HTML，离线可看）')
}

onMounted(load)
onBeforeUnmount(() => {
  if (pollTimer) clearTimeout(pollTimer)
})
</script>

<style scoped lang="scss">
.snapshot-report-page {
  .rp-head {
    background: linear-gradient(135deg, #0f766e, #115e59);
    color: #fff;
    border-radius: var(--gi-radius-md, 8px);
    padding: 18px 22px;
    margin-bottom: 14px;
    .t { font-size: 17px; font-weight: 600; display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
    .rp-tag { background: rgba(255, 255, 255, 0.18); border-radius: 4px; padding: 1px 8px; font-size: 12px; font-weight: 500; }
    .m { margin-top: 7px; font-size: 12px; opacity: 0.88; display: flex; gap: 18px; flex-wrap: wrap; }
  }
  .rp-nav {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    align-items: center;
    margin-bottom: 14px;
    a {
      font-size: 12px;
      color: var(--gi-text-secondary, rgba(0, 0, 0, 0.62));
      text-decoration: none;
      padding: 4px 10px;
      border-radius: 6px;
      border: 1px solid var(--gi-border-color, #e5e9ef);
      background: #fff;
      cursor: pointer;
      display: inline-flex;
      align-items: center;
      gap: 5px;
      &:hover { color: #115e59; border-color: #0f766e; }
      &.export { background: #e6f2f1; border-color: transparent; color: #115e59; font-weight: 500; }
      &.danger { color: var(--gi-color-error, #ff4d4f); }
    }
    .rp-spacer { flex: 1; }
  }
  .card {
    background: #fff;
    border: 1px solid var(--gi-border-color, #e5e9ef);
    border-radius: 8px;
    padding: 18px 20px;
    margin-bottom: 14px;
    h2 { font-size: 15px; font-weight: 600; margin-bottom: 10px; display: flex; align-items: center; gap: 8px; }
    .no {
      display: inline-flex; align-items: center; justify-content: center;
      width: 22px; height: 22px; border-radius: 6px;
      background: #e6f2f1; color: #115e59; font-size: 12px; font-weight: 600; flex: none;
    }
  }
  .kv {
    display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); gap: 10px 18px; font-size: 12.5px;
    .item { border: 1px solid var(--gi-border-color, #e5e9ef); border-radius: 8px; padding: 8px 12px; background: #fcfdfe; }
    .k { font-size: 11px; color: var(--gi-text-tertiary, rgba(0, 0, 0, 0.45)); }
    .v { font-family: var(--gi-font-family-mono, monospace); font-size: 13px; margin-top: 1px; word-break: break-all; }
  }
  .lock-stats { display: flex; gap: 18px; flex-wrap: wrap; margin-bottom: 12px;
    .item { min-width: 180px; border: 1px solid var(--gi-border-color, #e5e9ef); border-radius: 8px; padding: 8px 12px; background: #fcfdfe; }
    .k { font-size: 11px; color: var(--gi-text-tertiary, rgba(0, 0, 0, 0.45)); }
    .v { font-size: 13px; }
  }
  .bar-row {
    display: grid; grid-template-columns: 180px 1fr 110px; align-items: center; gap: 10px; padding: 5px 0; font-size: 12.5px;
    .nm { font-family: var(--gi-font-family-mono, monospace); font-size: 11.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .bar { height: 10px; background: #eef1f4; border-radius: 5px; overflow: hidden; i { display: block; height: 100%; border-radius: 5px; } }
  }
  .sql-code { font-size: 11.5px; word-break: break-all; }
  .sec { font-size: 13px; font-weight: 600; margin: 14px 0 8px; }
  .foot-note { font-size: 11.5px; color: var(--gi-text-tertiary, rgba(0, 0, 0, 0.45)); margin-top: 8px; }
  .slow-count { font-size: 13px; margin-top: 6px; }
  .num { font-variant-numeric: tabular-nums; }
  .unsupported-box {
    display: flex; align-items: center; gap: 10px;
    background: #fffbe6; border: 1px solid #ffe58f; border-radius: 6px; padding: 8px 12px; font-size: 12.5px;
    .tag-n { background: #f0f2f5; border-radius: 4px; padding: 1px 8px; font-size: 11.5px; flex: none; }
    .reason { color: rgba(0, 0, 0, 0.62); }
  }
  .mt12 { margin-top: 12px; }
  .report-pending { padding: 40px 0; }
  .pending-body { display: flex; align-items: center; justify-content: center; gap: 24px;
    .t { font-size: 16px; font-weight: 600; }
    .sub { font-size: 12.5px; color: var(--gi-text-tertiary, rgba(0, 0, 0, 0.45)); margin-top: 4px; }
  }
}

@media (max-width: 768px) {
  /* 180px + 110px 两列被占死，375 屏直接溢出 */
  .bar-row {
    grid-template-columns: minmax(0, 1fr) 88px;
    grid-template-areas:
      'nm nm'
      'bar val';
    row-gap: 4px;
    column-gap: 8px;
  }

  .bar-row .nm {
    grid-area: nm;
  }

  .bar-row .bar {
    grid-area: bar;
  }

  .bar-row > :last-child {
    grid-area: val;
    text-align: right;
  }

  .lock-stats {
    gap: 10px;
  }

  .lock-stats .item {
    min-width: 0;
    flex: 1 1 140px;
  }

  .kv {
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 8px 12px;
  }

  .rp-head {
    padding: 14px 16px;
  }

  .card {
    padding: 14px;
  }
}
</style>
