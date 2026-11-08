import { getCurrentScope, onScopeDispose, ref } from 'vue'
import { errorMessage, isAbort } from './errors'

export interface UploadItem {
  id: number
  file: File
  // 0..1
  progress: number
  status: 'waiting' | 'uploading' | 'done' | 'failed' | 'canceled'
  error: string
}

export type Sender<T> = (file: File, opts: { signal: AbortSignal; onProgress: (ratio: number) => void }) => Promise<T>

// Upload queue: a few files at a time, each with progress, cancel and retry.
// Finished items leave the queue after a moment; failed ones stay until retried
// or removed, so a failure is never missed.
export function useUpload<T>(send: Sender<T>, opts: { concurrency?: number; onDone?: (result: T) => void } = {}) {
  const items = ref<UploadItem[]>([])
  const controllers = new Map<number, AbortController>()
  let nextId = 1

  const update = (id: number, patch: Partial<UploadItem>) => {
    items.value = items.value.map((it) => (it.id === id ? { ...it, ...patch } : it))
  }

  function pump() {
    const busy = items.value.filter((i) => i.status === 'uploading').length
    const next = items.value.filter((i) => i.status === 'waiting').slice(0, (opts.concurrency ?? 2) - busy)
    for (const it of next) void start(it)
  }

  async function start(it: UploadItem) {
    const ctrl = new AbortController()
    controllers.set(it.id, ctrl)
    update(it.id, { status: 'uploading', progress: 0, error: '' })
    try {
      const result = await send(it.file, { signal: ctrl.signal, onProgress: (p) => update(it.id, { progress: p }) })
      update(it.id, { status: 'done', progress: 1 })
      opts.onDone?.(result)
      setTimeout(() => remove(it.id), 1500)
    } catch (e) {
      if (isAbort(e) || ctrl.signal.aborted) update(it.id, { status: 'canceled' })
      else update(it.id, { status: 'failed', error: errorMessage(e) })
    } finally {
      controllers.delete(it.id)
      pump()
    }
  }

  function add(files: File[]) {
    items.value = [
      ...items.value,
      ...files.map((file) => ({ id: nextId++, file, progress: 0, status: 'waiting' as const, error: '' })),
    ]
    pump()
  }

  function cancel(id: number) {
    controllers.get(id)?.abort()
    if (items.value.find((i) => i.id === id)?.status === 'waiting') update(id, { status: 'canceled' })
  }

  function retry(id: number) {
    update(id, { status: 'waiting', progress: 0, error: '' })
    pump()
  }

  function remove(id: number) {
    controllers.get(id)?.abort()
    items.value = items.value.filter((i) => i.id !== id)
  }

  if (getCurrentScope()) onScopeDispose(() => controllers.forEach((c) => c.abort()))
  return { items, add, cancel, retry, remove }
}
