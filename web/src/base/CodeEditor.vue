<script setup lang="ts">
import { computed, ref } from 'vue'
import { highlight, type CodeLang } from './highlight'

// Editable code with highlighting: a transparent textarea over a highlighted
// copy of the same text. Tab inserts two spaces instead of leaving the field;
// Esc then Tab still moves focus on. For JSON fields use JsonField.
const props = withDefaults(
  defineProps<{ lang?: CodeLang; rows?: number; placeholder?: string; invalid?: boolean; name?: string }>(),
  { lang: 'text', rows: 8, placeholder: '', invalid: false, name: undefined },
)
const model = defineModel<string>({ required: true })

const pre = ref<HTMLElement | null>(null)
// the trailing newline keeps the overlay as tall as the textarea when the text ends in one
const html = computed(() => highlight(model.value, props.lang) + '\n')
let tabTraps = true

function onScroll(e: Event) {
  const t = e.target as HTMLTextAreaElement
  if (pre.value) {
    pre.value.scrollTop = t.scrollTop
    pre.value.scrollLeft = t.scrollLeft
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') tabTraps = false
  if (e.key !== 'Tab' || e.shiftKey || !tabTraps) return
  e.preventDefault()
  const t = e.target as HTMLTextAreaElement
  const { selectionStart: s, selectionEnd: end } = t
  model.value = model.value.slice(0, s) + '  ' + model.value.slice(end)
  requestAnimationFrame(() => t.setSelectionRange(s + 2, s + 2))
}
</script>

<template>
  <div class="code-editor" :class="{ invalid }" :style="{ '--rows': rows }">
    <!-- eslint-disable-next-line vue/no-v-html -->
    <pre ref="pre" aria-hidden="true" v-html="html" />
    <textarea
      v-model="model"
      :name="name"
      :placeholder="placeholder"
      :aria-invalid="invalid || undefined"
      spellcheck="false"
      autocapitalize="off"
      autocomplete="off"
      @scroll="onScroll"
      @keydown="onKeydown"
      @focus="tabTraps = true"
    />
  </div>
</template>

<style scoped>
.code-editor {
  position: relative;
  height: calc(var(--rows) * 1.6em + 14px);
  min-height: 80px;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  line-height: 1.6;
  background: var(--surface-raised);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  resize: vertical;
  overflow: hidden;
}
.code-editor:hover {
  border-color: var(--text-muted);
}
.code-editor:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-ring);
}
.code-editor.invalid {
  border-color: var(--danger);
}
.code-editor :is(pre, textarea) {
  position: absolute;
  inset: 0;
  margin: 0;
  padding: 6px 10px;
  font: inherit;
  line-height: inherit;
  white-space: pre;
  overflow: auto;
  tab-size: 2;
  border: 0;
  border-radius: 0;
  background: transparent;
  max-height: none;
}
.code-editor pre {
  pointer-events: none;
  overflow: hidden;
}
.code-editor textarea {
  height: 100%;
  min-height: 0;
  color: transparent;
  caret-color: var(--text);
  resize: none;
  box-shadow: none;
}
.code-editor textarea::selection {
  color: transparent;
  background: var(--accent-ring);
}
.code-editor textarea::placeholder {
  color: var(--text-muted);
}
</style>
