import { reactive } from 'vue'
import { errorMessage } from '@/base/errors'

export interface BrowseDir {
  name: string
  path: string
}

export interface BrowseResult {
  success: boolean
  error?: string
  detail?: string
  dirs: BrowseDir[]
}

// Folder browsing over any loader that lists the folders at a path (a connection test, a directory listing).
// Opening a path aborts the previous request, and a response that arrives after a newer open is dropped.
// `onResult` sees every accepted response, for what the caller needs besides the folders (a host key, the
// roots of a local machine).
export function useFolderBrowser<R extends BrowseResult>(
  load: (path: string, signal: AbortSignal) => Promise<R>,
  onResult?: (r: R, path: string) => void,
) {
  const state = reactive({
    path: '',
    dirs: [] as BrowseDir[],
    loading: false,
    ok: false,
    error: '',
    detail: '',
  })
  let ctl: AbortController | null = null

  async function open(path: string): Promise<boolean> {
    ctl?.abort()
    const mine = (ctl = new AbortController())
    Object.assign(state, { path, loading: true, error: '', detail: '' })
    try {
      const r = await load(path, mine.signal)
      if (mine.signal.aborted) return false
      Object.assign(state, { dirs: r.dirs, ok: r.success, error: r.error ?? '', detail: r.detail ?? '' })
      onResult?.(r, path)
      return r.success
    } catch (e) {
      if (mine.signal.aborted) return false
      Object.assign(state, { ok: false, dirs: [], error: errorMessage(e), detail: '' })
      return false
    } finally {
      if (ctl === mine) state.loading = false
    }
  }

  function abort() {
    ctl?.abort()
    ctl = null
    state.loading = false
  }

  function reset() {
    abort()
    Object.assign(state, { path: '', dirs: [], ok: false, error: '', detail: '' })
  }

  return { state, open, abort, reset }
}

export type FolderBrowser = ReturnType<typeof useFolderBrowser>
