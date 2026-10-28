<template>
  <span class="db-logo-wrap" :class="{ 'db-logo-wrap--plain': !showText }">
    <a-tooltip :title="tooltipText">
      <span class="db-logo-box">
        <img
          v-if="logoSrc"
          class="db-logo"
          :src="logoSrc"
          :alt="dbType || 'db'"
          loading="lazy"
          @error="handleImageError"
        />
        <span v-else class="db-logo-fallback" :style="fallbackStyle">{{ fallbackInitial }}</span>
      </span>
    </a-tooltip>
    <span v-if="showText" class="db-logo-text">{{ displayText }}</span>
  </span>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { dbTypeLogo, hasDbTypeLogo } from '@/utils/dbTypeLogo'
import { normalizeDbType } from '@/utils/dbFamily'

/**
 * 数据库类型品牌图标
 *
 * 设计口径（与全站 dbTypeLogo 一致）：
 *   - 图标固定 20×20，object-fit: contain —— 保证不同长宽比的 logo 都不拉伸变形；
 *   - 容器用 inline-flex + vertical-align: middle，与同行文字基线居中对齐；
 *   - 资源缺失 / 加载失败时回落到「首字母方块」占位，而不是留白或碎图，
 *     占位块用引擎族配色，仍能一眼区分 MySQL / PostgreSQL / Oracle。
 */
const props = defineProps({
  /** 原始 db_type，如 MySQL / PostgreSQL / Oracle */
  dbType: { type: String, default: '' },
  /** 实例画像采集到的版本串，如 8.0.43 */
  version: { type: String, default: '' },
  /** 是否展示类型文字（表格列需要，图标列不需要） */
  showText: { type: Boolean, default: true },
})

const imageFailed = ref(false)
const normalized = computed(() => normalizeDbType(props.dbType))

const logoSrc = computed(() => {
  if (imageFailed.value) return ''
  if (!props.dbType) return ''
  if (!hasDbTypeLogo(props.dbType)) return ''
  return dbTypeLogo(props.dbType)
})

// 换实例时重置失败状态，避免上一行的失败标记残留
watch(
  () => props.dbType,
  () => {
    imageFailed.value = false
  }
)

const handleImageError = () => {
  imageFailed.value = true
}

/** 各家族的占位配色：与产品主色同系，避免引入第四种色相 */
const FALLBACK_COLOR = {
  mysql: '#0f766e',
  postgresql: '#1677ff',
  oracle: '#d4380d',
}

const fallbackStyle = computed(() => ({
  background: `${FALLBACK_COLOR[normalized.value] || '#4a5c68'}1a`,
  color: FALLBACK_COLOR[normalized.value] || '#4a5c68',
}))

const fallbackInitial = computed(() => {
  const text = String(props.dbType || '').trim()
  return text ? text.charAt(0).toUpperCase() : '?'
})

const displayText = computed(() => {
  const type = String(props.dbType || '').trim()
  const version = String(props.version || '').trim()
  const text = `${type} ${version}`.trim()
  return text || '—'
})

const tooltipText = computed(() => {
  const type = String(props.dbType || '').trim()
  const version = String(props.version || '').trim()
  if (!type) return '未知数据库类型'
  return version ? `${type} ${version}` : type
})
</script>

<style scoped>
.db-logo-wrap {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
}

/* 固定 20px 的方形底座：所有 logo 都先落在这个盒子里，
   再由 img 的 object-fit 自适应，避免长短边不一导致的视觉参差 */
.db-logo-box {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  flex: 0 0 20px;
}

.db-logo {
  width: 20px;
  height: 20px;
  object-fit: contain;
  display: block;
}

.db-logo-fallback {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 5px;
  font-size: 11px;
  font-weight: 600;
  line-height: 1;
  user-select: none;
}

.db-logo-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
