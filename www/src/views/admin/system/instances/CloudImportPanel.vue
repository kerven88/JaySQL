<template>
  <div class="cloud-import-panel" :class="{ 'is-disabled': !canManage }">
    <!-- ============ 区块1: 云厂商选择 ============ -->
    <div class="instance-form-section">
      <div class="vendor-card-row">
        <div
          v-for="v in vendorList"
          :key="v.value"
          class="vendor-card"
          :class="{
            'is-active': formData.vendor === v.value,
            'is-disabled': !!v.disabled,
          }"
          @click="onVendorClick(v)"
        >
          <div class="vendor-card-meta">
            <div class="vendor-card-name">
              {{ v.label }}
              <a-tag v-if="v.disabled" color="default" class="vendor-card-tag">开发中</a-tag>
            </div>
            <template v-if="!v.hideDesc">
              <a-tooltip :title="v.desc" placement="bottomLeft">
                <div class="vendor-card-desc">{{ v.desc }}</div>
              </a-tooltip>
            </template>
            <div v-else class="vendor-card-desc vendor-card-desc-empty" />
          </div>
          <div v-if="formData.vendor === v.value" class="vendor-card-check">
            <CheckCircleOutlined />
          </div>
        </div>
      </div>
    </div>

    <!-- ============ 区块2: 凭据配置 ============ -->
    <div class="instance-form-section">
      <a-form layout="vertical" :model="formData" :disabled="!canManage" class="cloud-cred-form">
        <a-row :gutter="16">
          <a-col :span="12">
            <a-form-item label="AccessKey ID">
              <a-input
                v-model:value="formData.ak_id"
                placeholder="请输入 AccessKey ID"
                allow-clear
                @change="onCredChange"
              />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="AccessKey Secret">
              <div class="inline-action-row">
                <a-input-password
                  v-model:value="formData.ak_secret"
                  placeholder="请输入 AccessKey Secret"
                  @change="onCredChange"
                  style="flex: 1; min-width: 0"
                />
                <a-button
                  type="primary"
                  ghost
                  class="inline-action-btn"
                  :loading="uiState.validating"
                  :disabled="!canValidateCred || !canManage"
                  @click="onValidate"
                >
                  验证
                </a-button>
              </div>
            </a-form-item>
          </a-col>
        </a-row>
        <a-form-item label="地域">
          <div class="inline-action-row inline-action-row-region">
            <a-select
              v-model:value="formData.regions"
              mode="multiple"
              style="flex: 1; min-width: 0"
              :placeholder="regionPlaceholder"
              :options="regionOptions"
              :max-tag-count="4"
              allow-clear
              :disabled="uiState.validating || uiState.discovering"
            />
            <a-button
              type="primary"
              class="inline-action-btn discover-btn-inline"
              :loading="uiState.discovering"
              :disabled="!canDiscover || !canManage"
              @click="onDiscover"
            >
              {{ uiState.discovering ? '拉取中…' : '拉取实例列表' }}
            </a-button>
            <a-tooltip title="只看运行中实例" placement="top">
              <a-checkbox
                v-model:checked="formData.only_running"
                class="only-running-check-inline only-running-check-iconic"
              />
            </a-tooltip>
          </div>
        </a-form-item>
        <div class="cred-toolbar-meta cred-meta-row">
          <a-alert
            v-if="uiState.discoverStatus === 'error'"
            :message="uiState.discoverMessage"
            type="error"
            show-icon
            class="cred-alert"
          />
        </div>
      </a-form>
    </div>

    <!-- ============ 区块3: 实例导入 ============ -->
    <div v-if="instanceList.length" class="instance-form-section">
      <!-- 工具条：4 个批量预设 + 搜索框（18/18/18/18/28 比例） -->
      <div class="instance-toolbar-row">
        <div class="toolbar-grid">
          <div class="toolbar-item toolbar-item-env">
            <label class="toolbar-field-label">目标环境</label>
            <a-select
              v-model:value="batchForm.environment"
              :options="environments"
              :field-names="{ label: 'name', value: 'id' }"
              placeholder="请选择"
              :disabled="!canManage"
              class="toolbar-control"
            />
          </div>
          <div class="toolbar-item toolbar-item-user">
            <label class="toolbar-field-label">默认登录账号</label>
            <a-input
              v-model:value="batchForm.default_user"
              placeholder="请输入数据库账号"
              :disabled="!canManage"
              allow-clear
              class="toolbar-control"
            />
          </div>
          <div class="toolbar-item toolbar-item-pwd">
            <label class="toolbar-field-label">默认登录密码</label>
            <a-input-password
              v-model:value="batchForm.default_pwd"
              placeholder="请输入数据库密码"
              :disabled="!canManage"
              class="toolbar-control"
            />
          </div>
          <div class="toolbar-item toolbar-item-dedup">
            <label class="toolbar-field-label">去重策略</label>
            <a-select
              v-model:value="batchForm.duplicate_policy"
              :options="duplicatePolicyOptions"
              :disabled="!canManage"
              class="toolbar-control"
            />
          </div>
          <div class="toolbar-item toolbar-item-search">
            <label class="toolbar-field-label">搜索</label>
            <a-input
              v-model:value="searchKeyword"
              placeholder="搜索实例名称 / ID / 地址 / 引擎"
              allow-clear
              class="toolbar-control toolbar-search"
              @pressEnter="() => {}"
            >
              <template #suffix>
                <SearchOutlined class="toolbar-search-icon" />
              </template>
            </a-input>
          </div>
        </div>
      </div>

      <!-- 表格 -->
      <a-table
        :columns="tableColumns"
        :data-source="filteredInstanceList"
        :pagination="{ pageSize: 8, showSizeChanger: false }"
        :scroll="{ y: tableScrollY }"
        :row-selection="tableRowSelection"
        :row-key="(r) => r.instance_id"
        size="middle"
        class="cloud-instance-table"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'status'">
            <a-tag :color="statusTagColor(record.instance_status)" class="compact-tag">
              {{ record.is_running ? '运行中' : record.instance_status }}
            </a-tag>
          </template>
          <template v-else-if="column.key === 'engine'">
            <span class="engine-text">{{ record.engine }}</span>
            <span class="engine-version">{{ record.engine_version || '-' }}</span>
          </template>
          <template v-else-if="column.key === 'addr'">
            <span v-if="record.hostname && record.port" class="addr-text">
              {{ record.hostname }}:{{ record.port }}
            </span>
            <a-tag v-else color="warning" class="compact-tag">无可用地址</a-tag>
          </template>
          <template v-else-if="column.key === 'region'">
            <span class="region-tag">{{ regionDisplay(record.region_id) }}</span>
          </template>
        </template>
      </a-table>
    </div>

    <!-- ============ 导入结果 ============ -->
    <a-modal
      :open="showResultModal"
      title="导入结果报告"
      :width="680"
      :footer="null"
      @cancel="showResultModal = false"
      wrap-class-name="instance-form-modal-wrap"
    >
      <div v-if="importResult" class="import-result-body">
        <div class="result-stat-row">
          <div class="stat-card stat-total">
            <div class="stat-num">{{ importResult.total }}</div>
            <div class="stat-label">总计</div>
          </div>
          <div class="stat-card stat-success">
            <div class="stat-num">{{ importResult.success }}</div>
            <div class="stat-label">成功</div>
          </div>
          <div class="stat-card stat-fail">
            <div class="stat-num">{{ importResult.failed }}</div>
            <div class="stat-label">失败</div>
          </div>
          <div class="stat-card stat-skip">
            <div class="stat-num">{{ importResult.skipped }}</div>
            <div class="stat-label">跳过</div>
          </div>
        </div>
        <div class="result-detail-title">详细结果</div>
        <a-table
          :columns="resultColumns"
          :data-source="importResult.items"
          :pagination="{ pageSize: 8 }"
          size="small"
          :row-key="(r, i) => `${r.cloud_instance_id}_${i}`"
          class="result-detail-table"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'status'">
              <a-tag :color="resultStatusColor(record.status)" class="compact-tag">
                {{ resultStatusLabel(record.status) }}
              </a-tag>
            </template>
          </template>
        </a-table>
        <div class="result-footer">
          <a-button type="primary" @click="onResultConfirm">
            {{ importResult.success > 0 ? '完成，关闭并刷新列表' : '关闭' }}
          </a-button>
        </div>
      </div>
    </a-modal>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, reactive, ref, watch, nextTick } from 'vue'
