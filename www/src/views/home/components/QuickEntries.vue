<template>
  <div class="quick-grid">
    <div
      v-for="action in visibleActions"
      :key="action.key"
      class="quick-card gi-chip-host"
      :title="action.title"
      @click="go(action.path)"
    >
      <span class="gi-chip gi-chip--lg" :class="`gi-chip--${action.chip}`">
        <component :is="action.icon" />
      </span>
      <div class="quick-title">{{ action.title }}</div>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useUserStore } from '@/store/user'
import { quickActions } from './homeEntries'

const router = useRouter()
const userStore = useUserStore()

// 快捷入口与侧栏菜单同口径：可见性只由功能权限决定，与数据授权无关。
// 此前六个入口无条件展示，没有对应功能权限的用户点进去直接 403。
const visibleActions = computed(() =>
  quickActions.filter((action) => !action.permission || userStore.hasPermission(action.permission)),
)

const go = (path) => router.push({ path })
</script>

<style scoped>
.quick-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: clamp(6px, 0.6vw, 10px);
}

/* 卡片整体居中：图标与文字都在中轴上。
   高度与内边距跟视口联动（原固定 104px → 现在 76~90px），
   首页要在一屏内放完工具栏 + 概览 + 本区块，窄屏必须自动收，否则首屏要滚。 */
.quick-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: clamp(76px, 6.1vw, 90px);
  padding: clamp(9px, 0.8vw, 13px) 8px;
  border-radius: 16px;
  background: linear-gradient(180deg, #f7fbfc 0%, #eef5f6 100%);
  border: 1px solid rgba(25, 72, 89, 0.06);
  cursor: pointer;
  transition: transform 0.18s ease, box-shadow 0.18s ease;
}

.quick-card:hover {
  transform: translateY(-3px);
  box-shadow: 0 14px 26px rgba(17, 55, 69, 0.1);
}

.quick-title {
  margin-top: 10px;
  font-size: 13px;
  font-weight: 600;
  line-height: 1.3;
  text-align: center;
  color: #15303c;
}

@media (max-width: 1199px) {
  .quick-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (max-width: 767px) {
  .quick-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
