#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WAILS_DIR="${ROOT_DIR}/wails"

PLATFORM="auto"
ARCH="auto"
SKIP_BACKEND=0

usage() {
  cat <<'EOF'
Usage: ./build-wails.sh [--platform mac|linux|windows] [--arch amd64|arm64] [--skip-backend]

Environment:
  VERSION=<release version; defaults to git describe, then dev>
  WAILS=/path/to/wails   (override wails v2 CLI resolution)

Notes:
  The backend requires CGO (mattn/go-sqlite3). Cross-platform builds
  (--platform different from the host OS) need a matching cross C toolchain
  (e.g. zig cc / osxcross); native builds work out of the box.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --platform) PLATFORM="$2"; shift ;;
    --arch) ARCH="$2"; shift ;;
    --skip-backend) SKIP_BACKEND=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown: $1"; usage >&2; exit 1 ;;
  esac
  shift
done

log() { printf '\n==> %s\n' "$*" >&2; }

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "ERROR: required command not found: $1" >&2
    exit 1
  fi
}

detect_os() {
  case "$(uname -s | tr '[:upper:]' '[:lower:]')" in
    darwin) echo "darwin" ;; linux) echo "linux" ;; msys*|mingw*|cygwin*) echo "windows" ;; *) echo "linux" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;; arm64|aarch64) echo "arm64" ;; *) echo "amd64" ;;
  esac
}

HOST_OS=$(detect_os)
[[ "$PLATFORM" == "auto" ]] && PLATFORM=$(detect_os)
[[ "$PLATFORM" == "mac" ]] && PLATFORM="darwin"
[[ "$ARCH" == "auto" ]] && ARCH=$(detect_arch)

case "$PLATFORM" in
  darwin|linux|windows) ;;
  *) echo "ERROR: unsupported platform: $PLATFORM" >&2; exit 1 ;;
esac
case "$ARCH" in
  amd64|arm64) ;;
  *) echo "ERROR: unsupported arch: $ARCH" >&2; exit 1 ;;
esac

resolve_version() {
  if [[ -n "${VERSION:-}" ]]; then
    echo "${VERSION#v}"
    return
  fi
  local desc
  if desc=$(git -C "$ROOT_DIR" describe --tags --dirty --always 2>/dev/null); then
    echo "${desc#v}"
    return
  fi
  echo "dev"
}

# resolve_wails finds the wails v2 CLI: $WAILS override, then PATH, then
# GOPATH/bin, then installs it pinned to the version in wails/go.mod.
resolve_wails() {
  if [[ -n "${WAILS:-}" ]]; then
    command -v "$WAILS" >/dev/null 2>&1 || { echo "ERROR: WAILS=$WAILS not executable" >&2; exit 1; }
    command -v "$WAILS"
    return
  fi
  if command -v wails >/dev/null 2>&1; then
    command -v wails
    return
  fi
  local gopath candidate
  gopath="$(go env GOPATH)"
  candidate="${gopath}/bin/wails$(go env GOEXE 2>/dev/null || true)"
  if [[ -x "$candidate" ]]; then
    echo "$candidate"
    return
  fi
  local version
  version="$(cd "$WAILS_DIR" && go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2 2>/dev/null || echo latest)"
  log "Installing Wails v2 CLI ($version)"
  go install "github.com/wailsapp/wails/v2/cmd/wails@${version}"
  [[ -x "$candidate" ]] || { echo "ERROR: installed wails but cannot find $candidate" >&2; exit 1; }
  echo "$candidate"
}

need_cmd go
if [[ "$SKIP_BACKEND" -eq 0 ]]; then
  need_cmd npm
fi

VERSION="$(resolve_version)"
WAILS_BIN="$(resolve_wails)"

echo "=== FileEngine Wails Build ==="
echo "Platform: $PLATFORM, Arch: $ARCH, Version: $VERSION"
log "Using Wails CLI: $WAILS_BIN"

