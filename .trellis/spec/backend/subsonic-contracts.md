# Subsonic 兼容接口合同

`backend/internal/subsonic` 让 Symfonium、Feishin 等 Subsonic/OpenSubsonic 客户端用 ROOMusic 的账号连接。本文记录骨架（2026-11-25，迁移 0081）的协议外壳、鉴权、登录密码副本、实例开关与网页端设置接口，以及浏览、搜索与封面（§4.1–4.3）。播放、收藏等接口由后续子任务追加到本文。产品裁决见任务 `.trellis/tasks/11-25-opensubsonic-api/`。

## 1. 范围与触发

改动以下任一项时先读本文：`/rest/` 路由与分发、响应外壳与错误码、客户端鉴权、密码副本的写入与删除时机、`identity.PasswordObserver`、实例开关、`/api/v1/me/subsonic*` 与 `/api/v1/admin/subsonic`、`subsonic_endpoint` 日志字段与诊断脱敏。

## 2. 包与签名

| 包 | 职责 | 允许依赖 |
| --- | --- | --- |
| `subsonic/protocol` | 请求解析（`.view` 后缀、GET 与 POST 表单、`f`）、外壳、错误码、XML/JSON 双标签编码 | 标准库，可用 `net/http` |
| `subsonic/auth` | 实例开关、客户端凭据校验、成功校验缓存、密码副本写入 | `identity`、`platform/secret`、`subsonic/protocol` |
| `subsonic` | 目录与个人状态到协议实体的转换：`Song`、`Album`、`Artist(…, PersonalState)`，browse、personal、stream 共用 | `catalog`、`subsonic/protocol` |
| `subsonic/httpapi` | `/rest/` 分发与各接口处理器、网页端设置接口 | `catalog`、`identity`、`listening`、`personal`、`platform/uuid`、`platform/httpserver`、`subsonic`、`subsonic/auth`、`subsonic/protocol` |
| `platform/secret` | 实例密钥与 AES-GCM 加解密，从 `steward/secret` 原样移来，密钥文件仍是 `<数据目录>/steward/instance.key` | 无 |

```go
// identity：在已锁定账号的事务里接收已核对或即将生效的密码。
type PasswordObserver interface {
    ObservePassword(ctx context.Context, tx *sql.Tx, userID, password string) error
}
func CheckLogin(ctx, db, throttle, username, source, dummyInput string, verify func(User, string) (bool, error)) (User, string, *Attempt, error)
func ConfirmPassword(ctx, db, throttle, userID string, tokenHash []byte, password string, onVerified func(context.Context, *sql.Tx, string, string) error) error

// subsonic/auth
func New(db *sql.DB, secrets *secret.Store, throttle *identity.LoginThrottle, logger *slog.Logger) *Service
func (s *Service) Authenticate(ctx, request protocol.Request, source string) (identity.User, error)
func (s *Service) ObservePassword(ctx, tx, userID, password string) error // 实现 PasswordObserver
func (s *Service) Confirm(ctx, user identity.User, tokenHash []byte, password string) error
func (s *Service) SetEnabled(ctx, enabled bool) error
func (s *Service) Forget(userID string) // 会话撤销后清这个用户的校验缓存
```

`app` 用 `identityapi.New(...).WithPasswordObserver(subsonic)` 注入，`identity` 不导入 `subsonic`。

```go
// subsonic/httpapi：浏览依赖由 app 注入
type Browse struct {
    Catalog       *catalog.Store
    Personal      *personal.Service
    Listening     *listening.Service
    ReadArtwork   func(ctx context.Context, resourceID string, lookupOnly bool) (mime string, data []byte, err error)
    DataDirectory string
}
func Register(mux *http.ServeMux, service *auth.Service, guard *identity.Guard, version string, browse Browse)

// subsonic：个人状态随实体输出；没有值时对应字段省略
type PersonalState struct {
    StarredAt *time.Time // personal 子任务填写
    Rating    *int       // userRating
}
```

