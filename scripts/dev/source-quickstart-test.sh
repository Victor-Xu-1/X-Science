#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
QUICKSTART="$SCRIPT_DIR/source-quickstart.sh"
TMP_ROOT=$(realpath -e "${TMPDIR:-/tmp}")
TMP_DIR=$(mktemp -d "$TMP_ROOT/synon-source-quickstart-test.XXXXXX")
QUICKSTART_PID=''
OCCUPIED_PID=''
LOCK_HOLDER_PID=''
OWNED_START_PID=''

cleanup() {
  for pid in "$QUICKSTART_PID" "$OCCUPIED_PID" "$LOCK_HOLDER_PID" "$OWNED_START_PID"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  # Clean only this invocation's directory, including under an isolated TMPDIR.
  # A location-specific false condition must not turn passing assertions into
  # a failure exit from the EXIT trap.
  if [[ "$TMP_DIR" == "$TMP_ROOT"/synon-source-quickstart-test.* && -d "$TMP_DIR" && ! -L "$TMP_DIR" ]]; then
    rm -rf -- "$TMP_DIR"
  else
    echo 'refusing an unverified quickstart test cleanup path' >&2
    return 1
  fi
}
trap cleanup EXIT INT TERM

mkdir -p "$TMP_DIR/bin" "$TMP_DIR/state"
cat >"$TMP_DIR/bin/go" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == env && "${2:-}" == GOVERSION ]] && { printf '%s\n' "${FAKE_GO_VERSION:-go1.26.5}"; exit; }
echo "unexpected go invocation: $*" >&2; exit 2
EOF
cat >"$TMP_DIR/bin/node" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == --version ]] && { printf '%s\n' "${FAKE_NODE_VERSION:-v22.22.2}"; exit; }
[[ "${1:-}" == -e ]] && exec /usr/bin/node "$@"
echo "unexpected node invocation" >&2; exit 2
EOF
cat >"$TMP_DIR/bin/npm" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == --version ]] && { echo 10.9.7; exit; }
echo "unexpected npm invocation" >&2; exit 2
EOF
cat >"$TMP_DIR/bin/curl" <<'EOF'
#!/usr/bin/env bash
url="${*: -1}"
if [[ "$url" == */health || "$url" == */api/health ]]; then
  if [[ -n "${FAKE_HEALTH_BODY_FILE:-}" && -f "$FAKE_HEALTH_BODY_FILE" ]]; then
    cat "$FAKE_HEALTH_BODY_FILE"
  else
    printf '%s\n' '{"status":"healthy","service":"gateway"}'
  fi
else
  echo '<!doctype html><html><div id="root"></div></html>'
fi
EOF
cat >"$TMP_DIR/bin/xdg-open" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >"$FAKE_OPEN_RECORD"
EOF
cat >"$TMP_DIR/fake-backend" <<'EOF'
#!/usr/bin/env bash
echo $$ >"$FAKE_BACKEND_PID"
trap 'exit 0' INT TERM
while true; do sleep .05; done
EOF
cat >"$TMP_DIR/fake-frontend" <<'EOF'
#!/usr/bin/env bash
echo $$ >"$FAKE_FRONTEND_PID"
trap 'exit 0' INT TERM
[[ "${FAKE_FRONTEND_FAIL:-false}" == true ]] && exit 23
while true; do sleep .05; done
EOF
chmod 0755 "$TMP_DIR/bin/"* "$TMP_DIR/fake-"*

run_quickstart() {
  env PATH="$TMP_DIR/bin:/usr/bin:/bin" \
  FAKE_BACKEND_PID="$TMP_DIR/backend.pid" \
  FAKE_FRONTEND_PID="$TMP_DIR/frontend.pid" \
  FAKE_OPEN_RECORD="$TMP_DIR/open.txt" \
  FAKE_HEALTH_BODY_FILE="$TMP_DIR/health-body" \
  SYNON_QUICKSTART_BACKEND_ENTRY="$TMP_DIR/fake-backend" \
  SYNON_QUICKSTART_FRONTEND_ENTRY="$TMP_DIR/fake-frontend" \
  "$QUICKSTART" --state-dir "$TMP_DIR/state" --backend-port 39851 --web-port 39852 \
    --timeout-seconds 3 "$@"
}

