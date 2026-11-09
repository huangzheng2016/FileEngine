<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import Icon from '@/base/Icon.vue'
import Segmented from '@/base/Segmented.vue'
import Select from '@/base/Select.vue'
import { labelOptions } from '@/base/format'
import { MEMORY_SCOPE } from '@/base/labels'
import { useFetch } from '@/base/useFetch'
import { useRouteQuery } from '@/base/useRouteQuery'
import { listFilesystems } from '@/api/settings'
import MemoryTable from './MemoryTable.vue'

const { query, set } = useRouteQuery({ scope: 'global', fs: 0, q: '' })
const storages = useFetch(listFilesystems, { immediate: true })
const storageOptions = computed(() => (storages.data.value ?? []).map((f) => ({ value: f.id, label: f.name })))
// An unknown or deleted storage id in the URL falls back to the first storage.
watch(
  () => [query.value.scope, query.value.fs, storageOptions.value] as const,
  ([scope, fs, opts]) => {
    if (scope === 'filesystem' && opts.length && !opts.some((o) => o.value === fs)) set({ fs: opts[0].value })
  },
  { immediate: true },
)

const SCOPES = labelOptions(MEMORY_SCOPE).filter((o) => o.value !== 'session') as {
  value: 'global' | 'filesystem'
  label: string
}[]
const scope = computed(() => (query.value.scope === 'filesystem' ? 'filesystem' : 'global'))
const scopeId = computed(() => (scope.value === 'filesystem' ? query.value.fs : 0))
const ready = computed(() => scope.value === 'global' || storageOptions.value.some((o) => o.value === scopeId.value))

const search = ref(query.value.q)
let timer: ReturnType<typeof setTimeout> | undefined
watch(search, (q) => {
  clearTimeout(timer)
  timer = setTimeout(() => set({ q: q.trim() }), 250)
})
onBeforeUnmount(() => clearTimeout(timer))
watch(
  () => query.value.q,
  (q) => {
    if (q !== search.value.trim()) search.value = q
  },
)

const table = ref<InstanceType<typeof MemoryTable> | null>(null)
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <input v-model="search" type="search" class="search" placeholder="搜索记忆" aria-label="搜索记忆" />
      <Segmented :model-value="scope" :options="SCOPES" label="范围" @update:model-value="set({ scope: $event })" />
      <Select
        v-if="scope === 'filesystem' && ready"
        :model-value="query.fs"
        :options="storageOptions"
        label="存储"
        @update:model-value="set({ fs: $event })"
      />
      <span class="spacer" />
      <button v-if="ready" type="button" class="primary" @click="table?.create()"><Icon name="plus" />新建</button>
    </div>
    <MemoryTable
      v-if="ready"
      ref="table"
      :scope="scope"
      :scope-id="scopeId"
      :q="query.q"
      :empty-text="scope === 'global' ? '暂无全局记忆' : '这个存储还没有记忆'"
    />
    <p v-else-if="storages.data.value" class="text-muted">
      还没有存储，<RouterLink to="/settings/storage">新建存储</RouterLink>
    </p>
  </div>
</template>
