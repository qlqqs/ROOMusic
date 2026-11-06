#!/usr/bin/env bash
set -Eeuo pipefail
trap 'printf "生产 Compose 静态校验失败（第 %s 行）\n" "$LINENO" >&2' ERR

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
umask 077
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT

# 在独立目录中仅使用假值；不会读取工作区或部署目录的配置，也不创建容器与网络。
cp "$ROOT_DIR/compose.prod.yaml" "$work/compose.yaml"
printf '%s\n' 'ROOMUSIC_DB_PASSWORD=ci-placeholder' 'ROOMUSIC_MUSIC_DIR=/srv/music' > "$work/required.env"
printf '%s\n' 'ROOMUSIC_MUSIC_DIR=/srv/music' > "$work/no-password.env"
printf '%s\n' 'ROOMUSIC_DB_PASSWORD=ci-placeholder' > "$work/no-music.env"
cp "$work/required.env" "$work/bind.env"
printf '%s\n' 'ROOMUSIC_BACKUP_DIR=/srv/x' >> "$work/bind.env"

compose() {
  local env_file=$1
  shift
  (cd "$work" && env -i PATH="$PATH" HOME="$HOME" \
    docker compose --env-file "$work/$env_file" "$@")
}

expect_missing() {
  local env_file=$1 variable=$2
  if compose "$env_file" config --quiet > /dev/null 2> "$work/error"; then
    printf '缺少 %s 时应拒绝配置\n' "$variable" >&2
    exit 1
  fi
  grep -qF "请在 .env 里填写 $variable" "$work/error"
}

compose required.env config --quiet
expect_missing no-password.env ROOMUSIC_DB_PASSWORD
expect_missing no-music.env ROOMUSIC_MUSIC_DIR

# 完整 config 只进入管道；断言只读取非敏感字段，不向终端打印配置。
# 部分 Compose 版本会为未配置的 ipam 输出空对象，与字段缺失等价。
compose required.env config --format json | jq -e '
  .name == "roomusic-server" and
  (.services | keys) == ["app", "postgres"] and
  .services.app.image == "ghcr.io/qlqqs/roomusic:latest" and
  (.services.app.ports | length) == 1 and
  .services.app.ports[0].published == "8090" and
  .services.app.ports[0].target == 8080 and
  .services.app.ports[0].host_ip == "0.0.0.0" and
  (.networks.roomusic.ipam // {}) == {} and
  ([.services.app.volumes[] | select(.target == "/backups") | {type, source}]
    == [{"type": "volume", "source": "backups"}]) and
  ([.services[] | has("pull_policy")] | any | not)
' > /dev/null
compose bind.env config --format json | jq -e '
  [.services.app.volumes[] | select(.target == "/backups") | {type, source}]
    == [{"type": "bind", "source": "/srv/x"}]
' > /dev/null

cat > "$work/compose.override.yaml" <<'YAML'
networks:
  roomusic:
    ipam:
      config:
        - subnet: 10.203.51.0/24
YAML
compose required.env config --format json | jq -e '
  .networks.roomusic.ipam.config[0].subnet == "10.203.51.0/24"
' > /dev/null

printf '%s\n' '生产 Compose 静态校验通过'
