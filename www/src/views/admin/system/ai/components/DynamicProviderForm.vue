<template>
  <div class="dynamic-provider-form">
    <a-form layout="vertical" :model="form">
      <!-- 必要凭证：按模板渲染，只显示该厂商真正需要的字段 -->
      <a-form-item label="服务名称" required>
        <a-input
          v-model:value="form.display_name"
          placeholder="例如：内网 DeepSeek-32B"
          :maxlength="128"
        />
        <template #extra>仅用于平台内标识，便于区分同一厂商的多个接入</template>
      </a-form-item>

      <a-form-item :label="baseURLRequired ? '端点地址' : '端点地址（可选）'" :required="baseURLRequired">
        <a-input
          v-model:value="form.base_url"
          :placeholder="template?.default_base_url || 'https://api.example.com/v1'"
        />
        <template v-if="!template?.default_base_url" #extra>该厂商无默认端点，必须填写完整地址</template>
      </a-form-item>

      <a-form-item :label="apiKeyRequired ? 'API Key' : 'API Key（可选）'" :required="apiKeyRequired">
        <a-input-password
          v-model:value="form.api_key"
          :placeholder="apiKeyPlaceholder"
          autocomplete="new-password"
        />
        <template v-if="existingMask" #extra>当前已保存：{{ existingMask }}；留空表示不修改</template>
      </a-form-item>

      <!-- 厂商差异化字段（模板驱动） -->
      <a-form-item
        v-for="field in primaryFields"
        :key="field.key"
        :label="field.label || field.key"
        :required="!!field.required"
      >
        <a-select
          v-if="field.type === 'select'"
          v-model:value="form.extra_config[field.key]"
          :options="(field.options || []).map((item) => ({ value: item, label: item }))"
          :placeholder="field.placeholder"
          allow-clear
        />
        <a-switch
          v-else-if="field.type === 'boolean'"
          v-model:checked="form.extra_config[field.key]"
        />
        <a-input-number
          v-else-if="field.type === 'number'"
          v-model:value="form.extra_config[field.key]"
          style="width: 100%"
          :placeholder="field.placeholder"
        />
        <a-input-password
          v-else-if="field.type === 'password'"
          v-model:value="form.extra_config[field.key]"
          :placeholder="field.placeholder"
        />
        <a-input
          v-else
          v-model:value="form.extra_config[field.key]"
          :placeholder="field.placeholder"
        />
        <template v-if="field.help" #extra>{{ field.help }}</template>
      </a-form-item>

      <a-form-item label="共享给其他使用者">
        <a-switch v-model:checked="form.is_shared" />
        <template #extra>关闭后该配置仅创建人可见</template>
      </a-form-item>

      <a-collapse v-model:activeKey="advancedPanels" :bordered="false" class="advanced-collapse">
        <a-collapse-panel key="advanced">
          <template #header>
            <span class="advanced-title">高级选项</span>
          </template>

          <a-form-item label="鉴权方式">
            <a-select
              v-model:value="form.auth_mode"
              :options="authModeOptions"
              allow-clear
            />
          </a-form-item>

          <a-form-item label="鉴权 Header 名">
            <a-input
              v-model:value="form.auth_header_name"
              :placeholder="template?.auth_header_name || '留空则使用标准 Authorization: Bearer'"
            />
          </a-form-item>

          <a-form-item
            v-for="field in advancedFields"
            :key="field.key"
            :label="field.label || field.key"
            :required="!!field.required"
          >
            <a-select
              v-if="field.type === 'select'"
              v-model:value="form.extra_config[field.key]"
              :options="(field.options || []).map((item) => ({ value: item, label: item }))"
              :placeholder="field.placeholder"
              allow-clear
            />
            <a-switch
              v-else-if="field.type === 'boolean'"
              v-model:checked="form.extra_config[field.key]"
            />
            <a-input-number
              v-else-if="field.type === 'number'"
              v-model:value="form.extra_config[field.key]"
              style="width: 100%"
              :placeholder="field.placeholder"
            />
            <a-input-password
              v-else-if="field.type === 'password'"
              v-model:value="form.extra_config[field.key]"
              :placeholder="field.placeholder"
            />
            <a-input
              v-else
              v-model:value="form.extra_config[field.key]"
              :placeholder="field.placeholder"
            />
            <template v-if="field.help" #extra>{{ field.help }}</template>
          </a-form-item>

          <a-row :gutter="16">
            <a-col :span="12">
              <a-form-item label="请求超时（秒）">
                <a-input-number v-model:value="form.timeout_seconds" :min="1" :max="600" style="width: 100%" />
              </a-form-item>
            </a-col>
            <a-col :span="12">
              <a-form-item label="失败重试次数">
                <a-input-number v-model:value="form.max_retries" :min="0" :max="5" style="width: 100%" />
              </a-form-item>
            </a-col>
          </a-row>
        </a-collapse-panel>
      </a-collapse>

      <a-form-item label="备注">
        <a-input v-model:value="form.remark" :maxlength="256" placeholder="选填" />
      </a-form-item>
    </a-form>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { asArray, templateRequiresAPIKey } from '../helpers'

