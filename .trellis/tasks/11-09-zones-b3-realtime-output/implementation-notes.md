# B3 实现与测试交接

## 本轮边界

实现父任务 `implement.md` 的 B3 第 1–8 步；第 7 步按用户要求仅准备样本生成约定，生成测试与真实样本由独立测试 agent 交付。第 9 步独立测试尚未执行，任务不归档、不提交；没有修改前端、DSP 或进程关停顺序，也不启动数据库。

## 装配与部署

- `backend/internal/app/zones.go` 的 `zonePlayRecorder` 将 `listening.Service.Record` 的返回值转换为 `error`，由 `app.New` 注入 `zones.Options.PlayRecorder`；旧 `POST /me/plays` 与播放区共用同一 listening 服务。
- 幂等键属于服务端访问生命周期；达到门槛后的重试必须保持键、曲目与首次待提交秒数一致，因为 listening 会检查三者。
- 生产仍不装配假 Renderer，真实 Renderer 由算法线接入；本轮不改变 B2 的 `renderer_unavailable` 行为。
- 部署说明第 3 节注明降级队列清空，第 5 节覆盖两条长响应路径：不缓冲、读超时至少 60 秒、事件流不压缩。

## 样本生成方式（交独立测试 agent）

目标目录：`backend/internal/app/testdata/api-samples/playback-zones/`，不手写 DTO 金样，也不把 README 放入这个目录（现有 recorder 会把目录内文件纳入比较/清理）。

沿用 `backend/internal/app/api_samples_integration_test.go`：

1. 增加独立的 `TestPostgreSQLZonesAPIResponseSamples`，以真实 Guard、zones HTTP handler、隔离数据库和 `zonestest.Renderer` 产生请求/响应与 SSE；可复用 `integrationTestApplication` 与 `newAPISampleRecorder`。
2. `samples.scope = "playback-zones"`，调用 `samples.record("playback-zones/responses/...", response, status)`，最后 `samples.finish()`。在 `apiSampleRecorder.ownsSample` 增加 `playback-zones/` 的独占判断，避免默认样本测试误报多余文件或重写时删除这些样本；相应扩充已有所有权测试期望。
3. 普通响应保持仓库的 `{status,body}` 包装、两空格缩进、末尾换行，复用 UUID/时间/内嵌 URL 的规范化与敏感值检查。
4. 请求样本必须来自实际发给 handler 的字节，建议 `playback-zones/requests/<操作>.json`；事件样本必须解析真实 SSE 的 `event:` 与 `data:` 后录入，建议 `playback-zones/events/<类型>.json`，保留事件类型。请求和 SSE 不是 JSON HTTP 响应，不可直接伪造 `httptest.ResponseRecorder` 当成真实响应；在测试 recorder 增加专用录入入口，复用已有规范化、脱敏及比较/重写逻辑。
5. 覆盖合同全部 B2/B3 端点与命令、认领带 `playing`/接手、status/ended/两种 fault、release，以及 `snapshot/zone/queue/device/device_removed`；zone 公告各分支（包括 `play_recorded`）、接续流、nullable/空数组及错误矩阵均应有样本。204 只存状态与空体约定，不能强行 JSON 解码；渲染流保存响应头约定，音频字节由流测试比较，不伪装成 JSON。
6. 测试 agent 启动自己的隔离数据库并设置 `ROOMUSIC_TEST_DATABASE_URL` 后，只在 backend 执行以下目标测试；本轮不执行：

   ```sh
   ROOMUSIC_UPDATE_API_SAMPLES=1 go test ./internal/app -run '^TestPostgreSQLZonesAPIResponseSamples$' -count=1
   go test ./internal/app -run '^TestPostgreSQLZonesAPIResponseSamples$' -count=1
   ```

已有根脚本 `scripts/test-integration.sh` 会运行 `go test ./...`；如果使用它，必须设置 `ROOMUSIC_TEST_RUN` 的精准正则与独占 `ROOMUSIC_TEST_COMPOSE_PROJECT`，不启动默认全量测试。

## 根包运行时

公开签名与状态机说明见 [根包交接](./research/core-implementation.md)。认领/回报/释放维护独立输出身份，当前与接续流以句柄/代次阻挡旧事件，各有独立单次领取标记。所有会话事件经区锁应用，`track_started` 使用 `AdvanceTo`；重复格式边界替换接续流，故障与续流次数分别计数。服务关闭先取消长响应生命周期，关闭会话并等待事件消费者结束。

原子订阅先持用户注册表锁，再按 zone ID 锁 dispatch 与区状态，把已提交增量排完，再创建首条 snapshot；关联的输出连接在 Close/溢出时解除保活。

## 具体化与限制

