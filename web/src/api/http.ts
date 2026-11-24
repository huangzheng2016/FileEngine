// The one place that talks to the backend. `request` hides the transport: axios over HTTP in a browser,
// the Wails `Call` binding in the desktop window. Both return the same raw response, which is turned into
// data or a typed ApiError here, so no page ever sees an envelope or a transport detail.
import { CanceledError } from 'axios'
import { transport } from './env'
import { callDesktop } from './transportDesktop'
import { callWeb } from './transportWeb'

export type QueryValue = string | number | boolean | null | undefined
export type Query = Record<string, QueryValue>

export interface RequestOptions {
  query?: Query
  body?: unknown
  signal?: AbortSignal
  // the sign-in probe and the login itself handle a 401 on their own
  skipAuthRedirect?: boolean
}

export interface RawRequest {
  method: 'GET' | 'POST'
  path: string
  query: Record<string, string>
  // JSON text; undefined for a GET
  body?: string
  signal?: AbortSignal
  // blob keeps the bytes (downloads); json reads text
  as: 'json' | 'blob'
}

export interface RawResponse {
  status: number
  contentType: string
  disposition: string
  text: string
  blob?: Blob
}

// A failure the backend answered ({code, msg}) or a response that is not a success. `message` is the Chinese
// msg, which base/errors.ts shows as is.
export class ApiError extends Error {
  readonly name = 'ApiError'
  constructor(
    readonly status: number,
    readonly code: number,
    msg: string,
  ) {
    super(msg)
  }
}

export function isConflict(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409
}

let onUnauthorized: () => void = () => {}

export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

// The session ended: the handler sends the user to the sign-in page.
export function notifyUnauthorized() {
  onUnauthorized()
}

// Absent (undefined, null) keys are dropped; an empty string is kept because "parent_path present and empty"
// means the scan root while absent means no folder filter.
export function buildQuery(query?: Query): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(query ?? {})) {
    if (v !== undefined && v !== null) out[k] = String(v)
  }
  return out
}

function parseEnvelope(text: string): { code?: number; msg?: string } | null {
  try {
    const v: unknown = JSON.parse(text)
    return v && typeof v === 'object' ? (v as { code?: number; msg?: string }) : null
  } catch {
    return null
  }
}

async function send(method: 'GET' | 'POST', path: string, opts: RequestOptions, as: 'json' | 'blob') {
  const raw: RawRequest = {
    method,
    path,
    query: buildQuery(opts.query),
    body: method === 'POST' ? JSON.stringify(opts.body ?? {}) : undefined,
    signal: opts.signal,
    as,
  }
  if (opts.signal?.aborted) throw new CanceledError('canceled')
  const res = await (transport() === 'desktop' ? callDesktop(raw) : callWeb(raw))
  if (res.status >= 200 && res.status < 300) {
    if (as === 'blob') return res
    const env = res.text ? parseEnvelope(res.text) : {}
    if (env && !env.code) return res
    throw new ApiError(res.status, env?.code ?? 0, env?.msg || `请求失败（${res.status}）`)
  }
  // a failed download carries its JSON envelope inside the blob
  const text = res.text || (res.blob ? await res.blob.text() : '')
  const env = parseEnvelope(text)
  if (res.status === 401 && !opts.skipAuthRedirect) notifyUnauthorized()
  throw new ApiError(res.status, env?.code ?? 0, env?.msg || `请求失败（${res.status}）`)
}

export async function request<T>(method: 'GET' | 'POST', path: string, opts: RequestOptions = {}): Promise<T> {
  const res = await send(method, path, opts, 'json')
  return (res.text ? JSON.parse(res.text) : {}) as T
}

export const get = <T>(path: string, query?: Query, signal?: AbortSignal) => request<T>('GET', path, { query, signal })

export const post = <T>(path: string, body?: unknown, signal?: AbortSignal) =>
  request<T>('POST', path, { body, signal })

// The server answers every list as a (possibly null) array; pages want an array.
export const list = <T>(items: T[] | null | undefined): T[] => items ?? []

export interface Download {
  blob: Blob
  filename: string
}

export function filenameOf(disposition: string, fallback: string): string {
  const star = /filename\*=(?:UTF-8|utf-8)''([^;]+)/.exec(disposition)
  if (star) {
    try {
      return decodeURIComponent(star[1].trim())
    } catch {
      // fall through to the plain form
    }
  }
  const plain = /filename="?([^";]+)"?/.exec(disposition)
  return plain ? plain[1].trim() : fallback
}

export async function fetchBlob(
  method: 'GET' | 'POST',
  path: string,
  opts: RequestOptions = {},
  fallbackName = 'download',
): Promise<Download> {
  const res = await send(method, path, opts, 'blob')
  const blob = res.blob ?? new Blob([res.text], { type: res.contentType })
  return { blob, filename: filenameOf(res.disposition, fallbackName) }
}

// Saves a response as a file through the browser (the desktop webview downloads the same way).
export async function download(
  path: string,
  opts: RequestOptions & { method?: 'GET' | 'POST'; filename?: string } = {},
) {
  const { blob, filename } = await fetchBlob(opts.method ?? 'GET', path, opts, opts.filename)
  const href = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = href
  a.download = filename
  a.click()
  URL.revokeObjectURL(href)
}
