import { describe, expect, it, vi } from 'vitest'
import { useFolderBrowser, type BrowseResult } from '@/lib/useFolderBrowser'

const ok = (...names: string[]): BrowseResult => ({
  success: true,
  dirs: names.map((n) => ({ name: n, path: n })),
})

// A loader whose responses are released by hand, so the order of arrival is under the test's control.
function manual() {
  const pending: {
    path: string
    signal: AbortSignal
    resolve: (r: BrowseResult) => void
    reject: (e: unknown) => void
  }[] = []
  const load = vi.fn(
    (path: string, signal: AbortSignal) =>
      new Promise<BrowseResult>((resolve, reject) => pending.push({ path, signal, resolve, reject })),
  )
  return { load, pending }
}

describe('useFolderBrowser', () => {
  it('shows the folders of an opened path', async () => {
    const b = useFolderBrowser(async () => ok('a', 'b'))
    expect(await b.open('x')).toBe(true)
    expect(b.state).toMatchObject({ path: 'x', ok: true, loading: false, error: '' })
    expect(b.state.dirs.map((d) => d.name)).toEqual(['a', 'b'])
  })

  it('keeps the reason of a failed listing', async () => {
    const b = useFolderBrowser(async () => ({ success: false, error: '没有权限', detail: 'EACCES', dirs: [] }))
    expect(await b.open('x')).toBe(false)
    expect(b.state).toMatchObject({ ok: false, error: '没有权限', detail: 'EACCES', loading: false })
  })

  it('turns a thrown request into an error', async () => {
    const b = useFolderBrowser(async () => {
      throw new Error('boom')
    })
    expect(await b.open('x')).toBe(false)
    expect(b.state.ok).toBe(false)
    expect(b.state.error).not.toBe('')
  })

  it('aborts the previous request and drops its late answer', async () => {
    const { load, pending } = manual()
    const b = useFolderBrowser(load)
    const first = b.open('one')
    const second = b.open('two')
    expect(pending[0].signal.aborted).toBe(true)
    expect(pending[1].signal.aborted).toBe(false)
    pending[1].resolve(ok('two-child'))
    expect(await second).toBe(true)
    pending[0].resolve(ok('one-child'))
    expect(await first).toBe(false)
    expect(b.state.path).toBe('two')
    expect(b.state.dirs.map((d) => d.name)).toEqual(['two-child'])
    expect(b.state.loading).toBe(false)
  })

  it('ignores the failure of an aborted request', async () => {
    const { load, pending } = manual()
    const b = useFolderBrowser(load)
    const first = b.open('one')
    const second = b.open('two')
    pending[0].reject(new Error('aborted'))
    expect(await first).toBe(false)
    expect(b.state.error).toBe('')
    pending[1].resolve(ok())
    await second
    expect(b.state.ok).toBe(true)
  })

  it('stops loading when aborted and forgets everything on reset', async () => {
    const { load, pending } = manual()
    const b = useFolderBrowser(load)
    const p = b.open('one')
    expect(b.state.loading).toBe(true)
    b.abort()
    expect(b.state.loading).toBe(false)
    pending[0].resolve(ok('late'))
    expect(await p).toBe(false)
    expect(b.state.dirs).toEqual([])
    const done = useFolderBrowser(async () => ok('a'))
    await done.open('x')
    done.reset()
    expect(done.state).toMatchObject({ path: '', dirs: [], ok: false, loading: false })
  })

  it('hands every accepted answer and its path to the caller', async () => {
    const seen: string[] = []
    const b = useFolderBrowser(
      async (path) => ({ ...ok('a'), extra: path }),
      (r, path) => seen.push(`${path}:${r.extra}`),
    )
    await b.open('p')
    expect(seen).toEqual(['p:p'])
  })
})
