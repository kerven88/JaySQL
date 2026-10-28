<template>
  <a-spin :spinning="loading">
    <div class="grade-editor-page">
      <!-- 顶部工具栏：返回 / 状态 / 模式切换 / 保存 -->
      <div class="editor-toolbar">
        <a-space :size="12">
          <a-button @click="goBack">
            <ArrowLeftOutlined />
            返回
          </a-button>
          <div class="editor-title">健康分级配置</div>
          <a-tag :color="draft.customized ? 'blue' : 'default'">
            {{ draft.customized ? '已自定义' : '内置默认' }}
          </a-tag>
          <span class="editor-meta">
            版本 v{{ draft.version }}
            <template v-if="draft.updated_by"> · {{ draft.updated_by }}</template>
            <template v-if="draft.updated_at"> · {{ draft.updated_at }}</template>
          </span>
        </a-space>
        <a-space :size="8">
          <a-segmented
            v-model:value="mode"
            :options="[
              { label: '表单模式', value: 'form' },
              { label: 'JSON 高级模式', value: 'json' },
            ]"
            @change="onModeChange"
          />
          <a-popconfirm
            title="恢复为内置默认分级策略？当前自定义内容将丢失。"
            ok-text="恢复默认"
            cancel-text="取消"
            @confirm="resetDefault"
          >
            <a-button :disabled="saving">恢复默认</a-button>
          </a-popconfirm>
          <a-button type="primary" :loading="saving" @click="save">保存并生效</a-button>
        </a-space>
      </div>

      <!-- 运行状态提示（降级 / 生效说明） -->
      <a-alert
        v-if="draft.degraded"
        class="page-alert"
        type="warning"
        show-icon
        message="当前配置存在非法项，已按内置默认降级运行"
      >
        <template #description>{{ draft.degrade_reason }}</template>
      </a-alert>
      <a-alert
        v-if="draft.apply_note"
        class="page-alert"
        type="info"
        show-icon
        :message="draft.apply_note"
      />

      <!-- 校验错误清单（保存前拦截 / 后端错误回显） -->
      <a-alert
        v-if="errors.length"
        class="page-alert"
        type="error"
        show-icon
        :message="`配置有 ${errors.length} 处问题，保存被拒绝`"
      >
        <template #description>
          <ul class="error-list">
            <li v-for="(item, index) in errors" :key="index">
              <a class="error-jump" @click="jumpToTab(item.tab)">{{ tabLabelOf(item.tab) }}</a>
              — {{ item.text }}
            </li>
          </ul>
        </template>
      </a-alert>

      <!-- 表单模式 -->
      <a-tabs v-if="mode === 'form'" v-model:activeKey="activeTab" class="editor-tabs">
        <!-- 等级定义 -->
        <a-tab-pane key="levels" tab="等级定义">
          <div class="section-hint">
            等级是「结果项 → 健康状态」的中间层：结果项只声明归属哪个等级，大屏最终显示什么由等级的健康状态决定。
            rank 决定严重度顺序（越小越严重；多个结果项同时命中时取 rank 最小者），与 P0~P3 一一对应，不可在此修改。
          </div>
          <a-table
            :columns="levelColumns"
            :data-source="draft.levels"
            row-key="code"
            :pagination="false"
            size="small"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'code'">
                <span class="level-badge" :style="{ background: record.color || '#8c8c8c' }">{{ record.code }}</span>
              </template>
              <template v-else-if="column.key === 'name'">
                <a-input v-model:value="record.name" />
              </template>
              <template v-else-if="column.key === 'health_state'">
                <a-select v-model:value="record.health_state" :options="stateOptions" />
              </template>
              <template v-else-if="column.key === 'color'">
                <div class="color-cell">
                  <span class="color-swatch" :style="{ background: record.color || '#d9d9d9' }" />
                  <a-input v-model:value="record.color" placeholder="#rrggbb" />
                </div>
              </template>
              <template v-else-if="column.key === 'response'">
                <a-input v-model:value="record.response" />
              </template>
              <template v-else-if="column.key === 'description'">
                <a-input v-model:value="record.description" />
              </template>
            </template>
          </a-table>
          <div class="fallback-row">
            <span class="fallback-label">兜底结果项（无任何规则命中时使用，决定"健康"本身）：</span>
            <a-select
              v-model:value="draft.fallback"
              :options="resultOptions"
              style="width: 320px"
              show-search
              option-filter-prop="label"
            />
          </div>
        </a-tab-pane>

        <!-- 结果项 -->
        <a-tab-pane key="results" tab="结果项">
          <div class="section-hint">
            结果项是规则命中后产出的结论单元（如"实例不可达"）。触发条件在「判定规则」页签维护，
            这里决定"这条结论算多严重"（归属等级）与是否启用。状态覆盖留空表示继承所属等级。
          </div>
          <div class="pane-toolbar">
            <a-space :size="8">
              <a-button @click="openAdd('result')">
                <PlusOutlined />
                新增结果项
              </a-button>
              <a-dropdown :disabled="!selectedResultCodes.length">
                <a-button :disabled="!selectedResultCodes.length">
                  批量设置等级
                  <DownOutlined />
                </a-button>
                <template #overlay>
                  <a-menu @click="batchSetLevel">
                    <a-menu-item v-for="lv in draft.levels" :key="lv.code">
                      设为 {{ lv.code }}（{{ lv.name }}）
                    </a-menu-item>
                  </a-menu>
                </template>
              </a-dropdown>
              <span v-if="selectedResultCodes.length" class="select-count">已选 {{ selectedResultCodes.length }} 项</span>
            </a-space>
          </div>
          <a-table
            :columns="resultColumns"
            :data-source="draft.results"
            row-key="code"
            :pagination="false"
            size="small"
            :row-selection="{ selectedRowKeys: selectedResultCodes, onChange: onResultSelectChange }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'code'">
                <span class="mono">{{ record.code }}</span>
              </template>
              <template v-else-if="column.key === 'name'">
                <a-input v-model:value="record.name" />
              </template>
              <template v-else-if="column.key === 'level'">
                <a-select v-model:value="record.level" :options="levelOptions" style="width: 100%" />
              </template>
              <template v-else-if="column.key === 'category'">
                <a-select v-model:value="record.category" :options="categoryOptions" />
              </template>
              <template v-else-if="column.key === 'health_state'">
                <a-select
                  v-model:value="record.health_state"
                  :options="stateOptions"
                  allow-clear
                  placeholder="继承等级"
                />
              </template>
              <template v-else-if="column.key === 'advice'">
                <a-input v-model:value="record.advice" />
              </template>
              <template v-else-if="column.key === 'enabled'">
                <a-switch v-model:checked="record.enabled" size="small" />
              </template>
              <template v-else-if="column.key === 'actions'">
                <a-popconfirm title="删除该结果项？引用它的规则会校验失败。" @confirm="removeResult(record)">
                  <a-button type="text" danger size="small">
                    <DeleteOutlined />
                  </a-button>
                </a-popconfirm>
              </template>
            </template>
          </a-table>
        </a-tab-pane>

        <!-- 判定规则 -->
        <a-tab-pane key="rules" tab="判定规则">
          <div class="section-hint">
            规则按优先级升序评估（越小越先）；any 内条件满足其一即可（OR），all 内必须全部满足（AND），两组之间再取 AND。
            事实键可从字典选择，动态指标事实可直接输入完整键（如 metric:perf.qps）。
          </div>
          <div class="pane-toolbar">
            <a-button @click="openAdd('rule')">
              <PlusOutlined />
              新增规则
            </a-button>
          </div>
          <div class="rule-list">
            <div v-for="rule in draft.rules" :key="rule.code" class="rule-card">
              <div class="rule-head" @click="toggleRule(rule.code)">
                <span class="rule-expand">
                  <UpOutlined v-if="expandedRules[rule.code]" />
                  <DownOutlined v-else />
                </span>
                <a-tag class="rule-priority">{{ rule.priority }}</a-tag>
                <span class="mono rule-code">{{ rule.code }}</span>
                <span class="rule-result">→ {{ resultNameOf(rule.result) }}</span>
                <span class="rule-head-spacer" />
                <span class="rule-head-actions" @click.stop>
                  <a-switch v-model:checked="rule.enabled" size="small" />
                  <a-popconfirm title="删除该规则？" @confirm="removeRule(rule)">
                    <a-button type="text" danger size="small">
                      <DeleteOutlined />
                    </a-button>
                  </a-popconfirm>
                </span>
              </div>
              <div v-if="expandedRules[rule.code]" class="rule-body">
                <div class="rule-fields">
                  <div class="field">
                    <span class="field-label">优先级</span>
                    <a-input-number v-model:value="rule.priority" :min="0" style="width: 120px" />
                  </div>
                  <div class="field">
                    <span class="field-label">产出结果项</span>
                    <a-select
                      v-model:value="rule.result"
                      :options="resultOptions"
                      style="width: 260px"
                      show-search
                      option-filter-prop="label"
                    />
                  </div>
                  <div class="field">
                    <span class="field-label">命中即停</span>
                    <a-switch v-model:checked="rule.stop_on_hit" size="small" />
                  </div>
                  <div class="field field-grow">
                    <span class="field-label">命中说明模板</span>
                    <a-input v-model:value="rule.message" placeholder="支持 {observed} {threshold} {label} {fact}" />
                  </div>
                </div>
                <div class="cond-group">
                  <div class="cond-group-title">任一条件满足即可（OR）</div>
                  <grade-condition-list
                    :conditions="rule.any"
                    :facts="facts"
                    @remove="(index) => rule.any.splice(index, 1)"
                  />
                  <a-button type="dashed" size="small" class="cond-add" @click="addCondition(rule.any)">
                    <PlusOutlined />
                    添加条件
                  </a-button>
                </div>
                <div class="cond-group">
                  <div class="cond-group-title">全部条件满足（AND）</div>
                  <grade-condition-list
                    :conditions="rule.all"
                    :facts="facts"
                    @remove="(index) => rule.all.splice(index, 1)"
                  />
                  <a-button type="dashed" size="small" class="cond-add" @click="addCondition(rule.all)">
                    <PlusOutlined />
                    添加条件
                  </a-button>
                </div>
              </div>
            </div>
          </div>
        </a-tab-pane>

        <!-- 事实字典（只读） -->
        <a-tab-pane key="facts" tab="事实字典（只读）">
          <div class="section-hint">
            事实是系统能提供的观测能力，由平台枚举维护，不接受配置覆盖 —— 这里是"规则里能引用什么"的唯一依据。
            标记为动态的事实按前缀引用具体指标键（如 metric:perf.qps）。
          </div>
          <a-table
            :columns="factColumns"
            :data-source="facts"
            row-key="key"
            :pagination="{ pageSize: 12 }"
            size="small"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'key'">
                <span class="mono">{{ record.key }}</span>
                <a-tag v-if="record.dynamic" class="dyn-tag" color="purple">动态</a-tag>
              </template>
              <template v-else-if="column.key === 'ops'">
                <a-space wrap :size="4">
                  <a-tag v-for="op in record.ops" :key="op" class="mono op-tag">{{ op }}</a-tag>
                </a-space>
              </template>
              <template v-else-if="column.key === 'values'">
                <a-space wrap :size="4">
                  <a-tag v-for="item in record.values || []" :key="item">{{ item }}</a-tag>
                  <span v-if="!(record.values || []).length">—</span>
                </a-space>
              </template>
            </template>
          </a-table>
        </a-tab-pane>
      </a-tabs>

      <!-- JSON 高级模式 -->
      <template v-else>
        <a-alert
          class="page-alert"
          type="info"
          show-icon
          message="JSON 高级模式"
          description="直接编辑完整配置。切换回表单模式或保存时会先解析并校验；facts 为只读字典，改动会被忽略。"
        />
        <a-textarea v-model:value="jsonText" class="grade-json" :rows="30" spellcheck="false" />
        <div v-if="jsonError" class="json-error">{{ jsonError }}</div>
      </template>

      <!-- 新增 结果项 / 规则 -->
      <a-modal
        v-model:open="addModal.open"
        :title="addModal.kind === 'result' ? '新增结果项' : '新增判定规则'"
        ok-text="添加"
        cancel-text="取消"
        @ok="confirmAdd"
      >
        <a-form layout="vertical">
          <a-form-item label="code（唯一标识，建议英文与下划线）" required>
            <a-input v-model:value="addModal.code" placeholder="如 slow_query_p95" />
          </a-form-item>
          <a-form-item v-if="addModal.kind === 'result'" label="名称（界面展示）" required>
            <a-input v-model:value="addModal.name" placeholder="如 P95 慢查询耗时偏高" />
          </a-form-item>
          <template v-if="addModal.kind === 'rule'">
            <a-form-item label="产出结果项" required>
              <a-select v-model:value="addModal.result" :options="resultOptions" />
            </a-form-item>
            <a-form-item label="优先级">
              <a-input-number v-model:value="addModal.priority" :min="0" style="width: 160px" />
            </a-form-item>
          </template>
        </a-form>
      </a-modal>
    </div>
  </a-spin>
