<template>
  <a-drawer
    :open="open"
    :width="720"
    :title="'接入大模型服务商'"
    :body-style="{ paddingBottom: '72px' }"
    @close="handleClose"
  >
    <a-steps :current="current" size="small" class="wizard-steps">
      <a-step title="选择服务商" />
      <a-step title="填写凭证" />
      <a-step title="测试连接" />
      <a-step title="选择模型" />
      <a-step title="保存启用" />
    </a-steps>

    <!-- 步骤 1：服务商卡片墙（数据来自模板表） -->
    <div v-if="current === 0" class="wizard-body">
      <div v-for="group in groupedTemplates" :key="group.category" class="vendor-group">
        <div class="vendor-group-title">
          <span>{{ group.label }}</span>
        </div>
        <div class="vendor-grid">
          <div
            v-for="item in group.items"
            :key="item.vendor"
            class="vendor-card"
            :class="{ active: selectedVendor?.vendor === item.vendor }"
            @click="selectVendor(item)"
          >
            <img
              v-if="llmVendorLogo(item)"
              class="vendor-logo"
              :src="llmVendorLogo(item)"
              :alt="item.label"
            />
            <div class="vendor-name">{{ item.label }}</div>
          </div>
        </div>
      </div>
      <a-empty v-if="!templates.length" description="暂无可用厂商模板，请联系管理员检查模板启用状态" />
    </div>

    <!-- 步骤 2：动态表单（模板驱动） -->
    <div v-if="current === 1" class="wizard-body">
      <DynamicProviderForm
        ref="formRef"
        :template="selectedVendor || {}"
        :form="form"
      />
    </div>

    <!-- 步骤 3：测试连接 -->
    <div v-if="current === 2" class="wizard-body">
      <div class="test-header">
        <a-button type="primary" :loading="testing" @click="runTest">
          {{ testResult ? '重新测试' : '开始测试' }}
        </a-button>
        <span class="test-hint">测试不会保存配置，可放心多次尝试</span>
      </div>

      <a-card v-if="testResult" :bordered="false" class="result-card">
        <a-result
          :status="testResult.ok ? 'success' : 'error'"
          :title="testResult.ok ? '连接成功，凭证有效' : testResult.message"
          :sub-title="testResult.ok ? `耗时 ${testResult.latency_ms} ms` : (testResult.hint || '')"
        >
          <template #extra>
            <div v-if="testResult.ok" class="result-detail">
              <div class="detail-row">
                <span class="detail-label">识别到模型</span>
                <span class="detail-value">{{ testResult.model_count || 0 }} 个</span>
              </div>
              <div class="detail-row">
                <span class="detail-label">能力探测</span>
                <span class="detail-value">
                  <a-tag
                    v-for="cap in testResult.capabilities || []"
                    :key="cap"
                    color="blue"
                    style="margin-bottom: 4px"
                  >
                    {{ capabilityLabel(cap) }}
                  </a-tag>
                </span>
              </div>
            </div>
            <div v-else class="result-detail">
              <div class="detail-row">
                <span class="detail-label">校验阶段</span>
                <span class="detail-value">{{ testResult.stage }}</span>
              </div>
              <div class="detail-row">
                <span class="detail-label">错误分类</span>
                <span class="detail-value">
                  <a-tag :color="categoryMeta(testResult.category).color">
                    {{ categoryMeta(testResult.category).text }}
                  </a-tag>
                </span>
              </div>
              <div v-if="testResult.raw" class="detail-row">
                <span class="detail-label">原始返回</span>
                <span class="detail-value mono">{{ testResult.raw }}</span>
              </div>
            </div>
          </template>
        </a-result>
      </a-card>
    </div>

    <!-- 步骤 4：选择模型 -->
    <div v-if="current === 3" class="wizard-body">
      <a-alert
        v-if="probeModels.length"
        type="success"
        show-icon
        :message="`已从厂商侧识别到 ${probeModels.length} 个模型`"
        description="请选择默认模型。创建完成后会自动同步完整模型清单，未选中的模型仍可在此后随时启用。"
        style="margin-bottom: 16px"
      />
      <a-alert
        v-else
        type="warning"
        show-icon
        message="未自动识别到模型清单"
        description="该服务可能不提供模型列表接口（私有化部署常见）。请手动填写默认模型名，或创建后在「模型管理」中补充。"
        style="margin-bottom: 16px"
      />

      <a-radio-group v-if="probeModels.length" v-model:value="selectedModel" class="model-list">
        <a-radio v-for="item in probeModels" :key="item.name" :value="item.name" class="model-item">
          <span class="model-name">{{ item.name }}</span>
        </a-radio>
      </a-radio-group>

      <a-form layout="vertical">
        <a-form-item label="默认模型名">
          <a-input v-model:value="selectedModel" placeholder="例如：deepseek-chat / qwen2.5:14b" />
        </a-form-item>
      </a-form>
    </div>

    <!-- 步骤 5：保存并启用 -->
    <div v-if="current === 4" class="wizard-body">
      <a-descriptions bordered size="small" :column="1">
        <a-descriptions-item label="厂商">{{ selectedVendor?.label }}</a-descriptions-item>
        <a-descriptions-item label="服务名称">{{ form.display_name }}</a-descriptions-item>
        <a-descriptions-item label="端点">{{ form.base_url || selectedVendor?.default_base_url }}</a-descriptions-item>
        <a-descriptions-item label="鉴权方式">{{ form.auth_mode || selectedVendor?.auth_mode }}</a-descriptions-item>
        <a-descriptions-item label="API Key">
          <span v-if="form.api_key">{{ maskPreview(form.api_key) }}</span>
          <span v-else class="muted">未填写（该厂商允许空置）</span>
        </a-descriptions-item>
        <a-descriptions-item label="默认模型">
          <span v-if="selectedModel">{{ selectedModel }}</span>
          <span v-else class="muted">未指定</span>
        </a-descriptions-item>
        <a-descriptions-item label="能力">
          <a-tag v-for="cap in testResult?.capabilities || []" :key="cap" color="blue">
            {{ capabilityLabel(cap) }}
          </a-tag>
        </a-descriptions-item>
        <a-descriptions-item label="共享">{{ form.is_shared ? '共享给全部使用者' : '仅自己可见' }}</a-descriptions-item>
      </a-descriptions>

      <a-alert
        v-if="saveError"
        type="error"
        show-icon
        :message="saveError"
        style="margin-top: 16px"
      />
      <a-alert
        v-else
        type="info"
        show-icon
        message="确认后将依次执行：加密入库 → 验证并同步模型 → 设为默认模型 → 启用"
        style="margin-top: 16px"
      />
    </div>

    <!-- 底部操作 -->
    <div class="wizard-footer">
      <a-button v-if="current > 0" :disabled="saving" @click="current -= 1">上一步</a-button>
      <div class="footer-right">
        <a-button @click="handleClose">取消</a-button>
        <a-button
          v-if="current < 3"
          type="primary"
          :disabled="!canGoNext"
          :loading="current === 2 && testing"
          @click="goNext"
        >
          下一步
        </a-button>
        <a-button
          v-if="current === 3"
          type="primary"
          @click="current = 4"
        >
          下一步
        </a-button>
        <a-button
          v-if="current === 4"
          type="primary"
          :loading="saving"
          @click="handleSave"
        >
          确认创建并启用
        </a-button>
      </div>
    </div>
  </a-drawer>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { message } from 'ant-design-vue'
