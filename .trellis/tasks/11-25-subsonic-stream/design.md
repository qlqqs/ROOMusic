# Subsonic 播放流与听歌记录：设计

## 设备

- **新增设备类型**：`zones` 的设备类型加 `"subsonic"`。
  - 登记入口是 `zones` 新增的服务端函数 `EnsureClientDevice(ctx, userID, clientName) (Device, Zone, error)`，按 (user, kind, name) 幂等。
  - 不走浏览器或原生设备的客户端登记接口。
- **默认设置**：登记时写入专用默认值，即 R2 规定的“全部处理关闭”，不使用播放区的通用默认值。
- **能力声明**：不声明输出能力，所以协商、派发、控制目标的选择都会跳过这台设备。
  - 前端的设备选择器按 `kind` 过滤掉它；
  - 设置页的设备列表照常显示它，图标用“第三方客户端”。
- **在线状态**：
  - 每次 `stream` 时更新 `last_seen_at`；
  - 显示为“最近使用于 …”，不显示在线或离线。
- **改名**：客户端名 `c` 变化时，视为另一台设备。

## 单曲渲染（复用处理链）

- **新入口**：`playback/render` 新增 `Renderer.RenderTrack(ctx, TrackRender) (io.ReadCloser, zones.StreamFormat, error)`。
- **入参**：
  - `UserID`、`TrackID`；
  - 播放区设置 `zones.ZoneSettings`；
  - 均衡与卷积文档；
  - 目标 `StreamFormat`；
  - `StartSeconds`。
- **复用的部分**：
  - `TrackSource.Prepare`：安全打开文件、CUE 区间、增益与静音分析；
  - 解码器，包括 DSD 经 `dsdpcm` 解码；
  - `renderPlan.buildChain`：SRC、降混、均衡、卷积、Headroom、量化；
  - `newEncoder`。
- **与播放区会话的区别**：
  - 没有 Cursor，不接续下一首，没有交叉渐变；
  - 没有播放头和 `waitToRender` 控速；
  - 不派发事件；
  - 背压只来自 HTTP 写出。
- **实现要求**：
  - 把会话里“准备 → 解码 → 处理链 → 编码”的单曲路径抽成会话和单曲共用的内部函数，不复制代码；
  - 会话的行为与时序保持不变，由现有 render 测试保护。
- **起点**：`StartSeconds` 换算成源帧号后，加到 CUE 起点上，再交给解码器起点。
- **格式协商**：
  - 复用 `Negotiate` 和 `src.Choose`；
  - Subsonic 设备的能力上限按“无上限”处理：FLAC 保留源采样率和位深，Opus 固定 48 kHz，MP3 和 AAC 取 44.1 或 48 kHz。
- **MP3 编码**：
  - `encode.go` 增加 `libmp3lame` 的 CBR 编码；
  - `zones.StreamFormat` 的编码枚举增加 `mp3`，只给单曲渲染用，播放区的格式协商不提供它；
  - 启动时用 `Tool.HasEncoder` 检查；缺少编码器时，对 `format=mp3` 的请求回退到 Opus，并在响应的 `Content-Type` 里如实标明。
- **并发**：沿用 `Tool.Reserve`，等待改为不等待（`TryReserve`，没有就补一个），拿不到名额立即返回。

## 直发原文件

- 从 `playback.Service.Prepare` 拆出“安全打开源文件”的公开函数，原直放接口在它之上保留自己的格式判断，行为不变。
- 直发使用 `http.ServeContent`，ETag 用内容指纹。
- 判断“设备没有开启任何处理”时，复用 `plan.go` 的 `hasProcessing`，与播放区判断能否逐位直通的规则一致。

## HTTP

- 渲染输出：`Transfer-Encoding: chunked`、`Accept-Ranges: none`，`Content-Type` 按实际编码填写。
- `estimateContentLength` 参数忽略。

## 听歌记录

- `scrobble` 调用 `listening.Service.Record`：
  - 幂等键为 `subsonic:<user>:<track>:<time>`；没有 `time` 时取服务器时间，按秒取整；
  - `playedSeconds` 填曲目时长；
  - 如果 `Record` 不支持传入播放时间，补一个可选参数。
- “正在播放”保存在进程内：用户 → 曲目、时间、设备名，10 分钟后过期，不落库。

## 前端（主代理）

- 设置 › 播放设备的设备列表显示 `subsonic` 设备，可以设置和删除。
- 设备选择器过滤掉 `subsonic` 设备。
- 解码器接受新的 `kind`，以及编码 `mp3`。
