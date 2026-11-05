# M5 ④ 搜索规范化与全局搜索、⑤ 艺人与别名：联合技术设计

## 0. 状态、交付边界与规划门禁

- 日期：2026-11-04。状态：**主会话已裁决的联合技术设计；本轮仅修订文档，不是实施批准**。
- 本次只修订本文件；不修改代码、迁移、spec、PRD、任务状态，不提交 git。不运行 `task.py start`。
- 本次按 `trellis-start`、`trellis-brainstorm` 做证据核对与方案收敛。运行时回退任务指针指向扫描反馈，**本文件的任务仍以用户指定的 `11-04-classical-m5-search` 为准**；不替其他实现线切换指针。
- 最终规划选择走**人工选择路线**：用户通过主会话于 2026-11-04 明确接受 D1–D8，并裁定短词性能、计数阈值、旧数据候选与规则同步方式；随后修订D2为**启动立即ready、后台回填、旧搜索回退与原子切换**，覆盖此前阻塞启动的决定。本文件已同步正文和§15。选择理由是“用户用起来方便、人性化”，同时保留稳定身份及个人收藏意图。没有采用代理选择路线；产品裁决已经记录，不等于获准实施。主会话在后续实施准备中将该选择记录同步两个子项 PRD，补齐实施计划与上下文清单；本轮不越权改这些文件。
- 依据：[④ PRD](prd.md)、[⑤ PRD](../11-04-classical-m5-artists/prd.md)、[父任务](../11-04-classical-m5/prd.md)、[收藏 PRD](../11-04-classical-m5-favorites/prd.md)。②+③ 与④+⑤是两条实现线；本文件不替收藏线决定内部表名。

| 子项 | 独立交付 | 不属于该子项 | 依赖与出口 |
| --- | --- | --- | --- |
| ④ | 共用规范化函数及内置数据；可重建搜索投影与索引；现有两个 `q` 接口升级；全局建议、专辑/曲目结果、可播放曲目 DTO、两栏页面 | 不建艺人实体，不做别名治理、不启用艺人栏 | 后端不依赖⑤；心形与加入歌单的页面组合依赖②/③公开合同。通过 S1–S5；⑤未上线时只有**专辑、曲目两栏** |
| ⑤ | 艺人实体、署名归属、别名种子、合并/拆开/改名、候选/忽略；艺人浏览/详情/队列；艺人收藏；所有规定位置的链接；全局第三栏和别名传播搜索 | 不重做④规范化，不做外部资料、传记、照片、合作网络、拼音搜索 | 显式依赖④规范化与投影版本机制、②收藏扩展位、③歌单动作。通过 R1–R7；治理遵守 R4 |

阅读提示：本文件覆盖两项跨包迁移，体量超过单项上下文注入预算；实施agent应按目录分段读取（后端重点§2–11、前端重点§9–12、验证重点§13–15），不要依赖整文件被一次注入而遗漏尾部合同。

两项共用查询语义、更新事务和 DTO 基础，但分别验收；不能把⑤未完成的能力算作④已交付。④保留接口扩展位，不返回虚构艺人。所有源音乐只读，规范化从不进入来源身份、内容指纹、organizer 分桶或 ReleaseGroup 自动合并规则。

## 1. 已核实的实现事实

| 事实 | 代码/合同锚点 | 设计约束 |
| --- | --- | --- |
| `q` 只在发行/组列表；LIKE 转义符为 `~`；上限是去首尾空白后 200 **字节**；空查询是浏览 | `catalog/read.go:28`、`groups.go`、`catalog/httpapi/handlers.go` | 保留旧路由、参数、响应形状、空查询与可见性，不偷加两字下限 |
| 所有发行展示字段应来自 `release_effective` | `catalog/internal/releasesql/release.go`、迁移 `0017_governed_operations.sql` | 投影不能直接用候选值盖掉修正；修正、清除、撤销、认领都要刷新 |
| `PersistCandidate` 自己开短事务，写当前观察、credits、发行字段；封面在提交之后另处理 | `catalog/ingest/persist.go`、`library/scanner.go` 的 `persistOneCandidate` | 搜索和艺人署名挂在候选事务内，不再追加一次“成功后全库解析” |
| `tracks.artist` 经硬编码别名处理；credits 在 parser 中已处理；原 Artist 观察与 credits 的保真程度不同 | `metadata/artist.go`、`ingest/persist_track.go:72`、`library/parser/tags.go:93`、`ingest/observation.go:66` | 不能声称旧 credits 原文全部保存在 `track_observations`：`fieldObservations` **没有写四类 credit 原文** |
| 重复的 `ARTIST` 标签当前会覆盖单个字符串；当前观察表每轨每字段唯一 | `library/parser/tags.go`、迁移 `0011_scan_staging_and_identity.sql` | “同轨同角色两种写法”的严格证据需要新增有界原始标签结构，不能靠最终拼接串反推 |
| 核心 catalog 不能导入 metadata、ingest、governance；ingest 不导入核心 | `app/dependency_rules_test.go` | 共享规范化与投影帮助包应保持无环，不能为了复用导入治理层 |
| `operations.Run` 开 READ COMMITTED 事务，记录主目标 `expected + 1`；其他资源由执行器保护 | `operations/operations.go`、`kind.go`、`records.go` | 合并必须检查并递增双方艺人 revision；不能只保护被保留者 |
| 历史曲目 DTO 已具有播放所需的安全音频事实，批量补全限 1,000 | `catalog/track_summary.go`、`listening-contracts.md` §6、`app/routes/history-playback.tsx` | 搜索及艺人曲目复用，不用详情/预检 N+1，也不返回源路径 |
| 队列最多 10,000；历史队列的 1,000 是历史产品合同，不是通用队列上限 | `player/engine/queue.ts`、历史合同 | 艺人队列单独定义上限；不能误用历史截断语义 |
| 迁移全部在事务内执行；旧迁移有校验和 | `platform/postgres/migrate.go`、数据库规范 | 不改旧迁移，不用 `CREATE INDEX CONCURRENTLY`，SQL 迁移不能调用 Go |

已阅读所指定的 catalog、扫描、治理、数据库、目录、错误、播放历史、产品目标、路线 R4、ADR 0002/0003 正文及 decisions。已裁决的新规则在实施时写入 `0002-metadata-overlays-decisions.md` / `0003-governed-operations-decisions.md`，具体同步清单见§13.2，不继续膨胀 ADR 正文；本轮不修改它们。

## 2. 包边界与公开合同

搜索仍由 **catalog** 拥有 PostgreSQL 查询，不另造跨能力 SQL 聚合器。新增下列包均有实际调用者；具体文件可按职责细分，不建空架构层。

| 包 | 职责 | 允许的内部依赖 |
| --- | --- | --- |
| `catalog/textnorm`（④） | 纯 `Text`、`CatalogNumber`、版本、内置 OpenCC 加载；无 DB、HTTP、文件系统运行期访问 | 无；仅标准库及**已存在**的 `golang.org/x/text` |
| `catalog/internal/searchindex`（④） | 搜索值表的读写 SQL、同事务刷新、通配符转义、排序/命中公共片段 | `textnorm`、`internal/releasesql`；⑤可增加 `internal/artistgraph`，后者不能反向导入它 |
| `catalog/internal/writegate`（④） | catalog 投影写者共用的事务级 advisory gate；只有 `Lock(ctx, tx)` | 无；它不是新的操作记录层 |
| `catalog/internal/artistgraph`（⑤） | 名字解析、署名事实/有效关联、派生展示、首字母表及查询片段 | `textnorm`、`metadata`、`platform/uuid`；不导入核心或 operations |
| `catalog/maintenance`（④、⑤扩展） | 短启动版本登记、单写者后台回填、断点恢复与原子发布 | `textnorm`、`internal/searchindex`、`internal/writegate`；⑤加 `internal/artistgraph`、`metadata` |

`catalog` 增加对 `textnorm`、`internal/searchindex`、⑤ `internal/artistgraph` 的读取依赖；⑤的公开事务内身份解析函数还允许依赖`internal/writegate`取得一致读取锁，不把写CRUD塞进只读Store；**仍不导入 metadata/ingest/governance**。`ingest` 增加三个内部投影/锁包，保留 metadata/uuid；`governance` 同样增加必要的投影/锁包。HTTP 仍只解码、鉴权、调用 Store/执行器、呈现错误。`operations` 仍只依赖 uuid；通过 `Kind` 注册获得艺人能力，不直接联艺人表。

启动由app装配maintenance：先完成短版本登记，再启动正常HTTP/扫描与受应用context管理的后台回填worker，**不再用阻塞式 `app.PrepareCatalog` 等待全库完成**。应用ready不依赖搜索/艺人投影完成；worker退出/关闭数据库前有有界等待与取消。`app` 可依赖 maintenance，`platform/postgres` 不反向依赖 catalog。为 `packageRules` **逐包登记上述增量**，禁止把 `catalog/*` 全部放开或让 parser 查数据库。parser 继续只调用纯 metadata 拆分函数。

catalog 对其他能力提供：

- 现有 `LoadTrackSummaries`，⑤追加身份对应字段；保持输入上限与 missing 语义。
- 新 `LoadArtistSummaries(ctx, ids)`，不同有效 UUID 最多 1,000；无效跳过、不存在无键；包含原 ID、规范目标 ID、可用性与显示名。艺人尚未发布时返回可辨别的 `ErrArtistCatalogPreparing`，不能把未就绪当成全部ID不存在；普通TrackSummary补全仍成功并返回空refs。
- 收藏需要的事务内艺人解析合同与解析变化通知见 §11；不允许收藏包直接查这些私有表。

## 3. 规范化函数、内置数据与版本

### 3.1 函数和准确顺序

确定合同：`Text(raw string) string`、`CatalogNumber(raw string) string`、`Version = 1`；对已校验 UTF-8 的有界文本执行确定函数。入口另负责长度/控制字符校验；函数不截断输入、不猜语言、不分词。

1. **NFKC**：全半角、兼容字形归一（`norm.NFKC`）。
2. **完整大小写折叠**：`cases.Fold()`，不是 `strings.ToLower`；比如 `Straße` 与 `STRASSE` 等价。不跨请求共享有可变状态的 transformer。
3. **去变音符**：NFD 分解后，仅删除附着于拉丁基字母的组合变音标记，随后 NFC；`Beyoncé → beyonce`。保留假名浊点/半浊点，不把 `が` 当 `か`，不剥掉其他文字的组成部分。这是 D1 已裁决的范围；不额外把 `Ł` 音译成 `L`。
4. **片假名 → 平假名**：普通片假名 U+30A1–U+30F6 对应转换；重复记号相应转换；浊音特殊字符先按规范分解为可映射基字母加标记再合成。长音符保留。不把读音近似、大小假名、汉字和罗马字等同；扩展小片假名没有标准对应者原样保留，测试固定范围。
5. **繁 → 简**：逐 Unicode scalar 应用 §3.2 的固定字表；不加载短语、地域词汇或外部配置。
6. **空白折叠**：Unicode 空白首尾去除，内部连续空白变成一个 ASCII 空格。
7. **只对唱片编号**：在以上结果上删除所有空白与 Unicode `Pd` 连字符；同时把编号常见 U+2212 减号视为连字符。其他标点保留；标题、姓名不执行这一步。编号专用搜索分支使用同一个 `CatalogNumber(q)`。

`Text` 用于标题、姓名、四种角色、厂牌；只用在匹配、索引和约定的排序键，不覆盖显示原文。`CatalogNumber` 不用于艺人身份。`q` 产生两份键：普通键与编号键；编号键为空时关闭该分支，不能构成 `LIKE '%%'`。

必须满足幂等性 `N(N(s)) = N(s)`，以及新路径的入库、查询、后台回填采用相同版本；未发布期间只走§4.4旧匹配，不混查不同版本投影。S1 五组例子全部双向测试；另测组合/预组合重音、半角带浊点假名、补充平面汉字、NBSP、空串、`% _ ~` 字面量与极长标签。

**D1 明确正反例**：`N("Beyoncé") = N("Beyonce")`、`N("ガ") = N("が")`、`N("ﾊﾟ") = N("ぱ")`；但 `N("が") ≠ N("か")`，`N("ば")`、`N("ぱ")`、`N("は")` 两两不同，`N("炼") ≠ N("链")`。组合/预组合浊点和半浊点均覆盖；用真实 OpenCC 表锁定 `鍊/鏈` 的逐字输出，确保不会因 ST/TS 全边闭包把“炼”和“链”合并。艺人解析集成测试重复这些反例，证明不仅字符串不同，默认身份键也不会误合并。

### 3.2 OpenCC：数据不是运行期依赖

建议固定 **OpenCC `ver.1.1.9`，提交 `556ed22496d650bd0b13b6c163be9814637970ae`**，不使用浮动 main、不引入 Go binding、CGO、共享库或运行期下载。该版本只是可复现的数据基线，不声称是最新版本。

未来随代码新增：

```text
backend/internal/catalog/textnorm/data/opencc/
  STCharacters.txt
  TSCharacters.txt
  LICENSE
  SOURCE.md
```

通过 `//go:embed data/opencc/*` 内置原文件与 Apache-2.0 许可证，保留版权/NOTICE（上游若提供则一并保留）；`SOURCE.md` 用简体中文记录仓库、提交、源路径、原始 SHA-256、算法与生成/校验方式。构建产物必须包含许可，不能只在开发机缓存里放一份。

本次只读核对所得的原文件校验和：

| 文件 | SHA-256 |
| --- | --- |
| `TSCharacters.txt` | `6b5a0a799bea2bb22c001f635eaa3fc2904310f0c08addbff275477a80ecf09a` |
| `STCharacters.txt` | `ed1d268e0ad028511dcf5b0089faed0a980ad332449ec11d481ceefde6879f41` |
| `LICENSE` | `b534e465949558eec2597b04f5092b5e161236a68dfbfd04d547592ac3964308` |

**选用规则**：TS 行的第一个目标字为 canonical；对 ST 行，只为 TS 未覆盖的繁体目标补逆向映射，且要求其候选简体经 TS 收敛后唯一。冲突项不猜、不扩展，原样保留并以固定夹具记录。加载时解析 TAB 与空格分隔的全部候选、验证每项恰好一个 scalar，检测重复键、坏 UTF-8、自循环以外的循环，求终值后保存只读 map。自映射合法。无终值/格式损坏使对应投影构建失败、禁止发布，记录安全诊断；旧目录/播放与旧搜索继续服务，不能悄悄发布半套规则。正常发行包由真实embed测试保证字表可加载。

**不采用 ST/TS 所有边的无向连通分量**：本次核对这种闭包会把 `炼/链/鍊/鏈` 等纳入同组；用于⑤自动身份归一时会多合并本来不同的人。逐字第一候选仍有 `發/髮 → 发`、`乾/幹 → 干` 等歧义，是 PRD 已接受的字级宽匹配，不是词义转换。不要把“所有 ST 候选都必须归同一位”作为验收条件；该策略已由 D1 裁决，不采用闭包方案。

加载采用显式 `LoadEmbedded` + `sync.Once` 缓存结果与错误，做有界预检后只读使用，不用全量回填充当启动预检；不在 `init` 中用 panic 吞掉错误上下文。单测直接注入小字表覆盖坏行/冲突/循环，真实 embed 测试验证文件 SHA、已知繁简对和归一幂等性；并发 `-race` 验证没有共享 transformer 状态。

### 3.3 版本与重算：后台回填，服务立即可用（D2修订）

**D2的新决定替代原“回填完才ready”方案。** 数据库连接、兼容结构迁移及短版本登记正常后立即提供HTTP并报告`/readyz=200`；目录、播放/预检/音频传输、历史、已有个人音乐功能、扫描与M3不等待回填。常规数据库故障仍按现有ready/503合同处理；投影未完成不是整站不可用。结构迁移不得偷偷执行十万轨数据转换，不能以“后台”为名在监听HTTP前等待任务清单或索引数据灌入。

#### 3.3.1 状态、版本与单后台写者

- 分开维护`textnorm_version`、`search_projection_version`、⑤`artist_projection_version/initials_version`；共同字符版本变化时相关能力一同进入构建，⑤单独补艺人且不改变④基础字段时可以保留④已经发布的新搜索。
- `catalog_projection_state`按能力保存`target_generation`、目标版本元组、`published_generation`及已发布版本、`build_id`、`status=preparing|building|ready|failed`、阶段/游标/计数；另有单调`catalog_epoch`。开始一轮构建和每次能力发布才递增epoch，不按批递增。**只有目标代次/版本与已发布代次/版本完全相等且状态ready时，才可读该能力的新投影。** 艺人ready还要求依赖的搜索字符版本、名字/有效署名/主名/候选及收藏解析缓存都就绪。
- 短启动事务在`CATP`内登记新目标并关闭受影响的新读能力，然后才允许本进程正常写者运行。全量对象发现/回填都在HTTP启动后进行；若多个新进程同版本启动，只复用同一build，不重置已完成数。仍不支持**不知道此写协议的旧二进制与新二进制混合写入**；正常应用替换后立即可用，不为回填再停服。
- 一个应用内worker承担此库的后台构建，用专用连接的maintenance advisory lock跨进程选主；该锁只排斥其他回填worker，**不是CATP写gate**，不能阻止扫描/M3。每个回填批事务也必须在持此锁的同一专用连接上运行，不能选主连接已断而另一池连接还在写；失锁/关闭context就停止调度并回滚未提交批，释放连接后其他进程可续跑；不新建通用任务平台，不新增Go依赖。
- `catalog_projection_work`保存该build初始快照的待处理subject ID（包括missing），唯一`(build_id,phase,subject_kind,subject_id)`，及完成状态/游标；清单和阶段初始总数在后台一致快照中登记，不在启动路径做。清单之后新建的UUID由实时写者直接写目标代次，无论UUID在游标前后都不漏；最终覆盖校验不以“游标到最大UUID”代替完整性。