## 3. 数据（迁移 0081）

```sql
subsonic_settings(id smallint PRIMARY KEY CHECK (id = 1), subsonic_enabled boolean NOT NULL DEFAULT true) -- 单行
subsonic_credentials(user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, sealed bytea NOT NULL, updated_at timestamptz NOT NULL DEFAULT now())
TRIGGER invalidate_subsonic_password AFTER UPDATE OF password_hash ON users -- 哈希变化即删该用户副本
```

- 新装与升级的实例都默认开启。
- `sealed` 用实例密钥加密，用途 `"subsonic-password"`，附加数据绑定 userID。解密失败（密钥换了、数据被篡改）视同没有副本。
- 触发器覆盖只连数据库的命令行 `reset-password`：副本被删，用户之后要重新登录网页或在账号页确认一次密码。

### 副本写入时机

| 入口 | 行为 |
| --- | --- |
| 网页登录成功、本人改密（成员与管理员）、管理员建用户、管理员重置密码 | 同一事务里先改密码哈希（触发器删旧副本），再经 `ObservePassword` 写新副本 |
| 实例关闭 | 不写 |
| 加密失败（实例密钥读不出或写不进） | 跳过写入，记 Warn `event=subsonic.credential.seal_failed`、`module=subsonic`、`user_id`、`error_kind=secret_unavailable`；入口本身照常成功 |
| 数据库错误 | 原样返回，整个事务回滚 |
| `PUT /api/v1/admin/subsonic {enabled:false}` | 同一事务里删除全部副本 |
| 命令行重置、管家工具改密 | 没有观察者，只由触发器删旧副本 |

Subsonic 的 `p=` 登录成功**不**补写副本：只有网页入口知道该不该保存。

## 4. 协议外壳与接口

- 路由：mux 只注册 `/rest/`，按去掉 `.view` 后缀的接口名分发；GET 与 POST 表单（体积上限 64 KiB）都接受。
- 外壳：`status`、`version="1.16.1"`、`type="roomusic"`、`serverVersion=<构建版本>`、`openSubsonic=true`。
  - XML 命名空间 `http://subsonic.org/restapi`；
  - JSON 顶层 `{"subsonic-response":{...}}`；
  - `f` 为空或 `xml`、`json`，其他值（含 `jsonp`）返回错误 0。
- 已实现接口：

| 接口 | 响应 |
| --- | --- |
| `ping` | 空外壳 |
| `getLicense` | `license{valid=true}` |
| `getOpenSubsonicExtensions` | 只列已实现的扩展，骨架阶段只有 `formPost` v1；后续子任务实现 `songLyrics`、`transcodeOffset` 时再追加 |
| `getMusicFolders` | 一个虚拟文件夹 `id=1`、`name="ROOMusic"` |
| `getUser` | 只能查本人；`streamRole`、`downloadRole`、`jukeboxRole` 为 true，`folder=[1]` |
| `setRating` | `id` 是曲目、专辑组或艺人（`personal.SetRatingByID` 判断类型）；`rating=0` 清除，1–5 设置。缺参数或非整数 → 10，整数越界 → 0，ID 不存在 → 70，失败都不写入。与网页端评分是同一份数据，见 `personal-contracts.md` §9 |

### 4.1 浏览与搜索

ID 直接用 ROOMusic 的 UUID：歌曲是曲目 ID，专辑是专辑组 ID，艺人是艺人 ID。歌曲的 `parent` 与 `albumId` 都是专辑组 ID。`coverArt` 是 `al-<组 ID>` 或 `ar-<艺人 ID>`，歌单是 `pl-<歌单 ID>`。不可用（全部曲目缺失）的专辑不出现在任何列表里。

