# 步骤 1 ④ 后端交付报告

日期：2026-11-04。仅本 worktree `m5-search-artists`；未提交、stash、reset、checkout，未实现⑤。前端文件与 web 构建产物由并行代理拥有，未纳入本报告文件清单。

## 1. 结论与改动范围

- 规范化、投影迁移、同事务扫描/M3双写、可续跑单写者后台回填、旧 ILIKE 回退/原子切换、新两栏接口及旧 q 相关性已完成。
- 最终17组完整服务端 HTTP 均达标。≥2字符建议/两栏首屏 p95 最大 **137.61/164.49 ms**，单字符最大 **284.63/233.70 ms**；门槛分别300/1000 ms。
- 12个受影响包 race：**381 个顶层测试通过，其中 259 个 PostgreSQL 集成，0 SKIP**。最后显式列投影及基准统计补充后，搜索/依赖最小回归另通过 29 项（21 集成、0 SKIP）；不是把重复测试相加冒充不同用例。两轮对应 vet 通过。
- 48份响应样本来自真实 app handler、鉴权和 PostgreSQL，不手写响应；最终未开更新开关的样本漂移测试通过。

完整逐文件清单：[step1-backend-files.txt](step1-backend-files.txt)。主要区域：

| 区域 | 文件/职责 |
| --- | --- |
| 纯规范化 | `backend/internal/catalog/textnorm/**`：NFKC/casefold/拉丁附加符/假名/繁简/编号、固定数据和许可 |
| 迁移与写投影 | `backend/migrations/0025_catalog_search.sql`；`catalog/internal/searchindex/**`、`internal/writegate/**` |
| 后台与生命周期 | `catalog/maintenance/**`、`internal/app/app.go`、`cmd/roomusic/main.go` |
| 事实挂接 | `catalog/ingest/persist.go`、`persist_track.go`、治理 correction/claim/revert/归组/默认公共锁；`library/scanner.go`仅加终态事务gate |
| 读取与 HTTP | `catalog/search*.go`、`read.go`、`groups.go`、`catalog/httpapi/search*.go`、`handlers.go` |
| 验证与样本 | app搜索集成、规模基准、依赖规则、治理锁回归；`app/testdata/api-samples/search/` |
| 后端合同 | `search-contracts.md`、catalog REST/扫描/治理/目录/index 链接。**未改 `database-guidelines.md`**，仅引用 P 线统一局部 JIT 规则 |

## 2. 迁移、规范化数据与预检

迁移：`0025_catalog_search.sql`，本轮后端重放已按 P/F 之后的空号改名，SQL 字节保持不变。SHA-256：`e678b3fa2936b160b5dc52916630ef5fdbe1b191d6a6060b51c05bbaaf71b071`。空schema迁移、rerun/漂移/回滚回归通过。`pg_trgm 1.6` 已核实安装在 public；固定镜像摘要、PG参数见 [environment.json](search-scale/environment.json) 与 HTTP summary。

OpenCC来源：BYVoid/OpenCC，固定 `ver.1.1.9`，提交 `556ed22496d650bd0b13b6c163be9814637970ae`，Apache-2.0。只嵌入上游原始 `TSCharacters.txt`、`STCharacters.txt`、完整 LICENSE；来源/三份SHA及算法见 `backend/internal/catalog/textnorm/data/opencc/SOURCE.md`。不新增 Go 依赖，无运行期下载。

`Text`/`CatalogNumber` 的 string 返回值不承载加载错误。入口先成功调用 `LoadEmbedded() error`，sync.Once缓存结果/错误，校验固定字表与许可；失败禁发新投影并继续旧路径。未成功预检就调用转换是内部编程错误，panic而不静默半规则，无init panic。TS第一目标+无歧义ST补充，不做闭包；`が≠か`、`ば/ぱ/は`、`炼≠链`等反例均覆盖。初轮8秒fuzz记录129,016次执行；最终race再次覆盖真实表与逐类反例。

## 3. 测试命令与结果

所有数据库操作使用 `/tmp/roomusic-m5s-backend.env` 的随机库/空闲端口，只连接 Compose项目 `roomusic-test-m5s`。未访问用户 `roomusic` 库或其他线项目。

```bash
source /tmp/roomusic-m5s-backend.env
export ROOMUSIC_TEST_FFMPEG="$(./scripts/ffmpeg-pinned.sh)"
cd backend
packages='./internal/catalog ./internal/catalog/httpapi ./internal/catalog/ingest ./internal/catalog/governance ./internal/catalog/internal/searchindex ./internal/catalog/internal/writegate ./internal/catalog/maintenance ./internal/catalog/textnorm ./internal/library ./internal/app ./internal/platform/postgres ./cmd/roomusic'
go test -race $packages -skip '^TestRealMusicLyricsCoverage$' -count=1 -timeout=15m -json
go vet $packages

go test -race ./internal/catalog ./internal/catalog/httpapi ./internal/app \
  -run 'Search|PackageDependencyDirections' -count=1 -timeout=10m -json
go vet ./internal/catalog ./internal/catalog/httpapi ./internal/app
```