import {
  aiModelListApi,
  aiModelSetDefaultApi,
  aiProviderCreateApi,
  aiProviderEnableApi,
  aiProviderTestApi,
  aiProviderValidateApi,
} from '@/api/ai'
import DynamicProviderForm from './components/DynamicProviderForm.vue'
import { llmVendorLogo } from '@/utils/llmVendorLogo'
import {
  CAPABILITY_LABELS,
  VENDOR_CATEGORY_META,
  VALIDATION_CATEGORY_META,
  buildProviderFormFromTemplate,
  parseJSONField,
} from './helpers'

const props = defineProps({
  open: { type: Boolean, default: false },
  templates: { type: Array, default: () => [] },
})
const emit = defineEmits(['update:open', 'success'])

const current = ref(0)
const selectedVendor = ref(null)
const form = ref(buildProviderFormFromTemplate({}))
const formRef = ref()
const testing = ref(false)
const testResult = ref(null)
const selectedModel = ref('')
const saving = ref(false)
const saveError = ref('')

watch(
  () => props.open,
  (value) => {
    if (value) reset()
  },
)

const reset = () => {
  current.value = 0
  selectedVendor.value = null
  form.value = buildProviderFormFromTemplate({})
  testResult.value = null
  selectedModel.value = ''
  saveError.value = ''
  saving.value = false
}

