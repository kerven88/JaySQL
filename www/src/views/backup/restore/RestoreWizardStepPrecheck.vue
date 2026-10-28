<template>
  <div class="step-panel">
    <div class="panel-title">恢复预检查</div>
    <div class="panel-desc">进入本步骤会自动执行预检查，结果只读展示；如需调整请返回上一步修改配置。</div>

    <RestorePrecheckPanel
      readonly
      :loading="precheckLoading"
      :result="precheckResult"
      :error-message="precheckError"
    />
  </div>
</template>

<script setup>
import RestorePrecheckPanel from './RestorePrecheckPanel.vue'

defineOptions({ name: 'RestoreWizardStepPrecheck' })

// 本步骤是纯展示：不注入表单、不 emit 任何事件，避免与「只读呈现」冲突。
defineProps({
  precheckLoading: { type: Boolean, default: false },
  precheckResult: { type: Object, default: null },
  precheckError: { type: String, default: '' },
})
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
  margin-bottom: var(--gi-spacing-md, 16px);
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-text-tertiary, #8a979f);
}
</style>
