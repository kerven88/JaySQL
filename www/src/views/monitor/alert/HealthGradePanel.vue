<template>
  <a-spin :spinning="loading">
    <div class="hg-panel">
      <!-- 头部：状态 + 版本 + 保存 -->
      <div class="hg-head">
        <div class="hg-head-left">
          <a-tag :color="draft.customized ? 'blue' : 'default'">
            {{ draft.customized ? '已自定义' : '内置默认' }}
          </a-tag>
          <span class="hg-meta">
            版本 v{{ draft.version }}
            <template v-if="draft.updated_by"> · 最近修改人 {{ draft.updated_by }}</template>
            <template v-if="draft.updated_at"> · {{ draft.updated_at }}</template>
          </span>
        </div>
        <a-space :size="8">
          <a-tag v-if="dirty" color="orange">有未保存修改</a-tag>
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

      <a-alert
        v-if="draft.degraded"
        type="warning"
        show-icon
        message="当前配置存在非法项，已按内置默认降级运行"
      >
        <template #description>{{ draft.degrade_reason }}</template>
      </a-alert>
      <a-alert v-if="draft.apply_note" type="info" show-icon :message="draft.apply_note" />
      <a-alert v-if="errors.length" type="error" show-icon :message="`有 ${errors.length} 处问题，保存被拒绝`">
        <template #description>
          <ul class="hg-error-list">
            <li v-for="(item, index) in errors" :key="index">{{ item }}</li>
          </ul>
        </template>
      </a-alert>

      <!-- ① 严重级别 -->
      <div class="hg-section">
        <div class="hg-section-head">
          <span class="hg-section-title">① 严重级别</span>
          <span class="hg-section-sub">
            决定大屏与列表最终把实例显示成什么（故障 / 风险 / 健康）。
            档位数量、顺序与「哪一档对应哪种显示」由平台固定，你只需要改名称和配色。
          </span>
        </div>
        <a-table
          :columns="levelColumns"
          :data-source="draft.levels || []"
          row-key="code"
          :pagination="false"
          size="small"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'code'">
              <span class="hg-level-badge" :style="{ background: record.color || '#8c8c8c' }">{{ record.code }}</span>
            </template>
            <template v-else-if="column.key === 'name'">
              <a-input v-model:value="record.name" style="width: 160px" />
            </template>
            <template v-else-if="column.key === 'color'">
              <div class="hg-color-cell">
                <span class="hg-color-swatch" :style="{ background: record.color || '#d9d9d9' }" />
                <a-input v-model:value="record.color" placeholder="#rrggbb" style="width: 110px" />
              </div>
            </template>
            <template v-else-if="column.key === 'health_state'">
              <a-tag :color="screenClassColor(record.health_state)">
                {{ screenClassText(record.health_state) }}
              </a-tag>
            </template>
          </template>
        </a-table>
      </div>

      <!-- ② 健康检查项 -->
      <div class="hg-section">
        <div class="hg-section-head">
          <span class="hg-section-title">② 健康检查项（{{ checkRows.length }} 项）</span>
          <span class="hg-section-sub">
            改数字就是改阈值，改下拉就是改严重程度。<strong>比较方向由检查项语义固定</strong>（使用率类=超过、命中率类=低于），
            不需要理解算子。以上都不命中时判定为
            <a-select
              v-model:value="draft.fallback"
              size="small"
              :options="fallbackOptions"
              class="hg-fallback-select"
            />
            。
          </span>
        </div>
        <a-table
          :columns="checkColumns"
          :data-source="checkRows"
          row-key="key"
          :pagination="false"
          size="small"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'label'">
              <div class="hg-check-label">
                <span>{{ record.label }}</span>
                <a-tag v-if="record.scenario" class="hg-scenario">{{ record.scenario }}</a-tag>
              </div>
            </template>
            <template v-else-if="column.key === 'group'">
              <span class="hg-group-text">{{ record.groupLabel }}</span>
            </template>
            <template v-else-if="column.key === 'trigger'">
              <!-- 数值型：方向符 + 数字 + 单位，一眼可改 -->
              <template v-if="record.kind === 'number'">
                <div class="hg-trigger">
                  <span class="hg-op">{{ record.opText }}</span>
                  <a-input-number
                    v-model:value="record.condition.value"
                    :min="0"
                    :step="stepOf(record.condition)"
                    style="width: 104px"
                  />
                  <span v-if="record.unit" class="hg-unit">{{ record.unit }}</span>
                  <a-tooltip :title="record.directionTip">
                    <QuestionCircleOutlined class="hg-tip-icon" />
                  </a-tooltip>
                </div>
              </template>
              <!-- 状态型 / 自定义：只读条件摘要，条件体在高级区维护 -->
              <template v-else>
                <span class="hg-trigger-text" :title="record.triggerText">{{ record.triggerText }}</span>
                <a-tag v-if="record.custom" class="hg-scenario" color="purple">组合条件</a-tag>
              </template>
              <div v-if="record.gateText" class="hg-gate">仅当 {{ record.gateText }}</div>
            </template>
            <template v-else-if="column.key === 'level'">
              <div class="hg-level-cell">
                <!-- 不用 v-model：结论项被删时 record.result 为 null，模板直接取属性会抛错 -->
                <a-select
                  :value="record.result ? record.result.level : undefined"
                  :options="levelOptions"
                  :disabled="!record.result"
                  size="small"
                  style="width: 110px"
                  @update:value="(val) => setRowLevel(record, val)"
                />
                <a-tooltip v-if="record.levelShared > 1" :title="`这 ${record.levelShared} 项共用同一个结论项（${record.resultCode}），改级别会一起生效`">
                  <LinkOutlined class="hg-tip-icon" />
                </a-tooltip>
                <a-tag v-if="!record.result" color="red">结论项缺失</a-tag>
              </div>
            </template>
            <template v-else-if="column.key === 'enabled'">
              <a-tooltip :title="record.ruleShared > 1 ? `这 ${record.ruleShared} 项属同一条检查规则，开关一起生效` : ''">
                <a-switch v-model:checked="record.rule.enabled" size="small" />
              </a-tooltip>
            </template>
            <template v-else-if="column.key === 'actions'">
              <a-space v-if="record.custom" :size="2">
                <a-button type="link" size="small" @click="openCheckModal(record)">编辑</a-button>
                <a-popconfirm title="删除该自定义检查项？" @confirm="removeCustomCheck(record)">
                  <a-button type="link" size="small" danger>删除</a-button>
                </a-popconfirm>
              </a-space>
              <span v-else class="hg-group-text">—</span>
            </template>
          </template>
        </a-table>
        <div v-if="missingChecks.length" class="hg-footnote">
          另有 {{ missingChecks.length }} 项内置检查项不在当前配置里，因此未在上表显示：
          {{ missingChecks.map((item) => item.label).join('、') }}。
          如属误删，可在下方「高级」的原始 JSON 中恢复默认配置。
        </div>
      </div>

      <!-- ③ 高级 -->
      <a-collapse v-model:activeKey="advancedKeys" class="hg-advanced">
        <a-collapse-panel key="advanced" header="③ 高级：自定义检查项与高级设置">
          <div class="hg-advanced-block">
            <div class="hg-advanced-head">
              <span>需要“多个条件同时满足”这类复杂判定时，新建自定义检查项。</span>
              <a-button size="small" type="primary" @click="openCheckModal()">
                <PlusOutlined />
                新建自定义检查项
              </a-button>
            </div>
            <div class="hg-advanced-hint">
              当前共 {{ customCount }} 条自定义检查项<template v-if="customCount">，已在上表以「组合条件」标记展示</template>。
            </div>
          </div>

          <div class="hg-advanced-block">
            <div class="hg-advanced-head">
              <span>规则明细（只读）——上表由这些规则投影而来，这里是完整视图，便于核对。</span>
            </div>
            <a-table
              :columns="ruleColumns"
              :data-source="draft.rules || []"
              row-key="code"
              :pagination="{ pageSize: 8 }"
              size="small"
            >
              <template #bodyCell="{ column, record }">
                <template v-if="column.key === 'conditions'">
                  <span class="hg-trigger-text">{{ ruleText(record) || '—' }}</span>
                </template>
                <template v-else-if="column.key === 'enabled'">
                  <a-tag :color="record.enabled ? 'green' : 'default'">{{ record.enabled ? '启用' : '停用' }}</a-tag>
                </template>
              </template>
            </a-table>
          </div>

          <div class="hg-advanced-block">
            <div class="hg-advanced-head">
              <span>原始 JSON（危险操作）——直接改整份配置，保存前会先解析校验。</span>
              <a-space :size="6">
                <a-button size="small" @click="loadJsonFromDraft">从当前配置生成</a-button>
                <a-button size="small" type="primary" @click="applyJson">解析并应用</a-button>
              </a-space>
            </div>
            <a-textarea v-model:value="jsonText" class="hg-json" :rows="14" spellcheck="false" />
            <div v-if="jsonError" class="hg-json-error">{{ jsonError }}</div>
          </div>
        </a-collapse-panel>
      </a-collapse>

      <!-- 自定义检查项弹窗：一次性建出「结论项 + 规则」，用户只面对一个概念 -->
      <a-modal
        v-model:open="checkModal.open"
        class="hg-check-modal"
        :title="checkModal.editingCode ? '编辑自定义检查项' : '新建自定义检查项'"
        width="880px"
        :confirm-loading="checkModal.saving"
        @ok="submitCheck"
      >
        <a-form layout="vertical">
          <a-alert
            v-if="checkModal.mixed"
            class="hg-modal-alert"
            type="warning"
            show-icon
            message="该检查项同时使用了「任一满足」与「全部满足」两组条件"
            description="弹窗只能表达其中一种组合方式，直接保存会把两组条件合并成一种。请关闭本弹窗，改用「高级 › 原始 JSON」编辑。"
          />
          <a-row :gutter="16">
            <a-col :span="10">
              <a-form-item label="检查项名称" required>
                <a-input v-model:value="checkModal.name" placeholder="如：P95 慢查询耗时偏高" />
              </a-form-item>
            </a-col>
            <a-col :span="7">
              <a-form-item label="分类">
                <a-select v-model:value="checkModal.category" :options="categoryOptions" />
              </a-form-item>
            </a-col>
            <a-col :span="7">
              <a-form-item label="命中后严重级别" required>
                <a-select v-model:value="checkModal.level" :options="levelOptions" />
              </a-form-item>
            </a-col>
            <a-col :span="24">
              <a-form-item label="触发条件（满足后命中）">
                <div class="hg-cond-mode">
                  <a-radio-group v-model:value="checkModal.mode" size="small">
                    <a-radio-button value="or">任一条件满足（OR）</a-radio-button>
                    <a-radio-button value="and">全部条件满足（AND）</a-radio-button>
                  </a-radio-group>
                  <span class="hg-advanced-hint">选择可用字段后，比较方式与取值形态会自动适配。</span>
                </div>
                <grade-condition-list
                  :conditions="checkModal.conditions"
                  :facts="facts"
                  @remove="(index) => checkModal.conditions.splice(index, 1)"
                />
                <a-button type="dashed" size="small" class="hg-cond-add" @click="addCondition">
                  <PlusOutlined />
                  添加条件
                </a-button>
              </a-form-item>
            </a-col>
            <a-col :span="24">
              <a-form-item label="命中说明（可选，展示在明细页的判定依据里）">
                <a-input v-model:value="checkModal.message" placeholder="如：P95 {observed}，达到 {threshold}" />
              </a-form-item>
            </a-col>
            <a-col :span="24">
              <a-form-item label="处置建议（可选）">
                <a-input v-model:value="checkModal.advice" placeholder="如：抓取慢日志定位 Top SQL" />
              </a-form-item>
            </a-col>
          </a-row>
        </a-form>
      </a-modal>
    </div>
  </a-spin>
