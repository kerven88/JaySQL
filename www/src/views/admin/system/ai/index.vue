<template>
  <div class="ai-capability-page gi-page-shell">
    <!-- 页面标题在内容卡片之外 -->
    <div class="gi-page-toolbar gi-toolbar-single-line">
      <div class="gi-page-title">AI 能力</div>
    </div>

    <a-card :bordered="false" class="page-card">
      <a-tabs v-model:activeKey="activeTab" class="page-tabs">
        <!-- 工具栏与 tab 标题同一行：随 activeKey 切换对应控件 -->
        <template #rightExtra>
          <div class="tab-toolbar">
            <a-space v-if="activeTab === 'providers'">
              <a-button type="primary" @click="openProviderWizard">
                <template #icon><PlusOutlined /></template>
                接入服务商
              </a-button>
              <a-input-search
                v-model:value="providerKeyword"
                placeholder="搜索名称 / 厂商 / 端点"
                style="width: 240px"
                allow-clear
              />
              <a-button :loading="providerListRef?.loading" @click="refreshProviders">
                <template #icon><ReloadOutlined /></template>
                刷新
              </a-button>
            </a-space>

            <a-space v-else>
              <a-select
                v-model:value="modelProviderId"
                placeholder="全部服务商"
                style="width: 220px"
                allow-clear
                :options="modelProviderOptions"
                @change="refreshModels"
              />
              <a-input-search
                v-model:value="modelKeyword"
                placeholder="搜索模型名 / 别名"
                style="width: 220px"
                allow-clear
                @search="refreshModels"
              />
              <a-button :loading="modelListRef?.loading" @click="refreshModels">
                <template #icon><ReloadOutlined /></template>
                刷新
              </a-button>
            </a-space>
          </div>
        </template>

        <a-tab-pane key="providers">
          <template #tab>服务商接入</template>
          <ProviderList
            ref="providerListRef"
            :templates="templates"
            :keyword="providerKeyword"
            @changed="handleProvidersChanged"
          />
        </a-tab-pane>
        <a-tab-pane key="models" force-render>
          <template #tab>模型管理</template>
          <ModelList
            ref="modelListRef"
            :provider-options="modelProviderOptions"
            :provider-id="modelProviderId"
            :keyword="modelKeyword"
          />
        </a-tab-pane>
      </a-tabs>
    </a-card>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { message } from 'ant-design-vue'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons-vue'
import { aiVendorListApi } from '@/api/ai'
import ModelList from './ModelList.vue'
import ProviderList from './ProviderList.vue'

const activeTab = ref('providers')
const templates = ref([])
const providers = ref([])
const providerListRef = ref()
const modelListRef = ref()

// 工具栏与 tab 同行后，筛选状态上提到本页统一持有
const providerKeyword = ref('')
const modelProviderId = ref(undefined)
const modelKeyword = ref('')

// 服务商下拉选项：工具栏与模型编辑弹窗共用同一份数据源
const modelProviderOptions = computed(() =>
  (providers.value || []).map((item) => ({
    value: item.provider_id,
    label: `${item.display_name}（${item.vendor_label || item.vendor}）`,
  })),
)

// 模板清单仍然是「服务商接入」向导的数据源（厂商卡片墙 + 动态表单），必须保留
const loadTemplates = async () => {
  try {
    const res = await aiVendorListApi()
    templates.value = res?.data || []
  } catch (err) {
    message.error(err?.message || '加载厂商模板失败')
  }
}

const handleProvidersChanged = (list) => {
  providers.value = list || []
}

const openProviderWizard = () => providerListRef.value?.openWizard?.()
const refreshProviders = () => providerListRef.value?.loadProviders?.()
// 等 props 落到子组件后再查询，避免拿到切换前的筛选值
const refreshModels = () => nextTick(() => modelListRef.value?.loadModels?.())

// 进入模型页时刷新一次，保证列表是最新的
watch(activeTab, (value) => {
  if (value === 'models') {
    modelListRef.value?.loadModels?.()
  }
})

onMounted(loadTemplates)
</script>

<style scoped>
.ai-capability-page {
  padding: 0;
  /* 标题行与内容卡片的间距减半（默认 12px），页面更紧凑 */
  gap: calc(var(--gi-spacing-ssm) / 2);
}
.page-card {
  background: #fff;
}
/* 标题行与 tab 行的间距减半：36px → 18px
   组成 = gap 12px→6px + 卡片上内边距 24px→12px（后者才是视觉上的大头） */
.page-card :deep(.ant-card-body) {
  padding-top: var(--gi-spacing-ssm);
}
.page-tabs {
  margin-top: 0;
}
/* 工具栏与 tab 标题同行，垂直居中 */
.tab-toolbar {
  display: flex;
  align-items: center;
  padding-right: 4px;
}
/* tab 标题字号在本页原有基础上 +1 号；仅作用于本页 tab，不影响其他区域 */
.page-tabs :deep(.ant-tabs-tab),
.page-tabs :deep(.ant-tabs-tab-btn) {
  font-size: 16px;
}
/* tab 栏与下方表格之间的留白加大，提升呼吸感 */
.page-tabs :deep(.ant-tabs-content) {
  padding-top: 16px;
}

@media (max-width: 768px) {
  /* 工具条与 tab 标题同排，窄屏必然重叠且没有 overflow 兜底 */
  .page-tabs :deep(.ant-tabs-nav) {
    flex-wrap: wrap;
    row-gap: 8px;
  }

  .page-tabs :deep(.ant-tabs-extra-content) {
    width: 100%;
    margin-inline-start: 0 !important;
  }

  .tab-toolbar {
    width: 100%;
    padding-right: 0;
    overflow-x: auto;
  }

  .tab-toolbar :deep(.ant-input-search),
  .tab-toolbar :deep(.ant-select) {
    width: 100% !important;
    min-width: 0;
  }
}
</style>
