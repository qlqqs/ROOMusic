# 阶段 P 播放历史合同

## 1. 范围与触发

每个用户各自保存“真实播放过什么”：一条记录表示“这个用户把这首曲目播放到了计次规则要求的程度”。服务端校验规则、按用户隔离、
换算“最近播放的发行”；前端按媒体事件累计真正出声的时间，达到规则时带幂等键上报，概览显示最近播放的专辑并可清除自己的历史。
修改 `play_history` 表、计次规则、三个接口、最近播放的读取规则，或前端的计次与上报行为时，先读本文件并同步更新。
需求与取舍见[播放历史任务](../../tasks/10-11-classical-p-play-history/prd.md)与它的[技术设计](../../tasks/10-11-classical-p-play-history/design.md)；
前端的计次实现与概览区块的状态规则见[播放器设计规范](../frontend/player-design-guidelines.md)与[状态管理规范](../frontend/state-management.md)。

范围外（M5 起）：完整历史页、收藏、歌单、播放统计与推荐、“继续聆听”的进度；暂停记录的开关、删除单条记录；管理员查看他人的播放；
外部服务同步；浏览器本地历史的迁移（开发阶段不做兼容）。

## 2. 签名

- 表 `play_history`、索引 `play_history_user_track_idx`（迁移 `0016_play_history.sql`），列与约束见第 3 节。
- `catalog.Store.LoadTrackDuration(ctx, trackID) (float64, error)`：present 曲目的扫描时长，未知（NULL、≤ 0、NaN、无穷）为 0；
  曲目不存在或不是 `present` 为 `catalog.ErrTrackNotFound`。
- `catalog.Store.LoadTrackPlacements(ctx, trackIDs) (map[string]TrackPlacement, error)`：每首 present 曲目的标题、组 ID 与所在发行的摘要
  （与发行列表同一个投影，含封面）；不是 UUID 的输入被跳过，键是小写 UUID，去重后最多 1,000 个，超出为错误；缺失与不存在的曲目没有条目。
- `listening.Threshold(duration)`、`ValidPlayedSeconds(seconds)`；常量 `MaxThresholdSeconds = 240`、`ThresholdToleranceSeconds = 1`、
  `MaxPlayedSeconds = 86400`、`RecentReleaseLimit = 8`。
- `listening.NewService(*sql.DB)` 自己创建 `catalog.Store`（与 `playback.NewService` 相同，`app` 不导入 catalog）；
  `Service.Record(ctx, userID, idempotencyKey, trackID, playedSeconds) (Recorded, error)`、`RecentReleases(ctx, userID)`、`Clear(ctx, userID) (int64, error)`。
  错误：`ErrTrackNotFound`（即 `catalog.ErrTrackNotFound`）、`ErrInvalidPlay`、`ErrPlayTooShort`、`ErrIdempotencyConflict`。
- HTTP（`listening/httpapi`）：`POST /api/v1/me/plays`、`GET /api/v1/me/plays/recent-releases`、`DELETE /api/v1/me/plays`。
- 依赖：`listening` 只导入 `catalog`；`listening/httpapi` 导入 `identity`、`listening`、`platform/httpserver`、`platform/uuid`；
  `app` 装配。声明在 `internal/app/dependency_rules_test.go`。`listening` 不读 catalog 的私有表，也不被 `playback` 导入。

## 3. 数据与计次规则

| 列 | 规则 |
| --- | --- |
| `id` | UUID 主键 |
| `user_id` | 引用 `users`，`ON DELETE CASCADE`：用户目前不能删除，将来若可以，个人数据随之删除 |
| `track_id` | 引用 `tracks`，不级联：曲目从不物理删除（只标 `missing`），将来若有删除曲目的操作，必须先显式处理历史，不能静默丢记录 |
| `idempotency_key` | UUID，`UNIQUE (user_id, idempotency_key)`：作用域是用户，另一个用户用同一个键得到的是自己的新记录 |
| `played_seconds` | 达到规则那一刻累计的实际出声秒数（约等于门槛，不是这次一共听了多久）；`CHECK (> 0 AND <= 86400)` |
| `played_at` | 服务端收到上报的时间（UTC 输出），不信任客户端时钟；重试晚到几秒到几十秒不影响顺序 |

不存 IP、User-Agent、设备或会话信息。没有自动过期，只有本人可以清除。

