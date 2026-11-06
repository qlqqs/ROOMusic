# 部署体验改进：执行计划（父任务）

子任务各自的执行计划：[builtin-backup](../11-06-builtin-backup/implement.md)、[versioned-release](../11-06-versioned-release/implement.md)、
[config-trim](../11-06-config-trim/implement.md)。

## 顺序

| 阶段 | 内容 | 执行者 | 需要用户 |
| --- | --- | --- | --- |
| P1 | builtin-backup B1 ‖ B2 → B3 → 提交 | astra ‖ Opus → Sol | 截图结果放进汇报，用户有意见再改 |
| P2 | versioned-release V1 → V2 → 提交 | astra → Sol | — |
| P3 | config-trim C1 → C2 → 提交 | astra → Sol | — |
| P4 | 联合演练（`roomusic-drill`，子网 `10.203.51.0/24`，端口 18090）：① 用当前旧部署方式的提交起一个“旧式”实例并造数据，按 config-trim design §8.1 原样切换到新 compose（config-trim A5）；② versioned-release design §8 的 A → B → A → B 与不可回退场景（versioned-release A5）；③ 恢复命令（builtin-backup A8）。结束 `down -v` 清理 | Sol | — |
| P5 | 在最终提交的导出树上跑一次与 CI 等价的门禁（gofmt、vet、`-race`、全量 Vitest、Playwright、`./scripts/test-integration.sh`、`./scripts/playback-e2e.sh`、compose 校验、actionlint） | 另一个 astra | — |
| P6 | spec 更新（`database-guidelines.md`、`core0-runtime-contracts.md`、`identity-contracts.md`）、各 PRD 写实施结果 → 提交 | 主会话 | — |
| P7 | 发布 v0.1.0：推送 main → `gh workflow run release.yml --ref main`（只构建不推送，验证 arm64）→ `scripts/release-check.sh v0.1.0` → 打标签推送 → 观察 Actions → 用户在 GitHub 网页把包设为公开 → 匿名拉取与 `docker manifest inspect` 核对 | 主会话 + 用户 | **用户说“发布”**；包设为公开由用户操作 |
| P8 | 本机生产切换（config-trim design §8.1 十步），核对计数、版本、备份、网络未重建、反代访问 | 主会话逐条执行 | **切换前用户确认** |
| P9 | 归档子任务与父任务 → 修相对链接 → 记日志 | 主会话 | — |

## 跨子任务验收

- [ ] X1 从 Release 下载 compose 与示例配置到空目录，只填两项，`docker compose up -d --wait` 后可以初始化管理员（P7 之后在演练项目中用公开镜像验证）。
- [ ] X2 升降级演练 A → B → A → B 全部 healthy、数据不丢、每次迁移前都有备份；不可回退时拒绝并指向正确备份（P4）。
- [ ] X3 本机生产实例切换到 `ghcr.io/qlqqs/roomusic:0.1.0`：计数一致、端口 8090、反代 `https://music.qlqqs.com:8888` 可登录、备份继续写入 `/home/qlqq/roomusic-backups`、页面显示版本 0.1.0（P8）。

## 回滚点

- P1–P6 只改仓库，逐步提交；生产不变。
- P7 发布失败：不打新标签的镜像不会出现；修复后发布 0.1.1（不删除已推送标签，除非用户同意）。
- P8 切换失败：config-trim design §8.2（A 路线直接换回旧镜像与旧 compose，不需要恢复备份）。

## 实施记录

- 2026-11-06 P1 builtin-backup 已提交 `45514a9`。发现既有问题：`internal/app` 集成测试包在全量并行下约 627 秒（HEAD 单包串行 470 秒），
  超过 Go 默认 10 分钟；已把 `scripts/test-integration.sh` 时限设为 15 分钟、CI integration job 设为 30 分钟。
  艺人相关测试重复准备大型夹具是主要耗时，提速留作后续技术债，不在本任务范围。
