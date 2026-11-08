<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import { toast } from '@/base/toast'
import { vTip } from '@/base/tooltip'
import { useFetch } from '@/base/useFetch'
import { listChat, sendChat } from '@/api/runs'
import type { ChatTurn } from '@/api/types'
import { chatFiles, clearChatFiles, removeChatFile } from '@/lib/chatFiles'
import { laneBlocks, type Block, type Timeline } from '@/lib/transcript'
import StreamMarkdown from './StreamMarkdown.vue'
import TranscriptBlock from './TranscriptBlock.vue'

const props = defineProps<{ sessionId: number; timeline: Timeline; busyReason: string }>()
const emit = defineEmits<{ sent: [runId: number] }>()

const turns = useFetch((signal) => listChat(props.sessionId, signal), { immediate: true })
const draft = ref('')
const sending = ref(false)
const sentText = ref(new Map<number, string>())
const files = computed(() => chatFiles(props.sessionId))

const liveRuns = computed(() =>
  props.timeline.order.map((id) => props.timeline.runs[id]).filter((r) => r && r.phase === 'chat'),
)
const liveIds = computed(() => new Set(liveRuns.value.map((r) => r.runId)))

interface Entry {
  key: string
  blocks: Block[]
  // past turns: the user's message and the final answer only
  user?: string
  answer?: string
}

const entries = computed<Entry[]>(() => {
  const out: Entry[] = []
  const byRun = new Map<number, ChatTurn[]>()
  for (const t of turns.data.value ?? []) {
    if (liveIds.value.has(t.run_id)) continue
    byRun.set(t.run_id, [...(byRun.get(t.run_id) ?? []), t])
  }
  for (const [runId, ts] of byRun) {
    out.push({
      key: `t-${runId}`,
      blocks: [],
      user: ts.find((t) => t.role === 'user')?.content,
      answer: ts.find((t) => t.role === 'assistant')?.content,
    })
  }
  for (const r of liveRuns.value) {
    const items = r.lanes.flatMap((l) => l.items)
    const hasPrompt = items.some((i) => i.kind === 'message' && i.role === 'user')
    const prompt = hasPrompt
      ? undefined
      : (sentText.value.get(r.runId) ??
        turns.data.value?.find((t) => t.run_id === r.runId && t.role === 'user')?.content)
    out.push({ key: `r-${r.runId}`, blocks: laneBlocks(items, true), user: prompt })
  }
  return out
})

const runErrors = computed(() =>
  liveRuns.value.filter((r) => r.done && r.error).map((r) => [r.runId, r.error] as const),
)
const errorOf = (key: string) => runErrors.value.find(([id]) => `r-${id}` === key)?.[1] ?? ''

const box = ref<HTMLElement | null>(null)
const following = ref(true)
function onScroll() {
  const el = box.value
  if (el) following.value = el.scrollHeight - el.scrollTop - el.clientHeight < 32
}
function toBottom() {
  const el = box.value
  if (el) el.scrollTop = el.scrollHeight
  following.value = true
}
watch(
  () => [entries.value, props.timeline.seq],
  async () => {
    if (!following.value) return
    await nextTick()
    toBottom()
  },
)
onMounted(toBottom)

// a finished turn is persisted; reload so it survives leaving the page
watch(
  () => liveRuns.value.filter((r) => r.done).length,
  () => void turns.refresh(),
)

const blocked = computed(() => props.busyReason || '')

async function send() {
  const text = draft.value.trim()
  if (!text || sending.value || blocked.value) return
  sending.value = true
  try {
    const runId = await sendChat(
      props.sessionId,
      text,
      files.value.map((f) => f.id),
    )
    sentText.value = new Map(sentText.value).set(runId, text)
    draft.value = ''
    clearChatFiles(props.sessionId)
    emit('sent', runId)
    following.value = true
  } catch (e) {
    toast.error('发送失败', e)
  } finally {
    sending.value = false
  }
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    void send()
  }
}
</script>

<template>
  <section class="chat card" aria-label="助手">
    <div class="card-head"><h3>助手</h3></div>
    <div ref="box" class="chat-list" @scroll.passive="onScroll">
      <Loading v-if="!turns.loaded.value" />
      <p v-else-if="turns.error.value" class="text-danger">加载对话失败：{{ turns.error.value }}</p>
      <p v-else-if="!entries.length" class="text-muted chat-empty">告诉助手你想怎么整理</p>
      <div v-for="e in entries" :key="e.key" class="chat-turn">
        <div v-if="e.user" class="chat-user">
          <StreamMarkdown :source="e.user" />
        </div>
        <TranscriptBlock v-for="b in e.blocks" :key="b.key" :block="b" />
        <StreamMarkdown v-if="e.answer" :source="e.answer" />
        <p v-if="errorOf(e.key)" class="text-danger">{{ errorOf(e.key) }}</p>
      </div>
    </div>
    <button v-if="!following" type="button" class="btn-sm chat-jump" @click="toBottom">
      <Icon name="arrow-down" :size="14" />跳到最新
    </button>
    <form class="chat-form" @submit.prevent="send">
      <div v-if="files.length" class="chat-files">
        <span v-for="f in files" :key="f.id" class="filter-chip">
          {{ f.name }}
          <button type="button" :aria-label="`移除 ${f.name}`" @click="removeChatFile(sessionId, f.id)">
            <Icon name="x" :size="14" />
          </button>
        </span>
      </div>
      <textarea
        v-model="draft"
        rows="3"
        name="message"
        aria-label="发给助手的消息"
        placeholder="例如：截图都放到 图片/截图"
        @keydown="onKey"
      />
      <div class="form-actions">
        <span class="text-muted text-sm">Enter 发送，Shift + Enter 换行</span>
        <span class="spacer" />
        <button v-tip="blocked" type="submit" class="primary btn-sm" :disabled="sending || !!blocked || !draft.trim()">
          <Icon name="send" :size="14" />{{ sending ? '发送中' : '发送' }}
        </button>
      </div>
    </form>
  </section>
</template>

<style scoped>
.chat {
  position: relative;
  min-height: 0;
  height: min(78vh, 820px);
}
.chat-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}
.chat-empty {
  margin: auto 0;
}
.chat-turn {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}
.chat-user {
  align-self: flex-end;
  max-width: 85%;
  padding: var(--space-2) var(--space-3);
  background: var(--accent-soft);
  border-radius: var(--radius-md);
}
.chat-jump {
  position: absolute;
  bottom: 150px;
  left: 50%;
  transform: translateX(-50%);
  box-shadow: var(--shadow-float);
}
.chat-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.chat-files {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}
</style>
