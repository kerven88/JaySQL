<template>
  <section class="redis-browser" aria-label="Redis 只读 Key 浏览器">
    <a-alert type="info" show-icon message="Redis 只读浏览 · SCAN 分页不代表快照，Key 可能重复或在读取前过期" />
    <div class="redis-controls">
      <label>DB <a-input-number v-model:value="db" :min="0" :max="65535" :precision="0" :disabled="scanning || reading" /></label>
      <label class="pattern">匹配模式 <a-input v-model:value="pattern" placeholder="*" @pressEnter="scan(false)" /></label>
      <a-button type="primary" :loading="scanning" :disabled="!validDb" @click="scan(false)">扫描 / 刷新</a-button>
      <a-button :disabled="!scanMore || scanning || !validDb" @click="scan(true)">下一页 Key</a-button>
    </div>
    <div class="redis-status">{{ scanned ? `本页 ${keys.length} 个 Key` : '选择 DB，点击扫描开始浏览' }} · SCAN cursor: {{ scanCursor }}</div>
    <div class="redis-columns">
      <div class="redis-key-list">
        <a-empty v-if="!keys.length" :description="scanMore ? '本页为空，仍可继续扫描下一页' : '暂无 Key'" />
        <button v-for="(item, index) in keys" :key="`${item.key_base64}:${index}`" class="redis-key" :class="{ selected: selected?.key_base64 === item.key_base64 }" @click="selectKey(item)">
          <span class="redis-key-name">{{ item.key === '' ? '(空 Key)' : item.key }}</span>
          <span><a-tag>{{ item.type }}</a-tag>{{ ttl(item.ttl_seconds) }}</span>
        </button>
      </div>
      <div class="redis-detail">
        <a-empty v-if="!selected" description="选择 Key 查看值（只读）" />
        <template v-else>
          <div class="redis-key-name"><strong>{{ selected.key === '' ? '(空 Key)' : selected.key }}</strong></div>
          <div class="redis-status">Key ID（base64）: <code>{{ selected.key_base64 }}</code></div>
          <a-spin :spinning="reading">
            <template v-if="detail">
              <div class="redis-meta"><a-tag>{{ detail.type }}</a-tag>{{ ttl(detail.ttl_seconds) }} · 大小 / 元素数: {{ detail.size }}</div>
              <a-alert v-if="detail.exists === false" type="warning" message="Key 已不存在或已过期" />
              <a-alert v-if="detail.message" type="info" :message="detail.message" />
              <div v-if="detail.value" class="redis-value">
                <a-tag>{{ detail.value.encoding }}</a-tag><a-tag v-if="detail.value.truncated" color="orange">预览已截断</a-tag>
                <pre>{{ detail.value.data }}</pre>
              </div>
              <div v-for="(row, index) in detail.rows || []" :key="index" class="redis-value">
                <div v-if="row.field !== undefined">字段: <code>{{ row.field }}</code><a-tag v-if="row.field_base64">字段 ID（base64）: {{ row.field_base64 }}</a-tag></div>
                <div v-if="row.index !== undefined">索引: {{ row.index }}</div>
                <div v-if="row.score !== undefined">分数: {{ row.score }}</div>
                <div v-if="row.id !== undefined">ID: {{ row.id }}</div>
                <a-tag>{{ row.value?.encoding }}</a-tag><a-tag v-if="row.value?.truncated" color="orange">预览已截断</a-tag>
                <pre>{{ row.value?.data }}</pre>
              </div>
              <a-button :loading="reading" :disabled="!detail.has_more" @click="readValue(true)">下一页值</a-button>
              <div class="redis-status">值 cursor: {{ detail.next_cursor || '0' }} · offset: {{ detail.next_offset || 0 }} · 分页替换当前预览</div>
            </template>
            <a-button v-else :disabled="reading" @click="readValue(false)">读取值</a-button>
          </a-spin>
        </template>
      </div>
    </div>
  </section>
</template>

