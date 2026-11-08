import { computed } from 'vue'
import { useRoute, useRouter, type LocationQuery } from 'vue-router'

type QueryValue = string | number
type Shape = Record<string, QueryValue>

// URL-backed state. Values equal to their default stay out of the URL, keys owned by
// other composables are preserved, and writes use replace so filters do not pile up history.
export function useRouteQuery<T extends Shape>(defaults: T) {
  const route = useRoute()
  const router = useRouter()

  const query = computed(() => read(route.query, defaults))

  function set(patch: Partial<T>) {
    const next: LocationQuery = { ...route.query }
    for (const key of Object.keys(patch) as (keyof T & string)[]) {
      const value = patch[key]
      if (value === undefined || value === '' || value === defaults[key]) delete next[key]
      else next[key] = String(value)
    }
    void router.replace({ query: next })
  }

  function reset() {
    set({ ...defaults })
  }

  return { query, set, reset }
}

function read<T extends Shape>(raw: LocationQuery, defaults: T): T {
  const out = { ...defaults }
  for (const key of Object.keys(defaults) as (keyof T & string)[]) {
    const v = raw[key]
    const s = Array.isArray(v) ? v[0] : v
    if (s == null || s === '') continue
    if (typeof defaults[key] === 'number') {
      const n = Number(s)
      if (Number.isFinite(n)) out[key] = n as T[typeof key]
    } else {
      out[key] = s as T[typeof key]
    }
  }
  return out
}
