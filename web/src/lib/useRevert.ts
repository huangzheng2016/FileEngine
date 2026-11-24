import { ref } from 'vue'
import { toast } from '@/base/toast'
import { useConfirm } from '@/base/useConfirm'
import { fmtNumber } from '@/base/format'
import { revertRun } from '@/api/sessions'
import { hasId, type Id, type Run } from '@/api/types'

// An execution can be undone while nothing has undone it yet.
export const canRevert = (r: Pick<Run, 'phase' | 'status' | 'reverted_by_run_id'> | undefined) =>
  !!r && r.phase === 'executing' && (r.status === 'done' || r.status === 'partial') && !hasId(r.reverted_by_run_id)

export function revertMessage(mode: Run['exec_mode'], success?: number) {
  const count = success ? ` ${fmtNumber(success)} 项` : '文件'
  return mode === 'copy' ? `会删除这次复制到新位置的${count}，原文件保留在原处。` : `会把这次移动的${count}放回原处。`
}

// The only revert entry point: copy-mode revert deletes the copies, so it is confirmed as dangerous.
export function useRevert(onStarted: (runId: Id) => void = () => {}) {
  const confirm = useConfirm()
  const reverting = ref(false)

  async function revert(run: Pick<Run, 'id' | 'exec_mode'>, success?: number) {
    if (reverting.value) return
    const copy = run.exec_mode === 'copy'
    const ok = await confirm({
      title: copy ? '撤销复制' : '撤销移动',
      message: revertMessage(run.exec_mode, success),
      confirmText: copy ? '删除复制的文件' : '放回原处',
      danger: copy,
    })
    if (!ok) return
    reverting.value = true
    try {
      const runId = await revertRun(run.id)
      toast.success('已开始撤销')
      onStarted(runId)
    } catch (e) {
      toast.error('撤销失败', e)
    } finally {
      reverting.value = false
    }
  }

  return { reverting, revert }
}
