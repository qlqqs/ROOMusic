# 设计：SlimProto 多台同步编组

详细依据见 `research/group-model-plan.md`（规划代理按代码给出的 `文件:行` 证据）与 `research/sync-algorithm.md`（Lyrion 与 Squeezelite 的同步机制）。本文只记定下的结构与接口。

## 1. 数据模型（规划方案 A）

- 迁移 `0079_playback_groups`：
  - `playback_group_members(device_id uuid PK, user_id uuid, leader_zone_id uuid REFERENCES playback_zones ON DELETE CASCADE, ordinal smallint, created_at)`，`(device_id,user_id)` 外键指向 `playback_devices`，按 `leader_zone_id` 建索引。
  - `playback_devices.sync_delay_ms smallint NOT NULL DEFAULT 0 CHECK (BETWEEN -500 AND 500)`。
  - 补 `rollbackFixtures[79]`。
- 不变式（用户写锁内检查）：主区与成员都是 `slimproto`；成员不是主设备；已是主区的设备不能当成员，已是成员的设备的区不能当主区；主区当前有活跃托管输出；每组最多 8 台（含主设备）。
- 运行时：`zoneRuntime.group`（成员设备 ID 列表与各自状态 `active|joining|waiting|dropped|offline|busy`）。

## 2. 占用

- 成员设备在 `upnp` 的 `owners` 里占“成员占用”（复用 `Acquire` 的循环、`device_busy` 与接管；撤销、改授权、会话失效自然覆盖）。
- 修正 `Acquire` 的复用判断：成员占用不算“同一用户同一设备在用”。
- 主输出被取消时，先结束全部成员占用，再关闭主输出。
- 成员设备正被本人自己的播放区使用时：取消那条输出并等它收尾，然后它的区停在暂停态。
- 成员自己的区：起播类命令与 `activate` 返回 `409 device_grouped`。
- 自动重新入组只在设备未被任何人占用时进行，被占用标 `busy`。

## 3. 分发（zones）

- 新增分发器：一个协程读一次渲染 `Body()`，追加到共享分块缓冲；每台成员一个令牌与读位置，各自记 `Delivered`；所有成员读过的块释放；总上限 `max(8 MiB, 30 秒码率)`，超限或超时未连接的成员单独剔除，不阻塞整组、不反压渲染。成员第二次 GET 返回 `ErrStreamGone`（FLAC 头只在流开头，中途不能接入）。
- 协商：编组时采样率上限取主设备与成员上限的最小值；成员集合变化在下一条流重新协商。
- zones 新增（可选接口，不破坏现有 `ManagedOutputs` 替身）：

```go
type GroupOutputs interface {
  AcquireMember(ctx, userID, leaderDeviceID, memberDeviceID, udn string, takeover bool) error
  ReleaseMember(ctx, userID, udn string) error
}
func (s *Service) ManagedGroupStream(userID, sessionID string, seq int64) (GroupStreamInfo, error) // 每成员 {DeviceID, Path, Delivered}
func (s *Service) GroupMembers(userID, sessionID string) []GroupMember                      // UDN、音量、静音、fixed、SyncDelayMS
func (s *Service) ReportGroupMember(ctx, userID, sessionID, deviceID, state string) error    // 只在状态变化时调用
```

## 4. 同步协调器（devices/slimproto/group.go）

- 替换编组时主输出的单设备循环；单台播放不变。唯一给各台 `Player` 写命令的协程，不持有任何 zones 锁，及时排空每台的事件通道（64 条满即断开）。
- 协议原语：`startStream` 增加 autostart 与 threshold 参数（单台仍为 `1`）；新增带毫秒参数的 `strm u/a/p`；时钟估计器移进 `Player`，按 Lyrion 做法“更小的偏移立即采用、带漂移上调”，入组与起播前以 250 ms 间隔发 `strm t` 预热。`strm u` 的 jiffies 不能为 0、目标在 10 秒内、处理 32 位回绕。
- 流程：

