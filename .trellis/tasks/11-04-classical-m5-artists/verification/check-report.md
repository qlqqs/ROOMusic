# M5 ⑤后端独立检查：最终增量报告

- 日期：2026-11-04。
- 任务：`.trellis/tasks/11-04-classical-m5-artists`。
- 本轮方法：只读代码、样本及既有运行日志，使用Python解析JSON、核验清单和哈希；只修改本目录的检查报告与样本状态说明。
- 本轮没有启动数据库、测试、编译或benchmark，没有读取用户音乐库，没有修改生产代码、前端、嵌入资源或提交Git。正式100k性能测试由主控独占执行并已失败停止；本报告只记录已提供的结果，不参与修复或重跑。

## 一、结论

此前指出的治理预算、真实重扫、M3图刷新和三处样本覆盖问题，在当前代码、落盘样本及既有复验日志中均已闭合。未发现本次范围内新的确定性问题；未发现通过删除或放宽旧业务断言来迁就⑤实现的情况。

该结论是后端增量合同及证据审计结论，**不等于⑤整项产品验收通过：正式完整规模验证已FAIL**。主控报告回填期间M3 correction返回503 `database_unavailable`，在73.972s停止，尚未进入p95/EXPLAIN阶段；因此在线可用性与正式性能门禁未通过，不得以普通回归或样本PASS覆盖。前端decoder往返、浏览器/真实播放与视觉无障碍验收仍须分别汇总。

## 二、三处样本缺口闭合证据

| 原缺口 | 当前文件与生成路径 | 独立文件审计 |
| --- | --- | --- |
| 超限队列只有断言，没有完整样本 | `recordArtistQueueLimit`调用`recordArtistLargeSample`，复用recorder的敏感路径/凭据检查、UUID/时间规范化；单独管理仓外文件，UPDATE写入，普通模式逐字比较，缺文件失败 | 完整JSON含10000个唯一present曲目，不是摘要或截短items；`total=10001`、`returned=limit=10000`、`truncated=true`、`missing_total=1`；每项保留安全TrackSummary及ReleaseSummary字段 |
| 搜索已就绪、艺人仍整理中的独立状态缺失 | `recordArtistSearchReadyPublication`在真实maintenance发布回调用channel barrier暂停艺人发布，另一连接通过HTTP读取；不直接UPDATE状态伪造样本 | `catalog/status-search-ready.json`为200，`search_mode=normalized`、`artists_state=preparing`、`epoch=3`；与初始整理、全就绪分别存放 |
| M3提交即搜索命中没有落盘证据 | `recordArtistCorrectionSearchSamples`分别在legacy与normalized路径执行真实PATCH，再立即GET搜索；PATCH与查询之间不重扫、不运行维护 | 两模式新标题各命中1组/2首present曲目，exact及原文正确，旧标题items/total均为0；normalized艺人结果为新有效署名，角色仅release_artist/album_artist，1专辑/2曲目 |

清单审计：`artistRequiredSamples`包含96个不重复名字，96个仓内文件全部存在，均为完整`{status,body}`信封。12个新增文件分别为1个中间态、2个修正回执、8个新旧词搜索结果、1个修正后艺人结果。仓外完整队列单独计为第97项，不混入仓内96项，也不再以“84项齐全”代替全部场景。

设计中的`catalog/status-legacy`与实际`search/catalog-status-legacy.json`已在`artist-sample-manifest.md`明确映射；旧`search/catalog-status-ready.json`是全就绪，不能代替新增中间态。

### 仓外完整队列

- 路径：`/home/qlqq/.local/share/roomusic-evidence/m5-artists/api-samples/artists/queue-truncated.json`。
- 大小：15,069,139字节。
- SHA-256：`2a363820c76054de51f277968bfc5b86848564112615a944d4e50d6235ab1d08`。
- 生成/比较必须设置`ROOMUSIC_ARTIST_LARGE_SAMPLES_DIR`；常规测试未设置时只运行完整行为断言，不能据此声称外置样本已生成。本次审计已核对设置后的实际写入、比较日志与文件。

## 三、增量测试断言审查矩阵

