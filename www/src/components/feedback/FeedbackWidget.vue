<!--
  反馈控件容器：右缘悬浮入口 + 反馈弹窗。
  挂在全局 Layout，一次接入全页面生效；提交后由弹窗内部展示成功态。
-->
<template>
  <FeedbackEntry @open="open = true" />
  <FeedbackModal v-model:open="open" />
</template>

<script setup>
import { ref } from 'vue'
import FeedbackEntry from './FeedbackEntry.vue'
import FeedbackModal from './FeedbackModal.vue'

defineOptions({ name: 'FeedbackWidget' })

const open = ref(false)
</script>