- 上轮曾有4个环境skip：3个ffmpeg用例和无关的真实音乐歌词遍历。最终启用仓库固定ffmpeg，3项确实运行；只显式不选择 `TestRealMusicLyricsCoverage`（不接触用户音乐，也不以合成fixture冒充真实音乐验证）。最终被选择的测试 **0 SKIP**。
- 未跑整个仓库/前端/E2E：本次只负责④后端；12包及app包含本步实际影响范围。没有执行需共享E2E锁的命令。
- 依赖规则 `TestPackageDependencyDirections` 通过；没有放开 catalog全包依赖。
- 回填必需集成：`TestPostgreSQLCatalogBackfillRestartAndAtomicPublish`、`TestPostgreSQLCatalogBackfillDuringRealScan`，另测单写者/连接取消释放锁、revision防覆盖/回滚修补、版本升级拒绝旧build。
- 读取/HTTP：逐类规范化、四credits不扩散兄弟轨、有效修正、非代表成员/missing默认/移组、RR发布与事实交错、999/1000/1001与21/99页、最大合法OFFSET、密集三档/超4096重复命中/长同前缀标题、鉴权/非法输入/安全封面与音频摘要、旧q与无q回归。
- `git diff --check`、本线Go文件gofmt检查通过；改动后端spec均小于24,000字节。

统计：[test-summary.json](search-scale/test-summary.json)；原始日志 `delivery-race.jsonl.gz`、`final-followup-race.jsonl.gz` 及对应 vet.log。样本路径：`backend/internal/app/testdata/api-samples/search/`（48份）。

## 4. 规模、环境与测量方式

- 100,001 present（含1首真实扫描探针）+10,000 missing；110,001 tracks、10,001 releases、8,001 groups、440,000 credits、710,004搜索值、120,002 subject完成行。没有降低规模。
- 合成素材有两万编号姓名系列、每轨两个拼接艺人/四类credit、五万轨热门艺人/作曲者、约50%热门厂牌、多版本与missing-only发行。④不造艺人实体或别名关系；⑤的20%别名与身份图性能**未测，不冒充完成**。
- Threadripper1920X，Go进程可见12CPU，RAM约31.34GiB，Go1.25.10；PG18.6、UTF8/en_US.utf8，shared_buffers128MB、work_mem4MB、max_connections100。全局jit仍on，仅搜索只读RR事务 `SET LOCAL jit=off`。
- 每组预热5次、测量30次。建议每栏5条；结果每栏50条，专辑/曲目并发，计整屏关键路径。真实app handler包含认证、连接等待、计数、排序、摘要与JSON序列化；本机httptest不含外部网络/前端防抖。不是只测Store或单条SQL。
- 独立首填测量后复用**同规模且已发布**的schema做最终查询验收，没有重做已完成的11分钟回填。最终summary的 BackfillMS=0 明确表示未在该轮回填，不是回填瞬时完成。
- 最终活跃Go heap约 3.13MiB（测量末端值，**非峰值RSS**）。表/索引字节数和所有冷首次、中位、p95见 `accepted-http-summary.json`。

### 完整 HTTP 时延（ms）

| 查询 | 建议 median / p95 | 两栏首屏 median / p95 | 专辑 / 曲目 p95 | 门槛 |
| --- | ---: | ---: | ---: | ---: |
| `交响曲 第00999号` | 126.83 / 136.90 | 139.60 / 153.75 | 92.60 / 153.67 | 300 |
| `交响曲 第009` | 128.13 / 137.61 | 150.36 / 164.49 | 108.94 / 164.43 | 300 |
| `周杰倫` | 85.27 / 104.41 | 113.17 / 133.43 | 133.29 / 64.14 | 300 |
| `周杰伦` | 83.44 / 88.92 | 113.57 / 119.80 | 119.74 / 61.22 | 300 |
| `爱` | 152.71 / 159.96 | 133.79 / 146.05 | 127.56 / 145.97 | 1000 |
| `周` | 88.21 / 91.41 | 108.17 / 131.77 | 131.63 / 57.30 | 1000 |
| `交响` | 120.46 / 128.17 | 141.07 / 153.04 | 152.93 / 80.27 | 300 |
| `a` | 209.90 / 220.41 | 165.70 / 178.57 | 163.97 / 178.50 | 1000 |
| `%` | 251.12 / 284.63 | 199.46 / 233.70 | 178.94 / 233.61 | 1000 |
| `_` | 251.78 / 266.63 | 197.78 / 221.59 | 165.61 / 221.51 | 1000 |
| `~` | 252.11 / 269.74 | 196.27 / 218.82 | 158.39 / 218.75 | 1000 |
| `不存在的长查询xyz` | 38.73 / 40.01 | 48.84 / 51.32 | 31.64 / 51.24 | 300 |
| `久石譲` | 88.94 / 95.77 | 120.39 / 127.46 | 127.38 / 65.24 | 300 |
| `热门厂牌` | 72.42 / 84.88 | 97.74 / 113.52 | 113.44 / 58.97 | 300 |
| `SRCL-1234` | 117.18 / 120.94 | 119.93 / 132.04 | 60.34 / 131.99 | 300 |
| `srcl 1234` | 116.59 / 126.99 | 118.90 / 125.72 | 59.93 / 125.66 | 300 |
| `在线修正厂牌` | 114.27 / 127.41 | 112.79 / 129.35 | 51.06 / 129.27 | 300 |

