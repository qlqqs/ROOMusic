# 步骤 1 后端实施边界

本轮沿用已获批的 `11-04-classical-m5-search`，只实施④后端；不新建任务，不实现⑤。

- 现有差距：原始字段 ILIKE 不能实现跨写法、新字段、全局分类搜索；目前没有在线投影版本管理。
- 所有权：纯字符规则归 `catalog/textnorm`；搜索投影与事务 gate 归 catalog 内部；后台构建归 `catalog/maintenance`；查询归 catalog、鉴权与参数归其 HTTP 层；app 只做生命周期装配。
- 新增迁移、规范化数据/许可、搜索内部模块；扫描/M3 仅挂接同事务刷新。既有扫描文件不顺手重构。
- 同 worktree 前端代理拥有 `frontend/**`、`backend/cmd/roomusic/web/**`；本轮不编辑这些文件。
- 当前并行实现划分：字符规则、投影/回填/写入挂接、读取/HTTP、app 搜索测试/样本，各自写集分开；主侧负责装配、依赖门禁、性能验证与合同同步。
- `Text` / `CatalogNumber` 的 string 签名不承载加载错误。生产入口显式成功执行 `LoadEmbedded() error` 后才调用；违反该内部前置条件可 panic，但禁止 `init` panic 或静默半规则。真正字表故障登记失败并保持旧服务，不发布新投影。
- 数据库只用 Compose 项目 `roomusic-test-m5s`，随机数据库与空闲端口，环境仅存 `/tmp/roomusic-m5s-backend.env`，结束 `down -v`。不操作用户数据库或其他 worktree。
- 测试采用受影响包及 `internal/app` 的 race/vet、依赖规则与真实 PostgreSQL 集成；不运行无关全量测试或前端 E2E。

## 性能阶段确定的内部细节

不改 design.md §9 的对外形状。增加同代完成行中的 `search_title`、定长完全匹配哈希索引与标题前缀排序索引；分档 top-k 只提前结束已足够的低优先级工作，不截任意候选。用有界原始值探测选择稀疏/密集执行路径，实体计数仍独立 LIMIT 1001。短词是否具备稳定 trigram 由数据库 locale 的字符分类判断，残差保持完整字面 LIKE。见 `search-contracts.md` 的内部策略及最终性能报告。

用户明确指定数据库规范由 P 线更新；本线已移除之前误加到 `database-guidelines.md` 的段落，只在搜索合同引用统一 JIT 规则。没有更改对外 API，也没有实施⑤。

### 空库复验后的局部性能修正

正式空 schema 回填后，“交响”两栏首屏 p95 为 353 ms（专辑栏占主导）。实际计划发现按 group 匹配每次顺序扫描 releases，基线没有 group_id 索引；另有代表版本可见性全库聚合。只在本线尚未发布的 0025 补 `releases(group_id,id)`，必要时局部调整搜索代表选择的 EXISTS 早停形状，不修改共享 releasesql、扫描或其他线文件。仍比较完整标题、完整匹配/分页，不降规模。修正后重新验收并保存失败证据。

审查补充：只在搜索内部修正 OFFSET + limit+1 的 bigint 溢出；保留共享 ParsePagination 与旧 API 的合法输入范围。真实 HTTP 回归覆盖新两栏/旧两路由、legacy/normalized、page_size 1/2/100 与最大合法偏移，空页仍保留全局计数。
