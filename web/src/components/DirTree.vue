<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import { errorMessage } from '@/base/errors'
import { getFileTree } from '@/api/files'
import type { TreeNode } from '@/api/types'

// Lazily loaded folder tree of a scanned session. The model is the selected
// folder path ('' for the scan root).
const props = defineProps<{ sessionId: number }>()
const model = defineModel<string>({ required: true })

const children = ref(new Map<string, TreeNode[]>())
const expanded = ref(new Set<string>())
const loading = ref(new Set<string>())
const error = ref('')

async function load(path: string) {
  if (children.value.has(path) || loading.value.has(path)) return
  loading.value = new Set(loading.value).add(path)
  try {
    const nodes = await getFileTree(props.sessionId, path)
    children.value = new Map(children.value).set(path, nodes)
    error.value = ''
  } catch (e) {
    error.value = errorMessage(e)
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
    void load('')
  },
  { immediate: true },
)

interface Row {
  node: TreeNode
  depth: number
}

const rows = computed<Row[]>(() => {
  const out: Row[] = []
  const walk = (path: string, depth: number) => {
    for (const node of children.value.get(path) ?? []) {
      out.push({ node, depth })
      if (expanded.value.has(node.original_path)) walk(node.original_path, depth + 1)
    }
  }
  walk('', 0)
  return out
})

function toggle(node: TreeNode) {
  const next = new Set(expanded.value)
  if (next.has(node.original_path)) next.delete(node.original_path)
  else {
    next.add(node.original_path)
    void load(node.original_path)
  }
  expanded.value = next
}

function select(node: TreeNode) {
  model.value = node.original_path
  if (!node.is_leaf && !expanded.value.has(node.original_path)) toggle(node)
}
</script>

<template>
  <nav class="tree" aria-label="目录">
    <button type="button" class="tree-row" :class="{ selected: model === '' }" @click="model = ''">
      <Icon name="folder" :size="14" />全部
    </button>
    <div v-for="r in rows" :key="r.node.original_path" class="tree-line" :style="{ '--depth': r.depth }">
      <button
        type="button"
        class="ghost btn-icon btn-sm tree-toggle"
        :class="{ hidden: r.node.is_leaf }"
        :aria-label="expanded.has(r.node.original_path) ? '收起' : '展开'"
        :aria-expanded="expanded.has(r.node.original_path)"
        @click="toggle(r.node)"
      >
        <Icon name="chevron-right" :size="12" class="chev" />
      </button>
      <button
        type="button"
        class="tree-row"
        :class="{ selected: model === r.node.original_path }"
        @click="select(r.node)"
      >
        <Icon name="folder" :size="14" />
        <span class="truncate">{{ r.node.label }}</span>
      </button>
    </div>
    <Loading v-if="loading.has('')" inline />
    <p v-if="error" class="text-danger text-sm">{{ error }}</p>
  </nav>
</template>

<style scoped>
.tree {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
  overflow: auto;
}
.tree-line {
  display: flex;
  align-items: center;
  padding-left: calc(var(--depth) * 14px);
  min-width: 0;
}
.tree-toggle.hidden {
  visibility: hidden;
}
.chev {
  transition: transform var(--dur-fast) var(--ease);
}
.tree-toggle[aria-expanded='true'] .chev {
  transform: rotate(90deg);
}
.tree-row {
  flex: 1;
  min-width: 0;
  justify-content: flex-start;
  gap: var(--space-2);
  height: 28px;
  padding: 0 6px;
  font-weight: 400;
  background: transparent;
  border: 0;
}
.tree-row:hover {
  background: var(--surface-hover);
}
.tree-row.selected {
  color: var(--accent-text);
  background: var(--accent-soft);
}
</style>
