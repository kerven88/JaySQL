<template>
  <div class="step-panel">
    <!-- 标题交给上方步骤条承载，这里不再重复「确认并执行」，避免与表格之间留大片空白 -->
    <a-descriptions :column="2" bordered size="small" class="confirm-desc">
      <a-descriptions-item v-for="item in summaryItems" :key="item.label" :label="item.label">
        <span v-if="item.danger" class="confirm-desc__danger">{{ item.value }}</span>
        <span v-else>{{ item.value }}</span>
      </a-descriptions-item>
    </a-descriptions>

    <a-form layout="vertical" class="step-form">
      <a-form-item v-if="isPhysical" label="危险操作确认" required>
        <a-input v-model:value="form.confirm_text" placeholder="请输入 RECOVER" />
        <div class="field-hint">物理恢复会重写实例数据目录，请输入 RECOVER 以确认。</div>
      </a-form-item>

      <a-form-item>
        <a-checkbox v-model:checked="form.execute_confirmed">
          我已确认备份集与目标环境一致，同意执行恢复
        </a-checkbox>
      </a-form-item>
    </a-form>
  </div>
</template>

<script setup>
import { inject } from 'vue'

defineOptions({ name: 'RestoreWizardStepConfirm' })

const form = inject('restoreWizardForm')

defineProps({
  summaryItems: { type: Array, default: () => [] },
  isPhysical: { type: Boolean, default: false },
})
</script>

<style scoped lang="scss">
.step-panel {
  max-width: 960px;
}

.confirm-desc {
  max-width: 960px;
}

.confirm-desc__danger {
  color: var(--gi-color-error, #ff4d4f);
  font-weight: 600;
}

.step-form {
  max-width: 720px;
  margin-top: var(--gi-spacing-md, 16px);
}

.field-hint {
  margin-top: var(--gi-spacing-xs, 4px);
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-text-tertiary, #8a979f);
}
</style>