import { message } from 'ant-design-vue'
import {
  CheckCircleOutlined,
  SafetyOutlined,
  CloudServerOutlined,
  ImportOutlined,
  SearchOutlined,
} from '@ant-design/icons-vue'
import {
  validateCloudAccountApi,
  discoverCloudInstancesApi,
  importCloudInstancesApi,
} from '@/api/admin'

const props = defineProps({
  environments: { type: Array, default: () => [] },
  canManage: { type: Boolean, default: true },
})
const emit = defineEmits(['imported'])

// ====== 云厂商配置 ======
const vendorList = [
  {
    value: 'aliyun',
    label: '阿里云',
    icon: '',
    desc: '支持 RDS (MySQL / PostgreSQL / SQL Server / MariaDB / PPAS / PolarDB)',
    disabled: false,
    hideDesc: false,
  },
  {
    value: 'huawei',
    label: '华为云',
    icon: '',
    desc: '',
    disabled: true,
    hideDesc: true,
  },
  {
    value: 'tencent',
    label: '腾讯云',
    icon: '',
    desc: '',
    disabled: true,
    hideDesc: true,
  },
]

// ====== 地域预设 ======
const PRESET_REGIONS = [
  { value: 'cn-hangzhou', label: '华东1 (杭州) cn-hangzhou' },
  { value: 'cn-shanghai', label: '华东2 (上海) cn-shanghai' },
  { value: 'cn-nanjing', label: '华东5 (南京) cn-nanjing' },
  { value: 'cn-beijing', label: '华北2 (北京) cn-beijing' },
  { value: 'cn-zhangjiakou', label: '华北3 (张家口) cn-zhangjiakou' },
  { value: 'cn-huhehaote', label: '华北5 (呼和浩特) cn-huhehaote' },
  { value: 'cn-wulanchabu', label: '华北6 (乌兰察布) cn-wulanchabu' },
  { value: 'cn-qingdao', label: '华北1 (青岛) cn-qingdao' },
  { value: 'cn-shenzhen', label: '华南1 (深圳) cn-shenzhen' },
  { value: 'cn-guangzhou', label: '华南3 (广州) cn-guangzhou' },
  { value: 'cn-heyuan', label: '华南2 (河源) cn-heyuan' },
  { value: 'cn-chengdu', label: '西南1 (成都) cn-chengdu' },
  { value: 'cn-hongkong', label: '中国 (香港) cn-hongkong' },
  { value: 'ap-southeast-1', label: '新加坡 ap-southeast-1' },
  { value: 'ap-southeast-2', label: '悉尼 ap-southeast-2' },
  { value: 'ap-southeast-3', label: '吉隆坡 ap-southeast-3' },
  { value: 'ap-southeast-5', label: '雅加达 ap-southeast-5' },
  { value: 'ap-south-1', label: '孟买 ap-south-1' },
  { value: 'ap-northeast-1', label: '东京 ap-northeast-1' },
  { value: 'us-east-1', label: '弗吉尼亚 us-east-1' },
  { value: 'us-west-1', label: '硅谷 us-west-1' },
  { value: 'eu-central-1', label: '法兰克福 eu-central-1' },
  { value: 'eu-west-1', label: '伦敦 eu-west-1' },
  { value: 'me-east-1', label: '迪拜 me-east-1' },
]
const DOMESTIC_COMMON_REGIONS = [
  'cn-hangzhou',
  'cn-shanghai',
  'cn-beijing',
  'cn-shenzhen',
  'cn-zhangjiakou',
  'cn-huhehaote',
  'cn-chengdu',
  'cn-hongkong',
]

