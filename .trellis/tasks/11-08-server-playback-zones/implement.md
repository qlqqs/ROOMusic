# 执行清单：服务端播放区与远程输出

设计：[design.md](./design.md)（架构、判定、上线回滚、子任务、待裁决、与算法线的接口）、[design-protocol.md](./design-protocol.md)（实时通道、输出端协议、数据模型、界面、其他输出端、测试范围）、
[design-contract.md](./design-contract.md)（接口合同，协议 v1）。本文按子任务给出执行顺序、最小验证命令、风险文件与回滚点。

## 0. 开工前

- [x] 用户裁决 design.md §12：“设置 › 播放”作用于正在控制的那台设备（已写回 prd.md）。
- [x] 本父任务下建子任务 B1、B2、B3、F2、F3、E（`task.py create … --parent .trellis/tasks/11-08-server-playback-zones`）；每个子任务的 prd 写本文对应小节的范围与验收。
      E 是第一批（本线 + 算法线三项）的共同验收，在算法线父任务的 prd 里互相引用。
- [ ] 分支：第一批用一条集成分支 `playback-baseline`（独立 worktree、独占测试库项目 `roomusic-test-zones`）。本线与算法线三项（dsp-pipeline、gapless-crossfade、dither）的子任务都合入它，E 通过后整体合入 main。
- [ ] 分工（2026-11-09 用户改定）：后端实现 `codex/gpt-6-astra`（high），前端实现 Opus（high），测试由独立的 `codex/gpt-6.1-sol`（high）编写与执行；实现 agent 不写新测试、不启动数据库。
- [ ] 派活 prompt 写明：测试只做必要的（只测改动直接影响的行为，不跑全量，不做演练和重复验证）；界面文案不写防御性说明和多余说明；参考稿只引章节号，不摘录原文。

通用命令（在 worktree 根目录）：

```bash
# 后端单元与依赖方向
(cd backend && go test ./internal/zones/... ./internal/app -run 'TestPackageDependencyDirections|^TestZones' -count=1)
# 后端集成（隔离 PostgreSQL，按名字缩小）
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-zones ROOMUSIC_TEST_RUN='TestPostgreSQLZones' scripts/test-integration.sh
# 前端
(cd frontend && npx vitest run src/features/player && npm run typecheck && npm run lint)
# 真实服务（Chromium + 合成素材）；先确认本线的测试库没有在跑
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-zones scripts/playback-e2e.sh e2e/real/playback-zones.real.ts
```

> `scripts/test-integration.sh` 目前写死了项目名 `roomusic-test`；B1 里先让它像 `playback-e2e.sh` 一样认 `ROOMUSIC_TEST_COMPOSE_PROJECT`（扫描对标线若已加过，同步 main 后直接用）。
> 浏览器依赖先 `source /home/qlqq/.local/share/playwright-deps/env.sh`（同一个 shell）。

## 依赖与时间线

```text
B1 ──► B2 ──► B3 ───────────────────────────────┐
 │      │                                        ├─► 联调（F2 换真实渲染器）─► F3 收尾 ─► E（第一批验收）─► 合入 main、发版
 │      └─► dsp-pipeline 按 B2 定下的端口实现 Renderer ┤
 │             gapless-crossfade、dither 在处理链骨架上并行 ┘
 └─ 与 dsp-pipeline 的 iOS 交付小验证并行
F2（按合同编码，对着假渲染器）与 B2/B3 并行；F3 在 F2 之后
```

- **联调具备条件**：B3 合入集成分支，且 dsp-pipeline 的渲染器能对一首 FLAC 出一条可播放的无损流。在此之前 F2、F3 只对着假渲染器开发与测试。
- **E 具备条件**：B1–B3、F2、F3 与算法线三项全部合入集成分支。

## B1 数据与队列语义（后端）

