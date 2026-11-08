# Architecture

FileEngine organizes files on a storage backend (SMB, SFTP, NFS, FTP, local) with an LLM agent. The agent only
writes decisions to the database; files move only when the user executes a reviewed plan.

```
scan ──▶ (optional) plan draft ──▶ tagging ──▶ review ──▶ preview ──▶ execute ──▶ (revert)
           survey run              tagging run   review run   user          executing run
```

## Data

| Thing | Lives in |
|---|---|
| Storage connections | `Filesystem` (secrets encrypted at rest) |
| Model providers | `ModelProvider` (`thinking_enabled`, `reasoning_effort`, `context_window`, `vision_enabled`) |
| One organizing task | `ScanSession` (scan state, goal, scope, switches, model ids) |
| Every scanned entry and its decision | `FileEntry` (`role`, `new_path`, `reason`, `confidence`, `needs_review`, metadata, hash) |
| The 整理方案 | `SessionPlan` (Markdown, versioned, draft/approved) and `PlanRule` rows |
| Categories | `Category`, scoped to a filesystem |
| Memory | `MemoryItem`: `session` notes shared by workers, `filesystem` and `global` long-term items |
| Agent and executor activity | `AgentRun` (phase, status, summary, tokens) |
| Everything a run streamed | `SessionEvent` (seq = row id) |
| Assistant conversation | `ChatTurn` |
| File operations | `ExecOp` journal (resume and revert) |

The scan root is the empty path `""`; stored paths are relative to the filesystem root and target paths start
with `/`.

Path columns compare as bytes on every database (SQLite BINARY, MySQL binary table collation, PostgreSQL
`"C"`), so subtree matches are index ranges (`db.subtreeCond`: `>= dir/` and `< dir0`). `new_path_key`
(`CollisionKey(new_path)`) backs the indexed case/normalization-insensitive collision check. MySQL indexes use
key prefixes to fit InnoDB's 3072-byte limit, so `original_path` uniqueness there covers its first 766
characters. Session counters (`total_files`, `planned_ops`, …) are stored on `ScanSession`: phases refresh them
when they end and decision writes schedule a coalesced refresh; reads never recompute them.

## Runs and events

A session runs at most one run at a time (`409 会话正在运行中` otherwise). Phases: `survey`, `tagging`,
`review` (chained after a finished tagging run), `chat`, `executing`. Runs end `done`, `partial`, `error` or
`stopped`; runs left `running` by a crash are swept to `stopped` on startup.

`db.Emit` is the only publisher. It persists the event, assigns `seq`, then broadcasts; text and reasoning
deltas are coalesced (150 ms / 2 KB) and flushed before any other event of the same run.

`GET /api/v1/sessions/:id/events/stream` serves SSE frames `id/event/data`. Resume with `Last-Event-ID` or
`?after=`; without either the replay starts at the latest run's first event. Replay then live is gap-free and
ordered. History: `GET /sessions/:id/events?run_id&after&before&limit`.

| Event | Payload |
|---|---|
| `run_status` | `{run_id, phase, status, title?, error?}` |
| `run_done` | `{run_id, phase, status, summary?, error?, tokens}` — exactly once, after the final `run_status` |
| `pull_begin` / `pull_done` | a worker starts / settles a work unit (`unit_id`) |
| `reasoning`, `text` | `{run_id, worker_id, msg_id, delta}` |
| `message` | finalizes a `msg_id`; `content` replaces the deltas |
| `tool_call` / `tool_result` | paired by `call_id`; `ok` false on `success:false` |
| `progress` | absolute counters |
| `plan_updated`, `memory_updated` | refetch the plan / memory |

## Decision model

Every path has at most one effective decision: the nearest ancestor-or-self whose role is `unit` (moved
whole) or `file`. Containers are not moved; members follow their unit; `skip` and `duplicate` are never
planned. The repository enforces this: a decision under a unit is rejected, and making a directory a unit
over decided descendants needs an explicit override.

Tagging walks the tree top-down. A directory is decided `unit`, `container` or `skip`; a container's loose
files form one work unit; versions of the same software form one unit. Deterministic signals (marker files,
extension histogram, version groups) come first, rules apply in bulk, and only the rest reaches the model.
Low-confidence decisions wait in `needs_review` and are not executed until confirmed.

## Prompt assembly (cache affinity)

Every model request is built from most stable to most volatile, so provider prefix caches hit:

1. tools — one fixed, ordered set per session and run kind
2. system prompt — static text
3. shared context — goal, plan, categories by name, memory, rules; byte-identical across a run's workers;
   Claude cache breakpoint
4. history — append-only; old tool results are cleared in place past the reduction threshold
5. the unit or turn — listing, signals, notes from other workers

Runs record `cached_tokens` and `cache_write_tokens`; `cache_hit_rate` is exposed on runs.

## Memory

Session notes let parallel workers share discoveries while a tagging run is live. Filesystem memory holds
conventions and learned corrections (a user editing a target records one automatically); global memory holds
preferences. A core block (pinned, preferences, conventions, corrections; ~1.5K tokens) is part of the shared
context; `recall` searches the rest. After review, a reflection call merges notes and corrections into
long-term memory.

## Execution

The executor re-validates every path, never overwrites, and compares the live source with the scanned size and
mtime. A move renames when possible; otherwise each file is copied, verified (size, plus SHA-256 up to 64 MB),
and only then deleted. Every step is journaled before it happens, so an interrupted run resumes and a finished
run can be reverted. `execute/check` runs the same validation without changes; `sample` executes the first N
entries.

## Storage backends

All backends confine paths to `BasePath`. NFS and FTP serialize operations on one connection; SMB and SFTP
multiplex. Zip archives need random access: native on local, SFTP and SMB, through an adapter on NFS, and a
temporary download (≤ 32 MB) on FTP.

## Desktop

`cmd/fileengine-desktop` runs the same Gin server in process on `127.0.0.1:<random>` with a per-launch token
(the Wails v3 asset handler cannot stream SSE on Windows). The window starts at the bootstrap URL, which sets
the session cookie.
