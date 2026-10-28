<template>
  <div class="ai-chat-code-block">
    <div class="code-block-header">
      <div class="code-block-meta">
        <span class="dialect-tag">{{ dialectLabel }}</span>
        <span v-if="payload.description" class="code-desc">{{ payload.description }}</span>
      </div>
      <div class="code-block-actions">
        <a-button size="small" :disabled="copied" @click="handleCopy">
          <template #icon>
            <component :is="copied ? CheckOutlined : CopyOutlined" />
          </template>
          {{ copied ? '已复制' : '复制' }}
        </a-button>
        <a-button type="primary" size="small" ghost @click="handleExecute">
          <template #icon>
            <ArrowRightOutlined />
          </template>
          去执行
        </a-button>
      </div>
    </div>
    <pre class="code-block-pre"><code>{{ payload.sql }}</code></pre>
    <div v-if="payload.suggestedInstance || payload.suggestedDatabase" class="code-block-suggest">
      <span class="suggest-label">建议</span>
      <span v-if="payload.suggestedInstance" class="suggest-chip">实例：{{ payload.suggestedInstance }}</span>
      <span v-if="payload.suggestedDatabase" class="suggest-chip">库：{{ payload.suggestedDatabase }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { message } from 'ant-design-vue'
import { ArrowRightOutlined, CheckOutlined, CopyOutlined } from '@ant-design/icons-vue'

const props = defineProps({
  payload: {
    type: Object,
    required: true,
  },
})

const router = useRouter()
const copied = ref(false)

const dialectLabel = computed(() => {
  const map = {
    mysql: 'MySQL',
    postgres: 'PostgreSQL',
    oracle: 'Oracle',
    sqlserver: 'SQL Server',
    clickhouse: 'ClickHouse',
    tidb: 'TiDB',
  }
  return map[String(props.payload?.dialect || '').toLowerCase()] || (props.payload?.dialect || 'SQL')
})

const handleCopy = async () => {
  if (!props.payload?.sql) return
  try {
    await navigator.clipboard.writeText(props.payload.sql)
    copied.value = true
    message.success('SQL 已复制')
    setTimeout(() => (copied.value = false), 1800)
  } catch {
    message.error('复制失败，请手动选择')
  }
}

const handleExecute = () => {
  if (!props.payload?.sql) return
  const query = {
    preset_sql: encodeURIComponent(props.payload.sql),
  }
  if (props.payload?.suggestedDatabase) {
    query.preset_db = props.payload.suggestedDatabase
  }
  router.push({ path: '/sql-workbench/query', query })
}
</script>

<style scoped>
.ai-chat-code-block {
  margin-top: 10px;
  border-radius: 14px;
  overflow: hidden;
  background: #0b1e25;
  border: 1px solid rgba(15, 118, 110, 0.22);
  box-shadow: 0 6px 16px rgba(11, 30, 37, 0.18);
}

.code-block-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 14px;
  background: linear-gradient(180deg, rgba(15, 118, 110, 0.18) 0%, rgba(11, 30, 37, 0) 100%);
  border-bottom: 1px solid rgba(255, 255, 255, 0.05);
}

.code-block-meta {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.dialect-tag {
  display: inline-flex;
  align-items: center;
  padding: 3px 10px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 600;
  background: rgba(15, 118, 110, 0.28);
  color: #8bd5c6;
  letter-spacing: 0.02em;
}

.code-desc {
  font-size: 12px;
  color: rgba(189, 214, 210, 0.78);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.code-block-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.code-block-actions :deep(.ant-btn) {
  border-radius: 10px;
}

.code-block-pre {
  margin: 0;
  padding: 14px 16px;
  max-height: 280px;
  overflow: auto;
}

.code-block-pre code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
  font-size: 12.5px;
  line-height: 1.7;
  color: #e6f4f1;
  white-space: pre;
  word-break: break-all;
}

.code-block-suggest {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 9px 14px;
  border-top: 1px solid rgba(255, 255, 255, 0.05);
  background: rgba(255, 255, 255, 0.02);
}

.suggest-label {
  font-size: 11px;
  font-weight: 600;
  color: #8bd5c6;
  letter-spacing: 0.04em;
}

.suggest-chip {
  display: inline-flex;
  align-items: center;
  padding: 2px 9px;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.06);
  font-size: 11.5px;
  color: rgba(189, 214, 210, 0.86);
}

@media (max-width: 767px) {
  .code-block-header {
    flex-direction: column;
    align-items: flex-start;
  }
  .code-block-actions {
    width: 100%;
    justify-content: flex-end;
  }
}
</style>
