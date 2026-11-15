#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOMUSIC_ENV_FILE:-$ROOT_DIR/.env.dev}"
[[ -f "$ENV_FILE" ]] || { echo "缺少开发配置：$ENV_FILE" >&2; exit 1; }
set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a
export ROOMUSIC_ENV="${ROOMUSIC_ENV:-dev}"
export ROOMUSIC_HTTP_ADDR="${ROOMUSIC_HTTP_ADDR:-:8080}"

ROOMUSIC_PG_BIN_DIR="${ROOMUSIC_PG_BIN_DIR:-$("$ROOT_DIR/scripts/pg-client-pinned.sh")}"
export ROOMUSIC_PG_BIN_DIR

docker compose -f "$ROOT_DIR/compose.dev.yaml" up -d --wait postgres

backend_pid=""
frontend_pid=""
backend_started_at=0
backend_restart_delay=0
backend_restart_at=0
cleanup() {
  trap - INT TERM EXIT
  [[ -n "$backend_pid" ]] && kill "$backend_pid" 2>/dev/null || true
  [[ -n "$frontend_pid" ]] && kill "$frontend_pid" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup INT TERM EXIT

start_backend() {
  (cd "$ROOT_DIR/backend" && exec go run ./cmd/roomusic) &
  backend_pid=$!
  backend_started_at=$SECONDS
  backend_restart_at=0
}

start_backend
(cd "$ROOT_DIR/frontend" && exec npm run dev -- --host 0.0.0.0) &
frontend_pid=$!

last_fingerprint=""
while :; do
  sleep 1
  fingerprint="$(find "$ROOT_DIR/backend" -type f \( -name '*.go' -o -name '*.sql' \) -printf '%T@ %p\n' 2>/dev/null | sort | sha256sum)"
  if [[ -n "$last_fingerprint" && "$fingerprint" != "$last_fingerprint" ]]; then
    kill "$backend_pid" 2>/dev/null || true
    wait "$backend_pid" 2>/dev/null || true
    backend_restart_delay=0
    start_backend
  elif ! kill -0 "$backend_pid" 2>/dev/null; then
    if (( backend_restart_at == 0 )); then
      wait "$backend_pid" 2>/dev/null || true
      if (( SECONDS - backend_started_at < 10 )); then
        if (( backend_restart_delay == 0 )); then
          backend_restart_delay=1
        else
          backend_restart_delay=$((backend_restart_delay * 2))
          (( backend_restart_delay <= 30 )) || backend_restart_delay=30
        fi
        printf '后端启动后很快退出，%s 秒后重启\n' "$backend_restart_delay" >&2
      else
        backend_restart_delay=0
      fi
      backend_restart_at=$((SECONDS + backend_restart_delay))
    fi
    if (( SECONDS >= backend_restart_at )); then
      start_backend
    fi
  elif (( SECONDS - backend_started_at >= 10 )); then
    backend_restart_delay=0
  fi
  last_fingerprint="$fingerprint"
done
