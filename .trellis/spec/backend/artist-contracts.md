# 艺人身份、浏览与别名合同（M5 ⑤）

## 1. 范围与权威

触及人物署名、名字归一、别名、主显示名、拆分作用域、艺人引用或队列时读取。产品裁决为 S 线设计 §5–8、§10–11、§13–15；本任务仍须按其测试与性能门禁验收，本文不是通过声明。

`catalog/internal/artistgraph` 拥有事实解析与派生，`catalog/governance` 拥有四张人工意图表，`personal` 拥有收藏。前端只消费明确ID，不按同名猜人；后端仅按版本化 `textnorm.Text` 同键默认归一，作用域人工拆分优先，不使用相似度。

## 2. 存储与端口

- `0026_artist_catalog.sql`：artists、artist_name_keys/names、artist_credits/effective_credits、artist_profiles/search_names、绑定/作用域/名字覆盖、原始标签证据、候选/忽略、发行完成标记及artist operation targets。三种原别名改为固定种子路由，不制造治理记录；`0027_artist_favorites.sql` 属于 personal。`0028_artist_profile_backfill.sql` 为profiles增加applied_generation，并允许清单的artist subject；不在迁移回算投影。`0029_artist_backfill_indexes.sql`增加`artist_effective_credits(artist_id,name_id,track_id,release_id)`覆盖索引；只改访问路径，不改Version/ready或事实内容。
- 姓名键 SHA-256+版本唯一，命中哈希后必须核对完整键；原写法同键同哈希去重，同样校验全文。单姓名4096字节，规范化键65536字节。艺人身份不物理删除，显示名不唯一。
- `artistgraph.Refresh(ctx, tx, releaseIDs)`：持CATP后从当前事实刷新scan/correction署名、有效关联、主名、搜索名字、候选及本代发行完成标记。`RefreshProfiles(ctx,tx,artistIDs)` 仅覆盖显式传入的保留身份；空集合不操作。在线Refresh仅取变更前后实际关联身份及本次facts新建身份，禁止UNION全库缺profile身份。被M3覆盖而未进入effective的新scan身份也须生成安全空profile、搜索名字与本代marker，不能依赖ready后不再运行的回填补齐。profile、搜索名字与applied_generation在同事务更新；二者不写人工意图/操作记录，不另开事务。
- `artistgraph.Ready(ctx,q)` 在当前读取快照检查状态/代次/规则版本。`Mappings(ctx,q)` 提供原锚点→一跳canonical映射。
- 公开 `catalog.AcquireArtistWriteGate(ctx,tx)` 只取CATP；`NewStore(tx).ResolveArtists(ctx,ids) (map[string]string,error)` 先检查ready，连空ids也不跳过；未知ID无键。`LoadArtistSummaries` 返回canonical安全摘要，单批最多1000；都不读收藏表。
- app 将 `personal.RefreshArtistResolution(ctx,tx,mapping)` 注入治理与 `maintenance.RunOptions.RefreshArtistResolutions`，失败回滚艺人变化/记录或阻止发布。

## 3. 事实、解析、后台与读取

### 解析与主名

事实按稳定release/track UUID和名字记录存储；不把人工覆盖绑到重扫会替换的旧credit行。解析顺序：track+key → release+key → 全局binding → initial_artist，再解析redirect。M3专辑艺人修正同时覆盖发行的release_artist/album_artist；清除恢复scan层，其它角色不受影响。

有效present署名每个subject/name一票，跨角色去重；并列原文C排序及name ID。无票取当前归属名字稳定首项，最后initial_name。人工display_name始终优先。改名不抢名字键，纯人工旧主名不自动成为永久别名；合并保留source主名覆盖，搜索投影可经 `source_artist_id` 读取被重定向身份的主名，不把该名字路由抢到target。

原始重复标签保存在 `artist_tag_evidence`，每轨最多100项、每项4096字节，超限失败诊断，不截掉末项伪造完整集合。只有新版原始证据能生成同轨同角色候选，明确多人分隔不是该信号。另一信号要求发行全部present轨真实单一同键Artist与有效单一AlbumArtist一一对应；不跨发行补齐。

### 在线构建

全库身份派生由独立artist_profiles清单阶段有界补齐（含孤立种子、空署名、旧代次），不得借在线扫描/M3或发布事务补全库。旧进行中build即使manifest已封闭仍补登记新阶段；当前代次已由在线写者完成的跳过，发布前只检查缺口，缺口另开修复清单。0028不改变算法内容，既有完整ready不因新增marker触发全量重建；下一轮构建按新目标代次补齐。

