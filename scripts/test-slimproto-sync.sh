#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fixtures="$root/scripts/slimproto"
duration=${ROOMUSIC_SLIMPROTO_SYNC_SECONDS:-600}
if [[ ! $duration =~ ^[1-9][0-9]*$ ]]; then
  echo 'ROOMUSIC_SLIMPROTO_SYNC_SECONDS 必须是正整数秒' >&2
  exit 2
fi
mkdir -p "$root/temp"
work=$(mktemp -d "$root/temp/slimproto-sync.XXXXXXXXXX")
suffix=${work##*.}
project="roomusic-compose-test-slimproto-sync-${suffix,,}"
compose=(docker compose --env-file /dev/null -p "$project" -f "$fixtures/compose.yaml" -f "$fixtures/sync.yaml")

export SLIMPROTO_WORK="$work" SLIMPROTO_RUN_USER="$(id -u):$(id -g)" SLIMPROTO_FFMPEG=
export ROOMUSIC_SQUEEZELITE_IMAGE="${ROOMUSIC_SLIMPROTO_PLAYER_IMAGE:-$project-player:local}"
export ROOMUSIC_SLIMPROTO_NETEM_IMAGE="${ROOMUSIC_SLIMPROTO_PLAYER_IMAGE:-$project-netem:local}"
clocks=() images=() started=

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ -n "$started" ]]; then
    if (( status != 0 )); then
      "${compose[@]}" logs --no-color --tail 250 app postgres | \
        sed -E 's@/upnp/streams/[^" ?]*@/upnp/streams/<token>@g' | \
        grep -vE '/readyz|http\.request\.completed' | tail -35 >&2 || true
      for name in receiver member; do
        echo "--- $name ---" >&2
        grep -E 'strm command [^t]|STAT: STM[^t]|autostart|track start|streaming|skip .*frames|bytes received|disconnect' \
          "$work/$name/player.log" | tail -40 >&2 || true
        # 保留整个阶段的计时器重启、欠载和修正；脉冲只需末尾几条。
        grep -E '"kind": "(clock|underrun|correction)"' "$work/$name/events.jsonl" >&2 || true
        tail -8 "$work/$name/events.jsonl" >&2 || true
      done
    fi
    "${compose[@]}" down -v --remove-orphans --timeout 2 >&2 || status=1
  fi
  for clock in "${clocks[@]}"; do
    kill "$clock" 2>/dev/null || true
    wait "$clock" 2>/dev/null || true
  done
  for image in "${images[@]}"; do docker image rm "$image" >&2 || status=1; done
  rm -rf -- "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

SLIMPROTO_FFMPEG=$("$root/scripts/ffmpeg-pinned.sh")
(cd "$root/backend" && CGO_ENABLED=0 GOOS=linux mise exec -- go build -o "$work/roomusic" ./cmd/roomusic)
python3 -B "$fixtures/sync-playback.py" generate "$work/music" "$SLIMPROTO_FFMPEG" "$duration"
if [[ -z "${ROOMUSIC_SLIMPROTO_PLAYER_IMAGE:-}" ]]; then
  docker build -q -t "$ROOMUSIC_SQUEEZELITE_IMAGE" -f "$root/deploy/production/squeezelite.Dockerfile" "$root/deploy/production" >/dev/null
  images+=("$ROOMUSIC_SQUEEZELITE_IMAGE")
  docker build -q --build-arg "SQUEEZELITE_IMAGE=$ROOMUSIC_SQUEEZELITE_IMAGE" \
    -t "$ROOMUSIC_SLIMPROTO_NETEM_IMAGE" -f "$fixtures/sync.Dockerfile" "$fixtures" >/dev/null
  images+=("$ROOMUSIC_SLIMPROTO_NETEM_IMAGE")
fi

for name in receiver member; do
  mkdir "$work/$name"
  mkfifo "$work/$name/audio.fifo"
  touch "$work/$name/player.log"
  echo 0 > "$work/$name/ppm"
  python3 -B "$fixtures/sync-clock.py" "$work/$name" &
  clocks+=("$!")
done

"${compose[@]}" config --quiet
started=1
"${compose[@]}" up -d --wait app
python3 -Bu "$fixtures/sync-playback.py" run "$work" "$duration" "${compose[@]}"