第100页（`tracks?q=交响`）单次 **134.15ms**，仅退化记录，不混作首屏p95。旧两路由保持精确count，单次HTTP另存summary的 `OldReleasesMS/OldGroupsMS`，不冒充30次分位数。

### 请求内累计 SQL 墙钟（p95，ms）

覆盖鉴权/事务/状态/匹配/补全；数据库查询执行到结果消费的累计时长。连接池等待及非DB处理仍由上面的完整HTTP涵盖。两栏SQL总工作量是两个请求耗时之和，不是并发整屏关键路径。

| 查询 | 建议SQL总耗时 | 专辑SQL总耗时 | 曲目SQL总耗时 | 两栏SQL总工作量 |
| --- | ---: | ---: | ---: | ---: |
| `交响曲 第00999号` | 136.15 | 91.63 | 153.10 | 235.59 |
| `周杰伦` | 88.19 | 118.15 | 60.18 | 177.76 |
| `爱` | 159.34 | 125.96 | 145.22 | 265.74 |
| `交响` | 127.01 | 151.87 | 79.35 | 230.65 |
| `热门厂牌` | 84.20 | 112.29 | 57.87 | 160.50 |
| `SRCL-1234` | 120.27 | 59.49 | 131.36 | 189.18 |

所有17组上述指标存summary。每条实际搜索/补全SQL的参数、调用次数、累计/最大时间在 `accepted-http-sql-01..17.json.gz`，不记录鉴权凭据。

### EXPLAIN ANALYZE BUFFERS

下表为实际生产SQL的新两栏有界计数及51行页查询；时间为 planning / execution，ms。它们是单次计划诊断，不能代替HTTP p95。

| 查询 | 栏 | count plan / exec | page plan / exec | count shared hit / read | page shared hit / read |
| --- | --- | ---: | ---: | ---: | ---: |
| `交响曲 第00999号` | albums | 3.42 / 2.62 | 26.35 / 16.64 | 220 / 0 | 889 / 0 |
| `交响曲 第00999号` | tracks | 1.19 / 45.15 | 7.32 / 73.89 | 3538 / 0 | 2737 / 0 |
| `周杰伦` | albums | 2.72 / 27.42 | 21.68 / 48.47 | 13518 / 0 | 15283 / 0 |
| `周杰伦` | tracks | 0.96 / 24.73 | 6.89 / 13.07 | 4619 / 0 | 1897 / 0 |
| `爱` | albums | 3.03 / 16.14 | 20.43 / 64.09 | 13050 / 0 | 40959 / 0 |
| `爱` | tracks | 1.09 / 48.75 | 6.10 / 82.83 | 5277 / 0 | 10193 / 0 |
| `交响` | albums | 2.72 / 33.14 | 19.99 / 69.27 | 18264 / 0 | 17414 / 0 |
| `交响` | tracks | 0.98 / 23.86 | 6.23 / 27.67 | 5155 / 0 | 3049 / 0 |
| `SRCL-1234` | albums | 4.96 / 6.28 | 24.18 / 18.39 | 223 / 0 | 881 / 0 |
| `SRCL-1234` | tracks | 1.27 / 97.40 | 8.22 / 77.57 | 110473 / 303 | 2883 / 0 |
| `不存在的长查询xyz` | albums | 2.96 / 0.79 | 21.58 / 3.42 | 118 / 0 | 9 / 0 |
| `不存在的长查询xyz` | tracks | 1.18 / 70.92 | 6.76 / 2.13 | 110684 / 0 | 9 / 0 |

原始17份计划：`accepted-explain-01..17.json.gz`（包含SQL、绑定参数、完整节点、BUFFERS、旧路由精确count与摘要）；紧凑指标：`accepted-plan-metrics.json`。

