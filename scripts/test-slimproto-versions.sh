#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
mkdir -p "$root/temp/slimproto-versions"
out=$(mktemp -d "$root/temp/slimproto-versions/$(date -u +%Y%m%dT%H%M%SZ).XXXXXXXX")
suffix=${out##*.}
image=
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ -n "$image" ]]; then docker image rm "$image" >/dev/null || status=1; fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf '矩阵日志 %s\n' "$out"
summary="$out/results.tsv"
printf '来源\tMICRO_VERSION\t提交\t构建\t单台\t编组\n' > "$summary"
result=0
while read -r -u 3 source micro commit; do
  # 发行说明与逐版源码链接见任务 research/squeezelite-versions.md。
  # 可传一个 MICRO_VERSION 以复查某一行。
  if [[ -n "${1:-}" && "$1" != "$micro" ]]; then continue; fi
  if [[ "$micro" == 1613 ]]; then
    # 对照走生产 Dockerfile 和两个入口的默认镜像路径。
    build=沿用两入口
    unset ROOMUSIC_SLIMPROTO_PLAYER_IMAGE
  else
    tag="roomusic-test-slimproto-version-${suffix,,}:$micro"
    echo "构建 $source $micro"
    if ! docker build --progress=plain -t "$tag" --build-arg "SQUEEZELITE_MICRO=$micro" \
        --build-arg "SQUEEZELITE_COMMIT=$commit" -f "$root/scripts/slimproto/squeezelite-ref.Dockerfile" \
        "$root/scripts/slimproto" > "$out/$micro-build.log" 2>&1; then
      tail -15 "$out/$micro-build.log" >&2
      printf '%s\t%s\t%s\t失败\t未运行\t未运行\n' "$source" "$micro" "$commit" >> "$summary"
      result=1
      continue
    fi
    build=通过
    image=$tag
    export ROOMUSIC_SLIMPROTO_PLAYER_IMAGE=$image
  fi
  single=通过
  sync=通过
  echo "单台 $source $micro"
  if ! "$root/scripts/test-slimproto.sh" > "$out/$micro-single.log" 2>&1; then single=失败; result=1; fi
  echo "编组 $source $micro"
  if ! ROOMUSIC_SLIMPROTO_SYNC_SECONDS=120 \
      "$root/scripts/test-slimproto-sync.sh" > "$out/$micro-sync.log" 2>&1; then sync=失败; result=1; fi
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$source" "$micro" "$commit" "$build" "$single" "$sync" >> "$summary"
  if [[ -n "$image" ]]; then docker image rm "$image" >/dev/null; image=; fi
done 3<<'MATRIX'
piCorePlayer-9.0.0 1468 df4df96fefd316ab51d455cf5272cdeb13317cb3
piCorePlayer-10.0.0 1476 7878fada339edebb72904da54f28350b44021510
piCorePlayer-11.1.0 1524 db51a7b16934f41b72437394bf8114c3a85e0a91
moOde-10.3.7 1541 72e1fd8abfa9b2f8e9636f033247526920878718
Debian-13 1517 262994a989dc29ce3be4390c57c6a43373dfdca2
ROOMusic 1613 b02a9ac77794c9914fa2b70caa98010e9f23cc2d
MATRIX
if [[ $(wc -l < "$summary") -eq 1 ]]; then echo "没有这个 MICRO_VERSION：$1" >&2; exit 2; fi
cat "$summary"
exit "$result"
