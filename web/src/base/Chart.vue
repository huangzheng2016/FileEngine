<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import type { EChartsCoreOption } from './echarts'
import EmptyState from './EmptyState.vue'
import Loading from './Loading.vue'

// An ECharts chart. The library loads on first use, so pages without charts do
// not pay for it. Give it an option object; colors, fonts, dark mode and resize
// are handled here. Pass loading/empty instead of drawing empty axes.
const props = withDefaults(
  defineProps<{ option: EChartsCoreOption; height?: string; loading?: boolean; empty?: boolean; emptyText?: string }>(),
  { height: '280px', loading: false, empty: false, emptyText: '暂无数据' },
)

type Instance = {
  setOption: (o: EChartsCoreOption, notMerge?: boolean) => void
  resize: () => void
  dispose: () => void
}

const el = ref<HTMLElement | null>(null)
const chart = shallowRef<Instance | null>(null)
let disposed = false
let resize: ResizeObserver | undefined
let theme: MutationObserver | undefined

async function draw() {
  const lib = await import('./echarts')
  if (disposed || !el.value) return
  chart.value?.dispose()
  chart.value = lib.init(el.value, lib.themeFromTokens()) as Instance
  chart.value.setOption(props.option, true)
}

watch(
  () => props.option,
  (o) => chart.value?.setOption(o, true),
  { deep: true },
)

onMounted(() => {
  void draw()
  if (typeof ResizeObserver !== 'undefined') {
    resize = new ResizeObserver(() => chart.value?.resize())
    resize.observe(el.value!)
  }
  // the theme is baked in at init, so a light/dark switch redraws
  theme = new MutationObserver(() => void draw())
  theme.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
})

onBeforeUnmount(() => {
  disposed = true
  resize?.disconnect()
  theme?.disconnect()
  chart.value?.dispose()
})
</script>

<template>
  <div class="chart" :style="{ height }">
    <div ref="el" class="chart-canvas" :class="{ hidden: loading || empty }" />
    <Loading v-if="loading" class="chart-overlay" />
    <EmptyState v-else-if="empty" :title="emptyText" icon="chart-line" class="chart-overlay" />
  </div>
</template>

<style scoped>
.chart {
  position: relative;
  min-width: 0;
}
.chart-canvas {
  position: absolute;
  inset: 0;
}
.chart-canvas.hidden {
  visibility: hidden;
}
.chart-overlay {
  position: absolute;
  inset: 0;
}
</style>
