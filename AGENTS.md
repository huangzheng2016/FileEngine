# FileEngine Agent Guide

Reply in the user's language. Keep this file to rules that prevent regressions; code, tests and `Taskfile.yml`
are the source of truth. How the system fits together: `docs/architecture.md`; security model:
`docs/security.md`.

## Project Map

- `main.go`, `internal/app`: server entry and lifecycle. `cmd/fileengine-desktop`: Wails v3 desktop (in-process
  server on loopback).
- `internal/api`: Gin handlers, one file per resource; auth, limits and run launching live here.
- `internal/agent`: tagging pipeline, chat, survey, review, reflection, tools, Tengo scripts, prompts, memory.
- `internal/db`: GORM models, repository, contract types (`contract.go`), runs and the event bus.
- `internal/scanner`, `internal/executor`, `internal/remotefs`, `internal/model`, `internal/pathutil`,
  `internal/config`, `internal/logging`.
- `web`: Vue 3 frontend on the danceinbox-style design system; `web/src/base` and `web/src/styles` are copied
  verbatim from the skill (only `app.ts`, `labels.ts`, `icons.ts` are project-owned).
- `third_party/go-nfs-client`: vendored NFS client. `deploy`: Docker test environment. `scripts`: build checks.

Toolchain: Go 1.27 (pinned toolchain), Node 24, npm, Wails v3 CLI pinned in `Taskfile.yml`. SQLite is pure Go;
the server builds with `CGO_ENABLED=0`; Linux desktop builds need GTK 4 and WebKitGTK 6.

## Working Rules

- No backward compatibility: no legacy fallbacks, shims, migrations or data backfills. A fresh database is
  expected after schema changes. Delete removed features completely.
- Single source of truth: each fact lives in one table or one config key (see `docs/architecture.md`). Session
  activity is read from runs, never inferred from `ScanSession.Status`.
- Comments are rare, English only, and explain a non-obvious why. No history notes, no restating code. UI copy
  and API error messages are Chinese, with full-width punctuation.
- Model requests stay cache-affine: fixed ordered tools → static system prompt → byte-identical shared context
  → append-only history → per-unit data. Never put counts, timestamps or ids in the prefix.
- Only `db.Emit` publishes events, never inside a DB transaction. Never put image data in events or logs.
- Retries are per model request; a run is never replayed after the model has answered.
- One effective decision per path: only `db.Decide` / `DecideBatch` write decisions; nothing is decided under a
  unit; overriding decided descendants needs `override` and is reported; every decision carries a reason.
  Agent-recoverable rejections are tool results (`success:false`), not Go errors.
- Echoed file names go through `SanitizeUntrustedName`; file contents reach the model inside `<data>` blocks;
  sensitive files are never read.
- Writes are column-scoped; never `Save` a previously loaded `FileEntry`.
- Executor: delete a source only after a verified copy; never overwrite; never `RemoveAll`; stop or cancel never
  deletes; journal each step before it happens; skip sources that changed since the scan.
- Scanner: writes go through the writer goroutine in batches; a rescan deletes rows only after a completed walk
  and keeps decisions of unchanged entries; metadata and hash errors are never fatal.
- Paths: validate with `pathutil.ValidateFSPath`; the scan root is `""`.
- Frontend: build pages from the danceinbox reference pages; enums through `labels.ts`; Markdown only through
  `StreamMarkdown`; stream events only through the event buffer and reducer; no hex colors, raw z-index or
  page restyling of shared classes.
- Release builds are stripped and reproducible (`-trimpath -buildvcs=false -s -w -buildid=`), checked by
  `scripts/stripcheck`.
- Never commit credentials, internal addresses or runtime data (`*.db*`, logs, `deploy/data/`). Examples use
  placeholders such as `192.168.1.x`.

## Validation

- `task check` is the gate: gofmt, module verification, vet (host and Windows), Go tests, frontend check
  (prettier, eslint, lint-ui, vitest, vue-tsc) and build. `task build` / `task desktop:build` also run
  stripcheck.
- Tests are deterministic. Concurrency changes run the affected packages with `-race -count=50`. An old test
  that encodes a superseded design is updated or deleted, not worked around.
- Database behavior is checked on SQLite; PostgreSQL tests run when `FILEENGINE_TEST_PG_DSN` points at a
  scratch database. Say which backends were not verified.
- The persistent test environment is `deploy/` (`docker compose up -d --build` + `./bootstrap.sh`). Keep its
  data; never call execute endpoints against it.

## Git and Collaboration

- Commit locally in small Conventional Commits; never push, rebase or rewrite history.
- Inspect `git status` and `git diff --cached` before committing.
- Delegated work uses non-overlapping file ownership and isolated worktrees; integrate on the feature branch and
  validate the merged tree before reporting.
- Report findings with location, impact and a concrete fix; say "cause not identified" when it is.
