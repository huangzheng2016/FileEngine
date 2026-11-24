import { CanceledError } from 'axios'
import type { RawRequest, RawResponse } from './http'

// The generated binding is loaded on first use, so the browser build never evaluates the Wails runtime.
const bindings = () => import('@bindings/FileEngine/cmd/fileengine-desktop/service')

export function decodeBase64(text: string): Uint8Array<ArrayBuffer> {
  const bin = atob(text)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return bytes
}

export async function callDesktop(req: RawRequest): Promise<RawResponse> {
  const { Call } = await bindings()
  const pending = Call({ method: req.method, path: req.path, query: req.query, body: req.body })
  const onAbort = () => pending.cancel()
  req.signal?.addEventListener('abort', onAbort, { once: true })
  // aborted while the binding was loading
  if (req.signal?.aborted) onAbort()
  try {
    const res = await pending
    const contentType = res.content_type
    const base = { status: res.status, contentType, disposition: res.content_disposition ?? '' }
    if (res.encoding === 'base64') {
      const blob = new Blob([decodeBase64(res.body)], { type: contentType })
      return { ...base, text: '', blob }
    }
    return {
      ...base,
      text: res.body,
      blob: req.as === 'blob' ? new Blob([res.body], { type: contentType }) : undefined,
    }
  } catch (e) {
    if (req.signal?.aborted) throw new CanceledError('canceled')
    throw e
  } finally {
    req.signal?.removeEventListener('abort', onAbort)
  }
}
