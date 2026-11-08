<script setup lang="ts">
import { defineAsyncComponent } from 'vue'

// The renderer is a large chunk, so it loads on first use; until then the text
// shows as plain text.
withDefaults(defineProps<{ source: string; streaming?: boolean }>(), { streaming: false })

const Renderer = defineAsyncComponent(() => import('./MarkstreamView.vue'))
</script>

<template>
  <Suspense>
    <Renderer :source="source" :streaming="streaming" />
    <template #fallback>
      <div class="plain">{{ source }}</div>
    </template>
  </Suspense>
</template>

<style scoped>
.plain {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.7;
}
</style>