const duplicatePolicyOptions = [
  { value: 'skip', label: '跳过已存在' },
  { value: 'update', label: '覆盖已存在' },
  { value: 'abort', label: '遇重即中止' },
]
const regionOptions = PRESET_REGIONS
const regionPlaceholder = '选择至少一个地域 (如 华东1 杭州)'

const regionDisplay = (id) => {
  const found = PRESET_REGIONS.find((r) => r.value === id)
  return found ? found.label : id
}

// ====== 表单状态 ======
const formData = reactive({
  vendor: 'aliyun',
  ak_id: '',
  ak_secret: '',
  regions: [],
  only_running: true,
})

const batchForm = reactive({
  environment: null,
  default_user: '',
  default_pwd: '',
  duplicate_policy: 'skip',
})

const uiState = reactive({
  validating: false,
  validateStatus: 'idle', // idle / success / error
  validateMessage: '',
  discovering: false,
  discoverStatus: 'idle',
  discoverMessage: '',
  lastSummary: '',
  importing: false,
})

const instanceList = ref([])
const selectedRowKeys = ref([])
const searchKeyword = ref('')
const showResultModal = ref(false)
const importResult = ref(null)
const scrollContainerRef = ref(null)
const tableScrollY = ref(300)
const resultColumns = [
  { title: '实例名', dataIndex: 'display_name', key: 'name', ellipsis: true },
  { title: '地址', dataIndex: 'hostname', key: 'host', width: 220, ellipsis: true, customRender: ({ record }) => (record.hostname ? `${record.hostname}:${record.port}` : '-') },
  { title: '状态', key: 'status', width: 80 },
  { title: '说明', dataIndex: 'message', key: 'msg', ellipsis: true },
]

