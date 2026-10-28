<template>
  <div class="gi-page-shell storage-target-page">
    <!-- 顶部工具栏 -->
    <div class="gi-page-toolbar">
      <div class="page-title">存储目标</div>
      <div class="gi-page-toolbar-right">
        <a-input-search
          v-model:value="query.search"
          placeholder="搜索名称 / 地址 / Bucket / 备注"
          class="gi-toolbar-search"
          allow-clear
          @search="handleSearch"
          @change="handleSearchChange"
        />
        <a-button v-if="canManageBackupTask" type="primary" @click="openEditor()">
          <template #icon><PlusOutlined /></template>
          新增存储目标
        </a-button>
      </div>
    </div>

    <!-- 列表卡片 -->
    <a-card :bordered="false" class="gi-plain-card">
      <a-table
        class="storage-target-table"
        :columns="columns"
        :data-source="tableData"
        :loading="uiState.loading"
        :pagination="pagination"
        row-key="id"
        table-layout="fixed"
        @change="handleTableChange"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'name'">
            <div class="target-name-row">
              <span class="target-name-text">{{ record.name || '-' }}</span>
            </div>
          </template>

          <template v-else-if="column.key === 'type'">
            <span class="type-text">{{ typeDisplayLabel(record.type) }}</span>
          </template>

          <template v-else-if="column.key === 'address'">
            <div class="address-single" :title="addressText(record)">{{ addressText(record) }}</div>
          </template>

          <template v-else-if="column.key === 'connection_status'">
            <span class="connection-text">{{ statusDisplayLabel(record.connection_status) }}</span>
          </template>

          <template v-else-if="column.key === 'created_at'">
            <span class="time-text">{{ formatDateTime(record.created_at) || '-' }}</span>
          </template>

          <template v-else-if="column.key === 'actions'">
            <div class="action-cell">
              <a-button
                size="small"
                type="primary"
                ghost
                @click="openPrecheck(record)"
              >
                <template #icon><ApiOutlined /></template>
                测试连通
              </a-button>
              <a-dropdown placement="bottomRight" trigger="click">
                <a-button size="small" class="more-btn">
                  <template #icon><MoreOutlined /></template>
                </a-button>
                <template #overlay>
                  <a-menu>
                    <a-menu-item @click="openEditor(record)">
                      <template #icon><EditOutlined /></template>
                      编辑
                    </a-menu-item>
                    <a-menu-item danger @click="handleDelete(record)">
                      <template #icon><DeleteOutlined /></template>
                      删除
                    </a-menu-item>
                  </a-menu>
                </template>
              </a-dropdown>
            </div>
          </template>
        </template>
      </a-table>
    </a-card>

    <!-- 新增 / 编辑抽屉 -->
    <a-drawer
      :open="editor.open"
      :title="editor.record?.id ? '编辑存储目标' : '新增存储目标'"
      :width="'60%'"
      :destroy-on-close="true"
      @close="closeEditor"
    >
      <a-form
        ref="editorFormRef"
        :model="editorForm"
        layout="vertical"
        :rules="editorRules"
        class="storage-target-form"
      >
        <a-form-item label="名称" name="name">
          <a-input v-model:value="editorForm.name" placeholder="例如：备份中心机-01 / 冷备 S3 桶" />
        </a-form-item>

        <a-form-item label="类型" name="type">
          <a-radio-group v-model:value="editorForm.type" class="type-radio-group">
            <a-radio
              v-for="opt in typeOptions"
              :key="opt.value"
              :value="opt.value"
              :disabled="!!opt.disabled"
            >
              {{ opt.label }}
              <span v-if="opt.tag" class="type-radio-tag">{{ opt.tag }}</span>
            </a-radio>
          </a-radio-group>
        </a-form-item>

        <template v-if="editorForm.type === 'LOCAL_SERVER'">
          <a-form-item label="服务器地址" name="host">
            <a-input v-model:value="editorForm.host" placeholder="例如 192.168.1.10:22" />
          </a-form-item>
          <a-form-item label="备份根路径" name="base_path">
            <a-input v-model:value="editorForm.base_path" placeholder="例如 /data/backups（必须为绝对路径）" />
          </a-form-item>
          <a-form-item label="SSH 用户名" name="ssh_user">
            <a-input v-model:value="editorForm.ssh_user" placeholder="例如 root / hs-ops" />
          </a-form-item>
          <a-form-item label="SSH 密码" name="ssh_password">
            <a-input-password v-model:value="editorForm.ssh_password" placeholder="输入密码" />
          </a-form-item>
          <a-form-item label="SSH 私钥" name="ssh_private_key">
            <a-textarea v-model:value="editorForm.ssh_private_key" :rows="5" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" />
          </a-form-item>
        </template>

        <template v-else>
          <div class="editor-form-grid">
            <a-form-item label="Endpoint" name="s3_endpoint">
              <a-input v-model:value="editorForm.s3_endpoint" :placeholder="endpointPlaceholder(editorForm.type)" />
            </a-form-item>
            <a-form-item label="Region" name="s3_region">
              <a-input v-model:value="editorForm.s3_region" :placeholder="regionPlaceholder(editorForm.type)" />
            </a-form-item>
          </div>
          <a-form-item label="Bucket" name="s3_bucket">
            <a-input v-model:value="editorForm.s3_bucket" placeholder="例如 my-backup-bucket" />
          </a-form-item>
          <a-form-item label="Path-Style" name="s3_force_path_style" extra="自建 MinIO 等 S3 兼容服务一般需开启；阿里云 OSS 使用虚拟主机寻址，保持关闭即可">
            <a-switch v-model:checked="editorForm.s3_force_path_style" />
          </a-form-item>
          <div class="editor-form-grid">
            <a-form-item label="AccessKey" name="s3_access_key">
              <a-input-password v-model:value="editorForm.s3_access_key" />
            </a-form-item>
            <a-form-item label="SecretKey" name="s3_secret_key">
              <a-input-password v-model:value="editorForm.s3_secret_key" />
            </a-form-item>
          </div>
        </template>

        <a-form-item label="备注" name="remark">
          <a-textarea v-model:value="editorForm.remark" :rows="2" placeholder="用途、环境、责任人等" />
        </a-form-item>
      </a-form>

      <template #footer>
        <div class="editor-footer">
          <a-button
            :type="canTestConnection ? 'primary' : 'default'"
            :disabled="!canTestConnection"
            :loading="editor.testing"
            @click="handleTestConnection"
          >
            测试连接
          </a-button>
          <a-space>
            <a-button @click="closeEditor">取消</a-button>
            <a-button type="primary" :loading="editor.saving" @click="handleEditorSubmit">保存</a-button>
          </a-space>
        </div>
      </template>
    </a-drawer>

    <!-- 连通测试抽屉 -->
    <a-drawer
      :open="precheck.open"
      title="存储目标连通测试"
      :width="800"
      :destroy-on-close="true"
      @close="closePrecheck"
    >
      <div v-if="precheck.record" class="precheck-shell">
        <div class="precheck-summary-card">
          <div class="precheck-summary-icon">
            <CloudServerOutlined v-if="precheck.record.type === 'LOCAL_SERVER'" />
            <CloudOutlined v-else />
          </div>
          <div class="precheck-summary-body">
            <div class="precheck-summary-title">{{ precheck.record.name }}</div>
            <div class="precheck-summary-addr">
              <template v-if="precheck.record.type === 'LOCAL_SERVER'">
                {{ precheck.record.ssh_user }}@{{ precheck.record.host }}:{{ precheck.record.port }} · {{ precheck.record.base_path }}
              </template>
              <template v-else>
                {{ precheck.record.s3_endpoint }} / {{ precheck.record.s3_bucket }}
              </template>
            </div>
          </div>
          <div class="precheck-summary-status">
            <a-tag v-if="precheck.result?.ok === true" color="success" class="precheck-status-tag">测试通过</a-tag>
            <a-tag v-else-if="precheck.result?.ok === false" color="error" class="precheck-status-tag">测试失败</a-tag>
            <a-tag v-else color="default" class="precheck-status-tag">未测试</a-tag>
          </div>
        </div>

        <a-space style="margin-bottom: 16px">
          <a-button type="primary" :loading="precheck.running" @click="runPrecheck">
            <template #icon>
              <ReloadOutlined v-if="precheck.result" />
              <PlayCircleOutlined v-else />
            </template>
            {{ precheck.running ? '测试中...' : (precheck.result ? '重新测试' : '运行连通测试') }}
          </a-button>
        </a-space>

        <a-alert
          v-if="precheck.result && !precheck.result.ok"
          type="error"
          show-icon
          :message="precheck.result.err_code || '连通测试失败'"
          :description="precheck.result.err_message || '未提供错误详情'"
          style="margin-bottom: 16px"
        />
        <a-alert
          v-for="(warn, wi) in (precheck.result?.warnings || [])"
          :key="wi"
          type="warning"
          show-icon
          :message="warn"
          style="margin-bottom: 8px"
        />

        <a-descriptions
          v-if="precheck.result?.capability"
          title="文件系统信息"
          bordered
          size="small"
          :column="2"
          style="margin-top: 16px"
        >
          <a-descriptions-item label="文件系统">{{ precheck.result.capability.fs_type || '-' }}</a-descriptions-item>
          <a-descriptions-item label="挂载点">{{ precheck.result.capability.mount_point || '-' }}</a-descriptions-item>
          <a-descriptions-item label="总容量">{{ formatBytes(precheck.result.capability.total_bytes) }}</a-descriptions-item>
          <a-descriptions-item label="剩余容量">{{ formatBytes(precheck.result.capability.free_bytes) }}（已用 {{ precheck.result.capability.used_percent ?? '-' }}%）</a-descriptions-item>
          <a-descriptions-item label="Inodes 总量">{{ precheck.result.capability.inodes_total ?? '-' }}</a-descriptions-item>
          <a-descriptions-item label="Inodes 剩余">{{ precheck.result.capability.inodes_free ?? '-' }}（已用 {{ precheck.result.capability.inode_percent ?? '-' }}%）</a-descriptions-item>
        </a-descriptions>

        <a-descriptions
          v-if="precheck.result?.s3_capability"
          title="对象存储权限"
          bordered
          size="small"
          :column="2"
          style="margin-top: 16px"
        >
          <a-descriptions-item label="Endpoint">{{ precheck.result.s3_capability.endpoint || '-' }}</a-descriptions-item>
          <a-descriptions-item label="Region">{{ precheck.result.s3_capability.region || '-' }}</a-descriptions-item>
          <a-descriptions-item label="Bucket">{{ precheck.result.s3_capability.bucket || '-' }}</a-descriptions-item>
          <a-descriptions-item label="Force Path-Style">{{ precheck.result.s3_capability.force_path_style ? '开启' : '关闭' }}</a-descriptions-item>
          <a-descriptions-item label="HeadBucket">
            <a-tag :color="precheck.result.s3_capability.head_bucket_ok ? 'success' : 'error'">{{ precheck.result.s3_capability.head_bucket_ok ? '通过' : '失败' }}</a-tag>
          </a-descriptions-item>
          <a-descriptions-item label="ListBucket">
            <a-tag :color="precheck.result.s3_capability.list_bucket_ok ? 'success' : 'error'">{{ precheck.result.s3_capability.list_bucket_ok ? '通过' : '失败' }}</a-tag>
          </a-descriptions-item>
          <a-descriptions-item label="PutObject">
            <a-tag :color="precheck.result.s3_capability.put_object_ok ? 'success' : 'error'">{{ precheck.result.s3_capability.put_object_ok ? '通过' : '失败' }}</a-tag>
          </a-descriptions-item>
          <a-descriptions-item label="DeleteObject">
            <a-tag :color="precheck.result.s3_capability.delete_object_ok ? 'success' : 'error'">{{ precheck.result.s3_capability.delete_object_ok ? '通过' : '失败' }}</a-tag>
          </a-descriptions-item>
          <a-descriptions-item label="对象样本数" :span="2">
            {{ precheck.result.s3_capability.object_count ?? '-' }} 个（只列前 10 条）
            <a-tag
              v-for="k in (precheck.result.s3_capability.object_sample_keys || []).slice(0, 5)"
              :key="k"
              color="default"
              style="margin-left: 4px"
            >{{ k.length > 40 ? k.slice(0, 38) + '…' : k }}</a-tag>
          </a-descriptions-item>
          <a-descriptions-item label="已允许动作">
            <a-tag v-for="a in (precheck.result.s3_capability.allowed_actions || [])" :key="a" color="success" style="margin-right: 4px">{{ a }}</a-tag>
            <span v-if="!precheck.result.s3_capability.allowed_actions?.length">-</span>
          </a-descriptions-item>
          <a-descriptions-item label="被拒绝动作">
            <template v-if="precheck.result.s3_capability.denied_actions?.length">
              <a-tag v-for="a in precheck.result.s3_capability.denied_actions" :key="a" color="error" style="margin-right:4px">{{ a.length > 60 ? a.slice(0, 58) + '…' : a }}</a-tag>
            </template>
            <span v-else>-</span>
          </a-descriptions-item>
        </a-descriptions>

        <div v-if="precheck.result?.fingerprints?.length && precheck.record.type === 'LOCAL_SERVER'" class="fingerprint-panel">
          <div class="fingerprint-header">
            <div>
              <div class="fingerprint-title">SSH Host Key 指纹</div>
              <div class="fingerprint-sub">请与运维登记信息或服务器控制台核对，确认无误后点击「确认指纹」。指纹一旦确认，后续备份任务才允许使用该目标。</div>
            </div>
            <a-space>
              <a-tag v-if="precheck.record.fingerprint_confirmed" color="success">已确认</a-tag>
              <a-button
                type="primary"
                :disabled="precheck.record.fingerprint_confirmed"
                :loading="precheck.confirmLoading"
                @click="confirmFingerprint"
              >
                <template #icon><SafetyCertificateOutlined /></template>
                确认指纹
              </a-button>
            </a-space>
          </div>
          <ul class="fingerprint-list">
            <li v-for="(fp, idx) in precheck.result.fingerprints" :key="idx" class="fingerprint-item">
              <span class="fingerprint-algo">{{ fp.algo }}</span>
              <span class="fingerprint-hash">{{ fp.hash }}</span>
            </li>
          </ul>
        </div>

        <a-collapse v-if="precheck.result?.server_output" style="margin-top: 16px">
          <a-collapse-panel key="output" header="服务器执行输出（调试用）">
            <pre class="server-output">{{ precheck.result.server_output }}</pre>
          </a-collapse-panel>
        </a-collapse>
      </div>
    </a-drawer>
  </div>
