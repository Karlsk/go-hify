<template>
  <div class="page">
    <h2 class="page__title">对话</h2>
    <el-empty description="骨架占位 — SSE 流式渲染层业务待实现" />
  </div>
</template>

<script setup lang="ts">
// ChatView：SSE 流式对话（fetch + ReadableStream，见 utils/sse.ts），业务待实现
</script>

<style scoped>
.page {
  padding: 16px;
}
.page__title {
  margin: 0 0 16px;
  font-size: 18px;
}
</style>