</template>

<script setup>
// 健康分级结构化编辑页：取代原「整段 JSON 弹窗」。
// 数据契约：GET/PUT /monitor/config/health-grade，PUT body = { config }；
// facts 由服务端回填（只读），保存后 customized=true，不再跟随内置默认升级。
// 前端校验与后端 ValidateHealthGradeConfig 对齐：等级/结果项/规则引用完整性 +
// 事实存在性（动态事实按前缀 metric: / metric_quality: 判定）+ 条件值类型。
import { message, Modal } from 'ant-design-vue'
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave, useRouter } from 'vue-router'
import {
  ArrowLeftOutlined,
  DeleteOutlined,
  DownOutlined,
  PlusOutlined,
  UpOutlined,
} from '@ant-design/icons-vue'
import {
  getMonitorHealthGradeApi,
  resetMonitorHealthGradeApi,
  updateMonitorHealthGradeApi,
} from '@/api/monitor'
import { screenClassText } from '../utils'
import GradeConditionList from './GradeConditionList.vue'

defineOptions({ name: 'MonitorHealthGradeEditorView' })

const router = useRouter()

const SCREEN_STATES = ['healthy', 'risk', 'fault', 'uncollected']

const CATEGORY_TEXT = {
  availability: '可用性',
  capacity: '容量',
  performance: '性能',
  collect: '采集',
}

