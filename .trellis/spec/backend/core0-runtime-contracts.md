# Core 0 当前运行合同

## 1. 范围与触发

本合同记录 Core 0 已落地且可回归验证的跨层实现：Go module 启动、
`/api/v1` REST、PostgreSQL 用户与目录状态、扫描终态、发行/逐轨元数据、前端 decoder、
真实音乐只读 Smoke 以及同源生产资产。它由启动失败、权限默认值、用户事务和扫描对账问题触发，
用于防止后续代理依据“尚未选择”的旧模板实现。

当前代码仍是过渡单体：后端位于 `backend/cmd/roomusic`，前端位于
`frontend/src`，尚未拆分为目标 `internal/*`/`features/*` 目录。

## 2. 接口签名

- 生产启动：`go -C backend run ./cmd/roomusic`，由 `scripts/prod.sh` 从仓库根目录调用。
- 开发启动：`./scripts/dev.sh`，默认读取 `.env.dev`，Go 运行在 `backend/`，
  Vite 通过开发配置代理 `/api` 到 `:8080`。
- HTTP：`POST /api/v1/users`、`PATCH /api/v1/users/{id}`、
  `POST /api/v1/library-roots`、`POST /api/v1/scans`、
  `GET /api/v1/scans/active`、`GET /api/v1/scans/{id}`、
  `POST /api/v1/scans/{id}/cancel`、`GET /api/v1/scans/{id}/diagnostics`、
  `GET /api/v1/releases`、`GET /api/v1/releases/{id}`、管理员专用
  `GET /api/v1/releases/{id}/evidence` 和 `GET /api/v1/artworks/{id}`。
- 用户更新请求：`{ "disabled": boolean }`；响应：`{ "disabled": boolean }`。
- 目录新增响应：`{ "id": string, "name": string, "status": "active" | "disabled", "revision": integer }`。
- 目录列表项目：`id`、安全 basename `path`/`name`、`status`、`revision`、时间戳；
  不返回原始绝对路径。
- 扫描状态枚举：`running | succeeded | failed | canceled | incomplete`。
- 前端请求入口：`requestApi(path, decoder, options?)`；所有成功响应先经过
  decoder，非 2xx 统一解码为 `ApiRequestError`。
- 真实音乐显式验收：`ROOMUSIC_REAL_LIBRARY_SMOKE=1 ./scripts/real-library-smoke.sh
  --music-root <绝对目录> --v0-archive <固定归档>`；不得进入默认测试或 CI。
- Release 详情的 `edition`、`label`、`barcode` 为 nullable string；每条 Track 的
  `credits` 项为 `{role,name}`，`role` 只允许
  `composer | conductor | performer | producer`。每轨最多 100 条、单个 Release 最多
  10,000 条 Track credits，超限后端 fail closed，前端 decoder 同样拒绝。

## 3. 合同

### 请求与响应

- 状态变更请求必须通过 Origin 校验、HttpOnly session 和后端角色授权；前端
  角色仅控制展示。
- 错误 envelope 为 `{ "error": { "code": string, "message": string }, "request_id": string }`。
  消息不得包含 SQL、绝对路径、密码、token 或数据库 URL。
- `requestApi` 使用 `credentials: "include"` 和 JSON Content-Type；浏览器不
  读取或存储 session token。
- JSON 请求体上限 1 MiB；搜索词最多 200 字节；发行分页默认 `page=1`、
  `page_size=50`，最大 100；扫描诊断最多 100 条。
- 发行列表只返回至少含一个 `present` Track 的 Release，支持严格 allowlist 的
  `attention=required`。详情返回有界 Medium/Track、credits、音频/CUE facts 和
  普通用户安全 evidence 摘要；完整候选、reason code 和安全相对来源只由管理员
  evidence 端点返回，普通用户必须得到 403。
- 发行列表/详情的封面摘要见 [Catalog REST 合同](./catalog-rest-contracts.md)。

### 数据库与扫描

- PostgreSQL 是 Core 0 唯一业务权威，连接实现为 `database/sql + pgx/v5`。
- 启动迁移由 `backend/cmd/roomusic/database.go` 负责：嵌入的
  `backend/migrations/*.sql` 必须从版本 1 连续排序，原始字节使用 lowercase
  SHA-256；执行器在一个事务中持有 `pg_advisory_xact_lock(0x524f4f4d55534943)`，
  成功版本在 `schema_migrations` 中记录文件名、校验和与时间。
