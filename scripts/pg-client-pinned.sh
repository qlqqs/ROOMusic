#!/usr/bin/env bash
# 为开发与集成测试生成固定 PostgreSQL 镜像的工具包装；只用宿主机网络，不创建 Docker 网络。
set -euo pipefail
PG_IMAGE='postgres:18.1-alpine3.23@sha256:aa6eb304ddb6dd26df23d05db4e5cb05af8951cda3e0dc57731b771e0ef4ab29'
if [[ "${1:-}" == '--image' ]]; then printf '%s\n' "$PG_IMAGE"; exit 0; fi
command -v docker >/dev/null || { echo '找不到 docker，无法准备 PostgreSQL 工具' >&2; exit 1; }
DIGEST="${PG_IMAGE##*sha256:}"
CACHE_ROOT="${XDG_CACHE_HOME:-${HOME:?HOME 未设置}/.cache}/roomusic"
mkdir -p "$CACHE_ROOT/pg-client-${DIGEST:0:12}"
TARGET_DIR="$(cd "$CACHE_ROOT/pg-client-${DIGEST:0:12}" && pwd -P)"
# 先拉取并验证，失败即中止；工具调用时不需要依赖父进程的 Docker 配置环境。
if ! docker image inspect "$PG_IMAGE" >/dev/null 2>&1; then docker pull "$PG_IMAGE" >&2; fi
docker run --rm --network host "$PG_IMAGE" pg_dump --version | grep -q '^pg_dump (PostgreSQL) 18\.'
for tool in pg_dump pg_restore; do
  staged="$(mktemp "$TARGET_DIR/.${tool}.XXXXXX")"
  cat >"$staged" <<SCRIPT
#!/bin/sh
exec docker run --rm -i --network host -e PGPASSWORD -e PGAPPNAME -e PGCONNECT_TIMEOUT '$PG_IMAGE' '$tool' "\$@"
SCRIPT
  chmod 0755 "$staged"
  mv -f "$staged" "$TARGET_DIR/$tool"
done
printf '%s\n' "$TARGET_DIR"
