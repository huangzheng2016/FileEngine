<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import ActionMenu, { type MenuItem } from '@/base/ActionMenu.vue'
import Alert from '@/base/Alert.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import EmptyState from '@/base/EmptyState.vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import StatusPill from '@/base/StatusPill.vue'
import Tabs from '@/base/Tabs.vue'
import { fmtNumber } from '@/base/format'
import { SESSION_STAGE } from '@/base/labels'
import { toast } from '@/base/toast'
import { useConfirm } from '@/base/useConfirm'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFetch } from '@/base/useFetch'
import { usePageTitle } from '@/base/usePageTitle'
import { usePolling } from '@/base/usePolling'
import { useRouteQuery } from '@/base/useRouteQuery'
import { download } from '@/api/http'
import { draftPlan, listRuns, stopRun } from '@/api/runs'
import { deleteSession, getSession, plansExportUrl, rescanSession, startTagging, updateSession } from '@/api/sessions'
import { listFilesystems } from '@/api/settings'
import ChatPanel from '@/components/ChatPanel.vue'
import { nextAction, runningRun, sessionPath, stageOf } from '@/lib/sessionView'
import { useMedia } from '@/lib/useMedia'
import { useSessionStream } from '@/lib/useSessionStream'
import ExecuteDialog from './ExecuteDialog.vue'
import MemoryPanel from './MemoryPanel.vue'
import SessionFiles from './SessionFiles.vue'
import SessionPlan from './SessionPlan.vue'
import SessionPreview from './SessionPreview.vue'
import SessionProgress from './SessionProgress.vue'

const route = useRoute()
const router = useRouter()
const confirm = useConfirm()

const id = computed(() => Number(route.params.id))
const detail = useFetch((signal) => getSession(id.value, signal))
const runs = useFetch((signal) => listRuns(id.value, signal))
watch(
  id,
  () => {
    void detail.refresh()
    void runs.refresh()
  },
  { immediate: true },
)
const s = computed(() => detail.data.value)
const storages = useFetch(listFilesystems, { immediate: true })
const storage = computed(() => storages.data.value?.find((f) => f.id === s.value?.filesystem_id))
usePageTitle(() => (s.value ? sessionPath(s.value) : undefined))

const planRev = ref(0)
const memoryRev = ref(0)
const stream = useSessionStream(id, {
  onRun: () => {
    void runs.refresh()
    void detail.refresh()
  },
  onPlan: () => planRev.value++,
  onMemory: () => memoryRev.value++,
})

usePolling(
  async () => {
    await detail.refresh()
    if (detail.error.value) throw new Error(detail.error.value)
  },
  { interval: 2000, enabled: () => s.value?.status === 'scanning' },
)

const wide = useMedia('(min-width: 1280px)')
const TABS_ALL = [
  { key: 'progress', label: '进展' },
  { key: 'plan', label: '方案' },
  { key: 'preview', label: '预览' },
  { key: 'files', label: '文件' },
  { key: 'memory', label: '记忆' },
  { key: 'chat', label: '助手' },
] as const
type TabKey = (typeof TABS_ALL)[number]['key']
const tabs = computed(() => TABS_ALL.filter((t) => t.key !== 'chat' || !wide.value))
const { query, set } = useRouteQuery({ tab: 'progress' })
const tab = computed<TabKey>({
  get: () => (tabs.value.some((t) => t.key === query.value.tab) ? (query.value.tab as TabKey) : 'progress'),
  set: (t) => set({ tab: t }),
})
const withChat = computed(() => wide.value && (tab.value === 'progress' || tab.value === 'preview'))

const running = computed(() => runningRun(runs.data.value ?? []))
const stage = computed(() => (s.value ? stageOf(s.value, running.value) : 'ready'))
const next = computed(() =>
  s.value ? nextAction(s.value, running.value ?? runs.data.value?.[0], tab.value === 'preview') : null,
)
const busyReason = computed(() => {
  if (s.value?.status === 'scanning') return '正在扫描，完成后可用'
  return running.value ? '正在运行，结束后可用' : ''
})

