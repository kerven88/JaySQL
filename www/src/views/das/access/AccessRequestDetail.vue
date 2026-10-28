<template>
  <div class="access-detail-page gi-page-shell">
    <a-card class="detail-top-card" :bordered="false">
      <div class="steps-shell">
        <a-steps :current="currentStep" :status="stepStatus" size="small">
          <a-step title="提交申请" />
          <a-step title="审批" />
          <a-step title="执行授权" />
          <a-step title="完成" />
        </a-steps>
      </div>

      <a-spin :spinning="loading">
        <div class="detail-header">
          <div class="detail-title-block">
            <!-- 第 1 行：标题 + 申请单号 + 复制 + 状态 -->
            <div class="header-title-row">
              <span class="detail-title-text">数据访问申请</span>
              <div v-if="request" class="request-id-block">
                <span class="detail-request-no mono-text">{{ request.request_no }}</span>
                <button
                  type="button"
                  class="copy-request-no-btn"
                  aria-label="复制申请单号"
                  title="复制申请单号"
                  @click.stop="copyRequestNo(request.request_no)"
                >
                  <CopyOutlined />
                </button>
                <a-tag :color="accessStatusColor(effectiveStatus)" class="detail-status-tag">
                  {{ accessStatusText(effectiveStatus) }}
                </a-tag>
              </div>
            </div>

            <!-- 第 2 行：申请人 / 创建时间 + 资源胶囊 + 当前处理人 -->
            <div v-if="request" class="header-content-row">
              <div class="creator-info">
                <span class="creator-name">{{ request.applicant_username || '-' }}</span>
                <span class="creator-sep">创建于</span>
                <span class="creator-time">{{ request.created_at || '-' }}</span>
              </div>

              <div class="tag-group">
                <span class="info-chip" :title="instanceChipFull">
                  <span class="chip-label">数据源</span>
                  <span class="chip-value">{{ instanceChip }}</span>
                </span>
                <span class="info-chip" :title="scopeSummary.database">
                  <span class="chip-label">库</span>
                  <span class="chip-value">{{ scopeSummary.database }}</span>
                </span>
                <span v-if="scopeSummary.schema !== '-'" class="info-chip" :title="scopeSummary.schema">
                  <span class="chip-label">模式</span>
                  <span class="chip-value">{{ scopeSummary.schema }}</span>
                </span>
                <span v-if="scopeSummary.table !== '-'" class="info-chip" :title="scopeSummary.table">
                  <span class="chip-label">表</span>
                  <span class="chip-value">{{ scopeSummary.table }}</span>
                </span>
              </div>

              <div class="approver-inline">
                <span class="info-label">{{ request.status === 'wait_grant' || request.status === 'approved' ? '执行人' : '当前处理人' }}</span>
                <span class="label-colon">：</span>
                <span class="info-value">{{ handlerText }}</span>
              </div>
            </div>
          </div>

          <!-- 右上角操作区：按钮可用性完全由服务端 permissions 决定 -->
          <div v-if="request" class="header-action-slot">
            <a-tooltip :title="disabledTip('grant')">
              <span>
                <a-button v-if="permissions.can_grant" type="primary" :loading="actionLoading" @click="openGrant">
                  执行授权
                </a-button>
              </span>
            </a-tooltip>
            <a-tooltip :title="disabledTip('approve')">
              <span>
                <a-button
                  type="primary"
                  :disabled="!permissions.can_approve"
                  :loading="actionLoading"
                  @click="openApprove"
                >
                  审批通过
                </a-button>
              </span>
            </a-tooltip>
            <a-tooltip :title="disabledTip('reject')">
              <span>
                <a-button danger :disabled="!permissions.can_reject" @click="openReject">
                  审批不通过
                </a-button>
              </span>
            </a-tooltip>
            <a-tooltip :title="disabledTip('forward')">
              <span>
                <a-button :disabled="!permissions.can_forward" @click="openForward">
                  转交
                </a-button>
              </span>
            </a-tooltip>
            <a-tooltip :title="disabledTip('withdraw')">
              <span>
                <a-button :disabled="!permissions.can_withdraw" @click="handleWithdraw">撤回</a-button>
              </span>
            </a-tooltip>
            <a-tooltip :title="disabledTip('terminate')">
              <span>
                <a-button danger :disabled="!permissions.can_terminate" @click="openTerminate">终止</a-button>
              </span>
            </a-tooltip>
            <a-tooltip :title="canApply ? '' : '缺少 das.access.apply 权限'">
              <span>
                <a-button :disabled="!canApply" @click="createSimilar">类似创建</a-button>
              </span>
            </a-tooltip>
          </div>
        </div>
      </a-spin>
    </a-card>

    <!-- 加载失败的兜底：单号不存在 / 无权查看时不要留一片空白 -->
    <a-card v-if="loadError" :bordered="false">
      <a-result status="warning" title="无法打开该申请单" :sub-title="loadError">
        <template #extra>
          <a-button type="primary" @click="goBack">返回列表</a-button>
        </template>
      </a-result>
    </a-card>

    <template v-if="request">
      <a-card size="small" class="detail-section-card" title="申请信息">
        <a-descriptions size="small" :column="2" bordered class="detail-desc">
          <template v-if="request.order_id">
            <a-descriptions-item label="关联工单" :span="2">
              <span class="mono-text">{{ request.order_id }}</span>
              <span v-if="request.order_title" class="detail-order-title">{{ request.order_title }}</span>
            </a-descriptions-item>
          </template>
          <a-descriptions-item label="申请人">{{ request.applicant_username || '-' }}</a-descriptions-item>
          <a-descriptions-item label="申请范围">{{ accessResourceLevelText(request.resource_level) }}</a-descriptions-item>
          <a-descriptions-item label="数据源">{{ request.instance_name || '-' }}</a-descriptions-item>
          <a-descriptions-item label="数据库类型">{{ request.db_type || '-' }}</a-descriptions-item>
          <a-descriptions-item label="库">{{ scopeSummary.database }}</a-descriptions-item>
          <a-descriptions-item label="模式">{{ scopeSummary.schema }}</a-descriptions-item>
          <a-descriptions-item label="表">{{ tableColumnText }}</a-descriptions-item>
          <a-descriptions-item label="申请权限">
            <a-space :size="4">
              <a-tag v-for="item in splitAccessActions(request.actions)" :key="item" :color="accessActionColor(item)">
                {{ accessActionText(item) }}
              </a-tag>
            </a-space>
          </a-descriptions-item>
          <a-descriptions-item label="申请有效期">{{ accessExpireText(request.requested_expire_at) }}</a-descriptions-item>
          <a-descriptions-item label="申请事由" :span="2">{{ request.reason || '-' }}</a-descriptions-item>
          <a-descriptions-item label="申请时间" :span="2">{{ request.created_at || '-' }}</a-descriptions-item>

          <template v-if="request.approver_username">
            <a-descriptions-item label="审批人">
              {{ request.approver_name || request.approver_username }}
            </a-descriptions-item>
            <a-descriptions-item label="审批时间">{{ request.approved_at || '-' }}</a-descriptions-item>
            <a-descriptions-item label="审批意见" :span="2">{{ request.approve_comment || '-' }}</a-descriptions-item>
          </template>

          <template v-if="request.executed_at">
            <a-descriptions-item label="执行授权人">
              {{ request.executor_name || request.executor_username || '-' }}
            </a-descriptions-item>
            <a-descriptions-item label="执行时间">{{ request.executed_at }}</a-descriptions-item>
          </template>

          <template v-if="request.status === 'approved' || request.grant_no">
            <a-descriptions-item label="生效时间">{{ request.effective_at || '-' }}</a-descriptions-item>
            <a-descriptions-item label="失效时间">{{ accessExpireText(request.expire_at) }}</a-descriptions-item>
            <a-descriptions-item label="授权单号" :span="2">
              <!-- 多资源申请单一次会落多条授权行，逐条列出单号与覆盖资源 -->
              <div class="grant-no-list">
                <div v-for="g in grantsList" :key="g.grant_no" class="grant-no-item">
                  <span class="mono-text">{{ g.grant_no }}</span>
                  <span class="grant-no-desc" :title="g.resource_desc">{{ g.resource_desc }}</span>
                  <a-tag :color="g.status === 'active' ? 'green' : 'default'" class="grant-status-tag">
                    {{ grantStatusText(g.status) }}
                  </a-tag>
                </div>
              </div>
            </a-descriptions-item>
          </template>
        </a-descriptions>
      </a-card>

      <a-card size="small" class="detail-section-card" title="流转记录">
        <a-timeline class="detail-timeline">
          <a-timeline-item
            v-for="event in events"
            :key="event.id"
            :color="accessEventColor(event.event_type)"
          >
            <div class="event-title">{{ accessEventText(event.event_type) }}</div>
            <div class="event-meta">
              {{ event.operator_name || event.operator_username || '-' }} · {{ event.created_at }}
            </div>
            <div v-if="event.comment" class="event-comment">{{ event.comment }}</div>
          </a-timeline-item>
        </a-timeline>
        <a-empty v-if="!events.length" description="暂无流转记录" />
      </a-card>
    </template>

    <!-- 审批通过：有效期可调整（默认沿用申请值） -->
    <a-modal
      v-model:open="approveModal.visible"
      title="审批通过"
      width="520px"
      ok-text="确认通过"
      cancel-text="取消"
      :confirm-loading="actionLoading"
      :mask-closable="false"
      @ok="submitApprove"
    >
      <p class="modal-hint">
        通过后本单进入「待执行授权」，申请人
        <b>{{ request?.applicant_username }}</b>
        需由执行人落地权限后才生效。
      </p>
      <a-form layout="vertical">
        <a-form-item label="批准有效期">
          <a-select v-model:value="approveModal.expireDays" :options="expireOptions" />
        </a-form-item>
        <a-form-item label="审批意见">
          <a-textarea v-model:value="approveModal.comment" :rows="2" :maxlength="512" show-count placeholder="选填" />
        </a-form-item>
      </a-form>
    </a-modal>

    <!-- 审批不通过 -->
    <a-modal
      v-model:open="rejectModal.visible"
      title="审批不通过"
      width="480px"
      ok-text="确认驳回"
      cancel-text="取消"
      :confirm-loading="actionLoading"
      :mask-closable="false"
      @ok="submitReject"
    >
      <p class="modal-hint">驳回后本单置为「已驳回」，申请人需重新发起申请。</p>
      <a-textarea
        v-model:value="rejectModal.comment"
        :rows="3"
        :maxlength="512"
        show-count
        placeholder="请填写驳回理由（必填）"
      />
    </a-modal>

    <!-- 转交 -->
    <a-modal
      v-model:open="forwardModal.visible"
      title="转交审批人"
      width="520px"
      ok-text="确认转交"
      cancel-text="取消"
      :confirm-loading="actionLoading"
      :mask-closable="false"
      @ok="submitForward"
    >
      <p class="modal-hint">转交后本单的当前处理人改为新的审批人，本单不退出审批流程。</p>
      <a-form layout="vertical">
        <a-form-item label="转交给" required>
          <a-select
            v-model:value="forwardModal.approver"
            show-search
            option-filter-prop="label"
            :options="forwardApproverOptions"
            :loading="approverLoading"
            placeholder="按账号搜索"
            not-found-content="暂无其他具备审批权限的账号"
          />
        </a-form-item>
        <a-form-item label="转交说明" required>
          <a-textarea
            v-model:value="forwardModal.comment"
            :rows="2"
            :maxlength="512"
            show-count
            placeholder="说明为什么转交（必填），接手人据此判断"
          />
        </a-form-item>
      </a-form>
    </a-modal>

    <!-- 终止 -->
    <a-modal
      v-model:open="terminateModal.visible"
      title="终止申请"
      width="480px"
      ok-text="确认终止"
      cancel-text="取消"
      :confirm-loading="actionLoading"
      :mask-closable="false"
      @ok="submitTerminate"
    >
      <p class="modal-hint">终止后本单直接关闭，不再流转，也不能恢复。</p>
      <a-textarea
        v-model:value="terminateModal.comment"
        :rows="3"
        :maxlength="512"
        show-count
        placeholder="终止原因（选填）"
      />
    </a-modal>

    <!-- 执行授权 -->
    <a-modal
      v-model:open="grantModal.visible"
      title="执行授权"
      width="480px"
      ok-text="执行授权"
      cancel-text="取消"
      :confirm-loading="actionLoading"
      :mask-closable="false"
      @ok="submitGrant"
    >
      <p class="modal-hint">
        将按批准的
        <b>{{ accessActionText(request?.actions) }}</b>
        权限为 <b>{{ request?.applicant_username }}</b> 写入授权，有效期
        <b>{{ accessExpireText(request?.approved_expire_at) }}</b>。
      </p>
      <a-textarea
        v-model:value="grantModal.comment"
        :rows="2"
        :maxlength="512"
        show-count
        placeholder="备注（选填）"
      />
    </a-modal>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { message, Modal } from 'ant-design-vue'
