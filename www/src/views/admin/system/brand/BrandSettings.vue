<template>
  <div class="gi-page-shell">
    <div class="gi-page-toolbar gi-toolbar-single-line">
      <div class="gi-page-title">品牌设置</div>
    </div>

    <a-card>
      <a-row :gutter="[16, 16]">
        <a-col v-for="item in brandItems" :key="item.type" :xs="24" :lg="12">
          <div class="brand-card">
            <div class="brand-card__head">
              <span class="brand-card__title">{{ item.title }}</span>
            </div>

            <div class="brand-card__preview" :class="`is-${item.type}`">
              <img :src="item.preview" :alt="item.title" @error="item.onError" />
            </div>

            <div class="brand-card__meta">
              <div>支持 png / jpg / webp，单张不超过 2MB</div>
              <div>{{ item.sizeHint }}</div>
            </div>

            <div class="brand-card__actions">
              <a-upload
                name="file"
                accept="image/png,image/jpeg,image/webp"
                :showUploadList="false"
                :disabled="!canManage || item.uploading"
                :beforeUpload="(file) => beforeUpload(item.type, file)"
              >
                <a-button type="primary" :disabled="!canManage" :loading="item.uploading">
                  <template #icon><UploadOutlined /></template>
                  {{ item.url ? '更换 Logo' : '上传 Logo' }}
                </a-button>
              </a-upload>

              <a-popconfirm
                title="恢复为系统内置 Logo？"
                ok-text="恢复"
                cancel-text="取消"
                :disabled="!canManage || !item.url"
                @confirm="handleReset(item.type)"
              >
                <a-button :disabled="!canManage || !item.url">恢复默认</a-button>
              </a-popconfirm>
            </div>
          </div>
        </a-col>
      </a-row>

      <!-- 品牌文字：侧栏左上角 Logo 旁的名称 + 首页大标题，两个字段一个保存按钮 -->
      <div class="brand-text-card">
        <div class="brand-text-card__head">
          <span class="brand-card__title">品牌文字</span>
        </div>

        <a-form layout="vertical" class="brand-text-card__form" :disabled="!canManage">
          <a-row :gutter="16">
            <a-col :xs="24" :md="12">
              <a-form-item label="侧栏品牌名称">
                <a-input
                  v-model:value="textContent.brandName"
                  :maxlength="32"
                  show-count
                  placeholder="默认 JaySQL"
                  allow-clear
                />
              </a-form-item>
            </a-col>
            <a-col :xs="24" :md="12">
              <a-form-item label="首页大标题">
                <a-input
                  v-model:value="textContent.homeTitle"
                  :maxlength="32"
                  show-count
                  placeholder="默认 JaySQL DevOps"
                  allow-clear
                />
              </a-form-item>
            </a-col>
          </a-row>
        </a-form>

        <div class="brand-text-card__actions">
          <a-button
            type="primary"
            :loading="textSaving"
            :disabled="!canManage || !textDirty"
            @click="handleSaveText"
          >
            保存文字
          </a-button>
          <a-popconfirm
            title="恢复为内置默认文字（JaySQL / JaySQL DevOps）？"
            ok-text="恢复"
            cancel-text="取消"
            :disabled="!canManage || (!brandStore.brandName && !brandStore.homeTitle)"
            @confirm="handleResetText"
          >
            <a-button
              :disabled="!canManage || (!brandStore.brandName && !brandStore.homeTitle)"
            >
              恢复默认文字
            </a-button>
          </a-popconfirm>
        </div>
      </div>

      <div v-if="!canManage" class="brand-readonly">当前账号无修改权限（需要「系统管理」访问权限）。</div>
    </a-card>
  </div>
</template>

<script setup>
defineOptions({ name: 'BrandSettings' })

import { resetBrandLogoApi, updateBrandTextApi, uploadBrandLogoApi } from '@/api/brand'
import { useBrandStore } from '@/store/brand'
import { useUserStore } from '@/store/user'
// 内置默认图：用于「未配置 / 自定义图加载失败」时的预览与回落
import loginDefault from '@/assets/jaysql-login.svg'
import sidebarDefault from '@/assets/jaysql-otter.png'
import { UploadOutlined } from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { computed, onMounted, reactive, ref, watch } from 'vue'

const MAX_SIZE = 2 * 1024 * 1024
const ALLOWED_TYPES = ['image/png', 'image/jpeg', 'image/webp']

const userStore = useUserStore()
const brandStore = useBrandStore()

// 自定义图加载失败时（文件被误删等）回落到内置图，避免预览区出现破图
const brokenState = reactive({ login: false, sidebar: false })
const uploadingState = reactive({ login: false, sidebar: false })

// 与后端 brand 模块一致：权限点复用 system.access（进入系统管理模块的既有权限）
const canManage = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.access'),
)

const brandItems = computed(() => [
  {
    type: 'login',
    title: '登录页 Logo',
    url: brandStore.loginLogoUrl,
    sizeHint: '建议宽度 ≤ 320px，高度自适应',
    uploading: uploadingState.login,
    preview: !brokenState.login && brandStore.loginLogoUrl ? brandStore.loginLogoUrl : loginDefault,
    onError: () => {
      brokenState.login = true
    },
  },
  {
    type: 'sidebar',
    title: '页面左上角 Logo',
    url: brandStore.sidebarLogoUrl,
    sizeHint: '建议正方形 1:1，显示尺寸约 28px',
    uploading: uploadingState.sidebar,
    preview:
      !brokenState.sidebar && brandStore.sidebarLogoUrl ? brandStore.sidebarLogoUrl : sidebarDefault,
    onError: () => {
      brokenState.sidebar = true
    },
  },
])

