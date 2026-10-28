<template>
  <a-config-provider :theme="antdTheme" :locale="zhCN">
    <div id="app" class="gi-app">
      <router-view />
    </div>
  </a-config-provider>
</template>

<script setup>
import zhCN from 'ant-design-vue/es/locale/zh_CN'
import { antdTheme } from '@/design/antdTheme'
</script>

<style>
html,
body {
  margin: 0;
  padding: 0;
  height: 100%;
  width: 100%;
  overflow: hidden;
  overscroll-behavior: none;
  -webkit-overflow-scrolling: touch;
}
</style>

<style scoped>
.gi-app {
  height: 100vh;
  min-height: 100vh;
  background: var(--gi-color-page-bg);
  overflow: hidden;
  overscroll-behavior: none;
}
</style>
