# Core 0 PostgreSQL 基础搜索技术设计

## 模块边界

搜索属于现有 Release 查询能力，扩展 Release 列表 handler 的 typed query 参数和 SQL 查询；不新增搜索服务、索引进程或跨模块 Repository。前端在现有 API client 和浏览页中维护 URL 查询状态。

## 查询合同

`GET /api/v1/releases?page=1&page_size=50&q=term` 返回既有 `{items, pagination}` DTO。`q` 经过 trim；空字符串不添加过滤条件。非空查询使用 PostgreSQL `ILIKE`/等价参数化表达式匹配 release title/artist 或关联 track title，并通过 `EXISTS` 避免重复 Release 行。COUNT 与 items 使用相同过滤条件。

## 数据流

`URLSearchParams -> API client -> decoder -> release list state -> UI`。搜索变更重置到第一页；请求失败保留安全错误和重试入口。后端继续执行现有会话认证、分页上限、稳定排序和 request ID 响应头。

## 兼容、性能与回退

无 `q` 的查询保持旧 SQL 语义。初期接受 PostgreSQL `ILIKE` 在中小型库上的成本；实现阶段用 EXPLAIN/代表性 fixture 检查是否出现重复扫描或 N+1。若性能不足，记录基准并另立索引任务，不在本任务引入 Meilisearch。回退可移除 q 分支而保留既有列表。
