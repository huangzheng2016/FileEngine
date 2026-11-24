<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import Alert from '@/base/Alert.vue'
import EmptyState from '@/base/EmptyState.vue'
import Loading from '@/base/Loading.vue'
import Tabs from '@/base/Tabs.vue'
import { useRouteQuery } from '@/base/useRouteQuery'
import ChatPanel from '@/components/ChatPanel.vue'
import { executable } from '@/lib/sessionView'
import { useMedia } from '@/lib/useMedia'
import { useSessionActions } from '@/lib/useSessionActions'
import { useSessionDetail } from '@/lib/useSessionDetail'
import ExecuteDialog from './ExecuteDialog.vue'
import GoalDialog from './session/GoalDialog.vue'
import SessionHead from './session/SessionHead.vue'
import SessionFiles from './SessionFiles.vue'
import SessionOverview from './SessionOverview.vue'
import SessionPlan from './SessionPlan.vue'
import SessionProgress from './SessionProgress.vue'

const route = useRoute()
const id = computed(() => String(route.params.id))
const d = useSessionDetail(id)
const { session: s } = d

// The chat sits beside every tab on wide screens and is a tab of its own otherwise.
const wide = useMedia('(min-width: 1280px)')
const TABS_ALL = [
  { key: 'overview', label: '概览' },
  { key: 'files', label: '全部文件' },
  { key: 'plan', label: '整理方案' },
  { key: 'runs', label: '运行记录' },
  { key: 'chat', label: '助手' },
] as const
type TabKey = (typeof TABS_ALL)[number]['key']
const tabs = computed(() => TABS_ALL.filter((t) => t.key !== 'chat' || !wide.value))
const { query, set } = useRouteQuery({ tab: '', focus: '' })
const tab = computed<TabKey>({
  get: () => tabs.value.find((t) => t.key === query.value.tab)?.key ?? 'overview',
  set: (t) => set({ tab: t === 'overview' ? '' : t }),
})
const showOverview = () => set({ tab: '', focus: '' })
const openReview = () => set({ tab: '', focus: 'review' })

const executing = ref(false)
const goalOpen = ref(false)
const refresh = () => {
  void d.detail.refresh()
  void d.runs.refresh()
}

const actions = useSessionActions({
  id,
  session: s,
  running: d.running,
  busyReason: d.busyReason,
  refresh,
  onTagging: showOverview,
  openGoal: () => (goalOpen.value = true),
})

function doNext() {
  const n = d.next.value
  if (n.kind === 'execute') executing.value = true
  else if (n.kind === 'review') openReview()
  else if (n.kind !== 'none') void actions.start(n.kind)
}

const pending = computed(() => (s.value ? executable(s.value) : 0))
const started = () => {
  showOverview()
  void d.runs.refresh()
}
</script>

<template>
  <div class="page">
    <Loading v-if="!s && d.detail.loading.value" />
    <EmptyState
      v-else-if="!s && d.detail.error.value"
      icon="alert-triangle"
      title="加载失败"
      :hint="d.detail.error.value"
    >
      <button type="button" @click="d.detail.refresh">重试</button>
    </EmptyState>
    <template v-else-if="s">
      <div :class="{ 'with-chat': wide }">
        <div class="main-col">
          <SessionHead
            :step="d.step.value"
            :running="!!d.running.value"
            :stopping="actions.stopping.value"
            :busy="actions.busy.value"
            :next="d.next.value"
            :menu="actions.menu.value"
            @stop="actions.stop"
            @next="doNext"
          />
          <p class="text-muted text-sm truncate">
            {{ d.storage.value?.name ?? '—'
            }}<template v-if="s.goal && d.stage.value !== 'ready'"> · {{ s.goal }}</template>
          </p>
          <Alert v-if="d.stream.error.value" tone="warning" title="实时进展已断开">
            {{ d.stream.error.value }}
            <template #action>
              <button type="button" class="btn-sm" @click="d.stream.reconnect()">重新连接</button>
            </template>
          </Alert>
          <Tabs v-model="tab" :items="tabs">
            <template #overview>
              <SessionOverview
                :session="s"
                :runs="d.runs.data.value ?? []"
                :timeline="d.stream.timeline.value"
                :has-draft="d.hasDraft.value"
                @changed="refresh"
                @tag="actions.start('tag')"
                @goal="goalOpen = true"
                @tab="tab = $event as TabKey"
              />
            </template>
            <template #runs>
              <SessionProgress
                :session-id="id"
                :runs="d.runs.data.value ?? []"
                :runs-error="d.runs.error.value"
                :timeline="d.stream.timeline.value"
                @changed="d.runs.refresh"
                @retry="d.runs.refresh"
              />
            </template>
            <template #plan>
              <SessionPlan
                :session-id="id"
                :rev="d.planRev.value"
                :busy-reason="d.busyReason.value"
                @started="started"
              />
            </template>
            <template #files>
              <SessionFiles :session="s" @changed="d.detail.refresh" />
            </template>
            <template #chat>
              <ChatPanel
                bare
                :session-id="id"
                :timeline="d.stream.timeline.value"
                :busy-reason="d.busyReason.value"
                @sent="d.runs.refresh"
              />
            </template>
          </Tabs>
        </div>
        <ChatPanel
          v-if="wide"
          :session-id="id"
          :timeline="d.stream.timeline.value"
          :busy-reason="d.busyReason.value"
          @sent="d.runs.refresh"
        />
      </div>

      <ExecuteDialog
        :open="executing"
        :session-id="id"
        :pending="pending"
        :review="s.review_count"
        :resume="s.executed_ops > 0"
        @close="executing = false"
        @review="((executing = false), openReview())"
        @started="((executing = false), started())"
      />
      <GoalDialog
        :open="goalOpen"
        :session-id="id"
        :goal="s.goal"
        @close="goalOpen = false"
        @saved="d.detail.refresh"
      />
    </template>
  </div>
</template>

<style scoped>
.main-col {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
}
.with-chat {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 380px;
  gap: var(--space-4);
  align-items: start;
}
</style>