#### 3.3.2 最小“双写”与不覆盖新值

这里的双写是**权威目录事实/有效修正 + 目标版本的新投影**同事务更新，不是要求同时维护两套规范化算法。搜索能力自身重建期间所有搜索回到现有原字段ILIKE路径，因而可在未对外读取的新投影中逐subject替换旧版本；仅⑤艺人重建且④未受影响时，继续读取已发布的④基础搜索、暂不使用艺人分支。不需要把旧规范化索引也继续双写。保留旧目录字段和ILIKE代码，不能先删旧路径再开worker。

1. 新增`catalog_projection_subject_state`，每个`(subject_kind,subject_id)`保存单调`input_revision`（存量首见为0，此后每次事实/有效修正/有效归属输入改变时递增）以及各能力`applied_generation/applied_input_revision`，该revision只保护派生输入，**不是**治理`edit_revision`。无字段/零署名的对象同样要有完成标记，不能凭搜索行数判定覆盖。集合主名/名字投影也须有本代完成或待刷新标记。
2. 扫描`PersistCandidate`、M3修正/清除/撤销、claim双方及撤销、missing对账、影响关联的归组等路径，先取同一`CATP`，重新读当前目标代次，更新目录权威值及输入revision，再按**目标新规则**写相关搜索/署名派生与完成标记，一次COMMIT。投影写失败按原合同回滚该业务事务，不留“事实成功、投影以后再说”的窗口；读取服务及其他独立请求不因此停服。新值从提交时即可被旧ILIKE路径检索，切换后也无需再扫一次。
3. 后台只预取ID、不预取可稍后覆盖的旧标签快照。每批取得`CATP`后，在新的READ COMMITTED语句中重读当前事实、`release_effective`、名字意图和输入revision：已应用目标代次且revision一致的subject直接标记任务完成，**不再覆盖**；其余才从当前原文重建。投影、完成标记和任务进度同事务提交。禁止`ON CONFLICT DO UPDATE`无条件写入持锁前算好的值；若将来把计算移到锁外，必须在锁内CAS复核revision/代次，不一致则丢弃结果重算。
4. 两种交错都安全：扫描/M3先提交→回填看到新代次/新revision并跳过；回填先持锁→业务写等待短批提交后按更新后的事实写新投影。旧回填任务发现target_generation已变则退出，不允许写新build或发布旧build。`DELETE+INSERT`式刷新也必须在同一gate/事务内，不留读者能看到的半组署名。
5. ⑤还原旧`tracks.artist`展示时，同样只用锁内最新可信观察；已被新版扫描处理的对象跳过；若实际恢复了tracks.artist字段，则在同批递增输入revision并刷新相关完成标记。纯投影重算不虚增输入revision。不改源文件/来源身份；人工绑定、scope覆盖、主名意图、忽略及operations仍只读。规范化升级的旧键→新键映射保留原艺人ID和治理意图；人工归属冲突只使艺人构建无法发布，不能为了上线抹掉覆盖，也不能拖垮普通目录/播放。出现这类冲突需显式维护映射，相关冲突写入不得虚构归属。
6. 每批释放gate并让出执行权；默认起步50轨或10发行，按实测自适应，上限500轨/100发行。以持gate约100–200 ms为调优目标，批间让出执行权（原固定至少50 ms；本轮用户授权按实测自适应，现正整批耗时小于200 ms时按十分之一、最低10 ms，无有效耗时或至少200 ms时保守50 ms），可因播放/交互负载退避。超大发行拆成带input_revision的可恢复子批并在全部完成后标记该subject；输入revision变化就废弃未完成旧子批并按最新事实重建，若实时写者已完成该subject则直接跳过，绝不能把旧子批续写到新revision上。不一次锁住十万行；未完成发布前不对外暴露局部投影。worker只有一个在途批次，不能占满HTTP/音频读取需要的连接池；先保证前台可用，再追求回填吞吐。

#### 3.3.3 检查、原子发布与失败恢复

- 初始清单耗尽后，后台检查所有当前对象（包括清单之后新建者）都有目标代次且输入revision一致的标记；核对空值对象、missing、有效修正、名字映射/悬挂引用和相关派生。发现缺口只补缺口，不靠全局暂停扫描补齐；实时写者始终维护上述不变量。
- 切换仅用一个短`CATP`事务：再次核对build_id/目标版本、已封闭的清单、没有待处理/过期标记及需要的依赖已发布，再更新该能力`published_generation/versions/status`和`catalog_epoch`并提交。最终检查只读有索引的缺口/完成标记，不在这个事务重新规范化全库。⑤可晚于④发布；⑤发布同时启用艺人读取、别名传播、第三栏、refs与收藏解析，不能先放开治理再补关系。
- 搜索每个请求在同一只读快照中读取状态、选择**单一路径**并完成计数/分页/补全：尚未发布走§4.4旧ILIKE，发布后只读对应新代次。不把部分已回填的新结果与旧结果拼接。切换前开始的请求可完成旧快照，切换后开始的新快照全部走新路径；前端依epoch刷新并丢弃过时代次响应，见§12。
- 首填、规则升级、断电/进程取消都走同一协议。已提交批与游标保留；未提交批回滚，重启立即服务并续跑，重复执行不会新增别名票数、重种绑定或增加治理revision。worker失败只记录/退避重试：未发布的搜索保持旧路径，未发布的艺人保持preparing且给明确状态；已经单独发布且不受该失败影响的能力继续服务。普通ready仍为200。数据库故障、结构不兼容仍是独立真正故障，不伪装成正常整理。
- 没有“失败时先清空正在使用的目录”或回滚用户意图的流程。不支持退回不认识新协议的scanner继续写；旧投影清理只能在不再被快照读取后异步做，不能把清理加入发布临界区。

#### 3.3.4 每批日志与十万轨耗时估算

每个已提交批次一行结构化日志：`event=catalog_projection_backfill_batch`、`build_id`、目标代次/版本、`phase`、`completed`（已完成）、`total`（该阶段初始清单总数）、`batch_size`、`written`、`skipped_current`、`batch_ms`、`elapsed_ms`。例如`phase=tracks completed=500 total=100000 written=40 skipped_current=10 batch_size=50`。completed包含“实时写者已完成而本批跳过”的清单项，不重复累计；清单外新增对象另报`live_updated`，不偷改原分母。阶段重开/新增修复清单要另标phase与总数；重启复用持久进度，回滚批只报失败不虚增。日志不含音乐路径、标签原文或凭据；开始/失败/发布另有事件。ETA来自实际已提交批的移动平均，而不是硬编码。

此前无前台负载预算（4 vCPU/8 GiB/SSD、十万轨、一万发行、约70万–100万搜索值）为④约4–9分钟、④+⑤约8–18分钟；它仍只是**未实测的累计处理工作量估算，绝不是停机窗口**。后台让出/负载退避后，按约50%有效处理占用作规划，墙钟时间约④8–18分钟、联合16–36分钟，持续扫描/慢盘可更久。期间播放照常，不能因为超时而跳过对象或提前发布；不再要求20分钟维护停机。实施时在隔离十万轨夹具同时跑播放/搜索/扫描与M3，记录实际完成时间、gate等待及前台p95，再替换这些假设。版本一致的普通重启不做全量回填。

## 4. 搜索存储与查询计划

### 4.1 可重建的独立值表

选**独立搜索值表**而非在每个事实表加多列、也不选全库 `REFRESH MATERIALIZED VIEW`。前者能保留命中字段与原文，局部更新；后两者不适合多值 credits、别名及 M3 覆盖。它是派生投影，不是第二份 catalog 权威。

`catalog_search_values` 建议列：

- `id UUID PK`；`release_id UUID NULL FK releases`、`track_id UUID NULL FK tracks`，CHECK **恰好一个非空**。
- `field` allowlist：`release_title | release_artist | album_artist | track_title | track_artist | credit | label | catalog_number`；`role` 仅 credit 非空，四角色 allowlist；`position > 0`。
- `original_value TEXT`：有效值/署名原文；`normalized_value TEXT`：一般字段 `Text`，编号字段 `CatalogNumber`；`normalization_version`与`projection_generation`。后台可在新读能力关闭时逐subject替换，查询必须校验已发布代次；subject完成/输入revision另记§3.3的状态表。
- `short_grams TEXT[]`：该键按 scalar 产生的去重一元、二元连续片段；用于短词/无可用 trigram 的兜底索引，仍用 LIKE 残差复核。
- 每个 subject 的 `(field, role, position)` 用带 NULL 语义的唯一约束或分别的部分唯一索引约束；不按每轮扫描累计。空字段不写行。原值沿用 4,096 字节预算，规范化输出另有受测的 64 KiB 防御预算，超限拒绝该投影写入、绝不静默截词。

索引：

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;
CREATE INDEX catalog_search_values_trgm_idx
    ON catalog_search_values USING gin (normalized_value public.gin_trgm_ops);
CREATE INDEX catalog_search_values_short_idx
    ON catalog_search_values USING gin (short_grams);
