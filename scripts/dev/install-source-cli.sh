#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_PATH=$(realpath -e "${BASH_SOURCE[0]}") || {
  echo "[synon-install] unable to resolve the installer path" >&2
  exit 1
}
SOURCE_ROOT=$(cd "$(dirname "$SCRIPT_PATH")/../.." && pwd -P)
WRAPPER="$SOURCE_ROOT/scripts/dev/synon"
HOME_DIR=$(realpath -e "$HOME") || {
  echo "[synon-install] unable to resolve the user home directory" >&2
  exit 1
}
BIN_DIR="$HOME_DIR/.local/bin"
TARGET="$BIN_DIR/synon"

fail() {
  echo "[synon-install] $*" >&2
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
TARGET="$BIN_DIR/synon"

# Preflight both names before writing either link. The legacy launcher shares
# the same implementation and state; it is not a second runtime owner.
TARGETS=("$BIN_DIR/x-science" "$TARGET")
for target in "${TARGETS[@]}"; do
  if [[ -e "$target" || -L "$target" ]]; then
    [[ -L "$target" && "$(realpath -e "$target" 2>/dev/null || true)" == "$WRAPPER" ]] ||
      fail "refusing to replace an existing non-Synon command: $target"
  fi
done
for target in "${TARGETS[@]}"; do
  if [[ -L "$target" ]]; then
    echo "[synon-install] already installed: $target"
  else
    ln -s "$WRAPPER" "$target"
    echo "[synon-install] installed: $target -> $WRAPPER"
  fi
  [[ "$(realpath -e "$target")" == "$WRAPPER" ]] || fail "installed launcher target verification failed"
done
case ":${PATH:-}:" in
  *":$BIN_DIR:"*) echo "[synon-install] PATH already contains $BIN_DIR" ;;
  *)
    echo "[synon-install] add this once to the current shell:"
    echo "export PATH=\"$BIN_DIR:\$PATH\""
    ;;
esac
echo "[synon-install] next command: x-science start"