// ====== 计算属性 ======
const canValidateCred = computed(
  () =>
    !!formData.vendor &&
    !!formData.ak_id?.trim() &&
    !!formData.ak_secret?.trim() &&
    !vendorList.find((v) => v.value === formData.vendor)?.disabled,
)
const canDiscover = computed(
  () =>
    uiState.validateStatus === 'success' &&
    formData.regions?.length > 0 &&
    !vendorList.find((v) => v.value === formData.vendor)?.disabled,
)
const canImport = computed(() => selectedRowKeys.value.length > 0 && canImportAll.value)
const canImportAll = computed(() => {
  if (!batchForm.environment) return false
  if (!batchForm.default_user?.trim()) return false
  if (!batchForm.default_pwd?.trim()) return false
  return importableCount.value > 0
})
const importDisableReason = computed(() => {
  if (importableCount.value === 0) return '暂无可导入实例（请先拉取实例列表）'
  if (!batchForm.environment) return '请先选择目标环境'
  if (!batchForm.default_user?.trim()) return '请输入默认数据库账号'
  if (!batchForm.default_pwd?.trim()) return '请输入默认数据库密码'
  return ''
})
const importableCount = computed(() => instanceList.value.filter((r) => r.hostname && r.port > 0).length)
const filteredInstanceList = computed(() => {
  const kw = (searchKeyword.value || '').trim().toLowerCase()
  if (!kw) return instanceList.value
  return instanceList.value.filter((r) => {
    return (
      (r.instance_name || '').toLowerCase().includes(kw) ||
      (r.instance_id || '').toLowerCase().includes(kw) ||
      (r.hostname || '').toLowerCase().includes(kw) ||
      (r.engine || '').toLowerCase().includes(kw) ||
      (r.db_type || '').toLowerCase().includes(kw)
    )
  })
})
const tableRowSelection = computed(() => ({
  selectedRowKeys: selectedRowKeys.value,
  onChange: (keys) => {
    selectedRowKeys.value = keys
  },
  getCheckboxProps: (record) => ({
    disabled: !record.hostname || record.port <= 0,
  }),
}))

const tableColumns = [
  { title: '实例名', dataIndex: 'instance_name', key: 'name', width: 180, ellipsis: true },
  { title: '地域', key: 'region', width: 150 },
  { title: '引擎 / 版本', key: 'engine', width: 130 },
  { title: '连接地址', key: 'addr', width: 220, ellipsis: true },
  { title: '规格', dataIndex: 'instance_class', key: 'class', width: 150, ellipsis: true },
  { title: '状态', key: 'status', width: 90 },
]

const statusTagColor = (s) => {
  switch (s) {
    case 'Running':
      return 'green'
    case 'Stopped':
      return 'default'
    case 'Creating':
      return 'blue'
    case 'Rebooting':
      return 'cyan'
    case 'Restoring':
      return 'purple'
    case 'Upgrading':
      return 'orange'
    case 'Deleting':
      return 'red'
    default:
      return 'default'
  }
}
const resultStatusColor = (s) => {
  return s === 'success' ? 'green' : s === 'skipped' ? 'default' : 'red'
}
const resultStatusLabel = (s) => (s === 'success' ? '成功' : s === 'skipped' ? '跳过' : '失败')