</template>

<script setup>
import { reactive, ref, computed, onMounted } from 'vue'
import { message, Modal } from 'ant-design-vue'
import {
  PlusOutlined,
  CloudServerOutlined,
  CloudOutlined,
  SafetyCertificateOutlined,
  ApiOutlined,
  MoreOutlined,
  EditOutlined,
  DeleteOutlined,
  PlayCircleOutlined,
  ReloadOutlined,
} from '@ant-design/icons-vue'
import {
  getStorageTargetsApi,
  createStorageTargetApi,
  updateStorageTargetApi,
  deleteStorageTargetApi,
  precheckStorageTargetApi,
  confirmStorageTargetFingerprintApi,
} from '@/api/backup'
import { useUserStore } from '@/store/user'

// 注意：这里必须用 store 的单参数 hasPermission。
// '@/utils/permission' 的 hasPermission 签名是 (permissions, route)，
// 只传一个字符串会把 route 置为 undefined，访问 route.meta 会抛
// TypeError: Cannot read properties of undefined (reading 'meta')，导致整页白屏。
const userStore = useUserStore()
const canManageBackupTask = computed(() => userStore.hasPermission('backup.task.manage'))

function formatDateTime(value) {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

const query = reactive({
  search: '',
})

const uiState = reactive({
  loading: false,
})

const tableData = ref([])

const pagination = reactive({
  current: 1,
  pageSize: 20,
  total: 0,
  showSizeChanger: true,
  showQuickJumper: true,
  pageSizeOptions: ['10', '20', '50', '100'],
  showTotal: (total) => `共 ${total} 条`,
})

// 列宽由 scoped CSS 的 .storage-target-table 统一等分（table-layout: fixed + width:100%），此处不声明固定 width。
// 对齐：除最后一列（操作）居中，其余列的表头与表体一律左对齐。
const columns = [
  { key: 'name', title: '存储目标', dataIndex: 'name', align: 'left' },
  { key: 'type', title: '类型', dataIndex: 'type', align: 'left' },
  { key: 'address', title: '地址', dataIndex: 'host', align: 'left' },
  { key: 'connection_status', title: '连接状态', dataIndex: 'connection_status', align: 'left' },
  { key: 'created_at', title: '创建时间', dataIndex: 'created_at', align: 'left' },
  { key: 'actions', title: '操作', align: 'center' },
]

// 本期支持的类型：本地备份服务器（SSH）、阿里云 OSS。
// MinIO / 自建 S3 暂不可选（禁用占位并标注「未来将支持」）；腾讯云 COS、华为云 OBS 本期不支持，已移除。
const typeOptions = [
  { value: 'LOCAL_SERVER', label: '本地备份服务器（SSH）' },
  { value: 'OSS_ALIYUN', label: '阿里云 OSS' },
  { value: 'S3_MINIO', label: 'MinIO / 自建 S3', disabled: true, tag: '未来将支持' },
]

const endpointPlaceholder = (t) => {
  switch (t) {
    case 'OSS_ALIYUN': return 'oss-cn-hangzhou.aliyuncs.com（无需 https://）'
    case 'S3_MINIO': return 'http://minio-host:9000'
    default: return ''
  }
}

const regionPlaceholder = (t) => {
  switch (t) {
    case 'OSS_ALIYUN': return 'cn-hangzhou / cn-beijing / cn-shenzhen …'
    default: return 'us-east-1（或自定义）'
  }
}

function typeDisplayLabel(t) {
  const map = {
    LOCAL_SERVER: '本地备份服务器',
    OSS_ALIYUN: '阿里云 OSS',
    S3_MINIO: 'MinIO',
  }
  return map[t] || t || '-'
}

function statusDisplayLabel(s) {
  const map = {
    OK: '连接正常',
    FAILED: '连接失败',
    PENDING: '检测中',
    UNKNOWN: '未检测',
  }
  return map[String(s || '').toUpperCase()] || '未检测'
}

// 地址列：单行展示，形如 root@192.168.1.99:/backup（对象存储为 bucket@endpoint）
function addressText(record) {
  if (record?.type === 'LOCAL_SERVER') {
    const user = record.ssh_user || '-'
    const host = record.host || '-'
    const path = record.base_path || '-'
    return `${user}@${host}:${path}`
  }
  const bucket = record?.s3_bucket || '-'
  const endpoint = record?.s3_endpoint || record?.s3_region || '-'
  return `${bucket}@${endpoint}`
}

async function fetchTargets() {
  uiState.loading = true
  try {
    const params = {
      page: pagination.current,
      page_size: pagination.pageSize,
      is_page: true,
    }
    if (query.search) params.search = query.search
    const res = await getStorageTargetsApi(params)
    tableData.value = res?.data?.list || res?.data || []
    pagination.total = res?.data?.total || res?.total || 0
  } catch {
    // request wrapper 会提示错误
  } finally {
    uiState.loading = false
  }
}

function handleSearch() {
  pagination.current = 1
  fetchTargets()
}

let debounceTimer = null
function handleSearchChange() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    pagination.current = 1
    fetchTargets()
  }, 400)
}

