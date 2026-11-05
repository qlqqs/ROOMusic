# I10 热门艺人搜索定点检查

## 状态与边界（2026-11-05）

**当前仅完成检查准备，等待主控通知代码稳定后审实际 delta；不是实现验收或性能通过报告。**

- 检查基线：`b5040370b9593fa93edde84bcdff73d070254b77`。Mencius 负责 `catalog/search*.go` 与迁移 0030，Nietzsche 负责 `catalog_scale` I10 harness；准备期间已观察到这些文件陆续出现，不把在途代码当最终交付。
- 本检查者只写本文件；不修改 owner 代码、不回退他人改动、不连接数据库、不启动容器、不跑全量测试或 race，忽略其他人的前端改动。不递归派发子代理。
- 已读取本机 `AGENTS.md`、保存的 hook 上下文、任务 PRD/设计/执行计划、联合设计 §15.2b I9/I10、搜索/艺人/目录/数据库/质量合同及 I8 历史报告。后续以实际稳定差异和其调用路径为准。

## 当前判定口径

- I9 接受 2026-11-05 02:51 起正式运行的回填结果；本轮不再测量或优化回填，不恢复 36 分钟、CATP p95/p99 或历史前台门槛。
- I10 唯一新增性能硬线：**“周杰伦”“周杰倫”分别执行建议与三栏首屏，每项 5 次预热、30 次正式测量，p95 ≤ 1,000 ms。** 三栏首屏计真实并发请求的整屏墙钟，不取三栏 p95 的平均、相加或用 SQL 累计时间代替。
- 单字、深页、10,000 首队列及剩余计划只作少量补充记录，不借此恢复整套旧矩阵、旧路由性能对照或回填验收。只记录并不允许掩盖实际请求错误、错误结果或缺失证据。

## 小检查清单（待稳定后逐项填写）

| 检查项 | 核对要点与最低反例 | 状态 |
| --- | --- | --- |
| 1. 拆 OR 等价 | 对照替换前谓词，逐支核验 NULL、互斥来源、`UNION ALL` 重复及外层去重/最佳说明；必须仍是实际 subject 的关联，不把全库命中错误连接进来。字符串 SQL 替换应覆盖分页、说明、计数、加入时间路径，不误替换其他 CTE | 待审 |
| 2. 艺人传播边界 | 发行级 `release_artist/album_artist` 可传播其 present 子轨；轨级 `track_artist`/四 credit 仅传播本轨及所属发行/组，不扩散兄弟轨。missing-only 命中不使发行/组可见；普通原始字段仍按 role/原文唯一身份判定，不能因拆分多算身份。核对发布代次及 preparing 回退 | 待审 |
| 3. 代表、排序、说明 | 非代表成员命中仍返回既有默认/年份规则代表，`matched_release_id` 保留实际命中；`added_at` 取全组可见成员。exact→prefix→contains、完整规范标题 COLLATE C、稳定实体 ID、字段/角色/原文说明顺序不变，组同标题按 group ID 而非 release ID | 待审 |
| 4. 计数与候选完整 | 4097 只选稀疏/密集路径，不作候选上限；1001 只探测专用分页下界；计数先按实体唯一关系、旧两列表仍精确 total。验证 1000/1001、跨层/深页、重复别名/署名、极大 offset 及超长同前缀标题；建议不引入总数计算 | 待审 |
| 5. 索引与局部范围 | 0030 只加相关查询索引，旧迁移字节不变；索引列顺序/部分谓词对应实际发行与曲目入口。不得引入回填、治理/扫描变化、缩短查询词、planner/全局 JIT 开关或额外固定候选 LIMIT；索引收益须有真实计划，不能静态宣布 | 待审 |
| 6. 唯一完整夹具 | 初始化一次完整规模：100000 present＋10000 missing、10000 发行/约8000组、至少20000参与艺人、每轨2艺人/4credit、20%不同键别名、50000热门 present 轨；新建与复用均校验规模及可读投影。标记/目录不能误指用户库；准备步骤与只读测量分开，不将构建耗时重新作为回填验收 | 待审 |
| 7. 失败与复用 | 失败保留同一隔离库/夹具供定位及仅热门词复测；身份/版本/ready/夹具不匹配须明确失败，不能静默重建、缩小或重新回填。证据目录逐次唯一，不覆盖首个失败；明确最终清理责任。中止/超时必须保留首因和未完成状态，不能把缺样本或默认零值计 PASS | 待审 |
| 8. 测量闭环 | 两词均 5warm/30，建议与三栏整屏独立样本，冷值/预热不混入；30样本 nearest-rank p95 为排序后第29项，边界≤1000。真实 handler 包含鉴权/状态/计数/补全/序列化及池等待；正式测量无后台构建或重诊断竞争，保留逐样本与代码/迁移身份 | 待审 |
| 9. 补测与合同同步 | 少量单字/深页/完整队列与代表性 EXPLAIN 只记录，不跑旧路由性能对照、不再次调用旧正式回填基准。仍覆盖相关搜索正确性最小回归；需要更新的内部查询策略写给主控/owner，不由本检查者修改 spec | 待审 |

## 历史证据，仅作背景

已只读核对仓外目录 `/home/qlqq/.local/share/roomusic-evidence/m5-artists/scale-i8-20261105T025149Z` 的 `summary.json`、`test.exit`、`command.txt`、`resource-cleanup.txt`，并与任务 `backend-status.md`/`i8-check.md` 对照：

