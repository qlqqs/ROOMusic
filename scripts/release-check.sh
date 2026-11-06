#!/usr/bin/env bash
# 发布前只读门禁：只更新远端引用，不修改工作树、不创建标签、不推送。
set -euo pipefail
export LC_ALL=C
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
fail() { printf '发布检查失败：%s\n' "$1" >&2; exit 1; }
[[ $# == 1 ]] || fail '用法：scripts/release-check.sh vX.Y.Z'
version="$1"
[[ "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || fail '版本必须是 vX.Y.Z（不含前导零或预发布后缀）'
[[ "$(git branch --show-current)" == main ]] || fail '当前分支必须是 main'
[[ -z "$(git status --porcelain --untracked-files=all)" ]] || fail '工作区必须干净'
git fetch origin main:refs/remotes/origin/main --tags
git merge-base --is-ancestor origin/main HEAD || fail '远端 main 不是本地 HEAD 的祖先，请先同步'
if git show-ref --verify --quiet "refs/tags/$version"; then fail '版本标签已存在'; fi
latest="$(git tag --list 'v*' | sort -V | tail -n 1)"
if [[ -n "$latest" && "$(printf '%s\n%s\n' "$latest" "$version" | sort -V | tail -n 1)" != "$version" ]]; then
  fail '新版本必须大于所有已有 v* 标签'
fi
# 只检查 HEAD 的 Git 对象；不读取工作树中的 .env 或其他私密文件。
# 首发包含根提交，不能遗漏第一个提交里引入的文件。
base="$(git tag --merged HEAD --list 'v*' | sort -V | tail -n 1)"
if [[ -z "$base" ]]; then base="$(git hash-object -t tree /dev/null)"; fi
python3 - "$base" <<'PY'
import fnmatch
import os
import re
import subprocess
import sys


def git(*args):
    return subprocess.check_output(["git", *args])


base = sys.argv[1]
paths = git("diff", "--no-ext-diff", "--no-textconv", "--name-only", "--diff-filter=ACMRT", "-z", base, "HEAD").split(b"\0")
blocked = []
for raw in filter(None, paths):
    path = os.fsdecode(raw)
    name = path.rsplit("/", 1)[-1]
    example = name == "example.env" or (name.startswith(".env.") and name.endswith(".example"))
    private = not example and (name == ".env" or name.startswith(".env."))
    private |= any(fnmatch.fnmatchcase(name, pattern) for pattern in ("*.pem", "*.key", "*.p12", "id_rsa*", "*.dump"))
    if private:
        blocked.append(path)
if blocked:
    for path in blocked:
        print(f"禁止发布敏感文件：{path!r}:0", file=sys.stderr)
    sys.exit(1)

# 前缀后须有令牌字符，避免把规范里的裸前缀和本脚本的正则声明误判为凭据。
pattern = re.compile(rb"(?:ghp_|github_pat_|gho_|glpat-)[A-Za-z0-9_]+|AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----|xox[baprs]-[A-Za-z0-9-]+|sk-[A-Za-z0-9]{20,}")
hunk = re.compile(rb"^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@")
found = False
for raw in filter(None, paths):
    path = os.fsdecode(raw)
    diff = git("diff", "--no-ext-diff", "--no-textconv", "--text", "--no-renames", "--unified=0", base, "HEAD", "--", path)
    line_number = None
    for line in diff.splitlines():
        match = hunk.match(line)
        if match:
            line_number = int(match[1])
        elif line_number is not None and line.startswith(b"+"):
            if pattern.search(line[1:]):
                print(f"疑似令牌：{path!r}:{line_number}", file=sys.stderr)
                found = True
            line_number += 1
        elif line_number is not None and line.startswith(b" "):
            line_number += 1
if found:
    sys.exit(1)
PY
for file in compose.prod.yaml .env.prod.example; do
  [[ -f "$file" ]] && grep -q '__RELEASE_VERSION__' "$file" || fail "缺少发布占位符：$file"
done
git diff --exit-code HEAD -- backend/cmd/roomusic/web >/dev/null || fail '内嵌前端产物有未提交修改'
[[ -z "$(git ls-files --others --exclude-standard -- backend/cmd/roomusic/web)" ]] || fail '内嵌前端产物有未跟踪文件'
printf '发布检查通过：版本 %s，提交 %s。未打标签、未推送。\n' "$version" "$(git rev-parse HEAD)"