| 接口 | 读取 | 要点 |
| --- | --- | --- |
| `getArtists` | `catalog.BrowseArtists` | 只列专辑艺人，按网页端艺人列表的首字母分组（`InitialGroup`）；艺人投影重建中返回空索引 |
| `getArtist` | `BrowseArtistAlbums` | 该艺人作为专辑艺人的可用专辑 |
| `getAlbum` | `BrowseAlbumTracks` | 默认版本的全部碟，按（碟、序号）排序；只有碟有标题时给 `discTitles` |
| `getSong` | `LoadBrowseTracks` | 缺失曲目 → 70 |
| `getAlbumList2` | 见下表 | `size` 默认 10、上限 500；`offset` 只计可用专辑 |
| `search3` | `BrowseSearch` | `query` 必填，`""` 与空串都表示全部；三栏各自 `*Count`（默认 20、上限 500）和 `*Offset`。沿用网页端搜索的规范化、别名与 legacy 匹配。艺人投影未就绪时艺人栏为空，不报错 |
| `getRandomSongs` | `RandomTracks` | `size`、`genre`、`fromYear`、`toYear`；只取在场曲目 |
| `getGenres` | `ListGenres` | 专辑数与歌曲数按有效流派统计 |
| `getArtistInfo2`、`getAlbumInfo2` | 艺人简介与 MusicBrainz ID、专辑 MusicBrainz ID | 没有资料时返回空对象，不报错 |

| `getAlbumList2 type` | 顺序 |
| --- | --- |
| `alphabeticalByName` / `alphabeticalByArtist` | 标题排序键 / 有效专辑艺人排序键 |
| `newest` | 加入时间新到旧 |
| `random` | 每次请求取新种子，同一请求内稳定 |
| `byYear` | `fromYear`、`toYear` 必填，`fromYear>toYear` 时倒序 |
| `byGenre` | `genre` 必填，按代表版本的流派过滤 |
| `starred` | 收藏时间新到旧（`personal`） |
| `recent` / `frequent` | 最近播放 / 播放次数（`listening.AlbumIDs`） |
| `highest` | `personal.HighestRatedAlbums`：评分降序，同分按评分时间新到旧，再按组 ID；按批读取，不可用专辑不占位置 |
| 其他值或缺 `type` | 10 |

### 4.2 本人评分

同一响应里的歌曲、专辑、艺人共用一次 `personal.LookupRatings`，按 ID 取回结果（它会去重并统一小写，不能按位置对齐），超过上限时分批（曲目 `personal.TrackLimit`，专辑、艺人各 1,000）。艺人按解析后的锚点取最新评分，规则见 `personal-contracts.md` §9。`search3` 读评分时遇到艺人投影重建（`ErrArtistCatalogPreparing`），只给专辑和歌曲带评分，艺人栏返回空。

### 4.3 封面 `getCoverArt`

- `id` 按前缀找资源：`al-` 专辑封面；`ar-` 艺人图，没有时回退到该艺人一张专辑的封面；`pl-` 本人歌单第一张有封面的组。找不到 → 70，客户端显示自己的占位图。
- `size` 归到五档：0（原图，不缩放）、64、128、256、512、1024（大于 512 一律 1024）。
- 缩放用 `golang.org/x/image/draw` 的 CatmullRom。输出 JPEG（质量 85），原图是带透明的 PNG 时输出 PNG。能读 JPEG、PNG、GIF、WebP。
- 缓存在 `<数据目录>/cache/subsonic-art/<资源ID>-<档位>.jpg|png`，先写临时文件再改名；同一键并发只缩放一次（singleflight）。资源 ID 由内容寻址，内容变了键就变，缓存不需要失效。
- 响应头：`ETag` 是 `<资源ID>-<档位>`，命中 `If-None-Match` 返回 304；`Cache-Control: private, max-age=31536000, immutable`。

## 5. 鉴权