| 历史查询 | 建议 p95 ms（30样本） | 三栏首屏 p95 ms（30样本） |
| --- | ---: | ---: |
| 周杰倫 | 3729.150 | 21860.786 |
| 周杰伦 | 3674.033 | 21648.577 |

旧 summary 为 `Finalized=true, Complete=false, Pass=false`，退出码1；只完成4/26组，队列未测，第四组旧路由对照未完整汇总。资源文件记录 04:24:43 UTC 容器/卷/网络均0、凭据环境文件删除；本检查者没有调用 Docker 独立检查。**以上不是 I10 性能数据，历史功能 race/vet/build 通过也不验证本轮新增代码。** I9 对回填的接受不改写历史原始报告。

## 发现（已修复）

无。尚未进行稳定 delta 审查，未修改实现代码。

## 发现（未修复）

尚无经过稳定 delta 核实的实现问题。上表为待检查事项，不应当作为已发现缺陷或已通过项。联合设计 §15.3 与执行计划仍保留历史 300ms 等原规划文字，阅读时由后补 I9/I10 明确覆盖；不据旧文字给本轮增加门槛。

## 验证记录

- 已执行：`git rev-parse HEAD`、`git status --short`、`git diff --name-only`；`cat`/`sed`/`rg` 读取相关任务、spec 与旧实现入口；Python 仅解析上述仓外 JSON/文本，不访问数据库。
- Lint：未执行（当前仅准备检查清单，owner 代码仍在修改）。
- TypeCheck：未执行（同上）。
- Tests：未执行；无数据库测试、无基准、无全量测试、无 race。
- 稳定后先只读审实际 delta；局部 `go vet`/编译与纯单测需分别记录实际命令和结果。PG/性能证据由主控或 owner 提供，只能标注“核对外部执行证据”，不能写成本检查者实测。

## 第一轮实际 delta 静态审查（2026-11-05 05:54–05:56 UTC）

主控通知开始只读审查；owner 仍可修订。本节对应下面 SHA 边界，不代表随后修订已复核。仅改本报告，没有执行 DB、测试、lint 或编译。

### 发现（未修复，已即时反馈）

1. **P2：复用没有重新证明原规模。** `backend/internal/app/catalog_scale_i10_test.go:156–160` 把 `i10FixtureFacts` 放在 `actions.Build` 条件里；`reuse/explain` 仅验证数据库/schema 标记及 ready 元组。`Counts/PG` 在复用报告里为空，manifest 的夹具 SHA 也只在 create 构造时核对。若诊断期间删减冷数据、减少热门轨或别名后仍保持 ready，复用测量没有拒绝这种变小夹具的保护，首屏 50 项/1000+ 不能替代完整规模证明。建议所有非 cleanup 模式先只读调用完整夹具断言，并核验夹具文件身份；`ANALYZE` 可继续仅限 create。此为 harness 验收缺口，不声称实际保留库已经变小。
2. **P2：热门建议空结果仍可计通过。** `catalog_scale_i10_test.go:i10MeasureCase` 调用旧 `scaleValidateSuggestions`（`catalog_artist_scale_test.go:112`），后者只检查 ready、非 nil 三栏及各栏不超过5项。三个空数组也通过；即使结果页正确，建议单独丢结果的优化错误仍可能被记为性能 PASS。建议 I10 热门两词专用断言：专辑/曲目各5项且 has_more，艺人恰好1项且 ID 为 `scalePopularArtist`、无更多；保留通用形状检查，并加空结果反例的纯测试。
3. **P2：完成的热门测量在 EXPLAIN 前没有持久化。** `catalog_scale_i10_test.go:187–206` 先完成 `i10MeasureCase`，再顺序请求并 EXPLAIN 四类 endpoint，之后才把 item 追加到 `report.Cases` 并 `save`。即使此时已拿到30个失败样本，进程若在后续昂贵计划期间被测试硬超时或外部终止，已完成样本仍只在栈变量中，仓外 summary 不含它；原子 JSON 写与逐条计划落盘不能补救未写入的测量。建议测量结束立即追加并保存，再取计划；逐样本检查点可作为进一步增强，不要求重造夹具或新增测量。

均为 owner 在途文件，未代改；本轮未发现需要越界调整产品接口/模块的理由。上述问题的修复状态须待 owner 最后修订逐条复核。

### SQL 等价性与边界（静态核对）

