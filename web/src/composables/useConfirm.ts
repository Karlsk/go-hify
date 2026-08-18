import { ElMessageBox } from 'element-plus'
import { notifySuccess } from '@/utils/notify'

export interface UseConfirmOptions {
  /** 确认框正文，如「删除提供商 "OpenAI"？」 */
  message: string
  /** 确认后执行的业务调用（如删除 API） */
  api: () => Promise<unknown>
  title?: string
  confirmText?: string
  successText?: string
}

/** 删除确认全流程：调用即执行。
 * 确认框（红色确认按钮，对齐设计系统危险操作）→ 调 api → 成功提示。
 * resolve(true) = 已成功；resolve(false) = 用户取消（静默）；api 失败 reject
 *（失败提示由 request.ts 拦截器统一弹，不重复）。 */
export function useConfirm(options: UseConfirmOptions): Promise<boolean> {
  return ElMessageBox.confirm(options.message, options.title ?? '删除确认', {
    type: 'warning',
    confirmButtonText: options.confirmText ?? '删除',
    cancelButtonText: '取消',
    // confirmButtonType 换 type（而非 confirmButtonClass 叠 class）：
    // 叠 class 会与默认 el-button--primary 并存，被主按钮渐变覆写刷成蓝色
    confirmButtonType: 'danger',
  }).then(
    () =>
      options.api().then(() => {
        notifySuccess(options.successText ?? '删除成功')
        return true
      }),
    (reason: unknown) => {
      // ElMessageBox 取消 / 关闭：静默返回 false，不向上抛
      if (reason === 'cancel' || reason === 'close') return false
      throw reason
    },
  )
}
