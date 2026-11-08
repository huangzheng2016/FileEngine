// The agent run transcript: what the backend's run events (biz/service/run)
// build up to. Pure functions, so the same reducer serves live streams, replays,
// and tests.

export type RunStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'canceled'

export interface TextBlock {
  kind: 'text'
  text: string
}

export interface ToolBlock {
  kind: 'tool'
  id: string
  name: string
  args: unknown
  status: 'running' | 'succeeded' | 'failed'
  output?: unknown
  error?: string
  durationMs?: number
}

export type Block = TextBlock | ToolBlock

export interface Transcript {
  status: RunStatus
  error: string
  blocks: Block[]
  // last applied event id, for resuming
  seq: number
  done: boolean
}

export const emptyTranscript = (): Transcript => ({ status: 'queued', error: '', blocks: [], seq: 0, done: false })

type Payload = Record<string, unknown>

// Applies one event. Events at or below the current seq are ignored, so a
// replay that overlaps what was already shown changes nothing.
export function applyEvent(t: Transcript, type: string, data: Payload, seq: number): Transcript {
  if (seq && seq <= t.seq) return t
  const next = { ...t, seq: seq || t.seq, blocks: t.blocks }
  const last = t.blocks[t.blocks.length - 1]
  switch (type) {
    case 'text':
      // consecutive chunks extend one block; a tool call in between starts a new one
      if (last?.kind === 'text')
        next.blocks = [...t.blocks.slice(0, -1), { ...last, text: last.text + String(data.delta ?? '') }]
      else next.blocks = [...t.blocks, { kind: 'text', text: String(data.delta ?? '') }]
      break
    case 'tool_call':
      next.blocks = [
        ...t.blocks,
        { kind: 'tool', id: String(data.id), name: String(data.name), args: data.args, status: 'running' },
      ]
      break
    case 'tool_result':
      next.blocks = t.blocks.map((b) =>
        b.kind === 'tool' && b.id === data.id
          ? {
              ...b,
              status: data.error ? 'failed' : 'succeeded',
              output: data.output,
              error: data.error ? String(data.error) : undefined,
              durationMs: Number(data.duration_ms ?? 0),
            }
          : b,
      )
      break
    case 'status':
      next.status = data.status as RunStatus
      next.error = String(data.error ?? '')
      break
    case 'done':
      next.status = (data.status as RunStatus) ?? next.status
      next.done = true
      // a canceled or crashed run leaves its open tool calls unfinished
      next.blocks = next.blocks.map((b) =>
        b.kind === 'tool' && b.status === 'running' ? { ...b, status: 'failed', error: '未完成' } : b,
      )
      break
  }
  return next
}

export const isActive = (s: RunStatus) => s === 'queued' || s === 'running'
