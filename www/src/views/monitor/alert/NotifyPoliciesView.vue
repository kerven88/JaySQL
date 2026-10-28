<template>
  <div class="notify-policies">
    <!-- 「新建策略」按钮已上移到父级 Tab 栏右侧（tabBarExtraContent），这里只保留列表，
         避免与 Tab 栏的按钮重复出现两个入口。 -->

    <a-table
      :columns="columns"
      :data-source="policies"
      row-key="id"
      :loading="loading"
      :pagination="{ pageSize: 10 }"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'name'">
          <div class="cell-ellipsis">{{ record.name }}</div>
        </template>
        <template v-if="column.key === 'actions'">
          <div class="cell-ellipsis">
            <a-space wrap :size="4">
              <span v-for="channel in record.channels" :key="channel.id">
                {{ channel.name }}<template v-if="channel.missing">（已删除）</template>
              </span>
            </a-space>
          </div>
        </template>
        <template v-if="column.key === 'created_at'">
          <span>{{ record.created_at || '—' }}</span>
        </template>
        <template v-if="column.key === 'created_by'">
          <span>{{ record.created_by || '—' }}</span>
        </template>
        <template v-if="column.key === 'enabled'">
          {{ record.enabled ? '启用' : '停用' }}
        </template>
        <template v-if="column.key === 'ops'">
          <a-space class="gi-table-actions">
            <a-button
              type="link"
              size="small"
              :loading="testingId === record.id"
              @click="runPolicyTest(record)"
            >
              测试
            </a-button>
            <a-button type="link" size="small" @click="openModal(record)">编辑</a-button>
            <a-button type="link" size="small" danger @click="removePolicy(record)">删除</a-button>
          </a-space>
        </template>
      </template>
    </a-table>

    <a-modal
      v-model:open="modal.open"
      :title="modal.form.id ? '编辑通知策略' : '新建通知策略'"
      width="880px"
      class="np-modal"
      wrap-class-name="np-modal-wrap"
      :confirm-loading="saving"
      @ok="submit"
    >
      <a-form layout="vertical" class="np-form">
        <!-- 01 策略基础信息 -->
        <div class="form-sec">
          <div class="sec-hd">
            <span class="sec-idx">01</span>
            <span class="sec-title">策略基础信息</span>
          </div>
          <a-row :gutter="16" class="basic-row">
            <a-col :span="12">
              <a-form-item label="策略名称" required>
                <a-input v-model:value="modal.form.name" placeholder="如：生产核心库 P0 即时通知" />
              </a-form-item>
            </a-col>
            <a-col :span="6">
              <a-form-item label="优先级">
                <a-input-number v-model:value="modal.form.priority" class="full-width" :min="1" :max="999" />
              </a-form-item>
            </a-col>
            <a-col :span="6">
              <a-form-item label="启用">
                <a-switch v-model:checked="modal.form.enabled" />
              </a-form-item>
            </a-col>
          </a-row>
        </div>

        <!-- 02 生效条件：可折叠，默认收起 -->
        <div class="form-sec">
          <div class="sec-hd">
            <span class="sec-idx">02</span>
            <span class="sec-title">生效条件</span>
          </div>
          <div class="cond" :class="{ 'is-open': condOpen, 'is-custom': hasCustomCondition }">
            <div class="cond-hd" @click="toggleCond">
              <span class="cond-caret"><RightOutlined /></span>
              <span class="cond-summary">{{ condHeadText }}</span>
              <a-tag v-if="hasCustomCondition" color="orange" class="cond-badge">已自定义过滤条件</a-tag>
              <span class="cond-spacer"></span>
              <span class="cond-toggle">{{ condOpen ? '收起' : '展开' }}</span>
            </div>

            <div v-show="condOpen" class="cond-bd">
              <a-row :gutter="16">
                <a-col :span="12">
                  <a-form-item label="告警级别">
                    <a-select
                      v-model:value="modal.form.match_levels"
                      mode="multiple"
                      allow-clear
                      placeholder="不限"
                      :options="levelOptions"
                    />
                  </a-form-item>
                </a-col>
                <a-col :span="12">
                  <a-form-item label="数据库类型">
                    <a-select
                      v-model:value="modal.form.match_db_types"
                      mode="multiple"
                      allow-clear
                      placeholder="不限"
                      :options="dbTypeOptions"
                    />
                  </a-form-item>
                </a-col>
              </a-row>

              <a-row :gutter="16">
                <a-col :span="12">
                  <a-form-item label="指标层级">
                    <a-select
                      v-model:value="modal.form.match_layers"
                      mode="multiple"
                      allow-clear
                      placeholder="不限"
                      :options="layerOptions"
                    />
                  </a-form-item>
                </a-col>
                <a-col :span="12">
                  <a-form-item label="所属业务">
                    <a-select
                      v-model:value="modal.form.match_business"
                      mode="multiple"
                      allow-clear
                      placeholder="不限"
                      :options="businessOptions"
                    />
                  </a-form-item>
                </a-col>
              </a-row>

              <a-row :gutter="16">
                <a-col :span="12">
                  <a-form-item label="所属环境">
                    <a-select
                      v-model:value="modal.form.match_envs"
                      mode="multiple"
                      allow-clear
                      placeholder="不限"
                      :options="envOptions"
                    />
                  </a-form-item>
                </a-col>
                <a-col :span="12">
                  <a-form-item label="指定实例">
                    <a-select
                      v-model:value="modal.form.match_instances"
                      mode="multiple"
                      allow-clear
                      show-search
                      option-filter-prop="label"
                      placeholder="不限（按业务 / 环境批量命中）"
                      :options="instanceOptions"
                      :max-tag-count="3"
                    />
                  </a-form-item>
                </a-col>
              </a-row>

              <a-form-item label="告警模板">
                <a-select
                  v-model:value="modal.form.match_template_ids"
                  mode="multiple"
                  allow-clear
                  placeholder="不限"
                  :options="templateOptions"
                />
              </a-form-item>

              <!-- 预览按钮已下线，命中数分支保留：将来若恢复预览入口，这里不用改 -->
              <div v-if="preview" class="panel-foot">
                <span class="preview-text" :class="{ 'is-warn': preview.matched_instances === 0 }">
                  命中 {{ preview.matched_instances }} 个实例 / {{ preview.matched_rules }} 条规则
                </span>
              </div>
              <div class="panel-foot">
                <span class="field-help">留空 = 该维度不限制</span>
              </div>
            </div>
          </div>
        </div>

        <!-- 03 通知动作 -->
        <div class="form-sec">
          <div class="sec-hd">
            <span class="sec-idx">03</span>
            <span class="sec-title">通知动作</span>
          </div>

          <a-form-item label="通知渠道" required>
            <a-select
              v-model:value="modal.form.channel_ids"
              mode="multiple"
              placeholder="选择渠道（可多选）"
              :options="channelOptions"
            />
            <div v-if="!channels.length" class="field-help">
              还没有可用渠道，请先到「通知渠道」Tab 新建。
            </div>
          </a-form-item>

          <a-space size="large" class="action-checks">
            <a-checkbox v-model:checked="modal.form.notify_recovery">恢复后发送通知</a-checkbox>
            <a-checkbox v-model:checked="modal.form.stop_on_ack">人工认领后停止升级</a-checkbox>
          </a-space>

          <!-- 接受人：默认收起，只有渠道里出现邮件这类「必须发给个人」的渠道才自动展开。
               收起只是不展示，值照常提交，不影响上方 submit 的必填校验。 -->
          <a-form-item :required="needSubjects">
            <template #label>
              <span class="field-label">
                接受人
                <a-tooltip :title="SUBJECT_HELP">
                  <QuestionCircleOutlined class="field-help-icon" />
                </a-tooltip>
              </span>
            </template>
            <div class="subj" :class="{ 'is-open': subjectOpen }">
              <div class="subj-hd" @click="toggleSubject">
                <span class="subj-caret"><RightOutlined /></span>
                <span class="subj-summary">{{ subjectSummary }}</span>
                <span class="subj-spacer"></span>
                <span class="subj-toggle">{{ subjectOpen ? '收起' : '展开' }}</span>
              </div>
              <div v-show="subjectOpen" class="subj-bd">
                <NotifySubjectPicker v-model:value="modal.form.subjects" />
                <!-- 只在「个人渠道已选但没选接受人」时才占位提示，其余情况不留空白行 -->
                <div v-if="needSubjects && !modal.form.subjects.length" class="field-help is-warn">
                  已选渠道中含邮件等「只能发给个人」的类型，必须至少选择一个接受人。
                </div>
              </div>
            </div>
          </a-form-item>

          <a-row :gutter="16" class="param-row">
            <a-col :span="6">
              <a-form-item label="重复通知间隔(分钟)">
                <a-input-number v-model:value="modal.form.notify_interval_minutes" class="full-width" :min="0" :max="1440" />
              </a-form-item>
            </a-col>
            <a-col :span="6">
              <a-form-item label="聚合窗口(秒)">
                <a-input-number v-model:value="modal.form.aggregate_window_seconds" class="full-width" :min="0" :max="3600" />
              </a-form-item>
            </a-col>
            <a-col :span="6">
              <a-form-item label="升级等待(分钟)">
                <a-input-number v-model:value="modal.form.escalate_after_minutes" class="full-width" :min="0" :max="1440" />
              </a-form-item>
            </a-col>
            <a-col :span="6">
              <a-form-item label="升级目标策略">
                <a-select
                  v-model:value="modal.form.escalate_policy_id"
                  allow-clear
                  placeholder="不升级"
                  :options="escalateOptions"
                />
              </a-form-item>
            </a-col>
          </a-row>
        </div>
      </a-form>
    </a-modal>

    <!-- 连通性测试结果：派发出去的任务到底发没发成功，一屏说清 -->
    <a-modal v-model:open="testState.open" title="策略连通性测试" :footer="null" width="620px">
      <div class="field-help">
        样例告警已按该策略派发，投递 worker 每 15 秒投递一轮；本窗口自动刷新
        <template v-if="testPending">（仍在投递中…）</template>
      </div>
      <a-empty
        v-if="!testState.tasks.length"
        description="任务已入队，等待首轮投递"
        :image="false"
      />
      <div v-for="task in testState.tasks" :key="task.id" class="notify-task">
        <div class="notify-task-head">
          <a-tag :color="testStatusColor(task.status)">{{ testStatusText(task.status) }}</a-tag>
          <span>{{ task.channel_name }}（{{ task.channel_kind }}）</span>
          <span class="task-desc">{{ task.target_label || task.target_type }}</span>
        </div>
        <div class="task-desc">
          <span v-if="task.attempt">尝试 {{ task.attempt }}/{{ task.max_attempt }}</span>
          <span v-if="task.channel_code"> 返回码 {{ task.channel_code }}</span>
          <span v-if="task.sent_at"> {{ task.sent_at }}</span>
        </div>
        <div v-if="task.last_error" class="notify-task-error">{{ task.last_error }}</div>
      </div>
    </a-modal>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { message, Modal } from 'ant-design-vue'
