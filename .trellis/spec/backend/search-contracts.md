# 搜索规范化与在线投影合同（M5 ④）

## 范围与所有权

目录搜索由 catalog 拥有，PostgreSQL 是唯一权威，搜索表仅为可重建派生。④只有专辑、曲目两栏；不安装艺人实体、别名治理或第三栏。已裁决设计见任务 `11-04-classical-m5-search/design.md` §2–§4、§9。

- `catalog/textnorm`：纯规范化叶子，无内部依赖；仅标准库及既有 `golang.org/x/text`。
- `catalog/internal/searchindex`：投影写入、代次/版本状态及 SQL 辅助。
- `catalog/internal/writegate`：`Lock(ctx, tx)`，目录写者共同的事务级 CATP advisory gate。
- `catalog/maintenance`：短初始化、专用连接单 worker、持久清单与后台续跑。
- catalog 核心负责查询及安全摘要；HTTP 层只校验、鉴权、呈现；app 在启动扫描之前初始化目标版本并装配 worker、取消及等待。

## 规范化与许可

`Text(raw string) string` 顺序为 NFKC → 完整 casefold → NFD 后仅去拉丁基字附加符并 NFC → 对应片假名转平假名 → 固定逐字繁转简 → Unicode 空白折叠。保留假名浊点、半浊点、长音与扩展小假名；不音译 Ł，不做拼音、分词或相似度。

`CatalogNumber(raw string) string` 在此基础上移除空白、Unicode Pd 和 U+2212；姓名与标题不移除连字符。普通键为空时空结果；编号键为空时关闭编号分支，不能用空 LIKE 匹配全库。

固定 OpenCC `ver.1.1.9`、提交 `556ed22496d650bd0b13b6c163be9814637970ae`；原始 ST/TS 文件、Apache-2.0 LICENSE 及中文来源/哈希说明随 `textnorm/data/opencc` 内置。TS 第一候选为目标，ST 仅补 TS 未覆盖且收敛后无歧义的逆映射；不做无向闭包，不合并“炼”和“链”。解析验证 UTF-8、单 scalar、重复键、坏行和非自环循环，最终函数幂等。

`Version=1`，`LoadEmbedded() error` 使用 `sync.Once` 保存结果和错误。所有生产入口先成功预检才调用 Text/CatalogNumber；误用此前置条件属于编程错误，可 panic，不能返回半规则字符串。无 init panic。加载失败禁发投影、保留旧目录与搜索，结构或数据库失败仍按真正故障处理。

## 持久化与写入

迁移 `0025_catalog_search.sql` 建空投影和状态/清单/进度表，启用 `public.pg_trgm`，一般文本 trigram GIN、短一/二 scalar `short_grams` GIN；不执行存量转换。角色、字段、原文 4096 字节、规范键 64 KiB、subject 二择一均由约束或写入边界校验；不截词。扩展在 database 级 public schema，不装进测试的临时 schema。

搜索值逐字段保存实际有效原文、规范键、normalization_version、projection_generation，不复制 group ID 或封面。subject 完成标记额外保存同事务更新的 `search_title`（空标题也有行），用于窄表排序，不能脱离完成代次单独更新。搜索常量/原文保留，不改标签或显示写法。发行字段从 `release_effective` 读取；曲目角色来自四类 credits。

扫描候选、M3 修正/清除/撤销、claim 双方/撤销，在业务行锁之前取得 CATP，事实/有效值变化、input_revision、目标代次投影及完成标记同事务提交。失败回滚业务，禁止提交后 best-effort。missing 保留索引值并实时 join 状态排除，不为搜索引入额外负向对账。归组不复制到投影，代表/默认版本在读取时按既有规则确定。

## 在线回填与发布

`Initialize` 只登记版本/代次、关闭不匹配的读能力；不发现对象、不等待回填。服务 ready 不依赖投影完成。`Run` 使用专用连接维护 advisory lock 选主，各批事务在同一连接上；该选主锁不排斥扫描/M3，CATP 只保护短批。

后台一致快照建立含 missing 的持久清单及固定分母。只预取 ID，CATP 后重新读当前事实、有效修正和 input_revision；已完成目标代次且 revision 相等则跳过。投影、subject 完成标记、清单进度同事务。新增对象由实时写者维护，不会因 UUID 落在游标前漏掉；大库分批，批间至少让出 50ms，取消回滚未提交批，重启续跑已提交清单。

最终在短 gate 中校验 build/目标代次、封闭清单、当前对象覆盖和 revision；仅全部满足才原子更新 published 版本/代次、ready 与 epoch。开始构建和发布递增 epoch，批次不递增。查询只在目标/发布元组全部相等、当前二进制版本相等且 ready 时读取新投影，否则完整走 legacy；不拼接半成品。

