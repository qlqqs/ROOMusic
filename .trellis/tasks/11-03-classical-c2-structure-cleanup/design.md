# C2 设计

依据两份只读分析（2026-11-03）。行号以分析时的 `main`（`641bdef`）为准，实施时以实际代码为准。

## S1 `internal/catalog` 拆包

### 分组（根目录非测试代码约 7.7k 行）

| 包 | 文件 | 说明 |
| --- | --- | --- |
| `catalog`（核心读取） | `read`、`projection`、`groups`、`comparison`、`lyrics`、`track_source`、`track_placement`、`issues`，新增 `fields.go` | `issues.go` 必须留在核心：`read.go` 的“需要关注”表达式引用它的 `releaseOpen*` 常量 |
| `catalog/ingest`（扫描写入） | `observation`、`organizer`、`folder_metadata`、`paths`、`scope`、`staging`、`identity`、`identity_plan`、`persist`、`persist_track` | 组外只依赖 `Queryer`（`StagingDB` 嵌入了它）；反向零依赖 |
| `catalog/governance` | `corrections`、`group_change`、`group_default`、`issue_review`、`issue_read`、`claims`、`claim`、`claim_read`、`claim_revert`、`revert` | 组内耦合紧；`issue_read`、`claim_read`、`claims` 不能归到读取组。catalog 里只有这一组导入 `operations` |
| `catalog/internal/releasesql` | 从核心移来的 SQL 片段与纯格式化函数 | Go 的 internal 规则保证只有 catalog 子树能导入 |

`catalog/httpapi` 不拆：handler 增加 `governance *governance.Store` 字段，治理相关调用改走它。

### 关键决定

1. **`fields.go` 先下沉**：`correctableFields`、`CorrectableFieldNames`、`CorrectionIntent` 与 `CorrectionSet/Null/Clear`、`loadCorrectionIntents`、
   `loadCorrectedFieldNames` 从 `corrections.go` 移到核心，因为 `read.go`、`comparison.go` 在用。否则核心要导入治理，形成循环。
2. **`StagingDB` 不再嵌入 `catalog.Queryer`**：直接写出那两个方法，方法集不变，`ingest` 因而不导入 `catalog`。
3. **治理方法换接收者**：`ListClaims`、`LoadClaim`、`LoadClaimHints`、`ListIssues`、`LoadReleaseIssues`、`LoadCorrectionState`、
   `PreviewClaim`、`LoadIssueBasisToken`、`LoadClaimSuggestions` 现在挂在 `*catalog.Store` 上，改挂到 `governance.Store`（`governance.NewStore(catalog.Queryer)`）。
   它们只用 `s.database` 与本组方法。
4. **哨兵错误不重复定义**：治理继续返回 `catalog.ErrQuery`、`catalog.ErrReleaseNotFound` 等，httpapi 的 `errors.Is` 不受影响。
5. **公开面**：治理需要核心约 39 个原先未导出的符号。20 个 SQL 常量和 6 个纯格式化函数进 `releasesql`，`catalog` 自身只新增约 10 个
   （摘要列扫描 `releaseSummaryColumns` 及其方法、`maxEvidence*`、字段词表等）。具体名单以实施时为准，PRD 的 A3 要求“必要且最少”。
6. **SQL 逐字节不变**：只改常量名与所在包，不改拼接方式；接口样本与集成测试兜底。
7. **测试夹具复制而不是抽包**：治理测试依赖链式夹具（`newPlacementFixture` → `newEffectiveFixture` → `newCorrectionFixture`）和
   `issueCounts` 等辅助，全部是裸 SQL。在 `governance/fixture_integration_test.go` 复制约 200 行；核心的 comparison 测试改用 `effectiveFixture`。
   抽测试专用包要把几百处调用改成导出名，不划算。

### 依赖规则（`dependency_rules_test.go`）

| 包 | 允许的内部导入 |
| --- | --- |
| `catalog` | `catalog/internal/releasesql`、`platform/uuid` |
| `catalog/internal/releasesql` | 无 |
| `catalog/ingest` | `catalog/metadata`、`platform/uuid` |
| `catalog/governance` | `catalog`、`catalog/internal/releasesql`、`catalog/metadata`（如确实需要）、`operations`、`platform/uuid` |
| `catalog/httpapi` | 现有规则加 `catalog/governance` |
| `library` | 现有规则加 `catalog/ingest`（读取侧的 `SafeRelativeSourceRef` 仍来自 `catalog`） |
| `app` | 现有规则加 `catalog/governance`（5 个 `*Operations` 改从治理包取） |

