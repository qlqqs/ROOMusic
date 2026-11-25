# SlimProto 多台同步编组：实现规划

## 结论

- **能不能多读**：现在一条流只能被一个 HTTP 客户端读一次，同一个令牌第二次 GET 会让整个播放区走 `stream_lost` 续流。要把同一份字节发给 N 台，得在 zones 里加一层分发器。
- **数据模型**：编组记录挂在主区（leader zone，即发起编组那台设备的播放区）上，每台成员设备一行。成员设备的物理占用用一种"成员占用"表示，放进 upnp 现有的 `owners` 表。
- **同步协调器**：放在 `devices/slimproto`，替换现有托管输出的单设备循环。zones 不需要知道任何 SlimProto 协议细节。
- **待 qlqq 定夺**：主设备掉线时怎么办（见第 5 节风险 1）。

## 1. 现状：区、设备、输出会话、渲染流的关系

- **一台设备对应一个播放区**。用户运行时用 `zones`/`devices` 两张映射表（`zones/service.go:41-47`）。播放区运行时只有一个 `output`、一个 `currentHandle` 和一个 `nextHandle`（`service.go:80-83`）。
- **输出会话**：`ClaimOutput` 生成新会话时会让旧会话失效，并关闭渲染会话（`outputs.go:83-97`）。托管设备行的 `Online` 取的是"可见"，不是租约（`service.go:327-331`）。
- **流令牌**：每个渲染句柄只有一个 `/upnp/streams/{token}`（`upnp_stream.go:46-55`），取流时 `h.taken=true`，然后交出 `Body()`（`:128-131`）。同一令牌再 GET 会按当前位置续流，并把令牌移到新的 seq（`:95-126`）。
- **Body 只能读一次**：底层是 `io.Pipe`（`render/session.go:65`、`:191`），接口注释也写明"长流只能读取一次"（`zones/ports.go:52`）。HTTP 处理函数读到 EOF 后才 `MarkDelivered`（`httpapi/upnp_stream.go:77-79`）。
- **渲染节奏**：渲染只领先播放头约 20 秒（`session.go:501-537`），另外至少保留 64 KiB 未播放字节（`session.go:17`）。播放头来自回报里的 `Observe`（`outputs.go:333`）。

**分发需要的东西**：

- 一个 goroutine 读一次 Body，把数据追加到共享的分块缓冲里。
- 每台成员有自己的令牌和读位置，各自记 Delivered。
- 所有成员读过的块就释放。缓冲有总上限，例如 `max(8 MiB, 30 秒码率)`。
- 读得慢或超时没来连接的成员单独剔除，不阻塞整组，也不反压渲染。
- 成员第二次 GET 只返回 `ErrStreamGone`。中途接不进去，因为 FLAC 的 STREAMINFO 头只在流开头，只能等下一条流（与 G7 一致）。

## 2. 数据模型（推荐方案 A）

| 方案 | 结论 |
|---|---|
| A. 成员表挂在主区上，成员设备在 upnp `owners` 里占"成员占用"，runtime 里 `zoneRuntime.group` | **推荐**。符合 G1（不另造对象），占用、接管和撤销规则基本不用改 |
| B. 另建虚拟组设备/组区 | 不推荐，违反 G1，快照模型也要大改 |
| C. 只在 slimproto 内存里记组 | 不推荐，无法持久化（G6），快照也看不到组 |

### 迁移 0079 草案

```sql
CREATE TABLE playback_group_members (
  device_id      uuid PRIMARY KEY,            -- 同一设备行最多在一个组
  user_id        uuid NOT NULL,
  leader_zone_id uuid NOT NULL REFERENCES playback_zones(id) ON DELETE CASCADE,
  ordinal        smallint NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (device_id, user_id) REFERENCES playback_devices(id, user_id) ON DELETE CASCADE
);
CREATE INDEX playback_group_members_leader_idx ON playback_group_members(leader_zone_id);
ALTER TABLE playback_devices ADD COLUMN sync_delay_ms smallint NOT NULL DEFAULT 0
  CHECK (sync_delay_ms BETWEEN -500 AND 500);
```