// ====== 动作 ======
const onVendorClick = (v) => {
  if (!props.canManage) return
  if (v.disabled) return
  formData.vendor = v.value
}
const selectCommonRegions = () => {
  formData.regions = [...DOMESTIC_COMMON_REGIONS]
}
const onCredChange = () => {
  uiState.validateStatus = 'idle'
  uiState.validateMessage = ''
  uiState.discoverStatus = 'idle'
  uiState.discoverMessage = ''
  uiState.lastSummary = ''
}
const onValidate = async () => {
  if (!canValidateCred.value) return
  uiState.validating = true
  uiState.validateStatus = 'idle'
  uiState.validateMessage = ''
  try {
    const resp = await validateCloudAccountApi(
      {
        vendor: formData.vendor,
        ak_id: formData.ak_id.trim(),
        ak_secret: formData.ak_secret.trim(),
      },
      { silentHttpError: true, silentBusinessError: true },
    )
    const data = resp?.data || resp
    if (data?.valid) {
      uiState.validateStatus = 'success'
      uiState.validateMessage = data.message || '验证通过'
      message.success('凭据验证通过')
    } else {
      uiState.validateStatus = 'error'
      uiState.validateMessage = data?.message || '验证失败'
      message.error(data?.message || '凭据验证失败')
    }
  } catch (e) {
    uiState.validateStatus = 'error'
    uiState.validateMessage = e?.message || e?.data?.message || '验证失败'
    message.error(uiState.validateMessage)
  } finally {
    uiState.validating = false
  }
}

const onDiscover = async () => {
  if (!canDiscover.value) return
  uiState.discovering = true
  uiState.discoverStatus = 'idle'
  uiState.discoverMessage = ''
  uiState.lastSummary = ''
  instanceList.value = []
  selectedRowKeys.value = []
  try {
    const resp = await discoverCloudInstancesApi(
      {
        vendor: formData.vendor,
        ak_id: formData.ak_id.trim(),
        ak_secret: formData.ak_secret.trim(),
        regions: formData.regions,
        only_running: !!formData.only_running,
      },
      { silentHttpError: true, silentBusinessError: true },
    )
    const data = resp?.data || resp
    const list = (data?.instances || []).map((it) => ({
      ...it,
      key: it.instance_id,
    }))
    instanceList.value = list
    selectedRowKeys.value = list
      .filter((r) => r.hostname && r.port > 0)
      .map((r) => r.instance_id)
    const running = list.filter((r) => r.is_running).length
    const addrOk = list.filter((r) => r.hostname && r.port > 0).length
    uiState.lastSummary = `共 ${list.length} 个实例，运行中 ${running} 个，地址可用 ${addrOk} 个`
    message.success(`拉取成功，共 ${list.length} 个实例`)
    await nextTick()
    calcTableScrollY()
  } catch (e) {
    uiState.discoverStatus = 'error'
    uiState.discoverMessage = e?.message || e?.data?.message || '拉取实例列表失败'
    message.error(uiState.discoverMessage)
  } finally {
    uiState.discovering = false
  }
}

const onImport = async (isAll) => {
  if (!canImportAll.value) {
    if (importDisableReason.value) message.warning(importDisableReason.value)
    return
  }
  const rows = isAll
    ? filteredInstanceList.value.filter((r) => r.hostname && r.port > 0)
    : filteredInstanceList.value.filter(
        (r) => selectedRowKeys.value.includes(r.instance_id) && r.hostname && r.port > 0,
      )
  if (!rows.length) {
    message.warning('请选择至少一个地址可用的实例')
    return
  }
  uiState.importing = true
  try {
    const payload = {
      vendor: formData.vendor,
      ak_id: formData.ak_id.trim(),
      ak_secret: formData.ak_secret.trim(),
      environment: batchForm.environment,
      default_user: batchForm.default_user.trim(),
      default_pwd: batchForm.default_pwd,
      duplicate_policy: batchForm.duplicate_policy,
      extra_config: {},
      instances: rows.map((r) => ({
        cloud_instance_id: r.instance_id,
        cloud_region: r.region_id,
        hostname: r.hostname,
        port: r.port,
        db_type: r.db_type,
        engine_version: r.engine_version,
        display_name: r.instance_name,
      })),
    }
    const resp = await importCloudInstancesApi(payload, { silentBusinessError: true, silentHttpError: true })
    const data = resp?.data || resp
    importResult.value = data
    showResultModal.value = true
    if (data.success > 0) {
      message.success(`导入完成：成功 ${data.success}，失败 ${data.failed}，跳过 ${data.skipped}`)
    } else if (data.failed > 0) {
      message.error(`导入失败：失败 ${data.failed}，跳过 ${data.skipped}`)
    } else {
      message.warning(`已跳过 ${data.skipped} 个实例`)
    }
  } catch (e) {
    message.error(e?.message || e?.data?.message || '导入失败')
  } finally {
    uiState.importing = false
  }
}