启动仅登记目标，不等待整理；单maintenance worker持专用CATM选主锁，先搜索后艺人，短批CATP下重读/跳过已由实时写者完成的代次。固定发现清单加发布前缺口修复，不依赖UUID高水位遗漏新对象；批次提交即保留断点，每批释放gate并记录进度。发布前刷新收藏解析、验证完整性后原子改变状态与epoch。启动、播放、旧目录、group/track收藏及歌单继续可用。

写锁顺序：OPER幂等/必要旧operation锁 → CATP → 发行UUID序 → 组UUID序 → 艺人/名字/派生 → 本人收藏state。扫描终态先CATP再scan_run；不在gate内反向拿旧operation锁。扫描只写事实/派生，不能写bindings、assignment_overrides、overrides、candidate_ignores或edit_revision。

### 有界回填的实现与验证约束

有效署名按本批发行级与轨级两条互斥入口读取，不能跨表OR后才过滤全库。名字只在本次原文集合按256项预取，未命中仍走原ensure创建；哈希只定位、完整键与原文不等必须失败并回滚，不能缓存全库或截断第257项。主名计票可用全部轨subject/name去重票减非present票，但必须丢弃零票，与原present计票等价；作用域别名沿覆盖索引按name_id严格递增枚举到耗尽，不设名字候选上限。主名与搜索名字共用完整归属读取，同事务批量写profile/搜索/代次。

串行prepared计划也需取证，不能只用新连接的custom计划证明热门性能。主名按本批身份轨级署名作4097行策略探测：不超过4096沿稀疏关联，超过时先MATERIALIZED非present轨集合避免generic逐署名探测tracks；探测只选择等价SQL，不限制完整计票、别名或输出。4097是符合条件行的消费界限，不是物理扫描上限；密集分支可能读取全tracks过滤非present，成本必须计入CATP，不能称无全库读取。不得用planner/JIT开关强制计划。密集轨票按身份LATERAL完整聚合，避免低可见页时将整批热门署名合并外部排序；入口必须先按数据库uuid类型DISTINCT，不能用文本去重替代旧ANY的集合语义。回归用独立热门身份触发dense，再以同一目标的发行票与轨票竞争证明混合大小写ID不重复乘轨票；仅对纯轨名字比较不足以抓住同比翻倍。扫描显示署名仅在实际变化时按128项批量更新，再经searchindex.RefreshTracks同事务刷新；未变化轨不得推进搜索revision。

`artistgraph.ArtistInputNameCost(raw string) int`提供原始输入的保守名字成本（拆分数量至少1），仅供调度；maintenance不得直接导入metadata或复制拆分规则。发行批在CATP内读当前marker后，按待办UUID连续前缀估计本批输入：起步10轨/64名字，最多50轨/256名字，并与发行数自适应并用；missing轨计入，按当前发行/轨索引入口查询，不依赖未生成effective。已完成项不消耗刷新预算；首个超预算subject仍完整执行，不能饿死或标假完成。估计提前结束必须标不完整，不能用估计代替真实graph/marker；估计或刷新失败整批回滚。profile阶段独立，批间释放gate后与搜索worker共用自适应让出：正的整批实测耗时小于200ms时取十分之一、最低10ms；无有效耗时或至少200ms时保守50ms。耗时含取锁等待和COMMIT，长批/争用因此仍多让出；context可中止等待。日志yield_ms是计划值（yield_status=planned），不是实测等待或CATP持有。该调度不是保证200ms的数学上限，超大完整subject及COMMIT尖峰必须计入整轮分布和最大值归因，按I8统一判定而非单次尖峰中断。

必需回归：跨256项预取/新名字/全文哈希不一致/回滚；超过256个作用域别名不截断；全missing零票回退、跨角色一票、人工主名/seed/redirect/被M3遮盖的新身份；真实输入估计/连续前缀/超大missing主体完整/零署名续跑/估计SQL失败不提交进度。性能证据必须区分诊断集合预置与空投影生产回填，同SQL有无索引对照、索引大小和写放大不得隐去。

### REST