- `search_aliases.go` 将 `(e.track_id=v.track_id OR e.release_id=v.release_id)` 拆为两支 `UNION ALL` 后再聚合。0025 搜索值与0026有效署名均约束 `num_nonnulls(release_id,track_id)=1`，所以同一来源不同时满足两支；NULL 不命中。即使同一身份有多个同名署名，`count(DISTINCT artist_id)=1` 与 `min(artist_id)` 保留原唯一身份语义，role/原文限定未丢。
- `search_ranked_sql.go` 说明来源分为轨级与发行级参数化读取，专辑/发行仍加 `ps.track_id IS NULL`，不为每首兄弟轨重复发行说明；曲目自身可继承本发行字段，但不会继承兄弟轨署名。最优说明的等级/字段/role/原文/来源打平次序及 `LIMIT 1` 位置未改变。新增的 `OFFSET 0` 是来源计划边界，不是候选数上限。
- `normalizedCountSQL(false, attention)` 改为完整 `hit_subjects`，不再使用4097策略探测。曲目由 present 的 `tracks JOIN media` 唯一基行对轨命中或本发行命中做 EXISTS；发行/组的 `matching_releases` 将可见发行字段、present 子轨映射发行，最终由 `releases` 或 `release_groups` 主键基表 `IN` 半连接计数。多字段/多别名/多轨/多成员重复命中不会乘 total；missing-only 来源仍排除。attention 仍按可见匹配发行自身或组内任一可见成员待处理条件；无 q、legacy 路径不变。
- bounded 分支仍使用完整原始命中/完整密集 EXISTS，再按唯一实体探测1001；本次没有增加固定候选上限。分页三档、代表选择、group ID 打平、全组 `added_at`、精确 total 的加入时间路径继续沿原调用链，不因说明拆支重选代表。
- 0030 仅新增两个带非空来源条件的 `(artist_id,track_id)` / `(artist_id,release_id)` 覆盖索引，未改旧迁移、数据或代次。可见有对应查询入口，但是否采用索引、是否减少热门成本只能以主控实际计划和测量判定。

### harness 生命周期与测量（静态核对）

- create 只在无 manifest 的证据根目录新建随机 schema；同目录 create 再入会拒绝。reuse/explain 不执行迁移、fixture SQL、InitializeCatalog 或 maintenance；`New(...pauseCatalogMaintenance=true)` 防止随后隐式启动 worker。不存在自动 fallback 到 create 的分支。
- create 仍调用一次真实 maintenance 来**准备可读夹具**，日志丢弃、不挂 CATP/activity 诊断、不计回填 SLA；这不等于完全不执行回填，而是不把夹具准备作为 I9 回填复测。此生命周期与主控要求的“唯一夹具后续只复用”一致，不能在失败后换目录再次 create 挑样。
- schema 永不自动 DROP；cleanup 必须显式模式、schema、manifest 数据库指纹、随机 nonce 与数据库 COMMENT 一致；新/失败 attempt 不覆盖旧目录。文件锁按证据根目录串行，操作者仍须维持唯一根目录，不能用另一个根规避唯一夹具约定。
- 热门两词各1次独立冷请求＋5次预热＋30次正式测量；建议单独计时，三栏并发的收齐墙钟为首屏，不混用 DB 累计。`scaleStats` 取30样本排序第29项且边界为 `<=1000`。错误进入 item.Errors 或 report.Errors，失败样本不会被成功重试替换，最终 Pass 不仅看延迟。
- `create` 的补测为“爱/周”各1warm/3样本、深页一次、两队列各3次；`reuse` 不重复补测，不跑旧列表对照。补测没有延迟硬线，但请求/形状错误仍使运行失败。队列检查数量/present/去重，未新增随机分布或在途取消断言；不能把这组少量记录称历史完整队列验收。
- 当前三个新纯测试文件中的 helper 检查只是静态已存在，不代表执行通过；新 `search_aliases_integration_test.go` 已覆盖非代表成员、发行传播/兄弟不传播、missing、三档、精确计数/attention、稀疏/密集别名深页及重复请求。真实执行、索引 fresh/rerun 和旧边界回归由主控负责。

### 取证版本与验证命令

05:55:16 UTC 读取 SHA-256（均为该时刻工作树，不是已提交版本）：

```text
4d23a64b9cb15068c759a55feff833a92e287feb1033ba35d0e50c396c0f59ec  backend/internal/catalog/search_aliases.go
0aa4491dada13915d4f8395ed134faceaf5ef7a7ecdccafdf2ce3515ce2e7753  backend/internal/catalog/search_probe_sql.go
6113482b1bdd6a9ef4bc5620617a64cb9c81de1e668e6196258d613be870808c  backend/internal/catalog/search_ranked_sql.go
4fcca1990ec08319958a6a880974af945987ee9b97b1381520a10211231c7fc0  backend/internal/catalog/search_test.go
3a6db60b7460afb362ce5d7f01f796d0210e5fa7655b00d45c43af7e3ffec0c5  backend/migrations/0030_artist_search_subject_indexes.sql
2d5c4a95351c5d90ef3c9ad0e6bdf58f7fe97fc7bc849abcdce9b1be6d83fe2f  backend/internal/app/catalog_scale_i10_lifecycle_test.go
f16953b47dfd941c0d19cec0f441ea1f6bb3222f578009503412617f3e03c947  backend/internal/app/catalog_scale_i10_test.go
67a661d46d9762571fc507c31e9091aaa354d957b93017416a782a577b28b4ab  backend/internal/app/catalog_scale_i10_unit_test.go
```

实际仅执行 `date -u +%FT%TZ`、`git diff --stat -- backend`、`git diff -- backend/internal/catalog`、`git status --short -- backend`、`sha256sum <上述文件>` 以及 `cat`/`sed`/`nl`/`rg` 静态读取。Lint、TypeCheck、Tests 均**未执行（主控要求本轮只读，由主控验证）**，没有 PG/性能实测结论，也没有重用 I8 PASS 作为本轮通过。

## 05:57 UTC：create 入口结论与旧计数计划对应

### 入口即时结论

