#!/usr/bin/env bash
set -Eeuo pipefail

readonly x_science_user="${X_SCIENCE_DEV_WSL_USER:-victor_1}"
readonly x_science_uid="$(id -u "$x_science_user")"
readonly runtime_dir="/run/user/$x_science_uid"
readonly units=(
  x-science-v010-backend.service
  x-science-v010-frontend.service
)

loginctl enable-linger "$x_science_user" >/dev/null
systemctl start "user@${x_science_uid}.service"

ready=false
for _ in $(seq 1 30); do
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
  exit 1
fi

exec /bin/sleep infinity
