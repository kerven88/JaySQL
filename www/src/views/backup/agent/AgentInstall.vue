<template>
  <div class="agent-install-page">
    <a-card :bordered="false" class="shell">
      <div class="page-header">
        <div>
          <div class="title">备份 Agent 修复与安装</div>
          <div class="subtitle">平台会直接通过 SSH 连接备份机，完成检测、安装、修复、重启和复检。用户只需要补充备份机连接信息，不再需要自己去服务器执行命令。</div>
        </div>
        <a-space>
          <a-button @click="goBack">返回列表</a-button>
          <a-button :loading="ui.recheckLoading" type="primary" @click="recheckNode">重新检测节点状态</a-button>
        </a-space>
      </div>

      <a-alert
        :type="healthAlertType"
        show-icon
        class="top-alert"
        :message="healthAlertMessage"
        :description="healthAlertDescription"
      />

      <a-spin :spinning="ui.loading">
        <template v-if="ui.node">
          <a-steps :current="currentStep" size="small" class="install-steps">
            <a-step title="填写连接信息" description="补充 SSH 用户、密码和必要运行参数" />
            <a-step title="平台远程执行" description="平台自动检测、安装、修复或重启 Agent" />
            <a-step title="自动复检" description="等待节点心跳恢复并确认在线" />
          </a-steps>

          <a-card size="small" title="节点诊断" class="section-card">
            <a-descriptions :column="2" bordered size="small">
              <a-descriptions-item label="节点 ID">{{ ui.node.id }}</a-descriptions-item>
              <a-descriptions-item label="节点名称">{{ ui.node.name }}</a-descriptions-item>
              <a-descriptions-item label="备份机 SSH">{{ ui.node.host }}:{{ ui.node.port }}</a-descriptions-item>
              <a-descriptions-item label="节点状态">
                <a-tag :color="ui.node.is_online ? 'green' : 'default'">{{ ui.node.status_label || (ui.node.is_online ? '在线' : '离线') }}</a-tag>
              </a-descriptions-item>
              <a-descriptions-item label="节点基准目录">{{ ui.node.base_dir || '-' }}</a-descriptions-item>
              <a-descriptions-item label="最近心跳">{{ ui.node.last_heartbeat || '-' }}</a-descriptions-item>
              <a-descriptions-item label="Agent 版本">{{ ui.node.agent_version || '-' }}</a-descriptions-item>
              <a-descriptions-item label="密码已保存">{{ ui.node.ssh_password_configured ? '是' : '否' }}</a-descriptions-item>
              <a-descriptions-item label="私钥已保存">{{ ui.node.ssh_private_key_configured ? '是' : '否' }}</a-descriptions-item>
              <a-descriptions-item label="节点 Token">{{ ui.node.token_masked || '-' }}</a-descriptions-item>
              <a-descriptions-item label="心跳超时">{{ ui.node.heartbeat_timeout_seconds || 30 }} 秒</a-descriptions-item>
              <a-descriptions-item label="诊断结论" :span="2">{{ ui.node.offline_reason || ui.node.status_hint || '-' }}</a-descriptions-item>
              <a-descriptions-item label="建议动作" :span="2">{{ ui.node.recommended_action_title || (ui.node.is_online ? '保持当前配置即可' : '请先测试连接，再执行安装或修复') }}</a-descriptions-item>
            </a-descriptions>
          </a-card>

          <a-card size="small" title="远程连接与运行参数" class="section-card">
            <a-form :model="form" layout="vertical">
              <a-row :gutter="16">
                <a-col :xs="24" :md="12">
                  <a-form-item label="备份机 IP / 主机名">
                    <a-input v-model:value="form.machineHost" disabled />
                  </a-form-item>
                </a-col>
                <a-col :xs="24" :md="12">
                  <a-form-item label="SSH 端口">
                    <a-input-number v-model:value="form.port" style="width: 100%" :min="1" :max="65535" disabled />
                  </a-form-item>
                </a-col>
                <a-col :xs="24" :md="12">
                  <a-form-item label="SSH 用户" required>
                    <a-input v-model:value="form.sshUser" placeholder="例如 root" />
                  </a-form-item>
                </a-col>
                <a-col :xs="24" :md="12">
                  <a-form-item label="SSH 密码" required>
                    <a-input-password v-model:value="form.sshPassword" placeholder="留空则使用节点里已保存密码" />
                  </a-form-item>
                </a-col>
                <a-col :xs="24" :md="24">
                  <a-form-item label="SSH 私钥">
                    <a-textarea v-model:value="form.sshPrivateKey" :rows="4" placeholder="可选，支持 OpenSSH 私钥；留空则继续使用已保存私钥或密码" />
                  </a-form-item>
                </a-col>
                <a-col :xs="24" :md="12">
                  <a-form-item label="平台服务地址">
                    <a-input v-model:value="form.serverUrl" placeholder="例如 http://192.168.53.102:8083" />
                  </a-form-item>
                </a-col>
                <a-col :xs="24" :md="12">
                  <a-form-item label="节点基准目录">
                    <a-input v-model:value="form.baseDir" disabled />
                  </a-form-item>
                </a-col>
              </a-row>

              <a-collapse ghost class="advanced-collapse">
                <a-collapse-panel key="advanced" header="高级设置">
                  <a-row :gutter="16">
                    <a-col :xs="24" :md="12">
                      <a-form-item label="Agent 安装目录">
                        <a-input v-model:value="form.installDir" />
                      </a-form-item>
                    </a-col>
                    <a-col :xs="24" :md="12">
                      <a-form-item label="工具目录">
                        <a-input v-model:value="form.toolsDir" />
                      </a-form-item>
                    </a-col>
                    <a-col :xs="24" :md="12">
                      <a-form-item label="服务名">
                        <a-input v-model:value="form.serviceName" />
                      </a-form-item>
                    </a-col>
                    <a-col :xs="24" :md="12">
                      <a-form-item label="日志文件">
                        <a-input v-model:value="form.logPath" />
                      </a-form-item>
                    </a-col>
                  </a-row>
                </a-collapse-panel>
              </a-collapse>
            </a-form>

            <a-alert
              type="info"
              show-icon
              class="info-alert"
              message="操作说明"
              description="推荐先点“测试连接”，确认平台能连到备份机；首次接入节点时使用“安装 Agent”，节点已存在但离线时优先用“修复 Agent”，只需要重新拉起进程时用“重启 Agent”。"
            />
          </a-card>

          <a-card size="small" title="平台远程操作" class="section-card">
            <a-space wrap class="op-actions">
              <a-button :loading="ui.actionLoading === 'connect_test'" @click="runNodeAction('connect_test')">测试连接</a-button>
              <a-button :loading="ui.actionLoading === 'install_agent'" type="primary" @click="runNodeAction('install_agent')">安装 Agent</a-button>
              <a-button :loading="ui.actionLoading === 'repair_agent'" @click="runNodeAction('repair_agent')">修复 Agent</a-button>
              <a-button :loading="ui.actionLoading === 'restart_agent'" @click="runNodeAction('restart_agent')">重启 Agent</a-button>
            </a-space>

            <a-alert
              v-if="ui.lastResult.message"
              :type="ui.lastResult.nodeOnline ? 'success' : 'warning'"
              show-icon
              class="result-alert"
              :message="ui.lastResult.title || '最近一次操作结果'"
              :description="ui.lastResult.message"
            />

            <a-alert
              v-if="ui.lastResult.warnings"
              type="warning"
              show-icon
              class="result-alert"
              message="补充提示"
              :description="ui.lastResult.warnings"
            />

            <div class="block-tip">平台会把远程执行输出回显在这里，便于你确认连接、安装、重启和复检结果。</div>
            <pre class="cmd">{{ ui.lastResult.output || '暂未执行远程操作。' }}</pre>
          </a-card>
        </template>
      </a-spin>
    </a-card>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, watch } from 'vue'
