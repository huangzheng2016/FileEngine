// The session event stream folded into per-run, per-worker transcripts. Pure:
// the same reducer serves the live stream, history replays and tests. A batch of
// events copies each touched object once, so a frame of deltas stays cheap.
import type { RunPhase, RunStatus, SessionEvent, TokenCounts } from '@/api/types'

export interface MessageItem {
  kind: 'message'
  id: string
  role: 'user' | 'assistant' | 'system'
  text: string
  reasoning: string
  // text or reasoning still arriving
  streaming: boolean
  tokens?: TokenCounts
}

export interface ToolItem {
  kind: 'tool'
  id: string
  name: string
  args: unknown
  status: 'running' | 'ok' | 'failed'
  output?: unknown
  error?: string
  durationMs?: number
}

export interface UnitItem {
  kind: 'unit'
  id: string
  label: string
  count: number
  done: boolean
  tagged: number
  failed: number
}

export type Item = MessageItem | ToolItem | UnitItem

export interface Lane {
  workerId: number
  items: Item[]
}

export interface Progress {
  total: number
  tagged: number
  planned: number
  success: number
  failed: number
  skipped: number
}

export interface RunView {
  runId: number
  phase: RunPhase | ''
  status: RunStatus
  title: string
  error: string
  summary: string
  tokens?: TokenCounts
  progress?: Progress
  done: boolean
  lanes: Lane[]
}

export interface Timeline {
  // highest applied sequence number; anything at or below it is a replay
  seq: number
  runs: Record<number, RunView>
  // run ids in the order they first appeared
  order: number[]
}

export const emptyTimeline = (): Timeline => ({ seq: 0, runs: {}, order: [] })

const emptyRun = (runId: number): RunView => ({
  runId,
  phase: '',
  status: 'running',
  title: '',
  error: '',
  summary: '',
  done: false,
  lanes: [],
})

type Data = Record<string, unknown>
const str = (v: unknown) => (typeof v === 'string' ? v : v == null ? '' : String(v))
const num = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : Number(v) || 0)

