#!/bin/sh
# FileEngine container entrypoint.
# Generates /data/config.yaml from FE_* env vars on first start, fixes
# ownership of the data dir, then runs the server as the fileengine user.
# The server binds FE_SERVER_HOST (default 127.0.0.1; the image sets 0.0.0.0).
set -eu

DATA_DIR="${FE_DATA_DIR:-/data}"
mkdir -p "$DATA_DIR"

if [ ! -f "$DATA_DIR/config.yaml" ]; then
    echo "entrypoint: generating $DATA_DIR/config.yaml from environment"
    cat > "$DATA_DIR/config.yaml" <<EOF
server:
  port: ${FE_SERVER_PORT:-8080}
  host: ${FE_SERVER_HOST:-127.0.0.1}
database:
  driver: ${FE_DB_DRIVER:-sqlite}
  dsn: ${FE_DB_DSN:-$DATA_DIR/fileengine.db}
agent:
  concurrency: ${FE_AGENT_CONCURRENCY:-1}
  batch_size: ${FE_AGENT_BATCH_SIZE:-50}
  max_step: ${FE_AGENT_MAX_STEP:-150}
  max_retries: ${FE_AGENT_MAX_RETRIES:-3}
  max_file_read_size: ${FE_AGENT_MAX_FILE_READ_SIZE:-102400}
EOF
fi

# Bind-mounted data dirs may arrive root-owned; make them writable, then drop
# privileges. Never fails the container on exotic mount setups.
chown -R fileengine:fileengine "$DATA_DIR" 2>/dev/null || true

exec su-exec fileengine /app/fileengine "$DATA_DIR/config.yaml"
