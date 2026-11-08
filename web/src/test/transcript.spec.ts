import { describe, expect, it } from 'vitest'
import type { SessionEvent } from '@/api/types'
import { createEventBuffer } from '@/lib/eventBuffer'
import {
  applyEvents,
  emptyTimeline,
  laneBlocks,
  stepSummary,
  type MessageItem,
  type StepGroup,
  type ToolItem,
} from '@/lib/transcript'

let seq = 0
const ev = (type: string, data: Record<string, unknown>, s = ++seq): SessionEvent => ({ seq: s, type, data })

describe('transcript reducer', () => {
  it('accumulates text and reasoning deltas by msg_id and lets message finalize them', () => {
    seq = 0
    let t = applyEvents(emptyTimeline(), [
      ev('run_status', { run_id: 1, phase: 'chat', status: 'running' }),
      ev('reasoning', { run_id: 1, worker_id: 0, msg_id: 'm1', delta: '先看' }),
      ev('text', { run_id: 1, worker_id: 0, msg_id: 'm1', delta: '你' }),
      ev('text', { run_id: 1, worker_id: 0, msg_id: 'm1', delta: '好' }),
    ])
    const m = t.runs[1].lanes[0].items[0] as MessageItem
    expect(m).toMatchObject({ text: '你好', reasoning: '先看', streaming: true })

    t = applyEvents(t, [ev('message', { run_id: 1, worker_id: 0, msg_id: 'm1', role: 'assistant', content: '你好！' })])
    expect(t.runs[1].lanes[0].items).toHaveLength(1)
    expect(t.runs[1].lanes[0].items[0]).toMatchObject({ text: '你好！', reasoning: '先看', streaming: false })
  })

  it('ignores replayed events at or below the applied seq', () => {
    const a = ev('text', { run_id: 2, msg_id: 'x', delta: 'a' }, 10)
    const b = ev('text', { run_id: 2, msg_id: 'x', delta: 'b' }, 11)
    let t = applyEvents(emptyTimeline(), [a, b])
    const same = applyEvents(t, [a, b])
    expect(same).toBe(t)
    // an older event arriving late changes nothing
    t = applyEvents(t, [ev('text', { run_id: 2, msg_id: 'x', delta: 'z' }, 9)])
    expect((t.runs[2].lanes[0].items[0] as MessageItem).text).toBe('ab')
    expect(t.seq).toBe(11)
  })

  it('pairs tool results with calls by call_id across workers', () => {
    seq = 100
    const t = applyEvents(emptyTimeline(), [
      ev('tool_call', { run_id: 3, worker_id: 2, call_id: 'c1', name: 'list_files', args: { path: 'a' } }),
      ev('tool_call', { run_id: 3, worker_id: 1, call_id: 'c2', name: 'set_target', args: {} }),
      ev('tool_result', { run_id: 3, worker_id: 1, call_id: 'c2', name: 'set_target', ok: false, error: '路径非法' }),
      ev('tool_result', {
        run_id: 3,
        worker_id: 2,
        call_id: 'c1',
        name: 'list_files',
        ok: true,
        output: { files: [] },
        duration_ms: 12,
      }),
    ])
    const lanes = t.runs[3].lanes
    expect(lanes.map((l) => l.workerId)).toEqual([1, 2])
    expect(lanes[0].items[0]).toMatchObject({ id: 'c2', status: 'failed', error: '路径非法' })
    expect(lanes[1].items[0]).toMatchObject({ id: 'c1', status: 'ok', durationMs: 12 })
  })

  it('tracks pull units, progress and closes open steps when the run ends', () => {
    seq = 200
    const t = applyEvents(emptyTimeline(), [
      ev('run_status', { run_id: 4, phase: 'tagging', status: 'running' }),
      ev('pull_begin', { run_id: 4, worker_id: 1, unit_id: 'u1', label: '照片/2023', count: 30 }),
      ev('tool_call', { run_id: 4, worker_id: 1, call_id: 'c', name: 'read_file', args: {} }),
      ev('text', { run_id: 4, worker_id: 1, msg_id: 'm', delta: '处理中' }),
      ev('pull_done', { run_id: 4, worker_id: 1, unit_id: 'u1', tagged: 28, failed: 2 }),
      ev('progress', { run_id: 4, phase: 'tagging', total: 100, tagged: 28 }),
      ev('run_done', {
        run_id: 4,
        phase: 'tagging',
        status: 'stopped',
        tokens: { prompt: 1, cached: 0, completion: 2, total: 3 },
      }),
    ])
    const r = t.runs[4]
    expect(r).toMatchObject({ status: 'stopped', done: true, progress: { total: 100, tagged: 28 } })
    const items = r.lanes[0].items
    expect(items[0]).toMatchObject({ kind: 'unit', done: true, tagged: 28, failed: 2 })
    expect((items[1] as ToolItem).status).toBe('failed')
    expect((items[2] as MessageItem).streaming).toBe(false)
  })

  it('does not mutate the previous timeline', () => {
    seq = 300
    const t1 = applyEvents(emptyTimeline(), [ev('text', { run_id: 5, msg_id: 'm', delta: 'a' })])
    const snapshot = JSON.stringify(t1)
    applyEvents(t1, [ev('text', { run_id: 5, msg_id: 'm', delta: 'b' }), ev('tool_call', { run_id: 5, call_id: 'c' })])
    expect(JSON.stringify(t1)).toBe(snapshot)
  })
})

