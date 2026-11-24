import { describe, expect, it } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import type { SessionEvent } from '@/api/types'
import { event } from './fixtures'
import { createRunActivity, runActivity, toolSummary } from '@/lib/toolView'
import {
  applyEvents,
  createLaneBlocks,
  emptyTimeline,
  laneBlocks,
  type Block,
  type MessageItem,
  type Timeline,
  type ToolItem,
} from '@/lib/transcript'
import TranscriptBlock from '@/components/TranscriptBlock.vue'
import { provideOpenState } from '@/components/openState'

function script(): SessionEvent[] {
  let seq = 0
  const ev = (kind: string, data: Record<string, unknown>) => event(kind, { run_id: 1, ...data }, ++seq)
  const out: SessionEvent[] = [ev('run_status', { phase: 'chat', status: 'running' })]
  for (let round = 0; round < 4; round++) {
    const w = round % 2
    out.push(ev('message', { worker_id: w, msg_id: `u${round}`, role: 'user', content: `问题 ${round}` }))
    out.push(ev('pull_begin', { worker_id: w, unit_id: `unit${round}`, label: `文件夹 ${round}`, count: 3 }))
    out.push(ev('reasoning', { worker_id: w, msg_id: `r${round}`, delta: '想' }))
    out.push(ev('text', { worker_id: w, msg_id: `r${round}`, delta: '先看看' }))
    out.push(ev('tool_call', { worker_id: w, call_id: `c${round}`, name: 'list_entries', args: { parent_path: 'a' } }))
    out.push(
      ev('tool_call', {
        worker_id: w,
        call_id: `d${round}`,
        name: 'decide',
        args: { items: [{ path: 'a/x', action: 'move', new_path: '/b/x', reason: 'r' }] },
      }),
    )
    out.push(
      ev('tool_result', {
        worker_id: w,
        call_id: `c${round}`,
        name: 'list_entries',
        ok: true,
        output: { success: true, rows: '<data>\nx\tf\t1\t-\t\t</data>' },
      }),
    )
    out.push(
      ev('tool_result', {
        worker_id: w,
        call_id: `d${round}`,
        name: 'decide',
        ok: true,
        output: { success: true, ok: 1, needs_review: 1 },
      }),
    )
    out.push(ev('message', { worker_id: w, msg_id: `a${round}`, role: 'assistant', content: `答复 ${round}` }))
    // reasoning after the answer
    out.push(ev('reasoning', { worker_id: w, msg_id: `z${round}`, delta: '补充思考' }))
    if (round === 2) out.push(ev('message', { worker_id: w, msg_id: `s${round}`, role: 'system', content: '出错了' }))
    out.push(ev('pull_done', { worker_id: w, unit_id: `unit${round}`, tagged: 1, failed: 0 }))
  }
  return out
}

function batches(events: SessionEvent[], size: number): SessionEvent[][] {
  const out: SessionEvent[][] = []
  for (let i = 0; i < events.length; i += size) out.push(events.slice(i, i + size))
  return out
}

describe('incremental lane blocks', () => {
  for (const prompts of [true, false]) {
    for (const size of [1, 2, 3, 7]) {
      it(`equals a full rebuild after every batch (prompts ${prompts}, batch ${size})`, () => {
        const build = [createLaneBlocks(prompts), createLaneBlocks(prompts)]
        let t: Timeline = emptyTimeline()
        for (const b of batches(script(), size)) {
          t = applyEvents(t, b)
          const lanes = t.runs[1].lanes
          lanes.forEach((l, i) => {
            const inc = (build[i] ?? (build[i] = createLaneBlocks(prompts)))(l.items)
            expect(inc).toEqual(laneBlocks(l.items, prompts))
          })
        }
      })
    }
  }

  it('keeps the identity of blocks that did not change', () => {
    let seq = 0
    const ev = (kind: string, data: Record<string, unknown>) => event(kind, { run_id: 1, ...data }, ++seq)
    const build = createLaneBlocks(true)
    let t = applyEvents(emptyTimeline(), [
      ev('message', { msg_id: 'u', role: 'user', content: '开始' }),
      ev('pull_begin', { unit_id: 'u1', label: 'a', count: 1 }),
      ev('text', { msg_id: 'm1', delta: '你' }),
    ])
    const first = build(t.runs[1].lanes[0].items)
    t = applyEvents(t, [ev('text', { msg_id: 'm1', delta: '好' })])
    const second = build(t.runs[1].lanes[0].items)
    expect(second[0]).toBe(first[0])
    expect(second[1]).toBe(first[1])
    expect(second[2]).not.toBe(first[2])
    // nothing new: the very same array comes back
    expect(build(t.runs[1].lanes[0].items)).toBe(second)
  })

  it('folds reasoning after the answer into the steps instead of an empty answer block', () => {
    let seq = 0
    const ev = (kind: string, data: Record<string, unknown>) => event(kind, { run_id: 1, ...data }, ++seq)
    const t = applyEvents(emptyTimeline(), [
      ev('text', { msg_id: 'm1', delta: '结论' }),
      ev('reasoning', { msg_id: 'm2', delta: '之后又想了想' }),
    ])
    const blocks: Block[] = laneBlocks(t.runs[1].lanes[0].items, false)
    expect(blocks.map((b) => b.kind)).toEqual(['steps', 'answer'])
    for (const b of blocks) if (b.kind === 'answer') expect(b.item.text).toBe('结论')
  })
})

