# 播放区协议：托管的局域网输出与编组

第 10 节从[播放区协议 v1](./playback-zone-contracts.md) 原样拆出（2026-11-25，原文件超过 24 KB）；第 11 节是 SlimProto 编组（任务 `11-25-slimproto-sync-groups`）。

## 10. 托管的局域网输出

设备首次可见时，快照与 SSE 首帧包含成员自己的设备与播放区。UPnP 行的 `external_id` 是物理设备 UDN，`online` 表示发现在线，`output_session_id` 为 null。撤销可见性后，SSE 发送 `device_removed`，设备重新可见时恢复自己的播放区与队列。管理员分配、共享与发现接口见 [局域网设备合同](./upnp-device-contracts.md)。

| 接口 | 请求与响应 |
| --- | --- |
| POST /devices/{device_id}/activate | 选择设备为控制目标。传 `{}` 或 `{"takeover":true}`，200 返回 `{server_time,device}`。同一成员重复选择复用当前占用 |
| PUT /devices/{device_id}/sample-rate | 传 `{"max_sample_rate":96000}`。允许 44100、48000、96000、192000 Hz，返回 `{server_time,device}`。活跃流重新协商 |
| POST /zones/{zone_id}/commands | `play_tracks`、`play_entry`、`play`、`next`、`previous`、`retry` 与 `transfer` 接受可选布尔字段 `takeover`。转移认领目标，成功后释放已经清空的源设备 |

UPnP 行带 `max_sample_rate` 和 `format_declared`，每个 `capabilities.stream_formats[]` 带协商上限与 24 位位深。占用时另有 `occupied_by:{user_id,display_name}`。设备已由其他成员占用时返回 409，`error.code=device_busy`，`error.message` 含占用者显示名，顶层带 `request_id`。传 `takeover:true` 会暂停旧区的输出。重新使用失去分配的设备 ID 时返回 404。实体设备面板暂停或继续时，区事件带 `kind:"pause"|"play"` 与 `source:"device"`。

请求与响应示例见 `backend/internal/app/testdata/api-samples/upnp/playback-snapshot.json`、`playback-occupied.json` 与 `device-busy.json`。

## 11. 编组（SlimProto 多台同步）

一个播放区可以同时在多台 SlimProto 设备上同步播放。主设备是发起编组的那台设备，它的播放区就是组的播放区；被加入的设备是成员。协议时间戳以毫秒为单位，服务端按帧补偿成员漂移，适合不同房间，不适合同一房间的左右声道组合。同步机制与取值见 [SlimProto 播放器合同 §8](./slimproto-device-contracts.md#8-编组同步)。

```text
Zone.group   = {members: [{device_id, state}]} | null      // 总是出现；不编组为 null。按加入顺序
state        = "active" | "joining" | "waiting" | "dropped" | "offline" | "busy"
Device.group = {zone_id, role: "leader"|"member"}          // 不在组里时省略
Device.sync_delay_ms                                        // 只在 slimproto 行出现，−500..500
```

| 方法与路径 | 请求 | 成功 |
| --- | --- | --- |
| `POST /zones/{zone_id}/group/members` | `{device_id, takeover?}` | `200 {server_time, device, zone}`（样本 group-add-member） |
| `DELETE /zones/{zone_id}/group/members/{device_id}` | 无 | `200 {server_time, device, zone}`（group-remove-member） |
| `PUT /zones/{zone_id}/group/volume` | `{volume}` | `200 {server_time, devices}`（group-volume） |
| `PUT /devices/{device_id}/sync-delay` | `{sync_delay_ms}` | `200 {server_time, device}`（sync-delay） |

- 不变式：主设备与成员都是 `slimproto`；成员不是主设备；已是主设备的不能当成员，已是成员的设备的播放区不能当主区；主区当前有活跃托管输出；每组最多 8 台（含主设备）。
- 占用：成员按托管设备的占用规则认领，别人在用时 `409 device_busy`，带 `takeover:true` 接管。加入本人正在单独使用的设备时，先等那条输出收尾，它的播放区停在暂停态。
- 成员自己的播放区在编组期间拒绝起播类命令与 `activate`：`409 device_grouped`。移出后回到自己的播放区（暂停态）。
- 组音量：组电平取组内非 fixed 设备音量的最大值，新值按比例缩放每台；原最大值为 0 时全部设为新值。单台音量仍用 `PUT /devices/{id}/volume`。
- 流与格式：全组协商同一格式，采样率上限取各台上限的最小值。服务端渲染一次共享 FLAC，分发器为每台签发独立取流令牌。主设备直读共享字节；`Options.NewMemberStreamFilter` 为每条成员流提供过滤器，首次 GET 开始无损解码、局部拉伸与 FLAC 重编码。每个调整块用 32 抽头 Kaiser 窗 sinc 在完整间隔内摊开一帧，块外与不足一块的尾段保留原样本。未装配工厂时各令牌直读共享字节。慢读者、超时未取流或过滤失败的成员单独标为 `dropped`。中途加入的成员在下一次开流时接入。
- 漂移：`GroupMemberStream.Drift` 提供 `Source(output)` 与 `Pending(output)`，按输出帧数查询源位置与待生效净调整量；`Adjust(frames)` 累加删帧（正）或插帧（负）请求，支持前馈的过滤器还实现 `DriftRateSetter.SetRate(ppm float64)`。按输出数量累计的整帧与偏移欠量合并，每 2000 个输出帧最多调整一帧（500 ppm）。参考主设备，由最近 60 秒内的未补偿落后量估计斜率，首估需跨度 ≥ 20 秒且 ≥ 15 个不同样本，此后每 10 秒更新。同组成员换流沿用 ppm，重新入组清零。将落后量外推到成员渲染头，扣除在途调整后，残差绝对值超过 0.5 ms 才追加偏移请求。原始偏差绝对值超过 20 ms 或没有补偿器时，落后 10 ms–10 s 的成员通过 `strm a` 跳帧，超前 10 ms–10 s 的成员通过带间隔的 `strm p` 插入静音。命令发送成功后抵消该播放点此前待生效的净调整，清空回归样本并保留 ppm。
- 掉线：成员掉线时其余继续播放，成员重新上线后在下一次开流时自动重新入组（设备被别人占用时为 `busy`，不接管）。主设备掉线时整组离线暂停。
- 编组按主区持久化（迁移 `0079_playback_groups`：`playback_group_members`、`playback_devices.sync_delay_ms`），重启后设备上线即恢复。

| 错误 | 情形 |
| --- | --- |
| `409 group_unsupported` | 主设备或成员不是 SlimProto，或主区没有活跃输出 |
| `409 group_full` | 超过 8 台 |
| `409 device_grouped` | 成员的播放区收到起播类命令或 `activate`；把主设备加入别的组 |
| `409 device_busy` | 成员被其他成员占用且没有 `takeover` |
| `400 invalid_input` | `sync_delay_ms` 超出 −500..500，`volume` 超出 0..1，未知字段 |