const onResultConfirm = () => {
  showResultModal.value = false
  const ok = importResult.value && importResult.value.success > 0
  if (ok) {
    emit('imported', {
      success: true,
      result: importResult.value,
    })
  }
}

const calcTableScrollY = () => {
  // 粗略控制表格高度,避免弹窗过长
  const len = filteredInstanceList.value.length
  if (len <= 5) tableScrollY.value = 220
  else if (len <= 10) tableScrollY.value = 320
  else tableScrollY.value = 380
}

watch(
  () => [filteredInstanceList.value.length],
  () => nextTick(calcTableScrollY),
)

onBeforeUnmount(() => {})

defineExpose({
  get canImport() {
    return canImport.value
  },
  get canImportAll() {
    return canImportAll.value
  },
  get isImporting() {
    return uiState.importing
  },
  get importDisabledReason() {
    return importDisableReason.value || ''
  },
  triggerImport: () => {
    const doImportAll = !selectedRowKeys.value.length && canImportAll.value
    return onImport(doImportAll)
  },
})
</script>

<style scoped>
.cloud-import-panel {
  padding-top: 2px;
}
.cloud-import-panel.is-disabled {
  opacity: 0.8;
  cursor: not-allowed;
}
.instance-form-section {
  margin-top: 4px;
  padding-top: 4px;
}
.instance-form-section-title {
  margin-bottom: 12px;
  color: rgba(0, 0, 0, 0.72);
  font-size: 13px;
  font-weight: 600;
}

