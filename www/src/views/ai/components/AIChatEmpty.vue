<template>
  <div class="ai-empty">
    <div class="empty-hero">
      <div class="empty-mark">
        <RobotOutlined />
      </div>
      <div class="empty-title">
        你好，我是 AI 助手
        <span class="beta-tag">Beta</span>
      </div>
      <div class="empty-sub">
        我可以在你的权限范围内查看备份、监控、工单数据，也能帮你生成 SQL、创建任务。<br />
        写操作我会先给出计划，等你确认后再执行。
      </div>
    </div>
  </div>
</template>

<script setup>
import { RobotOutlined } from '@ant-design/icons-vue'
</script>

<style scoped>
.ai-empty {
  max-width: 720px;
  margin: 0 auto;
  padding: 24px 0 8px;
}

.empty-hero {
  text-align: center;
}

.empty-mark {
  width: 62px;
  height: 62px;
  margin: 0 auto 14px;
  border-radius: 20px;
  background: linear-gradient(135deg, rgba(15, 118, 110, 0.12) 0%, rgba(109, 91, 214, 0.14) 100%);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 26px;
  color: #0f766e;
}

.empty-title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 20px;
  font-weight: 700;
  color: #14313d;
}

.beta-tag {
  display: inline-flex;
  align-items: center;
  padding: 2px 9px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: #147866;
  background: rgba(24, 165, 143, 0.08);
  border: 1px solid rgba(24, 165, 143, 0.14);
}

.empty-sub {
  margin-top: 8px;
  font-size: 13px;
  line-height: 1.7;
  color: #67808d;
}
</style>