CREATE INDEX catalog_search_values_release_idx ON catalog_search_values (release_id);
CREATE INDEX catalog_search_values_track_idx ON catalog_search_values (track_id);
```

⑤另给艺人当前主显示名/所有有效名字建立 `artist_search_names`（`artist_id,name_id?,original_value,normalized_value,short_grams,is_display_name,version,projection_generation`）及同类两个 GIN。它由 graph + searchindex 共同刷新，保存名字级行，**不把所有别名复制到每首曲目**。搜索命中艺人名后，经有效署名关联得到发行/曲目；这样改名或合并无需把同一个别名写十万次。

补充关联索引：有效署名 `(artist_id,role,release_id)`、`(artist_id,role,track_id)`；本体 `tracks.medium_id`、`media.release_id` 与现有索引对照后补缺，不重复建索引。长规范化键不直接做唯一 B-tree（长标签可能超过索引项限制），⑤用 SHA-256 定位再比较原键，见 §5。

### 4.2 谁更新、何时可见

| 写路径 | 同事务动作 |
| --- | --- |
| `ingest.PersistCandidate` | 写本次曲目/credits 后刷新曲目搜索值；写发行扫描列后重新读 `release_effective` 刷新发行值；⑤再解析当前署名并刷新关联。失败回滚整个候选 |
| M3 `CorrectionExecutor`、修正的 `Revert` | 意图变更后、返回结果前刷新有效发行值；专辑艺人改变时刷新修正层署名与艺人关联；不能只挂 PATCH HTTP handler |
| `ClaimExecutor` / claim 的 `Revert` | 来源与目标两边修正都会变化，必须刷新**双方**有效发行值与专辑艺人关联；只刷新目标会漏掉旧值清除 |
| 人工归组/归组撤销 | 组 ID 不重复存在搜索值中，读取实时经当前发行归组；⑤刷新参与专辑计数/候选相关派生，不改按发行绑定的拆分意图 |
| 设定默认版本/撤销 | 搜索命中成员不改变代表版本合同；代表摘要在读取时按已有规则选择，不复制到搜索表 |
| ⑤合并/拆开/改名 | 艺人ready前新命令409 `artist_catalog_not_ready`；ready后同一 `Run` 事务更新有效归属、主名/别名索引与受影响派生，不让搜索短暂指向旧实体 |
| 成功扫描的 missing 对账 / 换内容的旧轨 missing | 搜索值保留，读取总是 join `tracks.source_status`；⑤若缓存计数/主名/候选，必须同事务刷新受影响部分；不因投影引入额外负向对账 |
| 后台回填 | 持短gate重读当前事实、有效修正及人工艺人意图；跳过目标代次/revision已完成者，只补缺口；投影/任务进度同事务，不生成治理记录 |

所有实时写路径在后台构建期间也立即写目标代次及输入revision标记，先维护旧目录原字段再生成新投影；不能把“回填尚未完成”当作跳过扫描/M3投影的理由。不在前端“修补”过期搜索；不采用提交后 best-effort 钩子。封面仍在现有提交后阶段，搜索摘要从 catalog 当前封面关联读取，所以不用复制封面字段。

### 4.3 匹配、传播、去重与排序 SQL

**无 token AND/OR 或相似度搜索**：对每个完整字段做字面包含。绑定参数、先规范化再转义 `%`、`_`、`~`；不把输入当 SQL wildcard。编号分支用编号键。命中等级为 `0 完全相同 / 1 开头相同 / 2 包含`，不是 pg_trgm 的 similarity 分数。

下面是单分支 SQL 模式（`$1` 是未转义规范化键，`$2` 是已转义键，`$3` 是短片段预筛；生产代码在一般字段/编号字段两条 allowlist 分支中绑定各自值，`$4`是同一快照确认的新路径已发布代次）：

```sql
WITH hits AS MATERIALIZED (
  SELECT v.id, v.release_id, v.track_id, v.field, v.role,
         v.original_value,
         CASE WHEN v.normalized_value = $1 THEN 0
              WHEN v.normalized_value LIKE $2 || '%' ESCAPE '~' THEN 1
              ELSE 2 END AS match_rank
  FROM catalog_search_values v
  WHERE v.projection_generation = $4 -- 同一快照确认已发布的目标代次
    AND v.field <> 'catalog_number'
    AND v.normalized_value LIKE '%' || $2 || '%' ESCAPE '~'
    AND v.short_grams @> ARRAY[$3]::text[]
)
-- 此后通过当前 media/tracks/release_effective 与有效署名把 hits 转成实体命中；
-- ROW_NUMBER() OVER (PARTITION BY entity_id ORDER BY match_rank, field_priority, id)
-- 取每个实体最佳说明后分页；计数另对去重实体 LIMIT 1001 探测，不先截断原始值行。
SELECT id, match_rank FROM hits ORDER BY match_rank, id;
```

- 所有非空查询都带一个安全的一/二scalar短片段预筛（如查询最前两字符），并保留完整LIKE残差；planner可选择trigram GIN、short GIN或BitmapAnd。长词通常由trigram加速；一/二字、标点及locale下无法提取trigram的输入仍有short GIN路径。不要在Go里仅按rune数猜PostgreSQL能否提取trigram。预筛必须来自完整查询连续片段，不造成漏匹配；高频片段的退化是性能问题而非静默改语义。
- 全局实体最终 `ORDER BY best_rank, normalized_title COLLATE "C", entity_id`；艺人使用有效显示名的规范化键。组用**代表版本标题**打平，同分稳定 UUID，不能按被命中版本替换代表。
- 在选择说明前，先识别命中名字是否属于当前艺人的非主名别名；即使原始track_artist/credit值本身就是该别名，也应标成`artist_alias`并保留role，不能被普通署名分支抢走而漏掉“别名：”标记。规范化同键的大小写/繁简变体只是原写法，不强制标成跨名别名。
- 每实体多字段命中时，先取最佳等级；同等级说明优先级固定为自身标题/主名、当前署名、别名、厂牌、编号，再按角色序、原文、来源 ID。响应只给一条主要 `match`，不返回无限命中集合。
- 发行命中：自己的有效标题/艺人/专辑艺人/厂牌/编号，或任一 **present** 曲目的标题/曲目艺人/四种 credit；⑤加关联艺人任意别名。
- 专辑命中：任一可见成员发行命中，按 group 去重；返回 `matched_release_id`，点击可打开 `/albums/{groupId}?release={matched_release_id}`，卡片代表和全局排序仍用既有代表版本。
- 曲目命中：自己的标题、艺人、四类 credit，或它**当前所在发行**的标题/艺人/专辑艺人/厂牌/编号；别名按自己/发行级署名传播。某轨的作曲者命中**不传播到同发行的其他轨**。全局曲目始终 present-only，同组不同版本的不同 track_id 不折叠。
- 艺人命中：主显示名、原始写法、人工合并来的别名。别名只传播到该艺人实际有效关联的对象；拆开后同一个名字可能命中两位，各自只返回自己的参与项。
- 新 `/search` 的计数阈值固定 **1,000**：对完整匹配关系先按实体去重，再执行无排序 `LIMIT 1001` 的计数探测；读满 1,001 个不同实体就停止计数，返回 `total=1000,total_relation="gte"`，否则返回真实 `total` 和 `total_relation="eq"`。这是返回的计数探测上限，**不是候选或可翻页结果上限**；不能在原始搜索值/别名传播之前 LIMIT 1001，也不能先用 `COUNT(*) OVER()` 或物化全部实体再声称计数有界。排序页查询和计数探测各自使用同一匹配/可见性构造器，但不强制共享全量 MATERIALIZED 实体 CTE；上面的 hits SQL 只是单分支语义示意，物化边界需按实际 EXPLAIN 确定。计数分支优先从每实体唯一的基表行经完整匹配的 `EXISTS` 半连接筛选，再取前1,001个实体，避免全量 HashAggregate 去重后才 LIMIT；LIMIT本身不保证底层早停，必须检查实际计划中的扫描/聚合行数与buffers。
- 页查询按全体去重实体的相关性排序后取 `page_size + 1`，最后一条只决定 `has_more`；与计数探测放在同一只读 REPEATABLE READ 事务（或等价单语句快照）中。空页仍返回精确值或下界，不能用页行数推断全局 total。低命中计数仍可能读完匹配关系，热门词的全局排序也可能有成本，必须纳入性能验收。OFFSET 静态稳定，不承诺跨请求快照；并发扫描/治理可以改变后续页面。建议接口仅取每类 `limit + 1`，不执行计数探测。

### 4.4 旧路由兼容与回填期间的ILIKE回退

保持旧两路由参数、200字节、page=1/page_size=50最大100、attention、present-only、组去重、代表版本、响应shape和精确`pagination.total`；无q一直保持原排序。D3的相关性档位在新路径发布后适用于旧两路由带q的请求；D2修订明确允许回填期间复用完整旧匹配与旧排序，不要求先读半成品新索引来维持相关性。

| 状态/端点 | 完整查询路径 |
| --- | --- |
| search未发布：`/releases`、`/release-groups`带q | 原`releaseSearchPredicate`与`releaseGroupAnyMemberMatchesSearch`：有效发行标题/艺人/专辑艺人，或其present曲目标题，字面`ILIKE '%' || escaped_q || '%' ESCAPE '~'`；无NFKC/去音符/繁简/别名/新credit扩展，保留旧排序 |
| search未发布：新`/search`与建议的albums类 | 用相同旧发行谓词经可见成员找到group，复用旧代表/可见性；按旧标题/艺人/ID排序，封装新DTO和§9有界分页；不能先取一页旧发行再当全局group候选 |
| search未发布：新接口tracks类 | 旧系统没有独立曲目搜索路由，因此增加**只读兼容适配器**，从原表查询present曲目自身标题/artist及所属有效发行title/artist/album_artist，全部用同样的转义ILIKE，不读新投影、不加入credit/别名/新编号语义；按曲目title/artist/ID稳定排序并返回完整安全TrackSummary |
| search已发布，artists仍整理中 | 全部专辑/曲目查询使用新规范化与exact/prefix/contains档位；跳过艺人别名传播，艺人结果栏未启用，不把部分名字图用于搜索 |
| search与artists均发布 | 使用完整新匹配、别名传播与三类结果；每次请求只读状态匹配的新代次，不混合两条路径 |

回退新DTO的`match.original`来自实际旧字段，`artist_id/alias=null`，`kind`以同一未规范化查询做大小写不敏感exact/prefix/contains解释；这是命中说明，不宣称旧路径已按新规则排序。通过§9的`catalog_state.search_mode="legacy"`标明模式，前端可以正常搜索播放，不显示错误页或要求等回填。空q语义、通配符转义和当前有效M3字段都保持；结果可能比新规范化路径少，但绝不能漏掉本应按旧谓词命中的刚提交修正/扫描数据。

旧路由不增加必需match字段、不把1,000下界当精确total。新全局搜索两种模式都采用§9有界计数，按相应完整候选关系去重后分页；1000+不代表只查前1000项。常态300 ms/单字1 s验收针对已发布新路径；后台期另记录旧路径时延与播放/交互回归，不用临时兼容路径掩盖新路径性能不达标。

### 4.5 `pg_trgm` 部署与性能验收

- 在新迁移显式 `CREATE EXTENSION IF NOT EXISTS pg_trgm`。本项目应用连接按要求是超级用户，权限不是前提缺口；仍须确认**目标服务器安装了扩展文件**。不得只在开发库手工启用而漏掉部署与 smoke。
- `compose.yaml`、`compose.test.yaml`、`deploy/smoke/compose.yaml` 均指定 PostgreSQL 18 alpine。实施时在隔离测试项目验证实际镜像的 `pg_available_extensions` 与 `pg_extension`，记录镜像 digest、`extversion`、encoding/collation；不能只凭镜像标签保证可用。扩展缺失是结构迁移故障，按现有启动错误处理；这与正常回填期间有意采用的ILIKE回退不同，不能用回退掩盖缺扩展的部署失败。
- 扩展对象安装在确定 schema。现有测试采用隔离 schema，需检查 postgrestest 的 search_path：扩展建议固定 `public`，`gin_trgm_ops` 明确限定；扩展是 database 级，不能误以为每个隔离 schema 都独立安装。测试若首次安装在后被删除的 schema 会污染后续用例，需在隔离库准备阶段固定并验证位置。
- 不需 Redis、Meilisearch、unaccent 扩展或新的系统服务。普通非 concurrent GIN 建在新空投影表上，回填有写放大，后台完成耗时、前台播放/查询延迟与gate等待一并验收，不再计作停机准备窗口。
- 已裁决的全局搜索目标是：**两个字符及以上的建议与结果首屏 p95 ≤300 ms，单字符 p95 ≤1,000 ms**，不设最短长度，单个汉字“爱”必须可搜。时延档位按首尾 trim 后用户查询的 Unicode scalar 数确定（不是 UTF-8 字节数，也不因 `ß` 等折叠扩张把单字符偷偷归入长词）；空查询直接返回空响应。GIN 与有界计数都不是达标证明，本次未实测。

性能夹具必须至少：100,000 **present** 曲目 + 10,000 missing；10,000 个发行、约8,000组（含多版本和隐藏成员）；20,000 艺人；每轨平均2个曲目艺人、4条 credit、20%名字有别名，另有单艺人参与50,000轨/热门厂牌命中50%的倾斜数据。按实际条目展开，搜索值应达约700,000–1,000,000行，不可只造10万标题行；记录最终各表数量、索引大小、构建时间和内存。

对实际生成的生产 SQL（包含新接口的 1,001 实体计数探测及 `has_more`、去重、排序、可见性、代表版本/音频摘要补全；旧路由另测精确 count）执行 `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`：

1. 精确标题与前缀标题，例如 `交响曲 第00999号`；并造“包含”对照，验证顺序。
2. 繁体名字 `周杰倫` 与规范形式；⑤再测 `Teresa Teng` 别名扇出。
3. 单字 `爱`/`周`、双字 `交响`、极常见拉丁 `a`、只有标点 `%`/`_`/`~`，以及零结果长词；另造恰好 999/1,000/1,001 命中与 50,000 命中，检查计数探测确实提前停止且不影响全局相关性顺序。
4. 作曲/指挥等角色、热门厂牌、`SRCL-1234`/`srcl 1234` 编号。
5. 非代表版本命中、M3刚修正后的新词/旧词、missing-only命中排除。
6. 建议各5条；结果各50条首屏；旧两个路由带 q；单艺人大规模别名。另测第100页作为退化记录，不把它混作首屏通过。

先 `ANALYZE`，正常 planner，不 `enable_seqscan=off` 造假。每组预热5次、测量30次记录中位/p95与冷连接首次值；记录 CPU/RAM、PG参数、locale、并发度、计划/执行时间、shared hit/read、sort spill、过滤/实际行数。选择性查询应利用索引；高命中时顺序扫描可能合理，不把“必须有 Bitmap”当唯一标准。

新建议端点及结果页两/三类并发请求的整屏服务端关键路径，按上述长度档位验收：两个字符及以上 p95≤300 ms，单字符 p95≤1 s；HTTP真实服务计时包含鉴权、数据库等待/计数/补全及序列化，另报每条 SQL 与总数据库耗时，外部网络/前端250 ms防抖不计服务端指标。总数超过1,000时按§9明确显示“1,000+”，不要求精确全量count。短词/热门词未达标时凭计划优化去重、索引/统计与补全；**不得增加最短长度、只搜前缀或截掉可浏览候选**。保留失败计划与实测结果，不将样本数据缩小来过门禁；计数早停不免除排序/分页的性能验收。

## 5. ⑤ 艺人身份、名字、事实与治理意图

### 5.1 数据模型

不向会被重扫删除重建的 `track_credits.id` 或 `release_credits.id` 挂人工意图。所有关系都绑定稳定的艺人/名字键/发行/曲目 UUID。规范化相同是**默认路由**，不是禁止人工拆开的唯一艺人约束。

| 表 / 投影 | 主要列与约束 | 写入权威 |
| --- | --- | --- |
| `artists` | `id UUID PK, initial_name TEXT, edit_revision BIGINT DEFAULT 1 CHECK >0, created_at`；initial_name是创建时有界显示fallback、不是身份键；不物理删除 | 自动发现可 INSERT；只有治理操作可递增 revision |
| `artist_name_keys` | `id UUID PK, normalization_version, normalized_key, key_hash BYTEA(32), initial_artist_id FK`；`UNIQUE(version,key_hash)`；命中 hash 后再比完整键，不相等按投影错误失败 | scanner/回填创建初始路由，不覆写已存在路由；规范化升级走显式维护 |
| `artist_names` | `id UUID PK, key_id FK, spelling, spelling_hash, origin(scan/correction/seed), created_at`；每键每原写法唯一（hash定位后全文核对） | 追加去重的原写法；不能重扫累计次数，不按有无本轮引用删除名字 |
| `artist_credits` | `id, release_id?, track_id?, role, name_id, position, layer(scan/correction)`；subject恰好一个；release只准`release_artist/album_artist`，track只准`track_artist/composer/conductor/performer/producer`；subject/role/name/layer唯一 | scanner只替换scan层；M3投影刷新管理correction层；均是可重建事实 |
| `artist_name_bindings` | `key_id PK FK, artist_id FK, operation_id` | **治理专有**；没有行时取name key初始艺人；用于跨名字合并、别名拆回 |
| `artist_assignment_overrides` | `track_id? / release_id?`恰好一个，`key_id, artist_id, operation_id`；分别唯一`(track_id,key_id)`、`(release_id,key_id)` | **治理专有**；按专辑拆同名人的持久覆盖；不包含短命的credit行ID |
| `artist_overrides` | `artist_id PK, display_name?, redirect_artist_id?, name_operation_id?, redirect_operation_id?`；redirect不能指自己，图不能有环 | **治理专有**；显示名覆盖与被合并身份的重定向。源艺人原显示意图保留，恢复身份时可再用 |
| `artist_effective_credits` | 上述事实按修正层、名字归属和作用域覆盖解析后的`subject/role/name_id/artist_id`；索引见§4 | 派生投影，可由扫描/治理/回填重建；不是管理员意图 |
| `artist_profiles` | `artist_id PK, auto_display_name, effective_display_name, sort_key, initial_group, version` | 派生缓存；可重建，扫描更新它不等于扫描改人工主名 |
| `artist_candidate_signals` / `artist_candidate_ignores` | 见§8；前者是事实派生，后者是治理意图 | 严格分开，不因新scan_run清空忽略 |

`BYTEA(32)`表示32字节预算，实际 PostgreSQL 使用 `BYTEA` 加 `octet_length(...)=32` CHECK，而不是不存在的定长 BYTEA 类型。姓名原文预算4,096字节，姓名键预算与规范化函数一致；新HTTP显示名用同样的有界文本校验。所有意图表的 `operation_id` 外键均 `DEFERRABLE INITIALLY DEFERRED`，沿用先改业务再写成功记录的事务顺序。

核心约束：

- 每个 **规范化键** 默认一个初始艺人；**不对艺人的主显示名加 UNIQUE**，两个同名人可以并存。
- 同一个 key 可经曲目/发行作用域覆盖指向不同艺人。姓名表保存“有哪些写法”，不是断言世界上只存在一个同名人。
- `artist_effective_credits` 解析顺序：**该曲目+key覆盖 → 当前发行+key覆盖 → 全局key绑定 → 初始艺人**，最后解析当前canonical艺人。发行级署名从第二级开始。track级覆盖适用于该轨该key的所有角色。
- 专辑艺人有 M3 修正时，发行的 `release_artist` 和 `album_artist` 都用correction层；原scan层保留但不参与有效归属、主名计票或搜索。清除修正立即恢复当前scan层。其他角色不被专辑艺人修正覆盖。
- 只为真实非空署名建艺人。parser的 `inferred artist=未知艺术家` 不是人，不建占位实体；但用户真实标签恰好写同样文字仍是署名，通过provenance区分，不能硬编码名称黑名单。旧证据不够时标记来源不足，保守不制造“未知艺术家”实体。
- 组只是展示/选择层。不能把所有轨的人员合成“专辑艺人”，也不能用艺人归一改写 organizer 的 Release 身份或归组结果。

### 5.2 最常见写法与改名

D5 确定频率定义：从**当前有效且present的署名**计数，每个 `(subject_kind, subject_id, name_id)` 一票，跨角色去重；发行的release_artist和album_artist相同只计一票；同一轨重复标签只计一票。不累计扫描次数，不按名字首次发现时间加权；合并后的艺人汇总全部有效名字。并列按原文 `COLLATE "C"`、name_id稳定打平。

`auto_display_name` 取最高频写法；没有present票数时取当前归属名字中稳定排序第一项；连当前名字归属都已迁出的旧身份保留`initial_name`作fallback（种子首选亦仅用于无票数fallback），不删艺人。`effective_display_name = 人工覆盖 ?? auto_display_name`；人工主名一直优先，直到管理员再改或显式恢复自动。姓名仍逐项保留原样；页面署名链接文字用其原写法，艺人页标题用有效主名。

主名改为新字符串时，该字符串成为该艺人的**可搜索展示名**；原署名/已登记别名仍可搜索。先前纯人工展示名若不在名字表里，不自动成为永久别名；保留历次人工主名作为别名不在本次范围。改名**不是把另一个艺人的名字键抢走**：即使显示名与别人相同也不自动合并。新标签使用尚未绑定的另一个名字时，仍按名字解析规则发现实体，必要时由管理员合并；不得通过“改名”隐式改变其他艺人的归属。这个边界及计票定义已由 D5 接受。

先按受影响艺人集合重算profiles，不每读一页重新聚合整库。读专辑数/角色数可批量实时按有效关联统计，暂不物化随归组变化的大量计数；若因性能物化，必须纳入同事务更新矩阵，而不能只更新主名忘记计数。

### 5.3 按专辑拆同名人的准确表示

UI按 ReleaseGroup 选择专辑，D4 确定命令在确认时把组展开为**明确发行快照**：

1. 预览返回所选group、其当前发行ID、对应的名字键、参与角色与曲目数量，保存scope basis摘要。
2. 执行时对每个选择的 `(release_id,key_id)` 写发行范围覆盖；当前属于旧艺人的相关轨再写 `(track_id,key_id)` 覆盖。前者承接该发行以后新发现的同名署名，后者保证同一track_id重扫移动到别的发行时也不被重新合回。
3. 不把人工覆盖绑定相对路径，不绑定临时credits ID，不因本轮没有出现某个名字删除覆盖。
4. 以后新版本加入该group不自动继承；已选择发行后来改组时覆盖跟着发行，已选择曲目换发行时其track覆盖优先。不是永久“这个组永远属于某人”的动态规则。
5. 文件缺失/恢复且ID连续：覆盖保持；同路径换内容、跨root、Release身份不连续：新ID不猜测继承，旧覆盖保留。⑤不扩大M4认领的四类范围，不暗中迁移艺人拆分意图。
6. 合并同名的两位必须合并这些scope覆盖的有效目标，不能只合并名字键。反向拆开可重新选这些专辑；精确恢复旧身份见§7。

D4 已选择“快照+未来同发行+既有曲目”语义：这些发行以后新增的曲目跟随，不自动包含以后入组的新版本；不采用动态 group 规则。重扫、移动、新增版本与人工归组测试均以此为唯一合同。

### 5.4 重扫保留的结构保证

scanner只创建初始艺人/名字、替换scan署名、更新派生投影；**不 INSERT/UPDATE/DELETE `artist_name_bindings`、`artist_assignment_overrides`、`artist_overrides`、`artist_candidate_ignores`，不改 `artists.edit_revision`，不写 operations**。共享解析器可以读取意图来生成有效关联，不把意图伪装成新的标签事实。新增M3修正中的姓名用`origin=correction`，不标成扫描来源。

测试分别对这四张意图表及revision做前后快照，覆盖合并、拆开、改名、忽略后重复扫描；另外注入扫描失败/取消、候选回滚、missing恢复、同root移动及曲目换发行。只检查最后UI名字不够，因为错误覆盖可能暂时被同名掩盖。

## 6. 扫描挂接、旧别名升级与并发顺序

### 6.1 同一候选事务挂接

选择 `PersistCandidate` 同事务内解析名字，**不选扫描结束后第二遍认人**。后者会在incomplete、取消或进程退出时留下一半新目录一半旧关联，也不能保证M3修改后立即一致。

流程：已规划身份 → 取catalog write gate → 按现有计划写发行/曲目与原始观察 → 保守拆分名字、按规范化键解析/创建初始艺人 → 替换对应scan署名 → 从当前correction层和人工艺人意图生成有效关联 → 刷新搜索/主名/候选派生 → COMMIT。已有轨移动时收集旧发行和旧艺人，刷新两边派生；不是只更新目标发行。封面仍由原流程在提交后处理。

`CanonicalArtistNames` 拆成“保守拆分、原文去重”和“身份解析”两件事；纯 metadata 包只负责前者，固定组合规则保留。parser不访问DB、不套人工别名、不执行艺人合并。新增的原始署名观察以有界结构穿过 parser → `SourceObservation` → staging → persist，不扩大扫描读取权限。

原始多值Artist输入用于解析本轨的完整署名集合，不能仅为候选保存第一项却仍只给最后一项挂身份；展示串从这个集合稳定生成。AlbumArtist的候选选择/organizer归组保持现有规则，未选中的原始值只作证据，不冒充有效专辑艺人。

新 `artist_tag_evidence` 可作为每轨当前证据表（`track_id,role,tag_key,occurrence_index,raw_value,source_kind`），替换而不追加；或者在当前观察中增加一个明确版本化的有界JSON字段，**不能向当前每轨每字段唯一的 `track_observations` 偷塞多行**。本设计选前者，名字关联仍是结构化关系；每轨原始署名项最多100，每原值4,096字节，超限使候选失败并诊断，不无声丢最后一条来构造候选。这会触及parser合同，列入实施文件集。

### 6.2 硬编码别名改种子及旧数据处理

- 新迁移创建稳定种子艺人及 `周杰伦/周杰倫/Jay Chou` 名字记录，两个名字键（繁简同键、英文另键）初始指向同一艺人；用固定UUID、显式seed来源，不伪造管理员操作。初始数据只执行一次，重扫不重复“恢复种子绑定”，管理员拆开Jay之后不能被种子重合。
- 这些固定字符串的规范化结果由版本1测试锁定，可在迁移中明确写出；不是要求SQL对任意文本运行Go。字表版本变化由维护步骤处理。
- 删除 `officialArtistAliasEntries`/index 的硬编码；保留AC/DC等固定组合**拆分规则**，它们不是跨名别名表。
- 旧 `tracks.artist` 中的 `周杰伦` 不能直接SQL替换成 `Jay Chou`：原来可能真是中文，也可能是一组多人署名。
- ⑤后台回填时（遵循§3.3的锁内重读/新版已处理则跳过）：有当前可信 `track_observations.field_name='artist'` 的，按新的“只拆分、不改名字”规则重建**派生的** `tracks.artist` 展示串及身份对应；没有可信原文就保留旧串并按它建关联。只改这个原本由扫描派生的展示字段，不改UUID、来源、音频事实或扫描时间，不读音频。没有原文时不伪造英文别名的使用频次。
- 旧四角色credits已在parser丢失别名原文，`track_observations`没有那份原文；回填只能保留当前credit名，种子仍保证Jay可搜到周。**第一类候选信号仅对写入新版原始标签证据的数据生效：既有曲目需在⑤上线后成功重扫，新发现曲目由新版扫描直接产生证据。** 后台回填不伪造证据，不因升级强制遍历用户音乐库；这只是内部适用范围，界面不显示“候选不完整/需要重扫”的说明或提示。
- 新扫描的 `tracks.artist` 由原始名字去重、稳定排序、` / ` 连接；canonical艺人显示名放在身份DTO中，不回写标签/事实。该升级可能让既有行从中文显示回`Jay Chou`，是D5已接受的可见变化。

### 6.3 并发：先用一个短事务写gate保证完整性

M5初版建议用 `pg_advisory_xact_lock(class='CATP', key=1)` 串行**catalog派生相关写事务**。不是锁整个扫描，也不包文件读取；只是已有candidate、人工事务及后台回填短批，避免复杂的名字键、重定向和扇出死锁。maintenance选主锁不阻挡业务写；每批释放CATP，不能把整个构建期放在gate内。读请求MVCC不拿gate，播放不依赖艺人投影。性能测试同时记录扫描每候选耗时与治理等待，过大再凭证据拆细锁。

锁顺序必须统一：

1. `operations.Run`原有actor/key advisory lock；撤销原有的operation记录锁、需要恢复的历史operation引用锁（保留现有顺序）。
2. **所有涉及投影的路径在任何业务行写锁之前取CATP**。修正/撤销、认领/撤销、归组/撤销、艺人治理、扫描candidate、后台回填、版本登记与原子发布都遵守；锁后再读目标代次/输入revision，不用等待前的快照。
3. 发行行UUID升序 → 组行UUID升序 → 艺人/名字/投影行按稳定ID；艺人操作不需要的发行锁不要为了形式去拿。
4. gate内不再倒过来锁历史operation行；需要引用旧操作的类型在步骤1完成。新操作ID的延迟外键按既有流程处理。

`finalizeScan` 目前先锁scan_run再调`MarkMissingSources`；⑤若要更新主名/候选，必须在锁scan_run前先拿CATP，避免与candidate写轨/外键形成反序。它只在既有成功终态执行missing。将受影响ID通过catalog合同带回派生刷新，不能在library直接联艺人私有表。

同gate也防止两个同时扫描发现同规范化键时建两个实体；唯一约束仍兜底。加锁后分开语句重新读取修正/名字归属，不能用等待前的JOIN快照。扫描不递增人工revision，因此**按范围拆开的依据仍需basis**（§7），不能用gate代替用户预览冲突检查。

若长合并超过预算，整体拒绝/回滚，不能提交半个合并。初版上限建议一次影响100,000不同轨、1,000名字键、10,000显式scope覆盖；使用集合SQL，不逐轨发十万次查询。上限与超限处理须有样本，不能悄悄只合并前100条。

## 7. ⑤ 治理命令与操作记录

### 7.1 共同合同

所有写：同源403 → 管理员401/403 → 路由UUID → `Idempotency-Key` UUID → 严格JSON/预算 → `operations.Run`。失败/拒绝无业务变化、无成功记录；结果不确定用**同一键**重试。艺人构建期的新合并/拆开/改名/忽略命令由执行器在取得CATP后、读目标行和做任何业务修改前检查状态，返回`409 artist_catalog_not_ready`；执行器复核不能只靠前端禁用。先完成同源/角色校验，不能把未登录/非管理员请求改报409。既有成功记录的幂等重放仍按Run原合同返回原结果（不是执行新治理）；未执行的409不消费成功记录，ready后同键可重试。

新增 `TargetArtist = "artist"`，`operations_target_type_ck` 保留release/release_group再扩展artist。新类型：

- `artist_merge`
- `artist_split`
- `artist_rename`
- `artist_candidate_ignore`（忽略/恢复提示也是共享写，按R4记录；不是私人偏好）

每种注册独立Kind，可共用小型描述帮助函数。**全部 `Revert` 直接返回 `409 not_revertible`，`Describe.Revertible` 恒false**；已有五类及revert行为不变。前端不能把false解释成“之后又有修改”。没有独立审批、不引入editor/Agent模式。

指纹包含类型、路由目标、双方预期revision、按UUID排序去重后的名字键/组/发行集合、操作语义、basis、目标策略及规范化后的显示文本；不含生成时间。显示名只折叠显示空白，不使用搜索键取代显示值。

### 7.2 建议接口

路径均在 `/api/v1`；以下revision为人工版本，均>0；请求大小建议64 KiB，集合超限`400 invalid_input`，超出可原子处理的影响范围`409 artist_scope_too_large`。

| 端点 | 请求 / 响应 | 语义 |
| --- | --- | --- |
| `POST /artists/{target_id}/merge` | `{expected_revision, source_artist_id, source_expected_revision}` → `{operation_id, artist_id, revision, source:{id,revision,redirect_artist_id}}` | 把source并入当前target；双方必须不同、未重定向；同ID`409 same_artist`。保留target的人工主名优先；source原名/别名/原显示名成为target可搜索别名 |
| `POST /artists/{id}/split-preview` | `{mode:"aliases", name_key_ids:[UUID...]}` 或 `{mode:"albums",group_ids:[UUID...],name_key_ids:[UUID...]}` → `{artist_id,revision,basis,scope,limits}` | 管理员只读预览，POST须同源，不写operations。aliases最多100键；albums最多100组，展开最多1,000发行 |
| `POST /artists/{id}/split` | 上述scope + `{expected_revision,basis,target:{kind:"new",display_name?}}`；或 `target:{kind:"restore",artist_id,expected_revision}` → `{operation_id,artist_id,revision,target:{id,revision},scope_summary}` | 别名整体拆回，或按明确专辑范围拆同名人；restore只允许恢复目前重定向到本人的旧身份，不允许随便改第三个人 |
| `PATCH /artists/{id}/name` | `{expected_revision,mode:"set",name}` 或 `{expected_revision,mode:"auto"}` → `{operation_id,artist_id,revision,display_name,name_source}` | set钉住主名，auto清掉人工覆盖重新计算；同意图`400 no_changes`；旧别名保留 |
| `POST /artists/{id}/candidate-ignore` | `{expected_revision,other_artist_id,other_expected_revision,key_pairs:[{left_key_id,right_key_id}],ignored:true|false}` → `{operation_id,artist_id,revision,other_revision,ignored}` | 修改稳定名字键对的忽略意图；false是恢复提示的反向操作，不走revert |

拆分basis为服务端确定编码状态的SHA-256，不含个人敏感信息；至少绑定模式、目标artist、名字键归属/有效覆盖、所选group的成员发行集合、相关track及role/name集合、作用域所用revision。按稳定ID排序；相同语义重扫仅时间变化不失效，新增相关轨/改组/改专辑艺人会失效，提交返回`409 artist_scope_changed`让用户重新预览。输入只有一个digest不代表可信，执行必须重算；不把预览存成审批对象。

全局合并作用于source身份的全部当前/未来名字归属，类似设置意图，不依赖逐轨预览，双方revision足够；显示影响数量可读，但不宣称那是固定快照。按范围拆开确实依赖扫描可变集合，属于R4允许增加basis的情形。

### 7.3 合并、拆开的持久执行与反向恢复

合并在一事务中：锁并复核双方revision → 收集source所有全局名字绑定、发行/曲目覆盖和指向source的redirect → 把有效归属指向target → 建立source→target的一跳redirect、将更早redirect扁平化 → 刷新关联/主名/别名/候选/收藏解析投影 → 双方revision各+1 → 写一条以target为主目标的记录。记录 `expected/result` 是target；source前后revision放类型化state。redirect不在扫描时更新。

不要删除source艺人、名字、旧主名意图，也不把个人收藏直接“转移所有权”。别名拆回可选恢复旧source ID，清掉它的redirect、分配明确名字键；同名拆分按§5.3分配scope。aliases模式必须同步移动所选key目前归source的全局绑定和明确scope覆盖；不得仅换全局绑定而让高优先级覆盖仍指回source。已经分给第三位同名人的scope不在本次范围内、不被抢走，预览必须列明。保留源身份是为了旧链接、收藏和操作记录能够恢复，不是显示两个活动的重复艺人。

**反向操作不等于无条件还原过去**：前后state保存本次改变的名字绑定、scope覆盖、重定向与双方名字意图快照（带`schema_version=1`，大小有界）。详情/预览可给出恢复这次合并需要选择的名字与专辑，管理员通过split/merge/name正常命令恢复；如果此后又修改了同一映射或basis改变就拒绝旧预览。合并已拆开的同名人时必须记录覆盖原归属，不能只留一个总数而声称可恢复。对恢复旧身份，原来的收藏锚点可再次解析到它；新建一个同名UUID不算“恢复原身份”。

### 7.4 `Describe`、记录接口与前端解码器

整理期间操作记录列表仍可读：旧release/group类型不受影响；artist记录的历史摘要用已存state，当前艺人标题补全不读半成品profiles，必要时只保留ID/历史名且不渲染链接。能力状态和当前补全在同一只读快照内判断；不能因为artist未就绪使整份operations列表503。

`operations.Description` / HTTP条目新增 nullable `artist`：

```json
{
  "id": "71000000-0000-4000-8000-000000000001",
  "type": "artist_merge",
  "created_at": "2026-11-04T12:00:00Z",
  "actor": {"id": "70000000-0000-4000-8000-000000000001", "username": "admin"},
  "release": null,
  "album": null,
  "artist": {"id": "a0000000-0000-4000-8000-000000000001", "name": "邓丽君", "visible": true, "redirect_artist_id": null},
  "changes": {
    "kind": "artist_merge",
    "source": {"id": "a0000000-0000-4000-8000-000000000002", "name": "Teresa Teng"},
    "target": {"id": "a0000000-0000-4000-8000-000000000001", "name": "邓丽君"},
    "name_count": 1,
    "release_count": 12,
    "track_count": 150
  },
  "reverts": null,
  "reverted_by": null,
  "revertible": false
}
```

- `artist_split`摘要：`{kind,source,target,mode,name_count,release_count,track_count}`；`artist_rename`：`{kind,from:{name,source},to:{name,source}}`；ignore：`{kind,other_artist:{id,name},ignored}`。名字是当时快照，主目标artist信息是当前值；不暴露原始before/after、路径或所有轨的清单。
- `Describe`按页批量取艺人profile和redirect，不逐操作查询。坏state只令`changes:null`，不使整页失败；所有新操作false。
- `GET /operations`追加`artist_id`筛选，和release_id/group_id仍取**并集**。艺人筛选要包含以其为source/restore-target的操作，不只看主target。建议新增可索引的`operations_artist_targets(operation_id,artist_id)`关联，由operations在Run记录Outcome的附加Targets（主目标也写入，去重）；通用包只保存目标引用、不查艺人表。所有记录与关联同事务，加入外键/唯一键和`(artist_id,operation_id)`索引。
- `operations/httpapi`追加artist字段/筛选；旧记录显式`artist:null`。`revertResult.target_type`词表可扩artist以覆盖统一Target类型，但本期不可能产生成功artist撤销响应，不制造虚假样本。
- `frontend/src/features/governance/api/decoders.ts:178`附近扩展`OPERATION_TYPES`及四种changes decoder，`:275`附近同步target_type词表；条目新增artist decoder；旧样本/其他操作回归。
- `change-summary.ts`、`operation-list`、`operations-search.ts`及目标链接逻辑都改：明确“请通过拆开/合并/再次改名恢复”，不要落入当前“专辑已不可见”“之后又有修改”的默认分支。app组合artist目标链接。

### 7.5 新错误码与不变量

共同400/401/403/404/409/500/503沿用治理合同；新增：`400 invalid_artist_name`（空白、控制字符、超预算）、`400 invalid_artist_scope`、`409 same_artist`、`409 artist_redirected`（提交给已合并身份，必须刷新，不自动改命令目标）、`409 artist_scope_changed`、`409 artist_scope_too_large`、`409 artist_catalog_not_ready`（艺人尚在整理，新治理未执行）。对不存在artist/key用404；已不归目标的key用scope_changed。`revision_conflict`、`idempotency_conflict`、`no_changes`、`not_revertible`不另造同义码。

身份和图约束失败不暴露SQL；意外约束错误`500 operation_failed`；连接/提交结果未知`503 database_unavailable`。普通成员没有治理入口，但每个端点仍在服务端强制403。艺人读取的`503 artist_catalog_preparing`与上述409不同于`503 database_unavailable`，前端必须按专门code显示整理状态，不升级为整站故障；split预览、管理员候选读取也走整理状态，不读半图。

## 8. ⑤ “可能是同一位”候选与忽略

只使用PRD的两种信号，**都只是提示，不是同一人的证据结论**；不用相似度、编辑距离、向量或拼音比较。

1. **同轨同角色多写法**：一个track的同一角色原始标签出现两个不同规范化键。保留标签key/次序/来源后计算无序key对，给出这条轨和原写法作为理由。普通的两个共同作曲者也可能符合，因此UI只称“同一曲目同一角色出现两个名字”，不能写“确定是别名”。D6 已确定只取原始重复标签项，不把`A / B`这种明确多人列表自动变成该信号。既有曲目仅在⑤上线后重扫成功、写入新版原始证据后参与；后台回填的旧名串不能作为此类证据。新发现曲目由同一新版扫描产生证据。界面无需解释该历史数据边界，也不提示用户为候选专门重扫。
2. **同发行专辑艺人与曲目艺人一一对应**：该发行至少一首present轨；所有有真实Artist标签的present轨各自恰好一个同样的key B，没有多人/未知；有一个明确的有效AlbumArtist key A，A≠B；所有present轨都满足才出候选。不跨发行拼凑、不用各种艺人合集/缺失值补1:1。M3专辑艺人修正要触发重新计算，理由标明采用有效专辑艺人。

选择**按candidate同事务物化信号**，不是每次请求扫描全部观察，也不是只在整轮succeeded后全库生成：与已提交的当前目录一致，incomplete可保留已成功candidate的信号；没有成功missing对账就不因未访问而删除信号。`artist_candidate_signals`保存`left_key_id < right_key_id`、`kind`、证据subject ID及有界安全原文；scope刷新替换。读取时以证据track/发行scope按§5.1完整优先级解析key对应artist，不能只用全局key绑定；同一人不提示，明确按scope拆开过的相同证据不再提示合并；过滤当前不可见证据。

忽略表以稳定**名字键对**为唯一键，保存operation_id；不以scan_run、临时candidate行ID或原文hash作唯一忽略身份。一个UI候选可聚合多对key，ignore请求提交实际看到的key_pairs，新增不同的名字key对是新证据、可以再次提醒；这是 D6 已裁决的按具体名字键对持久忽略语义，不是永久忽略整对艺人。重扫同样证据、合并后再拆开相同key对，已有忽略仍有效。false命令移除忽略意图，记录保留。

接口建议：`GET /artist-candidates?page=1&page_size=50`（最大100，管理员），条目`{left:ArtistSummary,right:ArtistSummary,key_pairs,reasons:[{kind,track_id?,release_id,role?,left_name,right_name}],evidence_count}`；每条最多3个reason样例、100个key_pairs，超出的候选分组/分页而不偷偷忽略全部。按证据数降序、两artist ID升序稳定；不伪造“置信度百分比”。本接口非普通搜索结果，不向普通成员披露管理候选。

## 9. 全局搜索 API 与结果 DTO

### 9.1 路由和参数

全部是已登录成员可读，`Cache-Control: no-store`，没有角色越权的管理证据；未登录401。API路径均为 `/api/v1` 下：

| 接口 | 参数与上限 | 返回 |
| --- | --- | --- |
| `GET /search/suggestions` | `q`；`limit`默认5，每类1–8；空查询或新模式规范化后为空返回三类空数组，不全库浏览 | `{query,catalog_state,available_types,artists:{items,has_more},albums:{items,has_more},tracks:{items,has_more}}`；每类只读limit+1判断has_more，不做全量count |
| `GET /search` | 必须`type=artists|albums|tracks`，`q`，`page`默认1，`page_size`默认50、最大100 | `{query,type,catalog_state,items,pagination:{page,page_size,total,total_relation,has_more}}`；每类独立分页，请求一栏不重查另外两栏；计数阈值1,000 |

④时`artists_state="unavailable"`、`available_types=["albums","tracks"]`，建议的artists为空；直接请求`type=artists`返回`400 unsupported_search_type`。⑤已安装但未发布时`artists_state="preparing"`，available_types仍只给专辑/曲目；直接请求艺人类返回`503 artist_catalog_preparing`，不能冒充不存在/空结果。⑤发布后才为ready并包含三类。结果页首屏并发请求当前可用的两/三类，每栏独立加载/失败/重试，不因艺人整理阻断其他栏。

新增已登录只读`GET /catalog/status`（no-store），返回下列同一结构；新搜索响应内带同请求快照的`catalog_state`。epoch是不透明单调整数，不是规范化算法版本，也不携带内部进度/错误堆栈：

```text
CatalogState = {
  epoch:非负整数,
  search_mode:"legacy"|"normalized",
  artists_state:"unavailable"|"preparing"|"ready"
}
```

```json
{"epoch":2,"search_mode":"legacy","artists_state":"preparing"}
```

④阶段status也可用，artists_state固定unavailable；⑤构建失败但旧服务正常时对普通成员仍是preparing，后台安全日志区分失败/重试。状态接口不是全站ready探针；数据库真故障才按通用503处理。

q沿用去首尾空白后的200字节预算；新接口拒绝无效UTF-8和非空白控制字符`400 invalid_query`。没有最短长度，单个汉字“爱”可查；空白输入返回200空结果（`total=0,total_relation="eq",has_more=false`）。旧接口继续其现有输入校验；本次不收紧旧接口的控制字符校验。分页沿用现有解析器及偏移溢出校验、重复参数首值策略；新limit非法`400 invalid_limit`、type未知/缺失`400 invalid_search_type`；数据库/安全投影失败`503 database_unavailable`，不返回半页。未知方法沿用405。

### 9.2 公共结构

新 `/search` 专用分页结构，**不是全项目通用 pagination 的无条件替换**：

```text
SearchPagination = {
  page:正整数, page_size:1..100,
  total:0..1000,
  total_relation:"eq"|"gte",
  has_more:boolean
}
```

- 阈值固定为 **1,000 个去重结果/分类**。实际匹配数 ≤1,000：`total` 是精确值，`total_relation="eq"`；恰好1,000也属于精确值。探测到第1,001个结果时：`total=1000,total_relation="gte"`，前端显示“1,000+”，不再为展示总数全量计数。`gte`明确表示下界，不允许前端当精确总数计算最后一页。
- `has_more` 由当前页查询多取一项得到，与有界计数独立；超阈值仍能访问第21页及以后。前端用上一页/下一页，不显示虚假的“共20页”或禁用第21页。每页页码正常保留在地址中；即使第21页或更后为空，total仍按同快照计数探测返回下界，不伪造为0。`eq`与`gte`都必须返回`has_more`，分页元数据不允许省略/null。
- 其他艺人浏览/详情/队列/收藏计数及旧两个目录路由仍按各自精确计数合同；它们的`total`不能直接送入此专用decoder。建议接口本身没有total，保留每类`has_more`即可。

仅分页元数据的边界样本（不是完整结果响应）：

```json
[
  {"page":1,"page_size":50,"total":0,"total_relation":"eq","has_more":false},
  {"page":20,"page_size":50,"total":1000,"total_relation":"eq","has_more":false},
  {"page":1,"page_size":50,"total":1000,"total_relation":"gte","has_more":true},
  {"page":21,"page_size":50,"total":1000,"total_relation":"gte","has_more":false}
]
```

最后一项对应恰好1,001个结果时的最后一页，仍可看到第1,001项；计数阈值不是分页上限。

```text
ArtistRef = {artist_id:UUID, name:string, display_name:string}
Match = {
  kind:"exact"|"prefix"|"contains",
  field:"release_title"|"release_artist"|"album_artist"|"track_title"|"track_artist"|
        "credit"|"label"|"catalog_number"|"artist_name"|"artist_alias",
  role:null|"release_artist"|"album_artist"|"track_artist"|"composer"|"conductor"|"performer"|"producer",
  original:string,
  artist_id:UUID|null,
  alias:string|null,
  release_id:UUID|null,
  track_id:UUID|null
}
SearchAlbumItem = {group:ReleaseGroupSummary, matched_release_id:UUID, match:Match}
SearchTrackItem = TrackSummary + {match:Match}
SearchArtistItem = {artist:ArtistSummary, match:Match}
```

Match字段**全部必传，允许null的不省略**；original≤4,096字节，为实际命中的有效字段/署名原文，不是q、不返回规范化内部键。别名命中时`field=artist_alias`、`alias`是匹配的别名、`artist_id`是有效艺人，`role`保留传播关系的角色；纯艺人结果role为null。前端从field/role词表组合“作曲：久石譲”“别名：Teresa Teng”，不接收后端HTML、不按未规范化offset做高亮（NFKC/折叠会改变字数）。标题直接命中无需附解释。

④不需要ArtistRef，所有Match的artist_id/alias为null；⑤追加实体对应字段。`TrackSummary`延续历史形状：`track_id,title,artist,present,duration_seconds,group_id,release,source`，不是`ReleaseTrack`那种顶层source_kind/codec和平面文件source标签。安全数据含nullable时长/音频事实，禁止音乐绝对/相对路径、rootID、指纹和文件时间。

### 9.3 可直接播放的完整响应例子（⑤）

下面显示新路径和艺人均已发布后按作曲者查询所得一页；④保持同样TrackSummary基础，尚未出现的身份数组在⑤作为additive字段加入。

```json
{
  "query": "久石譲",
  "type": "tracks",
  "catalog_state": {"epoch":4,"search_mode":"normalized","artists_state":"ready"},
  "items": [{
    "track_id": "b0000000-0000-4000-8000-000000000001",
    "title": "Summer",
    "artist": "钢琴演奏者",
    "artist_refs": [{"artist_id":"a0000000-0000-4000-8000-000000000005","name":"钢琴演奏者","display_name":"钢琴演奏者"}],
    "present": true,
    "duration_seconds": 183.5,
    "group_id": "c0000000-0000-4000-8000-000000000001",
    "release": {
      "id": "d0000000-0000-4000-8000-000000000001",
      "title": "电影原声选集",
      "artist": "原声乐团",
      "artist_refs": [{"artist_id":"a0000000-0000-4000-8000-000000000004","name":"原声乐团","display_name":"原声乐团"}],
      "album_artist": "原声乐团",
      "album_artist_refs": [{"artist_id":"a0000000-0000-4000-8000-000000000004","name":"原声乐团","display_name":"原声乐团"}],
      "year": 1999,
      "source_type": "CD",
      "media_type": "CD",
      "medium_count": 1,
      "track_count": 12,
      "attention_count": 0,
      "artwork": null
    },
    "source": {
      "source_kind": "flac_vorbis_comment",
      "codec": "flac",
      "bit_depth": 16,
      "sample_rate": 44100,
      "channels": 2,
      "bitrate": null
    },
    "match": {
      "kind": "exact",
      "field": "credit",
      "role": "composer",
      "original": "久石譲",
      "artist_id": "a0000000-0000-4000-8000-000000000003",
      "alias": null,
      "release_id": "d0000000-0000-4000-8000-000000000001",
      "track_id": "b0000000-0000-4000-8000-000000000001"
    }
  }],
  "pagination": {"page":1,"page_size":50,"total":1,"total_relation":"eq","has_more":false}
}
```

别名传播的Match示例：

```json
{"kind":"exact","field":"artist_alias","role":"track_artist","original":"Teresa Teng","artist_id":"a0000000-0000-4000-8000-000000000001","alias":"Teresa Teng","release_id":"d0000000-0000-4000-8000-000000000002","track_id":"b0000000-0000-4000-8000-000000000002"}
```

建议空响应示例（⑤均已发布；④移除available_types中的artists，并将catalog_state.artists_state设为unavailable）：

```json
{"query":"无结果词","catalog_state":{"epoch":4,"search_mode":"normalized","artists_state":"ready"},"available_types":["artists","albums","tracks"],"artists":{"items":[],"has_more":false},"albums":{"items":[],"has_more":false},"tracks":{"items":[],"has_more":false}}
```

### 9.4 `PlayerTrack` 映射和播放动作

app层统一从安全曲目摘要映射，不令search/artists导入player：

| PlayerTrack | 数据来源 |
| --- | --- |
| `trackId/releaseId/groupId` | `track_id / release.id / group_id` |
| `title` | title，空白退回“（无标题）” |
| `artist` | artist，空白退回`release.album_artist ?? release.artist` |
| `albumTitle/albumArtist` | `release.title / (release.album_artist ?? release.artist)` |
| `durationSeconds/artworkId` | `duration_seconds / release.artwork?.resource_id ?? null` |
| `source` | 六个安全source字段转camelCase，nullable保持，不从文件扩展名猜codec |
| ⑤ `artistRefs/albumArtistRefs` | 对应refs；艺人文本发生fallback时refs也走同样fallback，不能名字和ID错配 |

行内播放仅替换为该首；下一首/加入队列不打断当前曲目；present还需通过现有capability table，不能把present当浏览器可播保证；最后仍由真实预检确认。满队列不截断，不成功时不清空当前队列。收藏/添加歌单由app组合②/③动作。

## 10. ⑤ 艺人浏览、首字母、详情与队列

### 10.1 首字母表与稳定排序

建议采用 Unicode **Unihan 17.0.0** 的 `Unihan_Readings.txt / kMandarin` 提取首字母，许可 **Unicode License V3**；只内置数据与许可证，不新增pinyin Go模块。使用固定版本数据，不在线查询。

本次核对：`Unihan.zip` SHA-256为 `f7a48b2b545acfaa77b2d607ae28747404ce02baefee16396c5d2d7a8ef34b5e`；其中`Unihan_Readings.txt`为 `575e69c9ad85a4737a889a4f94cbd987042a90a1a6cc16dd3f4ed995c715b17c`，含44,348条kMandarin记录。不是“覆盖所有汉字”的承诺。

未来文件放 `catalog/internal/artistgraph/data/pinyin/`：`initials.tsv`、`LICENSE`、中文`SOURCE.md`及可复现生成脚本（只读上游输入，输出按codepoint排序）。生成时取每字kMandarin字段**第一个读音**，去声调取A–Z首字母；记录多读音及未知项。保存上游版权/许可、源版本/哈希、生成器版本/输出哈希；`go:embed`内置精简表与许可。首次加载预检，坏codepoint/重复键/非A–Z值失败；测试真实表覆盖、边界、稀有字与未知fallback。

分组取有效显示名NFKC后首个非空白字符，不跳过开头标点、不用全名音译：

- 拉丁字母：大小写折叠、去变音符后A–Z；无法投到A–Z的拉丁字母归#，不猜音译。
- 汉字（含日文汉字）：查内置表；表无该字时再查明确繁简对应字，仍无读音归#。姓氏多音按该表第一读音，**不声称正确猜出人名读法**。
- 平/片假名（含半角NFKC后）：`kana`分组，UI写“假名”。
- 其他（数字、标点、希腊/西里尔等）：`#`。

