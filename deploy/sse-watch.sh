#!/usr/bin/env bash
# Watch a session's live agent events (SSE) from the host.
# Usage: ./sse-watch.sh <session_id> [logfile]
# Events stream to stdout; when a logfile is given they are tee'd into it.
# The stream carries only new events — refetch GET /api/v1/logs?session_id=N
# for history. Ctrl-C stops watching (the run continues server-side).
set -euo pipefail

cd "$(dirname "$0")"
SESSION_ID="${1:?usage: ./sse-watch.sh <session_id> [logfile]}"
PORT="${FE_PORT:-8080}"
if [ -f .env ]; then
    set -a
    # shellcheck disable=SC1091
    . ./.env
    set +a
    PORT="${FE_PORT:-8080}"
fi
# API token: FE_TOKEN from .env, else server.token from the generated config
# (readable only if your host user may read deploy/data/config.yaml; otherwise
# copy it from `docker compose logs fileengine | grep "access token"`).
FE_TOKEN="${FE_TOKEN:-$(sed -n 's/^ *token: *//p' data/config.yaml 2>/dev/null || true)}"
if [ -z "$FE_TOKEN" ]; then
    echo "FE_TOKEN not set and data/config.yaml unreadable — set FE_TOKEN in deploy/.env" >&2
    exit 1
fi
URL="http://localhost:${PORT}/api/v1/sessions/${SESSION_ID}/events/stream"

echo "watching $URL (Ctrl-C to stop)" >&2
if [ $# -ge 2 ]; then
    curl -fsS -N -H 'Accept: text/event-stream' -H "Authorization: Bearer $FE_TOKEN" "$URL" | tee -a "$2"
else
    curl -fsS -N -H 'Accept: text/event-stream' -H "Authorization: Bearer $FE_TOKEN" "$URL"
fi