| 范围 | 证据文件 | 断言审查结论 |
| --- | --- | --- |
| 真实治理后重扫 | `backend/internal/library/scanner_artist_governance_integration_test.go` | 合成FLAC经真实parser、staging、身份规划、候选事务与扫描终态；合并、按专辑拆分、改名、忽略后逐次重扫，比较四意图表、艺人revision、operations及有效图。修改源标题并确认事实更新，排除未真正扫描的假阳性 |
| 失败/取消不做负向对账 | 同上 | 移走合成来源，再由artist_tag_evidence触发器令剩余两个候选失败；非事务回滚序列证明两个候选均触发，断言一条根级诊断、零本轮观察、原3轨仍present、意图/图/主名不变。取消发生在终态对账前，也验证不missing；随后恢复并成功重扫。原“两个候选应有两条诊断”的错误预期已改为真实根级汇总语义，未删除事务断言 |
| M3修正图刷新 | `backend/internal/catalog/governance/artist_correction_integration_test.go` | 通过真实修正命令建立图并执行set、clear及两方向撤销，不直接Refresh掩盖遗漏调用点；核对scan事实保留、有效层、原名/身份、自动主名/人工主名、艺人意图不变及公开摘要/搜索refs |
| M3认领与撤销 | `backend/internal/catalog/governance/artist_claim_integration_test.go` | 使用同一严格投影断言验证源/目标在claim及revert前后有效层、missing可见性、计票、公开搜索与refs；不只验证旧M3字段 |
| 治理范围预算 | `backend/internal/catalog/governance/artist_participation_integration_test.go`及`artist_participation.go` | 发行级全部轨与直接署名轨取并集，重复角色去重、包括missing；真实100000/100001边界，别名/专辑预览、合并计数、新增纯发行参与轨导致basis变化，拒绝后意图/操作/解析端口无副作用 |
| 旧app个人验证 | `backend/internal/app/personal_api_integration_test.go` | 已合法空artist_ids改为明确正例，错误对象仍为400；新增空数组及无Retry-After断言。准备态、null与非空混合请求另有个人HTTP回归，不以合法扩展移除输入验证 |
| 旧app搜索API | `backend/internal/app/search_api_integration_test.go` | 保留exact、字段、原文、角色、发行/曲目来源和可见性；署名artist_id与独立艺人列表/详情核对，非署名仍要求null；新增artists准备态/就绪边界 |
| 旧app分页与建议 | `backend/internal/app/search_pagination_integration_test.go` | 原albums/tracks数量、分页、上限及has_more断言保留；第三栏验证精确身份、角色及跨角色去重的参与数量，没有简单删除两栏旧断言 |
| 旧app安全摘要和排序 | `backend/internal/app/search_projection_integration_test.go` | 新增refs必须为明确数组并逐项验证；只在验证之后归一这两个新增字段，再完整比较原摘要、排序与代表版本。安全字段白名单、nullable音频事实、非署名Match的null断言保留 |
| 统计helper | `backend/internal/platform/postgres/postgrestest/analyze.go`、`analyze_test.go` | allowlist先全部验证，未知/跨schema/视图等请求失败前不执行ANALYZE；标识符来自当前schema中PostgreSQL的format('%I.%I')，重复名单去重。支持固定连接，保留统计读取会话；测试涵盖引号/点、缺表与跨schema隔离 |
| 两个旧规模测试 | `backend/internal/catalog/recent_additions_explain_integration_test.go`、`backend/internal/app/catalog_read_performance_integration_test.go` | 第一阶段仅分析核心表，第二阶段才分析辅助表；reltuples=-1/0、手动统计次数、禁止自动统计、JIT恢复、数量和原性能阈值均保留，没有全schema分析提前破坏第一阶段 |

## 四、实际核验日志与运行归属

以下均来自已存在的仓外文件，本轮仅解析日志，未再次执行这些任务。证据根目录为`/home/qlqq/.local/share/roomusic-evidence/m5-artists`。

| 日志 | 本轮读取到的事实 |
| --- | --- |
| `artist-complete-samples-update.json`及`.exit` | 退出码0，app包PASS 144.995s，无FAIL/SKIP；主套件与独立入口均记录写入完整15,069,139字节仓外样本 |
| `artist-complete-samples-compare.json`及`.exit` | 退出码0，app包PASS 149.086s，无FAIL/SKIP；两个入口均记录完整仓外样本逐字比较成功 |
| `app-race-recheck.json`及`.exit` | 退出码0，app包PASS 502.623s，无FAIL/SKIP；旧app断言修复后的复验，不把首次完整运行的失败日志改写为成功 |
| `scan-artist-governance-recheck.json`及`.exit` | 退出码0，library包PASS 3.975s，无FAIL/SKIP |
| `cross-layer-regression.json`及`.exit` | 该批总体退出码1：governance包PASS 5.089s，scanner首次错误诊断数量断言FAIL；随后scanner单独复验如上。保留失败历史，不能称这一批整体PASS |

上一轮独立检查实际执行并通过以下命令；本轮正式benchmark独占期间未重复执行：

```sh
cd backend
go vet ./internal/catalog/... ./internal/library/... ./internal/operations/... ./internal/personal/... ./internal/app ./internal/platform/postgres/...
go build ./internal/catalog/... ./internal/library/... ./internal/operations/... ./internal/personal/... ./internal/app ./internal/platform/postgres/...
```

此前独立新增3项`TestPostgreSQLArtistGraphReview`普通测试与race分别PASS 3.183s、4.624s，0 SKIP。它们补计票、原文/候选、种子和意图只读等graph局部证据；不冒充真实扫描，真实扫描缺口由第三节所列新测试补齐。

## 五、本轮修改与未验证边界

- 新增本报告；同步`artist-large-samples.md`、`artist-sample-manifest.md`中“尚未生成/比较”的过期阶段表述，记录已查验的文件和日志事实。
- 未修改生产代码、测试断言、数据库、前端、嵌入资源或性能门槛。执行了纯文件JSON清单/形状/计数/唯一性/hash审计及`git diff --check`。
- 正式100k benchmark **FAIL并已停止**：回填期间M3 correction为503 `database_unavailable`，主控报告73.972s，未到p95/EXPLAIN阶段。仓外证据目录为`scale-full-20261104T200228Z`；本轮不推断未证实根因、不修改生产、不重跑测试。不能用race规模正确性、样本生成耗时或旧二栏性能替代⑤正式门槛。
- 前端decoder往返、五宽度、axe、键盘、真实播放与截图不属于本轮授权，未检查或宣称通过。
- `TestRealMusicReadOnlySmoke`、`TestRealMusicFingerprintCoverage`、`TestRealMusicLyricsCoverage`因用户音乐安全边界未运行；不设置`ROOMUSIC_REAL_MUSIC_ROOT`，也不拿合成库冒充这三项真实用户音乐覆盖。

## 六、结束状态

主控已报告失败证据归档、down-v清理确认资源0/0/0及测试环境文件删除。本检查代理不再次连接数据库、编译、测试或修改其他文件，至此结束审查。正式性能验证仍为FAIL，不标记任务整体验收通过。
