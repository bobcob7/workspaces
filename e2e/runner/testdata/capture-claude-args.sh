#!/usr/bin/env bash
# Regenerates e2e/runner/testdata/claude-args.golden: the arguments ws passes
# to `claude` on the default (Claude Code) runner, captured with a fake
# `claude` and a config dir with no [runner], [endpoints] or [models].
# Run it from the repo root after `make build`:
#
#   e2e/runner/testdata/capture-claude-args.sh [OUTPUT]   # default OUTPUT is
#                                                         # the golden next to this script
#
# It never touches the real config: everything lives in a temp dir that is
# removed on exit. Run it twice and `cmp` the results to check it is stable.
# The golden must be captured from a build before any model-runner change
# lands (ADR 0071). T22 of docs/design/ollama-agent/tasks.md replaces this
# script's use with a Go test (the claude-default-untouched scenario).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../.." && pwd)
ws="$root/bin/ws"
out=${1:-$here/claude-args.golden}
[ -x "$ws" ] || { echo "build bin/ws first: make build" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tmp=$(cd "$tmp" && pwd -P)
mkdir -p "$tmp/bin" "$tmp/home" "$tmp/run" "$tmp/cfg" "$tmp/claude-config" "$tmp/up/o" "$tmp/seed"
chmod 700 "$tmp/run"
raw="$tmp/raw"
: > "$raw"

# A fake claude: answers --version, records every other call's arguments as
# one JSON array per line, and prints what `claude --bg` prints.
cat > "$tmp/bin/claude" <<FAKE
#!/bin/sh
if [ "\$1" = --version ]; then echo "2.1.303 (Claude Code)"; exit 0; fi
jq -nc '\$ARGS.positional' --args -- "\$@" >> "$raw"
if [ "\$1" = --bg ]; then
  name=
  [ "\$2" = -n ] && name=\$3
  n=\$(cat "$tmp/n" 2>/dev/null || echo 0); n=\$((n + 1)); echo \$n > "$tmp/n"
  echo "backgrounded · 0a1b2c3d\$n · \$name"
fi
exit 0
FAKE
chmod +x "$tmp/bin/claude"

# A bare upstream o/n served for https://example.test/ by an insteadOf rule.
cat > "$tmp/gitconfig" <<GC
[url "$tmp/up/"]
	insteadOf = https://example.test/
[user]
	name = t
	email = t@example.test
[init]
	defaultBranch = main
GC
export GIT_CONFIG_GLOBAL="$tmp/gitconfig" GIT_CONFIG_NOSYSTEM=1
git init -q --bare "$tmp/up/o/n.git"
git init -q "$tmp/seed"
git -C "$tmp/seed" commit -q --allow-empty -m init
git -C "$tmp/seed" push -q "$tmp/up/o/n.git" HEAD:refs/heads/main

mkdir -p "$tmp/cfg"
cat > "$tmp/cfg/config.toml" <<CFG
[defaults]
owners = ["o"]
host = "example.test"
CFG
printf '{"projects":{"%s":{"hasTrustDialogAccepted":true}}}' "$tmp/cfg/workspaces" > "$tmp/claude-config/.claude.json"

# wsrun runs ws in $cwd with a clean environment.
wsrun() {
  (cd "$cwd" && env -i PATH="$tmp/bin:$(dirname "$(command -v git)"):/usr/bin:/bin" HOME="$tmp/home" \
    XDG_RUNTIME_DIR="$tmp/run" WS_CONFIG_DIR="$tmp/cfg" WS_DAEMON_AUTOSTART=0 \
    CLAUDE_CONFIG_DIR="$tmp/claude-config" GIT_CONFIG_GLOBAL="$tmp/gitconfig" GIT_CONFIG_NOSYSTEM=1 \
    "$ws" "$@" </dev/null)
}

# case_ LABEL ARGS...: runs ws and records the claude calls it made.
case_() {
  local label=$1; shift
  echo "## $label" >> "$raw"
  wsrun "$@" >> "$tmp/stdout" 2>> "$tmp/stderr" || { echo "ws $* failed:" >&2; tail -5 "$tmp/stderr" >&2; exit 1; }
}

# ws machine acts for the workspace it runs in, so the first case makes one.
cwd=$tmp/home
case_ 'ws claude -- x' claude --json --name parent -- "x"
cwd=$(jq -rs 'map(select(.path)) | .[0].path' "$tmp/stdout")
case_ 'ws machine spawn -- x' machine spawn --json -- "x"
case_ 'ws machine spawn o/n --name fix -- x' machine spawn --json --name fix o/n -- "x"
case_ 'ws machine spawn --role verifier' machine spawn --json --role verifier --var BRANCH=feat/x --var CHECKS="make test" --
cwd=$tmp/home
case_ 'ws claude o/n -- --model opus x' claude --json o/n -- --model opus "x"
case_ 'ws loop add claude-loop' loop add --json --every 15m --prompt "do the loop" --model sonnet --permission-mode plan claude-loop
case_ 'ws loop run claude-loop' loop run --json --force claude-loop

[ ! -e "$tmp/run/ws" ] && ! find "$tmp/run" -name '*.sock' | grep -q . || { echo "a daemon socket exists" >&2; exit 1; }

# Normalise: workspace ids, temp dir, timestamps.
ids=$(grep -ho '"id":"[^"]*"' "$tmp/stdout" | sed 's/"id":"\(.*\)"/\1/' | sort -u)
cp "$raw" "$tmp/norm"
sed -i "s#$tmp#<DIR>#g" "$tmp/norm"
for id in $ids; do sed -i "s#$id#<WS_ID>#g" "$tmp/norm"; done
sed -i -E 's/loop-[a-z0-9-]+-[0-9a-f]{8}/<WS_ID>/g' "$tmp/norm"
sed -i -E 's/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z?/<TIME>/g; s/\b0a1b2c3d[0-9]+\b/<SESSION>/g' "$tmp/norm"
mkdir -p "$(dirname "$out")"
cp "$tmp/norm" "$out"
