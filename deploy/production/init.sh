#!/bin/sh
# 初始化生产部署的目录与共享数据库密码。
set -eu
umask 077
case "${ROOMUSIC_DB_PASSWORD:-}" in
  *"$(printf '\r')"*|*'
'*) echo '数据库密码必须是单行。' >&2; exit 1 ;;
esac
mkdir -p /secrets /data /backups
password_file=/secrets/postgres_password
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
  if [ -n "$(find /postgres -mindepth 1 -maxdepth 1 -print -quit)" ]; then
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
chown 1000:1000 /secrets "$password_file" /data /backups
chmod 0700 /secrets /data /backups
chmod 0600 "$password_file"
echo '部署目录与数据库密码已就绪。'
