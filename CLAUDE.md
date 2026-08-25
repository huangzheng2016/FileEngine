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
- **Single source of truth** — Each piece of data lives in one place. Filesystem connections are in the `Filesystem` DB table. Model providers are in the `ModelProvider` DB table. Categories are scoped to filesystems. Per-session agent config (allow_read_file, allow_auto_category, allow_vision, model_provider_id) lives on `ScanSession`.

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
- localStorage for user preferences: `fe_last_fs_id`, `fe_log_order`, `fe_columns`

## Architecture Notes

### Two-Phase Design
- **Phase 1 (DB-only):** Agent scans, tags, and sets target paths — only modifies database records
- **Phase 2 (Execution):** Executor reads plans from DB, user chooses copy or move mode, performs actual file operations via RemoteFS

### Scanner
- Recursive directory walker, creates `FileEntry` records for all files/directories
- Supports blacklist/whitelist directory filtering (`ScanSession.FilterMode` + `FilterDirs`)
- `ExcludeCategoryDirs` option auto-appends category paths to blacklist
- Filter dirs stored as newline-separated text, parsed at scan time

### Agent Tagging Algorithm
- Processes directories **bottom-up by depth level** (deepest first)
- Each directory gets its own agent call (not batched)
- Agent explores upward to find project roots, marks entire project at once
- `mark_tagged` cascades to all descendants via `LIKE` query; also writes `batch_index`
- `set_target` on a directory cascades: clears all children's targets (outer overrides inner)
- `set_target` with empty `new_path` clears the target
- `set_target` validates `new_path` before writing: the path must be a legal, root-confined slash path — no NUL bytes, no `..` segments, not the bare root — illegal targets are rejected with an error, never written. `create_category`/`update_category` apply the same validation to category paths (they become `new_path` prefixes); the check lives in `agent.ValidateFSPath` and the REST category handlers (create/update/import) run it too, so the API cannot bypass the tool layer
- Before processing each directory, re-checks tagged status (another worker may have cascade-tagged it)
- Configurable concurrency: each worker gets its own FS connection + ReAct agent instance + token tracker
- All agent config (concurrency, batch_size, max_retries, system_prompt, agent_timeout) read from live `config.Get()` each iteration — hot-reloadable without restart
- Retry on failure: up to `max_retries` with linear backoff (2s, 4s, 6s). `agent_timeout` (seconds) caps one full agent run — the entire ReAct loop of up to `max_step` tool rounds, not a single Generate call; the default (unset or <= 0) is **no timeout** — a run is then bounded only by user stop and `max_step`
- Batch failures are never silent: after retries are exhausted the batch is logged (a `system` message persisted to `agent_logs` and published to the event stream), counted in `AutoTagAgent.Failures()`, and surfaced three ways — the session status becomes `tagged_partial` instead of `tagged`, the `run_status` done message carries the failed-batch count, and the run report (the `run_done` summary) gets a `## 失败批次` section with each failed batch and its error
- The category summary injected into each batch prompt (`buildCategorySummary`) lists the **complete** category system of the filesystem — unused categories included — sorted by usage count descending then name; `GetCategoryUsageSummary` counts with `new_path = path OR new_path LIKE path/%` so sibling prefixes (`/photo` vs `/photos2`) don't miscount
- The default system prompts are assembled per session to match the registered tools (`prompt.go`): the `create_category` workflow is only taught when `AllowAutoCategory` registers it (otherwise the prompt steers to the 未分类 fallback), and the instruct prompt's tool list follows `AllowReadFile` / `AllowAutoCategory`
- `processRemainingFiles` has 1000-iteration safety limit to prevent infinite loops

### Agent Tools
- 8 always-on tools: `list_files`, `get_file_info`, `update_description`, `mark_tagged`, `list_categories`, `set_target`, `update_category`, `delete_category`
- 3 conditional tools: `read_file` (when `allow_read_file` is true), `create_category` (when `allow_auto_category` is true), `view_image` (when both vision switches are on)
- Tool availability is per-session, configured via `ScanSession.AllowReadFile` / `AllowAutoCategory`; `view_image` is the exception — it requires both `ModelProvider.VisionEnabled` (model can read images) AND `ScanSession.AllowVision` (session opt-in), both defaulting to off
- `view_image` (`internal/agent/vision.go`) returns multimodal tool results via `utils.InferEnhancedTool` + `schema.ToolResult.Parts` (per-file text note + thumbnail image part); it decodes jpg/jpeg/png/gif server-side, downscales to a thumbnail (area-averaged, stdlib only), and base64-encodes. HEIC/RAW are skipped with a text note. Cost guardrails in `AgentConfig`: `vision_max_images_per_batch` (default 4 per call), `vision_image_max_px` (default 512 long edge), `vision_max_image_bytes` (default 20MB source cap); a hard 50MP decoded-pixel cap (`maxImagePixels`, checked via `DecodeConfig` before allocation) blocks decompression bombs. Per-file problems become text notes, never whole-call errors; base64 never enters `agent_logs` (metadata only) or the event stream (a text stand-in is emitted for multimodal tool results)
- All tool path inputs are normalized via `normalizePath()` (strips leading `/`)
- `delete_category` clears planned files under that category and resets `tagged=false` for re-classification
- `create_category` is guarded to keep the category system lean (`internal/agent/category_guard.go`): a per-worker version fingerprint (count + name set, in-memory only) is recorded whenever the agent receives the category list — the batch-prompt summary or a `list_categories` call. If the live set has changed since (or the agent never saw a list), creation is rejected with the latest complete list and must be re-decided from it; the rejection itself is the re-read, so a retry based on it passes. An objective-duplicate check then hard-blocks only normalized identical names (trim + case-fold) and normalized identical paths, returning the existing category as reference; fuzzy lookalikes (segment-aware path-prefix overlap/containment in either direction) never block — the successful result carries a `similar_categories` reference list (name/path/planned-file count/reason) plus a merge hint, and the model decides whether to consolidate via `set_target`/`update_category`. Successful creations update the snapshot, so back-to-back creations by the same worker are not re-challenged — but anyone else's change forces a re-read
- Categories have `agent_created` and `agent_editable` flags; agent can only modify/delete `agent_editable=true` categories
- Instruct mode (`RunInstruct`) has all tools available including `list_files`, `get_file_info`, `mark_tagged`
- Token tracking: `tokenTrackingModel` wrapper intercepts all LLM Generate and Stream calls (including intermediate ReAct rounds) to accumulate accurate token counts

