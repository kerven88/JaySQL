<template>
  <div class="subject-picker">
    <div class="picker-head">
      <a-input-search v-model:value="keyword" placeholder="搜索姓名 / 邮箱 / 用户组" allow-clear class="picker-search" />
      <span class="picker-hint">
        已选 <b>{{ value.length }}</b> 项 ·
        <span :class="{ 'is-warn': addressGap > 0 }">可达地址 {{ addressCount }} 个{{ addressGap > 0 ? `（${addressGap} 人缺地址）` : '' }}</span>
      </span>
    </div>

    <a-tabs v-model:activeKey="tab" size="small" class="picker-tabs">
      <a-tab-pane key="user" tab="用户">
        <div class="picker-list">
          <div
            v-for="user in filteredUsers"
            :key="user.uid"
            class="picker-item"
            :class="{ 'is-picked': isPicked('user', String(user.uid)) }"
            @click="toggle('user', String(user.uid), user.username)"
          >
            <span class="item-name">{{ user.username }}</span>
            <span class="item-meta">
              {{ user.has_email ? user.email_masked : '无邮箱' }}
              <em v-if="user.mobile">· {{ user.phone_masked }}</em>
            </span>
            <a-tag v-if="!user.has_email && !user.mobile" color="orange">地址缺失</a-tag>
          </div>
          <div v-if="!filteredUsers.length" class="picker-empty">没有匹配的用户</div>
        </div>
      </a-tab-pane>

      <a-tab-pane key="org" tab="用户组">
        <div class="picker-list">
          <div
            v-for="org in filteredOrgs"
            :key="org.key"
            class="picker-item"
            :class="{ 'is-picked': isPicked('org', org.key) }"
            @click="toggle('org', org.key, org.name)"
          >
            <span class="item-name" :style="{ paddingLeft: `${(org.level - 1) * 12}px` }">{{ org.name }}</span>
            <span class="item-meta">{{ org.members }} 人（含子节点）</span>
          </div>
          <div v-if="!filteredOrgs.length" class="picker-empty">没有匹配的用户组</div>
        </div>
      </a-tab-pane>

      <a-tab-pane key="raw" tab="外部地址">
        <div class="picker-raw">
          <a-input-group compact>
            <a-input v-model:value="rawInput" placeholder="输入外部邮箱，如 oncall@vendor.com" class="raw-input" @press-enter="addRaw" />
            <a-button @click="addRaw">添加</a-button>
          </a-input-group>
          <div class="picker-hint">用于不在平台内的接受人（如外包值班邮箱）。</div>
        </div>
      </a-tab-pane>
    </a-tabs>

    <div class="picker-selected">
      <a-tag
        v-for="(item, index) in value"
        :key="`${item.type}-${item.id}`"
        :color="tagColor(item.type)"
        closable
        @close="removeAt(index)"
      >
        {{ labelOf(item) }}
      </a-tag>
      <span v-if="!value.length" class="picker-empty inline">尚未选择接受人</span>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { getNotifySubjectTreeApi } from '@/api/monitor'

const props = defineProps({
  value: { type: Array, default: () => [] },
})
const emit = defineEmits(['update:value'])

const tab = ref('user')
const keyword = ref('')
const rawInput = ref('')
const users = ref([])
const orgs = ref([])

const loadTree = async () => {
  try {
    // 请求层 resolve 的是响应体 { code, data, message }，载荷在 .data 这一层
    const res = await getNotifySubjectTreeApi()
    users.value = res?.data?.users || []
    orgs.value = res?.data?.orgs || []
  } catch {
    // 错误提示由请求层统一弹出；此处保持空列表，不阻断策略编辑
  }
}
loadTree()

const filteredUsers = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return users.value
  return users.value.filter((item) => `${item.username} ${item.email} ${item.mobile}`.toLowerCase().includes(kw))
})

const filteredOrgs = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return orgs.value
  return orgs.value.filter((item) => `${item.name} ${item.key}`.toLowerCase().includes(kw))
})

const isPicked = (type, id) => props.value.some((item) => item.type === type && item.id === id)

const toggle = (type, id, note) => {
  if (isPicked(type, id)) {
    emit('update:value', props.value.filter((item) => !(item.type === type && item.id === id)))
    return
  }
  emit('update:value', [...props.value, { type, id, note }])
}

const addRaw = () => {
  const address = rawInput.value.trim()
  if (!address || !address.includes('@')) return
  if (isPicked('raw', address)) {
    rawInput.value = ''
    return
  }
  emit('update:value', [...props.value, { type: 'raw', id: address, note: address }])
  rawInput.value = ''
}

const removeAt = (index) => {
  const next = [...props.value]
  next.splice(index, 1)
  emit('update:value', next)
}

const tagColor = (type) => ({ user: 'blue', org: 'green', notify_group: 'purple', raw: 'default' }[type] || 'default')

const labelOf = (item) => {
  if (item.type === 'org') {
    const org = orgs.value.find((row) => row.key === item.id)
    if (org) return `${org.name}（${org.members} 人）`
  }
  if (item.type === 'notify_group') return `通知组 #${item.id}`
  return item.note || item.id
}

// 覆盖率提示：配置者能一眼看到"选的人里有多少能真正收到"，
// 避免配完之后才发现有人没有邮箱/手机号，通知静默丢失。
const addressCount = computed(() => {
  const addresses = new Set()
  props.value.forEach((item) => {
    if (item.type === 'user') {
      const user = users.value.find((row) => String(row.uid) === item.id)
      if (user?.email) addresses.add(user.email)
      if (user?.mobile) addresses.add(user.mobile)
    }
    if (item.type === 'raw') addresses.add(item.id)
  })
  return addresses.size
})

const addressGap = computed(() => {
  let gap = 0
  props.value.forEach((item) => {
    if (item.type !== 'user') return
    const user = users.value.find((row) => String(row.uid) === item.id)
    if (!user || (!user.has_email && !user.has_mobile)) gap += 1
  })
  return gap
})
</script>

<style scoped>
.subject-picker {
  border: 1px solid var(--gi-border-color, #e5e7eb);
  border-radius: 8px;
  padding: 12px;
}

.picker-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.picker-search {
  max-width: 280px;
}

.picker-hint {
  font-size: 12px;
  color: var(--gi-text-tertiary, #8c8c8c);
}

.picker-hint .is-warn {
  color: #d97706;
}

.picker-tabs {
  margin-top: 8px;
}

.picker-list {
  max-height: 220px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.picker-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border-radius: 6px;
  cursor: pointer;
}

.picker-item:hover {
  background: var(--gi-bg-hover, #f5f5f5);
}

.picker-item.is-picked {
  background: var(--gi-color-primary-light, #e6f4ff);
}

.item-name {
  font-size: 13px;
}

.item-meta {
  font-size: 12px;
  color: var(--gi-text-tertiary, #8c8c8c);
}

.picker-raw {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.raw-input {
  width: 320px;
}

.picker-selected {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px dashed var(--gi-border-color, #e5e7eb);
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.picker-empty {
  font-size: 12px;
  color: var(--gi-text-tertiary, #8c8c8c);
  padding: 8px 0;
}

.picker-empty.inline {
  padding: 0;
}
</style>