</template>

<script setup>
// 健康分级内联配置面板（替代原「只读预览 → 跳隐藏路由页编辑」的动线）。
//
// 设计约束（改动前务必读）：
// 1) 后端 schema 不动。本面板是现有 JSON 的「受限投影 + 就地编辑」，
//    行 → 条件、级别 → 结果项 level、启用 → 规则 enabled，全部原地写回。
// 2) 术语只用两个：严重级别、健康检查项。结果项 / 判定规则 / 事实字典 / 兜底
//    这些实现概念不下沉到主路径（规则明细放高级区且标为只读核对用）。
// 3) 档位数量与 health_state 映射收回平台，不暴露编辑 —— 它是平台语义契约
//    （大屏圆饼、列表排序、告警中心筛选都按它工作）。
// 4) 前端只做 3 类校验（名称必填 / 阈值非负数字 / 配色格式），
//    引用完整性与事实存在性交给后端 ValidateHealthGradeConfig 兜底，不再双份维护。
import { message } from 'ant-design-vue'
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { LinkOutlined, PlusOutlined, QuestionCircleOutlined } from '@ant-design/icons-vue'
import {
  getMonitorHealthGradeApi,
  resetMonitorHealthGradeApi,
  updateMonitorHealthGradeApi,
} from '@/api/monitor'
import { screenClassColor, screenClassText } from '../utils'
import { CHECK_GROUP_LABEL, resolveHealthChecks, ruleConditionText } from './healthGradeCatalog'
import GradeConditionList from './GradeConditionList.vue'