// ---------------------------------------------------------------- 状态

const loading = ref(false)
const saving = ref(false)
const mode = ref('form')
const activeTab = ref('levels')
const errors = ref([])
const jsonText = ref('')
const jsonError = ref('')
const facts = ref([])
const selectedResultCodes = ref([])
const expandedRules = reactive({})
const addModal = reactive({ open: false, kind: 'result', code: '', name: '', result: '', priority: 100 })

const draft = reactive({
  version: 1,
  name: '',
  levels: [],
  results: [],
  rules: [],
  fallback: 'healthy',
  customized: false,
  source: '',
  degraded: false,
  degrade_reason: '',
  updated_at: '',
  updated_by: '',
  apply_note: '',
})

// 脏标记：离开页面前提示未保存。applyConfig 回填时经 nextTick 后再恢复采集，
// 避免 Vue 异步 watcher 在回填完成后才触发、把加载误判为用户编辑。
const dirty = ref(false)
let suppressDirty = false
watch(draft, () => {
  if (!suppressDirty) dirty.value = true
}, { deep: true })

// ---------------------------------------------------------------- 下拉选项与表格列

const stateOptions = SCREEN_STATES.map((value) => ({ value, label: screenClassText(value) }))
const categoryOptions = Object.entries(CATEGORY_TEXT).map(([value, label]) => ({ value, label }))
const levelOptions = computed(() => (draft.levels || []).map((item) => ({ value: item.code, label: `${item.code} ${item.name}` })))
const resultOptions = computed(() => (draft.results || []).map((item) => ({ value: item.code, label: `${item.code} ${item.name}` })))

