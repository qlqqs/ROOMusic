# Subsonic 点唱机控制播放区：实施计划

- 分支 `task/subsonic-jukebox`，基线为合入 foundation 后的 `main`，工作目录用 Orca worktree。
- 本任务只做协议映射，不新增播放能力，所以没有单独的 design.md，设计要点写在下面。

## 设计要点

- **目标播放区的存储**
  - foundation 的 `subsonic` 数据里增加 `jukebox_zone_id uuid NULL`，按用户存储。
  - 读取时校验该播放区仍属于本人且还存在；不满足就按“没有选过”处理。
- **“最近播放过的播放区”**
  - 优先用 zones 现有的最近状态。
  - 没有现成数据时，取本人最近一条听歌记录的来源播放区；`listening` 已有播放区录入，见 `zonePlayRecorder`。
- **操作映射**
  - 全部调用 `zones` 服务层现有的命令函数，与网页端 HTTP 处理器调用的是同一组，不经过 HTTP。
  - `set` 对应“替换队列”，`add` 对应“插入到末尾”，`skip` 对应“跳到条目 + seek”，`shuffle` 对应现有的打乱操作。如果 zones 没有“打乱队列”，就用“替换为打乱后的顺序”实现。
  - `index` 与 `entry_id` 的换算：先读出当前队列，再换算。
- **音量**
  - 编组时 `setGain` 走编组音量命令，否则走设备音量命令。
- **前端（主代理）**
  - 在“第三方客户端”一行加一个“点唱机控制”选择器，选项是本人的播放区，包括编组。
  - 接口：`GET/PUT /api/v1/me/subsonic` 增加字段 `jukebox_zone_id`。

## 步骤

1. 【Codex 实现，sol】迁移字段，`jukeboxControl` 的各个 action，以及 `/api/v1/me/subsonic` 的扩展。
2. 【Codex 测试，sol】集成测试覆盖 AC1–AC6；播放区一侧用现有的测试输出端替身。
3. 【主代理】前端选择器及其单测。
4. 主代理验收，更新 `subsonic-contracts.md`，提交并合入。

## 验证命令

- `go test ./internal/subsonic/... -run 'Jukebox'`
- `go test ./internal/zones/... -run 'Shuffle|Queue'`：只在 zones 有改动时运行。
- 前端：`npm run typecheck`，以及选择器的测试文件。