所有GET先鉴权与参数校验。`GET /artists`：q≤200字节，initial=A–Z/kana/#，role七项白名单，sort=name/album_count，分页1/50、最大100。只列有present参与且未重定向的身份。`role=album_artist`匹配发行级release_artist或album_artist任一；`role=release_artist`仍只匹配本身，DTO七项roles词表不变。名字按首字母分组/C规范化键/ID；专辑数排序不分组。首字母采用固定Unicode17 Unihan kMandarin首音表，不扩为拼音搜索。

`ArtistSummary={id,name,initial_group,album_count,track_count,roles,available}`，数字为全角色去重可见group/present轨数，role筛选只筛身份。发行级署名的曲目参与取其全部轨。

- `GET /artists/{id}`：artist/revision/name_source/names/names_pagination/role_counts/redirect_artist_id；首批20名字；旧重定向ID仍200，保留安全摘要，子路由解析canonical；空身份available=false而不是404。
- `/names`：`{id,key_id,name,is_display_name,origin}` 分页，按key拆分不可只拆繁体写法。
- `/albums`：默认只有有效album_artist关系，按group去重，保留credited_release_id，代表仍按全组规则。`participation=any`改为所有角色的present参与组，追加非空七项词表`roles`（默认省略）；`q`在两种模式下都按任一可见成员的有效专辑标题作ILIKE字面包含，转义%/_/~，不改变代表/credited版本及排序。分页仍默认50、最大100。空/未知/重复participation为400 `invalid_artist_participation`，非法/重复q为400 `invalid_query`。
- `/tracks`：track_artist/composer/conductor/performer/producer必填角色，含missing；发行标题、发行ID、碟/轨序、轨ID稳定排序。
- `/queue`：order=album/random，random要求UUID seed；所有角色轨并集去重，present优先筛选，随机 `md5(seed||':'||track_id)` 全体排序后LIMIT10000，不能先限再随机。完整摘要同快照每1000一批，精确total/missing_total/returned、limit/truncated，格式支持由前端判断。

`ArtistRef={artist_id,name,display_name}`，name为原署名、display_name为有效主名。ReleaseSummary加artist_refs/album_artist_refs，ReleaseTrack/TrackSummary及每条credit加artist_refs，数组必传、最多100。所有共享摘要须在同一SQL/只读快照里判断ready并读取refs；未就绪返回[]，不读半图。catalog延迟敏感只读入口复用readWithLocalJITOff，事务后恢复的集成测试不可省。

## 4. 治理与错误矩阵

四Kind：artist_merge、artist_split、artist_rename、artist_candidate_ignore；全部接operations.Run、R4不可Revert，target_type=artist。双方revision、幂等指纹与scope basis须在CATP后重检；100000轨预算与预览计数均取发行级署名展开全部轨、轨级署名的去重并集（含missing），不能只数非空track_id；重放已有成功回执先于准备态判断。按专辑拆分保存发行快照+既有track覆盖，未来同发行新轨跟随，新入组版本不跟随；restore只恢复当前redirect到本人的旧ID。

`operations_artist_targets` 保存主/次艺人目标，GET operations新增artist_id，与release/group取并集再分页。记录显式artist nullable，坏state只使changes=null，不能拖垮整页；旧类型撤销不变。

| 情况 | HTTP/code |
| --- | --- |
| 未登录/普通成员治理 | 401 unauthorized / 403 forbidden |
| 已登录艺人读取、候选、split-preview未ready | 503 artist_catalog_preparing，Retry-After:3 |
| 新治理及艺人收藏写未ready | 409 artist_catalog_not_ready |
| 新显示名空/控制字符/超限 | 400 invalid_artist_name |
| 同ID合并/重定向ID提交 | 409 same_artist / artist_redirected |
| revision / 幂等异义 / scope依据变动 | 409 revision_conflict / idempotency_conflict / artist_scope_changed |
| 超100000轨、1000名字键、10000显式覆盖 | 409 artist_scope_too_large，整个事务回滚 |
| 无变化/独立撤销 | 400 no_changes / 409 not_revertible |
| 真实依赖失败 | 安全503 database_unavailable，不混淆整理专码 |

## 5. 基准与反例

- 周杰伦/周杰倫同规范化键；Jay Chou是明确种子别名，管理员拆开后扫描不重种。
- 同名两人按专辑拆开，已选轨移至另一发行仍按track覆盖；以后新入组版本不自动跟随。
- 缺失/不可用艺人保留身份和本人收藏；恢复旧ID后原收藏重新分开。
- 真实标签叫“未知艺术家”是署名；只有inferred或旧证据不足的占位不建人，不能字符串全局黑名单。

