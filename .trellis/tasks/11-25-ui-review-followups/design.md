# 设计

## 1. 混合文件夹的无专辑标签曲目（后端，`internal/catalog/ingest`）

现状：`OrganizeObservations` 对普通目录按 partition 拆成 `same_dir_split` 时，partition 为 `|`（没有真实专辑标签）的观察各自成一个候选（Partition `|@<路径>`），但观察里仍保留解析器 `applyFilenameFallback` 推断出的 `Album = 文件夹名`（`InferredFields["album"]=true`），`chooseValue` 取到它，于是标题是文件夹名。

改法：在 `organizer.go` 的 `part == "|"` 分支里，与 `directoryLooseSingles` 分支一样，在加入候选前清空推断的专辑名（`unknown.Album = ""`，仅当 `InferredFields["album"]`），标题回落到 `unknownCandidateTitle`（“未知专辑”）。候选种类与锚点（`same_dir_split`、`|@<路径>`）不变，因此发行身份不变，只有标题改变。

版本：归组规则变化，`ingest.GroupingVersion` 4 → 5（与第 3 条同批，合计加一）。

规范：`.trellis/spec/backend/library-scan-grouping.md` 第 75–78 行附近补一句；`library-scan-incremental.md` 的 GroupingVersion 历史补一行。

## 2. 参与的专辑（后端 + 前端）

后端合同：`ListArtistAlbums` 的 `Participation` 增加 `appears_on`。集合 = `any` 的参与组 − 默认（`album_artist`）列表会返回的组；roles 与 `any` 同样计算（非空）；`q`、排序、分页、`credited_release_id` 与 `any` 一致。HTTP 层 `artistReadEnum` 允许值加 `appears_on`。实现上复用 `artistAlbumsSQL`：`appears_on` 走 `any` 的 CTE，在 `credited` 里排除“组内任一可见发行有该艺人 album_artist 有效署名”的组（与默认列表的组集合判定一致）。

前端：`fetchArtistAppearsOnAlbums` / `useArtistAppearsOnAlbums`（解码器复用 participation 页），艺人页在“专辑”后加 `AppearsOnSection`：标题“参与的专辑”，动作区“N 张”，卡片与专辑栏同一种 `ArtistAlbumCard`，分页参数 `appears_page` 写进地址（`ArtistDetailSearch` 增加字段与校验）。total 为 0 且在第 1 页时不渲染。

## 3. AAC 位深（后端，`internal/library/parser`）

`m4a.go` 解析 sample entry 时只在 `alac` 时采用 `samplesize`（随后仍由 alac cookie 覆盖为真实值）；`mp4a` / `enca`（当作 AAC）不写 `BitDepth`。`nullablePositiveInt` 已把 0 存成 NULL，前端各处已按 null 不显示，前端无需改。

版本：`.m4a` 格式版本 1 → 2；`GroupingVersion` 与第 1 条合计加一。规范 `library-scan-incremental.md` 版本历史补记。

## 4. 桌面音量条（前端，`features/player/components`）

`player-controls.tsx` 新增 `InlineVolume`：静音按钮（图标随音量变化，`aria-pressed`）+ `input[type=range]`（`aria-label="播放器音量"`、`aria-valuetext` 百分比，0–1，步长 0.01），宽约 96px，音量条填充用强调色（`--value` 自定义属性驱动背景渐变）。不能调音量时两者禁用，外层 `title` 给原因；局域网设备保留“设备音量”字样。`player-bar.tsx` 桌面分组用 `InlineVolume` 取代 `VolumeButton`。`VolumeButton`/弹层若别处仍用（全屏播放器）则保留，否则删除。

## 5. 管家标签（前端，`app/shell/tab-bar.tsx`）

`useStewardEntry` 增加 `ready: StewardReadiness | undefined`（由已读到的设置计算，复用 `stewardReadiness`）。标签栏过滤条件改为 `steward.available && steward.readiness !== "not_configured"`。侧栏与其它入口不变。未读提示不受影响（未配置时不会有会话）。