const levelColumns = [
  { title: '等级', key: 'code', width: 80 },
  { title: '名称', key: 'name', width: 120 },
  { title: '健康状态（大屏显示）', key: 'health_state', width: 170 },
  { title: '配色', key: 'color', width: 160 },
  { title: 'rank', dataIndex: 'rank', key: 'rank', width: 70 },
  { title: '响应要求', key: 'response', width: 150 },
  { title: '说明', key: 'description' },
]
const resultColumns = [
  { title: 'code', key: 'code', width: 190 },
  { title: '名称', key: 'name', width: 170 },
  { title: '归属等级', key: 'level', width: 120 },
  { title: '分类', key: 'category', width: 110 },
  { title: '状态覆盖', key: 'health_state', width: 140 },
  { title: '处置建议', key: 'advice' },
  { title: '启用', key: 'enabled', width: 70 },
  { title: '', key: 'actions', width: 50 },
]
const factColumns = [
  { title: '事实键', key: 'key', width: 220 },
  { title: '名称', dataIndex: 'name', width: 170 },
  { title: '类型', dataIndex: 'type', width: 90 },
  { title: '支持操作符', key: 'ops' },
  { title: '候选值', key: 'values', width: 220 },
]

// ---------------------------------------------------------------- 加载与回填

const cloneWithoutFacts = (cfg) => {
  const copy = JSON.parse(JSON.stringify(cfg || {}))
  delete copy.facts
  return copy
}

