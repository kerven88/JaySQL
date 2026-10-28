<template>
  <a-card title="我的资源" class="home-panel-card resource-card" :bordered="false">
    <div class="resource-grid">
      <div
        v-for="card in cards"
        :key="card.key"
        class="resource-item"
        :title="cardTitle(card)"
        @click="go(card)"
      >
        <div class="resource-head">
          <span class="resource-name">{{ card.title }}</span>
        </div>
        <div class="resource-stats">
          <div v-for="stat in card.stats" :key="stat.key" class="resource-stat">
            <div class="resource-value">{{ stat.value }}</div>
            <div class="resource-label">{{ stat.label }}</div>
          </div>
        </div>
      </div>
    </div>
  </a-card>
</template>

<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useUserStore } from '@/store/user'
import { resourceCards } from './homeEntries'

const props = defineProps({
  // 来自 /home/dashboard 的 resources 字段；缺失（接口未返回/被降级）时全部显示 0
  resources: {
    type: Object,
    default: () => ({}),
  },
  // 首页顶部选中的环境（空 = 全部环境）。卡片数字受它影响，支持环境筛选的列表页要一并带上
  environment: {
    type: [Number, String],
    default: undefined,
  },
})

const router = useRouter()
const userStore = useUserStore()

// 接口给 null / 负数 / 非数字时统一兜成 0，卡片上永远是可读的数字
const toCount = (value) => {
  const n = Number(value)
  return Number.isFinite(n) && n > 0 ? n : 0
}

const cards = computed(() =>
  resourceCards.map((card) => {
    const source = props.resources?.[card.key] || {}
    return {
      ...card,
      stats: card.stats.map((stat) => ({ ...stat, value: toCount(source[stat.key]) })),
    }
  }),
)

// 卡片数字的口径与「点进去能看到的那份清单」严格一致，所以跳转目标随权限二选一：
// 有实例配置/监控页权限 → 该页（对所有人都是全量）；否则 → SQL 工作台（只有授权数据源）
const canAny = (codes) =>
  (codes || []).some((code) => userStore.hasPermission?.(code) ?? userStore.is_superuser)

const resolveTarget = (card) => {
  if (card.pathByPermission) {
    const { codes, path, fallbackPath } = card.pathByPermission
    return canAny(codes) ? path : fallbackPath || path
  }
  return card.path
}

const cardTitle = (card) => `${card.title} · ${card.scopeHint} · 点击查看`

const go = (card) => {
  const target = resolveTarget(card)
  if (!target) return
  const query = { ...(card.query || {}) }
  // 卡片数字受首页环境筛选影响，只有支持环境筛选的列表页能复现这个口径
  if (card.supportsEnvironment && props.environment !== undefined && props.environment !== null && props.environment !== '') {
    query.environment = String(props.environment)
  }
  router.push({ path: target, query })
}
</script>

<style>
@import './homeCards.css';
</style>

<style scoped>
.resource-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: clamp(8px, 0.8vw, 12px);
}

/* 内层卡片：图标 + 名称一行，下面三列数值/标签，与参考图结构一致 */
.resource-item {
  min-width: 0;
  padding: clamp(9px, 0.85vw, 13px) clamp(9px, 0.85vw, 13px);
  border-radius: 14px;
  background: linear-gradient(180deg, #ffffff 0%, #f8fbfc 100%);
  border: 1px solid rgba(21, 62, 77, 0.08);
  cursor: pointer;
  transition: transform 0.18s ease, box-shadow 0.18s ease, border-color 0.18s ease;
}

.resource-item:hover {
  transform: translateY(-2px);
  border-color: rgba(22, 127, 111, 0.18);
  box-shadow: 0 12px 24px rgba(17, 55, 69, 0.08);
}

/* 卡片不带图标：标题是唯一的识别元素，字号放大一档顶上原来图标的位置，
   整张卡不会因为少了 32px 的方块而显得头重脚轻 */
.resource-head {
  min-width: 0;
}

.resource-name {
  display: block;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.25;
  color: #15303c;
}

/* auto-fit：数据源/备份任务 3 列、SQL工单 4 列（多一个「审批驳回」），
   写死 3 列会把第四列挤到第二行，卡片高度就对不齐了 */
.resource-stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(0, 1fr));
  gap: 6px;
  margin-top: clamp(7px, 0.7vw, 11px);
}

.resource-stat {
  min-width: 0;
}

/* 数量一律淡灰：不再用绿/红区分好坏，避免三个数字抢走卡片名称的视觉重心 */
.resource-value {
  font-size: clamp(17px, 1.35vw, 21px);
  font-weight: 700;
  line-height: 1.12;
  font-variant-numeric: tabular-nums;
  color: #8c9ea9;
}

.resource-label {
  margin-top: 3px;
  font-size: 11px;
  color: #93a5b1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 767px) {
  .resource-grid {
    grid-template-columns: 1fr;
  }
}
</style>
