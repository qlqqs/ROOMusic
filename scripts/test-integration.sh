#!/usr/bin/env bash
# 测试只使用独占项目、随机数据库和默认 bridge；不读取本机 .env。
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# 与真实播放入口使用相同的独占项目命名规则。
COMPOSE_PROJECT="${ROOMUSIC_TEST_COMPOSE_PROJECT:-roomusic-test}"
[[ "$COMPOSE_PROJECT" =~ ^roomusic-test(-[a-z0-9]+)*$ ]] || {
  echo '测试项目必须是 roomusic-test 或其小写字母数字后缀项目' >&2; exit 1;
}
COMPOSE=(docker compose --env-file /dev/null -f "$ROOT_DIR/compose.test.yaml" -p "$COMPOSE_PROJECT")
exec 9>"${TMPDIR:-/tmp}/roomusic-playback-e2e.lock"
flock -n 9 || { echo '已有数据库集成测试或真实播放测试运行中' >&2; exit 1; }
existing_containers="$(docker ps -aq --filter "label=com.docker.compose.project=$COMPOSE_PROJECT")"
existing_volumes="$(docker volume ls -q --filter "label=com.docker.compose.project=$COMPOSE_PROJECT")"
existing_networks="$(docker network ls -q --filter "label=com.docker.compose.project=$COMPOSE_PROJECT")"
[[ -z "$existing_containers$existing_volumes$existing_networks" ]] || {
  echo "$COMPOSE_PROJECT 已有容器、卷或旧项目网络，拒绝接管历史测试资源" >&2; exit 1;
}
export ROOMUSIC_TEST_PG_PORT="${ROOMUSIC_TEST_PG_PORT:-$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    print(sock.getsockname()[1])
PY
)}"
export ROOMUSIC_TEST_PG_DATABASE="integration_${RANDOM}_${RANDOM}"
export ROOMUSIC_TEST_PG_USER=roomusic_test
export ROOMUSIC_TEST_PG_PASSWORD=roomusic_test
export ROOMUSIC_TEST_DATABASE_URL="postgres://${ROOMUSIC_TEST_PG_USER}:${ROOMUSIC_TEST_PG_PASSWORD}@127.0.0.1:${ROOMUSIC_TEST_PG_PORT}/${ROOMUSIC_TEST_PG_DATABASE}?sslmode=disable"

# 工具准备失败时立即退出，不允许相关用例静默 SKIP。
if [[ -z "${ROOMUSIC_TEST_FFMPEG:-}" ]]; then
  ROOMUSIC_TEST_FFMPEG="$("$ROOT_DIR/scripts/ffmpeg-pinned.sh")"
fi
if [[ -z "${ROOMUSIC_TEST_PG_BIN_DIR:-}" ]]; then
  ROOMUSIC_TEST_PG_BIN_DIR="$("$ROOT_DIR/scripts/pg-client-pinned.sh")"
fi
export ROOMUSIC_TEST_FFMPEG ROOMUSIC_TEST_PG_BIN_DIR

cleanup() {
  local result=$?
  trap - EXIT INT TERM
  if [[ "${ROOMUSIC_TEST_KEEP_DB:-false}" != true ]]; then
    "${COMPOSE[@]}" down -v --remove-orphans >/dev/null || result=1
  else
    echo '保留 PostgreSQL 测试容器（ROOMUSIC_TEST_KEEP_DB=true）' >&2
  fi
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
"${COMPOSE[@]}" up -d --wait postgres
printf '运行隔离 PostgreSQL 与 ffmpeg 集成测试：project=%s database=%s port=%s\n' "$COMPOSE_PROJECT" "$ROOMUSIC_TEST_PG_DATABASE" "$ROOMUSIC_TEST_PG_PORT" >&2
# ROOMUSIC_TEST_RUN 可缩小测试集；未设置时保留原有 PostgreSQL/ffmpeg 范围。
# app 包串行约 8 分钟，全包并行共用一个库时会更慢。
(cd "$ROOT_DIR/backend" && go test -v ./... -run "${ROOMUSIC_TEST_RUN:-TestPostgreSQL|TestFFmpeg}" -count=1 -timeout 15m)
