# M5 ⑤：艺人与别名

## 目标

所有署了名的人都成为可以浏览的艺人：侧栏有艺人列表，各处艺人名可以点进艺人页；写法只差繁简、大小写的自动是同一位；不同名字的同一位（如 邓丽君 / Teresa Teng）由管理员合并成别名，别名进入搜索。父任务见 [M5](../11-04-classical-m5/prd.md)；依赖 ④ 的规范化函数。

## 决定

规划路线同 ② 收藏（用户逐项裁决，qlqq，2026-11-04）。

最终规划批准：路线 (a) 人工批准最终规划摘要；批准人 qlqq，2026-11-04 回复“批准……直接开始执行”；理由：五项的裁决、默认、合同与并行方案（三条线、各自隔离库 `roomusic-test-m5p/m5f/m5s`、文件锁排队、合入时迁移改号）均已在摘要中列出并获同意。

**用户裁决**：
1. **范围**：专辑艺人、曲目艺人、作曲、指挥、演奏、制作，凡是署了名的都是艺人。
2. **自动归一**：规范化后（④ 的规则）相同的名字自动是同一位艺人，显示最常见的写法；这是字面等价，不是猜相似。少数重名的人会被合在一起，管理员可以拆开。
3. **别名维护（只有管理员）**：艺人页上可以“合并艺人…”（搜到另一位并入当前艺人，对方的名字变成别名）、把别名拆回独立艺人、修改主显示名。另有“可能是同一位”的候选列表，只提示，管理员确认后才合并。
4. **艺人页**：名字与别名、“播放全部”“随机播放”；作为专辑艺人的专辑；按角色分组的参与曲目（作曲、指挥、演奏、制作、曲目艺人），曲目可播放、收藏、加入歌单。**艺人可以收藏**，收藏页加“艺人”一栏。不做传记与合作网络。
5. **入口**：侧栏“艺人”列表页，按首字母分组（中文按拼音，假名写的名字归入假名组），可以按名字或专辑数排序，可以只看某个角色；专辑详情、曲目行署名、播放器、正在播放、历史、搜索结果里的艺人名都能点。专辑卡片整张是链接，卡片上的艺人名仍是文字。拼音首字母用内置的汉字拼音表。

**默认**：
- 合并、拆开、改名接入 `operations.Run`（R4），出现在操作记录里；合并的反向是拆开，改名的反向是再改名，所以都不另做撤销。扫描不写艺人治理数据：重扫后合并、拆开与改名都保留。
- 拆开重名：管理员可以把一位艺人在某些专辑上的署名分给新艺人（按专辑选），用于两个同名的人。
- `artist.go` 里硬编码的 周杰伦/周杰倫/Jay Chou 改为初始的别名数据，代码里的硬编码删除；繁简已由自动归一覆盖。
- “可能是同一位”的候选只来自明确信号（同一首曲目的标签里同一角色出现两种写法、或专辑艺人与曲目艺人在同一张专辑里一一对应不同写法），不按字符串相似度；候选可以忽略，忽略后不再提示。
- 首字母分组：拉丁字母按首字母（去变音符），汉字（含日文汉字）按拼音首字母，假名一组，其他归“#”。
- 按专辑拆开时，记住当时选中的那些版本；这些版本以后新增的曲目跟着走，以后新入组的版本不自动跟随。
- 合并后收藏的艺人仍在收藏里（按合并后的艺人显示、不重复），拆开后恢复原样。
- 艺人“播放全部”最多 10,000 首；“随机播放”先在全部曲目里打乱再取前 10,000 首；超出时提示一句。按名字排序时按首字母分组，按专辑数排序时不分组。
- “可能是同一位”被忽略后，同一对名字不再提示；出现新的写法对时可以再提示。第一类信号（同一首曲目同一角色两种写法）只对重扫后的数据有效。
- “规范化相同即同一位”是对旧规则“不把同名字符串合并成人物”的有限修订，实施时同步改 `frontend/DESIGN-components.md`、`.trellis/spec/backend/library-scan-contracts.md` 与 ADR 的 decisions 文件。
- 搜索：任何别名都能搜到这位艺人及其专辑和曲目，结果里写明“别名：Teresa Teng”。

## 现状与约束

