<template>
  <div class="gi-page-shell config-page">
    <!-- 刷新按钮已移到 Tab 栏右端（与 Tab 同一行），页头只留标题 -->
    <PageHeader title="告警配置" />

    <a-card class="section-card" :bordered="false">
      <a-tabs :active-key="activeTab" class="main-tabs" @change="onTabChange">
        <!-- 各 Tab 的新建/筛选操作统一挂在 Tab 栏右侧：
             原先放在内容区顶部单独占一行，把表格往下挤，且视觉上与 Tab 割裂；
             按当前 Tab 动态切换，避免把「新建渠道」误点到「新建模板」上。
             通知渠道/通知策略的按钮通过子组件 ref 触发，动作仍由子组件自己实现。
             插槽用 rightExtra：tabBarExtraContent 在 antd-vue 4 已废弃（会打 devWarning）。 -->
        <template #rightExtra>
          <div class="tabs-extra">
            <template v-if="activeTab === 'rules'">
              <a-button type="primary" @click="openAlertTemplateModal()">
                <PlusOutlined />
                新建模板
              </a-button>
              <a-select
                v-model:value="templateFilter.dbType"
                allow-clear
                class="filter-select"
                placeholder="全部数据库类型"
                :options="alertDbTypeFilterOptions"
                @change="reloadAlertTemplates"
              />
            </template>
            <template v-else-if="activeTab === 'channels'">
              <a-button type="primary" @click="channelRef?.openCreate()">
                <PlusOutlined />
                新建渠道
              </a-button>
              <a-select
                v-model:value="channelKindFilter"
                allow-clear
                class="filter-select"
                placeholder="全部类型"
                :options="channelRef?.kindOptions || []"
              />
            </template>
            <template v-else-if="activeTab === 'policies'">
              <a-button type="primary" @click="policyRef?.openCreate()">
                <PlusOutlined />
                新建策略
              </a-button>
            </template>
            <!-- 刷新是页面级操作，固定挂在最右端：不论当前是哪个 Tab 都在同一位置，
                 不会随 Tab 切换而左右跳动，也不会被前面的新建按钮挤走 -->
            <a-button :loading="loadingRules" @click="reloadAll">
              <SyncOutlined />
              刷新
            </a-button>
          </div>
        </template>
        <a-tab-pane key="rules" tab="告警模板">
          <a-table
            :columns="alertTemplateColumns"
            :data-source="filteredAlertTemplates"
            row-key="id"
            :loading="loadingTemplates"
            :pagination="{ pageSize: 10 }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'name'">
                <div>{{ record.display_name || record.name }}</div>
              </template>
              <template v-if="column.key === 'db_type'">
                <span>{{ record.db_type_label || record.db_type }}</span>
              </template>
              <template v-if="column.key === 'rule_count'">
                {{ record.rule_count }} 条
              </template>
              <template v-if="column.key === 'effective_instances'">
                <a-tooltip
                  v-if="record.is_default && record.effective_instances > record.bound_instances"
                  :title="`显式挂载 ${record.bound_instances} 个，另有 ${record.effective_instances - record.bound_instances} 个未挂载实例由默认模板兜底`"
                >
                  <span>{{ record.effective_instances }} 个实例</span>
                </a-tooltip>
                <span v-else>{{ record.effective_instances }} 个实例</span>
              </template>
              <template v-if="column.key === 'enabled'">
                <span :class="record.enabled ? 'status-on' : 'status-off'">{{ record.enabled ? '启用' : '停用' }}</span>
              </template>
              <template v-if="column.key === 'actions'">
                <a-space>
                  <a-button class="action-link" type="link" size="small" @click="openDetailModal(record)">详情</a-button>
                  <a-button class="action-link" type="link" size="small" @click="openRulesModal(record)">规则配置</a-button>
                  <a-button class="action-link" type="link" size="small" @click="openAlertBindModal(record)">绑定实例</a-button>
                  <a-popconfirm
                    v-if="!record.builtin && !record.is_default"
                    :title="`删除后其规则一并删除，且不可恢复。确认删除「${record.display_name || record.name}」？`"
                    @confirm="removeAlertTemplate(record.id)"
                  >
                    <a-button type="link" danger size="small">删除</a-button>
                  </a-popconfirm>
                </a-space>
              </template>
            </template>
          </a-table>
        </a-tab-pane>
        <a-tab-pane key="channels" tab="通知渠道">
          <NotifyChannelsView ref="channelRef" v-model:kind-filter="channelKindFilter" />
        </a-tab-pane>
        <a-tab-pane key="policies" tab="通知策略">
          <NotifyPoliciesView ref="policyRef" />
        </a-tab-pane>
        <a-tab-pane key="grade" tab="健康策略">
          <!-- 原「健康分级」（24 项检查项投影）已下线：实例健康/风险/故障统一由
               「告警模版 → 规则配置」的未恢复告警推导，本面板只配判定阈值与扣分。 -->
          <health-state-panel ref="gradePanelRef" />
        </a-tab-pane>
      </a-tabs>
    </a-card>

    <!-- 新建/编辑告警模板：基础信息一行排布（与采集策略模板弹窗同款布局）；
         模板标识由前端按 dbType-自定义-时间戳 自动生成，用户无感 -->
    <a-modal
      v-model:open="alertTplModal.open"
      :title="alertTplModal.editingId ? '编辑告警模板' : '新建告警模板'"
      width="720px"
      :confirm-loading="savingTemplate"
      @ok="submitAlertTemplate"
    >
      <a-form layout="vertical">
        <a-row :gutter="16">
          <a-col :span="8">
            <a-form-item label="模板名称">
              <a-input v-model:value="alertTplModal.form.display_name" placeholder="留空则使用自动标识" />
            </a-form-item>
          </a-col>
          <a-col :span="6">
            <a-form-item label="数据库类型">
              <a-select
                v-model:value="alertTplModal.form.db_type"
                :options="alertDbTypeFormOptions"
                :disabled="Boolean(alertTplModal.editingId)"
              />
            </a-form-item>
          </a-col>
          <a-col :span="6">
            <a-form-item label="模板说明">
              <a-input v-model:value="alertTplModal.form.description" placeholder="这个模板适合什么样的实例" />
            </a-form-item>
          </a-col>
          <a-col :span="4">
            <a-form-item label="启用状态">
              <a-switch v-model:checked="alertTplModal.form.enabled" />
            </a-form-item>
          </a-col>
          <a-col v-if="!alertTplModal.editingId" :span="24">
            <a-form-item label="复制已有模板">
              <a-select
                v-model:value="alertTplModal.form.copy_from"
                allow-clear
                :options="alertCopySourceOptions"
                placeholder="复制后沿用其规则集，可继续调整"
              />
            </a-form-item>
          </a-col>
        </a-row>
      </a-form>
    </a-modal>

    <!-- 绑定实例：与采集策略绑定弹窗同款交互（勾选即挂载，取消勾选已挂载的走「回落到默认模板」） -->
    <a-modal
      v-model:open="alertBindModal.open"
      width="860px"
      :confirm-loading="savingBind"
      @ok="submitAlertBind('bind')"
    >
      <template #title>
        <span class="modal-title-wrap"><span class="modal-title-dot" />绑定实例到模板：{{ alertBindModal.template?.display_name || alertBindModal.template?.name || '—' }}</span>
      </template>
      <template #footer>
        <a-space>
          <a-button @click="alertBindModal.open = false">取消</a-button>
          <a-button
            v-if="!alertBindModal.template?.is_default"
            :loading="savingBind"
            @click="submitAlertBind('unbind')"
          >回落到默认模板</a-button>
          <a-button type="primary" :loading="savingBind" @click="submitAlertBind('bind')">绑定到该模板</a-button>
        </a-space>
      </template>
      <a-input-search
        v-model:value="alertBindModal.search"
        placeholder="搜索数据源名称 / IP"
        style="width: 240px; margin-bottom: 12px"
        allow-clear
      />
      <a-table
        :columns="alertBindColumns"
        :data-source="alertBindCandidates"
        row-key="instance_id"
        size="small"
        :pagination="{ pageSize: 8 }"
        :row-selection="{ selectedRowKeys: alertBindModal.selected, onChange: onAlertBindSelectionChange }"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'datasource'">
            <div class="bind-ds-cell">
              <div class="bind-ds-name">{{ record.name || '—' }}</div>
              <div class="bind-ds-target">{{ record.target || '—' }}</div>
            </div>
          </template>
          <template v-if="column.key === 'current'">
            <span>{{ record.alert_template_label || '默认告警策略' }}</span>
          </template>
        </template>
      </a-table>
    </a-modal>

    <!-- 规则配置：按监控项分组，每个指标可配 1~4 档 P0–P3（阶梯告警）。
         三 Tab 与监控明细页同口径（主机 / 数据库 / 引擎），避免两页分类不一致。 -->
    <a-drawer
      v-model:open="ruleDrawer.open"
      :width="1080"
      :body-style="{ padding: '0', display: 'flex', flexDirection: 'column', overflow: 'hidden' }"
      destroy-on-close
      class="rule-drawer"
    >
      <template #title>
        <span class="modal-title-dot" />
        <span>配置告警规则：{{ ruleDrawer.template?.display_name || ruleDrawer.template?.name || '—' }}</span>
      </template>
      <template #extra>
        <a-space :size="8">
          <a-input
            v-model:value="ruleDrawer.keyword"
            placeholder="搜索监控项 / 指标 key"
            allow-clear
            size="small"
            class="rule-search"
          />
          <a-popconfirm
            title="用平台推荐值覆盖当前 Tab 下已配置的档位？"
            ok-text="恢复"
            cancel-text="取消"
            @confirm="applyRecommendations"
          >
            <a-button size="small">恢复推荐值</a-button>
          </a-popconfirm>
        </a-space>
      </template>

      <div class="rule-drawer-alerts">
        <a-alert
          v-if="ruleDrawer.errors.length"
          type="error"
          show-icon
          :message="`${ruleDrawer.errors.length} 处硬校验不通过，请修正后再保存`"
        >
          <template #description>
            <div v-for="(item, index) in ruleDrawer.errors" :key="index" class="rule-error-line">
              <a-button type="link" size="small" @click="locateGroup(item)">{{ item.metric }}</a-button>
              <span>{{ item.message }}</span>
            </div>
          </template>
        </a-alert>
        <a-alert
          v-else-if="ruleDrawer.warnings.length"
          type="warning"
          show-icon
          :message="`${ruleDrawer.warnings.length} 条建议（不阻断保存）`"
        >
          <template #description>
            <div v-for="(item, index) in ruleDrawer.warnings" :key="index">{{ item }}</div>
          </template>
        </a-alert>
      </div>

      <a-tabs v-model:activeKey="ruleDrawer.activeLayer" class="rule-tabs">
        <a-tab-pane v-for="tab in ruleLayerTabs" :key="tab.key" :tab="`${tab.label}（${tab.count}）`">
          <a-spin :spinning="ruleDrawer.loading">
            <a-empty v-if="!visibleGroups.length" description="没有匹配的监控项" :image="false" />
            <div v-for="group in visibleGroups" :key="group.metric_key" class="rule-group" :class="{ 'is-open': group.expanded, 'is-off': !group.available }">
              <div class="rule-group-head" @click="toggleRuleGroup(group)">
                <span class="group-arrow">▶</span>
                <span class="group-name">{{ group.display_name }}</span>
                <span class="group-badges">
                  <a-tag v-if="!group.available" color="default" class="group-tag">无采集源</a-tag>
                  <a-tag v-for="lv in group.levels" :key="lv.level" :color="levelTagColor(lv.level)" class="group-tag">
                    {{ lv.level }}
                  </a-tag>
                  <span v-if="!group.levels.length && group.available" class="group-none">未配置</span>
                  <span class="group-adv" @click.stop="openGroupPolicy(group)">高级</span>
                </span>
              </div>
              <div v-if="group.expanded" class="rule-group-body">
                <div v-if="!group.available" class="group-hint">
                  {{ group.description || '该指标当前没有采集源，档位配置会保留，但不会参与告警评估。' }}
                </div>
                <div v-for="lv in group.levels" :key="lv.level" class="lv-row">
                  <a-tag :color="levelTagColor(lv.level)" class="lv-tag">{{ lv.level }}</a-tag>
                  <span class="lv-op">{{ operatorText(group.operator) }}</span>
                  <a-input-number v-model:value="lv.threshold" :min="0" :disabled="!group.available" size="small" class="lv-threshold" />
                  <span class="lv-unit">{{ group.unit || '—' }}</span>
                  <span class="lv-field">
                    持续
                    <a-input-number v-model:value="lv.for_seconds" :min="0" :max="86400" :disabled="!group.available" size="small" class="lv-num" />
                    秒
                  </span>
                  <!-- 原「静默」输入框已移除：规则级 silence_minutes 是惰性字段，
                       评估链（monitor_alerts.go）与派发链（notifySilenceHit 只看 policy.SilenceMinutes）
                       都不读它，编辑已有规则时后端也不回写，配了等于没配。
                       重复通知频率统一由「通知策略 → 重复通知间隔(分钟)」控制，避免两处并存误导。 -->
                  <a-switch v-model:checked="lv.enabled" :disabled="!group.available" size="small" checked-children="启用" un-checked-children="停用" />
                  <a-button v-if="group.available" type="link" danger size="small" @click.stop="removeRuleLevel(group, lv)">删除</a-button>
                </div>
                <div class="lv-actions">
                  <a-dropdown v-if="group.available && unusedLevels(group).length" :trigger="['click']">
                    <a-button type="dashed" size="small">
                      <PlusOutlined />
                      添加等级
                    </a-button>
                    <template #overlay>
                      <a-menu @click="(info) => addRuleLevel(group, info.key)">
                        <a-menu-item v-for="level in unusedLevels(group)" :key="level">
                          {{ level }} · {{ alertLevelText(level) }}
                        </a-menu-item>
                      </a-menu>
                    </template>
                  </a-dropdown>
                  <span v-else-if="group.available" class="lv-hint">该指标已配置全部四个等级</span>
                  <span v-if="group.recommended_levels?.length" class="lv-hint">
                    推荐：{{ group.recommended_levels.map((item) => `${item.level} ${operatorText(group.operator)} ${item.threshold}`).join('，') }}
                    <a-button type="link" size="small" @click.stop="applyGroupRecommendation(group)">应用</a-button>
                  </span>
                </div>
              </div>
            </div>
          </a-spin>
        </a-tab-pane>
      </a-tabs>

      <div class="rule-drawer-foot">
        <span class="foot-tip">
          保存为覆盖式：同「指标 + 等级」覆盖原规则（规则 ID 不变）；本页删掉的档位，保存后一并移除。
        </span>
        <a-space>
          <a-button @click="ruleDrawer.open = false">取消</a-button>
          <a-button type="primary" :loading="savingRules" :disabled="ruleDrawer.errors.length > 0" @click="submitTemplateRules">
            确定
          </a-button>
        </a-space>
      </div>
    </a-drawer>

    <!-- 模板详情（只读）：与规则配置抽屉同一个详情接口、同源同口径，
         只做呈现不做编辑——摘要卡 + 三层已配置档位，未配置的折进尾注。 -->
    <a-drawer
      v-model:open="detailDrawer.open"
      :width="720"
      destroy-on-close
      class="detail-drawer"
    >
      <template #title>
        <span class="modal-title-dot" />
        <span>模板详情：{{ detailDrawer.template?.display_name || detailDrawer.template?.name || '—' }}</span>
      </template>
      <a-spin :spinning="detailDrawer.loading">
        <div class="detail-card">
          <div class="detail-card-title">告警规则</div>
          <a-empty v-if="!detailDrawer.loading && !detailConfigured.length" description="该模板尚未配置任何告警档位" :image="false" />
          <!-- 按监控大项分类 tab：主机 / 数据库 / 引擎（空分类自动隐藏，默认落第一个有内容的 tab） -->
          <a-tabs
            v-else
            v-model:activeKey="detailDrawer.activeLayer"
            class="detail-layer-tabs"
          >
            <a-tab-pane v-for="tab in detailLayerTabs" :key="tab.key" :tab="`${tab.label} · ${tab.groups.length} 项`">
              <div v-for="group in tab.groups" :key="group.metric_key" class="detail-group">
              <div class="detail-group-head">
                <span class="detail-group-name">{{ group.display_name }}</span>
                <a-tag v-if="!group.available" color="default" class="detail-tag-off">无采集源</a-tag>
              </div>
              <div class="detail-levels">
                <div
                  v-for="lv in group.levels"
                  :key="lv.level"
                  class="detail-level"
                  :class="{ 'is-off': lv.enabled === false }"
                >
                  <span
                    class="detail-level-dot"
                    :style="{ background: lv.enabled === false ? '#c1ccd4' : (ALERT_LEVEL_HEX[lv.level] || '#8fa0ac') }"
                  />
                  <span
                    class="detail-level-name"
                    :style="{ color: lv.enabled === false ? '#8fa0ac' : (ALERT_LEVEL_HEX[lv.level] || '#33454f') }"
                  >{{ lv.level }} {{ alertLevelText(lv.level) }}</span>
                  <span class="detail-level-cond">
                    {{ operatorText(group.operator) }}
                    <b>{{ detailThreshold(lv.threshold) }}</b>{{ group.unit || '' }}
                    <template v-if="lv.for_seconds"> · 持续 {{ detailDuration(lv.for_seconds) }}</template>
                  </span>
                  <a-tag v-if="lv.enabled === false" color="default" class="detail-tag-off">已停用</a-tag>
                </div>
              </div>
              </div>
            </a-tab-pane>
          </a-tabs>
        </div>

        <div v-if="detailConfigured.length" class="detail-foot">
          以上共配置 <b>{{ detailConfigured.length }}</b> 个监控项、<b>{{ detailLevelCount }}</b> 档告警<template v-if="detailUnconfigured">；其余 {{ detailUnconfigured }} 个监控项未配置档位，不参与告警</template>。
        </div>
      </a-spin>
    </a-drawer>

    <!-- 指标级高级设置：恢复判定与无数据行为作用于该指标的所有档位 -->
    <a-modal
      v-model:open="rulePolicyModal.open"
      :title="`高级设置：${rulePolicyModal.group?.display_name || ''}`"
      width="520px"
      @ok="submitGroupPolicy"
    >
      <a-form layout="vertical">
        <a-form-item label="恢复判定（连续正常多少秒才判定恢复）">
          <a-input-number v-model:value="rulePolicyModal.form.recovery_seconds" :min="0" :max="86400" style="width: 100%" />
        </a-form-item>
        <a-form-item label="无数据行为">
          <a-select v-model:value="rulePolicyModal.form.no_data_behavior" :options="noDataOptions" />
        </a-form-item>
        <a-form-item label="指标说明（展示在告警详情里）">
          <a-textarea v-model:value="rulePolicyModal.form.description" :rows="3" :maxlength="255" show-count />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup>