const groupedTemplates = computed(() => {
  const order = ['international', 'domestic', 'private', 'custom']
  const groups = order
    .map((category) => {
      const meta = VENDOR_CATEGORY_META[category] || { text: category, color: 'default' }
      return {
        category,
        label: meta.text,
        color: meta.color,
        items: props.templates.filter((item) => item.category === category),
      }
    })
    .filter((group) => group.items.length > 0)
  return groups
})

const probeModels = computed(() => {
  const models = testResult.value?.models || []
  return models.slice(0, 200)
})

const canGoNext = computed(() => {
  if (current.value === 0) return !!selectedVendor.value
  if (current.value === 1) return true
  if (current.value === 2) return !!testResult.value?.ok
  return true
})

const selectVendor = (item) => {
  selectedVendor.value = item
  form.value = buildProviderFormFromTemplate(item)
}

const goNext = () => {
  if (current.value === 1) {
    const errors = formRef.value?.validate?.() || []
    if (errors.length) {
      message.warning(errors[0])
      return
    }
  }
  if (current.value === 2) {
    if (!testResult.value?.ok) {
      message.warning('请先完成连接测试')
      return
    }
  }
  current.value += 1
  if (current.value === 2 && !testResult.value) {
    runTest()
  }
  if (current.value === 3 && !selectedModel.value) {
    // 默认选中探测到的第一个模型
    selectedModel.value = probeModels.value[0]?.name || parseJSONField(form.value.extra_config?.model, '') || ''
  }
}

const runTest = async () => {
  testing.value = true
  testResult.value = null
  try {
    const res = await aiProviderTestApi({
      vendor: selectedVendor.value?.vendor,
      base_url: form.value.base_url,
      auth_mode: form.value.auth_mode,
      auth_header_name: form.value.auth_header_name,
      api_key: form.value.api_key,
      extra_config: form.value.extra_config,
      timeout_seconds: form.value.timeout_seconds,
      model: selectedModel.value || form.value.extra_config?.model || '',
    })
    testResult.value = res?.data || null
    if (testResult.value?.ok) {
      message.success('连接成功')
    }
  } catch (err) {
    testResult.value = {
      ok: false,
      stage: 'L2',
      message: err?.message || '测试失败',
      category: 'unknown',
    }
  } finally {
    testing.value = false
  }
}

