<script setup lang="ts">
import { computed, ref } from 'vue'
import CopyText from './CopyText.vue'
import { highlight, type CodeLang } from './highlight'

// Read-only code, logs, or payloads. Objects are shown as indented JSON.
// Long content starts folded so one payload cannot push the page away.
const props = withDefaults(defineProps<{ value: unknown; lang?: CodeLang; maxLines?: number }>(), {
  lang: undefined,
  maxLines: 24,
})

const text = computed(() =>
  typeof props.value === 'string' ? props.value : (JSON.stringify(props.value, null, 2) ?? String(props.value)),
)
const language = computed<CodeLang>(() => props.lang ?? (typeof props.value === 'string' ? 'text' : 'json'))
const lines = computed(() => text.value.split('\n'))
const expanded = ref(false)
const folded = computed(() => !expanded.value && lines.value.length > props.maxLines)
const html = computed(() =>
  highlight(folded.value ? lines.value.slice(0, props.maxLines).join('\n') : text.value, language.value),
)
</script>

<template>
  <div class="code-block">
    <div class="code-copy">
      <CopyText :value="text" icon-only />
    </div>
    <!-- eslint-disable-next-line vue/no-v-html -->
    <pre><code v-html="html" /></pre>
    <button v-if="lines.length > maxLines" type="button" class="ghost btn-sm code-more" @click="expanded = !expanded">
      {{ expanded ? '收起' : `展开全部 ${lines.length} 行` }}
    </button>
  </div>
</template>

<style scoped>
.code-block {
  position: relative;
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.code-block pre {
  max-height: min(480px, 60dvh);
  overflow-wrap: anywhere;
  padding-right: 40px;
}
.code-copy {
  position: absolute;
  top: 6px;
  right: 6px;
}
.code-more {
  align-self: flex-start;
  margin-top: 4px;
}
</style>
