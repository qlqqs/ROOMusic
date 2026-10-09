# M2b 歌词技术设计

需求与验收见 [prd.md](prd.md)，执行计划见 [implement.md](implement.md)。

## 1. 边界

- **解析器**（`library/parser`）：从标签提取原始歌词文本与来源键，只做规范化与上限检查，不解析 LRC。
- **扫描与 catalog**：歌词随观察进入暂存 JSON、organizer 与曲目写入；`catalog` 负责读取与 LRC 解析（纯函数），`catalog/httpapi` 提供接口。
- **前端**：`features/player` 新增歌词的端点、解码器、查询钩子、纯函数模型与面板；正在播放页调整布局。
- 不改曲目身份规划、Release 匹配、内容指纹与播放接口。

## 2. 提取规则

`AudioObservation` 增加 `Lyrics`（规范化后的原文）与 `LyricsSource`：

| 来源 | 规则 | `LyricsSource` |
| --- | --- | --- |
| Vorbis 注释（FLAC、Ogg Vorbis、Opus） | 键不区分大小写；有 `LYRICS` 取第一个，否则取 `UNSYNCEDLYRICS` 或 `UNSYNCED LYRICS` 的第一个 | `vorbis_lyrics`、`vorbis_unsyncedlyrics` |
| MP4 iTunes item | `©lyr` 的第一个类型为 1（UTF-8）的 `data` | `mp4_lyr` |
| ID3v2（MP3） | `USLT` 帧（v2.2 为 `ULT`）：按编码字节解码（0 ISO-8859-1、1 带 BOM 的 UTF-16、2 UTF-16BE、3 UTF-8），跳过 3 字节语言与描述；有多个帧时优先描述为空的帧 | `id3_uslt` |

规范化：去掉 UTF-8 BOM；`\r\n`、`\r` 改为 `\n`；删除 NUL 与除 `\n`、`\t` 外的 C0 控制字符；去掉首尾空白。结果为空、不是合法 UTF-8 或超过 65,536 字节时不提取（不记诊断：
歌词缺失不影响曲目可用性）。现有 M4A 元数据预算照常生效。

CUE 虚拟轨不带歌词（父文件的歌词对应整个文件，不能分给单条曲目）；`mergeSingleCueEvidence` 合并 CUE 字段时保留物理曲目的歌词。

## 3. 保存

迁移 `0015_track_lyrics.sql`（幂等写法与 0011 相同）：

```sql
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS lyrics TEXT;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS lyrics_source TEXT;
-- 约束 tracks_lyrics_ck：两列同为 NULL，或 lyrics 非空且 octet_length(lyrics) <= 65536、
-- lyrics_source IN ('vorbis_lyrics','vorbis_unsyncedlyrics','mp4_lyr','id3_uslt')
```

`SourceObservation` 增加 `lyrics`、`lyrics_source`（`omitempty`）；`persist_track.go` 的 INSERT 与 UPDATE 都写这两列，观察没有歌词时写 NULL，
因此重扫会更新或清空歌词。已入库曲目在迁移后为空，重扫一次才有歌词。

## 4. 接口

`GET /api/v1/tracks/{id}/lyrics`，注册在 `catalog/httpapi`，所有登录成员可读；检查顺序：401 → 400（非 UUID）→ 404（不存在或非 present）→ 200。
JSON 响应沿用 `no-store`。

```json
{ "track_id": "…", "lyrics": null }
{ "track_id": "…", "lyrics": { "kind": "synced", "source": "embedded", "lines": [ { "start_ms": 12340, "text": "…" } ] } }
{ "track_id": "…", "lyrics": { "kind": "plain", "source": "embedded", "lines": [ { "start_ms": null, "text": "…" } ] } }
```

`synced` 的每行 `start_ms` 都是非负整数且非递减；`plain` 的每行都是 `null`，`text` 可以是空串（分段）。`source` 目前只有 `embedded`。

`catalog.Store.LoadTrackLyrics(ctx, trackID)` 只读 present 曲目的两列；`catalog.ParseLyrics(raw)` 是纯函数：

1. 按 `\n` 分行。行首连续的 `[mm:ss]`、`[mm:ss.f]`、`[mm:ss.ff]`、`[mm:ss.fff]` 或 `[mm:ss:ff]`（分钟 1–3 位、秒小于 60）是时间戳；
   形如 `[字母或#:内容]` 的整行是元标签，全部去掉，其中 `offset` 的整数毫秒值（可带符号）参与计算。
2. 至少一行有合法时间戳时为 `synced`：每个时间戳生成一行，文本为去掉时间戳与逐字标记 `<mm:ss.xx>` 后的剩余部分（去首尾空白，可为空串，表示间奏）；
   `start_ms = 时间戳 − offset`，小于 0 取 0，超过 24 小时的行丢弃；没有时间戳的普通行丢弃；按 `start_ms` 稳定排序（时间相同的行保持原顺序，用于双语分组）。