const handleSave = async () => {
  saving.value = true
  saveError.value = ''
  let providerId = ''
  try {
    const payload = {
      ...form.value,
      vendor: selectedVendor.value?.vendor,
      extra_config: { ...form.value.extra_config },
    }
    if (selectedModel.value) {
      payload.extra_config.model = selectedModel.value
    }

    // 1) 创建（凭证加密入库，状态 draft）
    const created = await aiProviderCreateApi(payload)
    providerId = created?.data?.provider_id
    if (!providerId) throw new Error('创建失败：未返回 provider_id')

    // 2) 验证（同时把模型清单差分同步进库）
    const validated = await aiProviderValidateApi({ provider_id: providerId })
    const result = validated?.data
    if (result && result.ok === false) {
      throw new Error(`验证未通过：${result.message}`)
    }

    // 3) 指定默认模型
    if (selectedModel.value) {
      const list = await aiModelListApi({ provider_id: providerId, page_size: 500 })
      const rows = list?.data || []
      const matched = rows.find((item) => item.model_name === selectedModel.value)
      if (matched) {
        await aiModelSetDefaultApi({ model_uid: matched.model_uid })
      }
    }

    // 4) 启用
    await aiProviderEnableApi({ provider_id: providerId })

    message.success('接入成功，服务商已启用')
    emit('success')
    emit('update:open', false)
  } catch (err) {
    saveError.value = err?.message || '保存失败'
    message.error(saveError.value)
  } finally {
    saving.value = false
  }
}

const handleClose = () => {
  if (saving.value) return
  emit('update:open', false)
}

const capabilityLabel = (key) => CAPABILITY_LABELS[key] || key
const categoryMeta = (key) => VALIDATION_CATEGORY_META[key] || VALIDATION_CATEGORY_META.unknown
const maskPreview = (value) => {
  const text = String(value || '')
  if (text.length <= 8) return '*'.repeat(text.length)
  return `${text.slice(0, 4)}***${text.slice(-4)}`
}
</script>

<style scoped>
.wizard-steps {
  margin-bottom: 20px;
}
.wizard-body {
  min-height: 320px;
}
.vendor-group {
  margin-bottom: 18px;
}
.vendor-group-title {
  font-size: 13px;
  font-weight: 500;
  margin-bottom: 10px;
  color: rgba(0, 0, 0, 0.85);
}
.vendor-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}
.vendor-card {
  border: 1px solid #e6e4dd;
  border-radius: 10px;
  padding: 12px 14px;
  cursor: pointer;
  transition: all 0.2s;
  background: #fff;
  display: flex;
  align-items: center;
  gap: 9px;
}
.vendor-card:hover {
  border-color: #b5d4f4;
}
.vendor-card.active {
  border-color: #185fa5;
  background: #e6f1fb;
}
/* 品牌 Logo：固定 18px 方框，contain 避免各厂商标识别宽高比不一被拉伸 */
.vendor-logo {
  flex: 0 0 18px;
  width: 18px;
  height: 18px;
  object-fit: contain;
}
.vendor-name {
  font-size: 13px;
  font-weight: 500;
  line-height: 1.4;
}
.test-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}
.test-hint {
  font-size: 12px;
  color: rgba(0, 0, 0, 0.45);
}
.result-card {
  background: #fafaf8;
}
.result-detail {
  text-align: left;
  border-top: 1px solid #e6e4dd;
  padding-top: 12px;
}
.detail-row {
  display: flex;
  gap: 12px;
  padding: 4px 0;
  font-size: 12.5px;
}
.detail-label {
  flex: 0 0 84px;
  color: rgba(0, 0, 0, 0.45);
}
.detail-value {
  flex: 1;
  word-break: break-all;
}
.detail-value.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
}
.model-list {
  display: block;
  max-height: 260px;
  overflow-y: auto;
  margin-bottom: 16px;
}
.model-item {
  display: block;
  padding: 6px 0;
}
.model-name {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12.5px;
}
.muted {
  color: rgba(0, 0, 0, 0.45);
}
.wizard-footer {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  padding: 12px 24px;
  background: #fff;
  border-top: 1px solid #e6e4dd;
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.footer-right {
  display: flex;
  gap: 8px;
}

@media (max-width: 768px) {
  :deep(.ant-space) {
    flex-wrap: wrap;
    row-gap: 8px;
  }
}
</style>
