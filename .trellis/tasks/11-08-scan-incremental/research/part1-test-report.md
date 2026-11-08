# ④ 增量扫描第一部分测试记录

日期：2026-11-08。

## 范围

仅新增或修改测试、合成夹具、API 样本及本记录，没有修改实现代码，没有提交 git。第 6 步候选签名、候选状态和候选跳过不在本轮测试范围；不将候选每轮重写误报成失败。不访问真实音乐或生产环境。

## 测试与夹具

- `backend/internal/library/parser/version_test.go`：已知扩展名、未知扩展名及大小写的 `CacheVersion`。
- `backend/internal/library/scan_incremental_integration_test.go`：连续重扫解析为零、曲目/Release 身份与来源事实不变、missing 为零和报告零变化；标签/mtime 改动仅解析一轨；新增轨后的延迟内嵌封面；第二个 CUE 父文件的内嵌封面；无封面 CUE 缓存重扫不打开父音频；完整遍历清理已删除文件的缓存。
- `backend/internal/library/scan_incremental_open_linux_test.go`：独立监控实际音频打开，避免只凭解析摘要遗漏 CUE 时长旁路读取；inotify 实例耗尽时使用已安装的 strace，限定直接子进程附加授权、独立校准成功后计数并回收进程。非 Linux helper 明确说明独立证据的适用平台。
- `backend/internal/library/scan_version_incremental_integration_test.go`：第一扫建立缓存后，第二个进程用 Go `-overlay` 真正提高 `.flac` 格式版本或 `GroupingVersion`，复用同一隔离 schema。格式升级解析 1 个文件、命中另一个格式；归组升级解析 0 个、命中 2 个。实现文件没有被改写。
- `backend/internal/library/scan_borrow_full_integration_test.go`：目录改名保留 UUID/Release，借用指纹且无整段读取；旧侧、新侧、两侧同名/同大小/同时间歧义都不借用；完整重扫解析全部，移动文件不借用，同来源文件仍复用已存指纹，能读出保留 size/mtime 的标签变化。
- `backend/internal/app/scan_mode_integration_test.go`：完整模式持久化及启动/活动/详情/列表/取消 DTO；已有扫描不被另一模式覆盖；空请求体与显式 incremental 均命中缓存；非法请求返回 400 且不创建扫描。
- `identity_rebinding_integration_test.go`：只把 `canceled while planning` 子用例改为 `scanRootWithOptions(..., StartOptions{Mode: ScanModeFull})`，终态与身份断言原样保留。
- `migrations_rollback_integration_test.go`：增量迁移（现编号 0038）往返增加实际解析缓存及 full 扫描夹具，保留升级前业务数据，验证升级→回退→再升级。
- API 样本：真实 handler 重生主样本 212 个、扫描反馈样本 59 个，其中 39 个 JSON 文件实际变化，均仅增加 `mode`。样本测试及共享 `runScan` / `startScanFeedback` 夹具的启动请求从 `{}` 改为空请求体，符合已实现的严格 mode 合同。
- 前端 `api/decoders.test.ts`、`api/endpoints.test.ts`、`api/scan-filter-decoders.test.ts`、`components/scan-section.test.tsx`：mode 合法/缺失/非法；手写对象补 mode；两按钮的实际请求体及 Content-Type；202/200；失败后按原模式重试；记录和详情标签。

## 实际验证命令

PostgreSQL 全程只用 `compose.test.yaml` 的 `roomusic-test-scan-s` 项目、随机本机端口和随机隔离库；Compose 使用现有默认 bridge，没有新建自动子网网络。已配置 `ROOMUSIC_TEST_DATABASE_URL`、固定 ffmpeg 与 PostgreSQL 客户端。没有运行 `scripts/test-integration.sh`。

以下命令在 `backend/` 执行；保留 `ROOMUSIC_TEST_DATABASE_URL` 环境。

