# P 服务端播放历史：技术设计

需求与验收见 [prd.md](./prd.md)。本文的“计一次播放”规则、隐私与保留策略对应 PRD 的 P2–P5，已于 2026-10-11 获用户批准。

## 1. 边界

| 部分 | 归属 | 做什么 | 不做什么 |
| --- | --- | --- | --- |
| 后端 `internal/listening` | 新能力核心，拥有 `play_history` 表 | 记录、读取最近播放的发行、清除；按 catalog 的时长校验计次规则 | 不含 HTTP；不读 catalog 的私有表 |
| 后端 `internal/listening/httpapi` | 新传输层 | 三个路由的鉴权、同源校验、解码、错误映射 | 不含 SQL |
| 后端 `internal/catalog` | 已有，增加两个公开读取函数 | 曲目时长、曲目所在发行（含摘要与组 ID） | 不知道播放历史 |
| 前端 `features/player` | 已有 | 引擎按媒体事件累计实际出声时长，达到规则时经新的依赖端口交出一次播放 | 不发请求，不知道接口 |
| 前端 `features/listening` | 新 feature | 上报器（幂等键、有限重试、会话结束即作废）、最近播放查询、清除、概览区块 | 不判断什么算一次播放 |
| 前端 `app` | 已有 | 概览页组合“最近播放”区块 | — |

依赖方向：`listening` → `catalog`（公开合同）；`listening/httpapi` → `identity`、`listening`、`platform/httpserver`、`platform/uuid`；
`app` 装配。前端 `player` → `listening`（只用其公开的上报器），`listening` → `catalog`（公开的封面组件、摘要解码器与格式化函数），没有环。

## 2. 数据模型（迁移 0016）

`backend/migrations/0016_play_history.sql`：

```sql
CREATE TABLE IF NOT EXISTS play_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    track_id UUID NOT NULL REFERENCES tracks(id),
    idempotency_key UUID NOT NULL,
    played_seconds DOUBLE PRECISION NOT NULL,
    played_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT play_history_played_seconds_ck CHECK (played_seconds > 0 AND played_seconds <= 86400),
    CONSTRAINT play_history_user_key_uq UNIQUE (user_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS play_history_user_track_idx ON play_history (user_id, track_id, played_at DESC);
```

- `played_at` 用服务端收到时的时间，不信任客户端时钟；重试晚到几秒到几十秒，对“最近播放”的顺序没有实际影响。
- `played_seconds` 是达到规则那一刻累计的实际出声秒数（约等于门槛），是计次的依据，不是这次一共听了多久。
- `track_id` 不级联删除：曲目从不物理删除（只标 `missing`），将来若有删除曲目的操作，必须先显式处理历史，不能静默丢记录。
  `user_id` 级联：用户目前不能删除，若将来可以删除，个人数据随之删除。
- 不存 IP、User-Agent、设备或会话 ID（P3）。不设自动过期（P4）。
- 索引只按实际查询建一个（“最近播放”按用户分组取每首曲目的最后播放时间）；M5 的按时间倒序列表再按需要加索引。

## 3. 计一次播放的规则（P2）

**定义**：一次“访问”内，实际出声累计时长达到门槛就记一次，每次访问最多一次。

- 门槛 `T = min(D / 2, 240 秒)`。`D` 优先取扫描得到的曲目时长（`tracks.duration_seconds`，前端是 `PlayerTrack.durationSeconds`），
  没有时取媒体报告的时长，再没有就 `T = 240 秒`。前端与后端用同一个 `D`，所以两边算出的门槛一致。
- 只累计媒体真正向前走的时间：引擎状态为 `playing` 时，相邻两次 `timeupdate` 之间 `currentTime` 的增量；
  拖动（`seeking`）跨过的距离不算，后退不算；增量不超过同一段时间的真实流逝加 1 秒（防止没有 `seeking` 事件的跳变被算进去）。
  后台标签页的 `timeupdate` 变稀时增量变大，但仍受真实流逝限制，照常累计。
