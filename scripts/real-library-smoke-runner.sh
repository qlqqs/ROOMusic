#!/usr/bin/env bash
# 显式启用的真实音乐库 Smoke 所使用的 standalone V0 + REST 编排器。
#
# 父入口负责预检、镜像构建、Compose 隔离和清理。本脚本先运行隔离 V0 exporter
# 并校验 normalized SQLite，再通过公开 REST 合同启动 current；canonical 导出只读取
# 临时 current PostgreSQL 容器。任何含路径、凭据或 metadata 的响应都不会打印到 stdout。
set -Eeuo pipefail
umask 077

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
readonly ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd -P)"
readonly DEFAULT_POLL_INTERVAL="${ROOMUSIC_SMOKE_POLL_INTERVAL:-5}"
readonly DEFAULT_TIMEOUT="${ROOMUSIC_SMOKE_TIMEOUT_SECONDS:-14400}"
readonly EXPECTED_V0_SHA256="fe25388328698b26991ea3b59a14406a155eb92d578a9be2a68d67d331ecf97d"

compose_file=""
compose_project=""
env_file=""
music_root=""
v0_archive=""
artifacts_dir=""
poll_interval="$DEFAULT_POLL_INTERVAL"
timeout_seconds="$DEFAULT_TIMEOUT"

runner_dir=""
raw_dir=""
report_file=""
failure_reason_file=""
current_cookie_jar=""
sequence=0
cleanup_done=0
success=0
runner_stage="参数校验"

usage() {
  cat <<'EOF'
用法：
  scripts/real-library-smoke-runner.sh \
    --compose-file FILE --compose-project NAME --env-file FILE \
    --music-root DIR --v0-archive FILE --artifacts-dir DIR

该脚本仅由 real-library-smoke.sh 调用；它不会直接启动或清理 Compose。
EOF
}

log_error() {
  printf 'real-library-smoke-runner: %s\n' "$1" >&2
}

write_failure_reason() {
  local reason="$1"
  if [[ -n "$failure_reason_file" && "$failure_reason_file" == "$artifacts_dir/failure.reason" && -d "$artifacts_dir" && ! -L "$artifacts_dir" ]]; then
    printf '%s\n' "$reason" > "$failure_reason_file" 2>/dev/null || true
    chmod 600 -- "$failure_reason_file" 2>/dev/null || true
  fi
}

die() {
  write_failure_reason "$1"
  log_error "$1"
  exit 1
}