- 一秒为非 time 事件事务失败及暂时开流失败的重试间隔，均使用注入定时器并检查代次；接续预载异常提前产生的播放头事件最多暂存 64 条，之后对上游背压。
- 旧输出身份索引在进程内保留，用来区分本人已取代与他人会话；暂无旧身份数量上限。服务重启后统一未知。
- 访问累计与幂等键和 B2 工作副本一样只在内存中；重启恢复重新建立访问基准，未新增持久化列，不能跨重启保留未达门槛累计或原幂等键。
- 第 7 步样本测试/样本文件与第 9 步集成验收由独立测试 agent 完成；本轮只给生成流程，不手写样本。

## 给第 9 步测试 agent 的接入提示

- 复用 B2 的 `controlFixture`、隔离 schema、真实 Guard 与 `zonestest.Renderer`；新增 B3 测试放在外部 `zones_test`，避免 `zones -> zonestest -> zones` 测试循环依赖。
- `zones.Options{Now, AfterFunc, NewID, Random, Renderer, PlayRecorder}` 控制所有业务时间和身份；HTTP 复用 `Service.Now/AfterFunc`，无需另一套时钟。由于 B3 会话事件消费者是协程，升级假时钟为线程安全的实现，触发回调时释放时钟自己的锁，不在时钟锁内回调 Service。
- 45 秒租约/5 秒扫描：认领后关闭关联 SSE，再推进到边界前后；持续 SSE、持续报告分别保持在线。不要用 `SetOutputState` 代替真正认领来测试输出租约，该入口只是 B2 兼容接点。
- 10 秒接手：播放中 release 后分别在窗口内/外认领，测试普通认领不自动恢复；相同 session 的超租 `status:playing` 与重启后新认领 `playing` 分开测。明确区分外用户 404、已取代 409、已释放/重启未知 404。
- 5 秒暂停关闭与 300 ms 重建：复用同一 fake clock，验证旧回调不能关闭新会话、恢复/接手不重置访问累计。
- 用假渲染器会话的 `Emit` 注入事件，以 `Subscription.Events` 中的状态公告或其他同步信号确认已应用，不依靠固定 sleep。连续 `time` 不逐条广播，因此计次与位置节流测试可等 fake recorder 的通知或后续状态事件。
- 流测试通过带 middleware 的真实 HTTP server 验证 Flush、请求取消/应用取消与无 Content-Length；用可阻塞 reader/可观测 Close 的假会话核对 handler 返回但会话不被关闭。同一 seq 第二次、旧 seq、接续替换后的 seq 均 410。
- SSE 用可逐帧读取的 writer/client；先核对 snapshot，再发命令核对 queue 在 zone 之前；推进 15/60 秒检查心跳与认证失效。慢消费者推满 64 条后应观察 Done，不把剩余事件排空。
- 计次规则原测试被并行 F2 删除，可只读 `git show 9344074:frontend/src/features/player/engine/listen-tracker.test.ts`；不要恢复或改动前端文件。覆盖已知/未知时长、跳变、暂停/缓冲、续流、新访问，以及 fake PlayRecorder 首次返回错误后同键/同秒数重试；app 内另测真实 listening 仅写一行。

## HTTP 复核收敛

- 补充 HEAD 边界：Go 的 GET 路由隐式匹配 HEAD，两条长响应端点显式拒绝 HEAD（鉴权后 405、Allow: GET），防止探测消耗单次流或建立永久订阅。这是合同未列方法的具体化，不改变 GET 协议。
- 最小修改 `platform/httpserver/middleware.go` 的既有 `FlushError`：保持写入 200，但委托底层 ResponseController，避免原实现调用无返回值 Flush 吞掉网络刷新错误。`main.go` 与关停顺序未改。
- 非负的流序号 0 进入 Service 归属/流存活检查，返回 404/410，不在 HTTP 层先判 400；负数、不可解析的序号仍 400。
- HTTP 复核 agent 仅运行 gofmt 格式检查和限定路径 `git diff --check`；未新增测试或扩大测试集。测试 agent 补充 HEAD 不消耗流、底层 FlushError 原样传递、SSE 溢出打断阻塞写这三条。

## 自查

首轮已执行并通过：

```sh
cd backend && go build ./... && go vet ./internal/zones/... ./internal/app && go test ./internal/zones/... ./internal/app -run 'TestPackageDependencyDirections|^TestZones' -count=1
```

筛选结果：zones 根包 `no tests to run`，httpapi/zonestest 无测试文件，queue 与 app 匹配项通过。没有运行全量或数据库集成测试：用户明确限定范围且测试由独立 agent 负责。未修改已有测试期望值。
独立复核后的同一限定命令第二轮也通过；最终一轮结果见文末。

## 算法线首曲失败补充（2026-11-09 用户裁决）

