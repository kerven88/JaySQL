<template>
  <a-modal
    :open="open"
    :width="modalWidth"
    :body-style="{ maxHeight: bodyMaxHeight, overflowY: 'auto' }"
    :centered="true"
    :mask-closable="false"
    :confirm-loading="uiState.importing"
    @cancel="handleCancel"
  >
    <template #title>
      <div class="import-title-row">
        <span>批量导入实例</span>
      </div>
    </template>
    <a-steps class="import-steps" :current="uiState.step" size="small">
      <a-step title="上传文件" />
      <a-step title="预览校验" />
      <a-step title="执行导入" />
      <a-step title="导入结果" />
    </a-steps>
    <div class="import-body">
      <template v-if="uiState.step === 0">
        <div class="step0-header-row">
          <a-radio-group v-model:value="uiState.uploadMode" button-style="solid" class="import-mode-tabs">
            <a-radio-button value="file">上传文件</a-radio-button>
            <a-radio-button value="paste">粘贴文本</a-radio-button>
          </a-radio-group>
          <a-space class="step0-template-actions">
            <a-tooltip title="下载 CSV 模板" placement="top">
              <a-button size="small" class="step0-template-btn" @click="handleDownloadTemplate('csv')">
                <FileTextOutlined class="step0-template-btn__icon" />
                <span class="step0-template-btn__text">下载 CSV 模板</span>
              </a-button>
            </a-tooltip>
            <a-tooltip title="下载 MD 模板" placement="top">
              <a-button size="small" class="step0-template-btn" @click="handleDownloadTemplate('markdown')">
                <FileTextOutlined class="step0-template-btn__icon" />
                <span class="step0-template-btn__text">下载 MD 模板</span>
              </a-button>
            </a-tooltip>
          </a-space>
        </div>
        <div v-if="uiState.uploadMode === 'file'" class="upload-file-wrap">
          <a-upload-dragger
            name="instance-file"
            :multiple="false"
            :show-upload-list="false"
            accept=".csv,.md,.markdown,text/csv,text/markdown"
            :before-upload="handleBeforeUpload"
            @change="handleFileChange"
          >
            <p class="ant-upload-drag-icon">
              <CloudUploadOutlined />
            </p>
            <p class="ant-upload-text">将 CSV / Markdown 文件拖到此处，或 <span class="gi-link">点击上传</span></p>
            <p class="ant-upload-hint">
              支持格式：.csv / .md / .markdown；单次最多 {{ MAX_ROWS }} 行；支持 MySQL / PostgreSQL / SQLServer
            </p>
          </a-upload-dragger>
          <div v-if="uiState.fileName" class="selected-file-row">
            <FileOutlined />
            <span class="selected-file-name">{{ uiState.fileName }}</span>
            <a-button type="link" size="small" @click="handleClearFile">清除</a-button>
          </div>
        </div>
        <div v-else class="paste-text-wrap">
          <a-textarea
            v-model:value="uiState.pasteText"
            :rows="12"
            placeholder="在此粘贴 CSV 或 Markdown 表格内容（首行必须包含必填列：数据源名称 / 环境 / 数据库类型 / 连接地址 / 登录账号 / 密码）"
          />
          <div class="paste-tip">
            <a-tag color="cyan">提示</a-tag>
            <span>Markdown 必须是标准「 | col1 | col2 | 」+「 | --- | --- | 」格式；CSV 首行列名请与模板保持一致。</span>
          </div>
        </div>
        <div class="policy-row">
          <span class="policy-label">重复策略（hostname+port 命中已有实例时）：</span>
          <a-radio-group v-model:value="uiState.duplicatePolicy">
            <a-radio-button value="skip">跳过</a-radio-button>
            <a-radio-button value="update">覆盖更新</a-radio-button>
            <a-radio-button value="abort">终止整批</a-radio-button>
          </a-radio-group>
        </div>
      </template>
      <template v-else-if="uiState.step === 1">
        <div class="preview-summary-row">
          <div class="summary-chip summary-total">共 {{ preview.rows.length }} 行</div>
          <div class="summary-chip summary-valid">校验通过 {{ summary.validCount }}</div>
          <div class="summary-chip summary-invalid">校验失败 {{ summary.invalidCount }}</div>
          <div class="summary-chip summary-dup">重复/跳过 {{ summary.duplicateCount }}</div>
          <a-space class="preview-actions">
            <a-button @click="handleBackToUpload">返回上传</a-button>
            <a-button type="primary" :disabled="!summary.validCount" :loading="uiState.importing" @click="handleStartImport">
              确认导入 {{ summary.validCount }} 行
            </a-button>
          </a-space>
        </div>
        <a-table
          size="small"
          :columns="previewColumns"
          :data-source="preview.rows"
          :scroll="{ x: 1200, y: 380 }"
          :pagination="false"
          row-key="_line"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'password'">
              <span class="pwd-mask">******</span>
            </template>
            <template v-else-if="column.key === '_status'">
              <a-tag :color="statusTagColor(record._status)">
                {{ statusTagText(record._status) }}
              </a-tag>
            </template>
            <template v-else-if="column.key === '_reason'">
              <span v-if="record._errors?.length" class="reason-text">
                {{ record._errors.join('；') }}
              </span>
              <span v-else class="reason-text dim">—</span>
            </template>
          </template>
        </a-table>
      </template>
      <template v-else-if="uiState.step === 2">
        <a-progress
          :percent="uiState.importing ? 10 : 100"
          :status="uiState.importing ? 'active' : resultData ? 'success' : 'normal'"
        />
        <div class="progress-tip" v-if="uiState.importing">正在提交后端批量导入，请稍候…</div>
      </template>
      <template v-else-if="uiState.step === 3">
        <div v-if="resultData" class="result-summary-card">
          <StatGrid>
            <StatCard title="总行数" :value="resultData.total" tone="neutral" />
            <StatCard title="成功" :value="resultData.success" tone="success" />
            <StatCard title="跳过" :value="resultData.skipped" tone="warning" />
            <StatCard title="失败" :value="resultData.failed" tone="danger" />
          </StatGrid>
          <a-table
            size="small"
            :columns="resultColumns"
            :data-source="resultData.rows || []"
            :scroll="{ x: 1020, y: 320 }"
            :pagination="false"
            row-key="(r) => `r-${r.line}-${r.key}`"
            class="result-table"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'status'">
                <a-tag :color="resultTagColor(record.status)">
                  {{ resultTagText(record.status) }}
                </a-tag>
              </template>
              <template v-else-if="column.key === 'instance'">
                <span v-if="record.instance_id" class="instance-id-text">
                  {{ record.instance_id }}
                </span>
                <span v-else class="dim">—</span>
              </template>
            </template>
          </a-table>
        </div>
      </template>
    </div>
    <template #footer>
      <div class="import-footer">
        <a-button @click="handleCancel">{{ uiState.step === 3 ? '关闭' : '取消' }}</a-button>
        <template v-if="uiState.step === 0">
          <a-button type="primary" :disabled="!canGoPreview" @click="handleGoPreview">下一步：预览校验</a-button>
        </template>
        <template v-if="uiState.step === 3">
          <a-button
            v-if="(resultData?.failed || 0) > 0"
            @click="handleDownloadFailedCSV"
          >
            下载失败清单
          </a-button>
          <a-button v-if="(resultData?.success || 0) > 0" type="primary" @click="handleCloseDone">
            完成并刷新列表
          </a-button>
        </template>
      </div>
    </template>
  </a-modal>
