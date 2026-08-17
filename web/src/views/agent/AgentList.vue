<template>
  <div>
    <PageHeader
      title="Agent 管理"
      description="选模型、绑 MCP 工具、设系统提示词，组装可对话的 Agent"
    >
      <template #actions>
        <el-button type="primary">创建 Agent</el-button>
      </template>
    </PageHeader>
    <el-card>
      <p class="placeholder">Agent 列表待接入（后端 agent 模块联调后渲染表格）</p>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
</script>

<style scoped>
.placeholder {
  margin: 0;
  color: var(--hf-text-3);
}
</style>
