<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from '@/base/Icon.vue'
import Segmented from '@/base/Segmented.vue'
import Select from '@/base/Select.vue'
import { useFetch } from '@/base/useFetch'
import { useRouteQuery } from '@/base/useRouteQuery'
import { listFilesystems } from '@/api/settings'
import MemoryTable from './MemoryTable.vue'

const { query, set } = useRouteQuery({ scope: 'global', fs: 0 })
const storages = useFetch(listFilesystems, { immediate: true })
const storageOptions = computed(() => (storages.data.value ?? []).map((f) => ({ value: f.id, label: f.name })))
watch(
  () => [query.value.scope, storageOptions.value] as const,
  ([scope, opts]) => {
    if (scope === 'filesystem' && !query.value.fs && opts.length) set({ fs: opts[0].value })
  },
  { immediate: true },
)

const SCOPES = [
  { value: 'global', label: '全局偏好' },
  { value: 'filesystem', label: '按存储' },
]
const scope = computed(() => (query.value.scope === 'filesystem' ? 'filesystem' : 'global'))
const scopeId = computed(() => (scope.value === 'filesystem' ? query.value.fs : 0))
const search = ref('')
const table = ref<InstanceType<typeof MemoryTable> | null>(null)
const ready = computed(() => scope.value === 'global' || scopeId.value > 0)
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <input v-model="search" type="search" class="search" placeholder="搜索记忆" aria-label="搜索记忆" />
      <Segmented :model-value="scope" :options="SCOPES" label="范围" @update:model-value="set({ scope: $event })" />
      <Select
        v-if="scope === 'filesystem' && storageOptions.length"
        :model-value="query.fs"
        :options="storageOptions"
        label="存储"
        @update:model-value="set({ fs: $event })"
      />
      <span class="spacer" />
      <button type="button" class="btn-icon" aria-label="刷新" @click="table?.refresh()">
        <Icon name="refresh-cw" />
      </button>
      <button type="button" class="primary" :disabled="!ready" @click="table?.create()">
        <Icon name="plus" />新建
      </button>
    </div>
    <MemoryTable
      v-if="ready"
      ref="table"
      :scope="scope"
      :scope-id="scopeId"
      :q="search"
      :empty-text="scope === 'global' ? '暂无全局偏好' : '暂无这个存储的记忆'"
    />
    <p v-else class="text-muted">先在 设置 / 存储 中添加存储</p>
  </div>
</template>
