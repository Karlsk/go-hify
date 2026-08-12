<template>
  <div>
    <div class="title">模型提供商管理</div>
    <div v-if="state === 'loading'" class="status">正在检测后端连通性…</div>
    <div v-else-if="state === 'ok'" class="status status--ok">
      后端已连接：{{ message }}
    </div>
    <div v-else class="status status--fail">后端未连接</div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getHealth } from '@/api/health'

type State = 'loading' | 'ok' | 'fail'

const state = ref<State>('loading')
const message = ref('')

onMounted(async () => {
  try {
    message.value = await getHealth()
    state.value = 'ok'
  } catch {
    state.value = 'fail'
  }
})
</script>

<style scoped>
.title {
  margin-bottom: 16px;
  font-size: 18px;
  font-weight: 600;
}
.status--ok {
  color: var(--el-color-success);
}
.status--fail {
  color: var(--el-color-danger);
}
</style>