顺序固定A–Z、假名、#；名字排序在组内按规范化有效显示名`COLLATE "C"`再ID，**不是完整中文拼音排序**。拼音只用于首字母分组，不进入搜索索引，不能因有表就支持`zhoujielun`搜索。`sort=album_count`为全局专辑数倒序、同数按名字/ID，页面此时不分首字母组；按名字排序时分组、按专辑数排序时不分组，已由D7裁决。

### 10.2 列表与详情接口

**艺人尚在整理时先返回状态，不读取局部实体。** 已登录且参数合法的`GET /artists`、`/artists/{id}`及names/albums/tracks/queue子路由、艺人类全局搜索、管理员候选/预览在artists_state!=ready时返回`503 artist_catalog_preparing`和`Retry-After: 3`。沿用统一错误信封（request_id来自中间件），不报404、不返回伪造空列表；该专码必须在HTTP错误映射中保留，不能再包装成database_unavailable。前端只显示一句“艺人信息正在整理，请稍后再试。”，普通目录和正在播放继续可用。只有真正ready后才适用下表的200/404语义。

```json
{"error":{"code":"artist_catalog_preparing","message":"艺人信息正在整理，请稍后再试。"},"request_id":"sample-request"}
```

新合并/拆开/改名等写命令返回HTTP **409**（而不是上面的读取503）：