1. 实例关闭 → 40（先于参数检查，不泄露原因）。
2. 缺 `u`、`v`、`c` 任一，或既没有 `p` 也没有完整的 `t`+`s` → 10。
3. `identity.CheckLogin`：先查来源限流，再按用户名查未停用的账号，再查账号限流；与网页登录共用同一个 `LoginThrottle` 和计数。找不到、停用、限流都返回 40，与密码错误不可区分。
4. `p`：有 `enc:` 前缀时按 hex 解码（解码失败按密码错误处理），再按 bcrypt 校验。
5. `t`+`s`：解密副本，计算 `md5(密码+s)`，与 hex 解码后的 `t` 常量时间比较；没有副本或解密失败返回 40，计入失败次数。
6. 成功校验缓存（只用于 `p`）：按用户存 `sha256(当前密码哈希)` 与 `sha256(哈希 + "\x00" + 密码)`，5 分钟过期，每分钟清扫一次。改密后哈希变化，缓存自然失效；会话撤销时 `Forget`，关闭实例时清空。每个请求仍会查一次账号，停用立即生效。
7. 成功后 `httpserver.SetActor` 记 `actor_id`，用户放进请求上下文。不建会话、不写 cookie，`/rest` 不做同源检查。

## 6. 网页端接口

守卫与严格 JSON 解码同其他 `/me`、`/admin` 接口（未登录 401，成员访问 admin 403，写请求同源检查）。

| 方法与路径 | 输入 | 成功 | 失败 |
| --- | --- | --- | --- |
| GET `/api/v1/me/subsonic` | 无 | `{enabled, ready, username}`，`ready` 表示副本存在且能解密；关闭时 `ready=false` | 503 |
| POST `/api/v1/me/subsonic/confirm-password` | `{password}` | 204 | 403 `current_password_incorrect`；429 `too_many_attempts` 带 Retry-After；409 `subsonic_disabled`；400 `invalid_input`；加密失败 503 `database_unavailable` |
| GET `/api/v1/admin/subsonic` | 无 | `{enabled}` | 503 |
| PUT `/api/v1/admin/subsonic` | `{enabled}` | `{enabled}`；关闭时同事务删除全部副本 | 400 `invalid_input`、503 |

确认密码用 403 而不是 401：前端请求层把 401 当作会话结束。它与成员自助改密的当前密码核对共用限流与计数。样本在 `app/testdata/api-samples/subsonic/`，前端契约测试 `frontend/src/test/api-contract.test.ts` 逐份登记。

## 7. 错误矩阵（`/rest`）

| 条件 | HTTP | `error.code` |
| --- | --- | --- |
| 实例关闭（任何接口，含未知接口） | 200 | 40 |
| 缺少凭据参数 | 200 | 10 |
| 用户不存在、停用、密码错、令牌错、没有副本、限流 | 200 | 40 |
| `getUser` 缺 `username` / 查别人 | 200 | 10 / 50 |
| 浏览接口缺必填参数、数字参数非法或为负、未知 `type` | 200 | 10 |
| 浏览 ID 不是 UUID 或不存在、专辑不可用、没有封面 | 200 | 70 |
| 未知接口 | 404 | 0 |
| 不支持的方法、`f` 值，表单解析失败 | 200 | 0 |
| 数据库等内部故障 | 200 | 0（`Service unavailable`） |

## 8. 日志与隐私

- 请求完成日志的 `route_template` 固定为 `/rest/`；接口名记在 `subsonic_endpoint`，只取已注册的接口名，其他一律记 `unknown`，不回显客户端输入。
- 查询串不进日志（中间件原有行为）。`subsonic` 包内的日志不写 `u`、`p`、`t`、`s` 的值。
- 诊断导出：`subsonic_endpoint` 列为安全字段，`/rest/` 列为安全路由；凭据正则覆盖 `u=`、`p=`、`t=`、`s=`。

## 9. 正确与错误的做法

- **错误**：让副本写入失败中断网页登录。这会让副本这个附带功能拖垮核心入口，实例密钥一出问题，所有人都登录不了网页。
  **正确**：只有显式的确认密码入口在加密失败时报 503，其余入口记 Warn 后跳过（第 3 节）。
- **错误**：在 Subsonic 的 `p=` 登录成功后补写副本。
  **正确**：副本只由网页入口写，管理员关闭后 `p=` 也写不进去。
