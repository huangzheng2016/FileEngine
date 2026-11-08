import { reactive } from 'vue'

export interface ConfirmOptions {
  title?: string
  message: string
  confirmText?: string
  danger?: boolean
}

export const confirmState = reactive({
  open: false,
  title: '',
  message: '',
  confirmText: '',
  danger: false,
  resolve: null as ((ok: boolean) => void) | null,
})

export function resolveConfirm(ok: boolean) {
  const resolve = confirmState.resolve
  confirmState.resolve = null
  confirmState.open = false
  resolve?.(ok)
}

// const confirm = useConfirm()
// if (!(await confirm({ message: `删除「${row.name}」后不可恢复。`, confirmText: '删除', danger: true }))) return
export function useConfirm() {
  return (opts: ConfirmOptions): Promise<boolean> => {
    resolveConfirm(false)
    Object.assign(confirmState, {
      open: true,
      title: opts.title ?? (opts.danger ? '确认删除' : '确认'),
      message: opts.message,
      confirmText: opts.confirmText ?? '确定',
      danger: !!opts.danger,
    })
    return new Promise((resolve) => {
      confirmState.resolve = resolve
    })
  }
}