```json
{"error":{"code":"artist_catalog_not_ready","message":"艺人信息正在整理，请稍后再试。"},"request_id":"sample-request"}
```

```text
ArtistSummary = {
  id:UUID, name:string, initial_group:"A".."Z"|"kana"|"#",
  album_count:非负整数, track_count:非负整数,
  roles:Role[], available:boolean
}
Role = release_artist | album_artist | track_artist | composer | conductor | performer | producer
```

`album_count`按所有角色的**可见参与group去重**，不是发行版本数；`track_count`按所有角色参与的present track_id去重，发行级署名可扩展到其曲目。列表按角色过滤时只筛选艺人，数字仍是该艺人的全角色总数，避免同一个字段随筛选暗换含义；需要筛选内计数则另给明确字段。本设计的专辑区只列实际album_artist关联，不把缺AlbumArtist的所有参与者冒充专辑艺人。

| 接口 | 参数 | 合同 |
| --- | --- | --- |
| `GET /artists` | `q?`（同规范化与200字节），`initial?`，`role?`，`sort=name|album_count`，`page/page_size`（1/50，最大100） | `{items:ArtistSummary[],pagination}`。未重定向且至少有present参与者才进普通列表；q匹配主名/别名；角色是7项allowlist，字母只能A–Z/kana/# |
| `GET /artists/{id}` | UUID | `{artist:ArtistSummary,revision,name_source:"auto"|"manual",names:[ArtistName],names_pagination,role_counts:[{role,total,present}],redirect_artist_id:null|UUID}`；首批名字20条、总数显式；不内嵌所有专辑/曲目 |
| `GET /artists/{id}/names` | page/page_size默认1/50，最大100 | 所有原名/别名分页。`ArtistName={id,key_id,name,is_display_name,origin}`；同key的繁简写法作为同一组展示，按key拆别名，不能让“只拆繁体字”下一次自动重合 |
| `GET /artists/{id}/albums` | page/page_size | `{items:[{group:ReleaseGroupSummary,credited_release_id:UUID}],pagination}`；只取其有效album_artist关联的可见发行，再group去重；代表仍按全组规则，链接指定实际参与版本 |
| `GET /artists/{id}/tracks` | `role=track_artist|composer|conductor|performer|producer`必填；page/page_size | `{items:[TrackSummary],pagination}`；按角色分栏各自分页，**含missing**，按当前发行规范化标题、发行ID、碟序、轨序、trackID；同轨同角色只一条 |
| `GET /artists/{id}/queue` | `order=album|random`默认album；random必须带`seed` UUID | 见下一节；以所有参与角色并集选择track_id，去重 |

没有姓名/无曲目的已存在艺人，详情仍200，`available:false`、数量0，供收藏和恢复治理使用；普通列表/搜索不展示这种空身份。不存在404。被合并旧ID详情200给安全旧身份摘要与`redirect_artist_id`，app用replace导航到目标，不使用可被浏览器长期缓存的301；其他读子路由按canonical解析，响应返回canonical `artist_id`，不无限重定向。写给旧ID报artist_redirected。

`role_counts.total`包括missing，`present`单独给数量；missing行保留上次事实，标“文件缺失”、不能播放、仍能取消收藏/加入歌单（是否允许新增缺失收藏沿②合同），专辑不可见时不做失效详情链接。普通目录组/发行404语义不被艺人详情放宽。

错误：401、`400 invalid_id/invalid_query/invalid_pagination/invalid_artist_role/invalid_artist_initial/invalid_artist_sort/invalid_queue_order/invalid_seed`、404、`503 artist_catalog_preparing`、`503 database_unavailable`。艺人read API不返回修改人、原始操作state、音乐来源路径；管理命令另走§7。

### 10.3 播放全部 / 随机播放队列

队列响应：

```json
{"artist_id":"a0000000-0000-4000-8000-000000000003","order":"album","seed":null,"items":[],"total":0,"returned":0,"limit":10000,"truncated":false,"missing_total":0}
```

- 候选是所有有效参与角色（含发行级艺人）的track_id并集，去重；服务端排除missing，`total`为present候选总数，`missing_total`为被排除的不同missing轨数量。
- album顺序为发行标题、发行ID、碟序、曲序、trackID；random先对**全体候选**以`md5(seed || ':' || track_id)`排序（再trackID），再LIMIT 10,000，不是先取前10,000再打乱。固定seed在数据不变时可重试得到同一集合；排序hash只作随机序，不作安全标识。
- 返回完整安全TrackSummary，最多10,000；批量补全分成最多10个1,000-ID批次，置于同一只读快照、关闭前一批rows后读取下一批，不绕过现有单次上限；不做每轨详情调用。投影坏项整体503，不静默丢项。
- 前端capability table再过滤格式不支持者，分别报告加入数量、missing数量、格式跳过数量。`truncated=true`时用一句提示：“曲目较多，本次最多播放 10,000 首。”；没有可播项或队列满均不清空原队列。播放全部不自动追下一页，更不承诺播放超过队列容量的全部库。
- 随机播放先对全体候选生成带seed的随机排列，再取前10,000首；app按返回顺序建立队列，不能先限量再洗牌，不能把未取出的尾部宣称为已加入。D7 已明确**不改变全局播放器模式**：保留用户现有 shuffle/repeat 设置，不为本次动作隐式切换开关。

队列接口不纳入“搜索首屏300 ms / 单字符1 s”，但需单独测10,000摘要响应大小、延迟与取消，避免1,000上限被误当作十万完整载荷许可。

## 11. 艺人收藏：向②+③实现线提出的合同