```bash
ROOMUSIC_UPDATE_API_SAMPLES=1 go test ./internal/app -run 'TestPostgreSQL(APIResponseSamples|ScanFeedbackAPIResponseSamples)$' -count=1 -v
# 主样本通过后，反馈样本暴露共享夹具仍发 {}；修正该夹具后只重跑失败项：
ROOMUSIC_UPDATE_API_SAMPLES=1 go test ./internal/app -run '^TestPostgreSQLScanFeedbackAPIResponseSamples$' -count=1 -v

go test ./internal/library/parser/ -run 'CacheVersion' -count=1 -v
go test ./internal/library/ -run '^TestPostgreSQLParseCache(Format|Grouping)VersionUpgrade$' -count=1 -v

go test ./internal/platform/postgres/ -run '^TestPostgreSQLMigrationRollbackRoundTrip$' -count=1 -v

REGRESSION=$(sed -n 's/^func \(Test[^ (]*\).*/\1/p' internal/library/{identity_rebinding,scan_lifecycle,scanner_artwork}_integration_test.go | paste -sd'|')
go test ./internal/library/ ./internal/catalog/ingest/ -run "Incremental|Borrow|FullRescan|IdentityRebinding|ScanLifecycle|ArtworkIntegration|^(${REGRESSION})$" -count=1 -v

# 仅重跑修正过夹具/监控的三个失败项；其余通过项不重复验证。
go test ./internal/library/ -run '^TestPostgreSQLIncrementalParseCache(TagMtimeChangeOnlyReparsesChangedFile|CueSecondParentKeepsEmbeddedArtwork|CueWithoutArtworkDoesNotOpenParents)$' -count=1 -v

go test ./internal/app -run '^TestPostgreSQLScanMode' -count=1 -v
go test ./internal/app -run '^TestPackageDependencyDirections$'
go vet ./internal/library/... ./internal/catalog/ingest/
```

版本升级测试单独运行；其余解析缓存测试名均含 `Incremental`，上述联合命令不重复已通过的升级测试。不运行候选签名测试，也不创建候选状态/跳过测试。联合命令对 ingest 没有匹配用例，输出 `[no tests to run]`，不把它当作 ingest 数据库用例通过。

前端：

```bash
cd frontend
npx vitest run src/features/library
npm run typecheck
```

前端 11 个文件、287 个测试 PASS；typecheck PASS。仅有 jsdom 媒体方法未实现警告。

未运行全量：本轮只验证直接影响的扫描、身份、封面、迁移、API 和前端 library 范围，遵守用户的最小测试约束。

## 结果与问题

- CacheVersion 单测、真实版本升级 PostgreSQL 测试、模式 API PostgreSQL 测试、迁移 0031–0035 及增量迁移（现编号 0038）改号前的往返、包依赖方向、go vet 已 PASS，不是 SKIP。
- 扫描联合验证最初 39 个顶层测试 PASS、5 个 FAIL、0 SKIP。3 个新增用例失败来自夹具/监控环境，修正后定向重跑全部 PASS，详见下文；两个原回归失败未改断言、未改实现。因此全部新增测试已 PASS，既有回归不能宣称全绿。

### 1. 三文件轮换的身份回归

复现：

```bash
go test ./internal/library -run '^TestPostgreSQLSwappedFilesFollowContent$/^three_files_rotate$' -count=1 -v
```

原用例的三份合成 FLAC 标签等长、大小相同，紧邻创建可能具有相同的微秒 mtime。首扫后循环改名，旧路径上的 size/mtime/解析版本仍能命中旧缓存，读取的 FLAC 内容指纹没有跟随实际内容改变。

- 期望：UUID 跟音频内容走，旧断言 `b→旧a、c→旧b、a→旧c` 继续成立。
- 实际：联合测试中 `three_files_rotate` 的第一个 UUID 断言失败。
- 定位证据：仅测试侧 overlay 将三份文件首扫前的 mtime 固定为 `2026-11-08T00:00:00Z`；三份文件 size 均为 191 字节，轮换后直接解析得到三个不同 FLAC 内容指纹，原身份断言仍失败。没有覆盖或修改生产实现。该诊断仅用于定位失败，不作为通过证据。
- 原因：当前缓存的 stat 命中条件无法区分同大小/同 mtime 的内容置换。虽然设计已接受“保留 mtime 的内容变化需完整重扫”的局限，这次局限触及既有身份回归；需要主会话裁决，不能通过放宽断言假装 R6 全通过。

