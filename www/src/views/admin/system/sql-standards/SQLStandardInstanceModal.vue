<template>
  <a-modal
    :open="modalOpen"
    :title="`管理实例 - ${standard?.name || ''}`"
    width="900px"
    :footer="null"
    @cancel="handleCancel"
  >
    <a-row :gutter="16" class="instance-modal-body">
      <!-- 已关联实例 -->
      <a-col :span="14">
        <div class="section-title">已关联实例</div>
        <a-table
          size="small"
          :columns="boundColumns"
          :row-key="(record) => record.id"
          :data-source="boundData"
          :pagination="boundPagination"
          :loading="boundLoading"
          @change="handleBoundTableChange"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'action'">
              <a-popconfirm title="确认移除该实例的规范绑定？" @confirm="handleUnbind(record)">
                <a-button type="link" danger size="small">移除</a-button>
              </a-popconfirm>
            </template>
          </template>
        </a-table>
      </a-col>

      <!-- 可关联实例 -->
      <a-col :span="10">
        <div class="section-title">可关联实例</div>
        <a-input-search
          v-model:value="searchValue"
          placeholder="搜索实例..."
          size="small"
          @search="fetchAvailable"
        />
        <a-table
          size="small"
          :columns="availableColumns"
          :row-key="(record) => record.instance_id"
          :data-source="availableData"
          :pagination="availablePagination"
          :loading="availableLoading"
          @change="handleAvailableTableChange"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'action'">
              <a-button type="link" size="small" @click="handleBind(record)">绑定</a-button>
            </template>
          </template>
        </a-table>
      </a-col>
    </a-row>
  </a-modal>
</template>

<script setup>
import {
  bindSQLStandardInstanceApi,
  getSQLStandardAvailableInstancesApi,
  getSQLStandardInstancesApi,
  unbindSQLStandardInstanceApi,
} from '@/api/admin'
import { message } from 'ant-design-vue'
import { computed, reactive, ref, watch } from 'vue'

const props = defineProps({
  open: { type: Boolean, default: false },
  standard: { type: Object, default: () => null },
})

const emit = defineEmits(['update:open', 'change'])

const modalOpen = computed({
  get: () => props.open,
  set: (val) => emit('update:open', val),
})

const boundColumns = [
  { title: '实例ID', dataIndex: 'instance_id', key: 'instance_id', ellipsis: true },
  { title: '地址', dataIndex: 'hostname', key: 'hostname' },
  { title: '端口', dataIndex: 'port', key: 'port' },
  { title: '类型', dataIndex: 'db_type', key: 'db_type' },
  { title: '操作', dataIndex: 'action', key: 'action' },
]

const availableColumns = [
  { title: '地址', dataIndex: 'hostname', key: 'hostname' },
  { title: '类型', dataIndex: 'db_type', key: 'db_type' },
  { title: '操作', dataIndex: 'action', key: 'action', width: 80 },
]

const boundLoading = ref(false)
const boundData = ref([])
const boundPagination = reactive({ current: 1, pageSize: 10, total: 0 })

const availableLoading = ref(false)
const availableData = ref([])
const availablePagination = reactive({ current: 1, pageSize: 10, total: 0 })
const searchValue = ref('')

watch(
  () => props.open,
  (val) => {
    if (val && props.standard?.id) {
      fetchBound()
      fetchAvailable()
    }
  },
)

const handleCancel = () => {
  emit('update:open', false)
}

const fetchBound = async () => {
  boundLoading.value = true
  const res = await getSQLStandardInstancesApi({
    standard_id: props.standard.id,
    page: boundPagination.current,
    page_size: boundPagination.pageSize,
    is_page: true,
  }).catch(() => {})
  if (res) {
    boundData.value = res.data
    boundPagination.total = res.total
  }
  boundLoading.value = false
}

const fetchAvailable = async () => {
  availableLoading.value = true
  const res = await getSQLStandardAvailableInstancesApi({
    standard_id: props.standard.id,
    page: availablePagination.current,
    page_size: availablePagination.pageSize,
    is_page: true,
    search: searchValue.value,
  }).catch(() => {})
  if (res) {
    availableData.value = res.data
    availablePagination.total = res.total
  }
  availableLoading.value = false
}

const handleBoundTableChange = (pager) => {
  boundPagination.current = pager.current
  boundPagination.pageSize = pager.pageSize
  fetchBound()
}

const handleAvailableTableChange = (pager) => {
  availablePagination.current = pager.current
  availablePagination.pageSize = pager.pageSize
  fetchAvailable()
}

const handleBind = async (record) => {
  const res = await bindSQLStandardInstanceApi({
    standard_id: props.standard.id,
    instance_id: record.instance_id,
  }).catch(() => {})
  if (res) {
    message.success('绑定成功')
    fetchBound()
    fetchAvailable()
    emit('change')
  }
}

const handleUnbind = async (record) => {
  const res = await unbindSQLStandardInstanceApi(record.id).catch(() => {})
  if (res) {
    message.success('移除成功')
    fetchBound()
    fetchAvailable()
    emit('change')
  }
}
</script>

<style scoped>
.instance-modal-body {
  max-height: 520px;
  overflow-y: auto;
}

.section-title {
  font-weight: 600;
  margin-bottom: 8px;
}
</style>