逐批日志事件 `catalog_projection_backfill_batch`，包含 build_id、目标代次/版本、phase、completed/total、batch_size、written、skipped_current、batch_ms、elapsed_ms。禁止原文、路径或凭据。失败退避重试，不把失败伪装为发布成功。进程取消后等待 worker 退出，再关闭 DB。

## REST

所有新增接口认证成员可读、`Cache-Control: no-store`：

- `GET /api/v1/catalog/status` → `{epoch,search_mode,artists_state}`。④ `artists_state="unavailable"`，search_mode 为 legacy 或 normalized。
- `GET /api/v1/search/suggestions?q=&limit=`，默认 5、范围 1–8；返回 query、catalog_state、available_types（albums/tracks）与 artists/albums/tracks 三节，每节 `{items,has_more}`，artists 为 []。每栏 limit+1，不执行实体总数探测。
- `GET /api/v1/search?q=&type=albums|tracks&page=&page_size=`，默认 1/50、最大 100；返回 query、type、catalog_state、items、pagination。type=artists 返回 `400 unsupported_search_type`，缺失/未知为 `400 invalid_search_type`。

q 去首尾空白后最多 200 UTF-8 字节；新接口拒绝非法 UTF-8 与非空白控制字符为 `400 invalid_query`。单字符可搜，空白返回空数组、精确 0。limit 非法为 `400 invalid_limit`；分页沿用首值与偏移溢出校验。查询/安全补全失败整页 `503 database_unavailable`，不回传半页。

专用分页 `{page,page_size,total,total_relation,has_more}`：在完整匹配关系按实体去重后探测 LIMIT 1001；≤1000 返回精确值与 eq，≥1001 返回 1000/gte。页查询独立取 page_size+1 决定 has_more；第 21 页以后仍可浏览，空页仍返回全局下界。不替换其他接口的精确 total。

专辑项 `{group:ReleaseGroupSummary,matched_release_id,match}`；曲目项为完整安全 TrackSummary 加 match。match 必传 `{kind,field,role,original,artist_id,alias,release_id,track_id}`，nullable 项显式 null；④ artist_id/alias 恒 null。原文来自命中的有效字段，不返回规范键、路径/rootID/指纹或 HTML。

## 查询语义

字面完整字段包含，不分词、不用 similarity。先规范化再转义 `% _ ~`；普通/编号两条分支各自规范化。每个非空键带安全连续一/二 scalar 预筛并 LIKE 残差，不猜 PostgreSQL 是否能产生 trigram。

完全相同 → 开头相同 → 包含；同等级按规范化标题 COLLATE C、稳定 ID。说明同等级按自身标题、署名、厂牌、编号与固定角色序打平。发行命中来自有效发行字段或 present 子轨标题/艺人/四类 credit；曲目命中来自自身字段或所属发行字段，某轨 credit 不传播到兄弟轨。专辑按可见成员命中后按 group 去重；代表仍是既有默认/年份规则，matched_release_id 另给实际命中成员。

状态、路径选择、计数、分页及补全使用同一只读 RR 快照；对这类速度敏感的查询按[统一数据库规范](./database-guidelines.md)在事务内局部关闭 JIT（`SET LOCAL jit = off`），不改变数据库全局设置。legacy 下旧两路由匹配/排序不变；新专辑栏复用旧发行谓词，曲目栏只查原表标题/艺人及有效发行标题/艺人/专辑艺人。新投影发布后旧两路由有 q 时默认按相关性排序；组列表显式 `sort=added_at_desc` 保留 F 线的加入时间顺序，仍按完整规范化匹配过滤。无 q 保留原序及精确总数。搜索组摘要同时保留全部可见成员最早的 `added_at`，不能误用代表版本时间。

## 已实装的查询策略（内部细节）

