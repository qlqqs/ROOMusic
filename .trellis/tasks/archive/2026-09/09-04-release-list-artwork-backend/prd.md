# 完善发行列表后端封面摘要

## 规划状态

- 父任务：`.trellis/tasks/09-04-frontend-music-display`。
- 当前阶段：后端 REST 合同实施中；父任务中的前端目录工作保持 `planning`，本子任务不修改任何前端源文件或生成资产。
- 规划决策路线：人工选择（Human selection）。选择者为用户（qlqq）；用户已在 2026-09-04 明确批准最新后端方案并要求开始实施，现允许执行 `task.py start`。

## 目标与用户价值

让后续音乐库界面可以在一次发行列表请求中获得真实封面摘要，避免按卡片逐项请求详情（N+1），同时保持现有 Release 可见性、分页、搜索、attention 筛选、认证和错误语义不变。封面缺失必须是明确的 `null`，不能让前端猜测或接触原始文件路径。

## 背景与已确认事实

- `backend/cmd/roomusic/catalog_api.go:38-70` 的 `releaseSummaryProjection` 同时服务列表和详情摘要，目前没有 `release_artworks` 投影。
- `releaseSummaryDTO`（同文件 `:74-84`）没有 `artwork`；详情 DTO 已有 nullable `releaseArtworkDTO`，并由 `loadReleaseArtwork`（`:579-593`）单独查询。
- `release_artworks` 已由 `backend/migrations/0005_release_artwork.sql` 建立，`release_id` 是主键，MIME 和宽高有数据库约束；`0011_scan_staging_and_identity.sql` 允许同一 content-addressed 文件被多个 Release 复用。
- `GET /api/v1/artworks/{id}`（`backend/cmd/roomusic/application.go:315-340`）已有认证、basename 检查和私有缓存语义；本子任务不改变资源读取端点。
- 现有 PostgreSQL catalog 集成测试位于 `backend/cmd/roomusic/catalog_api_integration_test.go:11-230`，已覆盖 present-only、attention、详情和安全错误路径。

## 范围内需求

### R1. 列表摘要合同

- `GET /api/v1/releases` 每个项目新增 `artwork: ReleaseArtworkDTO | null`。
- 有封面时返回 `resource_id`、白名单 `mime`（`image/jpeg`、`image/png`、`image/gif`、`image/webp`）以及正整数 `width`、`height`。
- 没有封面时稳定返回 JSON `null`，不省略字段、不返回空对象、不暴露 `storage_key` 之外的文件路径信息。
- 列表继续只返回至少含一个 `present` Track 的 Release；分页上限、稳定排序、搜索和 `attention=required` 过滤保持现状。

### R2. 详情一致性与查询边界

- 列表和详情共享同一摘要投影与校验逻辑，详情中的 `artwork` 形状与列表一致。
- 封面通过与 `release_id` 的受索引关联在摘要查询中一次取得；禁止 HTTP handler 对每个 Release 再请求详情。
- 缺失封面不改变 Release 的可见性；封面记录损坏、资源标识不安全、MIME 不在白名单或尺寸非正数时 fail closed，返回既有 `database_error`/`database_unavailable` envelope，不泄露数据库内容。

### R3. 兼容与安全

- 不新增迁移、不改变 `release_artworks` 表或 artwork 资源端点，不修改扫描、权限、只读目录和 Operation Journal 规则。
- 未认证请求仍按现有认证错误返回；隐藏 Release 仍返回 `404 not_found`；数据库失败仍使用稳定的服务不可用错误分类。
- 变更必须补充后端定向测试：有封面、无封面、列表/详情形状一致、malformed artwork 防护，以及原有 present-only/attention/权限行为不回归。

## 验收标准

1. 已绑定封面的可见 Release 在列表 JSON 中得到完整 `artwork` 对象，字段值与详情响应相同。
2. 未绑定封面的可见 Release 在列表和详情 JSON 中都得到显式 `"artwork": null`。
3. 列表仍排除仅有 `missing` Track 的 Release，`attention=required`、搜索、分页和排序结果与改动前一致。
4. 资源 ID、MIME、宽高任一不安全或非法时，接口不返回 2xx 封面数据，不回显原始路径或 SQL；既有错误 envelope 和 request ID 语义保持稳定。
5. 后端 catalog 定向测试、`gofmt`、`go vet` 和 `go build` 通过；无数据库迁移漂移、无前端文件或 `backend/cmd/roomusic/web` 生成资产变更。
6. 运行合同文档明确记录列表 artwork 摘要为 additive 字段，并注明真实音频播放仍延期；文档使用简体中文。

## 明确范围外与依赖

- 不实现封面上传、重新抓取、外部 metadata、缩略图处理、缓存服务或新的数据库表/迁移。
- 不修改 `frontend/src/**`、前端 decoder、组件、样式、URL 状态、演示队列或 Vite 生成资产；这些工作由父任务后续前端子任务承接。
- 前端可在本子任务完成后消费该 additive 合同，但本子任务不等待或隐含前端实现；父任务的前端工作依赖本子任务的 REST 合同，依赖关系记录在父/子任务文档中而非由树结构推断。

## 关键风险与回滚

- 共享 SQL 投影的列顺序和 `Scan` 顺序若不同步会导致所有 catalog 响应失败；实现后必须用列表与详情集成测试同时锁定。
- 旧数据库若缺少 `release_artworks` 迁移，接口应按既有数据库错误失败，不在运行时创建表；回滚只恢复摘要投影和 DTO，不删除已有封面数据。

## 最终规划决策记录

- 路线：人工选择（Human selection）。
- 选择者：用户（qlqq）；用户已审核并批准本次后端子任务的最新规划。
- 候选结果：采用共享、单次索引关联的 nullable artwork 摘要投影；先交付后端，前端暂缓。
- 推荐理由：直接复用既有 `release_artworks` 关系和受鉴权资源端点，消除未来 N+1，同时不扩大 Core 0 基础设施或权限边界。
- 选择状态：已批准并进入实施；该批准仅覆盖本后端子任务，不授权父任务的前端实现。

## 规划产物

- [x] `prd.md`：后端范围、合同、验收和延期边界。
- [x] `design.md`：查询、DTO、错误和兼容设计。
- [x] `implement.md`：实现顺序、验证命令和回滚点。
- [x] `implement.jsonl` / `check.jsonl`：后端规范与研究上下文清单。
