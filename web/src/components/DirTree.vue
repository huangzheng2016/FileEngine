<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import { errorMessage } from '@/base/errors'
import { getFileTree } from '@/api/files'
import type { TreeNode, Id } from '@/api/types'

const props = defineProps<{ sessionId: Id }>()
const model = defineModel<string>({ required: true })

const children = ref(new Map<string, TreeNode[]>())
const expanded = ref(new Set<string>())
const loading = ref(new Set<string>())
const errors = ref(new Map<string, string>())

async function load(path: string) {
  if (children.value.has(path) || loading.value.has(path)) return
  loading.value = new Set(loading.value).add(path)
  try {
    const nodes = await getFileTree(props.sessionId, path)
    children.value = new Map(children.value).set(path, nodes)
    const next = new Map(errors.value)
    next.delete(path)
    errors.value = next
  } catch (e) {
    errors.value = new Map(errors.value).set(path, errorMessage(e))
  } finally {
    const next = new Set(loading.value)
    next.delete(path)
    loading.value = next
  }
}

watch(
  () => props.sessionId,
  () => {
    children.value = new Map()
    expanded.value = new Set()
    errors.value = new Map()
    void load('')
  },
  { immediate: true },
)

interface Row {
  // '' is the scan root row
  path: string
  label: string
  depth: number
  leaf: boolean
  // a load failure shown under this row
  error: string
  loading: boolean
}

const rows = computed<Row[]>(() => {
  const out: Row[] = [
    {
      path: '',
      label: '全部',
      depth: 0,
      leaf: false,
      error: errors.value.get('') ?? '',
      loading: loading.value.has(''),
    },
  ]
  const walk = (path: string, depth: number) => {
    for (const node of children.value.get(path) ?? []) {
      const p = node.original_path
      const open = expanded.value.has(p)
      out.push({
        path: p,
        label: node.label,
        depth,
        leaf: node.is_leaf,
        error: open ? (errors.value.get(p) ?? '') : '',
        loading: open && loading.value.has(p),
      })
      if (open) walk(p, depth + 1)
    }
  }
  walk('', 1)
  return out
})

// the one tab stop of the tree: the chosen folder, or the root while it is out of view
const focusPath = computed(() => (rows.value.some((r) => r.path === model.value) ? model.value : ''))
const isOpen = (path: string) => path === '' || expanded.value.has(path)

function expand(path: string) {
  if (!path || expanded.value.has(path)) return
  expanded.value = new Set(expanded.value).add(path)
  void load(path)
}

function collapse(path: string) {
  if (!expanded.value.has(path)) return
  const next = new Set(expanded.value)
  next.delete(path)
  expanded.value = next
}

function toggle(r: Row) {
  if (r.leaf || !r.path) return
  if (expanded.value.has(r.path)) collapse(r.path)
  else expand(r.path)
}

function select(r: Row) {
  model.value = r.path
  if (!r.leaf) expand(r.path)
}

function retry(r: Row) {
  void load(r.path)
}

const root = ref<HTMLElement | null>(null)
function focusStep(from: HTMLElement, step: number) {
  const items = [...(root.value?.querySelectorAll<HTMLElement>('[role="treeitem"]') ?? [])]
  items[items.indexOf(from) + step]?.focus()
}

function onKey(e: KeyboardEvent, r: Row) {
  const el = e.currentTarget as HTMLElement
  switch (e.key) {
    case 'Enter':
    case ' ':
      select(r)
      break
    case 'ArrowDown':
      focusStep(el, 1)
      break
    case 'ArrowUp':
      focusStep(el, -1)
      break
    case 'ArrowRight':
      if (!r.leaf) expand(r.path)
      break
    case 'ArrowLeft':
      collapse(r.path)
      break
    default:
      return
  }
  e.preventDefault()
}
</script>

<template>
  <div ref="root" class="tree" role="tree" aria-label="文件夹">
    <template v-for="r in rows" :key="r.path">
      <div
        role="treeitem"
        class="tree-item"
        :class="{ selected: model === r.path }"
        :style="{ '--depth': r.depth }"
        :tabindex="r.path === focusPath ? 0 : -1"
        :aria-selected="model === r.path"
        :aria-expanded="r.leaf || !r.path ? undefined : isOpen(r.path)"
        @click="select(r)"
        @keydown="onKey($event, r)"
      >
        <span class="tree-toggle" aria-hidden="true" @click.stop="toggle(r)">
          <Icon
            v-if="r.path && !r.leaf"
            name="chevron-right"
            :size="12"
            class="chev"
            :class="{ open: isOpen(r.path) }"
          />
        </span>
        <Icon name="folder" :size="14" />
        <span class="truncate">{{ r.label }}</span>
        <Loading v-if="r.loading" inline />
      </div>
      <p v-if="r.error" class="tree-error text-danger text-sm" :style="{ '--depth': r.depth }">
        加载失败：{{ r.error }}
        <button type="button" class="ghost btn-sm" @click="retry(r)">重试</button>
      </p>
    </template>
  </div>
</template>

<style scoped>
.tree {
  min-width: 0;
  overflow: auto;
}
.tree-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  height: 28px;
  padding: 0 6px 0 calc(var(--depth) * 14px - 14px + 6px);
  border-radius: var(--radius-sm);
  cursor: pointer;
}
.tree-item:hover {
  background: var(--surface-hover);
}
.tree-item:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}
.tree-item.selected {
  color: var(--accent-text);
  background: var(--accent-soft);
}
.tree-toggle {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 16px;
}
.chev {
  transition: transform var(--dur-fast) var(--ease);
}
.chev.open {
  transform: rotate(90deg);
}
.tree-error {
  padding-left: calc(var(--depth) * 14px + 6px);
}
</style>
