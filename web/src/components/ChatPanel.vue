<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import { toast } from '@/base/toast'
import { vTip } from '@/base/tooltip'
import { useFetch } from '@/base/useFetch'
import { listChat, sendChat, stopRun } from '@/api/runs'
import type { Id } from '@/api/types'
import { chatFiles, clearChatFiles, removeChatFile } from '@/lib/chatFiles'
import { toolLabel } from '@/lib/toolView'
import { createLaneBlocks, type Block, type Item, type Timeline } from '@/lib/transcript'
import StreamMarkdown from './StreamMarkdown.vue'
import TranscriptBlock from './TranscriptBlock.vue'
import { provideOpenState } from './openState'

// bare: rendered inside the 助手 tab, which already names it.
const props = defineProps<{ sessionId: Id; timeline: Timeline; busyReason: string; bare?: boolean }>()
const emit = defineEmits<{ sent: [runId: Id] }>()

const turns = useFetch((signal) => listChat(props.sessionId, signal), { immediate: true })
const draft = ref('')
const sending = ref(false)
const sentText = ref(new Map<Id, string>())
const files = computed(() => chatFiles(props.sessionId))

provideOpenState()

const liveRuns = computed(() =>
  props.timeline.order.map((id) => props.timeline.runs[id]).filter((r) => r && r.phase === 'chat'),
)
// a string, so frames that change nothing about which runs are live recompute nothing
const liveKey = computed(() => liveRuns.value.map((r) => r.runId).join(','))

interface Entry {
  key: string
  blocks: Block[]
  // past turns: the user's message and the final answer only
  user?: string
  answer?: string
}

// Past turns only depend on the persisted turns and on which runs are live.
const pastEntries = computed<Entry[]>(() => {
  const live = new Set(liveKey.value ? liveKey.value.split(',') : [])
  const byRun = new Map<Id, { user?: string; answer?: string }>()
  for (const t of turns.data.value ?? []) {
    if (live.has(String(t.run_id))) continue
    const e = byRun.get(t.run_id) ?? {}
    if (t.role === 'user') e.user ??= t.content
    else if (t.role === 'assistant') e.answer ??= t.content
    byRun.set(t.run_id, e)
  }
  return [...byRun].map(([runId, e]) => ({ key: `t-${runId}`, blocks: [], ...e }))
})

// One incremental builder per live run: a frame only rebuilds the stretch that changed.
const builders = new Map<Id, ReturnType<typeof createLaneBlocks>>()
function blocksOf(runId: Id, items: readonly Item[]): Block[] {
  let build = builders.get(runId)
  if (!build) builders.set(runId, (build = createLaneBlocks(true)))
  return build(items)
}