- 预检、正在准备、加载、缓冲、暂停、失败期间都不累计。静音或音量为 0 仍算出声（媒体在播放，网页无从得知用户是否在听）。
- **新的访问**：换到另一首（下一首、上一首、点播队列里的另一项、移除当前项后的后继、失败后前进），
  或者同一首从头再播（单曲循环自然重播、“上一首”回到曲首）。
- **同一次访问继续**：暂停后继续、暂时性故障后重试、媒体错误重新分类后从停下的位置继续、开发模式的卸载再挂载。
  刷新页面之后是新的页面，累计从零开始（不写浏览器存储）。
- 服务端校验：曲目时长已知时要求 `played_seconds ≥ T − 1`（1 秒容差吸收浮点与事件粒度），否则 `400 play_too_short`；
  时长未知时只校验 `0 < played_seconds ≤ 86400`。服务端无法证明用户真的听了，它保证的是规则一致与数值合理；
  用户只能影响自己的历史，伪造自己的记录不构成越权（第 7 节）。

## 4. 后端接口

所有路由先 `Guard.Authenticate`（401 `unauthorized`），写路由再 `RequireSameOrigin`（403）；路径里没有用户 ID，一律作用于当前会话用户，
管理员也只能操作自己的记录。

### `POST /api/v1/me/plays`：记录一次播放

- 请求头 `Idempotency-Key`（UUID，必填）；请求体 `{ "track_id": UUID, "played_seconds": number }`。
- 首次写入 `201`，响应 `{ "id": UUID, "track_id": UUID, "played_at": RFC 3339 }`。
- 同一用户同一键重放：请求体相同时回放首次记录（同样 `201` 与同一响应），不同时 `409 idempotency_conflict`，都不写新行。
  并发的同键请求由唯一约束串行：`INSERT … ON CONFLICT (user_id, idempotency_key) DO NOTHING RETURNING …`，
  没有返回行就读出已有行比较。
- 校验顺序：鉴权 → 同源 → 幂等键 → JSON → 字段 → 曲目 → 门槛 → 写入。

### `GET /api/v1/me/plays/recent-releases`：最近播放的发行

- 响应 `{ "items": [{ "group_id": UUID, "release": Release 摘要, "last_played_at": RFC 3339, "last_track": { "id": UUID, "title": string } }], "has_history": boolean }`。
- `release` 与 `/releases` 的摘要同形（含 `artwork`）；最多 8 项，按 `last_played_at` 倒序，同一时间按发行 ID。
- 每个发行只出现一次，代表它最近一次被播放的曲目；曲目已缺失（不再 `present`）的记录不显示，但仍保存，文件回来后照常出现。
- 曲目随重扫换到另一个发行时，按它**当前**所在的发行显示（记录引用曲目，不引用发行）。
- `has_history` 表示该用户是否有任何记录（含当前不显示的），前端据此决定是否提供“清除播放历史”。

### `DELETE /api/v1/me/plays`：清除自己的全部播放历史

- 响应 `200 { "deleted": integer }`。天然幂等，不需要幂等键；再次清除得到 `deleted: 0`。

### 错误矩阵

| 条件 | 状态与错误码 |
| --- | --- |
| 未登录、会话失效或成员已停用 | `401 unauthorized` |
| 写请求的 Origin 不可信 | `403`（沿用 `RequireSameOrigin`） |
| 缺少 `Idempotency-Key` 或不是 UUID | `400 invalid_input` |
| 请求体不是合法 JSON | `400 invalid_json`（只写一个信封） |
| `track_id` 不是 UUID，`played_seconds` 缺失、非有限数、≤ 0 或 > 86400 | `400 invalid_input` |
| 曲目不存在或不是 `present` | `404 not_found` |
| 曲目时长已知且 `played_seconds < T − 1` | `400 play_too_short` |
| 同键不同请求体 | `409 idempotency_conflict` |
| 数据库失败 | `503 database_unavailable` |

## 5. 后端结构

- `catalog` 新增两个公开读取函数（`catalog.Store`，只读、present-only）：
  - `LoadTrackDuration(ctx, trackID) (float64, error)`：曲目时长，未知为 0；不存在或已缺失返回 `ErrTrackNotFound`。
  - `LoadTrackPlacements(ctx, trackIDs []string) (map[string]TrackPlacement, error)`：每首 present 曲目的标题、组 ID 与所在发行的摘要。
    摘要复用 `releaseSummaryProjection` 与封面 `LEFT JOIN`，按发行去重后计算（不按曲目逐行重复计算摘要），封面校验沿用 `scanReleaseSummary`。
