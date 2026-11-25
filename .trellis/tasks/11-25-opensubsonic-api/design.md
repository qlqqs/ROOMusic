# OpenSubsonic 兼容接口：总体设计

各子任务共用的约定都写在这里；子任务的 design.md 只写各自的部分。

## 边界

- **新包 `backend/internal/subsonic`**，下设：
  - `protocol`：响应外壳、错误码、参数解析、XML/JSON 编码；
  - `auth`：凭据校验；
  - `httpapi`：各接口的处理器。
- 只调用各领域已有的服务：`catalog`、`personal`、`listening`、`playback`、`identity`，不直接写其他领域的表。确实缺读取函数时，在所属领域补，例如 `catalog` 补随机曲目和专辑列表的各种排序。
- 前端只改设置页：账号页的“第三方客户端”一行，管理设置的实例开关。

## 协议约定

- **协议版本**：响应 `version="1.16.1"`、`type="roomusic"`、`serverVersion=<构建版本>`、`openSubsonic=true`。
- **路由**：
  - `/rest/{endpoint}` 与 `/rest/{endpoint}.view`，GET 和 POST（表单体）都接受；
  - mux 只注册一个 `/rest/` 前缀处理器，内部按接口名分发；
  - `routes_test.go` 里记一条 `/rest/` 模板，日志的 `route_template` 保持 `/rest/`，接口名另记在 `subsonic_endpoint` 字段；
  - 未知接口返回错误码 0、HTTP 404，在外壳里说明。
- **格式**：`f=xml`（默认）或 `f=json`；第一版不支持 `jsonp`（返回错误 0）。
  - JSON 形状为 `{"subsonic-response":{...}}`；
  - XML 用 `http://subsonic.org/restapi` 命名空间；
  - 两种格式由同一份 Go 结构体编码，字段用 xml/json 双标签。
- **错误**：一律 HTTP 200 加外壳 `status="failed"`，`error{code,message}` 使用协议码：

  | 码 | 含义 |
  |---|---|
  | 0 | 一般错误 |
  | 10 | 缺少必填参数 |
  | 20/30 | 客户端/服务端版本不兼容（不主动返回） |
  | 40 | 用户名或密码错误 |
  | 41 | 不支持令牌认证（本实现不返回） |
  | 50 | 没有权限 |
  | 70 | 找不到 |

  例外：`stream`、`download`、`getCoverArt` 成功时直接返回二进制，失败时才返回外壳。
- **ID**：
  - 曲目、专辑（专辑组）、艺人、歌单直接用各自的 UUID 文本，它们在全库唯一；
  - 封面 ID 带前缀：专辑 `al-<专辑组 UUID>`，艺人 `ar-<艺人 UUID>`，歌单 `pl-<歌单 UUID>`；
  - 不认识的 ID 返回 70。
- **音乐文件夹**：一个虚拟文件夹 `id=1`，名称“ROOMusic”。`musicFolderId` 参数接受后忽略。
- **实体映射**：
  - 专辑：专辑组。曲目取默认版本，`discNumber` 取碟序号，多碟时给 OpenSubsonic 的 `discTitles`；
  - 歌曲：曲目的有效值（`track_effective`）。CUE 分轨照常作为歌曲出现，`suffix`/`contentType` 报父文件的格式，`isVideo=false`；
  - OpenSubsonic 扩展字段：`displayArtist`、`artists[]`、`albumArtists[]`、`musicBrainzId`、`sortName`、`genres[]`、`replayGain`（有数据时）、`bitDepth`、`samplingRate`、`channelCount`。
  - 个人字段：歌曲、专辑、艺人带 `starred`（personal 子任务）和 `userRating`（rating 子任务），由转换函数一次批量查回本人状态；不输出 `averageRating`。
- **参数**：
  - 整数参数非法返回 10；
  - 分页类参数（`size`、`offset`、`count`）按协议默认值，上限 500；
  - 时间参数是毫秒 Unix 时间戳。

## 鉴权

细节见 `11-25-subsonic-foundation/design.md`。

- 每个请求必带 `u`、`v`、`c`，加 `p` 或 `t`+`s`；缺少任何一个返回 10。
- 用户被停用、实例关闭 Subsonic 时返回 40 或 50，不泄露是哪种原因。
- 失败计数复用 `identity` 的按账号与按来源限流。
- 管理员与成员权限一致，第一版没有管理类接口。

## OpenSubsonic 扩展声明

`getOpenSubsonicExtensions` 只列已实现的扩展：

- `formPost`（v1）
- `songLyrics`（v1）
- `transcodeOffset`（v1）

`getUser` 的 `jukeboxRole=true`、`streamRole=true`、`downloadRole=true`，其余角色为 false。

## 日志与隐私

- 请求日志只记路由模板，不记查询串（`platform/httpserver/middleware.go`），保持现状。
- `subsonic` 包内的日志不得写入 `u`、`p`、`t`、`s` 的值。
- 诊断导出的脱敏规则（`platform/diagnostics/redact.go`）覆盖新增字段。

## 测试方法

- Go 集成测试用合成曲库调用 `/rest/*`，对照协议示例检查 XML 和 JSON 的结构，以及字段名和类型。
- 真实客户端（Symfonium、Feishin）由用户在测试实例上手动检查，结果记进父任务。
