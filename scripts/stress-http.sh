#!/usr/bin/env bash
set -euo pipefail

export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="$NO_PROXY"

binary="${1:-dist/x-science}"
requests="${X_SCIENCE_STRESS_REQUESTS:-1000}"
concurrency="${X_SCIENCE_STRESS_CONCURRENCY:-64}"
timeout="${X_SCIENCE_STRESS_TIMEOUT:-5s}"

port="$(
	python3 - <<'PY'
import socket

sock = socket.socket()
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()
PY
)"

tmp="$(mktemp -d)"
cfg="$tmp/x-science.json"
home="$tmp/home"
log="$tmp/server.log"

cleanup() {
	if [[ -n "${pid:-}" ]]; then
		kill "$pid" >/dev/null 2>&1 || true
		wait "$pid" >/dev/null 2>&1 || true
	fi
	rm -rf "$tmp"
}
trap cleanup EXIT

mkdir -p "$home"
cat >"$cfg" <<JSON
{
  "home_dir": "$home",
  "host": "127.0.0.1",
  "port": $port,
  "enabled_adapters": []
}
JSON

X_SCIENCE_CONFIG="$cfg" X_SCIENCE_HOME="$home" "$binary" >"$log" 2>&1 &
pid="$!"

for _ in $(seq 1 80); do
	if curl -fsS "http://127.0.0.1:$port/health" >/dev/null 2>&1; then
		break
	fi
	if ! kill -0 "$pid" >/dev/null 2>&1; then
		cat "$log" >&2 || true
		exit 1
	fi
	sleep 0.1
done

curl -fsS "http://127.0.0.1:$port/health" | grep -q '"status":"healthy"'
"$binary" stress-http \
	--base-url "http://127.0.0.1:$port" \
	--requests "$requests" \
	--concurrency "$concurrency" \
	--timeout "$timeout"
printf 'STRESS_HTTP_TEST=yes\n'
