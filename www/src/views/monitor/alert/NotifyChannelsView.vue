<template>
  <div class="notify-channels">
    <!-- 「新建渠道」与「全部类型」下拉已上移到父级 Tab 栏右侧（tabBarExtraContent），
         这里只保留内容区，避免同一页出现两处操作重心。 -->

    <!-- 【平台默认（通知地址前缀 · 邮件默认 SMTP）】折叠面板已移除（2026-10-18）：
         该配置属于全局兜底值，与渠道列表混在一起会挤占首屏，且极少改动。
         移除后这里只剩渠道表格，上方不留空容器（gap 只在多子元素间生效）。 -->

    <a-table
      :columns="columns"
      :data-source="filteredChannels"
      row-key="id"
      :loading="loading"
      :pagination="{ pageSize: 10 }"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'name'">
          <div>{{ record.name }}</div>
          <div v-if="record.scope_value">{{ record.scope_value }}</div>
        </template>
        <template v-if="column.key === 'kind'">
          <span>{{ kindShortLabel(record.kind) }}</span>
        </template>
        <template v-if="column.key === 'enabled'">
          <span>{{ record.enabled ? '启用' : '停用' }}</span>
        </template>
        <template v-if="column.key === 'health'">
          <!-- 只展示健康度状态：熔断停用 > 测试结果 > 未测试 -->
          <span v-if="record.fail_streak >= 5">连续失败 {{ record.fail_streak }} 次，已自动停用</span>
          <span v-else-if="record.last_test_at">{{ record.last_test_ok ? '测试通过' : '测试失败' }}</span>
          <span v-else>未测试</span>
        </template>
        <template v-if="column.key === 'created_at'">
          <span>{{ record.created_at || '—' }}</span>
        </template>
        <template v-if="column.key === 'created_by'">
          <span>{{ record.created_by || '—' }}</span>
        </template>
        <template v-if="column.key === 'actions'">
          <a-space class="gi-table-actions">
            <a-button type="link" size="small" :loading="testingId === record.id" @click="testChannel(record)">测试</a-button>
            <a-button type="link" size="small" @click="openModal(record)">编辑</a-button>
            <a-button type="link" size="small" danger @click="removeChannel(record)">删除</a-button>
          </a-space>
        </template>
      </template>
    </a-table>

    <a-modal
      v-model:open="modal.open"
      :title="modal.form.id ? '编辑通知渠道' : '新建通知渠道'"
      width="680px"
      :body-style="{ maxHeight: '70vh', overflowY: 'auto' }"
      :confirm-loading="saving"
      @ok="submit"
    >
      <a-form layout="vertical">
        <a-row :gutter="16">
          <a-col :span="12">
            <a-form-item label="渠道名称" required>
              <a-input v-model:value="modal.form.name" placeholder="如：核心库告警群 / DBA 值班邮件" />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="渠道类型" required>
              <a-select
                v-model:value="modal.form.kind"
                :options="kindOptions"
                :disabled="!!modal.form.id"
                @change="onKindChange"
              />
            </a-form-item>
          </a-col>
        </a-row>

        <a-alert v-if="currentMeta?.description" type="info" show-icon class="kind-tip" :message="currentMeta.description" />

        <!-- 渠道表单由后端元数据驱动：新增渠道类型（短信/电话）无需改前端。
             列宽均匀分配（窄屏自动降为单列），textarea 恒占整行 -->
        <a-row :gutter="16">
          <a-col
            v-for="field in currentMeta?.fields || []"
            :key="field.key"
            :xs="24"
            :sm="field.type === 'textarea' ? 24 : 12"
          >
            <a-form-item :required="field.required && field.type !== 'bool'">
              <template v-if="field.type !== 'bool'" #label>
                <span class="field-label">
                  {{ field.label }}
                  <a-tooltip v-if="field.help" :title="field.help">
                    <QuestionCircleOutlined class="field-help-icon" />
                  </a-tooltip>
                </span>
              </template>
              <!-- 布尔字段：文字与开关同行内联（label 在上的 antd 结构会让
                   开关与文字分居两行，与相邻字段基线错位），帮助图标随文字同行 -->
              <div v-if="field.type === 'bool'" class="bool-row">
                <span class="bool-group">
                  <span class="bool-label">
                    {{ field.label }}
                    <a-tooltip v-if="field.help" :title="field.help">
                      <QuestionCircleOutlined class="field-help-icon" />
                    </a-tooltip>
                  </span>
                  <a-switch v-model:checked="modal.form.config[field.key]" />
                </span>
                <!-- 渠道启停开关：跟在「@所有人」后面同一组并排，
                     不单独占一行。列表里只是文本展示，没有切换入口，
                     渠道被熔断（连续失败 5 次）置为停用后就再也开不回来，这里补上入口。 -->
                <span v-if="field.key === inlineEnableKey" class="bool-group">
                  <span class="bool-label">开启渠道</span>
                  <a-switch v-model:checked="modal.form.enabled" />
                </span>
              </div>
              <a-input-number
                v-else-if="field.type === 'number'"
                v-model:value="modal.form.config[field.key]"
                class="full-width"
                :min="0"
              />
              <a-input-password
                v-else-if="field.type === 'password'"
                v-model:value="modal.form.config[field.key]"
                :placeholder="modal.form.config[field.key] === MASK ? '已配置，留空或保持掩码表示不修改' : field.placeholder"
              />
              <a-input
                v-else
                v-model:value="modal.form.config[field.key]"
                :placeholder="field.placeholder || ''"
              />
            </a-form-item>
          </a-col>
          <!-- 没有「@所有人」字段的渠道类型（如邮件）：单独占一格，同样内联结构 -->
          <a-col v-if="currentMeta && !inlineEnableKey" :xs="24" :sm="12">
            <a-form-item>
              <div class="bool-row">
                <span class="bool-group">
                  <span class="bool-label">开启渠道</span>
                  <a-switch v-model:checked="modal.form.enabled" />
                </span>
              </div>
            </a-form-item>
          </a-col>
        </a-row>

        <a-collapse class="params-collapse">
          <a-collapse-panel key="message-template" header="告警消息模板（Markdown）">
            <NotifyMessageTemplateEditor v-if="modal.open" v-model="modal.form.config.message_template" :kind="modal.form.kind" :key="`${modal.form.id}:${modal.form.kind}`" />
          </a-collapse-panel>
        </a-collapse>

        <!-- 参数默认收起：非必填的投递调优项，需要时再展开 -->
        <a-collapse class="params-collapse">
          <a-collapse-panel key="params" header="参数">
            <a-row :gutter="16">
              <a-col :xs="24" :sm="8">
                <a-form-item label="超时(秒)">
                  <a-input-number v-model:value="modal.form.timeout_seconds" class="full-width" :min="1" :max="60" />
                </a-form-item>
              </a-col>
              <a-col :xs="24" :sm="8">
                <a-form-item label="最大尝试次数">
                  <a-input-number v-model:value="modal.form.max_attempt" class="full-width" :min="1" :max="8" />
                </a-form-item>
              </a-col>
              <a-col :xs="24" :sm="8">
                <a-form-item label="每分钟上限(0=不限)">
                  <a-input-number v-model:value="modal.form.rate_limit_per_min" class="full-width" :min="0" :max="600" />
                </a-form-item>
              </a-col>
            </a-row>
          </a-collapse-panel>
        </a-collapse>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup>
