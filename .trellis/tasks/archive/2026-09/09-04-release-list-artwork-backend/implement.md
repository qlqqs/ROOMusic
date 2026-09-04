# 后端封面摘要实施计划

## 实施前门禁

- [x] 用户已于 2026-09-04 明确批准本子任务最新规划；批准仅覆盖后端子任务。
- [x] 已执行 `python3 ./.trellis/scripts/get_context.py --mode phase --step 2.1 --platform codex`，读取实施阶段约定。
- [x] 已使用 `trellis-before-dev` 读取共享指南、后端层指南、数据库/错误/质量指南和 Core 0 运行合同。
- [x] 已检查工作区，仅有本轮 Trellis 任务文档改动；实施时不得覆盖用户已有改动。

## 有序清单

### 1. 建立后端基线

- [x] 搜索所有 `releaseSummaryDTO`、`releaseSummaryProjection` 和 `loadReleaseArtwork` 消费者，确认共享投影的完整影响面。
- [x] 运行 catalog 相关 Go 测试记录基线；确认未设置 PostgreSQL 时的跳过提示不被误判为集成通过。

### 2. 扩展共享摘要投影

- [x] 在 `backend/cmd/roomusic/catalog_api.go` 的摘要查询中加入对 `release_artworks` 的单次 `LEFT JOIN`，保持列表计数查询、present-only 谓词、搜索、attention、排序和分页不变。
- [x] 将 nullable 封面列按固定顺序加入 `scanReleaseSummary`，全空映射为 `nil`，部分空或数值溢出 fail closed。
- [x] 将 `Artwork *releaseArtworkDTO` 放入 `releaseSummaryDTO`，移除详情 DTO 的重复字段和详情额外查询，确保两个端点 JSON 形状一致。

### 3. 加强封面摘要安全校验

- [x] 复用/抽取单一校验函数，限制 resource ID、MIME 白名单和正宽高；错误只向上返回内部原因，不写入响应。
- [x] 保持 `GET /api/v1/artworks/{id}` 的认证、basename、私有缓存和文件读取行为不变；不新增运行时迁移或文件操作。

### 4. 补充回归测试

- [x] 更新 `backend/cmd/roomusic/catalog_api_integration_test.go` fixture，覆盖有封面与无封面列表项、详情一致性和显式 null。
- [x] 在 `backend/cmd/roomusic/catalog_api_test.go` 或同层测试覆盖非法 ID/MIME/尺寸、部分 nullable 列和安全错误路径。
- [x] 保留并验证现有隐藏 Release、attention、搜索/分页、未认证/403/404、路径脱敏断言。

### 5. 同步运行合同

- [x] 更新 `.trellis/spec/backend/core0-runtime-contracts.md` 的发行 REST 索引，并在拆分的
  `.trellis/spec/backend/catalog-rest-contracts.md` 中记录列表 `artwork` additive 字段、
  nullable/白名单边界和真实播放延期。
- [x] 文档及任务记录使用简体中文，不修改前端规划文件的实现内容。

### 6. 验证与审查

- [x] 运行 `gofmt -w` 仅作用于受影响 Go 文件，随后执行 `go test ./cmd/roomusic -run 'Release|Catalog|Artwork' -count=1`。
- [x] 执行 `go vet ./...`、`go build ./...`、`git diff --check`；若配置了 PostgreSQL，再运行对应集成测试/`scripts/test-integration.sh`。
- [x] 检查 `git diff --name-only`：只允许后端 Go、后端测试、Core 0 合同和本子任务文档；不得出现 `frontend/src/**` 或 `backend/cmd/roomusic/web/**`。
- [x] 已运行完整范围 `trellis-check`；代码未发现需修复项，补齐了跨层合同的签名、校验矩阵、案例和测试断言；未归档父任务或启动前端工作。

## 推荐验证命令

```bash
cd backend
go test ./cmd/roomusic -run 'Release|Catalog|Artwork' -count=1
gofmt -l cmd/roomusic/catalog_api.go cmd/roomusic/catalog_api_test.go cmd/roomusic/catalog_api_integration_test.go
go vet ./...
go build ./...
cd ..
git diff --check
git diff --name-only
```

若设置 `ROOMUSIC_TEST_DATABASE_URL`，再执行 catalog PostgreSQL 集成测试；未设置时应
记录测试明确跳过集成部分的事实。

## 实施记录

- 基线执行 `go test ./cmd/roomusic -run 'Release|Catalog|Artwork' -count=1 -v` 通过；当时
  `ROOMUSIC_TEST_DATABASE_URL` 未设置，输出明确跳过 PostgreSQL 用例。
- 实现后再次执行同一定向测试通过；单元覆盖全空、部分空、非法 resource ID、MIME、
  尺寸和 JSON 显式 `null`。
- 执行 `./scripts/test-integration.sh`，真实启动隔离 PostgreSQL 18，并运行
  `go test ./cmd/roomusic -run 'TestPostgreSQL' -count=1`；全部通过，脚本随后清理测试
  容器、网络和数据卷。
- `gofmt -l`、`go vet ./...`、`go build ./...`、`git diff --check` 均通过；变更范围
  不含 `frontend/src/**`、`backend/cmd/roomusic/web/**` 或数据库迁移。
- 完整范围 `trellis-check` 对照 PRD、设计、后端规范和数据流复核 SQL/Scan 顺序、
  `artwork` 显式 null、非法投影 fail closed、权限/路径脱敏及 N+1 边界；没有代码发现。
  依据 `trellis-update-spec`，进一步补齐 Core 0 合同的接口签名、错误矩阵、正确/错误
  案例和必需测试。只读确认现有前端列表 decoder 忽略未知 additive 字段，后端可先发布。

## 风险文件与回滚点

| 阶段 | 风险文件/行为 | 回滚点 |
| --- | --- | --- |
| SQL/Scan | `catalog_api.go` 投影与列顺序 | 恢复旧投影和 scan，保留数据库表与资源 endpoint。 |
| DTO | `releaseSummaryDTO` 与详情嵌入 | 恢复字段前的 JSON 形状，确保不留下重复 artwork key。 |
| 安全校验 | malformed artwork 的错误分类 | 恢复既有错误映射，不放宽资源路径或 MIME 边界。 |
| 测试/合同 | catalog fixture 与 Core 0 文档 | 回退测试/文档同步，不删除已有封面数据。 |

## 完成门槛

- [x] PRD 中每条后端验收标准有代码或测试证据。
- [x] 定向 Go 门禁及适用 PostgreSQL 集成测试通过。
- [x] `trellis-check` 通过，且工作区无前端或生成资产改动。
- [x] 后端子任务完成后再由父任务安排前端子任务；真实音频播放仍明确延期。
