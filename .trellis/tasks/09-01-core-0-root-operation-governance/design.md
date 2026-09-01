# Core 0 目录操作治理技术设计

## 数据模型

- 扩展 `library_roots`：`status`、`revision`、`updated_at`。
- 新增 `library_root_operations`，保存 operation id、actor、类型、状态、idempotency key/fingerprint、before/after JSON、expected/result revision、request id、错误分类和时间。
- 为 actor + operation + idempotency key 建唯一约束；路径继续保持唯一约束。

## API 合同

- `POST /api/v1/library-roots`：要求 `Idempotency-Key`，创建或幂等返回 root 与 revision。
- `PATCH /api/v1/library-roots/{id}`：body `{status, expected_revision}`，支持停用，要求幂等键。
- `POST /api/v1/library-roots/{id}/restore`：body `{expected_revision}`，从最近一次停用操作的 before state 恢复。
- `GET /api/v1/library-root-operations`：管理员分页查询安全摘要，不返回完整服务器路径。
- 错误统一映射为 `revision_conflict`、`idempotency_conflict`、`not_found`、`invalid_input` 或 `operation_failed`。

## 事务流程

1. 认证并校验管理员、来源和幂等键。
2. 计算 canonical fingerprint，查询幂等记录；重复请求直接返回记录结果。
3. 开启事务，锁定目标 root，比较 expected revision，写入资源新状态和递增 revision。
4. 写入 operation journal 的 before/after 与结果，提交事务；任一步骤失败整体回滚。

恢复只允许匹配未被后续修改的 revision，禁止 last-write-wins。扫描查询过滤 `status='active'`。

## 兼容性与回退

- 迁移为既有目录填充 `active`、revision 1；旧客户端缺少幂等键时获得明确 400，不改变已有读取 DTO 的安全路径展示。
- 应用回退可停止使用新写端点；迁移字段保持兼容，不删除历史操作记录。

## 风险

- `before/after` 使用受限 JSONB，仅保存状态、revision 和安全资源标识，避免泄露原始路径。
- 并发操作和重复请求必须使用 PostgreSQL 唯一约束与事务测试覆盖。
