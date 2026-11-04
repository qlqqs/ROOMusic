# 收藏与歌单的前端约定（M5 ② ③）

本文件记录 `features/personal` 与 app 层组合的已实现合同。接口形状见任务
[收藏与歌单技术设计](../../tasks/11-04-classical-m5-favorites/design.md) §4–§6、§9 与后端
[个人数据合同](../backend/personal-contracts.md)；组件与状态的通则仍以[组件规范](./component-guidelines.md)、[状态规范](./state-management.md)为准。

## 归属与依赖

- `features/personal` 拥有收藏与歌单的 API、解码器、查询键、心形（`FavoriteButton`）、批量收藏状态范围（`FavoriteStatusScope`）、
  “添加到歌单…”与新建/改名对话框、收藏页与歌单页的内容（`FavoritesView`、`PlaylistsView`、`PlaylistDetailView`）、“播放全部”候选的读取
  （`usePersonalQueue`）与跨标签页刷新（`PersonalSync`）。它只导入 `shared`、`auth` 的会话范围与 `catalog` 的公开合同（封面、卡片网格、
  曲目安全摘要），**不导入 player、listening、governance**；这些 feature 也不导入 personal。
- 解码器只依赖 `shared/api` 与 catalog 的纯解码模块（`catalog/api/decoders`、`catalog/api/track-summary`），样本测试与真实服务用例可以直接导入。
  曲目安全摘要 `TrackSummary` 的解码器与显示规则（`trackTitleOf`、`trackArtistOf`）归 catalog，收藏、歌单（以后的搜索与艺人页）共用。
- app 负责组合：
  - `app/routes/track-actions.tsx` 的 `TrackActions` 是**所有曲目行的统一动作**：行内播放、心形、“更多”（下一首播放、加入队列、添加到歌单…，
    再接页面自己的项，例如“从历史中移除”、歌单的移动与移除）。专辑页、历史页、收藏页、歌单详情都用它；以后的搜索结果与艺人页同样接入，不另造一套。
  - `app/routes/track-playback.ts` 是曲目安全摘要（历史条目同形）→ `PlayerTrack` 的唯一映射与“为什么不能播放”的判断。
  - `app/routes/personal-playback.tsx` 的 `PersonalPlayAll` 是收藏与歌单的“播放全部”。
  - `app/player-personal-actions.tsx` 把心形与“存为歌单”放进 player 的中立插槽；player 不导入 personal。
- 中立插槽：`PlayerBar.renderCurrentTrackActions(track)`、`PlayerQueue.renderQueueActions(tracks)`、`NowPlayingPage` 的同名两个插槽；
  `AlbumDetailView.renderReleaseScope({ groupId, release }, children)` 包住选中版本的头部与曲目区；`HistoryView.renderRowsScope(items, children)` 包住历史表格。
  插槽函数保持引用稳定（`useCallback` 或模块级函数），曲目行才能跳过没有变化的重绘。

## 查询键与失效

- 全部在 `viewerQueryKey(generation, "personal", …)` 之下：`favorites` 前缀（两栏 `[favorites, kind, "list", {page, pageSize, revision}]`、
  批量状态 `[favorites, "lookup", {trackIds, groupIds}]`（ID 小写、去重、排序）、候选 `[favorites, "track", "queue"]`）；`playlists` 前缀
  （`list`、对话框逐页加载的 `list/picker`、`["id", playlistId, "detail" | "items" | "queue"]`、`containing-track`）。revision 没有时写 `null`。
- 心形切换成功：先把每一份已缓存的批量状态里**这一个目标**改成回执的值，再让 `favorites` 前缀失效。回执里的集合版本不当作整份旧状态的确认。
- 歌单写命令成功：先把已缓存的条目页与摘要里的歌单状态（版本号、曲目数）换成回执里的（紧接着的下一条命令不会因自己的上一条冲突），
  再让 `playlists` 前缀整体失效（列表、各歌单、`containing-track`）；删除先移除该歌单的查询。
