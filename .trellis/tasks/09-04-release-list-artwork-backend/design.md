# 后端封面摘要技术设计

## 1. 边界与能力归属

本子任务只扩展 catalog 读取能力：`backend/cmd/roomusic/catalog_api.go` 负责
Release 摘要 DTO、SQL 投影和响应错误映射；`release_artworks` 仍由扫描/存储能力
写入，`application.go` 中的 artwork 资源端点继续负责认证和文件读取。数据库、扫描器、
权限、只读目录和 Operation Journal 不改变。

父任务的前端工作在本子任务完成后才可开始消费新字段，但不属于本次实现。不得修改
`frontend/src/**` 或 `backend/cmd/roomusic/web/**`。

## 2. 数据流与合同

```text
release_artworks (release_id 主键)
  -> releases LEFT JOIN artwork 摘要
  -> scanReleaseSummary / releaseSummaryDTO
  -> GET /api/v1/releases 与 GET /api/v1/releases/{id}
```

在列表和详情的摘要查询中使用一次 `LEFT JOIN release_artworks ON
release_artworks.release_id = releases.id`。`release_artworks.release_id` 为主键，
因此不会放大 Release 行；列表计数查询仍只访问 `releases`，避免改变总数语义。

`releaseSummaryDTO` 新增 `Artwork *releaseArtworkDTO`。详情 DTO 直接嵌入该字段，移除
重复的详情专用字段，保证两个端点序列化形状完全一致。SQL 的 nullable 列使用
`sql.NullString`/`sql.NullInt64` 扫描；四列全部为空时返回 `nil`，部分为空视为损坏并
fail closed。

## 3. 校验与错误

摘要读取后调用单一校验函数：

- `resource_id` 必须非空且只能是安全 basename（拒绝 `/`、`\\`、`.`、`..` 和控制字符）；
- `mime` 必须属于 artwork 表白名单；
- `width`、`height` 必须为正数且能安全转换为 `int`。

校验失败返回内部分类错误，由列表/详情现有映射分别输出 `503 database_error` 或
`503 database_unavailable`，不把 storage key、SQL 或主机路径写入响应。无记录是正常的
`nil`，不是错误。

详情不再额外调用 `loadReleaseArtwork`；这样同一个摘要查询完成列表/详情所需的封面
元数据，避免重复数据库往返，也避免列表实现 N+1。

## 4. 兼容、迁移与回滚

- 新字段是 additive JSON 字段；旧消费者忽略它即可。
- 不新增迁移，依赖现有 0005/0011 表和索引；运行时不自动建表。
- 资源 endpoint 的认证、私有缓存和 basename 防护保持不变。
- 若需要回滚，恢复原摘要投影、扫描顺序和 DTO 字段即可，不删除 `release_artworks` 数据。

## 5. 测试设计

- 单元测试覆盖 artwork nullable 四列的完整、全空、部分空、非法 ID/MIME/尺寸。
- PostgreSQL catalog 集成测试为一个 Release 插入合法封面、另一个不插入封面，断言列表
  与详情的对象/null 形状相同；同时保留 present-only、attention、隐藏 Release、
  未授权和路径脱敏断言。
- 定向运行 `go test ./cmd/roomusic -run 'Release|Catalog|Artwork' -count=1`，再按门禁
  运行 `gofmt -l .`、`go vet ./...`、`go build ./...`。