describe('lane blocks', () => {
  it('opens the conclusion and folds intermediate steps into one summary', () => {
    seq = 400
    const t = applyEvents(emptyTimeline(), [
      ev('message', { run_id: 6, msg_id: 'u', role: 'user', content: '把照片按年整理' }),
      ev('reasoning', { run_id: 6, msg_id: 'm1', delta: '想想' }),
      ev('text', { run_id: 6, msg_id: 'm1', delta: '我先看看目录' }),
      ev('tool_call', { run_id: 6, call_id: 'a', name: 'list_files' }),
      ev('tool_result', { run_id: 6, call_id: 'a', ok: true }),
      ev('message', { run_id: 6, msg_id: 'm2', role: 'assistant', content: '已完成整理方案。' }),
    ])
    const blocks = laneBlocks(t.runs[6].lanes[0].items, true)
    expect(blocks.map((b) => b.kind)).toEqual(['prompt', 'steps', 'answer'])
    expect(stepSummary(blocks[1] as StepGroup)).toBe('思考 1 段 · 工具 1 次')
    expect((blocks[2] as { item: MessageItem }).item.text).toBe('已完成整理方案。')
  })

  it('marks a group with a failed tool so it opens itself', () => {
    seq = 500
    const t = applyEvents(emptyTimeline(), [
      ev('tool_call', { run_id: 7, call_id: 'a', name: 'set_target' }),
      ev('tool_result', { run_id: 7, call_id: 'a', ok: false, error: '冲突' }),
    ])
    const [g] = laneBlocks(t.runs[7].lanes[0].items, false) as StepGroup[]
    expect(g.failed).toBe(true)
  })
})

describe('event buffer', () => {
  it('delivers events once per frame, in order, and flushes early past the pending cap', () => {
    const frames: (() => void)[] = []
    const out: number[][] = []
    const buf = createEventBuffer(
      (b) => out.push(b.map((e) => e.seq)),
      (cb) => {
        frames.push(cb)
        return () => {}
      },
      10,
    )
    buf.push(ev('text', { delta: 'abc' }, 1))
    buf.push(ev('tool_call', {}, 2))
    expect(out).toEqual([])
    frames[0]()
    expect(out).toEqual([[1, 2]])
    buf.push(ev('text', { delta: '0123456789AB' }, 3))
    expect(out).toEqual([[1, 2], [3]])
  })
})
