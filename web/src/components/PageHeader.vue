<template>
  <div class="page-header">
    <div class="page-header__text">
      <h1 class="page-header__title">{{ title }}</h1>
      <p v-if="description" class="page-header__desc">{{ description }}</p>
    </div>
    <div v-if="$slots.actions" class="page-header__actions">
      <slot name="actions" />
    </div>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  /** 页面标题（面包屑当前页另由 route.meta.title 提供，二者保持同文案） */
  title: string
  /** 一句话页面描述，可选 */
  description?: string
}>()
</script>

<style scoped>
/* 页面标题区：标题 + 描述（左）+ 操作按钮区（右）。
 * 视觉值全部引用 tokens.css，见 docs/design/design-system.md《整体布局》。 */
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--hf-space-4);
  margin-bottom: var(--hf-space-4);
}

.page-header__text {
  min-width: 0;
}

.page-header__title {
  margin: 0;
  font-size: var(--hf-font-size-lg);
  font-weight: var(--hf-font-weight-semibold);
  line-height: var(--hf-leading-tight);
  color: var(--hf-text-1);
}

.page-header__desc {
  margin: var(--hf-space-1) 0 0;
  font-size: var(--hf-font-size-base);
  line-height: var(--hf-leading-normal);
  color: var(--hf-text-2);
}

.page-header__actions {
  display: flex;
  align-items: center;
  gap: var(--hf-space-2);
  flex-shrink: 0;
}
</style>