- `0008_migration_metadata.sql` 为迁移记录增加 `name`/`checksum`。旧版数据库
  可能只有 0001、0006、0007 三条记录；确认这段连续历史后，0002--0005 只做一次
  元数据基线，不重放 SQL。名称或校验和漂移、未发布版本和无法证明连续历史均
  fail closed；不提供 down migration，生产回退使用备份或 forward-fix。
- 迁移 SQL、DDL 和记录写入同一事务；锁等待取消或任一步失败都会回滚，只有
  提交成功后才执行中断扫描恢复并进入 ready。
- 用户禁用/启用在同一事务中锁定启用管理员集合和目标用户，检查最后管理员，
  更新 `disabled_at`，并在禁用时撤销该用户未撤销 session。
- 目录新增对已存在路径执行幂等 upsert，但不隐式恢复 disabled 状态；响应和
  Operation Journal 使用数据库真实 `status`/`revision`。
- 扫描只读取 `active` roots。unsupported 音频候选、CUE 语法/编码错误、
  `unsafe`/`unchecked` 引用、权限和遍历错误将 root 标记为不完整；只有全局
  `succeeded` 才允许 `missing` 对账。已经安全解析但目标不存在的 CUE 引用，以及
  缺少 `INDEX 01` 的 CUE track，只写有界 `cue_reference` 诊断并跳过不可用虚拟 Track，
  不单独阻断其它确定输入完成扫描；不得为其伪造父来源或零偏移 Track。非音频附件
  （例如 `.jpg`、`.png`、`.txt`）不产生 unsupported 音频诊断。
- `0011_scan_staging_and_identity.sql` 增加有界 scan staging、稳定 candidate anchor、
  物理/CUE source identity、音频/CUE 列、current field/grouping evidence、Release credits
  和 source/media/genre/catalog 字段；旧 CUE 身份在首次修复扫描时复用原 Track ID。
- `0012_track_credits_and_release_metadata.sql` 增加 Release `edition`/`label`/`barcode`
  以及 `track_credits(track_id,role,name,position)`。逐轨角色由数据库 CHECK allowlist
  约束，`track_id + role + name` 唯一；重扫先替换该 Track 的 current credits，不能按扫描
  次数累计。
- scanner 先把 observation 写入 `scan_observations`，再按 organization scope 有界
  读取并运行纯 organizer；每个 candidate 使用独立短事务写入 Release Graph，解析
  失败不阻止其它有效 candidate 提交。无论成功或失败都清理本次 staging；只有全局
  `succeeded` 才执行 missing 对账。
- root 遍历开始前的 staging 清理失败必须立即中止该 root，结束时的兜底清理失败必须
  令 root 保持不完整并写入 `staging_write_failure`。每个 scope 最多读取 10,000 条
  observation，单条 JSON 最多 24 MiB，scope 名按 256 条分页遍历；不得退回 root 级
  无界内存 slice。
- 物理 Track 以 root + 规范化相对路径复用身份；CUE Track 以 sheet、父来源、track
  和 `INDEX 01` 复用身份。每次候选写入替换 current decisions、grouping evidence、
  credits 和 track observations，不按重扫次数追加。
- CUE 引用可以位于 sheet 的父目录或兄弟目录，只要仍在显式注册根 containment 内；
  staging 必须先按规范化 `cue_parent_relative_path` 与物理 observation 对齐 scope，
  organizer 再仅在同一 candidate 和明确父子关系内消除虚拟/物理重复。不得仅按 sheet
  所在目录、标题或时长模糊去重。扫描前使用解析 symlink 后的 `Stat` 验证媒体节点为
  regular file，FIFO、设备和逃逸链接不得传给 parser。
- parser 默认的 track/disc `1` 必须带 inferred 标记；organizer 只对缺失或 inferred
  位置应用 Disc/CD/Disk 目录与稳定路径 fallback，显式标签永远优先。CUE sheet 与
  track PERFORMER 的 provenance 分别为 `cue_sheet` 和 `cue_track`，构造每条虚拟 Track
  时复制 `InferredFields`/`FieldSources` map。