function handleTableChange(pg) {
  pagination.current = pg.current
  pagination.pageSize = pg.pageSize
  fetchTargets()
}

function formatBytes(bytes) {
  if (!bytes || bytes <= 0) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let value = bytes
  let idx = 0
  while (value >= 1024 && idx < units.length - 1) {
    value /= 1024
    idx++
  }
  return `${value.toFixed(2)} ${units[idx]}`
}

// 编辑器
const editor = reactive({
  open: false,
  record: null,
  saving: false,
  testing: false,
})
const editorFormRef = ref()
const editorForm = reactive(emptyEditorForm())

function emptyEditorForm() {
  return {
    id: 0,
    name: '',
    type: 'LOCAL_SERVER',
    host: '',
    port: 22,
    base_path: '',
    ssh_user: '',
    ssh_password: '',
    ssh_private_key: '',
    s3_endpoint: '',
    s3_region: '',
    s3_bucket: '',
    s3_force_path_style: false,
    s3_access_key: '',
    s3_secret_key: '',
    remark: '',
  }
}

// 服务器地址支持 host 或 host:port 两种写法，未写端口时默认 22
function splitHostPort(raw) {
  const value = String(raw ?? '').trim()
  if (!value) return { host: '', port: 22 }
  const idx = value.lastIndexOf(':')
  if (idx > 0 && idx < value.length - 1 && /^\d+$/.test(value.slice(idx + 1))) {
    return { host: value.slice(0, idx), port: Number(value.slice(idx + 1)) }
  }
  return { host: value, port: 22 }
}