## S2 播放引擎显式状态

### 现有字段的真实状态

- **挂载生命周期**：`attached`、`finished`、`restoreStarted` 合起来只有四种可达状态。
- **恢复阶段**：`restoring` 与 `restoreRetry` 互斥；`touched` 只增不减，保留为独立布尔值。`fault` 可以比重试状态活得久，不能从恢复阶段推出来。
- **加载管线**：`preflight` 与 `pollTimer` 互斥；`loadedEntryId` 与 `expectedSrc` 同设同清；`pendingSeek` 只在媒体已加载时非 null；
  预检在途时一定没有媒体。`status` 与这些字段耦合（`preparing` 时没有媒体，`playing`/`buffering` 时一定有媒体）。

### 目标模型（公开 API 不变）

```ts
type Lifecycle = "fresh" | "attached" | "detached" | "ended";
type RestorePhase = { kind: "none" } | { kind: "running" } | { kind: "retryable" };
type Pipeline =
  | { kind: "stopped"; status: "idle" | "paused" }
  | { kind: "requesting"; status: "loading" | "preparing"; sentAt: number;
      wait: { kind: "inflight"; controller: AbortController } | { kind: "timer"; handle: ReturnType<typeof setTimeout> } }
  | { kind: "media"; status: "loading" | "playing" | "paused" | "buffering";
      entryId: string; src: string; pendingSeek: number | null };
```

保持独立、不并入状态的字段：`playedVisitEntryId`、`resumePosition`、`progress`、`prefetch`、`persistTimer`、`attempt`（仍是计数器，
`pause()` 会在不卸载媒体时递增它）、`fault`、`touched`。“`preparing` → `loading` 先 commit 一次再设 `src`”这个中间快照必须保留。
符合 `.trellis/spec/frontend/type-safety.md` 对生命周期状态使用可辨识联合加穷尽 `switch` 的要求；`state-management.md` 的快照字段、
detach 与 endSession 的区分、保存键合同不变。

### 拆出的模块（同目录）

| 文件 | 内容 |
| --- | --- |
| `player-engine.ts` | 公开类型与类；继续导出原有全部类型，导入路径不变 |
| `engine-state.ts` | 三个联合类型与纯辅助函数 |
| `playback-failure.ts` | `failureReasonOf`、`isDerivationBusy`、`errorName`、`MEDIA_ERR_*` |
| `queue-restore.ts` | 按 4 并发读取发行、用详情重建队列的纯函数 |
| `media-session-binding.ts` | 媒体会话绑定与位置上报节流 |
| `prefetcher.ts` | 预取下一首 |

预检轮询与 `attempt`、`status` 耦合太紧，留在引擎里。`savedEntriesOf` 的缓存可以并入已有的 `persistence.ts`。

### 先补的特征测试（在旧实现上通过）

1. attach 之前 `playTracks` 再 `endSession`：预检的 signal 被中止，`src` 被移除。
2. attach 之前就有命令时恢复停在 `restoring`（固定现状）。
3. 恢复进入可重试状态后 `enqueue`：fault 仍是 transient，`retry()` 加载当前项，保存恢复。
4. “正在准备”的定时等待期间 `detach`：不再发预检，status 为 paused。
5. 预检或准备期间 `stop()`：预检被中止，位置为 0，status 为 paused。
6. 用 `subscribe` 记录 status 序列：普通加载、202 → 就绪、媒体错误再分类、单曲循环四条路径（抓多 commit 或少 commit）。
7. 准备轮询期间收到 401：paused、没有 fault、位置保留。
8. 单曲循环重播被 NotAllowed 拒绝后定时保存的现状（固定现状）。
9. 媒体错误与 `play()` 拒绝同时到达时只走一条分类链。

## S3 `parser.go` 拆文件

同包内拆分，导出零变化，测试文件不拆：

| 文件 | 内容 |
| --- | --- |
| `parser.go` | 包文档、`AudioObservation`、常量、`ParseAudioFile` 分发 |
| `tags.go` | 通用标签映射、credits、`parseNumber`、`ParseYear`、文件名兜底、码率估算 |
| `m4a.go` | atom 遍历、stsd、ilst 与 freeform、文本解码 |
| `cue.go`、`cue_text.go`、`cue_path.go` | CUE 解析与曲目区间；编码判别；路径边界检查 |
| `flac.go`（含 Vorbis 注释）、`ogg.go`、`wav.go`、`mp3.go` | 各格式 |

## 回退

每一步单独提交；任何一步验收不通过就 `git revert` 该步，不影响前面的步骤。
