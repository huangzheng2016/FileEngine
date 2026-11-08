import { onScopeDispose, ref } from 'vue'

export function useMedia(query: string) {
  const mql = window.matchMedia(query)
  const matches = ref(mql.matches)
  const update = (e: MediaQueryListEvent) => (matches.value = e.matches)
  mql.addEventListener('change', update)
  onScopeDispose(() => mql.removeEventListener('change', update))
  return matches
}