.vendor-card-row {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}
.vendor-card {
  position: relative;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 14px;
  border: 1.5px solid #e5e7eb;
  border-radius: 10px;
  background: #fafafa;
  cursor: pointer;
  transition: all 0.15s ease;
  min-height: 70px;
}
.vendor-card:hover {
  border-color: #94a3b8;
  background: #fff;
}
.vendor-card.is-active {
  border-color: #0f766e;
  background: #f0fdfa;
  box-shadow: 0 0 0 3px rgba(15, 118, 110, 0.1);
}
.vendor-card.is-disabled {
  opacity: 0.55;
  cursor: not-allowed;
  background: #f3f4f6;
}
.vendor-card.is-disabled:hover {
  border-color: #e5e7eb;
  background: #f3f4f6;
}
.vendor-card-icon {
  width: 40px;
  height: 40px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 20px;
  flex-shrink: 0;
  background: #fff;
  border: 1px solid #e5e7eb;
}
.vendor-card-icon.icon-aliyun {
  background: linear-gradient(135deg, #fff7e6 0%, #ffe7ba 100%);
  border-color: #ffd591;
}
.vendor-card-icon.icon-huawei {
  background: linear-gradient(135deg, #f6ffed 0%, #d9f7be 100%);
  border-color: #b7eb8f;
}
.vendor-card-icon.icon-tencent {
  background: linear-gradient(135deg, #e6f7ff 0%, #bae7ff 100%);
  border-color: #91d5ff;
}
.vendor-card-icon span {
  line-height: 1;
}
.vendor-card-meta {
  flex: 1;
  min-width: 0;
}
.vendor-card-name {
  font-size: 14px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.85);
  display: flex;
  align-items: center;
  gap: 6px;
}
.vendor-card-tag {
  font-size: 11px;
  padding: 0 5px;
  height: 18px;
  line-height: 16px;
}
.vendor-card-desc {
  margin-top: 2px;
  font-size: 12px;
  color: rgba(0, 0, 0, 0.55);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-height: 18px;
}
.vendor-card-desc-empty {
  visibility: hidden;
  pointer-events: none;
}
.vendor-card-check {
  color: #0f766e;
  font-size: 18px;
  flex-shrink: 0;
}

:deep(.cloud-cred-form .ant-select-selector) {
  height: 40px !important;
  min-height: 40px !important;
  border-radius: 8px !important;
  display: flex !important;
  align-items: center !important;
  box-sizing: border-box !important;
  padding: 0 11px !important;
}

:deep(.cloud-cred-form) {
  .ant-form-item {
    margin-bottom: 14px;
  }
  .ant-form-item-control {
    line-height: 1;
    overflow: visible !important;
  }
  .ant-form-item-control-input {
    min-height: 40px !important;
    overflow: visible !important;
  }
  .ant-form-item-control-input-content {
    overflow: visible !important;
  }
  .ant-form-item-explain,
  .ant-form-item-extra {
    display: none;
  }
  .ant-row {
    overflow: visible;
  }
  .ant-row > [class^="ant-col"] {
    overflow: visible;
  }
}

.region-select-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.region-quick-btn {
  flex-shrink: 0;
  height: 40px;
  border-radius: 8px;
}

.inline-action-row {
  display: flex;
  align-items: stretch;
  gap: 10px;
  width: 100%;
}
.inline-action-btn {
  flex-shrink: 0;
  height: 40px;
  border-radius: 8px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
}
.discover-btn-inline {
  border-color: #7fbfae;
  background: #e6f4ef;
  color: #0f766e;
  padding: 0 14px;
}
.discover-btn-inline:hover,
.discover-btn-inline:focus {
  border-color: #66b39f !important;
  background: #d4ecdf !important;
  color: #0f766e !important;
}
.only-running-check-inline {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 40px;
  color: rgba(0, 0, 0, 0.75);
  font-size: 13px;
  white-space: nowrap;
}
.only-running-check-iconic {
  width: 40px;
  padding: 0;
}
.cred-meta-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: -4px;
}
.cred-alert {
  margin: 0;
  flex: 1;
  min-width: 240px;
  border-radius: 8px;
  padding: 6px 12px;
}
:deep(.cred-alert .ant-alert-message) {
  font-size: 13px;
}
.discover-hint {
  color: rgba(0, 0, 0, 0.6);
  font-size: 12.5px;
  margin-left: auto;
}

:deep(.batch-preset-form-inline) {
  margin-bottom: 10px;
}
:deep(.batch-preset-form-inline .ant-form-item) {
  margin-bottom: 12px;
}
:deep(.batch-preset-form-inline .ant-select-selector) {
  height: 38px !important;
  min-height: 38px !important;
  border-radius: 8px !important;
  display: flex !important;
  align-items: center !important;
}
:deep(.batch-preset-form-inline) {
  .ant-form-item-control,
  .ant-form-item-control-input,
  .ant-form-item-control-input-content {
    overflow: visible !important;
  }
  .ant-form-item-control-input {
    min-height: 38px !important;
  }
  .ant-form-item-explain {
    display: none;
  }
}

.instance-toolbar-row {
  width: 100%;
  margin-bottom: 10px;
}
.toolbar-grid {
  display: flex;
  flex-direction: row;
  align-items: stretch;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
  box-sizing: border-box;
}
.toolbar-item {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 100%;
  box-sizing: border-box;
}
/* flex-grow 总和 1.8 + 1.8 + 1.8 + 1.8 + 2.8 = 10，对应 18/18/18/18/28 占比 */
.toolbar-item-env,
.toolbar-item-user,
.toolbar-item-pwd,
.toolbar-item-dedup {
  flex: 1.8 1.8 0;
}
.toolbar-item-search {
  flex: 2.8 2.8 0;
}
.toolbar-field-label {
  display: block;
  flex-shrink: 0;
  font-size: 13px;
  font-weight: 500;
  color: rgba(0, 0, 0, 0.85);
  margin: 0 0 6px 0;
  padding: 0;
  line-height: 18px;
  height: 18px;
  box-sizing: border-box;
}
.toolbar-control {
  width: 100%;
  max-width: 100%;
  min-width: 0;
  box-sizing: border-box;
}

/* 高度/圆角/对齐 —— 统一所有控件：select / input / input-password 统一 38px 高度 */
:deep(.toolbar-control.ant-select .ant-select-selector) {
  height: 38px !important;
  min-height: 38px !important;
  line-height: 38px;
  border-radius: 8px !important;
  display: flex !important;
  align-items: center !important;
  padding: 0 11px !important;
  box-sizing: border-box;
}
:deep(.toolbar-control.ant-select) {
  height: 38px;
}
:deep(.toolbar-control.ant-input),
:deep(.toolbar-control.ant-input-affix-wrapper) {
  height: 38px !important;
  min-height: 38px !important;
  line-height: 38px;
  border-radius: 8px;
  padding: 0 11px;
  box-sizing: border-box;
}
:deep(.toolbar-control.ant-input-affix-wrapper) {
  display: flex;
  align-items: center;
}
:deep(.toolbar-control.ant-input-affix-wrapper .ant-input) {
  height: 36px !important;
  line-height: 36px;
  padding: 0;
  border: none;
  box-shadow: none;
  background: transparent;
}

/* 选中态/控件容器不裁切，防止输入框底部被切 */
.toolbar-item :deep(.ant-form-item-control),
.toolbar-item :deep(.ant-form-item-control-input),
.toolbar-item :deep(.ant-form-item-control-input-content) {
  overflow: visible !important;
}
.toolbar-item :deep(.ant-form-item-control-input) {
  min-height: 38px !important;
}
.toolbar-item :deep(.ant-form-item-explain) {
  display: none;
}

/* 搜索框内嵌放大镜 */
.toolbar-search-icon {
  color: rgba(0, 0, 0, 0.45);
  font-size: 14px;
}
:deep(.toolbar-search.ant-input-affix-wrapper) {
  padding-right: 28px;
  position: relative;
}
:deep(.toolbar-search.ant-input-affix-wrapper .ant-input-suffix) {
  position: absolute;
  right: 11px;
  top: 50%;
  transform: translateY(-50%);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  pointer-events: none;
}

/* 清理掉旧的独立搜索行样式（不再使用） */
.instance-search-row {
  display: none;
}
.instance-search-input {
  max-width: 100%;
}
:deep(.instance-search-input .ant-input-affix-wrapper) {
  height: 36px;
  border-radius: 8px;
}

.cloud-instance-table {
  border-radius: 10px;
  overflow: hidden;
}
:deep(.cloud-instance-table .ant-table-thead > tr > th) {
  background: #f8fafc;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.78);
  font-size: 12.5px;
  padding: 10px 12px;
}
:deep(.cloud-instance-table .ant-table-tbody > tr > td) {
  padding: 8px 12px;
  font-size: 12.5px;
  vertical-align: middle;
}
.compact-tag {
  font-size: 11.5px;
  padding: 0 6px;
  height: 20px;
  line-height: 18px;
  border-radius: 5px;
}
.engine-text {
  font-weight: 600;
  color: rgba(0, 0, 0, 0.8);
}
.engine-version {
  color: rgba(0, 0, 0, 0.5);
  font-size: 11.5px;
  margin-left: 6px;
}
.addr-text {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: #334155;
  background: #f1f5f9;
  padding: 2px 6px;
  border-radius: 5px;
}
.region-tag {
  font-size: 12px;
  color: rgba(0, 0, 0, 0.7);
}

/* ===== 导入结果弹窗 ===== */
.import-result-body {
  padding: 4px 2px;
}
.result-stat-row {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
  margin-bottom: 14px;
}
.stat-card {
  border-radius: 10px;
  padding: 12px 14px;
  text-align: center;
  border: 1px solid #e5e7eb;
}
.stat-num {
  font-size: 24px;
  font-weight: 700;
  line-height: 1.2;
}
.stat-label {
  margin-top: 4px;
  font-size: 12px;
  color: rgba(0, 0, 0, 0.55);
}
.stat-total {
  background: #f8fafc;
}
.stat-total .stat-num {
  color: #0f172a;
}
.stat-success {
  background: #f0fdfa;
  border-color: #99f6e4;
}
.stat-success .stat-num {
  color: #0f766e;
}
.stat-fail {
  background: #fef2f2;
  border-color: #fecaca;
}
.stat-fail .stat-num {
  color: #b91c1c;
}
.stat-skip {
  background: #fafafa;
  border-color: #e5e7eb;
}
.stat-skip .stat-num {
  color: #64748b;
}
.result-detail-title {
  font-size: 13px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.72);
  margin-bottom: 8px;
}
.result-detail-table {
  border-radius: 8px;
  overflow: hidden;
}
:deep(.result-detail-table .ant-table-thead > tr > th) {
  background: #f8fafc;
  padding: 8px 12px;
  font-size: 12.5px;
}
:deep(.result-detail-table .ant-table-tbody > tr > td) {
  padding: 7px 12px;
  font-size: 12.5px;
}
.result-footer {
  margin-top: 16px;
  display: flex;
  justify-content: flex-end;
}
.result-footer .ant-btn {
  height: 36px;
  border-radius: 8px;
  padding: 0 18px;
}

@media (max-width: 768px) {
  :deep(.ant-table) {
    min-width: 940px;
  }

  :deep(.ant-table-wrapper) {
    overflow-x: auto;
  }
}
</style>