- `listening.Service`：
  - `Record(ctx, userID, key, trackID, playedSeconds)`：经 `LoadTrackDuration` 校验门槛，写入或回放；返回记录或分类错误
    （`ErrTrackNotFound`、`ErrPlayTooShort`、`ErrIdempotencyConflict`）。
  - `RecentReleases(ctx, userID)`：按“曲目最后播放时间”倒序分批（每批 200 首不同曲目，最多 10 批）取出曲目 ID，
    经 `LoadTrackPlacements` 换成发行，按顺序去重，凑满 8 个发行或没有更多记录为止；再查 `has_history`。
    分批是因为发行归属属于 catalog，listening 不能在自己的 SQL 里联表分组；最多读 2,000 首不同曲目，个人规模下通常一批就够。
  - `Clear(ctx, userID)`：删除该用户全部记录，返回行数。
- `listening/httpapi.Register(mux)` 注册三个路由；`app` 装配并在 `dependency_rules_test.go` 声明两个新包的依赖。
- 日志：沿用 `http.request.completed`（路由模板不含 ID），不额外记录播放事件。

## 6. 前端结构

### 引擎的计次（`features/player/engine`）

- 新的纯逻辑模块 `listen-tracker.ts`：维护当前访问（条目 ID、曲目 ID、门槛、累计秒数、是否已记录）、累计基线与真实时钟；
  提供开始访问、继续、停止累计、拖动、采样几个方法，采样跨过门槛的那一次返回“应记录”。单元测试覆盖第 3 节的每条规则。
- `PlayerEngine` 的依赖增加 `recordPlay: (play: { trackId: string; playedSeconds: number }) => void`。
  引擎在 `load` 判断是新访问还是继续，在 `playing`、`pause`、`waiting`、`seeking`、`timeupdate`、单曲循环重播、“上一首”回到曲首处调用跟踪器；
  跨过门槛时调用 `recordPlay`。`endSession` 清空当前访问。
- 新增监听 `seeking` 事件（现在只监听 `seeked`）。

### 上报器（`features/listening`）

- `createPlayReporter({ send, onRecorded, now })` 返回 `{ report, dispose }`：每次 `report` 生成新的幂等键（`newIdempotencyKey`），
  `POST /me/plays`；网络失败与 5xx 按 2、10、30 秒重试 3 次，同一个键；4xx（含 400、404、409）与取消直接放弃；
  401 交给请求层的会话边界，本次放弃。
- `dispose` 取消全部等待中的重试并中止在途请求。`PlayerProvider` 与引擎一起创建上报器，引擎的 `endSession` 调用 `dispose`：
  退出、会话失效、换用户之后，上一位用户未发出的记录不会在新会话下发出。
- 成功后让该查看者的“最近播放”查询失效（`viewerQueryKey(generation, "listening", "recent-releases")`），概览打开时随之刷新。
- 上报失败不改变播放状态、不提示用户（记录只是信息性的）；页面关闭时还没成功的记录放弃（P5）。

### 概览“最近播放”区块

- `RecentPlaysPanel` 放在概览最前、占满整行；用 `Panel`、`QueryContent`、`Skeleton`。
- 最多 8 张卡片：封面（复用 catalog 的 `Cover`）、专辑名、专辑艺人、“上次播放 YYYY-MM-DD HH:mm”（`formatDateTime`）；
  整张卡片是一个链接（与专辑列表卡片一致），指向 `/albums/$groupId`，带上播放过的版本 `release`。
- 空态：“还没有播放记录。一首曲目播放到一半（最长 4 分钟）就会记在这里。”
  有记录但当前都不显示（曲目都已缺失）时同样是这段空态，但仍提供清除。
- `has_history` 为真时，标题右侧有“清除播放历史”按钮，打开 `ConfirmDialog`（“只影响你自己的记录，清除后不能恢复”）；
  确认后 `DELETE /me/plays`，成功后查询数据置空并失效，失败在对话框里显示错误文案，不自动重试。
