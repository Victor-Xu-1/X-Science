#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
ENV_FILE="$ROOT_DIR/.env.example"

if [[ ! -f "$ENV_FILE" || -L "$ENV_FILE" ]]; then
  echo "ERROR: .env.example is missing or is a symbolic link" >&2
  exit 1
fi

invalid_lines=$(grep -Ev '^[[:space:]]*$|^#[^[:cntrl:]]*$|^[A-Z][A-Z0-9_]*=[^[:cntrl:]]*$' "$ENV_FILE" || true)
if [[ -n "$invalid_lines" ]]; then
  echo "ERROR: .env.example contains unsupported shell syntax" >&2
  printf '%s\n' "$invalid_lines" >&2
  exit 1
fi

duplicates=$(sed -n 's/^\([A-Z][A-Z0-9_]*\)=.*/\1/p' "$ENV_FILE" | sort | uniq -d)
if [[ -n "$duplicates" ]]; then
  echo "ERROR: .env.example contains duplicate variables" >&2
  printf '%s\n' "$duplicates" >&2
  exit 1
fi

required_variables=(
  X_SCIENCE_ADDRESS X_SCIENCE_HOME X_SCIENCE_CONFIG X_SCIENCE_RUNNER_ENABLED
  X_SCIENCE_RUNNER_PROVIDER X_SCIENCE_RUNNER_CHAT_ENDPOINT X_SCIENCE_RUNNER_CHAT_API_KEY
  X_SCIENCE_LINK_AUTH_PASSWORD X_SCIENCE_AUTH_PUBLIC_BASE_URL
  X_SCIENCE_AUTH_GOOGLE_CLIENT_ID X_SCIENCE_AUTH_GOOGLE_CLIENT_SECRET
  X_SCIENCE_AUTH_WECHAT_APP_ID X_SCIENCE_AUTH_WECHAT_APP_SECRET
  X_SCIENCE_MCP_CONFIG X_SCIENCE_LSP_CONFIG
  X_SCIENCE_ENABLED_ADAPTERS
  FEISHU_APP_ID FEISHU_APP_SECRET
  WECHAT_ACCOUNT_ID WECHAT_BOT_TOKEN
)
for variable in "${required_variables[@]}"; do
  if ! grep -Eq "^${variable}=" "$ENV_FILE"; then
    echo "ERROR: .env.example is missing $variable" >&2
    exit 1
  fi
done

secret_variables=(
  X_SCIENCE_RUNNER_CHAT_API_KEY X_SCIENCE_COMPACT_SUMMARIZER_API_KEY
  X_SCIENCE_LINK_AUTH_PASSWORD X_SCIENCE_AUTH_GOOGLE_CLIENT_SECRET X_SCIENCE_AUTH_WECHAT_APP_SECRET
  FEISHU_APP_SECRET FEISHU_VERIFICATION_TOKEN
  FEISHU_ENCRYPT_KEY WECHAT_BOT_TOKEN
  FEISHU_TENANT_ACCESS_TOKEN
  X_SCIENCE_FEEDBACK_TOKEN
)
for variable in "${secret_variables[@]}"; do
  if ! grep -Eq "^${variable}=$" "$ENV_FILE"; then
    echo "ERROR: secret field $variable must be present and empty" >&2
    exit 1
  fi
done

if grep -Eiq '(/home/[^$[:space:]]+|[A-Z]:\\Users\\|victor|sk-[A-Za-z0-9_-]{8,}|bearer[[:space:]]+[A-Za-z0-9._-]+)' "$ENV_FILE"; then
  echo "ERROR: .env.example contains a personal path or credential-like value" >&2
  exit 1
fi

env -i PATH="$PATH" bash -c '
  set -a
  source "$1"
  set +a
  [[ "$X_SCIENCE_ADDRESS" == "127.0.0.1:8765" ]]
  [[ "$X_SCIENCE_RUNNER_ENABLED" == "true" ]]
  [[ "$X_SCIENCE_RUNNER_PROVIDER" == "workspace" ]]
  [[ -z "$X_SCIENCE_RUNNER_CHAT_API_KEY" ]]
  [[ -z "$WECHAT_BOT_TOKEN" ]]
' bash "$ENV_FILE"

echo "env-example-test: ok"