const editorRules = computed(() => {
  const base = {
    name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
    type: [{ required: true, message: '请选择类型', trigger: 'change' }],
  }
  if (editorForm.type === 'LOCAL_SERVER') {
    return {
      ...base,
      host: [
        { required: true, message: '请输入服务器地址，例如 192.168.1.10:22', trigger: 'blur' },
        {
          validator: (_r, v, cb) => {
            const { host, port } = splitHostPort(v)
            if (!host) cb(new Error('请输入服务器地址'))
            else if (String(host).includes(':')) cb(new Error('地址格式不正确，例如 192.168.1.10:22'))
            else if (!(port >= 1 && port <= 65535)) cb(new Error('端口需在 1-65535 之间'))
            else cb()
          },
          trigger: 'blur',
        },
      ],
      base_path: [
        { required: true, message: '请输入备份根路径（绝对路径）', trigger: 'blur' },
        {
          validator: (_r, v, cb) => {
            if (!v || !String(v).startsWith('/')) {
              cb(new Error('必须为绝对路径（以 / 开头）'))
            } else cb()
          },
          trigger: 'blur',
        },
      ],
      ssh_user: [{ required: true, message: '请输入 SSH 用户名', trigger: 'blur' }],
    }
  }
  return {
    ...base,
    s3_endpoint: [{ required: true, message: '请输入 Endpoint', trigger: 'blur' }],
    s3_region: [{ required: true, message: '请输入 Region', trigger: 'blur' }],
    s3_bucket: [{ required: true, message: '请输入 Bucket 名称', trigger: 'blur' }],
    s3_access_key: [{ required: !editor.record, message: '请输入 AccessKey', trigger: 'blur' }],
    s3_secret_key: [{ required: !editor.record, message: '请输入 SecretKey', trigger: 'blur' }],
  }
})