import { QuestionCircleOutlined, RightOutlined } from '@ant-design/icons-vue'
import {
  getNotifyPoliciesApi,
  createNotifyPolicyApi,
  updateNotifyPolicyApi,
  deleteNotifyPolicyApi,
  previewNotifyPolicyApi,
  testNotifyPolicyApi,
  getNotifyChannelsBriefApi,
  getNotifyTasksApi,
  getMonitorTargetConfigsApi,
  getMonitorAlertTemplatesApi,
} from '@/api/monitor'
import NotifySubjectPicker from '../components/NotifySubjectPicker.vue'
import { notifyChannelKindLabel, notifyChannelKindVisible } from './notifyChannelKinds'

const loading = ref(false)
const saving = ref(false)
const previewing = ref(false)
const policies = ref([])
const channels = ref([])
const preview = ref(null)

const targets = reactive({ rows: [], businesses: [], environments: [], engines: [] })
const templates = ref([])

const columns = [
  { title: '策略名称', key: 'name', width: '16.66%', ellipsis: true },
  { title: '渠道名称', key: 'actions', width: '16.66%' },
  { title: '状态', key: 'enabled', width: '16.66%' },
  { title: '创建人', key: 'created_by', width: '16.66%' },
  { title: '创建时间', key: 'created_at', width: '16.66%' },
  { title: '操作', key: 'ops', width: '16.66%', fixed: 'right' },
]