- “播放全部”的候选用 `fetchQuery(staleTime: 0, gcTime: 0)` 在按下时读取，不后台预取、不留在缓存里。
- 跨标签页：`PersonalSync` 挂在已登录布局里，用 `BroadcastChannel("roomusic:personal")` 只发 `{scope: "favorites" | "playlists"}`，
  收到后只让自己当前代次的对应前缀失效、不再转发；窗口聚焦或页面重新可见时让 `personal` 前缀失效（不受 30 秒新鲜期限制）。
  不传用户、内容或请求载荷；数据库的版本号仍是并发权威。

## 心形

- 一页一个 `FavoriteStatusScope`（一批 ID 一次 `POST /me/favorites/lookup`），范围里每个心形按目标读取；换一批 ID 时先沿用上一批已知的状态
  （`keepPreviousData`），不闪成不可用。专辑页的范围先只含专辑，版本详情到达后加上整版曲目（最多 10,000）。播放器栏与正在播放页各自一个只含当前曲目的范围。
- 切换是乐观的：进行中的切换（任何组件发起的）由范围经 `useMutationState` 叠加成目标值显示；失败后叠加消失，自然回到服务端的状态，
  不需要逐份回滚缓存，也不会把别的目标的并发切换一起回滚。同一目标的切换共用写操作键 `[personal, "favorite-toggle", kind, id]`，
  进行中再点不起作用（`busy`，焦点留在按钮上），PUT 与 DELETE 不会交错到达。
- 失败就近说一句：“没能收藏，请稍后再试。”“没能取消收藏，请稍后再试。”“已经找不到它了。”（404）；结果不确定（网络、无效响应、5xx）时说
  “不确定是否成功，正在重新读取。”。结果不确定的目标记在按 QueryClient 隔离的共享标记里（`unconfirmed-favorites.ts`，键含会话代次），
  **所有位置**的同一目标都保持忙碌，直到某个范围读到一次在标记之后才开始、并且成功的批量状态（用单调序号比较读取开始与标记的先后，不用时钟）；
  确认时把确定的值写进每一份已缓存的批量状态再解除。重读也失败时仍然不能切换，每个位置说“没能确认是否已收藏。”并给“重新读取”（重新读取全部批量状态）。曲目行把这句话放进行内提示；播放器栏放不下，只给读屏。
- 读屏名称固定是“收藏《标题》”，状态由 `aria-pressed` 表达；状态还没读到时按钮不可用并以 `title` 说明。

## 收藏页（`/me/favorites`）

- “我的音乐”的页签顺序是收藏、歌单、历史；`/me` 用替换历史项的方式转到收藏。
- 两栏“专辑 / 曲目”写进地址：`?tab=tracks`（专辑是默认，不写）与 `page`（2 及以上才写），两栏各自翻页；超出末页时在读到真实总数后替换成末页。
- 专辑栏是与专辑库同一种网格（`AlbumGridLayout`），可见的专辑整张卡片是链接，心形在卡片下方（不放进链接）；暂时没有可见版本的专辑灰色、
  标“暂时不可用”、没有链接，仍可取消收藏。曲目栏是与历史页同样的表格与行动作，文件缺失的行灰色、标“文件缺失”、不能播放但可以取消收藏。
- **保留行**（`useRetainedRows`）：第一次读到这一页时记下顺序与内容；之后服务端已经没有的行（刚取消收藏的）留在原处、心形变空心，可以马上点回来；
  后台重新读取、窗口聚焦、其他标签页的通知都不让它消失。服务端多出这一页没显示的行时只提示“列表有更新。”与“刷新”，不自动挤进来。
  换页、换栏、按“刷新”或重新加载才按服务端的列表重新显示。保留层只是显示，不往 Query 缓存里塞伪造的收藏；还有保留行时不规范化页码。
