# CLAUDE.md

Instructions for Claude Code when working on this project.

## Project Overview

FileEngine is a Go + Vue 3 monorepo. The Go backend uses Gin (HTTP), GORM (ORM), and Cloudwego Eino (AI agent framework). The Vue frontend is embedded into the Go binary via `go:embed`.

## Build Commands

```bash
# Frontend build (must run first, output goes to web/dist/ which is embedded)
cd web && npm run build

# Backend build (includes embedded frontend)
go build -o fileengine .

# Full build
cd web && npm run build && cd .. && go build -o fileengine .

# Run
./fileengine              # uses config.yaml in CWD
./fileengine path/to/config.yaml
```

## Code Style & Conventions

### Design Principles
- **No backward compatibility** — Do not add legacy fallbacks, compatibility shims, or migration paths. Old data/config formats are not supported. If a schema changes, the old version is simply dropped.
- **No over-engineering** — Don't add feature flags, version negotiation, or graceful degradation for removed features. If something is removed, delete it completely with no trace.
- **Single source of truth** — Each piece of data lives in one place. Filesystem connections are in the `Filesystem` DB table. Model providers are in the `ModelProvider` DB table. Categories are scoped to filesystems. Per-session agent config (allow_read_file, allow_auto_category, allow_vision, allow_script, model_provider_id) lives on `ScanSession`.

### Go
- Module path: `FileEngine` (not a URL-style path)
- All internal packages under `internal/`
- API handlers: one file per resource (`handler_session.go`, `handler_file.go`, `handler_model.go`, etc.)
- Config: global singleton with `config.Get()` / `config.Update()` / `config.Save()`
- DB: repository pattern via `db.Repository` wrapping `*gorm.DB`; `db.WithRetry()` for SQLite lock retries
- GORM models use struct tags for composite indexes (e.g., `gorm:"index:idx_name,priority:N"`)
- Agent tools use Eino's `utils.InferTool` with struct tags for JSON schema generation
- Error handling: return errors up, log at the handler level (including background goroutines)
- No CLI framework — pure HTTP server with embedded frontend

### Vue / TypeScript
- Vue 3 Composition API (`<script setup lang="ts">`)
- Element Plus UI framework
- Hash-based routing (`createWebHashHistory`)
- i18n: Chinese (zh-CN) as default, English (en) as fallback
- Locale files: `web/src/i18n/locales/{zh-CN,en}.yaml`
- API client: Axios wrapper at `web/src/api/index.ts`
- Types: `web/src/types/index.ts`
- All icons globally registered from `@element-plus/icons-vue`
- CodeMirror for prompt editing (One Dark theme + markdown)
- Colored tags for protocols (SMB/SFTP/FTP/NFS/LOCAL), file types (文件夹/文件), providers (OpenAI/Claude/Ollama), session status (i18n)
- localStorage for user preferences: `fe_last_fs_id`, `fe_log_order`, `fe_columns_v2`

## Architecture Notes

### Two-Phase Design
- **Phase 1 (DB-only):** Agent scans, tags, and sets target paths — only modifies database records
- **Phase 2 (Execution):** Executor reads plans from DB, user chooses copy or move mode, performs actual file operations via RemoteFS

### Scanner
- Recursive directory walker, creates `FileEntry` records for all files/directories
- Concurrent walk (worker pool) for backends implementing `HandleLister` (NFS, SMB); serial otherwise; junk dirs (`db.JunkDirNames`) pruned during walk
- Live progress: atomic counters + current path flushed to `ScanSession.ScannedCount` / `CurrentPath` every 2s; surfaced via session JSON (no new endpoints); walk failure sets `Status="error"` + `ErrorMessage`
- On startup `db.Init` sweeps stale `scanning` sessions to `interrupted` (counts preserved) — a session can never sit in `scanning` forever
- Rescan/delete wait for the previous scan goroutine to finish (`scanHandle.done`, 30s timeout fallback); `RefreshSessionCounts` writes only count columns (never status/progress)
- All RemoteFS ops carry timeouts (`FE_REMOTEFS_TIMEOUT_SEC`, default 30s; SFTP uses sliding activity deadlines; NFS pool discards timed-out conns and redials); errors are wrapped as `OpError` with `errors.Is(os.ErrDeadlineExceeded)` support
- Supports blacklist/whitelist directory filtering (`ScanSession.FilterMode` + `FilterDirs`)
- `ExcludeCategoryDirs` option auto-appends category paths to blacklist
- Filter dirs stored as newline-separated text, parsed at scan time

