<template>
  <div>
    <PageHeader
      title="模型管理"
      description="多模型提供商统一接入，API Key 加密存储，支持连通性探测"
    >
      <template #actions>
        <el-button>批量导入</el-button>
        <!-- 主操作：品牌渐变按钮（main.css 全局覆写，见 design-system.md《整体布局》） -->
        <el-button type="primary">添加提供商</el-button>
      </template>
    </PageHeader>
    <el-card>
      <div v-if="state === 'loading'" class="status">正在检测后端连通性…</div>
      <div v-else-if="state === 'ok'" class="status status--ok">
        后端已连接：{{ message }}
      </div>
      <div v-else class="status status--fail">后端未连接</div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getHealth } from '@/api/health'
import PageHeader from '@/components/PageHeader.vue'

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
.status--ok {
  color: var(--el-color-success);
}
.status--fail {
  color: var(--el-color-danger);
}
</style>
