#!/usr/bin/env bash
# Produce a runtime Docker build context without credentials or live Server data.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
output=${1:?Usage: build-business-server.sh EMPTY_OUTPUT_DIRECTORY}
native_public=${2:-$root/portainer/dist/public}
test -f "$native_public/index.html" || {
  echo 'Build the native frontend first, or pass its public directory as the second argument.' >&2
  exit 2
}
if test -d "$output" && test -n "$(ls -A "$output")"; then
  echo 'Use an empty output directory.' >&2
  exit 2
fi
mkdir -p "$output"
output=$(cd "$output" && pwd)
(cd "$root/portainer" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags '-s -w' -o "$output/portainer" ./api/cmd/portainer)
cp "$root/deploy/server/Dockerfile.business" "$output/Dockerfile"
cp "$root/worker/business-events/voice_test.html" "$output/voice-test.html"
python3 - "$native_public" "$output/public" <<'PY'
from pathlib import Path
import shutil, sys
source, destination = map(Path, sys.argv[1:])
for path in source.rglob('*'):
    if not path.is_file() or path.suffix == '.map':
        continue
    target = destination / path.relative_to(source)
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(path, target)
PY
printf 'Runtime context ready: %s\n' "$output"
printf 'Build on the Server Docker host, record its immutable image ID, and pass it to build-ui.py --server-image.\n'
