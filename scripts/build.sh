#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/src"
DIST="$ROOT/dist"
VERSION="${VERSION:-$(tr -d '[:space:]' < "$ROOT/VERSION")}" 
test -n "$VERSION"
mkdir -p "$DIST"
cd "$SRC"
go test ./...
go vet ./...
if command -v node >/dev/null 2>&1; then
  node --check ui.js
fi
go build -buildvcs=false -buildmode=c-shared -trimpath -ldflags="-s -w -X main.pluginVersion=${VERSION}" -o "$DIST/key-chain-router-v${VERSION}.so" .
cp "$DIST/key-chain-router-v${VERSION}.so" "$DIST/key-chain-router.so"
sha256sum "$DIST/key-chain-router-v${VERSION}.so" "$DIST/key-chain-router.so"