const executing = ref(false)
const busy = ref(false)

async function doNext() {
  const n = next.value
  if (!n || busy.value) return
  if (n.kind === 'preview') return void (tab.value = 'preview')
  if (n.kind === 'execute') return void (executing.value = true)
  busy.value = true
  try {
    if (n.kind === 'tag') {
      await startTagging(id.value)
      toast.success('已开始整理')
      tab.value = 'progress'
    } else if (n.kind === 'rescan') {
      await rescanSession(id.value)
      toast.success('已开始扫描')
    }
    void runs.refresh()
    void detail.refresh()
  } catch (e) {
    toast.error(n.kind === 'tag' ? '开始整理失败' : '扫描失败', e)
  } finally {
    busy.value = false
  }
}

const stopping = ref(false)
async function stop() {
  if (!running.value) return
  stopping.value = true
  try {
    await stopRun(running.value.id)
    toast.success('已请求停止')
  } catch (e) {
    toast.error('停止失败', e)
  } finally {
    stopping.value = false
  }
}

async function surveyFirst() {
  try {
    await draftPlan(id.value, '')
    toast.success('已开始出方案')
    tab.value = 'progress'
    void runs.refresh()
  } catch (e) {
    toast.error('出方案失败', e)
  }
}

async function rescan() {
  const ok = await confirm({
    title: '重新扫描',
    message: '重新扫描会更新文件列表，没有变化的文件保留已有的整理决定。',
    confirmText: '重新扫描',
  })
  if (!ok) return
  try {
    await rescanSession(id.value)
    toast.success('已开始扫描')
    void detail.refresh()
  } catch (e) {
    toast.error('扫描失败', e)
  }
}

