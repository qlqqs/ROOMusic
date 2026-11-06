#!/usr/bin/env bash
# 只使用合成素材、独立数据库和独占测试项目；不读取 .env 或真实音乐目录。
# ALAC 的派生播放用 scripts/ffmpeg-pinned.sh 取出的固定版本 ffmpeg（经 ROOMUSIC_FFMPEG_PATH 交给隔离服务）；取不到时脚本失败，
# 不会静默退回“ALAC 不能播放”。
set -Eeuo pipefail
umask 077
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
# 扫描反馈入口只生成用例自己的三首短 WAV，不准备全套播放素材或 ffmpeg。
SCAN_FEEDBACK_ONLY=false
if [[ "${1:-}" == '--scan-feedback-only' ]]; then
  SCAN_FEEDBACK_ONLY=true
  shift
  set -- e2e/real/scan-feedback.real.ts "$@"
fi
unset ROOMUSIC_DATABASE_URL ROOMUSIC_TEST_DATABASE_URL
for dependency in docker go npm python3 curl flock; do
  command -v "$dependency" >/dev/null || { echo "缺少依赖：$dependency" >&2; exit 1; }
done
# 默认保持原测试项目；并行实现线可显式选择带安全前缀的独占项目。
COMPOSE_PROJECT="${ROOMUSIC_TEST_COMPOSE_PROJECT:-roomusic-test}"
[[ "$COMPOSE_PROJECT" =~ ^roomusic-test(-[a-z0-9]+)*$ ]] || {
  echo '测试项目必须是 roomusic-test 或其小写字母数字后缀项目' >&2; exit 1;
}
docker info >/dev/null
# 与其他使用同一 Compose project 的本入口互斥；已存在的测试容器也不能接管。
exec 9>"${TMPDIR:-/tmp}/roomusic-playback-e2e.lock"
flock -n 9 || { echo '已有真实播放测试运行中' >&2; exit 1; }
# 测试库使用默认 bridge，不创建项目网络；仍拒绝旧版遗留的孤立网络，不能接管他人的资源。
# 查询失败必须停下。
existing_containers="$(docker ps -aq --filter "label=com.docker.compose.project=$COMPOSE_PROJECT")" || { echo '无法检查已有测试容器' >&2; exit 1; }
existing_volumes="$(docker volume ls -q --filter "label=com.docker.compose.project=$COMPOSE_PROJECT")" || { echo '无法检查已有测试数据卷' >&2; exit 1; }
existing_networks="$(docker network ls -q --filter "label=com.docker.compose.project=$COMPOSE_PROJECT")" || { echo '无法检查已有测试网络' >&2; exit 1; }
[[ -z "$existing_containers$existing_volumes$existing_networks" ]] || {
  echo "$COMPOSE_PROJECT 已有容器、数据卷或网络；拒绝接管历史测试资源" >&2; exit 1;
}
# 先于任何临时资源取出 ffmpeg：它只用 docker create/cp（不属于 roomusic-test 项目），失败就直接退出。
FFMPEG_PATH=''
if [[ "$SCAN_FEEDBACK_ONLY" == false ]]; then
  FFMPEG_PATH="$("$ROOT_DIR/scripts/ffmpeg-pinned.sh")"