import { CopyOutlined } from '@ant-design/icons-vue'
import { useUserStore } from '@/store/user'
import {
  ApproveAccessRequestApi,
  ForwardAccessRequestApi,
  GetAccessApproverOptionsApi,
  GetAccessRequestDetailApi,
  GrantAccessRequestApi,
  RejectAccessRequestApi,
  TerminateAccessRequestApi,
  WithdrawAccessRequestApi,
} from '@/api/das'
import {
  ACCESS_EXPIRE_OPTIONS,
  accessActionColor,
  accessActionText,
  accessEffectiveStatus,
  accessEventColor,
  accessExpireText,
  accessEventText,
  accessInApproval,
  accessRequestedExpireDays,
  accessResourceLevelText,
  accessScopeSummary,
  accessStatusColor,
  accessStatusText,
  parseAccessResourceSpec,
  splitAccessActions,
} from './accessRequestMeta'

defineOptions({ name: 'DasAccessRequestDetail' })

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()

const requestNo = computed(() => String(route.params.request_no || '').trim())
const canApply = computed(() => userStore.hasPermission('das.access.apply'))

const loading = ref(false)
const actionLoading = ref(false)
const loadError = ref('')
const detail = ref(null)

const request = computed(() => detail.value?.request || null)
const events = computed(() => detail.value?.events || [])
const grant = computed(() => detail.value?.grant || null)
const effectiveStatus = computed(() => accessEffectiveStatus(request.value, grant.value))