</template>

<script setup>
import { batchImportInstancesApi } from '@/api/admin'
import StatCard from '@/components/patterns/StatCard.vue'
import StatGrid from '@/components/patterns/StatGrid.vue'
import {
  buildDuplicateIndex,
  buildFailedRowsCSV,
  detectFormat,
  downloadTemplate,
  generateBatchID,
  MAX_ROWS,
  normalizeRow,
  parseByFormat,
  triggerDownload,
} from '@/utils/instanceBatchImport'
import {
  CloudUploadOutlined,
  FileOutlined,
  FileTextOutlined,
} from '@ant-design/icons-vue'
import { message, Upload } from 'ant-design-vue'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'

const props = defineProps({
  open: { type: Boolean, default: false },
  environments: { type: Array, default: () => [] },
  existingRows: { type: Array, default: () => [] },
})

const emit = defineEmits(['update:open', 'done'])

const viewport = reactive({ w: window.innerWidth, h: window.innerHeight })
const handleViewportResize = () => {
  viewport.w = window.innerWidth
  viewport.h = window.innerHeight
}
onMounted(() => {
  handleViewportResize()
  window.addEventListener('resize', handleViewportResize)
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', handleViewportResize)
})

const modalWidth = computed(() => {
  const minW = 760
  const maxW = 1000
  const percent = Math.floor(viewport.w * 0.82)
  return Math.max(minW, Math.min(maxW, percent))
})
const bodyMaxHeight = computed(() => {
  const minH = 420
  const base = Math.floor(viewport.h - 240)
  return Math.max(minH, base) + 'px'
})