import {
  bindMonitorAlertInstancesApi,
  createMonitorAlertTemplateApi,
  deleteMonitorAlertTemplateApi,
  getMonitorAlertTemplateDetailApi,
  getMonitorAlertTemplatesApi,
  getMonitorRulesApi,
  getMonitorTargetConfigsApi,
  saveMonitorAlertTemplateRulesApi,
  unbindMonitorAlertInstancesApi,
  updateMonitorAlertTemplateApi,
} from '@/api/monitor'
import { message, Modal } from 'ant-design-vue'
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { PlusOutlined, SyncOutlined } from '@ant-design/icons-vue'
import { operatorText } from '../utils'
import HealthStatePanel from './HealthStatePanel.vue'
import PageHeader from '../components/PageHeader.vue'
import NotifyChannelsView from './NotifyChannelsView.vue'
import NotifyPoliciesView from './NotifyPoliciesView.vue'

// ============================ 告警等级（P0–P3） ============================
// 与后端 monitor_alert_levels.go 同一套语义：P0 最高。前端只做展示与预校验，
// 真正的裁决在后端 ValidateMetricAlertLevels（硬校验拒绝保存、软校验仅提示）。
const ALERT_LEVELS = ['P0', 'P1', 'P2', 'P3']
const ALERT_LEVEL_TEXT = { P0: '紧急', P1: '严重', P2: '一般', P3: '提示' }
// antd 内置色名，只能喂 a-tag :color；页面里当背景色必须用十六进制（见 LEVEL_HEX）
const ALERT_LEVEL_TAG = { P0: 'red', P1: 'orange', P2: 'gold', P3: 'blue' }
const ALERT_LEVEL_HEX = { P0: '#dc2626', P1: '#ea580c', P2: '#d97706', P3: '#2563eb' }
const alertLevelText = (level) => ALERT_LEVEL_TEXT[level] || level || '—'
const levelTagColor = (level) => ALERT_LEVEL_TAG[level] || 'default'
const alertLevelRank = (level) => {
  const index = ALERT_LEVELS.indexOf(level)
  return index === -1 ? 99 : index
}

