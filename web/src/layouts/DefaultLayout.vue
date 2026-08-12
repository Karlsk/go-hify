<template>
  <el-container class="layout">
    <el-aside width="220px" class="layout__aside">
      <div class="layout__logo">Hify</div>
      <el-menu :default-active="activeMenu" router class="layout__menu">
        <el-menu-item index="/provider">
          <el-icon><Connection /></el-icon>
          <span>模型提供商</span>
        </el-menu-item>
        <el-menu-item index="/agent">
          <el-icon><Setting /></el-icon>
          <span>Agent</span>
        </el-menu-item>
        <el-menu-item index="/chat">
          <el-icon><ChatDotRound /></el-icon>
          <span>对话</span>
        </el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header class="layout__header">
        <span class="layout__title">{{ pageTitle }}</span>
      </el-header>
      <el-main class="layout__main">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { ChatDotRound, Connection, Setting } from '@element-plus/icons-vue'

const route = useRoute()
const activeMenu = computed(() => route.path)
const pageTitle = computed(() => (route.meta.title as string | undefined) ?? 'Hify')
</script>

<style scoped>
.layout {
  height: 100vh;
}
.layout__aside {
  border-right: 1px solid var(--el-border-color-light);
}
.layout__logo {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 60px;
  font-size: 20px;
  font-weight: 600;
  color: var(--el-color-primary);
}
.layout__menu {
  border-right: none;
}
.layout__header {
  display: flex;
  align-items: center;
  border-bottom: 1px solid var(--el-border-color-light);
}
.layout__title {
  font-size: 16px;
  font-weight: 500;
}
.layout__main {
  background-color: var(--el-bg-color-page);
}
</style>