defineOptions({ name: 'MonitorHealthGradePanel' })

const CATEGORY_OPTIONS = Object.entries(CHECK_GROUP_LABEL).map(([value, label]) => ({ value, label }))

// ---------------------------------------------------------------- 状态
// 注意：draft 必须在所有引用它的 computed / watch 之前定义（setup 期 TDZ 会白屏）
const loading = ref(false)
const saving = ref(false)
const errors = ref([])
const facts = ref([])
const jsonText = ref('')
const jsonError = ref('')
const advancedKeys = ref([])

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

// 脏标记：回填/重置期间抑制，避免把加载动作误判为用户编辑
const dirty = ref(false)
let suppressDirty = false

const checkModal = reactive({
  open: false,
  saving: false,
  editingCode: '',
  mixed: false,
  name: '',
  category: 'performance',
  level: 'P2',
  mode: 'or',
  conditions: [],
  message: '',
  advice: '',
})

// ---------------------------------------------------------------- 投影
const projection = computed(() => resolveHealthChecks({
  rules: draft.rules,
  results: draft.results,
  facts: facts.value,
}))
const checkRows = computed(() => projection.value.rows)
const missingChecks = computed(() => projection.value.missing)
const customCount = computed(() => checkRows.value.filter((row) => row.custom).length)
// 保存后回写 draft 会触发脏标记，这里统一靠 suppressDirty 抑制
const projectionFactMap = computed(() => projection.value.factMap)

