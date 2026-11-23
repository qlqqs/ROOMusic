# B3 验证记录（2026-11-23）

日志在 `temp/archive/2026-11-23/`（本地，不提交）。下文说明每一项是“本轮实际运行”还是“沿用之前的结果”。

## 后端

| 范围 | 命令 | 结果 |
| --- | --- | --- |
| 全部后端单元测试 | `cd backend && mise exec -- go test ./...` | 通过：修复与测试改动之后、合入之前由主代理运行 |
| 新增单元用例 | `go test ./internal/sources/acoustid ./internal/curation/program -run 'TestB3\|TestLookupFingerprint'` | 10 个通过，78 个子测试（测试代理）；修复后 `TestB3` 由修复代理复跑并通过 |
| 摘要基准 | `TestB3DigestBaseline`、`TestB3MatchWithoutFingerprint` 在 `main`（`97174a96`）的分离工作树上 | 都通过：B3 前后的摘要相同，不带指纹时的匹配结果与之前相同 |
| B3 集成用例 | `ROOMUSIC_TEST_RUN="^TestPostgreSQLB3"` | 第一次运行 7 个里 5 个通过；2 个失败是产品缺陷（短轨取整、候选分数），已修复。修复后受影响的 2 个由修复代理复跑并通过；全部 7 个在下一行的回归里再跑一遍并通过 |
| 回归：B3、自动整理、管家 | `GOFLAGS=-p=1 ROOMUSIC_TEST_RUN="^TestPostgreSQL(B3\|Curation\|Steward)"` | 168 个通过，2 个失败：B1 的旧用例断言恰好三个源。现在是四个源，期望随之更新后这 2 个复跑并通过 |
| 音频分析（取源函数抽取后） | `^Test(FFmpegAnalysis\|PostgreSQLAnalysis\|AnalysisWorker)` | 11 个通过（测试代理，在修复之前运行；修复没有改分析相关的代码） |
| 接口样本 | `TestPostgreSQLAPIResponseSamples` 加 `ROOMUSIC_UPDATE_API_SAMPLES=1` | 通过。主代理补了错误样本的请求编号后重新生成；与 `main` 相比变化的只有 `curation/sources`，另新增三份样本 |
| 完整的默认集成测试 | `./scripts/test-integration.sh`（`TestPostgreSQL\|TestFFmpeg`） | 见文末 |

## 前端

| 范围 | 命令 | 结果 |
| --- | --- | --- |
| 类型与规范 | `npm run -s typecheck`、`npx eslint src e2e` | 通过 |
| 全部 Vitest | `npx vitest run` | 4564 个通过，1 个失败：`agent-settings.test.tsx` 的改名用例。这个文件 B3 没有改动，单独重跑 36 个全部通过，属于满负载时的超时 |
| 自动整理页面与样本契约 | `src/app/curation-settings.test.tsx`、`src/test/api-contract.test.ts` | 通过；新增 5 个 AcoustID 用例，新样本经解码器往返无损 |
| Playwright 冒烟 | `npx playwright test e2e/curation.spec.ts` | 5 个通过：1440、375、390 三种宽度，两把密钥的填写与保存，没有横向溢出，axe 检查通过 |
| 截图复核 | `/tmp/roomusic-b3-sources-390.png` | 各行开关对齐，从属行布局正确；无头浏览器没有中文字体，只核对了布局 |

## 部署

| 范围 | 结果 |
| --- | --- |
| 生产镜像构建（amd64） | 通过。`fpcalc version 1.6.2`；这一层 5.42 MB；镜像共 260 MB |
| 容器启动 | 用独立的 Compose 项目 `roomusic-b3check`、命名卷与独立端口启动 `up -d --wait`：两个容器都健康。打开总开关后 AcoustID 显示 `key_missing`，说明镜像里的指纹自检通过。项目、卷、网络、镜像与目录都已清理 |
| `scripts/check-production-compose.sh`、`scripts/fpcalc-pinned.sh`、`--pins` 在 Dockerfile 里的核对 | 通过（实现代理） |

## 没有执行的

- arm64 镜像：本机没有 arm64，只能在发布流水线的构建阶段核对版本。
- 真实的 AcoustID 与 MusicBrainz：留给父任务的 V12。全部测试都用受控服务，没有用真实密钥。
- 自检成功的日志：已加上，并由修复代理核对；但上面的容器启动是在加日志之前做的，没有在容器里再看一次。

## 完整的默认集成测试

`sg docker -c 'mise exec -- ./scripts/test-integration.sh'`（没有加 `GOFLAGS=-p=1`，各包同时跑）：36 个包里 33 个通过。另外 3 个包里有 5 个用例失败，都是 `context deadline exceeded`：

- `TestPostgreSQLAPIResponseSamples`
- `TestPostgreSQLConcurrentScanMatchesSerialStagingDiagnosticsAndCache`
- 三个 `TestPostgreSQLMigrationPreBackup*`

这 5 个加上 `GOFLAGS=-p=1` 串行重跑，连同同组的第 4 个迁移用例，全部通过。B3 没有改库扫描与迁移包的代码，所以判断是负载导致的超时。
迁移 0063 的升级与回退往返（`TestPostgreSQLMigrationRollbackRoundTrip/0063_acoustic_fingerprints.sql`）通过。