### 2. 跨 root 移动与空根保护的旧合同冲突

复现：

```bash
go test ./internal/library -run '^TestPostgreSQLCrossRootMoveGetsNewIdentity$' -count=1 -v
```

- 操作：把来源 root 唯一一首音频移动到另一个 root，原 root 变空。
- 原测试期望：扫描 succeeded，旧轨 missing，目标 root 产生新身份。
- 实际：原 root 返回 `empty root`，整次终态 incomplete，不执行 missing。
- 当前实现和 `HEAD` 均已有①的空根保护；现行扫描合同也明确要求该场景 `root_empty/incomplete`。因此这是①与旧回归夹具/预期冲突，不是④新引入的缓存或借用实现错误。本轮保留原断言，留给主会话处理。

### 新增测试的局部修正

- CUE：每父仅一轨会触发现有单轨合并规则。改为每父两轨，确保真正验证四条 CUE 虚拟轨及第二父封面；不改业务实现。
- 文件打开监控：本机 inotify 的用户实例额度耗尽，初次出现 `too many open files`，不能当作未打开音频的证据。增加经校准的 strace 实际系统调用监控后，三个局部重跑 PostgreSQL 用例全部 PASS（2.00s、2.00s、1.92s），没有 SKIP；无封面 CUE 的两个父文件实际打开次数为 0，带封面 CUE 的第一父文件也为 0。监控权限或校准失败会明确失败，不返回伪造的零计数。

## 清理

已执行并成功退出（退出码 0）：

```bash
docker compose --env-file /dev/null -f "$WT/compose.test.yaml" -p roomusic-test-scan-s down -v
```

输出明确确认 `roomusic-test-scan-s-postgres-1` 容器及 `roomusic-test-scan-s_roomusic-test-postgres-data` 数据卷均已 Removed。未创建额外 Docker 网络。测试侧身份诊断 overlay 临时目录已清除。

## 收尾

- 独立 Trellis 检查静态审阅通过（包含版本 overlay、前端、迁移和 strace 回收/授权）；没有改实现来掩盖失败，也没有修改既有身份回归断言。
- 本轮没有新增或改变产品合同；现有规范已包含严格 mode、缓存命中条件和空根保护，因此不额外修改规范。上述两个失败的裁决留给主会话。
- 因用户禁止提交，未调用会自动提交的归档或会话脚本；第 6 步仍未实施，任务保持进行中。

## 验收后修订

- 主会话验收认为 inotify/strace 监控过重，已删除 `scan_incremental_open_linux_test.go` 与 `scan_incremental_open_other_test.go`。三个用例改为首扫后把不应读取的音频设为不可读（`Chmod(0)`，以 root 运行时跳过），增量扫描仍须成功、零诊断、零跳过且身份不变；三例 PostgreSQL 重跑 PASS，0 SKIP。上文关于 strace 的描述以此为准。
- `three_files_rotate` 首扫前给三个文件设不同修改时间；`TestPostgreSQLCrossRootMoveGetsNewIdentity` 在来源目录多留一首不动的音频。两例重跑 PASS。

## 2026-11-08 合并改号说明

增量迁移使用线内连续编号 `0038_incremental_scan{,.down}.sql`；若 T 线先合入 main，由主会话统一改号。此前跨号方案触发迁移连续性校验，已撤销该方案；不补空迁移、不改加载器或回退执行器。以上数据库 PASS 是原编号时的历史验收，不代表当前合并树已通过数据库验证。本轮不启动数据库，实际改号验证见 [合并记录](./merge-main-integration.md)。
