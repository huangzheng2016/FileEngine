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
- **Single source of truth** — Each piece of data lives in one place. Filesystem connections are in the `Filesystem` DB table. Model providers are in the `ModelProvider` DB table. Categories are scoped to filesystems. Per-session agent config (allow_read_file, allow_auto_category, model_provider_id) lives on `ScanSession`.

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
- localStorage for user preferences: `fe_last_fs_id`, `fe_log_order`

## Architecture Notes

### Two-Phase Design
- **Phase 1 (DB-only):** Agent scans, tags, and sets target paths — only modifies database records
- **Phase 2 (Execution):** Executor reads plans from DB, user chooses copy or move mode, performs actual file operations via RemoteFS

### Agent Tagging Algorithm
- Processes directories **bottom-up by depth level** (deepest first)
- Each directory gets its own agent call (not batched)
- Agent aggressively explores upward: if depth > 3 or directory name matches common sub-dir patterns (src, lib, test, spec, node_modules, vendor), finds project root and marks entire project
- `mark_tagged` cascades to all descendants via `LIKE` query
- Before processing each directory, re-checks tagged status (another worker may have cascade-tagged it)
- Configurable concurrency: each worker gets its own FS connection + ReAct agent instance
- System prompt and directory prompt are in Chinese

### Agent Tools
- 6 always-on tools: `list_files`, `get_file_info`, `update_description`, `mark_tagged`, `list_categories`, `set_target`
- 2 conditional tools: `read_file` (when `allow_read_file` is true), `create_category` (when `allow_auto_category` is true)
- Tool availability is per-session, configured via `ScanSession.AllowReadFile` / `AllowAutoCategory`
- `set_target` replaces old `plan_move`/`plan_copy` — Agent only sets the target path, copy vs move is decided at execution time

### Model Providers
- `ModelProvider` DB entity stores multiple LLM configurations (name, provider, api_key, model, base_url, temperature, max_tokens)
- Supports OpenAI (via `eino-ext/components/model/openai`), Claude (native via `eino-ext/components/model/claude`), Ollama (via OpenAI-compatible endpoint)
- Each `ScanSession` has `ModelProviderID` — can use a different model per scan
- Falls back to global `config.yaml` model if no provider is set on session
- `model.NewChatModelFromProvider()` creates model from DB entity

### RemoteFS
- Unified `RemoteFS` interface in `internal/remotefs/interface.go`
- 5 implementations: Local, SFTP, FTP, SMB, NFS
- Factory: `remotefs.NewFromConfig(cfg)` creates the right implementation
- Each concurrent agent worker creates its own FS connection
- SMB: `BasePath` is the share name, normalized (backslashes → forward slashes, trimmed)

### Executor
- Handles both files and directories
- Files: direct `CopyFile` / `MoveFile`
- Directories: `MoveFile` tries rename first (fast, atomic on same share), falls back to `recursiveCopy`
- `recursiveCopy` walks source tree, creates subdirectories via `MkdirAll`, copies each file
- Mode (`copy` or `move`) passed via API query param, overrides per-file operation

### Database
- SQLite (default) or MySQL, configured in `config.yaml`
- SQLite optimized: WAL mode, busy_timeout=30s, _txlock=immediate, MaxOpenConns=1 (single writer), mmap_size=256MB
- `db.WithRetry()` wraps write operations with retry on "database is locked"
- Composite indexes defined via GORM struct tags (not separate migration files)
- Key indexes: `idx_uniq_session_path`, `idx_session_type_tagged_depth`, `idx_session_parent`, `idx_session_op_exec`

### Eino Framework
- Agent: `react.NewAgent()` creates a ReAct agent
- Model: `react.AgentConfig.Model` takes `model.ChatModel`
- Tools: `utils.InferTool(name, desc, func)` auto-generates JSON schema from Go struct tags
- Struct tags: `json:"field_name" jsonschema_description:"description for LLM"`
- `model.ChatModel` is thread-safe for concurrent use
- Tool builder `NewToolBuilder(repo, fs, sessionID, filesystemID, logger, cfg)`

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
| Agent tagging loop | `internal/agent/agent.go` (RunTagging) |
| Agent tools | `internal/agent/tools.go` |
| System prompt | `internal/agent/prompt.go` |
| LLM factory | `internal/model/factory.go` |
| FS interface | `internal/remotefs/interface.go` |
| Executor | `internal/executor/executor.go` |
| Config types | `internal/config/config.go` |
| Frontend types | `web/src/types/index.ts` |
| Frontend API | `web/src/api/index.ts` |
| Frontend routes | `web/src/router/index.ts` |
| Model manager page | `web/src/views/ModelManager.vue` |

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