### Agent Tagging Algorithm
- Processes directories **bottom-up by depth level** (deepest first)
- Each directory gets its own agent call (not batched)
- Agent explores upward to find project roots, marks entire project at once
- `mark_tagged` cascades to all descendants via `LIKE` query; also writes `batch_index`
- `set_target` on a directory cascades: clears all children's targets (outer overrides inner) — the tool result reports `cleared_children` + guidance when it discards finer classifications, so the agent can re-decide instead of silently losing them
- `set_target` with empty `new_path` clears the target
- `set_target` validates `new_path` before writing: the path must be a legal, root-confined slash path — no NUL bytes, no `..` segments, not the bare root — illegal targets are rejected with an error, never written. `create_category`/`update_category` apply the same validation to category paths (they become `new_path` prefixes); the check lives in `agent.ValidateFSPath` and the REST category handlers (create/update/import) run it too, so the API cannot bypass the tool layer
- Before processing each directory, re-checks tagged status (another worker may have cascade-tagged it)
- Configurable concurrency: each worker gets its own FS connection + ReAct agent instance + token tracker
- All agent config (concurrency, batch_size, max_retries, rate_limit_max_retries, retry_base_delay, retry_max_delay, system_prompt, agent_timeout) read from live `config.Get()` each iteration — hot-reloadable without restart
- `batch_size` (default 200, capped 500) no longer scales with `max_step` (default 150) in the script bulk workflow: `run_script` has a `mark_tagged(path)` builtin alongside `set_target`/`set_category`, so one script can classify and close out a whole batch (query → bulk set_category → mark_tagged loop) in 1–2 tool calls. The per-file fallback still costs ≥2 calls/file; leftovers are caught by `recheckBatchTagged` + end-of-run sweeps
- Retry on failure is class-aware (`runADKWithRetry` → `runWithRetry` in `internal/agent/retry.go`): the error is classified via the eino-ext openai `APIError.HTTPStatusCode` (falling back to the `error, status code: NNN` text pattern for proxy-style non-JSON error bodies) into `rate_limit` (429), `server` (5xx — plus unrecognized errors, keeping the old retry-everything behavior), `network` (timeouts/connection/DNS errors) and `hard` (other 4xx: 400/401/403/context-length/… — **zero retries**, fails immediately instead of burning 3 pointless attempts). Retryable classes use exponential backoff with full jitter: attempt N sleeps rand[0, min(`retry_max_delay`, `retry_base_delay`·2^(N-1))] (defaults 2s/60s), so the 10 workers no longer thunder in lockstep. 429s get a separate, larger retry budget (`rate_limit_max_retries`, default 8 vs `max_retries` default 3) so brief throttling cannot fail a long run; server `Retry-After` hints are honored when present (currently a best-effort parse of the error message text, clamped to 4×`retry_max_delay` — the SDK chain drops the actual header) and a fleet-wide consecutive-429 streak (`rateLimitStreak` atomic, reset by any success) adds up to 4 extra exponent slots to every worker's backoff while throttling persists. The class is re-evaluated after every attempt and every retry log line carries class + actual wait. `agent_timeout` (seconds) caps one full agent run — the entire ReAct loop of up to `max_step` tool rounds, not a single Generate call; the default (unset or <= 0) is **no timeout** — a run is then bounded only by user stop and `max_step`
- Batch failures are never silent: after retries are exhausted the batch is logged (a `system` message persisted to `agent_logs` and published to the event stream), counted in `AutoTagAgent.Failures()`, and surfaced three ways — the session status becomes `tagged_partial` instead of `tagged`, the `run_status` done message carries the failed-batch count, and the run report (the `run_done` summary) gets a `## 失败批次` section with each failed batch and its error
- Loose-file batches are also re-checked after success (`recheckBatchTagged`): `set_target` does not flip `tagged`, so an agent can finish a batch yet leave files behind — such silent misses are recorded as `BatchFailure`s too (same surfacing as hard failures). The default prompt requires per-file `mark_tagged` in loose-file batches
- After the loose-file pass, `Run` re-sweeps up to 2 extra rounds (`runRemainingSweeps`) with the cursor back at ID 0 — the dispatcher's ID cursor never looks back, so only fresh passes reach batch misses; a round whose untagged-file count does not drop stops the loop, and directories are never re-swept (container dirs stay untagged by design and would otherwise be re-tagged forever)
- **Loose files default to the continuous harness mode** (`harness_disabled` to opt out): each worker runs ONE long agent run that pulls the next untagged batch itself (`list_files`/`query_files` with `tagged=false`) until its shard is empty — category knowledge carries across pulls inside the run (the create_category version guard still forces a re-read when another worker changed the set). Untagged pulls are auto-sharded by `id % numWorkers` (`FileQuery.IDMod`, disjoint and deterministic) and each non-empty pull rolls a fresh pull on the worker's lane (`pull_begin` from the tool layer, with the pull count and starting cursor; the previous pull is first settled with a `pull_done` carrying tagged/missed counts, and the last open pull settles at run end). Pulls also inject the archive listing summaries the batch prompt promises (`archive_summary` on `FileItem`/script rows via `ToolBuilder.archiveSummaries` — same guardrails as `buildBatchPrompt`, capped at `harnessArchiveHintsPerPull` opens per pull). `harness_max_step` (default 2000) bounds one continuous run — steps scale with total files, not one batch. Leftovers are still covered by the sweep rounds; a dead run's shard resumes naturally on the next pass (untagged rows are idempotent). Failure token usage is attributed too: `runADKWithRetry` writes the last attempt's tokens into the failure `system` row, so failed batches no longer vanish from token accounting
- The tagging report ends with a `## 打标对账` reconciliation line (`untaggedReconciliationSection`): untagged remainders split into files / directories / symlinks — container dirs and symlinks are expected deltas, only a non-zero file count signals real misses
- The category summary injected into each batch prompt (`buildCategorySummary`) lists the **complete** category system of the filesystem — unused categories included — sorted by usage count descending then name; `GetCategoryUsageSummary` counts with `new_path = path OR new_path LIKE path/%` so sibling prefixes (`/photo` vs `/photos2`) don't miscount
- The default system prompts are assembled per session to match the registered tools (`prompt.go`): the `create_category` workflow is only taught when `AllowAutoCategory` registers it (otherwise the prompt steers to the 未分类 fallback), and the instruct prompt's tool list follows `AllowReadFile` / `AllowAutoCategory`
- `processRemainingFiles` has 1000-iteration safety limit to prevent infinite loops

