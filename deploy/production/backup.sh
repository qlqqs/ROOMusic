#!/bin/sh
# PostgreSQL 自定义格式备份；不得把连接串或 pg_dump 的原始错误写入日志。
set -eu
umask 077
backup_dir=/backups
hour=${ROOMUSIC_BACKUP_HOUR:-3}
keep_days=${ROOMUSIC_BACKUP_KEEP_DAYS:-14}
case "$hour" in 0|1|2|3|4|5|6|7|8|9|1[0-9]|2[0-3]) ;; *) echo 'ROOMUSIC_BACKUP_HOUR 必须为 0–23' >&2; exit 2 ;; esac
case "$keep_days" in ''|*[!0-9]*|0*) echo 'ROOMUSIC_BACKUP_KEEP_DAYS 必须为正整数' >&2; exit 2 ;; esac

recent_success() {
  [ -f "$backup_dir/.last-success" ] || return 1
  modified=$(stat -c %Y "$backup_dir/.last-success")
  age=$(( $(date +%s) - modified ))
  [ "$age" -ge 0 ] && [ "$age" -lt "$1" ]
}

# 子 shell 独占临时文件与清理 trap；调用方以显式状态判断重试。
backup_once() (
  mode=$1
  partial=''
  trap 'test -z "$partial" || rm -f -- "$partial"' EXIT
  trap 'exit 1' HUP INT TERM
  stage=prepare
  failed() {
    printf '{"level":"ERROR","event":"backup.failed","stage":"%s","exit_code":%s}\n' "$stage" "$1" >&2
    exit 1
  }
  partial=$(mktemp "$backup_dir/.roomusic-$(date +%Y%m%d-%H%M%S)-$mode-$$.partial.XXXXXX") || failed "$?"
  stage=pg_dump
  pg_dump -Fc -f "$partial" 2>/dev/null || failed "$?"
  stage=pg_restore_list
  pg_restore --list "$partial" >/dev/null 2>&1 || failed "$?"
  suffix=''
  [ "$mode" != now ] || suffix=-manual
  stage=publish
  # 硬链接原子发布且不覆盖；同一秒并发的手动备份顺延命名。
  while :; do
    filename="roomusic-$(date +%Y%m%d-%H%M%S)$suffix.dump"
    if ln "$partial" "$backup_dir/$filename" 2>/dev/null; then break; fi
    [ -e "$backup_dir/$filename" ] || failed 1
    sleep 1
  done
  rm -f -- "$partial" || failed "$?"
  partial=''
  touch "$backup_dir/.last-success" || failed "$?"
  bytes=$(stat -c %s "$backup_dir/$filename") || failed "$?"
  printf '{"level":"INFO","event":"backup.completed","file":"%s","bytes":%s}\n' "$filename" "$bytes"
  stage=retention
  # -mmin 按分钟计时，避免 -mtime 的整天取整额外保留一天。
  find "$backup_dir" -maxdepth 1 -type f -name 'roomusic-*.dump' -mmin "+$((keep_days * 1440))" -exec rm -f -- '{}' ';' || failed "$?"
)

case "${1:-loop}" in
  now) backup_once now ;;
  check) recent_success 93600 ;;
  loop)
    sleeper=''
    trap 'test -z "$sleeper" || kill "$sleeper" 2>/dev/null; exit 0' INT TERM
    next_retry=0
    first=true
    while :; do
      now=$(date +%s)
      due=false
      if "$first"; then
        recent_success 86400 || due=true
        first=false
      fi
      today=$(date +%Y%m%d)
      last_day=''
      if [ -f "$backup_dir/.last-success" ]; then
        last_day=$(date -r "$backup_dir/.last-success" +%Y%m%d)
      fi
      local_hour=$(date +%H | sed 's/^0//')
      [ "${local_hour:-0}" -lt "$hour" ] || [ "$last_day" = "$today" ] || due=true
      # 首次补备份若失败，即使尚未到当天计划时刻，也要每小时重试。
      [ "$next_retry" -eq 0 ] || due=true
      if "$due" && [ "$now" -ge "$next_retry" ]; then
        if backup_once loop; then next_retry=0; else next_retry=$(( $(date +%s) + 3600 )); fi
      fi
      sleep 600 &
      sleeper=$!
      wait "$sleeper"
      sleeper=''
    done
    ;;
  *) echo '用法：backup.sh loop|now|check' >&2; exit 2 ;;
esac