// 规则抽屉最多允许的档位数（与后端 form 校验 max=4 对齐）
const MAX_LEVELS_PER_METRIC = 4

defineOptions({ name: 'MonitorAlertConfigView' })

const activeTab = ref('rules')
// 健康判定面板的引用：刷新按钮与「切走 Tab 时提示未保存」都要用到面板内部状态
const gradePanelRef = ref(null)
// 通知渠道 / 通知策略面板的引用：新建按钮挂到 Tab 栏右侧后，由父组件代为触发。
// 未激活的 Tab 面板尚未挂载，调用处一律用可选链兜底。
const channelRef = ref(null)
const policyRef = ref(null)
// 通知渠道的「类型」筛选：下拉在 Tab 栏、过滤逻辑在子组件，用 v-model 双向同步
const channelKindFilter = ref(undefined)

// Tab 切换改为手动接管（不再 v-model）：健康判定是就地编辑，改完没保存就切走
// 等于静默丢改动，必须先问一句。其余 Tab 直接放行。
const onTabChange = (key) => {
  if (activeTab.value === 'grade' && key !== 'grade' && gradePanelRef.value?.isDirty()) {
    Modal.confirm({
      title: '健康判定有未保存的修改，确定离开？',
      okText: '放弃修改并切换',
      cancelText: '留在本页',
      onOk: () => {
        gradePanelRef.value?.clearDirty()
        activeTab.value = key
      },
    })
    return
  }
  activeTab.value = key
}
const loadingRules = ref(false)
const loadingTemplates = ref(false)
const savingTemplate = ref(false)
const savingBind = ref(false)
const savingRules = ref(false)
// 模板列表过滤（「数据库类型」下拉的选项来自模板接口下发的 db_types）
const templateFilter = reactive({
  dbType: undefined,
})

