<template>
  <div class="ov-card">
    <div class="ov-card-label">{{ label }}</div>
    <div
      class="ov-card-value ov-num-link"
      :class="toneClass"
      :title="clickable ? '点击查看明细' : ''"
      @click="onClick"
    >
      {{ value }}
    </div>
    <div class="ov-card-sub">{{ sub }}</div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

defineOptions({ name: 'MonitorOverviewCard' })

const props = defineProps({
  label: { type: String, required: true },
  value: { type: [String, Number], required: true },
  sub: { type: String, default: '' },
  // 与现有计数卡色调保持一致：bad（红）/ warn（橙）/ good（绿）/ info（蓝）/ p2（P2 级别色）
  tone: { type: String, default: 'info' },
  clickable: { type: Boolean, default: true },
})

const emit = defineEmits(['click'])

const toneClass = computed(() => `tone-${props.tone}`)

const onClick = () => {
  if (props.clickable) emit('click')
}
</script>

<style scoped>
.ov-card {
  padding: 14px 16px;
  background: var(--gi-color-container-bg, #fff);
  border: 1px solid var(--gi-color-border-soft, rgba(0, 0, 0, 0.08));
  border-radius: 8px;
}

.ov-card-label {
  font-size: 13px;
  color: var(--gi-color-text-primary, rgba(0, 0, 0, 0.85));
}

.ov-card-value {
  padding: 8px 0 4px;
  font-size: 30px;
  font-weight: 600;
  line-height: 1.2;
}

.ov-card-value.tone-bad {
  color: #cf1322;
}
.ov-card-value.tone-warn {
  color: #d46b08;
}
.ov-card-value.tone-good {
  color: #389e0d;
}
.ov-card-value.tone-info {
  color: #1677ff;
}
/* P2 级别色：与 utils.js ALERT_LEVEL_DEFS 中 P2 的 color 定义同源（--gi-color-warning），
   不另硬编码色值，级别色调整时自动跟随。 */
.ov-card-value.tone-p2 {
  color: var(--gi-color-warning, #faad14);
}

.ov-card-sub {
  font-size: 12px;
  color: var(--gi-color-text-tertiary, #8a94a6);
}

.ov-card-value.ov-num-link {
  cursor: pointer;
}
.ov-card-value.ov-num-link:hover {
  text-decoration: underline;
  text-underline-offset: 4px;
}
</style>