const levelOptions = computed(() => (draft.levels || [])
  .map((item) => ({ value: item.code, label: `${item.code} ${item.name}` })))

// 兜底只能是"健康侧"的结论项：让它指向故障档等于把健康实例判成故障
const fallbackOptions = computed(() => {
  const healthyLevels = new Set((draft.levels || [])
    .filter((item) => item.health_state === 'healthy')
    .map((item) => item.code))
  const list = (draft.results || []).filter((item) => healthyLevels.has(item.level))
  const options = list.map((item) => ({ value: item.code, label: `${item.name}（${item.code}）` }))
  if (draft.fallback && !options.some((item) => item.value === draft.fallback)) {
    const current = (draft.results || []).find((item) => item.code === draft.fallback)
    options.push({ value: draft.fallback, label: current ? `${current.name}（${current.code}）` : draft.fallback })
  }
  return options
})

const levelColumns = [
  { title: '等级', key: 'code', width: 90 },
  { title: '名称', key: 'name', width: 200 },
  { title: '配色', key: 'color', width: 180 },
  { title: '大屏显示', key: 'health_state', width: 140 },
  { title: '说明', dataIndex: 'description' },
]
const checkColumns = [
  { title: '检查项', key: 'label', width: 220 },
  { title: '分类', key: 'group', width: 80 },
  { title: '触发条件', key: 'trigger', width: 330 },
  { title: '严重级别', key: 'level', width: 150 },
  { title: '启用', key: 'enabled', width: 70 },
  { title: '操作', key: 'actions', width: 110 },
]
const ruleColumns = [
  { title: '优先级', dataIndex: 'priority', key: 'priority', width: 80 },
  { title: '规则', dataIndex: 'code', key: 'code', width: 190 },
  { title: '命中后结论项', dataIndex: 'result', key: 'result', width: 170 },
  { title: '判定条件', key: 'conditions' },
  { title: '启用', key: 'enabled', width: 80 },
]
const categoryOptions = CATEGORY_OPTIONS