- Track artist 与 composer/conductor/performer/producer 使用同一组保守拆分规则：明确的
  `; `、`；`、` / `、`feat.`、`ft.`、`with`、`、` 可拆分；紧凑 `/`、`,`、`&`
  只按已固化规则处理，`Simon & Garfunkel`、`Earth, Wind & Fire`、`AC/DC` 等固定组合
  保持整体。只有显式官方别名表可以改变 canonical display name；禁止用相似度猜别名。
  最终 Track artist 对 canonical 名去重、稳定排序并以 ` / ` 连接，原 tag observation
  仍保留为来源证据。
- 缺失 `AlbumArtist` 表示未知，不是与唯一明确值冲突：相同权威 album 下只有一个明确
  `AlbumArtist` 时，缺失值与该 partition 兼容；出现多个明确值时仍按多数决或稳定拆分。
  生成 Release artist/credit 时，只要存在明确 `AlbumArtist`，就先在这些值之间决策，
  不允许多数 Track Artist 覆盖它。根目录 staging scope 先按权威 album 聚合，再由
  organizer 决定 album artist partition，避免在决策前永久拆散兼容 observation。
- `.log` 抓轨证据只读取候选来源目录中普通、非 symlink 文件的前 64 KiB；只有明确
  EAC/XLD 产品签名才补 `source_type=CD`、`media_type=CD`。它可以替换此前仅由
  `folder` 产生的补充值和 provenance，但不得覆盖 tag、CUE、音频规格或其它明确来源；
  替换后每个字段仍只能有一条 current decision。field uncertainty、grouping inconsistency 和可见 missing 来源
  在 `attention_count` 中按语义去重；同目录拆分使用独立
  `same_directory_conflict` reason。
- 封面只在 Release ID 确定后写入 ROOMusic 管理目录并绑定；读取、校验或数据库绑定
  失败产生独立诊断，不修改源音乐或源封面。命名 folder artwork 存在但不安全、为空、
  超限或格式无效时必须 fail closed 并保留原关系，不能当作“来源已删除”。复用由 hash
  命名的受管文件前核对实际字节，损坏时原子替换；数据库查询、绑定失败后的新文件清理、
  旧 key 引用检查和文件删除错误都必须向上返回，使扫描进入 `incomplete`。
- `canceled` 是已落地的 wire 终态；取消意图持久化在
  `scan_runs.cancel_requested_at`，扫描 worker 由应用生命周期 context 管理，
  不继承启动或取消请求的 HTTP context。

### 环境与资产

- Node/Go 版本由 `.mise.toml` 管理，依赖安装使用 `npm ci`；`package-lock.json`
  是前端可重复安装依据。当前 `package.json` 的 `latest` 依赖是待治理风险。
- 生产 Vite 构建写入唯一的 `backend/cmd/roomusic/web`；该目录是生成资产，
  不手工编辑。开发 `vite.config.dev.ts` 才允许自定义 Host 和 API 代理。
- `allowedHosts: true` 只放宽 Vite Host 校验；后端仍按
  `ROOMUSIC_PUBLIC_URL`、Origin scheme/host 和 Secure Cookie 校验写请求。
- `scripts/dev.sh` 默认只确保 PostgreSQL、Go 和 Vite；Redis/Meilisearch 是
  Compose/mise 可选服务。`scripts/prod.sh` 不启动 Node 服务。
- 真实 Smoke 使用随机 Compose project、空 PostgreSQL 18 volume、临时数据目录和
  `/music:ro`；V0 只从固定 hash 归档注入独立 adapter，运行时网络为 `none`，先写
  normalized SQLite 再导出 snapshot v2。前后 corpus 摘要、SQLite round-trip、current
  首扫/重扫、REST 投影、零 `current_regression`、零未知分类和分类计数闭合都是成功
  门禁；本地产物只能写入权限受限且 Git 忽略的 `.roomusic-smoke/`。成功后可保留 V0
  SQLite/canonical 及经过稳定键、字段白名单与脱敏约束的 current A/B snapshot、comparison
  和 manifest；原始 SQL dump、逐项路径、凭据、Cookie 与 exporter rows 必须删除。
- 任务内 `research/smoke-result.md` 只发布脱敏聚合。并发成功运行各自保留独立审计目录，
  聚合报告采用“最后一个完整成功运行胜出”的同目录原子 rename，不允许并发直接覆盖写入。
- corpus 摘要对每个节点纳入规范相对路径、节点类型、mode 和 mtime；普通文件另外纳入
  size 与流式内容 SHA-256，目录节点也必须参与总摘要。遍历不跟随 symlink，前后任一
  节点事实变化都会使本轮对照失效。

