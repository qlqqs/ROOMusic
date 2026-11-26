# 整理艺人技术设计

本设计依据本任务 `prd.md`、父任务 G3、`docs/architecture/adr/0009-curation-chain-review.md` 第 7 节，以及子任务 7 的模型调用合同。艺人联网查身份是一个由管理员显式启动的后台模型队列。它复用现有艺人待定项、模型会话、网页工具、MusicBrainz 来源闸门和治理写入口，整理链只负责已经确认身份后的图片和简介补齐。

## 1. PRD 范围对照与改动边界

| PRD 范围 | 设计落点 |
| --- | --- |
| 不再自动跑 | `backend/internal/curation/program/artist_media_pass.go` 删除为 `artist_identity` 建项和把网页搜索作为艺人资料缺口的分支。`artist_media` 仍处理已有 MusicBrainz 对应艺人的图片、简介。`backend/internal/curation/model_queue.go` 只允许带本次手动队列代次的艺人项进入模型调度，历史自动项保留为只读候选。 |
| 手动入口 | `backend/internal/curation/artist_web_queue.go` 提供单艺人加入、全量整理、停止、状态读取。单艺人入口使用 `POST /api/v1/artists/{id}/web-search`，全量和停止使用管理员整理接口。排队顺序在 `curation/artist_web_queue.go` 的署名查询生成，先取专辑艺人，再取合辑曲目艺人。 |
| 查过且本地没变的跳过 | 艺人证据摘要、当前 `ArtistWebRuleVersion` 和网页来源配置组成 `input_digest`。同一艺人已有相同摘要且有结论或候选时不重复建项。输入变化才清理本项次数并重新排队。 |
| 随时可以停、额度用完停、之后接着查 | 新增 `curation_artist_web_queue` 单行控制表保存 `idle/running/stopped`、代次；进度按本代项目统计。停止只阻止尚未发出的艺人项，已在途请求按当前模型合同完成后收尾。日额度由 `CheckDailyBudget` 在每个新项和每次重试前检查，达到额度时保留等待项和当前项次数，进入下一日或调高额度后继续。 |
| 每位最多 10 次工具调用、5 万 token | `artist_identity` 项沿用 `item_tool_calls`、`item_tokens` 记录实际消耗。艺人联网执行器将上限固定为 `10` 和 `50000`，网页搜索、读取网页和同一逻辑请求的重试都计入。达到上限时结束该项并保留已有候选，后端状态为 `shelved`，不再自动发送。 |
| 程序核对 | `backend/internal/curation/program/artist_identity.go` 复用子任务 5 合入后的 MusicBrainz 候选读取与专辑结构核对。候选 MBID 查出的发行按曲数、碟曲数和可比时长与本地该艺人的发行逐张比对。出现结构吻合时写艺人对应，发行按第 7 节区分唯一版、同组和跨组。没有结构吻合时写入待人工确认候选，不建立对应。 |
| 不审查 | `resolve_artist_identity` 的手动艺人路线去掉独立审查前置条件。结构核对通过的候选直接进入现有 `ApplyArtistIdentity` 和发行写入。结构未通过的候选只进入现有 `curation_model_exceptions` 的 `open` 行，由艺人页确认或否掉。候选引用仍经网页合同校验。 |
| 额度共用 | `model_usage.go` 的 `TodayUsage`、`CheckDailyBudget`、`AddModelUsage` 和 `model_failures.go` 的 `ModelState` 是唯一额度和暂停状态来源。独立艺人入口不检查整理开关，不伪造整理开启者或发行待定项。模型配置、工具能力和网页来源仍由管家设置与 `Sources` 共同判断。 |
| 显示条件 | `web_search` 的可用性读取独立于整理开关。未配置模型或搜索来源时状态返回 `configured=false`，前端不显示联网按钮。配置已保存但来源暂时不可用时返回原因，点击接口返回分类错误。 |
| 后续资料补齐 | 成功写入 `external_links` 后唤醒 `artist_media` 投影。图片、简介沿用 `artist_media_pass.go`、`artist_media.go` 的来源优先级与可撤销操作。联网查身份本身不写图片和简介。 |
| 规范同步 | 实施最后一步同步 `.trellis/spec/backend/curation-contracts.md` 的网络搜索和模型队列段、`.trellis/spec/backend/steward-web-contracts.md` 的后台搜索能力段、`.trellis/spec/backend/artist-contracts.md` 的身份待定项段和 `.trellis/spec/backend/steward-config-contracts.md` 的共享模型状态段。 |