### Agent Tools
- 8 always-on tools: `list_files`, `get_file_info`, `update_description`, `mark_tagged`, `list_categories`, `set_target`, `update_category`, `delete_category`
- 4 conditional tools: `read_file` (when `allow_read_file` is true), `create_category` (when `allow_auto_category` is true), `view_image` (when both vision switches are on), `run_script` (when `allow_script` is true — the DB default, owner decision)
- `read_file` is format-aware (`internal/agent/readfmt.go`): plain text is byte-capped (`max_file_read_size`) with a binary sniff, and documents get text extraction — PDF via `github.com/ledongthuc/pdf` (maintained pure-Go fork of rsc.io/pdf), docx/xlsx/pptx via stdlib `archive/zip`+`encoding/xml` only (OOXML = zip of XML members). Documents are read up to a fixed 32MB cap (`maxDocumentReadBytes`, they must be parsed whole — zip central directory / PDF xref live at the end), extracted text is capped to `max_file_read_size` runes and PDFs to 50 pages (`maxPDFPages`), `Truncated` flags every cut; legacy binary `.doc` is refused with an explicit unsupported note; paths go through `ValidateFSPath` like every other tool
- `read_file` also handles archives: zip/tar/tar.gz(`.tgz`) get a capped listing (name + uncompressed size, first 200 entries) and individual text entries can be read via the `path::entry` syntax when they are ≤ `archive_entry_max_size` (default 64KB) — larger entries and archives over 32MB are listing-only; rar/7z return an explicit unsupported note. zip needs random access — per-backend behavior and the FTP degradation notice are in the RemoteFS capability matrix; tar/tar.gz are streamed and work everywhere
- Tool availability is per-session, configured via `ScanSession.AllowReadFile` / `AllowAutoCategory` / `AllowScript`; `view_image` is the exception — it requires both `ModelProvider.VisionEnabled` (model can read images) AND `ScanSession.AllowVision` (session opt-in), both defaulting to off
- `run_script` (`internal/agent/script.go`) runs a Tengo script (github.com/d5/tengo/v2, the latest published module line — the v3.0.0 tag is unusable, see go.mod) mid-run for bulk operations ("类似 ipy"): the agent gets `print(...)` output plus the script's `result` global (JSON-encoded) as the tool result. **Script API**: builtins `query_files({parent_path?, file_type?, tagged?, category_path?, offset?, limit?})` → `{files, total}`, `count_files({filters})`, `get_file_info(path)`, `get_categories()` (records the create_category guard snapshot, like `list_categories`), `mark_tagged(path)` (idempotent close-out — bulk loops end with it), `set_target(path, new_path)`, `set_category(path, category_name)` (targets category path + file name), `create_category(name, path, description)` (only when `allow_auto_category`; same version/similarity guard rails as the tool — both cores live in `setTargetCore`/`createCategoryCore` so scripts can never bypass `ValidateFSPath` or the M14 guard), `rename_category(name, new_name)` (agent_editable only), `sample_files([filter,] n)` (random subset via `Repository.SampleFiles`). Domain failures return tengo **error values** (check with `is_error(x)`); argument-shape mistakes are script runtime errors. **Security**: stdlib is trimmed to pure-function modules (text/math/json/base64/hex/rand/enum — no os/exec/env/file/network, file import disabled, no fmt: `print`/`println` are captured per-run into a capped buffer instead of process stdout; `times` is excluded too — its `sleep()` blocks inside a VM builtin where the timeout's abort flag cannot interrupt it, so it would bypass `script_timeout` and wedge the worker); all data access goes through the builtins. **Guardrails** (AgentConfig): `script_max_size` (source bytes, default 16KB), `script_timeout` (seconds per run, default 10 — deliberately independent of the unlimited-by-default `agent_timeout`), `script_max_result_size` (print output and result value, each, default 32KB), plus a fixed 4M object-allocation cap (`scriptMaxAllocs`); tengo's `RunContext` aborts on timeout and recovers VM panics into errors. **Residual risk (accepted)**: the alloc cap counts objects, not bytes — an exponential-growth loop (`x += x` doubles memory per round with one allocation per round) can OOM the whole process inside the timeout window, and Go's out-of-memory is a fatal error, not a recoverable panic; tengo has no byte-level memory cap, so real containment would need a subprocess with rlimits. Impact is availability-only (no sandbox escape, no cross-session data access). Each execution is persisted to `agent_logs` as one `run_script` row (script source + capped output; failures logged with the error) and flows through the normal SSE `tool_call`/`tool_result` events like any other tool
- `view_image` (`internal/agent/vision.go`) returns multimodal tool results via `utils.InferEnhancedTool` + `schema.ToolResult.Parts` (per-file text note + thumbnail image part); it decodes jpg/jpeg/png/gif server-side, downscales to a thumbnail (area-averaged, stdlib only), and base64-encodes. HEIC/RAW are skipped with a text note. Cost guardrails in `AgentConfig`: `vision_max_images_per_batch` (default 4 per call), `vision_image_max_px` (default 512 long edge), `vision_max_image_bytes` (default 20MB source cap); a hard 50MP decoded-pixel cap (`maxImagePixels`, checked via `DecodeConfig` before allocation) blocks decompression bombs. Per-file problems become text notes, never whole-call errors; base64 never enters `agent_logs` (metadata only) or the event stream (a text stand-in is emitted for multimodal tool results)
- All tool path inputs are normalized via `normalizePath()` (strips leading `/`)
- Writes are column-scoped: `update_description` updates only the description column (`Repository.UpdateDescription`) — a full-row `Save` of a previously loaded struct races with parallel tool calls on the same row (observed: a racing `update_description` silently wiped a just-committed `set_target`). Same rule for any future FileEntry writer
- **Agent-recoverable rejections are results, not errors**: eino makes any tool Go error fatal to the whole ReAct run (batch dies after retries). Guard rejections (`create_category` version/duplicate, not-found, invalid input, not-editable) therefore return `success:false` + an `error` message field with re-decision guidance — the model recovers in the next round. System failures (DB/FS errors) stay fatal Go errors. The `rejectError` sentinel marks the recoverable class through the shared `*Core` functions (scripts still get tengo error values)
- `delete_category` clears planned files under that category and resets `tagged=false` for re-classification
- `create_category` is guarded to keep the category system lean (`internal/agent/category_guard.go`): a per-worker version fingerprint (count + name set, in-memory only) is recorded whenever the agent receives the category list — the batch-prompt summary or a `list_categories` call. If the live set has changed since (or the agent never saw a list), creation is rejected with the latest complete list and must be re-decided from it; the rejection itself is the re-read, so a retry based on it passes. An objective-duplicate check then hard-blocks only normalized identical names (trim + case-fold) and normalized identical paths, returning the existing category as reference; fuzzy lookalikes (segment-aware path-prefix overlap/containment in either direction) never block — the successful result carries a `similar_categories` reference list (name/path/planned-file count/reason) plus a merge hint, and the model decides whether to consolidate via `set_target`/`update_category`. Successful creations update the snapshot, so back-to-back creations by the same worker are not re-challenged — but anyone else's change forces a re-read
- Categories have `agent_created` and `agent_editable` flags; agent can only modify/delete `agent_editable=true` categories
- Instruct mode (`RunInstruct`) has all tools available including `list_files`, `get_file_info`, `mark_tagged`
- Token tracking: `tokenTrackingModel` wrapper intercepts all LLM Generate and Stream calls (including intermediate ReAct rounds) to accumulate accurate token counts

