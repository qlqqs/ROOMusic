#!/usr/bin/env bash
# 用假的 docker 检查隔离启动器的拒绝路径；不访问 Docker 守护进程或数据库。
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEMP_DIR="$(mktemp -d)"
trap 'rm -rf -- "$TEMP_DIR"' EXIT
mkdir "$TEMP_DIR/bin"
cat > "$TEMP_DIR/bin/docker" <<'DOCKER'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$GUARD_CALLS"
[[ "$*" == info ]] && exit 0
case "$*" in
  "ps -aq --filter label=com.docker.compose.project=$GUARD_PROJECT") resource=container ;;
  "volume ls -q --filter label=com.docker.compose.project=$GUARD_PROJECT") resource=volume ;;
  "network ls -q --filter label=com.docker.compose.project=$GUARD_PROJECT") resource=network ;;
  *) echo '不应执行资源创建或清理命令' >&2; exit 99 ;;
esac
if [[ "$GUARD_RESOURCE" == "$resource" ]]; then
  [[ "$GUARD_MODE" == error ]] && exit 42
  echo existing-resource
fi
exit 0
DOCKER
chmod +x "$TEMP_DIR/bin/docker"
export PATH="$TEMP_DIR/bin:$PATH" TMPDIR="$TEMP_DIR" GUARD_CALLS="$TEMP_DIR/calls"
# 两条实现线都覆盖普通入口与扫描反馈专用入口。
for GUARD_PROJECT in roomusic-test-m5p roomusic-test-m5f; do
  export GUARD_PROJECT
  for entry in playback scan-feedback; do
    args=()
    [[ "$entry" != scan-feedback ]] || args+=(--scan-feedback-only)
    for GUARD_RESOURCE in container volume network; do
      for GUARD_MODE in error occupied; do
        export GUARD_RESOURCE GUARD_MODE
        : > "$GUARD_CALLS"
        result=0
        ROOMUSIC_TEST_COMPOSE_PROJECT="$GUARD_PROJECT" bash "$ROOT_DIR/scripts/playback-e2e.sh" "${args[@]}" > "$TEMP_DIR/output" 2>&1 || result=$?
        [[ "$result" == 1 ]]
        if [[ "$GUARD_MODE" == error ]]; then
          grep -q '无法检查已有测试' "$TEMP_DIR/output"
        else
          grep -q '拒绝接管历史测试资源' "$TEMP_DIR/output"
        fi
        ! grep -q '不应执行' "$TEMP_DIR/output"
        # 精确核对调用序列，连被吞掉错误的创建/清理尝试也不能漏过。
        {
          echo info
          echo "ps -aq --filter label=com.docker.compose.project=$GUARD_PROJECT"
          if [[ "$GUARD_MODE" != error || "$GUARD_RESOURCE" != container ]]; then
            echo "volume ls -q --filter label=com.docker.compose.project=$GUARD_PROJECT"
            if [[ "$GUARD_MODE" != error || "$GUARD_RESOURCE" != volume ]]; then
              echo "network ls -q --filter label=com.docker.compose.project=$GUARD_PROJECT"
            fi
          fi
        } > "$TEMP_DIR/expected-calls"
        diff -u "$TEMP_DIR/expected-calls" "$GUARD_CALLS"
        printf 'PASS %s %s %s %s：拒绝启动，未创建或清理资源\n' "$GUARD_PROJECT" "$entry" "$GUARD_RESOURCE" "$GUARD_MODE"
      done
    done
  done
done
for entry in playback scan-feedback; do
  args=()
  [[ "$entry" != scan-feedback ]] || args+=(--scan-feedback-only)
  : > "$GUARD_CALLS"
  result=0
  ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic bash "$ROOT_DIR/scripts/playback-e2e.sh" "${args[@]}" > "$TEMP_DIR/output" 2>&1 || result=$?
  [[ "$result" == 1 && ! -s "$GUARD_CALLS" ]]
  grep -q '测试项目必须是' "$TEMP_DIR/output"
  printf 'PASS 生产项目名 %s：在调用 Docker 前拒绝\n' "$entry"
done