// 本单落地的全部授权行（多资源申请单 = 多条），旧数据回退到单条 grant
const grantsList = computed(() => {
  const list = detail.value?.grants || []
  if (list.length) return list
  return grant.value ? [grant.value] : []
})

// 库 / 模式 / 表三行摘要：多资源单读 resource_spec，旧单据在 meta 里按旧三列回退。
// 展示口径只允许来自 accessScopeSummary，详情页与申请页 / 列表页不再各写一套。
const scopeSummary = computed(() =>
  accessScopeSummary(request.value || { resource_level: 'instance' }),
)

// 「表」一列不再把表名全部铺开：只列前 2 个，其余用「等」收口，后面跟总表数。
// 表名很多时（实测一单十几张表）整行会把描述表格撑变形，也很难扫读。
// 只在「全部表级条目都是指定表(list)」时截断；混有通配符 / 排除表时退回原摘要，避免丢信息。
const tableColumnText = computed(() => {
  const fallback = scopeSummary.value.table || '-'
  const spec = parseAccessResourceSpec(request.value?.resource_spec)
  const entries = Array.isArray(spec?.entries) ? spec.entries : []
  if (!entries.length) return fallback

  const tableEntries = entries.filter((item) => item && String(item.table_mode || ''))
  if (!tableEntries.length || tableEntries.some((item) => item.table_mode !== 'list')) {
    return fallback
  }

  const names = []
  const containers = []
  for (const item of tableEntries) {
    const tables = Array.isArray(item.tables) ? item.tables : []
    names.push(...tables)
    const container = item.kind === 'schema' ? item.schema : item.database
    if (container) containers.push(container)
  }
  if (!names.length) return fallback

  const uniqContainers = [...new Set(containers)]
  const prefix = uniqContainers.length === 1 ? `${uniqContainers[0]}：` : ''
  const head = names.slice(0, 2).join('、')
  const ellipse = names.length > 2 ? ' 等' : ''
  return `${prefix}${head}${ellipse}（共 ${names.length} 张表）`
})

