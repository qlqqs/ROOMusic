# 艺人与别名的前端约定（M5 ⑤）

本文件记录 `features/artists` 与相关 feature、app 层组合的已实现合同。接口形状见任务
[搜索与艺人联合设计](../../tasks/archive/2026-11/11-04-classical-m5-search/design.md) §7–§12 与后端[艺人合同](../backend/artist-contracts.md)；
艺人收藏的接口见[收藏与歌单技术设计](../../tasks/archive/2026-11/11-04-classical-m5-favorites/design.md) §8。组件与状态的通则仍以
[组件规范](./component-guidelines.md)、[状态规范](./state-management.md)、[收藏与歌单约定](./personal-guidelines.md)为准。

## 归属与依赖

- **catalog 的纯数据模块**拥有各处共用的艺人合同：`ArtistRef`（`artist_id, name, display_name`，`api/decoders.ts`）、`ArtistSummary`、
  七个角色与首字母分组的词表及中文名（`api/artist-summary.ts`），以及曲目摘要上的回退规则 `trackArtistRefsOf`、`albumArtistRefsOf`（与
  `trackArtistOf`、`albumArtistOf` 同一个回退，名字与链接不会错配）。search、personal、governance、artists 都从这里取，互不导入解码器。
- **catalog 的 `ArtistNames`**（`components/artist-names.tsx`）是唯一把署名渲染成艺人链接的组件：只有艺人数据就绪（`artists_state=ready`）且有引用时，
  每位艺人一个 `/artists/{artist_id}` 链接（文字用这处署名的原写法，多位之间“ / ”），否则原样文字，**不加“正在整理”之类的说明**。
  就绪与否由外壳里的 `ArtistLinksScope` 读一次目录状态、经 context 提供，曲目行不各自订阅查询。专辑卡片整张是链接，卡片上的艺人名仍是文字。
  用在：专辑页标题下的专辑艺人、曲目行的曲目艺人与每条署名、“制作人员”、播放器栏与正在播放、历史、收藏、歌单、搜索结果与艺人页的曲目行。
- `features/artists` 拥有艺人列表与艺人页的 API、解码器、查询键、URL 校验（`artist-search.ts`）与视图；只导入 catalog 的公开合同，
  **不导入 player、personal、governance、search**。艺人页的播放、心形、管理员操作与曲目行动作都经插槽由 app 组合（`app/routes/artist-detail-page.tsx`）。
- governance 拥有艺人的治理（`use-artist-commands.ts`、合并/拆开/改名对话框、“重复艺人”的候选页）；它可以导入 artists 的公开入口（找人、读详情取版本号），
  artists 不导入 governance。
- `PlayerTrack` 增加可选的 `artistRefs`：两个构造入口（`playerTrackOf`、`playerTrackOfSummary`）都给出；缺省等于空数组，只用于显示，不进入刷新恢复的记录。
- 队列里已有曲目的显示元数据（I4）：引擎只提供 `updateTrackMetadata(patches)`（纯函数 `patchTrackMetadata`，按曲目 ID 只换标题、艺人与 `artistRefs`、专辑名与专辑艺人、封面；
  条目 ID、顺序、当前项、历史、随机袋、播放状态与进度都不变，不重新加载音频，不写恢复记录）。由 app 的 `PlayerMetadataRefresh`（已登录布局里，包住外壳）驱动：
  艺人数据变为就绪、就绪时 epoch 变大、艺人治理成功（`useArtistDataRefresh` 失效完之后）各刷新一次；从当前曲目起往后再到前面、去重后最多 1,000 首，
  一次 `POST /tracks/summaries`（catalog 的 `fetchTrackSummaries`，响应复用 `trackSummaryDecoder`），不在结果里或请求失败的保持原样；新一轮开始后旧一轮不再写入。第一次读到目录状态时不刷新。

## 地址与查询键

- `/artists?q=&initial=&role=&sort=album_count&page=`：q 与专辑列表同一个校验；initial 是 A–Z、`kana`、`#`，只在按名字排序时保留；
  role 是七个角色之一；按名字排序是默认、不写；任何筛选改变都回到第 1 页；越界页码替换成末页。按名字排序时按首字母分组显示，按专辑数排序时不分组。
- `/artists/{id}?albums_page=&track_role=&tracks_page=&names_page=`：专辑、曲目、名字各自翻页；曲目栏按角色分栏（只列有曲目的角色），换角色时曲目回到第 1 页。
  路径不是 UUID 时不发请求、显示“艺人不存在”；详情给出 `redirect_artist_id` 时用替换历史的方式带到现在的艺人页。