// 前端放行 MySQL / PostgreSQL / Oracle / SQLServer / TiDB：五类引擎的默认模板
// 均已播种开箱规则（推荐档位，见 seedDefaultAlertRulesForEmptyTemplates），
// 规则配置/复制模板/绑定实例等交互与 MySQL 完全同源。
// Doris/ClickHouse 暂不展示（后台照常播种与生效）；支持新引擎时在这里补 key 即可。
const VISIBLE_ALERT_DB_TYPES = new Set(['mysql', 'postgresql', 'oracle', 'sqlserver', 'tidb'])
const isVisibleDbType = (value) => VISIBLE_ALERT_DB_TYPES.has((value || '').toLowerCase())

const ruleState = reactive({
  filters: { environments: [], db_types: [], engines: [] },
  rules: [],
})

const alertTemplateState = reactive({
  templates: [],
  dbTypes: [],
})

// 绑定实例弹窗的候选数据源：与采集策略绑定弹窗同款，从目标配置列表取
const targetState = reactive({
  targets: [],
  loaded: false,
})

// 告警模板弹窗三件套：基础信息 / 绑定实例 / 规则配置
const alertTplModal = reactive({
  open: false,
  editingId: undefined,
  form: createTemplateForm(),
})
// 规则配置抽屉：数据源是模板详情接口的 metrics 分组（按监控项一行、组内 1~4 档）
const ruleDrawer = reactive({
  open: false,
  template: null,
  loading: false,
  activeLayer: 'host',
  keyword: '',
  metrics: [],
  errors: [],
  warnings: [],
})
// 指标级高级设置（恢复判定 / 无数据行为 / 说明）
const rulePolicyModal = reactive({
  open: false,
  group: null,
  form: { recovery_seconds: 300, no_data_behavior: 'ignore', description: '' },
})
const noDataOptions = [
  { label: '忽略（推荐：没采到 ≠ 异常）', value: 'ignore' },
  { label: '告警（缺数据本身要报警）', value: 'alert' },
  { label: '恢复（缺数据视为已恢复）', value: 'recover' },
]
const alertBindModal = reactive({
  open: false,
  template: null,
  search: '',
  selected: [],
  // 打开弹窗时已挂载在本模板上的实例（区分「新勾选」与「取消勾选」）
  originalBound: [],
})
// 引擎清单来自规则接口下发的 filters.engines（后端能力注册表），页面不硬编码类型
const engineList = computed(() => ruleState.filters?.engines || [])
// 表单/过滤用的数据库类型选项：value 用展示名（如 "MySQL"），后端 resolveEngine 会归一化
const alertDbTypeFormOptions = computed(() => engineList.value
  .filter((item) => item.implemented && isVisibleDbType(item.key || item.label))
  .map((item) => ({ label: item.label, value: item.label })))
const alertDbTypeFilterOptions = computed(() => {
  if (alertTemplateState.dbTypes.length) {
    return alertTemplateState.dbTypes
      .filter((item) => isVisibleDbType(item.value || item.label))
      .map((item) => ({ label: item.label, value: item.value }))
  }
  return alertDbTypeFormOptions.value
})

/** 指标下拉按字典分类分组，直接由指标字典驱动。 */
const alertTemplateColumns = [
  { title: '模板名称', key: 'name', width: 220 },
  { title: '数据库类型', key: 'db_type', width: 120 },
  { title: '规则数', key: 'rule_count', width: 90 },
  { title: '生效实例', key: 'effective_instances', width: 120 },
  { title: '状态', dataIndex: 'enabled', key: 'enabled', width: 90 },
  { title: '说明', dataIndex: 'description', key: 'description', ellipsis: true },
  { title: '操作', key: 'actions', width: 264 },
]
const alertBindColumns = [
  { title: '数据源', key: 'datasource', ellipsis: true },
  { title: '类型', dataIndex: 'db_type', key: 'db_type', width: 110 },
  { title: '环境', dataIndex: 'environment_name', key: 'environment_name', width: 110 },
  { title: '当前生效模板', key: 'current' },
]
function createTemplateForm(record = {}) {
  return {
    display_name: record?.display_name || '',
    db_type: record?.db_type || 'MySQL',
    description: record?.description || '',
    copy_from: undefined,
    enabled: record?.enabled ?? true,
  }
}
// 模板标识自动生成（后端必填唯一键，前端不暴露输入项）：
// <库类型>-custom-yyyymmdd-hhmmss，与采集策略模板自动标识同款口径
const autoAlertTemplateName = (dbType) => {
  const engine = (dbType || 'mysql').toLowerCase()
  const pad = (n) => String(n).padStart(2, '0')
  const d = new Date()
  const stamp = `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`
  return `${engine}-custom-${stamp}`
}
const filteredAlertTemplates = computed(() => {
  let list = (alertTemplateState.templates || []).filter((item) => isVisibleDbType(item.db_type))
  if (!templateFilter.dbType) return list
  return list.filter((item) => item.db_type === templateFilter.dbType)
})
const reloadAlertTemplates = async () => {
  loadingTemplates.value = true
  try {
    const res = await getMonitorAlertTemplatesApi()
    alertTemplateState.templates = res?.data?.templates || []
    alertTemplateState.dbTypes = res?.data?.db_types || []
  } catch {
    message.error('告警模板加载失败')
  } finally {
    loadingTemplates.value = false
  }
}
const reloadRules = async () => {
  loadingRules.value = true
  try {
    const res = await getMonitorRulesApi()
    ruleState.rules = res?.data?.rules || []
    // 规则接口同时下发 filters.engines（引擎能力注册表），供新建模板的类型下拉使用；
    // 之前漏了这一步，下拉一直显示「暂无数据」
    if (res?.data?.filters) ruleState.filters = res.data.filters
  } catch {
    message.error('告警规则加载失败')
  } finally {
    loadingRules.value = false
  }
}
// ---------------------------------------------------------------------------
// 告警模板：基础信息 / 绑定实例 / 规则配置
// ---------------------------------------------------------------------------