import { message } from 'ant-design-vue'
import { useRoute, useRouter } from 'vue-router'
import { getBackupNodeDetailApi, operateBackupNodeApi } from '@/api/backup'

defineOptions({ name: 'BackupAgentInstallView' })

const route = useRoute()
const router = useRouter()
const nodeId = computed(() => Number(route.params.id || 0))

const ui = reactive({
  loading: false,
  recheckLoading: false,
  actionLoading: '',
  node: null,
  lastResult: {
    title: '',
    message: '',
    output: '',
    warnings: '',
    nodeOnline: false,
  },
})

const form = reactive({
  machineHost: '',
  port: 22,
  sshUser: 'root',
  sshPassword: '',
  sshPrivateKey: '',
  serverUrl: window.location.origin,
  baseDir: '',
  installDir: '/usr/local/goinsight/backup-agent',
  toolsDir: '/usr/local/goinsight/tools',
  serviceName: '',
  logPath: '',
})

const trimOrFallback = (value, fallback = '') => {
  const normalized = String(value || '').trim()
  return normalized || fallback
}

const currentStep = computed(() => (ui.node?.is_online ? 2 : 1))
const healthAlertType = computed(() => (ui.node?.is_online ? 'success' : 'warning'))
const healthAlertMessage = computed(() => (ui.node?.is_online ? '节点已在线，可继续使用备份任务。' : '节点当前离线，请在平台内直接执行检测、安装、修复或重启。'))
const healthAlertDescription = computed(() => {
  if (!ui.node) return ''
  if (ui.node.is_online) {
    return ui.node.status_hint || 'Agent 心跳正常。如需检查配置是否正确，可先执行一次“测试连接”。'
  }
  return ui.node.offline_reason || ui.node.status_hint || '平台尚未收到该节点的最新心跳，请先测试连接，再决定是安装、修复还是重启。'
})