### Model Providers
- `ModelProvider` DB entity stores multiple LLM configurations (name, provider, api_key, model, base_url, temperature, max_tokens, context_window, vision_enabled, reasoning_effort)
- `max_tokens` is the output completion cap passed to the LLM API; `context_window` is the model's context window in tokens — it alone drives the reduction/summarization thresholds in `buildADKAgent`. Suggested tiers 200K/256K/500K/900K/1M, custom values accepted; unset/legacy rows fall back to `db.DefaultContextWindow` (200K), backfilled on DB init
- `vision_enabled` (default false) marks a model as image-capable; it is the model-level half of the two-level vision switch gating `view_image` (see Agent Tools)
- `reasoning_effort` (default "" = unset) passes through to OpenAI-compatible APIs as `reasoning_effort` (low/medium/high) via the eino-ext openai extension — wired in `model.NewChatModelFromProvider()`; validated on create/update, additive like `context_window` (absent keeps stored value). kimi-k3 note: its sampler is locked (only temperature=1 accepted) and `reasoning_effort: low` cuts thinking tokens ~85%
- Supports OpenAI (via `eino-ext/components/model/openai`), Claude (native via `eino-ext/components/model/claude`), Ollama (via OpenAI-compatible endpoint)
- Each `ScanSession` has `ModelProviderID` — can use a different model per scan
- `model.NewChatModelFromProvider()` creates model from DB entity
- Model test endpoint sends "Hi" to verify connectivity and shows reply

