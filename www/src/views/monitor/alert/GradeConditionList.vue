<template>
  <div class="cond-list">
    <div v-for="(cond, index) in conditions" :key="index" class="cond-row">
      <a-auto-complete
        :value="cond.fact"
        :options="factOptions"
        style="width: 250px"
        placeholder="事实键，动态指标可直输完整键"
        :filter-option="filterFactOption"
        @update:value="(val) => onFactChange(cond, val)"
      />
      <a-select
        :value="cond.op"
        :options="opOptionsOf(cond)"
        style="width: 170px"
        @update:value="(val) => onOpChange(cond, val)"
      />
      <!-- 比较值：按操作符与事实类型切换编辑形态（per-row 求值） -->
      <span v-if="kindOf(cond) === 'none'" class="cond-none">（无比较值）</span>
      <a-select
        v-else-if="kindOf(cond) === 'list' && enumOptionsOf(cond.fact).length"
        :value="listValueOf(cond)"
        :options="enumOptionsOf(cond.fact)"
        mode="multiple"
        style="flex: 1 1 220px; min-width: 200px"
        placeholder="选择候选值"
        @update:value="(val) => { cond.value = val }"
      />
      <a-select
        v-else-if="kindOf(cond) === 'list'"
        :value="listValueOf(cond)"
        mode="tags"
        style="flex: 1 1 220px; min-width: 200px"
        placeholder="输入后回车，可多个"
        :token-separators="[',']"
        @update:value="(val) => { cond.value = val }"
      />
      <div v-else-if="kindOf(cond) === 'range'" class="cond-range">
        <a-input-number
          :value="rangeValueOf(cond)[0]"
          style="width: 100px"
          placeholder="下限"
          @update:value="(val) => { cond.value = [val, rangeValueOf(cond)[1]] }"
        />
        <span class="cond-range-sep">~</span>
        <a-input-number
          :value="rangeValueOf(cond)[1]"
          style="width: 100px"
          placeholder="上限"
          @update:value="(val) => { cond.value = [rangeValueOf(cond)[0], val] }"
        />
      </div>
      <a-input-number
        v-else-if="kindOf(cond) === 'number'"
        :value="cond.value"
        style="width: 160px"
        @update:value="(val) => { cond.value = val }"
      />
      <a-input
        v-else
        :value="cond.value"
        style="flex: 1 1 200px; min-width: 160px"
        @update:value="(val) => { cond.value = val }"
      />
      <a-input
        :value="cond.label"
        style="width: 150px; flex-shrink: 0"
        placeholder="说明（判定依据用）"
        @update:value="(val) => { cond.label = val }"
      />
      <a-button type="text" danger size="small" @click="emit('remove', index)">
        <DeleteOutlined />
      </a-button>
    </div>
    <div v-if="!conditions.length" class="cond-empty">（无条件）</div>
  </div>
</template>

<script setup>
// 判定规则的条件行编辑器。
// 事实键用 AutoComplete：字典项下拉可选，动态指标事实（metric:<key>）允许自由输入。
// 比较值编辑形态随操作符 + 事实类型切换：
//   exists/absent → 无值；in/not_in → 多选（枚举型用候选值，否则 tags 自由输入）；
//   between → 上下限两个数字；number 型 → 数字输入；其余 → 文本输入。
// 切换事实/操作符时重置比较值为该形态的合法空值，避免残留上一形态的脏值。
import { computed } from 'vue'
import { DeleteOutlined } from '@ant-design/icons-vue'

const props = defineProps({
  conditions: { type: Array, required: true },
  facts: { type: Array, default: () => [] },
})
const emit = defineEmits(['remove'])

const ALL_OPS = ['eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'in', 'not_in', 'between', 'exists', 'absent', 'contains']

const OP_TEXT = {
  eq: '=', ne: '≠', gt: '>', gte: '≥', lt: '<', lte: '≤',
  in: '属于', not_in: '不属于', between: '介于', exists: '存在', absent: '不存在', contains: '包含',
}

// 与后端 factDef 同构：精确匹配静态事实，动态事实按前缀匹配
const factDefOf = (fact) =>
  props.facts.find((def) => {
    if (def.dynamic) {
      const prefix = `${def.key}:`
      return fact?.startsWith(prefix) && fact.length > prefix.length
    }
    return def.key === fact
  }) || null

const opsOf = (fact) => factDefOf(fact)?.ops || ALL_OPS

const opOptionsOf = (cond) => opsOf(cond.fact).map((op) => ({ value: op, label: `${op}（${OP_TEXT[op] || op}）` }))

const factOptions = computed(() =>
  props.facts.map((def) => ({
    value: def.key,
    label: def.dynamic ? `${def.key}:（${def.name}，${def.param_hint || '按前缀引用具体键'}）` : `${def.key}（${def.name}）`,
  })))

const filterFactOption = (input, option) =>
  option.value.toLowerCase().includes(input.toLowerCase()) || (option.label || '').toLowerCase().includes(input.toLowerCase())

const enumOptionsOf = (fact) => (factDefOf(fact)?.values || []).map((item) => ({ value: item, label: String(item) }))

// 比较值编辑形态：按行求值（模板内方法调用）
const kindOf = (cond) => {
  if (cond.op === 'exists' || cond.op === 'absent') return 'none'
  if (cond.op === 'in' || cond.op === 'not_in') return 'list'
  if (cond.op === 'between') return 'range'
  return factDefOf(cond.fact)?.type === 'number' ? 'number' : 'text'
}

const listValueOf = (cond) => (Array.isArray(cond.value) ? cond.value : [])
const rangeValueOf = (cond) => (Array.isArray(cond.value) && cond.value.length === 2 ? cond.value : [null, null])

const resetCondValue = (cond) => {
  const kind = kindOf(cond)
  if (kind === 'none') cond.value = null
  else if (kind === 'list') cond.value = Array.isArray(cond.value) ? cond.value : []
  else if (kind === 'range') cond.value = Array.isArray(cond.value) && cond.value.length === 2 ? cond.value : [null, null]
  else if (kind === 'number') cond.value = Number.isFinite(Number(cond.value)) && cond.value !== '' ? Number(cond.value) : 0
  else cond.value = cond.value == null ? '' : String(cond.value)
}

const onFactChange = (cond, val) => {
  cond.fact = val
  const supported = opsOf(cond.fact)
  if (!supported.includes(cond.op)) cond.op = supported[0] || 'eq'
  resetCondValue(cond)
}

const onOpChange = (cond, val) => {
  cond.op = val
  resetCondValue(cond)
}
</script>

<style scoped>
.cond-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.cond-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 2px 0;
}

.cond-none {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.cond-range {
  display: flex;
  align-items: center;
  gap: 4px;
}

.cond-range-sep {
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.cond-empty {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #8a94a6);
  padding: 2px 0;
}

@media (max-width: 768px) {
  /* 控件写的是内联 min-width: 200px/160px */
  :deep(.ant-select),
  :deep(.ant-input-number) {
    flex: 1 1 100% !important;
    min-width: 0 !important;
    max-width: 100% !important;
  }
}
</style>