- `sync_delay_ms` 放在设备行上，因为主设备本身也需要同步微调。
- 外键依据：`playback_devices` 有 `UNIQUE(id,user_id)`（`0043`）；同一用户、同一物理设备只有一行（`0070` 的 `UNIQUE(user_id, external_id)`）。
- 同时要补 `rollbackFixtures[79]`。

### 不变式（在 Service 的用户写锁内检查）

- 主区和成员设备的 `kind` 都必须是 `slimproto`（G2）。
- 成员不能是主设备自己。
- 已经是主区（有成员）的设备不能再当成员；已经是成员的设备，它的区不能当主区。
- 主区当前必须有活跃的托管输出。
- 每组成员上限 8 台，用来限制分发内存。
- 同一台物理设备可以出现在不同成员各自的组里，运行时靠占用仲裁。

### 占用、接管与可见性

- **加入**：`AcquireMember` 走 `Acquire` 现有的循环（`upnp/output.go:116-169`），被其他成员占用返回 `device_busy`，带 `takeover` 才接管（G3）。设备正被本人自己的区使用时，先取消那条输出并等它收尾（释放），然后 zones 清掉 `offlinePaused`，让它的区停在暂停态（G1）。
- **其他成员看到的占用**：成员占用进了 `owners` 表，`VisibleDevices` 会按现有逻辑显示 `occupied_by`（`:106-110`）。
- **撤销**：`cancelOutputs`、`SessionRevoked`、`enforceGrants` 会自然覆盖成员占用。主输出被取消时，要先结束全部成员占用，再关闭主输出的 `done`。
- **必须修的一个坑**：`Acquire` 看到"同一用户、同一 deviceID"就当作复用直接返回成功（`:150-153`）。成员占用必须排除在外，否则用户激活成员设备会被误判为"已在用"。
- **成员自己的区**：起播类命令和 `activate` 返回 `409 device_grouped`，前端提示"先移出组"。
- **自动重新入组**：只在设备没有被任何人占用时才尝试；被占用就把状态标为 `busy` 并跳过，不抢本人正在单独使用的设备。

## 3. 同步协调器放在哪、怎么和 zones 交互

协调器放在 `devices/slimproto/group.go`。依赖规则已允许 slimproto 导入 zones（`dependency_rules_test.go:137`），zones 不导入 slimproto。

### zones 侧新增

zones 侧用一个可选接口，避免破坏现有的 `ManagedOutputs` 测试替身：

```go
type GroupOutputs interface {
  AcquireMember(ctx, userID, leaderDeviceID, memberDeviceID, udn string, takeover bool) error
  ReleaseMember(ctx, userID, udn string) error
}
func (s *Service) ManagedGroupStream(userID, sessionID string, seq int64) (GroupStreamInfo, error) // 每成员 {DeviceID, Path, Delivered}
func (s *Service) GroupMembers(userID, sessionID string) []GroupMember // UDN、音量、静音、fixed、SyncDelayMS
func (s *Service) ReportGroupMember(ctx, userID, sessionID, deviceID, state string) error // 只在状态变化时调用
```

- 协商（`render_events.go:62-65`）：编组时，采样率上限取主设备和本次预期成员中的最小值（G4，目前都是 FLAC/24 位）。成员集合变了，就在下一条流重新协商。
- `ManagedStream` 的种类判断（`upnp_stream.go:43`）保持不变。

### slimproto 侧新增

- `startStream` 增加 autostart 和 threshold 两个参数（现在硬编码 `'1'`，`protocol.go:435`）。
- 新增 `controlMS('u'|'a'|'p', ms)`，现有 `control()` 不填 replay_gain 字段（`:424-428`）。
- 时钟估计器从 `handle()` 的局部变量（`player.go:190`）移进 `Player`，改成 LMS 的算法：更小的偏移立即采用，并做漂移上调。现在的实现要攒 10 个样本才可用、每 30 秒才重算一次 epoch（`clock.go:299-310`），而心跳是每 5 秒一次（`player.go:237`），预热要将近 50 秒。所以入组和起播前要先以 250 ms 间隔发 `strm t` 预热。

### 流程