const applyConfig = (cfg) => {
  suppressDirty = true
  facts.value = cfg?.facts || []
  // 先重置为空骨架再合并：服务端对空值字段 omitempty 不下发，
  // 直接 assign 会让上一份配置的旧值（如 degrade_reason）残留
  Object.assign(draft, {
    version: 1,
    name: '',
    levels: [],
    results: [],
    rules: [],
    fallback: 'healthy',
    customized: false,
    source: '',
    degraded: false,
    degrade_reason: '',
    updated_at: '',
    updated_by: '',
    apply_note: '',
  }, cloneWithoutFacts(cfg))
  Object.keys(expandedRules).forEach((key) => delete expandedRules[key])
  if (draft.rules?.length) expandedRules[draft.rules[0].code] = true
  nextTick(() => {
    suppressDirty = false
    dirty.value = false
  })
  errors.value = []
  jsonError.value = ''
}

const load = async () => {
  loading.value = true
  try {
    const res = await getMonitorHealthGradeApi()
    applyConfig(res?.data || {})
  } catch (error) {
    message.error(error?.message || '健康分级配置加载失败')
  } finally {
    loading.value = false
  }
}

onMounted(load)

// ---------------------------------------------------------------- 表单模式辅助

const toggleRule = (code) => {
  expandedRules[code] = !expandedRules[code]
}

const resultNameOf = (code) => {
  const found = (draft.results || []).find((item) => item.code === code)
  return found ? `${found.code}（${found.name}）` : code
}

const tabLabelOf = (tab) => ({
  levels: '等级定义',
  results: '结果项',
  rules: '判定规则',
}[tab] || tab)

const jumpToTab = (tab) => {
  activeTab.value = tab
}

const onResultSelectChange = (keys) => {
  selectedResultCodes.value = keys
}

const batchSetLevel = ({ key }) => {
  const codes = new Set(selectedResultCodes.value)
  let changed = 0
  draft.results.forEach((item) => {
    if (codes.has(item.code)) {
      item.level = key
      changed += 1
    }
  })
  message.success(`已将 ${changed} 个结果项的归属等级设为 ${key}（尚未保存）`)
}

