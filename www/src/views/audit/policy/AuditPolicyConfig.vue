<template>
  <div class="gi-page-shell audit-policy-page">
    <div class="gi-page-toolbar">
      <div>
        <div class="page-title">策略配置</div>
        <div class="page-subtitle">配置审计等级、高危识别规则、日志保留策略与告警方式</div>
      </div>
    </div>

    <a-card class="gi-page-section" :bordered="false">
      <a-empty description="策略配置页面壳子已预留，后续可继续接入规则配置与保留策略表单" />
    </a-card>
  </div>
</template>

<script setup>
defineOptions({ name: 'AuditPolicyConfigView' })
</script>

<style scoped>
.audit-policy-page {
  gap: var(--gi-spacing-md);
}

.page-title {
  font-size: 16px;
  font-weight: 600;
  color: #1f2d38;
}

.page-subtitle {
  margin-top: 4px;
  font-size: 12px;
  color: #71818c;
}
</style>
