# 管家 C9 执行计划

分支 `steward-c9-config`，依赖 C6、C7 已合入。后端实现 `gpt-6-astra` high（抽取）与 `gpt-6.1-sol` high（工具与诊断），
测试由另一个子代理；前端只有工具标签，由主代理做。

- [x] 1 后端：`library`、`identity` 的服务层抽取；跑现有的目录与成员接口用例确认行为不变。
- [x] 2 后端：`platform/diagnostics`、各 `stewardtools` 包、注册与依赖规则。
- [x] 3 后端测试；规范新增工具清单，`logging-guidelines.md` 补日志环与 `steward.config.changed`。
- [x] 4 前端工具标签；独立审查（只读，重点是脱敏与权限），修复，提交。

## 验证

- 后端：`go test ./internal/library/httpapi/... ./internal/identity/httpapi/...`（抽取的影响范围），
  `./internal/platform/diagnostics/...`，各新 `stewardtools` 包，`./internal/steward/...` 里确认流程新增的用例。
- 前端：`tsc --noEmit`、`tool-labels` 测试。

## 风险点

- 抽取动的是全站在用的管理接口：只移动不改写；出现用例失败时先回看移动是否完整，不改用例。
- 日志包装器在全局日志路径上：缓冲写入不能阻塞，也不能因为脱敏出错影响原有输出。

## 执行记录（2026-11-16）

- 步骤 1、2 由 `gpt-6-astra` 实现；测试由 `gpt-6.1-sol`。只读审查（trellis-check）报告脱敏 4 条、确认卡片 1 条中等问题和 8 条低级问题，
  由另一个 `gpt-6-astra` 修复，决定记在 PRD D6–D8。
- 首轮测试：120 个叶子用例，117 通过，3 失败（值内 `api_key=` 等形态未脱敏）。既有目录/成员接口回归 17 个顶层用例通过。
- 修复后收窄重跑：25 个顶层、87 个叶子用例全部通过，其中数据库集成 11 个顶层。首轮通过且未受修复影响的用例沿用首轮结果。
- `-race`：首轮诊断包并发用例通过，无数据竞争；修复后未重跑。
- 前端：`tsc --noEmit`、eslint 通过，`tool-labels.test.ts` 90 个通过。
- 未执行：后端与前端全量套件、API 样例生成（HTTP 输出合同未变）、真实实例与真实模型试用、修复后的再次独立审查。
- 已知限制：目录操作以外的配置写入没有统一的操作记录与幂等对账；statfs 超时用例验证的是探测槽位等待，未挂起真实系统调用。