run_quickstart_exec() {
  exec env PATH="$TMP_DIR/bin:/usr/bin:/bin" \
  FAKE_BACKEND_PID="$TMP_DIR/backend.pid" \
  FAKE_FRONTEND_PID="$TMP_DIR/frontend.pid" \
  FAKE_OPEN_RECORD="$TMP_DIR/open.txt" \
  FAKE_HEALTH_BODY_FILE="$TMP_DIR/health-body" \
  SYNON_QUICKSTART_BACKEND_ENTRY="$TMP_DIR/fake-backend" \
  SYNON_QUICKSTART_FRONTEND_ENTRY="$TMP_DIR/fake-frontend" \
  "$QUICKSTART" --state-dir "$TMP_DIR/state" --backend-port 39851 --web-port 39852 \
    --timeout-seconds 3 "$@"
}

wait_for_line() {
  local pattern="$1"
  local file="$2"
  for _ in $(seq 1 100); do
    grep -Fq "$pattern" "$file" 2>/dev/null && return
    kill -0 "$QUICKSTART_PID" 2>/dev/null || break
    sleep .03
  done
  echo "did not observe $pattern" >&2
  cat "$file" >&2 || true
  return 1
}

[[ -x "$QUICKSTART" ]] || { echo "quickstart entry is missing or not executable" >&2; exit 1; }

(run_quickstart_exec --open-browser) >"$TMP_DIR/ready.log" 2>&1 &
QUICKSTART_PID=$!
wait_for_line 'READY_URL=http://127.0.0.1:39852/#/login' "$TMP_DIR/ready.log"
for _ in $(seq 1 100); do
  [[ -f "$TMP_DIR/open.txt" ]] && break
  sleep .02
done
grep -Fqx 'http://127.0.0.1:39852/#/login' "$TMP_DIR/open.txt"
backend_pid=$(<"$TMP_DIR/backend.pid")
frontend_pid=$(<"$TMP_DIR/frontend.pid")
kill -0 "$backend_pid"
kill -0 "$frontend_pid"
kill -TERM "$QUICKSTART_PID"
set +e
wait "$QUICKSTART_PID"
shutdown_status=$?
set -e
if (( shutdown_status != 0 )); then
  echo "quickstart shutdown status=$shutdown_status, want 0" >&2
  cat "$TMP_DIR/ready.log" >&2
  exit 1
fi
QUICKSTART_PID=''
! kill -0 "$backend_pid" 2>/dev/null
! kill -0 "$frontend_pid" 2>/dev/null

rm -f "$TMP_DIR/backend.pid" "$TMP_DIR/frontend.pid"
set +e
FAKE_FRONTEND_FAIL=true run_quickstart >"$TMP_DIR/failure.log" 2>&1
failure_status=$?
set -e
(( failure_status != 0 ))
grep -Eq 'frontend exited (before|after) readiness' "$TMP_DIR/failure.log" || {
  echo 'frontend failure was not diagnosed' >&2
  cat "$TMP_DIR/failure.log" >&2
  exit 1
}
backend_pid=$(<"$TMP_DIR/backend.pid")
! kill -0 "$backend_pid" 2>/dev/null

