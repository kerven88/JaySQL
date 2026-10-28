<template>
  <div class="step-panel">
    <div class="panel-title">选择备份集</div>
    <div class="panel-desc">仅支持从备份集恢复；不支持按时间点或日志回放。</div>

    <a-form layout="vertical" class="step-form">
      <a-form-item label="备份集" required>
        <a-select
          :value="form.backup_set_id"
          class="block-select"
          :options="setOptions"
          :field-names="{ label: 'label', value: 'value' }"
          :loading="setsLoading"
          :filter-option="filterSetOption"
          show-search
          placeholder="按备份集 ID、任务ID、源库搜索"
          @change="(value) => emit('change-set', value)"
        >
          <template #option="{ label, sub }">
            <div class="set-option">
              <span class="set-option__main">{{ label }}</span>
              <span class="set-option__sub">{{ sub }}</span>
            </div>
          </template>
        </a-select>
      </a-form-item>

      <a-form-item label="任务名称">
        <a-input :value="form.name" maxlength="128" placeholder="请输入任务名称" @change="(e) => (form.name = e.target.value)" />
      </a-form-item>
    </a-form>

    <div v-if="selectedSet" class="kind-card-grid">
      <div class="kind-card">
        <div class="kind-card__label">恢复方式</div>
        <div class="kind-card__value">{{ isPhysical ? '物理恢复' : '逻辑恢复' }}</div>
        <div class="kind-card__sub">{{ selectedSet.db_type || '-' }} · {{ driverLabel }}</div>
      </div>
      <div class="kind-card">
        <div class="kind-card__label">备份类型</div>
        <div class="kind-card__value" :class="{ 'is-accent': !isFull }">
          {{ isFull ? '全量备份' : '非全量备份' }}
        </div>
        <div class="kind-card__sub">{{ objectsInfo.hint || '解析中…' }}</div>
      </div>
      <div class="kind-card">
        <div class="kind-card__label">产物</div>
        <div class="kind-card__value">{{ formatBytes(selectedSet.size_bytes) }}</div>
        <div class="kind-card__sub">{{ objectsInfo.artifact_format || selectedSet.driver_type || '-' }}</div>
      </div>
    </div>

    <div v-else class="panel-desc">选择备份集后，后续步骤会按其类型自动分流。</div>
  </div>
</template>

<script setup>
import { computed, inject } from 'vue'
import { DRIVER_LABELS, formatBytes } from './constants'

defineOptions({ name: 'RestoreWizardStepSource' })

const form = inject('restoreWizardForm')

const props = defineProps({
  sets: { type: Array, default: () => [] },
  setsLoading: { type: Boolean, default: false },
  objectsInfo: { type: Object, required: true },
  selectedSet: { type: Object, default: null },
  isPhysical: { type: Boolean, default: false },
  isFull: { type: Boolean, default: true },
})

const emit = defineEmits(['change-set'])

const driverLabel = computed(() => {
  const key = String(props.selectedSet?.driver_type || '').toUpperCase()
  return DRIVER_LABELS[key] || props.selectedSet?.driver_type || '-'
})

const setOptions = computed(() =>
  props.sets.map((item) => ({
    value: item.id,
    label: item.backup_sets_id || `备份集 #${item.id}`,
    sub: [
      item.db_type || '-',
      DRIVER_LABELS[String(item.driver_type || '').toUpperCase()] || item.driver_type || '-',
      formatBytes(item.size_bytes),
    ].join(' · '),
    search: `${item.backup_sets_id || ''} ${item.name || ''} ${item.task_id || ''} ${item.db_type || ''} ${item.source_db_name || ''}`.toLowerCase(),
  })),
)

const filterSetOption = (input, option) => String(option?.search || '').includes(String(input || '').toLowerCase())
</script>

<style scoped lang="scss">
.step-panel {
  max-width: 960px;
}

.panel-title {
  font-size: var(--gi-font-size-body-lg, 16px);
  font-weight: 600;
  color: var(--gi-color-text-primary, #1f2d3d);
}

.panel-desc {
  margin-top: var(--gi-spacing-xs, 4px);
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-text-tertiary, #8a979f);
}

.step-form {
  max-width: 720px;
  margin-top: var(--gi-spacing-md, 16px);
}

.block-select {
  width: 100%;
  max-width: 640px;
}

.set-option {
  display: flex;
  align-items: baseline;
  gap: var(--gi-spacing-sm, 8px);
  overflow: hidden;
}

.set-option__main {
  color: var(--gi-color-text-primary, #1f2d3d);
}

.set-option__sub {
  color: var(--gi-text-tertiary, #8a979f);
  font-size: var(--gi-font-size-caption, 12px);
}

.kind-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: var(--gi-spacing-md, 16px);
  margin-top: var(--gi-spacing-md, 16px);
}

.kind-card {
  padding: var(--gi-spacing-md, 16px);
  border: 1px solid var(--gi-border-color, #d9e1e6);
  border-left: 3px solid var(--gi-color-primary, #0f766e);
  border-radius: var(--gi-radius-md, 8px);
  background: var(--gi-color-container-bg, #fff);
}

.kind-card__label {
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-text-tertiary, #8a979f);
}

.kind-card__value {
  font-size: var(--gi-font-size-body-lg, 16px);
  font-weight: 600;
  color: var(--gi-color-primary, #0f766e);
  margin-top: var(--gi-spacing-xs, 4px);
}

.kind-card__value.is-accent {
  color: var(--gi-color-warning, #ad6800);
}

.kind-card__sub {
  margin-top: var(--gi-spacing-xs, 4px);
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-color-text-secondary, #5c6b73);
}
</style>
