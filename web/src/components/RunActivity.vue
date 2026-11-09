<script setup lang="ts">
import { computed, ref } from 'vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import ProgressBar from '@/base/ProgressBar.vue'
import StatusPill from '@/base/StatusPill.vue'
import { fmtNumber } from '@/base/format'
import { ENTRY_FLAG } from '@/base/labels'
import { baseName } from '@/lib/paths'
import { landing, runActivity } from '@/lib/toolView'
import type { RunView } from '@/lib/transcript'
import RunTranscript from './RunTranscript.vue'

// What a running agent run is doing, in the person's terms: how far along it is,
// which folders it is looking at, and what it has decided so far. The raw
// transcript stays one click away.
const props = defineProps<{ run: RunView; mode?: 'copy' | 'move' }>()

const SHOWN = 8
const activity = computed(() => runActivity(props.run))
const showAll = ref(false)
const decisions = computed(() =>
  showAll.value ? activity.value.decisions.slice(0, 200) : activity.value.decisions.slice(0, SHOWN),
)
const detailsOpen = ref(false)

const moves = computed(() => props.run.phase === 'executing' || props.run.phase === 'revert')
const title = computed(() => {
  switch (props.run.phase) {
    case 'survey':
      return 'AI 正在写整理方案'
    case 'review':
      return 'AI 正在检查整理结果'
    case 'executing':
      return props.mode === 'move' ? '正在移动文件' : '正在复制文件'
    case 'revert':
      return '正在撤销'
    default:
      return 'AI 正在整理'
  }
})

const progress = computed(() => {
  const p = props.run.progress
  if (!p || !p.total) return null
  const done = moves.value ? p.success + p.failed + p.skipped : p.tagged
  return { done, total: p.total }
})
const desc = computed(() => {
  const p = props.run.progress
  if (moves.value) {
    if (!p) return '正在检查要处理的文件'
    const parts = [`已完成 ${fmtNumber(p.success)} 项`]
    if (p.failed) parts.push(`失败 ${fmtNumber(p.failed)} 项`)
    if (p.skipped) parts.push(`跳过 ${fmtNumber(p.skipped)} 项`)
    return parts.join('，')
  }
  if (props.run.phase === 'tagging' && p?.total)
    return `已决定 ${fmtNumber(p.tagged)} / ${fmtNumber(p.total)} 项，这一步不会移动文件`
  return '这一步不会移动文件'
})
</script>

<template>
  <section class="card" :aria-label="title">
    <div class="card-head">
      <div>
        <h3 class="activity-title">
          <Loading v-if="!run.done" inline label="" :delay="0" />
          {{ title }}
        </h3>
        <p class="card-desc tabular">{{ desc }}</p>
      </div>
    </div>
    <ProgressBar
      v-if="progress"
      :value="progress.done"
      :max="progress.total"
      :label="`${fmtNumber(progress.done)} / ${fmtNumber(progress.total)}`"
    />

    <div v-if="activity.current.length" class="now text-sm" aria-label="正在处理">
      <span class="text-muted">正在看</span>
      <span v-for="u in activity.current" :key="u.id" class="now-item">
        <Icon name="folder" :size="14" class="text-muted" />
        <span class="truncate">{{ u.label }}</span>
      </span>
    </div>

    <div v-if="decisions.length" class="decisions">
      <div class="field-label">最新决定</div>
      <ul class="decision-list text-sm">
        <li v-for="d in decisions" :key="d.key" class="decision">
          <span class="decision-main">
            <Icon :name="d.role === 'unit' ? 'folder' : 'file'" :size="14" class="text-muted" />
            <span class="truncate decision-name">{{ baseName(d.path) }}</span>
            <Icon name="arrow-right" :size="14" class="text-muted" />
            <span class="truncate">{{ landing(d.path, d.target) }}</span>
            <StatusPill v-if="d.review" value="needs_review" :map="ENTRY_FLAG" />
          </span>
          <span v-if="d.reason" class="cell-sub">{{ d.reason }}</span>
        </li>
      </ul>
      <button
        v-if="activity.decisions.length > SHOWN"
        type="button"
        class="ghost btn-sm more"
        @click="showAll = !showAll"
      >
        {{ showAll ? '收起' : `显示全部 ${fmtNumber(Math.min(activity.decisions.length, 200))} 条` }}
      </button>
    </div>

    <div v-if="!moves" class="details">
      <button
        type="button"
        class="ghost btn-sm details-head"
        :aria-expanded="detailsOpen"
        @click="detailsOpen = !detailsOpen"
      >
        <Icon name="chevron-right" :size="14" class="chev" />详细过程
      </button>
      <RunTranscript v-if="detailsOpen" :run="run" />
    </div>
  </section>
</template>

<style scoped>
.activity-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.now {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-3);
}
.now-item {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  min-width: 0;
  max-width: 100%;
}
.decisions {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}
.decision-list {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  list-style: none;
  border-top: 1px solid var(--border);
}
.decision {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  padding: var(--space-2) 0;
  border-bottom: 1px solid var(--border);
}
.decision-main {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}
.decision-name {
  max-width: 50%;
  flex-shrink: 0;
}
.more,
.details-head {
  align-self: flex-start;
}
.details {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}
.details-head {
  gap: 4px;
  color: var(--text-muted);
  font-weight: 400;
}
.chev {
  transition: transform var(--dur-fast) var(--ease);
}
.details-head[aria-expanded='true'] .chev {
  transform: rotate(90deg);
}
</style>