- 数据：只有字符串——`releases.artist`、`releases.album_artist`、`tracks.artist`、`track_credits(role ∈ composer/conductor/performer/producer, name)`（`backend/migrations/0002_core_slice.sql:37,54`，`0010_album_organization.sql:4`，`0012_track_credits_and_release_metadata.sql:36-46`）。曲目艺人与署名在扫描时按 `backend/internal/catalog/metadata/artist.go` 拆分、套硬编码别名（:27-29）、排序后以 “ / ” 连接；发行艺人与专辑艺人只折叠空白（`ingest/organizer.go:284-295`，`ingest/persist_track.go:72`）。原始标签值留在 `track_observations`。
- 规则：不按相似度猜别名，只有显式别名表能改显示名（`.trellis/spec/backend/library-scan-contracts.md:94-99`）；弱相似永远不能做权威合并（`.trellis/spec/guides/product-goals.md:135-137`）；扫描不写治理表（`.trellis/spec/backend/governance-contracts.md:133-136`）。
- 治理：R4 见 `docs/architecture/core-1-roadmap.md:67`。`operations.target_type` 只允许 release / release_group（`0018_release_group_default.sql:17-18`），修订号约束要求目标有自己的 `edit_revision`（`0017_governed_operations.sql:26`）；操作记录页、解码器与描述按发行和专辑写死（`frontend/src/features/governance/api/decoders.ts:178,275`，`change-summary.ts:5-25,96-106`）。ADR 0002/0003 正文接近注入上限，决策行写在 `*-decisions.md`。
- 专辑艺人可通过 M3 修正改写（`backend/internal/catalog/fields.go:24-33`），修正值参与艺人归属；曲目艺人与署名不能改。
- 前端：艺人名都是纯文字（`frontend/DESIGN-acceptance.md:33`），出现位置见 `album-card.tsx:26`、`album-detail-view.tsx:189`、`release-media.tsx:26-41`、`player-bar.tsx:18`、`now-playing-page.tsx:26`、`history-view.tsx:92` 等；导航没有“艺人”（`frontend/src/app/shell/nav-items.ts:14`）。传记与合作网络按 `frontend/DESIGN-components.md:137` 不做。

## 验收

- R1 归属：扫描后每个署名都对应一位艺人；规范化相同的名字是同一位；重扫不改变管理员的合并、拆开与改名。
- R2 治理：合并、拆开、改名只有管理员能做，走 `operations.Run`，有幂等与修订号冲突；操作记录页能看到并显示目标艺人；普通成员 403。
- R3 候选：候选只来自明确信号，确认后合并、忽略后不再出现。
- R4 艺人页：专辑、按角色分组的曲目、播放全部与随机播放、收藏艺人；缺失曲目照历史页处理。
- R5 列表与入口：首字母分组（拼音、假名、#）、两种排序、角色筛选、分页；各处艺人名链接到正确的艺人。
- R6 搜索：别名、规范化写法都能搜到艺人，全局搜索出现艺人栏。
- R7 质量：接口样本与解码器往返；五种宽度不溢出、axe 无违规、纯键盘可完成浏览、合并与拆开；Playwright 与真实服务用例通过；截图复核。

## 范围外

传记、照片、合作网络、外部元数据源（MusicBrainz 等）、艺人排序名的手工编辑、按相似度自动合并。

## 设计与执行

见 [④ 搜索的 design.md](../11-04-classical-m5-search/design.md) 与 [implement.md](../11-04-classical-m5-search/implement.md)：④ 与 ⑤ 同一条实现线，④ 先交付规范化函数。

## 实施结果

2026-11-05 完成。提交：`482a10d`（后端：艺人身份、别名与治理、艺人收藏、后台回填，迁移 0026–0029）、`b504037`（前端：艺人页、合并拆分、收藏艺人栏）、`9bfb58b`（概况只在分隔处换行、首字母导航宽屏一行）、`1480b25`（热门艺人搜索，迁移 0030）。设计裁决 I1–I10 记在 [design.md](../11-04-classical-m5-search/design.md) §15.2a/§15.2b 末尾。

- R1–R6：后端由 catalog/personal/operations 的 PostgreSQL 集成测试覆盖（定稿时全影响 race 2229 项通过，PG 0 SKIP）；前端由 Vitest（全量 3603 项一次）与 `e2e/artists*.spec.ts`、`e2e/search.spec.ts` 覆盖，后端交付的 35 份真实样本全部登记往返。
- 真实服务：新增 `e2e/real/artists.real.ts` 3 项——繁简写法扫描为同一位艺人；管理员合并“邓丽君/Teresa Teng”后按别名搜到、拆回恢复；普通成员收藏随合并与拆回转移。与 `search.real.ts` 2 项、`personal.real.ts` 2 项、`playback.real.ts` 24 项经 `scripts/playback-e2e.sh` 同批运行，31 / 0 / 0，`--retries=0`。
- 性能（用户裁决 I9）：十万轨联合后台回填 32 分 04 秒；单批 CATP p95 199 ms／p99 276 ms，最大 5986 ms（几乎全在 WAL 提交刷盘，只记录）；回填期间 M3 66/66、扫描 65/65 成功，播放与搜索 1913 次请求无 503。10,000 首队列：专辑顺序约 1.2 s，随机约 2.3 s。
- 截图：1440/375 共 26 张（合成数据，`/tmp/roomusic-m5-artists-screens/`，未入库），主会话复核；修了概况词中断行和首字母导航分两行，375 下管理员按钮两行、收藏页心形较远保持现状。
- 后端详细记录：[verification/backend-status.md](verification/backend-status.md)。

已知风险：回填单批最大值受磁盘刷盘影响；单字搜索最慢约 1 s；文件 `state-management.md`、`catalog-rest-contracts.md` 超出建议体积待整理；密码提示“12 字符”与后端按字节计数不一致，另行处理。