1. [ ] 迁移 `backend/migrations/0037_playback_zones.sql` 与 `.down.sql`（design-protocol.md §4，含 `gapless`、`quality` 两列）。编号暂定，合入 main 前按 main 当时的最大编号顺延，同时改 `migration_version_integration_test` 的期望版本。
2. [ ] `internal/zones/queue`：逐条移植 `frontend/src/features/player/engine/queue.ts` 的纯函数，加“下一条” `PeekNatural`（随机模式预选）与 `AdvanceTo`（按预选结果前进，历史与袋子的更新与 `advance` 相同）。随机数与 ID 注入。
3. [ ] `internal/zones` 的存储层：按用户加载全部播放区与条目；按操作写库（插入腾位、删除、交换、整段替换、播放区行更新）。
4. [ ] `dependency_rules_test.go` 声明 `zones/queue`、`zones`、`zones/httpapi`、`app` 的新依赖；`scripts/test-integration.sh` 认独占项目名。
5. [ ] 测试 agent：把 `queue.test.ts` 的用例表移植为 Go 表驱动测试，补“下一条”两条（随机预选与前进一致、预选条目被移除后重选）；迁移升级与回退各一条集成测试。

验证：`go test ./internal/zones/queue/...`；依赖方向测试；`TestPostgreSQLZones.*Migration`。风险文件：`backend/migrations/`（编号冲突）、`dependency_rules_test.go`。回滚点：B1 提交。

## B2 控制面（后端）

1. [ ] `zones.Service`：内存工作副本、每播放区互斥锁、合同 §3.3 的全部命令（含转移的双锁、导入的核对、300 ms 去抖重建、暂停 5 秒后关闭会话）、hub 的发布接口。
2. [ ] 端口：`Renderer`、`RenderSession`、`Cursor`、`Event`、`PlayheadSample`、`Capabilities`、`StreamFormat`（design.md §14）与 `PlayRecorder`。端口定下后通知 dsp-pipeline 开始实现。
3. [ ] **假渲染器**（只在测试与开发装配里用）：`Negotiate` 返回 WAV，`Format()` 给出具体格式；`Body` 输出静音 WAV；事件按测试脚本或按曲目时长自动推进（起始条目之后每首 `track_started`，按 `gapless` 设置给 `transition`；`time`；放完 `stream_ended`）。
4. [ ] 设备：登记（同名追加序号）、改名、删除（在线拒绝）、音量（不可调拒绝）。快照与队列读取（`LoadTrackSummaries` 每批 1,000）。每小时清理 30 天未上线的设备（时钟注入）。
5. [ ] `zones/httpapi`：合同 §3 的路由、解码、错误映射（§6 的顺序）；`app.New` 装配（集成分支上由 dsp-pipeline 接上真实 `Renderer`；没装配时起播类命令返回 `503`）。
6. [ ] 测试 agent（隔离库，假渲染器）：每条命令一个代表性成功用例；`queue_full`、`track_not_found`、`zone_offline`、`device_online`、`volume_fixed`；转移（目标得到队列与位置并开流、源清空并关闭会话）；
       导入（丢弃不存在的曲目、当前项被丢弃取后继、第二次 `imported: false`）；定位与改设置 300 ms 内多次只开一次流；隔离（A/B 互相 404、管理员也一样）；写接口 403→401；清理只删超期且离线的设备。

验证：`TestPostgreSQLZones` 的 B2 部分。风险文件：`internal/app/app.go`。回滚点：B2 提交。

## B3 实时与输出端（后端）

