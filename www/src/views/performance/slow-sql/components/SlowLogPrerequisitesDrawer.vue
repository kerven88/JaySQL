<template>
  <a-drawer
    v-model:open="visible"
    :width="drawerWidth"
    placement="right"
    title="慢 SQL 采集前提条件"
    :body-style="{ padding: '16px 20px 32px' }"
    @close="handleClose"
  >
    <template #extra>
      <a-button size="small" @click="loadPrerequisites">
        <SyncOutlined />
        重新加载
      </a-button>
    </template>

    <a-alert
      class="drawer-alert"
      type="info"
      show-icon
      message="按数据库类型对照下表逐项确认。只要有一项不满足，该实例的慢 SQL 就不会出现在本页面；状态列会直接标出卡在哪一项。"
    />

    <a-tabs v-model:activeKey="activeEngine" class="engine-tabs" type="card">
      <a-tab-pane v-for="engine in engines" :key="engine.family" :tab="engine.label">
        <template v-if="!engine.items.length">
          <a-empty description="该引擎暂无前提条件说明" />
        </template>

        <template v-else>
          <div class="engine-meta">
            <span>
              数据源：
              <b>{{ engine.sourceText }}</b>
            </span>
            <span>共 {{ engine.items.length }} 项</span>
          </div>

          <div v-for="group in engine.groups" :key="group.kind" class="prereq-group">
            <div class="prereq-group__header">
              <span class="prereq-group__dot" :style="{ background: group.color }"></span>
              <span class="prereq-group__title">{{ group.title }}</span>
              <span class="prereq-group__count">{{ group.items.length }}</span>
            </div>

            <div
              v-for="item in group.items"
              :key="item.key"
              class="prereq-item"
              :class="{ 'prereq-item--hit': isHit(item.key) }"
            >
              <div class="prereq-item__title">
                <span>{{ item.title }}</span>
                <a-tag v-if="isHit(item.key)" color="error" class="prereq-item__badge">疑似未满足</a-tag>
              </div>

              <div class="prereq-item__row">
                <span class="prereq-item__label">要求</span>
                <span class="prereq-item__value">{{ item.requirement }}</span>
              </div>

              <div v-if="item.how_to" class="prereq-item__row">
                <span class="prereq-item__label">开启方式</span>
                <span class="prereq-item__value">{{ item.how_to }}</span>
              </div>

              <div v-if="item.consequence" class="prereq-item__row">
                <span class="prereq-item__label">不满足后果</span>
                <span class="prereq-item__value prereq-item__value--warn">{{ item.consequence }}</span>
              </div>

              <div v-if="item.verify" class="prereq-item__verify">
                <div class="prereq-item__verify-head">
                  <span>自检语句</span>
                  <a-button type="link" size="small" @click="copyVerify(item.verify)">复制</a-button>
                </div>
                <pre class="prereq-item__code">{{ item.verify }}</pre>
              </div>
            </div>
          </div>
        </template>
      </a-tab-pane>
    </a-tabs>
  </a-drawer>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { message } from 'ant-design-vue'
import { SyncOutlined } from '@ant-design/icons-vue'
import { getSlowLogPrerequisitesApi } from '@/api/performance'

const props = defineProps({
  open: { type: Boolean, default: false },
  /** 需要高亮的「前提项 Key」列表，来自 /slowlog-diagnosis 的 prerequisite_keys */
  highlightKeys: { type: Array, default: () => [] },
  /** 预选引擎家族：mysql / postgresql / oracle */
  defaultFamily: { type: String, default: '' },
})

const emit = defineEmits(['update:open', 'close'])

const visible = computed({
  get: () => props.open,
  set: (value) => emit('update:open', value),
})

const loading = ref(false)
const engines = ref([])
const activeEngine = ref('mysql')

const FAMILY_LABELS = {
  mysql: 'MySQL / 兼容引擎',
  postgresql: 'PostgreSQL',
  oracle: 'Oracle',
}

const KIND_META = [
  { kind: 'version', title: '版本要求', color: '#1677ff' },
  { kind: 'parameter', title: '服务端参数', color: '#0f766e' },
  { kind: 'permission', title: '账号权限', color: '#722ed1' },
  { kind: 'feature', title: '可选增强', color: '#13c2c2' },
  { kind: 'channel', title: '平台通路', color: '#faad14' },
  { kind: 'file', title: '日志文件', color: '#d4380d' },
  { kind: 'caliber', title: '统计口径', color: '#64748b' },
]