const removeResult = (record) => {
  const index = draft.results.findIndex((item) => item.code === record.code)
  if (index !== -1) draft.results.splice(index, 1)
  selectedResultCodes.value = selectedResultCodes.value.filter((code) => code !== record.code)
}

const removeRule = (record) => {
  const index = draft.rules.findIndex((item) => item.code === record.code)
  if (index !== -1) draft.rules.splice(index, 1)
  delete expandedRules[record.code]
}

const addCondition = (list) => {
  list.push({ fact: '', op: 'eq', value: '', label: '' })
}

// 新增 结果项 / 规则
const openAdd = (kind) => {
  addModal.kind = kind
  addModal.code = ''
  addModal.name = ''
  addModal.result = draft.results?.[0]?.code || ''
  const maxPriority = Math.max(0, ...(draft.rules || []).map((item) => item.priority || 0))
  addModal.priority = maxPriority + 10
  addModal.open = true
}

const confirmAdd = () => {
  const code = addModal.code.trim()
  if (!code) {
    message.warning('code 不能为空')
    return
  }
  if (addModal.kind === 'result') {
    if (draft.results.some((item) => item.code === code)) {
      message.warning(`结果项 code 已存在：${code}`)
      return
    }
    draft.results.push({
      code,
      name: addModal.name.trim() || code,
      level: draft.levels?.[0]?.code || 'P2',
      category: 'availability',
      advice: '',
      health_state: '',
      enabled: true,
    })
    activeTab.value = 'results'
  } else {
    if (draft.rules.some((item) => item.code === code)) {
      message.warning(`规则 code 已存在：${code}`)
      return
    }
    if (!addModal.result) {
      message.warning('请选择产出结果项')
      return
    }
    draft.rules.push({
      code,
      result: addModal.result,
      enabled: true,
      priority: addModal.priority,
      any: [],
      all: [],
      message: '',
    })
    expandedRules[code] = true
    activeTab.value = 'rules'
  }
  addModal.open = false
}

// ---------------------------------------------------------------- 校验（与后端 ValidateHealthGradeConfig 对齐）

