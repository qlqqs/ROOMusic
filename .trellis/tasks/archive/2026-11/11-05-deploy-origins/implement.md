# 执行计划：来源清单与按来源设置会话 Cookie

依据：[prd.md](./prd.md)、[design.md](./design.md)。父任务三个子任务中最先做；完成并提交后，throttle 与 packaging 才开始。

## 步骤

| 步 | 执行者 | 内容 | 产出 |
| --- | --- | --- | --- |
| 1 | 后端实现（`codex/gpt-6-astra`，high，full-access） | 按 design §1–§4 实现：`platform/weborigin`、`config`、`guard`、`handlers.go` 两处 Cookie 调用、`app.go` 装配、`scripts/prod.sh` 检查；按 §7 更新 `identity-contracts.md`、`core0-runtime-contracts.md`、`.env.example`、README 配置说明 | 改动文件清单；`gofmt`、`go vet`、现有相关包测试结果 |
| 2 | 测试（`codex/gpt-6.1-sol`，high，full-access） | 按 design §6 写测试：`weborigin_test.go`、`config_test.go`、`guard_test.go`、`identity_api_integration_test.go` 的清单模式用例；在 `roomusic-test` 隔离库上运行 §6 的命令。发现实现缺陷时回报，不自己改实现 | 新增测试清单、命令与 PASS/FAIL 结果（集成测试确认是 PASS 不是 SKIP） |
| 3 | 主会话 | 审阅 diff 与测试输出，对照 A1–A5 验收；不通过打回原 agent；通过后提交一次（中文提交信息） | 提交哈希 |

## 验证命令（只跑这些，不扩大到全量）

```bash
(cd backend && test -z "$(gofmt -l ./internal/platform ./internal/identity ./internal/app)")
go -C backend test ./internal/platform/weborigin ./internal/platform/config ./internal/identity/... -count=1
go -C backend vet ./internal/platform/... ./internal/identity/... ./internal/app
# 集成：roomusic-test 隔离库（--env-file /dev/null、绝对路径、空闲端口、用完 down -v），只跑身份相关用例
go -C backend test ./internal/app -run 'TestPostgreSQL.*(Identity|Login|Origin|Password|Session)' -count=1
```

## 注意

- 不碰用户的 `roomusic-postgres-1`，不运行 `./scripts/dev.sh`。
- 合同改动后，用 `rg` 检查 `frontend/e2e/real/` 与 `scripts/playback-e2e.sh` 是否依赖 `ROOMUSIC_SECURE_COOKIES` 的生产语义（design §3.4 认为不受影响，执行时复核）。
- 子 agent 不提交 git。

## 回滚点

单个提交；回退即恢复旧模式。没有迁移与数据变化。

## 增补：主机一致模式（D8，2026-11-05）

依据：[prd.md](./prd.md) 的 O8、A6，[design.md](./design.md) 第 9 节。上面第 1–3 步已完成（`db4b4f3`）。

| 步 | 执行者 | 内容 | 产出 |
| --- | --- | --- | --- |
| 4 | 后端实现（`codex/gpt-6-astra`，high） | 按 design §9.1–§9.5 实现 `weborigin.FromHost`、`config.OriginMode`、`NewGuardWithSameHost`、`app.go` 装配（含 §9.5 日志，已裁决做）；按 §9.7 改合同、README、`.env.example`、`.env.prod.example`、`deploy/production/compose.yaml`、`deploy/production/README.md` 与 packaging design 的相关段落 | 改动文件清单；`gofmt`、`go vet`、相关包测试结果；`config --quiet` 结果 |
| 5 | 测试（`codex/gpt-6.1-sol`，high） | 按 design §9.8 写测试并在 `roomusic-test` 隔离库上运行；发现实现缺陷时回报，不自己改实现 | 新增测试清单、命令与 PASS/FAIL（集成测试确认是 PASS 不是 SKIP） |
| 6 | 主会话 | 审阅 diff 与测试输出，对照 A1–A6 验收；不通过打回；通过后提交一次（中文提交信息） | 提交哈希 |
| 7 | 正式启动 | 按 packaging design §10.2 与部署说明执行；`.env.prod` 不填 `ROOMUSIC_ALLOWED_ORIGINS`，以主机一致模式运行；本次不重跑演练 | 正式启动记录 |

增补验证命令（只跑这些）：

```bash
(cd backend && test -z "$(gofmt -l ./internal/platform ./internal/identity ./internal/app)")
go -C backend test ./internal/platform/weborigin ./internal/platform/config ./internal/identity/... -count=1
go -C backend vet ./internal/platform/... ./internal/identity/... ./internal/app
docker compose -f deploy/production/compose.yaml --env-file .env.prod.example config --quiet
# 集成：roomusic-test 隔离库，只跑身份与来源相关用例
go -C backend test ./internal/app -run 'TestPostgreSQL.*(Identity|Login|Origin|Password|Session|SameHost)' -count=1
```

回滚点：步骤 4–5 合成一个提交，回退即恢复“生产必须配清单”（回退后需在 `.env.prod` 补清单）。
