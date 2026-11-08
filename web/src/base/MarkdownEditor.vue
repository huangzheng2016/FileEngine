<script setup lang="ts">
import { ref } from 'vue'
import MarkdownText from './MarkdownText.vue'
import Segmented from './Segmented.vue'

// Long text with formatting. Markdown is the format: it stores as plain text,
// diffs cleanly, and renders the same here and in MarkdownText. There is no
// WYSIWYG editor in the system.
withDefaults(defineProps<{ rows?: number; placeholder?: string; name?: string }>(), {
  rows: 6,
  placeholder: '支持 Markdown',
  name: undefined,
})
const model = defineModel<string>({ required: true })
const mode = ref<'edit' | 'preview'>('edit')
const MODES = [
  { value: 'edit', label: '编辑' },
  { value: 'preview', label: '预览' },
] as const
</script>

<template>
  <div class="md-editor">
    <Segmented v-model="mode" :options="MODES" label="编辑或预览" class="md-mode" />
    <textarea v-if="mode === 'edit'" v-model="model" :name="name" :rows="rows" :placeholder="placeholder" />
    <div v-else class="md-preview">
      <MarkdownText v-if="model.trim()" :source="model" />
      <p v-else class="text-muted">暂无内容</p>
    </div>
  </div>
</template>

<style scoped>
.md-editor {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.md-mode {
  align-self: flex-start;
}
.md-preview {
  min-height: 80px;
  max-height: 320px;
  overflow-y: auto;
  padding: 6px 10px;
  border: 1px dashed var(--border-strong);
  border-radius: var(--radius-sm);
}
</style>
