#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
requested_environment="${ROOMUSIC_ENV:-}"
requested_database_url="${ROOMUSIC_DATABASE_URL:-}"
if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$ROOT_DIR/.env"
  set +a
fi
[[ -n "$requested_environment" ]] && ROOMUSIC_ENV="$requested_environment"
[[ -n "$requested_database_url" ]] && ROOMUSIC_DATABASE_URL="$requested_database_url"

environment="${ROOMUSIC_ENV:-development}"
if [[ "$environment" == "production" ]]; then
  echo "拒绝执行：ROOMUSIC_ENV=production 禁止数据库重置。" >&2
  exit 1
fi

database_url="${ROOMUSIC_DATABASE_URL:-}"
pg_database="${PG_DATABASE:-roomusic}"
pg_user="${PG_USER:-roomusic}"
pg_port="${PG_PORT:-5432}"
if [[ -z "$database_url" ]]; then
  database_url="postgres://${pg_user}:${PG_PASSWORD:-}@127.0.0.1:${pg_port}/${pg_database}?sslmode=disable"
fi

if [[ "${CONFIRM:-}" != "1" ]]; then
  echo "警告：这将删除本地数据库中的用户、会话、目录、扫描和操作日志。" >&2
  read -r -p '请输入 RESET 继续：' confirmation
  [[ "$confirmation" == "RESET" ]] || { echo "已取消。"; exit 1; }
fi

if command -v psql >/dev/null 2>&1; then
  psql_command=(psql "$database_url")
elif docker compose ps postgres --status running --format '{{.Service}}' 2>/dev/null | grep -qx postgres; then
  psql_command=(docker compose exec -T postgres psql -U "$pg_user" -d "$pg_database")
else
  echo "需要 psql 命令，或运行中的 postgres Compose 服务。" >&2
  exit 1
fi
"${psql_command[@]}" -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
TRUNCATE TABLE
  sessions,
  track_observations,
  scan_diagnostics,
  tracks,
  media,
  release_artworks,
  releases,
  release_groups,
  scan_runs,
  library_root_operations,
  library_roots,
  users,
  setup_state
  RESTART IDENTITY CASCADE;
COMMIT;
SQL
echo "数据库已重置为未初始化状态。"