// ---------- 选项 ----------

const LEVEL_TEXT = { P0: '紧急', P1: '严重', P2: '一般', P3: '提示' }

// 「接受人」标签旁的说明气泡。与实际交互的对应关系：
// 没勾邮件渠道 → 区域默认收起、必填星号不亮、submit 也不校验，确实不需要填。
// 口径说明：文案写的是「邮箱」，而代码里的必填判定用的是渠道的 personalized 标记
// （needSubjects），目前后端只有 email 渠道是 personalized，两者等价；
// 若将来新增其它「只能发给个人」的渠道（如短信单人），必填会以 personalized 为准，
// 与这句文案可能出现偏差——届时同步改文案即可，不要改校验规则。
const SUBJECT_HELP = '没有选择邮箱的途径，不需要选择接受人信息'
const levelOptions = ['P0', 'P1', 'P2', 'P3'].map((value) => ({ label: `${value} ${LEVEL_TEXT[value] || ''}`.trim(), value }))

const LAYER_TEXT = { host: '主机', engine: '数据库', kernel: '引擎' }
const layerOptions = Object.entries(LAYER_TEXT).map(([value, label]) => ({ label, value }))

const dbTypeOptions = computed(() => targets.engines.map((item) => ({ label: item.label, value: item.label })))
const businessOptions = computed(() => targets.businesses.map((value) => ({ label: value, value })))
const envOptions = computed(() => targets.environments.map((value) => ({ label: value, value })))
const instanceOptions = computed(() => targets.rows.map((row) => ({
  label: `${row.target || row.instance_id}（${row.db_type || '—'}${row.environment_name ? ' · ' + row.environment_name : ''}）`,
  value: row.instance_id,
})))
const templateOptions = computed(() => templates.value.map((item) => ({
  label: `${item.display_name || item.name}（${item.db_type_label || item.db_type}）`,
  value: item.id,
})))

