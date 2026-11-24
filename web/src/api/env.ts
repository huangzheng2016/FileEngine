export type Transport = 'web' | 'desktop'

declare global {
  interface Window {
    // Wails injects its runtime (window._wails.invoke) into every page the desktop serves
    _wails?: { invoke?: unknown }
    // forces a transport (tests, embedding)
    __FILEENGINE_TRANSPORT__?: Transport
  }
}

// Decided per call, never cached: it is a plain property read, and tests switch it.
export function transport(): Transport {
  if (typeof window === 'undefined') return 'web'
  const forced = window.__FILEENGINE_TRANSPORT__
  if (forced === 'web' || forced === 'desktop') return forced
  return typeof window._wails?.invoke === 'function' ? 'desktop' : 'web'
}

export const isDesktop = () => transport() === 'desktop'
