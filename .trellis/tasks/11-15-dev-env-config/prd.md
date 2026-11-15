# 开发与测试环境配置收敛（意见 1、2、6、8）

## Goal

开发与测试的 Compose、mise 任务、环境示例、脚本和文档说的是同一件事：开发只依赖 PostgreSQL，项目名和端口绑定与文档一致，测试库与生产同一版本，后端反复崩溃时开发脚本不刷屏。

## 裁决

- 端口绑定（2026-11-15 用户选择，人工裁决）：应用端口（Vite、Go 后端）绑 `0.0.0.0`；开发与测试的 PostgreSQL 只绑 `127.0.0.1`。`AGENTS.md` 与 `docs/development-environment.md` 的规则改写为这一表述。
- Redis 与 Meilisearch 直接删除，不保留为 profile（意见 1，用户批准“1-11 一起做”）。
- 本机没有 `roomusic` 开发项目的容器与卷（2026-11-15 只读检查），改项目名不丢开发数据；生产实例 `roomusic-server` 不触碰。

## Requirements

1. 删除 Redis 与 Meilisearch：`compose.dev.yaml` 的两个服务与卷；`.env.dev.example` 的 `REDIS_PORT`、`MEILI_PORT`、`MEILI_MASTER_KEY` 段；`.mise.toml` 的 `env-up` 描述与 `env-logs` 服务列表；`.github/workflows/ci.yml` “Validate Compose configuration” 的 `MEILI_MASTER_KEY`。
2. 同步描述“Compose 含 Redis/Meilisearch”的现状句：`.trellis/spec/guides/product-goals.md:161`、`.trellis/spec/backend/database-guidelines.md:6`、`.trellis/spec/backend/index.md:158`、`.trellis/spec/backend/deployment-environment-contracts.md:21`。只改现状描述，保留“Core 0 不依赖 Redis/Meilisearch”这类约束条款。
3. `compose.dev.yaml` 改为 `name: roomusic-compose-dev`。`compose.test.yaml` 改为 `name: roomusic-compose-test`，`scripts/test-integration.sh` 的默认项目名与校验正则改为 `roomusic-compose-test` 加随机后缀（与文档一致）；其他引用 `roomusic-test` 项目名的脚本与测试同步（如 `scripts/playback-e2e*.sh`、`scripts/tests/`）。
4. `compose.dev.yaml`、`compose.test.yaml` 的 PostgreSQL 镜像改为与 `compose.prod.yaml` 的 `x-postgres-image` 完全相同的钉死字符串；CI 增加一致性检查（沿用现有 `grep -qF` 写法）。
5. `scripts/dev.sh`：后端进程在启动后很短时间内退出时，按 1、2、4…秒退避重启，上限 30 秒，并打印一行中文提示；文件变更触发的重启或运行足够久后退避归零。文件指纹轮询方式不变。
6. 文档：`AGENTS.md`（“Docker 使用”一节，Trellis 管理块之外）与 `docs/development-environment.md` 写明端口规则；README 开发一节如有涉及同步。

## Acceptance Criteria

- [ ] `docker compose -f compose.dev.yaml config --quiet` 在只设置 `PG_PASSWORD` 时通过，不再要求 `MEILI_MASTER_KEY`。
- [ ] `docker compose -f compose.test.yaml config` 输出的项目名为 `roomusic-compose-test`；`scripts/test-integration.sh` 默认项目名带随机后缀且以 `roomusic-compose-test-` 开头。
- [ ] 三个 compose 文件的 PostgreSQL 镜像字符串相同，CI 检查覆盖。
- [ ] 后端启动即退出时，`dev.sh` 的重启间隔递增且不超过 30 秒（用一个立即退出的假后端或 shell 片段验证）。
- [ ] 仓库内除归档与历史设计文档外，不再有“开发 Compose 含 Redis/Meilisearch”的现状描述；`bash -n scripts/*.sh` 与 `actionlint` 通过。

## Out of Scope

- 开发数据目录从命名卷或 `backend/data` 迁到 `temp/dev`（记为后续事项）。
- 生产 Compose 与生产实例。
