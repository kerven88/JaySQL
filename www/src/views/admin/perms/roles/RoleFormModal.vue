<template>
  <a-modal
    :open="props.open"
    :width="modalWidth"
    :centered="true"
    @cancel="handleCancel"
  >
    <template #title>
      <div class="modal-title-row">
        <span class="modal-title-text">{{ props.title }}</span>
        <a-button
          v-if="props.readOnly && props.isBuiltin"
          type="primary"
          size="small"
          class="clone-btn"
          @click="onCloneBuiltin"
        >
          <template #icon><CopyOutlined /></template>
          基于此角色创建
        </a-button>
      </div>
    </template>
    <div class="role-modal-body">
      <a-form
        ref="formRef"
        :model="formData"
        :rules="!canEdit ? {} : rules"
        :label-col="{ flex: '84px' }"
        :wrapper-col="{ flex: '1' }"
        :disabled="!canEdit"
        class="role-modal-form"
      >
        <a-form-item label="角色名" name="name" has-feedback>
          <a-input
            v-model:value="formData.name"
            placeholder="请输入角色名"
            allow-clear
            :disabled="props.readOnly"
          />
        </a-form-item>
        <a-form-item label="角色编码" name="code" has-feedback>
          <a-input
            v-model:value="formData.code"
            placeholder="例如：system_admin"
            allow-clear
            :disabled="props.readOnly || !!formData.id"
          />
        </a-form-item>
        <a-form-item label="角色描述" name="description">
          <a-textarea
            v-model:value="formData.description"
            placeholder="请输入角色描述"
            :rows="2"
            :disabled="props.readOnly"
            :auto-size="{ minRows: 2, maxRows: 3 }"
          />
        </a-form-item>
        <a-form-item label="权限集合" name="permission_ids" class="perm-col-form-item">
          <div class="perm-select-wrapper">
            <a-select
              ref="permSelectRef"
              v-model:value="formData.permission_ids"
              mode="multiple"
              :options="props.permissions"
              :field-names="{ label: 'label', value: 'value' }"
              placeholder="请选择角色拥有的权限"
              option-filter-prop="label"
              show-search
              :disabled="props.readOnly"
              :list-height="256"
              style="width: 100%;"
            />
          </div>
        </a-form-item>
      </a-form>
    </div>

    <template #footer>
      <div class="modal-footer-actions">
        <div class="footer-left">
          <a-popconfirm
            v-if="canEdit && formData.id && !isReadonlyBuiltin(formData)"
            title="确定删除该角色吗？删除后所有绑定此角色的用户将失去这些权限。"
            ok-text="确定删除"
            ok-button-props="{ danger: true }"
            cancel-text="取消"
            @confirm="onDeleteClick"
          >
            <a-button danger :disabled="!canEdit">删除角色</a-button>
          </a-popconfirm>
        </div>
        <div class="footer-right">
          <a-button @click="handleCancel">{{ props.readOnly ? '关闭' : '取消' }}</a-button>
          <a-button v-if="canEdit" type="primary" :loading="uiState.loading" :disabled="!canEdit" @click="onSubmit">确定</a-button>
        </div>
      </div>
    </template>
  </a-modal>
</template>

<script setup>
import { computed, reactive, ref } from 'vue'
import { useWindowSize } from '@vueuse/core'
import { CopyOutlined } from '@ant-design/icons-vue'

// 『不可编辑/不可删除的内置角色白名单』——系统管理员/全局只读/超级管理员（System Principal硬编码入口），
// 其它内置角色（member 普通成员、viewer 只读成员）允许管理员直接改权限/改名称/描述，并且不允许删除（后端也拦）。
const READONLY_BUILTIN_ROLE_CODES = ['admin', 'global_readonly', 'super_admin']
const isReadonlyBuiltin = (formOrRole) => {
  const code = String(formOrRole?.code || '')
  if (!code) return false
  return READONLY_BUILTIN_ROLE_CODES.includes(code)
}

const props = defineProps({
  open: Boolean,
  title: String,
  canManage: {
    type: Boolean,
    default: true,
  },
  permissions: {
    type: Array,
    default: () => [],
  },
  readOnly: {
    type: Boolean,
    default: false,
  },
  isBuiltin: {
    type: Boolean,
    default: false,
  },
})