**门槛** `T = min(D / 2, 240 秒)`，`D` 是扫描得到的曲目时长（`tracks.duration_seconds`，前端是 `PlayerTrack.durationSeconds`）：
前后端用同一个 `D` 与同一个公式。`D` 未知（不是正的有限数）时 `T = 240`。服务端：`played_seconds` 必须是 `(0, 86400]` 内的有限数；
`D` 已知时再要求 `played_seconds ≥ T − 1`（1 秒容差吸收浮点与事件粒度），否则 `400 play_too_short`；`D` 未知时没有可依据的门槛，只校验数值范围。
服务端无法证明用户真的听了，它保证的是规则一致与数值合理；客户端可以伪造自己的记录，影响只限于自己的历史，P 不防这一点、不设速率限制。

**一次访问**内累计达到门槛记一次，最多一次：换曲或同一首从头再播（单曲循环的每一轮、在队首按“上一首”、停止后再播、播完最后一首后再按播放）是新的访问；
暂停后继续、缓冲、暂时性故障后重试、媒体错误重新分类、卸载再挂载是同一次访问；刷新页面后累计从零开始，不写浏览器存储。
只累计引擎状态为 `playing` 时播放位置的前进量：拖动跨过的距离、后退、预检、准备、加载、缓冲、暂停都不算；静音与音量为 0 照常累计。

## 4. 接口

读路由只做 `Authenticate`；写路由先 `RequireSameOrigin`（403）再 `Authenticate`（401），与其他写接口一致。路径与请求体里没有用户 ID，
一律作用于当前会话用户，管理员也只能操作自己的记录；停用成员无法鉴权，记录保留，重新启用后本人仍可见。

- `POST /api/v1/me/plays`：请求头 `Idempotency-Key`（UUID，必填），请求体 `{ "track_id": UUID, "played_seconds": number }`；
  首次写入 `201 { "id", "track_id", "played_at" }`。同一用户同一键重放：曲目与秒数都相同时回放首次记录（同样 `201`），不同则 `409 idempotency_conflict`，都不写新行；
  并发的同键请求由唯一约束串行（`INSERT … ON CONFLICT (user_id, idempotency_key) DO NOTHING RETURNING`，没有返回行就读出已有行比较）。
  检查顺序：同源 → 鉴权 → 幂等键 → JSON → 字段 → 曲目 → 门槛 → 写入。
- `GET /api/v1/me/plays/recent-releases`：`{ "items": [{ "group_id", "release": 发行摘要, "last_played_at", "last_track": { "id", "title" } }], "has_history": boolean }`。
- `DELETE /api/v1/me/plays`：`200 { "deleted": integer }`；天然幂等，不需要幂等键，没有记录时 `deleted` 为 0。

| 条件 | 状态与错误码 |
| --- | --- |
| 未登录、会话失效或成员已停用 | `401 unauthorized` |
| 写请求的 Origin 不可信 | `403`（`RequireSameOrigin`） |
| 缺少 `Idempotency-Key` 或不是 UUID | `400 invalid_input` |
| 请求体不是合法 JSON，或字段类型不符（如 `played_seconds` 是字符串） | `400 invalid_json`（只写一个信封） |
| `track_id` 不是 UUID，`played_seconds` 缺失、非有限数、≤ 0 或 > 86400 | `400 invalid_input` |
| 曲目不存在或不是 `present` | `404 not_found` |
| 曲目时长已知且 `played_seconds < T − 1` | `400 play_too_short` |
| 同键不同请求 | `409 idempotency_conflict` |
| 数据库失败 | `503 database_unavailable` |

## 5. 最近播放的读取规则

- 按曲目的最后播放时间倒序（同一时间按曲目 ID，分批读取时保持稳定），每个发行只出现一次，代表它最近一次被播放的曲目，最多 8 个。
- 记录引用曲目，不引用发行：重扫、改名、同 root 移动后曲目 ID 不变，记录仍指向同一曲目；曲目随重扫换到另一个发行时按它**当前**所在的发行显示。
- 曲目已缺失（不再 `present`）的记录不显示，但仍保存，文件回来后照常出现。`has_history` 表示是否有任何记录（含当前不显示的），界面据此决定是否提供清除。
- 发行归属属于 catalog，listening 不在自己的 SQL 里联表分组：每批 200 首不同曲目、最多 10 批（最多读 2,000 首），经 `LoadTrackPlacements` 换成发行后去重，
  凑满 8 个或没有更多记录为止；个人规模下通常一批就够。
- 已接受的限制：上报失败后等待重试的记录，在用户随后清除历史时不会被取消，可能在清除之后才写入（窗口约 42 秒，且清除成功说明服务已恢复）；
  记录只属于本人，取消它需要让概览的清除触及播放器创建的上报器，收益不抵复杂度。

## 6. 前端的数据合同

