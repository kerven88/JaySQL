<template>
  <!--
    指标说明图标（○?）+ 悬浮说明面板。
    内容完全由 props 驱动（rows: [{ name, definition, unit }]），
    组件内不写任何指标文案 —— 新增指标时只要目录里带了 definition/unit 就自动生效。
  -->
  <a-popover
    v-model:open="open"
    :trigger="['hover', 'click']"
    placement="bottomLeft"
    overlay-class-name="metric-help-popover"
    :mouse-enter-delay="0.3"
    :mouse-leave-delay="0.15"
  >
    <template #content>
      <div class="mh-panel">
        <div class="mh-head">
          <span class="mh-title">指标说明（{{ title }}）</span>
          <button
            type="button"
            class="mh-close"
            aria-label="关闭指标说明"
            @click="open = false"
          >
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
              <line x1="5" y1="5" x2="19" y2="19" />
              <line x1="19" y1="5" x2="5" y2="19" />
            </svg>
          </button>
        </div>
        <table class="mh-table">
          <thead>
            <tr>
              <th class="col-name">指标名</th>
              <th class="col-def">说明</th>
              <th class="col-unit">单位</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in normalizedRows" :key="row.key">
              <td class="col-name">{{ row.name }}</td>
              <td class="col-def">{{ row.definition || '—' }}</td>
              <td class="col-unit">{{ row.unit || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
    <button
      type="button"
      class="mh-icon"
      :class="{ 'mh-icon--dense': dense }"
      aria-label="查看指标说明"
      :aria-expanded="open ? 'true' : 'false'"
      title="查看指标说明"
      @click.stop
    >
      <QuestionCircleOutlined />
    </button>
  </a-popover>
</template>

<script setup>
import { computed, ref } from 'vue'
import { QuestionCircleOutlined } from '@ant-design/icons-vue'

const props = defineProps({
  // 监控项名称（面板标题里展示）
  title: { type: String, default: '' },
  // 该监控项下的指标行：name / definition / unit（与展示目录下发字段一致）
  rows: { type: Array, default: () => [] },
  // 紧凑模式：用于图例行等小尺寸场景（图标更小、间距更紧）
  dense: { type: Boolean, default: false },
})

const open = ref(false)

const normalizedRows = computed(() =>
  (props.rows || []).map((row, index) => ({
    key: row.key || row.metricKey || `${index}`,
    name: row.name || '',
    definition: row.definition || '',
    unit: row.unit || '',
  })),
)
</script>

<!-- 弹层 teleport 到 body，样式必须非 scoped -->
<style>
.metric-help-popover {
  --mh-width: 560px;
}
.metric-help-popover .ant-popover-inner {
  padding: 0;
  border-radius: 10px;
  box-shadow: 0 6px 24px rgba(15, 34, 67, 0.14);
}
.mh-panel {
  width: var(--mh-width);
  max-width: 82vw;
}
.mh-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid #f0f0f0;
}
.mh-title {
  font-size: 14px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.88);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.mh-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  width: 24px;
  height: 24px;
  padding: 0;
  color: rgba(0, 0, 0, 0.45);
  background: transparent;
  border: none;
  border-radius: 6px;
  cursor: pointer;
  transition: color 0.2s, background 0.2s;
}
.mh-close:hover {
  color: rgba(0, 0, 0, 0.88);
  background: rgba(0, 0, 0, 0.04);
}
.mh-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  table-layout: fixed;
}
.mh-table th,
.mh-table td {
  padding: 9px 16px;
  text-align: left;
  word-break: break-word;
  line-height: 1.5;
}
.mh-table thead th {
  color: rgba(0, 0, 0, 0.65);
  font-weight: 500;
  background: #fafafa;
  border-bottom: 1px solid #f0f0f0;
}
.mh-table tbody td {
  color: rgba(0, 0, 0, 0.88);
  border-bottom: 1px solid #f5f5f5;
}
.mh-table tbody tr:last-child td {
  border-bottom: none;
}
.mh-table .col-name {
  width: 34%;
}
.mh-table .col-def {
  width: 46%;
}
.mh-table .col-unit {
  width: 20%;
}

/* 触发图标本身 */
.mh-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  width: 20px;
  height: 20px;
  padding: 0;
  font-size: 13px;
  color: var(--gi-text-3, rgba(0, 0, 0, 0.4));
  background: transparent;
  border: none;
  border-radius: 50%;
  cursor: pointer;
  opacity: 0.55;
  transition: opacity 0.2s, color 0.2s, background 0.2s;
}
.mh-icon:hover,
.mh-icon:focus-visible,
.mh-icon[aria-expanded='true'] {
  color: var(--gi-primary, #1677ff);
  opacity: 1;
}

/* 紧凑模式：图例行内使用，与 12px 文字同一水平线 */
.mh-icon--dense {
  width: 15px;
  height: 15px;
  font-size: 11px;
}
.mh-icon--dense .anticon {
  vertical-align: -0.05em;
}
</style>
