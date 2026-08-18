import { ElMessage } from 'element-plus'

/** 统一通知封装：业务代码一律走这里，duration / 样式一处配置。
 * 请求失败的错误提示由 utils/request.ts 拦截器统一弹，
 * notifyError 留给业务层显式场景（如本地校验失败、非请求错误）。 */

const DURATION = 3000

export function notifySuccess(message: string): void {
  ElMessage({ type: 'success', message, duration: DURATION })
}

export function notifyError(message: string): void {
  ElMessage({ type: 'error', message, duration: DURATION })
}

export function notifyWarning(message: string): void {
  ElMessage({ type: 'warning', message, duration: DURATION })
}