const alertCopySourceOptions = computed(() => (alertTemplateState.templates || [])
  .filter((item) => item.db_type === alertTplModal.form.db_type.toLowerCase())
  .map((item) => ({ label: item.display_name || item.name, value: item.name })))

const openAlertTemplateModal = (record) => {
  alertTplModal.editingId = record?.id
  alertTplModal.form = createTemplateForm(record)
  alertTplModal.open = true
}

const submitAlertTemplate = async () => {
  savingTemplate.value = true
  try {
    const form = alertTplModal.form
    if (alertTplModal.editingId) {
      await updateMonitorAlertTemplateApi(alertTplModal.editingId, {
        display_name: form.display_name,
        description: form.description,
        enabled: form.enabled,
      })
      message.success('告警模板已更新')
    } else {
      const payload = {
        name: autoAlertTemplateName(form.db_type),
        display_name: form.display_name,
        db_type: form.db_type,
        description: form.description,
        enabled: form.enabled,
        copy_from: form.copy_from || '',
      }
      await createMonitorAlertTemplateApi(payload)
      message.success('告警模板已创建，可在「规则配置」里继续调整阈值')
    }
    alertTplModal.open = false
    await Promise.all([reloadAlertTemplates(), reloadRules()])
  } catch (error) {
    message.error(error?.message || '告警模板保存失败')
  } finally {
    savingTemplate.value = false
  }
}

const removeAlertTemplate = async (id) => {
  try {
    await deleteMonitorAlertTemplateApi(id)
    message.success('告警模板已删除')
    await Promise.all([reloadAlertTemplates(), reloadRules()])
  } catch (error) {
    message.error(error?.message || '删除失败')
  }
}

// ---------------------------------------------------------------------------
// 规则配置抽屉：按监控项分组、每个指标 1~4 档（P0–P3 阶梯）
// ---------------------------------------------------------------------------

// 三 Tab 与监控明细页同口径：主机（res.*）/ 数据库（engine）/ 引擎（kernel，MySQL 系为 InnoDB）
const RULE_LAYER_TABS = [
  { key: 'host', label: '主机' },
  { key: 'engine', label: '数据库' },
  { key: 'kernel', label: '引擎' },
]
const ruleLayerTabs = computed(() => RULE_LAYER_TABS.map((tab) => ({
  ...tab,
  count: ruleDrawer.metrics.filter((item) => item.layer === tab.key).length,
})))
const visibleGroups = computed(() => {
  const keyword = (ruleDrawer.keyword || '').trim().toLowerCase()
  return ruleDrawer.metrics.filter((item) => {
    if (item.layer !== ruleDrawer.activeLayer) return false
    if (!keyword) return true
    return `${item.display_name} ${item.metric_key} ${item.detail_item || ''}`.toLowerCase().includes(keyword)
  })
})

const unusedLevels = (group) => {
  const used = new Set((group.levels || []).map((item) => item.level))
  return ALERT_LEVELS.filter((level) => !used.has(level) && used.size < MAX_LEVELS_PER_METRIC)
}

// 前端预校验：与后端 ValidateMetricAlertLevels 同规则（硬校验阻断保存）
const validateGroup = (group) => {
  const errors = []
  const levels = [...(group.levels || [])].sort((left, right) => alertLevelRank(left.level) - alertLevelRank(right.level))
  if (!levels.length) return errors
  const seen = new Set()
  levels.forEach((item) => {
    if (seen.has(item.level)) {
      errors.push(`${group.display_name} 的 ${item.level} 重复配置`)
    }
    seen.add(item.level)
  })
  const operator = group.operator || '>='
  if (operator === '!=' || operator === '=') {
    if (levels.length > 1) {
      errors.push(`${group.display_name} 使用 ${operatorText(operator)} 判定，只能配置一个档位`)
    }
    return errors
  }
  const greater = operator.startsWith('>')
  for (let index = 0; index + 1 < levels.length; index += 1) {
    const current = levels[index]
    const next = levels[index + 1]
    const broken = greater
      ? Number(current.threshold) <= Number(next.threshold)
      : Number(current.threshold) >= Number(next.threshold)
    if (broken) {
      errors.push(`${group.display_name} 阈值倒挂：${current.level}(${current.threshold}) 必须${greater ? '高于' : '低于'} ${next.level}(${next.threshold})`)
    }
  }
  return errors
}

const recomputeRuleValidation = () => {
  const errors = []
  ruleDrawer.metrics.forEach((group) => {
    validateGroup(group).forEach((item) => errors.push({ metric: group.display_name, metric_key: group.metric_key, layer: group.layer, message: item }))
  })
  ruleDrawer.errors = errors
}

const resetRuleDrawer = () => {
  ruleDrawer.metrics = []
  ruleDrawer.errors = []
  ruleDrawer.warnings = []
  ruleDrawer.keyword = ''
  ruleDrawer.activeLayer = 'host'
}

// 打开抽屉：数据源是模板详情接口的 metrics 分组（后端已按明细口径组装好）
const openRulesModal = async (record) => {
  ruleDrawer.template = record
  resetRuleDrawer()
  ruleDrawer.open = true
  ruleDrawer.loading = true
  try {
    const res = await getMonitorAlertTemplateDetailApi(record.id)
    const metrics = res?.data?.metrics || []
    ruleDrawer.metrics = metrics.map((item) => ({
      ...item,
      operator: item.operator || '>=',
      levels: (item.levels || [])
        .map((lv) => ({
          // 注意别写成 level: lv —— 参数名一旦叫 level 就会遮蔽字段名，
          // 把整个档位对象塞进 level 字段：档位标签渲染成 JSON、
          // unusedLevels 的 Set 里装的是对象（下拉不再排除已用等级）、
          // 提交时把对象发给后端直接被拒。这里统一用 lv，避免再踩。
          level: lv.level,
          threshold: lv.threshold ?? 0,
          for_seconds: lv.for_seconds ?? 0,
          enabled: lv.enabled !== false,
        }))
        .sort((left, right) => alertLevelRank(left.level) - alertLevelRank(right.level)),
      // 默认展开已配置的监控项：未配置的保持折叠，避免一屏几十行淹没收紧过的注意力
      expanded: (item.levels || []).length > 0,
    }))
    if (!metrics.some((item) => item.layer === ruleDrawer.activeLayer)) {
      const first = RULE_LAYER_TABS.find((tab) => metrics.some((item) => item.layer === tab.key))
      if (first) ruleDrawer.activeLayer = first.key
    }
    recomputeRuleValidation()
  } catch (error) {
    message.error(error?.message || '模板规则加载失败')
  } finally {
    ruleDrawer.loading = false
  }
}

// ---------------------------------------------------------------------------
// 模板详情抽屉（只读）：与规则配置抽屉走同一个详情接口，同源同口径。
// 差异只在呈现：详情页只讲"这个模板会怎么告警"——只列出配置了档位的监控项，
// 未配置的折叠成尾注；通知方式/静默策略从平铺 rules 聚合去重后展示。
// ---------------------------------------------------------------------------
const detailDrawer = reactive({
  open: false,
  template: null,
  loading: false,
  metrics: [],
  rules: [],
  activeLayer: undefined,
})

