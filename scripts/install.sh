#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${1:-./plugins/linux/amd64}"
VERSION="${VERSION:-0.3.0}"
SRC="$ROOT/dist/key-chain-router-v${VERSION}.so"
if [[ ! -f "$SRC" ]]; then
  echo "Missing build artifact: $SRC" >&2
  exit 1
fi
mkdir -p "$TARGET"
cp "$SRC" "$TARGET/"
chmod 755 "$TARGET/key-chain-router-v${VERSION}.so"
echo "Installed: $TARGET/key-chain-router-v${VERSION}.so"
echo "Restart CPA to load the new plugin."