### Model Providers
- `ModelProvider` DB entity stores multiple LLM configurations (name, provider, api_key, model, base_url, temperature, max_tokens, context_window, vision_enabled)
- `max_tokens` is the output completion cap passed to the LLM API; `context_window` is the model's context window in tokens — it alone drives the reduction/summarization thresholds in `buildADKAgent`. Suggested tiers 200K/256K/500K/900K/1M, custom values accepted; unset/legacy rows fall back to `db.DefaultContextWindow` (200K), backfilled on DB init
- `vision_enabled` (default false) marks a model as image-capable; it is the model-level half of the two-level vision switch gating `view_image` (see Agent Tools)
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
- Local: `resolve()` confines every path to `BasePath` — absolute paths are accepted only inside it, `..` escapes are rejected, and symlinks are resolved (nearest existing ancestor for not-yet-existing write targets) so links pointing outside are rejected too (matches the root semantics of SFTP/SMB)

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
- **Event types and payloads** (`phase`: `tagging` / `instruct` / `executing`):
  - `run_status` — `{session_id, phase, status, message?}`; status: `started` / `done` / `error` / `stopped` (user stop). Mirrors `ScanSession.Status` transitions; emitted by the orchestrator (tagging/instruct) and API handlers (executing).
  - `batch_start` — `{session_id, batch_index, label}`; one agent batch begins (label: directory path, "N files", or "instruct").
  - `message` — `{session_id, batch_index, role, content, prompt_tokens?, completion_tokens?, total_tokens?}`; role: `user` / `assistant` / `system`. Persisted messages (user prompts, final assistant answers with token counts, system errors) also land in `agent_logs`; intermediate assistant reasoning steps are stream-only (no tokens, not persisted).
  - `tool_call` — `{session_id, batch_index, tool_name, input}`; `input` is the raw JSON arguments from the model.
  - `tool_result` — `{session_id, batch_index, tool_name, output, ok}`; `output` is the tool's JSON output. `agent_logs` keeps the combined input+output row per tool call; the stream emits separate call/result events from the ADK event loop (`runADKOnce`).
  - `progress` — `{session_id, phase, total, tagged?, planned?, success?, failed?, skipped?, batch_index?}`; tagging fills total/tagged/planned (+batch_index) after every finished batch; executing fills total/success/failed/skipped (1s poll bridge over `Executor.Progress()` while it runs, plus final counters).
  - `run_done` — `{session_id, phase, status, summary?}`; exactly-once terminal event of a run (status: `done` / `error` / `stopped`; `summary` carries the tagging report / instruct response).
- The old WebSocket endpoint `GET /logs/stream` was removed — its broadcast source never reached tagging workers or instruct runs (dead live channel). Use the SSE stream instead.

### Database
- SQLite (default) or MySQL, configured in `config.yaml`
- SQLite optimized: WAL mode, busy_timeout=30s, _txlock=immediate, MaxOpenConns=1 (single writer), mmap_size=256MB
- `db.WithRetry()` wraps write operations with retry on "database is locked"
- Composite indexes defined via GORM struct tags (not separate migration files)
- Key indexes: `idx_uniq_session_path`, `idx_session_type_tagged_depth`, `idx_session_parent`, `idx_session_op_exec`

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
| DB init + SQLite opts | `internal/db/db.go` |
| All DB queries | `internal/db/repository.go` |
| Session event bus + SSE protocol types | `internal/db/events.go` |
| SSE endpoint + run event helpers | `internal/api/handler_events.go` |
| Agent entry / lifecycle (Stop, running state) | `internal/agent/agent.go` |
| Agent construction + middleware chain | `internal/agent/adk.go` (buildADKAgent) |
| Orchestrator (routes autotag/instruct) | `internal/agent/orchestrator.go` |
| Agent tagging loop | `internal/agent/autotag.go` |
| Instruct mode | `internal/agent/instruct.go` |
| Agent tools | `internal/agent/tools.go` |
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
