<script setup>
// 监控告警模块统一的页面顶部标题条：5 个二级页面（监控大屏/监控明细/
// 告警中心/告警配置/监控配置）复用，消除各自的标题字体与内边距差异。
// 布局复用全局 .gi-page-toolbar（flex wrap + 两端对齐），
// 标题字号/字重/颜色统一走 tokens.scss 的全局 token。
defineProps({
  title: { type: String, required: true },
})
</script>

<template>
  <header class="gi-page-toolbar page-header">
    <div class="page-header-left">
      <h1 class="page-header-title">{{ title }}</h1>
      <!-- left：标题右侧的附加控件（如大屏的时间区间/业务线/机房筛选） -->
      <slot name="left" />
    </div>
    <!-- right：页面右侧操作区（刷新/全屏/状态徽标等） -->
    <div class="page-header-right">
      <slot name="right" />
    </div>
  </header>
</template>

<style scoped>
/* 常驻表头：不参与 flex 剩余空间分配，也不会被内容挤压缩水 */
.page-header {
  flex: none;
}

.page-header-left {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px var(--gi-spacing-sm, 16px);
  min-width: 0;
}

.page-header-right {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.page-header-title {
  margin: 0;
  padding: 0;
  font-size: var(--gi-font-size-title, 20px);
  font-weight: 600;
  letter-spacing: 0.2px;
  line-height: 1.3;
  color: var(--gi-color-text-primary, rgba(15, 23, 32, 0.9));
}
</style>
