#!/bin/sh
# 开机时容器顺序不保证；应用只在启动时连接一次数据库，因此先等数据库接受连接。
set -eu
waited=0
until pg_isready -q -d "$ROOMUSIC_DATABASE_URL" -t 3; do
  if [ $((waited % 30)) -eq 0 ]; then
    printf '{"level":"INFO","event":"platform.database.waiting","module":"platform","waited_seconds":%s}\n' "$waited"
  fi
  sleep 2
  waited=$((waited + 2))
done
exec /app/roomusic "$@"
