# Architecture

FileEngine organizes files on a storage backend (SMB, SFTP, NFS, FTP, local) with an LLM agent. The agent only
writes decisions to the database; files move only when the user executes a reviewed plan.

```
scan ──▶ (optional) plan draft ──▶ tagging ──▶ review ──▶ preview ──▶ execute ──▶ (revert)
           survey run              tagging run   review run   user          executing run
```

## Code layout and request flow

```
idl/*.thrift ──hz──▶ biz/model, biz/router
request ─▶ biz/mw (auth, limits) ─▶ biz/handler/api (bind, one call, convert, respond)
        ─▶ biz/service/<feature> ─▶ biz/dal/db/query (gorm/gen, typed)  [+ internal/db for the domain core]
```

- Handlers hold no logic and no queries; failures are `biz/errs` values (Chinese messages) and answer the
  `{code, msg}` envelope through `biz/handler/resp`.
- A service takes the writer's `*query.Query`; list and get endpoints that need no read-your-writes inside the request
  take `*dal.Reader` instead, which on SQLite is a separate `query_only` pool (WAL), elsewhere the same handle.
  Dialect-dependent fragments (path ranges, case-insensitive search) come only from `biz/dal/db` helpers.
- `biz/service/run` resolves a session's storage and model and builds its agent; `runner` owns every live run
  (launch on a detached context, stop, shutdown drain, the cancel registry the executor also registers in).
  `settings` serves the versioned runtime settings. `eventfeed` reads persisted events and follows the live
  broadcast for SSE and the desktop.
- `internal/db` is what the agent, executor and scanner call (their queries, `Decide`, run rows, `Emit`, rules,
  preview, journal); new resource endpoints do not add to it. `container` wires the services with Wire
  (`task gen:wire`); `internal/app` owns startup and shutdown order.
- Generated and checked by `task gen:check`: `biz/model`, `biz/router`, `biz/dal/db/query`, `container/wire_gen.go`,
  the desktop bindings. The frontend's `web/src/api/idl.gen.ts` comes from `npm run gen:idl`.

## Data

| Thing | Lives in |
|---|---|
| Storage connections | `Filesystem` (secrets encrypted at rest) |
| Model providers | `ModelProvider` (`thinking_enabled`, `reasoning_effort`, `context_window`, `vision_enabled`; `temperature` 0 = model default, not sent; `max_tokens` 0 = no cap, Claude gets 8192 because its API requires one) |
| One organizing task | `ScanSession` (scan state, goal, scope, switches, model ids) |
| Every scanned entry and its decision | `FileEntry` (`role`, `new_path`, `reason`, `confidence`, `needs_review`, metadata, hash) |
| The 整理方案 | `SessionPlan` (Markdown, versioned, draft/approved) and `PlanRule` rows |
| Categories | `Category`, scoped to a filesystem |
| Memory | `MemoryItem`: `session` notes shared by workers, `filesystem` and `global` long-term items |
| Agent and executor activity | `AgentRun` (phase, status, summary, tokens; `exec_mode` on executing runs, `reverts_run_id` on revert runs) |
| Everything a run streamed | `SessionEvent` (seq = row id) |
| Assistant conversation | `ChatTurn` |
| File operations | `ExecOp` journal (resume and revert) |

The scan root is the empty path `""`; stored paths are relative to the filesystem root and target paths start
with `/`.

Path columns compare as bytes on every database (SQLite BINARY, MySQL binary table collation, PostgreSQL
`"C"`), so subtree matches are index ranges (`dal.SubtreeCond`: `>= dir/` and `< dir0`). `new_path_key`
(`CollisionKey(new_path)`) backs the indexed case/normalization-insensitive collision check. MySQL indexes use
key prefixes to fit InnoDB's 3072-byte limit, so `original_path` uniqueness there covers its first 766
characters. Session counters (`total_files`, `tagged_files`, `planned_ops`, `review_count`, `executed_ops`,
`total_size`) are stored on `ScanSession`: phases refresh them when they end and decision writes (and review
confirmation) schedule a coalesced refresh; reads never recompute them. `review_count` counts planned, unexecuted
entries still in `needs_review`, which the executor skips. A refresh that changes a counter emits
`session_updated`.

A session needs a model: `POST /sessions` and `PATCH /sessions/:id` reject a missing or unknown
`model_provider_id` (and an unknown non-zero `bulk_model_provider_id`) with `400 请选择模型`. With `auto_tag: true`
the server starts a tagging run as soon as the scan completes. Deleting a storage or a model that a session still
uses (either model field) answers `409 有 N 个整理任务在用这个存储/模型，先删除这些任务`. Session list and
detail responses carry `latest_run: {id, phase, status}` when the session has runs.

## Runs and events

A session runs at most one run at a time (`409 整理任务正在运行中` otherwise). Phases: `survey`, `tagging`,
`review` (chained after a finished tagging run), `chat`, `executing`, `revert`. Runs end `done`, `partial`,
`error` or `stopped`; runs left `running` by a crash are swept to `stopped` on startup. Titles are the phase name
(`生成方案`, `整理`, `复查`, `执行`, `撤销`) without ids; chat runs carry the user's message.

A `revert` run undoes one executing run and stores its id in `reverts_run_id`; the executing run then reports
`reverted_by_run_id` (derived from a running, done or partial revert; a stopped or failed revert can be retried).
`POST /runs/:id/revert` answers `409` for a revert run and for an executing run that is already reverted
(`这次执行已撤销`).