// 服务端下发的按钮可用性。前端只做展示与灰置提示，真正的拦截在服务层
// （ensureCurrentApprover 等）——两边各写一套条件必然漂移，最后表现成「按钮能点但点了 403」。
const permissions = computed(
  () =>
    detail.value?.permissions || {
      is_applicant: false,
      is_current_approver: false,
      can_approve: false,
      can_reject: false,
      can_forward: false,
      can_withdraw: false,
      can_terminate: false,
      can_grant: false,
      can_create_similar: true,
    },
)

const expireOptions = ACCESS_EXPIRE_OPTIONS

// 步骤条：提交 → 审批 → 执行授权 → 完成
const currentStep = computed(() => {
  const status = request.value?.status
  if (status === 'wait_grant') return 2
  if (status === 'approved' || status === 'expired' || status === 'revoked') return 3
  if (!status) return 0
  return 1
})

const stepStatus = computed(() => {
  const status = request.value?.status
  if (['rejected', 'withdrawn', 'terminated', 'expired', 'revoked'].includes(status)) return 'error'
  return 'process'
})

const instanceChipFull = computed(() => {
  const req = request.value
  if (!req) return ''
  return req.instance_name ? `${req.instance_name}（${req.db_type || '-'}）` : '-'
})

// 数据源胶囊：名称过长时截断，完整内容走 title
const instanceChip = computed(() => {
  const text = String(request.value?.instance_name || '-')
  const chars = [...text]
  return chars.length <= 24 ? text : `${chars.slice(0, 24).join('')}...`
})