### CI 质量门禁

- `.github/workflows/ci.yml` 在 Pull Request、推送到 `main` 和手动触发时运行，权限
  固定为 `contents: read`，同一分支的新运行会取消旧运行。
- 工作流使用 `.mise.toml` 中的 Go `1.25.10` 和 Node.js `24.16.0`。后端门禁从
  `backend/` module 运行 `gofmt`、`go test`、`go test -race`、`go vet` 和 `go build`；
  前端使用 `frontend/package-lock.json` 执行 `npm ci`、lint、typecheck、Vitest 和
  production build。
- production build 后必须执行 `git diff --exit-code -- backend/cmd/roomusic/web`，
  确认 Go 内嵌生成资产没有漂移。集成 job 执行 `bash -n scripts/*.sh`、两个 Compose
  配置校验和 `./scripts/test-integration.sh`；后者真实启动 PostgreSQL 18 并在结束时
  清理专用容器和数据卷。
- CI 不启动或依赖 Redis/Meilisearch，不读取生产 `.env`，也不使用真实音乐目录。普通
  测试因未设置 `ROOMUSIC_TEST_DATABASE_URL` 而跳过时，不得被视为 PostgreSQL 集成门禁
  通过。

### HTTP 完成日志

请求中间件在每个请求结束后恰好写出一个 `http.request.completed` 结构化 JSON 事件。
事件字段为：

| 字段 | 合同 |
| --- | --- |
| `event` | 固定为 `http.request.completed` |
| `module` | 固定为 `platform` |
| `message` | 固定为 `http request completed` |
| `request_id` | 有界且经过字符集校验，并回写 `X-Request-ID` |
| `method` | HTTP 方法 |
| `route_template` | ServeMux 注册的路由模板；未匹配时为 `<unmatched>` |
| `status` | 最终状态码；隐式响应默认为 200 |
| `response_bytes` | 实际写入字节数 |
| `duration_ms` | 非负耗时（毫秒） |
| `actor_id` | 可选稳定用户 ID，仅认证成功时出现 |

JSON `slog` handler 另外提供 UTC `time` 和 `level`。状态小于 400 记录为 `INFO`，
400--499 为 `WARN`，500 及以上为 `ERROR`。日志中间件不查询数据库、不执行权限判断；
日志失败不得改变响应、事务或 panic 语义。事件不得包含 query string、请求体、Cookie、
Authorization/session token、密码、数据库 URL、完整 NAS 路径或音频/封面内容；匿名请求
不填充 `actor_id`。

## 4. 校验与错误矩阵

| 条件 | 行为 |
| --- | --- |
| session 缺少/未知 `role` | decoder 拒绝，不能默认为 admin |
| 用户目标不存在 | `404 not_found`，事务回滚 |
| 禁用最后一个启用管理员 | `409 last_admin`，不改用户或 session |
| 用户 SQL/事务失败 | `503 database_unavailable` 或分类错误，不返回成功 |
| 重复注册 disabled root | 返回 `status=disabled` 与真实 revision，不隐式恢复 |
| unsupported 音频、CUE 语法/编码、`unsafe`/`unchecked` 引用或遍历错误 | 有界诊断，扫描 `incomplete`/`failed`，禁止 `missing` |
| CUE 引用目标不存在或 Track 缺少 `INDEX 01` | 写 `cue_reference` 诊断并跳过不可用虚拟 Track；不单独令扫描 incomplete，也不伪造父来源/offset |
| 初始或终态 staging 清理失败 | root 不完整并记录 `staging_write_failure`；旧 observation 不得混入本次候选 |
| 媒体节点不是 regular file | 不调用 parser，记录 `non_regular_file` 并令扫描不完整 |
| CUE 显式父来源跨 sheet 目录但仍在注册根内 | 对齐到父来源 scope，保留稳定虚拟身份并按明确关系去重 |
| rip log 无明确签名、签名晚于 64 KiB 或为 symlink | 不产生 CD 语义，不读取超出预算的内容 |
| 明确 rip log 与 `folder` 补充值并存 | 选定 CD 并把 decision provenance 提升为 `rip_log`；不得保留重复字段决定 |
| 明确 rip log 与 tag/CUE/音频规格决定冲突 | 保留明确来源，不允许抓轨日志覆盖 |
| 多艺人/credit 含明确分隔符或官方别名 | 按保守规则拆分、官方表归一、去重；固定组合名不得误拆 |
| V0 codec 为 `aac`、current 为 `alac`，且 V0 缺少 current 的 5 类音频事实 | 只允许 `duration_ms`、`sample_rate`、`channels`、`bitrate`、`bit_depth` 归为窄 `intentional_contract_difference` |
| canonical 对照出现未知字段差异 | 保留为 `current_regression`/未分类失败；不得用空值或通用 ignore 放行 |
| V0/current 报告出现 `current_regression`、未知分类或分类计数与差异项不闭合 | 整轮 Smoke 非零退出，不发布成功报告或保留该轮审计产物 |
| 相同 album 的部分 Track 缺少 AlbumArtist，且其它 Track 只有一个明确值 | 保持同一候选，明确 AlbumArtist 优先于 Track Artist |
| 命名 folder artwork 非普通文件、为空、超限或格式损坏 | `artwork_failure`，保留已有关系，不解释为封面删除 |
| content-addressed 受管文件与其 hash 内容不符 | 写临时文件并原子替换，随后才确认数据库关系 |
| 受管封面查询或旧文件清理失败 | 返回分类错误并令扫描不完整，不静默吞错 |
| 单个 candidate/封面写入失败 | 已提交候选保持可见，记录分类诊断并使扫描不完整，禁止 `missing` |
| 扫描成功且所有 root 完整 | 允许负向 `missing` 对账 |
| 非音频附件 | 忽略，不改变扫描完整性 |
| 前端非 2xx 或 malformed JSON | `ApiRequestError` 安全回退并保留 request ID |

