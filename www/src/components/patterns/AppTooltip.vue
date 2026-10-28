<template>
  <a-tooltip
    :placement="placement"
    :mouse-enter-delay="mouseEnterDelay"
    :mouse-leave-delay="mouseLeaveDelay"
    :overlay-class-name="mergedOverlayClassName"
    :destroy-tooltip-on-hide="destroyOnHide"
    transition-name="ant-fade"
  >
    <template #title>
      <div class="gi-app-tooltip__content">
        <slot name="title">
          {{ title }}
        </slot>
      </div>
    </template>
    <slot />
  </a-tooltip>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  title: {
    type: String,
    default: '',
  },
  placement: {
    type: String,
    default: 'top',
  },
  mouseEnterDelay: {
    type: Number,
    default: 0.2,
  },
  mouseLeaveDelay: {
    type: Number,
    default: 0.12,
  },
  overlayClassName: {
    type: String,
    default: '',
  },
  destroyOnHide: {
    type: Boolean,
    default: true,
  },
})

const mergedOverlayClassName = computed(() => ['gi-app-tooltip', props.overlayClassName].filter(Boolean).join(' '))
</script>
