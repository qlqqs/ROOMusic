# 步骤 1 搜索查询审查与修复

## 独立审查

现有子代理只读检查三档分页、密集/稀疏策略、短词谓词、组代表与迁移；未修改生产代码，也未并发运行数据库负载。确认：

- 4,097 条原始命中探测只决定执行路径，不截断候选；低等级只有高等级完整耗尽后才补入。
- 密集专辑路径从发行标题索引流选当前代表，匹配仍覆盖全组；missing 默认与移出组的默认静默失效。
- 长标题的前 128 scalar 只作排序索引前缀，最终完整标题及 group ID 决定顺序。
- 唯一发现的正确性缺陷是合法极大 OFFSET 与 `limit+1` 相加溢出，已局部修正并增加真实 HTTP 回归。

## 极大页码溢出

- 根因：D（测试覆盖缺口）及 E（隐式假设）。共享分页器保证 `(page-1)*page_size` 不溢出，不保证搜索内部 top-k 窗口再加 `limit+1` 安全。
- 反例：64 位下 `page=9223372036854775807&page_size=1&q=tone`。OFFSET 合法，额外取两行却使内部 bigint 相加溢出，错误映射成 503。
- 修正：`search_tiers_sql.go` 先比较 `MaxInt64-limit` 再加，窗口仅在整数表示边界饱和；外层仍使用完整 OFFSET。没有收紧共享分页器、旧 API 或候选数量。
- 回归：`TestPostgreSQLSearchLargestLegalOffsetRemainsEmptyPage` 在有真实命中的夹具上，覆盖 legacy/normalized、新专辑与曲目两栏及旧两路由、page_size 1/2/100；断言 200 空数组、页码不改写、全局总数保留且无更多页。

## 正式空库性能失败与修正

- 之前复用合成 schema 的探索轮达标不能替代空 schema 复验。首轮空库的“交响”首屏 p95 为 353.32 ms，失败证据保存为 `search-scale/failed-fresh-summary.json` 与 `failed-fresh-shortword-explain.json.gz`。
- 计划显示按 group 反查发行没有索引：计数对前 1,001 组重复顺序扫描 releases；密集页的代表选择还先聚合十万 present 曲目。
- 修正只涉及本线 0025 的 `releases(group_id,id)` 和搜索内部候选流。沿规范标题顺序探测发行，只保留符合已有代表规则的成员，完整匹配后才 LIMIT；无 SQL planner 开关、最短长度或候选截断。
- `TestPostgreSQLSearchDenseGroupsUseCurrentRepresentative` 强制同标题的 group ID 与 representative release ID 顺序相反，并验证跨页、仅非代表成员命中、missing 默认与成员移组。已有密集三档测试继续覆盖空标题、超长同前缀标题与重复命中超过策略阈值。
- 最终验收数字、测试总数与产物见 `step1-backend-report.md`，不把单次 SQL EXPLAIN 当 HTTP p95。

## 预防

已将上述内部排序与 bigint 边界写入 `search-contracts.md`。数据库 JIT 统一规则由 P 线维护，本线仅引用，不修改 `database-guidelines.md`。没有提交、stash、reset 或切换分支。

## 索引变化后的稀疏计数与热计划回归

- 加入组关系索引后，稀疏计数被反向展开为逐组/逐轨扫描值表，精确标题、零结果等约 1 s。保留失败轮 `failed-sparse-index-*`。
- 修正：探测确认原始命中≤4,096才物化完整原始值命中，没有截取候选；传播 CTE 不物化/不去重，从唯一实体基表做半连接，在 1,001 个实体处停止。`IN (...) IS TRUE` 保留布尔子计划边界，避免 LIMIT 前全量实体 HashAggregate。实际计数计划未见全量实体聚合。
- 随后单次 EXPLAIN 约 30 ms，但复用连接热测“周杰倫”曲目计数约 5.8 s；不能据冷计划声称达标。失败的部分矩阵及 SQL 时序保留为 `failed-generic-*`。
- 根因：关联条件与全局 LIKE/GIN 在同一层，通用计划可为每个对象重复检索完整热门词命中。
- 最小修正：`subjectMatchSQL` 先按当前 track/release 读取其完整字段，`OFFSET 0` 保留来源子查询边界，外层执行完整搜索残差；曲目自身/发行字段两支 EXISTS 保持原传播语义。不是 SQL planner 开关，也没有来源字段数量上限。
- 预防：`TestSearchDenseSubjectPredicateBoundary` 锁定边界；密集三档/代表测试与 17 组完整 HTTP 的五次预热、30次测量覆盖。最终结果见 `step1-backend-report.md`。
