import axios from 'axios'
import type { RawRequest, RawResponse } from './http'

// Every status is a normal answer here; http.ts turns failures into ApiError. Network errors, timeouts and
// aborts still throw (AxiosError / CanceledError), which base/errors.ts and isAbort understand.
const client = axios.create({ timeout: 30_000, validateStatus: () => true })

export async function callWeb(req: RawRequest): Promise<RawResponse> {
  const res = await client.request({
    method: req.method,
    url: req.path,
    params: req.query,
    data: req.body,
    headers: req.body === undefined ? undefined : { 'Content-Type': 'application/json' },
    signal: req.signal,
    responseType: req.as === 'blob' ? 'blob' : 'text',
    transformResponse: (d) => d,
  })
  const header = (name: string) => String(res.headers[name] ?? '')
  const blob = res.data instanceof Blob ? res.data : undefined
  return {
    status: res.status,
    contentType: header('content-type'),
    disposition: header('content-disposition'),
    text: blob ? '' : typeof res.data === 'string' ? res.data : '',
    blob,
  }
}
