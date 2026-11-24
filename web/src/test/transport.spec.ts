import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { CanceledError } from 'axios'
import { errorMessage, isAbort } from '@/base/errors'

const web = vi.hoisted(() => ({ callWeb: vi.fn() }))
const desktop = vi.hoisted(() => ({ callDesktop: vi.fn() }))
vi.mock('@/api/transportWeb', () => web)
vi.mock('@/api/transportDesktop', () => desktop)

import {
  ApiError,
  buildQuery,
  fetchBlob,
  filenameOf,
  isConflict,
  request,
  setUnauthorizedHandler,
  type RawRequest,
  type RawResponse,
} from '@/api/http'

const ok = (body: unknown, extra: Partial<RawResponse> = {}): RawResponse => ({
  status: 200,
  contentType: 'application/json',
  disposition: '',
  text: JSON.stringify(body),
  ...extra,
})
const fail = (status: number, body: unknown): RawResponse => ok(body, { status })

beforeEach(() => {
  window.__FILEENGINE_TRANSPORT__ = 'web'
  setUnauthorizedHandler(() => {})
})
afterEach(() => {
  delete window.__FILEENGINE_TRANSPORT__
  vi.clearAllMocks()
})

describe('query building', () => {
  it('drops absent keys, keeps empty strings and stringifies numbers and booleans', () => {
    expect(buildQuery({ a: undefined, b: null, c: '', d: 0, e: false, f: 'x' })).toEqual({
      c: '',
      d: '0',
      e: 'false',
      f: 'x',
    })
    expect(buildQuery()).toEqual({})
  })
})

describe('request', () => {
  it('sends GET reads with a query and no body, on the web transport', async () => {
    web.callWeb.mockResolvedValue(ok({ code: 0, msg: '', items: [] }))
    const r = await request<{ items: unknown[] }>('GET', '/api/file/list', {
      query: { session_id: '7', parent_path: '' },
    })
    expect(r.items).toEqual([])
    const sent = web.callWeb.mock.calls[0][0] as RawRequest
    expect(sent).toMatchObject({
      method: 'GET',
      path: '/api/file/list',
      query: { session_id: '7', parent_path: '' },
      as: 'json',
    })
    expect(sent.body).toBeUndefined()
    expect(desktop.callDesktop).not.toHaveBeenCalled()
  })

  it('sends POST writes as JSON text, an empty object when there is no body', async () => {
    web.callWeb.mockResolvedValue(ok({ code: 0 }))
    await request('POST', '/api/session/delete', { body: { id: '9007199254740993' } })
    await request('POST', '/api/run/stop')
    expect((web.callWeb.mock.calls[0][0] as RawRequest).body).toBe('{"id":"9007199254740993"}')
    expect((web.callWeb.mock.calls[1][0] as RawRequest).body).toBe('{}')
  })

  it('uses the desktop binding when the window is a Wails window', async () => {
    window.__FILEENGINE_TRANSPORT__ = 'desktop'
    desktop.callDesktop.mockResolvedValue(ok({ code: 0, item: { id: '1' } }))
    const r = await request<{ item: { id: string } }>('GET', '/api/session/get', { query: { id: '1' } })
    expect(r.item.id).toBe('1')
    expect(web.callWeb).not.toHaveBeenCalled()
  })

  it('turns a {code,msg} failure into an ApiError carrying the Chinese message', async () => {
    web.callWeb.mockResolvedValue(fail(409, { code: 409, msg: '方案已被修改，请刷新后重试' }))
    const e = await request('POST', '/api/plan/update', { body: {} }).catch((x: unknown) => x)
    expect(e).toBeInstanceOf(ApiError)
    expect(e).toMatchObject({ status: 409, code: 409, message: '方案已被修改，请刷新后重试' })
    expect(isConflict(e)).toBe(true)
    expect(errorMessage(e)).toBe('方案已被修改，请刷新后重试')
  })

  it('treats a non-zero code on a 200 as a failure too', async () => {
    web.callWeb.mockResolvedValue(ok({ code: 1001, msg: '存储不存在' }))
    await expect(request('GET', '/api/filesystem/get')).rejects.toMatchObject({ code: 1001, message: '存储不存在' })
  })

  it('falls back to a Chinese status text when the body is not an envelope', async () => {
    web.callWeb.mockResolvedValue({ ...ok({}), status: 502, text: '<html>bad gateway</html>' })
    await expect(request('GET', '/api/session/list')).rejects.toMatchObject({ status: 502, message: '请求失败（502）' })
  })

  it('signs the user out on 401 unless the call handles it', async () => {
    const out = vi.fn()
    setUnauthorizedHandler(out)
    web.callWeb.mockResolvedValue(fail(401, { code: 401, msg: '请先登录' }))
    await expect(request('GET', '/api/session/list')).rejects.toBeInstanceOf(ApiError)
    expect(out).toHaveBeenCalledTimes(1)
    await expect(request('GET', '/api/auth/me', { skipAuthRedirect: true })).rejects.toBeInstanceOf(ApiError)
    expect(out).toHaveBeenCalledTimes(1)
  })

  it('does not call the transport for an already aborted signal', async () => {
    const ctrl = new AbortController()
    ctrl.abort()
    const e = await request('GET', '/api/session/list', { signal: ctrl.signal }).catch((x: unknown) => x)
    expect(isAbort(e)).toBe(true)
    expect(web.callWeb).not.toHaveBeenCalled()
  })

  it('lets a transport abort through as a CanceledError', async () => {
    web.callWeb.mockRejectedValue(new CanceledError('canceled'))
    expect(isAbort(await request('GET', '/api/x').catch((x: unknown) => x))).toBe(true)
  })
})

describe('downloads', () => {
  it('returns the blob and the filename from Content-Disposition', async () => {
    const blob = new Blob(['a,b'], { type: 'text/csv' })
    web.callWeb.mockResolvedValue({
      status: 200,
      contentType: 'text/csv',
      disposition: `attachment; filename="plan.csv"; filename*=UTF-8''%E7%A7%BB%E5%8A%A8%E6%B8%85%E5%8D%95.csv`,
      text: '',
      blob,
    })
    const d = await fetchBlob('GET', '/api/plan/ops_export', { query: { session_id: '7' } })
    expect(d.filename).toBe('移动清单.csv')
    expect(d.blob).toBe(blob)
    expect((web.callWeb.mock.calls[0][0] as RawRequest).as).toBe('blob')
  })

  it('reads the envelope out of a failed download blob', async () => {
    web.callWeb.mockResolvedValue({
      status: 404,
      contentType: 'application/json',
      disposition: '',
      text: '',
      blob: new Blob([JSON.stringify({ code: 404, msg: '文件不存在' })]),
    })
    await expect(fetchBlob('GET', '/api/file/content', { query: { id: '1' } })).rejects.toMatchObject({
      status: 404,
      message: '文件不存在',
    })
  })

  it('parses the plain and the extended filename forms', () => {
    expect(filenameOf('attachment; filename=a.txt', 'x')).toBe('a.txt')
    expect(filenameOf('', 'fallback.bin')).toBe('fallback.bin')
  })
})
