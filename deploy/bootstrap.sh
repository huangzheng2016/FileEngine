#!/usr/bin/env bash
# FileEngine docker test environment — one-shot provisioning.
# Reads deploy/.env on the HOST (never inside the container), waits for the
# API, then creates the filesystem and model provider if absent (idempotent
# by name). Requires: bash, curl.
set -euo pipefail

cd "$(dirname "$0")"

if [ ! -f .env ]; then
    echo "bootstrap: deploy/.env missing — copy .env.example to .env and fill it in" >&2
    exit 1
fi
set -a
# shellcheck disable=SC1091
. ./.env
set +a

BASE_URL="${FE_BASE_URL:-http://localhost:${FE_PORT:-8080}}"
API="$BASE_URL/api/v1"

echo "bootstrap: waiting for $API ..."
for i in $(seq 1 60); do
    if curl -fsS -o /dev/null "$API/config" 2>/dev/null; then
        break
    fi
    if [ "$i" = 60 ]; then
        echo "bootstrap: API did not come up in 60s" >&2
        exit 1
    fi
    sleep 1
done
echo "bootstrap: API is up"

# --- Filesystem -------------------------------------------------------------
existing_fs=$(curl -fsS "$API/filesystems")
if echo "$existing_fs" | grep -q '"name":"'"${FE_FS_NAME}"'"'; then
    echo "bootstrap: filesystem '${FE_FS_NAME}' already exists, skipping create"
else
    echo "bootstrap: creating filesystem '${FE_FS_NAME}' (${FE_FS_PROTOCOL}://${FE_FS_HOST}${FE_FS_BASE_PATH})"
    curl -fsS -X POST "$API/filesystems" -H 'Content-Type: application/json' -d '{
        "name": "'"${FE_FS_NAME}"'",
        "protocol": "'"${FE_FS_PROTOCOL}"'",
        "host": "'"${FE_FS_HOST}"'",
        "port": '"${FE_FS_PORT:-0}"',
        "base_path": "'"${FE_FS_BASE_PATH}"'"
    }' >/dev/null
fi

# --- Model provider ---------------------------------------------------------
existing_models=$(curl -fsS "$API/models")
if echo "$existing_models" | grep -q '"name":"'"${FE_MODEL_NAME}"'"'; then
    echo "bootstrap: model provider '${FE_MODEL_NAME}' already exists, skipping create"
else
    echo "bootstrap: creating model provider '${FE_MODEL_NAME}' (${FE_MODEL_MODEL} via ${FE_MODEL_PROVIDER})"
    curl -fsS -X POST "$API/models" -H 'Content-Type: application/json' -d '{
        "name": "'"${FE_MODEL_NAME}"'",
        "provider": "'"${FE_MODEL_PROVIDER}"'",
        "api_key": "'"${FE_MODEL_API_KEY}"'",
        "model": "'"${FE_MODEL_MODEL}"'",
        "base_url": "'"${FE_MODEL_BASE_URL}"'",
        "temperature": '"${FE_MODEL_TEMPERATURE:-0.6}"',
        "max_tokens": '"${FE_MODEL_MAX_TOKENS:-8192}"',
        "context_window": '"${FE_MODEL_CONTEXT_WINDOW:-200000}"',
        "vision_enabled": '"${FE_MODEL_VISION_ENABLED:-false}"'
    }' >/dev/null
fi

# --- Optional connectivity checks -------------------------------------------
if [ "${FE_BOOTSTRAP_TEST:-1}" = "1" ]; then
    echo "bootstrap: testing filesystem connection ..."
    curl -fsS -X POST "$API/filesystems/test" -H 'Content-Type: application/json' -d '{
        "name": "'"${FE_FS_NAME}"'",
        "protocol": "'"${FE_FS_PROTOCOL}"'",
        "host": "'"${FE_FS_HOST}"'",
        "port": '"${FE_FS_PORT:-0}"',
        "base_path": "'"${FE_FS_BASE_PATH}"'"
    }'
    echo
    echo "bootstrap: testing model provider ..."
    curl -fsS -X POST "$API/models/test" -H 'Content-Type: application/json' -d '{
        "name": "'"${FE_MODEL_NAME}"'",
        "provider": "'"${FE_MODEL_PROVIDER}"'",
        "api_key": "'"${FE_MODEL_API_KEY}"'",
        "model": "'"${FE_MODEL_MODEL}"'",
        "base_url": "'"${FE_MODEL_BASE_URL}"'",
        "temperature": '"${FE_MODEL_TEMPERATURE:-1}"',
        "max_tokens": '"${FE_MODEL_MAX_TOKENS:-8192}"'
    }'
    echo
fi

echo "bootstrap: done — UI at $BASE_URL"
