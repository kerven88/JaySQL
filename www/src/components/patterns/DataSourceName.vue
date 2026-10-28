<template>
  <AppTooltip :title="fullName" :placement="placement" overlay-class-name="gi-data-source-name-tooltip">
    <span class="gi-data-source-name">{{ displayName }}</span>
  </AppTooltip>
</template>

<script setup>
import { computed } from 'vue'
import AppTooltip from './AppTooltip.vue'
import { MAX_DATASOURCE_NAME_LENGTH, truncateByRune } from '@/utils/schemaDisplay'

const props = defineProps({
  name: {
    type: [String, Number],
    default: '',
  },
  max: {
    type: Number,
    default: MAX_DATASOURCE_NAME_LENGTH,
  },
  placement: {
    type: String,
    default: 'topLeft',
  },
})

const fullName = computed(() => String(props.name == null ? '' : props.name))
const displayName = computed(() => {
  if (!fullName.value) return '-'
  return truncateByRune(fullName.value, props.max)
})
</script>

<style lang="scss" scoped>
.gi-data-source-name {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  line-height: 1.5;
  word-break: break-all;
  vertical-align: middle;
  color: #748893;
  font-size: 12px;
}
</style>
