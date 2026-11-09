<script setup lang="ts">
import { computed } from 'vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import { vTip } from '@/base/tooltip'
import type { Protocol } from '@/api/types'
import { crumbs, parentPath } from '@/lib/storage'

// One folder level of a storage: breadcrumb, up, and the subfolders to open.
// The parent owns loading; this only shows a listing and asks to open a path.
const props = defineProps<{
  protocol: Protocol
  path: string
  // where browsing started; never go above it
  top: string
  roots?: string[]
  dirs: { name: string; path: string }[]
  loading: boolean
  topLabel: string
  emptyText?: string
}>()
defineEmits<{ open: [string] }>()

const trail = computed(() => crumbs(props.protocol, props.path, props.top, props.roots))
const up = computed(() => parentPath(props.protocol, props.path, props.top, props.roots))
</script>

<template>
  <div class="picker">
    <nav class="picker-head" aria-label="当前位置">
      <button type="button" class="ghost btn-sm" :disabled="up === null || loading" @click="$emit('open', up!)">
        <Icon name="arrow-up" :size="14" />上一级
      </button>
      <ol class="picker-trail">
        <li>
          <span v-if="path === top" class="crumb crumb-here">{{ topLabel }}</span>
          <button v-else type="button" class="ghost btn-sm" :disabled="loading" @click="$emit('open', top)">
            <span class="crumb">{{ topLabel }}</span>
          </button>
        </li>
        <li v-for="c in trail" :key="c.path">
          <Icon name="chevron-right" :size="12" class="text-muted" />
          <span v-if="c.path === path" v-tip="c.path" class="crumb crumb-here mono">{{ c.label }}</span>
          <button
            v-else
            v-tip="c.path"
            type="button"
            class="ghost btn-sm"
            :disabled="loading"
            @click="$emit('open', c.path)"
          >
            <span class="crumb mono">{{ c.label }}</span>
          </button>
        </li>
      </ol>
    </nav>
    <div class="picker-list" :aria-busy="loading">
      <Loading v-if="loading" />
      <template v-else>
        <button v-for="d in dirs" :key="d.path" type="button" class="picker-row" @click="$emit('open', d.path)">
          <Icon name="folder" :size="16" class="text-muted" />
          <span class="truncate mono row-name">{{ d.name }}</span>
          <Icon name="chevron-right" :size="14" class="text-muted" />
        </button>
        <p v-if="!dirs.length" class="picker-empty text-muted text-sm">{{ emptyText ?? '这里没有子文件夹' }}</p>
      </template>
    </div>
  </div>
</template>

<style scoped>
.picker {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  overflow: hidden;
}
.picker-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
  background: var(--surface-muted);
  border-bottom: 1px solid var(--border);
  min-width: 0;
}
.picker-trail {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
  min-width: 0;
}
.picker-trail li {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  min-width: 0;
}
.picker-list {
  display: flex;
  flex-direction: column;
  height: 260px;
  overflow-y: auto;
}
.picker-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  height: auto;
  min-height: 36px;
  padding: var(--space-1) var(--space-3);
  text-align: left;
  justify-content: flex-start;
  background: transparent;
  border: 0;
  border-bottom: 1px solid var(--border);
  border-radius: 0;
}
.picker-row:hover {
  background: var(--surface-hover);
}
.crumb {
  display: block;
  max-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.crumb-here {
  padding: 0 var(--space-2);
  font-weight: 600;
}
.row-name {
  max-width: none;
  flex: 1;
  min-width: 0;
}
.picker-empty {
  margin: auto;
  padding: var(--space-4);
}
</style>
