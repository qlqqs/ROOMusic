#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
mkdir -p "$root/temp"
work=$(mktemp -d "$root/temp/airplay.XXXXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

case "${1:-}" in
  ""|--ptp) ;;
  --production)
    : "${2:?需要本次演练构建的生产镜像标签}"
    export AIRPLAY_PRODUCTION_IMAGE="$2"
    ;;
  *) echo "用法：$0 [--ptp | --production 本地生产镜像]" >&2; exit 2 ;;
esac

export AIRPLAY_WORK="$work" AIRPLAY_PROBE="$work/airplay-probe"
export AIRPLAY_FFMPEG
AIRPLAY_FFMPEG=$("$root/scripts/ffmpeg-pinned.sh")
(cd "$root/backend" && CGO_ENABLED=0 GOOS=linux mise exec -- go build -o "$work/" ./cmd/airplay-probe ./cmd/roomusic ./cmd/roomusic-ptp ./cmd/roomusic-fake-cast)
python3 "$root/scripts/airplay/playback.py" generate "$work/music" "$AIRPLAY_FFMPEG"

project_empty() {
  local label="label=com.docker.compose.project=$1"
  local containers networks volumes
  containers=$(docker ps -aq --filter "$label") || return 1
  networks=$(docker network ls -q --filter "$label") || return 1
  volumes=$(docker volume ls -q --filter "$label") || return 1
  [[ -z "$containers$networks$volumes" ]]
}

