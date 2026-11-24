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
const upBlocked = computed(() => (props.loading ? '正在读取' : up.value === null ? '已在最上层' : ''))
</script>

<template>
  <div class="picker">
    <nav class="picker-head" aria-label="当前位置">
      <button v-tip="upBlocked" type="button" class="ghost btn-sm" :disabled="!!upBlocked" @click="$emit('open', up!)">
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
    <div class="table-wrap picker-list" :aria-busy="loading">
      <Loading v-if="loading" />
      <table v-else-if="dirs.length">
        <tbody>
          <tr
            v-for="d in dirs"
            :key="d.path"
            class="clickable"
            tabindex="0"
            @click="$emit('open', d.path)"
            @keydown.enter.self.prevent="$emit('open', d.path)"
          >
            <td>
              <span class="folder">
                <Icon name="folder" :size="16" class="text-muted" />
                <span class="truncate mono">{{ d.name }}</span>
              </span>
            </td>
            <td class="num"><Icon name="chevron-right" :size="14" class="text-muted" /></td>
          </tr>
        </tbody>
      </table>
      <p v-else class="picker-empty text-muted text-sm">
        <slot name="empty">{{ emptyText ?? '这里没有子文件夹' }}</slot>
      </p>
    </div>
  </div>
</template>

<style scoped>
.picker {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.picker-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
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
  height: 260px;
}
.folder {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
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
.picker-empty {
  margin: 0;
  padding: var(--space-4);
  text-align: center;
}
</style>