const openDetailModal = async (record) => {
  detailDrawer.template = record
  detailDrawer.metrics = []
  detailDrawer.rules = []
  detailDrawer.activeLayer = undefined
  detailDrawer.open = true
  detailDrawer.loading = true
  try {
    const res = await getMonitorAlertTemplateDetailApi(record.id)
    // 用详情响应里的 template 兜底覆盖列表行：列表对内置模板刻意清空了 updated_at
    // （种子数据无意义），详情口径保留真实更新时间，详情页直接展示。
    detailDrawer.template = { ...record, ...(res?.data?.template || {}) }
    detailDrawer.metrics = (res?.data?.metrics || []).map((item) => ({
      ...item,
      operator: item.operator || '>=',
      levels: [...(item.levels || [])].sort((left, right) => alertLevelRank(left.level) - alertLevelRank(right.level)),
    }))
    detailDrawer.rules = res?.data?.rules || []
    // 默认落在第一个有配置的监控大项上
    detailDrawer.activeLayer = detailLayerTabs.value[0]?.key
  } catch (error) {
    message.error(error?.message || '模板详情加载失败')
  } finally {
    detailDrawer.loading = false
  }
}

const detailConfigured = computed(() => detailDrawer.metrics.filter((item) => (item.levels || []).length > 0))
// 详情页的监控大项 tab：与规则配置抽屉同一套 layer 语义（host/engine/kernel 对所有引擎通用），
// 空分类自动隐藏，模板属于哪种引擎都能落到这套分类上。
const detailLayerTabs = computed(() => RULE_LAYER_TABS
  .map((tab) => ({ ...tab, groups: detailConfigured.value.filter((item) => item.layer === tab.key) }))
  .filter((tab) => tab.groups.length))
const detailUnconfigured = computed(() => detailDrawer.metrics.length - detailConfigured.value.length)
const detailLevelCount = computed(() => detailConfigured.value.reduce((sum, item) => sum + item.levels.length, 0))

// 阈值展示：整数十进制原样，小数最多保留两位（85.00 → 85，85.50 → 85.5）
const detailThreshold = (value) => {
  const num = Number(value) || 0
  return Number.isInteger(num) ? String(num) : String(Math.round(num * 100) / 100)
}
const detailDuration = (seconds) => {
  const value = Number(seconds) || 0
  if (value <= 0) return '—'
  if (value < 60) return `${value} 秒`
  if (value % 3600 === 0) return `${value / 3600} 小时`
  if (value % 60 === 0) return `${value / 60} 分钟`
  return `${Math.floor(value / 60)} 分 ${value % 60} 秒`
}
// 原 detailMinutes（分钟格式化）随「触发后静默」展示一并移除，其唯一调用点已删除。
const toggleRuleGroup = (group) => {
  group.expanded = !group.expanded
}

const addRuleLevel = (group, level) => {
  const levels = group.levels || []
  const greater = (group.operator || '>=').startsWith('>')
  // 新档阈值给个合理起点：紧挨相邻档外推一点，避免一上来就倒挂
  const values = levels.map((item) => Number(item.threshold) || 0)
  let threshold = 0
  if (values.length) {
    threshold = greater ? Math.max(...values) + 10 : Math.max(1, Math.min(...values) - 10)
  }
  group.levels = [...levels, {
    level,
    threshold,
    for_seconds: level === 'P0' ? 0 : 60,
    enabled: true,
  }].sort((left, right) => alertLevelRank(left.level) - alertLevelRank(right.level))
  group.expanded = true
  recomputeRuleValidation()
}

const removeRuleLevel = (group, level) => {
  group.levels = (group.levels || []).filter((item) => item !== level)
  recomputeRuleValidation()
}

// 恢复推荐值：只作用于当前 Tab（避免"点一下把三个 Tab 的调整全冲掉"）
const applyRecommendations = () => {
  let changed = 0
  ruleDrawer.metrics.forEach((group) => {
    if (group.layer !== ruleDrawer.activeLayer) return
    if (!group.recommended_levels?.length) return
    group.levels = group.recommended_levels.map((item) => ({ ...item }))
    group.expanded = true
    changed += 1
  })
  recomputeRuleValidation()
  if (changed) {
    message.success(`已按平台推荐值重置 ${changed} 个监控项（${RULE_LAYER_TABS.find((tab) => tab.key === ruleDrawer.activeLayer)?.label}）`)
  } else {
    message.info('当前分组没有可用的推荐值（无采集源或平台未给出建议）')
  }
}

const applyGroupRecommendation = (group) => {
  group.levels = (group.recommended_levels || []).map((item) => ({ ...item }))
  recomputeRuleValidation()
  message.success(`${group.display_name} 已套用推荐档位`)
}

const openGroupPolicy = (group) => {
  rulePolicyModal.group = group
  rulePolicyModal.form = {
    recovery_seconds: group.recovery_seconds ?? 300,
    no_data_behavior: group.no_data_behavior || 'ignore',
    description: group.description || '',
  }
  rulePolicyModal.open = true
}

const submitGroupPolicy = () => {
  const group = rulePolicyModal.group
  if (group) {
    group.recovery_seconds = rulePolicyModal.form.recovery_seconds ?? 300
    group.no_data_behavior = rulePolicyModal.form.no_data_behavior || 'ignore'
    group.description = rulePolicyModal.form.description || ''
  }
  rulePolicyModal.open = false
}

// 点击错误摘要：跳到对应 Tab 并展开该监控项
const locateGroup = (error) => {
  const group = ruleDrawer.metrics.find((item) => item.metric_key === error.metric_key)
  if (!group) return
  ruleDrawer.activeLayer = group.layer
  group.expanded = true
  ruleDrawer.keyword = ''
}

const submitTemplateRules = async () => {
  recomputeRuleValidation()
  if (ruleDrawer.errors.length) {
    message.error(`存在 ${ruleDrawer.errors.length} 处阈值阶梯问题，请按错误提示修正`)
    return
  }
  // 覆盖式提交：提交当前模板可见的全部监控项（含未配置的）。
  // replace_all 显式声明"这份列表就是该模板的完整期望状态"，未提交的档位由后端删除；
  // 不传这个标记时后端只替换本次提交出现过的指标，部分提交不会误伤其他指标。
  const metrics = ruleDrawer.metrics.map((group) => ({
    metric_key: group.metric_key,
    operator: group.operator,
    recovery_seconds: group.recovery_seconds ?? 300,
    no_data_behavior: group.no_data_behavior || 'ignore',
    description: group.description || '',
    levels: (group.levels || []).map((item) => ({
      level: item.level,
      threshold: Number(item.threshold) || 0,
      for_seconds: Number(item.for_seconds) || 0,
      // 不再提交 silence_minutes：该值对存量规则不生效（后端 update 不写这一列），
      // 新建规则时后端会按等级兜底为 P0=5/P1=15/P2=60/P3=240，不会出现 0 值脏数据。
      enabled: item.enabled !== false,
    })),
  }))
  savingRules.value = true
  try {
    const res = await saveMonitorAlertTemplateRulesApi(ruleDrawer.template.id, { metrics, replace_all: true })
    ruleDrawer.warnings = res?.data?.warnings || []
    const levelCount = metrics.reduce((total, item) => total + item.levels.length, 0)
    message.success(`规则已保存：${metrics.filter((item) => item.levels.length).length} 个监控项、${levelCount} 个档位`)
    if (!ruleDrawer.warnings.length) {
      ruleDrawer.open = false
    }
    await Promise.all([reloadAlertTemplates(), reloadRules()])
  } catch (error) {
    message.error(error?.message || '规则保存失败')
  } finally {
    savingRules.value = false
  }
}