1. [ ] 事件流 handler（合同 §5）：快照、增量、15 秒心跳、60 秒凭据复核、64 条缓冲溢出即断、应用生命周期结束即返回。
2. [ ] 输出端：认领（`protocol: 1`、能力、`takeover`、`playing`）、回报（`status/ended/fault`、`stream_seq` 栅栏、`stream_lost` 续流与连续 3 次失败后暂停）、释放；会话取代与 `output_unknown`。
3. [ ] 渲染流端点（合同 §4.4）：当前流与接续流可取、其余 `410`；每条流只能取一次，第二个连接 `410`；原样写出 `session.Body()`。
4. [ ] 会话事件的应用（design-protocol.md §3.4）：`time`、`track_started`、`track_failed`（5 次停止）、`format_boundary`（接续流）、`stream_ended`、`rebuild_needed`、`signal`；`segments` 随之更新。
5. [ ] 在线租约 45 秒、5 秒扫描、`offline_paused` 及其恢复；重启后首次加载记 `offline_paused`，认领带 `playing` 时接着放。
6. [ ] 服务端计次（design-protocol.md §3.8）：移植 `listen-tracker.ts` 的规则，经 `PlayRecorder` 记一次（`app` 接 `listening.Service.Record`），推送 `play_recorded`。
7. [ ] 为合同里的每种请求、响应与事件生成样本到 `backend/internal/app/testdata/api-samples/playback-zones/`。
8. [ ] `deploy/production/README.md`：第 5 节加事件流与渲染流的反代要求（不缓冲、读超时 ≥ 60 秒、不压缩 `text/event-stream`）；第 3 节加“降级后各设备的播放队列会清空”。
9. [ ] 测试 agent（隔离库，假渲染器）：事件流首条是快照且收到一条命令的增量、只收本人事件；release 立即离线并暂停；无流无回报 45 秒后离线（时钟注入，不真等）；
       离线后同会话回报 `playing` 恢复；`takeover` 10 秒内恢复、超时不恢复；旧会话 `409`；新建 Service 读同一库后 `404 output_unknown`、认领带 `playing` 接着放；
       `stream_lost` 续流、连续 3 次失败暂停；旧 seq 的流 `410`；`track_started` 推进当前条目、`format_boundary` 出现接续流、`stream_ended` 后暂停在 0；
       计次：把 `listen-tracker.test.ts` 的规则用例移植为 Go 表驱动测试，集成测试一条“过半后 `play_history` 有一行、继续回报不再写”。

验证：`TestPostgreSQLZones` 全部（本包，不跑全量）。风险文件：`cmd/roomusic/main.go`（关停顺序，原则上不改）、`platform/httpserver/middleware.go`（长响应的 Flush 透传，已有 `FlushError`）。回滚点：B3 提交。

## F2 播放区客户端（前端；按合同可与 B2/B3 并行编码，联调需要 B3 与真实渲染器）

1. [ ] `features/player/zones/`：api 与解码器（合同 §2–§4，样本取自 B3 生成的真实 handler 输出）、事件流（fetch 流解析、退避重连、401 走会话边界、控制端标签页隐藏 30 秒断开）、
       zone store（快照、`revision` 丢弃、队列增量与重读、时钟偏移、进度外推、`play_recorded` 时让 `listeningKeys.all` 失效）、controller（与现有引擎同名的命令方法，`playTracks/playNext/enqueue` 仍同步返回 `added | queue_full`）。
2. [ ] `features/player/output/`：两个 `<audio>` 元素、对齐函数（design-protocol.md §3.1）、换流与接续流（§3.5）、回报（串行、合并、事件类重发）、释放（`pagehide` keepalive）、
       手势解锁、Media Session（按 `segments` 换曲目信息，按钮发命令给本机播放区）、音量（写元素，节流 125 ms）、能力声明（合同 §8）。
3. [ ] 设备身份与登记（Web Lock 串行）、输出权 Web Lock 与 `takeover`；R11：`legacy-import.ts` 读旧 sessionStorage 队列发 `import`，之后删键。
4. [ ] `player-provider.tsx` 改为装配 store/controller/output；`usePlayer()` 等 hook 名字与 `PlayerSnapshot` 形状保持（去掉 `leveling`、`audioRoute`，增加 `zone`、`device`、`isLocal`）；
       `usePlaybackCapabilities` 改为只看服务端格式表；`app/player-metadata-refresh.tsx` 改为修补 store 里的显示数据。
5. [ ] 删除：`engine/player-engine.ts`、`audio-graph.ts`、`leveling.ts`、`listen-tracker.ts`、`prefetcher.ts`、`queue-restore.ts`、`queue.ts` 的纯函数、`persistence.ts` 的保存部分、`capability.ts` 的 `canPlayType` 判定，
       以及 `features/listening` 的播放上报器与它们的测试。保留并改造：`media-session.ts`（端口）、`lyrics.ts`、组件。