// Applies a batch of events in order and returns a new timeline (or the same one
// when nothing applied).
export function applyEvents(t: Timeline, events: readonly SessionEvent[]): Timeline {
  const owned = new WeakSet<object>()
  const own = <T extends object>(o: T, copy: (o: T) => T): T => {
    if (owned.has(o)) return o
    const c = copy(o)
    owned.add(c)
    return c
  }
  let next = t
  const draft = () => (next = own(next, (o) => ({ ...o, runs: { ...o.runs }, order: o.order })))

  function run(runId: number): RunView {
    draft()
    const cur = next.runs[runId]
    if (!cur) {
      const r = emptyRun(runId)
      owned.add(r)
      next.runs[runId] = r
      next.order = [...next.order, runId]
      return r
    }
    const r = own(cur, (o) => ({ ...o }))
    next.runs[runId] = r
    return r
  }

  function lane(r: RunView, workerId: number): Lane {
    let i = r.lanes.findIndex((l) => l.workerId === workerId)
    if (!owned.has(r.lanes)) {
      r.lanes = [...r.lanes]
      owned.add(r.lanes)
    }
    if (i < 0) {
      const l: Lane = { workerId, items: [] }
      owned.add(l)
      owned.add(l.items)
      r.lanes.push(l)
      r.lanes.sort((a, b) => a.workerId - b.workerId)
      return l
    }
    const l = own(r.lanes[i], (o) => ({ ...o }))
    if (!owned.has(l.items)) {
      l.items = [...l.items]
      owned.add(l.items)
    }
    i = r.lanes.findIndex((x) => x.workerId === workerId)
    r.lanes[i] = l
    return l
  }

  function findLast<K extends Item['kind']>(l: Lane, kind: K, id: string): number {
    for (let i = l.items.length - 1; i >= 0; i--) {
      const it = l.items[i]
      if (it.kind === kind && it.id === id) return i
    }
    return -1
  }

  function message(l: Lane, id: string, role: MessageItem['role']): MessageItem {
    const i = findLast(l, 'message', id)
    if (i < 0) {
      const m: MessageItem = { kind: 'message', id, role, text: '', reasoning: '', streaming: true }
      owned.add(m)
      l.items.push(m)
      return m
    }
    const m = own(l.items[i] as MessageItem, (o) => ({ ...o }))
    l.items[i] = m
    return m
  }

  function tool(l: Lane, id: string): ToolItem {
    const i = findLast(l, 'tool', id)
    if (i < 0) {
      const it: ToolItem = { kind: 'tool', id, name: '', args: {}, status: 'running' }
      owned.add(it)
      l.items.push(it)
      return it
    }
    const it = own(l.items[i] as ToolItem, (o) => ({ ...o }))
    l.items[i] = it
    return it
  }

  for (const ev of events) {
    if (ev.seq && ev.seq <= next.seq) continue
    const d: Data = ev.data ?? {}
    const runId = num(d.run_id)
    if (ev.seq) {
      draft()
      next.seq = ev.seq
    }
    if (!runId) continue
    const workerId = num(d.worker_id)

    switch (ev.type) {
      case 'run_status': {
        const r = run(runId)
        r.phase = (str(d.phase) as RunPhase) || r.phase
        r.status = (str(d.status) as RunStatus) || r.status
        if (d.title) r.title = str(d.title)
        if (d.error) r.error = str(d.error)
        if (r.status !== 'running') r.done = true
        break
      }
      case 'run_done': {
        const r = run(runId)
        r.phase = (str(d.phase) as RunPhase) || r.phase
        r.status = (str(d.status) as RunStatus) || r.status
        r.summary = str(d.summary)
        r.error = str(d.error)
        r.tokens = d.tokens as TokenCounts | undefined
        r.done = true
        // a stopped or crashed run leaves unfinished steps behind
        r.lanes = r.lanes.map((l) => {
          if (
            !l.items.some(
              (it) => (it.kind === 'tool' && it.status === 'running') || (it.kind === 'message' && it.streaming),
            )
          )
            return l
          return {
            ...l,
            items: l.items.map((it) =>
              it.kind === 'tool' && it.status === 'running'
                ? { ...it, status: 'failed' as const, error: '未完成' }
                : it.kind === 'message' && it.streaming
                  ? { ...it, streaming: false }
                  : it,
            ),
          }
        })
        owned.add(r.lanes)
        break
      }
      case 'progress': {
        const r = run(runId)
        r.progress = {
          total: num(d.total),
          tagged: num(d.tagged),
          planned: num(d.planned),
          success: num(d.success),
          failed: num(d.failed),
          skipped: num(d.skipped),
        }
        break
      }
      case 'pull_begin': {
        const l = lane(run(runId), workerId)
        const u: UnitItem = {
          kind: 'unit',
          id: str(d.unit_id),
          label: str(d.label),
          count: num(d.count),
          done: false,
          tagged: 0,
          failed: 0,
        }
        owned.add(u)
        l.items.push(u)
        break
      }
      case 'pull_done': {
        const l = lane(run(runId), workerId)
        const i = findLast(l, 'unit', str(d.unit_id))
        if (i < 0) break
        l.items[i] = { ...(l.items[i] as UnitItem), done: true, tagged: num(d.tagged), failed: num(d.failed) }
        break
      }
      case 'text':
      case 'reasoning': {
        const l = lane(run(runId), workerId)
        const m = message(l, str(d.msg_id), 'assistant')
        if (ev.type === 'text') m.text += str(d.delta)
        else m.reasoning += str(d.delta)
        m.streaming = true
        break
      }
      case 'message': {
        const l = lane(run(runId), workerId)
        const role = (str(d.role) || 'assistant') as MessageItem['role']
        const m = message(l, str(d.msg_id) || `seq-${ev.seq}`, role)
        m.role = role
        m.text = str(d.content)
        m.streaming = false
        if (d.tokens) m.tokens = d.tokens as TokenCounts
        break
      }
      case 'tool_call': {
        const l = lane(run(runId), workerId)
        const it = tool(l, str(d.call_id))
        it.name = str(d.name)
        it.args = d.args ?? {}
        break
      }
      case 'tool_result': {
        const l = lane(run(runId), workerId)
        const it = tool(l, str(d.call_id))
        if (!it.name) it.name = str(d.name)
        it.status = d.ok === false || d.error ? 'failed' : 'ok'
        it.output = d.output
        it.error = d.error ? str(d.error) : undefined
        it.durationMs = num(d.duration_ms)
        break
      }
    }
  }
  return next
}