describe('tool call ids', () => {
  it('does not overwrite a finished call when a provider repeats an id', () => {
    let seq = 0
    const ev = (kind: string, data: Record<string, unknown>) => event(kind, { run_id: 1, ...data }, ++seq)
    const t = applyEvents(emptyTimeline(), [
      ev('tool_call', { call_id: '0', name: 'read_file', args: { path: 'a' } }),
      ev('tool_result', { call_id: '0', name: 'read_file', ok: true, output: { success: true } }),
      ev('tool_call', { call_id: '0', name: 'read_file', args: { path: 'b' } }),
      ev('tool_result', { call_id: '0', name: 'read_file', ok: false, error: '没有权限' }),
    ])
    const tools = t.runs[1].lanes[0].items as ToolItem[]
    expect(tools).toHaveLength(2)
    expect(tools.map((x) => [x.key, x.status, (x.args as { path: string }).path])).toEqual([
      ['0', 'ok', 'a'],
      ['0~1', 'failed', 'b'],
    ])
  })

  it('shows success:false results as failed steps with their error', () => {
    let seq = 0
    const ev = (kind: string, data: Record<string, unknown>) => event(kind, { run_id: 1, ...data }, ++seq)
    const t = applyEvents(emptyTimeline(), [
      ev('tool_call', { call_id: 'c', name: 'decide', args: { items: [] } }),
      ev('tool_result', { call_id: 'c', name: 'decide', output: { success: false, error: 'items 不能为空' } }),
    ])
    const tool = t.runs[1].lanes[0].items[0] as ToolItem
    expect(tool).toMatchObject({ status: 'failed', error: 'items 不能为空' })
  })
})

describe('tool summaries', () => {
  const tool = (name: string, args: unknown, output: unknown, status: ToolItem['status'] = 'ok'): ToolItem => ({
    kind: 'tool',
    id: 'x',
    key: 'x',
    name,
    args,
    output,
    status,
  })

  it('says what a decision call did', () => {
    const items = Array.from({ length: 14 }, (_, i) => ({ path: `p${i}`, action: 'move', new_path: '/a', reason: 'r' }))
    expect(toolSummary(tool('decide', { items }, { success: true, ok: 12, needs_review: 3, failed: [] }))).toBe(
      '决定 12 项，3 项待复查',
    )
    expect(
      toolSummary(
        tool('decide', { items }, { success: false, ok: 12, failed: [{ path: 'p1', error: 'x' }] }, 'failed'),
      ),
    ).toBe('决定 12 项，1 项未写入')
    expect(toolSummary(tool('decide', { items }, undefined, 'running'))).toBe('14 项')
  })

  it('summarizes listings, rules and plans', () => {
    expect(
      toolSummary(
        tool(
          'list_entries',
          { parent_path: '/下载' },
          { rows: '<data>\na\tf\t1\t-\t\t\nb\td\t0\t?\t\t</data>', has_more: true },
        ),
      ),
    ).toBe('下载：2 项，还有更多')
    expect(
      toolSummary(
        tool(
          'apply_rules',
          {},
          {
            rules: [
              { name: 'a', matched: 3 },
              { name: 'b', matched: 2 },
            ],
          },
        ),
      ),
    ).toBe('应用 2 条规则，匹配 5 项')
    expect(toolSummary(tool('write_plan', { content: 'x' }, { version: 3 }))).toBe('方案第 3 版')
  })
})

describe('incremental run activity', () => {
  it('equals a full rebuild after every batch', () => {
    const act = createRunActivity()
    let t: Timeline = emptyTimeline()
    for (const b of batches(script(), 2)) {
      t = applyEvents(t, b)
      expect(act(t.runs[1])).toEqual(runActivity(t.runs[1]))
    }
    const last = act(t.runs[1])
    expect(last.decisions.length).toBe(4)
  })
})

describe('open state', () => {
  it('survives the block being unmounted and mounted again', async () => {
    const t = applyEvents(emptyTimeline(), [
      event('tool_call', { run_id: 1, call_id: 'a', name: 'note', args: { content: 'x' } }, 1),
      event('tool_result', { run_id: 1, call_id: 'a', ok: true, output: { success: true } }, 2),
      event('message', { run_id: 1, msg_id: 'm', role: 'assistant', content: '好了' }, 3),
    ])
    const block = laneBlocks(t.runs[1].lanes[0].items, false)[0]
    const shown = ref(true)
    const Host = defineComponent({
      setup() {
        provideOpenState()
        return () => (shown.value ? h(TranscriptBlock, { block }) : null)
      },
    })
    const w = mount(Host)
    expect(w.find('.steps-body').exists()).toBe(false)
    await w.find('.steps-head').trigger('click')
    expect(w.find('.steps-body').exists()).toBe(true)
    shown.value = false
    await nextTick()
    expect(w.find('.steps-body').exists()).toBe(false)
    shown.value = true
    await nextTick()
    expect(w.find('.steps-body').exists()).toBe(true)
    expect((block as { items: MessageItem[] }).items).toHaveLength(1)
  })
})