const validateDraft = () => {
  const list = []
  const levels = draft.levels || []
  if (!levels.length) list.push({ tab: 'levels', text: '至少需要定义一个等级' })
  const levelCodes = new Set()
  const rankOwner = new Map()
  levels.forEach((level) => {
    if (!level.code?.trim()) list.push({ tab: 'levels', text: '存在 code 为空的等级' })
    else if (levelCodes.has(level.code)) list.push({ tab: 'levels', text: `等级 code 重复：${level.code}` })
    else levelCodes.add(level.code)
    if (!SCREEN_STATES.includes(level.health_state)) {
      list.push({ tab: 'levels', text: `等级 ${level.code || '?'} 的健康状态非法：${level.health_state}（只允许 healthy/risk/fault/uncollected）` })
    }
    if (rankOwner.has(level.rank)) {
      list.push({ tab: 'levels', text: `等级 ${rankOwner.get(level.rank)} 与 ${level.code} 的 rank 重复（必须唯一）` })
    } else rankOwner.set(level.rank, level.code)
  })

  const resultCodes = new Set()
  ;(draft.results || []).forEach((result) => {
    if (!result.code?.trim()) list.push({ tab: 'results', text: '存在 code 为空的结果项' })
    else if (resultCodes.has(result.code)) list.push({ tab: 'results', text: `结果项 code 重复：${result.code}` })
    else resultCodes.add(result.code)
    if (!levelCodes.has(result.level)) {
      list.push({ tab: 'results', text: `结果项 ${result.code || '?'} 引用了不存在的等级：${result.level}` })
    }
    if (result.health_state && !SCREEN_STATES.includes(result.health_state)) {
      list.push({ tab: 'results', text: `结果项 ${result.code} 的状态覆盖非法：${result.health_state}` })
    }
  })

  if (!draft.fallback || !resultCodes.has(draft.fallback)) {
    list.push({ tab: 'results', text: `兜底结果项无效：${draft.fallback || '（空）'}` })
  }

  ;(draft.rules || []).forEach((rule) => {
    if (!rule.code?.trim()) list.push({ tab: 'rules', text: '存在 code 为空的规则' })
    if (!resultCodes.has(rule.result)) {
      list.push({ tab: 'rules', text: `规则 ${rule.code || '?'} 引用了不存在的结果项：${rule.result}` })
    }
    if (!(rule.any?.length || rule.all?.length)) {
      list.push({ tab: 'rules', text: `规则 ${rule.code || '?'} 至少要有一个条件（any 或 all）` })
    }
    ;[...(rule.any || []), ...(rule.all || [])].forEach((cond) => {
      if (!cond.fact?.trim()) {
        list.push({ tab: 'rules', text: `规则 ${rule.code || '?'} 有条件未选择事实键` })
        return
      }
      const def = (facts.value || []).find((item) => {
        if (item.dynamic) {
          const prefix = `${item.key}:`
          return cond.fact.startsWith(prefix) && cond.fact.length > prefix.length
        }
        return item.key === cond.fact
      })
      if (!def) {
        list.push({ tab: 'rules', text: `规则 ${rule.code || '?'} 引用了未知事实：${cond.fact}` })
        return
      }
      if (!def.ops.includes(cond.op)) {
        list.push({ tab: 'rules', text: `规则 ${rule.code || '?'} 的 ${cond.fact} 不支持操作符 ${cond.op}` })
      }
      const kind = valueKindOf(cond.op, def)
      if (kind === 'none') return
      if (kind === 'list' && !(Array.isArray(cond.value) && cond.value.length)) {
        list.push({ tab: 'rules', text: `规则 ${rule.code || '?'} 的条件 ${cond.fact}（${cond.op}）未选择任何值` })
      } else if (kind === 'range' && !(Array.isArray(cond.value) && cond.value.length === 2 && cond.value.every((item) => item != null))) {
        list.push({ tab: 'rules', text: `规则 ${rule.code || '?'} 的条件 ${cond.fact}（between）需要上下限` })
      } else if ((kind === 'number' || kind === 'text') && (cond.value == null || cond.value === '')) {
        list.push({ tab: 'rules', text: `规则 ${rule.code || '?'} 的条件 ${cond.fact} 缺少比较值` })
      }
    })
  })
  return list
}

const valueKindOf = (op, def) => {
  if (op === 'exists' || op === 'absent') return 'none'
  if (op === 'in' || op === 'not_in') return 'list'
  if (op === 'between') return 'range'
  return def?.type === 'number' ? 'number' : 'text'
}

// ---------------------------------------------------------------- JSON 模式

const serializeDraft = () => JSON.stringify(cloneWithoutFacts(draft), null, 2)

const applyJsonText = () => {
  let parsed
  try {
    parsed = JSON.parse(jsonText.value)
  } catch (error) {
    jsonError.value = `JSON 解析失败：${error?.message || '格式不合法'}`
    return false
  }
  if (!Array.isArray(parsed.levels) || !Array.isArray(parsed.results) || !Array.isArray(parsed.rules)) {
    jsonError.value = '配置结构不完整：levels / results / rules 必须是数组'
    return false
  }
  jsonError.value = ''
  Object.assign(draft, cloneWithoutFacts(parsed))
  Object.keys(expandedRules).forEach((key) => delete expandedRules[key])
  if (draft.rules?.length) expandedRules[draft.rules[0].code] = true
  return true
}

const onModeChange = (value) => {
  if (value === 'json') {
    jsonText.value = serializeDraft()
    jsonError.value = ''
    return
  }
  // JSON → 表单：解析成功才允许切换，否则留在 JSON 模式
  if (!applyJsonText()) {
    mode.value = 'json'
    message.warning('JSON 尚未通过解析，已停留在高级模式')
  }
}

// ---------------------------------------------------------------- 保存 / 恢复 / 返回

