#!/usr/bin/env bash
set -euo pipefail
ROOT=/home/qlqq/.paseo/worktrees/2i6geckb/m5-search-artists
OUT=/tmp/roomusic-m5s-replay-results
exec 9>/tmp/roomusic-test-m5s.lock
flock -n 9 || { echo '隔离项目锁已被占用'; exit 1; }
test -z "$(docker ps -aq --filter label=com.docker.compose.project=roomusic-test-m5s)" || { echo '隔离项目已有容器，不接管'; exit 1; }
test -z "$(docker volume ls -q --filter label=com.docker.compose.project=roomusic-test-m5s)" || { echo '隔离项目已有卷，不接管'; exit 1; }
unset ROOMUSIC_DATABASE_URL ROOMUSIC_UPDATE_API_SAMPLES
export ROOMUSIC_TEST_PG_PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
export ROOMUSIC_TEST_PG_DATABASE="m5s_replay_$(python3 -c 'import secrets; print(secrets.token_hex(8))')"
export ROOMUSIC_TEST_PG_USER=roomusic_test
export ROOMUSIC_TEST_PG_PASSWORD="$(python3 -c 'import secrets; print(secrets.token_hex(16))')"
export ROOMUSIC_TEST_DATABASE_URL="postgres://${ROOMUSIC_TEST_PG_USER}:${ROOMUSIC_TEST_PG_PASSWORD}@127.0.0.1:${ROOMUSIC_TEST_PG_PORT}/${ROOMUSIC_TEST_PG_DATABASE}?sslmode=disable"
export ROOMUSIC_TEST_FFMPEG=/home/qlqq/.cache/roomusic/ffmpeg-11a44711684c/ffmpeg
export ROOMUSIC_REAL_MUSIC_ROOT="$ROOT/backend/testdata/playback"
export ROOMUSIC_CATALOG_EXPLAIN=1
export ROOMUSIC_CATALOG_EXPLAIN_DIR="$OUT/personal-explain"
export ROOMUSIC_CATALOG_EXPLAIN_OUTPUT="$OUT/recent-explain.md"
export ROOMUSIC_CATALOG_HTTP_PERF_OUTPUT="$OUT/catalog-http-performance.md"
COMPOSE=(docker compose --env-file /dev/null -f "$ROOT/compose.test.yaml" -p roomusic-test-m5s)
created=0
cleanup() { if [ "$created" = 1 ]; then "${COMPOSE[@]}" down -v --remove-orphans; fi; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
created=1
"${COMPOSE[@]}" up -d --wait postgres
printf '隔离项目 roomusic-test-m5s，port=%s，database=%s\n' "$ROOMUSIC_TEST_PG_PORT" "$ROOMUSIC_TEST_PG_DATABASE"
cd "$ROOT/backend"
"$@"
