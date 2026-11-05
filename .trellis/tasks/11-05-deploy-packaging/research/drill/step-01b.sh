#!/usr/bin/env bash
# 仅在临时 clone 制造分支/脏文件，主仓库不提交、不切分支。
set -euo pipefail
ROOT=/home/qlqq/workspace/ROOMusic
CLONE=/tmp/roomusic-drill/clone
MAIN_SHA=$(git -C "$ROOT" rev-parse main)
export BUILDKIT_PROGRESS=plain
printf '命令：git clone -q %s %s\n' "$ROOT" "$CLONE"
git clone -q "$ROOT" "$CLONE"
cd "$CLONE"
printf '命令：git switch -q -c drill-side\n'
git switch -q -c drill-side
printf '\nLABEL drill.marker=branch\n' >> deploy/production/Dockerfile
# 提交只发生在临时 clone；身份只对这一条命令生效。
git -c user.name=roomusic-drill -c user.email=drill@localhost commit -qam 'drill: side branch'
SIDE_SHA=$(git rev-parse HEAD)
printf 'LABEL drill.marker=dirty\n' >> deploy/production/Dockerfile
printf '命令：scripts/production.sh build-image --prefix roomusic-drill-c6\n'
scripts/production.sh build-image --prefix roomusic-drill-c6
printf '命令：docker image inspect（revision、drill.marker）\n'
docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}} {{index .Config.Labels "drill.marker"}}' roomusic-drill-c6-app:current
test "$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' roomusic-drill-c6-app:current)" = "$MAIN_SHA"
test "$(docker image inspect -f '{{index .Config.Labels "drill.marker"}}' roomusic-drill-c6-app:current)" = ''
printf '命令：scripts/production.sh build-image --prefix roomusic-drill-c6 --ref drill-side\n'
scripts/production.sh build-image --prefix roomusic-drill-c6 --ref drill-side
docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}} {{index .Config.Labels "drill.marker"}}' roomusic-drill-c6-app:current
test "$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' roomusic-drill-c6-app:current)" = "$SIDE_SHA"
test "$(docker image inspect -f '{{index .Config.Labels "drill.marker"}}' roomusic-drill-c6-app:current)" = branch
printf '命令：scripts/production.sh build-image --prefix roomusic-drill-c6 --ref no-such-ref（预期退出 2）\n'
set +e
scripts/production.sh build-image --prefix roomusic-drill-c6 --ref no-such-ref
rc=$?
set -e
printf '实际退出码：%s\n' "$rc"
test "$rc" -eq 2
cd "$ROOT"
printf '删除前确认：只删除 roomusic-drill-c6-app/tools 标签，不强制删除，不操作其他镜像。\n'
mapfile -t tags < <(docker image ls --format '{{.Repository}}:{{.Tag}}' | grep -E '^roomusic-drill-c6-(app|tools):' | sort -u)
for tag in "${tags[@]}"; do
  [[ "$tag" == roomusic-drill-c6-app:* || "$tag" == roomusic-drill-c6-tools:* ]]
done
printf '命令：docker image rm'; printf ' %q' "${tags[@]}"; printf '\n'
docker image rm "${tags[@]}"
[[ "$CLONE" == /tmp/roomusic-drill/clone ]]
rm -r -- "$CLONE"
printf '结果：通过；主仓库分支未变，临时 clone 与 c6 标签已清理。\n'