- 翻页带集合版本：点页码链接时把当前页的 `revision` 带给目标页；服务端返回 409 `favorites_changed` 时丢掉它、从当前版本重新读，不把两个版本拼在一起。
  直接打开某一页不带版本。

## 歌单

- 写命令都带幂等键（`useIdempotentCommand`）：一次用户意图一个键；结果不确定时界面给“重试”，用**同一个键与同一个请求**重发，不换键、不重新取版本；
  改了输入再提交就是新的意图。除新建与撤销外都带读到的 `expected_revision`；409 `revision_conflict` 时说“歌单刚在别处改过，已重新读取，请再试一次。”，
  不自动用新版本重发。
- 列表（`/me/playlists`）：卡片是封面拼图（前四个不同专辑；不足四个用第一个；第一个没有封面就是文字版面）、名字、曲目数与总时长
  （口语写法，如“12 首 · 1 小时 20 分”；有时长未知的曲目时已知时长只是下限，写成“1 小时 20 分以上”，不加括号说明；全都未知只写曲目数）。“新建歌单”只要名字，建好后进入这个歌单。
- 详情（`/me/playlists/{id}`）：页签“歌单”保持选中；顶部是名字（区块标题）、曲目数与总时长、“播放全部”、改名、删除（确认文案“歌单《X》会被删除，不能恢复。”）。
  撤销让歌单超过上限时显示“歌单比上限多 N 首，先移除一些才能再添加。”。同一时间只有一条修改在进行，其他入口暂时不可用。
- 排序：行首把手用 `@dnd-kit`（`PointerSensor` 覆盖鼠标与触屏，把手 `touch-action: none`；`KeyboardSensor` 空格拿起、方向键移动、空格放下、Esc 取消，
  中文播报）。拖动只在当前页内，放下后先按新顺序显示，提交 `action: "before"`：锚点是放下后紧跟着的条目，本页最后时用 `page_after_item_id`（整个歌单最后时是 null）；
  失败时回到原顺序。“更多”里的上移、下移、移到最前、移到最后按整个歌单移动，首尾时对应项不可用；移出本页时提示移到了第几首、第几页。
  修改进行中把手不设 `disabled`（键盘放下后焦点不丢），只是不能开始新的拖动。
- 移除不弹确认：成功后提示“已移除《X》”并给“撤销”，焦点回到区块标题（打开菜单的那一行已经不在了）。撤销只带 token，按服务端回执的位置说
  “已放回《X》，在第 N 首。”或“《X》已在歌单里。”，位置在别的页时跳到那一页。只显示最近一次移除的撤销。
- “添加到歌单…”对话框：歌单按最近修改倒序逐页加载（“显示更多歌单”）；一首时已包含它的歌单标“已在歌单里”、满了的标“已满”，都不可选；
  对话框里可以直接新建并加入（同一个请求）。结果在原处说：一首“已加入《X》。”，一批“《X》：已加入 N 首，M 首已在歌单里。”。
  专辑头部的“添加到歌单…”加入当前选中版本的全部曲目（碟序、轨序）；队列的“存为歌单”按队列顺序去重（只留第一次出现），名字预填“队列 年-月-日”（本地日期）。
- 名字：去掉首尾空白后必填，最多 100 个字（按 Unicode 码点，与后端一致），不能有控制字符，可以重名。

## 播放全部

收藏的曲目与歌单的“播放全部”每次按下都读一次候选（服务端先去掉文件缺失的，再按顺序取前 10,000 首并给出省略数），
再去掉这个浏览器放不了的，用一次 `playTracks` 替换当前队列（原来的队列满了也不影响）。提示把三组数分开说：
“已选择 N 首。跳过了 M 首文件缺失、U 首不能在这里播放。另有 O 首超出 10,000 首的上限，这次没有放进来。”；一首都不能播放、读取失败或会话变了时当前队列不变。
行内的“下一首播放 / 加入队列”仍按队列剩余容量整批检查，放不下就整批拒绝。
