<template>
  <a-modal
    title="修改头像"
    :open="props.open"
    width="800px"
    :mask-closable="false"
    :footer="null"
    @cancel="handleCancel"
  >
    <a-row :gutter="24">
      <a-col :xs="24" :md="14" :style="{ height: '350px' }">
        <vue-cropper
          ref="cropperRef"
          :img="options.img"
          :info="true"
          :autoCrop="options.autoCrop"
          :autoCropWidth="options.autoCropWidth"
          :autoCropHeight="options.autoCropHeight"
          :fixedBox="options.fixedBox"
          :outputType="options.outputType"
          @realTime="realTime"
        >
        </vue-cropper>
      </a-col>
      <a-col :xs="24" :md="10" class="preview-area">
        <div class="avatar-upload-preview">
          <div :style="previews.div" class="preview-wrapper">
            <img :src="previews.url" :style="previews.img" />
          </div>
        </div>
      </a-col>
    </a-row>
    <br />
    <a-row type="flex" justify="space-between" align="middle">
      <a-col>
        <a-upload name="file" :beforeUpload="beforeUpload" :showUploadList="false" accept="image/*">
          <a-button
            ><template #icon><UploadOutlined /></template>选择图片</a-button
          >
        </a-upload>
      </a-col>
      <a-col>
        <a-space>
          <a-button @click="changeScale(1)"
            ><template #icon><PlusOutlined /></template
          ></a-button>
          <a-button @click="changeScale(-1)"
            ><template #icon><MinusOutlined /></template
          ></a-button>
          <a-button @click="rotateLeft"
            ><template #icon><RotateLeftOutlined /></template
          ></a-button>
          <a-button @click="rotateRight"
            ><template #icon><RotateRightOutlined /></template
          ></a-button>
          <a-button type="primary" @click="onSave" :loading="loading">保存</a-button>
        </a-space>
      </a-col>
    </a-row>
  </a-modal>
</template>

<script setup>
import { ChangeAvatarApi } from '@/api/profile'
import {
    MinusOutlined,
    PlusOutlined,
    RotateLeftOutlined,
    RotateRightOutlined,
    UploadOutlined,
} from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { reactive, ref } from 'vue'
import { VueCropper } from 'vue-cropper'
import 'vue-cropper/dist/index.css'

const props = defineProps({ open: Boolean })
const emit = defineEmits(['update:open', 'ok'])

const cropperRef = ref(null)
const loading = ref(false)
const previews = ref({})
const options = reactive({
  img: '',
  autoCrop: true,
  autoCropWidth: 200,
  autoCropHeight: 200,
  fixedBox: true,
  outputType: 'png',
  fileName: '',
})

// ... changeScale, rotateLeft, beforeUpload 等方法保持上面的代码不变 ...
const changeScale = (num) => {
  num = num || 1
  cropperRef.value?.changeScale(num)
}
const rotateLeft = () => cropperRef.value?.rotateLeft()
const rotateRight = () => cropperRef.value?.rotateRight()
const realTime = (data) => {
  previews.value = data
}

const beforeUpload = (rawFile) => {
  if (rawFile.type.indexOf('image/') === -1) {
    message.error('请上传图片类型文件!')
    return false
  }
  if (rawFile.size / 1024 / 1024 > 5) {
    message.error('文件大小不能超过 5MB!')
    return false
  }
  options.fileName = rawFile.name
  const reader = new FileReader()
  reader.readAsDataURL(rawFile)
  reader.onload = () => {
    options.img = reader.result
  }
  return false
}

const onSave = () => {
  if (!options.img) return message.warning('请先选择图片')
  loading.value = true

  cropperRef.value.getCropBlob(async (blobData) => {
    if (!blobData) {
      loading.value = false
      return message.error('裁剪失败')
    }

    const formData = new FormData()
    formData.append('file', blobData, options.fileName || 'avatar.png')

    try {
      const res = await ChangeAvatarApi(formData)

      // 只要 code 是 0000，就认为上传成功
      if (res.code === '0000') {
        message.success('头像修改成功')

        // 不把本地 blob: 地址交给上层：它只在当前页面有效，
        // 一旦随 store 持久化，刷新后必然失效（头像回落首字方块）。
        // 由父组件重新拉取个人信息，使用服务端返回的权威地址。
        emit('ok')
        handleCancel()
      } else {
        message.error(res.message || '上传失败')
      }
    } catch {
      message.error('网络请求异常')
    } finally {
      loading.value = false
    }
  })
}

const handleCancel = () => {
  emit('update:open', false)
}
</script>

<style lang="less" scoped>
/* 样式保持不变，参考上一个回答 */
.preview-area {
  display: flex;
  align-items: center;
  justify-content: center;
  background-color: #fafafa;
}
.avatar-upload-preview {
  width: 200px;
  height: 200px;
  border-radius: 50%;
  overflow: hidden;
  border: 2px solid #fff;
  box-shadow: 0 0 10px rgba(0, 0, 0, 0.1);
  display: flex;
  justify-content: center;
  align-items: center;
}
.preview-wrapper {
  overflow: hidden;
  border-radius: 50%;
}

@media (max-width: 768px) {
  .preview-area {
    padding-block: 12px;
  }

  .avatar-upload-preview {
    width: min(200px, calc(100vw - 64px));
    height: min(200px, calc(100vw - 64px));
  }

  :deep(.ant-row) {
    margin-inline: 0 !important;
  }

  :deep(.ant-col) {
    padding-inline: 0 !important;
  }

  :deep(.ant-space) {
    flex-wrap: wrap !important;
  }
}
</style>
