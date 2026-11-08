# ⑦ 署名角色扩充：实施计划

设计见 [design.md](./design.md)。T 线第 2 项，在 ⑥ 合入线分支之后开始（复用 ⑥ 的拆分规则、`TXXX` 入口与 `finalizeTags`）。子 agent 不提交 git。

## 共同约束（写进每个派活 prompt）

- 前端文案：不写防御性说明，不加不必要的说明；确实需要的说明用一句日常话。署名只显示“组名：名字”，不加解释。
- 测试只做必要的：只测改动直接影响的部分，跑下面列出的最小测试集；不跑全量，不做演练、不重复验证、不为假想场景写测试、不过度兜底。
- 前后端解码器是严格枚举，前后端改动必须同批合入线分支。

## 后端（GPT-6-Astra，high）

1. `catalog/metadata/text.go`：`Credit` 增加 `Detail string \`json:"detail,omitempty"\``、`Source string \`json:"source,omitempty"\``（空等于 `tag`）；`CanonicalCredits` 按 design §3.1 排序、按 `role+name+detail` 去重。
2. `catalog/metadata/credit_roles.go`（新文件）：规范角色序、`MapCreditRole`、`MapInstrument`、细分键、`CreditRoleTableVersion=1`。表的唯一性与目标合法性由单测保证。
3. `library/parser`：
   - `artist_evidence.go`：`artistTagRole` 改为查角色表（键名映射），新键记证据。
   - `tags.go`：署名键统一走角色表；`PERFORMER` 类值做 `名字 (乐器)` 拆分。
   - `mp3.go`：帧白名单加 `TEXT`、`TIPL`、`IPLS`、`TMCL`；成对解析。
   - `m4a.go`：freeform 署名键走角色表。
   - `lyrics_credits.go`（新文件）：design §3.4（角色表与乐器表命中的子标签全部读；带细分的存歌词原写法；名字去 `@` 后缀；同角色已有标签署名时丢弃歌词来源）；在 ⑥ 的 `finalizeTags` 里调用。
4. 迁移（线内草号 0034，含 `.down.sql`）：design §3.5；先在隔离库应用到 0033 后用 `\d` 核对约束名。`migrations_rollback_integration_test.go` 的 `rollbackFixtures` 补新角色与 detail 数据。
5. `catalog/ingest/persist.go`：`INSERT INTO track_credits` 增加 `detail`、`source`。
6. `catalog/internal/artistgraph/facts.go`：credits 读取后按 `(track, role, name)` 去重再建事实（否则同一语句里 `ON CONFLICT` 二次命中报错）；`bracket.go` 排除词加乐器表写法。
7. `catalog/internal/searchindex/values.go`：`appendCredit` 角色白名单加两项。
8. `catalog`：`projection.go` `ReleaseCredit.Detail *string \`json:"detail"\``；`read.go` 读 `c.detail`（发行级为 NULL）；`artists_personal.go`、`search_sql.go`、`artists_summary_sql.go`、`releasesql/artist_refs.go`、`httpapi/artist_reads.go` 角色词表与顺序。
9. 版本（design §7）：T 线已升过 `artistgraph.Version`、`ProjectionVersion` 则不再升；④ 先合入 main 时，T 线 rebase 后 `parser.CommonVersion` +1（与 ⑥⑧ 合计一次）；T 线先合入则不升。
10. 合同：`library-scan-contracts.md`（署名来源、映射表、歌词署名）、`artist-contracts.md`（9 个角色）、`catalog-rest-contracts.md`（credits 的 `detail` 与 6 个角色）、`search-contracts.md`（credit 角色）、`docs/architecture/core-1-roadmap.md:30` 的“作词/编曲需额外数据合同”改为已提供。

可并行：2 与 4 可同时；3 依赖 2；5–8 依赖 1、4。

## 前端（Claude Opus 5.5，high）

一个 agent 一次做完下列各项（含 ⑥ 的一行词表，如果 ⑥ 时未单独派）：