const handlerText = computed(() => {
  const req = request.value
  if (!req) return '-'
  if (req.executor_username && req.status === 'approved') return req.executor_name || req.executor_username
  return req.approver_name || req.approver_username || '-'
})

const GRANT_STATUS_TEXT = { active: '生效中', revoked: '已回收', expired: '已过期' }
const grantStatusText = (value) => GRANT_STATUS_TEXT[value] || value || '-'

// 按钮灰置原因：说清「为什么不能点」，而不是留给用户猜
const disabledTip = (kind) => {
  const req = request.value
  if (!req) return ''
  const perms = permissions.value
  const approver = req.approver_username || '-'
  if (kind === 'approve' || kind === 'reject' || kind === 'forward') {
    if (perms[kind === 'approve' ? 'can_approve' : kind === 'reject' ? 'can_reject' : 'can_forward']) return ''
    if (!accessInApproval(req.status)) return `当前状态（${accessStatusText(req.status)}）不可审批`
    if (perms.is_applicant) return '不能审批自己提交的申请'
    return `仅当前处理人（${approver}）可操作`
  }
  if (kind === 'withdraw' || kind === 'terminate') {
    if (perms[kind === 'withdraw' ? 'can_withdraw' : 'can_terminate']) return ''
    if (!accessInApproval(req.status)) return `当前状态（${accessStatusText(req.status)}）不可操作`
    return '仅申请人或管理员可操作'
  }
  if (kind === 'grant') {
    if (perms.can_grant) return ''
    if (req.status !== 'wait_grant') return `仅「待执行授权」状态可操作（当前：${accessStatusText(req.status)}）`
    if (perms.is_applicant) return '不能为自己的申请执行授权'
    return '需要数据访问审批权限'
  }
  return ''
}

