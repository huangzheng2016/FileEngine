<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import MarkdownIt from 'markdown-it'

// Renders Markdown from models or users. Raw HTML in the source is shown as text,
// links open in a new tab without access to this page, and wide tables scroll.
// While streaming, re-rendering is coalesced to one per animation frame.
const props = withDefaults(defineProps<{ source: string; streaming?: boolean }>(), { streaming: false })

const md = new MarkdownIt({ html: false, linkify: true, breaks: true })
const defaultLink =
  md.renderer.rules.link_open ?? ((tokens, idx, opts, _env, self) => self.renderToken(tokens, idx, opts))
md.renderer.rules.link_open = (tokens, idx, opts, env, self) => {
  tokens[idx].attrSet('target', '_blank')
  tokens[idx].attrSet('rel', 'noopener noreferrer')
  return defaultLink(tokens, idx, opts, env, self)
}
md.renderer.rules.table_open = () => '<div class="md-table"><table>'
md.renderer.rules.table_close = () => '</table></div>'

const html = ref('')
let frame = 0

watch(
  () => props.source,
  (src) => {
    if (!props.streaming) {
      cancelAnimationFrame(frame)
      html.value = md.render(src)
      return
    }
    cancelAnimationFrame(frame)
    frame = requestAnimationFrame(() => (html.value = md.render(src)))
  },
  { immediate: true },
)
watch(
  () => props.streaming,
  (s) => {
    if (!s) html.value = md.render(props.source)
  },
)
onBeforeUnmount(() => cancelAnimationFrame(frame))
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -->
  <div class="md" :class="{ streaming }" v-html="html" />
</template>

<style scoped>
.md {
  min-width: 0;
  max-width: 100%;
  font-size: var(--text-base);
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.md :deep(> :first-child) {
  margin-top: 0;
}
.md :deep(> :last-child) {
  margin-bottom: 0;
}
.md :deep(:is(p, ul, ol, blockquote, pre, .md-table)) {
  margin: 0 0 var(--space-3);
}
.md :deep(:is(h1, h2, h3, h4)) {
  margin: var(--space-4) 0 var(--space-2);
}
.md :deep(h1) {
  font-size: var(--text-xl);
}
.md :deep(h2) {
  font-size: var(--text-lg);
}
.md :deep(:is(ul, ol)) {
  padding-left: 1.4em;
}
.md :deep(blockquote) {
  padding-left: var(--space-3);
  color: var(--text-secondary);
  border-left: 3px solid var(--border-strong);
}
.md :deep(img) {
  max-width: 100%;
  height: auto;
}
.md :deep(.md-table) {
  overflow-x: auto;
}
.md :deep(table) {
  width: auto;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}
.md :deep(:is(th, td)) {
  height: 32px;
  white-space: normal;
}
.md :deep(th) {
  position: static;
}
.md.streaming :deep(> :last-child)::after {
  content: '';
  display: inline-block;
  width: 7px;
  height: 1em;
  margin-left: 2px;
  vertical-align: text-bottom;
  background: var(--accent);
  animation: blink 1s steps(2) infinite;
}
@keyframes blink {
  50% {
    opacity: 0;
  }
}
</style>
