#!/usr/bin/env bash
# Compile the original application and native business administration together.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root/portainer"
node -e 'if(process.versions.node!=="22.22.1") throw Error("Use the pinned Node 22.22.1 toolchain")'
test "$(pnpm --version)" = 10.26.2 || { echo 'Use pnpm 10.26.2' >&2; exit 2; }
# The original lock has no pnpmfileChecksum. Keep its exact dependency graph;
# installing the optional types-fixer hook would otherwise rewrite resolution.
pnpm install --frozen-lockfile --ignore-pnpmfile --ignore-scripts
pnpm exec tsc --noEmit
NODE_OPTIONS=--max-old-space-size=8192 pnpm exec webpack --config webpack/webpack.production.js
python3 - "$root/portainer/dist/public" <<'PY'
from pathlib import Path
import hashlib, json, sys
public = Path(sys.argv[1])
files = {str(p.relative_to(public)): hashlib.sha256(p.read_bytes()).hexdigest()
         for p in sorted(public.rglob('*')) if p.is_file() and p.suffix != '.map' and p.name != 'native-manifest.json'}
if 'index.html' not in files:
    raise SystemExit('Native frontend index is missing')
(public / 'native-manifest.json').write_text(json.dumps({'schemaVersion': 1, 'nativeAdmin': True, 'files': files}, indent=2) + '\n')
print('Native frontend built:', len(files), 'files')
PY
