<template>
  <img :src="logoSrc" :alt="altText" class="db-logo-img" />
</template>

<script setup>
import { computed } from 'vue';
import { dbTypeLogo } from '@/utils/dbTypeLogo';

/**
 * 数据库类型 → 品牌 logo 统一入口。
 * 与 dbTypeLogo.js 共用同一套资源（2026-10-20 起全部为正方形透明底 PNG，
 * 小尺寸下清晰度与位图/矢量无差异，故不再需要 PG 单独走 svg 的特例）。
 */
const props = defineProps({
  type: {
    type: String,
    required: true,
    validator: (value) =>
      ['clickhouse', 'mysql', 'tidb', 'doris', 'postgresql', 'oracle', 'sqlserver'].includes(value),
  },
});

const logoSrc = computed(() => dbTypeLogo(props.type));

const altText = computed(() => String(props.type || '').toUpperCase());
</script>

<style scoped>
.db-logo-img {
  display: inline-block;
  vertical-align: middle;
  object-fit: contain;
  /* 全站统一：数据库类型 logo 一律 20px。
     标签页图标槽位（.workspace-tab-status / .console-tab-status）已同步加宽到 20px */
  width: 20px;
  height: 20px;
}
</style>