const alertBindCandidates = computed(() => {
  const template = alertBindModal.template
  if (!template) return []
  const keyword = (alertBindModal.search || '').trim().toLowerCase()
  return (targetState.targets || [])
    .filter((item) => (item.db_type || '').toLowerCase() === template.db_type)
    .filter((item) => {
      if (!keyword) return true
      return (item.name || '').toLowerCase().includes(keyword) || (item.target || '').toLowerCase().includes(keyword)
    })
})

const onAlertBindSelectionChange = (keys) => {
  alertBindModal.selected = keys
}

const openAlertBindModal = async (record) => {
  alertBindModal.template = record
  alertBindModal.search = ''
  alertBindModal.selected = []
  alertBindModal.originalBound = []
  alertBindModal.open = true
  if (!targetState.loaded) {
    try {
      const res = await getMonitorTargetConfigsApi()
      targetState.targets = res?.data?.targets || []
      targetState.loaded = true
    } catch {
      message.error('实例列表加载失败')
      return
    }
  }
  // 预勾选：已挂载在本模板的实例；默认模板兜底的实例（未挂载）也算在册
  const bound = (targetState.targets || [])
    .filter((item) => item.alert_template_id === record.id)
    .map((item) => item.instance_id)
  alertBindModal.originalBound = [...bound]
  if (record.is_default) {
    for (const item of targetState.targets || []) {
      if ((item.db_type || '').toLowerCase() === record.db_type && !item.alert_template_id) {
        bound.push(item.instance_id)
      }
    }
  }
  alertBindModal.selected = bound
}

const submitAlertBind = async (mode) => {
  savingBind.value = true
  try {
    if (mode === 'bind') {
      const res = await bindMonitorAlertInstancesApi(alertBindModal.template.id, {
        instance_ids: alertBindModal.selected,
      })
      const notes = res?.data?.notes || []
      message.success(`已绑定 ${res?.data?.bound ?? alertBindModal.selected.length} 个实例`)
      for (const note of notes) message.warning(note)
    } else {
      // 回落：原挂载在本模板、但这次被取消勾选的实例
      const ids = alertBindModal.originalBound.filter((id) => !alertBindModal.selected.includes(id))
      if (ids.length === 0) {
        message.info('没有需要回落的实例')
        alertBindModal.open = false
        return
      }
      const res = await unbindMonitorAlertInstancesApi({ instance_ids: ids })
      const notes = res?.data?.notes || []
      message.success(`已解绑 ${res?.data?.bound ?? ids.length} 个实例并回落默认模板`)
      for (const note of notes) message.warning(note)
    }
    alertBindModal.open = false
    await Promise.all([reloadAlertTemplates(), reloadRules()])
  } catch (error) {
    message.error(error?.message || '操作失败')
  } finally {
    savingBind.value = false
  }
}

// 刷新：健康判定由面板自行加载与保存，这里通过 ref 触发它的 reload，
// 本页不再维护 grade 状态（原「健康分级」只读预览动线已删除）。
const reloadAll = async () => {
  await Promise.all([reloadAlertTemplates(), reloadRules(), gradePanelRef.value?.reload()])
}

// 抽屉里改阈值/开关不走 add/remove 等结构变化，必须挂深度监听，
// 否则把 P1 从 95 改成 75（比 P2 还低）页面毫无反应，用户要等到点保存才知道倒挂。
// 校验是纯前端内存计算（69 组 × 最多 4 档），深监听的开销可以忽略。
watch(
  () => ruleDrawer.metrics,
  () => {
    if (ruleDrawer.open) recomputeRuleValidation()
  },
  { deep: true },
)

onMounted(() => {
  reloadAll()
})
</script>

<style scoped>
/* 滚动条只落在 Tab 内容区：页面壳占满视口高度且自身不滚动（压掉右侧整页滚动条），
   表单内容超出时由 .main-tabs 的 content-holder 内部滚动。
   高度口径与 layout-content-inner 一致：视口 - header - 上下 lg 内边距。 */
.config-page {
  height: calc(100vh - var(--gi-layout-header-height, 56px) - 2 * var(--gi-spacing-lg, 24px));
  overflow: hidden;
  /* 与监控告警下其它二级页面统一：头部与内容区间距同为 12px（原为 24px） */
  gap: var(--gi-spacing-ssm);
}

.config-page :deep(.section-card) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.config-page :deep(.section-card .ant-card-body) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  /* 选项卡贴紧卡片顶部，避免默认 24px 内边距造成的大留白 */
  padding-top: 8px;
}

/* 仅外层 Tab 的直接内容区滚动；健康分级 Tab 内的 grade-sub-tabs 二级 Tab 不受影响 */
.main-tabs {
  flex: 1;
  min-height: 0;
}