const uiState = reactive({
  step: 0,
  uploadMode: 'file',
  fileName: '',
  fileContent: '',
  pasteText: '',
  duplicatePolicy: 'skip',
  importing: false,
})

const preview = reactive({
  rows: [],
  format: 'csv',
  fileName: '',
})

const resultData = ref(null)

const previewColumns = [
  { title: '行号', dataIndex: '_line', key: '_line', width: 60, align: 'left' },
  { title: '状态', dataIndex: '_status', key: '_status', width: 90, align: 'left' },
  { title: '数据源名称', dataIndex: 'remark', key: 'remark', width: 160, align: 'left' },
  { title: '环境', dataIndex: 'environment', key: 'environment', width: 90, align: 'left' },
  { title: '数据库类型', dataIndex: 'db_type', key: 'db_type', width: 100, align: 'left' },
  { title: '连接地址', dataIndex: 'connection_address', key: 'connection_address', width: 200, align: 'left' },
  { title: '登录账号', dataIndex: 'user', key: 'user', width: 120, align: 'left' },
  { title: '密码', dataIndex: 'password', key: 'password', width: 90, align: 'left' },
  { title: '默认数据库', dataIndex: 'database', key: 'database', width: 110, align: 'left' },
  { title: '失败原因', dataIndex: '_reason', key: '_reason', ellipsis: true, align: 'left' },
]

const resultColumns = [
  { title: '行号', dataIndex: 'line', key: 'line', width: 60, align: 'left' },
  { title: '数据源名称', dataIndex: 'remark', key: 'remark', width: 160, align: 'left' },
  { title: '连接地址', dataIndex: 'key', key: 'key', width: 200, align: 'left' },
  { title: '实例ID', dataIndex: 'instance', key: 'instance', width: 140, align: 'left' },
  { title: '状态', dataIndex: 'status', key: 'status', width: 80, align: 'left' },
  { title: '原因', dataIndex: 'reason', key: 'reason', ellipsis: true, align: 'left' },
]

const summary = computed(() => {
  const rows = preview.rows || []
  let validCount = 0
  let invalidCount = 0
  let duplicateCount = 0
  for (const r of rows) {
    if (r._status === 'valid') validCount++
    else if (r._status === 'dup') duplicateCount++
    else invalidCount++
  }
  return { validCount, invalidCount, duplicateCount }
})

const canGoPreview = computed(() => {
  if (uiState.uploadMode === 'file') return !!(uiState.fileName && uiState.fileContent)
  return !!String(uiState.pasteText || '').trim()
})

const duplicateSet = computed(() => {
  const list = []
  for (const r of props.existingRows || []) {
    list.push({ hostname: r.hostname || r.host, port: r.port })
  }
  return buildDuplicateIndex(list)
})

const resetAll = async () => {
  Object.assign(uiState, {
    step: 0,
    uploadMode: 'file',
    fileName: '',
    fileContent: '',
    pasteText: '',
    duplicatePolicy: 'skip',
    importing: false,
  })
  preview.rows = []
  preview.format = 'csv'
  preview.fileName = ''
  resultData.value = null
  await nextTick()
}

watch(
  () => props.open,
  async (v) => {
    if (v) {
      await resetAll()
    }
  },
)