// ---------------------------------------------------------------------------
// 取数
// ---------------------------------------------------------------------------
const fetchDetail = async () => {
  if (!requestNo.value) {
    loadError.value = '缺少申请单号'
    return
  }
  loading.value = true
  loadError.value = ''
  try {
    const res = await GetAccessRequestDetailApi({ request_no: requestNo.value }).catch(() => null)
    if (!res) {
      loadError.value = '申请单不存在，或您无权查看'
      detail.value = null
      return
    }
    detail.value = res.data || null
    if (!detail.value) loadError.value = '申请单不存在，或您无权查看'
  } finally {
    loading.value = false
  }
}

// 服务端 success 文案多数是 "ok" 占位值，不能直接弹给用户看
const serverMsg = (res, fallback) => {
  const text = String(res?.message || res?.msg || '').trim()
  return text && text !== 'ok' ? text : fallback
}

const copyRequestNo = async (text) => {
  const value = String(text || '')
  if (!value) return
  let ok = false
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value)
      ok = true
    } catch {
      ok = false
    }
  }
  if (!ok) {
    try {
      const input = document.createElement('textarea')
      input.value = value
      input.style.position = 'fixed'
      input.style.opacity = '0'
      document.body.appendChild(input)
      input.select()
      ok = document.execCommand('copy')
      document.body.removeChild(input)
    } catch {
      ok = false
    }
  }
  if (ok) message.success('申请单号已复制')
  else message.error('复制失败，请手动选中复制')
}

const goBack = () => router.push('/datasource/access')
const createSimilar = () => router.push(`/datasource/access/apply?from=${requestNo.value}`)

// ---------------------------------------------------------------------------
// 审批通过
// ---------------------------------------------------------------------------
const approveModal = reactive({ visible: false, expireDays: 7, comment: '' })

const openApprove = () => {
  if (!permissions.value.can_approve) return
  approveModal.expireDays = accessRequestedExpireDays(request.value)
  approveModal.comment = ''
  approveModal.visible = true
}

const submitApprove = async () => {
  actionLoading.value = true
  try {
    const res = await ApproveAccessRequestApi({
      request_no: requestNo.value,
      expire_days: approveModal.expireDays,
      comment: String(approveModal.comment || '').trim(),
    }).catch(() => null)
    if (!res) return
    message.success(serverMsg(res, '审批通过，等待执行授权'))
    approveModal.visible = false
    await fetchDetail()
  } finally {
    actionLoading.value = false
  }
}

// ---------------------------------------------------------------------------
// 审批不通过
// ---------------------------------------------------------------------------
const rejectModal = reactive({ visible: false, comment: '' })

const openReject = () => {
  if (!permissions.value.can_reject) return
  rejectModal.comment = ''
  rejectModal.visible = true
}

const submitReject = async () => {
  if (!String(rejectModal.comment || '').trim()) {
    message.warning('请填写驳回理由')
    return
  }
  actionLoading.value = true
  try {
    const res = await RejectAccessRequestApi({
      request_no: requestNo.value,
      comment: String(rejectModal.comment).trim(),
    }).catch(() => null)
    if (!res) return
    message.success(serverMsg(res, '已驳回'))
    rejectModal.visible = false
    await fetchDetail()
  } finally {
    actionLoading.value = false
  }
}

// ---------------------------------------------------------------------------
// 转交
// ---------------------------------------------------------------------------
const forwardModal = reactive({ visible: false, approver: undefined, comment: '' })
const approverLoading = ref(false)
const approverOptions = ref([])

const forwardApproverOptions = computed(() =>
  approverOptions.value.filter((item) => item.value !== String(request.value?.approver_username || '')),
)

const loadApproverOptions = async () => {
  approverLoading.value = true
  try {
    const res = await GetAccessApproverOptionsApi().catch(() => null)
    approverOptions.value = (res?.data || []).map((item) => ({
      value: item.username,
      label: item.name || item.username,
    }))
  } finally {
    approverLoading.value = false
  }
}

