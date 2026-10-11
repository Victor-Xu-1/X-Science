#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
WATCHER="$SCRIPT_DIR/source-backend-watch.sh"
TMP_DIR=$(mktemp -d)
ROOT="$TMP_DIR/source"
STATE="$TMP_DIR/state"
BUILD_COUNT="$TMP_DIR/build-count"
START_COUNT="$TMP_DIR/start-count"
FAIL_NEXT_BUILD="$TMP_DIR/fail-next-build"
FAIL_NEXT_ASSET_VERIFY="$TMP_DIR/fail-next-asset-verify"
export SYNON_DEV_MODEL_ENV_FILE="$TMP_DIR/synthetic-model.env"
printf '# owned empty test configuration\n' >"$SYNON_DEV_MODEL_ENV_FILE"
RUNTIME_HEALTH="$TMP_DIR/runtime-health.json"
WATCHER_PID=""
PROBE_HELP_PID_FILE="$TMP_DIR/probe-help.pid"
PROBE_LEGACY_PID_FILE="$TMP_DIR/probe-legacy.pid"
PROBE_BINARY="$TMP_DIR/probe-state/synon-go"

cleanup() {
  # The stalled native help fixture ignores TERM. Kill only its recorded owned
  # fixture when an assertion interrupts the probe; the foreground timeout
  # remains its parent and reaps it before we wait for the outer watcher.
  if [[ -f "$PROBE_HELP_PID_FILE" ]]; then
    local probe_pid="$(<"$PROBE_HELP_PID_FILE")"
    if kill -0 "$probe_pid" 2>/dev/null &&
        grep -aFq "$PROBE_BINARY" "/proc/$probe_pid/cmdline" 2>/dev/null; then
      kill -KILL "$probe_pid" 2>/dev/null || true
    fi
  fi
  if [[ -n "$WATCHER_PID" ]] && kill -0 "$WATCHER_PID" 2>/dev/null; then
    kill -TERM "$WATCHER_PID" 2>/dev/null || true
    wait "$WATCHER_PID" 2>/dev/null || true
  fi
  rm -rf -- "$TMP_DIR"
}
trap cleanup EXIT INT TERM

mkdir -p \
  "$ROOT/cmd" \
  "$ROOT/internal" \
	"$ROOT/internal/testdata" \
	"$ROOT/internal/persistence/workspace" \
  "$ROOT/assets" \
  "$ROOT/skills" \
  "$ROOT/scripts/dev" \
  "$ROOT/scripts/product-identity" \
  "$STATE"
[[ ! -e "$ROOT/tools" ]] || { echo 'fixture unexpectedly contains retired root tools directory' >&2; exit 1; }
printf 'module example.invalid/source-backend-watch-test\n' >"$ROOT/go.mod"
printf 'package internal\n' >"$ROOT/internal/backend.go"
printf 'fixture v1\n' >"$ROOT/internal/testdata/input.txt"
printf 'package workspace\n' >"$ROOT/internal/persistence/workspace/versioned_schema.go"
printf 'package workspace\n' >"$ROOT/internal/persistence/workspace/example_schema_migration.go"
printf 'package workspace\n' >"$ROOT/internal/persistence/workspace/example_schema_migration_test.go"
printf '#!/usr/bin/env bash\n' >"$ROOT/scripts/dev/frontend-only.sh"
printf '{"active_session_runs":0,"runtime_draining":false}\n' >"$RUNTIME_HEALTH"

cat >"$TMP_DIR/fake-go" <<'FAKE_GO'
#!/usr/bin/env bash
set -Eeuo pipefail
if [[ "${1:-}" == "env" && "${2:-}" == "GOVERSION" ]]; then
  echo go1.26.5
  exit 0
fi
if [[ "${1:-}" == "run" ]]; then
  printf 'x-science\tX-Science\n'
  exit 0
fi
if [[ "${1:-}" != "build" ]]; then
  echo "unexpected fake go invocation: $*" >&2
  exit 2