const handleCancel = () => {
  if (uiState.importing) return
  emit('update:open', false)
}

const handleCloseDone = () => {
  emit('update:open', false)
  emit('done', { needRefresh: true })
}

const handleDownloadTemplate = (format) => {
  downloadTemplate(format)
}

const handleBeforeUpload = (file) => {
  if (!file) return false
  const name = String(file.name || '').toLowerCase()
  const okExt = /\.(csv|md|markdown)$/.test(name) || file.type === 'text/csv' || file.type === 'text/markdown'
  if (!okExt) {
    message.error('仅支持 .csv / .md / .markdown 格式文件')
    return Upload.LIST_IGNORE
  }
  if (file.size && file.size > 2 * 1024 * 1024) {
    message.error('文件大小不能超过 2MB')
    return Upload.LIST_IGNORE
  }
  return false
}

const handleFileChange = async (info) => {
  const f = info?.file
  if (!f || info?.fileList?.length === 0) return
  const raw = f.originFileObj || f
  if (!raw) {
    message.error('未读取到有效文件，请重试或切换到「粘贴文本」模式')
    return
  }
  const name = String(raw.name || f.name || '')
  const size = raw.size || f.size || 0
  const sizeKB = size ? Math.ceil(size / 1024) : 0
  if (sizeKB > 2048) {
    message.error('文件大小不能超过 2MB')
    return
  }
  let text = ''
  try {
    if (typeof raw.text === 'function') {
      text = await raw.text()
    } else {
      text = await new Promise((resolve, reject) => {
        const reader = new FileReader()
        reader.onload = () => resolve(String(reader.result || ''))
        reader.onerror = () => reject(reader.error || new Error('FileReader error'))
        reader.readAsText(raw, 'utf-8')
      })
    }
  } catch (e) {
    message.error('读取文件失败：' + (e?.message || String(e)))
    return
  }
  if (!String(text || '').trim()) {
    message.error('文件内容为空，请检查文件后重试')
    return
  }
  uiState.fileName = name
  uiState.fileContent = text
  message.success(`已读取 ${name}，共 ${sizeKB} KB，可点击下一步进入预览`)
}

const handleClearFile = () => {
  uiState.fileName = ''
  uiState.fileContent = ''
}

const prepareContent = () => {
  if (uiState.uploadMode === 'file') {
    const fileName = uiState.fileName
    const content = uiState.fileContent
    const format = detectFormat({ fileName, content })
    return { format, fileName, content }
  }
  const content = uiState.pasteText
  const format = detectFormat({ content })
  return { format, fileName: 'paste.' + (format === 'markdown' ? 'md' : 'csv'), content }
}

const handleGoPreview = () => {
  try {
    const { format, fileName, content } = prepareContent()
    const parsed = parseByFormat({ format, fileName, content })
    if (parsed.length > MAX_ROWS) {
      message.error(`单次最多允许 ${MAX_ROWS} 行，当前 ${parsed.length} 行`)
      return
    }
    const normalizedRows = []
    const dupsInBatch = new Map()
    for (let i = 0; i < parsed.length; i++) {
      const line = i + 2
      const raw = parsed[i] || {}
      const { ok, errors, normalized } = normalizeRow(raw, props.environments || [])
      const connectionAddress =
        raw['连接地址'] || raw.connection_address || (normalized.hostname && normalized.port ? `${normalized.hostname}:${normalized.port}` : '')
      const envText = raw['环境'] || raw.environment || normalized.environment_name || (normalized.environment != null ? normalized.environment : '')
      const row = {
        _line: line,
        _status: ok ? 'valid' : 'invalid',
        _errors: errors || [],
        remark: normalized.remark || raw['数据源名称'] || raw.remark || '',
        environment: envText,
        db_type: normalized.db_type || raw['数据库类型'] || raw.db_type || '',
        connection_address: connectionAddress,
        user: normalized.user || raw['登录账号'] || raw.user || '',
        password: normalized.password || raw['密码'] || raw.password || '',
        database: normalized.database || raw['默认数据库'] || raw.database || '',
      }
      if (normalized.hostname && normalized.port) {
        const key = `${String(normalized.hostname).toLowerCase()}:${normalized.port}`
        if (duplicateSet.value.has(key)) {
          row._status = 'dup'
          if (!row._errors.includes('hostname+port 已存在，将按所选重复策略处理')) {
            row._errors.unshift('hostname+port 已存在，将按所选重复策略处理')
          }
        } else if (dupsInBatch.has(key)) {
          row._status = 'dup'
          row._errors.unshift(`与本次第 ${dupsInBatch.get(key)} 行重复`)
        } else {
          dupsInBatch.set(key, line)
        }
      }
      normalizedRows.push(row)
    }
    preview.format = format
    preview.fileName = fileName
    preview.rows = normalizedRows
    uiState.step = 1
  } catch (e) {
    message.error(e?.message || String(e))
  }
}

