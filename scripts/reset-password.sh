#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOMUSIC_ENV_FILE:-$ROOT_DIR/.env}"
[[ -f "$ENV_FILE" ]] || { echo "缺少生产配置：$ENV_FILE" >&2; exit 1; }
set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a
tmpdir=$(mktemp -d)
trap 'rm -rf -- "$tmpdir"' EXIT
go -C "$ROOT_DIR/backend" build -o "$tmpdir/roomusic" ./cmd/roomusic
status=0
"$tmpdir/roomusic" reset-password "$@" || status=$?
exit "$status"