- 不修改或依赖 `dsp-render` 分支新增的三个设备上限字段；未来合并 `ports.go` 时保留算法线字段与 B3 的注释更新。
- `Stream.format` 在起始条目打不开、会话 Format 零值时输出 null；这一可空性已同步父合同与 backend spec，前端 agent 必须同步 decoder（本轮不改前端）。
- 空 Body 的 EOF/输出端 ended 可能先于会话 track_failed、format_boundary；不能由前者提前销毁会话。测试 agent 用脚本分别排列“先 ended 后边界”和“先边界后 ended”，验证接续流最终可取、换上后正确推进，失败计数不因空流接续而清零。
- 增加 Format 零值的真实 handler 样本，不能用全零 codec/sample_rate/channels 的对象充数；没有接续且全部失败时仍须以会话最终结束事件收尾。

只读核对当前 F2：`output/output.ts` 的 `reconcile` 收到晚到 next_stream 时只 preload，而 `onEnded` 才会 promote。空流先 ended、后边界时，F2 还需处理“活动流已经 ended 或空流报错，接续流此时才出现”的立即换上；只放宽 format decoder 不足以保证实播。由前端 agent 修改，B3 不碰前端。独立测试分别验证后端事件/响应与这条跨端对齐路径。

## 复核追加的测试断言

- `stream_lost` 的“三次”指三次续流本身失败，原始流首次断开不占一次；成功 playing/time 清零。覆盖 Open 失败与新流已打开但输出再次报错两种来源。
- DTO 的能力指针、流格式/分段、曲目嵌套摘要与 signal 是独立投影；修改返回 DTO 不能反向修改运行时状态。
- 暂停后旧 playing 回报、seek 去抖期间旧 ended/故障不能覆盖已接受意图或目标位置；暂停期间的续流不能把五秒关闭期限重新延后。
- 短曲连续跨段后立即 stream_lost/release，按已有分段推进并保存正确曲目/位置/历史；不能为恢复重新随机。
- 当前 A 正在放、B 预载时的 NextChanged/rebuild_needed 应只重建正确会话；B 宣布 C 的格式边界不能直接把唯一 next 槽中的 B 替换成 C、跳过 B。

## 根包复核收敛

已修复 DTO 嵌套别名、设备注册/排序读锁、暂停/缓冲锚点、重建中的旧回报栅栏、暂停续流保持原关闭期限、释放接续位置、SSE 在线时间刷新、重复离线恢复信息、节流写库锚点，以及接续事件消费者退出。
首曲失败输出 null 格式并保留协商出的 quality/MIME；空流的 ended/stream_lost 只对账、不提前毁掉待派发的失败/边界事件，连续空接续失败也能推进并在阈值停止。

补充收敛已完成：跨段恢复按已知 segments 逐段 AdvanceTo（包括同条目循环的新访问），不改随机选择，不提交半截队列状态；预载自身重建独立去抖，当前流继续出声；B 预载时宣布 C 只保存最新目标，B 晋升后才开 C，不额外创建第三流。广播改为锁内冻结 DTO、解区锁后发布，dispatch 仍保序。

剩余设计缺口必须单独说明：visit UUID、累计、recorded 没有持久化列，跨重启可能把原本已计次的访问重新累计并重计；这不等于“跨重启访问语义完全延续”。旧输出身份索引没有淘汰规则，暂保留到服务重启以保证 superseded 的 409 语义。未在本轮擅自修改 B1 表或引入任意容量上限。

## 改动文件清单

- `backend/internal/zones/`：修改 `commands.go`、`devices.go`、`hub.go`、`ports.go`（仅注释）、`render_runtime.go`、`service.go`、`transfer.go`、`types.go`；新增 `dto_clone.go`、`listen_tracker.go`、`next_rebuild.go`、`output_lifecycle.go`、`output_types.go`、`outputs.go`、`render_events.go`。
- `backend/internal/zones/httpapi/`：修改 `handlers.go`；新增 `events.go`、`output.go`、`output_input.go`、`stream.go`、`streaming.go`。
- `backend/internal/app/app.go`、新增 `backend/internal/app/zones.go`；`backend/internal/platform/httpserver/middleware.go`（仅 FlushError）。
- `deploy/production/README.md`；backend spec 的 `playback-zone-contracts.md`、`index.md`、`directory-structure.md`、`listening-contracts.md`；父任务 `design-contract.md`（用户裁决的 format nullable）；本交接及 `research/core-implementation.md`。

没有改动测试文件/期望值、frontend、DSP、main.go；没有暂存、提交或归档任务。独立第 9 步与样本生成待测试 agent。

### 最终限定自查结果

最后一次在全部代码修复完成后执行同一条指定命令，退出码 0：build、局部 vet、筛选测试全部通过。输出为 zones 根包无匹配测试、httpapi/zonestest 无测试文件、queue 和 app 成功。没有运行其他测试集合，也没有数据库或真实渲染器验收。
