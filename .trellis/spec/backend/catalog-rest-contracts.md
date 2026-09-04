# Catalog REST 合同

## 1. 范围与触发

本合同适用于发行版本列表、详情和封面资源读取。新增或修改 Release 摘要字段、可见性、
搜索、筛选、分页或封面投影时必须同步阅读并更新本文件。Catalog 是读取 owner；扫描器仍
负责写入 Release Graph 与 `release_artworks`，音乐源保持只读。

## 2. 接口与数据签名

- `GET /api/v1/releases?q=<string>&attention=required&page=<int>&page_size=<int>`
- `GET /api/v1/releases/{id}`
- `GET /api/v1/releases/{id}/evidence`：仅管理员。
- `GET /api/v1/artworks/{resource_id}`：认证后读取受管封面。
- Release 摘要包含 `id`、标题/艺人、nullable 年份/来源/介质、Medium/Track/attention
  计数，以及 `artwork: null | {resource_id,mime,width,height}`。
- `artwork` 是在既有 Release 摘要上追加的向后兼容（additive）字段；真实音频播放仍
  明确延期，不属于当前 Catalog REST 合同。

## 3. 请求与响应合同

- 列表只返回至少含一个 `present` Track 的 Release；默认 `page=1`、`page_size=50`，
  `page_size` 最大 100；搜索词最多 200 字节，`attention` 只允许 `required`。
- 列表与详情复用同一 Release 摘要投影。封面按 `release_id` 主键一次 `LEFT JOIN`，
  禁止在列表 handler 中逐 Release 查询详情或封面。
- 没有封面关系时必须显式返回 `"artwork": null`，不能省略字段或隐藏 Release。
- 有封面时只返回安全 basename `resource_id`、白名单 MIME 和 PostgreSQL 正整数宽高；
  不返回图片字节、`content_hash`、`source_type`、受管目录或音乐源路径。
- MIME 只允许 `image/jpeg | image/png | image/gif | image/webp`。资源字节仍通过认证的
  artwork endpoint 返回。

## 4. 校验与错误矩阵

| 条件 | 行为 |
| --- | --- |
| 未认证 | `401 unauthorized` |
| 分页、搜索或 attention 非法 | 对应稳定 `400` 错误码 |
| Release 不存在或仅含 `missing` Track | 详情返回 `404 not_found` |
| 无 `release_artworks` 关系 | 列表与详情返回显式 `artwork: null` |
| 封面四列完整且合法 | 列表与详情返回相同 artwork 对象 |
| 封面列部分为空，ID 含路径分隔符/点目录/控制字符，MIME 或尺寸非法 | fail closed 为既有 `503` 安全错误 envelope；不得回显存储值、SQL 或路径 |
| artwork 资源不存在、记录不安全或文件不可读 | `404 not_found`，不返回主机路径 |

## 5. 正确、基准与错误案例

- 正确：一页 50 个 Release 由一次列表查询获得封面摘要；有封面对象与详情一致。
- 基准：无封面的 Release 仍参与相同搜索、排序、分页和 attention 结果，并返回 `null`。
- 错误：为了显示 50 张封面再发 50 个详情请求，或把 `storage_key` 当主机路径返回。
- 错误：部分空或非法封面元数据仍返回 2xx，迫使客户端猜测数据是否可信。

## 6. 必需测试与断言点

- 单元测试：四列全空、部分空、四种 MIME、空/点目录/分隔符/控制字符/非法 UTF-8 ID、
  零/负/越界尺寸，以及摘要 JSON 的显式 `null`。
- PostgreSQL REST 集成：有封面、无封面、列表/详情一致、present-only、搜索/分页、
  attention、未认证、隐藏 Release、非法资源 ID 的安全 503 和无路径/SQL 泄露。
- 运行 `go test ./cmd/roomusic -run 'Release|Catalog|Artwork' -count=1`；涉及 SQL 投影时，
  还需通过隔离 PostgreSQL 18 的 `./scripts/test-integration.sh`。

## 7. 错误与正确示例

错误：

```go
for _, release := range releases {
	release.Artwork, _ = loadReleaseArtwork(ctx, release.ID)
}
```

正确：列表与详情把同一投影和关联一起传给 `scanReleaseSummary`，由一个函数解释 nullable
列并校验安全边界：

```go
query := "SELECT " + releaseSummaryProjection + " FROM releases" + releaseSummaryArtworkJoin
summary, err := scanReleaseSummary(db.QueryRowContext(ctx, query+" WHERE releases.id=$1::uuid", id))
```
