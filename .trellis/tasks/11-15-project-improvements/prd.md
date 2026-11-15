# 项目改进意见 1-11 与外置 LRC 歌词

## Goal

落实 2026-11-15 项目检查提出的第 1–11 项改进（第 12 项陈旧任务已于同日归档），并实现本地 `TODO.md` 第一优先的“读取外置 `.lrc` 歌词”。
目标是让配置、文档与实际行为一致，减少合并冲突与无用依赖，降低成员首屏下载量，并让歌词覆盖只有外置 `.lrc` 的曲目。

## 任务地图

| 子任务 | 意见 | 内容 |
| --- | --- | --- |
| `11-15-dev-env-config` | 1、2、6、8 | 删除未用的 Redis 与 Meilisearch；Compose 项目名、端口绑定与文档一致；测试库镜像与生产钉死版本一致；`dev.sh` 崩溃退避 |
| `11-15-backend-hygiene` | 3、7 | `go mod tidy` 并在 CI 检查；HTTP Server 补 `IdleTimeout` |
| `11-15-frontend-route-split` | 4 | 按路由懒加载，管理员页面不进成员首屏 |
| `11-15-web-assets-build` | 5 | 前端产物在 Docker 构建阶段生成后内嵌，仓库不再提交 `backend/cmd/roomusic/web` 产物 |
| `11-15-repo-cleanup` | 9、10、11 | 删除 `.cursor` 并同步技能副本；Trellis 归档不再入库；`frontend-desgin` 改名 |
| `11-15-external-lrc` | TODO | 扫描时只读同名 `.lrc` 文件，与内嵌歌词按裁决的先后关系保存 |

依赖顺序：`web-assets-build` 与 `backend-hygiene` 都改 `ci.yml`，`web-assets-build` 在后；`frontend-route-split` 与 `external-lrc` 改前端源码，
排在 `web-assets-build` 之后合入，就不必再提交重新构建的产物。其余子任务互不依赖。

## 已确认事实

- `compose.dev.yaml` 含 Redis 与 Meilisearch，`.env.dev.example` 注明应用不用，但 `MEILI_MASTER_KEY` 为 `:?` 必填，CI 传占位值；`.mise.toml` 的 `env-up`、`env-logs` 会启动它们，`scripts/dev.sh` 只启动 postgres。
- `docs/development-environment.md` 写开发项目名 `roomusic-compose-dev`、测试 `roomusic-compose-test-` 加随机后缀、开发和测试端口绑 `0.0.0.0`；实际 `compose.dev.yaml` 为 `name: roomusic`、`compose.test.yaml` 为 `name: roomusic-test`（`scripts/test-integration.sh` 用 `-p` 覆盖），两者端口都绑 `127.0.0.1`。
- `backend/go.mod`：`go mod tidy -diff` 有差异，pgx、x/crypto、x/sys、x/term 被标为 `// indirect`。
- `backend/cmd/roomusic/main.go:83` 只设 `ReadHeaderTimeout`。
- 前端 `src` 无 `lazy` 或动态 `import()`；主包 JS 约 597 KB、CSS 约 178 KB。
- 产物由 `frontend/vite.config.ts` 输出到 `backend/cmd/roomusic/web`，`//go:embed web` 内嵌；CI 漂移检查、`scripts/release-check.sh:74-75`、`backend/cmd/roomusic/main_test.go`、`deploy/production/Dockerfile`（注释“不安装 Node”）依赖已提交产物；`scripts/playback-e2e.sh` 自行构建到临时目录。
- `.claude/skills` 与 `.agents/skills` 的 `trellis-brainstorm`、`trellis-update-spec` 内容不一致；`.cursor` 54 个文件。全局约定只使用 Claude 与 Codex。
- `.trellis/tasks/archive` 入库 986 个文件；多份 spec 链接到归档任务文档（如 `.trellis/spec/backend/lyrics-contracts.md`）。Trellis 归档自动提交遇到被忽略路径时只告警并跳过提交（`.trellis/scripts/common/safe_commit.py`）。
- 歌词合同 `.trellis/spec/backend/lyrics-contracts.md` 规定只读内嵌标签、`lyrics_source` 四个来源键、接口 `source` 目前只有 `embedded`，前端无歌词时显示“这首曲目没有内嵌歌词”。

## 裁决记录

- 路线：人工裁决。2026-11-15 用户在提问中选择四项（LRC 外置优先；LRC 编码 UTF-8/16 + GB18030；应用端口 0.0.0.0、数据库 127.0.0.1；归档取消跟踪），详见各子任务 PRD。
- 其余技术取舍（LRC 署名随歌词、匹配规则、`external` 取值，删除 `.cursor`，Redis/Meilisearch 直接删除，构建产物方案，模块路径不改名，管理员对话框暂不拆）由主代理依据调研提出；2026-11-15 用户批准最终规划摘要（“开始实施”），全部按提议执行。

## 执行安排

- 每个子任务独立分支与 worktree（`.claude/worktrees/` 已忽略），完成并验收后依次以 merge 提交合入 `main`，合入后删除分支。
- 合入顺序：`backend-hygiene` → `dev-env-config` → `repo-cleanup` → `web-assets-build` → `frontend-route-split` → `external-lrc`。前三个可并行实现；`frontend-route-split` 与 `external-lrc` 可与 `web-assets-build` 并行实现，合入时变基到其之后。
- 分工按全局约定：后端与脚本由 Codex 子代理实现（`external-lrc`、`web-assets-build` 用 gpt-6-astra，其余用 gpt-6.1-sol），测试由另一个 Codex 子代理编写；前端、文档与 spec 由主代理完成；主代理审阅后提交。
- `repo-cleanup` 取消跟踪归档后，本任务树归档时按新规则手动提交。

## 后续事项（不在本任务）

- 开发数据目录迁到 `temp/dev`（`compose.dev.yaml` 命名卷、`ROOMUSIC_DATA_DIR`）。
- 专辑与艺人详情里的管理员对话框按需加载。

## 跨子任务验收

- [ ] 各子任务验收全部通过，CI 在 `main` 上通过。
- [ ] README、`docs/development-environment.md`、相关 spec 与实际行为一致。
- [ ] 未改动现有实例的数据、配置或容器。