for health_body in \
  '{"status":"healthy"' \
  '{"status":"healthy","service":"gateway"' \
  '[{"status":"healthy","service":"gateway"}]'; do
  rm -f "$TMP_DIR/backend.pid" "$TMP_DIR/frontend.pid"
  printf '%s\n' "$health_body" >"$TMP_DIR/health-body"
  set +e
  FAKE_HEALTH_BODY="$health_body" run_quickstart >"$TMP_DIR/invalid-health.log" 2>&1
  invalid_health_status=$?
  set -e
  (( invalid_health_status != 0 )) || {
    echo "invalid health body passed: $health_body" >&2
    cat "$TMP_DIR/curl.log" >&2 || true
    exit 1
  }
  grep -Fq 'services did not become healthy within 3s' "$TMP_DIR/invalid-health.log" || {
    echo "invalid health body was not rejected: $health_body" >&2
    cat "$TMP_DIR/invalid-health.log" >&2
    exit 1
  }
  backend_pid=$(<"$TMP_DIR/backend.pid")
  ! kill -0 "$backend_pid" 2>/dev/null
done

FAKE_NODE_VERSION=v21.9.0 run_quickstart >"$TMP_DIR/node-version.log" 2>&1 && {
  echo 'unsupported Node version passed' >&2; exit 1;
}
grep -Fq 'Node.js >=22.22.0 and <25 is required' "$TMP_DIR/node-version.log"

mkdir -p "$TMP_DIR/state-occupied/logs"
printf 'port-busy-backend-marker\n' >"$TMP_DIR/state-occupied/logs/backend.log"
printf 'port-busy-frontend-marker\n' >"$TMP_DIR/state-occupied/logs/frontend.log"
python3 -m http.server 39855 --bind 127.0.0.1 >"$TMP_DIR/occupied.log" 2>&1 &
OCCUPIED_PID=$!
for _ in $(seq 1 50); do
  timeout 1 bash -c 'exec 3<>/dev/tcp/127.0.0.1/39855' 2>/dev/null && break
  sleep .03
done
PATH="$TMP_DIR/bin:/usr/bin:/bin" "$QUICKSTART" --state-dir "$TMP_DIR/state-occupied" \
  --backend-port 39855 --web-port 39856 >"$TMP_DIR/occupied-result.log" 2>&1 && {
  echo 'occupied port passed' >&2; exit 1;
}
grep -Fq 'port is already in use: 127.0.0.1:39855' "$TMP_DIR/occupied-result.log"
grep -Fqx 'port-busy-backend-marker' "$TMP_DIR/state-occupied/logs/backend.log" || {
  echo 'occupied-port rejection truncated backend.log' >&2; exit 1;
}
grep -Fqx 'port-busy-frontend-marker' "$TMP_DIR/state-occupied/logs/frontend.log" || {
  echo 'occupied-port rejection truncated frontend.log' >&2; exit 1;
}

mkdir -p "$TMP_DIR/lock-state/logs"
printf 'lock-held-backend-marker\n' >"$TMP_DIR/lock-state/logs/backend.log"
printf 'lock-held-frontend-marker\n' >"$TMP_DIR/lock-state/logs/frontend.log"
LOCK_READY="$TMP_DIR/lock-state/lock-holder.ready"
(
  exec {held_lock_fd}>"$TMP_DIR/lock-state/quickstart.lock"
  flock -n "$held_lock_fd" || exit 1
  : >"$LOCK_READY"
  while true; do sleep .05; done
) &
LOCK_HOLDER_PID=$!
for _ in $(seq 1 100); do
  [[ -f "$LOCK_READY" ]] && break
  kill -0 "$LOCK_HOLDER_PID" 2>/dev/null || {
    echo 'lock holder exited before acquiring lock' >&2
    exit 1
  }
  sleep .03
done
[[ -f "$LOCK_READY" ]] || {
  echo 'lock holder never signaled readiness' >&2
  exit 1
}
PATH="$TMP_DIR/bin:/usr/bin:/bin" "$QUICKSTART" --state-dir "$TMP_DIR/lock-state" \
  --backend-port 39859 --web-port 39860 >"$TMP_DIR/lock-held-result.log" 2>&1 && {
  echo 'lock-held state directory passed' >&2; exit 1;
}
grep -Fq 'another quickstart owns this state directory' "$TMP_DIR/lock-held-result.log"
grep -Fqx 'lock-held-backend-marker' "$TMP_DIR/lock-state/logs/backend.log" || {
  echo 'lock contention truncated backend.log' >&2; exit 1;
}
grep -Fqx 'lock-held-frontend-marker' "$TMP_DIR/lock-state/logs/frontend.log" || {
  echo 'lock contention truncated frontend.log' >&2; exit 1;
}
kill -TERM "$LOCK_HOLDER_PID" 2>/dev/null || true
wait "$LOCK_HOLDER_PID" 2>/dev/null || true
LOCK_HOLDER_PID=''