- **错误**：按位置把 `LookupRatings` 的结果对回本页实体。
  **正确**：它会去重并统一小写，按 ID 建映射再取（§4.2）。
- **错误**：`highest` 先按评分取一页再过滤不可用专辑，导致页面变短、偏移错位。
  **正确**：偏移只计可用专辑，不够一页时继续读下一批。
- **错误**：改密后靠清缓存来让旧密码失效。
  **正确**：缓存绑定当前密码哈希，哈希一变就不会命中；`Forget` 只是额外的清理。

## 10. 测试

| 用例 | 断言 |
| --- | --- |
| `TestSubsonicProtocolResponseEncoding` | XML/JSON 外壳、命名空间、错误外壳、各接口字段名与类型 |
| `TestSubsonicPasswordCacheExpiryAndBinding` | 5 分钟过期、绑定哈希与候选密码、`Forget`/清空后不命中 |
| `TestPostgreSQLSubsonicAuthenticationAndPayloads` | `p=`、`enc:`、`t+s` 对 `ping.view` 成功，POST 表单，五个接口载荷 |
| `TestPostgreSQLSubsonicProtocolErrors` | 第 7 节错误矩阵 |
| `TestPostgreSQLSubsonicSharesWebLoginThrottle` | Subsonic 失败触发的限流对网页登录生效，反之亦然 |
| `TestPostgreSQLSubsonicExistingMembers` | 开启前已有的用户：`p=` 可用且不补写副本，`t+s` 40；确认或重新登录后可用 |
| `TestPostgreSQLSubsonicDisableClearsCredentials` | 关闭后副本表为空、`/rest` 全 40；再开启仍需重新登录或确认 |
| `TestPostgreSQLSubsonicPasswordRotation` | 改密、重置、只改数据库哈希后旧密码三种方式都失败（含缓存期内），新密码 `t+s` 立即可用 |
| `TestPostgreSQLSubsonicDiagnosticsHideCredentials` | 请求日志与诊断导出不含 `p`、`t`、`s` 的值，有 `subsonic_endpoint` |
| `TestPostgreSQLSubsonicSealFailurePreservesIdentity` | 密钥损坏时登录与改密成功、Warn 不含密码与密文，确认密码 503 |
| `TestPostgreSQLSubsonicWebSettings` | 网页端四个接口的守卫与第 6 节状态码，样本录制 |
| `TestPostgreSQLSubsonicMigrationRoundTrip` | 0081 up → down → up |
| `TestSubsonicBrowseEntityMapping`、`TestSubsonicBrowseWireEncoding` | 实体映射、可选字段省略、XML/JSON 编码 |
| `TestSubsonicBrowseProtocol` | 合成曲库（多碟、CUE、多艺人、有无封面与流派）上 §4.1 各接口的 XML 与 JSON |
| `TestSubsonicBrowseAlbumLists`、`TestSubsonicBrowsePersonalAlbumLists` | 各 `type` 的顺序、分页、500 截断、参数错误；收藏、最近、最常播放的用户隔离与不可用过滤 |
| `TestSubsonicBrowseSearchPagination` | 三栏分页、艺人索引未就绪时艺人栏为空 |
| `TestSubsonicBrowseRandomTracks` | 流派、年份、在场过滤 |
| `TestSubsonicBrowseCoverArt` | 五档、透明 PNG、缓存命中、ETag/304、艺人图回退、歌单隔离、70 |
| `TestSubsonicBrowseScale` | 一万个可见专辑组下 `getAlbumList2` 与 `search3` 单次 p95 < 300ms；需 `ROOMUSIC_SUBSONIC_BROWSE_SCALE=1`，夹具 `testdata/subsonic-browse-scale.sql` |

```bash
cd backend
mise exec -- go test ./internal/subsonic/... ./internal/app/ -run 'Subsonic|Route|Dependency'
ROOMUSIC_TEST_RUN='Subsonic' ../scripts/test-integration.sh
```