is_absolute() {
  [[ "$1" == /* ]]
}

is_safe_name() {
  [[ "$1" =~ ^[a-zA-Z0-9._-]+$ ]]
}

is_uuid() {
  [[ "$1" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$ ]]
}

valid_integer() {
  [[ "$1" =~ ^[0-9]+$ ]]
}

while (($# > 0)); do
  case "$1" in
    --help|-h)
      usage
      exit 0
      ;;
    --compose-file)
      (($# >= 2)) || die "--compose-file 缺少参数"
      compose_file="$2"
      shift 2
      ;;
    --compose-file=*) compose_file="${1#*=}"; shift ;;
    --compose-project)
      (($# >= 2)) || die "--compose-project 缺少参数"
      compose_project="$2"
      shift 2
      ;;
    --compose-project=*) compose_project="${1#*=}"; shift ;;
    --env-file)
      (($# >= 2)) || die "--env-file 缺少参数"
      env_file="$2"
      shift 2
      ;;
    --env-file=*) env_file="${1#*=}"; shift ;;
    --music-root)
      (($# >= 2)) || die "--music-root 缺少参数"
      music_root="$2"
      shift 2
      ;;
    --music-root=*) music_root="${1#*=}"; shift ;;
    --v0-archive)
      (($# >= 2)) || die "--v0-archive 缺少参数"
      v0_archive="$2"
      shift 2
      ;;
    --v0-archive=*) v0_archive="${1#*=}"; shift ;;
    --artifacts-dir)
      (($# >= 2)) || die "--artifacts-dir 缺少参数"
      artifacts_dir="$2"
      shift 2
      ;;
    --artifacts-dir=*) artifacts_dir="${1#*=}"; shift ;;
    --poll-interval)
      (($# >= 2)) || die "--poll-interval 缺少参数"
      poll_interval="$2"
      shift 2
      ;;
    --poll-interval=*) poll_interval="${1#*=}"; shift ;;
    --timeout-seconds)
      (($# >= 2)) || die "--timeout-seconds 缺少参数"
      timeout_seconds="$2"
      shift 2
      ;;
    --timeout-seconds=*) timeout_seconds="${1#*=}"; shift ;;
    *) die "未知参数；使用 --help 查看用法" ;;
  esac
done

[[ -n "$compose_file" && -f "$compose_file" ]] || die "Compose 文件无效"
[[ -n "$env_file" && -f "$env_file" && ! -L "$env_file" ]] || die "Compose env 文件无效"
[[ -n "$music_root" && -d "$music_root" ]] || die "音乐根无效"
[[ -n "$v0_archive" && -f "$v0_archive" ]] || die "V0 归档无效"
[[ -n "$artifacts_dir" && -d "$artifacts_dir" && ! -L "$artifacts_dir" ]] || die "产物目录无效"
is_absolute "$compose_file" || die "Compose 文件必须是绝对路径"
is_absolute "$env_file" || die "Compose env 文件必须是绝对路径"
is_absolute "$music_root" || die "音乐根必须是绝对路径"
is_absolute "$v0_archive" || die "V0 归档必须是绝对路径"
is_absolute "$artifacts_dir" || die "产物目录必须是绝对路径"
is_safe_name "$compose_project" || die "Compose project 名无效"
[[ "$compose_project" == roomusic-smoke-* ]] || die "Compose project 不是本轮生成的名称"
[[ -f "$artifacts_dir/run.marker" ]] || die "产物目录不是本轮生成的目录"
[[ -f "$artifacts_dir/project.marker" && ! -L "$artifacts_dir/project.marker" ]] || die "产物目录缺少 project marker"
[[ "$(<"$artifacts_dir/project.marker")" == "$compose_project" ]] || die "产物目录与 Compose project 不匹配"
valid_integer "$poll_interval" && ((poll_interval > 0 && poll_interval <= 300)) || die "轮询间隔无效"
valid_integer "$timeout_seconds" && ((timeout_seconds >= 30 && timeout_seconds <= 604800)) || die "扫描超时无效"

for required in curl docker jq openssl python3 sha256sum; do
  command -v "$required" >/dev/null 2>&1 || die "缺少依赖：$required"
done
python3 - <<'PY' >/dev/null 2>&1 || die "Python sqlite3 版本不支持 normalized reference"
import sqlite3

if sqlite3.sqlite_version_info < (3, 37, 0):
    raise SystemExit(1)
PY

runner_dir="$artifacts_dir/runner"
raw_dir="$runner_dir/raw"
report_file="$artifacts_dir/smoke-result.md"
failure_reason_file="$artifacts_dir/failure.reason"
mkdir -m 700 -- "$runner_dir" "$raw_dir"
current_cookie_jar="$runner_dir/current.cookies"
touch -- "$current_cookie_jar"
chmod 600 -- "$current_cookie_jar"

# 只解析生成的 key=value 项，不 source env 文件：路径可能含空格，source 还会把数据
# 不必要地变成 Shell 代码。
env_value() {
  local key="$1"
  local value
  value="$(awk -v wanted="$key" 'index($0, wanted "=") == 1 { print substr($0, length(wanted) + 2); exit }' "$env_file" 2>/dev/null || true)"
  [[ -n "$value" ]] || die "Compose env 缺少必要配置"
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || die "Compose env 含非法换行"
  printf '%s' "$value"
}

current_http_port="$(env_value ROOMUSIC_SMOKE_CURRENT_HTTP_PORT)"
current_pg_password="$(env_value ROOMUSIC_SMOKE_CURRENT_PG_PASSWORD)"
current_image="$(env_value ROOMUSIC_SMOKE_CURRENT_IMAGE)"
v0_image="$(env_value ROOMUSIC_SMOKE_V0_IMAGE)"
comparator="$(env_value ROOMUSIC_SMOKE_COMPARATOR)"
v0_adapter_sha256="$(env_value ROOMUSIC_SMOKE_V0_ADAPTER_SHA256)"
current_code_sha256="$(env_value ROOMUSIC_SMOKE_CURRENT_CODE_SHA256)"
valid_integer "$current_http_port" || die "HTTP 端口配置无效"
((current_http_port >= 1024 && current_http_port <= 65535)) || die "HTTP 端口超出范围"
[[ -n "$current_pg_password" ]] || die "隔离凭据缺失"
[[ -n "$current_image" && -n "$v0_image" ]] || die "隔离镜像配置缺失"
[[ "$v0_adapter_sha256" =~ ^[0-9a-f]{64}$ ]] || die "V0 standalone adapter 摘要无效"
[[ "$current_code_sha256" =~ ^[0-9a-f]{64}$ ]] || die "current 构建输入摘要无效"
is_absolute "$comparator" || die "比较器路径必须是绝对路径"
[[ -x "$comparator" && ! -L "$comparator" ]] || die "比较器不可执行"

readonly current_url="http://127.0.0.1:${current_http_port}"
readonly current_origin="$current_url"
readonly admin_username="smoke-admin"

compose() {
  env -u COMPOSE_PROJECT_NAME -u COMPOSE_FILE -u COMPOSE_PROFILES \
    COMPOSE_DISABLE_ENV_FILE=1 \
    docker compose --ansi never --progress plain --env-file "$env_file" \
    --file "$compose_file" --project-name "$compose_project" "$@"
}

compose_psql() {
  local service="$1" password="$2" sql="$3"
  compose exec -T -e "PGPASSWORD=$password" "$service" \
    psql -X -v ON_ERROR_STOP=1 -P pager=off -U roomusic -d roomusic -At -c "$sql" \
    2>/dev/null
}

compose_dump() {
  local service="$1" password="$2" output="$3"
  shift 3
  compose exec -T -e "PGPASSWORD=$password" "$service" \
    pg_dump -U roomusic -d roomusic --no-owner --no-privileges --format=plain "$@" \
    > "$output" 2>/dev/null || die "PostgreSQL 只读导出失败"
  chmod 600 -- "$output"
  [[ -s "$output" ]] || die "PostgreSQL 导出为空"
}

next_file() {
  sequence=$((sequence + 1))
  printf '%s/http-%04d.json' "$raw_dir" "$sequence"
}

# 执行一次有界 JSON REST 请求。参数依次为 label、method、URL、status、token、origin、
# cookie-jar、idempotency-key 和 body；最终响应路径通过全局 response_file 返回。
response_file=""
request_json() {
  local label="$1" method="$2" url="$3" expected="$4" token="$5" origin="$6" cookie="$7" idem="$8" body="$9" setup_token="${10:-}"
  local body_file="" status
  response_file="$(next_file)"
  if [[ -n "$body" ]]; then
    body_file="$raw_dir/body-${sequence}.json"
    printf '%s' "$body" > "$body_file"
    chmod 600 -- "$body_file"
  fi

  local -a args=(--silent --show-error --noproxy 127.0.0.1 --connect-timeout 5 --max-time 30
    --output "$response_file" --write-out '%{http_code}'
    --request "$method" --header 'Accept: application/json')
  [[ -n "$body_file" ]] && args+=(--header 'Content-Type: application/json' --data-binary "@$body_file")
  [[ -n "$token" ]] && args+=(--header "Authorization: Bearer $token")
  [[ -n "$setup_token" ]] && args+=(--header "X-ROOMusic-Setup-Token: $setup_token")
  [[ -n "$origin" ]] && args+=(--header "Origin: $origin")
  [[ -n "$cookie" ]] && args+=(--cookie "$cookie" --cookie-jar "$cookie")
  [[ -n "$idem" ]] && args+=(--header "Idempotency-Key: $idem")
  status="$(curl "${args[@]}" "$url" 2>/dev/null || true)"
  [[ -n "$body_file" ]] && rm -f -- "$body_file"
  [[ "$status" == "$expected" ]] || die "${label} 请求未返回预期状态"
  jq -e . "$response_file" >/dev/null 2>/dev/null || die "${label} 返回的 JSON 无效"
}

json_value() {
  local file="$1" expression="$2"
  jq -er "$expression" "$file" 2>/dev/null || die "无法读取 ${3:-JSON} 字段"
}

wait_health() {
  local label="$1" url="$2" path="$3" deadline=$((SECONDS + 300)) status
  while ((SECONDS < deadline)); do
    status="$(curl --silent --show-error --noproxy 127.0.0.1 --connect-timeout 3 --max-time 5 \
      --output /dev/null --write-out '%{http_code}' "$url$path" 2>/dev/null || true)"
    if [[ "$status" == 200 ]]; then
      return 0
    fi
    sleep 2
  done
  die "${label} 健康检查超时"
}

tree_summary() {
  local root="$1" output="$2"
  python3 - "$root" "$output" <<'PY' 2>/dev/null || die "资产树摘要失败"
import hashlib
import json
import os
import stat
import sys

root = os.path.abspath(sys.argv[1])
output = sys.argv[2]
entries = []
files = 0
total_bytes = 0
stack = [root]

while stack:
    directory = stack.pop()
    with os.scandir(directory) as iterator:
        for item in iterator:
            path = item.path
            info = os.lstat(path)
            mode = info.st_mode
            relative = os.path.relpath(path, root).replace(os.sep, "/")
            if stat.S_ISDIR(mode):
                stack.append(path)
                content_digest = ""
                kind = "directory"
            elif stat.S_ISREG(mode):
                digest = hashlib.sha256()
                descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
                try:
                    checked = os.fstat(descriptor)
                    if not stat.S_ISREG(checked.st_mode):
                        raise OSError("file type changed")
                    while True:
                        block = os.read(descriptor, 1024 * 1024)
                        if not block:
                            break
                        digest.update(block)
                    if checked.st_size != info.st_size:
                        raise OSError("file changed while reading")
                finally:
                    os.close(descriptor)
                content_digest = digest.hexdigest()
                files += 1
                total_bytes += info.st_size
                kind = "regular"
            elif stat.S_ISLNK(mode):
                content_digest = hashlib.sha256(("symlink\0" + os.readlink(path)).encode("utf-8", "surrogateescape")).hexdigest()
                kind = "symlink"
            else:
                content_digest = hashlib.sha256(("special\0" + stat.filemode(mode)).encode("ascii")).hexdigest()
                kind = "special"
            entry = b"\0".join((
                os.fsencode(relative), kind.encode("ascii"), str(info.st_size).encode("ascii"),
                oct(stat.S_IMODE(mode)).encode("ascii"), str(info.st_mtime_ns).encode("ascii"),
                content_digest.encode("ascii")))
            entries.append(entry)

hasher = hashlib.sha256()
for entry in sorted(entries):
    hasher.update(entry)
    hasher.update(b"\n")
result = {"files": files, "entries": len(entries), "bytes": total_bytes, "digest": hasher.hexdigest()}
with open(output, "w", encoding="ascii", opener=lambda path, flags: os.open(path, flags, 0o600)) as handle:
    json.dump(result, handle, sort_keys=True, separators=(",", ":"))
    handle.write("\n")
PY
  chmod 600 -- "$output"
}

migration_schema_digest() {
  local migrations="$1"
  python3 - "$migrations" <<'PY' 2>/dev/null || die "current migration schema 摘要失败"
import hashlib
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
paths = sorted(path for path in root.glob("*.sql") if path.is_file() and not path.is_symlink())
if not paths:
    raise SystemExit(1)
digest = hashlib.sha256()
for path in paths:
    digest.update(path.name.encode("utf-8"))
    digest.update(b"\0")
    digest.update(path.read_bytes())
    digest.update(b"\n")
print(digest.hexdigest())
PY
}

scan_status=""
scan_run_id=""
scan_diagnostics_file=""
scan_start_epoch=0
scan_end_epoch=0

terminal_diagnostic_summary() {
  local status_file="$1"
  python3 - "$status_file" <<'PY'
import json
import re
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
summary = payload.get("diagnostics")
if not isinstance(summary, dict):
    raise SystemExit(1)
total = summary.get("total")
counts = summary.get("counts")
if isinstance(total, bool) or not isinstance(total, int) or total < 0 or not isinstance(counts, list) or len(counts) > 100:
    raise SystemExit(1)
safe = []
computed_total = 0
for item in counts:
    if not isinstance(item, dict):
        raise SystemExit(1)
    kind = item.get("kind")
    count = item.get("count")
    if not isinstance(kind, str) or re.fullmatch(r"[a-z0-9_]{1,64}", kind) is None:
        raise SystemExit(1)
    if isinstance(count, bool) or not isinstance(count, int) or count < 0:
        raise SystemExit(1)
    computed_total += count
    if len(safe) < 8:
        safe.append(f"{kind}={count}")
if computed_total != total:
    raise SystemExit(1)
suffix = ",..." if len(counts) > len(safe) else ""
print(f"diagnostics={','.join(safe) or 'none'}{suffix};total={total}")
PY
}

cue_reference_reason_summary() {
  local scan_id="$1" rows row result="" count=0
  rows="$(compose_psql current-postgres "$current_pg_password" "
    SELECT reason || '=' || COUNT(*)::text
    FROM (
      SELECT CASE
        WHEN message LIKE 'missing FILE reference:%' THEN 'missing_reference'
        WHEN message LIKE 'unable to stat FILE reference:%' THEN 'stat_failure'
        WHEN message = 'empty or NUL FILE reference' OR message = 'empty FILE reference' THEN 'empty_reference'
        WHEN message = 'unsafe absolute FILE reference' THEN 'absolute_reference'
        WHEN message = 'CUE file is outside containment root' THEN 'cue_outside_root'
        WHEN message = 'unsafe FILE path traversal' THEN 'path_traversal'
        WHEN message LIKE 'FILE reference is not a regular file:%' THEN 'non_regular_reference'
        WHEN message LIKE 'unable to resolve FILE symlink:%' THEN 'symlink_resolution'
        WHEN message LIKE 'FILE symlink escapes containment root:%' THEN 'symlink_escape'
        WHEN message LIKE 'non-monotonic INDEX 01 in FILE reference:%' THEN 'non_monotonic_index'
        WHEN message LIKE 'missing INDEX 01 for track %' THEN 'missing_index'
        ELSE 'other'
      END AS reason
      FROM scan_diagnostics
      WHERE scan_run_id = '$scan_id'::uuid AND kind = 'cue_reference'
    ) classified
    GROUP BY reason
    ORDER BY reason;
  " 2>/dev/null)" || return 1
  while IFS= read -r row; do
    [[ -z "$row" ]] && continue
    [[ "$row" =~ ^[a-z_]{1,32}=[0-9]+$ ]] || return 1
    count=$((count + 1))
    if ((count <= 4)); then
      [[ -z "$result" ]] || result+=","
      result+="$row"
    fi
  done <<< "$rows"
  [[ -n "$result" ]] || return 0
  ((count <= 4)) || result+=",..."
  printf 'cue_reasons=%s' "$result"
}

poll_current_scan() {
  local id="$1" run_label="$2" deadline=$((SECONDS + timeout_seconds)) status diagnostic_summary cue_summary
  scan_start_epoch="$SECONDS"
  while ((SECONDS < deadline)); do
    request_json "${run_label}-status" GET "$current_url/api/v1/scans/$id" 200 "" "" "$current_cookie_jar" "" ""
    status="$(json_value "$response_file" '.status' "${run_label}状态")"
    case "$status" in
      succeeded)
        scan_status="$status"
        scan_end_epoch="$SECONDS"
        return 0
        ;;
      failed|canceled|incomplete)
        diagnostic_summary="$(terminal_diagnostic_summary "$response_file" 2>/dev/null || true)"
        [[ -n "$diagnostic_summary" ]] || diagnostic_summary="diagnostics=unavailable"
        cue_summary="$(cue_reference_reason_summary "$id" 2>/dev/null || true)"
        [[ -z "$cue_summary" ]] || diagnostic_summary+=";${cue_summary}"
        die "${run_label}未成功完成（status=${status};${diagnostic_summary}）"
        ;;
      running) ;;
      *) die "${run_label}返回未知终态" ;;
    esac
    sleep "$poll_interval"
  done
  die "${run_label}轮询超时"
}

wait_v0_exporter() {
  local deadline=$((SECONDS + timeout_seconds)) id state status exit_code
  id="$(compose ps --all -q v0 2>/dev/null || true)"
  [[ "$id" =~ ^[0-9a-f]{64}$ ]] || die "无法确认 V0 exporter 容器身份"
  compose start v0 >/dev/null 2>/dev/null || die "V0 exporter 启动失败"
  scan_start_epoch="$SECONDS"
  while ((SECONDS < deadline)); do
    state="$(docker inspect --format '{{.State.Status}}|{{.State.ExitCode}}' "$id" 2>/dev/null || true)"
    [[ "$state" == *"|"* ]] || die "V0 exporter 状态不可读"
    status="${state%%|*}"
    exit_code="${state#*|}"
    case "$status" in
      exited)
        [[ "$exit_code" == 0 ]] || die "V0 standalone scanner 未成功完成"
        scan_end_epoch="$SECONDS"
        return 0
        ;;
      created|running|restarting) ;;
      dead|removing|paused) die "V0 exporter 进入异常状态" ;;
      *) die "V0 exporter 返回未知状态" ;;
    esac
    sleep "$poll_interval"
  done
  die "V0 standalone scanner 轮询超时"
}

validate_current_isolation() {
  local current_id container_inspect network_inspect data_root
  current_id="$(compose ps -q current 2>/dev/null || true)"
  [[ "$current_id" =~ ^[0-9a-f]{64}$ ]] || die "无法确认 current 容器身份"
  data_root="$(env_value ROOMUSIC_SMOKE_DATA_ROOT)"
  is_absolute "$data_root" || die "current 数据目录配置无效"

  container_inspect="$runner_dir/current-container-inspect.json"
  network_inspect="$runner_dir/current-network-inspect.json"
  docker inspect "$current_id" > "$container_inspect" 2>/dev/null || die "无法读取 current 容器配置"
  docker network inspect "${compose_project}_current-net" "${compose_project}_current-control" \
    > "$network_inspect" 2>/dev/null || die "无法读取 current 隔离网络"
  chmod 600 -- "$container_inspect" "$network_inspect"

  python3 - "$container_inspect" "$network_inspect" "$music_root" "$data_root/current" \
    "$compose_project" "$current_id" "$current_http_port" <<'PY' \
    >/dev/null 2>&1 || die "current 运行时隔离合同校验失败"
import json
import os
import sys

container_path, network_path, music_root, data_root, project, identifier, host_port = sys.argv[1:]
with open(container_path, encoding="utf-8") as handle:
    containers = json.load(handle)
if len(containers) != 1 or containers[0].get("Id") != identifier:
    raise ValueError("container identity mismatch")
container = containers[0]
with open(network_path, encoding="utf-8") as handle:
    networks = {item["Name"]: item for item in json.load(handle)}

mounts = {item.get("Destination"): item for item in container.get("Mounts", [])}
music = mounts.get("/music")
data = mounts.get("/data")
if music is None or music.get("RW") is not False:
    raise ValueError("music mount is not read-only")
if os.path.realpath(music.get("Source", "")) != os.path.realpath(music_root):
    raise ValueError("music mount source mismatch")
if data is None or data.get("RW") is not True:
    raise ValueError("data mount is not writable")
if os.path.realpath(data.get("Source", "")) != os.path.realpath(data_root):
    raise ValueError("data mount source mismatch")

host = container.get("HostConfig", {})
if host.get("ReadonlyRootfs") is not True:
    raise ValueError("root filesystem is writable")
if "ALL" not in (host.get("CapDrop") or []):
    raise ValueError("capabilities were not dropped")
if "no-new-privileges:true" not in (host.get("SecurityOpt") or []):
    raise ValueError("no-new-privileges is missing")
environment = container.get("Config", {}).get("Env") or []
proxy_names = {"http_proxy", "https_proxy", "all_proxy", "no_proxy"}
if any(item.partition("=")[0].lower() in proxy_names for item in environment):
    raise ValueError("proxy leaked into runtime")

expected_networks = {f"{project}_current-net", f"{project}_current-control"}
actual_networks = set(container.get("NetworkSettings", {}).get("Networks", {}))
if actual_networks != expected_networks:
    raise ValueError("unexpected current network")
bindings = container.get("NetworkSettings", {}).get("Ports", {}).get("8080/tcp") or []
if len(bindings) != 1 or bindings[0].get("HostIp") != "127.0.0.1" or bindings[0].get("HostPort") != host_port:
    raise ValueError("current control port is not loopback-only")

data_network = networks[f"{project}_current-net"]
control_network = networks[f"{project}_current-control"]
if data_network.get("Internal") is not True or control_network.get("Internal") is not False:
    raise ValueError("unexpected network isolation")
if (control_network.get("Options") or {}).get("com.docker.network.bridge.enable_ip_masquerade") != "false":
    raise ValueError("control network permits masquerade")
PY

  rm -f -- "$container_inspect" "$network_inspect"
}

read_schema_version() {
  local service="$1" password="$2" value
  value="$(compose_psql "$service" "$password" "SELECT COALESCE(MAX(version)::text, 'unknown') FROM schema_migrations;" | tail -n 1 || true)"
  [[ -n "$value" ]] && printf '%s' "$value" || printf 'unknown'
}

export_current_rows() {
  local scan_id="$1" prefix="$2"
  compose_psql current-postgres "$current_pg_password" \
    "SELECT jsonb_build_object('id',r.id::text,'title',r.title,'artist',r.artist,'album_artist',r.album_artist,'year',r.year,'source_type',r.source_type,'media_type',r.media_type,'edition',r.edition,'label',r.label,'barcode',r.barcode,'catalog',r.catalog_number,'genre',r.genre,'candidate_kind',r.candidate_kind,'candidate_anchor',r.candidate_anchor,'source_root_id',r.source_root_id::text,'relative_directory',r.relative_directory)::text FROM releases r ORDER BY r.id;" \
    > "$raw_dir/${prefix}-releases.ndjson" || die "当前 releases 导出失败"
  compose_psql current-postgres "$current_pg_password" \
    "SELECT jsonb_build_object('id',m.id::text,'release_id',m.release_id::text,'position',m.position,'title',m.title,'format','')::text FROM media m ORDER BY m.release_id,m.position,m.id;" \
    > "$raw_dir/${prefix}-media.ndjson" || die "当前 media 导出失败"
  compose_psql current-postgres "$current_pg_password" \
    "SELECT jsonb_build_object('id',t.id::text,'medium_id',t.medium_id::text,'position',t.position,'title',t.title,'artist',t.artist,'source_kind',CASE WHEN t.source_kind='cue_virtual' THEN 'cue_virtual' ELSE 'physical' END,'source_status',t.source_status,'source_identity',t.source_identity,'relative_path',t.relative_path,'cue_sheet_path',t.cue_sheet_path,'cue_parent_relative_path',t.cue_parent_relative_path,'cue_index_frames',t.cue_index_frames,'cue_end_frames',t.cue_end_frames,'cue_isrc',t.cue_isrc,'duration_ms',CASE WHEN t.duration_seconds IS NULL THEN NULL ELSE FLOOR(t.duration_seconds * 1000)::bigint END,'codec',LOWER(t.codec),'sample_rate',t.sample_rate,'channels',t.channels,'bitrate',t.bitrate,'bit_depth',t.bit_depth)::text FROM tracks t ORDER BY t.medium_id,t.position,t.id;" \
    > "$raw_dir/${prefix}-tracks.ndjson" || die "当前 tracks 导出失败"
  compose_psql current-postgres "$current_pg_password" \
    "SELECT jsonb_build_object('release_id',d.release_id::text,'field',d.field_key,'value',COALESCE(d.selected_value #>> '{}',''),'source',d.selected_source,'confidence',d.confidence,'action',d.action,'rule_id',d.rule_id)::text FROM release_field_decisions d ORDER BY d.release_id,d.field_key;" \
    > "$raw_dir/${prefix}-decisions.ndjson" || die "当前字段证据导出失败"
  compose_psql current-postgres "$current_pg_password" \
    "SELECT jsonb_build_object('release_id',e.release_id::text,'candidate_kind',e.candidate_kind,'rule_id',e.rule_id,'source_refs',e.source_refs,'reason',e.reason)::text FROM release_grouping_evidence e ORDER BY e.release_id;" \
    > "$raw_dir/${prefix}-grouping.ndjson" || die "当前归组证据导出失败"
  compose_psql current-postgres "$current_pg_password" \
    "SELECT jsonb_build_object('release_id',c.release_id::text,'role',c.role,'name',c.name)::text FROM release_credits c ORDER BY c.release_id,c.position,c.role,c.name;" \
    > "$raw_dir/${prefix}-credits.ndjson" || die "当前署名导出失败"
  compose_psql current-postgres "$current_pg_password" \
    "SELECT jsonb_build_object('track_id',c.track_id::text,'role',c.role,'name',c.name)::text FROM track_credits c ORDER BY c.track_id,c.position,c.role,c.name;" \
    > "$raw_dir/${prefix}-track-credits.ndjson" || die "当前逐轨署名导出失败"
  compose_psql current-postgres "$current_pg_password" \
    "SELECT jsonb_build_object('kind',d.kind,'reason',CASE WHEN d.kind='cue_reference' THEN CASE WHEN d.message LIKE 'missing FILE reference:%' THEN 'missing_reference' WHEN d.message LIKE 'unable to stat FILE reference:%' THEN 'stat_failure' WHEN d.message IN ('empty or NUL FILE reference','empty FILE reference') THEN 'empty_reference' WHEN d.message='unsafe absolute FILE reference' THEN 'absolute_reference' WHEN d.message='CUE file is outside containment root' THEN 'cue_outside_root' WHEN d.message='unsafe FILE path traversal' THEN 'path_traversal' WHEN d.message LIKE 'FILE reference is not a regular file:%' THEN 'non_regular_reference' WHEN d.message LIKE 'unable to resolve FILE symlink:%' THEN 'symlink_resolution' WHEN d.message LIKE 'FILE symlink escapes containment root:%' THEN 'symlink_escape' WHEN d.message LIKE 'non-monotonic INDEX 01 in FILE reference:%' THEN 'non_monotonic_index' WHEN d.message LIKE 'missing INDEX 01 for track %' THEN 'missing_index' ELSE 'other' END ELSE '' END)::text FROM scan_diagnostics d WHERE d.scan_run_id = '$scan_id'::uuid ORDER BY d.id;" \
    > "$raw_dir/${prefix}-diagnostics.ndjson" || die "当前诊断导出失败"
  compose_psql current-postgres "$current_pg_password" \
    "SELECT jsonb_build_object('attention_count',COALESCE((SELECT COUNT(*) FROM release_field_decisions WHERE action='uncertain_apply'),0))::text;" \
    > "$raw_dir/${prefix}-summary.ndjson" || die "当前摘要导出失败"
}

build_snapshot() {
  local implementation="$1" corpus_digest="$2" code_hash="$3" schema_digest="$4" prefix="$5" output="$6"
  python3 - "$implementation" "$corpus_digest" "$code_hash" "$schema_digest" "$raw_dir" "$prefix" "$output" <<'PY' 2>/dev/null || die "canonical snapshot 构建失败"
import hashlib
import json
import os
import posixpath
import sys

implementation, corpus_digest, code_hash, schema_digest, raw_dir, prefix, output = sys.argv[1:]

def load(name):
    path = os.path.join(raw_dir, f"{prefix}-{name}.ndjson")
    values = []
    if not os.path.exists(path):
        return values
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if line:
                values.append(json.loads(line))
    return values

def digest(value):
    return hashlib.sha256(value.encode("utf-8", "surrogatepass")).hexdigest()

def slash(value):
    if not value:
        return ""
    return posixpath.normpath(str(value).replace("\\", "/"))

def relative(value):
    value = slash(value)
    if value == ".":
        return ""
    if value.startswith("/music/"):
        return value[7:]
    if value == "/music":
        return ""
    # V0 location 已是相对路径；不得转为小写或折叠空格。
    return value.lstrip("/")

def source_key(row):
    source_kind = str(row.get("source_kind") or "physical")
    rel = relative(row.get("relative_path") or row.get("file_path") or row.get("source_identity"))
    if source_kind == "cue_virtual":
        sheet = relative(row.get("cue_sheet_path") or "")
        parent = relative(row.get("cue_parent_relative_path") or row.get("parent_file_path") or rel)
        index = row.get("cue_index_frames")
        if index is None:
            index = row.get("position") or 0
        return digest("cue\0%s\0%s\0%s\0%s" % (sheet, parent, row.get("position") or 0, index))
    return digest("source\0" + rel)

releases_raw = load("releases")
media_raw = load("media")
tracks_raw = load("tracks")
decisions = load("decisions")
grouping = load("grouping")
credits = load("credits")
track_credits = load("track-credits")
diagnostics_raw = load("diagnostics")
summary_rows = load("summary")

track_credits_by_id = {}
for row in track_credits:
    track_id = str(row.get("track_id") or "")
    role = str(row.get("role") or "")
    name = str(row.get("name") or "")
    if not track_id or role not in {"composer", "conductor", "performer", "producer"} or not name:
        raise ValueError("invalid track credit")
    track_credits_by_id.setdefault(track_id, []).append({"role": role, "name": name})

media_by_id = {str(row.get("id")): row for row in media_raw}
release_by_id = {str(row.get("id")): row for row in releases_raw}
tracks_by_medium = {}
track_models = []
physical_sources_by_release = {}
for row in tracks_raw:
    source = source_key(row)
    medium_id = str(row.get("medium_id") or "")
    medium_row = media_by_id.get(medium_id)
    if medium_row is None:
        raise ValueError("track has no medium")
    release_id = str(medium_row.get("release_id") or "")
    if release_id not in release_by_id:
        raise ValueError("track has no release")
    source_kind = str(row.get("source_kind") or "physical")
    if source_kind == "file":
        source_kind = "physical"
    if source_kind == "cue_virtual":
        physical_path = relative(row.get("cue_parent_relative_path") or row.get("parent_file_path") or "")
    else:
        physical_path = relative(row.get("relative_path") or row.get("file_path") or row.get("source_identity") or "")
    if not physical_path:
        raise ValueError("track has no physical file identity")
    parent_source = digest("source\0" + physical_path)
    physical_sources_by_release.setdefault(release_id, set()).add(parent_source)
    track_model = {
        "key": source,
        "medium_key": medium_id,
        "source_key": source,
        "parent_source_key": parent_source,
        "position": int(row.get("position") or 0),
        "title": str(row.get("title") or ""),
        "artist": str(row.get("artist") or ""),
        "source_kind": source_kind,
        "fields": {},
        "credits": track_credits_by_id.pop(str(row.get("id") or ""), []),
        "_physical_path": physical_path,
        "_release_id": release_id,
    }
    for field in (
        "duration_ms", "codec", "sample_rate", "channels", "bitrate", "bit_depth",
        "cue_index_frames", "cue_end_frames", "cue_isrc",
    ):
        value = row.get(field)
        if value is not None and value != "":
            track_model["fields"][field] = str(value)
    tracks_by_medium.setdefault(medium_id, []).append((row, track_model))

if track_credits_by_id:
    raise ValueError("track credit has no exported track")

medium_models = []
release_medium_keys = {}
for row in media_raw:
    release_id = str(row.get("release_id") or "")
    track_models_for_medium = []
    for track_row, track_model in tracks_by_medium.get(str(row.get("id")), []):
        track_model["medium_key"] = "pending"
        track_models_for_medium.append((track_row, track_model))
    medium_models.append((row, track_models_for_medium))

release_key_by_id = {}
for row in releases_raw:
    release_id = str(row.get("id") or "")
    sources = sorted(physical_sources_by_release.get(release_id, set()))
    fallback = relative(row.get("relative_directory") or row.get("folder_path") or release_id)
    release_key_by_id[release_id] = digest("release\0" + "\0".join(sources if sources else [digest("source\0" + fallback)]))

medium_models_out = []
for row, pairs in medium_models:
    release_id = str(row.get("release_id") or "")
    release_key = release_key_by_id.get(release_id, digest("release\0" + release_id))
    position = int(row.get("position") or 0)
    medium_key = digest("medium\0%s\0%s" % (release_key, position))
    track_keys = []
    for track_row, track_model in pairs:
        track_model["medium_key"] = medium_key
        track_keys.append(track_model["key"])
        track_model["_release_key"] = release_key
    formats = sorted({str(item[1].get("fields", {}).get("codec") or "").strip().lower() for item in pairs} - {""})
    medium_models_out.append({
        "key": medium_key,
        "release_key": release_key,
        "position": position,
        # 当前 schema 没有独立 format 列；统一 codec 的 Medium 可从已持久化 Track
        # facts 无损映射。混合或未知时保留空值，绝不猜测。
        "title": "",
        "format": formats[0] if len(formats) == 1 else "",
        "track_keys": track_keys,
    })

decision_by_release = {}
for row in decisions:
    decision_by_release.setdefault(str(row.get("release_id") or ""), []).append({
        "field": str(row.get("field") or ""), "value": str(row.get("value") or ""),
        "source": str(row.get("source") or ""),
        "confidence": str(row.get("confidence") or ""), "action": str(row.get("action") or ""),
        "rule_id": str(row.get("rule_id") or ""),
    })
credits_by_release = {}
for row in credits:
    credits_by_release.setdefault(str(row.get("release_id") or ""), []).append({"role": str(row.get("role") or ""), "name": str(row.get("name") or "")})
grouping_by_release = {}
for row in grouping:
    grouping_by_release[str(row.get("release_id") or "")] = row

release_models = []
for row in releases_raw:
    release_id = str(row.get("id") or "")
    release_key = release_key_by_id.get(release_id, digest("release\0" + release_id))
    media_for_release = [item for item in medium_models_out if item["release_key"] == release_key]
    media_for_release.sort(key=lambda item: (item["position"], item["key"]))
    evidence = decision_by_release.get(release_id, [])
    fields = {item["field"]: item["value"] for item in evidence if item["field"]}
    candidate_kind = str(row.get("candidate_kind") or "")
    if candidate_kind:
        fields["candidate_kind"] = candidate_kind
    barcode = str(row.get("barcode") or "")
    if barcode:
        fields["barcode"] = barcode
    fields["grouping_medium_count"] = str(len(media_for_release))
    fields["grouping_track_count"] = str(sum(len(item["track_keys"]) for item in media_for_release))
    release_models.append({
        "key": release_key,
        "title": str(row.get("title") or ""), "artist": str(row.get("artist") or ""),
        "album_artist": str(row.get("album_artist") or ""), "year": int(row.get("year") or 0),
        "source_type": str(row.get("source_type") or "").strip().lower(),
        "media_type": str(row.get("media_type") or "").strip().lower(),
        "edition": str(row.get("edition") or ""), "label": str(row.get("label") or ""),
        "catalog": str(row.get("catalog") or ""), "genre": str(row.get("genre") or ""),
        "medium_keys": [item["key"] for item in media_for_release],
        "fields": dict(sorted(fields.items())), "credits": credits_by_release.get(release_id, []),
        "evidence": evidence,
    })

track_models_out = []
files_by_key = {}
for _, pairs in medium_models:
    for row, track_model in pairs:
        release_key = track_model.pop("_release_key", None)
        physical_path = track_model.pop("_physical_path", None)
        track_model.pop("_release_id", None)
        if not release_key or not physical_path:
            raise ValueError("track has no release/file mapping")
        track_models_out.append(track_model)
        physical_key = digest("source\0" + physical_path)
        file_model = {
            "key": physical_key,
            "release_key": release_key,
            "source_key": physical_key,
            "media": str(row.get("codec") or "").strip().lower(),
            # 当前 schema 不持久化可靠文件大小；双方 canonical 明确用 0 表示不可比。
            "size": 0,
        }
        previous = files_by_key.get(physical_key)
        if previous is not None and previous != file_model:
            raise ValueError("physical file mapping is inconsistent")
        files_by_key[physical_key] = file_model

diagnostics = {}
for row in diagnostics_raw:
    kind = str(row.get("kind") or "unknown")
    reason = str(row.get("reason") or "")
    key = f"{kind}.{reason}" if reason else kind
    diagnostics[key] = diagnostics.get(key, 0) + 1
attention_count = 0
if summary_rows:
    attention_count = int(summary_rows[0].get("attention_count") or 0)

snapshot = {
    "snapshot_version": 2,
    "implementation": implementation,
    "corpus_digest": corpus_digest,
    "code_hash": code_hash,
    "schema_digest": schema_digest,
    "releases": sorted(release_models, key=lambda item: item["key"]),
    "media": sorted(medium_models_out, key=lambda item: item["key"]),
    "tracks": sorted(track_models_out, key=lambda item: (item["medium_key"], item["position"], item["key"])),
    "files": sorted(files_by_key.values(), key=lambda item: item["key"]),
    "diagnostics": dict(sorted(diagnostics.items())),
    "attention_count": attention_count,
}
with open(output, "w", encoding="utf-8", opener=lambda path, flags: os.open(path, flags, 0o600)) as handle:
    json.dump(snapshot, handle, ensure_ascii=True, sort_keys=True, separators=(",", ":"))
    handle.write("\n")
PY
  chmod 600 -- "$output"
}

write_manifest() {
  local output="$1" v0_snapshot="$2" current_a_snapshot="$3" current_b_snapshot="$4" v0_sqlite="$5" current_dump="$6"
  python3 - "$output" "$v0_snapshot" "$current_a_snapshot" "$current_b_snapshot" "$v0_sqlite" "$current_dump" <<'PY' 2>/dev/null || die "Smoke manifest 构建失败"
import hashlib
import json
import os
import sqlite3
import sys
import urllib.parse

output, v0_path, current_a_path, current_b_path, v0_sqlite, current_dump = sys.argv[1:]

def file_digest(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        while True:
            block = handle.read(1024 * 1024)
            if not block:
                break
            digest.update(block)
    return digest.hexdigest()

def identity(path, expected_implementation):
    with open(path, encoding="utf-8") as handle:
        snapshot = json.load(handle)
    if snapshot.get("snapshot_version") != 2:
        raise ValueError("unexpected snapshot version")
    if snapshot.get("implementation") != expected_implementation:
        raise ValueError("unexpected implementation")
    for field in ("corpus_digest", "code_hash", "schema_digest"):
        value = snapshot.get(field)
        if not isinstance(value, str) or len(value) != 64 or any(character not in "0123456789abcdef" for character in value):
            raise ValueError(f"missing {field}")
    result = {
        "snapshot_version": snapshot["snapshot_version"],
        "implementation": snapshot["implementation"],
        "corpus_digest": snapshot["corpus_digest"],
        "code_hash": snapshot["code_hash"],
        "schema_digest": snapshot["schema_digest"],
        "snapshot_sha256": file_digest(path),
        "counts": {
            "release": len(snapshot.get("releases", [])),
            "medium": len(snapshot.get("media", [])),
            "track": len(snapshot.get("tracks", [])),
            "file": len(snapshot.get("files", [])),
        },
    }
    if expected_implementation == "v0_release_graph_generated_corrected":
        adapter = snapshot.get("adapter_hash")
        if not isinstance(adapter, str) or len(adapter) != 64 or any(character not in "0123456789abcdef" for character in adapter):
            raise ValueError("invalid V0 adapter hash")
        if snapshot.get("generation_mode") != "standalone_scanner":
            raise ValueError("invalid V0 generation mode")
        if snapshot.get("baseline_scope") != "release_graph_only" or snapshot.get("degraded") is not False:
            raise ValueError("invalid V0 baseline scope")
        excluded = snapshot.get("excluded_evidence")
        if excluded != ["local_evidence", "quality_badges", "scan_diagnostics", "production_runtime_status"]:
            raise ValueError("invalid V0 excluded evidence")
        result.update({
            "adapter_hash": adapter,
            "generation_mode": snapshot["generation_mode"],
            "baseline_scope": snapshot["baseline_scope"],
            "degraded": False,
            "excluded_evidence": excluded,
        })
    return result

v0 = identity(v0_path, "v0_release_graph_generated_corrected")
current_a = identity(current_a_path, "current")
current_b = identity(current_b_path, "current")
if len({v0["corpus_digest"], current_a["corpus_digest"], current_b["corpus_digest"]}) != 1:
    raise ValueError("snapshot corpus mismatch")
if current_a["code_hash"] != current_b["code_hash"] or current_a["schema_digest"] != current_b["schema_digest"]:
    raise ValueError("current identity changed between scans")

quoted = urllib.parse.quote(v0_sqlite, safe="/")
connection = sqlite3.connect(f"file:{quoted}?mode=ro&immutable=1", uri=True)
try:
    connection.execute("PRAGMA query_only = ON")
    stored = {str(key): str(value) for key, value in connection.execute(
        "SELECT key,value FROM baseline_manifest ORDER BY key"
    )}
finally:
    connection.close()
for field in (
    "implementation", "generation_mode", "baseline_scope", "corpus_digest",
    "code_hash", "adapter_hash", "schema_digest",
):
    if stored.get(field) != str(v0[field]):
        raise ValueError(f"SQLite identity mismatch: {field}")
if stored.get("degraded") != "false" or stored.get("canonical_sha256") != v0["snapshot_sha256"]:
    raise ValueError("SQLite canonical identity mismatch")
for name, amount in v0["counts"].items():
    if stored.get(f"{name}_count") != str(amount):
        raise ValueError(f"SQLite count mismatch: {name}")
v0["reference_sqlite_sha256"] = file_digest(v0_sqlite)
v0["sqlite_schema_version"] = stored.get("sqlite_schema_version", "")
v0["rows_sha256"] = stored.get("rows_sha256", "")
v0["generated_at"] = stored.get("generated_at", "")

manifest = {
    "manifest_version": 2,
    "corpus_digest": v0["corpus_digest"],
    "v0": v0,
    "current_first": current_a,
    "current_second": current_b,
    "current_catalog_sha256": file_digest(current_dump),
}
descriptor = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(descriptor, "w", encoding="ascii") as handle:
    json.dump(manifest, handle, ensure_ascii=True, sort_keys=True, separators=(",", ":"))
    handle.write("\n")
PY
  chmod 600 -- "$output"
}

count_rows() {
  local file="$1"
  if [[ ! -s "$file" ]]; then
    printf '0'
  else
    wc -l < "$file" | tr -d ' '
  fi
}

validate_rest_release_detail() {
  local detail_file="$1" release_id="$2" prefix="$3"
  python3 - "$detail_file" "$release_id" "$raw_dir" "$prefix" <<'PY' \
    >/dev/null 2>&1 || die "current REST 详情与数据库聚合不一致"
import json
import os
import sys

detail_path, release_id, raw_dir, prefix = sys.argv[1:]

def load(name):
    path = os.path.join(raw_dir, f"{prefix}-{name}.ndjson")
    values = []
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            if line.strip():
                value = json.loads(line)
                if not isinstance(value, dict):
                    raise ValueError("row is not an object")
                values.append(value)
    return values

with open(detail_path, encoding="utf-8") as handle:
    detail = json.load(handle)
if not isinstance(detail, dict) or detail.get("id") != release_id:
    raise ValueError("release identity mismatch")

release_rows = [row for row in load("releases") if str(row.get("id") or "") == release_id]
if len(release_rows) != 1:
    raise ValueError("release row mismatch")
release = release_rows[0]

def nullable_trimmed(value):
    if value is None:
        return None
    value = str(value).strip()
    return value or None

for field in ("edition", "label", "barcode"):
    if field not in detail or detail[field] != nullable_trimmed(release.get(field)):
        raise ValueError(f"{field} mismatch")

medium_ids = {
    str(row.get("id") or "")
    for row in load("media")
    if str(row.get("release_id") or "") == release_id
}
expected_track_ids = {
    str(row.get("id") or "")
    for row in load("tracks")
    if str(row.get("medium_id") or "") in medium_ids and row.get("source_status") == "present"
}
expected_credits = {track_id: [] for track_id in expected_track_ids}
for credit in load("track-credits"):
    track_id = str(credit.get("track_id") or "")
    if track_id in expected_credits:
        expected_credits[track_id].append({
            "role": credit.get("role"),
            "name": credit.get("name"),
        })

actual_credits = {}
media = detail.get("media")
if not isinstance(media, list):
    raise ValueError("media is not an array")
for medium in media:
    if not isinstance(medium, dict) or not isinstance(medium.get("tracks"), list):
        raise ValueError("invalid medium")
    for track in medium["tracks"]:
        if not isinstance(track, dict):
            raise ValueError("invalid track")
        track_id = track.get("id")
        credits = track.get("credits")
        if not isinstance(track_id, str) or not isinstance(credits, list) or track_id in actual_credits:
            raise ValueError("invalid track projection")
        actual_credits[track_id] = credits

if set(actual_credits) != expected_track_ids or actual_credits != expected_credits:
    raise ValueError("track credit aggregate mismatch")
PY
}

rest_sample_current() {
  local phase="$1" prefix="$2" snapshot="$3" page=1 total=0 page_size=100 id
  local database_total snapshot_total unique_total
  local -a ids=() all_ids=()
  while :; do
    request_json "current-${phase}-releases-${page}" GET "$current_url/api/v1/releases?page=$page&page_size=$page_size" 200 "" "" "$current_cookie_jar" "" ""
    jq -e --argjson page "$page" --argjson page_size "$page_size" '
      (.items | type == "array") and
      (.pagination | type == "object") and
      (.pagination.page == $page) and
      (.pagination.page_size == $page_size) and
      (.pagination.total | type == "number" and . >= 0 and floor == .) and
      (.items | length <= $page_size) and
      (.items | all(.id | type == "string" and length > 0))
    ' "$response_file" >/dev/null 2>/dev/null || die "current REST 列表合同无效"
    total="$(json_value "$response_file" '.pagination.total' "当前发行总数")"
    valid_integer "$total" || die "当前 REST 返回无效发行总数"
    mapfile -t ids < <(jq -r '.items[]?.id // empty' "$response_file" 2>/dev/null)
    all_ids+=("${ids[@]}")
    if ((${#ids[@]} > 0)); then
      for id in "${ids[@]}"; do
        is_uuid "$id" || die "当前 REST 返回无效 release ID"
      done
      for id in "${ids[0]}" "${ids[${#ids[@]}-1]}"; do
        [[ -n "$id" ]] || continue
        request_json "current-${phase}-release-detail" GET "$current_url/api/v1/releases/$id" 200 "" "" "$current_cookie_jar" "" ""
        validate_rest_release_detail "$response_file" "$id" "$prefix"
        request_json "current-${phase}-release-evidence" GET "$current_url/api/v1/releases/$id/evidence" 200 "" "" "$current_cookie_jar" "" ""
      done
    fi
    ((page * page_size >= total)) && break
    ((page++))
  done
  database_total="$(compose_psql current-postgres "$current_pg_password" "
    SELECT COUNT(*)::text
    FROM releases
    WHERE EXISTS (
      SELECT 1
      FROM tracks visible_tracks
      JOIN media visible_media ON visible_media.id = visible_tracks.medium_id
      WHERE visible_media.release_id = releases.id
        AND visible_tracks.source_status = 'present'
    );
  " | tail -n 1 || true)"
  snapshot_total="$(jq -er '.releases | length' "$snapshot" 2>/dev/null || true)"
  valid_integer "$database_total" && valid_integer "$snapshot_total" || die "无法核对 current REST 发行总数"
  [[ "$total" == "$database_total" && "$total" == "$snapshot_total" ]] || die "current REST、数据库与 canonical 发行总数不一致"
  ((${#all_ids[@]} == total)) || die "current REST 分页未覆盖全部发行"
  if ((${#all_ids[@]} > 0)); then
    unique_total="$(printf '%s\n' "${all_ids[@]}" | sort -u | wc -l | tr -d ' ')"
    [[ "$unique_total" == "$total" ]] || die "current REST 分页包含重复发行"
  fi
  current_rest_total="$total"
}

cleanup() {
  local rc=$?
  [[ "$cleanup_done" == 1 ]] && return "$rc"
  cleanup_done=1
  trap - EXIT INT TERM
  rm -f -- "$current_cookie_jar" 2>/dev/null || true
  if [[ "$success" != 1 ]]; then
    if [[ -n "$failure_reason_file" && ! -e "$failure_reason_file" ]]; then
      write_failure_reason "执行阶段异常退出：$runner_stage"
    fi
    # 失败结果不是可用基准。删除本 runner 生成的全部 HTTP、snapshot、dump、
    # comparison 和 manifest；父入口仍保留 marker 以完成精确 project 清理。
    [[ -n "$runner_dir" && "$runner_dir" == "$artifacts_dir/runner" && -d "$runner_dir" ]] && rm -rf -- "$runner_dir" 2>/dev/null || true
    [[ -n "$report_file" && "$report_file" == "$artifacts_dir/smoke-result.md" ]] && rm -f -- "$report_file" 2>/dev/null || true
  else
    # cookie jar 含 current session，SQL 行导出含私人 metadata。成功保留运行时只留下
    # 显式审计产物；--keep-artifacts 输出中绝不能残留凭据。
    [[ -n "$raw_dir" && "$raw_dir" == "$runner_dir/raw" && -d "$raw_dir" ]] && rm -rf -- "$raw_dir" 2>/dev/null || true
    if [[ -n "${v0_rows:-}" && "$v0_rows" == "$artifacts_dir/data/v0/v0-rows.ndjson" ]]; then
      rm -f -- "$v0_rows" 2>/dev/null || true
    fi
    [[ -n "$runner_dir" ]] && rm -f -- "$runner_dir/current-catalog.dump" 2>/dev/null || true
  fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# 若 corpus 在首个服务启动前发生变化，则失败并关闭。
runner_stage="前置资产摘要"
tree_summary "$music_root" "$runner_dir/corpus-before.json"
corpus_digest="$(json_value "$runner_dir/corpus-before.json" '.digest' '前置资产摘要')"
corpus_files="$(json_value "$runner_dir/corpus-before.json" '.files' '前置资产摘要')"
corpus_bytes="$(json_value "$runner_dir/corpus-before.json" '.bytes' '前置资产摘要')"

v0_code_hash="$(sha256sum -- "$v0_archive" 2>/dev/null | awk '{print $1}')"
[[ "$v0_code_hash" == "$EXPECTED_V0_SHA256" ]] || die "V0 归档哈希在运行前发生变化"
v0_rows="$artifacts_dir/data/v0/v0-rows.ndjson"
[[ ! -e "$v0_rows" && ! -L "$v0_rows" ]] || die "V0 exporter 输出目标已存在"

runner_stage="V0 standalone scanner"
wait_v0_exporter
v0_first_seconds=$((scan_end_epoch - scan_start_epoch))
[[ -f "$v0_rows" && ! -L "$v0_rows" && -s "$v0_rows" ]] || die "V0 exporter 未生成完整 rows"
chmod 600 -- "$v0_rows" || die "无法限制 V0 rows 权限"

# normalized SQLite 与 canonical JSON 是 current 启动前的强制门禁。任何一步失败，
# runner 直接退出，父入口只清理本轮 project，不会启动 current 或读取其数据库。
runner_stage="V0 normalized SQLite"
[[ -f "$ROOT_DIR/scripts/v0_reference_sqlite.py" && ! -L "$ROOT_DIR/scripts/v0_reference_sqlite.py" ]] || die "V0 SQLite writer 无效"
python3 "$ROOT_DIR/scripts/v0_reference_sqlite.py" build \
  --rows "$v0_rows" \
  --database "$runner_dir/v0-reference.sqlite" \
  --snapshot "$runner_dir/v0-snapshot.json" \
  --corpus-digest "$corpus_digest" \
  --code-hash "$v0_code_hash" \
  --adapter-hash "$v0_adapter_sha256" \
  >/dev/null 2>/dev/null || die "V0 normalized SQLite/canonical 校验失败"
[[ -f "$runner_dir/v0-reference.sqlite" && ! -L "$runner_dir/v0-reference.sqlite" ]] || die "V0 normalized SQLite 缺失"
[[ -f "$runner_dir/v0-snapshot.json" && ! -L "$runner_dir/v0-snapshot.json" ]] || die "V0 canonical snapshot 缺失"

runner_stage="V0 后资产摘要"
tree_summary "$music_root" "$runner_dir/corpus-after-v0.json"
[[ "$(json_value "$runner_dir/corpus-after-v0.json" '.digest' 'V0 后资产摘要')" == "$corpus_digest" ]] || die "V0 扫描期间检测到资产变化"

# 依次执行 current setup/login/root/scan，然后做一次完整幂等重扫。
runner_stage="current 隔离启动"
compose up -d --wait current >/dev/null 2>/dev/null || die "current 隔离服务启动失败"
validate_current_isolation
runner_stage="current 健康检查"
wait_health current "$current_url" /healthz

# 生成的凭据不会离开进程环境或原始请求文件；每次调用后立即删除临时请求体。
admin_password="$(openssl rand -hex 24 2>/dev/null || true)"
[[ -n "$admin_password" ]] || die "无法生成管理员凭据"

runner_stage="current setup"
request_json current-setup POST "$current_url/api/v1/setup/admin" 201 "" "$current_origin" "$current_cookie_jar" "" \
  "{\"username\":\"$admin_username\",\"password\":\"$admin_password\"}"
runner_stage="current login"
request_json current-login POST "$current_url/api/v1/auth/login" 200 "" "$current_origin" "$current_cookie_jar" "" \
  "{\"username\":\"$admin_username\",\"password\":\"$admin_password\"}"
request_json current-me GET "$current_url/api/v1/auth/me" 200 "" "" "$current_cookie_jar" "" ""
runner_stage="current library root"
request_json current-library-root POST "$current_url/api/v1/library-roots" 201 "" "$current_origin" "$current_cookie_jar" "smoke-root-1" '{"path":"/music"}'
request_json current-roots GET "$current_url/api/v1/library-roots" 200 "" "" "$current_cookie_jar" "" ""
runner_stage="current 首轮扫描"
request_json current-scan-trigger-a POST "$current_url/api/v1/scans" 202 "" "$current_origin" "$current_cookie_jar" "" '{}'
current_scan_id="$(json_value "$response_file" '.id' '当前首轮 scan ID')"
is_uuid "$current_scan_id" || die "当前首轮 scan ID 无效"
poll_current_scan "$current_scan_id" current-first
current_first_seconds=$((scan_end_epoch - scan_start_epoch))
request_json current-diagnostics-a GET "$current_url/api/v1/scans/$current_scan_id/diagnostics" 200 "" "" "$current_cookie_jar" "" ""
scan_diagnostics_file="$response_file"
runner_stage="current 首轮导出"
export_current_rows "$current_scan_id" current-a
current_schema_version="$(read_schema_version current-postgres "$current_pg_password")"
valid_integer "$current_schema_version" || die "current migration schema 版本无效"
current_schema_digest="$(migration_schema_digest "$ROOT_DIR/backend/migrations")"
[[ "$current_schema_digest" =~ ^[0-9a-f]{64}$ ]] || die "current migration schema 摘要无效"
current_code_hash="$current_code_sha256"
build_snapshot current "$corpus_digest" "$current_code_hash" "$current_schema_digest" current-a "$runner_dir/current-snapshot-a.json"
compose_dump current-postgres "$current_pg_password" "$runner_dir/current-catalog.dump" \
  --table=releases --table=media --table=tracks --table=release_field_decisions \
  --table=release_grouping_evidence --table=release_credits --table=track_credits \
  --table=scan_diagnostics
rest_sample_current first current-a "$runner_dir/current-snapshot-a.json"
current_first_total="$current_rest_total"

runner_stage="current 重扫"
request_json current-scan-trigger-b POST "$current_url/api/v1/scans" 202 "" "$current_origin" "$current_cookie_jar" "" '{}'
current_scan_id_b="$(json_value "$response_file" '.id' '当前二轮 scan ID')"
is_uuid "$current_scan_id_b" || die "当前二轮 scan ID 无效"
poll_current_scan "$current_scan_id_b" current-second
current_second_seconds=$((scan_end_epoch - scan_start_epoch))
request_json current-diagnostics-b GET "$current_url/api/v1/scans/$current_scan_id_b/diagnostics" 200 "" "" "$current_cookie_jar" "" ""
runner_stage="current 重扫导出"
export_current_rows "$current_scan_id_b" current-b
build_snapshot current "$corpus_digest" "$current_code_hash" "$current_schema_digest" current-b "$runner_dir/current-snapshot-b.json"
rest_sample_current second current-b "$runner_dir/current-snapshot-b.json"
current_second_total="$current_rest_total"

runner_stage="最终资产摘要"
tree_summary "$music_root" "$runner_dir/corpus-after.json"
corpus_after_digest="$(json_value "$runner_dir/corpus-after.json" '.digest' '后置资产摘要')"
[[ "$corpus_after_digest" == "$corpus_digest" ]] || die "扫描期间检测到资产变化"

# comparator 是从本工作树显式构建的可执行文件。A/B 在 canonical 语义层必须逐字节
# 等价；V0/current 仅保留脱敏报告，以便处置每个已分类差异。
runner_stage="current A/B 对照"
"$comparator" compare \
  --expected "$runner_dir/current-snapshot-a.json" \
  --actual "$runner_dir/current-snapshot-b.json" \
  --output "$runner_dir/current-ab-comparison.json" \
  --fail-on-diff >/dev/null 2>/dev/null || die "当前两轮 canonical comparator 检测到差异"
runner_stage="V0/current 对照"
"$comparator" compare \
  --expected "$runner_dir/v0-snapshot.json" \
  --actual "$runner_dir/current-snapshot-a.json" \
  --output "$runner_dir/v0-current-comparison.json" \
  --fail-on-category current_regression \
  --fail-on-category unclassified \
  >/dev/null 2>/dev/null || die "V0/current canonical comparator 检测到未处置回归、未知分类或执行失败"

current_ab_diff_count="$(jq -er '.differences | length' "$runner_dir/current-ab-comparison.json" 2>/dev/null)" || die "无法读取当前幂等比较结果"
v0_current_diff_count="$(jq -er '.differences | length' "$runner_dir/v0-current-comparison.json" 2>/dev/null)" || die "无法读取 V0/current 比较结果"
v0_current_counts="$(jq -cS '.counts // {}' "$runner_dir/v0-current-comparison.json" 2>/dev/null)" || die "无法读取 V0/current 差异分类"
[[ "$current_ab_diff_count" == 0 ]] || die "当前两轮 canonical comparator 检测到差异"
jq -e '
  (.differences | type == "array") and
  (.counts | type == "object") and
  ([.differences[].category] - [
    "current_regression",
    "schema_mapping_gap",
    "capability_gap",
    "historical_corpus_drift",
    "intentional_contract_difference"
  ] | length == 0) and
  ((.differences | group_by(.category) | map({key: .[0].category, value: length}) | from_entries) == .counts)
' "$runner_dir/v0-current-comparison.json" >/dev/null 2>&1 || die "V0/current 差异分类未知或计数不闭合"
v0_current_regression_count="$(jq -er '(.counts.current_regression // 0) | select(type == "number" and floor == . and . >= 0)' "$runner_dir/v0-current-comparison.json" 2>/dev/null)" || die "无法读取 current regression 计数"
v0_current_unclassified_count=0
[[ "$v0_current_regression_count" == 0 ]] || die "V0/current 仍存在 current regression"

runner_stage="manifest 与报告"
manifest_file="$runner_dir/manifest.json"
write_manifest "$manifest_file" \
  "$runner_dir/v0-snapshot.json" \
  "$runner_dir/current-snapshot-a.json" \
  "$runner_dir/current-snapshot-b.json" \
  "$runner_dir/v0-reference.sqlite" \
  "$runner_dir/current-catalog.dump"
manifest_digest="$(sha256sum "$manifest_file" | awk '{print $1}')"
[[ "$manifest_digest" =~ ^[0-9a-f]{64}$ ]] || die "Smoke manifest 摘要无效"

v0_release_count="$(jq '.releases | length' "$runner_dir/v0-snapshot.json" 2>/dev/null)"
v0_medium_count="$(jq '.media | length' "$runner_dir/v0-snapshot.json" 2>/dev/null)"
v0_track_count="$(jq '.tracks | length' "$runner_dir/v0-snapshot.json" 2>/dev/null)"
v0_file_count="$(jq '.files | length' "$runner_dir/v0-snapshot.json" 2>/dev/null)"
current_a_release_count="$(jq '.releases | length' "$runner_dir/current-snapshot-a.json" 2>/dev/null)"
current_a_medium_count="$(jq '.media | length' "$runner_dir/current-snapshot-a.json" 2>/dev/null)"
current_a_track_count="$(jq '.tracks | length' "$runner_dir/current-snapshot-a.json" 2>/dev/null)"
current_a_file_count="$(jq '.files | length' "$runner_dir/current-snapshot-a.json" 2>/dev/null)"
current_b_release_count="$(jq '.releases | length' "$runner_dir/current-snapshot-b.json" 2>/dev/null)"
current_b_medium_count="$(jq '.media | length' "$runner_dir/current-snapshot-b.json" 2>/dev/null)"
current_b_track_count="$(jq '.tracks | length' "$runner_dir/current-snapshot-b.json" 2>/dev/null)"
current_b_file_count="$(jq '.files | length' "$runner_dir/current-snapshot-b.json" 2>/dev/null)"
current_a_diagnostics="$(jq -cS '.diagnostics // {}' "$runner_dir/current-snapshot-a.json" 2>/dev/null)"
current_b_diagnostics="$(jq -cS '.diagnostics // {}' "$runner_dir/current-snapshot-b.json" 2>/dev/null)"

cat > "$report_file" <<EOF
# 真实音乐库 Smoke 结果

> 本报告只保存聚合计数、身份摘要和终态，不包含路径、凭据或逐项媒体 metadata。

## 执行入口

\`ROOMUSIC_REAL_LIBRARY_SMOKE=1 ./scripts/real-library-smoke.sh --music-root <真实音乐根> --v0-archive <固定 V0 归档>\`

- 运行身份：${compose_project}
- V0 归档 SHA-256：${v0_code_hash}
- V0 standalone adapter SHA-256：${v0_adapter_sha256}
- corpus 文件数：${corpus_files}
- corpus 总字节数：${corpus_bytes}
- corpus 摘要前/后：${corpus_digest} / ${corpus_after_digest}
- 资产前后是否一致：是
- manifest SHA-256：${manifest_digest}

## 扫描终态

| 实现 | 首次终态 | 首次耗时（秒） | 二次终态 | 二次耗时（秒） |
| --- | --- | ---: | --- | ---: |
| V0 standalone corrected | completed | ${v0_first_seconds} | 不适用 | 不适用 |
| current | succeeded | ${current_first_seconds} | succeeded | ${current_second_seconds} |

## Canonical 聚合

| 快照 | Release | Medium | Track | File |
| --- | ---: | ---: | ---: | ---: |
| V0 standalone corrected | ${v0_release_count} | ${v0_medium_count} | ${v0_track_count} | ${v0_file_count} |
| current A | ${current_a_release_count} | ${current_a_medium_count} | ${current_a_track_count} | ${current_a_file_count} |
| current B | ${current_b_release_count} | ${current_b_medium_count} | ${current_b_track_count} | ${current_b_file_count} |

## Current REST 抽查与对照

- current 首轮列表总数：${current_first_total}
- current 二轮列表总数：${current_second_total}
- current A/B canonical 差异数：${current_ab_diff_count}
- current 首轮诊断分类：${current_a_diagnostics}
- current 二轮诊断分类：${current_b_diagnostics}
- V0/current canonical 差异数：${v0_current_diff_count}
- V0/current 差异分类计数：${v0_current_counts}
- current regression：${v0_current_regression_count}
- 未分类差异：${v0_current_unclassified_count}

## 处置

- V0 输出标记：v0_release_graph_generated_corrected
- V0 生成方式：standalone_scanner；范围：release_graph_only；degraded=false
- V0 排除范围：local evidence、quality badges、scan diagnostics、production runtime status
- 当前 A/B 幂等差异：已由 canonical comparator 判定为 0
- V0/current 差异分类：全部进入有证据的窄分类；无 current regression 或未知分类
- capability gap：保持在已批准范围外；intentional contract difference：按当前合同接受
- schema mapping gap：按两端字段所有权和生成时点的显式映射处置
- 资产变更：无
EOF
chmod 600 -- "$report_file"
success=1
printf 'real-library-smoke-runner: smoke 完成（脱敏报告已写入产物目录）\n' >&2
exit 0
