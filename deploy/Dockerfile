# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# Stage 1: build the Vue frontend (output: web/dist, embedded into the binary)
# ---------------------------------------------------------------------------
FROM node:24-alpine AS web-builder
WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --loglevel=error --fund=false
COPY web/ ./
RUN npm run build

# ---------------------------------------------------------------------------
# Stage 2: build the static Go server (pure-Go SQLite, no cgo toolchain)
# ---------------------------------------------------------------------------
FROM golang:1.27.2-alpine AS go-builder
ENV GOTOOLCHAIN=local GOFLAGS=-mod=readonly
WORKDIR /build
COPY go.mod go.sum ./
# Local replace targets must exist before go mod download reads go.mod.
COPY third_party ./third_party
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# web/dist comes from stage 1, never from the build context (see .dockerignore)
COPY --from=web-builder /build/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" -o /out/fileengine .

# ---------------------------------------------------------------------------
# Stage 3: runtime — single binary + entrypoint, state lives in /data
# ---------------------------------------------------------------------------
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata su-exec \
    && adduser -D -u 10001 fileengine

COPY --from=go-builder /out/fileengine /app/fileengine
COPY deploy/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

# /data holds config.yaml + fileengine.db (bind-mounted by compose).
ENV FE_DATA_DIR=/data
# Inside the container the server must listen on all interfaces to be
# reachable through the published port; host exposure is decided by the
# compose port binding (127.0.0.1 by default).
ENV FE_SERVER_HOST=0.0.0.0
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${FE_SERVER_PORT:-8080}/healthz" >/dev/null || exit 1

# Starts as root so the entrypoint can chown the bind-mounted /data, then
# drops to the fileengine user via su-exec.
ENTRYPOINT ["/app/docker-entrypoint.sh"]
