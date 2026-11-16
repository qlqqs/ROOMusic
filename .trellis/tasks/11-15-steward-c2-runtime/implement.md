# 管家 C2 执行计划

前置：ADR 0005 已批准；用户批准本任务的规划后 `task.py start`，在独立任务分支上做。
后端实现与测试交给不同的 Codex 子代理（复杂任务，`gpt-6-astra` high），子代理不提交 Git；每一步主代理审阅后再派下一步。

## 步骤

- [x] 1 查两种协议当前的流式与工具调用文档，要点和链接写进 `research/provider-protocols.md`（主代理）。
- [ ] 2 `steward/tool`：Definition、Registry、Invoke 与鉴权、schema 校验、输出截断。
  - 评审点：字段与调用入口交用户确认，再继续。
- [ ] 3 `steward/secret` 与 `steward/egress`。
- [ ] 4 迁移 0051 起（含 down）与 `runtime` 的设置、会话、消息存储。
- [ ] 5 `steward/provider`：两个适配器与探测。
- [ ] 6 `runtime` 循环。
- [ ] 7 `steward/httpapi` 与 `app` 注册；依赖规则测试加条目。
- [ ] 8 业务包的只读工具（`catalog`、`library`、`operations`）与注册。
- [ ] 9 测试（另一个子代理）：两种协议的本地假模型服务；循环的上限、取消与断流；鉴权与隔离；
  出站地址判断；密钥不出现在响应与日志；探测的三种结果。
- [ ] 10 合同文档：新增 `.trellis/spec/backend/steward-runtime-contracts.md`，在 `index.md` 登记。

## 验证

命令只覆盖本任务改动的包，例如 `mise exec -- go test ./internal/steward/... ./internal/app/...`，
以及第 8 步改到的业务包里新增的用例（用 `-run` 收窄）。需要数据库的集成测试按
`docs/development-environment.md` 的做法，用独立临时目录、唯一 Compose 项目名和合成数据。
不调用真实模型；真实端点的试用由用户提供端点，单独记录。

## 风险点

- 迁移：只新增表，不改现有表；down 脚本删表。
- `app` 的注册与依赖规则是共享文件，改动限于新增条目。
- 出站客户端是服务端第一处主动外连，地址判断的测试要覆盖解析结果、重定向和 IPv6。

## 启动前检查

- [ ] ADR 0005 已批准。
- [ ] 用户批准本任务的最终规划摘要。
- [ ] `implement.jsonl`、`check.jsonl` 已有条目。
