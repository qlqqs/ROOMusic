# 设计：网络搜索（模型联网认人与模型整理）

锚点见调研 `../11-24-artist-web-search/research/repo.md`（A–H）。依赖手动子任务已合入：`artist_link` 操作、人工层、`apply` 的依据泛化为 `Link{Source, ExternalID}`、入口放宽到 Wikidata。

## 1. 来源“网络搜索”

- `app.go` 的内置注册加 `SourceRegistration{Declaration{ID:"web_search", Name:"网络搜索", Capabilities:[artist_image, artist_bio], DefaultEnabled:true}}`，无 finder。
- `Unavailable`：开启者的管家设置没有可用的搜索（`search_source` 为 none；或 native 但探测不支持）→ 新 `inactive_reason = "search_unconfigured"`。对话模型未配置时模型层本身不可用，不另设原因。
- `SourceSettings` 加可选 `requirements: [{kind: "model"|"search", ready: bool}]`，只有 `web_search` 返回。
- 程序层来源摘要名单（`artist_media_pass.go:76-82`）加入 `web_search`。

## 2. 来源等级与替换（`curation/artist_media.go`）

- 等级：`fanarttv`、`theaudiodb`、`commons`、`wikipedia` = 3；`plugin:*` = 2；`web_search` = 1。
- `apply` 的 `occupied` 判定改为：家族内已有行且其等级 ≥ 新值等级 → `occupied`；等级更低 → 在同一事务删除旧行，新操作的 Before 记旧快照，旧图片文件在提交后按引用计数清理。撤销新操作只删除自己的行。
- 依据（Evidence）三种：`Link`（musicbrainz / wikidata，事务内复查家族仍有该对应）、`Name`（插件按名字，复查艺人仍是有效身份且仍无任何艺人对应）、`WebSearch`（复查待定项归属：项为 `artist_identity`、目标是该艺人、由本次 ModelRun 领取）。
- DB 与 Go 的来源枚举：图片与简介都加 `web_search`。
- 外部条目标识（用于抑制）：插件沿用插件给的 ID；网络搜索图片 = `sha256(图片地址)`；网络搜索简介 = 固定值 `artist`（标记不对后这位艺人不再写模型整理的简介）。

## 3. 读取

- `ArtistImage`、`ArtistBio` 增加 `Citations []Citation{URL, Title}`（非网络搜索为空数组）。存储：两张外部表各加 `citations jsonb NOT NULL DEFAULT '[]'`。
- `ArtistDetail` 不暴露待定项；管理员另有状态接口（第 7 节）。

## 4. 待定项与例外（迁移 0073）

- `curation_pending_items`：加 `artist_id UUID REFERENCES artists(id)`；`target_id` 改可空；CHECK `(target_type='release' AND target_id IS NOT NULL AND artist_id IS NULL) OR (target_type='artist' AND artist_id IS NOT NULL AND target_id IS NULL)`；kind 加 `artist_identity`；未结束项唯一索引覆盖艺人目标。保留发行的外键与级联。
- `curation_model_exceptions` 的 tool CHECK 加 `resolve_artist_identity`；决定加 `superseded`。
- `curation_suppressions` 新 subject `artist_link`（值为 `sha256(source\0external_id)`）。
- Go：`UpsertPendingItem` / `ResolvePendingItems` 支持艺人目标；`ClaimPendingItems`、`scheduler.go:137` 到期判断、`status.go:85` 计数改为“可见发行或有效艺人”；被并入或不再在范围内的艺人项由程序层收尾为 `resolved`（`artist_out_of_scope`）。
- 例外读取 `exceptionSelect` 改为左连接发行与艺人；`ModelException` 的 `Release` 改可空并新增 `Artist *{ID, Name}`；接口样本更新。

## 5. 程序层（`program/artist_media_pass.go`、`program/artist_media.go`）

