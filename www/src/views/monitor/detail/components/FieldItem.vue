<template>
  <div class="ov-item">
    <dt>{{ label }}</dt>
    <dd v-if="resolved.available" :title="resolved.value + (resolved.sub ? ' ' + resolved.sub : '')">
      {{ resolved.value }}
      <span v-if="resolved.sub" class="sub">{{ resolved.sub }}</span>
      <span v-if="resolved.unit" class="sub">{{ resolved.unit }}</span>
    </dd>
    <dd v-else class="is-empty" :title="resolved.reason || '暂未采集到该项'">—</dd>
  </div>
</template>

<script setup>
import { computed } from 'vue'

// 字段值来源：
//  - value：纯字符串（后端保证一定有值），直接展示
//  - field：ProfileValue（可能取不到），取不到时显示 "—" 并把 reason 挂到 title
// 这样"没采到"和"值是 0"在界面上是两件不同的事，绝不混淆。
const props = defineProps({
  label: { type: String, required: true },
  value: { type: String, default: '' },
  field: { type: Object, default: null },
  // 原 metric 开关用于放大数值类字段（15px / 600）。三个区块统一排版后已废弃，
  // 指标值一律走 .ov-item dd 的统一样式，避免同一行里字号字重不一致。
})

const resolved = computed(() => {
  if (props.field) {
    return {
      available: !!props.field.available,
      value: props.field.value || '',
      sub: props.field.sub || '',
      unit: props.field.unit || '',
      reason: props.field.reason || '',
    }
  }
  return {
    available: !!props.value,
    value: props.value || '',
    sub: '',
    unit: '',
    reason: '暂未采集到该项',
  }
})
</script>

<style scoped>
/* 一套排版走天下：基本信息 / 架构 两个区块共用同一份
   字号、字重、行高、颜色、内外边距与对齐方式。名称与取值同为浅灰（--gi-text-3）、
   同样不加粗，只靠位置（上/下）区分层级，不在信息密集区做粗细反差 */
.ov-item {
  /* 名称与值都左对齐、单行省略，保证多列之间横向对齐不参差 */
  min-width: 0;
  text-align: left;
}
.ov-item dt {
  margin: 0 0 4px;
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
  color: var(--gi-text-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ov-item dd {
  margin: 0;
  /* 取值不加粗、与标签同为浅灰（--gi-text-3）：整体视觉轻量统一，
     重量感交给区块标题（.ov-row-head），不让字段值在信息密集区抢视线 */
  font-size: 13px;
  font-weight: 400;
  line-height: 20px;
  color: var(--gi-text-3);
  /* 数值类字段等宽对齐，避免刷新时数字跳动 */
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ov-item dd .sub {
  margin-left: 4px;
  font-size: 12px;
  font-weight: 400;
  line-height: 20px;
  color: var(--gi-text-3);
}
.ov-item dd.is-empty {
  color: var(--gi-muted);
  cursor: help;
}
</style>