# Build backend
if [[ "$SKIP_BACKEND" -eq 0 ]]; then
  if [[ "$PLATFORM" != "$HOST_OS" ]]; then
    log "WARNING: cross-compiling for $PLATFORM on $HOST_OS — the backend needs CGO (go-sqlite3); a cross C toolchain is required"
  fi
  log "Building frontend assets"
  cd "$ROOT_DIR/web" && npm ci --loglevel=error --fund=false && npm run build && cd "$ROOT_DIR"
  log "Building backend for ${PLATFORM}/${ARCH}"
  CGO_ENABLED=1 GOOS="$PLATFORM" GOARCH="$ARCH" \
    go build -trimpath -ldflags="-s -w" -o "fileengine_${PLATFORM}_${ARCH}" .
fi

BACKEND_BIN=""
EXT=""
[[ "$PLATFORM" == "windows" ]] && EXT=".exe"

for candidate in \
  "${ROOT_DIR}/fileengine_${PLATFORM}_${ARCH}${EXT}" \
  "${ROOT_DIR}/fileengine${EXT}"; do
  if [[ -f "$candidate" ]]; then
    BACKEND_BIN="$candidate"
    break
  fi
done

if [[ -z "$BACKEND_BIN" ]]; then
  echo "ERROR: Backend binary not found" >&2
  exit 1
fi

echo "Using backend: $BACKEND_BIN"
mkdir -p "${WAILS_DIR}/bin"
cp "$BACKEND_BIN" "${WAILS_DIR}/bin/backend.bin"

# Build Wails
cd "$WAILS_DIR"

# Ensure go.sum exists
if [[ ! -f go.sum ]]; then
  GOPROXY=https://goproxy.cn,direct go mod tidy
fi

log "Building Wails app..."
LDFLAGS="-s -w -X main.Version=${VERSION}"
if [[ "$PLATFORM" == "windows" ]]; then
  LDFLAGS="-s -w -H windowsgui -X main.Version=${VERSION}"
fi
GOARCH="$ARCH" "$WAILS_BIN" build -platform "${PLATFORM}/${ARCH}" -clean -ldflags "$LDFLAGS" -skipbindings -s

# Package
OUTPUT_DIR="${WAILS_DIR}/build/bin"
mkdir -p "$OUTPUT_DIR"

OUTPUT_NAME="fileengine-desktop_${PLATFORM}_${ARCH}"

if [[ "$PLATFORM" == "darwin" ]]; then
  # macOS: Wails outputs a .app bundle
  WAILS_BIN_OUT=$(find "${OUTPUT_DIR}" -name "*.app" -maxdepth 1 | head -1)
  if [[ -z "$WAILS_BIN_OUT" ]]; then
    echo "ERROR: Wails .app bundle not found" >&2
    exit 1
  fi
  cd "$OUTPUT_DIR"
  tar -czf "${OUTPUT_NAME}.tar.gz" "$(basename "$WAILS_BIN_OUT")"
elif [[ "$PLATFORM" == "windows" ]]; then
  WAILS_BIN_OUT=$(find "${OUTPUT_DIR}" -name "fileengine-desktop*.exe" -maxdepth 1 | head -1)
  if [[ -z "$WAILS_BIN_OUT" ]]; then
    echo "ERROR: Wails exe not found" >&2
    exit 1
  fi
  cd "$OUTPUT_DIR"
  powershell -Command "Compress-Archive -Path '$(basename "$WAILS_BIN_OUT")' -DestinationPath '${OUTPUT_NAME}.zip'"
else
  WAILS_BIN_OUT=$(find "${OUTPUT_DIR}" -name "fileengine-desktop" -maxdepth 1 | head -1)
  if [[ -z "$WAILS_BIN_OUT" ]]; then
    echo "ERROR: Wails binary not found" >&2
    exit 1
  fi
  cd "$OUTPUT_DIR"
  tar -czf "${OUTPUT_NAME}.tar.gz" "$(basename "$WAILS_BIN_OUT")"
fi

echo "=== Build complete: ${OUTPUT_DIR}/${OUTPUT_NAME}.* ==="