import { computed, reactive, ref } from 'vue'
import { message, Modal } from 'ant-design-vue'
import { QuestionCircleOutlined } from '@ant-design/icons-vue'
import NotifyMessageTemplateEditor from './NotifyMessageTemplateEditor.vue'
import {
  getNotifyChannelsApi,
  createNotifyChannelApi,
  updateNotifyChannelApi,
  deleteNotifyChannelApi,
  testNotifyChannelApi,
} from '@/api/monitor'
import { NOTIFY_CHANNEL_KINDS_VISIBLE, notifyChannelKindLabel, notifyChannelKindVisible } from './notifyChannelKinds'

// 后端回显的密钥占位符：提交时原样带回表示"保持原值不变"
const MASK = '******'

const loading = ref(false)
const saving = ref(false)
const testingId = ref(null)
// 渠道类型筛选：下拉已上移到父级 Tab 栏，值由父组件通过 v-model 持有，
// 过滤逻辑仍留在子组件（只有这里知道渠道清单）。
// 不声明 type：allowClear 清空时值是 undefined，声明 String 会触发 prop 类型告警
const kindFilter = defineModel('kindFilter', { default: undefined })
const channels = ref([])
const kinds = ref([])

// 列宽等分：7 列各 100/7%，全部左对齐；不 fixed，窄屏随容器等比收缩
const columns = [
  { title: '渠道名称', key: 'name', width: '14.28%', align: 'left' },
  { title: '类型', key: 'kind', width: '14.28%', align: 'left' },
  { title: '是否启用', key: 'enabled', width: '14.28%', align: 'left' },
  { title: '状态', key: 'health', width: '14.28%', align: 'left' },
  { title: '创建时间', key: 'created_at', width: '14.28%', align: 'left' },
  { title: '创建人', key: 'created_by', width: '14.28%', align: 'left' },
  { title: '操作', key: 'actions', width: '14.32%', align: 'left' },
]

