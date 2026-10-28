<template>
  <div class="login-page">
    <div class="login-shell">
      <section class="visual-panel" :style="visualStyle">
        <div class="visual-overlay"></div>
      </section>
      <section class="form-panel">
        <div class="login-card">
          <div class="login-header">
            <img
              class="login-logo"
              :src="loginLogoSrc"
              alt="JaySQL"
              @error="loginLogoBroken = true"
            />
            <p>{{ uiState.registerMode ? '填写少量信息即可快速注册账号' : '请输入账号信息登录控制台' }}</p>
          </div>

          <a-form
            ref="formRef"
            :model="formState"
            autocomplete="off"
            :rules="rules"
            layout="vertical"
            @finish="onSubmit"
          >
            <a-form-item name="username">
              <a-input
                v-model:value="formState.username"
                size="large"
                placeholder="请输入用户名"
                allow-clear
              >
                <template #prefix>
                  <UserOutlined class="input-icon" />
                </template>
              </a-input>
            </a-form-item>

            <a-form-item name="password">
              <a-input-password
                v-model:value="formState.password"
                size="large"
                placeholder="请输入密码"
              >
                <template #prefix>
                  <LockOutlined class="input-icon" />
                </template>
              </a-input-password>
            </a-form-item>

            <a-form-item v-if="uiState.registerMode" name="confirm_password">
              <a-input-password
                v-model:value="formState.confirm_password"
                size="large"
                placeholder="请再次输入密码"
              >
                <template #prefix>
                  <LockOutlined class="input-icon" />
                </template>
              </a-input-password>
            </a-form-item>

            <a-form-item v-if="uiState.registerMode" name="email">
              <a-input v-model:value="formState.email" size="large" placeholder="请输入邮箱">
                <template #prefix>
                  <MailOutlined class="input-icon" />
                </template>
              </a-input>
            </a-form-item>

            <a-form-item v-if="uiState.registerMode" name="mobile">
              <a-input v-model:value="formState.mobile" size="large" placeholder="手机号（选填）">
                <template #prefix>
                  <PhoneOutlined class="input-icon" />
                </template>
              </a-input>
            </a-form-item>

            <a-form-item class="submit-item">
              <a-button type="primary" html-type="submit" size="large" :loading="uiState.loading" block>
                {{ submitButtonText }}
              </a-button>
            </a-form-item>
          </a-form>

          <div class="entry-switch">
            <template v-if="uiState.registerMode">
              <span>已有账号？</span>
              <a @click="backToLoginMode">返回登录</a>
            </template>
            <template v-else>
              <span>还没有账号？</span>
              <a @click="openRegisterMode">立即注册</a>
            </template>
          </div>

          <div class="login-footer">
            <span>© 2026 JaySQL</span>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup>
defineOptions({ name: 'UserLogin' })

import { Login, Register } from '@/api/login'
import { useBrandStore } from '@/store/brand'
import { useUserStore } from '@/store/user'
// 内置默认图：未配置自定义 Logo、或自定义地址加载失败时回落它
import logoSvg from '@/assets/jaysql-login.svg'
import loginVisual from '@/assets/original2.png'
import { resolveLoginTarget } from '@/views/login/loginModel'
import {
  LockOutlined,
  MailOutlined,
  PhoneOutlined,
  UserOutlined,
} from '@ant-design/icons-vue'
import { regEmail, regPassword, regPhone } from '@/utils/validate'
import { message } from 'ant-design-vue'
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const visualStyle = {
  backgroundImage: `linear-gradient(120deg, rgba(8, 24, 31, 0.72), rgba(9, 30, 42, 0.82)), url(${loginVisual})`,
}

const formRef = ref(null)
const router = useRouter()
const route = useRoute()
const userStore = useUserStore()
// 登录页**必须自己拉一次**品牌配置：它不走登录后的 Pinia 初始化链路，
// 而 GET /api/v1/brand 是公开接口，未登录态也能读。
const brandStore = useBrandStore()
const loginLogoBroken = ref(false)
const loginLogoSrc = computed(() =>
  !loginLogoBroken.value && brandStore.loginLogoUrl ? brandStore.loginLogoUrl : logoSvg,
)
brandStore.fetchBrand()

