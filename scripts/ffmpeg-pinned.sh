#!/usr/bin/env bash
# 取出按摘要固定的静态 ffmpeg（mwader/static-ffmpeg 7.1.1），打印可执行文件的绝对路径到标准输出，其余信息写到标准错误。
# 测试、CI 与推荐的部署都用这个构建：把输出的路径设为 ROOMUSIC_FFMPEG_PATH（运行时）或 ROOMUSIC_TEST_FFMPEG（测试）。
# 项目不分发 ffmpeg；这个脚本只从固定摘要的镜像里取出二进制，缓存到 ${XDG_CACHE_HOME:-$HOME/.cache}/roomusic/ffmpeg-<摘要前 12 位>/ffmpeg，
# 已取出并通过版本校验时直接打印路径。取不到时以非零状态退出，调用方不得因此静默跳过派生测试。
#
#   scripts/ffmpeg-pinned.sh          # 取出（如需要）并打印路径
#   scripts/ffmpeg-pinned.sh --image  # 只打印固定的镜像引用（scripts/gen-playback-fixtures.sh 共用同一处定义）
#
# 升级镜像须重新核对派生素材与测试，并同步更新这里的摘要与版本前缀。
set -euo pipefail

FFMPEG_IMAGE="mwader/static-ffmpeg@sha256:11a44711684c0b9f754c047dcd64235b8b52deab251bd0e0a86f22faa160749c"
FFMPEG_VERSION_PREFIX="ffmpeg version 7.1.1"

if [[ "${1:-}" == "--image" ]]; then
  echo "$FFMPEG_IMAGE"
  exit 0
fi

DIGEST="${FFMPEG_IMAGE##*sha256:}"
CACHE_ROOT="${XDG_CACHE_HOME:-${HOME:?HOME 未设置，无法确定 ffmpeg 的缓存目录}/.cache}/roomusic"
TARGET_DIR="$CACHE_ROOT/ffmpeg-${DIGEST:0:12}"
TARGET="$TARGET_DIR/ffmpeg"

# 版本串必须以固定的版本前缀开头：不是这个构建就当作没有取到。
verified() {
  [[ -x "$1" ]] && [[ "$("$1" -hide_banner -version 2>/dev/null | head -n 1)" == "$FFMPEG_VERSION_PREFIX"* ]]
}

if verified "$TARGET"; then
  echo "$TARGET"
  exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "找不到 docker：无法取出固定版本的 ffmpeg；请安装 docker，或自行安装 ffmpeg 并设置 ROOMUSIC_FFMPEG_PATH" >&2
  exit 1
fi

mkdir -p "$TARGET_DIR"
container=""
staged="$(mktemp "$TARGET_DIR/ffmpeg.XXXXXX")"
cleanup() {
  rm -f "$staged"
  if [[ -n "$container" ]]; then
    docker rm -f "$container" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

echo "从 $FFMPEG_IMAGE 取出 ffmpeg……" >&2
# 镜像不含 shell，只有 /ffmpeg 与 /ffprobe：创建（不启动）容器后复制出文件。
container="$(docker create --network none "$FFMPEG_IMAGE")"
docker cp "$container:/ffmpeg" "$staged" >&2
chmod 0755 "$staged"
if ! verified "$staged"; then
  echo "取出的 ffmpeg 不是预期的 $FFMPEG_VERSION_PREFIX，已放弃" >&2
  exit 1
fi
mv -f "$staged" "$TARGET"
echo "$TARGET"
