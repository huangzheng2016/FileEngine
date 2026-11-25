<script setup lang="ts">
import { computed } from 'vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import { fmtNumber } from '@/base/format'
import { toolLabel } from '@/lib/toolView'
import { stepActivity, stepSummary, type Block } from '@/lib/transcript'
import StreamMarkdown from './StreamMarkdown.vue'
import ToolCard from './ToolCard.vue'
import { useOpenState } from './openState'

const props = defineProps<{ block: Block; scope?: string }>()

const state = useOpenState()
const stateKey = computed(() => `${props.scope ?? ''}b:${props.block.key}`)
const open = computed(() => state.get(stateKey.value) ?? (props.block.kind === 'steps' && props.block.failed))
const toggle = () => state.set(stateKey.value, !open.value)
const activity = computed(() => (props.block.kind === 'steps' ? stepActivity(props.block) : null))
// the newest lines of thinking still arriving, shown while the group is folded
const preview = computed(() => activity.value?.reasoning?.trim().split('\n').slice(-4).join('\n') ?? '')
</script>

<template>
  <div v-if="block.kind === 'unit'" class="unit-line text-sm">
    <Icon name="folder" :size="14" />
    <span class="truncate">{{ block.item.label }}</span>
    <span v-if="block.item.count" class="text-muted">{{ fmtNumber(block.item.count) }} 个</span>
    <span v-if="block.item.done" class="unit-result">
      已处理 {{ fmtNumber(block.item.tagged)
      }}<template v-if="block.item.failed"
        >，<span class="text-danger">失败 {{ fmtNumber(block.item.failed) }}</span></template
      >
    </span>
    <Loading v-else class="unit-result" inline label="" :delay="0" />
  </div>

  <div v-else-if="block.kind === 'prompt'" class="prompt">
    <StreamMarkdown :source="block.item.text" />
  </div>

  <div v-else-if="block.kind === 'answer'" class="answer">
    <template v-if="block.item.role === 'system'">
      <p class="text-danger system-line"><Icon name="alert-triangle" :size="14" />{{ block.item.text }}</p>
    </template>
    <template v-else>
      <StreamMarkdown :source="block.item.text" :streaming="block.item.streaming" />
    </template>
  </div>

  <div v-else class="steps">
    <button type="button" class="ghost btn-sm steps-head" :aria-expanded="open" @click="toggle">
      <Icon name="chevron-right" :size="14" class="chev" />
      {{ stepSummary(block, toolLabel) }}
      <Icon v-if="block.failed" name="x-circle" :size="14" class="text-danger" />
      <span v-if="activity" class="steps-live">
        <Loading inline label="" :delay="0" />{{ activity.tool ? `正在${toolLabel(activity.tool)}` : '正在思考' }}
      </span>
    </button>
    <p v-if="!open && preview" class="steps-preview text-sm" aria-hidden="true">{{ preview }}</p>
    <div v-if="open" class="steps-body">
      <template v-for="it in block.items" :key="it.kind === 'tool' ? it.key : it.id">
        <ToolCard v-if="it.kind === 'tool'" :tool="it" :scope="scope" />
        <template v-else>
          <div v-if="it.reasoning" class="thinking">
            <div class="field-label">思考过程</div>
            <StreamMarkdown :source="it.reasoning" :streaming="it.streaming && !it.text" />
          </div>
          <div v-if="it.role === 'user'" class="thinking">
            <div class="field-label">指令</div>
            <StreamMarkdown :source="it.text" />
          </div>
          <StreamMarkdown v-else-if="it.text" :source="it.text" :streaming="it.streaming" />
        </template>
      </template>
    </div>
  </div>
</template>

<style scoped>
.unit-line {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  padding: var(--space-1) 0;
  color: var(--text-secondary);
  border-top: 1px dashed var(--border);
}
.unit-result {
  margin-left: auto;
  white-space: nowrap;
}
.prompt {
  align-self: flex-end;
  max-width: 85%;
  padding: var(--space-2) var(--space-3);
  background: var(--accent-soft);
  border-radius: var(--radius-md);
}
.answer {
  min-width: 0;
}
.system-line {
  display: flex;
  align-items: flex-start;
  gap: 6px;
}
.steps {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}
.steps-head {
  align-self: flex-start;
  gap: 4px;
  color: var(--text-muted);
  font-weight: 400;
}
.steps-live {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-left: var(--space-1);
  color: var(--text-secondary);
}
.steps-preview {
  max-height: 5.6em;
  margin: 0;
  padding-left: var(--space-3);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  white-space: pre-wrap;
  color: var(--text-muted);
  border-left: 2px solid var(--border);
  mask-image: linear-gradient(to bottom, transparent, var(--text-primary) 1.6em);
}
.chev {
  transition: transform var(--dur-fast) var(--ease);
}
.steps-head[aria-expanded='true'] .chev {
  transform: rotate(90deg);
}
.steps-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding-left: var(--space-3);
  border-left: 2px solid var(--border);
  min-width: 0;
}
.thinking {
  color: var(--text-secondary);
  font-size: var(--text-sm);
}
</style>
