<template>
  <!-- 「实例列表」下钻弹窗：告警中心「告警实例总数」与监控大屏「健康/风险/故障实例」共用。
       scope 决定实例集合口径（后端 /monitor/alerts/instances 的 scope 参数）：
         alert   有未恢复告警的实例（与告警中心计数卡同口径）
         all     全部纳管实例
         healthy / risk / fault / uncollected  健康分级引擎四分类对应集合
                             （与大屏卡片数字、环形图图例同一判定）
       行内"查看该实例告警明细"通过 view-detail 事件交给父页面决定跳转方式。 -->
  <a-modal
    :open="open"
    wrap-class-name="alert-inst-modal"
    centered
    :title="modalTitle"
    width="880px"
    :mask-closable="false"
    :footer="null"
    @cancel="emit('update:open', false)"
  >
    <a-table
      :columns="columns"
      :data-source="state.items"
      row-key="instance_id"
      :loading="state.loading"
      :pagination="pagination"
      size="middle"
      @change="onTableChange"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'instance'">
          <!-- 实例名称：展示名（remark 优先），两行式名称 + ip:port；
               名称 15 字符 / ip:port 20 字符截断（产品统一口径），完整值见 title 悬浮提示 -->
          <span class="inst-cell">
            <span class="inst-name" :title="instanceNameOf(record)">{{ clipText(instanceNameOf(record), 15) }}</span>
            <span class="inst-ip" :title="instanceAddrOf(record)">{{ clipText(instanceAddrOf(record), 20) }}</span>
          </span>
        </template>
        <template v-else-if="column.key === 'db_type'">
          <span>{{ dbTypeTextOf(record) }}</span>
        </template>
        <template v-else-if="column.key === 'unresolved'">
          <span class="inst-unresolved">{{ record.unresolved }}</span>
        </template>
        <template v-else-if="column.key === 'latest_at'">
          <span class="inst-time">{{ record.latest_at || '—' }}</span>
        </template>
        <template v-else-if="column.key === 'actions'">
          <a-button class="drill-detail-btn" type="link" size="small" @click="emit('view-detail', record)">
            查看该实例告警明细
          </a-button>
        </template>
      </template>
    </a-table>
  </a-modal>
</template>

<script setup>
import { computed, reactive, watch } from 'vue'
import { getMonitorAlertInstancesApi } from '@/api/monitor'
import { message } from 'ant-design-vue'
import { clipText } from '../utils'

const props = defineProps({
  open: { type: Boolean, default: false },
  // alert（默认）/ all / healthy / risk / fault，与后端 scope 参数一致
  scope: { type: String, default: 'alert' },
  // 标题前缀覆盖：不传则按 scope 生成默认文案
  titlePrefix: { type: String, default: '' },
})

const emit = defineEmits(['update:open', 'view-detail'])

const state = reactive({
  loading: false,
  items: [],
  total: 0,
  page: 1,
  pageSize: 10,
})

const columns = [
  { title: '实例名称', key: 'instance', width: 220 },
  { title: '实例类型', key: 'db_type', width: 150 },
  { title: '未恢复告警总数', key: 'unresolved', width: 130 },
  { title: '最新告警触发时间', key: 'latest_at', width: 180 },
  { title: '操作', key: 'actions' },
]

const SCOPE_TITLES = {
  alert: { label: '告警实例列表', unit: '个触发告警的DB实例' },
  all: { label: '监控实例列表', unit: '个纳管实例' },
  healthy: { label: '健康实例列表', unit: '个健康实例' },
  risk: { label: '风险实例列表', unit: '个风险实例' },
  fault: { label: '故障实例列表', unit: '个故障实例' },
  uncollected: { label: '未采集实例列表', unit: '个未采集实例' },
}

const modalTitle = computed(() => {
  const meta = SCOPE_TITLES[props.scope] || SCOPE_TITLES.alert
  const label = props.titlePrefix || meta.label
  return `${label}（共 ${Number(state.total).toLocaleString()} ${meta.unit}）`
})

// 实例名称两行展示取值（与告警下钻弹窗 AlertDrillModal 同口径）：
//   名称 —— instance 缺失兜底 —；ip:port —— hostname 缺失兜底 —
const instanceNameOf = (record) => record?.instance || '—'
const instanceAddrOf = (record) => (record?.hostname ? `${record.hostname}:${record.port}` : '—')

// 实例类型文案：按数据库惯例取版本位——MySQL 系显示 major.minor（8.0.43 → MySQL 8.0），
// PostgreSQL 显示 major（16.15 → PostgreSQL 16）；版本未采集到时只显示 db_type。
const dbTypeTextOf = (record) => {
  const dbType = record.db_type || '—'
  const match = String(record.db_version || '').match(/^(\d+)(?:\.(\d+))?/)
  if (!match) return dbType
  if (/postgres/i.test(dbType)) return `${dbType} ${match[1]}`
  return match[2] ? `${dbType} ${match[1]}.${match[2]}` : `${dbType} ${match[1]}`
}

const pagination = computed(() => ({
  current: state.page,
  pageSize: state.pageSize,
  total: state.total,
  showSizeChanger: false,
  hideOnSinglePage: true,
  showTotal: () => `共 ${Number(state.total).toLocaleString()} 个实例`,
}))

const load = async () => {
  state.loading = true
  try {
    const params = props.scope === 'alert' ? { unresolved: true } : { scope: props.scope }
    const res = await getMonitorAlertInstancesApi(params)
    state.items = res?.data?.items || []
    state.total = Number(res?.data?.total || 0)
  } catch (error) {
    if (!isCanceled(error)) message.error(error?.message || '实例列表加载失败')
  } finally {
    state.loading = false
  }
}

// 请求层在路由切换时会 abort 在途请求并抛 { canceled: true }，不能当失败弹错误
const isCanceled = (error) => Boolean(error && (error.canceled || error.code === 'ERR_CANCELED'))

const onTableChange = (pag) => {
  state.page = pag.current || 1
}

// 打开时拉数据；scope 变化后再次打开也要重新加载
watch(
  () => props.open,
  (open) => {
    if (open) {
      state.page = 1
      load()
    }
  }
)
</script>

<style>
/* a-modal teleport 到 body，scoped 样式不生效：用 wrap class 前缀 .alert-inst-modal 限定。
   实例名称两行（名称 + ip:port 淡灰小一号）；未恢复告警总数红色强调。 */
.alert-inst-modal .inst-cell {
  display: block;
}
.alert-inst-modal .inst-name {
  display: block;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  color: var(--gi-color-text-primary, rgba(15, 23, 32, 0.9));
}
.alert-inst-modal .inst-ip {
  display: block;
  margin-top: 2px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--gi-color-text-tertiary, #8a94a6);
}
.alert-inst-modal .inst-unresolved {
  color: var(--gi-color-danger, #ef4444);
  font-weight: 600;
}
.alert-inst-modal .inst-time {
  white-space: nowrap;
  word-break: keep-all;
}
.alert-inst-modal .drill-detail-btn {
  padding: 0;
  /* 操作列链接统一用系统主题色：引用 :root 全局 token（teleport 到 body 也能取到），
     不硬编码色值；hover/active 用主题色深档保持交互反馈。 */
  color: var(--gi-color-primary, #0f766e);
}
.alert-inst-modal .drill-detail-btn:hover {
  color: var(--gi-color-primary-hover, #0d6b63);
}
.alert-inst-modal .drill-detail-btn:active {
  color: var(--gi-color-primary-active, #0b5f58);
}
</style>
