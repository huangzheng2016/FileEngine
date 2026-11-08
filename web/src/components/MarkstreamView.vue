<script setup lang="ts">
import MarkdownRender from 'markstream-vue'
import 'markstream-vue/index.css'
import { useDarkClass } from '@/lib/theme'

// Raw HTML in the source is shown as text. `streaming=false` marks the source
// final, so unclosed constructs settle once.
withDefaults(defineProps<{ source: string; streaming?: boolean }>(), { streaming: false })

const dark = useDarkClass()
</script>

<template>
  <div class="stream-md" :class="{ streaming }">
    <MarkdownRender
      mode="chat"
      :content="source"
      :final="!streaming"
      html-policy="escape"
      :is-dark="dark"
      :render-code-blocks-as-pre="true"
      :fade="false"
      :show-tooltips="false"
    />
  </div>
</template>

<style scoped>
.stream-md {
  min-width: 0;
  max-width: 100%;
  overflow-wrap: anywhere;
}
.stream-md :deep(.markstream-vue) {
  --code-bg: var(--surface-muted);
  --code-fg: var(--text);
  --code-border: var(--border);
  --table-border: var(--border);
  color: var(--text);
  background: transparent;
  font-family: inherit;
  font-size: var(--text-base);
  line-height: 1.7;
}
.stream-md :deep(:is(h1, h2, h3, h4)) {
  margin: var(--space-4) 0 var(--space-2);
  color: var(--text);
}
.stream-md :deep(h1) {
  font-size: var(--text-xl);
}
.stream-md :deep(h2) {
  font-size: var(--text-lg);
}
.stream-md :deep(:is(h3, h4)) {
  font-size: var(--text-md);
}
.stream-md :deep(:is(p, ul, ol, blockquote, pre, table)) {
  margin: 0 0 var(--space-3);
}
.stream-md :deep(.markstream-vue > :first-child) {
  margin-top: 0;
}
.stream-md :deep(.markstream-vue > :last-child) {
  margin-bottom: 0;
}
.stream-md :deep(a) {
  color: var(--accent-text);
}
.stream-md :deep(code) {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.stream-md :deep(pre) {
  padding: var(--space-2) var(--space-3);
  color: var(--text);
  background: var(--surface-muted);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  overflow-x: auto;
}
.stream-md :deep(blockquote) {
  padding-left: var(--space-3);
  color: var(--text-secondary);
  border-left: 3px solid var(--border-strong);
}
.stream-md :deep(:is(th, td)) {
  padding: var(--space-1) var(--space-2);
  border: 1px solid var(--border);
}
.stream-md :deep(th) {
  background: var(--surface-muted);
}
.stream-md.streaming::after {
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
