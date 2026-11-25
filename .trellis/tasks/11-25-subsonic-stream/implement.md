# Subsonic 播放流与听歌记录：实施计划

- 分支：`task/subsonic-stream`
- 基线：合入 foundation 后的 `main`
- 工作目录：Orca worktree

## 步骤

1. 【Codex 实现，astra】
   - `zones`：新增设备类型 `subsonic`，以及 `EnsureClientDevice` 与这类设备的专用默认设置。
   - `render`：抽出单曲路径，新增 `RenderTrack`、MP3 编码、`TryReserve`。
   - `playback`：拆出“打开源文件”。
   - 接口：`stream`、`download`、`scrobble`、`getNowPlaying`。
   - 视需要给 `listening.Record` 加上播放时间参数。
   - 实现代理不写新测试。
2. 【Codex 测试，sol】
   - 用合成音频（ffmpeg 生成的 FLAC、APE、DSF、CUE）做集成测试，覆盖 AC1–AC10。AC2 用均衡设置，对比单曲渲染与会话渲染的 PCM。
   - 检查进程残留的测试只在 Unix 上跑。
3. 【主代理前端】设备列表与选择器、解码器，覆盖 AC8 的前端部分。
4. 主代理验收：
   - 审阅改动，重点看 render 会话代码的抽取是否改变了原有行为（现有 render 测试由子代理跑过，不复跑）。
   - 更新规范 `subsonic-contracts.md`、`playback-render-contracts.md`、`playback-zone-contracts.md`。
   - 提交并合入。

## 验证命令

- `go test ./internal/playback/render/... ./internal/zones/... ./internal/subsonic/... ./internal/listening/...`
  - render 的抽取影响整个 render 包，所以 render 包跑全部测试；
  - zones 只跑设备相关用例：`-run 'Device|Subsonic'`。
- `go test ./internal/playback/ -run 'Prepare|Audio'`
- 前端：`npm run typecheck`，以及设备列表和选择器的测试文件。

## 风险与回滚点

- 抽取 render 单曲路径是本任务风险最大的改动，单独提交，方便回退。
- 本机 ffmpeg 可能没有 `libmp3lame`。测试按 `HasEncoder` 跳过并如实记录，生产镜像需要确认带有该编码器。

## 开工时的现状（2026-11-25，browse 已合入）

- 协议实体转换在 `backend/internal/subsonic/entities.go`：`Song`、`Album`、`Artist(…, subsonic.PersonalState)`。输出歌曲、专辑、艺人一律用它们，不另写转换。
- 本人状态在 `backend/internal/subsonic/httpapi/browse_ratings.go` 的 `ratedEntities` 里批量读取，同一响应只读一次，按 ID 取回，不按位置对齐。新接口输出实体时也走它。
- 接口约定、错误码与测试清单见 `.trellis/spec/backend/subsonic-contracts.md`。处理器的依赖通过 `httpapi.Browse` 注入（`app.go`）；需要新依赖就在这个结构上加字段。
- personal、stream、jukebox 三个子任务并行，都会改 `routes.go` 的接口表、`app.go` 的注入和 `dependency_rules_test.go`，合入时的冲突由主代理解决。迁移编号已预先分配：jukebox 用 0084，stream 需要迁移时用 0085，personal 不加迁移。
- `getNowPlaying` 的条目是歌曲，用 `subsonic.Song` 并走 `ratedEntities` 带上本人状态。
