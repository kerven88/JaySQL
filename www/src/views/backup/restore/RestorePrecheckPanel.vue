<template>
  <div class="precheck-panel">
    <a-spin :spinning="loading" tip="正在执行预检查…">
      <a-empty v-if="!result && !errorMessage && !loading" :description="readonly ? '等待预检查结果' : '进入本步骤会自动执行预检查'">
        <a-button v-if="!readonly" type="primary" @click="emit('refresh')">开始预检查</a-button>
      </a-empty>

      <template v-else>
        <a-alert v-if="errorMessage" type="error" show-icon :message="errorMessage" class="precheck-alert" />

        <template v-if="result">
          <!-- 三档概览：一眼看出是「能跑」「有风险」「跑不了」 -->
          <div class="precheck-overview">
            <div class="precheck-stat" :class="{ 'is-active': blocked.length }">
              <span class="precheck-stat__num">{{ blocked.length }}</span>
              <span class="precheck-stat__label">阻断</span>
            </div>
            <div class="precheck-stat" :class="{ 'is-active': warned.length }">
              <span class="precheck-stat__num">{{ warned.length }}</span>
              <span class="precheck-stat__label">警告</span>
            </div>
            <div class="precheck-stat">
              <span class="precheck-stat__num">{{ passed.length }}</span>
              <span class="precheck-stat__label">通过</span>
            </div>
            <a-button v-if="!readonly" class="precheck-refresh" :loading="loading" @click="emit('refresh')">重新检查</a-button>
          </div>

          <div v-if="blocked.length" class="precheck-group precheck-group--blocked">
            <div class="precheck-group__title">阻断项（必须处理）</div>
            <div v-for="item in blocked" :key="itemKey(item)" class="precheck-item">
              <a-tag color="red">阻断</a-tag>
              <span class="precheck-item__label">{{ item.phase || '-' }} · {{ item.label || item.item_key }}</span>
              <div class="precheck-item__message">{{ item.message }}</div>
              <div v-if="fixHintOf(item)" class="precheck-item__fix">建议：{{ fixHintOf(item) }}</div>
            </div>
          </div>

          <div v-if="warned.length" class="precheck-group precheck-group--warned">
            <div class="precheck-group__title">警告项（可继续，需知悉）</div>
            <div v-for="item in warned" :key="itemKey(item)" class="precheck-item">
              <a-tag color="orange">警告</a-tag>
              <span class="precheck-item__label">{{ item.phase || '-' }} · {{ item.label || item.item_key }}</span>
              <div class="precheck-item__message">{{ item.message }}</div>
            </div>
          </div>

          <a-collapse v-if="passed.length" class="precheck-collapse" ghost>
            <a-collapse-panel key="passed" :header="`通过项（${passed.length}）`">
              <div v-for="item in passed" :key="itemKey(item)" class="precheck-item precheck-item--passed">
                <a-tag color="green">通过</a-tag>
                <span class="precheck-item__label">{{ item.phase || '-' }} · {{ item.label || item.item_key }}</span>
                <span class="precheck-item__message">{{ item.message }}</span>
              </div>
            </a-collapse-panel>
          </a-collapse>

          <a-collapse v-if="commands.length" class="precheck-collapse" ghost>
            <a-collapse-panel key="commands" header="命令预览">
              <pre class="precheck-commands">{{ commands.join('\n') }}</pre>
            </a-collapse-panel>
          </a-collapse>
        </template>
        <div v-else class="precheck-placeholder" />
      </template>
    </a-spin>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { fixHintOf, precheckToken } from './constants'

defineOptions({ name: 'RestorePrecheckPanel' })

const props = defineProps({
  loading: { type: Boolean, default: false },
  result: { type: Object, default: null },
  errorMessage: { type: String, default: '' },
  // readonly：恢复向导第 3 步「恢复预检查」专用，只呈现结果、不提供重新检查入口。
  readonly: { type: Boolean, default: false },
})

const emit = defineEmits(['refresh'])

const items = computed(() => (Array.isArray(props.result?.items) ? props.result.items : []))
const blocked = computed(() => items.value.filter((item) => precheckToken(item) === 'blocked'))
const warned = computed(() => items.value.filter((item) => precheckToken(item) === 'warning'))
const passed = computed(() => items.value.filter((item) => precheckToken(item) === 'passed'))

const commands = computed(() => {
  const list = props.result?.plan?.commands
  return Array.isArray(list) ? list.filter((item) => typeof item === 'string' && item.trim()) : []
})

const itemKey = (item) => String(item?.item_key || item?.key || item?.label || Math.random())
</script>

<style scoped lang="scss">
.precheck-panel {
  max-width: 920px;
}

.precheck-alert {
  margin-bottom: var(--gi-spacing-md, 16px);
}

.precheck-overview {
  display: flex;
  align-items: center;
  gap: var(--gi-spacing-lg, 24px);
  padding: var(--gi-spacing-sm, 8px) var(--gi-spacing-md, 16px);
  margin-bottom: var(--gi-spacing-md, 16px);
  border: 1px solid var(--gi-border-color, #d9e1e6);
  border-radius: var(--gi-radius-md, 8px);
  background: var(--gi-color-container-bg, #fff);
}

.precheck-stat {
  display: flex;
  flex-direction: column;
  align-items: center;
  min-width: 64px;
}

.precheck-stat__num {
  font-size: 20px;
  font-weight: 600;
  color: var(--gi-color-text-primary, #1f2d3d);
}

.precheck-stat__label {
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-text-tertiary, #8a979f);
}

.precheck-stat.is-active .precheck-stat__num {
  color: var(--gi-color-error, #ff4d4f);
}

.precheck-refresh {
  margin-left: auto;
}

.precheck-group {
  padding: var(--gi-spacing-md, 16px);
  margin-bottom: var(--gi-spacing-md, 16px);
  border-radius: var(--gi-radius-md, 8px);
}

.precheck-group--blocked {
  border: 1px solid var(--gi-color-error, #ff4d4f);
  background: rgba(255, 77, 79, 0.04);
}

.precheck-group--warned {
  border: 1px solid var(--gi-color-warning, #faad14);
  background: rgba(250, 173, 20, 0.06);
}

.precheck-group__title {
  font-weight: 600;
  color: var(--gi-color-text-primary, #1f2d3d);
  margin-bottom: var(--gi-spacing-sm, 8px);
}

.precheck-item {
  padding: var(--gi-spacing-xs, 4px) 0;
}

.precheck-item__label {
  margin-left: var(--gi-spacing-xs, 4px);
  font-weight: 600;
  color: var(--gi-color-text-primary, #1f2d3d);
}

.precheck-item__message {
  margin-top: 2px;
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-color-text-secondary, #5c6b73);
  word-break: break-all;
}

.precheck-item--passed .precheck-item__message {
  display: inline;
  margin-left: var(--gi-spacing-xs, 4px);
}

.precheck-item__fix {
  margin-top: 2px;
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-color-primary, #0f766e);
}

.precheck-collapse {
  margin-top: var(--gi-spacing-sm, 8px);
}

.precheck-commands {
  max-height: 260px;
  overflow: auto;
  margin: 0;
  padding: var(--gi-spacing-sm, 8px);
  border-radius: var(--gi-radius-sm, 6px);
  background: #f6f8f9;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: var(--gi-font-size-caption, 12px);
  white-space: pre-wrap;
}

.precheck-placeholder {
  height: 120px;
}
</style>