### RemoteFS
- Unified `RemoteFS` interface in `internal/remotefs/interface.go`
- 5 implementations: Local, SFTP, FTP, SMB, NFS
- Factory: `remotefs.NewFromConfig(cfg)` creates the right implementation
- Each concurrent agent worker creates its own FS connection
- SMB: `BasePath` is the share name, normalized (backslashes → forward slashes, trimmed)
- NFS: `BasePath` is the export path (+ optional sub-path); `NewNFSFS` auto-discovers the real export by probing MNT from the deepest candidate upward, so deep exports (fnOS exports `/fs/1000/nfs` whole, not `/fs`) work without manual splitting. The locked lib always goes through portmap on TCP 111 (mountd/NFS ports are server-published, e.g. 9024/2049) — `RemoteFSConfig.Port` is currently unused by the NFS backend. The lib is a vendored fork at `third_party/go-nfs-client` (go.mod `replace`): it adds `ReadDirPlusByHandle` (list straight from a READDIRPLUS-returned file handle, skipping the LOOKUP-per-path-component walk) and raises the READDIRPLUS page size 512/4096 → 8K/64K. Scan listing additionally runs over a pool of extra mount connections (`FE_NFS_SCAN_CONNS`, default 8) driven by the scanner's worker pool through the `scanner.HandleLister` interface; the scanner also prunes `db.JunkDirNames` directories at traversal time (never listed, never persisted) unless `agent.junk_filter_disabled` is set
- **Random-access capability matrix** (verified against the locked go.mod libraries, not protocol reputation) — it decides how `read_file` parses zip archives (the central directory lives at the end of the file, so zip needs random access; tar/tar.gz are streamed and need none):

  | Backend | Locked lib | Opened file type | Random access | zip behavior in `read_file` |
  |---|---|---|---|---|
  | Local | stdlib | `*os.File` | native `io.ReaderAt` | direct |
  | SFTP | `pkg/sftp` v1.13.10 | `*sftp.File` (`ReadAt` = SSH_FXP_READ with offset) | native | direct |
  | SMB | `hirochachacha/go-smb2` v1.1.0 | `*smb2.File.ReadAt` | native | direct |
  | NFS | `vmware/go-nfs-client` (vendored fork, `third_party/go-nfs-client`) | `*nfs.File` (Seek start/current only, no `ReadAt`/`SeekEnd`) | via `seekReaderAt` adapter | direct (adapter) |
  | FTP | `jlaffaye/ftp` v0.2.0 | `*ftp.Response` (stream; `RetrFrom` restart ≠ `ReaderAt`) | none | temp-file download ≤32MB; beyond → explicit degradation notice |

  Detection is by type assertion on the opened file (`resolveArchiveReaderAt` in `internal/agent/readfmt.go`), never by protocol string. The FTP degradation notice is explicit (backend, size, limit, "Do not retry") so the agent cannot silently retry; the FilesystemManager form shows a matching per-protocol capability hint