## 6. 必需验证

治理幂等/双方revision/冲突/全事务回滚/不可撤销；重扫前后四意图表与revision快照；按专辑作用域、移动、未来新轨/新版本；候选仅两信号、忽略名字键对长期有效；整理期间503/409且播放旧能力可用；回填中断重启、实时写先后交错与原子发布；ready后M3遮盖的新scan身份仍有空profile/搜索名字/本代marker且不捎带无关身份；合并双方收藏去重/取消/恢复旧ID；真实handler样本；队列10001先全量随机再限10000。

搜索验收规模与门槛严格按S设计§4.5：100000present+10000missing、10000发行/约8000组、至少20000艺人、2艺人/4credit、20%别名、50000轨倾斜。预热5/测30，≥2字符p95≤300ms、单字符≤1000ms；任一未达标必须回报实测，不减规模或阈值。原始证据仓外，不把代码存在等同验收通过。2026-11-05主会话I8修订：联合回填≤36分钟；完整已提交应用批CATP分布p95≤200ms/p99≤500ms，最大值照实记录归因、不作中途停止条件。回填期M3全部成功且p95≤1s、扫描全部成功、播放/搜索不出现503；正式运行完成各项测量再统一判定。旧max≤1s是主会话口径而非用户要求，详见S设计§15.2b末I8。

## 7. 错误与正确

错误：单曲扫描捎带两万孤立profile，使在线写者持CATP阻塞M3与worker；先分页收藏再去重；改名把同键别人归属抢来；后台读旧值后无锁覆盖；先成功提交治理再异步刷新收藏。

正确：在线只刷新旧/新有效关联及本次facts新建身份，历史孤立身份由独立有界后台清单处理；personal聚合resolved_artist_id/MAX(created_at)后精确分页；显示名仅覆盖标题；CATP内重读当前事实；同事务调用收藏owner端口并一起提交或一起回滚。


## 8. 实施裁决 I1 / I4：管理员恢复来源与成员批量摘要

依据S设计§15.2a。`GET /artists/{id}/merged-sources`仅管理员可读，返回`{items:[{artist_id,name,revision,name_key_ids,operation_id,merged_at}]}`。只列当前直接重定向到本人、按§7.3可恢复的旧身份；从明确merge持久快照追踪该身份最近一次主动合并，不解析changes显示文本。链式合并回溯属于该身份的旧redirect operation；恢复后再次合并只用新事件。名字键为那次移入的binding/assignment键且当前全局canonical仍为本人，空时必须`[]`；不借历史scope抢回第三人的键。读不写意图、revision或操作记录。HTTP未登录401、普通成员403，参数/未知ID按艺人读取约定，未ready为503 `artist_catalog_preparing`。

治理只读入口`governance.ReadArtistMergedSources(ctx, database catalog.ReadTransactionBeginner, artistID)`经`catalog.ReadWithLocalJITOff`公开适配复用同一私有只读RR helper；事务内off/提交后恢复和失败回滚仍须测试，不复制另一套JIT策略。

`POST /tracks/summaries`是成员可读的POST查询，强制同源；`{track_ids:[UUID...]}`逐项校验、规范小写后按首次出现去重，1–1000个。空/null/无效UUID/超限为400 `invalid_input`，无Origin/跨源403、有效同源未登录401。响应`{items:[TrackSummary]}`按首次输入顺序，未知省略、missing仍返回`present=false`。复用`LoadTrackSummaries`安全字段及artist_refs；艺人preparing时refs为[]而不是整请求503。坏目录投影或真实依赖失败安全503，不暴露SQL/路径。详情见catalog-summary-contracts。

必需断言：真实merge→chain→restore→remerge历史选择及空键；普通成员不能读取恢复来源；any专辑组跨角色去重、q仅专辑名且字面转义、默认响应不新增roles；album_artist角色筛选并集与release_artist精确；1000/1001、重复UUID/顺序/unknown/missing、same-origin、整理期refs；真实handler样本的独立`artists/adjudication`所有权，不能被旧recorder删除。

反例：前端按操作记录文本猜可恢复者、把q命中曲目当专辑名、先LIMIT再去重、逐发行读队列摘要。正确：后端明确恢复来源、同快照按组筛选/计数/分页、单批安全摘要与首次输入顺序。
