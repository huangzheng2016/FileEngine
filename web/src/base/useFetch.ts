import { getCurrentScope, onScopeDispose, ref, shallowRef, type Ref, type ShallowRef } from 'vue'
import { errorMessage, isAbort } from './errors'

export interface FetchState<T> {
  data: ShallowRef<T | null>
  error: Ref<string>
  loading: Ref<boolean>
  // true once any request has settled; tables use it to tell "first load" from "refresh"
  loaded: Ref<boolean>
  refresh: () => Promise<void>
  cancel: () => void
}

// A newer refresh aborts the older one, and a stale response never overwrites newer data.
export function useFetch<T>(
  fn: (signal: AbortSignal) => Promise<T>,
  opts: { immediate?: boolean; timeoutMs?: number } = {},
): FetchState<T> {
  const data = shallowRef<T | null>(null)
  const error = ref('')
  const loading = ref(false)
  const loaded = ref(false)
  let seq = 0
  let controller: AbortController | null = null

  function cancel() {
    seq++
    controller?.abort()
    controller = null
    loading.value = false
  }

  async function refresh() {
    cancel()
    const current = seq
    const ctrl = new AbortController()
    controller = ctrl
    loading.value = true
    error.value = ''
    const timer = opts.timeoutMs ? setTimeout(() => ctrl.abort(), opts.timeoutMs) : undefined
    try {
      const next = await fn(ctrl.signal)
      if (current === seq) data.value = next
    } catch (e) {
      if (current !== seq) return
      if (isAbort(e) || ctrl.signal.aborted) error.value = '请求超时'
      else error.value = errorMessage(e)
    } finally {
      clearTimeout(timer)
      if (current === seq) {
        loading.value = false
        loaded.value = true
        controller = null
      }
    }
  }

  if (getCurrentScope()) onScopeDispose(cancel)
  if (opts.immediate) void refresh()
  return { data, error, loading, loaded, refresh, cancel }
}