// 渠道选择只给 UI 可见类型（email 已隐藏，后端能力保留）；已绑定隐藏渠道的
// 存量策略回显时不被过滤掉，仍能展示与保存，避免 select 值悬空。
const selectableChannels = computed(() => channels.value.filter((item) => notifyChannelKindVisible(item.kind)))
const channelOptions = computed(() => selectableChannels.value.map((item) => ({
  label: `${item.name}（${notifyChannelKindLabel(item.kind, item.kind_label)}${item.enabled ? '' : ' · 已停用'}${item.personalized ? ' · 需接受人' : ''}）`,
  value: item.id,
})))

// 选中"只能发给个人"的渠道时，接受人必填——与后端校验同口径，避免保存后才报错
const needSubjects = computed(() => modal.form.channel_ids.some((id) => channels.value.find((item) => item.id === id)?.personalized))

// 只有邮件这类「发给个人」的渠道才需要挑接受人；群机器人渠道不需要，默认收起省一屏噪音
const hasEmailChannel = computed(() => modal.form.channel_ids.some((id) => {
  const channel = channels.value.find((item) => item.id === id)
  return channel?.kind === 'email'
}))

// 收起时也让人知道里面有没有东西，避免"以为没配"
const subjectSummary = computed(() => {
  const count = modal.form.subjects.length
  if (!count) return needSubjects.value ? '未选择（邮件渠道必填）' : '未选择'
  const users = modal.form.subjects.filter((item) => item.type === 'user').length
  const orgs = modal.form.subjects.filter((item) => item.type === 'org').length
  const raws = count - users - orgs
  const parts = []
  if (users) parts.push(`${users} 个接受人`)
  if (orgs) parts.push(`${orgs} 个用户组`)
  if (raws) parts.push(`${raws} 个外部地址`)
  return `已选 ${parts.join(' · ')}`
})

// 升级目标只能选"别的"策略，避免自己升自己形成环
const escalateOptions = computed(() => policies.value
  .filter((item) => item.id !== modal.form.id)
  .map((item) => ({ label: item.name, value: item.id })))

// ---------- 表单 ----------

const modal = reactive({ open: false, form: createForm() })

function createForm(record = {}) {
  return {
    id: record.id || 0,
    name: record.name || '',
    enabled: record.enabled ?? true,
    priority: record.priority ?? 100,
    match_levels: [...(record.match_levels || [])],
    match_db_types: [...(record.match_db_types || [])],
    match_business: [...(record.match_business || [])],
    match_envs: [...(record.match_envs || [])],
    match_instances: [...(record.match_instances || [])],
    match_layers: [...(record.match_layers || [])],
    match_template_ids: [...(record.match_template_ids || [])],
    channel_ids: [...(record.channel_ids || [])],
    subjects: (record.subjects || []).map((item) => ({ ...item })),
    // 存量数据映射：旧 silence/repeat 双参数由后端统一为 notify_interval_minutes（=旧 repeat）
    // 新建时给 30 分钟作默认节奏（多数策略不需要"每条告警都发一次"），编辑时以库里值为准
    notify_interval_minutes: record.id ? (record.notify_interval_minutes ?? 0) : 30,
    aggregate_window_seconds: record.aggregate_window_seconds ?? 0,
    escalate_after_minutes: record.escalate_after_minutes ?? 0,
    escalate_policy_id: record.escalate_policy_id || undefined,
    stop_on_ack: record.stop_on_ack ?? true,
    notify_recovery: record.notify_recovery ?? true,
  }
}

