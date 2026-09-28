# F2：目录操作幂等指纹绑定目标目录

> 状态：planning，未启动。2026-09-28 由[路线修订任务](../09-28-core-1-roadmap-revision/design.md)（第二部分）创建，本文是 PRD 初稿；
> 启动前需按 Trellis 流程完成规划并获批准。父任务见 [Classical 重设计 / Core 1](../09-27-classical-core-1/prd.md)。

## 目标与用户价值

`root_operations.go` 中停用、恢复操作的幂等指纹不包含路由里的 root ID，重放查询也只按 actor、操作类型和幂等键查找。
因此同一幂等键加相同 body 用于不同目录时，可能返回前一个目标的结果。本任务修复这一问题，让 M1a 的目录管理可以安全重试。

## 依据

- [Core 1 路线](../../../docs/architecture/core-1-roadmap.md) 的“静态审查发现的现有风险”一节与 F2 行。
- [ADR 0003](../../../docs/architecture/adr/0003-governed-operations.md) 中的目录目标绑定缺口；运行合同技术债。
- `.trellis/spec/backend/database-guidelines.md` 的 revision 与幂等规则。

## 范围

先写复现测试；把路由中的 root ID 纳入指纹与重放查询；定义旧记录（缺少目标绑定）的重放兼容策略；
回归测试覆盖同目标重放、跨目标重放、旧记录重放。

## 前置依赖

无；建议先于或与 M1a 同期完成。

## 验收标准（初稿）

- 跨目标重放被拒绝或按目标区分（二者在规划时选定），且没有副作用。
- 同目标重放返回原结果；旧记录按兼容策略处理；PostgreSQL 集成测试通过。

## 验证方式

`./scripts/test-integration.sh`；相关单元测试；`gofmt`、`go vet`。

## 风险

幂等冲突的错误码与现有客户端行为；旧记录兼容；如需迁移，只能新增，不能改旧迁移。

## 范围外

通用操作平台（M3）；其他幂等场景。

## 待规划事项

- 跨目标重放返回 `idempotency_conflict`，还是按目标区分。
- 是否需要迁移补充列，还是只改指纹计算。
