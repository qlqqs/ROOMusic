#!/bin/sh
# 初始化生产部署的目录与共享数据库密码。
set -eu
umask 077
if [ "$(id -u)" -ne 0 ]; then
  echo '首次安装请使用 sudo sh production-init.sh。' >&2
  exit 1
fi
# 在部署目录执行；由 Compose 解析 dotenv，避免以 root 执行 .env 中的文本。
compose_file=${1:-compose.yaml}
env_file=${2:-.env}
# 先验证实际部署配置，再用同一 env 文件读取初始化参数；不调用 Docker daemon。
docker compose -f "$compose_file" --env-file "$env_file" config --quiet
configuration=$(docker compose -p roomusic-init-config -f - --env-file "$env_file" config --format json <<'YAML'
services: {}
x-roomusic-init:
  password: "${ROOMUSIC_DB_PASSWORD:-}"
  backup: "${ROOMUSIC_BACKUP_DIR:-./backups}"
YAML
)
configuration=$(printf '%s' "$configuration" | python3 -c '
import json, sys
values = json.load(sys.stdin)["x-roomusic-init"]
# Compose 导出配置时将美元符号转义为双美元符号，还原字面值。
values = {k: v.replace("$$", "$") for k, v in values.items()}
if any("\n" in v or "\r" in v for v in values.values()):
    sys.exit("数据库密码和备份目录必须是单行。")
print(values["password"])
print(values["backup"])
')
{
  IFS= read -r ROOMUSIC_DB_PASSWORD
  IFS= read -r backup_dir
} <<EOF
$configuration
EOF
unset configuration
mkdir -p ./secrets ./data "$backup_dir"
# PostgreSQL 降权后需要穿过挂载根目录，内部数据目录权限由其入口管理。
if [ ! -d ./postgres ]; then
  mkdir -m 0755 ./postgres
fi
password_file=./secrets/postgres_password
if [ -f "$password_file" ]; then
  password=$(cat "$password_file")
  if [ -z "$password" ]; then
    echo '数据库密码文件为空。' >&2
    exit 1
  fi
  if [ -n "${ROOMUSIC_DB_PASSWORD:-}" ] && [ "$ROOMUSIC_DB_PASSWORD" != "$password" ]; then
    echo 'ROOMUSIC_DB_PASSWORD 与已保存的数据库密码不一致。' >&2
    exit 1
  fi
else
  if [ -n "$(find ./postgres -mindepth 1 -maxdepth 1 -print -quit)" ]; then
    echo '数据库目录已有内容，请恢复 secrets/postgres_password。' >&2
    exit 1
  fi
  password=${ROOMUSIC_DB_PASSWORD:-}
  if [ -z "$password" ]; then
    password=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
  fi
  printf '%s' "$password" > "$password_file.tmp"
  mv "$password_file.tmp" "$password_file"
fi
chown 1000:1000 ./secrets "$password_file" ./data "$backup_dir"
chmod 0700 ./secrets ./data "$backup_dir"
chmod 0600 "$password_file"
echo '部署目录与数据库密码已就绪。'
