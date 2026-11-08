# Security

- **Access token.** One token per instance, generated on first start (`server.token` in the loaded
  `config.yaml`, mode 0600) and printed once to the log. Clients send `Authorization: Bearer <token>` or the
  HttpOnly, SameSite=Strict `fe_session` cookie set by `POST /api/v1/auth/login`. The desktop app injects a
  per-launch token.
- **Network.** The server binds `127.0.0.1` by default. On a loopback bind only loopback `Host` values are
  served (DNS rebinding); cross-site requests and foreign `Origin`s are rejected unless they carry a bearer
  header. There are no CORS headers.
- **Secrets at rest.** Filesystem passwords and model API keys are AES-GCM encrypted with `FE_SECRET_KEY`
  (32 bytes, base64) or a generated `secret.key` next to the database. Back up the key with the database.
  API responses only carry `password_set` / `api_key_set`; test endpoints reuse a stored secret only for the
  same endpoint.
- **Paths.** Stored paths are relative to the filesystem root. Every backend confines operations to its base
  path, and the executor re-validates source and target before each step. LOCAL storage and SFTP key files
  must be inside `FE_LOCAL_ROOTS` (empty disables LOCAL); the app's own data directory is always refused.
- **SFTP.** The server fingerprint is pinned on first use; a change is refused with `服务器指纹已变化` until the
  user confirms it.
- **Model input.** File contents reach the model inside delimited `<data>` blocks and file names are
  sanitized; prompts state that both are data, never instructions. Files flagged sensitive are never read.
- **Limits.** Request bodies are capped (1 MB, 8 MB for category import); connection and model test endpoints
  and login are rate limited.
- **Operations.** `GET /healthz` needs no token. `POST /api/v1/admin/backup` streams a consistent SQLite
  snapshot. Logs redact tokens, keys and passwords.

| Variable | Purpose |
|---|---|
| `FE_SECRET_KEY` | encryption key for stored secrets |
| `FE_LOCAL_ROOTS` | comma-separated host directories LOCAL storage may use |
| `FE_DATA_DIR` | desktop data directory |
| `FE_BIND`, `FE_PORT` | Docker compose publish address and port |
