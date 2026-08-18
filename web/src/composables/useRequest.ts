import { ref, type Ref } from 'vue'

export interface UseRequestOptions<T> {
  /** 组合时立即执行一次（无参调用 execute） */
  immediate?: boolean
  /** data 初始值，缺省 null */
  defaultData?: T
}

export interface UseRequestReturn<T, Args extends unknown[]> {
  data: Ref<T | null>
  loading: Ref<boolean>
  /** 最近一次失败原因；错误提示已由 request.ts 拦截器统一弹，这里只记状态 */
  error: Ref<unknown>
  execute: (...args: Args) => Promise<T | null>
}

/** 请求三态管理：收敛 try/catch/finally 样板。
 * execute 成功返回数据并写入 data；失败返回 null 并写入 error（不重复弹提示）。 */
export function useRequest<T, Args extends unknown[] = []>(
  api: (...args: Args) => Promise<T>,
  options: UseRequestOptions<T> = {},
): UseRequestReturn<T, Args> {
  const data = ref(options.defaultData ?? null) as Ref<T | null>
  const loading = ref(false)
  const error = ref<unknown>(null)

  async function execute(...args: Args): Promise<T | null> {
    loading.value = true
    error.value = null
    try {
      const result = await api(...args)
      data.value = result
      return result
    } catch (e) {
      error.value = e
      return null
    } finally {
      loading.value = false
    }
  }

  if (options.immediate) void execute(...([] as unknown as Args))

  return { data, loading, error, execute }
}
