# 自动整理 B2：验证记录

日期：2026-11-22。分支 `feat/auto-curation-b2-covers-lyrics`。外部服务全部用受控的本地服务，集成测试在隔离的 PostgreSQL 上用合成数据运行；没有请求真实的外部源。

## 一、验收对照

| 验收项 | 结果 | 依据 |
| --- | --- | --- |
| B2-V1 清单三跳、解析、重定向的拒绝、404 | 通过 | `TestRequesterRedirectPolicy`、`TestCoversManifestTargetsAndCache`、`TestCoversRejectsInvalidTargetsAndReturnsEmptyFor404`、`TestCoversManifestRejectsOtherHostsAndPaths` |
| B2-V2 选图、回退、PDF、图片校验 | 通过 | `TestFrontCoverSelection`、`TestFrontCoverWithoutManifestRelease`、`TestFrontCoverDownloadFallback`；无效图片在 `TestPostgreSQLExternalArtworkApply` |
| B2-V3 清单与 MusicBrainz 共用限速、图片另算 | 通过 | `TestCoverArtLimiters`（默认装配是同一个限速器实例；两个注入的限速器各自计数） |
| B2-V4 LRCLIB | 通过 | `sources/lrclib` 的六个用例 |
| B2-V5 管家工具输出不变、清单经过限速器 | 通过 | 现有两个封面用例（断言未改）与 `c8_cover_art_test.go` 的两个用例 |
| B2-V6 三个源、闸门 | 通过 | `TestPostgreSQLCurationSourcesCoverAndLyricsGate`、`TestPostgreSQLCurationSourcesSettings` |
| B2-V7 读取优先 | 通过 | `TestPostgreSQLExternalArtworkPriority`、`TestPostgreSQLExternalLyricsPriority` |
| B2-V8 重扫、改名移动、认领之后保持 | 通过 | `TestPostgreSQLExternalArtworkRescan` |
| B2-V9 写入的记录与跳过 | 通过 | `TestPostgreSQLExternalArtworkApply`、`…ApplyRollback`、`TestPostgreSQLExternalLyricsApply` |
| B2-V10 撤销 | 通过 | `TestPostgreSQLExternalArtworkRevert`、`TestPostgreSQLExternalLyricsRevert`、`TestPostgreSQLExternalScopeRevert` |
| B2-V11 共享文件与交叠 | 通过 | `TestPostgreSQLExternalArtworkRevertSharedFile`、`TestPostgreSQLExternalConcurrent` |
| B2-V12 操作记录的摘要 | 通过 | `TestPostgreSQLExternalOperationList`、`TestPostgreSQLExternalStewardRevert`；前端 `change-summary.test.ts` 与样本 |
| B2-V13 现有样本不变 | 通过 | `TestPostgreSQLAPIResponseSamples`：226 份样本里只有 `curation/sources` 变化，新增三份 |
| B2-V14 迁移往返、依赖方向 | 通过 | `TestPostgreSQLMigrationRollbackRoundTrip`（31 个版本，含 0061）、`TestPackageDependencyDirections` |
| B2-V15 变异检查 | 通过 | 13 个变异全部被用例发现（下表） |

## 二、本轮实际运行的命令

主代理在两条线合并、测试都写完之后运行（最终状态）：

| 命令 | 结果 |
| --- | --- |
| `cd backend && mise exec -- go build ./... && gofmt -l ./internal && go vet ./...` | 通过，无输出 |
| `go test ./internal/sources/... ./internal/steward/web/... ./internal/catalog/artwork/... ./internal/catalog/metadata/... ./internal/library/parser/... -count=1` | 通过 |
| 集成：`TestPostgreSQLExternal\|TestPostgreSQLCurationSources\|TestPackageDependencyDirections\|TestPostgreSQLAPIResponseSamples\|TestPostgreSQLRunAfterCommit\|TestPostgreSQLArtworkDescribeWriteDiscard\|TestPostgreSQLC8` | 通过（39 个顶层用例） |
| `cd frontend && npx tsc --noEmit`、`npx vitest run src/test/api-contract.test.ts src/features/governance src/features/player/api`、`src/app/routes/use-release-refresh.test.tsx`、eslint | 通过（接口合同 936 项通过、1 项跳过是既有的） |

