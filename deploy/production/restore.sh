#!/bin/sh
# 宿主机负责确认与停启；_inside 只在无网络的临时数据库容器中运行。
set -eu
umask 077

counts_sql() {
  cat <<'SQL'
SELECT CASE WHEN to_regclass('public.schema_migrations') IS NULL
  THEN 'SELECT ''schema_migrations.max|缺失''; SELECT ''schema_migrations.count|缺失'';'
  ELSE 'SELECT ''schema_migrations.max|'' || coalesce(max(version)::text, ''0'') FROM public.schema_migrations; SELECT ''schema_migrations.count|'' || count(*) FROM public.schema_migrations;'
END \gexec
SELECT CASE WHEN to_regclass('public.' || name) IS NULL
  THEN format('SELECT %L;', name || '|缺失')
  ELSE format('SELECT %L || count(*) FROM public.%I;', name || '|', name)
END FROM unnest(ARRAY['users','sessions','library_roots','releases','tracks','release_artworks']) AS name
\gexec
SQL
}

inside() {
  export PGHOST=/tmp PGUSER=postgres PGDATABASE=roomusic
  data=$(mktemp -d /tmp/roomusic-restore.XXXXXX)
  trap 'pg_ctl -D "$data" -m immediate -w stop >/dev/null 2>&1 || true; rm -rf -- "$data"' EXIT
  trap 'exit 1' HUP INT TERM
  initdb -D "$data" --auth-local=trust --auth-host=reject >/dev/null
  pg_ctl -D "$data" -l "$data/server.log" -o "-c listen_addresses='' -c unix_socket_directories='/tmp'" -w start >/dev/null
  createdb roomusic
  if ! pg_restore -d roomusic --no-owner --exit-on-error /backup.dump >/dev/null 2>&1; then
    echo '备份恢复失败；临时库将被删除，生产库未改动。' >&2
    exit 1
  fi
  counts_sql | psql -X -qAt -v ON_ERROR_STOP=1
  pg_ctl -D "$data" -m fast -w stop >/dev/null
}

if [ "${1:-}" = _inside ]; then inside; exit 0; fi
case "${1:-}" in
  --help|-h|help|'') echo '用法：deploy/production/restore.sh check|production <备份文件>'; exit 0 ;;
  check|production) [ "$#" -eq 2 ] || { echo '需要指定一个备份文件' >&2; exit 2; } ;;
  *) echo '用法：deploy/production/restore.sh check|production <备份文件>' >&2; exit 2 ;;
esac
action=$1
[ -f "$2" ] && [ -r "$2" ] || { echo '备份文件不存在或不可读' >&2; exit 1; }
backup_file=$(realpath -- "$2")
root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
project=${COMPOSE_PROJECT_NAME:-roomusic-prod}
env_file=${COMPOSE_ENV_FILES:-$root/.env.prod}
# 只接受设计中的生产/演练项目，绝不对开发项目运行恢复。
case "$project" in roomusic-prod|roomusic-drill) ;; *) echo '只允许恢复 roomusic-prod 或 roomusic-drill 项目' >&2; exit 2 ;; esac
[ -f "$env_file" ] || { echo '缺少恢复所用的配置文件' >&2; exit 1; }
env_file=$(realpath -- "$env_file")
# 只读取变量名，清掉继承的开发配置；恢复接口的两个变量随后显式设置。
for name in $(env | sed -n 's/^\([A-Za-z_][A-Za-z_0-9]*\)=.*/\1/p'); do
  case "$name" in ROOMUSIC_*|COMPOSE_*) unset "$name" ;; esac
done
export COMPOSE_PROJECT_NAME="$project" COMPOSE_ENV_FILES="$env_file"
cd "$root"
compose() { docker compose -f deploy/production/compose.yaml "$@"; }
postgres_running() { [ -n "$(compose ps --status running -q postgres)" ]; }
work=$(mktemp -d "${TMPDIR:-/tmp}/roomusic-restore.XXXXXX")
trap 'rm -rf -- "$work"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [ "$action" = check ]; then
  # 备份是 uid 1000 的 0600 文件，临时容器为 postgres 用户；只放宽副本，宿主机父目录仍为 0700。
  cp -- "$backup_file" "$work/backup.dump"
  chmod 644 "$work/backup.dump"
  compose --profile tools run --rm --no-deps -T -v "$work/backup.dump:/backup.dump:ro" restore-check > "$work/backup-counts"
  if postgres_running; then
    counts_sql | compose exec -T postgres psql -X -qAt -U roomusic -d roomusic -v ON_ERROR_STOP=1 > "$work/live-counts"
    printf '项目\t备份\t当前数据库\n'
    awk -F '|' 'NR==FNR { live[$1]=$2; next } { printf "%s\t%s\t%s\n", $1, $2, live[$1] }' "$work/live-counts" "$work/backup-counts"
  else
    echo '生产没有在运行，只显示备份里的数'
    printf '项目\t备份\n'
    tr '|' '\t' < "$work/backup-counts"
  fi
  exit 0
