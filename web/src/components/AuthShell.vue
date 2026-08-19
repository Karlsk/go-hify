<template>
  <!-- 登录 / 注册共用分栏壳：左品牌面板（深底，复刻应用侧边栏语言）+ 右表单卡片区。
       ≤992（isCompact）隐藏品牌面板，表单全宽——复用既有断点，不新增 token。 -->
  <div class="auth">
    <aside v-if="!isCompact" class="auth__brand">
      <div class="auth__brand-block">
        <div class="auth__logo">H</div>
        <div class="auth__name">Hify</div>
        <div class="auth__tagline">AI Agent Platform</div>
      </div>
      <div class="auth__version">v{{ appVersion }}</div>
    </aside>
    <main class="auth__main">
      <div class="auth__card">
        <slot />
      </div>
    </main>
  </div>
</template>

<script setup lang="ts">
import { useBreakpoint } from '@/composables/useBreakpoint'

const { isCompact } = useBreakpoint()
const appVersion = __APP_VERSION__
</script>

<style scoped>
.auth {
  display: flex;
  height: 100vh;
}

/* ---------- 左：品牌面板（复用应用侧边栏深底语言，登录后视觉无缝延续） ---------- */
.auth__brand {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 42%;
  flex-shrink: 0;
  background-color: var(--hf-sidebar-bg);
}

.auth__brand-block {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--hf-space-3);
}

.auth__logo {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  border-radius: var(--hf-radius-md);
  background: var(--hf-gradient-brand);
  color: var(--hf-text-inverse);
  font-size: var(--hf-font-size-xl);
  font-weight: var(--hf-font-weight-semibold);
}

.auth__name {
  background: var(--hf-gradient-brand);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
  font-size: var(--hf-font-size-2xl);
  font-weight: var(--hf-font-weight-semibold);
  letter-spacing: 0.02em;
  line-height: var(--hf-leading-tight);
}

.auth__tagline {
  color: var(--hf-sidebar-text-muted);
  font-size: var(--hf-font-size-sm);
  letter-spacing: 0.04em;
}

.auth__version {
  position: absolute;
  left: var(--hf-space-5);
  bottom: var(--hf-space-4);
  color: var(--hf-sidebar-text-muted);
  font-family: var(--hf-font-mono);
  font-size: var(--hf-font-size-xs);
}

/* ---------- 右：表单区（页面底 + 居中白卡片，radius-xl 即「登录卡片」token） ---------- */
.auth__main {
  display: flex;
  flex: 1;
  align-items: center;
  justify-content: center;
  padding: var(--hf-space-6);
  background-color: var(--hf-bg-page);
}

.auth__card {
  width: 400px;
  max-width: 100%;
  padding: var(--hf-space-10) var(--hf-space-8);
  background-color: var(--hf-bg-container);
  border: 1px solid var(--hf-border-2);
  border-radius: var(--hf-radius-xl);
  box-shadow: var(--hf-shadow-lg);
}
</style>
