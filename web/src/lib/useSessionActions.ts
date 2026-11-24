import { computed, ref, type ComputedRef, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { MenuItem } from '@/base/ActionMenu.vue'
import { linkFrom } from '@/base/backLink'
import { toast } from '@/base/toast'
import { useConfirm } from '@/base/useConfirm'
import { approvePlan, exportPlanOps, stopRun } from '@/api/runs'
import { deleteSession, rescanSession, startTagging } from '@/api/sessions'
import type { Id, Run, Session } from '@/api/types'
import { sessionPath, type NextKind } from './sessionView'

export interface SessionActionsContext {
  id: Ref<Id>
  session: ComputedRef<Session | null>
  running: ComputedRef<Run | undefined>
  busyReason: ComputedRef<string>
  // the page refetches the session and its runs after a mutation
  refresh: () => void
  // tagging started: the page shows the task's overview
  onTagging: () => void
  openGoal: () => void
}

export function useSessionActions(ctx: SessionActionsContext) {
  const route = useRoute()
  const router = useRouter()
  const confirm = useConfirm()
  const busy = ref(false)
  const stopping = ref(false)
  const exporting = ref(false)

  async function start(kind: Extract<NextKind, 'rescan' | 'tag' | 'plan_tag'>) {
    if (busy.value) return
    busy.value = true
    try {
      if (kind === 'rescan') {
        await rescanSession(ctx.id.value)
        toast.success('已开始扫描')
      } else {
        if (kind === 'plan_tag') await approvePlan(ctx.id.value)
        await startTagging(ctx.id.value)
        toast.success('已开始整理')
        ctx.onTagging()
      }
      ctx.refresh()
    } catch (e) {
      toast.error(kind === 'rescan' ? '重新扫描失败' : '开始整理失败', e)
    } finally {
      busy.value = false
    }
  }

  async function stop() {
    const r = ctx.running.value
    if (!r || stopping.value) return
    stopping.value = true
    try {
      await stopRun(r.id)
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
    if (ok) await start('rescan')
  }

  async function remove() {
    const s = ctx.session.value
    if (!s) return
    const ok = await confirm({
      message: `删除「${sessionPath(s)}」的整理任务？扫描结果和整理方案会一并删除。`,
      confirmText: '删除',
      danger: true,
    })
    if (!ok) return
    try {
      await deleteSession(ctx.id.value)
      toast.success('已删除')
      await router.replace('/sessions')
    } catch (e) {
      toast.error('删除失败', e)
    }
  }

  async function exportMoves() {
    if (exporting.value) return
    exporting.value = true
    try {
      await exportPlanOps(ctx.id.value, `移动清单-${ctx.id.value}.csv`)
      toast.success('已导出')
    } catch (e) {
      toast.error('导出失败', e)
    } finally {
      exporting.value = false
    }
  }

  const menu = computed<MenuItem[]>(() => [
    { label: '修改整理要求', icon: 'pencil', onSelect: ctx.openGoal },
    {
      label: '这个存储的记忆',
      icon: 'bookmark',
      onSelect: () =>
        void router.push(
          linkFrom('/memory', route, { scope: 'filesystem', fs: String(ctx.session.value?.filesystem_id ?? '') }),
        ),
    },
    {
      label: '重新扫描',
      icon: 'refresh-cw',
      disabled: !!ctx.busyReason.value,
      title: ctx.busyReason.value || undefined,
      onSelect: () => void rescan(),
    },
    {
      label: '导出移动清单',
      icon: 'download',
      disabled: !ctx.session.value?.planned_ops,
      title: ctx.session.value?.planned_ops ? undefined : '还没有待移动的文件',
      onSelect: () => void exportMoves(),
    },
    { label: '删除任务', icon: 'trash-2', danger: true, onSelect: () => void remove() },
  ])

  return { busy, stopping, exporting, start, stop, menu }
}
