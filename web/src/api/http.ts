import axios, { type AxiosError } from 'axios'
import { HttpError, readEventStream, type SseEvent } from '@/base/sse'

export const API = '/api/v1'

export const http = axios.create({ baseURL: API, timeout: 30_000 })

let onUnauthorized: () => void = () => {}

export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

declare module 'axios' {
  interface AxiosRequestConfig {
    // the sign-in probe and the login itself handle 401 on their own
    skipAuthRedirect?: boolean
  }
}

// The backend answers failures as `{ error: "..." }`; base errorMessage reads `msg`.
http.interceptors.response.use(undefined, (error: AxiosError<{ error?: string; msg?: string }>) => {
  const data = error.response?.data
  if (data && typeof data === 'object' && data.error && !data.msg) data.msg = data.error
  if (error.response?.status === 401 && !error.config?.skipAuthRedirect) onUnauthorized()
  throw error
})

export async function login(token: string) {
  await http.post('/auth/login', { token }, { skipAuthRedirect: true })
}

// Only a 401 means signed out; any other failure surfaces on the page that loads data.
export async function authed(): Promise<boolean> {
  try {
    await http.get('/auth/me', { skipAuthRedirect: true })
    return true
  } catch (e) {
    return (e as AxiosError).response?.status !== 401
  }
}

export function isConflict(e: unknown): boolean {
  return (e as AxiosError | undefined)?.response?.status === 409
}

export function isNotFound(e: unknown): boolean {
  return (e as AxiosError | undefined)?.response?.status === 404
}

// Go zero times and missing values become 0, which time.ts renders as "—".
export function toSec(iso?: string | null): number {
  if (!iso) return 0
  const ms = Date.parse(iso)
  return Number.isFinite(ms) && ms > 0 ? Math.floor(ms / 1000) : 0
}

export async function download(url: string, filename: string) {
  const { data } = await http.get<Blob>(url, { responseType: 'blob' })
  const href = URL.createObjectURL(data)
  const a = document.createElement('a')
  a.href = href
  a.download = filename
  a.click()
  URL.revokeObjectURL(href)
}

// Follows an SSE stream until aborted; reconnects resume with Last-Event-ID.
export function followStream(path: string, onEvent: (e: SseEvent) => void, signal: AbortSignal) {
  return readEventStream(`${API}${path}`, {
    onEvent,
    signal,
    maxRetries: 8,
    onError: (err) => {
      if (!(err instanceof HttpError)) return true
      if (err.status === 401) onUnauthorized()
      return err.status >= 500
    },
  })
}
