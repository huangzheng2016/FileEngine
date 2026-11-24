<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import Icon from '@/base/Icon.vue'
import Pagination from '@/base/Pagination.vue'
import Segmented from '@/base/Segmented.vue'
import Select from '@/base/Select.vue'
import { linkFrom } from '@/base/backLink'
import { labelOptions } from '@/base/format'
import { MEMORY_SCOPE } from '@/base/labels'
import { useFetch } from '@/base/useFetch'
import { PAGE_SIZES, useList } from '@/base/useList'
import { listMemory } from '@/api/memory'
import { listFilesystems } from '@/api/settings'
import { NO_ID, type Id } from '@/api/types'
import MemoryTable from './MemoryTable.vue'

const route = useRoute()
const storages = useFetch(listFilesystems, { immediate: true })
const storageOptions = computed(() => (storages.data.value ?? []).map((f) => ({ value: f.id, label: f.name })))

const list = useList(
  async (p: { scope: string; fs: string; q: string; page: number; size: number }, signal) => {
    const scope = p.scope === 'filesystem' ? 'filesystem' : 'global'
    if (scope === 'filesystem' && !p.fs) return { items: [], total: 0 }
    const all = await listMemory(scope, scope === 'filesystem' ? (p.fs as Id) : NO_ID, signal)
    const q = p.q.trim().toLowerCase()
    const rows = all
      .filter((m) => !q || `${m.content} ${m.path}`.toLowerCase().includes(q))
      .sort((a, b) => Number(b.pinned) - Number(a.pinned) || b.hits - a.hits)
    return { items: rows.slice((p.page - 1) * p.size, p.page * p.size), total: rows.length }
  },
  { scope: 'global', fs: '', q: '' },
  { search: 'q' },
)

// An unknown or deleted storage id in the URL falls back to the first storage.
watch(
  () => [list.filters.scope, list.filters.fs, storageOptions.value] as const,
  ([scope, fs, opts]) => {
    if (scope === 'filesystem' && opts.length && !opts.some((o) => o.value === fs)) list.filters.fs = opts[0].value
  },
  { immediate: true },
)

const SCOPES = labelOptions(MEMORY_SCOPE).filter((o) => o.value !== 'session') as {
  value: 'global' | 'filesystem'
  label: string
}[]
const scope = computed(() => (list.filters.scope === 'filesystem' ? 'filesystem' : 'global'))
const scopeId = computed<Id>(() => (scope.value === 'filesystem' ? list.filters.fs : NO_ID))
const ready = computed(() => scope.value === 'global' || storageOptions.value.some((o) => o.value === scopeId.value))
const filtered = computed(() => !!list.filters.q)
const noStorage = computed(() => scope.value === 'filesystem' && storages.data.value?.length === 0)

const table = ref<InstanceType<typeof MemoryTable> | null>(null)
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <input
        v-if="list.items.value.length || filtered"
        v-model="list.filters.q"
        type="search"
        class="search"
        placeholder="搜索记忆"
        aria-label="搜索记忆"
      />
      <Segmented
        :model-value="scope"
        :options="SCOPES"
        label="范围"
        @update:model-value="list.filters.scope = $event"
      />
      <Select
        v-if="scope === 'filesystem' && ready"
        :model-value="list.filters.fs"
        :options="storageOptions"
        label="存储"
        @update:model-value="list.filters.fs = $event"
      />
      <span class="spacer" />
      <button v-if="ready" type="button" class="primary" @click="table?.create()"><Icon name="plus" />新建</button>
    </div>
    <MemoryTable
      v-if="ready"
      ref="table"
      :items="list.items.value"
      :loading="list.loading.value && !list.loaded.value"
      :error="list.error.value"
      :scope="scope"
      :scope-id="scopeId"
      :filtered="filtered"
      :empty-text="scope === 'global' ? '暂无全局记忆' : '这个存储还没有记忆'"
      @changed="list.refresh"
      @retry="list.refresh"
      @clear="list.reset"
    />
    <p v-else-if="noStorage" class="text-muted">
      还没有存储，<RouterLink :to="linkFrom('/settings/storage', route)">新建存储</RouterLink>
    </p>
    <Pagination
      v-if="ready && list.total.value > PAGE_SIZES[0]"
      :total="list.total.value"
      :page="list.page.value"
      :size="list.size.value"
      @update:page="list.setPage"
      @update:size="list.setSize"
    />
  </div>
</template>
