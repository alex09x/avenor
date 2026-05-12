#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WATCHER="${WATCHER:-$HOME/.botfiles/skills/jockey-delegate/scripts/stableboy-watch.sh}"
SENTINEL="${SENTINEL:-/tmp/avenor-parity-sentinel}"
AVENOR_BIN="${AVENOR_BIN:-}"

export GOCACHE="${GOCACHE:-/tmp/avenor-gocache}"

if [ -z "$AVENOR_BIN" ]; then
  AVENOR_BIN="/tmp/avenor-watch-parity"
  (cd "$ROOT" && go build -o "$AVENOR_BIN" ./cmd/avenor)
fi

printf 'DONE\n' > "$SENTINEL"

fixtures=(
  "events-vocabulary"
  "happy-path"
  "permission-request"
)

status=0
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

for fixture in "${fixtures[@]}"; do
  input="$ROOT/testdata/digest/$fixture.ndjson"
  golden="$ROOT/testdata/digest/$fixture.golden"
  go_out="$tmpdir/$fixture.go"
  bash_out="$tmpdir/$fixture.bash"
  raw_bash_out="$tmpdir/$fixture.bash.raw"

  "$AVENOR_BIN" watch "$input" > "$go_out"
  set +e
  if command -v timeout >/dev/null 2>&1; then
    timeout 4s bash "$WATCHER" "$input" "$SENTINEL" > "$raw_bash_out"
    watcher_status=$?
  else
    bash "$WATCHER" "$input" "$SENTINEL" > "$raw_bash_out"
    watcher_status=$?
  fi
  set -e
  if [ "$watcher_status" -ne 0 ] && [ "$watcher_status" -ne 124 ]; then
    echo "not ok $fixture (bash watcher exited $watcher_status)"
    status=1
    continue
  fi
  sed '/^SENTINEL: /d' "$raw_bash_out" > "$bash_out"

  if diff -u "$bash_out" "$go_out" >/dev/null; then
    echo "ok $fixture"
    continue
  fi

  if [ "$fixture" = "events-vocabulary" ] && diff -u "$golden" "$go_out" >/dev/null; then
    echo "ok $fixture (expected divergence: Go extracts .content.text; bash stringifies .content objects)"
    continue
  fi

  if [ "$fixture" = "permission-request" ] && diff -u "$golden" "$go_out" >/dev/null; then
    echo "ok $fixture (expected divergence: Go falls back to .tool when .question is empty; bash only falls back on null/missing)"
    continue
  fi

  echo "not ok $fixture"
  diff -u "$bash_out" "$go_out" || true
  status=1
done

exit "$status"
