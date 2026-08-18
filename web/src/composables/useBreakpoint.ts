import { computed, readonly, ref, type ComputedRef, type Ref } from 'vue'

/** 断点表：与 docs/design/design-system.md《响应式断点》保持同步，值只增不改。
 * CSS 变量不能用于 @media（CSS 规范限制），断点做不成 --hf-* token，
 * 此常量即代码侧唯一事实源。 */
export const BREAKPOINTS = {
  /** ≤1200：侧边栏折叠为 64px 图标模式 */
  lg: 1200,
  /** ≤992：表格次要列（hideBelow 标记）隐藏 */
  md: 992,
} as const

// 模块级单例：一个 passive resize 监听，App.vue 与所有 HifyTable 实例共享；
// 与页面同生命周期，不随组件卸载移除
const viewportWidth = ref(window.innerWidth)
window.addEventListener(
  'resize',
  () => {
    viewportWidth.value = window.innerWidth
  },
  { passive: true },
)

const isNarrow = computed(() => viewportWidth.value <= BREAKPOINTS.lg)
const isCompact = computed(() => viewportWidth.value <= BREAKPOINTS.md)

/** 响应式断点：isNarrow（≤1200，侧边栏折叠档）/ isCompact（≤992，次要列隐藏档） */
export function useBreakpoint(): {
  width: Readonly<Ref<number>>
  isNarrow: ComputedRef<boolean>
  isCompact: ComputedRef<boolean>
} {
  return { width: readonly(viewportWidth), isNarrow, isCompact }
}