6. [ ] 测试 agent：对齐函数（新流换上、接续流在 ended 换上、意图与暂停、旧 revision 丢弃）；store（外推、队列增量序号不连续时重读）；事件流解析与 401；导入后删键；按 `segments` 换锁屏信息；
       真实服务（假渲染器）一条：两个标签页只有一个出声，关掉出声的那个另一个接着放。

验证：前端三件套；联调后跑 `playback-zones.real.ts` 中本机播放一条。风险文件：`player-provider.tsx`、`app/shell/app-shell.tsx`、`app/routes/*playback*.tsx`、`features/listening`。回滚点：F2 提交。

## F3 设备与远程控制界面（前端）

1. [ ] 播放栏“播放设备”按钮与设备列表；控制目标与回退（存储键、15 分钟、每分钟与可见时重算）；转移、转到本机、改名、删除确认；远端音量（不可调时禁用）；离线显示；
       控制目标不是本机时显示设备名；“设置 › 播放”（电平化、无缝、交叉渐变与时长、音质）作用于控制目标（§12），标题旁显示设备名。文案照 design-protocol.md §6。
2. [ ] 测试 agent：控制目标回退四种情形；设备列表的转移、改名、删除流程；设置页改动发出 `set_settings`；五种宽度不溢出、axe、纯键盘操作设备列表。

验证：前端三件套与相关组件测试。回滚点：F3 提交。

## E 第一批验收（测试；B1–B3、F2、F3 与算法线三项都在集成分支上之后）

1. [ ] `frontend/e2e/real/playback-zones.real.ts`（真实渲染器）：两个浏览器上下文模拟手机与电脑，覆盖 PRD 验收各条（手机控制电脑、转移、离线、重启、多标签页、隔离、不退化、迁移）。
       “远程出声”“接手”以页面先被点击一次为前提（prd 的验收前提）；“重启”用脚本重启隔离服务进程；“不退化”含 ALAC、电平化、无缝、交叉渐变、歌词、计次。
2. [ ] 真机手工核对一次：iPhone 锁屏后连续播放跨过两首（含一次无缝）、锁屏信息随曲目切换、解锁后列表状态正确；有损档在手机上能放。
3. [ ] 派生退役的升级核对：在保留了旧 `media-cache` 的数据卷上启动新版本，目录被删除、日志一条 `playback.derivation.cache_removed`（实现归 dsp-pipeline P10，这里只验）。
4. [ ] spec：新增 `.trellis/spec/backend/playback-zone-contracts.md`（由合同整理，≤ 24 KB，作为原生客户端的协议文档），更新 backend `index.md`、`directory-structure.md`、`playback-contracts.md`（标出直出接口只保留到下一版）；
       frontend `playback-and-personal-state.md`、`player-design-guidelines.md`、`state-management.md` 中关于引擎、sessionStorage 恢复、设备偏好、计次上报的段落。

验证：`scripts/playback-e2e.sh e2e/real/playback-zones.real.ts`（只这一份；旧的 `playback.real.ts` 测的是退役的直出路径，随 F2 改写或删除）。

## 合入与收尾

- 合入顺序（集成分支内）：B1 → B2 → B3；F2 在 B3 之后合入；算法线三项按它们自己的顺序合入；F3 → E。集成分支合入 main 前迁移改号、`migration_version` 期望值随之更新。
- 回滚点：集成分支合入 main 的那个提交（design.md §8）。
- 发版说明写“播放改由服务器统一处理：队列不再随标签页丢失，可以在一台设备上控制另一台，曲间默认无缝，可开交叉渐变；在外可选省流量音质”，并注明反代要求与“升级后自动删除旧的 ALAC 转码缓存”。
- 下一个版本删除 `GET /tracks/{id}/playback`、`/audio`、`POST /me/plays` 及其合同（design.md §8）。
- 收尾：截图（设备列表、远程控制中的播放栏，1440 与 375）、记录结果、归档任务、更新日志。