- 分页入口在“有对应的艺人”之外加第二路：没有任何 musicbrainz/wikidata 艺人对应、有 `release_artist`/`album_artist`/`track_artist` 有效署名、图片或简介缺一项的有效艺人。两路共用 `walkArtistMedia` 的分页与工作状态，`artistEvidence` 带 `NoLink bool`、名字集合（含别名）。
- 无对应艺人的处理：先插件按名字（`ArtistLinks{Name}`，依据 `Name`）；仍有缺口且 `sources.Check("web_search")` 活跃、模型层设置已开启 → `UpsertPendingItem(artist_identity)`，证据：`names`、最多 8 个专辑（名 + 年份）、最多 12 个曲目名、`gaps{image,bio}`。输入摘要包含名字集合、缺口、插件与网络搜索状态、抑制标记；不变不重做。
- 艺人之后有了对应或资料补齐 → 解决待定项（`resolution='program_matched'` 或新的 `filled`）。

## 6. 模型工具（`curation/stewardtools`）

| 工具 | 作用域 | 输入 | 行为 |
| --- | --- | --- | --- |
| `read_pending_items` | read | 现有 | 艺人项输出 `{item_id, kind:"artist_identity", artist_id, evidence}` |
| `resolve_artist_identity` | shared + Preview + Revertible（独立审查） | `{item_id, source:"musicbrainz"\|"wikidata", external_id, basis:{note, citations:[url]}}` | Plan：读条目（复用手动子任务的预览读取）；Wikidata 的 P31 须为人（Q5）或音乐团体（Q215380、Q2088357、Q9212979 及其直接子类清单写成常量）；名字或别名经 `textnorm.Text` 与本地任一名字一致；未被 `artist_link` 抑制。不满足 → 工具错误 `identity_mismatch` / `not_an_artist` / `suppressed`。Apply：`artist_link`（模型来源、批次、会话），待定项 `resolved/model_linked` |
| `submit_artist_media` | personal（不审查） | `{item_id, image?:{url, page_url}, bio?:{text, lang}, citations:[{url,title}] 1..5}` | 引用须为公网 http(s)；`image.page_url` 须在引用中；图片经 `platform/egress` 的 PublicOnly 客户端下载（与 `fetch_page` 同一策略，含管理员放行名单），≤16 MiB、10 秒、`artwork.ValidateImage`，短边 ≥ 200px；简介纯文本（去 HTML）、20–4000 字符、lang zh/en。写入自动层来源 `web_search`（依据 `WebSearch`）；缺口补齐 → 待定项 `resolved/model_media` |
| `shelve_pending_item` | 现有 | 现有 | 什么都没找到时搁置，证据变化后由现有规则重开 |

- `claimed`/归属核对仿 `model_run.go:78-87`，艺人项核对 `artist_id`。
- 审查证据：`reviewTargetInput` 加 `resolve_artist_identity` 分支（艺人名字、署名样本、条目摘要、引用）；`SupportsReviewEvidence` 登记。
- `Scheduler.Escalate`：`resolve_artist_identity` 按 `item_id` 绑定例外；同一待定项允许多个未决例外。
- 例外采纳：写 `artist_link`（模型来源 + `ConfirmedBy` 管理员），同项其余未决例外置 `superseded`，待定项 `resolved/human_accepted`。不采纳：写 `artist_link` 抑制，例外 `declined`；该项全部有决定且无采纳 → 回到 `waiting_model`。

## 7. 管理员状态接口

`GET /api/v1/artists/{id}/web-search`（管理员）：

```jsonc
{ "state": "none" | "queued" | "running" | "shelved" | "waiting_human",
  "candidates": [ { "exception_id": "...", "source": "musicbrainz" | "wikidata", "external_id": "...",
                    "name": "...", "description": "..." | null, "note": "...", "citations": [ {"url": "...", "title": "..."} ] } ] }
```

## 8. 后台会话联网（`app/curation_model.go`）