// 渠道类型展示统一走公共常量 notifyChannelKinds.js：弹窗下拉、类型列、
// 筛选下拉三处同源，名称（钉钉/飞书/企微/邮箱）与顺序完全一致。
const kindShortLabel = (kind) => notifyChannelKindLabel(kind, kinds.value.find((item) => item.kind === kind)?.label)

// 弹窗「渠道类型」下拉：UI 可见类型（公共常量，email 已隐藏），不随后端 meta.label 漂移；
// 选中后表单字段仍由后端 meta 驱动（currentMeta 按 kind 查找）。
const kindOptions = NOTIFY_CHANNEL_KINDS_VISIBLE.map((item) => ({ label: item.label, value: item.kind }))
// 筛选下拉：同源常量取 UI 可见子集，只保留后端实际支持的类型，顺序不变
const kindFilterOptions = computed(() => (
  NOTIFY_CHANNEL_KINDS_VISIBLE
    .filter((item) => kinds.value.some((meta) => meta.kind === item.kind))
    .map((item) => ({ label: item.label, value: item.kind }))
))
const filteredChannels = computed(() => (kindFilter.value
  ? channels.value.filter((item) => item.kind === kindFilter.value)
  : channels.value.filter((item) => notifyChannelKindVisible(item.kind))))

const modal = reactive({
  open: false,
  form: createForm(),
})

const currentMeta = computed(() => kinds.value.find((item) => item.kind === modal.form.kind))

// 启停开关的挂载位置：优先贴着「@所有人」同一行；
// 该字段不是每个渠道都有（邮件就没有），没有时回落到单独占一格。
const inlineEnableKey = computed(() => {
  const fields = currentMeta.value?.fields || []
  return fields.some((item) => item.key === 'at_all') ? 'at_all' : ''
})

function createForm(record = {}) {
  const config = {}
  Object.entries(record.config || {}).forEach(([key, value]) => {
    config[key] = value
  })
  return {
    id: record.id || 0,
    name: record.name || '',
    kind: record.kind || 'dingtalk',
    enabled: record.enabled ?? true,
    scope_type: record.scope_type || 'global',
    scope_value: record.scope_value || '',
    timeout_seconds: record.timeout_seconds ?? 8,
    max_attempt: record.max_attempt ?? 4,
    rate_limit_per_min: record.rate_limit_per_min ?? 0,
    aggregate_window_seconds: record.aggregate_window_seconds ?? 0,
    config,
  }
}

const load = async () => {
  loading.value = true
  try {
    // 请求层 resolve 的是响应体 { code, data, message }，载荷在 .data 这一层
    const res = await getNotifyChannelsApi()
    const payload = res?.data || {}
    channels.value = payload.channels || []
    kinds.value = payload.kinds || []
  } catch {
    // 错误提示由请求层统一弹出
  } finally {
    loading.value = false
  }
}
load()

