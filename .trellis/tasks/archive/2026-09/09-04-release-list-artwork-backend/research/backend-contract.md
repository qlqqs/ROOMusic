# 研究：发行列表封面摘要后端合同

- 查询：扫描完成后的发行列表需要哪些后端补充，如何避免前端 N+1。
- 范围：仓库内部代码、迁移、测试与 Trellis 合同。
- 日期：2026-09-04

## 已确认事实

### 代码与数据

- `backend/cmd/roomusic/catalog_api.go:38-70` 的 `releaseSummaryProjection` 被列表和
  `loadReleaseSummary` 共用，目前只投影 Release 元数据、Medium/Track 数和 attention 数。
- `releaseSummaryDTO`（同文件 `:74-84`）没有 artwork；`releaseDetailDTO`（`:139-150`）
  有 nullable artwork，但详情在 `:579-593` 另发一次查询。
- `release_artworks.release_id` 在 `backend/migrations/0005_release_artwork.sql:1-9`
  为主键，MIME 白名单和正宽高由 CHECK 约束保证；`0011_scan_staging_and_identity.sql`
  取消 storage key 唯一性并建立索引，允许内容寻址文件被多个 Release 复用。
- `backend/cmd/roomusic/application.go:315-340` 的 artwork 资源端点已有认证、basename
  检查、私有缓存和文件读取；列表摘要只需要返回受控资源元数据，不应暴露路径。

### 测试与合同

- `backend/cmd/roomusic/catalog_api_integration_test.go:11-230` 已覆盖 present-only 列表、
  attention 计数、详情 Medium/Track、普通用户 evidence 权限和安全路径脱敏。
- `backend/cmd/roomusic/catalog_api_test.go` 已覆盖 attention allowlist、分页溢出和 REST
  标识校验，可在同层增加纯封面校验测试。
- `.trellis/spec/backend/core0-runtime-contracts.md` 规定发行列表只返回含 `present`
  Track 的 Release、错误 envelope 安全、PostgreSQL 为唯一业务权威，以及封面读取/绑定
  失败不得静默吞错。

## 推荐方案

在共享摘要查询中对 `release_artworks` 做一次按 `release_id` 的 `LEFT JOIN`，将四个 nullable
列按固定顺序扫描为 `releaseArtworkDTO`。全空代表无封面；部分空、非法 MIME、非正尺寸或
不安全 resource ID fail closed。把 artwork 放到 `releaseSummaryDTO`，详情直接复用，删除
额外详情查询，既保证列表/详情形状一致，也避免逐 Release 的 N+1。

## 不采用的方案

- 前端逐项请求详情：请求数随列表大小增长，且刷新/取消复杂度放大。
- 新增迁移或 artwork 表：现有关系和约束已满足需求，扩大回滚面。
- 列表只返回首字占位：无法为后续浏览 UI 提供真实封面。

## 注意事项

- 该子任务不修改 `frontend/src/**` 或 `backend/cmd/roomusic/web/**`；前端 decoder 和展示
  由父任务后续阶段承接。
- 未配置 `ROOMUSIC_TEST_DATABASE_URL` 时，Go 集成测试会跳过 PostgreSQL；必须在验证记录中
  明确这一点，不能把跳过当作数据库证据。