**未发现阻止唯一一次 create 的启动/生命周期错误或隐式重建。** `ScopedURL` 固定随机 schema；迁移连接关闭后才装载；maintenance 同步返回 ready 后才开始只读测量；`New` 的暂停选项防止第二个 worker。schema 清理为显式动作，测试 Cleanup 仅关连接/会话等资源；最终报告在资源 Cleanup 之后封口。reuse 的搜索读取会自行调用字表预检，不必为加载字表调用会写代次的 InitializeCatalog。

正式 create 前仍建议先修前节第2、3项（热门建议结果断言与测完立即落盘）；第1项复用夹具核验最迟第一次 reuse 前补齐。这是证据可信度要求，不是新增性能门槛或要求重建夹具。

05:57:18 UTC 再读三个文件：生命周期文件 SHA 未变；`catalog_scale_i10_test.go` 为 `bd301aeaa2d029db4e414f881c5e209fef34cc5868b2e6eb69694652839a6f0f`，unit 为 `f5bd5b256cef7291d3913ec7550cb11cd170907fb9f8d32006a840abac175d03`。已新增队列响应字节数/计数记录与一次 `scaleCancelArtistQueue`，在全部正式热门测量后、仅 create 执行。该取消 helper 等待真实候选 SQL 被关系锁阻塞，再取消，拒绝200/部分items，最后回滚锁并检查连接可用；静态可确认不是预先取消 context 的假测试。前节三项问题在该版本仍存在。主控报告编译/纯测/vet已通过，本检查者尚未读取其原始日志，未将其记为本人验证。

### 旧 explain-03 的1322/2014ms不是首屏 bounded 计数

只读解析旧 `scale-i8-20261105T025149Z/explain-03.json` 与 `final-plan-index.json`，按 **0起始 index** 对照实际 SQL，不根据耗时猜入口：

| index | Execution Time ms | 关系与LIMIT | 入口对应 |
| --- | ---: | --- | --- |
| 6 | 36.840 | tracks唯一实体；稀疏/密集各有 `LIMIT 1001` | 新 `/search?type=tracks` bounded count |
| 7 | 30.937 | release_groups唯一实体；稀疏/密集各有 `LIMIT 1001` | 新 `/search?type=albums` bounded count |
| 8 | 1322.081 | release_groups；没有 `LIMIT 1001` | 旧 `/release-groups?q=…` 精确 count |
| 9 | 2014.730 | releases；没有 `LIMIT 1001` | 旧 `/releases?q=…` 精确 count |

index8 SQL SHA `9b9b5b903bf0d374f135d409c97d06f0082ddb733e97a8858394a672209e6ac6`，index9 SQL SHA `09d44f5310a5ef732be2caba24daa39a3dc5b718b6f6bc937d9a0847c46f0171`；参数均是规范化“周杰伦”的7个搜索参数、代次1。代码调用链吻合：`Search → countSearch(...true)`，旧 `ListReleases/listReleaseGroupsSearch → countSQL(false, attention)`；建议不调用实体总数计数。

因此当前 `normalizedCountSQL` 的 `!bounded` 改动确实针对旧精确计数慢计划，不应声称它直接修复了I10首屏计数，也不能以index8/9推断首屏遗漏同等慢查询。bounded 分支结构没改，公共别名子查询拆 OR 及0030索引仍可能影响它的实际计划；是否足够只能由本轮热门测量与 endpoint 分开的计划确认。完整来源→唯一基表半连接的精确计数等价性见前节，未发现静态正确性回退。

本节实际额外执行仅 Python JSON 读取与字符串/SHA核对、源码 `sed`/`rg`、`sha256sum`；无DB/test。所有Execution Time均为历史原始计划，不是I10当前性能。

## 06:02:45 UTC：三项修复定点关闭

**前三项 P2 均已由 owner 修复，静态复核关闭；本检查者未改实现，未执行 DB/test。**

1. `i10FixtureFacts` 已移出 `actions.Build`，所有 non-cleanup 模式均读取原规模、倾斜、参与身份/别名及署名事实；ANALYZE 仍只在 create。fixture 文件读取与 `i10ValidateFixtureHash` 也在 Build 分支外，在复用时拒绝不同 SHA；cleanup 先返回，不被夹具断言阻碍。没有引入复用重建或 maintenance。
2. 所有测量循环的建议校验改为 `i10ValidateSuggestions(q, body)`；热门两词强制专辑/曲目各5项且有更多、艺人恰好目标身份1项且无更多；explain 模式的真实响应也校验。新增纯测试包含空三栏、少项、错误身份、错误 has_more 等反例。
3. 每个热门词 `i10MeasureCase` 返回后立即 `i10CheckpointCase(&report,item,save)`，该 helper 先追加含全部样本和错误的 item 再同步 save，之后才进入 EXPLAIN。新增纯测试读取检查点，验证30样本及首次失败仍在；最终 Cleanup 不是保存已完成热门测量的唯一机会。

该版本未发现剩余 create 生命周期/执行入口阻塞点；主控可在 owner 最终纯测通知后执行唯一一次 create。这里只批准静态检查项关闭，不预判性能或实际测试通过。

本次边界 SHA-256：

```text
2d5c4a95351c5d90ef3c9ad0e6bdf58f7fe97fc7bc849abcdce9b1be6d83fe2f  catalog_scale_i10_lifecycle_test.go
dfc2e4f174ec0085bc31d2661463e163569bea9329059df6ac103cdbe29e0534  catalog_scale_i10_test.go
3f5ef8116c06950c18d04cdeaf0cf17565a0e5bc48652b09b45000775f4e4a6b  catalog_scale_i10_unit_test.go
```