沿用子代理的结果（之后相关代码没有改过，没有重跑）：

| 范围 | 执行者 | 结果 |
| --- | --- | --- |
| 迁移回退往返 31 个版本 | 存储线实现与测试子代理各一次 | 通过 |
| 现有自动整理用例（`TestPostgreSQLCuration*`）、封面与扫描封面相关用例，共 51 个 | 存储线测试子代理 | 通过 |
| 目录读取的门禁：四个 EXPLAIN 用例与 `TestPostgreSQLCatalogHTTPPerformance`（显式启用，实际执行） | 存储线测试子代理 | 通过；最近新增 p95 71–72 ms（阈值 150），按加入时间首页 p95 125–137 ms（阈值 300） |

没有运行：后端与前端的全量测试；Playwright；真实外部服务。

## 三、变异检查

| 变异 | 发现它的用例 |
| --- | --- |
| 去掉重定向的主机判断 | `TestRequesterRedirectPolicy/host` |
| 去掉清单跳转的 `/index.json` 限制 | `TestCoversManifestRejectsOtherHostsAndPaths/not_index` |
| 类型含 Front 的图压过 `front` 标记 | `TestFrontCoverSelection/front_flag_over_type` |
| 非 404 的失败继续试下一档 | `TestFrontCoverDownloadFallback/non_404` |
| `GetBytes` 写了响应缓存 | `TestRequesterGetBytesDoesNotCacheResponse` |
| 清单改用自己的限速器 | `TestCoverArtLimiters` |
| 摘要的封面只取本地列 | `TestPostgreSQLExternalArtworkPriority` |
| 去掉 `local_present` 判断 | `TestPostgreSQLExternalArtworkApply` |
| 去掉 `occupied` 判断 | `TestPostgreSQLExternalArtworkApply` |
| 撤销不核对归属 | `TestPostgreSQLExternalArtworkApply` |
| 引用计数只数本地表 | `TestPostgreSQLArtworkDescribeWriteDiscard` |
| 提交失败时也调用 `AfterCommit` | `TestPostgreSQLRunAfterCommit` |
| 不再提出只查内容 | `TestPostgreSQLExternalArtworkRevert` |

变异用编译器的替换文件注入，工作目录里的实现没有被改动；两个测试子代理各自核对了实现文件前后一致。

## 四、过程中的发现与处理

- 外部源线实现的首轮：启用跳转的请求器在跳转被拒绝时返回了裸的出站错误，破坏了“后台只会得到约定的几种错误”；返工为同时满足失败哨兵与 `egress.ErrDenied`。
  歌词搜索在艺人为空时发了空参数，一并改掉。
- 外部源线测试报告的一条“缺陷”（清单 404 时工具输出没有 `covers` 键）是既有行为：`covers` 一直是 `omitempty`。测试改为断言既有行为。
- 前端：两类操作的撤销记录带的是被撤销的那一份（不分前后），列表按记录类型把它写成“去掉……”。
- 两条实现线并行时共用文件的合并只有 `app.go` 一处冲突，手工合并。

## 五、没有验证的事

- 对真实的 Cover Art Archive、archive.org 与 LRCLIB 没有发过请求：合并过的发行、缺失的缩略图、archive.org 的限流返回码、LRCLIB 对华语的命中率，留到父任务收尾的真实试用。
- 操作记录页上两类新记录的文字没有截图复核（只有文字行，由单元测试与样本固定）；整理记录页在 E1。
- 本任务没有生产调用方：什么时候取封面与歌词由子任务 C 接上之后才有端到端的行为。
