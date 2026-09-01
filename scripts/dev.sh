#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOMUSIC_ENV_FILE:-$ROOT_DIR/.env.dev}"
[[ -f "$ENV_FILE" ]] || { echo "缺少开发配置：$ENV_FILE" >&2; exit 1; }
set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a
requested_environment="${ROOMUSIC_ENV:-}"
requested_database_url="${ROOMUSIC_DATABASE_URL:-}"
if [[ -f "$ROOT_DIR/.env" && "${ROOMUSIC_ENV_FILE:-}" == "$ROOT_DIR/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$ROOT_DIR/.env"
  set +a
fi
[[ -n "$requested_environment" ]] && ROOMUSIC_ENV="$requested_environment"
[[ -n "$requested_database_url" ]] && ROOMUSIC_DATABASE_URL="$requested_database_url"
export ROOMUSIC_ENV="${ROOMUSIC_ENV:-dev}"
export ROOMUSIC_HTTP_ADDR="${ROOMUSIC_HTTP_ADDR:-:8080}"

docker compose up -d --wait postgres

backend_pid=""
frontend_pid=""
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
}

start_backend
(cd "$ROOT_DIR/frontend" && exec npm run dev -- --host 0.0.0.0) &
frontend_pid=$!

last_fingerprint=""
while :; do
  sleep 1
  if ! kill -0 "$backend_pid" 2>/dev/null; then
    start_backend
    last_fingerprint=""
    continue
  fi
  fingerprint="$(find "$ROOT_DIR/backend" -type f \( -name '*.go' -o -name '*.sql' \) -printf '%T@ %p\n' 2>/dev/null | sort | sha256sum)"
  if [[ -n "$last_fingerprint" && "$fingerprint" != "$last_fingerprint" ]]; then
    kill "$backend_pid" 2>/dev/null || true
    wait "$backend_pid" 2>/dev/null || true
    start_backend
  fi
  last_fingerprint="$fingerprint"
done
