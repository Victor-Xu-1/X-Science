#!/usr/bin/env bash
set -euo pipefail

: "${FAKE_NODE_REAL_BIN:?real node executable is required}"
: "${FAKE_NPM_STATE:?fixture state is required}"

if [[ "${1:-}" == "--version" ]]; then
  version_calls=0
  if [[ -f "$FAKE_NPM_STATE/node-version-calls" ]]; then
    read -r version_calls <"$FAKE_NPM_STATE/node-version-calls"
  fi
  version_calls=$((version_calls + 1))
  printf '%s\n' "$version_calls" >"$FAKE_NPM_STATE/node-version-calls"
  # dependency_key hashes package bytes before querying tool versions. The
  # second query is the post-ci snapshot, not a timing-based approximation.
  if [[ "$version_calls" == "2" && -e "$FAKE_NPM_STATE/hold-install-key" ]]; then
    printf 'install-key-captured\n' >>"$FAKE_NPM_STATE/events"
    while [[ -e "$FAKE_NPM_STATE/hold-install-key" ]]; do
      sleep 0.01
    done
  fi
fi

if [[ "${1##*/}" == "frontend-dependency-watch.mjs" && -e "$FAKE_NPM_STATE/hold-watcher" ]]; then
  printf 'watcher-gated\n' >>"$FAKE_NPM_STATE/events"
  while [[ -e "$FAKE_NPM_STATE/hold-watcher" ]]; do
    sleep 0.01
  done
fi

exec "$FAKE_NODE_REAL_BIN" "$@"
