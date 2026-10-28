<template>
  <div class="restore-object-picker">
    <!-- 单文件产物：没有可拆分对象，整块消失而不是留一张空表 -->
    <a-alert v-if="!supportsSelect" type="info" show-icon class="picker-alert">
      <template #message>
        <span class="picker-alert__title">该备份为单对象产物</span>
      </template>
      <template #description>
        {{ hint || '将按整体导入到目标库，不支持按对象拆分。' }}
      </template>
    </a-alert>

    <template v-else>
      <div class="selection-card-group">
        <button type="button" class="selection-card" :class="{ active: form.object_scope === 'ALL' }" @click="setScope('ALL')">
          全部对象
        </button>
        <button type="button" class="selection-card" :class="{ active: form.object_scope === 'CUSTOM' }" @click="setScope('CUSTOM')">
          自定义选择
        </button>
      </div>

      <a-table
        v-if="form.object_scope === 'CUSTOM'"
        class="picker-table"
        :columns="objectColumns"
        :data-source="objects"
        :pagination="false"
        :row-selection="rowSelection"
        size="small"
        row-key="name"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'format'">
            <a-tag>{{ String(record.format || '-').toUpperCase() }}</a-tag>
          </template>
        </template>
      </a-table>

      <a-divider class="picker-divider" />

      <div class="picker-subtitle">
        目标{{ dbLabel }}映射
      </div>
      <a-table
        class="picker-table"
        :columns="mappingColumns"
        :data-source="mappingRows"
        :pagination="false"
        size="small"
        row-key="source_db"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'target_db'">
            <a-input
              :value="record.target_db"
              :status="nameError(record.target_db) ? 'error' : ''"
              :placeholder="record.source_db"
              @change="(e) => setMapping(record.source_db, e.target.value)"
            />
            <div v-if="nameError(record.target_db)" class="picker-error">{{ nameError(record.target_db) }}</div>
          </template>
        </template>
      </a-table>

      <div v-if="!mappingRows.length" class="picker-empty">请先选择需要恢复的对象。</div>
      <div v-else-if="isMultiTarget" class="picker-hint">
        已选择 {{ mappingRows.length }} 个{{ dbLabel }}，未单独指定目标名的将按同名导入。
      </div>
    </template>
  </div>
</template>

<script setup>
import { computed, inject } from 'vue'
import { validateObjectName } from './constants'

defineOptions({ name: 'RestoreObjectPicker' })

// 向导表单由父级 RestoreWizard 通过 provide 注入：
// 子组件需要读写十余个字段，用 props 双向绑定会触发 vue/no-mutating-props，
// 逐字段 emit 又会把调用方淹没在样板代码里。这里统一走注入的响应式对象。
const form = inject('restoreWizardForm')

const props = defineProps({
  objects: { type: Array, default: () => [] },
  supportsSelect: { type: Boolean, default: false },
  dbType: { type: String, default: '' },
  hint: { type: String, default: '' },
})

const emit = defineEmits(['change'])

const dbLabel = computed(() => (String(props.dbType || '').toUpperCase() === 'POSTGRESQL' ? '库' : '数据库'))

const objectColumns = [
  { title: '源对象', dataIndex: 'name', key: 'name' },
  { title: '格式', dataIndex: 'format', key: 'format', width: 110 },
  { title: '产物', dataIndex: 'entry', key: 'entry' },
]

const mappingColumns = computed(() => [
  { title: `源${dbLabel.value}`, dataIndex: 'source_db', key: 'source_db', width: 260 },
  { title: `目标${dbLabel.value}`, key: 'target_db' },
])

const selectedKeys = computed(() =>
  form.object_scope === 'CUSTOM' ? form.selected_objects || [] : props.objects.map((item) => item.name),
)

const mappingRows = computed(() =>
  (selectedKeys.value || []).map((name) => ({
    source_db: name,
    target_db: form.mapping?.[name] || '',
  })),
)

const isMultiTarget = computed(() => mappingRows.value.length > 1)

const rowSelection = computed(() => ({
  selectedRowKeys: form.selected_objects || [],
  onChange: (keys) => {
    form.selected_objects = keys
    emit('change')
  },
}))

const setScope = (scope) => {
  form.object_scope = scope
  if (scope === 'ALL' || !form.selected_objects?.length) {
    form.selected_objects = props.objects.map((item) => item.name)
  }
  emit('change')
}

const setMapping = (source, value) => {
  if (!form.mapping) form.mapping = {}
  form.mapping[source] = value
  emit('change')
}

const nameError = (value) => validateObjectName(value, props.dbType)
</script>

<style scoped lang="scss">
.restore-object-picker {
  max-width: 860px;
}

.picker-alert {
  margin-bottom: var(--gi-spacing-sm, 8px);
}

.picker-alert__title {
  font-weight: 600;
}

.selection-card-group {
  display: flex;
  gap: var(--gi-spacing-sm, 8px);
  flex-wrap: wrap;
}

.selection-card {
  min-width: 108px;
  padding: 8px 16px;
  border: 1px solid var(--gi-border-color, #d9e1e6);
  border-radius: var(--gi-radius-md, 8px);
  background: var(--gi-color-container-bg, #fff);
  color: var(--gi-color-text-secondary, #5c6b73);
  cursor: pointer;
  transition: all 120ms;
}

.selection-card:hover {
  border-color: var(--gi-color-primary, #0f766e);
  color: var(--gi-color-primary, #0f766e);
}

.selection-card.active {
  border-color: var(--gi-color-primary, #0f766e);
  background: var(--gi-color-primary-light, rgba(15, 118, 110, 0.08));
  color: var(--gi-color-primary, #0f766e);
  font-weight: 600;
}

.picker-divider {
  margin: var(--gi-spacing-md, 16px) 0;
}

.picker-subtitle {
  font-size: var(--gi-font-size-body, 14px);
  font-weight: 600;
  color: var(--gi-color-text-primary, #1f2d3d);
  margin-bottom: var(--gi-spacing-sm, 8px);
}

.picker-subtitle__hint {
  margin-left: var(--gi-spacing-sm, 8px);
  font-weight: 400;
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-text-tertiary, #8a979f);
}

.picker-table {
  margin-bottom: var(--gi-spacing-sm, 8px);
}

.picker-hint,
.picker-empty {
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-text-tertiary, #8a979f);
}

.picker-error {
  margin-top: 2px;
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-color-error, #ff4d4f);
}
</style>