fi

postgres_running || { echo '数据库没有在运行，先 make prod（演练请先启动演练数据库）' >&2; exit 1; }
printf '备份文件：%s\n' "$backup_file"
stat -c '修改时间：%y；大小：%s 字节' "$backup_file"
printf '将覆盖 %s 的数据库。请输入项目名确认：' "$project"
read -r confirmation
[ "$confirmation" = "$project" ] || { echo '已取消。'; exit 1; }

# 使用一次性备份进程，即使 app 或 backup 已停止也能保护现状。
if ! compose run --rm --no-deps -T backup now > "$work/pre-restore"; then
  cat "$work/pre-restore"
  echo '恢复前备份失败，未停止服务或覆盖数据库。' >&2
  exit 1
fi
cat "$work/pre-restore"
safety_name=$(sed -n 's/.*"event":"backup.completed","file":"\(roomusic-[0-9-]*-manual.dump\)".*/\1/p' "$work/pre-restore")
[ -n "$safety_name" ] || { echo '未取得恢复前备份文件名，停止恢复。' >&2; exit 1; }
backup_dir=$(sed -n 's/^ROOMUSIC_BACKUP_DIR=//p' "$env_file" | tail -n 1 | tr -d '\r')
case "$backup_dir" in \"*\"|\'*\') backup_dir=$(printf '%s' "$backup_dir" | cut -c 2- | sed 's/.$//') ;; esac
case "$backup_dir" in /*) ;; *) echo 'ROOMUSIC_BACKUP_DIR 必须是绝对路径，停止恢复。' >&2; exit 1 ;; esac
safety_file="$backup_dir/$safety_name"
[ -f "$safety_file" ] || { echo '无法确认恢复前备份文件，停止恢复。' >&2; exit 1; }

# 用单引号引用宿主机参数，失败时给出可原样执行的完整恢复命令。
shell_quote() { printf "'"; printf '%s' "$1" | sed "s/'/'\\\\''/g"; printf "'"; }
restore_failed() {
  echo '恢复失败，应用与计划备份保持停止。用刚才的恢复前备份再恢复一次：' >&2
  {
    printf 'cd '; shell_quote "$root"; printf ' && COMPOSE_PROJECT_NAME='; shell_quote "$project"
    printf ' COMPOSE_ENV_FILES='; shell_quote "$env_file"
    printf ' deploy/production/restore.sh production '; shell_quote "$safety_file"; printf '\n'
  } >&2
  exit 1
}
compose stop app backup
if ! compose exec -T postgres psql -X -U roomusic -d postgres -v ON_ERROR_STOP=1 \
  -c 'DROP DATABASE roomusic WITH (FORCE)' -c 'CREATE DATABASE roomusic OWNER roomusic' >/dev/null 2>&1; then
  restore_failed
fi
if ! compose exec -T postgres pg_restore -U roomusic -d roomusic --no-owner --exit-on-error < "$backup_file" >/dev/null 2>&1; then
  restore_failed
fi
# start 使用已有容器，不会误用工作区里的新镜像重建。
healthy_app() {
  elapsed=0
  while [ "$elapsed" -lt 300 ]; do
    id=$(compose ps -a -q app)
    if [ -n "$id" ] && [ "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$id")" = healthy ]; then
      return 0
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done
  return 1
}
if ! compose start backup app || ! healthy_app; then
  echo '数据已恢复，但应用未健康；检查日志。若备份包含较新的迁移，先 make prod REF=<对应提交>。' >&2
  exit 1
fi
counts_sql | compose exec -T postgres psql -X -qAt -U roomusic -d roomusic -v ON_ERROR_STOP=1
