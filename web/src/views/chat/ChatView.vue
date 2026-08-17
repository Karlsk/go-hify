<template>
  <div class="chat">
    <PageHeader
      title="对话"
      description="SSE 流式响应，多轮上下文，支持 MCP 工具调用"
    />
    <el-card class="chat__panel">
      <p class="placeholder">对话界面建设中（SSE 渲染层为手写组件，见 web/README.md）</p>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
</script>

<style scoped>
/* 对话页未来需要全高布局，先占位；届时 chat__panel 撑满剩余高度 */
.placeholder {
  margin: 0;
  color: var(--hf-text-3);
}
</style>