实际命令仅 `date -u +%FT%TZ`、`sha256sum backend/internal/app/catalog_scale_i10*`、`rg`/`sed`/`tail` 读取上述修复与反例；Lint/TypeCheck/Tests仍由owner/主控执行，本检查者未运行。

## 本轮定点检查闭合

主控通知：唯一 schema 的 create 已启动，harness 冻结，无其他 DB 任务。本检查者至此结束本轮工作，不再修改实现、触碰数据库或启动验证；后续正式结果仅另行读取文件核验。

### 发现（已修复）

| 文件 | 问题 | owner 修复与本检查者结论 |
| --- | --- | --- |
| `backend/internal/app/catalog_scale_i10_test.go` | reuse/explain 未重新证明原规模 | non-cleanup 全部执行只读夹具计数/倾斜/别名断言及文件 SHA 校验；静态关闭 |
| 同上、`catalog_scale_i10_unit_test.go` | 热门建议空结果可能通过 | 专用数量/身份/has_more 校验接入测量及热门 explain 响应，补空结果等反例；静态关闭 |
| 同上 | 已完成样本在 EXPLAIN 前未持久化 | `i10CheckpointCase` 在 EXPLAIN 前追加完整 case 并同步保存，补失败样本落盘反例；静态关闭 |

### 发现（未修复）

本轮三项确定问题无剩余。SQL拆支/精确计数未发现静态语义回退；不以此替代真实PG正确性或性能证据。正式 create 尚在运行，热门 p95、补测及计划结果均待后续原始文件核验，**未判性能通过**。

### 验证

- Lint：主控报告 `go vet ./...` exit0；本检查者未执行，尚未独立核对日志。
- TypeCheck/构建：主控报告 `go build ./...` exit0；本检查者未执行，尚未独立核对日志。
- Tests：主控报告 `TestI10` 选择集 exit0；未提供完整命令，本报告不补写参数或声称全量通过。本检查者未执行测试。
- 本检查者实际执行：前节列明的源码/历史JSON只读检查、SHA/时间边界记录及本报告写入；无DB、无基准、无race、无全量测试。按用户要求，验证由主控集中执行，避免干扰唯一正式夹具。

## 首次 create 离线结果核验（2026-11-05 06:38 UTC）

证据根：`/home/qlqq/.local/share/roomusic-evidence/m5-artists/i10-search`。只读 `attempt-01/summary.json`、19个计划 JSON、`fixture.json`、`create-command.txt`、`create.log`、`create-exit.txt`、`create-source-sha256.txt` 与 validation 最终日志；没有数据库连接、测试或基准执行，也未审 owner 尚在修改的下一轮读取优化。

### 结论：本次采集完整，I10性能仍失败

`Finalized=true / Complete=true / Pass=false`，create exit1，日志 benchmark FAIL；不是超时或缺样本造成的未完成。两词分别5warm/30正式样本，建议/整屏及三栏独立耗时数组长度齐全。用原始数组重新按 nearest-rank 取第29项，四个 p95 与 summary 精确一致：

| 查询 | 建议 p95 ms | 三栏首屏 p95 ms | 按各≤1000ms判断 |
| --- | ---: | ---: | --- |
| 周杰伦 | 1071.245544 | 931.579849 | 建议FAIL，首屏PASS |
| 周杰倫 | 1095.952744 | 928.844770 | 建议FAIL，首屏PASS |

各 Case.Errors 为空。summary.Errors 只有最终封口添加的“测试或资源清理失败；详见执行日志”；create.log 明确记录上述性能失败，没有额外 cleanup 错误。因此不能把这个泛化字符串单独解释为清理故障，也不能因首屏通过而宣布整项通过。

### 夹具与不重造约束

报告记录100000 present＋10000 missing、10000发行、8000组、20002身份；参与池20000、4000身份有不同键别名、50000热门present轨；110000轨均具有两艺人/四credit，680000署名、820000搜索值。与完整规模合同一致。

manifest 的 schema 与 summary 一致：`i10_search_3a3d3c1012c54055840f10d909ff038c`，`ready=true / cleaned=false`；六项补测 claim 均属于 `attempt-01`。这些是文件留证，不冒称本检查者实查数据库仍在线。后续只在该夹具 reuse 热门词，不重复 create/回填/补测；本轮没有回填性能字段或验收结论，日志总运行2010.654秒也不能当回填耗时。

### 矩阵、计划及补测完整性

- PlanFiles 恰为19个互异文件：热门2词×4入口=8，单字2词×4入口=8，深页1，队列2；与预期集合逐项相等，文件均能解析。
- 共100条捕获SQL/计划记录，逐条无 error、plan非空、SQL SHA与实际文本一致；不是100种互异SQL。热门专辑/曲目各文件包含 count/page/completion，艺人文件有 count/page/completion，建议无实体总数count，符合入口合同。这里只核验计划矩阵与完整性，不从单次计划预判优化根因或热测p95。
- 单字“爱/周”各1warm/3正式样本；建议/整屏p95分别259.163/176.905ms和1045.226/949.937ms。`TargetMS=0`，仅记录，不因“周”建议超过1秒另立失败项；不等同30样本正式测量。
- 深页100记录169.570ms并有计划，形状断言完成且报告无对应错误。
- album/random队列各3次，均 returned/items/limit=10000、total=50000、missing_total=0、truncated=true；每次响应大小分别10,402,984 / 10,403,394字节。三样本p95分别1223.554 / 2355.395ms，均无新增时延SLA。
- 一次album在途取消记录0.136708ms、HTTP503、Error为空；这是预期取消请求，不是正式热门搜索503，不应计为意外故障。没有宣称完成旧5warm30队列验收。