`db.Emit` is the only publisher. It persists the event, assigns `seq`, then broadcasts; text and reasoning
deltas are coalesced (150 ms / 2 KB) and flushed before any other event of the same run. Each session has its own
emit lane, so one session's database write never delays another's; `seq` is the row id and rises per session.
Replay reads `(session_id, id)` through `idx_event_session_seq`.

`GET /api/event/stream?session_id=` serves SSE frames `id/event/data` (a `: ping` comment every 20 s so a dead
client is noticed by a failed write). Resume with `Last-Event-ID` or `?after=`; without either the replay starts at
the latest run's first event. Replay then live is gap-free and ordered (`eventfeed.Follow`, shared by SSE and the
desktop). History: `GET /api/event/list?session_id&run_id&after&before&limit`. Open streams are owned by one
registry that the app cancels first at shutdown, before runs, scans and the HTTP server stop.

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
| `session_updated` | `{}` (run_id 0) — stored counters changed; refetch the session |

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

1. tools — one fixed, ordered set per toolset kind, all tools always present: tagging has 12 (`list_entries`,
   `read_file`, `view_image`, `decide`, `split_unit`, `describe`, `add_rule`, `apply_rules`, `run_script`,
   `create_category`, `note`, `recall`); chat, survey and review add 7 (`update_category`, `delete_category`,
   `update_rule`, `delete_rule`, `read_plan`, `write_plan`, `remember`). Session switches and the run kind gate
   the handler, which answers `success:false`; they never change the list.
2. system prompt — static text per run kind, no switch-dependent sections
3. shared context — a snapshot taken once per tagging run (before the workers start) and once per chat epoch
   (stored with the summary turn): goal, plan, categories by name, rules by name, memory ordered by rank,
   scope, kind, key, id; never by usage or recency; Claude cache breakpoint
4. history — append-only
5. the unit or turn — a `## 运行中的变化` delta since the snapshot (past ~1k tokens a tagging run re-snapshots),
   then the task, listing, signals and notes from other workers. Chat stores the exact message it sent, so the
   next turn's request starts with this turn's messages

Every tool answers `{success, error?, hint?, ...}`: bad arguments, rejections and disabled switches are results the model can act on; only database and IO failures stop a run. Text derived from files
(names, listings, signals, notes, script output) is sanitized and sits in `<data>` blocks; sensitive files are
never read, including archive members.

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

Safety details that recovery relies on:

- A copy op journals `dst_created` the moment the backend created the destination, before any data is written.
  Recovery and failure cleanup delete a destination only for ops carrying that marker; a file at the target of
  any other op is never touched. Before a resumed move deletes its source it re-stats the copy against the
  journaled size and mtime and keeps both files when it is missing or different.
- Only a cross-device or unsupported rename switches the rest of the run to copy. After any other rename error
  the executor checks whether the rename took effect (source gone, destination present) before falling back.
- "Absent" means a not-exist error; any other stat error is treated as unknown and never as permission to act.
- A source whose delete fails keeps its verified op and is retried by the next recovery without blocking other
  work. A finished op whose entry was never marked executed is reconciled by recovery.
- Target paths reject control characters (FTP would read a line break as another command); all paths reject
  backslashes.

## Storage backends

All backends confine paths to `BasePath`. NFS, FTP and SFTP serialize operations on one connection (NFS scans
use a pool of extra connections; FTP copies read their source over a second, kept connection); SMB multiplexes.
FTP logs in anonymously when no user is set, uses MLST when the server has it and a LIST of the parent
otherwise. An unlistable directory is skipped by the scan and reported; a rescan keeps the rows below it.
Zip archives need random access: native on local, SFTP and SMB, through an adapter on NFS, and a
temporary download (≤ 32 MB) on FTP.

## Desktop

`cmd/fileengine-desktop` runs the backend in process with no listener, token or cookie. The embedded UI is served
by the Wails asset handler (behind `runtimeRequestGuard`), and the window reaches the backend through one Wails
service, `FileEngine`, whose bindings are generated into `cmd/fileengine-desktop/bindings` (`task gen:bindings`):

- `Call({method, path, query, body})` returns `{status, content_type, content_disposition, encoding, body}`. It
  runs the request through the same Hertz engine and IDL routes as the web server, with a context marked by
  `actor.WithDesktop` that default-deny auth accepts; only `/api` paths are served and `/api/event/stream` is
  refused. `body` is JSON text (`encoding: "utf8"`) or base64 for downloads (`"base64"`). Failures are normal
  responses with the `{code, msg}` envelope.
- `Subscribe({session_id, run_id?, after})` / `Unsubscribe(subscription_id)` replace SSE. Events arrive as Wails
  events on topic `session://events/<session_id>` with the payload `{seq, run_id, kind, data, created_at}`
  (`data` is the JSON text of the SSE data field); `<topic>/end` carries `{subscription_id, error?}` when a feed
  stops. `after` null starts at the latest run's first event, otherwise it resumes after that seq: history
  first, then live, ordered and gap-free, like the SSE route.

Shutdown cancels every subscription, then stops runs and scans, then closes the database; there is no HTTP
server to stop.

## Configuration and settings

`config.yaml` holds only what the process needs at startup: `server` (host, port, token) and `database` (driver,
dsn). It is decoded strictly (unknown keys fail), validated at startup, and a PostgreSQL or MySQL DSN is required,
never guessed. Everything an operator edits while the app runs, including the agent limits and the system prompt, is
one document stored as append-only `settings_versions` rows (`biz/service/settings`). A save names the version it was
edited from (`base_version`); a newer saved version answers 409. A save validates and persists
a new version. A run reads the current version once when it is launched (`settings.Current`) and hands that snapshot
to the agent, its tools and the scanner, so a save never changes a run in flight. Unchanged saves write nothing.
