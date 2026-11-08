import { computed, reactive, watch } from 'vue'
import { useFetch } from './useFetch'
import { useRouteQuery } from './useRouteQuery'

export const PAGE_SIZES = [20, 50] as const

export interface Page<T> {
  items: T[]
  total: number
}

type Filters = Record<string, string | number>

// One list page = one useList. Filters, page and size live in the URL, so a shared
// link reproduces the view and back/forward restores it.
export function useList<T, F extends Filters>(
  fetcher: (params: F & { page: number; size: number; sort: string }, signal: AbortSignal) => Promise<Page<T>>,
  defaults: F,
  opts: { search?: keyof F & string; debounceMs?: number } = {},
) {
  // sort: 'field' ascending, '-field' descending, '' the server's default order (see SortTh)
  const { query, set } = useRouteQuery({ ...defaults, page: 1, size: PAGE_SIZES[0] as number, sort: '' })

  const params = computed(() => {
    const q = { ...query.value }
    if (!PAGE_SIZES.includes(q.size as (typeof PAGE_SIZES)[number])) q.size = PAGE_SIZES[0]
    if (q.page < 1) q.page = 1
    return q as F & { page: number; size: number; sort: string }
  })

  // Bound to the filter controls with v-model. Edits flow to the URL; URL changes
  // (back/forward, links) flow back here.
  const filters = reactive({ ...defaults }) as F
  watch(
    query,
    (q) => {
      for (const k of Object.keys(defaults) as (keyof F)[]) {
        if (filters[k] !== q[k]) filters[k] = q[k] as F[keyof F]
      }
    },
    { immediate: true },
  )

  let timer: ReturnType<typeof setTimeout> | undefined
  watch(
    () => ({ ...filters }),
    (next, prev) => {
      const changed = (Object.keys(defaults) as (keyof F & string)[]).filter((k) => next[k] !== query.value[k])
      if (changed.length === 0) return
      clearTimeout(timer)
      const onlySearch = opts.search && changed.every((k) => k === opts.search) && prev
      const apply = () => set({ ...next, page: 1 } as never)
      if (onlySearch) timer = setTimeout(apply, opts.debounceMs ?? 300)
      else apply()
    },
  )

  const state = useFetch((signal) => fetcher(params.value, signal))
  watch(
    () => JSON.stringify(params.value),
    () => void state.refresh(),
    { immediate: true },
  )

  const items = computed(() => state.data.value?.items ?? [])
  const total = computed(() => state.data.value?.total ?? 0)

  // Deleting the last row of the last page would otherwise strand the user on an empty page.
  watch(total, (t) => {
    const last = Math.max(1, Math.ceil(t / params.value.size))
    if (params.value.page > last) set({ page: last } as never)
  })

  return {
    filters,
    items,
    total,
    page: computed(() => params.value.page),
    size: computed(() => params.value.size),
    sort: computed(() => params.value.sort),
    loading: state.loading,
    loaded: state.loaded,
    error: state.error,
    refresh: state.refresh,
    setPage: (page: number) => set({ page } as never),
    setSize: (size: number) => set({ size, page: 1 } as never),
    setSort: (sort: string) => set({ sort, page: 1 } as never),
    reset: () => set({ ...defaults, page: 1 } as never),
  }
}