// ---------- 生效条件折叠面板 ----------

// 默认收起：多数策略不设过滤条件，收起后主阅读顺序聚焦「基础信息 → 通知动作」，
// 需要精细限定时再展开，避免 7 个筛选项一进来就占满首屏
const condOpen = ref(false)

// 头部摘要：把散在 7 个字段里的条件压成一句话，收起状态下也能看出命中范围
const conditionSummary = computed(() => {
  const f = modal.form
  const parts = []
  if (f.match_levels.length) parts.push(f.match_levels.join('/'))
  if (f.match_db_types.length) parts.push(f.match_db_types.join('/'))
  if (f.match_envs.length) parts.push(f.match_envs.join('/'))
  if (f.match_business.length) parts.push(f.match_business.join('/'))
  if (f.match_layers.length) parts.push(f.match_layers.map((v) => LAYER_TEXT[v] || v).join('/'))
  if (f.match_instances.length) parts.push(`${f.match_instances.length} 个实例`)
  if (f.match_template_ids.length) parts.push(`${f.match_template_ids.length} 个模板`)
  return parts.length ? parts.join(' · ') : '全部不限'
})

// 任一维度被限定即视为自定义；头部据此亮橙色标记提示「不再是全量生效」
const hasCustomCondition = computed(() => conditionSummary.value !== '全部不限')

// 头部左侧文案：与「接受人」折叠面板同一套写法——没自定义就写「默认条件」，
// 自定义过就直接把条件摘要摆出来，收起状态下也能一眼看出过滤了什么
const condHeadText = computed(() => (hasCustomCondition.value ? conditionSummary.value : '默认条件'))

const toggleCond = () => {
  condOpen.value = !condOpen.value
}

// ---------- 接受人折叠：默认收起，选了邮件渠道才展开 ----------

const subjectOpen = ref(false)

const toggleSubject = () => {
  subjectOpen.value = !subjectOpen.value
}

// 渠道一变就重算折叠态：勾上邮件自动展开方便挑人，去掉邮件立刻收回，
// 群机器人渠道不需要接受人，收起后主流程只剩「渠道 → 频率参数」两步
watch(
  () => modal.form.channel_ids,
  () => {
    subjectOpen.value = hasEmailChannel.value
  },
)

const load = async () => {
  loading.value = true
  try {
    // 请求层 resolve 的是响应体 { code, data, message }，载荷在 .data 这一层
    const res = await getNotifyPoliciesApi()
    policies.value = res?.data?.policies || []
  } catch {
    // 错误提示由请求层统一弹出
  } finally {
    loading.value = false
  }
}

const loadOptions = async () => {
  // 三个下拉数据源互相独立：任一失败不阻断策略列表
  try {
    const res = await getNotifyChannelsBriefApi()
    channels.value = res?.data?.channels || []
  } catch {
    channels.value = []
  }
  try {
    const res = await getMonitorTargetConfigsApi({})
    const payload = res?.data || {}
    targets.rows = payload.targets || []
    targets.environments = payload.filters?.environments || []
    targets.engines = (payload.filters?.engines || []).filter((item) => item.implemented)
    // 业务清单后端未单列，从实例行去重得到（与实例实际取值一致，不会出现选了没人用的业务）
    targets.businesses = [...new Set(targets.rows.map((row) => row.business).filter(Boolean))]
  } catch {
    targets.rows = []
  }
  try {
    const res = await getMonitorAlertTemplatesApi({})
    templates.value = res?.data?.templates || []
  } catch {
    templates.value = []
  }
}

load()
loadOptions()

