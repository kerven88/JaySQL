<template>
  <a-card class="home-panel-card docs-card" :bordered="false">
    <template #title>
      <span class="panel-title"><ReadOutlined class="panel-title-icon" />常用文档</span>
    </template>
    <template #extra>
      <a class="panel-more" @click="go('/docs')">全部文档</a>
    </template>

    <ul v-if="visibleDocs.length" class="home-panel-list">
      <li v-for="(doc, index) in visibleDocs" :key="doc.key" class="home-panel-item" @click="openDoc(doc)">
        <span class="home-panel-rank" :class="{ 'is-top': index < 3 }">{{ index + 1 }}</span>
        <span class="doc-title">{{ doc.title }}</span>
        <span class="doc-group">{{ doc.group }}</span>
        <span class="doc-reading">{{ doc.reading }}</span>
      </li>
    </ul>
    <div v-else class="home-panel-empty">暂无文档</div>
  </a-card>
</template>

<script setup>
import { ReadOutlined } from '@ant-design/icons-vue'
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { commonDocs } from './homeEntries'

const router = useRouter()

// 固定展示前 6 条；数据不足 6 条时按实际条数渲染，不补空白占位
const visibleDocs = computed(() => commonDocs.slice(0, 6))

// 文档中心用 ?doc= 定位到具体一篇（路由内跳转，query 不会被守卫吞掉）
const openDoc = (doc) => router.push({ path: '/docs', query: { doc: doc.slug } })

const go = (path) => router.push({ path })
</script>

<style>
@import './homeCards.css';
</style>

<style scoped>
.docs-card {
  display: flex;
  flex-direction: column;
}

.docs-card :deep(.ant-card-body) {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.panel-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.panel-title-icon {
  color: #18a58f;
  font-size: 15px;
}

.panel-more {
  font-size: 12px;
  color: #18a58f;
}

.panel-more:hover {
  color: #0f7f6d;
}

.doc-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 13px;
  color: #29485a;
}

.home-panel-item:hover .doc-title {
  color: #0f7f6d;
}

.doc-group {
  flex: none;
  padding: 1px 7px;
  border-radius: 6px;
  background: rgba(58, 184, 155, 0.1);
  font-size: 11px;
  color: #147866;
}

.doc-reading {
  flex: none;
  width: 46px;
  text-align: right;
  font-size: 11px;
  color: #93a5b1;
}
</style>
