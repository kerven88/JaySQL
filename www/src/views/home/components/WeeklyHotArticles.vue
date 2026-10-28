<template>
  <a-card class="home-panel-card hot-card" :bordered="false">
    <template #title>
      <span class="panel-title"><FireOutlined class="panel-title-icon" />相关热文</span>
    </template>

    <ul v-if="visibleArticles.length" class="home-panel-list">
      <li
        v-for="(article, index) in visibleArticles"
        :key="article.key"
        class="home-panel-item hot-item"
        :class="{ 'is-static': !article.url }"
        :title="`${article.title} · ${article.source}`"
        @click="openArticle(article)"
      >
        <span class="home-panel-rank" :class="{ 'is-top': index < 3 }">{{ index + 1 }}</span>
        <span class="hot-title">{{ article.title }}</span>
        <span class="hot-tag">{{ article.tag }}</span>
        <span class="hot-heat"><EyeOutlined />{{ article.heat }}</span>
        <RightOutlined v-if="article.url" class="hot-arrow" />
      </li>
    </ul>
    <div v-else class="home-panel-empty">暂无热文</div>
  </a-card>
</template>

<script setup>
import { EyeOutlined, FireOutlined, RightOutlined } from '@ant-design/icons-vue'
import { computed, onMounted, ref } from 'vue'
import { getHomeHotArticlesApi } from '@/api/hotarticle'
import { weeklyHotArticles } from './homeEntries'

// 相关热文由管理员在【系统管理 → 系统配置 → 热文管理】维护，
// 接口只返回「已启用 + 排序前 3」的条目，这里不再做截断。
//
// 两条边界：
//  1. 接口正常返回空数组 = 管理员主动清空了配置，如实显示空态（不拿静态内容兜底，
//     否则管理员「全部停用」会看起来没生效）；
//  2. 接口异常（后端未就绪 / 网络抖动）= 回退到内置静态内容 —— 首页是登录后第一屏，
//     任何后端抖动都不该让它空着。
const remoteArticles = ref(null)

// 后端返回字段映射为组件既有结构；key 用 id（静态兜底数据用字符串 key，两者不冲突）
const normalize = (item) => ({
  key: `hot-${item.id}`,
  title: item.title,
  source: item.source || '',
  tag: item.tag || '',
  heat: formatHeat(item.heat),
  url: item.url || '',
})

// 热度：≥1000 显示为 1.2k，与静态兜底数据的展示口径保持一致
const formatHeat = (value) => {
  const num = Number(value || 0)
  if (num >= 1000) return `${(num / 1000).toFixed(1)}k`
  return String(num)
}

const visibleArticles = computed(() => {
  if (remoteArticles.value === null) {
    // 固定展示前 3 条；数据不足 3 条时按实际条数渲染，不补空白占位
    return weeklyHotArticles.slice(0, 3)
  }
  return remoteArticles.value
})

const loadArticles = async () => {
  const res = await getHomeHotArticlesApi().catch(() => null)
  if (!res || !Array.isArray(res?.data?.articles)) return // 失败：保持静态兜底
  remoteArticles.value = res.data.articles.slice(0, 3).map(normalize)
}

// url 为空的条目是还没配外链的占位：不跳空白页，整行渲染成不可点，补上链接即生效
const openArticle = (article) => {
  if (!article.url) return
  window.open(article.url, '_blank', 'noopener,noreferrer')
}

onMounted(loadArticles)
</script>

<style>
@import './homeCards.css';
</style>

<style scoped>
.hot-card {
  display: flex;
  flex-direction: column;
}

.hot-card :deep(.ant-card-body) {
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
  color: #f08a0b;
  font-size: 15px;
}

/* 单行条目：标题省略而不是折行，半截的字比省略号难看得多。完整标题与来源走 title 提示。 */
.hot-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 13px;
  line-height: 20px;
  color: #29485a;
}

.home-panel-item:hover .hot-title {
  color: #0f7f6d;
}

.hot-tag {
  flex: none;
  padding: 1px 6px;
  border-radius: 6px;
  background: rgba(240, 138, 11, 0.1);
  font-size: 11px;
  color: #b2650a;
}

.hot-heat {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 11px;
  color: #93a5b1;
}

.hot-arrow {
  flex: none;
  font-size: 11px;
  color: #b7c6d0;
}
</style>