async function remove() {
  if (!s.value) return
  const ok = await confirm({
    message: `删除「${sessionPath(s.value)}」的整理任务？扫描结果和整理方案会一并删除，存储里的文件不受影响。`,
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  try {
    await deleteSession(id.value)
    toast.success('已删除')
    await router.replace('/sessions')
  } catch (e) {
    toast.error('删除失败', e)
  }
}

async function exportPlan() {
  try {
    await download(plansExportUrl(id.value), `整理计划-${id.value}.csv`)
  } catch (e) {
    toast.error('导出失败', e)
  }
}

const menu = computed<MenuItem[]>(() => [
  {
    label: '先出方案',
    icon: 'list' as const,
    disabled: !!busyReason.value,
    title: busyReason.value || undefined,
    onSelect: () => void surveyFirst(),
  },
  { label: '修改整理目标', icon: 'pencil' as const, onSelect: () => openGoal() },
  {
    label: '重新扫描',
    icon: 'refresh-cw' as const,
    disabled: !!busyReason.value,
    title: busyReason.value || undefined,
    onSelect: () => void rescan(),
  },
  { label: '导出计划', icon: 'download' as const, onSelect: () => void exportPlan() },
  { label: '删除任务', icon: 'trash-2' as const, danger: true, onSelect: () => void remove() },
])

const goalForm = reactive({ open: false, text: '', saving: false })
const goalGuard = useDirtyGuard(() => goalForm.text)
function openGoal() {
  goalForm.text = s.value?.goal ?? ''
  goalGuard.reset()
  goalForm.open = true
}
async function closeGoal() {
  if (await goalGuard.confirmDiscard()) goalForm.open = false
}
async function saveGoal() {
  goalForm.saving = true
  try {
    await updateSession(id.value, { goal: goalForm.text.trim() })
    toast.success('已保存')
    goalGuard.reset()
    goalForm.open = false
    void detail.refresh()
  } catch (e) {
    toast.error('保存失败', e)
  } finally {
    goalForm.saving = false
  }
}

const pending = computed(() => (s.value ? Math.max(0, s.value.planned_ops - s.value.executed_ops) : 0))
</script>

<template>
  <div class="page">
    <Loading v-if="!s && detail.loading.value" />
    <EmptyState v-else-if="!s && detail.error.value" icon="alert-triangle" title="加载失败" :hint="detail.error.value">
      <button type="button" @click="detail.refresh">重试</button>
    </EmptyState>
    <template v-else-if="s">
      <Alert v-if="s.status === 'scanning'" tone="info">
        已发现 {{ fmtNumber(s.scanned_count) }} 个文件<template v-if="s.current_path"
          >，正在扫描 {{ s.current_path }}</template
        >
      </Alert>
      <Alert v-else-if="s.status === 'error'" tone="danger" title="扫描失败">{{ s.error_message || '—' }}</Alert>

      <div class="toolbar">
        <StatusPill :value="stage" :map="SESSION_STAGE" />
        <span class="text-muted text-sm tabular">
          {{ storage?.name ?? '—' }} · {{ fmtNumber(s.total_files) }} 个文件<template v-if="s.planned_ops">
            · 计划移动 {{ fmtNumber(s.planned_ops) }}</template
          ><template v-if="s.executed_ops"> · 已执行 {{ fmtNumber(s.executed_ops) }}</template>
        </span>
        <span class="spacer" />
        <button v-if="running" type="button" :disabled="stopping" @click="stop"><Icon name="stop" />停止</button>
        <button
          v-if="next && next.kind !== 'none'"
          type="button"
          class="primary"
          :disabled="busy || (next.kind === 'execute' && !!busyReason)"
          @click="doNext"
        >
          <Icon :name="next.kind === 'preview' ? 'eye' : 'play'" />{{ next.label }}
        </button>
        <ActionMenu :items="menu" />
      </div>
      <p v-if="s.goal" class="goal">{{ s.goal }}</p>
      <p v-if="stream.error.value" class="text-danger text-sm">实时进展连接中断：{{ stream.error.value }}</p>

      <div :class="{ 'with-chat': withChat }">
        <Tabs v-model="tab" :items="tabs">
          <template #progress>
            <SessionProgress
              :session-id="id"
              :runs="runs.data.value ?? []"
              :runs-error="runs.error.value"
              :timeline="stream.timeline.value"
              @changed="runs.refresh"
            />
          </template>
          <template #plan>
            <SessionPlan
              :session-id="id"
              :rev="planRev"
              :busy-reason="busyReason"
              @started="((tab = 'progress'), runs.refresh())"
            />
          </template>
          <template #preview>
            <SessionPreview :session-id="id" @changed="detail.refresh" />
          </template>
          <template #files>
            <SessionFiles :session="s" @changed="detail.refresh" />
          </template>
          <template #memory>
            <MemoryPanel :session-id="id" :filesystem-id="s.filesystem_id" :rev="memoryRev" />
          </template>
          <template #chat>
            <ChatPanel
              :session-id="id"
              :timeline="stream.timeline.value"
              :busy-reason="busyReason"
              @sent="runs.refresh"
            />
          </template>
        </Tabs>
        <ChatPanel
          v-if="withChat"
          :session-id="id"
          :timeline="stream.timeline.value"
          :busy-reason="busyReason"
          @sent="runs.refresh"
        />
      </div>

      <ExecuteDialog
        :open="executing"
        :session-id="id"
        :pending="pending"
        @close="executing = false"
        @started="((tab = 'progress'), runs.refresh())"
      />

      <BaseDialog :open="goalForm.open" title="修改整理目标" width="520px" @close="closeGoal">
        <form id="goal-form" class="form" @submit.prevent="saveGoal">
          <textarea
            v-model="goalForm.text"
            rows="4"
            name="goal"
            aria-label="整理目标"
            placeholder="例如：照片按年份整理，项目文件夹保持完整"
          />
        </form>
        <template #footer>
          <button type="button" @click="closeGoal">取消</button>
          <button type="submit" form="goal-form" class="primary" :disabled="goalForm.saving">
            {{ goalForm.saving ? '保存中' : '保存' }}
          </button>
        </template>
      </BaseDialog>
    </template>
  </div>
</template>

<style scoped>
.goal {
  margin-top: calc(-1 * var(--space-2));
  color: var(--text-secondary);
}
.with-chat {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 380px;
  gap: var(--space-4);
  align-items: start;
}
</style>