const openForward = () => {
  if (!permissions.value.can_forward) return
  forwardModal.approver = undefined
  forwardModal.comment = ''
  forwardModal.visible = true
  if (!approverOptions.value.length) loadApproverOptions()
}

const submitForward = async () => {
  if (!forwardModal.approver) {
    message.warning('请选择转交对象')
    return
  }
  if (!String(forwardModal.comment || '').trim()) {
    message.warning('请填写转交说明')
    return
  }
  actionLoading.value = true
  try {
    const res = await ForwardAccessRequestApi({
      request_no: requestNo.value,
      approver_username: forwardModal.approver,
      comment: String(forwardModal.comment).trim(),
    }).catch(() => null)
    if (!res) return
    message.success(serverMsg(res, '已转交'))
    forwardModal.visible = false
    await fetchDetail()
  } finally {
    actionLoading.value = false
  }
}

// ---------------------------------------------------------------------------
// 撤回 / 终止
// ---------------------------------------------------------------------------
const handleWithdraw = () => {
  if (!permissions.value.can_withdraw) return
  Modal.confirm({
    title: '撤回申请',
    content: `确定撤回申请单 ${requestNo.value} 吗？撤回后需重新发起。`,
    okText: '撤回',
    cancelText: '取消',
    onOk: async () => {
      const res = await WithdrawAccessRequestApi({
        request_no: requestNo.value,
        comment: '申请人主动撤回',
      }).catch(() => null)
      if (!res) return
      message.success(serverMsg(res, '已撤回'))
      await fetchDetail()
    },
  })
}

const terminateModal = reactive({ visible: false, comment: '' })

const openTerminate = () => {
  if (!permissions.value.can_terminate) return
  terminateModal.comment = ''
  terminateModal.visible = true
}

const submitTerminate = async () => {
  actionLoading.value = true
  try {
    const res = await TerminateAccessRequestApi({
      request_no: requestNo.value,
      comment: String(terminateModal.comment || '').trim(),
    }).catch(() => null)
    if (!res) return
    message.success(serverMsg(res, '已终止'))
    terminateModal.visible = false
    await fetchDetail()
  } finally {
    actionLoading.value = false
  }
}

// ---------------------------------------------------------------------------
// 执行授权
// ---------------------------------------------------------------------------
const grantModal = reactive({ visible: false, comment: '' })

const openGrant = () => {
  if (!permissions.value.can_grant) return
  grantModal.comment = ''
  grantModal.visible = true
}

const submitGrant = async () => {
  actionLoading.value = true
  try {
    const res = await GrantAccessRequestApi({
      request_no: requestNo.value,
      comment: String(grantModal.comment || '').trim(),
    }).catch(() => null)
    if (!res) return
    message.success(serverMsg(res, '授权已生效'))
    grantModal.visible = false
    await fetchDetail()
  } finally {
    actionLoading.value = false
  }
}

onMounted(fetchDetail)
</script>

<style scoped>
.access-detail-page {
  gap: var(--gi-spacing-sm);
  height: 100%;
  width: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: auto;
}

.detail-top-card {
  padding: 0;
  flex: 0 0 auto;
}

.detail-top-card :deep(.ant-card-body) {
  padding: 0;
}

.steps-shell {
  padding: 12px 20px;
  border: 1px solid rgb(235, 237, 240);
  border-radius: 8px 8px 0 0;
  background: #fff;
}

.detail-header {
  position: relative;
  padding: 16px 20px 14px;
  border: 1px solid rgb(235, 237, 240);
  border-top: none;
  border-radius: 0 0 8px 8px;
  background: #fff;
}

.detail-title-block {
  width: 100%;
  min-width: 0;
}

/* 标题行右侧要给 7 个按钮留位，否则长标题会钻到按钮下面 */
.header-title-row {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  padding-right: 640px;
}

.detail-title-text {
  font-size: 18px;
  font-weight: 600;
  white-space: nowrap;
}

.request-id-block {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex: 0 0 auto;
}

.detail-request-no {
  font-size: 13px;
  color: var(--gi-color-text-secondary);
}