- 计次：`features/player/engine/listen-tracker.ts`（纯逻辑，时钟注入）与 `PlayerEngine`。引擎经依赖 `playRecorder: { record, discard }` 交出达到规则的播放，
  不发请求、不等结果；`record` 抛出的错误不影响播放；`endSession` 先调用 `discard`。`load(entryId, seekTo, visit)` 显式区分新访问与继续。
  没有 `seeking` 事件的位置跳变（引擎自己设置 `currentTime`）按“前进量不超过同一段时间的真实流逝加 1 秒”折算，不累计。
- 上报：`features/listening` 的 `createPlayReporter`：每次上报生成新的幂等键；结果不确定的失败（网络失败、无效响应、5xx，即 `isOutcomeUncertain`）
  按 2、10、30 秒用**同一个键**重试，4xx、取消与不是 `ApiError` 的错误直接放弃；同时等待的上报最多 50 个；`dispose` 中止在途请求、清除定时器、之后的上报被忽略。
  `PlayerProvider` 与引擎一起创建上报器，会话结束（退出、失效、换用户）时作废，上一位用户未发出的记录不会在新会话下发出。页面关闭时未成功的记录放弃，不写存储。
  上报失败不改变播放状态、不提示用户。成功后让 `listeningKeys.recentReleases(generation)` 失效。
- 概览：`RecentPlaysPanel`（`app` 层的概览页组合，listening 不依赖 player）。最多 8 张卡片，整张卡片是链接，指向 `/albums/$groupId?release=<播放过的版本>`，
  写专辑名、专辑艺人（缺失时退回艺人）与“上次播放 YYYY-MM-DD HH:mm”；不展示进度、剩余时间或“全部历史”链接，不做禁用占位。
  空态说明计次规则；有记录但曲目都已缺失时说明“没有可显示的播放记录”并仍可清除。清除先确认（只影响自己、不能恢复），成功后立刻置空并重新读取，
  失败在对话框里显示错误、按钮变成“重试清除”（幂等，安全），成功后焦点回到区块标题并用状态提示说明结果。
- 样本与解码：三个接口的响应样本在 `backend/internal/app/testdata/api-samples/listening/` 与 `errors/play-too-short.json`，前端解码器逐一往返核对，上限（8 项）与后端一致。
  Playwright 运行在 Node 里，不能导入带样式的应用代码，所以真实服务用例自己定义精简的解码器。

## 7. 必需测试与断言

- 门槛：表驱动覆盖 `Threshold` 与容差边界、`ValidPlayedSeconds`（NaN、无穷、0、负数、超限）。
- 记录：201 与重放（同一响应）、同键不同曲目或秒数 409、并发同键只写一行、曲目缺失与不存在 404、低于门槛 400、时长未知只校验范围、缺键与非 UUID 400、
  类型不符 `invalid_json`、跨源 403（含没有会话时）、未登录 401。
- 隔离：用户 A、用户 B、管理员互相读不到、清不掉；同一个幂等键在不同用户下各写各的。
- 最近播放：倒序、同一发行去重、最多 8 个、多批读取、缺失曲目不显示但保留且文件回来后重现、曲目换发行后跟随、空与 `has_history`。
- 重扫：用合成 FLAC 经扫描接口验证改名与同 root 移动后记录仍指向同一曲目、最近播放仍显示同一专辑。
- 前端：跟踪器与引擎（恰好一次、拖动不算、暂停与重试继续、换曲与循环重新计、会话结束作废）、上报器（重试节奏与同一个键、4xx 放弃、`dispose` 之后不再发送、上限）、
  默认装配的 provider、概览区块（状态、卡片链接、清除流程与失败）、五种宽度不溢出、axe、纯键盘清除。
- 真实服务（隔离 PostgreSQL、Chromium、合成 FLAC）：播放过半后接口有记录、概览显示该专辑、清除后为空；只拖动到接近末尾不产生记录。

## 8. 错误与正确做法

| 错误做法 | 正确做法 |
| --- | --- |
| 路径或请求体里放用户 ID，或让管理员读取他人的历史 | 只取会话用户，SQL 一律带 `user_id` |
| 用客户端时间作为 `played_at` | 用服务端收到的时间 |
| 重试时换新的幂等键 | 同一次播放的所有重试共用一个键，后端按键只写一行 |
| 在 listening 的 SQL 里联表 catalog 的私有表去分组 | 经 `catalog.LoadTrackPlacements` 换算，再在 listening 里去重 |
| 把缺失曲目的记录删掉或在界面上显示成坏卡片 | 保留记录、不显示；文件回来后照常出现 |
| 引擎里直接发请求或处理上报失败 | 引擎只经 `playRecorder` 交出播放，上报器处理重试与失败 |
| 把进度条、剩余时间或“全部历史”做进概览 | 只显示接口给的事实；历史页面属于 M5 |