- **Protocol recommendation**: default to SMB (first choice for NAS) or SFTP; FTP is the fallback — it alone lacks random access, which caps archive support
- **Every backend's `resolve()` confines paths to `BasePath`**: absolute paths are accepted only inside it, `..` escapes are rejected (the shared lexical check is `confineSlashPath` in `internal/remotefs/resolve.go`; an empty BasePath confines nothing). Local additionally resolves symlinks (nearest existing ancestor for not-yet-existing write targets) so links pointing outside are rejected too — the remote backends cannot chase symlinks portably (FTP/NFS/SMB expose no readlink), so a remote symlink inside the base may still point outside; NFS/SMB are otherwise also confined server-side to the export/share, and their BasePath sub-path is enforced by the lexical check
- **Concurrency safety**: eino runs parallel tool calls concurrently, so every backend must be goroutine-safe per connection. SFTP (`pkg/sftp`) and SMB (`go-smb2`) multiplex natively; NFS (`go-nfs-client`, one non-multiplexed TCP stream — concurrent `Call`s fail with "xid did not match") and FTP (`jlaffaye/ftp`, single control connection whose 226 replies would desync under interleaved commands) are serialized with a mutex inside `NFSFS`/`FTPFS` — opened files included: NFS locks Read/Seek/Close per call, FTP holds the lock for the whole `Retr`→`Close` transfer (transfers serialize, no parallel data connections). The one exception is NFS *scan listing*: `NFSFS` dials a pool of extra mount connections (`FE_NFS_SCAN_CONNS`) and hands each out exclusively via a channel, so scan-time `ListByHandle` calls run concurrently while every agent/tool path stays on the mutex-serialized primary connection

### Executor
- Handles both files and directories
- Files: direct `CopyFile` / `MoveFile`
- Directories: `MoveFile` tries rename first (fast, atomic on same share), falls back to `recursiveCopy`
- `recursiveCopy` walks source tree, creates subdirectories via `MkdirAll`, copies each file
- Mode (`copy` or `move`) passed via API query param, overrides per-file operation

### Session Event Stream (SSE)
- **Endpoint**: `GET /api/v1/sessions/:id/events/stream` — SSE stream of live agent run events for one session. History stays in `GET /logs` (agent_logs persistence is unchanged); the stream only carries new events.
- **Bus**: `internal/db/events.go` — session-scoped in-memory event bus living in the `db` package so every producer (agent Logger, ADK event loop, orchestrator, API handlers) can publish without import cycles. `db.PublishEvent` broadcasts with non-blocking sends (slow consumers drop events; refetch `/logs` to fill gaps); subscriber channels are never closed by the bus.
- **Frame format**: `event: <type>\ndata: <json payload>\n\n`, plus `: ping` comment heartbeats every 25s. Consume with `EventSource`; there is no last-event-id replay — on reconnect, refetch history from `/logs`.
- **Wails desktop**: `wails/app.go` `proxySSE` already forwards any request with `Accept: text/event-stream` to the backend with chunked flushing — it works with this endpoint as-is.
- **Event types and payloads** (`phase`: `tagging` / `instruct` / `executing`). The batch system is gone from the stream: every step event carries `worker_id` and the frontend renders one lane per worker (batch_index survives only as an internal DB cursor — `agent_logs.batch_index` / `file_entries.batch_index` — and is never exposed on the stream; `GET /logs/batches` and the `?batch=` log filter were removed with the batch UI):
  - `run_status` — `{session_id, phase, status, message?}`; status: `started` / `done` / `error` / `stopped` (user stop). Mirrors `ScanSession.Status` transitions; emitted by the orchestrator (tagging/instruct) and API handlers (executing).
  - `pull_begin` — `{session_id, worker_id, label, count?, cursor?}`; a worker starts one work unit (label: directory path, "N files", or "instruct"; count: files pulled this round; cursor: smallest file id of a loose-file pull).
  - `pull_done` — `{session_id, worker_id, label?, tagged?, failed?}`; settles the worker's current work unit (tagged = classified, failed = missed/failed). Harness runs settle the previous pull right before the next `pull_begin` and once at run end.
  - `message` — `{session_id, worker_id, role, content, prompt_tokens?, completion_tokens?, total_tokens?}`; role: `user` / `assistant` / `system`. Persisted messages (user prompts, final assistant answers with token counts, system errors) also land in `agent_logs` (which carries `worker_id` too — rows from before lanes existed read as worker 0 and render as a single lane); intermediate assistant reasoning steps are stream-only (no tokens, not persisted).
  - `tool_call` — `{session_id, worker_id, tool_name, input}`; `input` is the raw JSON arguments from the model.
  - `tool_result` — `{session_id, worker_id, tool_name, output, ok}`; `output` is the tool's JSON output. `agent_logs` keeps the combined input+output row per tool call; the stream emits separate call/result events from the ADK event loop (`runADKOnce`).
  - `progress` — `{session_id, phase, total, tagged?, planned?, success?, failed?, skipped?}`; absolute counters only. Tagging fills total/tagged/planned after every finished work unit; executing fills total/success/failed/skipped (1s poll bridge over `Executor.Progress()` while it runs, plus final counters).
  - `run_done` — `{session_id, phase, status, summary?}`; exactly-once terminal event of a run (status: `done` / `error` / `stopped`; `summary` carries the tagging report / instruct response).
