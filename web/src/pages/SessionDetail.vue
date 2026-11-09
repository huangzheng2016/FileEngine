<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import ActionMenu, { type MenuItem } from '@/base/ActionMenu.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import EmptyState from '@/base/EmptyState.vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import Steps from '@/base/Steps.vue'
import Tabs from '@/base/Tabs.vue'
import { toast } from '@/base/toast'
import { useConfirm } from '@/base/useConfirm'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFetch } from '@/base/useFetch'
import { useFormValidation } from '@/base/useFormValidation'
import { usePageTitle } from '@/base/usePageTitle'
import { usePolling } from '@/base/usePolling'
import { useRouteQuery } from '@/base/useRouteQuery'
import { download } from '@/api/http'
import { approvePlan, getPlan, listRuns, stopRun } from '@/api/runs'
import { deleteSession, getSession, plansExportUrl, rescanSession, startTagging, updateSession } from '@/api/sessions'
import { listFilesystems } from '@/api/settings'
import ChatPanel from '@/components/ChatPanel.vue'
import {
  FLOW_STEPS,
  executable,
  flowRun,
  flowStep,
  nextAction,
  runningRun,
  sessionPath,
  stageOf,
} from '@/lib/sessionView'
import { useMedia } from '@/lib/useMedia'
import { useSessionStream } from '@/lib/useSessionStream'
import ExecuteDialog from './ExecuteDialog.vue'
import SessionFiles from './SessionFiles.vue'
import SessionOverview from './SessionOverview.vue'
import SessionPlan from './SessionPlan.vue'
import SessionProgress from './SessionProgress.vue'

const route = useRoute()
const router = useRouter()
const confirm = useConfirm()

const id = computed(() => Number(route.params.id))
const detail = useFetch((signal) => getSession(id.value, signal))
const runs = useFetch((signal) => listRuns(id.value, signal))
const plan = useFetch((signal) => getPlan(id.value, signal))
const planRev = ref(0)
watch(
  id,
  () => {
    void detail.refresh()
    void runs.refresh()
  },
  { immediate: true },
)
watch([id, planRev], () => void plan.refresh(), { immediate: true })
const s = computed(() => detail.data.value)
const storages = useFetch(listFilesystems, { immediate: true })
const storage = computed(() => storages.data.value?.find((f) => f.id === s.value?.filesystem_id))
usePageTitle(() => (s.value ? sessionPath(s.value) : undefined))

const stream = useSessionStream(id, {
  onRun: () => {
    void runs.refresh()
    void detail.refresh()
  },
  onPlan: () => planRev.value++,
  onSession: () => void detail.refresh(),
})

usePolling(
  async () => {
    await detail.refresh()
    if (detail.error.value) throw new Error(detail.error.value)
  },
  { interval: 2000, enabled: () => s.value?.status === 'scanning' },
)

