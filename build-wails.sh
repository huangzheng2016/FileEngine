#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WAILS_DIR="${ROOT_DIR}/wails"

PLATFORM="auto"
ARCH="auto"
SKIP_BACKEND=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --platform) PLATFORM="$2"; shift ;;
    --arch) ARCH="$2"; shift ;;
    --skip-backend) SKIP_BACKEND=1 ;;
    -h|--help)
      echo "Usage: ./build-wails.sh [--platform mac|linux|windows] [--arch amd64|arm64] [--skip-backend]"
      exit 0 ;;
    *) echo "Unknown: $1"; exit 1 ;;
  esac
  shift
done

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

[[ "$PLATFORM" == "auto" ]] && PLATFORM=$(detect_os)
[[ "$PLATFORM" == "mac" ]] && PLATFORM="darwin"
[[ "$ARCH" == "auto" ]] && ARCH=$(detect_arch)

echo "=== FileEngine Wails Build ==="
echo "Platform: $PLATFORM, Arch: $ARCH"

# Find backend binary
if [[ "$SKIP_BACKEND" -eq 0 ]]; then
  echo "Building backend..."
  cd "$ROOT_DIR/web" && npm ci --loglevel=error --fund=false && npm run build && cd "$ROOT_DIR"
  CGO_ENABLED=1 GOARCH="$ARCH" go build -ldflags="-s -w" -o "fileengine_${PLATFORM}_${ARCH}" .
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
  echo "ERROR: Backend binary not found"
  exit 1
fi

echo "Using backend: $BACKEND_BIN"
cp "$BACKEND_BIN" "${WAILS_DIR}/bin/backend.bin"

# Build Wails
cd "$WAILS_DIR"

# Ensure go.sum exists
if [[ ! -f go.sum ]]; then
  GOPROXY=https://goproxy.cn,direct go mod tidy
fi

echo "Building Wails app..."
GOARCH="$ARCH" wails build -platform "${PLATFORM}/${ARCH}"

# Package
OUTPUT_DIR="${WAILS_DIR}/build/bin"
mkdir -p "$OUTPUT_DIR"

OUTPUT_NAME="fileengine-desktop_${PLATFORM}_${ARCH}"
WAILS_BIN=$(find "${WAILS_DIR}/build/bin" -name "fileengine-desktop*" -not -name "*.tar.gz" -not -name "*.zip" | head -1)

if [[ -z "$WAILS_BIN" ]]; then
  echo "ERROR: Wails build output not found"
  exit 1
fi

if [[ "$PLATFORM" == "windows" ]]; then
  cd "$OUTPUT_DIR"
  zip "${OUTPUT_NAME}.zip" "$(basename "$WAILS_BIN")"
else
  cd "$OUTPUT_DIR"
  tar -czf "${OUTPUT_NAME}.tar.gz" "$(basename "$WAILS_BIN")"
fi

echo "=== Build complete: ${OUTPUT_DIR}/${OUTPUT_NAME}.* ==="
