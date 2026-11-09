# 播放与个人状态的阶段边界

本文从[状态管理规范](./state-management.md)的 `## 播放与个人状态的阶段边界` 一节移出（2026-11-05，控制单文件体积），含播放区客户端、播放设置、计次、M5 ① 与 M5 ④ 各节；状态归属的通则仍以状态管理规范为准。

旧 `music-client-experience` 的模拟播放和本地收藏/歌单持久化只是历史任务方案，
不再作为 Classical 客户端的例外合同。M1 没有真实播放时隐藏或明确禁用播放动作，
不生成播放历史；受控响应只用于测试，不作为生产 fallback。

## 播放区客户端（服务端播放区，2026-11-09 起）

播放由服务端播放区统一处理：队列、模式、播放设置、传输状态与计次都在服务端，前端只是**输出端**（把渲染流放出来、回报位置）与**遥控**（发命令、显示状态）。
协议见[播放区协议](../backend/playback-zone-contracts.md)；旧的浏览器本地引擎（预检与 202 等待、预取、Web Audio 增益、交叉渐变、本地队列与恢复、本地计次）已删除。

**入口与订阅**：`features/player` 公开 `PlayerProvider({ userId, children })`、`usePlayer()`、`usePlayerProgress()`、`usePlayerProgressSelector()`、`usePlayerEngine()`（返回 `PlayerController`，只发命令）、
`usePlayerSelector()`、`useCurrentTrackMarker()`、`usePlaybackCapabilities()`。provider 放在已登录会话代次子树内，创建一个不依赖 React 的 `PlayerController`（`zones/controller.ts`），切页不重建。
离散快照与进度分开订阅；`PlayerSnapshot` 仍是 `status/queue/current/hasNext/failures/fault/event/volume/muted/restoring/settings/volumeControllable`，另加 `zone/device/isLocal/devices/rejection`。
大量重复的组件只用 `usePlayerEngine()` 发命令，只关心某个值的组件用 `usePlayerSelector()` 选出基本值；曲目行 `memo`。

**zone store**（`zones/zone-store.ts`）：服务端快照（设备与播放区）、正在显示的播放区队列与时钟偏移，不进 Query 缓存。每个播放区按 `revision` 丢弃旧状态；
队列按 `queue_revision` 应用增量，对不上就整段重读；偏移取最近一次带 `server_time` 的响应。发命令的标签页直接用响应更新，其他标签页经事件流更新。
进度：本机出声时用元素的流内位置按 `segments` 换算，否则按 `position` 外推，播放中每 250 ms 刷新。

**事件流**（`zones/event-stream.ts`）：`fetch` + `ReadableStream` 按行解析 SSE（不用 `EventSource`，要拿到状态码）；401 经会话边界处理，其他断开按 1、2、5、10 秒退避，页面重新可见时立即重连。
控制端标签页隐藏 30 秒后断开（HTTP/1.1 每主机 6 条连接），输出端标签页始终保持。

**启动顺序**（`attach`）：读快照 → 确认或登记设备 → 读本机播放区队列 → 连事件流 → 导入旧队列 → 排队拿输出锁。读快照失败按 1、2、5、10 秒重试；401 与 404（没有播放区接口的旧后端）不重试。

**设备与输出权**
- 设备 ID 存 `localStorage["roomusic:playback-device:v1:<userId>"]` = `{version: 1, deviceId}`，边界解码，坏了当没有；同一浏览器的标签页共用，退出登录不删。
  首次登记在 Web Lock `roomusic:device-registration:<userId>` 里串行；认领得 404（被清理或删除）或收到本设备的 `device_removed` 就重新登记。名称按 UA 生成“系统 · 浏览器”。
- 每个标签页排队请求 Web Lock `roomusic:output:<deviceId>`：拿到的创建输出端（`output/output.ts`）并认领，排队等到的认领带 `takeover: true`；其他标签页是纯控制端，不创建音频元素。没有 Web Locks 时直接出声。
- 输出端用两个 `<audio>` 元素按协议的对齐规则换流、预载接续流；回报经 `output/report-queue.ts` 串行发送；`waiting` 超过 15 秒或元素出错按 `stream_lost` 回报。
  `pagehide` 与 `detach` 时以 `keepalive` 请求释放。本机第一次点击或按键时同步解锁两个元素，之后远程起播、换流不再需要手势。
- 能力声明（`output/capabilities.ts`）只用 `canPlayType`：长流候选按 FLAC、WAV、Opus、AAC 顺序；音量只读（写 0.5 读回不变，即 iPhone/iPad）且两项 HLS 候选（ALAC、AAC）都能探测到时只声明 HLS。
  `max_channels: 2`、`dsd: "none"`、`exclusive: false`。
- 锁屏信息由 `MediaSessionBinding` 按 `segments` 在曲目边界本地切换（标题、艺人、专辑、封面与时长）；媒体键与系统暂停总是发给本机播放区。