const running = computed(() => runningRun(runs.data.value ?? []))
const stage = computed(() => (s.value ? stageOf(s.value, flowRun(running.value)) : 'ready'))
const step = computed(() => flowStep(stage.value))
const hasDraft = computed(() => plan.data.value?.status === 'draft' && !!plan.data.value.content.trim())
const next = computed(() => (s.value ? nextAction(s.value, running.value, hasDraft.value) : null))
const busyReason = computed(() => {
  if (s.value?.status === 'scanning') return '正在扫描，完成后可用'
  return running.value ? '正在运行，结束后可用' : ''
})

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
const { query, set } = useRouteQuery({ tab: '' })
const tab = computed<TabKey>({
  get: () => tabs.value.find((t) => t.key === query.value.tab)?.key ?? 'overview',
  set: (t) => set({ tab: t === 'overview' ? '' : t }),
})
async function openReview() {
  tab.value = 'overview'
  await nextTick()
  document.querySelector('[aria-label="需要你确认"]')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

const executing = ref(false)
const busy = ref(false)

function doNext() {
  const n = next.value
  if (!n) return
  if (n.kind === 'execute') executing.value = true
  else if (n.kind === 'review') void openReview()
  else if (n.kind !== 'none') void start(n.kind)
}

const doTag = () => void start('tag')

async function start(kind: 'rescan' | 'tag' | 'plan_tag') {
  if (busy.value) return
  busy.value = true
  try {
    if (kind === 'rescan') {
      await rescanSession(id.value)
      toast.success('已开始扫描')
    } else {
      if (kind === 'plan_tag') await approvePlan(id.value)
      await startTagging(id.value)
      toast.success('已开始整理')
      tab.value = 'overview'
    }
    void runs.refresh()
    void detail.refresh()
  } catch (e) {
    toast.error(kind === 'rescan' ? '重新扫描失败' : '开始整理失败', e)
  } finally {
    busy.value = false
  }
}

const stopping = ref(false)
async function stop() {
  if (!running.value || stopping.value) return
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

async function rescan() {
  const ok = await confirm({
    title: '重新扫描',
    message: '重新扫描会更新文件列表，没有变化的文件保留已有的整理决定。',
    confirmText: '重新扫描',
  })
  if (!ok || busy.value) return
  busy.value = true
  try {
    await rescanSession(id.value)
    toast.success('已开始扫描')
    void detail.refresh()
  } catch (e) {
    toast.error('重新扫描失败', e)
  } finally {
    busy.value = false
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

const exporting = ref(false)
async function exportMoves() {
  if (exporting.value) return
  exporting.value = true
  try {
    await download(plansExportUrl(id.value), `移动清单-${id.value}.csv`)
  } catch (e) {
    toast.error('导出失败', e)
  } finally {
    exporting.value = false
  }
}

const menu = computed<MenuItem[]>(() => [
  { label: '修改整理要求', icon: 'pencil' as const, onSelect: () => openGoal() },
  {
    label: '这个存储的记忆',
    icon: 'bookmark' as const,
    onSelect: () =>
      void router.push({ path: '/memory', query: { scope: 'filesystem', fs: String(s.value?.filesystem_id ?? '') } }),
  },
  {
    label: '重新扫描',
    icon: 'refresh-cw' as const,
    disabled: !!busyReason.value,
    title: busyReason.value || undefined,
    onSelect: () => void rescan(),
  },
  {
    label: '导出移动清单',
    icon: 'download' as const,
    disabled: !s.value?.planned_ops,
    title: s.value?.planned_ops ? undefined : '还没有待移动的文件',
    onSelect: () => void exportMoves(),
  },
  { label: '删除任务', icon: 'trash-2' as const, danger: true, onSelect: () => void remove() },
])

const goalForm = reactive({ open: false, text: '', saving: false })
const goalGuard = useDirtyGuard(() => goalForm.text)
const goalCheck = useFormValidation(() => ({
  goal: goalForm.text.trim().length > 1000 ? '整理要求不超过 1000 个字符' : '',
}))
function openGoal() {
  goalForm.text = s.value?.goal ?? ''
  goalGuard.reset()
  goalCheck.reset()
  goalForm.open = true
}
async function closeGoal() {
  if (await goalGuard.confirmDiscard()) goalForm.open = false
}
async function saveGoal() {
  if (!goalCheck.validate() || goalForm.saving) return
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

const pending = computed(() => (s.value ? executable(s.value) : 0))
</script>

<template>
  <div class="page">
    <Loading v-if="!s && detail.loading.value" />
    <EmptyState v-else-if="!s && detail.error.value" icon="alert-triangle" title="加载失败" :hint="detail.error.value">
      <button type="button" @click="detail.refresh">重试</button>
    </EmptyState>
    <template v-else-if="s">
      <div :class="{ 'with-chat': wide }">
        <div class="main-col">
          <div class="toolbar session-head">
            <Steps class="flow" :steps="FLOW_STEPS" :index="step" :reached="step" locked />
            <span class="head-actions">
              <button v-if="running" type="button" :disabled="stopping" @click="stop">
                <Icon name="stop" />{{ stopping ? '停止中' : '停止' }}
              </button>
              <button
                v-if="next && next.kind !== 'none'"
                type="button"
                class="primary"
                :disabled="busy"
                @click="doNext"
              >
                <Icon :name="next.kind === 'review' ? 'eye' : 'play'" />{{ next.label }}
              </button>
              <ActionMenu :items="menu" />
            </span>
          </div>
          <p class="text-muted text-sm truncate">
            {{ storage?.name ?? '—' }}<template v-if="s.goal && stage !== 'ready'"> · {{ s.goal }}</template>
          </p>
          <p v-if="stream.error.value" class="text-danger text-sm">实时进展连接中断：{{ stream.error.value }}</p>
          <Tabs v-model="tab" :items="tabs">
            <template #overview>
              <SessionOverview
                :session="s"
                :runs="runs.data.value ?? []"
                :timeline="stream.timeline.value"
                :has-draft="hasDraft"
                @changed="(detail.refresh(), runs.refresh())"
                @tag="doTag"
                @goal="openGoal"
                @tab="tab = $event as TabKey"
              />
            </template>
            <template #runs>
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
                @started="((tab = 'overview'), runs.refresh())"
              />
            </template>
            <template #files>
              <SessionFiles :session="s" @changed="detail.refresh" />
            </template>
            <template #chat>
              <ChatPanel
                bare
                :session-id="id"
                :timeline="stream.timeline.value"
                :busy-reason="busyReason"
                @sent="runs.refresh"
              />
            </template>
          </Tabs>
        </div>
        <ChatPanel
          v-if="wide"
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
        :review="s.review_count"
        :resume="s.executed_ops > 0"
        @close="executing = false"
        @review="((executing = false), openReview())"
        @started="((tab = 'overview'), runs.refresh())"
      />

      <BaseDialog :open="goalForm.open" title="修改整理要求" width="520px" @close="closeGoal">
        <form id="goal-form" class="form" novalidate v-on="goalCheck.form" @submit.prevent="saveGoal">
          <label class="field">
            <textarea
              v-model="goalForm.text"
              rows="4"
              name="goal"
              aria-label="整理要求"
              placeholder="例如：照片按年份整理，项目文件夹保持完整"
              :aria-invalid="!!goalCheck.error('goal')"
            />
            <span v-if="goalCheck.error('goal')" class="field-error">{{ goalCheck.error('goal') }}</span>
          </label>
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
.session-head {
  flex-wrap: wrap;
  row-gap: var(--space-3);
}
.flow {
  flex: 1 1 360px;
  min-width: 0;
}
.head-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-left: auto;
}
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