**D8 的用户行为已裁决**：收藏保留原艺人锚点，合并后按当前身份去重显示，恢复旧身份的拆开后恢复原收藏。收藏线的物理表和端口由另一个 agent 在 `../11-04-classical-m5-favorites/design.md` 起草，主会话统一命名与装配；本节定义⑤必须得到的最小合同，不替收藏 owner 写另一份设计。物理组织可二选一，不重新打开已裁决的产品语义。

### 11.1 两种表形状的接受条件

共同要求：保存 `user_id`、**不可被合并重写的原艺人ID锚点**、`created_at`；原锚点按用户唯一，允许一个用户原来对合并双方各有一条收藏。保留可重建的当前解析身份 `resolved_artist_id`（同表列或收藏 owner 的独立解析表），不能用它替换原锚点，也不能对 `(user_id,resolved_artist_id)` 加唯一约束而丢掉双方意图。具备按本人/原锚点定位、按本人/解析身份归并及按收藏时间稳定分页的索引；用户删除可级联，艺人缺失、合并、改名不删除收藏。

| 组织方式 | ⑤接受的最小结构与约束 | ⑤的后续迁移 |
| --- | --- | --- |
| 通用收藏表 | `user_id,target_kind,target_id,created_at`，唯一`(user_id,target_kind,target_id)`；`target_kind='artist'`时`target_id`是原艺人锚点。艺人解析列只用于artist行，或以带类型/用户/原ID关联的附表保存；已有group/track行不能被重解释。通用UUID列不能假称同时有多个目标表FK：可通过类型化附表/目标注册表实现数据库约束；若公共表采用应用层引用检查，必须在同一事务经catalog验证艺人存在、禁止物理删除艺人、后台升级发布前检查悬挂引用并覆盖并发测试，明确其并非多态FK保证 | 在⑤存在artists后扩展target_kind的CHECK/DTO allowlist，增加艺人解析存储与相应索引/引用保护，不迁移或重写既有group/track收藏 |
| 独立艺人收藏表 | 逻辑列`user_id FK users ON DELETE CASCADE, artist_id FK artists RESTRICT, created_at`，唯一`(user_id,artist_id)`；`artist_id`就是原锚点，解析列或附表的`resolved_artist_id`同样FK artists。艺人永不物理删除，时间排序加稳定ID平局键 | ⑤新增该表及解析存储/索引；②不提前引用尚不存在的artists；对外仍复用收藏owner的通用状态、列表和动作合同 |

二者都必须满足**先解析归并、再计数/排序/分页**；不接受前端去重、先分页后去重或把本人全部收藏无界加载到Go。物理表名、端口命名和引用约束方式由主会话按收藏线的设计统一，以上接受条件不因选择通用表而放宽。

### 11.2 收藏锚点与用户行为

1. ②先交付专辑(group)与曲目收藏；**⑤负责艺人收藏迁移、端点/解码、批量状态查询及收藏页第三栏**。艺人页面心形与收藏页 `tab=artists` 共享本人状态。
2. 合并时保留每一条原收藏锚点和原收藏时间，仅重建解析身份。同一用户原来收藏双方时只显示一个canonical条目，取等价锚点中较晚的 `created_at` 排序，再按canonical ID稳定打平；列表的total是归并后精确数。
3. 取消canonical条目删除**本人所有当前解析到该身份的原锚点**，其他用户不受影响。拆开恢复旧ID时，尚未被本人取消的原锚点自然恢复；拆到全新ID不为所有用户凭空复制收藏。
4. 合并后新收藏锚定当前canonical；如果本人已有等价收藏，幂等PUT不新增锚点、不改原时间。真正新增的canonical锚点不自动跟随以后新拆的人。失去present曲目的已收藏艺人保留并可取消，身份不可用不等于收藏不存在。
5. 本人CRUD遵循②的幂等PUT/DELETE、SQL按会话user_id隔离、先同源后会话、用户不由路径/body提供、不进operations。路径沿收藏线统一合同（可映射为`GET /me/favorites/artists`、`PUT|DELETE /me/favorites/artists/{id}`）；默认page=1/page_size=50，最大100，不另建竞争API。不存在艺人的新增收藏404。

### 11.3 最小端口、事务与展示合同

- catalog 提供**接收既有事务**的有界批量艺人解析/摘要端口，输出原ID、当前canonical ID、可用性与显示名；收藏 owner 不读取艺人私表。单次批量最多1,000个ID，治理大量映射可在同一事务分批调用；不为每条收藏单独查询。
- app 注入收藏 owner 的窄写端口（逻辑签名 `RefreshArtistResolution(ctx, tx, mapping)`），由⑤治理在**同一数据库事务**中传递受影响的原ID→当前ID映射，只刷新收藏 owner 的解析缓存；失败使艺人治理和操作记录一起回滚。该端口不得自行提交、读请求user_id或允许管理员读取他人收藏；这是系统投影更新，不是代用户执行收藏动作。
- 艺人收藏PUT/DELETE必须先在自身事务通过catalog公开解析合同获取同一 `CATP` gate，再读取最新canonical并锁/写本人收藏行；治理也先gate再行锁。不能先锁收藏行后等待gate，不能提交后异步刷新解析缓存。扫描不改变人工redirect，不调用跨用户收藏写入。
- 初次⑤迁移没有既有artist收藏，但不能假设通用收藏表整体为空。后续版本升级在后台经公开解析合同重建缓存，它是**艺人能力发布**的前置条件，不再阻塞应用ready；不重写原锚点、created_at或group/track收藏。整理期间保留旧艺人收藏锚点，艺人收藏列表/状态读返回`artist_catalog_preparing`、艺人收藏新写返回409 `artist_catalog_not_ready`，不把未就绪显示成未收藏。app将艺人与group/track批量状态请求分开，专辑/曲目收藏、歌单及其中的播放始终照常。catalog/governance不能直接 `UPDATE favorites`，所有收藏存储写入归其owner。
- 列表返回`{artist,created_at,available}`，created_at按上节归并规则取得；不展开无限原锚点数组。批量心形查询按当前解析身份回答，按输入ID映射回结果；成员停用保留个人数据，退出/换用户清缓存。
- 不把`favorited`混入共享catalog/search DTO。app用收藏线公开的批量状态接口和query前缀组合心形，⑤补`artist` target词表、上限、状态decoder及收藏第三栏，避免N+1。

本节是主会话统一收藏设计的**已确定输入要求**，不是“艺人收藏已经实现”的声明。歌单只保存曲目ID；⑤没有“整位艺人加入歌单”的新批量动作，每曲添加及从队列添加按③合同。

## 12. 前端合同、地址、分层及失效

### 12.1 新feature与依赖

- 新建 `features/search`：suggestions/results DTO、decoder、endpoints、query keys、URL纯函数、查询hook、搜索框/结果栏；消费catalog公开的CatalogState，不直接管理播放或收藏。catalog的公开只读模块拥有status端点/decoder，app统一协调，不让player导入search。
- 新建 `features/artists`：列表/详情/名字/角色曲目/队列的只读DTO、hook、视图及整理状态；可导入catalog的公开摘要/封面/基础ArtistRef合同，**catalog不反向导入artists**。
- 艺人管理员命令/候选治理、成功/不确定/重试状态、操作记录展示留在 `features/governance`，它可消费artists公开的类型/搜索选择能力；artists通过插槽接收管理员区域，不导入governance。
- 公共可播放摘要及 `ArtistRef` decoder放catalog的**纯数据模块**，listening/search/artists/②③可复用；不要经含CSS组件的barrel让Node E2E加载样式，也不让共享API层拥有业务DTO。
- `app/routes/search-page.tsx`、`artists-page.tsx`、`artist-detail-page.tsx`组合查询、播放、心形、更多菜单/添加歌单、管理面板、跳转与查询失效。跨feature的PlayerTrack转换在app统一小模块复用历史映射，而不是复制四份。
- `player`只增加公开的ArtistRef数据/渲染插槽，底栏/正在播放的艺人链接由app注入；不为了链接导入artists feature。shared UI仍不认识路由、艺人、收藏owner。

### 12.2 URL与查询键

地址是业务状态权威，草稿输入/弹出建议和焦点只在组件内：

```text
/search?q=久石譲&artists_page=1&albums_page=2&tracks_page=1
/artists?q=&initial=Z&role=composer&sort=name&page=2
/artists/{id}?albums_page=1&track_role=composer&tracks_page=2&names_page=1
/me/favorites?tab=artists&page=1
```

非法/默认值按现有router校验归一；换q把各栏page重置1，换角色/排序重置对应页。前进/后退、刷新、复制链接能复现查询；搜索页不依赖上一次的内存结果。只在⑤启用artists页码/栏。

使用现有 `viewerQueryKey(generation, ...)`；下列`g`是会话代次，`e`是catalog epoch，二者不能混为一谈：

```text
catalogStateKeys.current(g)              → viewerQueryKey(g,"catalog-state")
searchKeys.all(g)                         → viewerQueryKey(g,"search")
searchKeys.suggestions(g,e,q,limit)       → (...,"suggestions",{e,q,limit})
searchKeys.page(g,e,type,q,page,pageSize)  → (...,"results",{e,type,q,page,pageSize})
artistsKeys.all(g)                        → viewerQueryKey(g,"artists")
artistsKeys.page(g,e,params)              → (...,"list",{e,...params})
artistsKeys.detail(g,e,id)                → (...,"detail",id,{e})
artistsKeys.names(g,e,id,page,pageSize)    → (...,"names",id,{e,page,pageSize})
artistsKeys.albums(g,e,id,page,pageSize)   → (...,"albums",id,{e,page,pageSize})
artistsKeys.tracks(g,e,id,role,page,pageSize)→ (...,"tracks",id,{e,role,page,pageSize})
artistsKeys.queue(g,e,id,order,seed)       → (...,"queue",id,{e,order,seed})
```

键里的q保留首尾trim后的用户查询，不在浏览器复制整套规范化表，也不把两个大小写不同的用户输入错误复用旧query回显。建议输入防抖250 ms，IME composition未结束不查询；AbortSignal取消旧请求，结果按完整query key绑定，慢响应不能覆盖新词。`/`仅在非input/textarea/contenteditable、无组合键且非IME输入中聚焦；Esc关闭建议，方向键移动，Enter执行选择/搜索，焦点可见。窄屏提供等价搜索入口，不挤掉导航；保持现有设计风格。

全局结果栏使用专用 `SearchPagination` decoder：`eq`显示精确总数，`gte`显示“1,000+”，翻页由`has_more`控制；不从total下界计算最后一页、不禁止第21页。单字符与长词都正常请求、独立加载/重试，不出现“至少输入两个字符”；建议框不额外请求总数。名字排序的艺人列表渲染首字母组，专辑数排序取消分组；切换排序/筛选仍重置页码。

app的已登录壳在挂载/回到前台时读`/catalog/status`；search_mode=legacy或artists_state=preparing期间每3秒轻量轮询，隐藏页暂停，全部已发布后停止轮询。拿到更高epoch就取消旧search/artist请求、失效当前目录refs及个人音乐元数据并重取当前页；不重置用户q/页码，不自动播放，不动播放器模式/进度。搜索响应的epoch也参与核对，晚到的旧epoch结果不能盖掉新epoch；新epoch响应先同步状态再按正确查询键展示。整理中的艺人页只显示短文案并等状态变ready，不做通用503密集重试。

app在治理成功后失效catalog、search、artists、相关governance、listening及②/③元数据查询；favorites成员态只因本人切换/身份解析变化失效，不能无条件将所有用户客户端状态置空。扫描反馈线报告scan完成或用户刷新时同样触发这些**公开前缀**失效；不把search hook嵌进scanner feature。所有请求带会话代次，退出/换用户中止并清旧缓存。

播放器里的当前/队列元数据是既有快照，光失效Query缓存不会更新它：⑤在app增加受影响trackIDs的有界刷新/映射更新，只替换显示名和refs等元数据，不换entryId、不重新load音频、不重置进度。至少当前轨和打开的队列页要刷新；已合并旧ID链接可经redirect兜底，不能因此声称改名已即时同步整个内存队列。

### 12.3 各处可点击艺人名的additive DTO变化

⑤保持旧 `artist`、`album_artist`、credit `name` 文本字段，追加明确身份对应，不让前端拆字符串猜ID。**艺人尚未发布时所有普通目录/历史/搜索/歌单/曲目收藏DTO的refs返回`[]`，文本与播放事实照常返回，不查半成品艺人图；共享摘要/艺人读取须在同一数据库只读快照中检查能力状态并加载refs，不能先判断ready再跨快照读已开始重建的半图；前端全局状态不是ready时也隐藏内存中缓存的旧refs链接，显示纯文字。** 状态未知时同样先显示文字，ready后重取refs再出现链接，不因整理把艺人名隐藏：

- `ReleaseSummary`：`artist_refs:ArtistRef[]`、`album_artist_refs:ArtistRef[]`；空/推断占位为`[]`，必传不为null。ReleaseGroup representative和所有复用摘要一起带字段。
- `ReleaseTrack`、`TrackSummary`：`artist_refs`；每个credit追加`artist_refs`，逐轨一般一位，专辑级聚合署名可多位。每数组最大100，发行总credit上限仍10,000。
- 历史、收藏、歌单、全局搜索、艺人曲目：复用新增TrackSummary字段；安全音频字段不变。
- `PlayerTrack`：camelCase refs；发布的数组name与显示串一致，fallback与旧历史映射一致。所有构造入口/decoder测试一起改，禁止用`as`逃过缺字段。
- 节点可点范围：专辑详情头部、曲目行署名/四角色、播放器、正在播放、历史、搜索、收藏与歌单；app仅在artists_state=ready且有效refs已取得时渲染`/artists/{artist_id}`链接；整理期间一律为同名文字。缺失轨也能访问保留的艺人身份，链接不依赖文件存在。
- **专辑卡片整卡链接不变，卡片艺人名仍是文字**，不能嵌套链接。只有另有明确链接区的详情/行才用refs。

CatalogState变为ready后只做有界元数据补全，当前轨/队列项的entryId、播放进度、音频实例保持不变；不借显示链接重新load音频。新增字段是后端additive，但项目解码器对必需字段严格，必须同一交付更新全部复用样本和构造器。④的样本在⑤更新时保留匹配/分页语义回归，不允许顺手重写不相关字段。

## 13. 迁移、交付顺序与跨线冲突

### 13.1 未来迁移只定名字，不预占编号

下表是**待实施的迁移名字**，本轮没有创建SQL文件；正式编号由主会话合并时分配。不要修改或重排已发布迁移。

| 子项 | 迁移名字 | 内容 / 顺序依赖 |
| --- | --- | --- |
| ④ | `catalog_search_projection.sql` | pg_trgm启用、搜索值表的generation、投影目标/发布版本与epoch状态、subject输入revision/完成标记、后台work清单及其索引、GIN。SQL只建结构，后台Go填充，不用lower/unaccent替代Text |
| ⑤ | `artist_catalog.sql` | artists、名字键/原写法、scan/correction署名、有效关联和profiles、artist_search_names及generation；扩充能力发布/完成标记、相关FK/唯一/check/GIN；依赖④ |
| ⑤ | `artist_governance.sql` | 三类意图表、revision约束、operations的artist目标与新类型、operations_artist_targets；保留全部既有类型和约束名，DROP/ADD只扩词表 |
| ⑤ | `artist_seed_aliases.sql` | 周杰伦/周杰倫/Jay Chou初始身份/名字映射；固定seed身份，不制造operations；依赖artist_catalog |
| ⑤ | `artist_candidates.sql` | 原始署名证据、候选信号及忽略意图表/索引；ignore操作引用在本版本有效；依赖artist_catalog与artist_governance |
| ⑤（收藏owner协作） | `artist_favorites.sql` | 按§11接受条件扩展通用target_kind/解析存储，或新增有artists外键的独立艺人收藏表；加入解析缓存/索引与引用保护，保留原锚点；依赖②收藏公共合同和⑤艺人表，不提前放入②迁移 |

生产中的`normalized_key/hash`等值一律由既定版本Go生成；种子是明确小集合的例外，不把以后任意字表更新塞进SQL。DDL可以在新空表上一次建普通GIN；回填前后记录索引大小和ANALYZE。迁移测试要覆盖扩展缺失/权限失败、已有扩展、fresh/rerun/旧库升级、约束漂移、checksum、后台中断续跑及发布前后HTTP能力状态，不能仅在空库测试；未来编号仍只由主会话分配。

### 13.2 合并实施顺序（不是本次执行）

1. 以§15已裁决结果准备实施：主会话将人工选择路线、选择者、结果与理由同步两个子项PRD，并统一收藏线物理表/端口以满足§11；补implement.md和真实spec/research manifests，再单独取得实施批准。文档规则同步是明确实施项，见下列清单，D1–D8直接作为实施基线。
2. 总体合入顺序遵循父PRD的P个人音乐 → F扫描反馈 → S检索（各自worktree）。S在前两线合入后变基确认真实持久化签名，再完成④textnorm及数据许可/fixture；新投影表、短启动登记、单worker后台回填/旧ILIKE回退与原子发布；补全扫描、M3修正/撤销、claim/撤销双写/输入revision矩阵与依赖规则。
3. ④完成匹配/索引/旧q、两类新搜索DTO及API样本；前端两栏/建议/URL/键盘与app动作组合；隔离10万轨计划、≥2字符300 ms/单字符1 s门禁及1,000阈值分页。
4. ⑤先建身份/名字/意图模型、seed、raw证据及后台回填，补艺人整理状态/治理409/暂不渲染refs；扫描不写治理及回填并发的集成测试先行；再合并/拆开/改名与全部操作记录解码。
5. ⑤上艺人列表/详情/队列/候选；加入别名搜索、第三栏及身份DTO；最后与收藏owner接第三栏/心形/合并解析，并补全各处链接与队列元数据刷新。
6. 独立验证④S1–S5、⑤R1–R7，再跨项联合验收。不能把“接口已返回”替代真实播放、同用户状态同步与重扫保存意图。

