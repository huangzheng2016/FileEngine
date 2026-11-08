import { ref } from 'vue'
import { errorMessage } from './errors'

export type ToastKind = 'success' | 'error'

export interface Toast {
  id: number
  kind: ToastKind
  text: string
}

const DURATION: Record<ToastKind, number> = { success: 3000, error: 6000 }

export const toasts = ref<Toast[]>([])
const timers = new Map<number, ReturnType<typeof setTimeout>>()
let nextId = 1

export function dismissToast(id: number) {
  clearTimeout(timers.get(id))
  timers.delete(id)
  toasts.value = toasts.value.filter((t) => t.id !== id)
}

function push(kind: ToastKind, text: string) {
  const same = toasts.value.find((t) => t.kind === kind && t.text === text)
  const id = same?.id ?? nextId++
  if (!same) toasts.value = [...toasts.value.slice(-3), { id, kind, text }]
  clearTimeout(timers.get(id))
  timers.set(
    id,
    setTimeout(() => dismissToast(id), DURATION[kind]),
  )
}

export const toast = {
  success: (text: string) => push('success', text),
  // toast.error('保存失败', e) renders "保存失败：<backend msg>"
  error: (action: string, cause?: unknown) =>
    push('error', cause === undefined ? action : `${action}：${errorMessage(cause)}`),
}
