#!/usr/bin/env bash
# 真实音乐库 Smoke 的显式、失败即关闭入口。
#
# 本入口负责预检、镜像构建和隔离。REST 工作流位于显式提供的 runner 中，
# 便于在默认不打开真实音乐库的情况下测试。
# 源音乐库不会被用作工作目录，也不会被写入。
set -Eeuo pipefail
umask 077

readonly ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly COMPOSE_FILE="${ROOMUSIC_SMOKE_COMPOSE_FILE:-$ROOT_DIR/deploy/smoke/compose.yaml}"
readonly EXPECTED_V0_SHA256="fe25388328698b26991ea3b59a14406a155eb92d578a9be2a68d67d331ecf97d"
readonly V0_ADAPTER_SOURCE="$ROOT_DIR/deploy/smoke/v0-exporter/main.go"
readonly PROJECT_PREFIX="roomusic-smoke-"

music_root_arg=""
v0_archive_arg=""
runner_arg=""
dry_run=0
keep_artifacts=0
tmp_root=""
tmp_parent=""
tmp_marker=""
project=""
project_marker=""
compose_env_file=""
compose_started=0
cleanup_done=0
v0_source=""
current_image=""
v0_image=""
comparator=""
v0_adapter_sha256=""
current_code_sha256=""
report_temporary=""

usage() {
  cat <<'EOF'
用法：
  ROOMUSIC_REAL_LIBRARY_SMOKE=1 scripts/real-library-smoke.sh \
    --music-root /绝对路径/music \
    --v0-archive /绝对路径/ROOMusic-migration.tar.gz \
    [--dry-run|--preflight] [--runner /绝对路径/编排器]

说明：
  --dry-run/--preflight 只执行参数、路径、归档哈希和 Compose 隔离预检，不连接 Docker，
  也不读取真实音乐文件。
  --runner 仅用于注入经过审计的编排器；未提供时使用仓库内置 runner。
EOF
}

redacted_error() {
  printf 'real-library-smoke: %s\n' "$1" >&2
  exit 2
}

die() {
  redacted_error "$1"
}

has_newline() {
  [[ "$1" == *$'\n'* || "$1" == *$'\r'* ]]
}

