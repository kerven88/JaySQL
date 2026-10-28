<template>
  <div class="gi-stat-card" :class="`gi-stat-card--${tone}`">
    <div class="gi-stat-card__title">{{ title }}</div>
    <div class="gi-stat-card__value">{{ displayValue }}</div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  title: {
    type: String,
    default: '',
  },
  value: {
    type: [Number, String],
    default: 0,
  },
  tone: {
    type: String,
    validator: (v) => ['neutral', 'success', 'warning', 'danger'].includes(v),
    default: 'neutral',
  },
})

const displayValue = computed(() => {
  const raw = props.value
  if (raw === null || raw === undefined || raw === '') return '0'
  return raw
})
</script>

<style scoped>
.gi-stat-card {
  padding: 14px;
  border-radius: 12px;
  background: #f5f8fb;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.gi-stat-card__title {
  font-size: 12px;
  color: #708390;
  font-weight: 500;
}

.gi-stat-card__value {
  font-size: 20px;
  font-weight: 700;
  color: #14313d;
  line-height: 1.2;
}

.gi-stat-card--success {
  background: rgba(82, 196, 26, 0.08);
}
.gi-stat-card--success .gi-stat-card__value {
  color: #389e0d;
}

.gi-stat-card--warning {
  background: rgba(250, 173, 20, 0.1);
}
.gi-stat-card--warning .gi-stat-card__value {
  color: #d48806;
}

.gi-stat-card--danger {
  background: rgba(255, 77, 79, 0.08);
}
.gi-stat-card--danger .gi-stat-card__value {
  color: #cf1322;
}
</style>
