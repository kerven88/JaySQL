<template>
  <span v-if="hasAccess" class="permission-hint">
    <a-tooltip>
      <template #title>您有该表的数据访问权限</template>
      <TableOutlined :style="{ fontSize: '14px', color: '#52c41a' }" />
    </a-tooltip>
  </span>
  <span v-else class="permission-hint">
    <a-tooltip>
      <template #title>您没有该表的数据访问权限</template>
      <CloseCircleTwoTone two-tone-color="#eb2f96" />
    </a-tooltip>
  </span>
</template>

<script setup>
import { CloseCircleTwoTone, TableOutlined } from '@ant-design/icons-vue';

defineProps({
  hasAccess: {
    type: Boolean,
    required: true,
  },
})
</script>

<style scoped>
.permission-hint {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  margin-right: 4px;
  font-size: 14px;
  line-height: 14px;
  vertical-align: middle;
  flex-shrink: 0;
}
</style>