is_absolute() {
  [[ "$1" == /* ]]
}

is_same_or_descendant() {
  local parent="$1"
  local child="$2"
  [[ "$child" == "$parent" || "$child" == "$parent"/* ]]
}

is_same_or_ancestor() {
  local parent="$1"
  local child="$2"
  [[ "$parent" == "$child" || "$parent" == "$child"/* ]]
}

path_overlaps() {
  local left="$1"
  local right="$2"
  is_same_or_descendant "$left" "$right" || is_same_or_descendant "$right" "$left"
}

canonical_dir() {
  local supplied="$1"
  is_absolute "$supplied" || die "音乐根必须是绝对路径"
  has_newline "$supplied" && die "音乐根包含不允许的换行字符"
  [[ -d "$supplied" ]] || die "音乐根不存在或不是目录"
  [[ ! -L "$supplied" ]] || die "音乐根不能是符号链接"
  realpath -e -- "$supplied" 2>/dev/null || die "音乐根无法规范化"
}

canonical_archive() {
  local supplied="$1"
  is_absolute "$supplied" || die "V0 归档必须是绝对路径"
  has_newline "$supplied" && die "V0 归档包含不允许的换行字符"
  [[ -f "$supplied" ]] || die "V0 归档不存在或不是普通文件"
  [[ ! -L "$supplied" ]] || die "V0 归档不能是符号链接"
  realpath -e -- "$supplied" 2>/dev/null || die "V0 归档无法规范化"
}

random_hex() {
  local bytes="$1"
  openssl rand -hex "$bytes" 2>/dev/null || die "无法生成隔离随机标识"
}

allocate_ports() {
  python3 - <<'PY'
import socket

sockets = []
try:
    for _ in range(2):
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(("127.0.0.1", 0))
        sockets.append(sock)
    print("\n".join(str(sock.getsockname()[1]) for sock in sockets))
finally:
    for sock in sockets:
        sock.close()
PY
}

write_env_value() {
  local key="$1"
  local value="$2"
  # Compose dotenv 值仅允许生成的安全值，或不含插值元字符的规范路径。
  if [[ "$value" == *$'\n'* || "$value" == *$'\r'* || "$value" == *"'"* || "$value" == *'"'* || "$value" == *'$'* || "$value" == *'`'* ]]; then
    die "生成的 Compose 环境值包含不安全字符"
  fi
  printf '%s=%s\n' "$key" "$value" >> "$compose_env_file"
}

compose_base_args() {
  printf '%s\n' \
    --ansi never \
    --progress plain \
    --env-file "$compose_env_file" \
    --file "$COMPOSE_FILE" \
    --project-name "$project"
}

compose() {
  local -a args
  mapfile -t args < <(compose_base_args)
  # COMPOSE_DISABLE_ENV_FILE 阻止隐式发现 .env；上面的显式 --env-file
  # 是 Compose 唯一接收的配置文件。
  env \
    -u COMPOSE_PROJECT_NAME \
    -u COMPOSE_FILE \
    -u COMPOSE_PROFILES \
    COMPOSE_DISABLE_ENV_FILE=1 \
    docker compose "${args[@]}" "$@"
}

docker_build() {
  local dockerfile="$1"
  local image="$2"
  local context="$3"
  local log_file="$4"
  local variable
  local -a proxy_args=()

  # Docker 将这些名称视为预定义代理构建参数，不会把值写入镜像历史或缓存元数据。
  # 此处只传变量名，代理地址不会进入脚本、命令行参数或日志。
  for variable in http_proxy https_proxy all_proxy no_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY; do
    if [[ -n "${!variable:-}" ]]; then
      proxy_args+=(--build-arg "$variable")
    fi
  done

  docker build \
    --file "$dockerfile" \
    --tag "$image" \
    "${proxy_args[@]}" \
    "$context" >"$log_file" 2>&1
}

current_source_digest() {
  python3 - "$ROOT_DIR/backend" "$ROOT_DIR/deploy/smoke/current.Dockerfile" <<'PY'
import hashlib
import os
import pathlib
import stat
import sys

backend = pathlib.Path(sys.argv[1])
dockerfile = pathlib.Path(sys.argv[2])
if not backend.is_dir() or backend.is_symlink() or not dockerfile.is_file() or dockerfile.is_symlink():
    raise SystemExit(1)

inputs = [(dockerfile, "deploy/smoke/current.Dockerfile")]
for path in backend.rglob("*"):
    info = path.lstat()
    if stat.S_ISDIR(info.st_mode):
        if path.is_symlink():
            raise SystemExit(1)
        continue
    if not stat.S_ISREG(info.st_mode) or path.is_symlink():
        raise SystemExit(1)
    inputs.append((path, "backend/" + path.relative_to(backend).as_posix()))

digest = hashlib.sha256()
for path, relative in sorted(inputs, key=lambda item: item[1]):
    info = path.stat()
    content = hashlib.sha256()
    descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    try:
        checked = os.fstat(descriptor)
        if not stat.S_ISREG(checked.st_mode) or checked.st_size != info.st_size:
            raise OSError("build input changed")
        while True:
            block = os.read(descriptor, 1024 * 1024)
            if not block:
                break
            content.update(block)
    finally:
        os.close(descriptor)
    digest.update(b"file\0")
    digest.update(relative.encode("utf-8", "surrogateescape"))
    digest.update(b"\0")
    digest.update(oct(stat.S_IMODE(info.st_mode)).encode("ascii"))
    digest.update(b"\0")
    digest.update(str(info.st_size).encode("ascii"))
    digest.update(b"\0")
    digest.update(content.hexdigest().encode("ascii"))
    digest.update(b"\n")
print(digest.hexdigest())
PY
}

install_v0_adapter() {
  local target_dir="$v0_source/ROOMusic/cmd/roomusic-smoke-exporter"
  local target="$target_dir/main.go"
  local copied_sha256

  [[ -f "$V0_ADAPTER_SOURCE" && ! -L "$V0_ADAPTER_SOURCE" ]] || die "V0 standalone adapter 源文件无效"
  [[ ! -e "$target_dir" && ! -L "$target_dir" ]] || die "V0 临时源码中已存在 adapter 目标"
  v0_adapter_sha256="$(sha256sum -- "$V0_ADAPTER_SOURCE" 2>/dev/null || true)"
  v0_adapter_sha256="${v0_adapter_sha256%% *}"
  [[ "$v0_adapter_sha256" =~ ^[0-9a-f]{64}$ ]] || die "V0 standalone adapter 摘要无效"

  mkdir -m 755 -- "$target_dir" || die "无法创建 V0 standalone adapter 目录"
  install -m 0644 -- "$V0_ADAPTER_SOURCE" "$target" || die "无法安装 V0 standalone adapter"
  copied_sha256="$(sha256sum -- "$target" 2>/dev/null || true)"
  copied_sha256="${copied_sha256%% *}"
  [[ "$copied_sha256" == "$v0_adapter_sha256" ]] || die "V0 standalone adapter 复制后摘要不匹配"
}

validate_v0_isolation() {
  local v0_id container_inspect
  v0_id="$(compose ps --all -q v0 2>/dev/null || true)"
  [[ "$v0_id" =~ ^[0-9a-f]{64}$ ]] || die "无法确认 V0 exporter 容器身份"

  container_inspect="$tmp_root/v0-container-inspect.json"
  docker inspect "$v0_id" > "$container_inspect" 2>/dev/null || die "无法读取 V0 exporter 容器配置"
  chmod 600 -- "$container_inspect"

  python3 - \
    "$container_inspect" "$music_root" "$tmp_root/data/v0" "$v0_id" <<'PY' \
    >/dev/null 2>&1 || die "V0 exporter 隔离合同校验失败"
import json
import os
import sys

container_path, music_root, output_root, identifier = sys.argv[1:]
with open(container_path, encoding="utf-8") as handle:
    containers = json.load(handle)
if len(containers) != 1 or containers[0].get("Id") != identifier:
    raise ValueError("container identity mismatch")
container = containers[0]

mounts = {item.get("Destination"): item for item in container.get("Mounts", [])}
music = mounts.get("/music")
output = mounts.get("/output")
if music is None or music.get("RW") is not False:
    raise ValueError("music mount is not read-only")
if os.path.realpath(music.get("Source", "")) != os.path.realpath(music_root):
    raise ValueError("music mount source mismatch")
if output is None or output.get("RW") is not True:
    raise ValueError("output mount is not writable")
if os.path.realpath(output.get("Source", "")) != os.path.realpath(output_root):
    raise ValueError("output mount source mismatch")

host_config = container.get("HostConfig", {})
if host_config.get("ReadonlyRootfs") is not True:
    raise ValueError("root filesystem is writable")
if host_config.get("NetworkMode") != "none":
    raise ValueError("network is enabled")
if host_config.get("PortBindings") not in (None, {}):
    raise ValueError("ports are published")
if "ALL" not in (host_config.get("CapDrop") or []):
    raise ValueError("capabilities were not dropped")
if "no-new-privileges:true" not in (host_config.get("SecurityOpt") or []):
    raise ValueError("no-new-privileges is missing")

environment = container.get("Config", {}).get("Env") or []
proxy_names = {"http_proxy", "https_proxy", "all_proxy", "no_proxy"}
if any(item.partition("=")[0].lower() in proxy_names for item in environment):
    raise ValueError("proxy leaked into runtime")
if container.get("NetworkSettings", {}).get("Ports") not in (None, {}):
    raise ValueError("runtime ports are present")
PY

  unlink -- "$container_inspect"
}

validate_cleanup_target() {
  [[ -n "$project" && "$project" =~ ^roomusic-smoke-[a-z0-9-]+$ ]] || return 1
  [[ "$project" != "roomusic" && "$project" != "roomusic-test" ]] || return 1
  [[ -n "$project_marker" && -f "$project_marker" ]] || return 1
  [[ "$(<"$project_marker")" == "$project" ]] || return 1
}

validate_image_target() {
  local image="$1"
  [[ -n "$project" && ("$image" == "${project}-current:smoke" || "$image" == "${project}-v0:smoke") ]]
}

retain_artifact() {
  local source="$1" destination="$2"
  [[ -f "$source" && ! -L "$source" ]] || die "成功产物缺失或是符号链接"
  if [[ -e "$destination" || -L "$destination" ]]; then
    [[ -f "$destination" && ! -L "$destination" ]] || die "本地基准产物目标无效"
    cmp -s -- "$source" "$destination" || die "同一 V0/corpus 身份已有内容不同的基准产物"
    chmod 600 -- "$destination"
    return 0
  fi
  install -m 600 -- "$source" "$destination" || die "无法保留成功 Smoke 产物"
}

retain_canonical_marker() {
  local snapshot="$1" baseline_dir="$2"
  local marker="$baseline_dir/v0-canonical.sha256"
  local temporary_marker="$tmp_root/v0-canonical.sha256"
  local snapshot_digest existing_digest

  snapshot_digest="$(sha256sum -- "$snapshot" 2>/dev/null | awk '{print $1}')"
  [[ "$snapshot_digest" =~ ^[0-9a-f]{64}$ ]] || die "V0 canonical 摘要无效"

  # 兼容本任务早期生成的扁平目录：在首次写 marker 前，先证明已有
  # snapshot 与本轮完全相同。marker 之后成为同一 V0/adapter/corpus
  # 身份唯一的 canonical 内容哨兵。
  if [[ ! -e "$marker" && ! -L "$marker" && -e "$baseline_dir/v0-snapshot.json" ]]; then
    [[ -f "$baseline_dir/v0-snapshot.json" && ! -L "$baseline_dir/v0-snapshot.json" ]] || die "已有 V0 canonical 产物无效"
    existing_digest="$(sha256sum -- "$baseline_dir/v0-snapshot.json" 2>/dev/null | awk '{print $1}')"
    [[ "$existing_digest" == "$snapshot_digest" ]] || die "同一 V0/corpus 身份已有不同 canonical 内容"
  fi

  printf '%s\n' "$snapshot_digest" > "$temporary_marker"
  chmod 600 -- "$temporary_marker"
  if [[ -e "$marker" || -L "$marker" ]]; then
    [[ -f "$marker" && ! -L "$marker" ]] || die "V0 canonical 标记无效"
    [[ "$(<"$marker")" == "$snapshot_digest" ]] || die "同一 V0/corpus 身份已有不同 canonical 内容"
  elif ! ln -- "$temporary_marker" "$marker" 2>/dev/null; then
    # 并发运行可能刚刚发布了相同 marker；只接受完全相同的常规文件。
    [[ -f "$marker" && ! -L "$marker" ]] || die "无法原子发布 V0 canonical 标记"
    [[ "$(<"$marker")" == "$snapshot_digest" ]] || die "同一 V0/corpus 身份并发产生不同 canonical 内容"
  fi
  chmod 600 -- "$marker"
  retain_artifact "$snapshot" "$baseline_dir/v0-snapshot.json"
}

retain_success_artifacts() {
  local manifest="$tmp_root/runner/manifest.json"
  [[ -f "$manifest" && ! -L "$manifest" ]] || die "Smoke manifest 缺失"
  local identity
  identity="$(python3 - "$manifest" <<'PY'
import json
import re
import sys

with open(sys.argv[1], encoding="ascii") as handle:
    manifest = json.load(handle)
value = manifest.get("v0", {}).get("code_hash")
adapter = manifest.get("v0", {}).get("adapter_hash")
corpus = manifest.get("corpus_digest")
snapshot = manifest.get("v0", {}).get("snapshot_sha256")
snapshot_version = manifest.get("v0", {}).get("snapshot_version")
v0_schema = manifest.get("v0", {}).get("schema_digest")
current_first = manifest.get("current_first", {})
current_second = manifest.get("current_second", {})
current_code = current_first.get("code_hash")
current_schema = current_first.get("schema_digest")
values = (value, adapter, corpus, snapshot, v0_schema, current_code, current_schema)
if any(not isinstance(item, str) or not re.fullmatch(r"[0-9a-f]{64}", item) for item in values):
    raise SystemExit(1)
if not isinstance(snapshot_version, int) or isinstance(snapshot_version, bool) or snapshot_version < 1:
    raise SystemExit(1)
if current_second.get("code_hash") != current_code or current_second.get("schema_digest") != current_schema:
    raise SystemExit(1)
print(" ".join((*values[:4], str(snapshot_version), *values[4:])))
PY
  )" || die "Smoke manifest 身份无效"
  local v0_hash adapter_hash corpus_hash snapshot_hash snapshot_version v0_schema_hash current_hash current_schema_hash
  local baseline_root baseline_dir runs_dir run_dir manifest_digest actual_snapshot_hash
  read -r v0_hash adapter_hash corpus_hash snapshot_hash snapshot_version v0_schema_hash current_hash current_schema_hash <<< "$identity"
  [[ "$v0_hash" =~ ^[0-9a-f]{64}$ && "$adapter_hash" =~ ^[0-9a-f]{64}$ && "$corpus_hash" =~ ^[0-9a-f]{64}$ ]] || die "Smoke manifest 身份无效"
  [[ "$snapshot_version" =~ ^[1-9][0-9]*$ && "$v0_schema_hash" =~ ^[0-9a-f]{64}$ ]] || die "V0 canonical schema 身份无效"
  actual_snapshot_hash="$(sha256sum -- "$tmp_root/runner/v0-snapshot.json" 2>/dev/null | awk '{print $1}')"
  [[ "$actual_snapshot_hash" == "$snapshot_hash" ]] || die "V0 canonical 文件与 manifest 不一致"
  manifest_digest="$(sha256sum -- "$manifest" 2>/dev/null | awk '{print $1}')"
  [[ "$manifest_digest" =~ ^[0-9a-f]{64}$ ]] || die "Smoke manifest 摘要无效"

  baseline_root="$ROOT_DIR/.roomusic-smoke"
  if [[ -e "$baseline_root" || -L "$baseline_root" ]]; then
    [[ -d "$baseline_root" && ! -L "$baseline_root" ]] || die "本地 Smoke 产物目录无效"
  else
    mkdir -m 700 -- "$baseline_root" || die "无法创建本地 Smoke 产物目录"
  fi
  chmod 700 -- "$baseline_root"
  # Canonical/SQLite schema 属于审计身份。将它纳入目录键后，显式的 snapshot
  # 迁移可与旧版只读产物共存，同一 schema 内的内容漂移仍会 fail closed。
  baseline_dir="$baseline_root/${v0_hash:0:16}-${adapter_hash:0:16}-${corpus_hash:0:16}-v${snapshot_version}-${v0_schema_hash:0:16}"
  if [[ -e "$baseline_dir" || -L "$baseline_dir" ]]; then
    [[ -d "$baseline_dir" && ! -L "$baseline_dir" ]] || die "本地 Smoke 基准目录无效"
  else
    mkdir -m 700 -- "$baseline_dir" || die "无法创建本地 Smoke 基准目录"
  fi
  chmod 700 -- "$baseline_dir"

  retain_canonical_marker "$tmp_root/runner/v0-snapshot.json" "$baseline_dir"

  runs_dir="$baseline_dir/runs"
  if [[ -e "$runs_dir" || -L "$runs_dir" ]]; then
    [[ -d "$runs_dir" && ! -L "$runs_dir" ]] || die "本地 Smoke 运行目录无效"
  else
    mkdir -m 700 -- "$runs_dir" || die "无法创建本地 Smoke 运行目录"
  fi
  chmod 700 -- "$runs_dir"
  run_dir="$runs_dir/${current_hash:0:16}-${current_schema_hash:0:16}-${manifest_digest:0:16}"
  if [[ -e "$run_dir" || -L "$run_dir" ]]; then
    [[ -d "$run_dir" && ! -L "$run_dir" ]] || die "本地 Smoke 单次运行目录无效"
  else
    mkdir -m 700 -- "$run_dir" || die "无法创建本地 Smoke 单次运行目录"
  fi
  chmod 700 -- "$run_dir"

  for artifact in \
    v0-reference.sqlite v0-snapshot.json current-snapshot-a.json current-snapshot-b.json \
    current-ab-comparison.json v0-current-comparison.json manifest.json; do
    retain_artifact "$tmp_root/runner/$artifact" "$run_dir/$artifact"
  done
  printf 'real-library-smoke: 已保留 V0 canonical 身份和本轮审计产物（权限受限）\n' >&2
}

cleanup() {
  local rc=$?
  local cleanup_target_valid=0
  [[ "$cleanup_done" == 1 ]] && return "$rc"
  cleanup_done=1
  trap - EXIT INT TERM

  if validate_cleanup_target; then
    cleanup_target_valid=1
  fi

  if [[ "$compose_started" == 1 ]]; then
    if [[ "$cleanup_target_valid" == 1 ]]; then
      # 不在终端暴露可能带路径的 Compose 诊断；生成的 project marker
      # 是本次精确清理的权威依据。
      compose down --volumes --remove-orphans >/dev/null 2>/dev/null || true
    else
      printf 'real-library-smoke: 拒绝清理未验证的 Compose project\n' >&2
      [[ "$rc" == 0 ]] && rc=1
    fi
  fi

  # 镜像标签包含经 marker 验证的随机 project 名。第二次构建或 Compose
  # 启动失败时也会删除部分构建镜像；共享层仍保留在 Docker 内容存储中。
  if [[ "$cleanup_target_valid" == 1 ]]; then
    for image in "$current_image" "$v0_image"; do
      if validate_image_target "$image" && command -v docker >/dev/null 2>&1; then
        docker image rm "$image" >/dev/null 2>/dev/null || true
      fi
    done
  fi

  if [[ -n "$compose_env_file" && -f "$compose_env_file" ]]; then
    rm -f -- "$compose_env_file"
  fi

  if [[ -n "$report_temporary" ]]; then
    case "$report_temporary" in
      "$ROOT_DIR/.trellis/tasks/09-03-real-library-smoke-v0-comparison/research/smoke-result.md.tmp."*)
        rm -f -- "$report_temporary"
        ;;
    esac
  fi

  if [[ "$keep_artifacts" == 1 && "$rc" == 0 ]]; then
    # 保留产物必须显式启用，并且只保留这个生成且受 marker 保护的运行目录。
    printf 'real-library-smoke: 已保留隔离产物（权限受限）\n' >&2
  elif [[ -n "$tmp_root" && -d "$tmp_root" && -f "$tmp_marker" ]]; then
    # marker 和生成名称校验防止宽泛目录或用户指定目录成为 rm 目标。
    case "$tmp_root" in
      "$tmp_parent"/roomusic-smoke-run.*) rm -rf -- "$tmp_root" ;;
      *) printf 'real-library-smoke: 拒绝清理未验证的临时目录\n' >&2; [[ "$rc" == 0 ]] && rc=1 ;;
    esac
  fi
  exit "$rc"
}

on_signal() {
  local signal_number="$1"
  exit "$signal_number"
}

trap cleanup EXIT
trap 'on_signal 130' INT
trap 'on_signal 143' TERM

while (($# > 0)); do
  case "$1" in
    --help|-h)
      usage
      exit 0
      ;;
    --music-root)
      (($# >= 2)) || die "--music-root 缺少参数"
      music_root_arg="$2"
      shift 2
      ;;
    --music-root=*)
      music_root_arg="${1#*=}"
      shift
      ;;
    --v0-archive)
      (($# >= 2)) || die "--v0-archive 缺少参数"
      v0_archive_arg="$2"
      shift 2
      ;;
    --v0-archive=*)
      v0_archive_arg="${1#*=}"
      shift
      ;;
    --runner)
      (($# >= 2)) || die "--runner 缺少参数"
      runner_arg="$2"
      shift 2
      ;;
    --runner=*)
      runner_arg="${1#*=}"
      shift
      ;;
    --dry-run|--preflight)
      dry_run=1
      shift
      ;;
    --keep-artifacts)
      keep_artifacts=1
      shift
      ;;
    *)
      die "未知参数；使用 --help 查看用法"
      ;;
  esac
done

[[ "${ROOMUSIC_REAL_LIBRARY_SMOKE:-}" == 1 ]] || die "必须显式设置 ROOMUSIC_REAL_LIBRARY_SMOKE=1"
[[ -n "$music_root_arg" ]] || die "必须显式提供 --music-root"
[[ -n "$v0_archive_arg" ]] || die "必须显式提供 --v0-archive"
[[ -f "$COMPOSE_FILE" ]] || die "smoke Compose 定义不存在"
[[ -f "$V0_ADAPTER_SOURCE" && ! -L "$V0_ADAPTER_SOURCE" ]] || die "V0 standalone adapter 不存在或无效"
command -v realpath >/dev/null 2>&1 || die "需要 realpath"
command -v sha256sum >/dev/null 2>&1 || die "需要 sha256sum"
command -v openssl >/dev/null 2>&1 || die "需要 openssl"
command -v python3 >/dev/null 2>&1 || die "需要 python3"
python3 - <<'PY' >/dev/null 2>&1 || die "需要支持 STRICT table 的 Python sqlite3（SQLite >= 3.37）"
import sqlite3

if sqlite3.sqlite_version_info < (3, 37, 0):
    raise SystemExit(1)
PY

music_root="$(canonical_dir "$music_root_arg")"
v0_archive="$(canonical_archive "$v0_archive_arg")"

# 不允许把文件系统根、仓库本身、包含仓库的父目录或宽泛工作区作为来源根。
[[ "$music_root" != / ]] || die "音乐根不能是文件系统根目录"
[[ "$music_root" != "$ROOT_DIR" ]] || die "音乐根不能是工作区根目录"
is_same_or_descendant "$music_root" "$ROOT_DIR" && die "音乐根不能包含工作区"

# 工作区后代路径只允许已记录的真实 music 树，防止误扫 .git、源码或 .trellis。
if is_same_or_descendant "$ROOT_DIR" "$music_root"; then
  is_same_or_descendant "$ROOT_DIR/music" "$music_root" || die "工作区内部仅允许使用 music/ 目录"
fi

for protected_path in \
  "$ROOT_DIR/data" \
  "$ROOT_DIR/appdata" \
  "$ROOT_DIR/.roomusic-smoke" \
  "$ROOT_DIR/.git" \
  "$ROOT_DIR/.trellis"; do
  if [[ -e "$protected_path" || -L "$protected_path" ]] && path_overlaps "$music_root" "$(realpath -m -- "$protected_path")"; then
    die "音乐根与受保护的数据或工作区目录重叠"
  fi
done

# 位于来源根内的归档可能被后续 scanner 看见；保持两项输入互斥也能避免清理或解包重叠。
path_overlaps "$music_root" "$v0_archive" && die "V0 归档不能位于音乐根内"

actual_v0_sha256="$(sha256sum -- "$v0_archive" 2>/dev/null || true)"
actual_v0_sha256="${actual_v0_sha256%% *}"
[[ "$actual_v0_sha256" =~ ^[[:xdigit:]]{64}$ ]] || die "无法读取 V0 归档哈希"
[[ "$actual_v0_sha256" == "$EXPECTED_V0_SHA256" ]] || die "V0 归档哈希不匹配（预期固定版本）"

tmp_parent="${TMPDIR:-/tmp}"
is_absolute "$tmp_parent" || die "TMPDIR 必须是绝对路径"
[[ -d "$tmp_parent" ]] || die "TMPDIR 不存在或不是目录"
tmp_parent="$(realpath -e -- "$tmp_parent" 2>/dev/null)" || die "TMPDIR 无法规范化"
[[ "$tmp_parent" != / ]] || die "拒绝将临时运行目录直接创建在文件系统根"
tmp_root="$(mktemp -d -- "$tmp_parent/roomusic-smoke-run.XXXXXX")" || die "无法创建隔离临时目录"
chmod 700 -- "$tmp_root"
tmp_marker="$tmp_root/run.marker"
printf '%s\n' "$(random_hex 16)" > "$tmp_marker"
chmod 600 -- "$tmp_marker"

# TMPDIR 等宽泛来源根不得包含本轮数据；常规真实根或临时 fixture 根应与其相互独立。
path_overlaps "$music_root" "$tmp_root" && die "音乐根与隔离临时目录重叠"
path_overlaps "$v0_archive" "$tmp_root" && die "V0 归档与隔离临时目录重叠"

project="${PROJECT_PREFIX}$(date -u +%s)-$(random_hex 8)"
project_marker="$tmp_root/project.marker"
printf '%s\n' "$project" > "$project_marker"
chmod 600 -- "$project_marker"
[[ "$project" =~ ^roomusic-smoke-[a-z0-9-]+$ ]] || die "生成的 project 名无效"

ports_text="$(allocate_ports)" || die "无法分配 loopback 预检端口"
mapfile -t ports <<< "$ports_text"
[[ "${#ports[@]}" == 2 ]] || die "loopback 端口分配结果无效"
for port in "${ports[@]}"; do
  [[ "$port" =~ ^[0-9]+$ && "$port" -ge 1024 && "$port" -le 65535 ]] || die "生成的 loopback 端口无效"
done

compose_env_file="$tmp_root/compose.env"
: > "$compose_env_file"
chmod 600 -- "$compose_env_file"
write_env_value ROOMUSIC_SMOKE_PROJECT "$project"
write_env_value ROOMUSIC_SMOKE_MUSIC_ROOT "$music_root"
write_env_value ROOMUSIC_SMOKE_V0_ARCHIVE "$v0_archive"
write_env_value ROOMUSIC_SMOKE_CURRENT_HTTP_PORT "${ports[0]}"
write_env_value ROOMUSIC_SMOKE_CURRENT_PG_PORT "${ports[1]}"
write_env_value ROOMUSIC_SMOKE_CURRENT_PG_PASSWORD "$(random_hex 24)"
write_env_value ROOMUSIC_SMOKE_CURRENT_SETUP_TOKEN "$(random_hex 32)"
write_env_value ROOMUSIC_SMOKE_CURRENT_JWT_SECRET "$(random_hex 32)"
write_env_value ROOMUSIC_SMOKE_CURRENT_REFRESH_SECRET "$(random_hex 32)"
write_env_value ROOMUSIC_SMOKE_DATA_ROOT "$tmp_root/data"
mkdir -m 700 -- "$tmp_root/data" "$tmp_root/data/current" "$tmp_root/data/v0"
chown 1000:1000 "$tmp_root/data/current" "$tmp_root/data/v0" 2>/dev/null || chmod 0770 "$tmp_root/data/current" "$tmp_root/data/v0"

printf 'real-library-smoke: 预检通过（project=%s，端口=%s/%s）\n' \
  "$project" "${ports[0]}" "${ports[1]}" >&2
printf 'real-library-smoke: V0 归档固定哈希已核对，真实音乐未读取\n' >&2

if ((dry_run)); then
  printf 'real-library-smoke: dry-run/preflight 完成；未连接 Docker、未解包归档、未扫描音乐\n' >&2
  exit 0
fi

command -v docker >/dev/null 2>&1 || die "真实运行需要 docker；无 Docker 时请使用 --dry-run"
command -v go >/dev/null 2>&1 || die "真实运行需要 go；无 Go 时请使用 --dry-run"

command -v tar >/dev/null 2>&1 || die "真实运行需要 tar"
v0_source="$tmp_root/v0-source"
mkdir -m 700 -- "$v0_source"
tar -xzf "$v0_archive" --no-same-owner --no-same-permissions --directory "$v0_source" || die "V0 归档解包失败"
[[ -d "$v0_source/ROOMusic" ]] || die "V0 归档目录结构无效"
install_v0_adapter

# 构建上下文从不包含 music_root。构建日志只留在本轮 0700 临时目录，
# 不复制到任务报告或基准产物。
current_image="${project}-current:smoke"
v0_image="${project}-v0:smoke"
comparator="$tmp_root/roomusic-smoke-cli"
(cd "$ROOT_DIR/backend" && go build -trimpath -o "$comparator" ./cmd/roomusic-smoke-cli) >/dev/null 2>/dev/null || die "Smoke 比较器构建失败"
chmod 700 -- "$comparator"
current_code_sha256="$(current_source_digest 2>/dev/null)" || die "无法计算 current 构建输入摘要"
[[ "$current_code_sha256" =~ ^[0-9a-f]{64}$ ]] || die "current 构建输入摘要无效"
docker_build "$ROOT_DIR/deploy/smoke/current.Dockerfile" "$current_image" "$ROOT_DIR/backend" "$tmp_root/current-image-build.log" || die "当前版本 Smoke 镜像构建失败"
[[ "$(current_source_digest 2>/dev/null)" == "$current_code_sha256" ]] || die "current 构建输入在镜像构建期间发生变化"
docker_build "$ROOT_DIR/deploy/smoke/v0.Dockerfile" "$v0_image" "$v0_source/ROOMusic" "$tmp_root/v0-image-build.log" || die "V0 Smoke 镜像构建失败"
write_env_value ROOMUSIC_SMOKE_CURRENT_IMAGE "$current_image"
write_env_value ROOMUSIC_SMOKE_V0_IMAGE "$v0_image"
write_env_value ROOMUSIC_SMOKE_COMPARATOR "$comparator"
write_env_value ROOMUSIC_SMOKE_V0_ADAPTER_SHA256 "$v0_adapter_sha256"
write_env_value ROOMUSIC_SMOKE_CURRENT_CODE_SHA256 "$current_code_sha256"

# Config 是只读 Compose 操作，仍只使用生成的 env 文件和 project 名，
# 不读取仓库的 .env/.env.dev。
compose config --quiet >/dev/null 2>/dev/null || die "smoke Compose 配置校验失败"

if ! docker ps -a --filter "label=com.docker.compose.project=$project" --quiet 2>/dev/null | grep -q .; then
  :
else
  die "生成的 Compose project 已存在，拒绝复用"
fi
if ! docker volume ls --filter "label=com.docker.compose.project=$project" --quiet 2>/dev/null | grep -q .; then
  :
else
  die "生成的 Compose volume 已存在，拒绝复用"
fi

# 在启动任何服务前验证物理只读合同；Compose v2 的 JSON 结构稳定，
# 此断言同时覆盖两个应用镜像。
compose_config_json="$(compose config --format json 2>/dev/null)" || die "无法读取 smoke Compose 配置"
python3 - "$compose_config_json" "$music_root" <<'PY' || die "Compose 未证明 music 挂载为只读"
import json
import os
import sys

config = json.loads(sys.argv[1])
expected = os.path.realpath(sys.argv[2])
services = config.get("services", {})
for service in ("current", "v0"):
    mounts = services.get(service, {}).get("volumes", [])
    found = False
    for mount in mounts:
        if (
            mount.get("target") == "/music"
            and mount.get("type") == "bind"
            and mount.get("read_only") is True
            and os.path.realpath(mount.get("source", "")) == expected
        ):
            found = True
            break
    if not found:
        raise SystemExit(1)
PY

if [[ -z "$runner_arg" ]]; then
  runner_arg="$ROOT_DIR/scripts/real-library-smoke-runner.sh"
fi
is_absolute "$runner_arg" || die "--runner 必须是绝对路径"
[[ -x "$runner_arg" ]] || die "--runner 不存在或不可执行"

# V0 必须在 current 或 PostgreSQL 启动前先建立并完成 SQLite/canonical
# 门禁。这里只 create 不 start，runner 会在资产摘要完成后启动 exporter。
compose_started=1
compose create v0 >/dev/null 2>/dev/null || die "V0 exporter 容器创建失败"
validate_v0_isolation

# 保持 runner 边界窄且显式。runner 可能产生带路径的诊断，因此不回放其
# stdout/stderr；它可以改为在生成的产物目录写脱敏报告。
if ! "$runner_arg" \
  --compose-file "$COMPOSE_FILE" \
  --compose-project "$project" \
  --env-file "$compose_env_file" \
  --music-root "$music_root" \
  --v0-archive "$v0_archive" \
  --artifacts-dir "$tmp_root" >/dev/null 2>/dev/null; then
  if [[ "$runner_arg" == "$ROOT_DIR/scripts/real-library-smoke-runner.sh" && -f "$tmp_root/failure.reason" && ! -L "$tmp_root/failure.reason" ]]; then
    failure_reason="$(<"$tmp_root/failure.reason")"
    if [[ -n "$failure_reason" && ${#failure_reason} -le 200 ]] && ! has_newline "$failure_reason"; then
      die "扫描执行器失败：$failure_reason"
    fi
  fi
  die "扫描执行器失败"
fi

retain_success_artifacts

# runner 只在 marker 保护的临时目录写脱敏 Markdown 结果。成功后，V0 基准和
# 字段白名单约束的审计产物进入 Git 忽略目录；任务内只发布这份聚合报告。
# 并发运行采用“最后一个完整成功运行胜出”，同目录临时文件 + rename 保证
# 读者不会看到截断或混合的报告。
report_source="$tmp_root/smoke-result.md"
report_destination="$ROOT_DIR/.trellis/tasks/09-03-real-library-smoke-v0-comparison/research/smoke-result.md"
if [[ -f "$report_source" ]]; then
  mkdir -p -- "${report_destination%/*}"
  report_temporary="$(mktemp -- "${report_destination}.tmp.XXXXXX")" || die "无法创建脱敏 Smoke 临时报告"
  if ! install -m 0644 -- "$report_source" "$report_temporary"; then
    rm -f -- "$report_temporary"
    die "无法写入脱敏 Smoke 临时报告"
  fi
  if ! mv -f -- "$report_temporary" "$report_destination"; then
    rm -f -- "$report_temporary"
    die "无法原子发布脱敏 Smoke 报告"
  fi
  report_temporary=""
fi

printf 'real-library-smoke: 执行器完成，隔离资源将由 trap 清理\n' >&2
exit 0