| 场景 | 做法 |
|---|---|
| 开新流（起播、跳转、换设置） | zones 重建后发出新 seq。协调器对每台成员：`strm q` → `strm s autostart=0`，各用自己的路径 → 等全部 `STMl`（超时约 8 秒，超时的标 `dropped`）→ T = 现在 + 300 ms + 各成员微调的最大值 → 每台 `strm u jiffies_i` |
| 维持同步 | 每秒比较各成员的表观开始时刻，基准取最超前的那台。落后 10 ms 到 10 秒的发 `strm a`，之后冷却 1 秒。播放点超过 3 秒没更新的成员这一轮跳过 |
| 暂停 / 继续 | 全组 `strm p`。继续时用 LMS 的做法：每台在统一时刻上再加 (e_i − e_min) 后发 `strm u`。缺少新鲜的 `STMp` 播放点时退回到重新开流 |
| 接续流（格式变化） | 每台活动成员都 Delivered 且都报了 `STMd` 之后，才对每台发 `strm s autostart=1`，接在缓冲后面无缝播放。迟迟不报的成员按超时剔除 |
| 回报 zones | 只由参考成员回报：主设备在线时用主设备，否则取第一台活动成员。全部 `STMu` 才回报 `ended`。单台的 `STMn`、`DSCO`、`STMo` 只剔除那一台，不触发全区 `stream_lost` |
| 掉线 / 重新入组 | 玩家连接断开或 3–5 秒没有 STAT 就剔除。下一次开流前，对 `waiting`、`dropped` 的成员调一次重新入组的回调（不接管，G7） |

### 锁的约束

- 协调器 goroutine 是唯一给各台 `Player` 写命令的地方。`Player.send` 是非阻塞入队（`player.go:37-47`）。
- 协调器不持有任何 zones 锁。zones 只通过 hub 推事件给它，不会同步回调。
- 加入流程：先在锁外 Acquire，再加锁复核、写库、改 runtime；写库失败就 `ReleaseMember` 补偿。
- 锁顺序保持现有的 `upnp.s.mu` → 区锁。
- 每台 `Player` 的事件通道只有 64 条，满了会直接断开连接（`player.go:111-114`）。协调器必须及时排空所有成员的通道。

## 4. 接口与前端

前缀 `/api/v1/me/playback`，全部是 v1 内的增量改动。

```text
POST   /zones/{zone_id}/group/members            {device_id, takeover:bool} → 200 {server_time, zone, device}
DELETE /zones/{zone_id}/group/members/{device_id}                            → 200 {server_time, zone, device}
PUT    /zones/{zone_id}/group/volume             {volume}                   → 200 {server_time, devices}
PUT    /devices/{device_id}/sync-delay           {sync_delay_ms}            → 200 {server_time, device}
（单台音量沿用 PUT /devices/{id}/volume）

Zone.group   = {members:[{device_id, state:"active"|"joining"|"waiting"|"dropped"|"offline"|"busy"}]} | null
Device.group = {zone_id, role:"leader"|"member"}（不在组里时省略）
Device.sync_delay_ms（只在 slimproto 行出现）

错误：409 device_busy | device_grouped | group_unsupported（协议不符或主设备不在用）| group_full；
      主设备不在线时 409 zone_offline
```

- **组音量（G5）**：组的电平取各台非 fixed 设备音量的最大值，按比例缩放每台；原最大值为 0 时，全部设成新值。
- **前端最小改动**：
  - `zones/decoders.ts` 解码新字段。
  - `device-picker.tsx` 加"加入其他设备"列表（只列同为 SlimProto、未入组、可见的设备）。
  - 组面板：每台成员的状态标签、音量滑块、同步微调输入、移出按钮。
  - 播放区的音量控件改调组音量。
  - 复用现有 `device_busy` 接管对话框，新增 `device_grouped` 的提示。

## 5. 风险、实施顺序与测试

### 风险

1. **主设备掉线（需决定）**：主设备断开时注册表会删掉这一行，主区就变得不可见（推送 `device_removed`）。而且 `Output.Run` 一看到主设备的 Player 退出就整体结束（`output.go:48-51`、`:81-82`）。
   - MVP 建议：主设备掉线时整组离线暂停，G7 只对成员生效。
   - 后续可做：编组输出存活期间，`VisibleDevices` 继续投影主设备这一行，让其余成员接着放。
