<template>
  <el-container class="app">
    <el-aside width="220px" class="app__aside">
      <div class="app__brand">
        <div class="app__logo">H</div>
        <span class="app__name">Hify</span>
      </div>
      <el-menu :default-active="activeMenu" router class="app__menu">
        <el-menu-item index="/provider">
          <el-icon><Connection /></el-icon>
          <span>模型管理</span>
        </el-menu-item>
        <el-menu-item index="/agent">
          <el-icon><Setting /></el-icon>
          <span>Agent 管理</span>
        </el-menu-item>
        <el-menu-item index="/chat">
          <el-icon><ChatDotRound /></el-icon>
          <span>对话</span>
        </el-menu-item>
      </el-menu>
    </el-aside>
    <el-main class="app__main">
      <router-view />
    </el-main>
  </el-container>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { ChatDotRound, Connection, Setting } from '@element-plus/icons-vue'

const route = useRoute()
const activeMenu = computed(() => route.path)
</script>

<style scoped>
.app {
  height: 100vh;
}

/* ---------- 深色侧边栏（token 见 tokens.css《深色侧边栏》） ---------- */
.app__aside {
  display: flex;
  flex-direction: column;
  background-color: var(--hf-sidebar-bg);
}

.app__brand {
  display: flex;
  align-items: center;
  gap: var(--hf-space-3);
  height: 56px;
  padding: 0 var(--hf-space-5);
  border-bottom: 1px solid var(--hf-sidebar-border);
}

.app__logo {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: var(--hf-radius-sm);
  background: var(--hf-gradient-brand);
  color: var(--hf-text-inverse);
  font-size: var(--hf-font-size-base);
  font-weight: var(--hf-font-weight-semibold);
}

.app__name {
  color: var(--hf-sidebar-text-active);
  font-size: var(--hf-font-size-md);
  font-weight: var(--hf-font-weight-semibold);
  letter-spacing: 0.02em;
}

.app__menu {
  flex: 1;
  padding: var(--hf-space-3);
  border-right: none;
  background-color: transparent;
}

.app__menu :deep(.el-menu-item) {
  height: 40px;
  line-height: 40px;
  margin-bottom: var(--hf-space-1);
  padding-left: var(--hf-space-3) !important;
  border-radius: var(--hf-radius-sm);
  color: var(--hf-sidebar-text);
  transition:
    background-color var(--hf-duration-fast) var(--hf-ease-in-out),
    color var(--hf-duration-fast) var(--hf-ease-in-out);
}

.app__menu :deep(.el-menu-item .el-icon) {
  color: inherit;
}

.app__menu :deep(.el-menu-item:hover) {
  background-color: var(--hf-sidebar-bg-hover);
  color: var(--hf-sidebar-text-hover);
}

.app__menu :deep(.el-menu-item.is-active) {
  background-color: var(--hf-sidebar-bg-active);
  color: var(--hf-sidebar-text-active);
}

/* ---------- 主内容区 ---------- */
.app__main {
  padding: var(--hf-space-6);
  background-color: var(--hf-bg-page);
}
</style>