watch(
  () => ui.node,
  (node) => {
    if (!node) return
    form.machineHost = trimOrFallback(node.host)
    form.port = Number(node.port || 22)
    form.sshUser = trimOrFallback(node.ssh_user, 'root')
    form.serverUrl = trimOrFallback(node.server_url, window.location.origin)
    form.baseDir = trimOrFallback(node.base_dir)
    form.installDir = trimOrFallback(node.install_dir, '/usr/local/goinsight/backup-agent')
    form.toolsDir = trimOrFallback(node.tools_dir, '/usr/local/goinsight/tools')
    form.serviceName = trimOrFallback(node.service_name, `goinsight-backup-agent-${node.id}`)
    form.logPath = trimOrFallback(node.log_path, `${trimOrFallback(node.install_dir, '/usr/local/goinsight/backup-agent').replace(/\/+$/, '')}/backup-agent.log`)
  },
  { immediate: true },
)

watch(
  () => form.installDir,
  (installDir) => {
    if (!String(form.logPath || '').trim()) {
      const normalized = trimOrFallback(installDir, '/usr/local/goinsight/backup-agent').replace(/\/+$/, '')
      form.logPath = `${normalized}/backup-agent.log`
    }
  },
)

const hydrateNode = (data) => {
  ui.node = data || null
}

const reloadNode = async (withToast = false) => {
  if (!nodeId.value) return
  ui.loading = true
  try {
    const { data } = await getBackupNodeDetailApi(nodeId.value)
    hydrateNode(data)
    if (withToast) {
      if (data?.is_online) {
        message.success(`节点已恢复${data.status_label || '在线'}`)
      } else {
        message.warning(data?.offline_reason || '节点仍然离线，请继续执行修复或检查网络连通性')
      }
    }
  } finally {
    ui.loading = false
  }
}

const buildPayload = (action) => ({
  action,
  ssh_user: trimOrFallback(form.sshUser),
  ssh_password: trimOrFallback(form.sshPassword),
  ssh_private_key: trimOrFallback(form.sshPrivateKey),
  server_url: trimOrFallback(form.serverUrl),
  install_dir: trimOrFallback(form.installDir),
  tools_dir: trimOrFallback(form.toolsDir),
  service_name: trimOrFallback(form.serviceName),
  log_path: trimOrFallback(form.logPath),
})