function openEditor(record = null) {
  Object.assign(editorForm, emptyEditorForm())
  if (record) {
    editor.record = record
    editorForm.id = record.id
    editorForm.name = record.name || ''
    editorForm.type = record.type || 'LOCAL_SERVER'
    editorForm.host = record.host ? `${record.host}${record.port ? `:${record.port}` : ''}` : ''
    editorForm.port = record.port || 22
    editorForm.base_path = record.base_path || ''
    editorForm.ssh_user = record.ssh_user || ''
    editorForm.s3_endpoint = record.s3_endpoint || ''
    editorForm.s3_region = record.s3_region || ''
    editorForm.s3_bucket = record.s3_bucket || ''
    editorForm.s3_force_path_style = !!record.s3_force_path_style
    editorForm.remark = record.remark || ''
    // 凭据永远不明文回填，空字符串代表不改
    editorForm.ssh_password = ''
    editorForm.ssh_private_key = ''
    editorForm.s3_access_key = ''
    editorForm.s3_secret_key = ''
  } else {
    editor.record = null
    editorForm.port = 22
    editorForm.type = 'LOCAL_SERVER'
  }
  editor.open = true
}

function closeEditor() {
  editor.open = false
  editor.record = null
  editor.testing = false
  Object.assign(editorForm, emptyEditorForm())
}