<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ScanRedisKeysApi, GetRedisValueApi } from '@/api/das'
const props = defineProps({ instanceId: { type: [String, Number], required: true } })
const db = ref(0)
const pattern = ref('*')
const activePattern = ref('*')
const keys = ref([])
const selected = ref(null)
const detail = ref(null)
const scanning = ref(false)
const reading = ref(false)
const scanned = ref(false)
const scanCursor = ref('0')
const scanMore = ref(false)
const validDb = computed(() => Number.isInteger(db.value) && db.value >= 0 && db.value <= 65535)
let scanVersion = 0
let valueVersion = 0
const ttl = (value) => value === -1 ? '无过期' : value === -2 ? '已不存在' : `TTL ${value ?? '未知'} 秒`
const reset = () => {
  scanVersion++; valueVersion++
  keys.value = []; selected.value = null; detail.value = null
  scanning.value = false; reading.value = false; scanned.value = false
  scanCursor.value = '0'; scanMore.value = false
}
watch(() => props.instanceId, () => { db.value = 0; reset() }, { flush: 'sync' })
watch(db, reset, { flush: 'sync' })
onBeforeUnmount(reset)
defineExpose({ reset })
const scan = async (next) => {
  if (!validDb.value || scanning.value) return
  const version = ++scanVersion
  valueVersion++; selected.value = null; detail.value = null; reading.value = false
  scanning.value = true
  const requestedPattern = next ? activePattern.value : pattern.value || '*'
  try {
    const res = await ScanRedisKeysApi({ instance_id: props.instanceId, db: db.value, pattern: requestedPattern, cursor: next ? scanCursor.value : '0', count: 100 })
    if (version !== scanVersion) return
    const data = res.data
    activePattern.value = requestedPattern
    // COUNT is only a hint: display the entire page, including repeated SCAN results.
    keys.value = data.keys || []
    scanCursor.value = String(data.next_cursor || '0')
    scanMore.value = !!data.has_more || scanCursor.value !== '0'
    scanned.value = true
  } catch { /* request interceptor displays the error; retain the last successful page/cursor */ }
  finally { if (version === scanVersion) scanning.value = false }
}
const selectKey = (item) => { selected.value = item; detail.value = null; readValue(false) }
const readValue = async (next) => {
  if (!selected.value || !validDb.value) return
  const version = ++valueVersion
  reading.value = true
  try {
    const res = await GetRedisValueApi({ instance_id: props.instanceId, db: db.value, key_base64: selected.value.key_base64, cursor: next ? String(detail.value?.next_cursor || '0') : '0', offset: next ? detail.value?.next_offset || 0 : 0, count: 100 })
    if (version === valueVersion) detail.value = res.data
  } catch { /* request interceptor displays the error */ }
  finally { if (version === valueVersion) reading.value = false }
}
</script>

<style scoped>
.redis-browser { display: flex; flex-direction: column; gap: 12px; height: 100%; min-height: 0; color: var(--ant-colorText, #262626); }
.redis-controls { display: flex; gap: 12px; align-items: end; flex-wrap: wrap; }
.redis-controls label { display: flex; flex-direction: column; gap: 4px; }
.pattern { flex: 1; min-width: 160px; }
.redis-status { color: var(--ant-colorTextSecondary, #777); font-size: 12px; overflow-wrap: anywhere; }
.redis-columns { display: grid; grid-template-columns: minmax(180px, 32%) minmax(0, 1fr); flex: 1; min-height: 0; border: 1px solid var(--ant-colorBorder, #ddd); border-radius: 6px; overflow: hidden; }
.redis-key-list, .redis-detail { overflow: auto; min-height: 0; padding: 12px; }
.redis-key-list { border-right: 1px solid var(--ant-colorBorder, #ddd); }
.redis-key { display: flex; flex-direction: column; gap: 6px; width: 100%; text-align: left; padding: 10px; margin-bottom: 6px; color: inherit; background: transparent; border: 1px solid var(--ant-colorBorder, #ddd); border-radius: 4px; cursor: pointer; }
.redis-key.selected { border-color: var(--ant-colorPrimary, #1677ff); background: var(--ant-colorPrimaryBg, #e6f4ff); }
.redis-key-name, code { overflow-wrap: anywhere; white-space: pre-wrap; }
.redis-meta { margin: 12px 0; }
.redis-value { margin: 12px 0; padding: 10px; background: var(--ant-colorFillQuaternary, #fafafa); border: 1px solid var(--ant-colorBorderSecondary, #eee); border-radius: 4px; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; margin: 8px 0 0; font-family: monospace; }
@media (max-width: 700px) { .redis-columns { grid-template-columns: minmax(130px, 38%) minmax(0, 1fr); } }
</style>