let submittingSync = false
let loginSuccessNotified = false

const uiState = reactive({
  loading: false,
  registerMode: false,
})

const formState = reactive({
  username: '',
  password: '',
  confirm_password: '',
  email: '',
  mobile: '',
})

const rules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    {
      validator: (_r, value) => {
        const v = String(value ?? '').trim()
        if (!v) return Promise.resolve()
        if (v.length > 30) return Promise.reject(new Error('用户名称不能超过 30 个字符'))
        if (!/^[\u4e00-\u9fa5A-Za-z0-9_\-@]+$/.test(v)) {
          return Promise.reject(new Error('用户名仅支持英文、汉字、数字及 _ - @ 字符'))
        }
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
  password: [
    {
      validator: (_r, value) => {
        if (!uiState.registerMode && !value) return Promise.resolve()
        if (!value) return Promise.reject(new Error('请输入密码'))
        if (value.length < 8) return Promise.reject(new Error('密码长度至少 8 个字符'))
        if (!regPassword.test(value)) {
          return Promise.reject(new Error('密码必须包含大小写字母、数字和特殊字符'))
        }
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
  confirm_password: [
    { required: true, message: '请再次输入密码', trigger: 'blur' },
    {
      validator: (_rule, value) => {
        if (!uiState.registerMode || value === formState.password) {
          return Promise.resolve()
        }
        return Promise.reject(new Error('两次输入的密码不一致'))
      },
      trigger: 'blur',
    },
  ],
  email: [
    { required: true, message: '请输入邮箱', trigger: 'blur' },
    {
      validator: (_, value) => {
        const v = String(value || '').trim()
        if (!v) return Promise.resolve()
        if (!regEmail.test(v)) return Promise.reject(new Error('请输入合法的邮箱地址'))
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
  mobile: [
    {
      validator: (_rule, value) => {
        const v = String(value || '').trim()
        if (!v) return Promise.resolve()
        if (!regPhone.test(v)) return Promise.reject(new Error('请输入合法的手机号码'))
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
}

const submitButtonText = computed(() => {
  if (uiState.loading) {
    return uiState.registerMode ? '注册中...' : '登录中...'
  }
  return uiState.registerMode ? '注册账号' : '登录'
})

const consumeSessionExpiredNotice = () => {
  if (route.query.sessionExpired !== '1') {
    return
  }

  try {
    message.error({
      content: '登录已过期，请重新登录',
      duration: 4,
    })
  } catch (_) {
    try { message.error('登录已过期，请重新登录', 4) } catch (__) {}
  }
  const nextQuery = { ...route.query }
  delete nextQuery.sessionExpired
  router.replace({
    path: route.path,
    query: nextQuery,
  })
}

onMounted(() => {
  consumeSessionExpiredNotice()
})

watch(
  () => route.query.sessionExpired,
  (value) => {
    if (value === '1') {
      consumeSessionExpiredNotice()
    }
  },
)

const onSubmit = async () => {
  if (submittingSync) return
  if (uiState.registerMode) {
    await submitRegister()
    return
  }
  try {
    submittingSync = true
    uiState.loading = true
    const res = await Login(formState).catch(() => null)

    if (res?.code === '0000') {
      localStorage.setItem('onLine', '1')
      userStore.setToken(res.data.token)
      if (!loginSuccessNotified) {
        loginSuccessNotified = true
        try {
          message.success({
            content: '登录成功，正在跳转…',
            duration: 2,
          })
        } catch (_) {
          try { message.success('登录成功，正在跳转…', 2) } catch (__) {}
        }
      }
      const target = resolveLoginTarget(route.query.redirect)
      window.setTimeout(() => {
        try {
          window.location.assign(target)
        } catch (_) {
          window.location.href = target
        }
      }, 120)
      return
    }
  } finally {
    if (!loginSuccessNotified) {
      uiState.loading = false
      submittingSync = false
    }
  }
}

const submitRegister = async () => {
  if (submittingSync) return
  try {
    submittingSync = true
    uiState.loading = true
    const mobileStr = (formState.mobile ?? '').toString().trim()
    const payload = {
      username: formState.username,
      password: formState.password,
      confirm_password: formState.confirm_password,
      email: formState.email,
      mobile: mobileStr,
    }
    const res = await Register(payload).catch(() => null)
    if (res?.code === '0000') {
      message.success('注册成功，请登录')
      const registeredUsername = formState.username
      resetForm()
      uiState.registerMode = false
      formState.username = registeredUsername
      formRef.value?.clearValidate?.()
    }
  } finally {
    uiState.loading = false
    submittingSync = false
  }
}

const resetForm = () => {
  formState.username = ''
  formState.password = ''
  formState.confirm_password = ''
  formState.email = ''
  formState.mobile = ''
}

const openRegisterMode = () => {
  uiState.registerMode = true
  resetForm()
  formRef.value?.clearValidate?.()
}

const backToLoginMode = () => {
  uiState.registerMode = false
  resetForm()
  formRef.value?.clearValidate?.()
}
</script>

<style scoped>
.login-page {
  --ink-950: var(--gi-color-text-primary);
  --ink-700: var(--gi-color-text-secondary);
  --ink-500: var(--gi-color-text-tertiary);
  --line-200: var(--gi-color-border);
  --surface-100: var(--gi-color-page-bg);
  --primary-600: var(--gi-color-primary);
  --primary-700: var(--gi-color-primary-active);
  --primary-050: rgba(15, 118, 110, 0.12);

  min-height: 100vh;
  width: 100vw;
  padding: 0;
  overflow: hidden;
  background:
    radial-gradient(circle at 10% 20%, #dcebf4 0%, transparent 45%),
    radial-gradient(circle at 90% 85%, #d9efe8 0%, transparent 40%),
    linear-gradient(140deg, #e9f0f4 0%, #eef3f4 52%, #f4f7f7 100%);
}

.login-shell {
  width: 100vw;
  max-width: none;
  min-height: 100vh;
  height: 100vh;
  display: grid;
  grid-template-columns: 6fr 4fr;
  border-radius: 0;
  overflow: hidden;
  background: var(--gi-color-container-bg);
  box-shadow: none;
}

.visual-panel {
  position: relative;
  background-size: cover;
  background-position: center;
  padding: 56px 56px 48px;
  display: flex;
  align-items: flex-end;
}

.visual-overlay {
  position: absolute;
  inset: 0;
  background:
    linear-gradient(0deg, rgba(0, 0, 0, 0.25), transparent 32%),
    radial-gradient(circle at 25% 25%, rgba(154, 196, 214, 0.26), transparent 52%);
}

.visual-content {
  position: relative;
  z-index: 1;
  max-width: 500px;
  color: #eff8fb;
}

.visual-tag {
  display: inline-flex;
  padding: 6px 12px;
  margin-bottom: 18px;
  font-size: 12px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  border: 1px solid rgba(239, 248, 251, 0.38);
  border-radius: 999px;
  color: #f6fbfd;
  background: rgba(255, 255, 255, 0.08);
}

.visual-content h2 {
  margin: 0;
  font-size: 34px;
  line-height: 1.22;
  font-weight: 700;
}

.visual-desc {
  margin: 18px 0 24px;
  font-size: 15px;
  line-height: 1.7;
  color: rgba(239, 248, 251, 0.9);
}

.visual-points {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.visual-points li {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 14px;
  color: rgba(239, 248, 251, 0.96);
}

.form-panel {
  background: linear-gradient(180deg, #fbfdfd 0%, #f4f7f8 100%);
  padding: 36px 48px;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow-y: auto;
}

.login-card {
  width: min(440px, 100%);
  padding: var(--gi-spacing-ssm) 10px;
  background: transparent;
  border: none;
  border-radius: 0;
  backdrop-filter: none;
  box-shadow: none;
}

.login-header {
  text-align: center;
  margin-bottom: var(--gi-spacing-md);
}

.login-logo {
  width: min(280px, 80%);
  height: auto;
  max-height: 160px;
  margin: 0 auto 2px;
  object-fit: contain;
  transform: translateY(3px);
}

.login-header p {
  margin: 0;
  font-size: 13px;
  color: var(--ink-500);
}

.input-icon {
  color: #8ea2b0;
  font-size: 16px;
}

:deep(.ant-form-item) {
  margin-bottom: 20px;
}

:deep(.ant-input-affix-wrapper) {
  display: flex;
  align-items: center;
  height: 48px;
  padding: 0 14px;
  border-radius: 10px;
  border: 1.5px solid var(--line-200);
  background: #f9fbfc;
  transition: all 0.2s ease;
}

:deep(.ant-input-affix-wrapper .ant-input-prefix) {
  display: flex;
  align-items: center;
  margin-inline-end: 10px;
}

:deep(.ant-input-affix-wrapper .ant-input) {
  height: auto;
  padding: 0;
  border: none;
  background: transparent;
}

:deep(.ant-input:not(.ant-input-affix-wrapper .ant-input)) {
  height: 48px;
  border-radius: 10px;
  border: 1.5px solid var(--line-200);
  background: #f9fbfc;
  transition: all 0.2s ease;
}

:deep(.ant-input-affix-wrapper:hover),
:deep(.ant-input:hover) {
  border-color: #b7c6cf;
  background: #ffffff;
}

:deep(.ant-input-affix-wrapper-focused),
:deep(.ant-input:focus),
:deep(.ant-input-affix-wrapper:focus) {
  border-color: var(--primary-600);
  background: #ffffff;
  box-shadow: 0 0 0 3px var(--primary-050);
}

:deep(.ant-input) {
  font-size: 15px;
  background: transparent;
}

:deep(.ant-input::placeholder) {
  color: #a2b0bb;
}

.submit-item {
  margin-top: var(--gi-spacing-lg);
  margin-bottom: 0;
}

.entry-switch {
  margin-top: 16px;
  text-align: center;
  font-size: 13px;
  color: var(--ink-500);
}

.entry-switch a {
  margin-left: 6px;
  color: var(--primary-600);
}

:deep(.ant-btn-primary) {
  height: 48px;
  border-radius: 10px;
  background: linear-gradient(135deg, var(--primary-600) 0%, var(--primary-700) 100%);
  border: none;
  font-size: 15px;
  font-weight: 600;
  box-shadow: 0 8px 18px rgba(15, 118, 110, 0.24);
  transition: all 0.2s ease;
}

:deep(.ant-btn-primary:hover:not(:disabled)) {
  filter: brightness(1.06);
  box-shadow: 0 10px 20px rgba(15, 118, 110, 0.3);
}

:deep(.ant-btn-primary:active:not(:disabled)) {
  filter: brightness(0.94);
}

.login-footer {
  margin-top: 28px;
  text-align: center;
  font-size: var(--gi-font-size-caption);
  color: color-mix(in srgb, var(--gi-color-text-tertiary), #fff 12%);
}

@media (max-width: 1080px) {
  .login-shell {
    height: auto;
    min-height: 100vh;
    grid-template-columns: 1fr;
  }

  .visual-panel {
    min-height: 300px;
    padding: 36px 30px;
    align-items: flex-end;
  }

  .visual-content h2 {
    font-size: 28px;
  }

  .form-panel {
    padding: 24px;
  }
}

@media (max-width: 640px) {
  .visual-panel {
    min-height: 250px;
    padding: 28px 22px;
  }

  .visual-content h2 {
    font-size: 23px;
  }

  .visual-desc {
    margin-bottom: 16px;
  }

  .visual-points {
    gap: 8px;
  }

  .login-card {
    padding: 6px 2px;
  }

  .login-logo {
    width: min(240px, 84%);
    max-height: 140px;
  }
}

@media (max-width: 768px) {
  /* 100vw 会把移动端滚动条/地址栏宽度算进去，稳定产生横向溢出 */
  .login-page {
    width: 100%;
    min-width: 0;
    min-height: 100dvh;
    overflow-x: hidden;
  }

  .login-shell {
    width: 100%;
    max-width: 100%;
    min-height: 100dvh;
    height: auto;
    grid-template-columns: minmax(0, 1fr);
  }

  .form-panel {
    padding: 20px 16px;
  }

  .visual-panel {
    min-height: 200px;
    padding: 24px 18px;
  }

  .visual-content h2 {
    font-size: 21px;
  }

  .visual-desc {
    margin: 12px 0 16px;
    font-size: 14px;
  }
}
</style>