.copy-request-no-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: none;
  background: transparent;
  color: #b6c0c8;
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
  transition: color 0.2s ease;
}

.copy-request-no-btn:hover {
  color: #71818c;
}

.detail-status-tag {
  margin-inline-end: 0;
}

.header-content-row {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-top: 10px;
  min-width: 0;
  /* 操作按钮只与标题行同高（absolute 定位在第一行），这一行不需要给按钮留位 */
}

.creator-info {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  flex: 0 0 auto;
}

.creator-name {
  font-weight: 600;
}

.creator-sep,
.creator-time {
  color: var(--gi-color-text-secondary);
}

.tag-group {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  overflow: hidden;
}

.info-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 24px;
  padding: 0 8px;
  border-radius: 4px;
  background: var(--gi-color-fill-quaternary, #f5f7f8);
  font-size: 12px;
  white-space: nowrap;
  max-width: 240px;
  overflow: hidden;
}

.chip-label {
  color: var(--gi-color-text-secondary);
  flex: 0 0 auto;
}

.chip-value {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.approver-inline {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  font-size: 13px;
  flex: 0 0 auto;
  margin-left: auto;
}

.info-label,
.label-colon {
  color: var(--gi-color-text-secondary);
}

.info-value {
  font-weight: 600;
}

.header-action-slot {
  position: absolute;
  top: 14px;
  right: 20px;
  z-index: 5;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

/* 按钮内文字垂直居中：
   这里把高度写死成 32px，同时字号下调到 13px。antd 的按钮是靠
   line-height: 1.5715 把文字"撑"到居中的，字号一变（13px → 行盒 20.4px），
   与写死的 32px 就对不上，文字会整体偏下 / 偏上，禁用与悬浮态同样偏移。
   改用 inline-flex + align-items 居中，不再依赖 line-height 与字号的耦合，
   任何 size / 状态（hover、disabled、loading）下都居中。 */
.header-action-slot :deep(.ant-btn) {
  height: 32px;
  padding-block: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  line-height: 1;
  font-size: 13px;
  border-radius: 6px;
}

.detail-section-card {
  flex: 0 0 auto;
}

.detail-desc :deep(.ant-descriptions-item-label) {
  width: 96px;
}

.mono-text {
  font-family: var(--gi-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 12px;
}

.detail-order-title {
  margin-left: 8px;
  color: var(--gi-color-text-secondary);
}

.grant-status-tag {
  margin-left: 8px;
}

/* 多资源申请单会落多条授权行：单号 + 覆盖资源逐行列出 */
.grant-no-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.grant-no-item {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.grant-no-desc {
  overflow: hidden;
  color: var(--gi-color-text-secondary);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-timeline {
  padding-left: 2px;
  margin-bottom: 0;
}

.event-title {
  font-size: 13px;
  font-weight: 600;
}

.event-meta {
  font-size: 12px;
  color: var(--gi-color-text-secondary);
  margin-top: 2px;
}

.event-comment {
  font-size: 12px;
  margin-top: 4px;
  padding: 6px 8px;
  border-radius: 6px;
  background: var(--gi-color-fill-quaternary, #f5f5f5);
  word-break: break-all;
}

.modal-hint {
  margin: 0 0 12px;
  font-size: 13px;
  color: var(--gi-color-text-secondary);
  line-height: 1.6;
}

@media (max-width: 1400px) {
  /* 按钮组绝对定位会盖住标题，窄屏改为正常文档流上下排布 */
  .header-title-row {
    padding-right: 0;
  }

  .header-action-slot {
    position: static;
    justify-content: flex-start;
    flex-wrap: wrap;
    margin-top: 12px;
  }
}

@media (max-width: 768px) {
  .detail-header {
    padding: 12px;
  }

  .header-title-row {
    flex-wrap: wrap;
    gap: 8px;
  }

  .header-content-row {
    flex-wrap: wrap;
    gap: 8px;
  }

  .approver-inline {
    margin-left: 0;
  }
}
</style>
