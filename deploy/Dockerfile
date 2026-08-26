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
# Stage 2: build the Go backend (CGO required by gorm.io/driver/sqlite)
# ---------------------------------------------------------------------------
FROM golang:1.25-alpine AS go-builder
RUN apk add --no-cache build-base
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
    CGO_ENABLED=1 go build -ldflags="-s -w" -o /out/fileengine .

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
VOLUME ["/data"]
EXPOSE 8080

# Starts as root so the entrypoint can chown the bind-mounted /data, then
# drops to the fileengine user via su-exec.
ENTRYPOINT ["/app/docker-entrypoint.sh"]
