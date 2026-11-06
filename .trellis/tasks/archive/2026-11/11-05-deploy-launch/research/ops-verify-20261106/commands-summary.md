# 实际执行的命令清单（无密码）

以下命令均在 `/home/qlqq/workspace/ROOMusic` 执行。`E=/tmp/roomusic-ops-verify-20261106`。
逐次 Docker、Git、systemctl 命令与 HTTP 请求清单见 `commands.log`；重复健康轮询未在本摘要展开。SQL 通过标准输入执行，正文保留在 `ops.py`、`drilldb.py`；所有查询以只读事务执行。

## 只读准备

- `cat /home/qlqq/workspace/AGENTS.md`、`cat AGENTS.md`
- `cat .agents/skills/trellis-start/SKILL.md`
- `python3 .trellis/scripts/get_context.py`
- `python3 .trellis/scripts/get_context.py --mode phase`
- `git status --short`、`git rev-parse --short HEAD`
- `compgen -e | grep -E '^(ROOMUSIC_|COMPOSE_)'`（仅变量名；无残留）
- `cat deploy/production/README.md`、`compose.yaml`、`backup.sh`、`restore.sh`、`scripts/production.sh`；并用 `sed` 分段补读。
- `cat .env.prod.example`（仅模板，未读取真实 .env 文件）
- 阅读归档演练 README、父任务 PRD 及 `step-00.py`、`browser-common.cjs`、`step-03.cjs`、`step-04.cjs`、`step-05.cjs`。
- `id`、`docker ps --format ...`、`docker volume ls --format ...`
- 使用 `rg`、`cat`、`sed` 只读查阅相关 handler、迁移、封面组件及派生缓存实现。

## 操作与检查的入口命令

```bash
python3 "$E/ops.py"
python3 "$E/step-a.py"
python3 "$E/step-b-start.py"
node --check "$E/step-b2.cjs"
node "$E/step-b2.cjs"          # 初次假设错误；仅修正 /tmp 脚本后再跑一次
python3 "$E/step-b3.py"
node --check "$E/step-b4-mutate.cjs"
node "$E/step-b4-mutate.cjs"
python3 "$E/step-b4-restore.py"
node --check "$E/step-b4-verify.cjs"
node "$E/step-b4-verify.cjs"    # 首次字段假设错误；修正 /tmp 脚本后重跑
node --check "$E/check-artworks.cjs"
node --check "$E/play-alac.cjs"
node "$E/check-artworks.cjs" before
python3 "$E/cache-stats.py" c1-cache-before-play.json
source /home/qlqq/.local/share/playwright-deps/env.sh
node "$E/play-alac.cjs" before
python3 "$E/cache-stats.py" c1-cache-after-play.json
python3 "$E/step-c2.py"
node --check "$E/check-cover-page.cjs"
node "$E/check-artworks.cjs" lost
python3 "$E/cache-stats.py" c2-cache-empty.json
source /home/qlqq/.local/share/playwright-deps/env.sh
node "$E/check-cover-page.cjs" lost
# node -：调用 browser-common.cjs 的 scan(ctx,'c3')，完整重扫
# python3 -：调用 drilldb.artwork_snapshot、counts，保存重扫前后聚合
node "$E/check-artworks.cjs" after
source /home/qlqq/.local/share/playwright-deps/env.sh
node "$E/check-cover-page.cjs" after
node "$E/play-alac.cjs" after
python3 "$E/cache-stats.py" c3-cache-after-play.json
python3 "$E/step-d.py"
# python3 -：挂载数组按 Destination/Source 归一化，只读记录并发 Git 状态
```

另执行 `node -` 白名单诊断：登录响应状态和 Cookie 属性（不记录值）、歌单/曲目摘要字段名及顺序比较。首次初始化成功后误期待已有会话，诊断用原密码登录成功。每个会话文件仅写入 `/tmp/roomusic-drill/`，清理时删除。

## 关键底层命令（已实际由入口脚本执行）

```bash
P=(docker compose -p roomusic-prod -f deploy/production/compose.yaml --env-file .env.prod)
D=(docker compose -p roomusic-drill -f deploy/production/compose.yaml --env-file /tmp/roomusic-drill/drill.env)
"${P[@]}" restart
kill -KILL 2862416
systemctl is-enabled docker

deploy/production/restore.sh check /home/qlqq/roomusic-backups/roomusic-20261106-005718-manual.dump
"${D[@]}" config --quiet
"${D[@]}" up -d --wait --wait-timeout 300
"${D[@]}" exec -T backup /bin/sh /opt/roomusic/backup.sh now
COMPOSE_PROJECT_NAME=roomusic-drill COMPOSE_ENV_FILES=/tmp/roomusic-drill/drill.env \
  deploy/production/restore.sh check /tmp/roomusic-drill/backups/roomusic-20261106-125331-manual.dump
# production 确认输入为 roomusic-drill；不是 roomusic-prod。
COMPOSE_PROJECT_NAME=roomusic-drill COMPOSE_ENV_FILES=/tmp/roomusic-drill/drill.env \
  deploy/production/restore.sh production /tmp/roomusic-drill/backups/roomusic-20261106-125331-manual.dump
COMPOSE_PROJECT_NAME=roomusic-drill COMPOSE_ENV_FILES=/tmp/roomusic-drill/drill.env \
  deploy/production/restore.sh check /tmp/roomusic-drill/backups/roomusic-20261106-125409-manual.dump

"${D[@]}" stop app
"${D[@]}" rm -f app
docker volume inspect -f '{{json .}}' roomusic-drill_app-data
docker volume rm roomusic-drill_app-data
"${D[@]}" up -d --no-deps --wait --wait-timeout 300 app
"${D[@]}" down --volumes --remove-orphans
# Python shutil.rmtree('/tmp/roomusic-drill')
```

## 业务 HTTP 验证

通过 Playwright APIRequestContext / Chromium 执行，URL 均为本机演练端口 `18090`；正式端口 `8090` 只做 GET。

- 正式：GET `/healthz`、`/readyz`、`/api/v1/setup/status`。
- 演练：POST `/api/v1/setup/admin`、`/auth/login`，GET `/auth/me`。
- POST `/api/v1/library-roots`（`/music`），POST `/api/v1/scans`，轮询 GET `/api/v1/scans/{id}`。
- GET releases/release-groups 及发行详情，在内存选择曲目；真实音乐元数据不保存到证据。
- PUT/DELETE `/api/v1/me/favorites/albums/{id}`，POST `/me/favorites/lookup`。
- POST/DELETE `/api/v1/me/playlists...`，GET 歌单、条目、收藏；歌单建入 3 首歌曲。
- 带 Cookie 的无条件 GET `/api/v1/artworks/{resource_id}`，只保存状态、字节数和 SHA-256，不保存图片。
- Chromium 打开专辑页，真实点击 ALAC 播放按钮，验证派生预检 200、音频 206、原生媒体时间推进，然后暂停。
- Chromium 在删卷未扫描时和重扫后抽查内嵌、文件夹、多碟封面详情页，无截图、不保存页面正文。

未执行全量 Go、前端、集成测试：本轮没有实现代码变更，按用户要求只做对应运维路径验证。