## 5. 正确、基准与错误案例

- 正确：管理员状态变更锁定同一组管理员行，session 撤销与 `disabled_at` 一起提交；
  两个并发禁用请求至多成功一个。
- 基准：重复 POST 已停用目录返回 disabled；管理员随后使用 restore 端点显式恢复。
- 正确：多碟来源按 `disc_number=1/2` 形成两个 Medium，重扫保持 Track ID。
- 正确：`sheets/album.cue` 引用 `../media/image.flac` 时，只要两者仍在同一注册根内，
  虚拟轨会与显式父来源对齐；每轨 provenance 独立，track PERFORMER 不污染相邻轨。
- 基准：历史 CUE 指向已不存在的原始 WAV，或个别 Track 没有 `INDEX 01` 时，保留
  `cue_reference` 诊断并跳过这些虚拟 Track；同目录其它有效物理来源仍可完成扫描。
- 基准：只有 EAC/XLD 明确签名位于 `.log` 前 64 KiB 时补充缺失的 CD 语义；无签名
  日志保持未知。
- 正确：目录名先补了 WEB/CD 等来源值，但同 candidate 存在明确 EAC/XLD 日志时，
  抓轨规则可以替换 `folder` 决定；tag 或 CUE 的明确来源仍保持不变。
- 正确：`Artist Beta, Artist Alpha` 作为 Track artist 被拆分后以稳定顺序展示；已登记的
  官方别名合并为 canonical 名，而固定组合名不会因包含 `/`、`,` 或 `&` 被误拆。
- 基准：V0 的组装前 `grouping_medium_count=1` 与双方最终两个 Medium 并不代表图回归；
  只有在 Medium keys 完全一致且 current 值等于最终图计数时，才允许归为窄
  `schema_mapping_gap`。
- 基准：V0 将 ALAC 报为 `aac` 且没有音频事实、current 将同一来源报为 `alac` 并补齐
  5 类音频事实时，差异可以按窄规则接受；同样的空值差异若发生在 catalog、CUE ISRC
  或其它字段，仍是 `current_regression`。
- 正确：一张专辑只有一轨提供 `AlbumArtist`、其余轨只提供各自 Artist 时仍形成一个
  candidate，Release artist 与 credit 使用明确 `AlbumArtist`；缺失轨不伪造自己的 tag。
- 正确：已存在的 content-addressed 封面文件被破坏后，重扫按期望字节修复；旧 key
  清理失败返回 `artwork_failure`，不报告成功。
- 错误：把 `role` 缺失解释为 admin；把 root 原始路径放入列表 JSON；unsupported
  文件仍让扫描 succeeded 并执行 missing；把 FIFO 交给 parser；按标题删除 CUE 轨；
  忽略 `Exec` 错误后返回 200。

