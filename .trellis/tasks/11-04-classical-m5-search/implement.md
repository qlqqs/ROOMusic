# M5 ④ 搜索与 ⑤ 艺人：执行计划

需求见 [④ prd](prd.md) 与 [⑤ prd](../11-04-classical-m5-artists/prd.md)，合同见 [design.md](design.md)。属于父任务的 **S 线**（[父任务](../11-04-classical-m5/prd.md)“一次规划、并行实现”），合入顺序第 3（P、F 之后）。分工按 `~/.claude/CLAUDE.md`：后端与所有测试由 GPT-6-Astra，前端由 Opus 5.5；子 agent 不提交，主会话审阅后在本线 worktree 里按步骤提交。

## 并行环境

- 工作目录：独立 git worktree，分支 `m5-search-artists`，基于当前 `main`。
- 隔离库：Compose 项目名 `roomusic-test-m5s`（`--env-file /dev/null`、绝对路径 `compose.test.yaml`、`-p roomusic-test-m5s`、空闲端口、随机库名、用完 `down -v`）；不得碰用户的 `roomusic-postgres-1`。
- Playwright 与 `scripts/playback-e2e.sh` 用 `flock /tmp/roomusic-m5-e2e.lock <命令>` 与其他线串行。
- 迁移：在本线基线上用下一个空号；变基与合入时由主会话改号并重跑迁移与集成测试。

## 步骤

| 步骤 | 执行者 | 内容 | 验证 | 提交 |
| --- | --- | --- | --- | --- |
| 1 后端 ④ | Astra | design.md §2–§4、§9：规范化叶子包与内置 OpenCC 字表（含许可证、固定版本）；搜索投影表、`pg_trgm` 与 GIN 索引；扫描与 M3 修正同事务写投影；后台分批回填（单写者、断点续跑、逐批日志、回填前走旧 ILIKE、完成后原子切换）；建议与结果接口（专辑、曲目两栏，`total_relation`/`has_more`）；旧 `q` 路由按相关性档位；10 万首合成库 `EXPLAIN (ANALYZE, BUFFERS)`（≥2 字符 300 ms、单字符 1 s）；接口样本；同步 `catalog-rest-contracts.md` 等 | `go test -race` 涉及的包与 `./internal/app`（集成 0 SKIP）；`go vet`；依赖规则测试；规范化反例单测；回填中途重启、回填中扫描的集成测试 | `feat(catalog): 搜索规范化与全局搜索接口` |
| 2 前端 ④ | Opus（与 1 并行，按 design.md §9、§12） | 侧栏全局搜索框、输入稍停的建议、`/` 聚焦、`/search?q=` 结果页（专辑、曲目两栏，各自分页、“1,000+”）；曲目行播放、下一首、加入队列由 app 层组合；专辑页筛选沿用新匹配；“匹配到了什么”的小字。界面文案遵守 `~/.claude/CLAUDE.md`“前端界面文案” | lint、typecheck、build | 等 3 一起提交 |
| 3 测试 ④ | Astra | 解码器往返；Vitest；Playwright（五种宽度、axe、纯键盘搜索并播放）；真实服务用例（繁简、全半角各搜一次能找到） | `npx vitest run`（相关）、`flock … npx playwright test <相关 spec>`、`flock … scripts/playback-e2e.sh` | `feat(frontend): 全局搜索` |
| 4 变基 | 主会话 + Astra | P 线合入 `main` 后把本线变基到 `main`（迁移改号）；之后搜索结果曲目行接入 P 线的心形与“添加到歌单”；建议里点曲目打开所在专辑后定位到该曲目（滚到、短暂高亮、焦点落在该行；2026-11-04 主会话裁决）；结果页专辑卡下的“匹配到了什么”小字最多两行（截图复核发现超长署名占五行） | 变基后重跑步骤 1、3 的测试 | 随 5、6 提交 |
| 5 后端 ⑤ | Astra | design.md §5–§8、§10–§11：艺人、名字键与署名事实表；扫描同事务解析（不写治理数据）；硬编码别名改种子；艺人数据后台回填与“正在整理”状态（503 `artist_catalog_preparing`、409 `artist_catalog_not_ready`）；合并、拆开（含按专辑）、改名接入 `operations.Run`（R4，不可 revert），`target_type=artist`；候选与忽略；列表（首字母、两种排序、角色筛选）、详情、播放全部/随机队列；艺人收藏（在 P 线收藏表上的后续迁移）；现有 DTO 增加艺人引用；搜索艺人栏与别名匹配；同步 DESIGN、`library-scan-contracts.md`、`governance-contracts.md`、ADR decisions | 同步骤 1 的范围加 `./internal/operations/...`；治理幂等与修订号冲突；重扫保留合并/拆开/改名 | `feat(catalog): 艺人与别名` |
| 6 前端 ⑤ | Opus（与 5 并行） | 侧栏“艺人”列表页与艺人页；各处艺人名链接（专辑卡除外，未就绪时为纯文字）；管理员合并、拆开、改名对话框与候选列表；操作记录页支持艺人目标；收藏页“艺人”一栏；全局搜索艺人栏；艺人页曲目行用 P 线的心形与“添加到歌单” | lint、typecheck、build | 等 7 一起提交 |
| 7 测试 ⑤ + 收尾 | Astra + 主会话 | 解码器往返；Vitest；Playwright（五种宽度、axe、纯键盘浏览与合并/拆开）；真实服务用例（同一位艺人繁简两种写法合为一页；合并后用别名能搜到）；导出树验证；1440 与 375 截图复核 | 同上 | `feat(frontend): 艺人页与别名维护`；结果写入两个 PRD，合入后归档 |

## 注意

- ④ 的规范化包先交付，⑤ 依赖它；两步在同一 worktree 顺序进行。
- 与 F 线冲突风险最高：`library/scanner.go`、`catalog/ingest/persist.go`、`persist_track.go`；与 P 线：曲目行组件、`router.tsx`、`nav-items.ts`、收藏表。本线最后合入，冲突在步骤 4 与合入前的变基里解决。
- 回滚点：④ 后端、④ 前端、⑤ 后端、⑤ 前端各是独立提交。
