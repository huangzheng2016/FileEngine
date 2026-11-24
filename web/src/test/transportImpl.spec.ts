import { afterEach, describe, expect, it, vi } from 'vitest'
import { isAbort } from '@/base/errors'

const axiosMock = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('axios', async (importOriginal) => {
  const actual = await importOriginal<typeof import('axios')>()
  return { ...actual, default: { ...actual.default, create: () => axiosMock } }
})

const bindings = vi.hoisted(() => ({ Call: vi.fn() }))
vi.mock('@bindings/FileEngine/cmd/fileengine-desktop/service', () => bindings)

import { callDesktop, decodeBase64 } from '@/api/transportDesktop'
import { callWeb } from '@/api/transportWeb'
import type { RawRequest } from '@/api/http'

const req = (o: Partial<RawRequest> = {}): RawRequest => ({
  method: 'GET',
  path: '/api/x',
  query: {},
  as: 'json',
  ...o,
})

afterEach(() => vi.clearAllMocks())

describe('web transport', () => {
  it('passes the query as params, a body as JSON, and returns every status as a response', async () => {
    axiosMock.request.mockResolvedValue({
      status: 409,
      data: '{"code":409,"msg":"冲突"}',
      headers: { 'content-type': 'application/json', 'content-disposition': 'attachment; filename=a' },
    })
    const r = await callWeb(req({ method: 'POST', query: { a: '1' }, body: '{"x":1}' }))
    expect(axiosMock.request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'POST',
        url: '/api/x',
        params: { a: '1' },
        data: '{"x":1}',
        headers: { 'Content-Type': 'application/json' },
        responseType: 'text',
      }),
    )
    expect(r).toMatchObject({ status: 409, text: '{"code":409,"msg":"冲突"}', disposition: 'attachment; filename=a' })
  })

  it('keeps the bytes of a download as a blob', async () => {
    const blob = new Blob(['x'])
    axiosMock.request.mockResolvedValue({ status: 200, data: blob, headers: {} })
    const r = await callWeb(req({ as: 'blob' }))
    expect(axiosMock.request.mock.calls[0][0]).toMatchObject({ responseType: 'blob' })
    expect(r.blob).toBe(blob)
  })
})

describe('desktop transport', () => {
  const pending = <T>(value: T) => Object.assign(Promise.resolve(value), { cancel: vi.fn() })

  it('maps the request onto Service.Call and reads a utf8 body as text', async () => {
    bindings.Call.mockReturnValue(
      pending({ status: 200, content_type: 'application/json', encoding: 'utf8', body: '{"code":0}' }),
    )
    const r = await callDesktop(req({ method: 'POST', path: '/api/run/stop', query: { q: '1' }, body: '{}' }))
    expect(bindings.Call).toHaveBeenCalledWith({ method: 'POST', path: '/api/run/stop', query: { q: '1' }, body: '{}' })
    expect(r).toMatchObject({ status: 200, text: '{"code":0}', disposition: '' })
  })

  it('decodes a base64 download into a blob with its type and filename header', async () => {
    bindings.Call.mockReturnValue(
      pending({
        status: 200,
        content_type: 'text/csv',
        content_disposition: 'attachment; filename="a.csv"',
        encoding: 'base64',
        body: btoa('a,b\n'),
      }),
    )
    const r = await callDesktop(req({ as: 'blob' }))
    expect(r.blob?.type).toBe('text/csv')
    expect(await r.blob!.text()).toBe('a,b\n')
    expect(r.disposition).toContain('a.csv')
  })

  it('round-trips arbitrary bytes through base64', () => {
    const bytes = Uint8Array.from([0, 1, 127, 128, 255])
    expect([...decodeBase64(btoa(String.fromCharCode(...bytes)))]).toEqual([...bytes])
  })

  it('cancels the call and rejects as an abort when the signal fires', async () => {
    let reject!: (e: Error) => void
    const call = Object.assign(
      new Promise((_, r) => {
        reject = r
      }),
      { cancel: vi.fn(() => reject(new Error('cancelled'))) },
    )
    bindings.Call.mockReturnValue(call)
    const ctrl = new AbortController()
    const p = callDesktop(req({ signal: ctrl.signal }))
    await vi.waitFor(() => expect(bindings.Call).toHaveBeenCalled())
    ctrl.abort()
    const e = await p.catch((x: unknown) => x)
    expect(call.cancel).toHaveBeenCalled()
    expect(isAbort(e)).toBe(true)
  })
})