- **Frontend timeline** (`web/src/components/SessionTimeline.vue`): worker lanes — one column per worker (CSS grid ≈3 lanes wide, adapting to the actual worker count), each lane a continuous message/tool stream with pull markers and failure highlights; the toolbar carries the run mainline (latest phase transition + absolute progress counters + tokens). Historical sessions render from `agent_logs` the same way (pre-lane rows all land in the single worker-0 lane).
- The old WebSocket endpoint `GET /logs/stream` was removed — its broadcast source never reached tagging workers or instruct runs (dead live channel). Use the SSE stream instead.

### Database
- Three dialects, selected by `database.driver` in `config.yaml`: `sqlite` (default), `mysql`, `postgres`. The DSN format follows the driver: file path for SQLite, go-sql-driver DSN for MySQL (`user:pass@tcp(host:3306)/dbname`), libpq keyword/value string or `postgres://` URL for PostgreSQL (e.g. `host=localhost port=5432 user=postgres password=... dbname=fileengine sslmode=disable`)
- SQLite optimized: WAL mode, busy_timeout=30s, _txlock=immediate, MaxOpenConns=1 (single writer), mmap_size=256MB
- `db.WithRetry()` wraps write operations with retry on "database is locked"
- Composite indexes defined via GORM struct tags (not separate migration files)
- Key indexes: `idx_uniq_session_path`, `idx_session_type_tagged_depth`, `idx_session_parent`, `idx_session_op_exec`
- LIKE semantics are unified in `internal/db/dialect.go`: path-prefix matches (`original_path`/`new_path` under `path/`) are intentionally case-sensitive exact prefixes — plain LIKE is correct on all dialects because paths come from the same scanner — while user-facing name search must be case-insensitive, so PostgreSQL uses `ILIKE` (its LIKE is case-sensitive, unlike SQLite ASCII / MySQL `*_ci`). All LIKE patterns escape `%`/`_` with `ESCAPE '!'` (a backslash escape would need dialect-specific quoting in the clause itself), so wildcard characters in directory names or search text match literally
- AutoMigrate runs on startup for all dialects; on PostgreSQL it creates fresh tables cleanly, but altering column types on an existing PG database is strict — schema changes on live PG deployments need manual migration
- PostgreSQL-specific behavior is covered by `TestPostgresDialectSemantics` in `internal/db/postgres_integration_test.go`, skipped unless `FILEENGINE_TEST_PG_DSN` is set (point it at a scratch database — the test drops tables)

