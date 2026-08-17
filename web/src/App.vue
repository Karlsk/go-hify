<template>
  <el-container class="app">
    <el-aside
      :width="collapsed ? '64px' : '220px'"
      class="app__aside"
      :class="{ 'app__aside--collapsed': collapsed }"
    >
      <div class="app__brand">
        <div class="app__logo">H</div>
        <div v-show="!collapsed" class="app__brand-text">
          <span class="app__name">Hify</span>
          <span class="app__tagline">AI Agent Platform</span>
        </div>
      </div>
      <el-menu
        :default-active="activeMenu"
        :collapse="collapsed"
        router
        class="app__menu"
      >
        <el-menu-item index="/provider">
          <el-icon><Setting /></el-icon>
          <template #title>模型管理</template>
        </el-menu-item>
        <el-menu-item index="/agent">
          <el-icon><User /></el-icon>
          <template #title>Agent 管理</template>
        </el-menu-item>
        <el-menu-item index="/chat">
          <el-icon><ChatDotRound /></el-icon>
          <template #title>对话</template>
        </el-menu-item>
      </el-menu>
      <div class="app__footer">
        <button
          type="button"
          class="app__collapse-btn"
          :aria-label="collapsed ? '展开侧边栏' : '折叠侧边栏'"
          @click="collapsed = !collapsed"
        >
          <el-icon :size="16">
            <Expand v-if="collapsed" />
            <Fold v-else />
          </el-icon>
        </button>
        <span v-show="!collapsed" class="app__version">v{{ appVersion }}</span>
      </div>
    </el-aside>
    <el-main class="app__main">
      <router-view />
    </el-main>
  </el-container>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import {
  ChatDotRound,
  Expand,
  Fold,
  Setting,
  User,
} from '@element-plus/icons-vue'

const route = useRoute()
const activeMenu = computed(() => route.path)
const collapsed = ref(false)
const appVersion = __APP_VERSION__
</script>

<style scoped>
.app {
  height: 100vh;
}

/* ---------- 深色侧边栏（token 见 tokens.css《深色侧边栏》） ---------- */
.app__aside {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background-color: var(--hf-sidebar-bg);
  transition: width var(--hf-duration-slow) var(--hf-ease-out);
}

/* 品牌区：渐变方块 + 渐变文字 "Hify" + 副标题 */
.app__brand {
  display: flex;
  align-items: center;
  gap: var(--hf-space-3);
  height: 56px;
  padding: 0 var(--hf-space-5);
  border-bottom: 1px solid var(--hf-sidebar-border);
  flex-shrink: 0;
}

.app__aside--collapsed .app__brand {
  justify-content: center;
  padding: 0;
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
  flex-shrink: 0;
}

.app__brand-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.app__name {
  background: var(--hf-gradient-brand);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
  font-size: var(--hf-font-size-md);
  font-weight: var(--hf-font-weight-semibold);
  letter-spacing: 0.02em;
  line-height: var(--hf-leading-tight);
}

.app__tagline {
  color: var(--hf-sidebar-text-muted);
  font-size: var(--hf-font-size-xs);
  letter-spacing: 0.04em;
  line-height: var(--hf-leading-tight);
}

/* 菜单：透明底 + 三态 token（默认 / hover / active） */
.app__menu {
  flex: 1;
  padding: var(--hf-space-3) var(--hf-space-2);
  border-right: none;
  background-color: transparent;
  overflow-x: hidden;
  overflow-y: auto;
}

.app__menu.el-menu--collapse {
  width: 100%;
  padding: var(--hf-space-3) 0;
}

.app__menu :deep(.el-menu-item) {
  position: relative;
  height: 40px;
  line-height: 40px;
  margin-bottom: var(--hf-space-1);
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

/* 选中态：左侧 3px 主色竖线（圆角、垂直居中、60% 高） */
.app__menu :deep(.el-menu-item.is-active::before) {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  width: 3px;
  height: 60%;
  border-radius: var(--hf-radius-full);
  background-color: var(--hf-sidebar-indicator);
  transform: translateY(-50%);
}

/* 底部区：折叠按钮 + 版本号 */
.app__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--hf-space-3) var(--hf-space-4);
  border-top: 1px solid var(--hf-sidebar-border);
  flex-shrink: 0;
}

.app__aside--collapsed .app__footer {
  justify-content: center;
  padding: var(--hf-space-3) 0;
}

.app__collapse-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: none;
  border-radius: var(--hf-radius-sm);
  background: transparent;
  color: var(--hf-sidebar-text);
  cursor: pointer;
  transition:
    background-color var(--hf-duration-fast) var(--hf-ease-in-out),
    color var(--hf-duration-fast) var(--hf-ease-in-out);
}

.app__collapse-btn:hover {
  background-color: var(--hf-sidebar-bg-hover);
  color: var(--hf-sidebar-text-hover);
}

.app__collapse-btn:focus-visible {
  outline: 2px solid var(--hf-sidebar-text-hover);
  outline-offset: 1px;
}

.app__version {
  color: var(--hf-sidebar-text-muted);
  font-family: var(--hf-font-mono);
  font-size: var(--hf-font-size-xs);
}

/* ---------- 主内容区 ---------- */
.app__main {
  padding: var(--hf-space-6);
  background-color: var(--hf-bg-page);
}
</style>