// 表单 → 接口 payload（「测试连接」与「保存」共用，保证两者参数口径完全一致）
function buildEditorPayload() {
  const payload = { ...editorForm }
  if (payload.type === 'LOCAL_SERVER') {
    const { host, port } = splitHostPort(payload.host)
    payload.host = host
    payload.port = port
  }
  return payload
}

// 「测试连接」可用条件：与连通测试实际需要的参数一一对应，任一项缺失即置灰。
// 编辑态下凭据留空表示沿用已保存的凭据（后端会用库中凭据补齐），故不强制填写。
const canTestConnection = computed(() => {
  const f = editorForm
  if (f.type === 'LOCAL_SERVER') {
    const { host, port } = splitHostPort(f.host)
    if (!host || String(host).includes(':')) return false
    if (!(port >= 1 && port <= 65535)) return false
    if (!String(f.ssh_user || '').trim()) return false
    if (!String(f.base_path || '').trim().startsWith('/')) return false
    if (!editor.record && !f.ssh_password && !f.ssh_private_key) return false
    return true
  }
  if (!String(f.s3_endpoint || '').trim()) return false
  if (!String(f.s3_region || '').trim()) return false
  if (!String(f.s3_bucket || '').trim()) return false
  if (!editor.record && (!f.s3_access_key || !f.s3_secret_key)) return false
  return true
})

// 测试连接：新增（id=0）走后端草稿模式，仅按表单参数探测、不落库；
// 编辑态用已保存 id 就地复测（结果会刷新该目标的连接状态，与列表页「连通测试」口径一致）。
async function handleTestConnection() {
  if (!canTestConnection.value || editor.testing) return
  editor.testing = true
  try {
    const res = await precheckStorageTargetApi(editor.record?.id || 0, buildEditorPayload())
    const result = res?.data || null
    if (result?.ok) {
      message.success('连接测试通过')
    } else {
      const code = result?.err_code ? `（${result.err_code}）` : ''
      message.error(`连接测试未通过${code}：${result?.err_message || '未返回失败原因'}`)
    }
  } catch {
    // request wrapper 已统一提示
  } finally {
    editor.testing = false
  }
}

async function handleEditorSubmit() {
  try {
    await editorFormRef.value?.validate()
  } catch {
    return
  }
  if (editorForm.type === 'LOCAL_SERVER') {
    if (!editorForm.ssh_password && !editorForm.ssh_private_key && !editor.record) {
      message.error('SSH 密码或私钥至少填写一项')
      return
    }
  }
  editor.saving = true
  try {
    const payload = buildEditorPayload()
    if (editor.record?.id) {
      await updateStorageTargetApi(payload)
      message.success('已更新存储目标')
    } else {
      await createStorageTargetApi(payload)
      message.success('已新增存储目标，建议下一步先做连通测试并确认指纹。')
    }
    closeEditor()
    fetchTargets()
  } finally {
    editor.saving = false
  }
}

