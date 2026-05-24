#!/usr/bin/env bash
# Build the SpotiFLAC web server: compiles the frontend, copies the bundle into
# cmd/server/frontend_dist/ (the go:embed target), then builds the Go binary.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST_SRC="${ROOT}/frontend/dist"
EMBED_DIR="${ROOT}/cmd/server/frontend_dist"
BIN="${ROOT}/spotiflac-server"

cd "${ROOT}/frontend"
if [ ! -d node_modules ]; then
  pnpm install
fi
pnpm run build

rm -rf "${EMBED_DIR}"
mkdir -p "${EMBED_DIR}"
cp -r "${DIST_SRC}/." "${EMBED_DIR}/"

cd "${ROOT}"
go build -o "${BIN}" ./cmd/server

echo "✓ built ${BIN}"