const ruleText = (rule) => ruleConditionText(rule, projectionFactMap.value)

// 阈值步长：小于 1 的阈值（如慢查询 0.02 条/秒）用小数步长，否则整数
const stepOf = (condition) => (Number(condition?.value) < 1 ? 0.01 : 1)

// 改级别的落点是「结论项」，不是规则：同一结论项被多行共用时一次改全生效
// （这正是"4 个容量阈值同属一个结论"的正确语义，表格里已用联动标记提示过）
const setRowLevel = (row, level) => {
  if (!row.result) return
  row.result.level = level
}

// ---------------------------------------------------------------- 加载与回填

const cloneWithoutFacts = (cfg) => {
  const copy = JSON.parse(JSON.stringify(cfg || {}))
  delete copy.facts
  return copy
}

const applyConfig = (cfg) => {
  suppressDirty = true
  facts.value = cfg?.facts || []
  // 先重置骨架再合并：服务端对空字段 omitempty 不下发，
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
  errors.value = []
  jsonError.value = ''
  jsonText.value = ''
  nextTick(() => {
    suppressDirty = false
    dirty.value = false
  })
}

const reload = async () => {
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

onMounted(reload)

// 深度监听整体草案：任何就地编辑都会置脏（回填/重置期间由 suppressDirty 抑制）
watch(draft, () => {
  if (!suppressDirty) dirty.value = true
}, { deep: true })

// ---------------------------------------------------------------- 校验与保存

const validate = () => {
  const list = []
  const levels = draft.levels || []
  if (!levels.length) list.push('至少需要一个严重级别')
  levels.forEach((level) => {
    if (!level.name?.trim()) list.push(`等级 ${level.code} 的名称不能为空`)
    if (level.color && !/^#[0-9a-fA-F]{6}$/.test(level.color)) {
      list.push(`等级 ${level.code} 的配色应为 #rrggbb 形式`)
    }
  })
  checkRows.value.forEach((row) => {
    if (row.kind !== 'number') return
    const value = Number(row.condition?.value)
    if (!Number.isFinite(value) || value < 0) {
      list.push(`${row.label}${row.scenario ? `（${row.scenario}）` : ''} 的阈值必须是不小于 0 的数字`)
    }
  })
  return list
}

const save = async () => {
  const problems = validate()
  errors.value = problems
  if (problems.length) {
    message.error(`有 ${problems.length} 处问题，请先修正`)
    return
  }
  saving.value = true
  try {
    const res = await updateMonitorHealthGradeApi({ config: cloneWithoutFacts(draft) })
    applyConfig(res?.data || {})
    message.success('健康分级已保存，10 秒内生效')
  } catch (error) {
    // 后端结构校验的失败原因直接透出，不吞掉
    errors.value = [error?.message || '保存失败']
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
    message.success('已恢复为内置默认分级策略')
  } catch (error) {
    message.error(error?.message || '恢复默认失败')
  } finally {
    saving.value = false
  }
}

// ---------------------------------------------------------------- 自定义检查项
// 一次操作同时建「结论项 + 规则」：用户只面对"检查项"一个概念，
// 代码层面两者共用同一个 code（与内置配置 1:1 的形态一致），引用永远成立。
const openCheckModal = (row) => {
  checkModal.editingCode = row?.ruleCode || ''
  if (row) {
    const rule = row.rule
    const anyList = rule.any || []
    const allList = rule.all || []
    // 同一条规则里同时用 OR 与 AND 时，弹窗只能表达其一；强行展平会在保存时
    // 悄悄把「A 或 B」改成「A 且 B」。这类规则只在原始 JSON 里编辑。
    checkModal.mixed = Boolean(anyList.length && allList.length)
    checkModal.mode = allList.length && !anyList.length ? 'and' : 'or'
    checkModal.conditions = JSON.parse(JSON.stringify([...anyList, ...allList]))
    checkModal.name = row.result?.name || rule.code
    checkModal.category = row.result?.category || 'performance'
    checkModal.level = row.result?.level || 'P2'
    checkModal.message = rule.message || ''
    checkModal.advice = row.result?.advice || ''
  } else {
    checkModal.mixed = false
    checkModal.mode = 'or'
    checkModal.conditions = []
    checkModal.name = ''
    checkModal.category = 'performance'
    checkModal.level = draft.levels?.[2]?.code || draft.levels?.[0]?.code || 'P2'
    checkModal.message = ''
    checkModal.advice = ''
  }
  checkModal.open = true
}

const addCondition = () => {
  checkModal.conditions.push({ fact: '', op: 'eq', value: '', label: '' })
}

// code 生成：同一秒内连建两个会撞号，这里靠集合兜底自增
const nextCode = () => {
  const now = new Date()
  const pad = (value) => String(value).padStart(2, '0')
  const base = `custom_${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
  const used = new Set((draft.results || []).map((item) => item.code))
  if (!used.has(base)) return base
  let seq = 2
  while (used.has(`${base}_${seq}`)) seq += 1
  return `${base}_${seq}`
}

const submitCheck = () => {
  if (checkModal.mixed) {
    message.warning('该检查项同时使用了「任一满足」与「全部满足」，请在原始 JSON 中编辑')
    return
  }
  if (!checkModal.name.trim()) {
    message.warning('请填写检查项名称')
    return
  }
  if (!checkModal.conditions.length) {
    message.warning('至少需要一个触发条件')
    return
  }
  if (checkModal.conditions.some((cond) => !cond.fact)) {
    message.warning('有条件未选择字段')
    return
  }
  const code = checkModal.editingCode || nextCode()
  const isEdit = Boolean(checkModal.editingCode)
  const any = checkModal.mode === 'or' ? checkModal.conditions : []
  const all = checkModal.mode === 'and' ? checkModal.conditions : []

  const result = (draft.results || []).find((item) => item.code === code)
  if (result) {
    result.name = checkModal.name.trim()
    result.category = checkModal.category
    result.level = checkModal.level
    result.advice = checkModal.advice
    result.enabled = true
  } else {
    draft.results.push({
      code,
      name: checkModal.name.trim(),
      level: checkModal.level,
      category: checkModal.category,
      advice: checkModal.advice,
      health_state: '',
      enabled: true,
    })
  }

  const rule = (draft.rules || []).find((item) => item.code === code)
  if (rule) {
    rule.any = any
    rule.all = all
    rule.message = checkModal.message
    rule.enabled = true
  } else {
    const maxPriority = Math.max(0, ...(draft.rules || []).map((item) => item.priority || 0))
    draft.rules.push({
      code,
      result: code,
      enabled: true,
      priority: maxPriority + 10,
      any,
      all,
      message: checkModal.message,
      description: '',
      stop_on_hit: false,
    })
  }
  checkModal.open = false
  message.success(isEdit ? '自定义检查项已更新（尚未保存）' : '自定义检查项已添加（尚未保存）')
}

const removeCustomCheck = (row) => {
  const ruleIndex = (draft.rules || []).findIndex((item) => item.code === row.ruleCode)
  if (ruleIndex !== -1) draft.rules.splice(ruleIndex, 1)
  // 结论项只被这条规则引用，一并移除，避免留下孤儿导致校验失败
  const resultIndex = (draft.results || []).findIndex((item) => item.code === row.resultCode)
  if (resultIndex !== -1) draft.results.splice(resultIndex, 1)
  message.success('已删除（尚未保存）')
}

// ---------------------------------------------------------------- 原始 JSON

const loadJsonFromDraft = () => {
  jsonText.value = JSON.stringify(cloneWithoutFacts(draft), null, 2)
  jsonError.value = ''
}

const applyJson = () => {
  let parsed
  try {
    parsed = JSON.parse(jsonText.value)
  } catch (error) {
    jsonError.value = `JSON 解析失败：${error?.message || '格式不合法'}`
    return
  }
  if (!Array.isArray(parsed.levels) || !Array.isArray(parsed.results) || !Array.isArray(parsed.rules)) {
    jsonError.value = '配置结构不完整：levels / results / rules 必须是数组'
    return
  }
  jsonError.value = ''
  suppressDirty = true
  Object.assign(draft, cloneWithoutFacts(parsed))
  nextTick(() => {
    suppressDirty = false
    dirty.value = true
  })
  message.success('已应用到表单，确认无误后点「保存并生效」')
}

// ---------------------------------------------------------------- 对外接口

defineExpose({
  reload,
  isDirty: () => dirty.value,
  // 父级切换 Tab 时用来提示未保存：确认放弃后由调用方重置标记
  clearDirty: () => { dirty.value = false },
})
</script>

<style scoped>
.hg-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.hg-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.hg-head-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.hg-meta {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.hg-error-list {
  margin: 0;
  padding-left: 18px;
}

.hg-section {
  padding: 12px 14px;
  border: 1px solid var(--gi-border-color, #e5e8eb);
  border-radius: 8px;
  background: var(--gi-color-bg-container, #fff);
}

.hg-section-head {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 10px;
}

.hg-section-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--gi-color-text-primary, #1f2d38);
}

.hg-section-sub {
  font-size: 12px;
  line-height: 1.7;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.hg-fallback-select {
  width: 190px;
  margin: 0 2px;
}

.hg-level-badge {
  display: inline-block;
  min-width: 30px;
  padding: 1px 6px;
  border-radius: 4px;
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  text-align: center;
}

.hg-color-cell {
  display: flex;
  align-items: center;
  gap: 6px;
}

.hg-color-swatch {
  width: 14px;
  height: 14px;
  border-radius: 3px;
  border: 1px solid var(--gi-border-color, #e5e8eb);
  flex-shrink: 0;
}

.hg-check-label {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.hg-scenario {
  font-size: 11px;
  line-height: 16px;
  margin: 0;
}

.hg-group-text {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.hg-trigger {
  display: flex;
  align-items: center;
  gap: 6px;
}

.hg-op {
  font-size: 13px;
  font-weight: 600;
  color: var(--gi-color-text-secondary, #5a6a7a);
  min-width: 14px;
  text-align: center;
}

.hg-unit {
  font-size: 12px;
  color: var(--gi-color-text-secondary, #5a6a7a);
}

.hg-trigger-text {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  font-size: 12px;
  color: var(--gi-color-text-secondary, #5a6a7a);
  word-break: break-all;
}

.hg-gate {
  margin-top: 2px;
  font-size: 11px;
  color: var(--gi-color-text-quaternary, #a8b3c2);
}

.hg-tip-icon {
  font-size: 12px;
  color: var(--gi-color-text-quaternary, #b6c0cd);
  cursor: help;
}

.hg-level-cell {
  display: flex;
  align-items: center;
  gap: 6px;
}

.hg-footnote {
  margin-top: 8px;
  font-size: 12px;
  line-height: 1.7;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.hg-advanced-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 4px 0 12px;
}

.hg-advanced-block + .hg-advanced-block {
  border-top: 1px dashed var(--gi-border-color, #e5e8eb);
  padding-top: 12px;
}

.hg-advanced-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 13px;
  color: var(--gi-color-text-secondary, #5a6a7a);
  flex-wrap: wrap;
}

.hg-advanced-hint {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.hg-cond-mode {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}

.hg-cond-add {
  margin-top: 6px;
}

.hg-json {
  font-family: 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px;
}

.hg-json-error {
  font-size: 12px;
  color: #cf1322;
}
</style>

<!--
  弹窗内容会被 teleport 到 body，上面 scoped 的样式选不到它，
  所以弹窗内的样式必须单独写在非 scoped 块里，并用弹窗自身的 class 做前缀。
-->
<style>
.hg-check-modal .hg-modal-alert {
  margin-bottom: 16px;
}
</style>