const handleBackToUpload = () => {
  uiState.step = 0
}

const handleStartImport = async () => {
  const validCount = summary.value.validCount + summary.value.duplicateCount
  if (!validCount) {
    message.warning('没有可导入的行，请先修正校验失败项')
    return
  }
  const { format, fileName, content } = prepareContent()
  const batchID = generateBatchID()
  try {
    uiState.importing = true
    uiState.step = 2
    await nextTick()
    const res = await batchImportInstancesApi({
      format,
      file_name: fileName,
      content,
      duplicate_policy: uiState.duplicatePolicy,
      rows: [],
      batch_id: batchID,
    })
    const data = res?.data || null
    if (!res || !data) {
      throw new Error('后端返回为空')
    }
    resultData.value = data
    uiState.step = 3
    if (data.success > 0) {
      message.success(`导入完成：成功 ${data.success} / 跳过 ${data.skipped} / 失败 ${data.failed}`)
    } else if (data.failed > 0) {
      message.warning(`导入完成：全部 ${data.failed} 行失败，可下载失败清单查看原因`)
    }
  } catch (e) {
    uiState.step = 1
    message.error('导入失败：' + (e?.message || String(e)))
  } finally {
    uiState.importing = false
  }
}

const handleDownloadFailedCSV = () => {
  if (!resultData.value) return
  const rows = (resultData.value.rows || []).filter((r) => r.status === 'failed')
  if (!rows.length) {
    message.warning('没有失败行可下载')
    return
  }
  const content = buildFailedRowsCSV(rows)
  triggerDownload(
    `instance-batch-import-failed-${resultData.value.batch_id || Date.now()}.csv`,
    content,
    'text/csv;charset=utf-8',
  )
}

const statusTagText = (s) => {
  if (s === 'valid') return '校验通过'
  if (s === 'dup') return '重复'
  return '校验失败'
}
const statusTagColor = (s) => {
  if (s === 'valid') return 'green'
  if (s === 'dup') return 'gold'
  return 'red'
}
const resultTagText = (s) => {
  if (s === 'success') return '成功'
  if (s === 'skipped') return '跳过'
  return '失败'
}
const resultTagColor = (s) => {
  if (s === 'success') return 'green'
  if (s === 'skipped') return 'gold'
  return 'red'
}
</script>