实现范围只覆盖 `backend/internal/curation`、`backend/internal/curation/program`、`backend/internal/curation/model`、`backend/internal/curation/stewardtools`、`backend/internal/curation/httpapi`、`backend/internal/app` 的后台契约和现有候选处理。网页按钮和提示由主代理修改前端，前端改动不属于本设计的实现文件。协议层在 `backend/internal/steward/provider` 与 `runtime` 按单个实际请求核算艺人余量，治理层在 `backend/internal/operations` 提供同一事务登记两项操作的入口。

## 2. 队列与数据流

### 2.1 加入队列

1. `POST /api/v1/artists/{id}/web-search` 先解析有效艺人，读取当前有效姓名、别名、专辑和曲目样本及本地文件摘要。请求只在 `web_search` 和当前模型都已配置时接受。重复请求复用同一代次，不创建重复待定项。
2. 全量入口先从目录读取没有 MusicBrainz 对应的有效艺人，使用有效主署名和可见发行查询生成稳定顺序。专辑艺人排在前，合辑中的曲目艺人按有效艺人 ID 排列。逐项写入 `curation_pending_items`，并将 `artist_web_generation` 设置为当前队列代次。创建完成后将队列状态置为 `running` 并唤醒模型调度器。
3. 已有对应、艺人已合并、没有在库主署名或当前摘要与已结论相同的艺人不入队。当前摘要的开放候选保留；本地输入变化后可重新查找。成功对应的发行由整理链接手资料补齐。
4. 停止接口在同一语句内挂起本代各项并将控制表改为 `stopped`。恢复时，艺人页只恢复该艺人，全量入口恢复本代未处理项。等待中的项仍是 `waiting_model`，因此重新开始时可以继续。重新开始只为当前仍无对应且摘要未变的艺人恢复本代次，不恢复已达到单项上限的项。

### 2.2 模型执行

模型调度查询 `artist_identity` 时必须连接当前 `curation_artist_web_queue`，仅领取当前代次且队列为 `running` 的项。现有 `PendingItem` 的领取摘要、会话 ID、输入摘要和可见性检查继续生效。独立入口在 `BeforeModelKind` 之外先调用 `CheckDailyBudget`，随后读取 `ModelState`，配置、上游余额、临时暂停和空回复状态按子任务 7 处理。

模型请求只提供 `web_search`、`fetch_page` 和提交艺人候选所需的内部工具。网页工具输入输出沿用 `steward-web-contracts.md`，URL、引用数量和网页读取上限沿用现有校验。每次实际模型 usage 通过 `RecordItemUsage` 记入共享日账，每个工具调用通过 `AddItemCalls` 记入本项。五类模型失败不增加回答次数，项级重试和恢复由现有调度器处理。

模型回答的候选只接受 `source=musicbrainz` 和合法 MBID，保留候选名、消歧说明、依据说明和 1–5 个引用 URL。模型凭记忆给出的姓名、专辑或简介不进入目录，也不作为结构证据。空回答按共享模型合同结束或重试，不能把“没有候选”写成 MusicBrainz 未匹配来源结论。

### 2.3 程序核对和写入

`program` 收到候选后，先在网页工具返回的 MBID 上读取艺人和其发行摘要，再用本地该艺人的可见专辑证据筛选。结构核对复用子任务 5 的相同曲数、碟曲数和时长容差，不新增一套艺人专属阈值。核对上游条目编号和艺人类型，校验引用属于本项。候选名称与本地别名是否吻合保存在预览内，直接写入以结构核对为准。

本地发行通过结构核对时，事务按以下锁序执行。先锁艺人和待定项，再按既有发行写锁和来源对应顺序写入 MusicBrainz 艺人对应、该发行的 MusicBrainz 对应及曲目对应，最后结束艺人项并保存来源、批次和模型会话。写入复用 `external_link.go`、`program/enrich.go` 和 `program/artist_links.go` 的治理入口，不能直接插入 `external_links`。

