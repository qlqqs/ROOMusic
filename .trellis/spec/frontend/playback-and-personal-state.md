# 播放与个人状态的阶段边界

本文从[状态管理规范](./state-management.md)的 `## 播放与个人状态的阶段边界` 一节原样移出（2026-11-05，控制单文件体积），含 M2a、阶段 P、M5 ① 与 M5 ④ 各节，内容未改；状态归属的通则仍以状态管理规范为准。

旧 `music-client-experience` 的模拟播放和本地收藏/歌单持久化只是历史任务方案，
不再作为 Classical 客户端的例外合同。M1 没有真实播放时隐藏或明确禁用播放动作，
不生成播放历史；受控响应只用于测试，不作为生产 fallback。

## M2a 播放生命周期与恢复合同

`features/player` 的公开入口导出 `PlayerProvider({ userId, children })`、`usePlayer()`、
`usePlayerProgress()`、`usePlayerProgressSelector()`、`usePlayerEngine()`、`usePlayerSelector()`、`useCurrentTrackMarker()`、`usePlaybackCapabilities()`。
provider 放在已登录会话代次子树内，只装配一个分离的 `Audio()` 与不依赖 React 的 `PlayerEngine`；切页不重建。
离散快照通过 `useSyncExternalStore` 订阅，进度独立订阅，避免每次 `timeupdate` 重绘整队列。
大量重复的组件（专辑页每行动作）只用 `usePlayerEngine()` 发命令；只关心某个值的组件用 `usePlayerSelector()` 选出基本值
（外壳只看队列是否为空，专辑页只看当前曲目与状态），音量、队列变化不重绘整张曲目表；曲目行 `memo`，插槽函数保持引用稳定。
`useReleaseLoader` 经 catalog 公开入口复用会话范围的 Query 缓存，格式表 Query 在会话内保持有效。
歌词是服务器状态：`useTrackLyrics(trackId)`（键 `viewerQueryKey(generation, "player", "lyrics", trackId)`，`staleTime: Infinity`，会话结束随代次清空）；
面板的跟随与暂停跟随是组件内局部状态，不进入引擎快照，也不持久化。

普通卸载执行 `detach()`：中断媒体与异步工作，但保留刷新恢复记录；
会话控制器 `onEnded` 同步执行 `endSession()`：停止音频、清空 `src` 并 `load()` 中断网络，
清空媒体键、队列与本账号保存记录，不等待 React 卸载。`401`、跨标签核对发现退出、换查看者
均走这一边界。旧请求代次与引擎异步令牌阻止迟到响应覆盖新曲目/新账号。
不能把“普通卸载”和“结束会话”混为一谈，否则刷新恢复记录会被删除。

队列条目为 `{ entryId, track }`，同曲重复项按 `entryId` 操作。快照包含
`status/queue/current/hasNext/failures/fault/event/volume/muted/restoring`；`status` 含 M2b 的 `preparing`（预检收到 202，服务端正在无损转换），
`fault` 含 `busy`（503 `derivation_busy`，与 `transient` 共用重试）；预检结果是 `ready | preparing`，状态码与响应体不符按无效响应处理。
预取下一首派生曲目的预检由引擎持有独立的取消控制器，不进入快照；进度快照包含
`positionSeconds/durationSeconds/seekable`。只有实际媒体事件才能更新正在播放、进度与时长，
未知时长为 `null`。失败条目与可重试暂停分别表达，不靠模拟时钟推断结束。

恢复键 `sessionStorage["roomusic:player:v1:<userId>"]`，只存 version、entryId/trackId/releaseId/groupId、
currentEntryId、mode、positionSeconds 与有限的随机“上一首”历史（条目 ID），不存标题、路径或凭据。
边界解码版本、UUID、条目唯一性、数量与当前项归属；损坏/未知版本删除，不可用存储安全降级。
按发行 ID 去重、最多 4 个并发重新加载详情；只有明确 404 或详情中缺少的曲目才丢弃并提示数量，
网络/503 等暂时失败保留原恢复记录并提供重试，不能把临时故障当作不存在删除。恢复后保持暂停，首播重新预检后定位。
普通队列动作、暂停、切歌与 pagehide 保存，播放中每 5 秒保存；写失败不声称已保存。
开始位置在元数据与可定位范围到达后才生效，此前保存或出错重试用待定位的位置，不写成 0；在队首按“上一首”从头重播时作废该位置；队列未变时复用已序列化的条目。
音量/静音是设备偏好，存 `localStorage["roomusic:player-volume:v1"]`，不随账号清除。

收藏与歌单由服务端按用户保存（M5 ② ③，查询键与失效见[收藏与歌单的前端约定](./personal-guidelines.md)），播放历史同样由服务端保存（阶段 P），队列恢复不构成浏览器个人库权威。以后新增的缓存或偏好持久化同样要带版本与边界校验，
含账号数据的按账号命名（不含私有数据的设备偏好如音量除外）；损坏、未知版本、禁用存储都应可恢复，写失败不能声称“已保存”。
Cookie、凭据、服务器路径和治理操作结果不得写进浏览器持久层。
具体交互与错误矩阵见[播放器规范](./player-design-guidelines.md)；API 见[播放合同](../backend/playback-contracts.md)。

## 阶段 P 播放上报与最近播放

引擎经依赖 `playRecorder: { record, discard }` 交出达到计次规则的播放，不碰网络；`PlayerProvider` 与引擎一起创建 `createViewerPlayReporter(queryClient, generation)`，
把 `report` 接到 `record`、`dispose` 接到 `discard`（`endSession` 调用），所以上报器的生命周期等于引擎的：普通卸载与 StrictMode 重挂载沿用它，会话结束作废它。
上报器是模块内的局部状态（待发送与等待重试的上报，最多 50 个），不进入 Query 缓存，也不持久化；成功后让 `listeningKeys.all(generation)`（本人的全部历史查询）失效。
“最近播放”是服务器状态（键 `viewerQueryKey(generation, "listening", "recent-releases")`）；清除是写操作：成功后 `setQueryData` 置空并失效，不等重新读取，
失败时保留旧数据、对话框里显示错误，DELETE 幂等所以可以直接重试。

## M5 ① 历史页签的查询键与失效

- 键都在 `viewerQueryKey(generation, "listening", …)` 之下，会话切换（代次变化）自然隔离：`listeningKeys.all` 是前缀；`recentReleases`；`tracks`（历史页全部分页的前缀）与
  `trackPage(generation, { page, pageSize })`（一页，页大小固定 50，显式传给后端）；`queue`（“播放全部”的候选，最近最多 1,000 首）。
- 失效责任：上报成功、按曲目移除、清除全部之后都让 `listeningKeys.all(generation)` 失效（概览“最近播放”、历史页与候选一起）；管理员修改专辑数据后
  `useReleaseDataRefresh`（app）同样失效这个前缀（标题、归组会改变历史里显示的专辑）。移除成功先用 `setQueriesData` 从已缓存的各页去掉这一行，
  清除成功先把“最近播放”与各页置空，都不等重新读取；失败时保留旧数据、对话框里显示错误，DELETE 幂等可直接重试。
- “播放全部”每次按下都经 `queryClient.fetchQuery(queue, staleTime: 0)` 读最新候选，不沿用旧缓存；结果只在发起时的会话仍是当前会话时交给调用方。
- URL：`/me/history?page=N`，`page` 经 `parsePageParam` 解析，只在 2 及以上写进地址；`/me` 用替换历史项的方式转到第一个页签 `/me/favorites`（M5 ② ③ 起；页签顺序收藏、歌单、历史）。
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