const decorate = (engine = {}) => {
  const items = Array.isArray(engine.items) ? engine.items : []
  const groups = KIND_META.map((meta) => ({
    ...meta,
    items: items.filter((item) => item.kind === meta.kind),
  })).filter((group) => group.items.length)
  return {
    ...engine,
    label: FAMILY_LABELS[engine.family] || engine.db_type || engine.family || '未知',
    sourceText: engine.family === 'oracle' ? 'V$SQL 定时采样（无日志文件）' : '服务端慢日志文件（增量读取）',
    items,
    groups,
  }
}

const loadPrerequisites = async () => {
  loading.value = true
  try {
    const res = await getSlowLogPrerequisitesApi({})
    const raw = Array.isArray(res?.data?.engines) ? res.data.engines : []
    engines.value = raw.map(decorate)
    const preferred = props.defaultFamily || activeEngine.value
    if (engines.value.some((engine) => engine.family === preferred)) {
      activeEngine.value = preferred
    } else if (engines.value.length) {
      activeEngine.value = engines.value[0].family
    }
  } catch (error) {
    message.error(error?.message || '加载采集前提条件失败')
  } finally {
    loading.value = false
  }
}

const isHit = (key) => props.highlightKeys.includes(key)

const copyVerify = async (text) => {
  try {
    await navigator.clipboard.writeText(text)
    message.success('已复制自检语句')
  } catch {
    message.warning('复制失败，请手动选中复制')
  }
}

const handleClose = () => {
  emit('close')
}

// 每次打开都重新对齐「选中引擎」与「高亮项」：
// 抽屉是复用的，若不在打开时重置，从 A 实例切到 B 实例会残留上一次的高亮。
watch(
  () => props.open,
  (opened) => {
    if (!opened) return
    if (!engines.value.length) {
      loadPrerequisites()
      return
    }
    const preferred = props.defaultFamily || activeEngine.value
    if (engines.value.some((engine) => engine.family === preferred)) {
      activeEngine.value = preferred
    }
  }
)

const drawerWidth = computed(() => (window.innerWidth < 768 ? '100%' : 720))
</script>

<style scoped>
.drawer-alert {
  margin-bottom: 16px;
}

.engine-tabs :deep(.ant-tabs-nav) {
  margin-bottom: 12px;
}

.engine-meta {
  display: flex;
  justify-content: space-between;
  margin-bottom: 12px;
  font-size: 13px;
  color: var(--gi-text-secondary, #4a5c68);
}

.prereq-group {
  margin-bottom: 18px;
}

.prereq-group__header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.prereq-group__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
}

.prereq-group__title {
  font-size: 13px;
  font-weight: 600;
  color: var(--gi-text-primary, #0f1720);
}

.prereq-group__count {
  font-size: 12px;
  color: var(--gi-text-secondary, #4a5c68);
}

.prereq-item {
  padding: 10px 12px;
  margin-bottom: 8px;
  background: #fbfdfd;
  border: 1px solid var(--gi-border, #d9e1e6);
  border-radius: 8px;
}

.prereq-item--hit {
  background: #fff7f7;
  border-color: #ffccc7;
}

.prereq-item__title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
  font-size: 13px;
  font-weight: 600;
  color: var(--gi-text-primary, #0f1720);
}

.prereq-item__badge {
  margin-inline-end: 0;
  font-size: 11px;
}

.prereq-item__row {
  display: flex;
  gap: 8px;
  margin-bottom: 4px;
  font-size: 12px;
  line-height: 1.6;
}

.prereq-item__label {
  flex: 0 0 68px;
  color: var(--gi-text-secondary, #4a5c68);
}

.prereq-item__value {
  flex: 1;
  color: var(--gi-text-primary, #0f1720);
  word-break: break-word;
  white-space: pre-wrap;
}

.prereq-item__value--warn {
  color: #d4380d;
}

.prereq-item__verify {
  margin-top: 8px;
}

.prereq-item__verify-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12px;
  color: var(--gi-text-secondary, #4a5c68);
}

.prereq-item__code {
  max-height: 160px;
  padding: 8px 10px;
  margin: 4px 0 0;
  overflow: auto;
  font-size: 12px;
  line-height: 1.6;
  color: #0f1720;
  white-space: pre-wrap;
  background: #f4f7f8;
  border-radius: 6px;
  word-break: break-all;
}
</style>