fi
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/roomusic-playback-e2e.XXXXXXXX")"
SERVER_PID=''
COMPOSE_STARTED=false
COMPOSE=(docker compose --env-file /dev/null -f "$ROOT_DIR/compose.test.yaml" -p "$COMPOSE_PROJECT")
cleanup() {
  local result=$?
  trap - EXIT INT TERM
  if [[ -n "$SERVER_PID" ]]; then
    kill -TERM "$SERVER_PID" 2>/dev/null || true
    # 等待本轮精确 PID，绝不使用 pkill 或按端口杀进程。
    for _ in {1..60}; do kill -0 "$SERVER_PID" 2>/dev/null || break; sleep 0.2; done
    kill -KILL "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [[ "$COMPOSE_STARTED" == true ]]; then "${COMPOSE[@]}" down -v >/dev/null || result=1; fi
  rm -rf -- "$TEMP_DIR"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
free_port() {
  python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    print(sock.getsockname()[1])
PY
}
export ROOMUSIC_TEST_PG_PORT="$(free_port)"
export ROOMUSIC_TEST_PG_DATABASE="playback_${RANDOM}_${RANDOM}"
export ROOMUSIC_TEST_PG_USER=roomusic_test
export ROOMUSIC_TEST_PG_PASSWORD=roomusic_test
export ROOMUSIC_TEST_DATABASE_URL="postgres://${ROOMUSIC_TEST_PG_USER}:${ROOMUSIC_TEST_PG_PASSWORD}@127.0.0.1:${ROOMUSIC_TEST_PG_PORT}/${ROOMUSIC_TEST_PG_DATABASE}?sslmode=disable"
mkdir -p "$TEMP_DIR/library" "$TEMP_DIR/backend"
if [[ "$SCAN_FEEDBACK_ONLY" == false ]]; then
mkdir -p "$TEMP_DIR/library/formats" "$TEMP_DIR/library/cue" "$TEMP_DIR/library/slow"
cp "$ROOT_DIR"/backend/testdata/playback/*.{flac,m4a,mp3,wav,ogg,opus} "$TEMP_DIR/library/formats/"
cp "$ROOT_DIR/backend/testdata/playback/flac-16-44100.flac" "$TEMP_DIR/library/cue/image.flac"
cat > "$TEMP_DIR/library/cue/image.cue" <<'CUE'
PERFORMER "Fixture Artist"
TITLE "Virtual CUE Fixtures"
FILE "image.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Virtual First"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Virtual Second"
    INDEX 01 00:01:00
CUE
# 180 秒 PCM 素材用于真实拖动与播放历史门槛验证，以及跨标签页退出时的停播清理。
python3 - "$TEMP_DIR/library/slow/slow.wav" <<'PY'
import struct, sys
samples = 44100 * 180 * 4
info = b''
for key, value in [(b'INAM', b'Slow Revocation'), (b'IPRD', b'Slow Fixtures'), (b'IART', b'Fixture Artist')]:
    value += b'\0'
    info += key + struct.pack('<I', len(value)) + value + (b'\0' if len(value) % 2 else b'')
metadata = b'LIST' + struct.pack('<I', len(info) + 4) + b'INFO' + info
with open(sys.argv[1], 'wb') as out:
    out.write(b'RIFF' + struct.pack('<I', 36 + samples + len(metadata)) + b'WAVEfmt ')
    out.write(struct.pack('<IHHIIHH', 16, 1, 2, 44100, 176400, 4, 16))
    out.write(metadata + b'data' + struct.pack('<I', samples))
    chunk = bytes(65536)
    while samples:
        size = min(samples, len(chunk))
        out.write(chunk[:size]); samples -= size
PY
fi
# 白名单复制编译源树，不接触 backend/data 或其他运行期目录；内嵌资产也只在临时树生成。
(cd "$ROOT_DIR/backend" && tar --exclude='cmd/roomusic/web' --exclude='.env' --exclude='.env.*' -cf - go.mod go.sum cmd internal migrations testdata) | (cd "$TEMP_DIR/backend" && tar -xf -)
# Vite 默认会加载 .env；此隔离配置显式关闭 envDir，连前端 .env 也不读取。
ln -s "$ROOT_DIR/frontend/node_modules" "$TEMP_DIR/node_modules"
python3 - "$ROOT_DIR/frontend/vite.config.ts" "$TEMP_DIR/vite.config.mts" <<'PYCONFIG'
import json, sys
with open(sys.argv[2], 'w') as config:
    config.write('import base from ' + json.dumps(sys.argv[1]) + ';\n')
    config.write('export default { ...base, envDir: false };\n')
PYCONFIG
(cd "$ROOT_DIR/frontend" && npm exec tsc -- --noEmit && npm exec vite -- build --config "$TEMP_DIR/vite.config.mts" --outDir "$TEMP_DIR/backend/cmd/roomusic/web" --emptyOutDir)
(cd "$TEMP_DIR/backend" && go build -o "$TEMP_DIR/roomusic" ./cmd/roomusic)
# 编译门禁通过后才启动 PostgreSQL，避免源码尚未稳定时创建测试数据库。
printf '隔离测试：project=%s database=%s pg_port=%s compose=%s\n' "$COMPOSE_PROJECT" "$ROOMUSIC_TEST_PG_DATABASE" "$ROOMUSIC_TEST_PG_PORT" "$ROOT_DIR/compose.test.yaml"
COMPOSE_STARTED=true
"${COMPOSE[@]}" up -d --wait postgres
PORT="$(free_port)"
export ROOMUSIC_PLAYBACK_BASE_URL="http://127.0.0.1:$PORT"
export ROOMUSIC_PLAYBACK_FIXTURE_ROOT="$TEMP_DIR/library"
env ROOMUSIC_ENV=development ROOMUSIC_HTTP_ADDR="127.0.0.1:$PORT" \
  ROOMUSIC_DATABASE_URL="$ROOMUSIC_TEST_DATABASE_URL" ROOMUSIC_ALLOWED_LIBRARY_ROOTS="$TEMP_DIR/library" \
  ROOMUSIC_DATA_DIR="$TEMP_DIR/data" ROOMUSIC_PUBLIC_URL="$ROOMUSIC_PLAYBACK_BASE_URL" ROOMUSIC_SECURE_COOKIES=false \
  ROOMUSIC_FFMPEG_PATH="$FFMPEG_PATH" \
  "$TEMP_DIR/roomusic" >"$TEMP_DIR/server.log" 2>&1 &
SERVER_PID=$!
ready=false
for _ in {1..150}; do
  kill -0 "$SERVER_PID" 2>/dev/null || { echo '隔离服务提前退出' >&2; exit 1; }
  if curl --silent --fail "$ROOMUSIC_PLAYBACK_BASE_URL/api/v1/setup/status" >/dev/null; then ready=true; break; fi
  sleep 0.2
done
[[ "$ready" == true ]] || { echo '隔离服务启动超时' >&2; exit 1; }
# 派生必须已就绪（ffmpeg 自检通过）：否则 ALAC 用例会以“不能播放”失败，这里先给出明确原因。
if [[ "$SCAN_FEEDBACK_ONLY" == false ]]; then
  grep -q '"event":"playback.derivation.ready"' "$TEMP_DIR/server.log" || { echo '隔离服务没有启用 ALAC 派生（ffmpeg 不可用或自检失败）' >&2; exit 1; }
fi
(cd "$ROOT_DIR/frontend" && npm exec playwright -- test --config playwright.real.config.ts "$@")
