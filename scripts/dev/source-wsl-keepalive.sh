#!/usr/bin/env bash
set -Eeuo pipefail

source_dev_keepalive_bootstrap() {
  local -r x_science_user="${X_SCIENCE_DEV_WSL_USER:-victor_1}"
  local x_science_uid
  x_science_uid="$(id -u "$x_science_user")"
  local -r runtime_dir="/run/user/$x_science_uid"
  local -ar units=(
    x-science-backend.service
    x-science-frontend.service
  )

  loginctl enable-linger "$x_science_user" >/dev/null
  systemctl start "user@${x_science_uid}.service"

  local ready=false bootstrap_try
  for bootstrap_try in $(seq 1 30); do
    if [[ -S "$runtime_dir/bus" ]] &&
      runuser -u "$x_science_user" -- env XDG_RUNTIME_DIR="$runtime_dir" \
        systemctl --user start "${units[@]}"; then
      ready=true
      break
    fi
    sleep 1
  done

  if [[ "$ready" != true ]] ||
    ! runuser -u "$x_science_user" -- env XDG_RUNTIME_DIR="$runtime_dir" \
      systemctl --user is-active --quiet "${units[@]}"; then
    echo "X-Science source services did not become active" >&2
    return 1
  fi
}

source_dev_keepalive_main() {
  local -r expected_start=$'XSCIENCE_KEEPALIVE_V1_START\n'
  local start_byte start_index
  # A normal line read discards NUL. Read this bounded ASCII marker bytewise so
  # CRLF, embedded NUL, an incomplete line and any other spelling fail closed.
  for ((start_index = 0; start_index < ${#expected_start}; start_index++)); do
    if ! LC_ALL=C IFS= read -r -d '' -n 1 start_byte ||
      [[ "$start_byte" != "${expected_start:start_index:1}" ]]; then
      echo "X-Science keepalive requires its owned stdin start line" >&2
      return 64
    fi
  done

  # Serialize the trusted initialization boundary, not another public CLI or
  # environment authority. No bootstrap command can consume the owner's stdin.
  local bootstrap_program bootstrap_status=0
  bootstrap_program="$(declare -f source_dev_keepalive_bootstrap)"
  bootstrap_program+=$'\nsource_dev_keepalive_bootstrap'
  # GNU timeout's default process-group mode bounds initialization and its
  # children. Do not use --foreground, which would leave child-group gaps.
  timeout --signal=TERM --kill-after=5s 30s \
    bash -Eeuo pipefail -c "$bootstrap_program" </dev/null || bootstrap_status=$?
  if (( bootstrap_status != 0 )); then
    echo "X-Science keepalive bootstrap did not complete" >&2
    return "$bootstrap_status"
  fi

  # The exclusive writer owns lifetime; holding itself has no wall-clock limit.
  exec cat >/dev/null
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  source_dev_keepalive_main
fi