fi
output=""
while (( $# > 0 )); do
  if [[ "$1" == "-o" ]]; then
    shift
    output="${1:-}"
    break
  fi
  shift
done
[[ -n "$output" ]] || { echo 'fake go build did not receive -o' >&2; exit 2; }
count=0
[[ ! -f "$FAKE_BUILD_COUNT" ]] || count=$(<"$FAKE_BUILD_COUNT")
printf '%s\n' "$((count + 1))" >"$FAKE_BUILD_COUNT"
if [[ -f "$FAKE_FAIL_NEXT_BUILD" ]]; then
  rm -f "$FAKE_FAIL_NEXT_BUILD"
  echo 'injected build failure' >&2
  exit 1
fi
cat >"$output" <<'FAKE_BACKEND'
#!/usr/bin/env bash
set -Eeuo pipefail
if [[ "${1:-}" == "serve" && "${2:-}" == "--help" ]]; then
  printf 'Usage of serve:\n  -runtime-endpoint-file string\n'
  exit 0
fi
if [[ "${1:-}" == "runtime-endpoint" ]]; then
  shift
  python3 - "$@" <<'PY'
import argparse, json
parser = argparse.ArgumentParser()
parser.add_argument('--file', required=True)
parser.add_argument('--pid', required=True, type=int)
args = parser.parse_args()
with open(args.file) as handle:
    receipt = json.load(handle)
assert receipt['schema'] == 1 and receipt['pid'] == args.pid
print(receipt['health_url'])
PY
  exit 0
fi
if [[ "${1:-}" == "assets" && "${2:-}" == "verify" ]]; then
  if [[ -f "$FAKE_FAIL_NEXT_ASSET_VERIFY" ]]; then
    rm -f "$FAKE_FAIL_NEXT_ASSET_VERIFY"
    echo 'injected asset verification failure' >&2
    exit 1
  fi
  exit 0
fi
if [[ "${1:-}" == "serve" && "${2:-}" == "--runtime-endpoint-file" ]]; then
  python3 - "$3" "$$" "${FAKE_HEALTH_URL:-http://127.0.0.1:18886/health}" <<'PY'
import json, os, sys
with open(sys.argv[1], 'x') as handle:
    os.chmod(sys.argv[1], 0o600)
    json.dump({'schema': 1, 'pid': int(sys.argv[2]), 'health_url': sys.argv[3]}, handle)
PY
fi
count=0
[[ ! -f "$FAKE_START_COUNT" ]] || count=$(<"$FAKE_START_COUNT")
printf '%s\n' "$((count + 1))" >"$FAKE_START_COUNT"
trap 'exit 0' INT TERM USR1
while true; do sleep 0.1; done
FAKE_BACKEND
FAKE_GO
chmod 0755 "$TMP_DIR/fake-go"
ln -s "$TMP_DIR/fake-go" "$TMP_DIR/go"
cat >"$TMP_DIR/old-go" <<'OLD_GO'
#!/usr/bin/env bash
[[ "${1:-}" == env && "${2:-}" == GOVERSION ]] && { echo go1.25.9; exit; }
exit 2
OLD_GO
chmod 0755 "$TMP_DIR/old-go"
if GO="$TMP_DIR/old-go" SYNON_SOURCE_ROOT="$ROOT" bash "$WATCHER" >"$TMP_DIR/old-go.log" 2>&1; then
  echo 'unsupported Go version passed backend watcher preflight' >&2
  exit 1
fi
grep -Fq 'Go 1.26 or newer is required; found: go1.25.9' "$TMP_DIR/old-go.log" || {
  echo 'unsupported Go version was not diagnosed' >&2
  cat "$TMP_DIR/old-go.log" >&2
  exit 1
}

wait_for_value() {
  local file="$1"
  local expected="$2"
  for _ in $(seq 1 100); do
    if [[ -f "$file" ]] && [[ "$(<"$file")" == "$expected" ]]; then
      return 0
    fi
    sleep 0.05
  done
  echo "timed out waiting for $file=$expected" >&2
  return 1
}

run_probe_timeout_regression() {
  local probe_state="$TMP_DIR/probe-state"
  mkdir -p "$probe_state"
  # A cached accepted binary starts through the migration-deferral path.
  printf 'previous-accepted-migration\n' >"$probe_state/accepted-migration-digest"
  cat >"$PROBE_BINARY" <<'PROBE_BINARY'
#!/usr/bin/env bash
set -Eeuo pipefail
if [[ "${1:-}" == "serve" && "${2:-}" == "--help" ]]; then
  # A native TERM-ignoring leaf isolates timeout's own wait/reap contract.
  # Spawning sleep grandchildren would test an unrelated descendant owner.
  exec python3 - "$FAKE_PROBE_HELP_PID" "$0" <<'PROBE_HELP'
import os, signal, sys
signal.signal(signal.SIGTERM, signal.SIG_IGN)
with open(sys.argv[1], 'w') as handle:
    handle.write(str(os.getpid()) + '\n')
while True:
    signal.pause()
PROBE_HELP
fi
[[ "$*" != *'--runtime-endpoint-file'* ]] || exit 2
printf '%s\n' "$$" >"$FAKE_PROBE_LEGACY_PID"
trap 'exit 0' INT TERM USR1
while true; do sleep .05; done
PROBE_BINARY
  chmod 0755 "$PROBE_BINARY"
  FAKE_PROBE_HELP_PID="$PROBE_HELP_PID_FILE" \
  FAKE_PROBE_LEGACY_PID="$PROBE_LEGACY_PID_FILE" \
  SYNON_SOURCE_ROOT="$ROOT" \
  SYNON_DEV_BUILD_DIR="$probe_state" \
  SYNON_DEV_POLL_SECONDS=.05 \
  GO="$TMP_DIR/fake-go" \
    bash "$WATCHER" >"$TMP_DIR/probe-watcher.log" 2>&1 &
  WATCHER_PID=$!
  # This independent outer deadline fails rather than hanging if timeout's
  # initial TERM is ignored. Startup must complete by the eighth second.
  for _ in $(seq 1 160); do
    [[ -f "$PROBE_LEGACY_PID_FILE" ]] && break
    kill -0 "$WATCHER_PID" 2>/dev/null || break
    sleep .05
  done
  [[ -f "$PROBE_LEGACY_PID_FILE" ]] || {
    echo 'read-only legacy help probe exceeded its outer deadline' >&2
    return 1
  }
  [[ -f "$PROBE_HELP_PID_FILE" ]] || { echo 'help fixture never ran' >&2; return 1; }
  local help_pid="$(<"$PROBE_HELP_PID_FILE")"
  printf 'source-backend-probe-help-pid:%s\n' "$help_pid"
  ! kill -0 "$help_pid" 2>/dev/null || {
    echo 'read-only legacy help process survived the bounded probe' >&2
    return 1
  }
  kill -0 "$(<"$PROBE_LEGACY_PID_FILE")" 2>/dev/null || {
    echo 'legacy accepted child did not remain usable after failed probe' >&2
    return 1
  }
  grep -Fq 'automatic cutover is deferred until an explicit idle-drain handoff' "$TMP_DIR/probe-watcher.log"
  kill -TERM "$WATCHER_PID"
  wait "$WATCHER_PID"
  WATCHER_PID=""
  ! kill -0 "$(<"$PROBE_LEGACY_PID_FILE")" 2>/dev/null || {
    echo 'owned legacy child survived watcher cleanup' >&2
    return 1
  }
}

if [[ "${1:-}" == "--probe-timeout-only" ]]; then
  run_probe_timeout_regression
  echo 'source-backend-watch-probe-timeout: ok'
  exit 0
fi

FAKE_BUILD_COUNT="$BUILD_COUNT" \
FAKE_START_COUNT="$START_COUNT" \
FAKE_FAIL_NEXT_BUILD="$FAIL_NEXT_BUILD" \
FAKE_FAIL_NEXT_ASSET_VERIFY="$FAIL_NEXT_ASSET_VERIFY" \
SYNON_SOURCE_ROOT="$ROOT" \
SYNON_DEV_BUILD_DIR="$STATE" \
SYNON_DEV_POLL_SECONDS=0.05 \
SYNON_DEV_BUILD_RETRY_MIN_SECONDS=1 \
SYNON_DEV_BUILD_RETRY_MAX_SECONDS=2 \
SYNON_DEV_RUNTIME_HEALTH_FILE="$RUNTIME_HEALTH" \
PATH="$TMP_DIR:$PATH" \
  env -u GO bash "$WATCHER" >"$TMP_DIR/watcher.log" 2>&1 &
WATCHER_PID=$!

wait_for_value "$BUILD_COUNT" 1
wait_for_value "$START_COUNT" 1
[[ -s "$STATE/accepted-migration-digest" ]] || {
	echo 'initial verified build did not establish its migration digest' >&2
	exit 1
}

set +e
FAKE_BUILD_COUNT="$BUILD_COUNT" \
FAKE_START_COUNT="$START_COUNT" \
FAKE_FAIL_NEXT_BUILD="$FAIL_NEXT_BUILD" \
FAKE_FAIL_NEXT_ASSET_VERIFY="$FAIL_NEXT_ASSET_VERIFY" \
SYNON_SOURCE_ROOT="$ROOT" \
SYNON_DEV_BUILD_DIR="$STATE" \
SYNON_DEV_POLL_SECONDS=0.05 \
SYNON_DEV_BUILD_RETRY_MIN_SECONDS=1 \
SYNON_DEV_BUILD_RETRY_MAX_SECONDS=2 \
SYNON_DEV_RUNTIME_HEALTH_FILE="$RUNTIME_HEALTH" \
GO="$TMP_DIR/fake-go" \
  bash "$WATCHER" >"$TMP_DIR/second-watcher.log" 2>&1
second_status=$?
set -e
[[ "$second_status" == 73 ]] || {
  echo "second watcher status=$second_status, want 73" >&2
  exit 1
}
grep -q 'another watcher already owns this build directory' "$TMP_DIR/second-watcher.log" || {
  echo 'second watcher did not report the single-owner boundary' >&2
  exit 1
}
[[ "$(<"$BUILD_COUNT")" == 1 && "$(<"$START_COUNT")" == 1 ]] || {
  echo 'rejected watcher changed the active backend' >&2
  exit 1
}

# Go test files are verification inputs, not production binary or migration
# inputs. Editing one must not restart the user's live backend or require an
# operator to accept a new production schema digest.
printf '// migration test changed\n' >>"$ROOT/internal/persistence/workspace/example_schema_migration_test.go"
sleep 0.4
[[ "$(<"$BUILD_COUNT")" == 1 && "$(<"$START_COUNT")" == 1 ]] || {
	echo 'test-only migration change rebuilt or restarted the backend' >&2
	exit 1
}
[[ ! -e "$STATE/pending-migration-digest" ]] || {
	echo 'test-only migration change produced a pending production digest' >&2
	exit 1
}

# Go testdata directories are test fixtures and are ignored by production
# package builds. Editing a fixture must not rebuild or restart the live API.
printf 'fixture v2\n' >>"$ROOT/internal/testdata/input.txt"
sleep 0.4
[[ "$(<"$BUILD_COUNT")" == 1 && "$(<"$START_COUNT")" == 1 ]] || {
	echo 'testdata-only change rebuilt or restarted the backend' >&2
	exit 1
}

# Migration inputs must never hot-deploy into the active user database.  The
# watcher keeps the accepted binary until an independently verified exact
# digest is promoted by the operator.
printf '// migration changed\n' >>"$ROOT/internal/persistence/workspace/example_schema_migration.go"
for _ in $(seq 1 100); do
	[[ -s "$STATE/pending-migration-digest" ]] && break
	sleep 0.05
done
[[ -s "$STATE/pending-migration-digest" ]] || {
	echo 'migration change did not produce a pending digest' >&2
	exit 1
}
[[ "$(<"$BUILD_COUNT")" == 1 && "$(<"$START_COUNT")" == 1 ]] || {
	echo 'unaccepted migration change replaced the active backend' >&2
	exit 1
}
grep -q 'migration inputs changed; keeping the accepted backend' "$TMP_DIR/watcher.log" || {
	echo 'unaccepted migration change was not diagnosed' >&2
	exit 1
}

# A service restart while a production migration is pending must continue to
# serve the accepted binary, then resume the candidate build after an operator
# accepts the pending digest.
kill -TERM "$WATCHER_PID"
wait "$WATCHER_PID"
WATCHER_PID=""
FAKE_BUILD_COUNT="$BUILD_COUNT" \
FAKE_START_COUNT="$START_COUNT" \
FAKE_FAIL_NEXT_BUILD="$FAIL_NEXT_BUILD" \
FAKE_FAIL_NEXT_ASSET_VERIFY="$FAIL_NEXT_ASSET_VERIFY" \
SYNON_SOURCE_ROOT="$ROOT" \
SYNON_DEV_BUILD_DIR="$STATE" \
SYNON_DEV_POLL_SECONDS=0.05 \
SYNON_DEV_BUILD_RETRY_MIN_SECONDS=1 \
SYNON_DEV_BUILD_RETRY_MAX_SECONDS=2 \
SYNON_DEV_RUNTIME_HEALTH_FILE="$RUNTIME_HEALTH" \
GO="$TMP_DIR/fake-go" \
  bash "$WATCHER" >"$TMP_DIR/resumed-watcher.log" 2>&1 &
WATCHER_PID=$!
wait_for_value "$START_COUNT" 2
[[ "$(<"$BUILD_COUNT")" == 1 ]] || {
	echo 'restarted watcher built an unaccepted migration' >&2
	exit 1
}
cp "$STATE/pending-migration-digest" "$STATE/accepted-migration-digest"
wait_for_value "$BUILD_COUNT" 2
wait_for_value "$START_COUNT" 3

# Frontend supervision is outside the backend dependency graph and must not
# interrupt an active API process.
printf '# frontend host changed\n' >>"$ROOT/scripts/dev/frontend-only.sh"
sleep 0.4
[[ "$(<"$BUILD_COUNT")" == 2 ]] || {
  echo 'frontend-only scripts/dev change rebuilt the backend' >&2
  exit 1
}
[[ "$(<"$START_COUNT")" == 3 ]] || {
  echo 'frontend-only scripts/dev change restarted the backend' >&2
  exit 1
}

# A verified candidate must remain pending while a task is active. Once the
# runtime reports idle, SIGUSR1 closes the admission race and activates that
# exact already-built candidate without interrupting the task.
printf '{"active_session_runs":1,"runtime_draining":false}\n' >"$RUNTIME_HEALTH"
printf '// busy-runtime candidate changed\n' >>"$ROOT/internal/backend.go"
wait_for_value "$BUILD_COUNT" 3
sleep 0.3
[[ "$(<"$START_COUNT")" == 3 ]] || {
  echo 'candidate activated while a task was active' >&2
  exit 1
}
grep -q 'activation deferred until active tasks finish' "$TMP_DIR/resumed-watcher.log" || {
  echo 'busy-runtime activation was not diagnosed' >&2
  exit 1
}
printf '{"active_session_runs":0,"runtime_draining":false}\n' >"$RUNTIME_HEALTH"
wait_for_value "$START_COUNT" 4

# A source snapshot whose built binary cannot verify its live asset manifests
# must never replace the active child. The unchanged coherent digest is retried
# with backoff and promoted only after verification succeeds.
touch "$FAIL_NEXT_ASSET_VERIFY"
printf '// asset-coherence candidate changed\n' >>"$ROOT/internal/backend.go"
wait_for_value "$BUILD_COUNT" 4
[[ "$(<"$START_COUNT")" == 4 ]] || {
  echo 'asset-invalid candidate replaced the active backend' >&2
  exit 1
}
wait_for_value "$BUILD_COUNT" 5
wait_for_value "$START_COUNT" 5
grep -q 'candidate asset verification failed; the previous backend remains active' "$TMP_DIR/resumed-watcher.log" || {
  echo 'asset-invalid candidate was not diagnosed' >&2
  exit 1
}

# A failed backend build keeps the active child and retries the unchanged
# digest after a bounded delay instead of waiting for an unrelated file touch.
touch "$FAIL_NEXT_BUILD"
printf '// backend source changed\n' >>"$ROOT/internal/backend.go"
wait_for_value "$BUILD_COUNT" 6
[[ "$(<"$START_COUNT")" == 5 ]] || {
	echo 'failed candidate replaced the active backend' >&2
	exit 1
}
wait_for_value "$BUILD_COUNT" 7
wait_for_value "$START_COUNT" 6
grep -q 'retrying failed digest' "$TMP_DIR/resumed-watcher.log" || {
  echo 'failed digest was not scheduled for retry' >&2
  exit 1
}

kill -TERM "$WATCHER_PID"
wait "$WATCHER_PID"
WATCHER_PID=""

# An accepted binary predating endpoint receipts must still start when the
# first build fails. It explicitly rejects the new flag, rather than ignoring it.
LEGACY_STATE="$TMP_DIR/legacy-state"
LEGACY_CHILD_PID="$TMP_DIR/legacy-child.pid"
LEGACY_BUILD_COUNT="$TMP_DIR/legacy-build-count"
LEGACY_START_COUNT="$TMP_DIR/legacy-start-count"
mkdir -p "$LEGACY_STATE"
cp "$STATE/accepted-migration-digest" "$LEGACY_STATE/accepted-migration-digest"
printf '0\n' >"$LEGACY_BUILD_COUNT"
printf '0\n' >"$LEGACY_START_COUNT"
cat >"$LEGACY_STATE/synon-go" <<'LEGACY_BACKEND'
#!/usr/bin/env bash
set -Eeuo pipefail
if [[ "${1:-}" == "serve" && "${2:-}" == "--help" ]]; then
  printf 'Usage of serve:\n  -health-json\n  -version\n'
  exit 0
fi
if [[ "$*" == *'--runtime-endpoint-file'* ]]; then
  echo 'legacy rejected unsupported endpoint flag' >&2
  exit 2
fi
printf '%s\n' "$$" >"$FAKE_LEGACY_CHILD_PID"
count=$(<"$FAKE_START_COUNT")
printf '%s\n' "$((count + 1))" >"$FAKE_START_COUNT"
trap 'exit 0' INT TERM USR1
while true; do sleep .05; done
LEGACY_BACKEND
chmod 0755 "$LEGACY_STATE/synon-go"
touch "$FAIL_NEXT_BUILD"
FAKE_BUILD_COUNT="$LEGACY_BUILD_COUNT" \
FAKE_START_COUNT="$LEGACY_START_COUNT" \
FAKE_LEGACY_CHILD_PID="$LEGACY_CHILD_PID" \
FAKE_FAIL_NEXT_BUILD="$FAIL_NEXT_BUILD" \
FAKE_FAIL_NEXT_ASSET_VERIFY="$FAIL_NEXT_ASSET_VERIFY" \
SYNON_SOURCE_ROOT="$ROOT" \
SYNON_DEV_BUILD_DIR="$LEGACY_STATE" \
SYNON_DEV_POLL_SECONDS=.05 \
SYNON_DEV_BUILD_RETRY_MIN_SECONDS=1 \
SYNON_DEV_BUILD_RETRY_MAX_SECONDS=2 \
GO="$TMP_DIR/fake-go" \
  bash "$WATCHER" >"$TMP_DIR/legacy-watcher.log" 2>&1 &
WATCHER_PID=$!
wait_for_value "$LEGACY_START_COUNT" 1
wait_for_value "$LEGACY_BUILD_COUNT" 2
sleep .15
[[ -f "$LEGACY_CHILD_PID" ]] || {
  echo 'legacy accepted child never started' >&2
  tail -20 "$TMP_DIR/legacy-watcher.log" >&2
  exit 1
}
[[ "$(<"$LEGACY_START_COUNT")" == 1 ]] && kill -0 "$(<"$LEGACY_CHILD_PID")" || {
  echo 'legacy accepted child was stopped or entered a restart loop' >&2
  exit 1
}
! grep -Fq 'legacy rejected unsupported endpoint flag' "$TMP_DIR/legacy-watcher.log" || {
  echo 'legacy accepted child received the unsupported endpoint flag' >&2
  exit 1
}
grep -Fq 'automatic cutover is deferred until an explicit idle-drain handoff' "$TMP_DIR/legacy-watcher.log" || {
  echo 'missing legacy endpoint limitation was not diagnosed' >&2
  exit 1
}
# With no trusted legacy receipt, automatic activation stays fail-closed. An
# explicit native idle-drain signal can then hand off to the built new binary.
kill -USR1 "$(<"$LEGACY_CHILD_PID")"
wait_for_value "$LEGACY_START_COUNT" 2
find "$LEGACY_STATE" -maxdepth 2 -name endpoint.json -type f | grep -q . || {
  echo 'new child did not publish its owned endpoint after legacy handoff' >&2
  exit 1
}
kill -TERM "$WATCHER_PID"
wait "$WATCHER_PID"
WATCHER_PID=""

# HTTP probes follow the owned child's bound endpoint, not a reconstructed
# environment address or a fixed port. No formal listener is occupied or changed.
# These cases intentionally omit SYNON_DEV_RUNTIME_HEALTH_FILE.
find_free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

port_is_open() {
  timeout 1 bash -c "exec 3<>/dev/tcp/127.0.0.1/$1" >/dev/null 2>&1
}

wait_for_port() {
  local port="$1"
  for _ in $(seq 1 100); do
    if port_is_open "$port"; then
      return 0
    fi
    sleep 0.05
  done
  return 1
}

cat >"$TMP_DIR/health-server.py" <<'PY'
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

port = int(sys.argv[1])
body_path = sys.argv[2]

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != "/health":
            self.send_response(404)
            self.end_headers()
            return
        with open(body_path, "rb") as handle:
            payload = handle.read()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, _format, *_args):
        return

HTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY

start_health_server() {
  local port="$1"
  local body_file="$2"
  python3 "$TMP_DIR/health-server.py" "$port" "$body_file" &
  HEALTH_SERVER_PID=$!
}

HTTP_STATE="$TMP_DIR/http-state"
HTTP_BUILD_COUNT="$TMP_DIR/http-build-count"
HTTP_START_COUNT="$TMP_DIR/http-start-count"
SUPERVISED_HEALTH="$TMP_DIR/supervised-health.json"
DECOY_HEALTH="$TMP_DIR/decoy-health.json"
HTTP_WATCHER_PID=""
SUPERVISED_HEALTH_PID=""
DECOY_HEALTH_PID=""

http_health_cleanup() {
  if [[ -n "$HTTP_WATCHER_PID" ]] && kill -0 "$HTTP_WATCHER_PID" 2>/dev/null; then
    kill -TERM "$HTTP_WATCHER_PID" 2>/dev/null || true
    wait "$HTTP_WATCHER_PID" 2>/dev/null || true
    HTTP_WATCHER_PID=""
  fi
  for pid_var in SUPERVISED_HEALTH_PID DECOY_HEALTH_PID; do
    local pid="${!pid_var}"
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
      printf -v "$pid_var" ''
    fi
  done
}
trap 'http_health_cleanup; cleanup' EXIT INT TERM

mkdir -p "$HTTP_STATE"
printf '0\n' >"$HTTP_BUILD_COUNT"
printf '0\n' >"$HTTP_START_COUNT"
printf '{"active_session_runs":0,"runtime_draining":false}\n' >"$SUPERVISED_HEALTH"
printf '{"active_session_runs":1,"runtime_draining":false}\n' >"$DECOY_HEALTH"

SUPERVISED_HEALTH_PORT="$(find_free_port)"
start_health_server "$SUPERVISED_HEALTH_PORT" "$SUPERVISED_HEALTH"
SUPERVISED_HEALTH_PID="$HEALTH_SERVER_PID"
if ! wait_for_port "$SUPERVISED_HEALTH_PORT"; then
  echo "supervised health server did not bind: $SUPERVISED_HEALTH_PORT" >&2
  exit 1
fi

run_http_watcher() {
  local log_file="$1"
  FAKE_BUILD_COUNT="$HTTP_BUILD_COUNT" \
  FAKE_START_COUNT="$HTTP_START_COUNT" \
  FAKE_FAIL_NEXT_BUILD="$FAIL_NEXT_BUILD" \
  FAKE_FAIL_NEXT_ASSET_VERIFY="$FAIL_NEXT_ASSET_VERIFY" \
  FAKE_HEALTH_URL="http://127.0.0.1:$SUPERVISED_HEALTH_PORT/health" \
  SYNON_SOURCE_ROOT="$ROOT" \
  SYNON_DEV_BUILD_DIR="$HTTP_STATE" \
  SYNON_DEV_POLL_SECONDS=0.05 \
  SYNON_DEV_BUILD_RETRY_MIN_SECONDS=1 \
  SYNON_DEV_BUILD_RETRY_MAX_SECONDS=2 \
  SYNON_ADDRESS="${MISLEADING_ADDRESS:-127.0.0.1:$SUPERVISED_HEALTH_PORT}" \
  GO="$TMP_DIR/fake-go" \
    bash "$WATCHER" >"$log_file" 2>&1 &
  HTTP_WATCHER_PID=$!
}

run_http_watcher "$TMP_DIR/http-watcher-owned-endpoint.log"
wait_for_value "$HTTP_BUILD_COUNT" 1
wait_for_value "$HTTP_START_COUNT" 1

printf '// http custom-port candidate changed\n' >>"$ROOT/internal/backend.go"
wait_for_value "$HTTP_BUILD_COUNT" 2
wait_for_value "$HTTP_START_COUNT" 2

http_health_cleanup

DECOY_HEALTH_PORT="$(find_free_port)"
start_health_server "$DECOY_HEALTH_PORT" "$DECOY_HEALTH"
DECOY_HEALTH_PID="$HEALTH_SERVER_PID"
if ! wait_for_port "$DECOY_HEALTH_PORT"; then
  echo 'isolated decoy health server did not bind' >&2
  exit 1
fi

SUPERVISED_HEALTH_PORT="$(find_free_port)"
start_health_server "$SUPERVISED_HEALTH_PORT" "$SUPERVISED_HEALTH"
SUPERVISED_HEALTH_PID="$HEALTH_SERVER_PID"
if ! wait_for_port "$SUPERVISED_HEALTH_PORT"; then
  echo "supervised health server did not restart on: $SUPERVISED_HEALTH_PORT" >&2
  exit 1
fi

printf '0\n' >"$HTTP_BUILD_COUNT"
printf '0\n' >"$HTTP_START_COUNT"
rm -rf "$HTTP_STATE"
mkdir -p "$HTTP_STATE"
printf '{"active_session_runs":0,"runtime_draining":false}\n' >"$SUPERVISED_HEALTH"
printf '{"active_session_runs":1,"runtime_draining":false}\n' >"$DECOY_HEALTH"

MISLEADING_ADDRESS="127.0.0.1:$DECOY_HEALTH_PORT" run_http_watcher "$TMP_DIR/http-watcher-busy-decoy.log"
wait_for_value "$HTTP_BUILD_COUNT" 1
wait_for_value "$HTTP_START_COUNT" 1

printf '// http busy-decoy candidate changed\n' >>"$ROOT/internal/backend.go"
wait_for_value "$HTTP_BUILD_COUNT" 2
wait_for_value "$HTTP_START_COUNT" 2

http_health_cleanup
if find "$HTTP_STATE" -maxdepth 1 -type d -name 'runtime-endpoint.*' | grep -q .; then
  echo 'owned endpoint generation survived watcher cleanup' >&2
  exit 1
fi
trap cleanup EXIT INT TERM

run_probe_timeout_regression

echo 'source-backend-watch-test: ok'