- 有界计数从唯一实体基表出发，密集路径在第1,001个实体停止，实际计划无LIMIT前全量实体HashAggregate。稀疏命中仅物化完整原始字段集合，不截断，不物化传播后的全体实体。
- 热门词不再先展开所有传播结果并全局排序：三档精确top-k、当前代表有序流、页后说明补全。short/trigram/exact索引各在适当路径生效；不强制planner开关。
- 低命中曲目页可能将title_values CTE写入临时文件（例如645个8KiB块，约5MiB）；没有把临时写入隐藏成零。sort spill/读写块及每个节点行数见原始计划，当前完整HTTP仍达标。
- 复用语句额外诊断：同一只读RR事务执行6次后 EXPLAIN EXECUTE，`pg_prepared_statements` 实际为5次custom、2次generic；热门“周杰伦”曲目有界count执行 **25.239ms**。不设置plan_cache_mode或enable_*。证据 `accepted-prepared-count.{json,sql}.gz`。
- 保留初始JIT、空schema短词353ms、稀疏索引回归以及通用计划5.8s等失败证据；修复过程见 [step1-search-review.md](step1-search-review.md)。最终通过不能抹去失败轮。

复现（隔离库/env先按本任务约束创建；空schema首填去掉复用变量）：

```bash
source /tmp/roomusic-m5s-backend.env
export ROOMUSIC_SEARCH_SCALE_OUTPUT=/tmp/roomusic-m5s-final-accepted
export ROOMUSIC_SEARCH_SCALE_ITERATIONS=30
# 仅当本线同一测试库中对应合成schema尚在时才可设置：
export ROOMUSIC_SEARCH_SCALE_REUSE_SCHEMA=search_scale_b04274e874b24c798ee7f4dbaa6dc899
cd backend
go test ./internal/app -run '^$' -bench '^BenchmarkPostgreSQLSearchScale$' \
  -benchtime=1x -count=1 -timeout=15m -v
# 从全新schema造库/回填应 unset ROOMUSIC_SEARCH_SCALE_REUSE_SCHEMA，超时用45m。
```

## 5. 在线回填实测

独立首填从 worker启动到发布 **665.45s（约11分5秒）**；3834个已提交批次，batch墙钟p95=147ms，最大2151ms。最大值说明自适应短批目标不是硬实时保证，单对象或索引写入仍可能拖长该批；不能声称每批都小于200ms。

日志发布elapsed=691813ms自持久状态started_at起算，包含先前fixture初始化间隔；665452.61ms是本轮worker墙钟，二者计时起点不同。日志安全保留 `build_id/版本/phase/completed/total/written/skipped/batch_ms/elapsed/eta`，不含音乐原文或路径。

| 回填期间前台动作 | 样本数 | p95 / 单次（ms） |
| --- | ---: | ---: |
| ready | 665 | 0.090 |
| 旧建议 | 665 | 447.73 |
| 真实探针音频 Range 206 | 665 | 1.79 |
| 真实扫描启动并最终成功 | 1 | 23.24 |
| M3修正并同事务投影 | 1 | 22.67 |
| CATP获取（含往返） | 3840 | 0.388 |

CATP获取指标不是持锁时长，扫描/M3仅各一次不冒充统计分位数。回填不中断ready、旧搜索或播放；并发扫描产生新对象被覆盖，修正值不被旧回填覆盖。首填证据 `backfill-run-summary.json`、`backfill-batches.jsonl.gz`；首填轮查询性能有失败，**仅其回填指标被接受**，最终查询结论来自独立 accepted-http 轮。

## 6. 内部细节、偏差与后续

对外DTO、两栏边界、1000/eq/gte/has_more、旧路由、完整字面匹配均未偏离design，无待裁决的接口形状问题。

内部细化已写入search spec：md5定长定位仍全键复核；subject完成行同步search_title；前128scalar索引但完整标题/ID排序；4097原始值探测仅选执行策略；精确三档top-k不截候选；稀疏计数完整原始命中边界；密集按来源字段先取再匹配；正常prepared计划；极大合法OFFSET饱和保护；关系索引支持当前组代表。CATP治理并发测试按真实阻塞链修正，不依赖所有等待者都处于FOR UPDATE。

未宣称完成：⑤艺人/别名性能、前端解码器/视觉/Playwright（步骤3）、跨线合入后的迁移改号与回归。基准为本机服务端handler计时，不含网络；内存仅记录末端heap；回填个别批次超过200ms目标如实保留。它们不是放宽300ms/1s搜索门槛。

## 7. 清理

清理结果见 `search-scale/cleanup.txt`：只清理本线 `roomusic-test-m5s`，完成后删除容器和卷。没有终止其他线进程或操作用户数据库。临时env删除后，上述复现命令需重新创建隔离库和env。
