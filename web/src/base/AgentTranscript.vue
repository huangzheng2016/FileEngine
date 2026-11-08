<script setup lang="ts">
import { nextTick, onMounted, ref, watch } from 'vue'
import type { Transcript } from './agent'
import Icon from './Icon.vue'
import Loading from './Loading.vue'
import MarkdownText from './MarkdownText.vue'
import ToolCallCard from './ToolCallCard.vue'

// Renders a run transcript: Markdown text and tool calls in order. It follows
// new output while the reader is at the bottom; once they scroll up it stays
// put and offers a jump back instead of yanking them down.
const props = defineProps<{ transcript: Transcript; maxHeight?: string }>()

const box = ref<HTMLElement | null>(null)
const following = ref(true)

function atBottom() {
  const el = box.value
  return !el || el.scrollHeight - el.scrollTop - el.clientHeight < 24
}

function onScroll() {
  following.value = atBottom()
}

function toBottom() {
  box.value?.scrollTo({ top: box.value.scrollHeight })
  following.value = true
}

watch(
  () => props.transcript,
  async () => {
    if (!following.value) return
    await nextTick()
    box.value?.scrollTo({ top: box.value.scrollHeight })
  },
)
onMounted(toBottom)

const running = () => !props.transcript.done

// A run that failed because a tool failed already shows that error on the tool card.
const runError = () => {
  const t = props.transcript
  if (!t.done || !t.error) return ''
  return t.blocks.some((b) => b.kind === 'tool' && b.error === t.error) ? '' : t.error
}
</script>

<template>
  <div class="transcript-wrap">
    <div ref="box" class="transcript" :style="{ maxHeight }" @scroll="onScroll">
      <template v-for="(b, i) in transcript.blocks" :key="i">
        <MarkdownText
          v-if="b.kind === 'text'"
          :source="b.text"
          :streaming="running() && i === transcript.blocks.length - 1"
        />
        <ToolCallCard v-else :tool="b" />
      </template>
      <Loading v-if="running() && transcript.blocks.length === 0" inline label="等待输出" :delay="0" />
      <p v-if="runError()" class="text-danger transcript-error"><Icon name="x-circle" :size="14" />{{ runError() }}</p>
    </div>
    <button v-if="!following" type="button" class="btn-sm jump" @click="toBottom">
      <Icon name="arrow-down" :size="14" />跳到最新
    </button>
  </div>
</template>

<style scoped>
.transcript-wrap {
  position: relative;
  min-height: 0;
}
.transcript {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  overflow-y: auto;
}
.transcript-error {
  display: flex;
  align-items: center;
  gap: 6px;
}
.jump {
  position: absolute;
  bottom: var(--space-2);
  left: 50%;
  transform: translateX(-50%);
  box-shadow: var(--shadow-float);
}
</style>