printf '%s\n' '{"status":"healthy","service":"gateway"}' >"$TMP_DIR/health-body"
mkdir -p "$TMP_DIR/owned-start/logs"
printf 'stale-backend-line\n' >"$TMP_DIR/owned-start/logs/backend.log"
printf 'stale-frontend-line\n' >"$TMP_DIR/owned-start/logs/frontend.log"
env PATH="$TMP_DIR/bin:/usr/bin:/bin" \
  FAKE_BACKEND_PID="$TMP_DIR/owned-backend.pid" \
  FAKE_FRONTEND_PID="$TMP_DIR/owned-frontend.pid" \
  FAKE_OPEN_RECORD="$TMP_DIR/owned-open.txt" \
  FAKE_HEALTH_BODY_FILE="$TMP_DIR/health-body" \
  SYNON_QUICKSTART_BACKEND_ENTRY="$TMP_DIR/fake-backend" \
  SYNON_QUICKSTART_FRONTEND_ENTRY="$TMP_DIR/fake-frontend" \
  "$QUICKSTART" --state-dir "$TMP_DIR/owned-start" --backend-port 39861 --web-port 39862 \
    --timeout-seconds 3 >"$TMP_DIR/owned-start-run.log" 2>&1 &
OWNED_START_PID=$!
for _ in $(seq 1 200); do
  grep -Fq 'READY_URL=http://127.0.0.1:39862/#/login' "$TMP_DIR/owned-start-run.log" 2>/dev/null && break
  kill -0 "$OWNED_START_PID" 2>/dev/null || break
  sleep .05
done
grep -Fq 'READY_URL=http://127.0.0.1:39862/#/login' "$TMP_DIR/owned-start-run.log" || {
  echo 'owned start did not become ready' >&2
  cat "$TMP_DIR/owned-start-run.log" >&2
  exit 1
}
grep -Fq 'stale-backend-line' "$TMP_DIR/owned-start/logs/backend.log" && {
  echo 'owned start did not initialize backend.log' >&2; exit 1;
}
grep -Fq 'stale-frontend-line' "$TMP_DIR/owned-start/logs/frontend.log" && {
  echo 'owned start did not initialize frontend.log' >&2; exit 1;
}
kill -TERM "$OWNED_START_PID" 2>/dev/null || true
wait "$OWNED_START_PID" 2>/dev/null || true
OWNED_START_PID=''

PATH="$TMP_DIR/bin:/usr/bin:/bin" "$QUICKSTART" --state-dir "$SCRIPT_DIR/state" \
  --backend-port 39857 --web-port 39858 >"$TMP_DIR/source-state.log" 2>&1 && {
  echo 'source-contained state path passed' >&2; exit 1;
}
grep -Fq 'state directory must be a dedicated path outside the source tree' "$TMP_DIR/source-state.log"

ROOT_DIR=$(cd "$SCRIPT_DIR/../.." && pwd -P)
make -C "$ROOT_DIR" -n source-quickstart | grep -Fq 'bash scripts/dev/source-quickstart.sh'
for document in "$ROOT_DIR/README.md" "$ROOT_DIR/docs/operations-runbook.md"; do
  grep -Fq 'git clone https://github.com/Victor-Xu-1/X-Science.git' "$document"
  grep -Fq 'READY_URL=http://127.0.0.1:8765/#/login' "$document"
done

echo 'source quickstart tests passed'