3. 否则为 `plain`：去掉元标签行，行尾去空白，去掉首尾空行，连续空行合并为一行。
4. 最多 2,000 行，单行最多 1,000 个字符（超出截断）。`synced` 过滤后为空时按 `plain` 处理。

## 5. 前端

- **数据**：`api/lyrics.ts`（端点与解码器，读取接口样本）；`hooks/use-track-lyrics.ts`，查询键 `viewerQueryKey(generation, "player", "lyrics", trackId)`，
  会话内不过期，会话变化随现有机制清空。
- **模型**（`engine/lyrics.ts`，纯函数）：把 `synced` 行按相同 `start_ms` 分组（组内第一行为主行，其余为对照行）；`activeGroupIndex(groups, positionMs)`
  用二分查找返回最后一个 `start ≤ 位置` 的组，播放位置在第一组之前为 -1。
- **面板**（`components/lyrics-panel.tsx`）：
  - 状态：加载中；读取失败（文案与“重试”按钮）；没有歌词（“这首曲目没有内嵌歌词”）；纯文本（静态段落）；同步。
  - 同步：`<ol>` 列出各组，当前组 `aria-current="true"`，用强调色、其他组用中性色、已唱过的组降低对比度；**不改字号与字重**，行高不随当前行变化。
    媒体可拖动时每组是一个按钮，点击或 Enter 调用 `engine.seek(start_ms / 1000)`；不可拖动时组为普通文本。
  - 跟随：默认开启，当前组变化时把它滚到面板中部（`prefers-reduced-motion` 时不平滑）；用户在面板内滚轮、触摸或键盘滚动即暂停跟随并显示“回到当前行”按钮，
    点它恢复跟随并立即定位。程序触发的滚动不算用户滚动。
  - 底部注明来源：“内嵌歌词 · 逐行同步 · 点任一行跳到该处”（可拖动时）、“内嵌歌词 · 逐行同步”、“内嵌歌词 · 纯文本”。
  - 不设 `aria-live`，当前行变化不播报；面板有标题“歌词”，可被读屏按列表阅读。
- **正在播放页**：首屏改为两栏，左为封面（沿用 `minmax(200px, min(400px, 42%))`），右为歌词面板（与封面同高，内部滚动，上下渐隐）；
  标题、艺人、专辑、状态与控制移到两栏下方；再往下是信号路径与队列。容器宽度小于 760px 时单栏：封面（最大 280px）→ 标题与控制 → 歌词（最大高度 60vh）。
  没有当前曲时不显示歌词面板。
- 播放位置来自 `usePlayerProgress()`（媒体 `timeupdate`，约每秒 4 次），不另设计时器。

## 6. 测试

### 后端

- 解析器：用 `parsertest` 与测试内构造的 FLAC、Ogg、MP4、ID3 数据覆盖每种来源与优先级、三种 ID3 编码、规范化、超限、非法 UTF-8、空白；CUE 虚拟轨没有歌词。
- `ParseLyrics` 表驱动测试：多时间戳、三种小数位与冒号写法、元标签与 offset、逐字标记、间奏空行、相同时间戳保持顺序、超过 24 小时、纯文本分段、上限截断、全部无效回退纯文本。
- `TestPostgreSQL…`：迁移约束；扫描写入、改标签后重扫更新、删除歌词后重扫清空；接口 400/401/404/200 与 `null`；接口样本（同步、纯文本、无歌词）。

### 前端

- Vitest：解码器（拒绝非递减、`synced` 中的 `null` 时间、`plain` 中的数字时间）；分组与 `activeGroupIndex`；面板各状态、跟随与暂停跟随、回到当前行、
  点击与 Enter 跳转、不可拖动时不可点、减少动态效果。
- Playwright（路由模拟）：同步、纯文本、无歌词三种页面在五种宽度下不溢出，axe 无新增问题，键盘可跳转。

### 真实服务自动化

- `scripts/gen-playback-fixtures.sh` 生成带 LRC 歌词的 FLAC 与 ALAC 小素材（ffmpeg 的 `-metadata lyrics=` 在 FLAC 写入 Vorbis 注释 `lyrics`、
  在 MP4 写入 `©lyr`；MP3 的 `USLT` 无法由 ffmpeg 写出，只在 Go 测试中构造）。
- `playback.real.ts`：扫描后打开正在播放页，播放时当前行前进；点击第三行后播放位置不早于该行时间。

## 7. 取舍

- **扫描时提取**：与其他标签一致，可被后续覆盖层修正；代价是迁移与重扫。
- **服务端解析 LRC**：解析规则只有一份、由接口样本约束，前端只做分组与定位。
- **不改字号**：放弃原型的放大效果，换取换行时布局不跳动（DESIGN.md 要求评估）；收尾用截图请用户确认。
- **只读内嵌标签**：覆盖真实库全部来源；外置 `.lrc` 需要扫描跟踪额外文件，等有真实需要再做。

## 8. 兼容与回退

- 迁移只加可空列与约束；回退代码后两列闲置，不影响其他功能。
- 接口与解码器同一提交引入；正在播放页布局变化可单独回退（面板组件独立）。