## 6. 必需测试

- 前端 `api.test.ts`：session role 缺失/未知、创建/更新用户、root active/disabled、
  malformed payload 和错误 envelope fallback。
- 后端单元：终态映射、unsupported 音频候选识别、来源身份规范化和路径 containment。
- parser/organizer 单元：M4A/WAV 容器有效性、CUE 多 FILE 与跨目录 containment、
  missing reference/missing `INDEX 01` 非阻断跳过、`unsafe`/`unchecked` 引用阻断、
  sheet/track performer provenance、逐轨 map 隔离、inferred track/disc fallback、
  明确标签优先、缺失 AlbumArtist 兼容与 AlbumArtist 决策优先级、艺人/credit 多分隔符、
  官方别名/固定组合名，以及 rip log 64 KiB 预算、签名和仅覆盖 `folder` 的优先级。
- PostgreSQL 集成（配置 `ROOMUSIC_TEST_DATABASE_URL` 时）：用户禁用与 session
  原子性、最后管理员/并发保护、未知用户 404、目录幂等与真实状态、扫描 incomplete
  禁止 missing、多 Medium 与 Track 身份稳定；另需覆盖 staging 清理、候选短事务
  回滚/部分提交、跨目录 CUE 父来源对齐、CUE legacy identity、regular-file 拒绝、
  current evidence/track credits 替换、Track artist canonical 展示、受管封面内容重校验、
  旧 key 清理错误传播和绑定失败清理。
- 发行 REST/前端：present-only 列表、稳定分页/搜索、attention allowlist、详情事实、
  管理员 evidence 权限、路径脱敏、nullable/enum/数组上限和 malformed payload。
- HTTP 中间件：隐式 200、显式状态、错误响应的状态/字节统计，路由模板、耗时、日志
  级别、请求 ID 回写、可选 actor_id 以及敏感字段脱敏；每个请求只能有一个完成事件。
- 运行门禁：`npm run lint`、`npm run typecheck`、`npm run test`、
  `gofmt -l .`、`go test ./... -count=1`、`go vet ./...`、`go build ./...`、
  `go test -race ./... -count=1`、`bash -n scripts/*.sh`、两个 Compose 配置校验、
  `./scripts/test-integration.sh` 和 `git diff --check`（按改动取最小集合）。
- 真实 Smoke 比较器：ALAC current-only composer 只有在 V0 `aac`/current `alac` 且
  其它非 performer credits 完全保持时才是合同差异；current-only 音频事实只允许
  `duration_ms`、`sample_rate`、`channels`、`bitrate`、`bit_depth` 这 5 类；stale Medium
  count 只有图 keys 相同才是映射差异；未知 catalog/CUE ISRC 以及每条窄分类的相邻反例
  必须仍落入 `current_regression`。

## 7. 错误与正确示例

### Wrong

```go
_ = db.ExecContext(ctx, "UPDATE users SET disabled_at=NOW() WHERE id=$1", id)
writeJSON(w, 200, map[string]bool{"disabled": true})
```

这会吞掉数据库错误，也没有最后管理员保护或 session 原子撤销。

### Correct

```go
tx, err := db.BeginTx(ctx, nil)
// lock enabled admins and target, validate last-admin invariant
// update user and revoke sessions in the same tx
if err == nil {
    err = tx.Commit()
}
if err != nil {
    _ = tx.Rollback()
    writeAPIError(w, r, http.StatusServiceUnavailable, "database_unavailable", "无法更新用户")
    return
}
writeJSON(w, http.StatusOK, map[string]bool{"disabled": requested})
```

前端同样必须调用 `requestApi(..., decodeUpdatedUser)`，禁止把 `unknown` 直接
cast 成 `UserDTO`。

### CUE 诊断错误与正确示例

错误：把所有 CUE diagnostic 都提升为 root incomplete；这会让缺失的历史 sidecar
引用阻断同目录中已经确定的物理音频，也无法完成真实库重扫。

```go
for range document.Diagnostics {
	complete = false
}
```

正确：诊断始终保留；只有无法证明安全边界的引用阻断扫描。`missing` 是已确定的不存在，
对应虚拟 Track 被跳过，后续成功终态可以让旧来源按正常规则进入 missing 对账。

