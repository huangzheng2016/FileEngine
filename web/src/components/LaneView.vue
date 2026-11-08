<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import { laneBlocks, type Item } from '@/lib/transcript'
import TranscriptBlock from './TranscriptBlock.vue'

// One worker's stream. Follows new output while the reader is at the bottom and
// stops once they scroll up; long lanes render only the visible blocks.
const props = withDefaults(defineProps<{ items: readonly Item[]; prompts?: boolean; live?: boolean }>(), {
  prompts: false,
  live: false,
})

const VIRTUAL_THRESHOLD = 60

const blocks = computed(() => laneBlocks(props.items, props.prompts))
const virtual = computed(() => blocks.value.length > VIRTUAL_THRESHOLD)

const box = ref<HTMLElement | null>(null)
const following = ref(true)

const virtualizer = useVirtualizer(
  computed(() => ({
    count: virtual.value ? blocks.value.length : 0,
    getScrollElement: () => box.value,
    estimateSize: () => 64,
    overscan: 6,
    gap: 12,
    getItemKey: (i: number) => blocks.value[i]?.key ?? i,
  })),
)

function measure(el: unknown) {
  if (el instanceof Element) virtualizer.value.measureElement(el)
}

function onScroll() {
  const el = box.value
  if (el) following.value = el.scrollHeight - el.scrollTop - el.clientHeight < 32
}

function toBottom() {
  const el = box.value
  if (el) el.scrollTop = el.scrollHeight
  following.value = true
}

// a change anywhere in the last item (streamed text) counts as new output
const revision = computed(() => {
  const last = props.items[props.items.length - 1]
  if (!last) return 0
  return props.items.length * 1e6 + (last.kind === 'message' ? last.text.length + last.reasoning.length : 0)
})

watch(revision, async () => {
  if (!following.value) return
  await nextTick()
  if (virtual.value) virtualizer.value.scrollToIndex(blocks.value.length - 1, { align: 'end' })
  toBottom()
})
onMounted(toBottom)
</script>

<template>
  <div class="lane-wrap">
    <div ref="box" class="lane" @scroll.passive="onScroll">
      <div v-if="virtual" class="lane-virtual" :style="{ height: `${virtualizer.getTotalSize()}px` }">
        <div
          v-for="row in virtualizer.getVirtualItems()"
          :key="row.key as string"
          :ref="measure"
          :data-index="row.index"
          class="lane-row"
          :style="{ transform: `translateY(${row.start}px)` }"
        >
          <TranscriptBlock :block="blocks[row.index]" />
        </div>
      </div>
      <template v-else>
        <TranscriptBlock v-for="b in blocks" :key="b.key" :block="b" />
      </template>
      <Loading v-if="live && blocks.length === 0" inline label="等待输出" :delay="0" />
    </div>
    <button v-if="!following" type="button" class="btn-sm jump" @click="toBottom">
      <Icon name="arrow-down" :size="14" />跳到最新
    </button>
  </div>
</template>

<style scoped>
.lane-wrap {
  position: relative;
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
  flex: 1;
}
.lane {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-right: var(--space-1);
}
.lane-virtual {
  position: relative;
  flex-shrink: 0;
}
.lane-row {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
}
.jump {
  position: absolute;
  bottom: var(--space-2);
  left: 50%;
  transform: translateX(-50%);
  box-shadow: var(--shadow-float);
}
</style>
