# 整理链 C 实施与验证（2026-11-25）

`MatchRuleVersion` 已从 101 升为 102。版本通常用一次搜索和一次详情。同名跨组消歧只读取筛选后最多三个版本。后面的表格区分实际请求和请求上限。[审阅后的设计](design.md) · [真实查询记录](research/mb-query-probes-2026-11-25.md)

## 文件与实现

下表的目录相对于仓库根目录。共涉及 43 个 Go 文件、2 个迁移文件。本文、设计与实测记录均在本任务目录。

| 目录 | 文件 | 实现 |
| --- | --- | --- |
| `backend/internal/sources/musicbrainz/` | `artist_links.go`、`client.go`、`disc.go`、`entity.go`、`finders.go`、`search_page.go` | 合并条码、目录号、标题署名搜索，合并 ISRC 搜索；整理按 100 条请求，保留实际总数、偏移、返回行数和各碟曲数。碟片精确查询直接读取曲目。艺人查找用无字段短语，别名和外链共用缓存。 |
| `backend/internal/app/`、`backend/internal/curation/` | 应用的 `app.go`、`curation_musicbrainz.go`；整理的 `sources.go`、`source_finders.go`、`search_pages.go` | 由应用接入已有 MB 客户端，分页请求沿用同一后台来源授权。模型与联网可用性读取复用开启者的设置。 |
| `backend/internal/catalog/` | `curation_artist_credits.go`、`curation_evidence.go`、`ingest/works.go`、`metadata/genre.go` | 为识别投影艺人编号、署名角色、作品、合辑、组对应；整理复用入库的古典流派判别。 |
| `backend/internal/curation/program/` | `match.go`、`match_album.go`、`match_lookup.go`、`match_search.go`、`match_single.go`、`match_snapshot.go`、`fingerprint.go` | 依次核对标识、组唯一、结构吻合及待定。文字与曲目同时否决错误标签编号。按实际查询完整性、真实性和各碟曲数判断；同组多版通过时只建组对应。散放单曲可写录音或共同艺人。候选快照保存页、曲数、已读详情、别名与逐项冲突。 |
| 同上 | `artist_links.go`、`enrich.go`、`enrich_media.go`、`track_fields.go` | 组级结果用于组封面、归组和经结构核实的缺曲名。艺人逐位比较署名与登记别名，有角色反证时排除错位。其余同位异名产生供 E 判断的提案。 |
| 同上 | `pass.go`、`release_digests.go`、`release_route.go`、`release_state.go`、`unit.go`、`worker.go` | 在线和离线结论共用 `applyMatch`、`unit.write`、`afterMatch`、`saveConclusion` 与待定项收尾；升级先按旧摘要核实已有状态。未知总数不能证明唯一，实际截断或零候选才登记补查。规则升级的离线取证不请求外部来源。 |
| `backend/internal/curation/` | `external_link.go`、`external_link_revert.go`、`join_group.go`、`suppressions.go`、`artist_credit_proposals.go` | 按本发行的受治理操作检查组对应、成员、相容性、撤销和抑制。发行并入仍要求发行版本及录音证据。E 可用 `OpenArtistCreditProposals` 按当前摘要与有效来源操作读取。 |
| 同上 | `pending_items.go`、`work_states.go`、`progress.go`、`unlisted.go`、`release_identity.go`、`httpapi/handlers.go` | 路由为 select、lookup、web；后两者写 waiting_route。已抑制或占用的候选从模型选项中排除。未收录满 90 天仅作一次程序重查，管理员也可用 `POST /api/v1/admin/curation/releases/{id}/recheck` 登记；临时服务错误沿用退避。E 用 `FinishUnlisted` 在同一事务结清领取项并登记到期日。 |
| `backend/migrations/` | `0087_curation_match_rules.sql`、`0087_curation_match_rules.down.sql` | 增加外部对应类型 release_group_match、工作状态 unlisted 和 no_usable_candidates、未收录到期索引、待定路由列和 waiting_route 状态；新建 `curation_artist_credit_proposals` 及发行/状态索引。down 删除新增结构，恢复 0086 的三个 CHECK，沿用 PostgreSQL 的正常约束校验。 |

## 命令与结果

| 命令或范围 | 结果 |
| --- | --- |
| 在 `backend/` 执行 `mise exec -- go build ./...`、`mise exec -- go vet ./...` | 均通过。 |
| `mise exec -- go test ./internal/curation/... ./internal/sources/... ./internal/catalog/...` | 退出码 1。程序包、来源根包和 MusicBrainz 包的失败项详见下表。catalog 子树通过。 |
| `ROOMUSIC_TEST_RUN` 收窄的 `mise exec -- ./scripts/test-integration.sh` | 使用独立 Compose 项目。艺人回填、艺人对应撤销、目录取证、待定项 upsert、归组写入、已有候选的离线升级、两种待定项的候选换序通过。分批的实际选择表达式和失败项见下文。 |
| 0087 手工 DDL 往返 | 在本次独立 PostgreSQL 18 测试容器内另建空库，顺序应用仓库迁移至 0086，再执行 0087 up、down、up。用固定 restrict key 的 `pg_dump --schema-only --no-owner --no-privileges` 比较，回退后的 0086 和再次升级后的 0087 均与各自首次结构哈希相同。此项验证空库结构。 |
| `go test dependency_rules_test.go -run '^TestPackageDependencyDirections$'`（在 `backend/internal/app/` 用 mise 执行） | 退出码 1。剩余两条均是基线已有的 `catalog → catalog/metadata`、`catalog → sources`。 |
| 格式与范围 | 43 个 Go 文件的 `gofmt -l` 输出为空，`git diff --check` 通过，测试文件改动数为 0。 |