const entries = computed<Entry[]>(() => {
  const out = [...pastEntries.value]
  for (const r of liveRuns.value) {
    const items = r.lanes.flatMap((l) => l.items)
    const hasPrompt = items.some((i) => i.kind === 'message' && i.role === 'user')
    const prompt = hasPrompt
      ? undefined
      : (sentText.value.get(r.runId) ??
        turns.data.value?.find((t) => t.run_id === r.runId && t.role === 'user')?.content)
    out.push({ key: `r-${r.runId}`, blocks: blocksOf(r.runId, items), user: prompt })
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

// The live turn and, in words, what it is doing: the running tool, else thinking or writing.
const current = computed(() => liveRuns.value.find((r) => !r.done))
const status = computed(() => {
  const r = current.value
  if (!r) return ''
  const items = r.lanes.flatMap((l) => l.items)
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i]
    if (it.kind === 'tool') return it.status === 'running' ? `正在${toolLabel(it.name)}` : '正在思考'
    if (it.kind === 'message' && it.role === 'assistant') return it.streaming && it.text ? '正在回复' : '正在思考'
  }
  return '正在思考'
})
const stopping = ref(false)
async function stop() {
  const r = current.value
  if (!r || stopping.value) return
  stopping.value = true
  try {
    await stopRun(r.runId)
  } catch (e) {
    toast.error('停止失败', e)
  } finally {
    stopping.value = false
  }
}

// a chat turn of its own is not a reason to block the box: it shows the stop button instead
const blocked = computed(() => (current.value ? '' : props.busyReason || ''))

const SUGGESTIONS = [
  '截图都放到 /图片/截图',
  '照片按 年/月 归档，保留原文件夹名',
  '还有哪些文件没有归类？',
  '解释一下现在的整理方案',
]
const input = ref<HTMLTextAreaElement | null>(null)
function suggest(text: string) {
  draft.value = text
  input.value?.focus()
}
function retry(text: string | undefined) {
  if (text) suggest(text)
}

async function send() {
  const text = draft.value.trim()
  if (!text || sending.value || blocked.value || current.value) return
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
  <section class="chat card" :class="{ docked: !bare }" aria-label="助手">
    <div v-if="!bare" class="card-head">
      <h3 class="with-icon"><Icon name="message-square" :size="16" />助手</h3>
    </div>
    <div ref="box" class="chat-list" @scroll.passive="onScroll">
      <Loading v-if="!turns.loaded.value" />
      <p v-else-if="turns.error.value" class="text-danger">
        加载对话失败：{{ turns.error.value }}
        <button type="button" class="btn-sm" @click="turns.refresh()">重试</button>
      </p>
      <div v-else-if="!entries.length" class="chat-empty">
        <span class="chat-empty-icon"><Icon name="message-square" :size="20" /></span>
        <p class="chat-empty-title">告诉助手你想怎么整理</p>
        <p class="text-sm text-muted">它会调整目标路径、分类、方案和规则，不会移动文件；执行前你都能复查。</p>
        <div class="chat-suggest">
          <button v-for="t in SUGGESTIONS" :key="t" type="button" class="chat-suggest-item" @click="suggest(t)">
            {{ t }}
          </button>
        </div>
      </div>
      <div v-for="e in entries" :key="e.key" class="chat-turn">
        <div v-if="e.user" class="chat-user">
          <StreamMarkdown :source="e.user" />
        </div>
        <div class="chat-reply">
          <TranscriptBlock v-for="b in e.blocks" :key="b.key" :block="b" :scope="e.key" />
          <StreamMarkdown v-if="e.answer" :source="e.answer" />
        </div>
        <div v-if="errorOf(e.key)" class="chat-error text-sm" role="alert">
          <Icon name="alert-triangle" :size="14" />
          <span class="chat-error-text">{{ errorOf(e.key) }}</span>
          <button v-if="e.user" type="button" class="btn-sm" @click="retry(e.user)">重新编辑</button>
        </div>
      </div>
    </div>
    <button v-if="!following" type="button" class="btn-sm chat-jump" @click="toBottom">
      <Icon name="arrow-down" :size="14" />跳到最新
    </button>
    <p v-if="status" class="chat-status text-sm" role="status" aria-live="polite">
      <Loading inline label="" :delay="0" />{{ stopping ? '正在停止' : status }}
    </p>
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
        ref="input"
        v-model="draft"
        rows="3"
        name="message"
        class="chat-input"
        aria-label="发给助手的消息"
        :placeholder="files.length ? '对选中的文件说点什么，例如：都放到 /文档/合同' : '例如：截图都放到 /图片/截图'"
        @keydown="onKey"
      />
      <div class="chat-actions">
        <span class="chat-hint">Enter 发送，Shift + Enter 换行</span>
        <span class="spacer" />
        <button
          v-if="current"
          v-tip="'停止这次回复'"
          type="button"
          class="danger btn-sm btn-icon"
          aria-label="停止"
          :disabled="stopping"
          @click="stop"
        >
          <Icon name="square" :size="14" />
        </button>
        <button
          v-else
          v-tip="blocked || '发送'"
          type="submit"
          class="primary btn-sm btn-icon"
          aria-label="发送"
          :disabled="sending || !!blocked || !draft.trim()"
        >
          <Loading v-if="sending" inline label="" :delay="0" />
          <Icon v-else name="send" :size="14" />
        </button>
      </div>
    </form>
  </section>
</template>

<style scoped>
.chat {
  position: relative;
  min-height: 360px;
  height: calc(100dvh - var(--header-h) - 240px);
}
.chat.docked {
  position: sticky;
  top: 0;
  height: calc(100dvh - var(--header-h) - 2 * var(--space-5));
}
@media (max-width: 760px) {
  .chat {
    height: auto;
  }
  .chat .chat-list {
    overflow: visible;
  }
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
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  margin: auto 0;
  padding: var(--space-4) var(--space-2);
  text-align: center;
}
.chat-empty p {
  margin: 0;
}
.chat-empty-icon {
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  color: var(--accent);
  background: var(--accent-soft);
  border-radius: var(--radius-md);
}
.chat-empty-title {
  font-weight: 500;
}
.chat-suggest {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: var(--space-2);
  margin-top: var(--space-2);
}
.chat-suggest-item {
  font-size: var(--text-sm);
  font-weight: 400;
  color: var(--text-secondary);
  background: var(--surface);
  border-radius: 999px;
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
  border: 1px solid var(--border);
  border-radius: var(--radius-md) var(--radius-md) var(--radius-sm) var(--radius-md);
  overflow-wrap: anywhere;
}
.chat-reply {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}
.chat-reply:empty {
  display: none;
}
.chat-error {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  color: var(--danger);
  background: var(--danger-soft);
  border-radius: var(--radius-md);
}
.chat-error-text {
  flex: 1;
  min-width: 0;
}
.chat-jump {
  position: absolute;
  bottom: 150px;
  left: 50%;
  transform: translateX(-50%);
  box-shadow: var(--shadow-float);
}
.chat-status {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  color: var(--text-secondary);
}
.chat-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2);
  background: var(--surface-raised);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-md);
  transition: border-color var(--dur-fast) var(--ease);
}
.chat-form:focus-within {
  border-color: var(--accent);
}
.chat-input {
  min-height: 56px;
  max-height: 160px;
  padding: 0 var(--space-1);
  resize: none;
  background: transparent;
  border: 0;
  box-shadow: none;
}
.chat-input:focus {
  outline: none;
  box-shadow: none;
}
.chat-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.chat-hint {
  font-size: var(--text-xs);
  color: var(--text-muted);
}
.chat-files {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}
</style>
