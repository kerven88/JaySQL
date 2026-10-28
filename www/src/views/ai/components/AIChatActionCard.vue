<template>
  <div class="ai-action-card" :class="`tone-${toneClass}`">
    <div class="action-card-bar" :class="`bar-${toneClass}`" />
    <div class="action-card-body">
      <div class="action-card-title-row">
        <component :is="iconComponent" class="action-card-icon" />
        <div class="action-card-texts">
          <div class="action-card-title">{{ displayTitle }}</div>
          <div v-if="displayDescription" class="action-card-desc">{{ displayDescription }}</div>
        </div>
      </div>
      <a-button
        :type="buttonType"
        size="small"
        class="action-card-btn"
        :loading="submitting"
        @click="handleGo"
      >
        {{ displayButtonText }}
        <template v-if="!submitting" #icon>
          <ArrowRightOutlined />
        </template>
      </a-button>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { message as antMessage } from 'ant-design-vue'
import { useOrderCreatePrefillStore } from '@/store/prefill'
import { aiCreateBackupConfirmApi, aiCreateOrderConfirmApi } from '@/api/ai'
import {
  ArrowRightOutlined,
  CheckCircleOutlined,
  ExclamationCircleOutlined,
  InfoCircleOutlined,
} from '@ant-design/icons-vue'

const props = defineProps({
  payload: {
    type: Object,
    required: true,
  },
})

const router = useRouter()

/**
 * 就地提交型草稿按类型选确认接口（两者都是「点确认即落库」）：
 *   - create_order：建工单并进入规范预审；
 *   - create_backup：建备份任务并纳入调度。
 * 其它类型（或后端没给 type）一律不做就地提交，退回按 route 跳转，避免误落库。
 */
const CONFIRM_APIS = {
  create_order: aiCreateOrderConfirmApi,
  create_backup: aiCreateBackupConfirmApi,
}

const draftType = computed(() => String(props.payload?.type || '').trim())
const confirmApi = computed(() => CONFIRM_APIS[draftType.value] || null)

// 成功后的展示口径：工单看状态与创建人，备份看方式与调度
const isBackupDraft = computed(() => draftType.value === 'create_backup')

/**
 * payload 是消息对象上的 action 字段本身（不是拷贝），因此这里写入的 submit_result
 * 会随会话一起持久化：切走再回来卡片仍显示「已创建」，不会因组件重建而变回可点击状态，
 * 也就不会重复建单。
 */
const submitResult = computed(() => props.payload?.submit_result || null)
const submitting = ref(false)
const isDone = computed(() => submitResult.value?.status === 'success')
const isFailed = computed(() => submitResult.value?.status === 'error')

const toneClass = computed(() => {
  if (isDone.value) return 'success'
  if (isFailed.value) return 'warning'
  return props.payload?.tone === 'warning' ? 'warning' : (props.payload?.tone === 'success' ? 'success' : 'primary')
})

const iconComponent = computed(() => {
  switch (toneClass.value) {
    case 'warning':
      return ExclamationCircleOutlined
    case 'success':
      return CheckCircleOutlined
    default:
      return InfoCircleOutlined
  }
})

const buttonType = computed(() => {
  if (toneClass.value === 'warning') return 'primary'
  if (toneClass.value === 'success') return 'default'
  return props.payload?.tone === 'primary' ? 'primary' : 'default'
})

const displayTitle = computed(() => {
  if (isDone.value) {
    return isBackupDraft.value
      ? `备份任务 ${submitResult.value.task_id || ''} 已创建`
      : `工单 ${submitResult.value.order_id} 已创建`
  }
  return props.payload?.title || ''
})

const displayDescription = computed(() => {
  if (isDone.value) {
    if (isBackupDraft.value) {
      return [
        submitResult.value.instance,
        submitResult.value.driver_type ? `备份方式：${submitResult.value.driver_type}` : '',
        submitResult.value.schedule_text || '',
      ].filter(Boolean).join(' · ')
    }
    return [
      submitResult.value.instance,
      `当前状态：${submitResult.value.status_text || '待规范预审'}`,
      submitResult.value.applicant ? `创建人：${submitResult.value.applicant}` : '',
    ].filter(Boolean).join(' · ')
  }
  if (isFailed.value) return `创建失败：${submitResult.value.message || '请稍后重试'}`
  return props.payload?.description || ''
})