// ---- 渠道弹窗 ----：不同渠道的字段完全不同，留着旧值只会造成困惑
const onKindChange = () => {
  modal.form.config = {}
  applyDefaults()
}

const applyDefaults = () => {
  const meta = kinds.value.find((item) => item.kind === modal.form.kind)
  ;(meta?.fields || []).forEach((field) => {
    if (field.default !== undefined && modal.form.config[field.key] === undefined) {
      modal.form.config[field.key] = field.default
    }
  })
}

const openModal = (record) => {
  modal.form = createForm(record || {})
  if (!record) applyDefaults()
  modal.open = true
}

const submit = async () => {
  if (!modal.form.name.trim()) {
    message.warning('请填写渠道名称')
    return
  }
  saving.value = true
  try {
    const payload = { ...modal.form }
    if (payload.id) {
      await updateNotifyChannelApi(payload.id, payload)
    } else {
      await createNotifyChannelApi(payload)
    }
    message.success('渠道已保存')
    modal.open = false
    await load()
  } catch {
    // 错误提示由请求层统一弹出
  } finally {
    saving.value = false
  }
}

const testChannel = async (record) => {
  testingId.value = record.id
  try {
    await testNotifyChannelApi(record.id)
    message.success('测试消息已发送，请到目标渠道确认')
    await load()
  } catch {
    // 测试失败原因已写回渠道健康度，刷新后可见
    await load()
  } finally {
    testingId.value = null
  }
}

const removeChannel = (record) => {
  Modal.confirm({
    title: `删除渠道「${record.name}」？`,
    content: record.policy_refs?.length
      ? `该渠道正被策略「${record.policy_refs.join('、')}」引用，需先解除引用。`
      : '删除后该渠道的投递记录仍会保留，便于追溯历史通知。',
    okText: '删除',
    okType: 'danger',
    cancelText: '取消',
    onOk: async () => {
      try {
        await deleteNotifyChannelApi(record.id)
        message.success('已删除')
        await load()
      } catch {
        // 被引用时后端会给出明确原因
      }
    },
  })
}

// 暴露给父级 Tab 栏：新建按钮由父组件代为触发，类型下拉的选项也取自这里
// （只有本组件持有渠道类型元数据）。expose 的 ref 会被自动解包，父组件可直接当值用。
defineExpose({
  reload: load,
  openCreate: () => openModal(),
  kindOptions: kindFilterOptions,
})
</script>

<style scoped>
.notify-channels {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* 「新建渠道」按钮与「全部类型」下拉已上移到父级 Tab 栏（.tab-toolbar / .filter-select
   随之移除）；【平台默认】折叠面板已删除（.defaults-collapse / .defaults-hint 随之移除）。
   这里不再有第二个内容块，gap 只在多子元素间生效，移除后上方不会留下空行。 */

.kind-tip {
  margin-bottom: 12px;
}

.field-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.field-help-icon {
  color: var(--gi-text-tertiary, #8c8c8c);
  font-size: 13px;
  cursor: help;
}

/* 布尔字段（@所有人 / 开启渠道）：文字与开关同一行内联，垂直居中。
   两个组用同一套 .bool-group 结构，保证基线、字号、间距一致；
   组间 32px 间距，组内文字与开关 8px。控件区本身垂直居中（antd flex），
   行高对齐相邻输入框（32px），不再用负 margin 上提的脆弱 hack。 */
.bool-row {
  display: flex;
  align-items: center;
  gap: 32px;
  min-height: 32px;
}

.bool-group {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.bool-label {
  font-size: 14px;
  line-height: 1.5714;
  font-weight: 400;
  color: var(--gi-text-primary, rgba(0, 0, 0, 0.88));
}

.params-collapse {
  margin-top: 4px;
  background: transparent;
}

.params-collapse :deep(.ant-collapse-header) {
  padding: 8px 0 !important;
  color: var(--gi-text-secondary, #595959);
  font-size: 13px;
}

.params-collapse :deep(.ant-collapse-content-box) {
  padding: 4px 0 0 !important;
}

.full-width {
  width: 100%;
}
</style>
