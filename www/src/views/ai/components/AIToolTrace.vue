<template>
  <div class="ai-tool-trace" :class="{ 'is-open': open }">
    <div class="trace-head" @click="open = !open">
      <ThunderboltOutlined class="trace-head-icon" />
      <span>已调用 {{ steps.length }} 个工具</span>
      <span class="trace-total">共 {{ totalMs }} ms</span>
      <DownOutlined class="trace-caret" />
    </div>
    <div class="trace-body">
      <div v-for="(step, idx) in steps" :key="`${step.tool}_${idx}`" class="trace-row">
        <span class="trace-tool">{{ step.tool }}</span>
        <span class="trace-arg">{{ step.arg }}</span>
        <span class="trace-ms">{{ step.ms }}</span>
        <CheckOutlined class="trace-ok" />
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { CheckOutlined, DownOutlined, ThunderboltOutlined } from '@ant-design/icons-vue'

const props = defineProps({
  steps: {
    type: Array,
    default: () => [],
  },
  // 由右栏「显示工具调用明细」策略控制默认展开
  defaultOpen: {
    type: Boolean,
    default: false,
  },
})

const open = ref(props.defaultOpen)

watch(
  () => props.defaultOpen,
  (val) => {
    open.value = Boolean(val)
  },
)

const totalMs = computed(() =>
  props.steps.reduce((sum, step) => sum + (parseInt(step?.ms, 10) || 0), 0),
)
</script>

<style scoped>
.ai-tool-trace {
  margin-top: 8px;
  border: 1px solid rgba(27, 73, 88, 0.08);
  border-radius: 12px;
  background: linear-gradient(180deg, #fbfdfe 0%, #f6fafb 100%);
  overflow: hidden;
}

.trace-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  font-size: 12.5px;
  color: rgba(36, 57, 71, 0.72);
  cursor: pointer;
  user-select: none;
}

.trace-head:hover {
  background: rgba(15, 118, 110, 0.04);
}

.trace-head-icon {
  color: #0f766e;
  font-size: 13px;
}

.trace-total {
  color: #7b8f9c;
  font-size: 11.5px;
}

.trace-caret {
  margin-left: auto;
  font-size: 12px;
  color: #7b8f9c;
  transition: transform 0.16s ease;
}

.ai-tool-trace.is-open .trace-caret {
  transform: rotate(180deg);
}

.trace-body {
  display: none;
  padding: 2px 12px 10px;
}

.ai-tool-trace.is-open .trace-body {
  display: block;
}

.trace-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 6px 0;
  font-size: 12px;
  line-height: 1.6;
  border-top: 1px dashed rgba(27, 73, 88, 0.1);
}

.trace-row:first-child {
  border-top: none;
}

.trace-tool {
  flex: 0 0 auto;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11.5px;
  color: #0b5f58;
}

.trace-arg {
  flex: 1;
  min-width: 0;
  color: rgba(36, 57, 71, 0.72);
  word-break: break-all;
}

.trace-ms {
  flex: 0 0 auto;
  color: #7b8f9c;
  font-size: 11.5px;
}

.trace-ok {
  flex: 0 0 auto;
  color: #0f766e;
  font-size: 12px;
}
</style>
