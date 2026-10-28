<template>
  <div class="hs-panel">
    <a-spin :spinning="loading">
      <!-- 状态阈值：三联卡片（tile 语言与监控明细页 health-tile 同族） -->
      <section class="hs-block">
        <div class="hs-block-title">状态阈值</div>
        <div class="hs-tile-row">
          <div class="hs-tile">
            <div class="hs-tile-head">
              <span class="hs-dot" style="background: #cf3f3f"></span>
              <span class="tone-fault-text">故障</span>
            </div>
            <div class="hs-tile-input">
              健康分 ≤
              <a-input-number
                v-model:value="form.faultThreshold"
                :min="0"
                :max="100"
                :step="1"
                class="hs-input"
              />
              分
            </div>
          </div>
          <div class="hs-tile">
            <div class="hs-tile-head">
              <span class="hs-dot" style="background: #e8933c"></span>
              <span class="tone-risk-text">风险</span>
            </div>
            <div class="hs-tile-input">
              健康分 ≤
              <a-input-number
                v-model:value="form.riskThreshold"
                :min="1"
                :max="100"
                :step="1"
                class="hs-input"
              />
              分
            </div>
          </div>
          <div class="hs-tile">
            <div class="hs-tile-head">
              <span class="hs-dot" style="background: #35b583"></span>
              <span class="tone-healthy-text">健康</span>
            </div>
            <div class="hs-tile-input">健康分 &gt; <b class="hs-strong">风险阈值</b></div>
          </div>
        </div>
        <div v-if="thresholdError" class="hs-error">{{ thresholdError }}</div>
      </section>

      <!-- 健康分扣分：2×2 卡片，与阈值卡同一规格 -->
      <section class="hs-block">
        <div class="hs-block-title">健康分扣分</div>
        <!-- 4 张扣分卡固定同行：窄屏不换行不压缩，宽度不足时容器出横向滚动条 -->
        <div class="hs-ded-scroll">
          <div class="hs-ded-grid">
            <div v-for="level in levels" :key="level.code" class="hs-tile">
              <div class="hs-tile-head">
                <a-tag :color="level.color">{{ level.code }}</a-tag>
                <span class="hs-ded-name">{{ level.name }}</span>
              </div>
              <div class="hs-tile-input">
                每条扣
                <a-input-number
                  v-model:value="form.deductions[level.code]"
                  :min="0"
                  :max="100"
                  :step="1"
                  class="hs-input"
                />
                分
              </div>
            </div>
          </div>
        </div>
      </section>

      <div class="hs-footer">
        <div class="hs-actions">
          <a-button :loading="resetting" @click="onReset">恢复默认</a-button>
          <a-button type="primary" :loading="saving" :disabled="!!thresholdError" @click="onSave">保存并生效</a-button>
        </div>
      </div>
    </a-spin>
  </div>
</template>

<script setup>
// 健康判定面板：实例健康/风险/故障的唯一可配置入口。
// 判定模型（健康分驱动）：先算分（100 − Σ 扣分），再按分数区间定级：
//   1. 状态阈值：健康分 ≤ 故障阈值 → 故障；≤ 风险阈值 → 风险；其余 → 健康。
//      区间说明由 descFault/descRisk/descHealthy 统一驱动，
//      避免阈值来源分散导致同一分数落在不同区间或颜色不一致。
//   2. 扣分表：每条未恢复告警按 P 级扣多少分。
// 两组数字必须配套理解（默认 60/90 + 40/10/5/3：单条 P0 即故障、单条 P1 即风险）。
// 后端 evaluateInstanceState / healthStateClassFromScore 使用同一套区间，保存后 10 秒内全平台生效。
// UI 采用 tile 卡片（与监控明细页 health-tile 同语言），不再用表格。
import { message } from 'ant-design-vue'
import { computed, onMounted, reactive, ref } from 'vue'
import {
  getMonitorHealthGradeApi,
  resetMonitorHealthGradeApi,
  updateMonitorHealthGradeApi,
} from '@/api/monitor'

defineOptions({ name: 'MonitorHealthStatePanel' })

const levels = [
  { code: 'P0', name: '致命', color: 'red' },
  { code: 'P1', name: '严重', color: 'orange' },
  { code: 'P2', name: '一般', color: 'gold' },
  { code: 'P3', name: '提示', color: 'green' },
]


const loading = ref(false)
const saving = ref(false)
const resetting = ref(false)
const form = reactive({
  faultThreshold: 60,
  riskThreshold: 90,
  deductions: { P0: 40, P1: 10, P2: 5, P3: 3 },
})
const baseline = ref('')

const serialize = () => JSON.stringify({
  faultThreshold: form.faultThreshold,
  riskThreshold: form.riskThreshold,
  deductions: { ...form.deductions },
})

const thresholdError = computed(() => {
  const fault = Number(form.faultThreshold)
  const risk = Number(form.riskThreshold)
  if (!Number.isInteger(fault) || fault < 0 || fault > 100) return '故障阈值必须是 0~100 的整数'
  if (!Number.isInteger(risk) || risk < 1 || risk > 100) return '风险阈值必须是 1~100 的整数'
  if (fault >= risk) return '故障阈值必须小于风险阈值（同一分数不能同时落两个区间）'
  return ''
})

