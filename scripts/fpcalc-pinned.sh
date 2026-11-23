#!/usr/bin/env bash
# 从上游发布取得固定版本的静态 fpcalc，标准输出只写可执行文件路径。
set -euo pipefail

version=1.6.2
base_url="https://github.com/acoustid/chromaprint/releases/download/v$version"
declare -A digests=(
  [x86_64]=c968ad4a0821592a79b158fb2e1f46cbe0a22a0a1dbc0743c67d68e75d15d2bb
  [arm64]=3aa1498126ed272d805bc9d081c77965c9e1e1a0891bb112c13b56ef2ad47bad
)
archive_name() { printf 'chromaprint-fpcalc-%s-linux-%s' "$version" "$1"; }
if [[ "${1:-}" == --pins ]]; then
  for arch in x86_64 arm64; do
    printf '%s %s/%s.tar.gz\n' "${digests[$arch]}" "$base_url" "$(archive_name "$arch")"
  done
  exit 0
fi

case "$(uname -m)" in
  x86_64) arch=x86_64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "上游没有为本机架构发布 fpcalc" >&2; exit 1 ;;
esac
name="$(archive_name "$arch")"
url="$base_url/$name.tar.gz"
if [[ "${1:-}" == --url ]]; then
  echo "$url"
  exit 0
fi

cache_dir="${XDG_CACHE_HOME:-$HOME/.cache}/roomusic/fpcalc-$version"
target="$cache_dir/fpcalc"
verified() {
  [[ -x "$1" ]] && [[ "$("$1" -version 2>/dev/null)" == "fpcalc version $version ("* ]]
}
if ! verified "$target"; then
  mkdir -p "$cache_dir"
  staged="$(mktemp -d "$cache_dir/.install.XXXXXX")"
  trap 'rm -rf -- "$staged"' EXIT
  curl --fail --silent --show-error --location --retry 3 --output "$staged/archive" "$url"
  (cd "$staged" && printf '%s  archive\n' "${digests[$arch]}" | sha256sum --check >&2)
  tar -xzf "$staged/archive" -C "$staged" "$name/fpcalc"
  if ! verified "$staged/$name/fpcalc"; then
    echo "fpcalc 版本校验失败，预期 $version" >&2
    exit 1
  fi
  mv -f "$staged/$name/fpcalc" "$target"
fi
printf '%s\n' "$target"