- 不照搬原型“继续聆听”的进度条、“第 5 首 · 剩 24 分”和“全部历史”链接（P1；历史页面在 M5，不做禁用占位）。
- 卡片网格响应式重排，窄屏不溢出；收尾时用合成数据截图请用户确认布局（与 M1b 的 K9、歌词的 Y5 相同做法）。

### 公开合同的调整

- `catalog/index.ts` 增加导出：`Cover`、`albumArtistOf`、发行摘要解码器 `releaseSummaryDecoder` 与 `ReleaseSummaryDTO`。
- `shared/api/request.ts` 的方法增加 `DELETE`。
- 测试替身：Vitest 的 `fake-api`、Playwright 的 `api-mock` 增加三个路由的受控响应（取自后端样本），否则现有概览测试会得到 404 信封。

## 7. 安全与隐私

- 隔离靠“只取会话用户”：三个路由都没有用户 ID 参数；SQL 一律带 `user_id = 当前用户`。集成测试覆盖用户 A/B 与管理员互相读不到、清不掉。
- 写路由都经同源校验；`Idempotency-Key` 的作用域是用户，另一个用户用同一个键得到的是自己的新记录，看不到别人的。
- 客户端可以伪造自己的播放（任何浏览器都能直接发请求），影响只限于自己的历史，P 不防这一点；不设速率限制，与其他接口一致。
- 不在日志、错误信封或响应里出现路径或其他用户的信息；停用成员的记录保留，重新启用后本人仍可见。

## 8. 测试策略

- Go 单元：门槛计算（时长未知、短曲、长曲、容差边界）；请求解码与校验。
- PostgreSQL 集成（`internal/app/listening_api_integration_test.go`）：201 与重放、同键不同体 409、并发同键只写一行、门槛 400、
  不存在与缺失曲目 404、非 UUID、缺键、401、跨源 403；用户隔离（含管理员）；最近播放的倒序、去重、上限 8、缺失曲目隐藏、
  `has_history`；改名与同 root 移动后重扫，记录仍指向同一曲目并显示在同一发行；清除只影响本人。
  catalog 两个新函数的集成测试（present-only、批量、空输入、封面校验）。
- API 样本：新增记录、最近播放（有数据与空）、清除、`play_too_short` 错误；前端契约测试逐个解码。
- Vitest：`listen-tracker` 的规则；引擎集成（达到门槛只调用一次、拖动不算、暂停后继续累计、重试继续、换曲与单曲循环重新计、
  会话结束清空）；上报器（重试节奏、4xx 放弃、`dispose` 之后不再发送、成功后失效查询）；最近播放区块（加载、空态、错误、卡片链接、
  清除确认与失败）；解码器的样本与反例。
- Playwright（受控后端）：概览区块在五种宽度不溢出、axe 无违规、键盘可达清除与确认。
- 真实服务（`scripts/playback-e2e.sh`）：合成 FLAC 真实播放过半后接口出现记录、概览显示该专辑、清除后为空；只拖动不产生记录。

## 9. 文档与规范

- 新增 `.trellis/spec/backend/listening-contracts.md`（接口、规则、隔离、测试）；运行合同的路由表与专题文件表各加一行。
- 前端：播放器规范加“计一次播放”的规则；状态规范加上报器的会话边界与查询键；目录规范加 `listening` 的归属。
  `state-management.md` 已有 22.2 KB，只加几行，保持在 24,000 字节以内。
- 路线图 P 行、父任务 P 行、DESIGN §7 概览行、`product-goals.md` 的状态句。

## 10. 风险与回滚

- 回滚：迁移只新增一张表，不改已有数据；回退代码时这张表可以留着不用（开发阶段不写 down migration）。
- 风险：浏览器的 `timeupdate` 频率和后台节流因浏览器而异，自动化只有 Linux Chromium；规则的累计受真实流逝限制，频率变化只影响精度，不会多算。
  三组手工实播的“歌词面板”同一张表里再加一行“最近播放”，随 F1 一起验证。
- 风险：概览新增一个请求；它与现有区块并行，失败只影响本区块（`QueryContent` 的错误态）。