const canEdit = computed(() => props.canManage && !props.readOnly)

const emit = defineEmits(['update:open', 'submit', 'delete', 'clone-builtin'])

const formData = defineModel('modelValue', {
  type: Object,
  required: true,
})

const { width: winWidth } = useWindowSize()

const modalWidth = computed(() => {
  const w = winWidth.value || 1280
  if (w < 640) return Math.max(320, w - 32)
  if (w < 1280) return Math.round(w * 0.82)
  if (w < 1920) return 760
  return 880
})

const permSelectRef = ref()
const formRef = ref()

const uiState = reactive({
  loading: false,
})

const rules = computed(() => ({
  name: [
    {
      required: canEdit.value,
      message: '角色名不能为空',
      trigger: 'blur',
    },
    {
      min: 2,
      max: 32,
      message: '角色名长度应在2到32个字符之间',
      trigger: 'blur',
    },
    {
      pattern: /^[a-zA-Z0-9\u4e00-\u9fa5_]+$/,
      message: '角色名只能包含字母、数字、中文或下划线',
      trigger: 'blur',
    },
  ],
  code: [
    {
      required: canEdit.value,
      message: '角色编码不能为空',
      trigger: 'blur',
    },
    {
      min: 2,
      max: 64,
      message: '角色编码长度应在2到64个字符之间',
      trigger: 'blur',
    },
    {
      pattern: /^[a-zA-Z0-9_.-]+$/,
      message: '角色编码只能包含字母、数字、点、下划线或中划线',
      trigger: 'blur',
    },
  ],
}))

const handleCancel = () => {
  emit('update:open', false)
  formRef.value?.resetFields()
}

const onSubmit = async () => {
  try {
    await formRef.value.validateFields()
    uiState.loading = true
    emit('submit', formData.value)
  } catch {
    // ignore
  } finally {
    uiState.loading = false
  }
}

const onDeleteClick = () => {
  emit('delete', formData.value)
}

const onCloneBuiltin = () => {
  emit('clone-builtin', {
    name: formData.value?.name ? `${formData.value.name} (副本)` : '',
    code: formData.value?.code ? `${formData.value.code}_copy` : '',
    description: formData.value?.description
      ? `（基于【${formData.value.name}】内置角色克隆）${formData.value.description}`
      : '',
    permission_ids: [...(formData.value?.permission_ids || [])],
  })
}
</script>

<style scoped lang="less">
.role-modal-body {
  padding: 24px 28px 8px 28px;
  max-height: calc(100vh - 200px);
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
}

.modal-title-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-right: 56px;
  .modal-title-text {
    font-size: 16px;
    font-weight: 600;
    color: #262626;
    flex: 0 0 auto;
  }
  .clone-btn {
    margin-left: auto;
    flex-shrink: 0;
  }
}

.role-modal-form {
  display: flex;
  flex-direction: column;
  gap: 0;
  flex: 0 0 auto;
}

.perm-col-form-item {
  margin-bottom: 0;
  :deep(.ant-form-item-row) {
    align-items: flex-start;
  }
}

.perm-select-wrapper {
  max-height: 260px;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 4px 10px 22px 4px;
  box-sizing: border-box;
  border: 1px solid #d9d9d9;
  border-radius: 8px;
  background: #fafafa;
  transition: border-color 0.2s, box-shadow 0.2s;

  &:hover {
    border-color: #4096ff;
  }

  &:focus-within {
    border-color: #1677ff;
    box-shadow: 0 0 0 2px rgba(5, 145, 255, 0.1);
    background: #ffffff;
  }

  :deep(.ant-select) {
    width: 100% !important;
  }
  :deep(.ant-select .ant-select-selector) {
    border: none !important;
    box-shadow: none !important;
    background: transparent !important;
    padding: 4px 6px !important;
    min-height: auto !important;
    height: auto !important;
  }
  :deep(.ant-select-disabled .ant-select-selector) {
    background: transparent !important;
    cursor: default;
  }
}

.modal-footer-actions {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
}
.footer-left,
.footer-right {
  display: flex;
  gap: 8px;
}
</style>