**控制目标**：存 `localStorage["roomusic:control-target:v1:<userId>"]` = `{version: 1, zoneId, selectedAt}`，`storage` 事件同步标签页；没有或无效时是本机。
有效 = 播放区还在、设备在线，并且正在播放或停下不到 15 分钟（从 `paused_at` 与 `selectedAt` 中较晚的算起）；每分钟与页面重新可见时重算，失效就清掉回到本机。转移成功后目标跟到目标播放区。

**命令与音量**：控制器的方法名沿用旧引擎（`playTracks/playNext/enqueue/playEntry/removeEntry/moveEntry/clearQueue/setMode/play/pause/togglePlayback/stop/next/previous/seek/retry/setPlaybackSettings/setVolume/setMuted`），
另有 `setControlTarget/transfer/renameDevice/deleteDevice`。命令发给有效控制目标，按顺序一条条发、不自动重试；命令得 `409 zone_offline` 时记为 `rejection`（转移除外），下一条命令成功或换控制目标时清掉，其他失败不记（界面规则见[播放器规范](./player-design-guidelines.md)的错误矩阵）；三种入队动作在本地先按 10,000 上限判断，返回 `added|queue_full`。
音量属于设备：本机先立即写元素，再按 125 ms 节流发 `PUT volume`，1 秒内以本地值显示；控制的设备 `volume: fixed` 时音量控件禁用。

**旧版本留下的浏览器数据**（只读，`engine/persistence.ts`、`engine/playback-settings.ts`）
- 旧队列 `sessionStorage["roomusic:player:v1:<userId>"]`：每个标签页登记完成后读一次（沿用旧的边界解码），有内容就向本机播放区发 `import`；
  请求有了结果（不论服务端是否采用）或得到 4xx 就删除这个键，网络失败或 5xx 留到下次打开再导。服务端只在播放区为空且没导入过时采用，所以多个旧标签页只有第一个生效。
- 旧偏好 `localStorage["roomusic:player-volume:v1"]`（音量、静音）与 `roomusic:playback-settings:v1`（音量均衡、交叉渐变与时长）只在登记设备时读作初值，不再写入，也不删除。
- 浏览器不再保存队列、播放位置、播放设置或计次；这些都以服务端为准。

**会话边界**：普通卸载执行 `detach()`：断开事件流、释放输出权并停声，服务端的队列与状态不受影响。会话控制器 `onEnded` 同步执行 `endSession()`：在 `detach` 之后清掉界面上的账号状态并不再接受命令，
存着的设备 ID 与控制目标保留。`401`、跨标签页核对发现退出、换查看者都走这一边界。

歌词是服务器状态：`useTrackLyrics(trackId)`（键 `playerKeys.lyrics`，`staleTime: Infinity`，随代次清空）；面板的跟随与暂停跟随是组件内局部状态。
能否播放只看服务端格式表（`usePlaybackCapabilities`，键 `playerKeys.formats`，会话内有效），与设备无关，不特判来源类别（CUE 分轨同样按表放行）。

## 分析数据与播放设置（2026-11-09 更新）

后端合同见[音频分析合同](../backend/audio-analysis-contracts.md)；界面（波形、信号路径、全屏、设置页）见[播放器规范](./player-design-guidelines.md)。

- 曲目分析：`trackAnalysisQuery(generation, trackId)`，键 `playerKeys.analysis(g, trackId)`；`status=done` 时 `staleTime: Infinity`，`pending/failed` 5 分钟；`retry: false`，读取失败按“没有分析结果”处理、不提示。
  波形进度条与信号路径的响度一行共用这份缓存。电平化增益改由服务端渲染会话选择与施加，前端不再读分析结果算增益。
- 资料库统计：`catalogKeys.stats(g)`，在 `catalog` 前缀下，随 catalog 整体失效刷新。分析进度：`libraryKeys.analysisProgress(g)`，`running`/`waiting_for_scan` 时每 60 秒轮询（页面隐藏时暂停）。
- **播放设置**属于播放区（服务端），快照的 `settings` 是正在控制的播放区的设置；`setPlaybackSettings(patch)` 发 `set_settings`，`headroom` 总是整对象发送。
  设置页“设置 › 播放”作用于正在控制的那台设备，标题旁显示设备名；还没读到播放区时全部禁用。

## 计一次播放与最近播放

计次由服务端按输出端回报的播放头判定（规则见[播放区协议](../backend/playback-zone-contracts.md) §3.5 与[播放历史合同](../backend/listening-contracts.md)），前端不累计、不上报；
`POST /me/plays` 只为旧标签页保留到下一版。播放区事件 `play_recorded` 第一次出现时（每个 `seq` 一次），provider 让 `listeningKeys.all(generation)`（本人的全部历史查询）失效。
“最近播放”是服务器状态（键 `viewerQueryKey(generation, "listening", "recent-releases")`）；清除是写操作：成功后 `setQueryData` 置空并失效，不等重新读取，
失败时保留旧数据、对话框里显示错误，DELETE 幂等所以可以直接重试。