**实施时必须同步的规则/决策文档清单（本轮只列条目，不修改这些文件）**：

- `frontend/DESIGN-components.md`：将“不得按同名字符串认人”细化为“前端不能自行字符串匹配/拆名认人，只消费后端artist_refs；后端允许按已版本化规范化键自动归一，人工scope拆分优先”。同步原署名链接文字与艺人主名区分、整理期纯文字及短提示、就绪后只补refs不打断播放、卡片不嵌套链接、名字/专辑数两种分组行为。
- `.trellis/spec/backend/library-scan-contracts.md`：写明纯标签拆分与身份解析分离；移除硬编码别名改写展示的旧合同；新版原始多值证据、第一类候选仅在既有曲目重扫后启用；PersistCandidate同事务投影、同key初始归一、扫描只读治理意图且不改revision/operations；重扫保留发行快照及既有track覆盖；回填并发时在同事务写目标代次/输入revision，后台不得覆盖最新提交。
- `docs/architecture/adr/0002-metadata-overlays-decisions.md`：追加规范化自动身份的有限范围、扫描事实/原文与人工主名/归属覆盖的优先级；有效专辑字段修正/清除/撤销/认领同步搜索及署名；后台版本回填、旧ILIKE回退、双写/原子发布与旧原文缺失处理。正文`0002-metadata-overlays.md`不再追加长篇。
- `docs/architecture/adr/0003-governed-operations-decisions.md`：追加artist target、新Kind/双资源revision/Describe、R4不可Revert及反向命令恢复旧身份、按发行快照拆开与名字键对忽略；明确收藏原锚点属个人意图，治理仅同事务刷新收藏owner的解析缓存。正文`0003-governed-operations.md`不再追加长篇。
- `.trellis/spec/backend/catalog-rest-contracts.md`及前端对应API合同：同步旧q相关性、新搜索`SearchPagination`的1,000阈值/`total_relation`/`has_more`、单字符可搜、CatalogState/epoch、整理状态读取503/写入409、空refs及新旧匹配切换；不要把搜索有界total扩散成其他分页接口的精确total变更。

### 13.3 接触点和可能冲突文件

| 文件 / 合同区域 | 同线或跨线协作要求 |
| --- | --- |
| `backend/internal/app/app.go`、`routes_test.go`、`dependency_rules_test.go` | ②③新增服务/路由/依赖，④⑤后台worker/status路由与Kind注册均会修改；主会话合并增量，不覆盖另一线包规则 |
| `backend/cmd/roomusic/main.go` | ④短版本登记及后台worker生命周期；ready不等待投影，关闭前取消/等待批事务；不要和扫描恢复的顺序改动冲突 |
| `backend/migrations/`、迁移数量/样本测试 | 所有线共享编号，按本文名字合并后统一编号，保留旧checksum |
| `catalog/track_summary.go`、`projection.go`、`read.go`、`groups.go`、`httpapi/handlers.go`、`internal/releasesql/*` | ②③消费摘要；⑤追加refs不能改变missing/音频/封面合同；④旧q同时供治理选择器使用 |
| `catalog/ingest/persist.go`、`persist_track.go`、`observation.go`、`staging.go` | ⑥扫描反馈可能要candidate成功ID/新增差量；④⑤加投影与署名。F线先合入；若其已扩展`PersistCandidate`返回结果结构，S变基后沿用该公开合同并将投影纳入原事务，不退回本轮代码基线的旧签名 |
| `library/scanner.go`、`scans.go`、`library/parser/tags.go`与各格式tag入口 | ⑥进度/新增报告只算已提交candidate；新搜索/艺人投影失败要沿既有catalog_write_failure使扫描incomplete，不计“成功新增”，不额外执行missing；原始标签证据的预算不是进度计数 |
| `catalog/governance/{corrections,revert,claim,claim_revert,group_change}.go` | ④刷新有效值、⑤补署名及gate；必须在执行器而非handler接入，含撤销/来源侧 |
| `operations/{operations,kind,records}.go`与HTTP | ⑤新增artist引用和类型；其他线个人数据不接Run，不能把收藏迁进共享治理 |
| `backend/internal/app/testdata/api-samples/`及样本生成器 | ④新增search目录；⑤新artist/operation样本与所有ReleaseSummary/TrackSummary复用样本；merge前确认生成没有吞掉其他线样本 |
| `frontend/src/app/{router,runtime}.tsx/ts`、`shell/nav-items.ts`、shell布局 | ②③我的音乐tabs；④全局框；⑤艺人入口。按现有分组增加，不重做整站导航 |
| `app/routes/{album-detail-page,history-playback,me-layout,...}` | 心形/歌单/播放slot是②③与④⑤共同接点，优先统一app的安全摘要映射及动作组合，feature之间不要互相深导入 |
| `features/catalog/api/decoders.ts`、`features/listening/api/decoders.ts`、`features/player/engine/queue.ts`、底栏/正在播放 | ⑤refs与②心形插槽共存；所有PlayerTrack构造者要同步，不重置播放引擎 |
| `features/governance/api/decoders.ts`、`change-summary.ts`、`operations-search.ts`、记录列表 | ⑤完整类型/目标/错误分支；旧操作可撤销逻辑不能被新类型的默认分支破坏 |
| `frontend/src/test/*`、`frontend/e2e/support/*`、真实服务runner | 共用mock词表、sample decoder、路由列表与合成库；新case独立目录与清理，不依赖用例先后 |
| `frontend/DESIGN-components.md`、`.trellis/spec/backend/library-scan-contracts.md`、`catalog-rest-contracts.md`、ADR 0002/0003 decisions及frontend状态规范 | 按§13.2逐条同步已裁决规则；收藏线也会写API/个人数据规则，主会话合并增量；**只在以后实施获批后**由owner修改，本轮零修改 |

收藏+歌单线应发布：安全TrackSummary复用方案、批量心形状态、action slots、query前缀、队列上限/失败行为。扫描反馈线应发布：candidate提交成功信号、scan终态刷新事件与差量事务边界。它们不是新搜索/艺人能力的隐式写owner。

## 14. 测试计划、样本清单与安全验证命令

### 14.1 局部测试矩阵

| 层 | 关键断言 | 对应验收 |
| --- | --- | --- |
| textnorm单元/fuzz/race | 固定顺序、幂等、五组双向例子；Beyoncé/Beyonce等价、ガ/が等价、ﾊﾟ/ぱ等价，但が≠か、ば/ぱ/は两两不同、炼≠链；组合/预组合标记、编号Pd、空串、supplementary Han、LIKE字面转义、嵌入表坏行/哈希/循环；艺人解析不误合并这些反例 | S1 |
| 搜索PG集成 | 每个新增字段命中；非代表成员、exact/prefix/contains排序与平局、去重后分页；0/999/1,000为eq、1,001/热门词为gte、has_more和第21页及空页；旧路由精确total/有q相关性/无q原序、missing排除、只匹配某轨credit不带出同发行兄弟轨、空q和旧attention兼容 | S2–S4 |
| 有效值刷新 | set/null/clear、修正撤销、claim来源/目标及撤销、扫描改基值但覆盖不变；同时断言页面与搜索匹配相同；投影写失败业务与record一起回滚 | S2、R1 |
| 回填/升级 | 启动立即ready/目录播放可用；回填中断续跑、逐批日志与固定清单总数/skip计数；两进程单worker、丢锁停止；新UUID落游标前后不漏、missing/零字段完成标记、旧build不得发布；后台失败不使全站503、规范化冲突不覆盖意图；种子不重种 | S1、R1 |
| 回填并发/切换 | 扫描/M3先提交后回填、回填先锁后扫描/M3、clear/revert/claim双方、missing/移动/新增曲目全部交错；新revision不被旧结果覆盖；只补缺口；发布前原ILIKE/发布后新路径、一个响应内计数与页行不混代；发布前后进程退出均幂等 | S1–S4、R1 |
| 在线可用性 | worker人工阻塞/退避/失败期间ready=200；真实预检/Range音频/播放、历史、group/track收藏与歌单正常；测gate等待/连接池及前台p95；艺人整理读503专码、新治理409专码且无副作用/无成功记录、403优先、旧幂等记录可重放 | S5、R2/R4/R7 |
| 艺人PG集成 | 自动同key、同名scope例外、多角色/多名映射、最常见票数无重扫累积、手工主名优先、M3有效AlbumArtist、缺失/恢复/移动/换发行/新身份边界 | R1、R4–R6 |
| 治理与并发 | 401/403、origin顺序、无效ID/shape、双方revision冲突、同键重放/异义、并发合并相反方向无环、scan与merge/split/correction交错无死锁、scope basis变化、全事务回滚、所有新Kind不可撤销 | R2 |
| 操作记录 | artist主/次目标都可筛选，与release/group并集去重分页；Describe批量、坏state为null、旧类型回归、merged旧目标链接/restore身份 | R2 |
| 候选 | 仅两种信号；多人/未知/非1:1反例；旧数据后台回填不生第一类信号、既有曲目新版重扫才生效且UI无重扫说明；原样重扫无重复、同key对忽略持久、新key对可提醒、解除忽略、人工scope拆开不反复提示 | R3 |
| 拼音/艺人读取 | A–Z/kana/#、Beyoncé/周杰伦/久石譲/さユり、日文汉字、罕见缺表、多音字规则；分页边界、角色与album_count语义、不可用实体/redirect | R4–R5 |
| 队列 | 多角色同轨去重；当前版本/发行定位；0/1/1,000/10,000/10,001；seed可复现且先全体随机再截断；超限一句提示、全局shuffle/repeat不变；missing/不可播计数；满队列/无可播不改变现有队列 | R4 |
| 收藏联调 | A/B/admin隔离、停用保留、合并双方已收藏只显示一项、取消清本人等价锚点、恢复旧身份不复制他人新收藏、解析投影失败整体回滚；整理期间保留艺人锚点、艺人读写专门状态，group/track收藏不受影响；收藏解析未完成不发布艺人 | R4 |
| 前端单元/样本 | 所有新DTO真样本往返，非法enum/null/缺字段/过大数组拒绝；公共摘要→PlayerTrack；CatalogState/epoch切换、整理提示与空refs、旧响应不可覆盖新代次、就绪后链接出现不重启播放；SearchPagination的eq/gte合法组合与1,000+展示、has_more驱动第21页；query keys代次、URL恢复、单字“爱”、建议过时取消/IME、错误/空页/重试 | S3/S5、R7 |
| 浏览器/真实服务 | `/`、IME/Tab/Esc、纯键盘搜索并播放；三栏独立翻页；五宽度320/375/768/1024/1440无溢出、axe；管理员合并/拆开/改名后重扫仍保持；alias立即命中；普通成员403；真实播放器不被改名重启 | S5、R7 |

变异检查只针对权限、revision、事务/扫描gate、回填/种子/覆盖保留等关键逻辑，不扩大到无关业务。新增回填并发用例通过测试barrier控制事务顺序，不靠sleep碰运气；崩溃点至少覆盖投影写前、写后未提交、批提交后、发布前和发布提交后。所有SQL/扩展/真实服务验证只能用下述隔离环境；不扫描仓库真实music，也不以测试目的读取用户实例。

### 14.2 必须落盘的接口样本

未来样本路径在 `backend/internal/app/testdata/api-samples/`，均由真实app handler生成 `{status,body}`，不是手写JSON替代服务合同；前端decoder逐个往返。本文JSON只是设计示意，不声称已生成。

- **④ `search/`**：`suggestions`、`suggestions-empty`、`suggestions-limit`（每栏has_more）、`suggestions-single-character`；`albums`、`albums-non-representative-match`、`tracks`、`tracks-credit-match`、`tracks-label-match`、`tracks-catalog-number-match`、`results-empty`、`results-out-of-range`、`results-ranking`、`results-single-character`、`results-total-999`、`results-total-1000`、`results-total-1001`、`results-total-capped`、`results-capped-page-21`、`results-capped-out-of-range`。分别覆盖exact/prefix/contains、literal `%/_/~`、eq/gte与has_more的合法组合；旧两路由另补有q相关性/精确total及无q排序样本。
- **④后台/回退样本**：`catalog/status-legacy`、`status-search-ready`；`search/suggestions-legacy`、`albums-legacy`、`tracks-legacy`、`results-after-publish`；旧目录带q回退及M3提交即命中；统一新DTO不读部分投影。
- **⑤搜索增量**：`suggestions-artists`、`artists`、`artists-alias-match`、`albums-alias-match`、`tracks-alias-match`、`same-name-split-results`、`artists-preparing`、`catalog/status-artists-preparing`、`status-all-ready`；更新④复用摘要的refs，不改④原语义。
- **⑤ `artists/`**：`list-preparing`、`detail-preparing`、`queue-preparing`、`list-name`、`list-album-count`、`list-role`、`list-initial`、`list-empty`、`list-out-of-range`、`detail`、`detail-unavailable`、`detail-redirect`、`names`、`names-out-of-range`、`albums`、`role-tracks`、`role-tracks-missing`、`queue`、`queue-empty`、`queue-random`、`queue-truncated`。
- **⑤治理**：`artist-merge`、`artist-split-preview-aliases`、`artist-split-preview-albums`、`artist-split-new`、`artist-split-restored-id`、`artist-name-set`、`artist-name-auto`、`artist-candidates`、`artist-candidates-empty`、`artist-ignore`、`artist-unignore`、`artist-candidates-after-rescan`、`artist-candidates-new-key-pair`、`artist-merge-not-ready`、`artist-split-not-ready`、`artist-rename-not-ready`；`operations/artists`、`operations/artist-source-filter`、`operations/artist-union-filter`、`operations/artist-damaged-state`。
- **⑤收藏协作**：`favorites/artists`、`artists-empty`、`artist-added`、`artist-removed`、`artist-unavailable`、`artists-merged-deduplicated`、`artists-restored`、`artists-preparing`以及批量状态含artist类型；历史/歌单/group与track收藏补空refs仍可播样本。
- **错误样本**：未登录、普通成员403、无Origin/跨源、invalid_query/limit/type/ID/role/initial/sort/seed/pagination、revision_conflict、idempotency_conflict、same_artist、artist_redirected、artist_scope_changed、artist_scope_too_large、invalid_artist_name、no_changes、artist的not_revertible、读取artist_catalog_preparing、写入artist_catalog_not_ready、安全数据库503；两种整理错误不得混淆为database_unavailable。无效sample必须确认不泄露源路径、SQL和内部state。

### 14.3 实施时可执行的隔离验证方式（本轮未执行）

**禁止**连接/exec/启动/停止用户 `roomusic-postgres-1` 或 `roomusic`实例。只使用`roomusic-test` compose项目、绝对路径compose文件、`--env-file /dev/null`、空闲端口、随机库名；不要直接运行会采用默认数据库名且运行全量测试的 `scripts/test-integration.sh`。当前真实库smoke runner使用另一项目名/固定内部库名，不符合本轮限定，本轮不运行。

下面是以后局部验证的安全模板；运行前协调并行agent，若roomusic-test已有容器/卷则**退出而非清理他人环境**：

```bash
ROOT=/home/qlqq/workspace/ROOMusic
exec 9>/tmp/roomusic-test-m5.lock
flock -n 9 || exit 1
# 只查询roomusic-test标签，不连接任何已有用户数据库。
test -z "$(docker ps -aq --filter label=com.docker.compose.project=roomusic-test)" || exit 1
test -z "$(docker volume ls -q --filter label=com.docker.compose.project=roomusic-test)" || exit 1
export ROOMUSIC_TEST_PG_PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
export ROOMUSIC_TEST_PG_DATABASE="m5_search_$(python3 -c 'import secrets; print(secrets.token_hex(8))')"
export ROOMUSIC_TEST_PG_USER=roomusic_test
export ROOMUSIC_TEST_PG_PASSWORD="$(python3 -c 'import secrets; print(secrets.token_hex(16))')"
export ROOMUSIC_TEST_DATABASE_URL="postgres://${ROOMUSIC_TEST_PG_USER}:${ROOMUSIC_TEST_PG_PASSWORD}@127.0.0.1:${ROOMUSIC_TEST_PG_PORT}/${ROOMUSIC_TEST_PG_DATABASE}?sslmode=disable"
COMPOSE=(docker compose --env-file /dev/null -f "$ROOT/compose.test.yaml" -p roomusic-test)
# 只在确认自己创建后cleanup；端口竞争或up失败必须核对，不接管已有项目。
created=0
cleanup() { if [ "$created" = 1 ]; then "${COMPOSE[@]}" down -v --remove-orphans; fi; }
trap cleanup EXIT INT TERM
created=1
"${COMPOSE[@]}" up -d --wait postgres
```

公共 `roomusic-test` 名称不能同时跑两套验证；空闲端口检查不等于项目名互斥。实际脚本需在检查/创建到cleanup全程持有约定的本机锁（如`flock`），防两个agent都在空项目时通过检查。连接只使用上面显式URL，不继承`.env`或现有`ROOMUSIC_DATABASE_URL`。

局部验证命令（新包/用例在实施后才存在；后端集成需显式URL并断言0 SKIP）：