没有结构吻合的候选进入 `curation_model_exceptions`，`review_id` 等审查字段为空，`status=open`。现有 `GET /api/v1/artists/{id}/web-search` 继续返回候选。确认操作重新读取候选 MBID 和当前艺人摘要，再调用同一个结构核对和写入入口。否掉操作为该候选写 `external_link` 抑制，所有候选均否掉时将项结为 `no_result`。候选确认或否掉期间摘要发生变化返回 `exception_stale`，不覆盖新的本地事实。

## 3. 表、迁移和字段

### 3.1 复用的表

- `curation_pending_items` 继续保存每位艺人的 `target_type=artist`、`artist_id`、`kind=artist_identity`、`evidence`、`input_digest`、候选、状态、领取摘要、`item_tool_calls` 和 `item_tokens`。新增可空代次 `artist_web_generation bigint`、排序 `artist_web_priority`、单项停止标记 `artist_web_suspended` 和配置所属管理员 `artist_web_actor`，只有本任务创建或接续的项填写。旧自动项不删除，也不再被独立队列领取。
- `curation_model_exceptions` 继续保存候选网页引用和确认状态。身份候选的 `tool` 仍为 `resolve_artist_identity`，但本任务产生的行允许没有审查字段。已有媒体候选和历史审查行按原状态兼容。
- `curation_work_states` 的 `subject=artist_media` 继续记录资料投影状态。它不再触发网页身份项。成功身份写入后只唤醒该艺人的资料投影。
- `external_links`、`operations`、艺人资料表和发行对应表沿用现有治理、操作来源、撤销和家族解析。
- `curation_model_usage`、`curation_model_state` 和 `curation_settings` 沿用子任务 7 的共享日账、暂停和模型设置。

### 3.2 新表

新增 `curation_artist_web_queue` 单行表，字段如下。

| 字段 | 类型和含义 |
| --- | --- |
| `id` | `boolean primary key`，固定为 `true` |
| `state` | `idle`、`running` 或 `stopped` |
| `generation` | `bigint`，每次全量或重新开始生成时递增 |
| `actor_id` | 发起管理员。总数、已结项数和运行数按本代待定项统计 |
| `started_at`、`stopped_at` | 当前代次启动和停止时间，可空 |


迁移在表中插入固定行，并为 `curation_pending_items.artist_web_generation` 建索引。迁移不把历史自动项转入任何代次。旧列默认空值保持可读，down 删除新列和单行表，历史待定项、例外、对应和日账保留。

迁移文件按合入时的最大编号顺延，预定名称为 `backend/migrations/NNNN_artist_web_lookup.sql` 与同名 `.down.sql`。子任务 5 若占用预定编号，由主代理按父任务的迁移顺序调整文件名，不改变 SQL 合同。

## 4. HTTP 契约

接口沿用管理员会话、同源校验和统一错误映射。读取艺人状态的现有成员 403 规则保持不变。

### 4.1 单艺人

`GET /api/v1/artists/{id}/web-search` 保留原有 `state` 和 `candidates`，增加以下字段。

```json
{
  "state": "none|queued|running|shelved|waiting_human",
  "configured": true,
  "can_start": true,
  "can_stop": false,
  "queue": {"generation": 3, "position": 4, "total": 28},
  "usage": {"tool_calls": 2, "tokens": 1840, "tool_call_limit": 10, "token_limit": 50000},
  "candidates": []
}
```

`configured` 表示模型与网页搜索均已就绪。`can_start` 和 `can_stop` 是后端按当前状态计算的动作提示，不能代替写接口的权限和状态复查。`queue` 在该艺人不属于当前代次时返回 `null`。候选仍返回 `exception_id`、`source`、`external_id`、`name`、`description`、`note` 和 `citations`，本任务新候选的 `source` 固定为 `musicbrainz`。

`POST /api/v1/artists/{id}/web-search` 无请求体，返回 202 和同一状态。未配置网页搜索返回 `409 search_unconfigured`，未配置模型返回 `409 model_not_configured`，已存在对应返回 409 `artist_already_linked`，并入旧艺人返回 409 `artist_redirected`。重复请求可安全重放，不产生第二个开放项。

### 4.2 全量队列

`GET /api/v1/admin/curation/artist-web-search` 返回：

