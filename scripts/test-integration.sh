#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE=(docker compose -f "$ROOT_DIR/compose.test.yaml" -p roomusic-test)
PG_PORT="${ROOMUSIC_TEST_PG_PORT:-55432}"
PG_DATABASE="${ROOMUSIC_TEST_PG_DATABASE:-roomusic_test}"
PG_USER="${ROOMUSIC_TEST_PG_USER:-roomusic_test}"
PG_PASSWORD="${ROOMUSIC_TEST_PG_PASSWORD:-roomusic_test}"
export ROOMUSIC_TEST_DATABASE_URL="${ROOMUSIC_TEST_DATABASE_URL:-postgres://${PG_USER}:${PG_PASSWORD}@127.0.0.1:${PG_PORT}/${PG_DATABASE}?sslmode=disable}"

cleanup() {
  if [[ "${ROOMUSIC_TEST_KEEP_DB:-false}" != "true" ]]; then
    "${COMPOSE[@]}" down -v --remove-orphans >/dev/null
  else
    echo "保留 PostgreSQL 测试容器（ROOMUSIC_TEST_KEEP_DB=true）" >&2
  fi
}
trap cleanup EXIT INT TERM

"${COMPOSE[@]}" up -d --wait postgres
echo "运行 PostgreSQL 集成测试：${ROOMUSIC_TEST_DATABASE_URL}" >&2
(cd "$ROOT_DIR/backend" && go test ./cmd/roomusic -run 'TestPostgreSQL' -count=1)
