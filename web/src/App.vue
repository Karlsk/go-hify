<template>
  <el-container class="app">
    <el-aside width="220px" class="app__aside">
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
.app__aside {
  border-right: 1px solid var(--el-border-color-light);
}
.app__menu {
  height: 100%;
  border-right: none;
}
</style>