// ---------- display grouping ----------

export interface StepGroup {
  kind: 'steps'
  key: string
  items: (MessageItem | ToolItem)[]
  thoughts: number
  tools: number
  failed: boolean
}

export interface AnswerBlock {
  kind: 'answer'
  key: string
  item: MessageItem
}

export interface PromptBlock {
  kind: 'prompt'
  key: string
  item: MessageItem
}

export interface UnitBlock {
  kind: 'unit'
  key: string
  item: UnitItem
}

export type Block = StepGroup | AnswerBlock | PromptBlock | UnitBlock

// Turns a lane into what the reader sees: conclusions (the last assistant text of
// each stretch, and anything still streaming) open, intermediate thinking and tool
// calls folded into one group per stretch. In the assistant conversation the user's
// own messages show as prompts; in agent runs the generated prompt is a raw step.
export function laneBlocks(items: readonly Item[], showUserPrompts: boolean): Block[] {
  const out: Block[] = []
  let stretch: (MessageItem | ToolItem)[] = []

  const flush = () => {
    if (!stretch.length) return
    let answerAt = -1
    for (let i = stretch.length - 1; i >= 0; i--) {
      const it = stretch[i]
      if (it.kind === 'tool') break
      if (it.kind === 'message' && it.role === 'assistant' && it.text.trim()) {
        answerAt = i
        break
      }
    }
    const steps = answerAt < 0 ? stretch : stretch.slice(0, answerAt)
    const answer = answerAt < 0 ? undefined : (stretch[answerAt] as MessageItem)
    // reasoning of the answer itself belongs to the folded steps
    if (steps.length || answer?.reasoning) {
      const items = answer?.reasoning ? [...steps, { ...answer, text: '' }] : steps
      out.push({
        kind: 'steps',
        key: `s-${items[0].id}`,
        items,
        thoughts: items.filter((i) => i.kind === 'message' && (i.reasoning || (i.role === 'assistant' && i.text)))
          .length,
        tools: items.filter((i) => i.kind === 'tool').length,
        failed: items.some(
          (i) => (i.kind === 'tool' && i.status === 'failed') || (i.kind === 'message' && i.role === 'system'),
        ),
      })
    }
    if (answer) out.push({ kind: 'answer', key: `a-${answer.id}`, item: answer })
    for (const it of stretch.slice(answerAt + 1)) {
      if (answerAt >= 0) out.push({ kind: 'answer', key: `a-${it.id}`, item: it as MessageItem })
    }
    stretch = []
  }

  for (const it of items) {
    if (it.kind === 'unit') {
      flush()
      out.push({ kind: 'unit', key: `u-${it.id}`, item: it })
    } else if (it.kind === 'message' && it.role === 'user' && showUserPrompts) {
      flush()
      out.push({ kind: 'prompt', key: `p-${it.id}`, item: it })
    } else if (it.kind === 'message' && it.role === 'system') {
      // failures are never folded away
      flush()
      out.push({ kind: 'answer', key: `a-${it.id}`, item: it })
    } else {
      stretch.push(it)
    }
  }
  flush()
  return out
}

export function stepSummary(g: StepGroup): string {
  const parts: string[] = []
  if (g.thoughts) parts.push(`思考 ${g.thoughts} 段`)
  if (g.tools) parts.push(`工具 ${g.tools} 次`)
  return parts.join(' · ') || '过程'
}

export const isLive = (r: RunView | undefined) => !!r && !r.done
