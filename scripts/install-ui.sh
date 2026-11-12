#!/usr/bin/env bash
# Run in Server WSL. Persistent mode recreates the Server only on first enable.
set -euo pipefail

release=${1:?Usage: install-ui.sh RELEASE_DIRECTORY [CONTAINER] [--persistent]}
container=${2:-edgestack-server}
mode=${3:-}
case "$container" in edgestack-server) ;; *) echo 'Unexpected container name' >&2; exit 2 ;; esac
case "$mode" in ''|--persistent) ;; *) echo 'Unexpected install option' >&2; exit 2 ;; esac
test -f "$release/index.html"
test -f "$release/manifest.json"
test -d "$release/edgestack"

python3 - "$release" <<'PY'
import hashlib, json, sys
from pathlib import Path
p = Path(sys.argv[1]).resolve()
m = json.loads((p / 'manifest.json').read_text())
for name, expected in m['files'].items():
    f = (p / name).resolve()
    if not f.is_relative_to(p) or hashlib.sha256(f.read_bytes()).hexdigest() != expected:
        raise SystemExit('Release checksum failed: ' + name)
if m['brand'] != 'EdgeStack.io' or m['locale'] != 'zh-CN':
    raise SystemExit('Unexpected release metadata')
print('Release verified:', m['version'], 'phrases:', m['phrase_count'])
PY

test "$(docker inspect --format '{{.State.Running}}' "$container")" = true
expected_image=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["server_image"])' "$release/manifest.json")
test "$(docker inspect --format '{{.Config.Image}}' "$container")" = "$expected_image" || {
  echo 'Server image differs from the pinned UI version; review compatibility first.' >&2
  exit 1
}
backup_root="$HOME/edgestack/ui-backups"
install -d -m 700 "$backup_root"
backup=$(mktemp -d "$backup_root/pre-ui-$(date -u +%Y%m%dT%H%M%SZ)-XXXXXX")
docker cp "$container:/public/index.html" "$backup/index.html"
if ! test -f "$backup_root/upstream-index.html"; then
  if python3 - "$backup/index.html" <<'PY'
from pathlib import Path
import sys
raise SystemExit('data-edgestack-ui' in Path(sys.argv[1]).read_text())
PY
  then
    cp "$backup/index.html" "$backup_root/upstream-index.html"
  else
    echo 'Save the original index.html from the pinned image before continuing.' >&2
    exit 1
  fi
fi

# Upload assets first, and change the entry page last.
if test "$mode" = --persistent; then
  config="$HOME/edgestack/server"
  test -f "$config/compose.yaml"
  test -f "$release/compose.ui.yaml"
  if test -f "$config/compose.override.yaml"; then
    cmp "$release/compose.ui.yaml" "$config/compose.override.yaml" || {
      echo 'Existing Compose override differs; refusing to overwrite it.' >&2
      exit 1
    }
    cp "$config/compose.override.yaml" "$backup/compose.override.yaml"
  fi
  if test -f "$config/ui/index.html"; then
    cp "$config/ui/index.html" "$backup/persisted-index.html"
  fi
  rollback_persistent() {
    status=$?
    trap - ERR
    if test -f "$backup/compose.override.yaml"; then
      cp "$backup/compose.override.yaml" "$config/compose.override.yaml"
    else
      rm -f "$config/compose.override.yaml"
    fi
    if test -f "$backup/persisted-index.html"; then
      cp "$backup/persisted-index.html" "$config/ui/index.html"
    fi
    (cd "$config" && docker compose up -d --pull never --no-deps portainer) >&2 || true
    exit "$status"
  }
  trap rollback_persistent ERR
  install -d -m 755 "$config/ui/edgestack"
  cp -a "$release/edgestack/." "$config/ui/edgestack/"
  # cp preserves the destination inode, so subsequent updates reach a live bind.
  cp "$release/index.html" "$config/ui/index.html"
  cp "$release/compose.ui.yaml" "$config/compose.override.yaml"
  (cd "$config" && docker compose config --quiet && docker compose up -d --pull never --no-deps portainer)
else
  test -z "$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/public/index.html"}}{{.Source}}{{end}}{{end}}' "$container")" || {
    echo 'UI is mounted read-only. Use --persistent to update the host files.' >&2
    exit 1
  }
  trap 'docker cp "$backup/index.html" "$container:/public/index.html" >&2 || true' ERR
  docker cp "$release/edgestack" "$container:/public/"
  docker cp "$release/index.html" "$container:/public/index.html"
fi
docker cp "$container:/public/index.html" "$backup/installed-index.html"
cmp "$release/index.html" "$backup/installed-index.html"
trap - ERR
printf 'UI installed. Previous entry page: %s/index.html\n' "$backup"
printf 'Server container: %s\n' "$(docker inspect --format '{{.Id}} {{.State.StartedAt}}' "$container")"