2. **分发内存与慢成员**：用共享分块缓冲加上限，超限只剔除那一台。
3. **时钟预热与估计器改写**：会影响单台播放的位置锚点，`clock_test` 要重写。
4. **`strm u` 的边界**：jiffies 不能为 0，目标时刻要在 10 秒以内，还要处理 32 位回绕。
5. **精度边界**：毫秒级同步，不适合同一房间的左右声道组合，文档要写明。
6. **跨成员冲突**：不同成员各自的持久化组里可能有同一台设备，靠运行时占用决定谁能用。

### 实施顺序（每步都能单独测试）

| 步骤 | 内容 | 测试 |
|---|---|---|
| S1 | 迁移 0079 与 store 读写 | PostgreSQL 往返 |
| S2 | zones 编组接口、不变式、`device_grouped` 守卫、DTO 字段、组音量与同步微调 | 用假 `GroupOutputs` 做集成测试，重新生成 API 样本 |
| S3 | zones 分发器、成员令牌、`ManagedGroupStream`、最小采样率协商 | 用 zonestest：N 个读者字节一致、慢读者被剔除且不阻塞、加入超时、Close 后 EOF |
| S4 | slimproto 协议原语和时钟 | protocol/clock 单元测试 |
| S5 | 协调器 | 假时钟加 slimtest 脚本化的 STMl/STMs/STAT，断言 `strm u`/`strm a` 的精确取值，以及暂停、接续流、掉线 |
| S6 | upnp 成员占用、重新入组回调、撤销级联 | 跨账号接管成员、撤销主输出级联、修正后的复用判断 |
| S7 | 前端 | 解码器测试，device-picker 测试 |
| S8 | 真实播放器同步测试（见下） | — |
| S9 | 更新 spec | 协议 §1/§10、服务端 §9、slimproto 合同（去掉"只做单台"）、upnp 占用规则 |

**受影响的现有测试**：

- `protocol_test` 与 `app/slimproto_integration_test`：`strm s` 字节会变。单台仍用 autostart=1。
- `clock_test`。
- API 样本与 `TestPostgreSQLZonesAPIResponseSamples`。
- `migrations_rollback_integration_test`。
- 前端 decoders 测试。

### 同步测量方案

- 新脚本 `scripts/test-slimproto-sync.sh`，起两个 Squeezelite 容器，都用 `-o - -a 32`。
- 计时程序：扩展现有的 `pcm-clock.py`（已支持 ppm），或另写一个 Go 版 pacedsink。
  - 记录的时刻要用**按名义采样率排出的计划时间**（每一帧的计划出声时刻），不用读取时刻。现在每次读 20 ms 的数据，读取时刻的粒度太粗。
  - 用 `F_SETPIPE_SZ` 把管道缩小，减少不计入 elapsed 的延迟。
- 测试音源：合成 FLAC，每秒一个脉冲。两个容器共用宿主机的 `CLOCK_MONOTONIC`，按最近的脉冲配对后直接相减。
- 判定：起播后与持续 10 分钟内，偏差的 95 分位 ≤ 10 ms、峰值 ≤ 15 ms。一台设 +100 ppm 时，大约每 100 秒应出现一次 `strm a`。
- 场景：
  - 暂停、继续、跳转，同格式换曲、跨格式接续流；
  - 用 `tc netem` 加 5±2 ms 抖动（容器需要 `NET_ADMIN`）；
  - `docker kill` 一台后另一台的脉冲不断，重启后在下一次开流时重新入组；
  - 重启应用后编组仍在。
- 每次用独立的 Compose 项目名和临时目录，沿用 `test-slimproto.sh` 的清理逻辑。

### Critical Files for Implementation
- /home/qlqq/workspace/ROOMusic/backend/internal/zones/upnp_stream.go
- /home/qlqq/workspace/ROOMusic/backend/internal/zones/managed_devices.go
- /home/qlqq/workspace/ROOMusic/backend/internal/devices/upnp/output.go
- /home/qlqq/workspace/ROOMusic/backend/internal/devices/slimproto/output.go
- /home/qlqq/workspace/ROOMusic/backend/internal/devices/slimproto/player.go