- `/me/favorites?tab=artists`：“我的收藏”的艺人一栏；`/search?artists_page=`：搜索结果的艺人栏页码；`/operations?artist=`：这位艺人的操作记录（与 release、group 取并集）；
  `/artist-candidates?status=&page=`（管理员）：“重复艺人”。`status` 只接受 `ignored`，不传就是待处理；切换时不带页码，回到第一页；
  两个状态链接用 `activeOptions={{ explicitUndefined: true }}`，只有当前那个是 `aria-current`。
- 侧栏“艺人”与“重复艺人”、“我的收藏”的“艺人”一栏只在 `artists_state` 是 `preparing` 或 `ready` 时出现；`unavailable`、状态还没读到或读取失败时不出现（I5）。
- 键在 `viewerQueryKey(g, "artists", …)` 之下，列表、详情、名字、专辑、曲目都带目录 epoch（`{ e }`）：艺人数据发布或重新整理后 epoch 变大，页面自动按新键重读；
  目录状态还没读到时不发请求，读取失败时 e 为 -1。“播放全部/随机播放”的候选每次按下用 `fetchQuery(staleTime: 0, gcTime: 0)` 读，随机每次换一个 seed。
- 艺人的收藏状态与曲目、专辑**分开请求**（`personalKeys.favoriteArtistLookup`，同在 lookup 前缀下，心形切换一起修补）：整理中带艺人的 lookup 整体 503，
  不能连累曲目与专辑的心形。lookup 的艺人按输入 ID 与 `resolved_artist_id` 都能匹配（合并过的旧 ID 与现在的 ID 是同一位）。
- 失效：外壳看到 epoch 变大时，在原有的目录失效之外让 artists、personal 的 favorites 前缀与 listening 失效；艺人治理成功后（`useArtistDataRefresh`）让 catalog、
  artists、search、favorites、listening 失效，governance 前缀由命令自己失效。

## 整理中与错误

- 读艺人列表、艺人页、收藏的艺人一栏、候选页得到 503 `artist_catalog_preparing` 时只说一句“艺人信息正在整理，请稍后再试。”，不显示成空列表或“未收藏”；
  epoch 变大后自动重读。别处的艺人名在整理中是纯文字；搜索的艺人栏与艺人建议只在就绪时出现，整理中不出现、不说明。
- 艺人的心形切换得到 409 `artist_catalog_not_ready` 时就近说同一句话；治理命令的 409 按错误码选文案（`artistFailureMessages`），
  不把整理状态当成数据库故障。

## 治理界面

- 命令模型与发行的相同：一次意图一个幂等键，结果不确定时用同一个键“重试”；悲观更新，成功或失败都刷新；`revision_conflict` 说明“艺人刚被修改过”。
  合并、拆开、改名与忽略**都没有撤销**：确认前各用一句话说明（“合并不能撤销，需要时可以再拆开。”“拆开不能撤销，需要时可以再合并。”“改名不能撤销，需要时可以再改。”），
  操作记录的“撤销”位置写反向的办法（“需要时可以拆开”……）。
- 合并：在艺人页搜到另一位并入当前艺人；从候选合并时先选保留哪一位的主名（默认参与更多的一位），两边的版本号读详情得到。
- 拆开（I1、I2）：
  - 最前面列出之前并入这里、现在可以拆回原身份的艺人（管理员接口 `GET /artists/{id}/merged-sources`，前端不从操作记录推断；没有时这一块不出现），
    每位一个“拆回《X》”：一键选好别名方式与那次合并带过来、现在仍在这里的名字键，目标恢复这位（收藏和原来的链接随之回来）。
  - 手动选名字时，所选名字键全部来自同一位已并入的艺人就默认恢复这位，否则默认新艺人；管理员明确选过“拆给谁”之后按他选的。
  - 别名整体拆回，或按专辑分给同名的另一位（名字按名字键成组，同一个键的写法一起拆）。按专辑时的专辑来自 `GET /artists/{id}/albums?participation=any`：
    以任何身份参与的专辑，每项标出身份，可按专辑名筛选（`q`）、在对话框里翻页，翻页与筛选时已选的保留。
  - 先预览（版本数、曲目数、已分给别人而不动的处数），再确认；预览过时（409 `artist_scope_changed`）自动重新预览。
- 改名：写一个主名；已经改过时也可以改回最常见的写法。
- 候选：只是提示，理由写看到了什么（如“同一张专辑的专辑艺人写‘甲’，曲目艺人写‘乙’”“‘甲（乙）’括号里写着‘乙’”），不写结论；可以合并或忽略，忽略后的提示里可以马上“恢复提示”（反向操作），之后也能在“已忽略”里逐条恢复，最近忽略的在前。
- 操作记录：目标是艺人的记录在“对象”列链接到艺人页（已合并的会被带到现在的艺人）；类型名“合并艺人”“拆开艺人”“修改艺人主名”“忽略重复艺人提示”“恢复重复艺人提示”。
