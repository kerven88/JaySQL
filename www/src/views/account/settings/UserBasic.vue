<template>
  <div class="account-settings-info-view">
    <a-row class="settings-row" :gutter="[24, 24]" type="flex" justify="center">
      <a-col :order="1" :xs="24" :lg="16">
        <a-card class="settings-card" :bordered="false">
          <template #title>
            <div class="card-title-wrap">
              <div class="card-title">基本信息</div>
              <div class="card-subtitle">用于展示在平台中的个人资料，更新后立即生效</div>
            </div>
          </template>

          <a-form
            class="settings-form"
            layout="vertical"
            :model="formState"
            :rules="rules"
            name="basic"
            autocomplete="off"
            @finish="onFinish"
          >
            <a-form-item label="用户名" name="username">
              <a-input disabled v-model:value="formState.username" placeholder="用户名不可修改" />
            </a-form-item>

            <a-form-item label="邮箱" has-feedback name="email">
              <a-input v-model:value="formState.email" placeholder="请输入邮箱" />
            </a-form-item>

            <a-form-item label="手机号" has-feedback name="mobile">
              <a-input v-model:value="formState.mobile" placeholder="请输入手机号" />
            </a-form-item>

            <a-form-item label="角色" extra="由系统管理员配置，如需调整请联系管理员">
              <div v-if="!roleNames.length" class="empty-value">未分配角色</div>
              <div v-else class="permission-tags">
                <a-tag v-for="item in roleNames" :key="item" color="blue">{{ item }}</a-tag>
              </div>
            </a-form-item>

            <a-form-item class="form-actions">
              <a-button type="primary" html-type="submit" :loading="loading">更新基本信息</a-button>
            </a-form-item>
          </a-form>
        </a-card>
      </a-col>

      <a-col :order="1" :xs="24" :lg="8">
        <a-card class="avatar-card" :bordered="false">
          <template #title>
            <div class="card-title-wrap">
              <div class="card-title">头像设置</div>
              <div class="card-subtitle">点击头像可重新上传，建议使用 1:1 比例图片</div>
            </div>
          </template>

          <button type="button" class="avatar-upload-panel" aria-label="更换头像" @click="openModal()">
            <img :src="option.img" alt="头像" @error="handleAvatarError" />
            <div class="avatar-mask">
              <PlusOutlined />
              <span>更换头像</span>
            </div>
          </button>
        </a-card>
      </a-col>
    </a-row>

    <avatar-modal :open="open" @update:open="open = $event" @ok="setavatar" />
  </div>
</template>

<script setup>
import { GetUserProfileApi } from '@/api/login'
import { UpdateUserInfoApi } from '@/api/profile'
import { useUserStore } from '@/store/user'
import { regEmail, regPhone } from '@/utils/validate'
import { PlusOutlined } from '@ant-design/icons-vue'
import { useThrottleFn } from '@vueuse/core'
import { message } from 'ant-design-vue'
import { computed, onMounted, reactive, ref, watch } from 'vue'
import AvatarModal from './AvatarModal.vue'

const userStore = useUserStore()
const open = ref(false)
const loading = ref(false) // 新增 loading 状态

// 表单数据
const formState = reactive({
  username: '',
  email: '',
  mobile: '',
  role: '',
})

const DEFAULT_AVATAR = '/avatar.png'

// 头像显示数据
const option = ref({
  img: userStore.avatar || DEFAULT_AVATAR, // 防止初始化为 undefined
})

// 校验规则：邮箱
const validateEmail = async (_rule, value) => {
  if (!value) {
    return Promise.reject('邮箱不可为空')
  } else if (!regEmail.test(value)) {
    return Promise.reject('请输入合法的邮箱')
  }
  return Promise.resolve()
}

// 校验规则：手机号
const validateMobile = async (_rule, value) => {
  if (!value) {
    return Promise.reject('手机号不可为空')
  } else if (!regPhone.test(value)) {
    return Promise.reject('请输入合法的手机号')
  }
  return Promise.resolve()
}

const rules = {
  email: [{ required: true, validator: validateEmail, trigger: 'blur' }],
  mobile: [{ required: true, validator: validateMobile, trigger: 'blur' }],
}

const roleNames = computed(() => {
  return (formState.role || '')
    .split(';')
    .map((item) => item.trim())
    .filter(Boolean)
})

const reloadUserProfile = async () => {
  try {
    const res = await GetUserProfileApi()
    if (res.code === '0000') {
      const data = res.data

      userStore.setUserInfo({
        uid: data.uid,
        username: data.username,
        avatar_file: data.avatar_file,
        email: data.email,
        mobile: data.mobile,
        roles: data.roles,
        role: data.role,
        date_joined: data.date_joined,
        is_superuser: data.is_superuser,
        permission_ids: data.permission_ids,
        effective_permissions: data.effective_permissions,
      })

      formState.username = data.username
      formState.email = data.email
      formState.mobile = data.mobile
      formState.role = data.role

      option.value.img = data.avatar_file || DEFAULT_AVATAR
    }
  } catch (error) {
    message.error('获取个人信息失败，请稍后重试')
    console.error('获取用户信息失败', error)
  }
}