```go
if cueReferenceMakesScanIncomplete(reference.Status) { // unsafe/unchecked/unknown
	complete = false
}
if reference.Status != "present" {
	continue
}
```

### 抓轨证据优先级错误与正确示例

错误：只看最终字符串是否为空，会把先到达的目录补充值误当作权威值。

```go
if candidate.SourceType != "" {
	return
}
candidate.SourceType = "CD"
```

正确：检查选定 decision 的 provenance；仅替换 `folder`，tag/CUE 等其它来源不变。

```go
if decision.Source == "folder" {
	candidate.SourceType = "CD"
	replaceDecision("source_type", ripLogDecision)
}
```

## 场景：持久化扫描取消与跨进程协调

### 1. 范围与触发

当代码创建、恢复、查询、取消或结束全库扫描时，必须遵守本场景。其目标是在多个
ROOMusic 进程之间只允许一个扫描执行器，并保证取消、停机、数据库故障或执行权丢失
不会触发错误的 `missing` 对账。PostgreSQL 同时是扫描状态和执行资格的权威；进程内
mutex 只保护本进程的 worker 引用。

### 2. 接口与数据库签名

- `POST /api/v1/scans`：管理员启动扫描；新建时返回 `202`，已有本进程或其他进程的
  活动扫描时返回该活动 DTO 和 `200`。
- `GET /api/v1/scans/active`：已登录用户查询活动扫描，返回
  `{ "scan": ScanDTO | null }`。
- `GET /api/v1/scans/{id}`：已登录用户查询指定扫描。
- `POST /api/v1/scans/{id}/cancel`：管理员持久化取消意图；请求不等待 worker 结束。
- `scan_runs.cancel_requested_at TIMESTAMPTZ NULL`：`NULL` 表示未请求取消，首次取消写入
  数据库时间，重复取消用 `COALESCE(cancel_requested_at, NOW())` 保留原时间。
- `scanAdvisoryLockKey = 0x5343414e`：专用 `database/sql.Conn` 调用
  `pg_try_advisory_lock(bigint)` 获取 PostgreSQL session advisory lock，并在 worker
  结束时显式 unlock 后关闭连接。

### 3. 请求、响应与生命周期合同

`ScanDTO` 的 JSON 字段固定为：

```json
{
  "id": "uuid",
  "scan_run_id": "uuid",
  "status": "running | succeeded | failed | canceled | incomplete",
  "started_at": "RFC 3339 timestamp",
  "finished_at": "RFC 3339 timestamp | null",
  "cancel_requested_at": "RFC 3339 timestamp | null"
}
```

- `id` 与 `scan_run_id` 当前必须相同；新增字段和状态必须同时更新后端 presenter、前端
  decoder、状态文案和测试，前端不得将未知状态强制转换为已知状态。
- 启动和取消是同源、session、管理员授权的状态变更；活动/指定状态查询要求有效
  session。前端权限只控制按钮可见性，不能代替后端鉴权。
- worker 的父 context 属于应用生命周期，不得使用 HTTP 请求 context。取消端点只提交
  意图；worker 每 500 ms 轮询一次，并在 root、目录遍历和文件处理边界协作式停止。
- session advisory lock 必须由贯穿扫描生命周期的专用连接持有；扫描读取、观察、诊断
  和终态写入使用该 holder。连接池 `MaxOpenConns=1` 无法同时容纳 holder 与控制查询，
  必须拒绝启动而不是进入死锁。
- 只有持有同一协调锁的恢复路径可以把遗留 `running` 改为 `incomplete`；锁竞争时必须
  保持活动行不变。正常停机先取消 worker、有限等待，再关闭数据库。
- `finalizeScan` 先 `SELECT ... FOR UPDATE` 锁定扫描行。持久化取消优先收敛为
  `canceled`；只有完整 `succeeded` 才能在同一事务执行所有 root 的 `missing` 对账和
  终态更新。`canceled`、`failed`、`incomplete` 与协调故障均禁止负向对账。
- session advisory lock 没有 TTL，不能据本地时间推断锁已失效；发布或回滚时不得让
  不认识该锁的旧扫描器与新扫描器同时运行。

### 4. 校验与错误矩阵