### 验证与版本证据

运行命令记录：

```sh
ROOMUSIC_I10_MODE=create ROOMUSIC_I10_TIMEOUT=150m ROOMUSIC_I10_OUTPUT=/home/qlqq/.local/share/roomusic-evidence/m5-artists/i10-search go -C backend test ./internal/app -run '^$' -bench '^BenchmarkPostgreSQLSearchI10$' -benchtime=1x -count=1 -timeout=160m -v
```

create-source-sha256 清单包含23个源码/迁移/夹具文件，06:38 UTC读取时均与工作树一致；三个harness SHA与06:02:45复核边界一致。该核对只覆盖清单文件和读取时刻，不声称后续owner优化仍未变，也不延伸为全仓冻结证明。

- `validation/harness-unit-final.log`：9个 `TestI10*` 顶层测试均PASS、无SKIP，app包PASS/0.290s，与主控exit0报告一致；本检查者没有运行这些测试。
- `validation/vet-final.log`、`build-final.log` 确实存在且均0字节，正常成功命令可无输出。`go vet ./...`、`go build ./...` exit0仍以主控执行回报为据，空日志本身不能独立证明退出码。
- 本检查者实际只运行 `find`/`date` 及 Python 标准库文件读取、JSON解析、样本p95/矩阵/SHA比对。无DB/test/race、无源代码修改；仅追加本报告。

## 艺人候选摘要优化：在途静态检查（2026-11-05 06:42 UTC）

按主控授权只读核对 `artists_personal.go` 新delta及新增 `artists_summary_sql.go`，并读取 owner 正在编写的直接对照测试。**未发现确定正确性问题；测试尚在实施，不声明稳定或通过。** 没有DB/test，没有修改owner文件。

### counts / roles / available 等价性

- 旧查询从 `artistParticipationCTE` 取得按艺人/角色/轨/发行/组去重的完整参与关系，再 FILTER present，跨角色 `count(DISTINCT track_id/group_id)`。新查询分别先去重 `(artist_id,release_id)` 与 `(artist_id,track_id)`，只展开本发行曲目，再将两支 present 轨以 `(artist_id,track_id,release_id)` UNION。目录外键和单一medium归属保证一轨只属于一个发行，所以同艺人同轨只剩一行，`count(*)` 等价原跨角色轨去重；没有将不同艺人的同轨误合并。
- 直接曲目署名与发行继承重叠、多名字同角色、同轨多角色、发行的 release_artist/album_artist 重叠均不会乘计数；轨级署名仍不传播至兄弟轨。新逻辑不限制候选轨/名字数，没有新增 LIMIT 或热门白名单。
- album_count 在 UNION 后 JOIN 当前 releases，再 `count(DISTINCT group_id)`；同组多版本只计一组，跨组计不同组，实时归组可立即反映，不依赖重新投影；没有按代表版本是否署名过滤参与组。
- roles 从固定七项词表逐项检查该艺人的有效署名：轨级须本轨present，发行级须本发行有present轨。存在性与原完整参与关系中该role的present行等价；仅missing有的角色排除，计数跨角色去重不吞掉角色本身。排序仍使用原 `artistRoleOrder`。
- counts 没行仍 LEFT JOIN＋COALESCE 0；roles无匹配生成空数组。profile存在但无present参与的身份仍返回摘要，`Available = TrackCount > 0` 未改；不是改成404或丢掉不可用身份。
- 原资料校验/缺profile错误、输入解析/最多1000/UUID规范去重、重定向canonical解析、readiness均沿原调用链。没有个人数据写入、治理意图、版本发布或事务生命周期变化。共享 `artistParticipationCTE` 未修改，含missing详情/角色统计和其他参与读取仍用旧合同。

### 调用影响与测试边界

该私有摘要函数除搜索结果/建议补全，也由艺人列表、艺人详情和公开 `LoadArtistSummaries` 消费；这是共享只读实现优化，不是仅搜索独有路径。既有详情旧redirect身份保留自身摘要、公开批量入口解析canonical的区别仍由调用者保持，不能用只有热门搜索的结果替代这些读取回归。

新增 `TestPostgreSQLArtistSummaryMatchesParticipation` 把旧完整参与关系SQL保留为结果对照，另有明确期望：重复名字/多角色与发行继承合计3轨、同组1专辑、missing-only producer不出现在roles，另一艺人同轨独立计数；随后不刷新投影直接跨组、部分missing、全部missing并对照。该测试对本改动主要不变量有针对性；它不是已运行证据，owner还可能修订。建议主控稳定后最小集包含此新测试及现有 canonical/personal、安全空身份、列表role筛选回归，避免扩大至全量测试。

### 版本边界

```text
7fb0b933105c4563fe48b311af6f990618f83e2515e7dd47ccafeaab2e27fb6e  backend/internal/catalog/artists_personal.go
5c06ce85aa3fa7033d67a0a80d36afd9df5d4787aca6a729bea0b577d9ea8269  backend/internal/catalog/artists_summary_sql.go
9c34d889bcd78035d2e7e61b95c6410d1a07a6c4926fc73d2cc3896370ee66b4  backend/internal/catalog/artists_summary_integration_test.go
```

