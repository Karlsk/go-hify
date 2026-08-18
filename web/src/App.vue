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
          <template #title>提供商管理</template>
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
    <el-container class="app__body" direction="vertical">
      <header class="app__header">
        <el-breadcrumb separator="/">
          <el-breadcrumb-item :to="{ path: '/' }">首页</el-breadcrumb-item>
          <el-breadcrumb-item>{{ pageTitle }}</el-breadcrumb-item>
        </el-breadcrumb>
        <!-- auth 接入前为占位；接入后换成真实用户（头像取用户名首字母） -->
        <div class="app__user">
          <div class="app__avatar" aria-hidden="true">{{ avatarLetter }}</div>
          <span class="app__username">{{ username }}</span>
        </div>
      </header>
      <el-main class="app__main">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import {
  ChatDotRound,
  Expand,
  Fold,
  Setting,
  User,
} from '@element-plus/icons-vue'
import { useBreakpoint } from '@/composables/useBreakpoint'

const route = useRoute()
const activeMenu = computed(() => route.path)

// 窄屏（≤1200）自动折叠：加载时定档；跨界瞬间自动同步，两次跨界之间手动自由
const { isNarrow } = useBreakpoint()
const collapsed = ref(isNarrow.value)
watch(isNarrow, (narrow) => {
  collapsed.value = narrow
})
const appVersion = __APP_VERSION__

// 面包屑当前项 = 路由 meta.title（router/index.ts 为唯一来源）
const pageTitle = computed(() => route.meta.title)

// auth 未接入的占位身份；接入后由 authctx/store 提供
const username = ref('Admin')
const avatarLetter = computed(() => username.value.charAt(0).toUpperCase())
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

/* ---------- 顶栏（面包屑 + 用户区） ---------- */
.app__body {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

/* 56px 与侧边栏品牌区等高，顶部一条线对齐；
 * 白底 + 底部浅分割线，与浅灰页面底、白卡片形成三层层次 */
.app__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--hf-space-4);
  height: 56px;
  padding: 0 var(--hf-space-6);
  background-color: var(--hf-bg-container);
  border-bottom: 1px solid var(--hf-border-2);
  flex-shrink: 0;
}

.app__user {
  display: flex;
  align-items: center;
  gap: var(--hf-space-2);
}

.app__avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--hf-radius-full);
  background: var(--hf-gradient-brand);
  color: var(--hf-text-inverse);
  font-size: var(--hf-font-size-xs);
  font-weight: var(--hf-font-weight-semibold);
  flex-shrink: 0;
}

.app__username {
  font-size: var(--hf-font-size-base);
  color: var(--hf-text-2);
}

/* ---------- 主内容区 ---------- */
.app__main {
  padding: var(--hf-space-6);
  background-color: var(--hf-bg-page);
}
</style>