const openModal = (record) => {
  modal.form = createForm(record || {})
  preview.value = null
  // 存量策略若已配置过滤条件则直接展开，否则用户看不到历史配置，还误以为没生效
  condOpen.value = hasCustomCondition.value
  // 编辑既有策略时按当前渠道还原折叠态，邮件渠道直接展开，避免看不到历史接受人
  subjectOpen.value = hasEmailChannel.value
  modal.open = true
  // 弹窗打开时刷新下拉数据源：渠道/模板可能在其他 Tab 里刚被增改
  loadOptions()
}

const runPreview = async () => {
  // 预览结果展示在折叠面板底部，先展开再请求，避免"点了没反应"
  condOpen.value = true
  previewing.value = true
  try {
    const res = await previewNotifyPolicyApi(buildPayload())
    preview.value = res?.data || null
  } catch {
    preview.value = null
  } finally {
    previewing.value = false
  }
}

const buildPayload = () => ({
  ...modal.form,
  escalate_policy_id: modal.form.escalate_policy_id || 0,
})

const submit = async () => {
  if (!modal.form.name.trim()) {
    message.warning('请填写策略名称')
    return
  }
  if (!modal.form.channel_ids.length) {
    message.warning('请至少选择一个通知渠道')
    return
  }
  if (needSubjects.value && !modal.form.subjects.length) {
    message.warning('所选渠道包含必须指定接受人的渠道（如邮件），请至少选择一个接受人')
    return
  }
  saving.value = true
  try {
    const payload = buildPayload()
    if (payload.id) {
      await updateNotifyPolicyApi(payload.id, payload)
    } else {
      await createNotifyPolicyApi(payload)
    }
    message.success('策略已保存')
    modal.open = false
    await load()
  } catch {
    // 保存失败原因（如"个人渠道必须有接受人"）由请求层展示
  } finally {
    saving.value = false
  }
}

const removePolicy = (record) => {
  Modal.confirm({
    title: `删除策略「${record.name}」？`,
    content: '删除后该策略不再产生新通知，已产生的投递记录会保留。',
    okText: '删除',
    okType: 'danger',
    cancelText: '取消',
    onOk: async () => {
      try {
        await deleteNotifyPolicyApi(record.id)
        message.success('已删除')
        await load()
      } catch {
        // 作为升级目标被引用时后端会拒绝并说明原因
      }
    },
  })
}

// ---------- 连通性测试 ----------

// 测试与"渠道测试"的分工：渠道测试只发固定文案验证 webhook 通不通；
// 策略测试走生产同一条链路（策略 → 渠道 → 接受人解析 → 渲染 → 投递任务），
// 用来回答"这条策略到底配到能发出去没有"。
const testingId = ref(0)
const testState = reactive({ open: false, tasks: [], fingerprint: '', timer: null, rounds: 0 })

const TEST_STATUS_TEXT = { pending: '待投递', sending: '投递中', success: '已送达', failed: '失败', skipped_silent: '静默跳过' }
const TEST_STATUS_COLOR = { pending: 'default', sending: 'blue', success: 'green', failed: 'red', skipped_silent: 'gold' }
const testStatusText = (status) => TEST_STATUS_TEXT[status] || status
const testStatusColor = (status) => TEST_STATUS_COLOR[status] || 'default'
const testPending = computed(() => testState.tasks.some((item) => item.status === 'pending' || item.status === 'sending'))

const stopTestPolling = () => {
  if (testState.timer) {
    clearInterval(testState.timer)
    testState.timer = null
  }
}

const loadTestTasks = async () => {
  try {
    const res = await getNotifyTasksApi({ fingerprint: testState.fingerprint, limit: 20 })
    testState.tasks = res?.data?.tasks || []
  } catch {
    // 轮询失败保留上一次结果，等下一轮
  }
  testState.rounds += 1
  // 最多轮询 60 秒：避免窗口常开时无限打接口
  if (!testPending.value || testState.rounds >= 12) stopTestPolling()
}

const runPolicyTest = async (record) => {
  testingId.value = record.id
  try {
    const res = await testNotifyPolicyApi(record.id)
    const payload = res?.data || {}
    stopTestPolling()
    testState.fingerprint = payload.fingerprint || ''
    testState.tasks = []
    testState.rounds = 0
    testState.open = true
    await loadTestTasks()
    if (testPending.value) testState.timer = setInterval(loadTestTasks, 5000)
  } catch {
    // "未生成任何投递任务"等失败原因由请求层提示
  } finally {
    testingId.value = null
  }
}