| 条件 | HTTP/状态行为 |
| --- | --- |
| 未登录访问扫描 API | `401` 安全错误 envelope |
| 普通用户启动或取消 | `403`，不创建扫描、不写 `cancel_requested_at` |
| 启动获得锁且无活动任务 | 插入一个 `running`，返回 `202` 和新 DTO |
| 本进程已有活动任务 | 返回同一 DTO 和 `200`，不创建第二行 |
| 未获锁但可读取其他实例的活动任务 | 返回该 DTO 和 `200`，不等待、不接管 |
| 未获锁且无法确认活动任务 | `503 scan_coordination_unavailable`，不创建扫描 |
| holder 会耗尽唯一连接池槽位 | `503 scan_coordination_unavailable`，不启动 worker |
| 查询未知扫描 | `404 not_found`；其他数据库错误为 `503 database_unavailable` |
| 首次或重复取消 `running` | `202`；时间戳非空且重复请求保持不变 |
| 取消已是终态的扫描 | `200` 和原 DTO；不得改写终态或取消时间 |
| 恢复时协调锁正被持有 | 跳过恢复，其他实例的 `running` 保持不变 |
| 恢复取得锁并发现遗留 `running` | 收敛为 `incomplete`，不自动接管或续扫 |
| 取消、停机、协调/数据库故障 | 不执行 `missing` 对账；终态按已持久化证据收敛 |

### 5. 正确、基准与错误案例

- 正确：管理员连续两次取消同一 `running` 扫描，两次均返回 `202`，DTO 中的
  `cancel_requested_at` 完全相同；worker 随后写入 `canceled` 和 `finished_at`。
- 基准：刷新管理页面后调用活动查询，从 PostgreSQL 恢复“正在扫描”或“取消请求中”，
  不依赖旧页面内存；没有活动任务时返回 `{ "scan": null }`。
- 正确：第二实例未取得 advisory lock，读取并返回第一实例的 `scan_run_id`，不插入
  排队行，也不启动替代 worker。
- 错误：启动时无条件把所有 `running` 改为 `incomplete`；用事务级 advisory lock、
  本地 mutex 或 HTTP context 代替 session holder；先逐 root 提交 `missing` 再更新终态。

### 6. 必需测试与断言点

- PostgreSQL/HTTP：首次与重复取消均返回 `202`，时间戳持久且幂等；终态取消返回
  `200` 且终态不可逆；未知 ID、匿名和普通用户分别验证错误 envelope 与无副作用。
- PostgreSQL 并发：两个独立连接竞争 `0x5343414e` 时只有一个成功；失败实例复用活动
  ID；unlock/close 后可重新获取；`MaxOpenConns=1` 快速失败而非阻塞。
- PostgreSQL 恢复：活 holder 存在时遗留行保持 `running`；释放后恢复为
  `incomplete`，且不创建替代扫描。
- PostgreSQL 终态：取消请求覆盖成功候选并得到 `canceled`；取消、失败和不完整均保持
  旧来源 `present`；成功扫描的所有 root 对账与 `succeeded` 更新一起提交或回滚。
- 后端并发与生命周期：取消轮询、停机等待、holder 释放和本进程 worker 引用通过
  适用的 `go test -race`；数据库/holder 故障不得继续 stale write。
- 前端 decoder/UI：六个 DTO 字段必需且时间/状态严格校验；活动空值、取消请求中、
  `canceled`/`failed`/`incomplete` 文案以及取消失败后的重试入口均有覆盖。

### 7. 错误与正确示例

#### Wrong

```go
go runScan(request.Context(), scanID) // 客户端断开会成为业务取消
_, _ = db.Exec("UPDATE scan_runs SET status='incomplete' WHERE status='running'")
```

这既没有跨进程执行资格，也会在另一实例仍健康时破坏其活动状态。

#### Correct

```go
execution, acquired, err := acquireScanExecution(app.scanContext, app.database.connection)
if err != nil || !acquired {
    // 返回活动任务，或以 scan_coordination_unavailable 安全失败。
    return
}
go app.runScan(scanID, execution) // worker 由应用生命周期和持久化取消共同驱动
```

终态仍必须在 holder 连接上的单个事务中锁定 `scan_runs`，并仅在判定为
`succeeded` 后执行 `missing` 对账。

## 后续技术债

- HTTP 请求现在通过 `http.request.completed` 事件记录状态码、耗时和路由模板；用户
  创建/禁用/会话撤销仍无独立 Operation Journal。
- `frontend/src/main.tsx` 与 `api.ts` 仍是 Core 0 过渡单体，feature/router/query
  cache 拆分需单独任务。