async function handleDelete(record) {
  try {
    await deleteStorageTargetApi(record.id)
    message.success('删除成功')
    fetchTargets()
  } catch {
    // request wrapper 已提示
  }
}

// 连通测试 + 指纹确认
const precheck = reactive({
  open: false,
  record: null,
  running: false,
  confirmLoading: false,
  result: null,
})

function openPrecheck(record) {
  precheck.record = { ...record }
  precheck.running = false
  precheck.confirmLoading = false
  precheck.result = record.last_precheck_result || null
  precheck.open = true
}

function closePrecheck() {
  precheck.open = false
  precheck.record = null
  precheck.result = null
  precheck.running = false
  precheck.confirmLoading = false
}

async function runPrecheck() {
  if (!precheck.record?.id) return
  precheck.running = true
  try {
    const res = await precheckStorageTargetApi(precheck.record.id)
    precheck.result = res?.data || null
  } catch {
    // request wrapper
  } finally {
    precheck.running = false
    fetchTargets()
    if (precheck.record?.id) {
      const fresh = (tableData.value || []).find((r) => r.id === precheck.record.id)
      if (fresh) {
        precheck.record.fingerprint_confirmed = !!fresh.fingerprint_confirmed
      }
    }
  }
}

async function confirmFingerprint() {
  const fps = precheck.result?.fingerprints || []
  if (!fps.length) {
    message.error('暂无可确认指纹，请先运行连通测试。')
    return
  }
  const mainFp = fps[0]
  const concatFps = fps.map((f) => `${f.algo}:${f.hash}`).join('; ')
  try {
    await new Promise((resolve, reject) => {
      Modal.confirm({
        title: '确认 SSH Host Key 指纹？',
        content: `请与服务器控制台或登记信息再次核对，确认无误即允许备份任务使用该存储目标。\n\n指纹：\n${concatFps}`,
        okText: '确认无误',
        cancelText: '取消',
        onOk: () => resolve(true),
        onCancel: () => reject(new Error('cancel')),
      })
    })
  } catch {
    return
  }
  precheck.confirmLoading = true
  try {
    await confirmStorageTargetFingerprintApi(precheck.record.id, {
      fingerprint: `${mainFp.algo}:${mainFp.hash}`,
      fingerprint_confirmed: true,
    })
    message.success('指纹已确认，现在可在备份任务中使用该存储目标。')
    if (precheck.record) {
      precheck.record.fingerprint_confirmed = true
    }
    fetchTargets()
  } finally {
    precheck.confirmLoading = false
  }
}

onMounted(fetchTargets)
</script>

<style scoped>
.storage-target-page {
  padding: var(--gi-spacing-sm);
}
.page-title {
  font-size: 18px;
  font-weight: 600;
  color: var(--gi-color-text-primary);
  line-height: 28px;
}

/* 工具栏：标题 +（搜索框、新增按钮）始终同一行，任何宽度都不换行 */
.storage-target-page .gi-page-toolbar {
  flex-wrap: nowrap;
}
.storage-target-page .gi-page-toolbar-right {
  flex-wrap: nowrap;
  white-space: nowrap;
  min-width: 0;
}
.storage-target-page .gi-toolbar-search {
  flex: 0 1 auto;
  width: 320px;
  min-width: 120px;
  max-width: 100%;
}

/* 表格：各列等分整行宽度 */
:deep(.storage-target-table .ant-table-content > table) {
  table-layout: fixed !important;
  width: 100% !important;
}

/* 默认左对齐；列定义上的 align（inline style）优先级更高，操作列用 center 覆盖 */
:deep(.storage-target-table .ant-table-thead > tr > th),
:deep(.storage-target-table .ant-table-tbody > tr > td) {
  text-align: left;
}

/* 表格单元格 */
.target-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.target-name-text {
  font-weight: 500;
  color: var(--gi-color-text-primary);
  font-size: 14px;
}
.type-text {
  font-size: 14px;
  color: var(--gi-color-text-primary);
}

/* 地址列：单行不换行，超出省略，hover 显示完整值 */
.address-single {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 100%;
  font-family: 'SF Mono', Menlo, Consolas, monospace;
  font-size: 13px;
  color: var(--gi-color-text-primary);
}

.connection-text {
  font-size: 14px;
  color: var(--gi-color-text-primary);
  line-height: 22px;
}
.time-text {
  font-size: 13px;
  color: var(--gi-color-text-secondary);
  white-space: nowrap;
}