const beforeUpload = (type, file) => {
  // 前端先做一次快筛，真正的把关在后端按文件头魔数嗅探——前端的 type/size 都能伪造
  if (!ALLOWED_TYPES.includes(file.type)) {
    message.error('仅支持 png / jpg / webp 格式')
    return false
  }
  if (file.size > MAX_SIZE) {
    message.error('图片不能超过 2MB')
    return false
  }
  handleUpload(type, file)
  return false // 交给 handleUpload 自己发请求，不走 a-upload 的默认上传
}

const handleUpload = async (type, file) => {
  uploadingState[type] = true
  try {
    const formData = new FormData()
    formData.append('type', type)
    formData.append('file', file)
    const res = await uploadBrandLogoApi(type, formData)
    if (res?.code === '0000') {
      message.success('Logo 已更新')
      brokenState[type] = false
      brandStore.applyBrand(res.data)
    } else {
      message.error(res?.message || '上传失败')
    }
  } catch {
    message.error('网络请求异常')
  } finally {
    uploadingState[type] = false
  }
}

const handleReset = async (type) => {
  try {
    const res = await resetBrandLogoApi(type)
    if (res?.code === '0000') {
      message.success('已恢复内置 Logo')
      brokenState[type] = false
      brandStore.applyBrand(res.data)
    } else {
      message.error(res?.message || '操作失败')
    }
  } catch {
    message.error('网络请求异常')
  }
}

// ---------- 品牌文字 ----------

const textContent = reactive({ brandName: '', homeTitle: '' })
const textSaving = ref(false)

// store 的值到达 / 变化时同步进编辑框（首次加载、刷新、保存成功后都会触发）
const syncTextFromStore = () => {
  textContent.brandName = brandStore.brandName || ''
  textContent.homeTitle = brandStore.homeTitle || ''
}
watch(() => [brandStore.brandName, brandStore.homeTitle], syncTextFromStore)

// 只有编辑框和当前生效值不一致时才允许保存，避免无变更的请求
const textDirty = computed(
  () =>
    textContent.brandName !== (brandStore.brandName || '') ||
    textContent.homeTitle !== (brandStore.homeTitle || ''),
)

const saveText = async (payload, successTip) => {
  textSaving.value = true
  try {
    const res = await updateBrandTextApi(payload)
    if (res?.code === '0000') {
      message.success(successTip)
      brandStore.applyBrand(res.data)
    } else {
      message.error(res?.message || '保存失败')
    }
  } catch {
    message.error('网络请求异常')
  } finally {
    textSaving.value = false
  }
}

const handleSaveText = () => {
  // 前端只拦首尾空白，长度 / 控制字符以后端 normalizeBrandText 为准（同一套规则）
  saveText(
    {
      brand_name: textContent.brandName.trim(),
      home_title: textContent.homeTitle.trim(),
    },
    '品牌文字已保存',
  )
}

const handleResetText = () => {
  saveText({ brand_name: '', home_title: '' }, '已恢复内置默认文字')
}

onMounted(() => {
  brandStore.fetchBrand(true)
})
</script>

<style scoped>
.brand-card {
  height: 100%;
  padding: 16px;
  background: #fafafa;
  border: 1px solid rgba(0, 0, 0, 0.06);
  border-radius: 8px;
}

.brand-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.brand-card__title {
  font-size: 15px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.85);
}

/* 预览区：登录页 Logo 是横向长图，侧栏 Logo 是方形小图标，分别给不同容器高度 */
.brand-card__preview {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
  background: #fff;
  border: 1px dashed rgba(0, 0, 0, 0.12);
  border-radius: 6px;
}

.brand-card__preview.is-login {
  height: 140px;
}

.brand-card__preview.is-sidebar {
  height: 140px;
}

/* object-fit: contain —— 不强裁剪，比例不合的图完整显示、居中留白 */
.brand-card__preview img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}

.brand-card__preview.is-sidebar img {
  width: 56px;
  height: 56px;
}

.brand-card__meta {
  margin-top: 12px;
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
  line-height: 1.8;
}

.brand-card__actions {
  display: flex;
  gap: 8px;
  margin-top: 12px;
}

.brand-readonly {
  margin-top: 12px;
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
}

/* 品牌文字卡片：与上方 Logo 卡片同一套底色描边，视觉上是一组 */
.brand-text-card {
  margin-top: 16px;
  padding: 16px;
  background: #fafafa;
  border: 1px solid rgba(0, 0, 0, 0.06);
  border-radius: 8px;
}

.brand-text-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 4px;
}

.brand-text-card__form {
  margin-top: 8px;
}

.brand-text-card__form :deep(.ant-form-item) {
  margin-bottom: 8px;
}

.brand-text-card__actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}

@media (max-width: 768px) {
  .brand-card {
    padding: 12px;
  }

  .brand-card__preview {
    padding: 12px;
  }

  .brand-card__actions {
    flex-wrap: wrap;
  }

  .brand-text-card {
    padding: 12px;
  }

  .brand-text-card__actions {
    flex-wrap: wrap;
  }
}
</style>