onBeforeUnmount(stopTestPolling)

// ---------- 列表展示 ----------

// 暴露给父级 Tab 栏：新建按钮由父组件代为触发
defineExpose({ reload: load, openCreate: () => openModal() })
</script>

<style scoped>
.notify-policies {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* 「新建策略」按钮已上移到父级 Tab 栏，.tab-toolbar 随之移除（无其它引用点）。 */

/* 单行省略：等宽列下防止长名称/多渠道把行撑高或截断无提示 */
.cell-ellipsis {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-desc {
  font-size: 12px;
  color: var(--gi-text-tertiary, #8c8c8c);
}

/* ---------- 弹窗：统一内边距，让表单有稳定呼吸 ---------- */

.np-modal :deep(.ant-modal-body) {
  padding: 20px 24px 16px;
}

/* ---------- 表单：统一标签、控件、间距 ---------- */

/* 三段区块用 flex gap 统一间距，避免 margin 硬编码与塌陷不一致 */
.np-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* form-item 默认 24px 下边距，统一收紧为 16px，建立更紧凑的垂直节奏 */
.np-form :deep(.ant-form-item) {
  margin-bottom: 16px;
}

.np-form :deep(.ant-form-item-label) {
  padding-bottom: 6px;
  line-height: 1.5;
}

.np-form :deep(.ant-form-item-label > label) {
  color: var(--gi-text-secondary, #5a6b7a);
  font-weight: 500;
  font-size: 13px;
}

/* 必填星号统一在标签文字左侧，颜色/间距一致 */
.np-form :deep(.ant-form-item-required::before) {
  display: inline-block;
  margin-right: 4px;
  color: #ff4d4f;
  font-size: 12px;
  line-height: 1;
}

/* 控件统一占满列宽，避免输入框、选择器、数字框宽度参差 */
.np-form :deep(.ant-input),
.np-form :deep(.ant-input-number),
.np-form :deep(.ant-select),
.np-form :deep(.ant-select-selector) {
  width: 100%;
}

/* 控件输入区统一 32px 基线 */
.np-form :deep(.ant-form-item-control-input) {
  min-height: 32px;
}

/* 首行三控件：input/input-number 固定 32px，switch 用 flex 垂直居中，实现三者同高同中线 */
.np-form .basic-row :deep(.ant-input),
.np-form .basic-row :deep(.ant-input-number) {
  height: 32px;
}

.np-form .basic-row :deep(.ant-form-item-control-input) {
  display: flex;
  align-items: center;
  min-height: 32px;
}

/* 压缩 01 基础信息末尾的表单下边距，让中间空白区域更紧凑 */
.np-form .basic-row :deep(.ant-form-item) {
  margin-bottom: 8px;
}

/* ---------- 三段式区块：01 基础信息 / 02 生效条件 / 03 通知动作 ---------- */

.form-sec {
  margin-bottom: 0;
}

.sec-hd {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.sec-idx {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: none;
  width: 20px;
  height: 20px;
  border-radius: 6px;
  background: #f0f2f5;
  color: var(--gi-text-secondary, #5a6b7a);
  font-size: 12px;
  font-weight: 600;
}

.sec-title {
  font-size: 14px;
  font-weight: 600;
}


/* ---------- 生效条件折叠面板 ---------- */

.cond {
  border: 1px solid var(--gi-border-color, #e5e7eb);
  border-radius: 8px;
  background: #fff;
}

/* 已自定义过滤条件时整框描边转橙，与头部橙色标记呼应 */
.cond.is-custom {
  border-color: #ffd8a8;
}

.cond-hd {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  cursor: pointer;
  user-select: none;
}

.cond-caret {
  display: inline-flex;
  color: var(--gi-text-tertiary, #8c8c8c);
  font-size: 12px;
  transition: transform 0.18s ease;
}

.cond.is-open .cond-caret {
  transform: rotate(90deg);
}

/* 头部左侧文案与「接受人」面板同一口径：淡灰、13px 以下、不加粗 */
.cond-summary {
  font-size: 12px;
  color: var(--gi-text-tertiary, #8c8c8c);
}

/* 头部右侧「展开 / 收起」，与 .subj-toggle 完全同款 */
.cond-spacer {
  flex: 1;
}

.cond-toggle {
  font-size: 12px;
  color: var(--gi-text-tertiary, #8c8c8c);
}

.cond-badge {
  margin: 0;
}

.cond-bd {
  padding: 16px;
  border-top: 1px solid var(--gi-border-color, #e5e7eb);
}

/* 7 个筛选项统一 16px 垂直节奏 */
.cond-bd > :deep(.ant-form-item) {
  margin-bottom: 16px;
}

/* 面板底部只剩一行说明，不再给已下线的预览结果留间距 */
.panel-foot {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 0;
}

.preview-text {
  font-size: 12px;
  color: #0f9f6f;
}

.preview-text.is-warn {
  color: #d97706;
}

.field-help {
  font-size: 12px;
  color: var(--gi-text-tertiary, #8c8c8c);
  line-height: 1.6;
}

.field-help.is-warn {
  color: #d97706;
}

/* 标签与提示图标：与「通知渠道」Tab（NotifyChannelsView）的字段帮助图标同一套尺寸/颜色/间距 */
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

/* 复选框组紧跟「通知渠道」，与下方「接受人」保持 16px 统一间距 */
.action-checks {
  display: flex;
  align-items: center;
  min-height: 32px;
  margin-top: 0;
  margin-bottom: 16px;
}

/* 底部 4 栏参数统一控件高度：前三个是 input-number（32px），第四个是 select，
   AntD 单选 select 在全局样式下会被撑到 40px，这里显式压回 32px 并对齐内部行高 */
.np-form .param-row :deep(.ant-input-number),
.np-form .param-row :deep(.ant-select),
.np-form .param-row :deep(.ant-select-selector) {
  min-height: 32px;
  height: 32px;
}

.np-form .param-row :deep(.ant-select-selector .ant-select-selection-item),
.np-form .param-row :deep(.ant-select-selector .ant-select-selection-placeholder),
.np-form .param-row :deep(.ant-select-selector .ant-select-selection-search-input) {
  line-height: 30px;
}

/* ---------- 接受人折叠面板：与 02 生效条件同一套折叠观感 ---------- */

.subj {
  border: 1px solid var(--gi-border-color, #e5e7eb);
  border-radius: 8px;
  background: #fff;
}

.subj-hd {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  cursor: pointer;
  user-select: none;
}

.subj-caret {
  display: inline-flex;
  color: var(--gi-text-tertiary, #8c8c8c);
  font-size: 12px;
  transition: transform 0.18s ease;
}

.subj.is-open .subj-caret {
  transform: rotate(90deg);
}

.subj-summary {
  font-size: 12px;
  color: var(--gi-text-tertiary, #8c8c8c);
}

.subj-spacer {
  flex: 1;
}

.subj-toggle {
  font-size: 12px;
  color: var(--gi-text-tertiary, #8c8c8c);
}

.subj-bd {
  padding: 14px;
  border-top: 1px solid var(--gi-border-color, #e5e7eb);
}

.notify-task {
  padding: 8px 0;
  border-bottom: 1px dashed var(--gi-border-color, #e5e7eb);
}

.notify-task:last-child {
  border-bottom: none;
}

.notify-task-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.notify-task-error {
  margin-top: 4px;
  font-size: 12px;
  color: #d64545;
  word-break: break-all;
}

.full-width {
  width: 100%;
}
</style>

<!-- 弹窗滚动层级：wrap / content 由 AntD 渲染，不在 scoped 作用域内，单独用非 scoped 块处理。
     目标：纵向滚动条只出现在弹窗内容区，父容器（.ant-modal-wrap）不再产生第二条滚动条。 -->
<style>
.np-modal-wrap {
  display: flex;
  align-items: center;
  justify-content: center;
  /* 父容器不再滚动：关掉 wrap 自带的 overflow:auto，滚动交给 body */
  overflow: hidden !important;
}

.np-modal-wrap .ant-modal {
  margin: 0 auto;
  top: auto;
  padding-bottom: 0;
  overflow: hidden;
}

/* 弹窗整体限高但不写死高度：内容少时贴着内容，内容超了才让 body 滚 */
.np-modal-wrap .ant-modal-content {
  display: flex;
  flex-direction: column;
  max-height: calc(100vh - 64px);
  overflow: hidden;
}

.np-modal-wrap .ant-modal-header,
.np-modal-wrap .ant-modal-footer {
  flex: none;
}

.np-modal-wrap .ant-modal-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
}
</style>