- `backgroundToolAllowed`：`web_search`、`fetch_page` 仅在 `sources.Check("web_search")` 活跃时放行（列出与调用时都判断）。
- `StartBackground`：开启者 `search_source == native` 且探测支持、且网络搜索活跃时设 `NativeSearch`。
- `SystemSuffix` 追加艺人身份的处理说明（中文）：用专辑与曲目名确认是同一个人；优先找 Wikidata / MusicBrainz 条目并提交身份；开放数据库没有时才提交模型整理的资料；同名多人且无法区分时分别提交候选让人工确认；什么都没找到就搁置；不猜。

## 9. mock（`backend/internal/upstreammock`）

- Wikidata `wbgetentities` 支持 `labels|aliases|descriptions|claims` 并返回 P31、P434。
- 搜索数据加艺人条目；一个公网形态的合成图片地址（开发时经放行名单访问 mock）。
- 模型脚本三套：认出身份、模型整理、转人工（两个候选）。

## 10. 规范更新（实现代理负责）

`curation-contracts.md`（网络搜索一节：范围、等级替换、待定项与例外、工具、抑制、外发数据）、`steward-runtime-contracts.md` / `steward-web-contracts.md`（后台联网工具）、`plugin-contracts.md`（按名字查）、`artist-contracts.md`（`citations`）、`docs/plugins.md`（按名字调用时 `mbid` 为空）。

## 风险

- 后台挂联网工具后单步 token 增大：靠每日额度与每批 5 项控制。
- 图片下载是新的出站面：只走 PublicOnly 客户端，限大小与时间，不跟随到非公网地址。
- 待定项改为两种目标后，所有按发行假设的查询都要覆盖：调研 D 节列出的位置逐一修改并各有测试。

## 评审修订（规划子代理评审，2026-11-24，全部采纳）

以下条目优先于上文。

- R-B1 待定项去重：艺人目标另建部分唯一索引 `(artist_id, kind) WHERE status <> 'resolved' AND target_type = 'artist'`，并写单独的 upsert（现有冲突判断 `work_states.go:96` 只覆盖发行）；`PendingItem.TargetID` 改为可空类型或拆出 `ArtistID`；进度计数除 `status.go:85` 外还有 `progress.go:48`。
- R-B2 `superseded`：同步修改 `0064` 中例外决定的两条 CHECK（决定取值与“决定与时间同时存在”的约束）。
- R-B3 等级在程序层生效：`artistEvidence` 带现有图片与简介的来源等级；程序层只在“缺”或“现有等级低于将尝试的来源”时尝试（`program/artist_media.go:146,152`）。读取排序（`artists_media.go:28`）先按等级再按现有顺序。
- R-B4 替换的记录：被替换的旧快照不放进 Before（撤销会据此把撤销后状态写成“已恢复”，`artist_media_revert.go:72`），另存在操作的 `replaced` 字段；撤销只删除自己的行，Describe 写明“替换了 X，撤销不恢复”。
- R-B5 模型写对应不替换：模型与例外采纳写 `artist_link` 遇到已有对应返回 `occupied`（手动子任务 R-A6 已实现）。
- R-B6 开关按开启者判断：`SourceRegistration.Unavailable` 签名改为接收 `context.Context` 与开启者 ID（或等价的设置读取端口），现有调用点同步修改。
- R-B7 审查豁免：`submit_artist_media` 保持 shared 作用域，`RunParams` 新增后台免审工具名单（只含它），由 `sharedDirect`（`run.go:511`）放行；保留注册表的输入校验与提交不确定时的对账重试。不用 personal 作用域。
- R-B8 细节：
  - 身份候选的拒绝复用现有 `external_link` 抑制 subject，不新增 `artist_link` subject。
  - W2 范围按家族计算（有效身份及并入方），人工层算作已有资料。
  - 待定项与例外处理前先解析到有效身份。
  - 模型整理资料的写入：资料行 `source = web_search`；操作 Origin 的 provider 为 `web_search`、模型名另记，保证“按来源撤销”能以 `web_search` 选中。
