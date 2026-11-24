#!/usr/bin/env bash
set -Eeuo pipefail
trap 'printf "生产 Compose 静态校验失败（第 %s 行）\n" "$LINENO" >&2' ERR

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
umask 077
mkdir -p "$ROOT_DIR/temp"
work=$(mktemp -d "$ROOT_DIR/temp/compose-config.XXXXXX")
trap 'rm -rf -- "$work"' EXIT

# 在独立目录中仅使用假值；不会读取工作区或部署目录的配置，也不创建容器与网络。
cp "$ROOT_DIR/compose.prod.yaml" "$work/compose.yaml"
cp "$ROOT_DIR/deploy/production/compose.bridge.yaml" "$work/compose.bridge.yaml"
printf '%s\n' 'ROOMUSIC_MUSIC_DIR=./music' > "$work/required.env"
: > "$work/empty.env"
cp "$work/required.env" "$work/bind.env"
printf '%s\n' 'ROOMUSIC_BACKUP_DIR=/srv/x' >> "$work/bind.env"

compose() {
  local env_file=$1
  shift
  if [[ -n "$env_file" ]]; then set -- --env-file "$work/$env_file" "$@"; fi
  (cd "$work" && env -i PATH="$PATH" HOME="$HOME" \
    docker compose "$@")
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
expect_missing empty.env ROOMUSIC_MUSIC_DIR

# 完整 config 只进入管道；断言只读取非敏感字段，不向终端打印配置。
# 部分 Compose 版本会为未配置的 ipam 输出空对象，与字段缺失等价。
compose required.env config --format json | jq -e '
  .name == "roomusic-server" and
  (.services | keys) == ["app", "postgres"] and
  .services.app.user == "1000:1000" and
  .services.app.image == "ghcr.io/qlqqs/roomusic:latest" and
  .services.app.container_name == "roomusic-app" and
  .services.postgres.container_name == "roomusic-postgres" and
  ((.volumes // {}) | length) == 0 and
  ([.services[].volumes[] | .type == "bind"] | all) and
  .services.app.depends_on.postgres.condition == "service_healthy" and
  .services.app.environment.ROOMUSIC_FPCALC_PATH == "/usr/local/bin/fpcalc" and
  .services.app.environment.ROOMUSIC_DB_PASSWORD_FILE == "/run/secrets/postgres_password" and
  .services.postgres.environment.POSTGRES_PASSWORD_FILE == "/run/secrets/postgres_password" and
  .services.app.read_only and
  (.services.app.security_opt | index("no-new-privileges:true") != null) and
  ([.services.app.volumes[] | select(.target == "/run/secrets" or .target == "/music") | .read_only] == [true, true]) and
  (.services.app.cap_drop | index("ALL") != null) and
  .services.app.network_mode == "host" and
  (.services.app.ports | length) == 0 and
  .services.app.environment.ROOMUSIC_HTTP_ADDR == "0.0.0.0:8090" and
  .services.app.environment.ROOMUSIC_DATABASE_URL == "postgres://roomusic@127.0.0.1:55439/roomusic?sslmode=disable" and
  .services.postgres.ports[0].host_ip == "127.0.0.1" and
  .services.postgres.ports[0].published == "55439" and
  .services.postgres.ports[0].target == 5432 and
  (.networks.roomusic.ipam // {}) == {} and
  ([.services.app.volumes[] | select(.target == "/backups") | .source | endswith("/backups")] | all) and
  ([.services.postgres.volumes[] | select(.target == "/var/lib/postgresql") | .source | endswith("/postgres")] | all) and
  ([.services[] | has("pull_policy")] | any | not)
' > /dev/null
# 将两份配置的挂载路径对齐到同一临时目录，比较两个服务和所有网络、卷等字段。
sed -e 's|/path/to/roomusic/|./|g' -e 's|/path/to/music|./music|g' \
  "$ROOT_DIR/deploy/nas/compose.yaml" > "$work/nas.yaml"
compose required.env -f compose.yaml config --format json | jq -S 'del(."x-logging", ."x-postgres-image")' > "$work/main.json"
compose '' -f nas.yaml config --format json | jq -S . > "$work/nas.json"
diff -u "$work/main.json" "$work/nas.json"

printf '%s\n' 'ROOMUSIC_IMAGE=example/roomusic' 'PUID=1026' 'PGID=100' >> "$work/bind.env"
compose bind.env config --format json | jq -e '
  .services.app.image == "example/roomusic:latest" and
  .services.app.user == "1026:100" and
  .services.postgres.environment.PUID == "1026" and
  .services.postgres.environment.PGID == "100" and
  ([.services.app.volumes[] | select(.target == "/backups") | .source] == ["/srv/x"]) and
  ([.services.postgres.volumes[] | select(.target == "/prep/backups") | .source] == ["/srv/x"])
' > /dev/null

printf '%s\n' 'ROOMUSIC_APP_CONTAINER_NAME=test-app' 'ROOMUSIC_POSTGRES_CONTAINER_NAME=test-postgres' >> "$work/bind.env"
compose bind.env config --format json | jq -e '
  .services.app.container_name == "test-app" and
  .services.postgres.container_name == "test-postgres" and
  ((.services.postgres.depends_on // {}) | length) == 0
' > /dev/null

printf '%s\n' 'ROOMUSIC_HTTP_PORT=19090' 'ROOMUSIC_DB_PORT=55440' 'ROOMUSIC_LAN_URL=http://192.168.1.2:19090' >> "$work/bind.env"
compose bind.env config --format json | jq -e '
  .services.app.environment.ROOMUSIC_HTTP_ADDR == "0.0.0.0:19090" and
  .services.app.environment.ROOMUSIC_DATABASE_URL == "postgres://roomusic@127.0.0.1:55440/roomusic?sslmode=disable" and
  .services.postgres.ports[0].published == "55440"
' > /dev/null
compose bind.env -f compose.yaml -f compose.bridge.yaml config --format json | jq -e '
  .services.app.network_mode == null and
  (.services.app.networks | has("roomusic")) and
  (.services.postgres.networks | has("roomusic")) and
  (.services.postgres.ports | length) == 0 and
  (.services.app.ports | length) == 1 and
  .services.app.ports[0].published == "19090" and
  .services.app.ports[0].host_ip == "0.0.0.0" and
  .services.app.ports[0].target == 8080 and
  .services.app.environment.ROOMUSIC_LAN_URL == "http://192.168.1.2:19090" and
  .services.app.environment.ROOMUSIC_HTTP_ADDR == "0.0.0.0:8080" and
  .services.app.environment.ROOMUSIC_DATABASE_URL == "postgres://roomusic@postgres:5432/roomusic?sslmode=disable"
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
