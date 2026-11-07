#!/usr/bin/env bash
set -euo pipefail

die() {
  echo "ws-online-entrypoint: $1" >&2
  exit 64
}

check_env() {
  [[ -n "${WS_ONLINE_USERNAME:-}" ]] || die "WS_ONLINE_USERNAME is required"
  if [[ -n "${WS_ONLINE_PASSWORD:-}" && -n "${WS_ONLINE_PASSWORD_HASH:-}" ]]; then
    die "set only one of WS_ONLINE_PASSWORD and WS_ONLINE_PASSWORD_HASH"
  fi
  if [[ -z "${WS_ONLINE_PASSWORD:-}" && -z "${WS_ONLINE_PASSWORD_HASH:-}" ]]; then
    die "set one of WS_ONLINE_PASSWORD and WS_ONLINE_PASSWORD_HASH"
  fi
  [[ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]] || die "CLAUDE_CODE_OAUTH_TOKEN is required; run 'claude setup-token' and set the token"
}

seed_home() {
  mkdir -p "$HOME/.local/bin" "$HOME/.claude" "$WS_CONFIG_DIR" "$WS_ONLINE_DATA_DIR"
  if [[ ! -x "$HOME/.local/bin/claude" ]]; then
    cp -a /opt/ws-online/seed/home/. "$HOME/"
  fi
}

mark_onboarded() {
  local file="$HOME/.claude.json" tmp
  [[ -s "$file" ]] || echo '{}' > "$file"
  tmp="$(mktemp "$file.XXXXXX")"
  jq --arg dir "$WS_CONFIG_DIR/workspaces" \
    '.hasCompletedOnboarding = true | .projects[$dir].hasTrustDialogAccepted = true' \
    "$file" > "$tmp"
  mv "$tmp" "$file"
  chmod 600 "$file"
}

setup_git() {
  if [[ -n "${WS_GIT_NAME:-}" ]]; then
    git config --global user.name "$WS_GIT_NAME"
  fi
  if [[ -n "${WS_GIT_EMAIL:-}" ]]; then
    git config --global user.email "$WS_GIT_EMAIL"
  fi
  if [[ -n "${GH_TOKEN:-}" ]]; then
    gh auth setup-git || echo "ws-online-entrypoint: warning: gh auth setup-git failed" >&2
  fi
}

if [[ $# -gt 0 && "$1" != "serve" ]]; then
  exec "$@"
fi

check_env
seed_home
mark_onboarded
setup_git
exec ws-online serve