本节没有将首次create的失败/成功数据移到新SQL版本；只有同一保留夹具上的后续热门reuse可验证新版本性能。实际执行仅 `date`、`git diff/status`、`cat/sed/rg` 与 `sha256sum` 文件检查；Lint/TypeCheck/Tests未执行。

## 摘要SQL冻结版最终定点结论（2026-11-05 06:46 UTC）

**无阻塞，可继续同一夹具仅 reuse 两个热门词；不重造、不回填复测、不重复补测。** `artists_personal.go` 与 `artists_summary_sql.go` SHA 和06:42静态审阅完全相同，counts/roles/available的等价结论不变。测试文件冻结SHA为 `56d5d4dbb33e079e0fc158ea48681c3d1b839059adeca454e245bd47fb417c99`，新增 missing→present 恢复反例：轨数3→4、producer恢复、missing-only身份available恢复；随后再回missing、跨组、全部missing，均对照旧参与关系SQL。

### 实际外部回归证据

已读取 `i10-search/summary-diagnostic-01/regression.log`：6个顶层PG测试PASS、0 SKIP，另4个别名子测试PASS，catalog包PASS/16.000s。不是本检查者执行，也不是全仓回归。记录命令为：

```sh
go test ./internal/catalog -run "^TestPostgreSQL(ArtistSummaryMatchesParticipation|ArtistReadPreparingAndUnavailable|ArtistReadParticipationAndAliases|ArtistReadCanonicalPersonalAndSafety|ArtistListCountsAndSearchDeepPages|SearchAliasSubjectBranches)$" -count=1 -v
```

覆盖新增完整关系对照、准备/不可用身份、参与/别名、canonical个人读取安全边界、列表计数/深页及搜索别名传播。未发现需要继续阻塞本次热门复测的正确性问题；没有扩展修改personal写入。

### 同库诊断边界

只读比较 `before.json` 与 `after-production.json`（不是仅候选SQL的candidate.json）：schema都为首次create保留的 `i10_search_3a3d3c1012c54055840f10d909ff038c`；结果完全一致：热门身份5000专辑/50000轨，roles为release_artist/album_artist/track_artist。两份计划的SQL SHA与文本一致，prepared计数均为custom5/generic3；计划执行时间976.411→432.606ms，顶层temp read/write blocks为4337/4343→356/357，与comparison.json吻合。

这是相同规模单条摘要SQL的串行prepared诊断，不是HTTP p95，也不是新的30样本验收；不能覆盖attempt-01建议失败或据此宣布I10通过。新SQL只有后续保留夹具上的热门reuse可取得正式结果。

本次仅 `date`、`sha256sum`、`cat`/`find` 和Python读取日志/计划核对；无DB/test、无源码修改。后续等待正式reuse文件再核验，不重复审旧harness。

## 最终热门 reuse 纯文件核验（2026-11-05 06:49 UTC）

**I10两个热门词的最终定向性能验收通过。** 本结论只针对最终摘要SQL版本的热门建议与三栏首屏；首次create失败原样保留，单字/深页/队列补测仍属于摘要SQL优化之前的attempt-01，不冒充最终版本全矩阵测量。

### 运行、完整分布与阈值

读取 `i10-search/attempt-02/summary.json`、`reuse.log`、`reuse-exit.txt`、`reuse-command.txt`：`Mode=reuse`，`Finalized=true / Complete=true / Pass=true`，Errors为空，benchmark及app包PASS，退出码0。运行命令：

```sh
ROOMUSIC_I10_MODE=reuse ROOMUSIC_I10_SCHEMA=i10_search_3a3d3c1012c54055840f10d909ff038c ROOMUSIC_I10_TIMEOUT=150m ROOMUSIC_I10_OUTPUT=/home/qlqq/.local/share/roomusic-evidence/m5-artists/i10-search go -C backend test ./internal/app -run '^$' -bench '^BenchmarkPostgreSQLSearchI10$' -benchtime=1x -count=1 -timeout=160m -v
```

两个case均记录5次预热、30次正式测量；已审冻结harness将冷请求和预热排除在正式数组外。实际文件内建议/整屏各30个有限正数，三栏墙钟及DB累计各30项、建议DB累计30项；没有空数组或缺样本。逐个原始数组重算median与nearest-rank p95，均精确吻合summary：

| 查询/指标 | 样本数 | median ms | p95 ms | 最大值 ms | ≤1000ms |
| --- | ---: | ---: | ---: | ---: | --- |
| 周杰伦／建议 | 30 | 518.690272 | 557.929750 | 602.769276 | PASS |
| 周杰伦／三栏首屏 | 30 | 380.285107 | 400.519897 | 409.780146 | PASS |
| 周杰倫／建议 | 30 | 524.065663 | 564.673320 | 614.329780 | PASS |
| 周杰倫／三栏首屏 | 30 | 378.876411 | 395.359658 | 411.642202 | PASS |

各case.Errors为空。首屏是三栏并发的收齐墙钟；没有把三栏p95相加、平均或把累计SQL时间当用户等待。文件包含完整30项正式分布，本检查者未另行选样或测量。

### 同一schema、原规模与版本

