#!/usr/bin/env bash
# 生产镜像、工具脚本及启动配置只来自所选提交，不读取工作区源码。
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOMUSIC_PROD_ENV_FILE:-$ROOT_DIR/.env.prod}"
cd "$ROOT_DIR"
# 防止开发 shell 的配置覆盖生产文件；先保存测试用 env 文件路径。
while IFS= read -r name; do
  case "$name" in ROOMUSIC_*|COMPOSE_*) unset "$name" ;; esac
done < <(compgen -v)

usage() {
  cat <<'HELP'
用法：scripts/production.sh [--dry-run] <命令> [参数]
  up [--ref <提交>]                            构建、备份、启动（默认 main）
  stop                                         只停容器，不删数据卷
  status                                       状态、运行版本与地址
  logs                                         跟随最近 200 行日志
  backup                                       立即备份
  reset-password <用户名>                      重置生产密码
  build-image [--ref <提交>] [--prefix <前缀>]  从 Git 提交构建应用和 tools 镜像
  --help                                       显示帮助
--dry-run 只查询并打印命令，不构建、不启动、不写文件。
HELP
}
fail() { echo "$*" >&2; exit 1; }
misuse() { echo "$*" >&2; exit 2; }
dry_run=false
if [[ "${1:-}" == --dry-run ]]; then dry_run=true; shift; fi
command_name=${1:---help}
shift "$(( $# > 0 ? 1 : 0 ))"
ref=main
prefix=roomusic-prod
username=''
case "$command_name" in
  -h|--help|help) usage; exit 0 ;;
  up|build-image)
    while (( $# )); do
      case "$1" in
        --ref) (( $# >= 2 )) && [[ -n "$2" ]] || misuse '--ref 缺少提交'; ref=$2; shift 2 ;;
        --prefix)
          [[ "$command_name" == build-image ]] || misuse '只有 build-image 支持 --prefix'
          (( $# >= 2 )) && [[ "$2" =~ ^[a-z0-9]+([._-][a-z0-9]+)*$ ]] || misuse '镜像前缀必须是小写字母、数字和分隔符'
          prefix=$2; shift 2 ;;
        *) misuse "未知参数：$1" ;;
      esac
    done ;;
  reset-password)
    [[ $# == 1 && -n "$1" ]] || misuse '用法：make prod-reset-password U=<用户名>'
    username=$1 ;;
  stop|status|logs|backup) (( $# == 0 )) || misuse '该命令不接受额外参数' ;;
  *) usage >&2; misuse "未知命令：$command_name" ;;
esac

print_command() { printf '+ '; printf '%q ' "$@"; printf '\n'; }
run() {
  if "$dry_run"; then print_command "$@"; else "$@"; fi
}
tmp=''
cleanup() { [[ -z "$tmp" ]] || rm -rf -- "$tmp"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# 执行错误统一为 1；参数错误由上面的显式 exit 2 返回。
trap 'exit 1' ERR

env_value() {
  local key=$1 line value=''
  while IFS= read -r line || [[ -n "$line" ]]; do
    line=${line%$'\r'}
    if [[ "$line" == "$key="* ]]; then value=${line#*=}; fi
  done < "$ENV_FILE"
  if [[ "$value" == \"*\" || "$value" == \'*\' ]]; then value=${value:1:${#value}-2}; fi
  [[ "$value" != *'$'* && "$value" != *'`'* ]] || fail "$key 必须使用字面量，不支持变量替换"
  printf '%s' "$value"
}
load_env() {
  [[ -f "$ENV_FILE" ]] || fail '缺少生产配置：请先从 .env.prod.example 复制为 .env.prod'
  local mode
  mode=$(stat -c %a "$ENV_FILE")
  if (( (8#$mode & 0177) != 0 )); then echo '提示：建议 chmod 600 保护生产配置。' >&2; fi
  http_port=$(env_value ROOMUSIC_HTTP_PORT)
  [[ "$http_port" =~ ^[0-9]{1,5}$ ]] && ((10#$http_port > 0 && 10#$http_port <= 65535)) || fail 'ROOMUSIC_HTTP_PORT 必须是 1–65535'
}
preflight_directories() {
  local key value
  for key in ROOMUSIC_MUSIC_DIR ROOMUSIC_BACKUP_DIR; do
    value=$(env_value "$key")
    [[ "$value" == /* && -d "$value" ]] || fail "$key 必须指向已存在的绝对目录"
  done
}
compose_file="$ROOT_DIR/deploy/production/compose.yaml"
compose() { run docker compose -p roomusic-prod -f "$compose_file" --env-file "$ENV_FILE" "$@"; }
app_id() { docker ps -q --filter label=com.docker.compose.project=roomusic-prod --filter label=com.docker.compose.service=app; }
show_version() {
  local id revision
  id=$(app_id)
  if [[ -n "$id" ]]; then
    revision=$(docker inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$id")
    echo "当前运行：${revision:0:12}"
  else
    echo '生产没有在运行，先 make prod'
  fi
}
require_running() { [[ -n "$(app_id)" ]] || fail '生产没有在运行，先 make prod'; }
resolve_ref() {
  sha=$(git rev-parse --verify --quiet --end-of-options "$ref^{commit}") || misuse "找不到提交：$ref"
  short=${sha:0:12}
  echo "将部署：$short $(git show -s --format=%s "$sha")"
  show_version
  if ! git merge-base --is-ancestor "$sha" main; then echo '注意：这个提交不在 main 上'; fi
  if [[ -n "$(git status --porcelain)" || "$(git symbolic-ref --short -q HEAD || true)" != main ]]; then
    echo '工作区的未提交改动和当前分支不会进入镜像'
  fi
  app_image="$prefix-app:$short"
  tools_image="$prefix-tools:$short"
}
ensure_tmp() {
  if [[ -z "$tmp" ]]; then tmp=$(mktemp -d "${TMPDIR:-/tmp}/roomusic-build.XXXXXX"); fi
}
image_matches() {
  local revision
  revision=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$1" 2>/dev/null) || return 1
  [[ "$revision" == "$sha" ]]
}
build_images() {
  local build_dir
  if image_matches "$app_image" && image_matches "$tools_image"; then
    echo '同一提交的镜像已存在，跳过构建。'
  else
    if "$dry_run"; then
      build_dir="${TMPDIR:-/tmp}/roomusic-build.DRY-RUN"
      print_command mktemp -d "${TMPDIR:-/tmp}/roomusic-build.XXXXXX"
      printf '+ git archive --format=tar %q | tar -x -C %q\n' "$sha" "$build_dir"
    else
      ensure_tmp
      build_dir=$tmp
      git archive --format=tar "$sha" | tar -x -C "$build_dir"
      [[ -f "$build_dir/backend/cmd/roomusic/web/index.html" ]] || fail '提交缺少已构建的内嵌前端'
      [[ -f "$build_dir/deploy/production/Dockerfile.dockerignore" && -f "$build_dir/deploy/production/Dockerfile" ]] || fail '提交缺少生产部署文件；请先将部署打包改动提交到 main'
    fi
    # 两个构建都成功后才移动 current，避免第二个构建失败留下半套新版本。
    # Docker 预定义代理参数只传变量名，不输出地址，也不写入镜像配置。
    local proxy_args=(--build-arg HTTP_PROXY --build-arg HTTPS_PROXY --build-arg NO_PROXY --build-arg http_proxy --build-arg https_proxy --build-arg no_proxy)
    run docker build "${proxy_args[@]}" -f "$build_dir/deploy/production/Dockerfile" --target tools --label "org.opencontainers.image.revision=$sha" -t "$tools_image" "$build_dir"
    run docker build "${proxy_args[@]}" -f "$build_dir/deploy/production/Dockerfile" --label "org.opencontainers.image.revision=$sha" -t "$app_image" "$build_dir"
  fi
  run docker tag "$tools_image" "$prefix-tools:current"
  run docker tag "$app_image" "$prefix-app:current"
}

case "$command_name" in
  build-image) resolve_ref; build_images ;;
  up)
    load_env
    preflight_directories
    resolve_ref
    git cat-file -e "$sha:deploy/production/compose.yaml" 2>/dev/null || fail '提交缺少生产 Compose；请先提交部署打包改动'
    build_images
    if "$dry_run"; then
      compose_file="${TMPDIR:-/tmp}/roomusic-build.DRY-RUN/compose.yaml"
      printf '+ git show %q > %q\n' "$sha:deploy/production/compose.yaml" "$compose_file"
    else
      ensure_tmp
      compose_file="$tmp/compose.yaml"
      git show "$sha:deploy/production/compose.yaml" > "$compose_file"
    fi
    export ROOMUSIC_IMAGE="$app_image" ROOMUSIC_TOOLS_IMAGE="$tools_image"
    if "$dry_run"; then print_command export "ROOMUSIC_IMAGE=$app_image" "ROOMUSIC_TOOLS_IMAGE=$tools_image"; fi
    compose config --quiet
    compose up -d --wait postgres
    # 数据库即使为空也备份；失败即退出，不执行后面的换版本。
    compose run --rm --no-deps -T backup now
    compose up -d --wait --wait-timeout 300
    compose ps
    echo "访问地址：http://192.168.50.206:$http_port"
    ;;
  stop) load_env; compose stop ;;
  status) load_env; compose ps; show_version; echo "访问地址：http://192.168.50.206:$http_port" ;;
  logs) load_env; compose logs -f --tail 200 ;;
  backup) load_env; require_running; compose exec backup /bin/sh /opt/roomusic/backup.sh now ;;
  reset-password) load_env; require_running; compose exec app /app/roomusic reset-password "$username" ;;
esac
