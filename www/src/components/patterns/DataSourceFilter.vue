<template>
  <!-- 数据源筛选下拉：默认「全部」（值为 0），选中非 0 时按实例过滤列表。
       展开最多直接显示 3 个选项，超出由下拉内部滚动条查看；
       支持按「实例名称」与「IP:端口」模糊搜索（多个关键词空格分隔，全部命中才算匹配）。 -->
  <a-select
    v-model:value="innerValue"
    class="gi-data-source-filter"
    :class="{ 'is-all': !innerValue }"
    :style="{ width: `${width}px` }"
    :loading="loading"
    :options="selectOptions"
    :list-height="LIST_HEIGHT"
    :dropdown-style="{ minWidth: `${Math.max(width, DROPDOWN_MIN_WIDTH)}px` }"
    :popup-class-name="DROPDOWN_CLASS"
    show-search
    :filter-option="filterOption"
    @change="handleChange"
  >
    <template #option="{ isAll, label, address, dbType, fullLabel }">
      <div class="gi-data-source-filter__option">
        <img
          v-if="!isAll"
          class="gi-data-source-filter__logo"
          :src="dbTypeLogo(dbType)"
          :alt="dbType || 'db'"
        />
        <span class="gi-data-source-filter__name" :title="fullLabel || label">{{ label }}</span>
        <span v-if="address" class="gi-data-source-filter__addr" :title="address">{{ address }}</span>
      </div>
    </template>
  </a-select>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { dbTypeLogo } from '@/utils/dbTypeLogo'

defineOptions({ name: 'DataSourceFilter' })

const props = defineProps({
  // 0 表示「全部」；其余为 insight_instances.id
  value: {
    type: [Number, String],
    default: 0,
  },
  // 原始实例列表，字段与 /api/v1/backup/instances 返回一致：
  // { id, hostname, port, db_type, remark }
  instances: {
    type: Array,
    default: () => [],
  },
  loading: {
    type: Boolean,
    default: false,
  },
  width: {
    type: Number,
    default: 200,
  },
  allLabel: {
    type: String,
    default: '全部',
  },
})

const emit = defineEmits(['update:value', 'change'])

const DROPDOWN_CLASS = 'gi-data-source-filter-dropdown'
// 单个选项高 32px（antd controlHeight），96px 即「最多直接显示 3 个选项」
const LIST_HEIGHT = 96
// 下拉面板最小宽度：保证「实例名 + IP:端口」单行尽量放得下（放不下时 CSS 省略号 + title 兜底）
const DROPDOWN_MIN_WIDTH = 300
const ALL_VALUE = 0

const innerValue = ref(props.value ?? ALL_VALUE)

watch(
  () => props.value,
  (next) => {
    innerValue.value = next ?? ALL_VALUE
  },
)

// 名称/IP 完整信息由 title 悬停兜底，宽度不足时交给 CSS 省略号
const selectOptions = computed(() => {
  const list = (props.instances || [])
    .map((item) => {
      const id = Number(item?.id) || 0
      if (!id) return null
      const remark = String(item?.remark || '').trim()
      const host = String(item?.hostname || '').trim()
      const port = String(item?.port || '').trim()
      const address = host ? (port ? `${host}:${port}` : host) : ''
      const dbType = String(item?.db_type || '').trim()
      // 无备注名时退回展示地址，避免出现空行；此时地址只展示一次
      const name = remark || address || `实例 #${id}`
      const showAddress = remark ? address : ''
      return {
        value: id,
        isAll: false,
        label: name,
        fullLabel: remark && address ? `${remark}（${address}）` : name,
        address: showAddress,
        dbType,
        searchText: [name, remark, host, port, address, dbType, `#${id}`].join(' ').toLowerCase(),
      }
    })
    .filter(Boolean)

  return [
    { value: ALL_VALUE, isAll: true, label: props.allLabel, fullLabel: props.allLabel, address: '', searchText: '' },
    ...list,
  ]
})

// 模糊搜索：多关键词（空格分隔）需全部命中名称/IP/端口/类型
const filterOption = (input, option) => {
  if (option?.isAll) {
    return String(props.allLabel).toLowerCase().includes(String(input || '').trim().toLowerCase())
  }
  const keywords = String(input || '')
    .trim()
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
  if (!keywords.length) return true
  const haystack = String(option?.searchText || '')
  return keywords.every((keyword) => haystack.includes(keyword))
}

const handleChange = (value) => {
  const next = value ?? ALL_VALUE
  innerValue.value = next
  emit('update:value', next)
  emit('change', next)
}
</script>

<style lang="scss" scoped>
/* 与页面 toolbar 内其它组件同高（antd 默认 32px）、同圆角/边框，不额外覆写颜色 */
.gi-data-source-filter {
  flex: 0 0 auto;
  min-width: 0;
}

.gi-data-source-filter__option {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.gi-data-source-filter__logo {
  /* 全站统一：数据库类型 logo 一律 20px */
  width: 20px;
  height: 20px;
  flex: none;
  object-fit: contain;
}

.gi-data-source-filter__name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  color: #1f2d38;
  font-size: 14px;
}

.gi-data-source-filter__addr {
  flex: none;
  margin-left: auto;
  color: #8a97a1;
  font-size: 12px;
}
</style>

<style lang="scss">
/* 下拉面板在 body 下渲染，无法用 scoped 命中，这里按 dropdown 类名收敛作用域。
   仅做细滚动条，其余沿用 antd 主题（高度、圆角、配色不覆写）。 */
.gi-data-source-filter-dropdown .rc-virtual-list-holder {
  scrollbar-width: thin;
}

.gi-data-source-filter-dropdown .rc-virtual-list-holder::-webkit-scrollbar {
  width: 6px;
}

.gi-data-source-filter-dropdown .rc-virtual-list-holder::-webkit-scrollbar-thumb {
  background: rgba(15, 23, 32, 0.16);
  border-radius: 3px;
}
</style>