### Eino Framework
- Agent: `adk.NewChatModelAgent()` creates a ReAct agent (see `internal/agent/adk.go`); executed via `adk.Runner`, `MaxIterations` caps the tool-call rounds per run
- `runADKOnce` forwards every ADK `AgentEvent` to the session event bus as step-level events (intermediate assistant `message` / `tool_call` / `tool_result`); the final assistant message is logged by the caller with token usage. Token-level streaming (`EnableStreaming`) is intentionally off — phase 2 candidate for instruct only
- Middleware chain (in order): `patchtoolcalls` (repairs dangling tool calls) → `reduction` (clears old tool outputs past 75% of the model context window) → `summarization` (official eino middleware; LLM-summarizes history past 90% of the window, sharing the same token estimator). The context window comes from `ModelProvider.EffectiveContextWindow()` — never from `max_tokens`
- The shared token estimator (`countTokens`) counts text plus multimodal parts: text parts as text, image parts at a flat per-image estimate (`estimatedTokensPerImagePart`) since provider image billing is dimension-based, not base64-length-based
- Model: `adk.ChatModelAgentConfig.Model` takes `model.ChatModel` (eino-ext openai/claude factories in `internal/model/factory.go`)
- Tools: `utils.InferTool(name, desc, func)` auto-generates JSON schema from Go struct tags; multimodal tool results use `utils.InferEnhancedTool` returning `*schema.ToolResult` (eino converts it to a tool message with `UserInputMultiContent` — supported by both the claude and openai model extensions at the locked versions)
- Struct tags: `json:"field_name" jsonschema_description:"description for LLM"`
- `model.ChatModel` is thread-safe for concurrent use
- Tool builder `NewToolBuilder(repo, fs, sessionID, filesystemID, logger, cfg, session, modelVisionEnabled)`

### Frontend Embedding
- `embed.go` uses `//go:embed all:web/dist` to embed the built frontend
- `main.go` serves it via Gin's NoRoute fallback (SPA support)
- Must run `cd web && npm run build` before `go build`

## Key File Locations

| What | Where |
|------|-------|
| Entry point | `main.go` |
| All API routes | `internal/api/server.go` (setupRouter) |
| DB models + indexes | `internal/db/models.go` |
| DB init + dialect selection | `internal/db/db.go` |
| LIKE dialect helpers (escape, ILIKE) | `internal/db/dialect.go` |
| All DB queries | `internal/db/repository.go` |
| Session event bus + SSE protocol types | `internal/db/events.go` |
| SSE endpoint + run event helpers | `internal/api/handler_events.go` |
| Agent entry / lifecycle (Stop, running state) | `internal/agent/agent.go` |
| Agent construction + middleware chain | `internal/agent/adk.go` (buildADKAgent) |
| Orchestrator (routes autotag/instruct) | `internal/agent/orchestrator.go` |
| Agent tagging loop | `internal/agent/autotag.go` |
| Instruct mode | `internal/agent/instruct.go` |
| Agent tools | `internal/agent/tools.go` |
| run_script (Tengo scripting) tool + builtins | `internal/agent/script.go` |
| create_category guard rails | `internal/agent/category_guard.go` |
| view_image (vision) tool | `internal/agent/vision.go` |
| Token tracker | `internal/agent/token_tracker.go` |
| System prompts | `internal/agent/prompt.go` (+ `prompt_builder.go` for category summaries) |
| LLM factory | `internal/model/factory.go` |
| FS interface | `internal/remotefs/interface.go` |
| Executor | `internal/executor/executor.go` |
| Config types | `internal/config/config.go` |
| Frontend types | `web/src/types/index.ts` |
| Frontend API | `web/src/api/index.ts` |
| Frontend routes | `web/src/router/index.ts` |
| Model manager page | `web/src/views/ModelManager.vue` |
| Wails desktop app | `wails/main.go`, `wails/app.go` |
| Wails build script | `build-wails.sh` |
| Docker test env (compose, bootstrap, SSE watch) | `deploy/` (see `deploy/README.md`) |
| CI/CD | `.github/workflows/build.yml` |

## Common Tasks

### Adding a new API endpoint
1. Create handler in `internal/api/handler_*.go`
2. Register route in `internal/api/server.go` → `setupRouter()`
3. Add TypeScript API function in `web/src/api/index.ts`
4. Add TypeScript types if needed in `web/src/types/index.ts`

### Adding a new agent tool
1. Define input/output structs in `internal/agent/tools.go` with `jsonschema_description` tags
2. Implement the handler function on `ToolBuilder`
3. Register via `utils.InferTool()` in `BuildTools()` — conditionally if needed
4. Update system prompt in `internal/agent/prompt.go` if the tool changes workflow

### Adding a new DB model
1. Define struct in `internal/db/models.go` with GORM tags
2. Add to `AutoMigrate()` in `internal/db/db.go`
3. Add repository methods in `internal/db/repository.go`

### Adding a new frontend page
1. Create Vue component in `web/src/views/`
2. Add route in `web/src/router/index.ts`
3. Add nav item in `web/src/App.vue`
4. Add i18n keys in both `web/src/i18n/locales/zh-CN.yaml` and `en.yaml`