.main-tabs > :deep(.ant-tabs-content-holder) {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

/* 表头吸顶：内容区上下滚动时表头固定不动，只滚动表体行。
   .ant-table-content 因 scroll.x 自带 overflow:auto 会成为吸顶参照的 scrollport，
   必须放开为 visible，让吸顶相对于外层 content-holder 生效；
   横向滚动改由 content-holder 承担，表头与表体横向滚动保持同步。 */
.main-tabs > :deep(.ant-tabs-content-holder) .ant-table-content {
  overflow: visible !important;
}

.main-tabs > :deep(.ant-tabs-content-holder) .ant-table-thead > tr > th {
  position: sticky;
  top: 0;
  z-index: 2;
  background: #fafafa;
}

/* Tab 栏右侧的操作区：各 Tab 的新建/筛选 + 页面级刷新，同排靠右。
   slot 内容由本组件渲染（带 scoped 属性），所以 scoped 选择器能命中。

   宽屏：extra 与 Tab 同处一行，刷新按钮位于最右端，与 Tab 文字垂直居中。
   窄屏：extra 整块折到第二行并保持右对齐，Tab 不被挤进「更多」下拉；
         若第二行仍放不下，extra 内部再折行，不出现横向滚动或溢出。
   实现关键：
     - .ant-tabs-nav 允许 flex-wrap: wrap；
     - .ant-tabs-extra-content 占满第二行（flex: 1 1 auto），让内部按钮靠右；
     - .tabs-extra 宽度 100%，内部折行时保持 justify-content: flex-end。 */
.main-tabs :deep(.ant-tabs-nav) {
  flex-wrap: wrap;
}

.main-tabs :deep(.ant-tabs-extra-content) {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
}

.tabs-extra {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;
  width: 100%;
}

/* 按钮/下拉不允许被压扁：宽度不够就换行，而不是挤成一条 */
.tabs-extra > * {
  flex: none;
}

.tabs-extra :deep(.ant-btn) {
  display: inline-flex;
  align-items: center;
}

.filter-select {
  width: 160px;
}

.full-width {
  width: 100%;
}

/* 告警模板列表：状态列纯文字，不再使用 a-tag 色块 */
.status-on {
  color: var(--gi-color-text-primary, rgba(0, 0, 0, 0.85));
}
.status-off {
  color: var(--gi-color-text-tertiary, #8a94a6);
}

/* 操作列链接统一使用系统主题色，覆盖 antd link 默认蓝色 */
.action-link {
  color: var(--gi-color-primary, #0f766e);
}
.action-link:hover {
  color: var(--gi-color-primary-hover, #0d6b63);
}
.action-link:active {
  color: var(--gi-color-primary-active, #0b5f58);
}</style>

<style>
/* 弹窗内容 teleport 到 body，scoped 样式不生效 —— 弹窗内部样式必须放非 scoped 块 */

/* 模板绑定弹窗：标题圆点 + 数据源双行单元格（与采集策略绑定弹窗一致） */
.modal-title-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  margin-right: 8px;
  vertical-align: middle;
  border-radius: 50%;
  background: var(--gi-primary, #0f766e);
}

.bind-ds-name {
  line-height: 1.5;
}

.bind-ds-target {
  font-size: 12px;
  line-height: 1.5;
  color: #8fa0ac;
}

/* 规则配置抽屉：监控项分组 + 档位行。
   抽屉内容 teleport 到 body，scoped 样式不生效，因此这些规则必须放在非 scoped 块里。 */
.rule-drawer .ant-drawer-body {
  display: flex;
  flex-direction: column;
  padding: 0;
  overflow: hidden;
}

.rule-search {
  width: 220px;
}

.modal-title-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  margin-right: 8px;
  background: #0f766e;
  border-radius: 50%;
}

/* 校验提示区：默认（无错误/警告）不渲染任何内容，不占位；
   仅在确有提示时才加上内边距，避免抽屉顶部出现一段死空白。 */
.rule-drawer-alerts {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.rule-drawer-alerts:not(:empty) {
  padding: 12px 20px 0;
}

.rule-error-line {
  display: flex;
  align-items: center;
  gap: 4px;
  line-height: 1.6;
}

.rule-tabs {
  flex: 1;
  min-height: 0;
  padding: 12px 20px 0;
  overflow-y: auto;
}

.rule-tabs .ant-tabs-content-holder {
  overflow: visible;
}

.rule-group {
  margin-bottom: 8px;
  overflow: hidden;
  background: #fff;
  border: 1px solid rgba(22, 52, 64, 0.08);
  border-radius: 8px;
}

.rule-group.is-off {
  background: #fafbfc;
}

.rule-group-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  cursor: pointer;
}

.rule-group-head:hover {
  background: #f8fbfa;
}

.group-arrow {
  font-size: 10px;
  color: #8fa0ac;
  transition: transform 0.15s;
}

.rule-group.is-open .group-arrow {
  transform: rotate(90deg);
}

.group-name {
  font-size: 13px;
  font-weight: 600;
  color: #163440;
}

.group-badges {
  display: flex;
  align-items: center;
  gap: 5px;
  margin-left: auto;
}

.group-tag {
  margin-inline-end: 0;
  font-size: 10px;
  font-weight: 600;
  line-height: 18px;
}

.group-none {
  font-size: 11px;
  color: #b0bec6;
}

.group-adv {
  font-size: 11px;
  color: #0f766e;
}

.rule-group-body {
  padding: 4px 14px 10px 32px;
}

.group-hint {
  padding: 4px 0 8px;
  font-size: 11px;
  color: #8fa0ac;
}

.lv-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
  border-top: 1px dashed rgba(22, 52, 64, 0.06);
}

.lv-tag {
  width: 34px;
  margin-inline-end: 0;
  font-weight: 600;
  text-align: center;
}

.lv-op {
  padding: 3px 8px;
  font-family: Menlo, monospace;
  font-size: 12px;
  color: #3c5563;
  background: #f4f6f7;
  border: 1px solid rgba(22, 52, 64, 0.08);
  border-radius: 4px;
}

.lv-threshold {
  width: 120px;
}

.lv-unit {
  width: 46px;
  font-size: 11px;
  color: #8fa0ac;
}

.lv-field {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  color: #6b7f8c;
}

.lv-num {
  width: 84px;
}

.lv-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 8px;
}

.lv-hint {
  font-size: 11px;
  color: #b0bec6;
}

.rule-drawer-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 20px;
  background: #fff;
  border-top: 1px solid rgba(22, 52, 64, 0.08);
}

.foot-tip {
  font-size: 11px;
  color: #8fa0ac;
}

/* ---------------------------------------------------------------------------
 * 模板详情抽屉（只读）。抽屉内容 teleport 到 body，样式必须放非 scoped 块；
 * 视觉语言与规则配置抽屉保持一致（同圆角卡片 / 同灰阶 / 主色 #0f766e）。
 * ------------------------------------------------------------------------- */
.detail-drawer .ant-drawer-body {
  padding: 16px 20px 24px;
  overflow-y: auto;
}

.detail-card {
  padding: 14px 16px;
  margin-bottom: 12px;
  background: #fff;
  border: 1px solid rgba(22, 52, 64, 0.08);
  border-radius: 8px;
}

.detail-card-title {
  margin-bottom: 10px;
  font-size: 13px;
  font-weight: 600;
  color: #33454f;
}

.detail-layer-tabs :deep(.ant-tabs-tab) {
  padding: 6px 2px;
}

.detail-group {
  padding: 10px 12px;
  margin-bottom: 8px;
  background: #fafbfc;
  border: 1px solid rgba(22, 52, 64, 0.06);
  border-radius: 8px;
}

.detail-group-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 6px;
}

.detail-group-name {
  font-size: 13px;
  font-weight: 600;
  color: #33454f;
}

.detail-tag-off {
  font-size: 11px;
  color: #8fa0ac;
}

.detail-levels {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.detail-level {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 0;
  font-size: 12px;
  line-height: 1.6;
}

.detail-level.is-off .detail-level-cond {
  color: #b0bec6;
  text-decoration: line-through;
}

.detail-level-dot {
  flex: 0 0 8px;
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.detail-level-name {
  flex: 0 0 68px;
  font-weight: 600;
}

.detail-level-cond {
  color: #4a5c68;
}

.detail-level-cond b {
  font-size: 13px;
  color: #22313a;
}

.detail-foot {
  padding: 10px 2px 0;
  font-size: 12px;
  color: #8fa0ac;
}

.detail-foot b {
  color: #0f766e;
  font-weight: 600;
}

@media (max-width: 768px) {
  /* 页面壳固定高度，移动端视口矮时内容被裁 */
  .config-page {
    height: auto;
    min-height: calc(100vh - 56px - 48px);
    overflow: visible;
  }

  /* 该文件主动把 ant-table-content 设成 overflow:visible，抵消了横向滚动 */
  .main-tabs > :deep(.ant-tabs-content-holder) .ant-table-content {
    overflow-x: auto !important;
  }

  .main-tabs > :deep(.ant-tabs-content-holder) .ant-table-content > table {
    min-width: 720px;
  }

  :deep(.ant-space) {
    flex-wrap: wrap;
    row-gap: 4px;
  }

  .filter-select {
    width: 100%;
  }

  .tabs-extra {
    justify-content: flex-start;
  }
}
</style>