- 保留 trigram/short-gram GIN，另用 `md5(normalized_value)` 定长索引定位完全相同层，之后仍比较完整规范键；哈希不是身份判定或安全校验，碰撞不能形成误命中。避免把可达 64 KiB 的键放进 B-tree。
- `catalog_projection_subject_state.search_title` 与值表/完成标记同事务生成；`catalog_projection_title_order_idx` 用前 128 scalar 作增量排序前缀，SQL 后续仍比较完整标题和 UUID，不截词。组排序标题仍来自当前代表发行，归组/默认版本不复制到该列。
- 依 exact、prefix、contains 三层依次求完整排序的 top(offset+limit+1)。更高层填满所需窗口时不展开低层；未填满说明该层已完整耗尽，低层仅排除这些已命中的实体。这是精确分页等价变换，不是固定候选上限。深页仍可遍历全部匹配。
- 每层最多读取 4,097 条原始搜索值来**选择稀疏/密集 SQL 路径**；不是结果候选、实体总数或页数上限。稀疏路径从 GIN 命中出发；密集路径按完整实体标题序做参数化 EXISTS，并在完整语义下取得该页。建议没有 1,001 实体总数探测，但允许这一内部执行策略判定。重复命中超过 4,096、跨三个等级/深页及超长同前缀标题有专门集成反例。
- 新结果总数仍从唯一实体基表经完整 EXISTS/IN 半连接取前 1,001 实体，没有先物化/聚合全体实体。稀疏路径只在同一快照探测确认原始命中≤4,096后物化**完整原始命中**，不 LIMIT 该集合，不物化传播后的全体实体；这样避免关系索引让 planner 反向逐轨扫描值表。原始策略探测不参与 `total`/`has_more`。旧两路由仍精确 count。
- PostgreSQL 在当前 locale 中用 `[[:alnum:]]{3}` 判断一般键和编号键是否具备连续三个字母/数字（可提取 trigram 的保守充分条件）。不足时使用 short-gram GIN，并把完整 LIKE 保留为残差；不在 Go 中仅凭 rune 数猜词法。这样避免复用的通用计划对不可提取 trigram 的短词做整棵 trigram 索引扫描。普通/编号键相同时合并重复分支；参数仍绑定。
- 密集路径的关联 EXISTS 先按 track_id/release_id 读取当前来源的全部字段（子查询 `OFFSET 0` 保留边界），再应用完整匹配残差。不能让通用计划把全局热门词 GIN 命中重复执行数千次；语句复用后的 HTTP 热测及来源边界单测同时覆盖。
- 搜索与最近新增/加入时间排序共用 catalog 的 `readWithLocalJITOff`，只在只读 RR 事务内 `SET LOCAL jit = off`；没有全局 JIT、`enable_*`、`plan_cache_mode` 或最短查询长度开关。语句复用后的热测同样纳入验收，不能只用新连接的单次 EXPLAIN 声称达标。
- 密集专辑候选沿发行规范标题索引流，只保留各组按既有默认/年份/创建时间/ID规则选出的当前可见代表；完整匹配仍检查全组成员。最后同标题按 **group ID** 打平，不误用发行 ID。`releases(group_id,id)` 支持局部成员查找；可见性 EXISTS 按发行早停，避免首屏之前聚合十万轨。
- top-k 的 OFFSET 加 `limit+1` 采用先判断剩余额度的 bigint 安全加法；极大但合法的页码仍返回空页与正确总数，不能变成 503，不能收紧旧分页合同。
- 仅对已选页补最佳命中说明与安全摘要。专辑说明分别读取发行值和 present 曲目值，避免对每首兄弟轨重复读取发行值；四类 credit 不传播到兄弟轨。

## 验证

规范化逐类正反例、真实表哈希/幂等/并发 race；回填重启与扫描交错；扫描与修正/撤销/claim 同事务；401/参数/空查询/短词/通配符/分页 1000 与 1001/非代表成员/missing/安全 DTO/真实 handler 样本。隔离十万 present 曲目库记录真实生产 SQL 的 EXPLAIN ANALYZE BUFFERS、回填耗时和首屏 p95；不得用强制索引、最短长度或截候选规避性能目标。

并发回归不能假定所有业务请求同时等待发行行锁：CATP 之前串行化后，后续请求可能等 advisory 锁。测试以测试持锁连接 PID 的 `pg_blocking_pids` 递归阻塞链证明重叠，不误数其他 schema 的请求；失败时先释放测试锁并等待事务完成，再清理 schema。

## P/F/S 合并边界

候选事务先取得 CATP，再捕获反馈前态、写目录事实与搜索投影，最后在独立反馈保存点记录后态并提交。反馈故障可降级，但不能回滚核心搜索投影；投影故障则候选整体回滚，不留下新增报告。F 的全根 missing 入口每批最多 256 个 ID，在核心区域同事务刷新搜索可见性完成标记，再记录反馈，不能只修改已不再被扫描调用的旧 missing 入口。搜索沿用既有 epoch 协议，不因普通扫描额外递增 epoch。

加入时间读取入口已有只读 RR 时，在该事务读取状态并调用快照内查询，不嵌套 `searchSnapshot`，不把任意事务视作已满足 RR。共享助手保留 `ErrQuery` 与底层错误链，配置失败必须短路回滚，事务结束恢复连接原 JIT 配置。
