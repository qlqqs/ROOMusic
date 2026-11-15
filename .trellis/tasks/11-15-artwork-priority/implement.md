# 执行计划

## 0 准备

- 分支：从 `main` 新建 `feat/artwork-priority`。工作区干净，直接在主工作目录切分支。
- 分工（按全局约定）：后端实现与测试交给 Codex 子代理，实现和测试由不同代理完成，实现代理不写新测试。子代理不提交 Git。
- 派活说明必须包含：
  - 任务目录 `.trellis/tasks/11-15-artwork-priority/`（prd.md、design.md）；
  - `implement.jsonl` 中列出的规范；
  - 工作目录与分支；
  - 本文件对应阶段的验证命令。

## 1 阶段 A：解析层（gpt-6.1-sol，high）

范围是 design §2，只改 `backend/internal/library/parser/`。

- [ ] A1 新增 `offerArtwork` 和 `artworkFront`；APIC、M4A、APEv2 改走统一入口
- [ ] A2 新增 `parseFLACPicture`；`parseFLAC` 处理块类型 6
- [ ] A3 `parseVorbisComments` 处理 `METADATA_BLOCK_PICTURE`，并放宽这个键的长度上限
- [ ] A4 WAV 处理 `id3 ` 块，INFO 只补缺失值
- [ ] A5 `CommonVersion` 改为 3

验证：`cd backend && mise exec -- go test ./internal/library/parser/... -count=1`

测试代理（gpt-6.1-sol，high）负责编写 AC7、AC8、AC9 的合成样本单元测试，样本生成辅助放在 `parser/parsertest`。

## 2 阶段 B：存储、暂存、选择（gpt-6-astra，high）

依赖阶段 A 合入。范围是 design §3 到 §5。

- [ ] B1 `artwork.describe` 以探测结果为准；`Save` / `SaveWithFeedback` 去掉 `mimeType` 参数
- [ ] B2 `SourceObservation.ArtworkScope`；`StageObservation` 去重键与 SQL；`EmbeddedArtworks()`；`GroupingVersion` 改为 2
- [ ] B3 `listFolderArtworkCandidates` 改为带档位的统一入口，处理歧义候选与 Box 父目录
- [ ] B4 `saveArtworkForRelease` 改为分档逐级回退
- [ ] B5 修改与新规则冲突的既有测试（“Box 叶子不取父目录”“内嵌优先”等），并在汇报中列出每处改动

验证：
```bash
cd backend && mise exec -- go test ./internal/catalog/artwork/... ./internal/catalog/ingest/... ./internal/library/... -count=1
ROOMUSIC_TEST_RUN='TestPostgreSQL.*(Artwork|Scan|Stage|Candidate)' ./scripts/test-integration.sh
```

测试代理（gpt-6-astra，high）负责编写：
- AC1 到 AC6、AC10 的单元或集成测试；
- AC11：增量扫描在版本升级后重写候选并重新选择封面；
- 暂存去重的三种键（逐文件、目录 + 专辑、碟目录）。

## 3 阶段 C：规范与验收（主代理）

- [ ] C1 审阅阶段 A、B 的改动与验证输出
- [ ] C2 按 design §6 更新四份规范
- [ ] C3 运行 `trellis-check`；只复跑受影响的测试，不跑全量
- [ ] C4 在提交说明中写明行为变化（封面来源可能改变；歧义目录的文件夹封面会被移除）

## 回滚点

- 阶段 A 可以单独合入：内嵌封面变多了，但仍是内嵌优先，不破坏现有行为。
- 阶段 B 依赖 A。出问题时整体还原 B 的提交即可，无迁移。