- attempt-01、attempt-02及manifest schema完全一致：`i10_search_3a3d3c1012c54055840f10d909ff038c`；数据库指纹、nonce和fixture SHA沿用原manifest。
- 两次Counts映射逐键完全相同，PG设置映射也完全相同：100000present＋10000missing、10000发行/8000组、20002身份、20000参与池/4000别名身份、50000热门轨、110000轨两艺人/四credit、680000署名/820000搜索值。reuse已实际运行完整只读夹具断言，非只引用create数字。
- `reuse-source-sha256.txt` 的25个文件在本次读取时均与工作树一致；包含已定点审阅的 `artists_personal.go` / `artists_summary_sql.go` 及冻结harness、0030与夹具。两次清单共享文件SHA无变化；create清单未包含这两个摘要文件，不能用“共享SHA不变”否认已明确记录的摘要SQL优化。

### 无重复回填或补测；计划矩阵完整

- reuse入口沿已审模式选择，不执行create的fixture/InitializeCatalog/maintenance分支；日志只记录reuse与两热门测量，没有回填验收。没有新增旧路由对照。
- attempt-02的Supplemental、Queues、QueueCancellation均为null，DeepPageMS默认0；**这里的0/null表示本轮未运行，不是零耗时或自动通过。** manifest六项补测归属仍全是attempt-01，未被reuse覆盖。
- PlanFiles恰好8个互异文件＝两热门词×建议/专辑/曲目/艺人四入口。逐个解析成功，共36条生产SQL计划记录，均无error且plan非空，SQL SHA与文本一致；专辑/曲目/艺人均含count/page/completion，建议无实体总数count。不是36种互异SQL，也不是重跑旧26词矩阵。

### 必须保留的补测时间边界

单字“爱/周”、深页100、两种10000首队列的时延/字节数，以及一次在途取消，**只在attempt-01执行，发生于最终artistSummarySQL优化之前**。相关数字及19文件/100计划完整性见前节create核验。用户禁止重跑，故本轮没有重复；不能把这些历史补测数字标成最终摘要SQL版的性能，也不能称最终版全矩阵实测通过。最终版对应的补充正确性证据是冻结后6项PG回归PASS/0SKIP，而正式性能证据仅为本节两热门5warm30。

### 清理与检查者边界

读取过程中主控已进行显式清理：最新manifest `cleaned=true`，`cleanup.log` 指向同一schema、mode=cleanup、attempt-03并显示PASS。这是文件证据，不是本检查者查询DB或Docker；不据此擅自宣布容器/卷/网络均0，基础资源清理由主控负责。

本次实际仅运行 `date` 及Python标准库读取上述JSON/日志、样本统计、集合/字典比较与SHA核对，并追加本报告；没有DB/test/race、没有实现修改。三项harness问题仍已关闭，摘要SQL无剩余定点阻塞项。后端整体任务是否完成及spec/提交由主控决定。

## backend-status 最终I10小节交叉核对

已只读核对 `backend-status.md` 的“2026-11-05 I9/I10：定向热门艺人搜索完成”与原始证据，**未发现需修正的数字、阶段或范围归属问题，无剩余报告阻塞项。**

- 首轮失败与最终同库成功分别对应attempt-01/02；历史I8数字、局部计划Execution Time、同库摘要generic诊断与HTTP5warm30 p95明确分开，没有用局部SQL毫秒冒充HTTP性能。最终热门数字与前节重算一致。
- 再查attempt-02计划：专辑首屏说明159.971ms、建议专辑说明46.121ms、曲目首屏说明28.503ms逐项吻合。旧index11确有热门artist节点3366 loops、每轮过滤59999；新实际执行3366次的节点是artist＋track组合索引，不把那些loops=0的计划分支误当实际收益。
- `exact-counts-01/plans.json` 只有热门词的albums/releases精确count，两条都没有LIMIT1001；Execution Time分别758.703/651.655ms，区别于command.log的完整EXPLAIN墙钟774.677/656.606ms。报告正确标为旧路由精确计数，不是首屏bounded计数；未宣称重新跑旧HTTP对照。
- 单字/深页/队列/取消明确限定attempt-01、最终摘要SQL优化前，且少量样本无新增SLA；最终未重跑这些补测的边界写明，没有将补测0/null作为新版本结果。
- `validation/catalog-search.log` 核对为catalog20个顶层PASS，再附platform迁移fresh/rerun 1个PASS，无SKIP；报告已将额外迁移测试如实列为超出“查询所在包”严格限制的范围偏差，不隐藏为查询测试，也不称全量测试通过。另两份dense-aliases日志分别保留参数$5类型失败及复验PASS，符合报告。摘要6项PG和harness9项纯测计数均与前述日志一致。
- `validation/vet-build-after-summary.txt` 明确记录最终摘要修订后vet/build命令、exit0及无输出，补足先前只有空日志的执行回报；仍属主控/owner执行证据，而非本检查者执行。
- `resource-cleanup.txt` 实际内容为2026-11-05 06:50:17Z、containers=0、volumes=0、networks=0、test_env_removed=yes；与同schema的cleanup日志PASS及manifest.cleaned=true一致。可据此记录主控实测清理完成；本检查者没有独立调用Docker或DB。

本次实际仅源码任务文档的 `rg`/`tail`，仓外日志的 `cat`/`find`/`ls`，以及Python读取JSON、节点与计数对照；无DB/test、无owner文件改动，仅追加本文件。该核对不将I10局部完成扩大为整个M5任务完成，不代主控提交或归档。
