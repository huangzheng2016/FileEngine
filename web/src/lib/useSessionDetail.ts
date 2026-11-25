import { computed, ref, watch, type Ref } from 'vue'
import { useFetch } from '@/base/useFetch'
import { usePageTitle } from '@/base/usePageTitle'
import { usePolling } from '@/base/usePolling'
import { getPlan, listRuns } from '@/api/runs'
import { getSession } from '@/api/sessions'
import { listFilesystems } from '@/api/settings'
import type { Id } from '@/api/types'
import { flowRun, flowStep, nextAction, runningRun, sessionPath, stageOf } from './sessionView'
import { useSessionStream } from './useSessionStream'

// Everything the task page reads: the session, its runs and plan, the live stream, and what follows from them.
export function useSessionDetail(id: Ref<Id>) {
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

  const session = computed(() => detail.data.value)
  const storages = useFetch(listFilesystems, { immediate: true })
  const storage = computed(() => storages.data.value?.find((f) => f.id === session.value?.filesystem_id))
  usePageTitle(() => (session.value ? sessionPath(session.value) : undefined))

  const stream = useSessionStream(id, {
    onRun: () => {
      void runs.refresh()
      void detail.refresh()
    },
    onPlan: () => planRev.value++,
    onSession: () => void detail.refresh(),
  })

  // The stream is the source of freshness; polling covers the time it is down, and a scan, whose progress is
  // written to the session row without an event.
  usePolling(
    async () => {
      await Promise.all([detail.refresh(), runs.refresh()])
      if (detail.error.value) throw new Error(detail.error.value)
    },
    {
      interval: 3000,
      enabled: () => !!session.value && (!stream.connected.value || session.value.status === 'scanning'),
    },
  )

  const running = computed(() => runningRun(runs.data.value ?? []))
  const stage = computed(() => (session.value ? stageOf(session.value, flowRun(running.value)) : 'ready'))
  const step = computed(() => flowStep(stage.value))
  const hasDraft = computed(() => plan.data.value?.status === 'draft' && !!plan.data.value.content.trim())
  const next = computed(() =>
    session.value ? nextAction(session.value, running.value, hasDraft.value) : { kind: 'none' as const, label: '' },
  )
  const busyReason = computed(() => {
    if (session.value?.status === 'scanning') return '正在扫描，完成后可用'
    return running.value ? '正在运行，结束后可用' : ''
  })

  return { detail, runs, plan, planRev, session, storage, stream, running, stage, step, hasDraft, next, busyReason }
}