```bash
cd /home/qlqq/workspace/ROOMusic/backend
go test -race ./internal/catalog/textnorm -count=1
go test ./internal/app -run '^TestPackageDependencyDirections$|^TestRoutesResolveToRegisteredPatterns$' -count=1
go test ./internal/catalog/... ./internal/operations/... ./internal/library/... ./internal/app \
  -run 'TestPostgreSQL.*(Search|Artist|Effective|Correction|Claim)|Test.*(Search|Artist|Normalization|Pinyin|CatalogBackfill|ProjectionPublish)' -count=1
```

实施者应按实际新增测试名收窄/补全regex，特别是existing扫描/claim/权限回归不能因名字未命中而假通过。EXPLAIN用§4.5同一生产SQL构造器与固定种子生成器，在隔离URL运行，输出计划到任务研究目录；不要手抄简化SQL测完就报告生产请求通过。

前端只跑新增/受影响的Vitest文件，示例（文件尚待创建）：

```bash
cd /home/qlqq/workspace/ROOMusic/frontend
npm run typecheck
npm run test -- src/features/search src/features/artists src/features/governance/api/decoders.test.ts
npx playwright test e2e/search.spec.ts e2e/artists.spec.ts
```

真实服务按 `scripts/playback-e2e.sh` 既有隔离模式启动合成音频库，只选择search/artists及必要收藏联调用例；如runner当前没有grep透传，先在获批实施中添加显式用例选择，不能为了方便运行全部实播。启动Playwright的同一shell按上层AGENTS加载 `/home/qlqq/.local/share/playwright-deps/env.sh`。截图至少1440与375，检查五宽度/axe/键盘，不修改界面样式来掩盖DTO错误。

本次仅文档交付：做路径/Markdown/JSON示意与范围核对；**未运行Go测试、前端测试、SQL/EXPLAIN、compose或真实服务**。不跑全量测试的理由是没有任何代码修改，性能与新接口尚未实现；后续报告必须列出实际执行而不是计划执行的命令。

## 15. 已裁决规则、现状差异与实施风险

### 15.1 PRD/现状差异及确定处理

1. **交付栏位**：④交付**专辑+曲目**两栏，⑤再加艺人栏；不把④描述成艺人/专辑两栏，available_types与前端入口同步。
2. **历史标签证据**：Artist单值有观察，四角色credit原文和重复ARTIST值不完整。后台回填不伪造原文；第一类候选仅对新版扫描证据生效，既有曲目须在⑤上线后成功重扫。新发现曲目由同一新版扫描写证据，界面无需解释历史数据边界，也不要求用户专门重扫。
3. **旧规则覆盖**：后端采用规范化同键自动归一，人工拆分覆盖优先；前端仍不得按字符串自行猜身份。实施时必须同步`frontend/DESIGN-components.md`、`.trellis/spec/backend/library-scan-contracts.md`与ADR 0002/0003的`*-decisions.md`，具体条目见§13.2。本轮不改这些文件，不同时承诺相反的旧新规则。
4. **原文与主名**：署名/Match显示来源写法，艺人实体标题显示最常见写法或管理员主名覆盖。旧tracks.artist仅在可信原观察存在时恢复原写法；无原文的旧credit保持现值，未来正常重扫更新。这是D5接受的显示变化，不是修改源文件标签。
5. **短词性能与计数**：不设最短长度，单字“爱”可搜。新全局建议与结果首屏：两个字符及以上p95≤300 ms，单字符p95≤1 s；按§4.5测量。结果按分类去重后≤1,000返回精确total，超过则`total=1000,total_relation="gte"`显示“1,000+”，用`has_more`继续翻页，不截掉候选；旧目录接口保留精确total兼容。pg_trgm短词退化与热门词排序仍有风险，本轮无EXPLAIN，目标不是达标声明。
6. **候选不是自动合并依据**：同轨同角色可能是共同创作者，专辑艺人与曲目艺人的1:1也可能是不同的人；仍只提示。按具体名字键对忽略，相同对重扫不再提示，新对可再提醒。
7. **首字母局限**：汉字含日文汉字按固定表的汉语读音首字母分组，多音字取固定首音，不承诺正确猜姓氏；名字排序分组，专辑数排序不分组，不扩成完整拼音排序或拼音搜索。
8. **反向恢复与收藏**：合并保留旧ID及恢复依据，拆开可恢复旧身份；收藏保留原锚点，当前canonical去重展示，恢复旧ID后自然恢复。永久改写原收藏ID或拆开只能新建UUID都不满足D8/R4。
9. **D2修订的在线升级**：撤销原先回填完才ready的决定。正常启动立即ready，后台单worker分批续跑；未完成时旧ILIKE检索，扫描/M3同事务写新投影，完成后原子发布。艺人整理只影响艺人页/链接及治理，不阻断现有目录与播放。读取503 `artist_catalog_preparing`、新治理409 `artist_catalog_not_ready`，就绪后恢复能力。

### 15.2 已裁决

人工选择路线；选择者为通过主会话反馈裁决的用户；裁决日期为2026-11-04。D1–D8均已接受；同日主会话补充修订D2，以下以**在线后台回填**为唯一实施基线，原停机等待方案已撤销。

| 编号 | 裁决项 | 已确定合同 |
| --- | --- | --- |
| D1 | 去变音符范围、OpenCC一对多算法 | 只去拉丁附加符，保留假名浊点/半浊点；TS第一候选+无歧义ST补充，不做闭包。§3.1固定が≠か、ば/ぱ/は与炼/链等反例 |
| D2（修订） | 升级期间服务可用 | 启动立即ready；单worker后台分批回填、续跑及每批进度日志；构建中旧ILIKE回退，扫描/M3同事务写目标新规则，版本/revision防覆盖，完成后原子切换。艺人未发布读503 artist_catalog_preparing、治理新写409 artist_catalog_not_ready、refs先空/纯文字后链接；播放照常。十万轨联合后台墙钟约16–36分钟仅为未实测负载预算，不是停机时间 |
| D3 | 旧两路由带q的排序 | 新路径发布后，`/releases`和`/release-groups`有q时也按完全相同>开头相同>包含排序；无q原排序不变。构建期间按D2修订暂走旧匹配/旧排序 |
| D4 | 按专辑拆开的归属范围 | 保存选择时的发行快照，这些发行以后新增曲目跟随；既有track覆盖随ID保留，不自动包含以后入组版本，不使用动态group规则 |
| D5 | 最常见写法、旧原文与改名 | present有效署名按subject/name去重计票，管理员主名覆盖优先；旧tracks.artist有可信原观察才恢复原写法；改名不抢他人名字键、不暗中改变身份归属 |
| D6 | 候选及忽略粒度 | 第一信号只取新版原始重复标签证据，排除明确多人分隔；既有曲目重扫后才参与且UI无需解释。忽略按具体名字键对持久，新名字键对可以再次提醒 |
| D7 | 名字分组与大队列 | 按名字排序分首字母组、按专辑数排序不分组；播放全部最多10,000，随机先全体洗牌再取前10,000，超出一句提示；不改全局播放器模式 |
| D8 | 合并/拆开后的艺人收藏 | 保留原艺人锚点，canonical去重后展示/分页，取消本人等价锚点，恢复旧ID后恢复；解析缓存由收藏owner在治理同事务刷新。§11列通用表/独立艺人表两种最小接受条件，主会话统一另一线的物理表与端口 |

### 15.2a 实施中的主会话裁决（2026-11-04，⑤ 前端步骤 6 提出）

按“用起来方便、人性化”的标准裁决，不重开 D1–D8：

| 编号 | 问题 | 裁决 | 合同影响 |
| --- | --- | --- | --- |
| I1 | 拆开时“拆给谁”的默认 | 拆开多半是在纠正合并错误。拆开对话框最前面列出“之前并入这里的艺人”，每位一个一键选项“拆回《X》”：选中后自动勾上那次合并带过来、且现在仍归本人的名字键，目标默认为恢复这位旧身份（收藏和旧链接随之回来）。手动选名字时，若所选名字键全部来自同一位已并入的艺人，默认恢复这位，否则默认新艺人；管理员都可以改 | 新增管理员只读 `GET /artists/{id}/merged-sources` → `{items:[{artist_id,name,revision,name_key_ids,operation_id,merged_at}]}`：只列目前重定向到本人、可按 §7.3 恢复的旧身份；`name_key_ids` 只含那次合并移入、现在仍归本人的键；没有时为空数组。前端不从操作记录推断 |
| I2 | 按专辑拆开能选哪些专辑 | 列出这位艺人以**任何身份**参与的专辑（专辑艺人、作曲、演奏等），每项标出身份，分页并可按名称筛选 | `GET /artists/{id}/albums` 增加可选 `participation=any`（默认保持现状，只列专辑艺人）；`any` 时每项增加 `roles:string[]`（七项角色词表，非空），可选 `q` 按专辑名筛选，分页上限同现有 |
| I3 | 整理中的提示 | 确认现做法：艺人列表、艺人页、收藏“艺人”栏和候选页整页说一句“艺人信息正在整理，请稍后再试。”；各处艺人名只是不成链接，不加任何说明 | 无 |
| I4 | 已在队列里的曲目艺人链接 | 按 §12.2 本轮实现：艺人数据变为就绪、或艺人治理成功后，有界刷新当前曲目与队列条目的展示元数据（只改元数据，不改顺序、当前位置和播放状态） | 播放引擎增加只改元数据的方法。批量曲目摘要原来只有 Go 内部函数、没有 HTTP 接口，补一个：成员可读的 `POST /tracks/summaries`，请求 `{track_ids:[UUID...]}`（去重后 1–1,000 个，超出 `400 invalid_input`），同源校验同其他 POST 读接口，响应 `{items:[TrackSummary]}`，按首次输入顺序、未知 ID 省略、缺失曲目照常返回并标缺失；`TrackSummary` 复用 catalog 现有安全合同（含 ⑤ 的 `artist_refs`）。前端不按发行逐个读详情 |
| I5 | `artists_state=unavailable` 时的“艺人”入口 | 隐藏；`preparing` 与 `ready` 时显示。收藏页“艺人”一栏同样处理 | 无 |
| I6 | `release_artist` 与 `album_artist` 在界面上分成“艺人”“专辑艺人”两个角色 | 对用户这是同一回事（专辑这一级的艺人），界面统一叫“专辑艺人”：角色摘要里两者去重成一个，角色筛选只有一个“专辑艺人”选项 | 艺人列表的 `role=album_artist` 筛选改为匹配发行级的 `release_artist` 或 `album_artist` 任一；`release_artist` 仍是合法取值（只匹配它本身），DTO 的 `roles` 词表不变，去重在前端显示层做 |
| I7 | 艺人名外面用书名号《》 | 书名号只用于作品名。艺人名用中文引号“”或不加标点，例如“拆回 Teresa Teng”“恢复为“Teresa Teng””按语句通顺选择 | 无 |

D2修订的在线回填/旧路径/原子发布/整理状态，以及短词时延、1,000计数阈值与`total_relation/has_more`、旧证据的UI静默处理、④/⑤栏位及实施文档同步均已纳入正文和§14验收。收藏表名/端口的跨线装配由主会话按§11执行，不重新裁决收藏行为。

### 15.2b 回填性能继续实施授权（2026-11-04）

在原M3 503修复后，主会话按§3.3提出当时的性能验收口径（其中max≤1秒并非用户要求，现由下文I8取代）：完整联合回填以100000 present＋10000 missing的原规模夹具、同时有播放/搜索/扫描/M3负载验收，墙钟**不超过36分钟**；已提交应用批的CATP持有p95目标约**200ms以内**、最大**不超过1秒**。先采有效署名INSERT及其他高耗时语句的EXPLAIN(ANALYZE, BUFFERS)，再据证据优化。45分钟只是旧benchmark总预算，可延长总执行context，但不可借此放宽36分钟回填目标或拉长M3请求超时。保持单worker、批间释放、原锁序，不跳对象、不提前发布、不用planner开关或业务ANALYZE。回填通过后仍须完成原5次预热/30次测量、搜索300/1000ms、完整规模计划及10000队列；任何验收项未达标停下报告实测。

先交付I1/I2/I4/I6真实服务接口与样本（A），回报后再实施上述性能部分（B）。原始大证据仓外，隔离项目仍为roomusic-test-m5s，不提交、不碰前端。

**2026-11-05 主会话修订（I8）**：上段“用户明确要求”实为主会话按设计 §3.3 定的验收口径，以此修订为准。
- 单批 CATP 持有的门槛改为 **p95 ≤ 200 ms、p99 ≤ 500 ms**，按整轮回填的完整分布判定；单批最大值照实记录并归因，不再作为中途停止条件。理由：门槛保护的是前台响应，用户能感到的是回填期间 M3、扫描、播放与搜索的实际等待，单次存储 fsync 抖动（WAL 刷盘）不能代表持锁设计。
- 前台指标同时验收：回填期间 M3 修正全部成功且 p95 ≤ 1 s；扫描全部成功；播放与搜索请求不出现 503。
- 允许回填批事务使用 `SET LOCAL synchronous_commit = off`，条件：该事务只写可由回填重建的派生投影及其自身进度标记（同一事务原子提交，崩溃后最多重做最近几批），不写治理意图、个人数据或扫描事实；M3、扫描、用户写入一律不得使用。须有集成测试证明只在回填批事务内生效、提交后连接恢复默认，并写入 `database-guidelines.md`。
- 正式运行不再因单批最大值中途停止，跑完全部验收项后统一判定；联合回填 ≤ 36 分钟仍是硬门槛。

**2026-11-05 用户裁决（I9，取代上文全部性能门槛）**：后台回填只需在后台慢慢完成，不追求速度。以本轮正式运行（2026-11-05 02:51 起）为最终测量，跑完即标记后端性能达标；36 分钟、CATP p95/p99 及前台各项数字只照实记录，不再作为门槛，也不再为此优化或重跑。正确性要求不变：不跳对象、不提前发布、ready 与播放持续可用。

**2026-11-05 用户裁决（I10）**：I9 的正式运行发现前台热门艺人搜索过慢（五万轨共享艺人“周杰伦/周杰倫”：建议 p95 约 3.7 s，三栏首屏约 21 s）。前台搜索是用户直接等待的，用户同意修复：只改相关查询与索引，按热门词定向测量；回填已按 I9 达标，不再测。门槛：热门艺人建议与三栏首屏 p95 ≤ 1 s；其余项（单字、深页、10,000 首队列、剩余计划）只照实记录。

### 15.3 规划摘要与实施边界

- **目标**：不引入外部搜索服务、不写源音乐，让不同写法命中同一音乐，让署名成为可浏览、可治理、可收藏的稳定身份；单字也能搜索，个人意图不因合并/拆开丢失。
- **范围内**：④专辑/曲目两栏、规范化与搜索；⑤艺人第三栏、身份/别名/治理/候选/浏览/队列/收藏；现有有效值及播放器合同贯通。
- **范围外**：外部人物资料、模糊自动合并、拼音/罗马字搜索、搜索历史、源标签写回、通用撤销/审批、新Go依赖、用户实例验证。
- **验收**：S1–S5与R1–R7逐项映射见§14；十万轨新搜索≥2字符300 ms/单字符1 s及1,000计数阈值；重扫不丢人工身份/别名意图；随机全体采样后限10,000、旧模式不变；合并/拆开后的本人收藏连续性；后台回填中断/扫描/M3交错不覆盖新值、ready和播放持续可用、整理状态/链接切换正确；真实服务/样本/键盘/axe/截图。
- **关键技术选择**：纯规范化叶子包、固定内置字表/许可、同事务旧事实/新投影双写、有进度日志与revision防覆盖的后台版本回填、按代次原子发布及艺人整理状态、扫描事实与治理覆盖分离、发行快照覆盖和可恢复旧艺人ID、R4不可Revert的新Kind。
- **实施风险/验证项**：时延与在线回填耗时尚未实测，§3.3仅是后台预算估算，需联测播放/扫描/M3争用与中途重启；旧credit原文不可无损恢复，多音字采用固定首音；收藏物理表与端口由主会话按§11统一，不能跳过同事务/去重后分页验证。这些不再是未决产品选择。
- **产物状态**：本轮仅修订本design；未改两个PRD、implement.md、迁移、代码、spec/ADR；未启动任务、未提交。主会话按§13.2准备其他产物并取得单独实施授权。
- **最终选择记录**：路线为人工选择，选择者为用户（经主会话传达）；结果为接受D1–D8及补充性能/历史证据裁决，并以本轮D2在线回填修订覆盖原阻塞启动方案；理由为用户使用方便、人性化，并保护身份与个人意图连续性。本轮保持planning，仅落盘已裁决设计，不把产品裁决等同于实施许可。

## 16. 外部数据与技术依据（本次只读核对）

以下是复现所需的官方上游定位；不是要求运行期联网：

```text
OpenCC固定提交：
https://github.com/BYVoid/OpenCC/tree/556ed22496d650bd0b13b6c163be9814637970ae
其 data/dictionary/STCharacters.txt、TSCharacters.txt 与 LICENSE

Unicode固定数据：
https://www.unicode.org/Public/17.0.0/ucd/Unihan.zip
https://www.unicode.org/Public/17.0.0/ucd/ReadMe.txt
属性说明：https://www.unicode.org/reports/tr38/ （kMandarin）
许可：https://www.unicode.org/license.txt
本次读取许可SHA-256：e7a93b009565cfce55919a381437ac4db883e9da2126fa28b91d12732bc53d96

PostgreSQL 18官方pg_trgm文档：
https://www.postgresql.org/docs/18/pgtrgm.html
```

版本/哈希基线来自本次读取原文件，不代表已经把这些数据加入仓库。上游许可原文在实施时完整内置；本文件不替代许可证。`pg_trgm`文档支持GIN的LIKE检索，也明确没有可提取trigram的pattern可能退化为全索引扫描；这正是§4必须单测短词/标点的依据。