run_fixture() (
  export AIRPLAY_TEST_MODE=$1 AIRPLAY_TEST_NAME=$2 AIRPLAY_TEST_IMAGE=$3
  local clock=${4:-}
  export AIRPLAY_TEST_DIR="$work/${clock:-$1}"
  mkdir "$AIRPLAY_TEST_DIR"
  mkfifo -m 0666 "$AIRPLAY_TEST_DIR/audio.fifo"
  cat > "$AIRPLAY_TEST_DIR/receiver.conf" <<'CONF'
pipe = { name = "/tmp/roomusic-audio"; output_rate = 44100; output_format = "S16_LE"; };
sessioncontrol = { allow_session_interruption = "yes"; };
CONF

  local suffix=${work##*.}
  local project="roomusic-compose-test-airplay-${suffix,,}-${clock:-$1}"
  local compose=(docker compose --env-file /dev/null -f "$root/scripts/airplay/compose.yaml" -p "$project")
  if [[ -n "$clock" ]]; then
    compose+=(-f "$root/scripts/airplay/clock.yaml")
    if [[ -n "${AIRPLAY_PRODUCTION_IMAGE:-}" ]]; then
      compose+=(-f "$root/scripts/airplay/production.yaml")
    fi
    if [[ $clock == occupied ]]; then
      compose+=(-f "$root/scripts/airplay/collision.yaml")
    fi
  fi
  if ! project_empty "$project"; then
    echo "无法确认测试项目为空：$project" >&2
    return 1
  fi

  # 双向打开 FIFO，使接收端在两个播放会话之间关闭输出时，采集器仍能读取下一场。
  cat <>"$AIRPLAY_TEST_DIR/audio.fifo" > "$AIRPLAY_TEST_DIR/audio.pcm" &
  local reader=$! clock_check=
  cleanup() {
    local status=$?
    trap - EXIT INT TERM
    if [[ -n "$clock_check" ]]; then
      kill "$clock_check" 2>/dev/null || true
      wait "$clock_check" 2>/dev/null || true
    fi
    if (( status != 0 )); then
      if [[ -n "$clock" ]]; then "${compose[@]}" logs --tail 20 ptp >&2 || true; fi
      "${compose[@]}" logs --tail 45 app >&2 || true
      "${compose[@]}" logs receiver | grep -Ei 'warning:|error:|SETRATEANCHORTIME|start playing' | tail -30 >&2 || true
    fi
    "${compose[@]}" down -v --remove-orphans --timeout 2 >&2 || status=1
    if ! project_empty "$project"; then
      echo "无法确认测试项目资源已清理：$project" >&2
      status=1
    fi
    kill "$reader" 2>/dev/null || true
    wait "$reader" 2>/dev/null || true
    exit "$status"
  }
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  "${compose[@]}" up -d receiver app
  if [[ $clock == occupied ]]; then
    "${compose[@]}" up -d port-holder homepod-advert cast
    for _ in {1..30}; do
      [[ $("${compose[@]}" logs --no-color port-holder) == *occupied* ]] && break
      sleep .1
    done
    [[ $("${compose[@]}" logs --no-color port-holder) == *occupied* ]]
  fi
  if [[ -n "$clock" ]]; then
    "${compose[@]}" up -d ptp
    if [[ -n "${AIRPLAY_PRODUCTION_IMAGE:-}" ]]; then
      local settings='{{json .HostConfig.CapDrop}} {{json .HostConfig.CapAdd}} {{.Config.User}} {{.HostConfig.ReadonlyRootfs}}'
      local app_caps ptp_caps
      app_caps=$(docker inspect -f "$settings" "$("${compose[@]}" ps -q app)")
      ptp_caps=$(docker inspect -f "$settings" "$("${compose[@]}" ps -q ptp)")
      echo "production permissions: app=$app_caps ptp=$ptp_caps"
      [[ $app_caps =~ ^\[\"ALL\"\]\ (null|\[\])\ 1000:1000\ true$ && ${ptp_caps/CAP_NET_BIND_SERVICE/NET_BIND_SERVICE} == '["ALL"] ["NET_BIND_SERVICE"] 0:0 true' ]]
    fi
  fi
  if [[ $clock == ptp || $clock == occupied ]]; then
    python3 "$root/scripts/airplay/check-clock.py" "$clock" "${compose[@]}" &
    clock_check=$!
  fi
  if [[ $clock == lost ]]; then
    install -m 0666 /dev/null "$AIRPLAY_TEST_DIR/first-tone"
    (
      for _ in {1..400}; do
        if [[ -s "$AIRPLAY_TEST_DIR/first-tone" ]]; then
          "${compose[@]}" stop --timeout 1 ptp
          exit
        fi
        sleep .1
      done
      echo "首曲尚未输出，未触发 PTP 停止" >&2
      exit 1
    ) &
    clock_check=$!
  fi
  "${compose[@]}" run --rm -T --no-deps sender
  if [[ $AIRPLAY_TEST_MODE == airplay2 ]]; then
    "${compose[@]}" exec -T receiver sh -c 'pidof nqptp >/dev/null && test -s /dev/shm/nqptp'
  fi
  local playback_mount=()
  if [[ $clock == lost ]]; then
    playback_mount=(-v "$AIRPLAY_TEST_DIR/first-tone:/first-tone")
  fi
  "${compose[@]}" run --rm -T --no-deps "${playback_mount[@]}" playback "$AIRPLAY_TEST_MODE" "$AIRPLAY_TEST_NAME" "$clock"
  if [[ -n "$clock_check" ]]; then
    wait "$clock_check"
    clock_check=
  fi
  local connected
  connected=$("${compose[@]}" logs --no-log-prefix app | python3 -c '
import json, sys
first = None
for line in sys.stdin:
    if first is None and line.startswith("{"):
        row = json.loads(line)
        if row.get("event") == "airplay.output.connected":
            first = row
if first is not None:
    print(json.dumps(first, separators=(",", ":")))
')
  echo "ROOMusic sender: $connected"
  if [[ $clock == ptp || $clock == lost ]]; then
    [[ $connected == *'"protocol":"airplay2"'* && $connected == *'"timing":"PTP"'* && $connected == *'"fallback":null'* ]]
  else
    [[ $connected == *'"protocol":"raop"'* && $connected == *'"timing":"NTP"'* ]]
  fi
  if [[ $clock == ptp ]]; then
    "${compose[@]}" logs --no-log-prefix receiver | awk '
        /SETRATEANCHORI Start playing, with anchor clock/ { anchors[$NF] = $0 }
        /NQPTP master clock [0-9a-f]+\./ { clocks[$NF] = $0 }
        END {
          for (id in anchors) if (id in clocks) {
            print anchors[id]
            print clocks[id]
            exit 0
          }
          print "接收端未确认同一主时钟的播放锚点和 NQPTP 选钟" > "/dev/stderr"
          exit 1
        }'
  elif [[ $clock == lost ]]; then
    [[ -n $("${compose[@]}" ps --status exited -q ptp) ]]
    "${compose[@]}" logs --no-log-prefix receiver | grep -F 'Classic AirPlay ("AirPlay 1") playback.'
  fi
)

# 上游 Docker Hub 的 5.6.3 与 5.6.3-classic 多架构镜像。
# 每一轮都有独立网络，nqptp 在接收端容器内绑定 UDP 319/320。
receiver2=mikebrady/shairport-sync@sha256:8ae074fc6ca71dc9e52789afc21e59319d878efe5b06d2ac4e9a08db78ad329c
run_fixture airplay2 ROOMusic-AirPlay2 "$receiver2" ptp
[[ "${1:-}" == --ptp ]] && exit 0
run_fixture airplay2 ROOMusic-AirPlay2 "$receiver2" occupied
[[ "${1:-}" == --production ]] && exit 0
run_fixture airplay2 ROOMusic-AirPlay2 "$receiver2" lost
run_fixture classic ROOMusic-Classic mikebrady/shairport-sync@sha256:364779d502857a88fc7a88068f6f1b85b4bfa1e04ffca34787f6621c7bb35554

run_native() (
  export AIRPLAY_TEST_DIR="$work/native"
  mkdir "$AIRPLAY_TEST_DIR"
  touch "$AIRPLAY_TEST_DIR/audio.pcm"
  local suffix=${work##*.}
  local project="roomusic-compose-test-airplay-${suffix,,}-native"
  local compose=(docker compose --env-file /dev/null -f "$root/scripts/airplay/native.yaml" -p "$project")
  if ! project_empty "$project"; then
    echo "无法确认测试项目为空：$project" >&2
    return 1
  fi
  cleanup() {
    local status=$?
    trap - EXIT INT TERM
    if (( status != 0 )); then
      # 旧接收端会打印配对报文，失败时只显示协议状态与错误类型。
      "${compose[@]}" logs receiver | sed -nE '/(Error:|SETUP rtsp|Sending CONTROL|serving at port)/p' >&2 || true
    fi
    "${compose[@]}" down -v --remove-orphans --timeout 2 >&2 || status=1
    if ! project_empty "$project"; then
      echo "无法确认测试项目资源已清理：$project" >&2
      status=1
    fi
    exit "$status"
  }
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  "${compose[@]}" up -d --wait receiver
  "${compose[@]}" run --rm -T --no-deps sender
  python3 "$root/scripts/airplay/playback.py" verify-native "$AIRPLAY_TEST_DIR/audio.pcm"
)

if [[ $(docker version --format '{{.Server.Arch}}') == amd64 ]]; then
  run_native
else
  echo '第三个原生接收端镜像仅提供 linux/amd64，本次仅验证了上方两个多架构接收端。'
fi
