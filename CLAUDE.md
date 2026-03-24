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
- **Single source of truth** — Each piece of data lives in one place. Filesystem connections are in the `Filesystem` DB table, not in config.yaml. Categories are scoped to filesystems. Don't duplicate or bridge between old and new designs.

### Go
- Module path: `FileEngine` (not a URL-style path)
- All internal packages under `internal/`
- API handlers: one file per resource (`handler_session.go`, `handler_file.go`, etc.)
- Config: global singleton with `config.Get()` / `config.Update()` / `config.Save()`
- DB: repository pattern via `db.Repository` wrapping `*gorm.DB`
- GORM models use struct tags for composite indexes (e.g., `gorm:"index:idx_name,priority:N"`)
- Agent tools use Eino's `utils.InferTool` with struct tags for JSON schema generation
- Error handling: return errors up, log at the handler level
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

## Architecture Notes

### Two-Phase Design
- **Phase 1 (DB-only):** Agent scans, tags, and plans file moves — only modifies database records
- **Phase 2 (Execution):** Executor reads plans from DB and performs actual file operations via RemoteFS

### Agent Tagging Algorithm
- Processes directories **bottom-up by depth level** (deepest first)
- Each directory gets its own agent call (not batched)
- Agent can explore upward (parent, grandparent) for context
- `mark_tagged` cascades to all descendants via `LIKE` query
- Before processing each directory, re-checks tagged status (another worker may have cascade-tagged it)
- Configurable concurrency: each worker gets its own FS connection + ReAct agent instance

### RemoteFS
- Unified `RemoteFS` interface in `internal/remotefs/interface.go`
- 5 implementations: Local, SFTP, FTP, SMB, NFS
- Factory: `remotefs.NewFromConfig(cfg)` creates the right implementation
- Each concurrent agent worker creates its own FS connection

### Database
- SQLite (default) or MySQL, configured in `config.yaml`
- SQLite optimized with WAL mode, NORMAL synchronous, 20MB cache
- Composite indexes defined via GORM struct tags (not separate migration files)
- Key indexes: `idx_uniq_session_path`, `idx_session_type_tagged_depth`, `idx_session_parent`, `idx_session_op_exec`

### Eino Framework
- Agent: `react.NewAgent()` creates a ReAct agent
- Model: `react.AgentConfig.Model` takes `model.ChatModel` (the deprecated but functional interface)
- Tools: `utils.InferTool(name, desc, func)` auto-generates JSON schema from Go struct tags
- Struct tags: `json:"field_name" jsonschema_description:"description for LLM"`
- `model.ChatModel` (backed by `openai.NewChatModel`) is thread-safe for concurrent use
- Tool builder `NewToolBuilder(repo, fs, sessionID, logger, cfg)` — the `fs` param varies per worker

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
| All DB queries | `internal/db/repository.go` |
| Agent tagging loop | `internal/agent/agent.go` (RunTagging) |
| Agent tools (8) | `internal/agent/tools.go` |
| System prompt | `internal/agent/prompt.go` |
| LLM factory | `internal/model/factory.go` |
| FS interface | `internal/remotefs/interface.go` |
| Config types | `internal/config/config.go` |
| Frontend types | `web/src/types/index.ts` |
| Frontend API | `web/src/api/index.ts` |
| Frontend routes | `web/src/router/index.ts` |

## Common Tasks

### Adding a new API endpoint
1. Create handler in `internal/api/handler_*.go`
2. Register route in `internal/api/server.go` → `setupRouter()`
3. Add TypeScript API function in `web/src/api/index.ts`
4. Add TypeScript types if needed in `web/src/types/index.ts`

### Adding a new agent tool
1. Define input/output structs in `internal/agent/tools.go` with `jsonschema_description` tags
2. Implement the handler function on `ToolBuilder`
3. Register via `utils.InferTool()` in `BuildTools()`
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
