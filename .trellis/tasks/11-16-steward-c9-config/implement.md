# 管家 C9 执行计划

分支 `steward-c9-config`，依赖 C6、C7 已合入。后端实现 `gpt-6-astra` high（抽取）与 `gpt-6.1-sol` high（工具与诊断），
测试由另一个子代理；前端只有工具标签，由主代理做。

- [ ] 1 后端：`library`、`identity` 的服务层抽取；跑现有的目录与成员接口用例确认行为不变。
- [ ] 2 后端：`platform/diagnostics`、各 `stewardtools` 包、注册与依赖规则。
- [ ] 3 后端测试；规范新增工具清单，`logging-guidelines.md` 补日志环与 `steward.config.changed`。
- [ ] 4 前端工具标签；独立审查（只读，重点是脱敏与权限），修复，提交。

## 验证

- 后端：`go test ./internal/library/httpapi/... ./internal/identity/httpapi/...`（抽取的影响范围），
  `./internal/platform/diagnostics/...`，各新 `stewardtools` 包，`./internal/steward/...` 里确认流程新增的用例。
- 前端：`tsc --noEmit`、`tool-labels` 测试。

## 风险点

- 抽取动的是全站在用的管理接口：只移动不改写；出现用例失败时先回看移动是否完整，不改用例。
- 日志包装器在全局日志路径上：缓冲写入不能阻塞，也不能因为脱敏出错影响原有输出。