const ensureFormReady = () => {
  if (!trimOrFallback(form.sshUser)) {
    message.error('请先填写 SSH 用户')
    return false
  }
  if (!trimOrFallback(form.serverUrl)) {
    message.error('请先填写平台服务地址')
    return false
  }
  return true
}

const runNodeAction = async (action) => {
  if (!nodeId.value || ui.actionLoading) return
  if (!ensureFormReady()) return
  ui.actionLoading = action
  try {
    const { data } = await operateBackupNodeApi(nodeId.value, buildPayload(action))
    ui.lastResult.title = data?.title || '远程操作完成'
    ui.lastResult.message = data?.message || ''
    ui.lastResult.output = data?.output || ''
    ui.lastResult.warnings = Array.isArray(data?.warnings) ? data.warnings.join('\n') : ''
    ui.lastResult.nodeOnline = !!data?.node_online
    if (data?.node) {
      hydrateNode(data.node)
    } else {
      await reloadNode()
    }
    if (data?.node_online) {
      message.success(data?.message || '节点已恢复在线')
    } else {
      message.warning(data?.message || '远程操作已执行，请继续查看输出和节点状态')
    }
    form.sshPassword = ''
  } catch (error) {
    const messageText = error?.msg || error?.message || '执行节点远程操作失败'
    ui.lastResult.title = '远程操作失败'
    ui.lastResult.message = messageText
    ui.lastResult.output = messageText
    ui.lastResult.warnings = ''
    ui.lastResult.nodeOnline = false
    message.error(messageText)
  } finally {
    ui.actionLoading = ''
  }
}

const recheckNode = async () => {
  ui.recheckLoading = true
  try {
    await reloadNode(true)
  } finally {
    ui.recheckLoading = false
  }
}

const goBack = () => {
  router.back()
}

onMounted(async () => {
  await reloadNode()
})
</script>

<style scoped>
.agent-install-page {
  min-height: calc(100vh - 120px);
  padding: 24px;
  background: #f5f7fa;
}

.shell {
  max-width: 1180px;
  margin: 0 auto;
  border-radius: 16px;
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.title {
  font-size: 20px;
  font-weight: 600;
  color: #1f2d38;
}

.subtitle {
  margin-top: 8px;
  font-size: 13px;
  line-height: 1.6;
  color: #71818c;
}

.top-alert {
  margin-top: 16px;
}

.install-steps {
  margin-top: 20px;
}

.section-card {
  margin-top: 16px;
  border-radius: 12px;
}

.inline-actions {
  margin-top: 12px;
}

.info-alert {
  margin-top: 4px;
}

.advanced-collapse {
  margin-top: 4px;
}

.block-tip {
  margin-bottom: 12px;
  font-size: 12px;
  line-height: 1.6;
  color: #71818c;
}

.cmd {
  padding: 14px 16px;
  margin: 0;
  background: #0f172a;
  color: #dbeafe;
  border-radius: 12px;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 12px;
  line-height: 1.6;
  min-height: 220px;
}

.op-actions {
  display: flex;
  margin-bottom: 12px;
}

.result-alert {
  margin-bottom: 12px;
}

@media (max-width: 900px) {
  .page-header {
    flex-direction: column;
    align-items: stretch;
  }
}

@media (max-width: 768px) {
  .agent-install-page {
    padding: 12px;
    min-height: calc(100dvh - 100px);
  }

  /* 3 步水平步骤条带 description，窄屏严重挤压 */
  .install-steps :deep(.ant-steps-item) {
    min-width: 0;
  }

  .install-steps :deep(.ant-steps-item-title) {
    font-size: 12px;
    white-space: normal;
    line-height: 1.35;
  }

  .install-steps :deep(.ant-steps-item-description) {
    font-size: 11px;
    white-space: normal;
    line-height: 1.4;
  }

  .op-actions :deep(.ant-space),
  .inline-actions :deep(.ant-space) {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(96px, 1fr));
    gap: 8px;
    width: 100%;
  }

  .op-actions :deep(.ant-btn) {
    width: 100%;
  }
}
</style>
