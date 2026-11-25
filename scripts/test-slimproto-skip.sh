#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fixtures="$root/scripts/slimproto"
mkdir -p "$root/temp/slimproto-skip"
result="$root/temp/slimproto-skip/$(date -u +%Y%m%dT%H%M%S.%NZ)"
mkdir "$result"
work=$(mktemp -d "$root/temp/slimproto-skip.XXXXXXXXXX")
suffix=${work##*.}
project="roomusic-compose-test-slimproto-skip-${suffix,,}"
compose=(docker compose --env-file /dev/null -p "$project" -f "$fixtures/compose.yaml" -f "$fixtures/sync.yaml")
export SLIMPROTO_WORK="$work" SLIMPROTO_RUN_USER="$(id -u):$(id -g)"
export ROOMUSIC_SQUEEZELITE_IMAGE="$project-player:local"
# 两台用同一份 1613 镜像，这项测量只改变成员的 PCM 时钟。
export ROOMUSIC_SLIMPROTO_NETEM_IMAGE="$ROOMUSIC_SQUEEZELITE_IMAGE"
export SLIMPROTO_FFMPEG
clocks=() started= image_built=

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ -n "$started" ]]; then
    if (( status != 0 )); then
      for name in receiver member; do
        grep -E 'track start|strm command [^t]|skip ahead|STAT: STM[lsu]' "$work/$name/player.log" | tail -15 >&2 || true
      done
    fi
    "${compose[@]}" down -v --remove-orphans --timeout 2 >&2 || status=1
  fi
  for clock in "${clocks[@]}"; do
    kill "$clock" 2>/dev/null || true
    wait "$clock" 2>/dev/null || true
  done
  if [[ -n "$image_built" ]]; then docker image rm "$ROOMUSIC_SQUEEZELITE_IMAGE" >&2 || status=1; fi
  rm -f -- "$result"/{receiver,member}/{audio.fifo,player.log}
  rm -rf -- "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "PCM、WAV 与报告目录：$result"
SLIMPROTO_FFMPEG=$("$root/scripts/ffmpeg-pinned.sh")
(cd "$root/backend" && CGO_ENABLED=0 GOOS=linux mise exec -- go build -o "$work/roomusic" ./cmd/roomusic)
python3 -B "$fixtures/skip-analysis.py" generate "$work" "$SLIMPROTO_FFMPEG"
docker build -q -t "$ROOMUSIC_SQUEEZELITE_IMAGE" -f "$root/deploy/production/squeezelite.Dockerfile" "$root/deploy/production" >/dev/null
image_built=1

for name in receiver member; do
  mkdir "$result/$name"
  ln -s "$result/$name" "$work/$name"
  mkfifo "$work/$name/audio.fifo"
  touch "$work/$name/player.log"
  ppm=0
  if [[ $name == member ]]; then ppm=800; fi
  echo "$ppm" > "$work/$name/ppm"
  python3 -B "$fixtures/sync-clock.py" "$work/$name" --record &
  clocks+=("$!")
done

"${compose[@]}" config --quiet
started=1
"${compose[@]}" up -d --wait app
python3 -Bu "$fixtures/sync-playback.py" record "$work" "${compose[@]}"
# 保留曲尾修正之后的几秒，供两台截取相同时段。
sleep 3
"${compose[@]}" stop -t 2 receiver member
for clock in "${clocks[@]}"; do wait "$clock"; done
clocks=()
python3 -Bu "$fixtures/skip-analysis.py" analyze "$work" "$result"
