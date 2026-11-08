import { getCurrentScope, onScopeDispose, ref, watch, type WatchSource } from 'vue'

// Re-runs fn while `enabled` is true: the next run is scheduled only after the
// previous one settles, so a slow request is never cut off by the next tick.
// Pauses while the tab is hidden and refreshes as soon as it is visible again.
// Errors back off (2x, 4x, up to 8x the interval, with jitter) and recover on
// the first success. fn must throw (or reject) to count as an error.
//   usePolling(async () => { await runs.refresh(); if (runs.error.value) throw new Error() }, {
//     interval: 3000, enabled: () => hasActiveRun.value })
export function usePolling(fn: () => Promise<unknown>, opts: { interval: number; enabled: WatchSource<boolean> }) {
  const failures = ref(0)
  let timer: ReturnType<typeof setTimeout> | undefined
  let active = false
  let inFlight = false

  function delay() {
    const backoff = Math.min(8, 2 ** failures.value)
    return opts.interval * (failures.value ? backoff : 1) * (0.9 + Math.random() * 0.2)
  }

  function schedule() {
    clearTimeout(timer)
    if (active && !document.hidden) timer = setTimeout(tick, delay())
  }

  async function tick() {
    if (!active || inFlight || document.hidden) return
    inFlight = true
    try {
      await fn()
      failures.value = 0
    } catch {
      failures.value++
    } finally {
      inFlight = false
      schedule()
    }
  }

  function onVisibility() {
    if (document.hidden) clearTimeout(timer)
    else void tick()
  }

  // enabling starts with a wait, not a run: the caller has usually just loaded
  watch(
    opts.enabled,
    (on) => {
      active = on
      if (on) schedule()
      else clearTimeout(timer)
    },
    { immediate: true },
  )
  document.addEventListener('visibilitychange', onVisibility)

  const stop = () => {
    active = false
    clearTimeout(timer)
    document.removeEventListener('visibilitychange', onVisibility)
  }
  if (getCurrentScope()) onScopeDispose(stop)
  return { failures, stop }
}