const onFinish = useThrottleFn(async (values) => {
  loading.value = true

  const params = {
    ...values,
    uid: userStore.uid,
  }

  const res = await UpdateUserInfoApi(params).catch(() => {})
  if (res) {
    message.success('更新成功')
    await reloadUserProfile()
  } else {
    message.error(res?.message || '更新失败')
  }

  loading.value = false
})

const setavatar = async () => {
  // 上传接口只回 success、不回文件地址，因此重新拉取个人信息拿服务端权威地址。
  // 不能沿用本地 blob: 地址——它只在当前页面有效，刷新后失效会导致头像回落首字方块。
  await reloadUserProfile()

  const file = String(userStore.avatar || '').trim()
  if (!file) {
    option.value.img = DEFAULT_AVATAR
    return
  }

  // 追加时间戳：后端文件名带时间戳本就不会重名，这里是双保险，防止同名文件被浏览器缓存
  const newUrl = `${file}${file.includes('?') ? '&' : '?'}t=${Date.now()}`
  option.value.img = newUrl
  userStore.setUserAvatar(newUrl)
}

const handleAvatarError = () => {
  option.value.img = DEFAULT_AVATAR
}

const openModal = () => {
  open.value = true
}

onMounted(() => {
  reloadUserProfile()
})

watch(
  () => userStore.username,
  (val) => {
    if (val && !formState.username) {
      formState.username = userStore.username
      formState.email = userStore.email
      formState.mobile = userStore.mobile
      formState.role = userStore.role
      option.value.img = userStore.avatar
    }
  },
  { immediate: true },
)
</script>

<style lang="less" scoped>
.account-settings-info-view {
  padding-top: 12px;
}

.settings-card,
.avatar-card {
  height: 100%;
  border-radius: 12px;
}

.settings-form {
  padding-top: 8px;
}

.settings-form :deep(.ant-form-item) {
  margin-bottom: 16px;
}

.settings-form :deep(.ant-form-item:last-child) {
  margin-bottom: 0;
}

.settings-form :deep(.ant-input[disabled]) {
  color: var(--gi-color-text-secondary);
}

.card-title-wrap {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.card-title {
  color: var(--gi-color-text-primary);
  font-size: 16px;
  font-weight: 600;
  line-height: 24px;
}

.card-subtitle {
  color: var(--gi-color-text-secondary);
  font-size: 12px;
  line-height: 20px;
}

.org-role-list {
  margin: 0;
  padding: 12px;
  list-style: none;
  border: 1px solid var(--gi-color-border);
  border-radius: 8px;
  background: color-mix(in srgb, var(--gi-color-page-bg), #ffffff 35%);
}

.org-role-list .org-role-item + .org-role-item {
  margin-top: 8px;
}

.org-role-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.org-name {
  flex: 1;
  min-width: 0;
  color: var(--gi-color-text-primary);
  font-size: 14px;
  line-height: 22px;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.empty-value {
  padding: 12px;
  border: 1px dashed var(--gi-color-border);
  border-radius: 8px;
  color: var(--gi-color-text-secondary);
  font-size: 14px;
  line-height: 22px;
}

.avatar-card :deep(.ant-card-body) {
  display: flex;
  align-items: center;
  justify-content: center;
  padding-top: 24px;
  padding-bottom: 24px;
}

.avatar-upload-panel {
  position: relative;
  width: 180px;
  height: 180px;
  border-radius: 50%;
  cursor: pointer;
  overflow: hidden;
  background: transparent;
  padding: 0;
  border: 1px solid var(--gi-color-border);
  box-shadow: var(--gi-shadow-sm);
}

.avatar-upload-panel:focus-visible {
  outline: 2px solid var(--gi-color-primary);
  outline-offset: 2px;
}

.avatar-upload-panel img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.avatar-mask {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  background: rgba(0, 0, 0, 0.45);
  color: #ffffff;
  opacity: 0;
  transition: opacity var(--gi-duration-base) ease;
}

.avatar-mask .anticon {
  font-size: 20px;
}

.avatar-mask span {
  font-size: 12px;
  line-height: 20px;
}

.avatar-upload-panel:hover .avatar-mask {
  opacity: 1;
}

.form-actions {
  margin-top: 8px;
}

@media (max-width: 1023px) {
  .account-settings-info-view {
    padding-top: 8px;
  }

  .avatar-upload-panel {
    width: 160px;
    height: 160px;
  }
}

@media (max-width: 767px) {
  .settings-row {
    row-gap: 12px;
  }

  .settings-form :deep(.ant-form-item) {
    margin-bottom: 12px;
  }

  .avatar-upload-panel {
    width: 140px;
    height: 140px;
  }
}

@media (max-width: 768px) {
  /* antd 的 xs 是 <576px，576~991 这段没有响应式 span 命中，
     两张卡片会按内容宽度挤在一行 */
  .settings-row :deep(.ant-col) {
    flex: 0 0 100% !important;
    max-width: 100% !important;
  }

  .settings-row :deep(.ant-row) {
    margin-inline: 0 !important;
  }

  .settings-row :deep(.ant-col) {
    padding-inline: 0 !important;
  }
}
</style>
