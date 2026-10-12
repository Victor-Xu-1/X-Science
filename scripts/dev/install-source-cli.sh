#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_PATH=$(realpath -e "${BASH_SOURCE[0]}") || {
  echo "[X-Science install] unable to resolve the installer path" >&2
  exit 1
}
SOURCE_ROOT=$(cd "$(dirname "$SCRIPT_PATH")/../.." && pwd -P)
WRAPPER="$SOURCE_ROOT/scripts/dev/x-science"
HOME_DIR=$(realpath -e "$HOME") || {
  echo "[X-Science install] unable to resolve the user home directory" >&2
  exit 1
}
BIN_DIR="$HOME_DIR/.local/bin"
TARGET="$BIN_DIR/x-science"

fail() {
  echo "[X-Science install] $*" >&2
  exit 1
}

command -v realpath >/dev/null 2>&1 || fail "required command not found: realpath"
command -v ln >/dev/null 2>&1 || fail "required command not found: ln"
[[ -x "$WRAPPER" ]] || fail "source launcher is missing or not executable: $WRAPPER"
[[ "$BIN_DIR" != / && "$BIN_DIR" != "" ]] || fail "refusing an unsafe empty or root bin directory"

if [[ -e "$HOME_DIR/.local" || -L "$HOME_DIR/.local" ]]; then
  [[ -d "$HOME_DIR/.local" && ! -L "$HOME_DIR/.local" ]] || fail "refusing a non-directory user .local path"
else
  mkdir "$HOME_DIR/.local"
fi
if [[ -e "$BIN_DIR" || -L "$BIN_DIR" ]]; then
  [[ -d "$BIN_DIR" && ! -L "$BIN_DIR" ]] || fail "refusing a non-directory user bin path"
else
  mkdir "$BIN_DIR"
fi
BIN_DIR=$(realpath -e "$BIN_DIR") || fail "unable to resolve user bin directory: $BIN_DIR"
TARGET="$BIN_DIR/x-science"

# One canonical command. Never create a competing compatibility launcher.
TARGETS=("$TARGET")
for target in "${TARGETS[@]}"; do
  if [[ -e "$target" || -L "$target" ]]; then
    [[ -L "$target" && "$(realpath -e "$target" 2>/dev/null || true)" == "$WRAPPER" ]] ||
      fail "refusing to replace a command not owned by this X-Science checkout: $target"
  fi
done
for target in "${TARGETS[@]}"; do
  if [[ -L "$target" ]]; then
    echo "[X-Science install] already installed: $target"
  else
    ln -s "$WRAPPER" "$target"
    echo "[X-Science install] installed: $target -> $WRAPPER"
  fi
  [[ "$(realpath -e "$target")" == "$WRAPPER" ]] || fail "installed launcher target verification failed"
done
case ":${PATH:-}:" in
  *":$BIN_DIR:"*) echo "[X-Science install] PATH already contains $BIN_DIR" ;;
  *)
    echo "[X-Science install] add this once to the current shell:"
    echo "export PATH=\"$BIN_DIR:\$PATH\""
    ;;
esac
echo "[X-Science install] next command: x-science start"