const displayButtonText = computed(() => {
  if (isDone.value) return isBackupDraft.value ? '查看备份任务' : '查看工单'
  if (isFailed.value) return '重试'
  return props.payload?.buttonText || '前往'
})

const handleGo = async () => {
  const payload = props.payload || {}

  // 已创建成功：按钮转为「查看工单」，直接进详情页看规范预审结果
  if (isDone.value) {
    if (submitResult.value?.detail_url) router.push(submitResult.value.detail_url)
    return
  }

  // 就地提交型草稿（AI 创建工单 / 创建备份）：确认即落库，不需要再跳创建页
  if (payload.submitPath) {
    const api = confirmApi.value
    if (!api) {
      antMessage.error('该草稿缺少可识别的确认方式，请重新提问生成草稿')
      return
    }
    if (submitting.value) return
    submitting.value = true
    try {
      const res = await api(payload.prefill || {})
      if (res?.code === '0000' && res.data) {
        payload.submit_result = { status: 'success', ...res.data }
        antMessage.success(res.message || (isBackupDraft.value ? '备份任务已创建' : '工单已创建'))
      } else {
        const text = res?.message || '创建失败'
        payload.submit_result = { status: 'error', message: text }
        antMessage.error(text)
      }
    } catch (err) {
      const text = err?.response?.data?.message || err?.message || '网络异常，请稍后重试'
      payload.submit_result = { status: 'error', message: text }
      antMessage.error(text)
    } finally {
      submitting.value = false
    }
    return
  }

  // 跳转型草稿：跳转前把预填数据写入一次性 store，由目标页消费（如工单创建页自动填充）
  if (!payload.route) return
  if (payload.prefill) {
    useOrderCreatePrefillStore().setCreatePrefill(payload.prefill)
  }
  router.push({
    path: payload.route,
    query: payload.queryParams || {},
  })
}
</script>

<style scoped>
.ai-action-card {
  margin-top: 10px;
  border-radius: 14px;
  overflow: hidden;
  background: #ffffff;
  border: 1px solid rgba(27, 73, 88, 0.08);
  box-shadow: 0 6px 18px rgba(17, 55, 69, 0.05);
}

.action-card-bar {
  width: 100%;
  height: 3px;
}

.action-card-bar.bar-primary {
  background: linear-gradient(90deg, #0f766e 0%, #10b981 100%);
}

.action-card-bar.bar-warning {
  background: linear-gradient(90deg, #f59e0b 0%, #ef4444 100%);
}

.action-card-bar.bar-success {
  background: linear-gradient(90deg, #10b981 0%, #34d399 100%);
}

.action-card-body {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 14px 16px;
}

.action-card-title-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  min-width: 0;
  flex: 1;
}

.action-card-icon {
  flex-shrink: 0;
  width: 34px;
  height: 34px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 10px;
  font-size: 17px;
}

.tone-primary .action-card-icon {
  color: #0f766e;
  background: rgba(15, 118, 110, 0.1);
}

.tone-warning .action-card-icon {
  color: #d97706;
  background: rgba(245, 158, 11, 0.12);
}

.tone-success .action-card-icon {
  color: #059669;
  background: rgba(16, 185, 129, 0.12);
}

.action-card-texts {
  min-width: 0;
  flex: 1;
}

.action-card-title {
  font-size: 14px;
  font-weight: 600;
  color: #14313d;
  line-height: 1.4;
}

.action-card-desc {
  margin-top: 4px;
  font-size: 12.5px;
  line-height: 1.6;
  color: #67808d;
}

.action-card-btn {
  flex-shrink: 0;
  border-radius: 10px;
  white-space: nowrap;
}

:deep(.ant-btn-primary) {
  background: linear-gradient(135deg, #0f766e 0%, #0d6b63 100%);
  border: none;
}

:deep(.ant-btn-primary:hover) {
  background: linear-gradient(135deg, #0d6b63 0%, #0b5f58 100%);
}

@media (max-width: 767px) {
  .action-card-body {
    flex-direction: column;
    align-items: flex-start;
  }
  .action-card-btn {
    align-self: flex-end;
  }
}
</style>