## M5 ① 播放历史页的查询键与失效

- 键都在 `viewerQueryKey(generation, "listening", …)` 之下，会话切换（代次变化）自然隔离：`listeningKeys.all` 是前缀；`recentReleases`；`tracks`（历史页全部分页的前缀）与
  `trackPage(generation, { page, pageSize })`（一页，页大小固定 50，显式传给后端）；`queue`（“播放全部”的候选，最近最多 1,000 首）。
- 失效责任：上报成功、按曲目移除、清除全部之后都让 `listeningKeys.all(generation)` 失效（概览“最近播放”、历史页与候选一起）；管理员修改专辑数据后
  `useReleaseDataRefresh`（app）同样失效这个前缀（标题、归组会改变历史里显示的专辑）。移除成功先用 `setQueriesData` 从已缓存的各页去掉这一行，
  清除成功先把“最近播放”与各页置空，都不等重新读取；失败时保留旧数据、对话框里显示错误，DELETE 幂等可直接重试。
- “播放全部”每次按下都经 `queryClient.fetchQuery(queue, staleTime: 0)` 读最新候选，不沿用旧缓存；结果只在发起时的会话仍是当前会话时交给调用方。
- URL：`/me/history?page=N`，`page` 经 `parsePageParam` 解析，只在 2 及以上写进地址；“我的音乐”没有共同的页签布局（2026-11-07 起，四页在侧栏各有入口，见[收藏与歌单的前端约定](./personal-guidelines.md)），`/me` 用替换历史项的方式转到 `/me/liked`。
  与专辑、扫描记录不同，**页码超出末页时不规范化地址**（不使用 `useClampedPage`）：显示“这一页没有记录。”与“回到第一页”的链接（产品裁决，2026-11-04）。
  点页码链接时把结果区标题滚到视野顶部并聚焦（与专辑列表相同）。

## M5 ④ 全局搜索的查询键、URL 与目录状态

- 目录状态 `GET /catalog/status`（`{ epoch, search_mode, artists_state }`）是服务器状态，键 `catalogStateKeys.current(g)`（`viewerQueryKey(g, "catalog-state")`，不在 `catalog` 前缀下）。
  已登录外壳挂载时读取，回到前台总是重读（`refetchOnWindowFocus: "always"`），`search_mode=legacy` 或 `artists_state=preparing` 期间每 3 秒轮询（页面隐藏时暂停），都就绪后停止；
  其他使用者 `refetchOnMount: false`，不重复请求。④ 的 `artists_state` 固定为 `unavailable`，不算在准备。
- 搜索的键带两个代次：`g` 是会话代次，`e` 是目录 epoch，二者不能混用。`searchKeys.suggestions(g, e, q, limit)`、`searchKeys.page(g, e, type, q, page, pageSize)`，
  q 是去掉首尾空白的用户输入，不在浏览器里做规范化。目录状态还没读到时搜索不发请求；状态读取失败时 e 记为 -1，搜索照常。
- 搜索响应带回的 `catalog_state.epoch` 比缓存新时：先把这份结果放进新 epoch 的键下，再更新目录状态（页面按新键直接显示，不重复请求）；旧 epoch 的迟到响应只落在旧键下，不会把状态改回去。
  外壳看到 epoch 变大时取消仍在路上的旧 epoch 搜索请求、让 `catalog` 前缀失效（专辑页带搜索词的结果换成新的匹配方式），不动地址里的查询词与页码，不碰播放器。
- 修改专辑数据之后（`useReleaseDataRefresh`）同时让 `searchKeys.all(g)` 失效：修正后的字段立即参与匹配。
- URL：`/search?q=&albums_page=&tracks_page=`；q 与专辑列表同一个校验（去掉首尾空白后非空且不超过 200 字节），页码只在 2 及以上写进地址；换 q 时两栏都回到第 1 页，翻一栏只改这一栏的页码。
  ⑤ 加入艺人栏时增加 `artists_page`。页码超出时显示“这一页没有结果”与回到第一页的链接，不改写地址；搜索的 total 超过 1,000 时只是下界（显示“1,000+”），翻页只看 `has_more`，不计算末页。
- 搜索框的草稿、建议浮层的开合与选中项是组件内局部状态；在结果页时输入框跟着地址里的 q 走（“按属性重置状态”）。输入稍停 250 ms 才查询建议，输入法组合中不查询；
  换词时旧请求经 AbortSignal 取消，慢响应只落在它自己的键下。
- 定位曲目：建议里点曲目进入 `/albums/$groupId?release=<曲目当前所在的发行>&track=<曲目>`（`track` 同样只留 UUID、转小写；规范 `release` 时保留它，切换版本的链接不带它）。
  每来到一条历史记录（打开、刷新、后退、前进，按历史记录的 key 区分）只定位一次：滚到这一行、焦点放上去、短暂高亮；重新渲染与轮询刷新不再滚动或抢焦点，曲目不在选中的版本里就什么也不做。