// 判定边界规则与后端 healthStateClassFromScore 完全一致：
//   健康分 ≤ faultThreshold       → 故障（左闭）
//   faultThreshold < 健康分 ≤ riskThreshold → 风险
//   健康分 > riskThreshold        → 健康

// 示例联动：1 条 P1 + 2 条 P3 的分数落在哪个区间
const applyConfig = (cfg = {}) => {
  form.faultThreshold = Number(cfg.fault_threshold ?? 60)
  form.riskThreshold = Number(cfg.risk_threshold ?? 90)
  form.deductions = {
    P0: Number(cfg.deductions?.P0 ?? 40),
    P1: Number(cfg.deductions?.P1 ?? 10),
    P2: Number(cfg.deductions?.P2 ?? 5),
    P3: Number(cfg.deductions?.P3 ?? 3),
  }
  baseline.value = serialize()
}

const reload = async () => {
  loading.value = true
  try {
    const res = await getMonitorHealthGradeApi()
    applyConfig(res?.data || {})
  } catch (error) {
    message.error(error?.message || '加载健康判定配置失败')
  } finally {
    loading.value = false
  }
}

const onSave = async () => {
  if (thresholdError.value) return
  saving.value = true
  try {
    const res = await updateMonitorHealthGradeApi({
      config: {
        fault_threshold: Number(form.faultThreshold),
        risk_threshold: Number(form.riskThreshold),
        deductions: { ...form.deductions },
      },
    })
    applyConfig(res?.data || {})
    message.success(res?.message || '已保存，判定立即生效')
  } catch (error) {
    message.error(error?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

const onReset = () => {
  resetting.value = true
  message.loading({ content: '正在恢复默认…', key: 'hs-reset' })
  void (async () => {
    try {
      const res = await resetMonitorHealthGradeApi()
      applyConfig(res?.data || {})
      message.success({ content: res?.message || '已恢复内置默认', key: 'hs-reset' })
    } catch (error) {
      message.error({ content: error?.message || '恢复默认失败', key: 'hs-reset' })
    } finally {
      resetting.value = false
    }
  })()
}

const isDirty = () => serialize() !== baseline.value
const clearDirty = () => {
  baseline.value = serialize()
}

defineExpose({ reload, isDirty, clearDirty })

onMounted(reload)
</script>

<style scoped>
.hs-panel {
  display: flex;
  flex-direction: column;
}

.hs-block {
  margin-top: 14px;
}

.hs-block:first-child {
  margin-top: 4px;
}

.hs-block-title {
  font-size: 14px;
  font-weight: 600;
  color: #243342;
  margin-bottom: 10px;
}

/* 状态阈值三联卡：用 4 列网格，让 3 张卡与下方扣分卡的 P0/P1/P2 一一对应，
   第 4 列留空，不再像原来那样撑满整行，使上下两区边界对齐。 */
.hs-tile-row {
  display: grid;
  grid-template-columns: repeat(4, minmax(220px, 1fr));
  gap: 14px;
}

/* 扣分卡固定 4 列同行：minmax 下限 220px 保证卡片不被压缩；
   容器 overflow-x 在窗口变窄时出横向滚动条，永不换行/堆叠 */
.hs-ded-scroll {
  overflow-x: auto;
  /* 滚动条贴圆角内容，留一点呼吸空间 */
  padding-bottom: 4px;
}

.hs-ded-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(220px, 1fr));
  gap: 14px;
}

.hs-tile {
  background: #f5f8fb;
  border-radius: 12px;
  padding: 16px 18px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 0;
}

.hs-tile-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13.5px;
  font-weight: 600;
}

.hs-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.hs-tile-input {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: #47566b;
}

.hs-input {
  width: 84px;
}

.hs-strong {
  font-size: 14px;
  color: #243342;
}

.hs-ded-name {
  color: #47566b;
}

.tone-fault-text {
  color: #cf1322;
}

.tone-risk-text {
  color: #d46b08;
}

.tone-healthy-text {
  color: #0f9f6f;
}

.hs-error {
  margin-top: 12px;
  font-size: 12.5px;
  color: #cf1322;
}

/* 原左侧的「当前为自定义配置 · 更新于 …」元信息已移除，这里只剩操作按钮；
   保持按钮贴右（原先靠 space-between 被左侧文案顶到右边），不让它跳到左边。 */
.hs-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 20px;
  flex-wrap: wrap;
}

.hs-actions {
  display: flex;
  gap: 10px;
}

@media (max-width: 768px) {
  /* 4 列 x 220px 下限 = 922px，容器没有 overflow，必然横向溢出 */
  .hs-tile-row,
  .hs-ded-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
  }

  .hs-tile {
    padding: 12px 14px;
  }

  .hs-input {
    width: 72px;
  }
}
</style>