const props = defineProps({
  template: { type: Object, default: () => ({}) },
  form: { type: Object, required: true },
  existingMask: { type: String, default: '' },
  showDisplayName: { type: Boolean, default: true },
})

const advancedPanels = ref([])

const requiredFields = computed(() => asArray(props.template?.required_fields))
const baseURLRequired = computed(() => requiredFields.value.includes('base_url'))
const apiKeyRequired = computed(() => templateRequiresAPIKey(props.template))
const apiKeyPlaceholder = computed(() =>
  apiKeyRequired.value ? '粘贴厂商控制台生成的 API Key' : '该厂商可不填（私有化服务空置即可）',
)

const allExtraFields = computed(() => asArray(props.template?.extra_fields))

// api_key 在基础区单独渲染，不重复出现
const primaryFields = computed(() =>
  allExtraFields.value.filter((field) => field?.key && field.key !== 'api_key' && field.required === true),
)
const advancedFields = computed(() =>
  allExtraFields.value.filter((field) => field?.key && field.key !== 'api_key' && field.required !== true),
)

const authModeOptions = [
  { value: 'bearer', label: 'Bearer Token（默认）' },
  { value: 'api_key_header', label: '自定义 Header（如 api-key）' },
  { value: 'query_param', label: 'URL 参数' },
  { value: 'signature', label: 'HMAC 签名' },
]

// 供向导在「下一步」时校验当前步骤
const validate = () => {
  const errors = []
  if (!String(props.form.display_name || '').trim()) errors.push('请填写服务名称')
  if (baseURLRequired.value && !String(props.form.base_url || '').trim()) {
    errors.push('请填写端点地址')
  }
  if (apiKeyRequired.value && !String(props.form.api_key || '').trim() && !props.existingMask) {
    errors.push('该厂商需要填写 API Key')
  }
  requiredFields.value.forEach((key) => {
    if (['base_url', 'api_key', 'auth_mode', 'auth_header_name'].includes(key)) return
    const value = props.form.extra_config?.[key]
    if (value === undefined || value === null || String(value).trim() === '') {
      errors.push(`请填写 ${key}`)
    }
  })
  primaryFields.value.forEach((field) => {
    if (!field.required) return
    const value = props.form.extra_config?.[field.key]
    if (value === undefined || value === null || String(value).trim() === '') {
      errors.push(`请填写 ${field.label || field.key}`)
    }
  })
  return errors
}

defineExpose({ validate })
</script>

<style scoped>
.dynamic-provider-form {
  max-width: 640px;
}
.advanced-collapse {
  margin-bottom: 16px;
  background: transparent;
}
.advanced-collapse :deep(.ant-collapse-header) {
  padding: 10px 12px;
  align-items: center;
}
.advanced-title {
  font-size: 13px;
  font-weight: 500;
}
.advanced-collapse :deep(.ant-collapse-content-box) {
  padding: 12px 12px 0;
}
</style>
