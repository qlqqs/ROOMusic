# O 线合入 main 前冲突整合（2026-11-08）

## 边界与状态

在现有 `git merge --no-commit --no-ff main` 上整合，不提交、不终止合并、不 reset，不操作其它 worktree。不启动数据库、不构建前端产物，数据库事务与迁移往返留给验收 agent。任务保持进行中。

## 冲突处理

- `library-scan-contracts.md` 原地合并：保留两侧所有新增主题章节；四轮封面顺序采用 O 线规则，来源校验与失败分类保留 main 的 `ValidateImage` / `errArtworkSource` / `artwork_unreadable`，不把源图片错误混成受管存储错误。保留 MP3 时长、新格式指纹、过滤与保住、改路径，以及碟目录、文件名、版本同组和收藏转移合同。不拆分文件。
- `scanner.go`：保留过滤、单文件失败保住、改路径的根可用性复检；扩展名只由 `scan_filter.go` 维护，保留 APE/WV/AIF/AIFF。CUE 对齐后标记碟父目录，再规划身份；保存时保留 `PlannedVersionGroup` / `ReleaseGroupOf` 和 `persistOneCandidate` 返回 Release ID。保存后先 touch 保住来源，再执行腾空组收藏转移，原失败和取消语义不变。四轮封面发现和 main 的安全验证、非致命来源错误分类同时保留。
- `migrations_rollback_integration_test.go`：保留 main 的 34–36 夹具及断言，O 的空夹具改为 37。0035 回退仍不撤销已改路径或 revision。
- 自动合并遗留编译问题：`evidence_test.go` 的 `minimalJPEGArtwork()` 改为 `minimalJPEGArtwork(t)`，与 main 的真实图片夹具签名对齐。
- `artistgraph.Version` 保持 3；未改其它版本常量。

## 迁移改号

- `backend/migrations/0037_scan_observation_previous_release.sql`
- `backend/migrations/0037_scan_observation_previous_release.down.sql`
- `backend/internal/platform/postgres/migrations_rollback_integration_test.go`
- `.trellis/spec/backend/library-scan-contracts.md`
- `.trellis/spec/backend/database-guidelines.md`
- `.trellis/tasks/11-08-scan-folder-names/design.md`
- `.trellis/tasks/11-08-scan-folder-names/implement.md`
- `.trellis/tasks/11-08-scan-folder-names/implement.jsonl`
- `.trellis/tasks/11-08-scan-folder-names/check.jsonl`

两份 SQL 仅重命名，已逐字节对照 O 线备份；迁移编号唯一，最高为 37。旧迁移文件名检索无残留。

## 交叉点

- 改路径仅更新根的路径/revision，根 ID 与来源相对路径不变；相同内容沿用曲目/发行，`previous_release_id` 等于当前发行，不进入 `group_moves` 的移动集合，也就不会触发收藏转移。组内仍有任何曲目（含 missing）时本来就不转移。该项是静态审查结论，未替代数据库验收。
- 常规目录遍历的过滤在音频暂存之前，系统/隐藏/排除目录不产生普通音频观察，不会被当作碟目录；附属封面仅接受 `scans/artwork/art/covers`，内置系统目录不在名单中。
- **需产品裁决，未修改规则**：封面发现独立读取目录，不接受 `scanFilter`；用户排除 `Album/scans` 后，第三/四轮仍可能读取该目录封面；第二/四轮的 `front` 包含匹配也会读取 `.front.jpg`。这是过滤范围与附属封面发现的交叉边界，不能因文本合并自行扩大或缩小过滤范围。

- APE/WV/AIF/AIFF 均沿用 `ParseAudioFile` → `buildPhysicalSourceObservation` → organizer 的来源标记，文件名推断有效。新增合成素材单测覆盖推断碟轨/标题/艺人及来源、APE/WavPack/AIFF 按 codec 分为三个版本；AIF/AIFF 不因扩展名不同被拆开。
- **既有缺口，未自行修复**：`catalog/ingest/staging.go:211–218` 的独占判断只看成功暂存观察，没有考虑 held/未知范围。例如 `艺人/某精选 CD1/` 正常，旁边另一专辑全部读取失败，父目录可能被判独占，使候选上提、封面搜索扩大到艺人目录。合并前 O 线即使标 incomplete，仍会保存未暂缓的正常候选，因此并非本次合并新引入。main 现在保住失败来源并允许成功扫描，但独占缺口仍在。应另裁决“held/未知范围是否阻止独占”，并由验收 agent 补针对性数据库回归。

## 验证

后端工作目录为 `backend/`：

```bash
gofmt -l ./internal/catalog ./internal/library ./internal/platform/postgres ./internal/app
go build ./...
go vet ./internal/catalog/... ./internal/library/... ./internal/platform/postgres/... ./internal/app/...
env -u ROOMUSIC_TEST_DATABASE_URL go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1
env -u ROOMUSIC_TEST_DATABASE_URL go test ./internal/library/... ./internal/catalog/ingest/... -count=1
```

构建通过。首次 vet 被上述 JPEG 夹具调用阻断，修复后 vet、依赖方向测试与指定包测试通过；数据库测试因未配置 URL 跳过，不能算数据库验收通过。

前端工作目录为 `frontend/`：

```bash
npx vitest run src/features/catalog/labels.test.ts
npm run typecheck
```

标签测试 18/18 通过，类型检查通过。未运行 `npm run build`，未修改 merge 已带入的 `backend/cmd/roomusic/web/` 产物。

补充：`scanner_merge_test.go` 覆盖四轮来源错误分类与第四轮有效封面替补；初次单独运行同样被 JPEG 夹具调用阻断，修复后已纳入上面的 library 包测试通过。

未跑全量测试：本轮仅处理合并交点，按用户指定最小范围执行；集成验收另行进行。

新增格式用例增量验证（通过）：

```bash
cd backend
env -u ROOMUSIC_TEST_DATABASE_URL go test -count=1 ./internal/library -run '^TestScannerMergeFormats'
go vet ./internal/library
```

收尾检查：`git diff --check`、`git diff --cached --check` 通过；`git diff --name-only --diff-filter=U` 为空，三处冲突已标记解决；`MERGE_HEAD` 仍在。最终 `gofmt -l` 指定四个目录无输出。未执行会自动提交的任务归档/日志脚本。
