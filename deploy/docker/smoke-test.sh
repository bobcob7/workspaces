#!/usr/bin/env bash
set -euo pipefail

image="${1:-ws-online}"
port="${2:-18080}"
run_id="$$"
name="ws-online-smoke-$run_id"
home_vol="ws-online-smoke-home-$run_id"
data_vol="ws-online-smoke-data-$run_id"
base="http://127.0.0.1:$port"
jar="$(mktemp)"

cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$home_vol" "$data_vol" >/dev/null 2>&1 || true
  rm -f "$jar"
}
trap cleanup EXIT

fail() {
  echo "smoke test failed: $1" >&2
  docker logs "$name" >&2 || true
  exit 1
}

# status_of METHOD URL [extra curl args...] prints the HTTP status code.
status_of() {
  local method="$1" url="$2"
  shift 2
  curl -s -o /dev/null -w '%{http_code}' -X "$method" "$url" "$@"
}

# start_container NAME runs the image on the smoke volumes. The :z flag is
# what lets a recreated container use them on SELinux hosts (docs/online.md).
start_container() {
  docker run -d --name "$1" -p "127.0.0.1:$port:8080" \
    -v "$home_vol:/home/ws:z" -v "$data_vol:/data:z" \
    -e WS_ONLINE_USERNAME=admin \
    -e WS_ONLINE_PASSWORD=smoke-secret \
    -e CLAUDE_CODE_OAUTH_TOKEN=smoke-token \
    "$image" >/dev/null
}

start_container "$name"

curl -fsS --retry 30 --retry-delay 1 --retry-all-errors "$base/healthz" | jq -e '.status == "ok"' >/dev/null \
  || fail "healthz did not report ok"

[[ "$(status_of POST "$base/api/login" -H 'Origin: http://127.0.0.1:'"$port" \
  -H 'Content-Type: application/json' -d '{"username":"admin","password":"wrong"}')" == "401" ]] \
  || fail "wrong password was not 401"

[[ "$(status_of POST "$base/api/login" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"smoke-secret"}')" == "403" ]] \
  || fail "login without Origin was not 403"

[[ "$(status_of POST "$base/api/login" -H 'Origin: http://127.0.0.1:'"$port" \
  -H 'Content-Type: application/json' -c "$jar" \
  -d '{"username":"admin","password":"smoke-secret"}')" == "204" ]] \
  || fail "login with the right password was not 204"
grep -q ws_online_session "$jar" || fail "cookie jar has no ws_online_session"

curl -fsS -b "$jar" "$base/api/session" | jq -e '.authenticated == true and .username == "admin"' >/dev/null \
  || fail "session is not authenticated as admin"

[[ "$(status_of POST "$base/api/invoke/open_defaults" -H 'Origin: http://127.0.0.1:'"$port")" == "401" ]] \
  || fail "invoke without cookie was not 401"

curl -fsS -b "$jar" -X POST -H 'Origin: http://127.0.0.1:'"$port" \
  -H 'Content-Type: application/json' -d '{}' "$base/api/invoke/versions" | jq -e '.app.version' >/dev/null \
  || fail "versions invoke failed"

curl -fsS "$base/" | grep -q '<div id="root">' || fail "index page has no root element"

docker exec "$name" claude --version >/dev/null || fail "claude is not runnable in the container"
docker exec "$name" gh --version >/dev/null || fail "gh is not runnable in the container"
docker exec "$name" ws --version >/dev/null || fail "ws is not runnable in the container"
docker exec "$name" jq -e '.hasCompletedOnboarding == true' /home/ws/.claude.json >/dev/null \
  || fail "onboarding is not marked done"
docker exec "$name" test -x /home/ws/.local/bin/claude || fail "claude is not installed in the home volume"
[[ "$(docker exec "$name" id -u)" == "1000" ]] || fail "container does not run as uid 1000"

# Recreate: remove the container and start a new one on the same volumes.
docker exec "$name" sh -c 'mkdir -p /home/ws/.claude && echo "{\"smoke\":true}" > /home/ws/.claude/settings.json && echo marker > /home/ws/.local/bin/marker' \
  || fail "could not write settings.json and marker"
docker rm -f "$name" >/dev/null
name="ws-online-smoke-$run_id-recreated"
start_container "$name"

curl -fsS --retry 30 --retry-delay 1 --retry-all-errors "$base/healthz" | jq -e '.status == "ok"' >/dev/null \
  || fail "recreated container did not report ok"
docker exec "$name" test -x /home/ws/.local/bin/claude || fail "claude is not executable after recreate"
docker exec "$name" test -f /home/ws/.local/bin/marker || fail "marker did not survive recreate"
docker exec "$name" jq -e '.smoke == true' /home/ws/.claude/settings.json >/dev/null \
  || fail "settings.json did not survive recreate"

if no_token_log="$(docker run --rm -e WS_ONLINE_USERNAME=admin -e WS_ONLINE_PASSWORD=smoke-secret "$image" 2>&1)"; then
  fail "container without CLAUDE_CODE_OAUTH_TOKEN started"
fi
grep -q 'claude setup-token' <<<"$no_token_log" || fail "missing-token run did not mention claude setup-token"

echo "smoke test passed"