集成测试分批使用以下 `ROOMUSIC_TEST_RUN`。首次执行部分分组时遇到正在修改的函数签名导致测试包编译失败，恢复兼容签名后已复跑受影响的分组。

```text
^TestPostgreSQLCuration(ProgramGroup|ArtistLinksBackfill|EvidenceProjection|ExternalRuleUpgradePending|ProgramRelink|PendingUpsert)$
^(TestPostgreSQLCurationArtistLinksBackfill|TestPostgreSQLCurationExternalLinkArtistRevertSuppresses)$
^TestPostgreSQLMigrationRollbackRoundTrip$/^0087
^(TestPostgreSQLCuration(ExternalRuleUpgradePending|ShelvedReopensOnNewCandidates)|TestPostgreSQLCurationCJoinGroup)$
^TestPostgreSQLCuration(ExternalRuleUpgradePending|ShelvedReopensOnNewCandidates|ProgramGroup|ProgramRelink)$
^TestPostgreSQLCurationExternalRuleUpgradePending$/^有保存候选时离线重判$
```

## 现有用例失败清单

同一顶层用例的子项归在一行。

| 用例 | 子项 | 实际差异 |
| --- | --- | --- |
| TestAlignCredits | 异名异标签整组丢弃、标签数量不等 | 能独立核实的同位署名仍被保留。 |
| TestB3FingerprintAlbumRules | 抽样不足、两个版本都通过 | 回到旧 finder 的文字页缺少总数，记 list_truncated；同组两个版本经结构核实可定到组。 |
| TestB3FingerprintReachAndFallback | invalid_key:_user、source_disabled:_测试途中关闭指纹源 | 保留原有声纹降级，文字搜索返回具体缺证原因。 |
| TestB3MatchWithoutFingerprint | 文字搜索、搜索无结果 | 结果含页快照、候选或后续路由，旧断言比较旧结果结构。 |
| TestMatchCandidatesKeepFirstTenAndSortByID | release、recording | 12 个输入候选都保留。 |
| TestAlbumContraryEvidence | suppressed、existing_link | 终止状态改为 no_usable_candidates。 |
| TestAlbumCorroboration | search、candidate_limits | 文字加充分结构可以定版；同组多版通过可以定组。 |
| TestSingleRules | 顶层 | 文字判定给出 artist_mismatch。 |
| TestLookupBounds | barcode、catalog、isrc | 保存上限由 10 提高至 100。 |
| TestArtistMediaMusicBrainzURLRelations、TestLookupProjections | 前者顶层，后者 artist | 两个入口请求同一份 aliases 与 url-rels。 |
| TestSearchQueries | artist | 查询用带引号的无字段名字以检索别名。 |
| TestDiscTOC | exact、fuzzy、missing | 查询增加 recordings；exact 的旧响应缺完整曲目。 |
| TestPostgreSQLCurationProgramGroup、TestPostgreSQLCurationProgramRelink | 前者 revert、manual_detach，后者顶层 | 三项在首个共享版本断言处停止。实际均已 matched，发行写入版本为 102；夹具比较的是仍用于资料的 StoredRuleVersion=101。后续撤销、重连断言在这些调用中尚未执行。 |
| TestPostgreSQLCurationExternalRuleUpgradePending | 零候选只登记和消费一次补查 | 无模型且零候选直接进入未收录，夹具尝试读取旧待定项。有保存候选的子例通过，升级轮新增搜索和详情读取均为 0。 |
| TestPostgreSQLCurationShelvedReopensOnNewCandidates | new_candidates、declined_exception | 旧夹具分别尝试领取无模型的零候选项、已被人工拒绝的唯一候选。两者均无第一档可领项；同候选换序的两个子例通过。 |
| TestPostgreSQLMigrationRollbackRoundTrip | 0087_curation_match_rules.sql | 测试框架发现旧 CHECK 扩展后要求在 rollbackFixtures 补充新取值夹具，停在自动 down 之前。独立 DDL 往返结果见上表。 |
| TestPackageDependencyDirections | 顶层 | 两项既有依赖见命令表。 |

## 请求度量与交接

| 项目 | 本次依据 |
| --- | --- |
| 真实 MusicBrainz 查询语法 | 共 40 次 GET，17 次 200、22 次 503、1 次 400。合并发行和合并 ISRC 的成功响应均可按本地标识分回。短页对照请求均遇 503。实施阶段新增真实请求 0 次。 |
| 离线升级 | 已有保存候选的 PostgreSQL 用例观察到升级和后续稳定轮次合计新增外部搜索 0 次、详情读取 0 次。 |
| 102 在线发行的请求数 | 普通组级路径设计为一次搜索；版本通常一次搜索加一次详情，同名消歧最多一次搜索加三次详情。别名、强标识、放宽与服务重试需另行实计。按子任务分工，92 个样本、两种模型模式及逐发行出站计数由测试代理重跑 M。本轮没有这些模式的实测分位数、错连率或模型调用降幅。 |

E 接入后可领取 waiting_route 和消费艺人提案。前端 `frontend/src/features/governance/api/decoders.ts` 的操作详情解码器当前枚举四个旧对象类型，遇到含 release_group_match 的操作详情会拒收。该客户端适配与 API 快照由相应分工接续。本轮没有新用例覆盖 90 天到期、手动重查、E 结清和新组级操作的端到端流程。
