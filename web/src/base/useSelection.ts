import { computed, ref, watch, type Ref } from 'vue'

// Row selection for batch actions on the current page. Rows that leave the page
// (paging, filtering, deletion) leave the selection too, so a batch action never
// touches rows the user cannot see.
export function useSelection<T extends { id: string }>(rows: Ref<readonly T[]>) {
  const ids = ref(new Set<string>())

  watch(rows, (now) => {
    const visible = new Set(now.map((r) => r.id))
    const kept = [...ids.value].filter((id) => visible.has(id))
    if (kept.length !== ids.value.size) ids.value = new Set(kept)
  })

  const count = computed(() => ids.value.size)
  const all = computed(() => rows.value.length > 0 && rows.value.every((r) => ids.value.has(r.id)))
  const some = computed(() => count.value > 0 && !all.value)

  function toggle(id: string) {
    const next = new Set(ids.value)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    ids.value = next
  }

  function toggleAll() {
    ids.value = all.value ? new Set() : new Set(rows.value.map((r) => r.id))
  }

  return {
    ids: computed(() => [...ids.value]),
    count,
    all,
    some,
    has: (id: string) => ids.value.has(id),
    toggle,
    toggleAll,
    clear: () => (ids.value = new Set()),
  }
}