1. `features/catalog/api/decoders.ts`：`creditDecoder` 增加 `detail: d.nullable(d.string({ min: 1, max: 256 }))`。
2. `features/catalog/labels.ts`：`creditRoleLabel` 加 `lyricist: "作词"`、`arranger: "编曲"`；新增 `creditDetailLabel`（design §3.2 的细分键中文名，未知原样）。
3. `features/catalog/components/release-media.tsx` 的 `CreditsLine`：按 `detail ?? role` 分组，组名用细分名或角色名。
4. `features/catalog/api/artist-summary.ts`：`artistRoles` 9 项、`artistRoleLabels` 加两项；`features/artists/api/decoders.ts` 的 `artistTrackRoles` 加两项；`features/search/api/decoders.ts` 的 `matchRoles` 与 `match-note.ts` 的 `roleNames` 加两项。艺人页分栏与列表筛选随词表自动出现，确认顺序与 design §3.1 一致。
5. `frontend/DESIGN-components.md:155` 的“无数据不补编曲/作词等角色”改为“无数据的角色不显示”；`.trellis/spec/frontend/artist-guidelines.md` 的“七个角色”改为九个。
6. `src/test/api-samples` 随后端重写的样本同步（由测试 agent 生成后，前端 agent 只改解码器）。

## 测试（GPT-6.1-Sol，high）

- `metadata`：映射表唯一性与目标合法性；design §5 中不依赖容器的行（`名字 (乐器)`、词曲双产出、未知兜底）。
- `parser`：Vorbis 新键、ID3 `TEXT`/`TIPL`/`TMCL`（含奇数个值）、M4A freeform 各一例；`lyrics_credits` 的 design §5 歌词各行（含制作人、监制、混音工程师、乐器、`@` 后缀、`吉他间奏/录音/制作人`、`协力` 结束署名块、标签优先）。
- 前端：`release-media.test.tsx` 的细分分组用例里含一条歌词原写法细分（“混音工程师：…”原样显示）。
- `artistgraph`：`TestBracketAliases` 增加乐器括号不生成别名；一个 PostgreSQL 用例：同一曲目同人两种乐器只建一条 performer 事实。
- 迁移：`TestPostgreSQLMigrationRollbackRoundTrip`（新 fixture）。
- API 样本：`ROOMUSIC_UPDATE_API_SAMPLES=1` 重写含 `detail` 与新角色的样本，前端 `api-contract.test.ts` 通过。
- 前端：`release-media.test.tsx` 增加一例细分分组（“小提琴：…；作词：…”）。

验证命令：

```bash
cd backend
go test ./internal/catalog/metadata/ ./internal/library/parser/ ./internal/catalog/internal/artistgraph/ -run 'Credit|Role|Instrument|Lyrics|TIPL|TMCL|BracketAliases' -count=1
ROOMUSIC_TEST_RUN='^(TestPostgreSQLMigrationRollbackRoundTrip|TestPostgreSQL.*CreditDetail.*)$' ../scripts/test-integration.sh
ROOMUSIC_UPDATE_API_SAMPLES=1 ROOMUSIC_TEST_RUN='^TestPostgreSQLAPIResponseSamples$' ../scripts/test-integration.sh
cd ../frontend && npx vitest run src/features/catalog src/features/artists src/features/search src/test/api-contract.test.ts && npm run typecheck
```

## 风险文件与回滚点

- 共享文件：`parser/tags.go`、`mp3.go`、`m4a.go`（O 线 ③、M 线 ⑩）；`ingest/persist.go`（O 线 ② 的收藏转移也改）。
- 迁移是回滚点：down 删除新角色数据与新列。
- 前端严格枚举：前后端同批。

## 合入前核对

- [ ] design §5 每一行有单测。
- [ ] 迁移约束名已用 `\d` 核对；回退往返通过。
- [ ] API 样本已重写，前端契约测试通过。
- [ ] 截图复核：曲目行显示“作词/编曲/小提琴”分组、艺人页新分栏、列表筛选（1440 与 375 宽）；界面没有说明性文字。
- [ ] 前后端同批合入（解码器严格枚举，后端单独上线会让旧前端整页解码失败）。
- [ ] 真实库 smoke：在本机隔离实例上对 `music/` 扫描，作词、编曲署名数量与 design §6 预期一致，曲目艺人与专辑艺人数量不变。
- [ ] 本机 smoke 中歌词来源的制作人或监制约 23 首、作词与编曲各约 38 首（design §6），曲目行原写法细分显示正确。
