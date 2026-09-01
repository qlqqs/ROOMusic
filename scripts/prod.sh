#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOMUSIC_ENV_FILE:-$ROOT_DIR/.env}"
[[ -f "$ENV_FILE" ]] || { echo "缺少生产配置：$ENV_FILE" >&2; exit 1; }
set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a
export ROOMUSIC_ENV=production
export ROOMUSIC_HTTP_ADDR="${ROOMUSIC_HTTP_ADDR:-:8080}"
[[ "${ROOMUSIC_SECURE_COOKIES:-}" == "true" ]] || { echo "生产环境必须设置 ROOMUSIC_SECURE_COOKIES=true" >&2; exit 1; }

if [[ "${ROOMUSIC_SKIP_BUILD:-0}" != "1" ]]; then
  (cd "$ROOT_DIR/frontend" && npm run build)
fi
exec go run "$ROOT_DIR/backend/cmd/roomusic"