<style scoped lang="less">
.import-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
}
.import-template-actions {
  display: inline-flex;
  gap: 8px;
}
.import-steps {
  margin: 6px 2px 12px;
}
.template-alert {
  margin-bottom: 8px;
}
.step0-header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: nowrap;
  overflow: hidden;
  gap: 8px;
  margin-bottom: 8px;
  max-width: 100%;
  min-width: 0;
}
.import-mode-tabs {
  flex: 0 0 auto;
  min-width: 0;
  margin-bottom: 0;
  :deep(.ant-radio-button-wrapper) {
    padding-inline: 12px;
  }
}
.step0-template-actions {
  display: inline-flex;
  flex: 0 0 auto;
  gap: 6px;
  min-width: 0;
  align-items: center;
}
.step0-template-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  white-space: nowrap;
}
.step0-template-btn__icon {
  flex: 0 0 auto;
  font-size: 14px;
}
.step0-template-btn__text {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.import-body {
  min-height: 280px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.upload-file-wrap :deep(.ant-upload) {
  min-height: 180px;
  padding: 20px 12px;
}
.upload-file-wrap :deep(.ant-upload-drag) {
  min-height: 180px;
  padding: 20px 12px;
}
.upload-file-wrap :deep(.ant-upload-btn) {
  padding: 14px 0;
}
.upload-file-wrap :deep(.ant-upload-drag-icon) {
  margin-bottom: 6px;
  color: #0f766e;
  font-size: 44px;
}
.upload-file-wrap :deep(.ant-upload-text) {
  font-size: 14px;
  margin-bottom: 4px;
}
.upload-file-wrap :deep(.ant-upload-hint) {
  font-size: 12px;
  color: #8aa0ab;
  margin-bottom: 0;
}
.upload-file-wrap,
.paste-text-wrap {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.selected-file-row {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border-radius: 6px;
  background: #f5fbf9;
  color: #0f766e;
  border: 1px solid rgba(15, 118, 110, 0.18);
  align-self: flex-start;
}
.selected-file-name {
  font-size: 12px;
  font-weight: 500;
  max-width: 420px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.paste-tip {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: #5b7078;
  font-size: 12px;
}
.policy-row {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin-top: 2px;
  flex-wrap: nowrap;
  max-width: 100%;
  min-width: 0;
}
.policy-label {
  font-size: 12px;
  color: #5b7078;
  flex: 0 0 auto;
  white-space: nowrap;
}
.policy-row :deep(.ant-radio-button-wrapper) {
  padding-inline: 10px;
}
@media (max-width: 920px) {
  .step0-template-btn__text {
    font-size: 12px;
  }
  .import-body {
    min-height: 260px;
  }
}
@media (max-width: 820px) {
  .step0-template-btn__text {
    display: none;
  }
  .step0-template-btn {
    gap: 0;
    padding-inline: 8px;
  }
  .step0-template-actions {
    gap: 4px;
  }
  .import-mode-tabs :deep(.ant-radio-button-wrapper) {
    padding-inline: 10px;
    font-size: 12px;
  }
  .policy-row {
    gap: 6px;
  }
  .policy-label {
    font-size: 11px;
  }
  .policy-row :deep(.ant-radio-button-wrapper) {
    padding-inline: 8px;
    font-size: 12px;
  }
  .upload-file-wrap :deep(.ant-upload) {
    min-height: 160px;
    padding: 16px 10px;
  }
  .upload-file-wrap :deep(.ant-upload-drag) {
    min-height: 160px;
    padding: 16px 10px;
  }
  .upload-file-wrap :deep(.ant-upload-drag-icon) {
    font-size: 38px;
    margin-bottom: 4px;
  }
  .upload-file-wrap :deep(.ant-upload-text) {
    font-size: 13px;
  }
  .upload-file-wrap :deep(.ant-upload-hint) {
    font-size: 11px;
  }
  .import-body {
    min-height: 240px;
  }
}
.preview-summary-row {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.summary-chip {
  display: inline-flex;
  align-items: center;
  padding: 3px 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.2px;
}
.summary-total {
  background: #eef2f7;
  color: #233240;
}
.summary-valid {
  background: #ecfdf5;
  color: #047857;
}
.summary-invalid {
  background: #fef2f2;
  color: #b91c1c;
}
.summary-dup {
  background: #fffbeb;
  color: #b45309;
}
.preview-actions {
  margin-left: auto;
}
.pwd-mask {
  font-family: monospace;
  color: #7a8993;
  letter-spacing: 1px;
}
.reason-text {
  color: #485a63;
  font-size: 12px;
}
.reason-text.dim {
  color: #a5b3bc;
}
.progress-tip {
  margin-top: 8px;
  color: #5b7078;
  font-size: 12px;
}
.result-summary-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.result-table {
  margin-top: 4px;
}
.instance-id-text {
  font-family: SFMono-Regular, Menlo, Consolas, monospace;
  color: #0f766e;
  font-size: 12px;
  letter-spacing: 0.2px;
}
.dim {
  color: #a5b3bc;
}
.gi-link {
  color: #0f766e;
}
.import-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