```json
{
  "configured": true,
  "state": "idle|running|stopped",
  "generation": 3,
  "total": 28,
  "queued": 17,
  "running": 1,
  "processed": 10,
  "can_start": true,
  "can_stop": true,
  "retry_at": null
}
```

`POST /api/v1/admin/curation/artist-web-search` 创建或接续当前代次并返回 202。`DELETE /api/v1/admin/curation/artist-web-search` 请求停止，返回 200 和更新后的状态。日额度达到上限时队列仍为 `running`，`retry_at` 为下一个日界。管理员停止后状态为 `stopped`，保留未处理项和候选，下一次 POST 接续当前代次。

### 4.3 候选确认

现有 `POST /api/v1/admin/curation/exceptions/{id}/accept` 和 `/decline` 保留路径和幂等语义。确认和否掉都在后端重新核对当前候选。确认成功返回既有操作回执，否掉返回 200 和更新后的 exception。候选确认不调用独立审查模型。

## 5. 规则版本、兼容和回滚

新增 `ArtistWebRuleVersion = 1`，定义于 `backend/internal/curation/artist_web_queue.go`。`input_digest` 包含有效艺人 ID、所有用于排序和结构核对的本地发行摘要、真实姓名与别名、相关文件基线摘要、当前规则版本和来源配置摘要。网页查询时间、模型回答次数和候选顺序不放进摘要，避免同一事实重复触发。

现有 `ArtistWebSearchStatus` 的旧状态值和候选字段保留。没有本任务代次的历史项仍可由旧读取接口显示，但不会被新队列自动领取。已有 `open` 候选仍可确认或否掉。旧模型审查候选的 `review_id` 和审查状态保持原流程，只有本任务新候选走无审查路径。

部署顺序是先迁移、再发布后端、最后发布前端。迁移失败不写队列状态。后端回滚先停止艺人网页队列，再回退应用和迁移。回滚会停止新代次并删除控制表和代次列，保留已经写入的对应、发行匹配、操作历史、候选例外和共享日账。已写入的数据按现有操作撤销，不通过 down SQL 删除。

## 6. 重要取舍

- 复用 `curation_pending_items` 保持现有领取、输入摘要、来源错误和用量记账语义，避免另建一套模型会话和重试协议。
- 用单行队列控制表区分手动队列与整理开关。独立艺人功能不依赖 `curation_settings.enabled`，符合子任务 7 关于独立队列启停的合同。
- 结构核对只调用已有 MusicBrainz 发行核对，不接受模型自行写入的专辑、曲目或艺人资料。无结构证据的候选必须由用户确认。
- 每位艺人最多执行 10 次工具调用。每次模型请求和重试前检查共享日账与本项剩余的 50000 token，按完整协议请求估算输入并限制输出。已发送请求按上游报告结算，最后一次实耗可越过余量，触线后停止发送。上游完全缺报 usage 时，本项扣除输入估算加此次输出上限，共享日账仅记录已报告用量。达到任一单项上限后保留候选，避免把“查不到”误报成 MusicBrainz 未收录。
- 队列顺序每次从当前可见本地证据计算。停止后恢复会跳过已结项和已变化项，不保存一份可能过期的专辑排序快照。

## 7. 已定口径

1. 迁移文件使用 `NNNN` 占位，合入父任务时按当时最大迁移号顺延。实现与接口文档不依赖具体编号。
2. 单艺人接口保留 `/api/v1/artists/{id}/web-search`，全量队列使用 `/api/v1/admin/curation/artist-web-search`，前端按本设计字段读取状态。
3. 候选 MusicBrainz 艺人名下只要有一张发行（或同组的几版）与本地该艺人的专辑结构吻合，艺人身份直接写入。专辑写入复用子任务 5 的判定，唯一一版认到版本，同组几版认到组且不补版本字段，跨组不写专辑对应并记录冲突，艺人对应仍写入。完全没有结构吻合时才将候选列给用户确认或否掉。

4. 每位艺人最多执行 10 次工具调用。每次模型请求和重试前检查共享日账与本项剩余的 50000 token，按完整协议请求估算输入并限制输出。已发送请求按上游报告结算，最后一次实耗可越过余量，触线后停止发送。上游完全缺报 usage 时，本项扣除输入估算加此次输出上限，共享日账仅记录已报告用量。