.action-cell {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  justify-content: center;
}
.more-btn {
  padding: 0 8px;
}

/* 类型行：独立圆圈（不用 button 型），始终单行不换行 */
.type-radio-group {
  display: flex;
  flex-wrap: nowrap;
  white-space: nowrap;
  overflow-x: auto;
  padding-bottom: 2px;
}
.type-radio-group :deep(.ant-radio-wrapper) {
  margin-right: 20px;
  white-space: nowrap;
}
.type-radio-group :deep(.ant-radio-wrapper:last-child) {
  margin-right: 0;
}
/* 未上线类型：整体置灰不可点，并挂一个「未来将支持」小标签 */
.type-radio-group :deep(.ant-radio-wrapper-disabled) {
  cursor: not-allowed;
}
.type-radio-tag {
  margin-left: 6px;
  padding: 0 6px;
  font-size: 11px;
  line-height: 16px;
  border-radius: 4px;
  color: var(--gi-color-text-tertiary, #8c8c8c);
  background: rgba(15, 23, 32, 0.05);
  border: 1px solid var(--gi-color-border, #d9e1e6);
}

/* 表单 */
.storage-target-form :deep(.ant-form-item-label) {
  font-weight: 500;
}
.editor-form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 16px;
}

/* 连通测试抽屉 */
.precheck-shell {
  display: flex;
  flex-direction: column;
}
.precheck-summary-card {
  background: linear-gradient(135deg, #f6ffed 0%, #f0f5ff 100%);
  border: 1px solid rgba(22, 119, 255, 0.12);
  border-radius: var(--gi-radius-card);
  padding: 16px 18px;
  margin-bottom: 16px;
  display: flex;
  align-items: center;
  gap: 14px;
}
.precheck-summary-icon {
  width: 48px;
  height: 48px;
  border-radius: 12px;
  background: rgba(22, 119, 255, 0.1);
  color: var(--gi-color-info);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 24px;
}
.precheck-summary-body {
  flex: 1;
  min-width: 0;
}
.precheck-summary-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--gi-color-text-primary);
}
.precheck-summary-addr {
  font-size: 13px;
  color: var(--gi-color-text-secondary);
  word-break: break-all;
  margin-top: 2px;
}
.precheck-status-tag {
  font-size: 13px;
  padding: 2px 10px;
}
.fingerprint-panel {
  border: 1px dashed #d9d9d9;
  border-radius: var(--gi-radius-card);
  padding: 16px 18px;
  margin-top: 16px;
  background: #fcfcfc;
}
.fingerprint-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
}
.fingerprint-title {
  font-weight: 600;
  color: var(--gi-color-text-primary);
}
.fingerprint-sub {
  display: block;
  color: var(--gi-color-text-secondary);
  font-size: 12px;
  margin-top: 4px;
  line-height: 18px;
  max-width: 520px;
}
.fingerprint-list {
  padding: 0;
  margin: 12px 0 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.fingerprint-item {
  font-family: 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px;
  display: flex;
  gap: 8px;
  line-height: 22px;
}
.fingerprint-algo {
  color: var(--gi-color-info);
  min-width: 90px;
}
.fingerprint-hash {
  color: #434343;
  word-break: break-all;
}
.server-output {
  background: #1f1f1f;
  color: #e6e6e6;
  padding: 12px 16px;
  border-radius: 6px;
  font-size: 12px;
  max-height: 320px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  line-height: 18px;
}

/* 编辑器抽屉 footer：测试连接靠左，取消/保存靠右（保存为最右主操作） */
.editor-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}

@media (max-width: 768px) {
  .editor-form-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 768px) {
  /* 公共工具栏这里被显式改成 nowrap，窄屏会挤压搜索框与按钮 */
  .storage-target-page .gi-page-toolbar {
    flex-wrap: wrap;
    row-gap: 8px;
  }

  .storage-target-page .gi-page-toolbar-right {
    flex-wrap: wrap;
    white-space: normal;
    width: 100%;
  }

  .storage-target-page .gi-toolbar-search {
    flex: 1 1 100%;
    width: 100%;
    min-width: 0;
  }

  /* 表格没有 scroll.x，6 列均分后地址/时间被截断 */
  :deep(.storage-target-table .ant-table-content) {
    overflow-x: auto;
  }

  :deep(.storage-target-table .ant-table-content > table) {
    width: 900px !important;
    table-layout: auto !important;
  }

  .address-single {
    white-space: normal;
    word-break: break-all;
  }

  .storage-target-page {
    padding: var(--gi-spacing-ssm);
  }
}
</style>
