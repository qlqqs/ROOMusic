#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
mkdir -p "$root/temp"
work=$(mktemp -d "$root/temp/slimproto.XXXXXXXXXX")
suffix=${work##*.}
project="roomusic-compose-test-slimproto-${suffix,,}"
fixtures="$root/scripts/slimproto"
compose=(docker compose --env-file /dev/null -p "$project" -f "$fixtures/compose.yaml")
sidecar=("${compose[@]}" -f "$root/deploy/production/compose.squeezelite.yaml" -f "$fixtures/sidecar.yaml")
collision=(docker compose --env-file /dev/null -p "$project-collision" -f "$fixtures/compose.yaml" -f "$fixtures/collision.yaml")
export SLIMPROTO_WORK="$work" SLIMPROTO_RUN_USER="$(id -u):$(id -g)"
export ROOMUSIC_SQUEEZELITE_IMAGE="$project:local"
export ROOMUSIC_SQUEEZELITE_NAME="ROOMusic USB DAC Probe"
export SLIMPROTO_FFMPEG=
receiver_clock= sidecar_clock= image_built= main_started= collision_started=

project_empty() {
  local label="label=com.docker.compose.project=$1"
  local containers networks volumes
  containers=$(docker ps -aq --filter "$label") || return 1
  networks=$(docker network ls -q --filter "$label") || return 1
  volumes=$(docker volume ls -q --filter "$label") || return 1
  [[ -z "$containers$networks$volumes" ]]
}

address() {
  local published port
  published=$("$@") || return 1
  port=${published##*:}
  [[ $port =~ ^[0-9]+$ ]] || return 1
  echo "http://127.0.0.1:$port"
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if (( status != 0 )); then
    if [[ -n "$main_started" ]]; then
      "${sidecar[@]}" logs --no-color --tail 100 app | grep -v '/readyz' | tail -20 >&2 || true
      "${sidecar[@]}" logs --tail 10 flac-probe >&2 || true
      if [[ -s "$work/receiver/flac.jsonl" ]]; then tail -10 "$work/receiver/flac.jsonl" >&2; fi
    fi
    if [[ -n "$collision_started" ]]; then "${collision[@]}" logs --tail 25 app port-holder cast >&2 || true; fi
    for log in "$work"/{receiver,sidecar}/player.log; do
      if [[ -s "$log" ]]; then
        echo "--- ${log%/player.log} ---" >&2
        grep -E 'discovery|connecting to|connected$|strm command|STAT: STM[cdmnopsru]|track start|flac error' "$log" | tail -35 >&2 || true
      fi
    done
  fi
  if [[ -n "$main_started" ]]; then
    "${sidecar[@]}" down -v --remove-orphans --timeout 2 >&2 || status=1
    project_empty "$project" || status=1
  fi
  if [[ -n "$collision_started" ]]; then
    "${collision[@]}" down -v --remove-orphans --timeout 2 >&2 || status=1
    project_empty "$project-collision" || status=1
  fi
  for pid in "$receiver_clock" "$sidecar_clock"; do
    if [[ -n "$pid" ]]; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  if [[ -n "$image_built" ]]; then docker image rm "$ROOMUSIC_SQUEEZELITE_IMAGE" >&2 || status=1; fi
  rm -rf -- "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

for name in "$project" "$project-collision"; do
  if ! project_empty "$name"; then
    echo "无法确认测试项目为空：$name" >&2
    exit 1
  fi
done
SLIMPROTO_FFMPEG=$("$root/scripts/ffmpeg-pinned.sh")
(cd "$root/backend" && CGO_ENABLED=0 GOOS=linux mise exec -- go build -o "$work/" \
  ./cmd/roomusic ./cmd/roomusic-fake-cast ./cmd/roomusic-fake-renderer ./cmd/airplay-probe)
python3 "$fixtures/playback.py" generate "$work/music" "$SLIMPROTO_FFMPEG"
docker build -q -t "$ROOMUSIC_SQUEEZELITE_IMAGE" -f "$root/deploy/production/squeezelite.Dockerfile" "$root/deploy/production" >/dev/null
image_built=1
docker run --rm --network none --entrypoint apk "$ROOMUSIC_SQUEEZELITE_IMAGE" list --installed squeezelite

docker compose --env-file /dev/null -p "$project" -f "$root/deploy/production/compose.squeezelite.yaml" config --quiet

collision_started=1
"${collision[@]}" up -d --wait app cast renderer homepod-advert
python3 -u "$fixtures/playback.py" collision "$(address "${collision[@]}" port port-holder 8080)" \
  "$(address "${collision[@]}" port cast 8099)"
"${collision[@]}" down -v --timeout 2
project_empty "$project-collision"
collision_started=

for name in receiver sidecar; do
  mkdir "$work/$name"
  mkfifo "$work/$name/audio.fifo"
  touch "$work/$name/player.log"
done
# 抓包容器仅有 NET_RAW，将摘要写入宿主机在本次私有临时目录中创建的文件。
install -m 0666 /dev/null "$work/receiver/flac.jsonl"
python3 "$fixtures/pcm-clock.py" "$work/receiver" --ppm "${ROOMUSIC_SLIMPROTO_CLOCK_PPM:-0}" &
receiver_clock=$!
python3 "$fixtures/pcm-clock.py" "$work/sidecar" &
sidecar_clock=$!

main_started=1
"${sidecar[@]}" config --quiet
"${compose[@]}" up -d --wait app flac-probe
python3 -u "$fixtures/playback.py" playback "$work" "$(address "${compose[@]}" port app 8080)" "${sidecar[@]}"
