#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cat "$ROOT"/src/parts/main.go.* > "$ROOT/src/main.go"
cat "$ROOT"/src/parts/page.html.* > "$ROOT/src/page.html"
gofmt -w "$ROOT/src/main.go"
python3 - "$ROOT/src/page.html" "$ROOT/src/ui.js" <<'PY'
from pathlib import Path
import sys
html = Path(sys.argv[1]).read_text()
start = html.find('<script>')
end = html.rfind('</script>')
if start < 0 or end < start:
    raise SystemExit('script block not found in page.html')
Path(sys.argv[2]).write_text(html[start + len('<script>'):end] + '\n')
PY