| 场景 | 做法 |
| --- | --- |
| 开新流 | 每台 `strm q` → `strm s autostart=0`（各用自己的令牌路径）→ 等全部 `STMl`（超时 8 秒，超时者标 `dropped`）→ T = 现在 + 300 ms + 各成员微调最大值 → 每台 `strm u (T − 微调_i 换算成该台 jiffies)` |
| 维持同步 | 每秒比较各成员的表观开始时刻，基准为最超前者；落后 10 ms–10 s 的发 `strm a`，冷却 1 秒；播放点超过 3 秒未更新的成员本轮跳过 |
| 暂停 / 继续 | 全组 `strm p`；继续时每台在统一时刻上加 (e_i − e_min) 后 `strm u`；缺新鲜播放点时退回重新开流 |
| 接续流 | 每台活动成员都 Delivered 且都报 `STMd` 后，才对每台 `strm s autostart=1`；迟迟不报者超时剔除 |
| 回报 zones | 只由参考成员回报（主设备）；全部 `STMu` 才回报 `ended`；单台 `STMn`/`DSCO`/`STMo` 只剔除那一台 |
| 掉线 / 入组 | 成员连接断开或 5 秒无 STAT 即剔除；下一次开流前对 `waiting`/`dropped` 成员尝试重新入组（不接管）；主设备掉线按 G9 整组暂停 |

## 5. 接口（`/api/v1/me/playback`，v1 增量）

```text
POST   /zones/{zone_id}/group/members            {device_id, takeover} → 200 {server_time, zone, device}
DELETE /zones/{zone_id}/group/members/{device_id}                       → 200 {server_time, zone, device}
PUT    /zones/{zone_id}/group/volume             {volume}              → 200 {server_time, devices}
PUT    /devices/{device_id}/sync-delay           {sync_delay_ms}       → 200 {server_time, device}
Zone.group   = {members:[{device_id, state}]} | null
Device.group = {zone_id, role:"leader"|"member"}（不在组里时省略）
Device.sync_delay_ms（只在 slimproto 行）
错误：409 device_busy | device_grouped | group_unsupported | group_full | zone_offline
```

组音量：取组内非 fixed 设备音量的最大值为组电平，按比例缩放每台；原最大值为 0 时全部设为新值。

## 6. 前端

`zones/decoders.ts` 解码新字段；设备选择器加“加入其他设备”（只列同为 SlimProto、未入组、可见的设备）；组面板：成员状态、各自音量、同步微调、移出；播放区音量控件改调组音量；复用接管对话框，新增 `device_grouped` 提示。图标优先。

## 7. 测试

- S1 迁移往返；S2 zones 编组接口与守卫（假 `GroupOutputs`）、API 样本；S3 分发器（N 个读者字节一致、慢读者剔除不阻塞、加入超时、关闭后 EOF）；S4 协议原语与时钟；S5 协调器（假时钟与脚本化 STAT，断言 `strm u`/`strm a` 取值、暂停、接续流、掉线）；S6 占用与级联。
- 真实播放器：`scripts/test-slimproto-sync.sh`，两台 Squeezelite `-o - -a 32` 接按名义采样率排出计划时刻的计时程序（管道用 `F_SETPIPE_SZ` 缩小），每秒一个脉冲，同主机 `CLOCK_MONOTONIC` 相减。判定：起播后与 10 分钟内偏差 95 分位 ≤ 10 ms、峰值 ≤ 15 ms；一台 +100 ppm 时约每 100 秒出现一次 `strm a`；暂停、继续、跳转、同格式换曲、跨格式接续、`tc netem` 5±2 ms 抖动、杀掉一台后另一台不断且下一次开流重新入组、重启应用后编组仍在。
- 受影响的现有测试：`protocol_test` 与 `app/slimproto_integration_test`（`strm s` 字节）、`clock_test`、API 样本与 `TestPostgreSQLZonesAPIResponseSamples`、迁移往返、前端解码测试。

## 8. 兼容与回滚

迁移只加表与列，有 down；单台播放的命令字节保持 autostart=1；不编组时行为不变。
