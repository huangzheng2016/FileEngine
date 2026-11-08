// Prefer the backend envelope message ({ code, msg }) over transport text like
// "Request failed with status code 500".
export function errorMessage(error: unknown): string {
  if (error && typeof error === 'object') {
    const e = error as { response?: { data?: { msg?: unknown } }; code?: string; message?: unknown }
    const msg = e.response?.data?.msg
    if (typeof msg === 'string' && msg) return msg
    if (e.code === 'ECONNABORTED' || e.code === 'ETIMEDOUT') return '请求超时'
    if (e.code === 'ERR_NETWORK') return '网络连接失败'
    // fetch() (event streams) rejects with an English TypeError when the network is down
    if (error instanceof TypeError && /fetch|network/i.test(String(e.message))) return '网络连接失败'
    if (typeof e.message === 'string' && e.message) return stripTransport(e.message)
  }
  if (typeof error === 'string') return stripTransport(error)
  return '未知错误'
}

export function isAbort(error: unknown): boolean {
  return !!error && typeof error === 'object' && (error as { name?: string }).name === 'CanceledError'
}

// "rpc error: code = InvalidArgument desc = 端口无效" -> "端口无效"
function stripTransport(msg: string): string {
  return msg.replace(/^rpc error: code = \w+ desc = /, '')
}