const save = async () => {
  if (mode.value === 'json' && !applyJsonText()) {
    message.error('JSON 解析失败，请先修正')
    return
  }
  const problems = validateDraft()
  errors.value = problems
  if (problems.length) {
    activeTab.value = problems[0].tab
    message.error(`配置有 ${problems.length} 处问题，已定位到「${tabLabelOf(problems[0].tab)}」`)
    return
  }
  saving.value = true
  try {
    const res = await updateMonitorHealthGradeApi({ config: cloneWithoutFacts(draft) })
    applyConfig(res?.data || {})
    mode.value = 'form'
    message.success('健康分级已保存，10 秒内生效')
  } catch (error) {
    // 后端结构校验的失败原因直接透出，不吞掉
    errors.value = [{ tab: activeTab.value, text: error?.message || '保存失败' }]
    message.error(error?.message || '健康分级保存失败')
  } finally {
    saving.value = false
  }
}

const resetDefault = async () => {
  saving.value = true
  try {
    const res = await resetMonitorHealthGradeApi()
    applyConfig(res?.data || {})
    mode.value = 'form'
    message.success('已恢复为内置默认分级策略')
  } catch (error) {
    message.error(error?.message || '恢复默认失败')
  } finally {
    saving.value = false
  }
}

const goBack = () => {
  router.push({ name: 'monitor.alert-config' })
}

onBeforeRouteLeave(() => {
  if (!dirty.value) return true
  return new Promise((resolve) => {
    Modal.confirm({
      title: '有未保存的修改，确定离开？',
      okText: '放弃修改并离开',
      cancelText: '留在本页',
      onOk: () => resolve(true),
      onCancel: () => resolve(false),
    })
  })
})
</script>

<style scoped>
.grade-editor-page {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.editor-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.editor-title {
  font-size: 16px;
  font-weight: 600;
  color: #1f2d38;
}

.editor-meta {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.error-list {
  margin: 0;
  padding-left: 18px;
}

.error-jump {
  font-weight: 600;
}

.section-hint {
  margin-bottom: 10px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.pane-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}

.select-count {
  font-size: 12px;
  color: var(--gi-color-primary, #2457f5);
}

.level-badge {
  display: inline-block;
  min-width: 28px;
  padding: 1px 6px;
  border-radius: 4px;
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  text-align: center;
}

.color-cell {
  display: flex;
  align-items: center;
  gap: 6px;
}

.color-swatch {
  width: 14px;
  height: 14px;
  border-radius: 3px;
  border: 1px solid var(--gi-border-color, #e5e8eb);
  flex-shrink: 0;
}

.fallback-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
}

.fallback-label {
  font-size: 13px;
  color: var(--gi-color-text-secondary, #5a6a7a);
}

.mono {
  font-family: 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px;
}

.rule-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.rule-card {
  border: 1px solid var(--gi-border-color, #e5e8eb);
  border-radius: 6px;
  background: var(--gi-color-bg-container, #fff);
}

.rule-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  cursor: pointer;
  user-select: none;
}

.rule-expand {
  color: var(--gi-color-text-tertiary, #8a94a6);
  font-size: 12px;
  width: 14px;
}

.rule-priority {
  min-width: 40px;
  text-align: center;
}

.rule-code {
  font-weight: 600;
  color: var(--gi-color-text-primary, #1f2d38);
}

.rule-result {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.rule-head-spacer {
  flex: 1;
}

.rule-head-actions {
  display: flex;
  align-items: center;
  gap: 6px;
}

.rule-body {
  padding: 10px 12px 12px 36px;
  border-top: 1px solid var(--gi-border-color, #e5e8eb);
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.rule-fields {
  display: flex;
  align-items: flex-end;
  gap: 16px;
  flex-wrap: wrap;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.field-grow {
  flex: 1;
  min-width: 240px;
}

.field-label {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.cond-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.cond-group-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--gi-color-text-secondary, #5a6a7a);
}

.cond-add {
  align-self: flex-start;
}

.dyn-tag {
  margin-left: 6px;
}

.op-tag {
  font-size: 11px;
}

.grade-json {
  font-family: 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px;
}

.json-error {
  color: #cf1322;
  font-size: 12px;
}
</style>
