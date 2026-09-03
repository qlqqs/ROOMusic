# 真实音乐库只读 Smoke 验收与 V0 对照

## 目标

在完全隔离的临时运行环境中，用当前 ROOMusic 对项目根目录的真实音乐资产执行
完整只读扫描和重复扫描；以固定 V0 代码对同一批资产重新生成的人工校正规则输出为
权威行为基准，按稳定来源和 Release Graph 语义对照，定位并收口当前 parser、organizer、
persistence、REST 投影中的真实缺陷。整个过程不得修改真实音乐、V0 代码/基准、当前
开发数据库或当前 ROOMusic 数据目录。

## 用户价值

- 证明当前 Core 0 的扫描整理闭环不仅通过合成 fixture，也能正确处理实际收藏。
- 用已固化人工校正规则的 V0 输出约束整理结果，避免仅凭当前实现自证正确。
- 每个差异都有可复核的归因和处置，不把资产变化、不可比字段或已知缺失能力误报为
  scanner 回归。

## 已确认事实

- `music/` 是真实用户资产，不是 fixture；本任务得到的是只读扫描授权，不包含移动、
  改名、覆盖、删除、标签/CUE 回写、转码或在该目录生成文件的权限。
- 当前真实资产盘点仅记录聚合信息：399 个文件、约 8.09 GB；任务文档和提交产物不得
  保存可识别的完整文件清单、绝对路径、媒体内容或不必要的 metadata。
- `09-03-album-scan-repair` 已用合成 fixture 验证 M4A/CUE、候选持久化、证据投影和
  重扫合同，并记录全量门禁通过；该任务明确没有执行真实音乐 smoke。
- V0 历史 UAT 的 schema v2 人工基准记录为 110 个 reviewed Release、112 个 Medium、
  466 个 Track、466 个 file evidence；后续记录还显示 631 条字段合同，reviewed-only
  diff 为 `No differences.`。这些数量用于识别历史 corpus 漂移和重建异常，不替代
  当前同一资产上的逐项对照。
- 用户已确认：当前固定 V0 代码的生成结果已经包含此前的人工修正。因此本任务以该代码
  对同一批当前资产生成的 production-model 结果为权威 V0 行为基准，不要求先恢复旧
  `golden.sqlite` 的 review ledger 或 notes。
- V0 后续曾把 SQLite candidate/golden 导入 PostgreSQL `golden` schema，再退役活跃
  SQLite/golden 工具链；本任务只临时运行固定 V0 production scanner 来生成基准，
  不把 V0 runtime 或旧 schema 恢复为当前产品依赖。
- 当前工作区、`ROOMusic-V0` 目录和 `ROOMusic-migration.tar.gz` 归档中未找到
  `golden.sqlite`、`candidate.sqlite`、`corpus-answer.sqlite`、PostgreSQL dump 或 backup；
  本机当前也没有可识别的 V0 PostgreSQL Docker volume，当前 Git/远端历史无法恢复旧
  golden 工具或数据。
- 已只读查询当前 `roomusic-postgres-1`：它只有当前 Core 0 的 `public` schema，不含
  `golden` schema 或 V0 dataset；其 volume 属于当前项目，禁止作为 smoke 临时库使用。
- `ROOMusic-migration.tar.gz` 的 SHA-256 为
  `fe25388328698b26991ea3b59a14406a155eb92d578a9be2a68d67d331ecf97d`。它是不含
  golden 数据的固定 V0 源码快照，但保留正式 scanner、33 个 PostgreSQL migration、
  PostgreSQL/Redis 运行边界以及真实语料集成测试，可作为重建时的代码版本锚点。
- 已对归档与 `ROOMusic-V0` 的 scanner、database、应用入口、migration 和 Compose
  关键文件做逐文件只读比较，未发现差异；归档可以代表当前确认的 V0 生成代码。
- 旧数据库中的 review notes 不参与本次产品结果判定；若日后找到历史 SQLite、dump 或
  PostgreSQL dataset，只作为审计候选。发现多个候选时仍须由用户确认，不自动选最新。

## 需求

### R1. 锁定 V0 代码生成基准

1. 只从上述 SHA-256 的 V0 归档构建并运行正式 scanner；实现前再次核对归档 hash，
   hash 不符即 fail closed，不静默切换到同名目录或“最新”代码。
2. 在独立 V0 PostgreSQL、Redis、Compose project、临时数据目录和只读音乐挂载中完成
   正式 migration、setup、library path 注册和 scan trigger；禁用 AI/provider 及非必要
   外部访问，禁止写回归档、真实资产或任何现存服务。
3. 成功后生成 `v0_generated_corrected` 基准：保存一个权限受限、Git 忽略的本地数据库
   dump 及 manifest，并导出仅供本轮比较的 canonical snapshot。manifest 至少包含代码
   hash、corpus 总摘要、schema/migration、生成时间和聚合计数，不保存凭据。
4. 历史 UAT 数量和 V0 真实语料场景用于 sanity check；因当前 corpus 已变化导致的数量
   差异不得阻止同一当前资产上的 V0/ROOMusic 对照，但必须记录为历史 corpus 漂移。
5. 若运行前或运行中发现多个 V0 数据库候选，先输出 hash、schema、review/生成类型、
   聚合计数和时间等非敏感清单并停止选择，由用户确认；不得自动选“最新”或覆盖候选。
   历史 SQLite 只能以 immutable/read-only 方式盘点，历史 PostgreSQL 只能从只读副本查询。

### R2. 隔离并证明真实资产只读

1. 使用独立 Compose project、临时 PostgreSQL volume、独立端口、临时
   `ROOMUSIC_DATA_DIR` 和专用临时用户；不得加载现有 `.env`/`.env.dev` 的业务库配置，
   不得清空、迁移或查询当前 ROOMusic 数据库。
2. 真实 `music/` 只作为 allowlisted source root。优先在只读 bind mount/sandbox 中
   运行；同时在扫描前后用保存在临时目录的树清单核对路径、类型、大小、mode、mtime
   和内容摘要，最终报告只保留汇总与总摘要，不泄露逐文件信息。
3. smoke 必须显式 opt-in，默认测试和 CI 保持跳过真实资产；异常、取消和进程退出时
   清理临时服务、volume、临时 data 及含敏感相对路径的临时报告。只有完整成功的 V0
   dump 可以保留在权限受限且 Git 忽略的本地目录。
4. V0 归档、任何历史数据库和真实资产在所有失败路径上都保持不变；无法建立隔离、
   只读挂载或前置摘要时不得启动扫描。

### R3. 执行完整扫描与重扫验收

1. 通过当前应用的正式迁移、setup、目录注册和扫描边界执行首次全库扫描，等待持久化
   终态；记录耗时、终态、Release/Medium/Track/来源/diagnostic/attention 等聚合指标。
2. 对每个被发现的支持格式、CUE、命名封面和抓轨日志给出聚合覆盖；解析失败、跳过、
   不安全引用和 unsupported 输入必须有有界分类诊断，不允许静默丢失。
3. 在输入未变化时执行第二次完整扫描，验证 Track 来源身份、Release candidate、
   Medium 归属和 current evidence 稳定，不产生重复或可见空壳；两次扫描均不得改变
   真实资产。
4. 通过管理员 REST 投影抽查列表、详情、attention、evidence 和 diagnostics 与数据库
   聚合一致；不得把绝对路径或完整来源清单写入报告。

### R4. 按稳定语义对照 V0

1. V0 和当前实现必须在同一前置 corpus 摘要下顺序运行；比较使用安全来源摘要、stable
   key 和确定排序，不使用两套数据库的自增 ID/UUID，也不只比较总数。运行期间 corpus
   摘要变化时整轮无效，禁止对部分交集给出回归结论。
2. 历史 UAT 与当前 corpus 的数量差异单独标为 `historical_corpus_drift`；它只影响历史
   sanity check，不影响同一当前资产上重新生成的 V0 基准权威性。
3. 对不变交集比较 Release/Medium/Track/File 层级、归组、多碟、Box leaf、CUE 虚拟轨、
   标题/artist/album artist/year/source/media/edition/label/catalog/genre、credits、
   program-owned field decisions、confidence/action 和关键 evidence。
4. 差异必须归入 `current_regression`、`schema_mapping_gap`、`capability_gap`、
   `historical_corpus_drift` 或 `intentional_contract_difference`；非回归分类必须给出代码、
   schema 或已批准范围证据，不能用作隐藏当前回归的兜底分类。
5. V0 `v0_generated_corrected` 输出是当前已实现 scanner/organizer 字段和结构的硬门禁。
   仅依赖 V0 后续 overlay、AI、provider 或拓扑治理的能力列为 `capability_gap`，除非用户
   重新批准扩大范围，否则本任务不顺带恢复这些运行时。

### R5. 问题收口

1. 每个 `current_regression` 先形成最小可复现的脱敏 fixture 或数据库回归测试，再在
   真正的 parser、organizer、persistence 或 REST owner 修复，禁止对真实资产打补丁。
2. 修复后运行直接相关的最小测试、PostgreSQL 集成测试、真实资产定向复验；最终一次
   完整 smoke 必须重新执行首次扫描、重扫和 V0 对照。
3. 每个差异必须有 `fixed`、`mapped`、`accepted_capability_gap`、
   `accepted_intentional_difference` 或 `historical_corpus_drift` 处置；未解释差异不得
   宣告任务完成。
4. 若差异要求新增人工 metadata overlay、拓扑治理、新格式平台或改变长期安全不变量，
   返回规划并由用户决定拆分子任务，不在本任务中无界扩张。

## 验收标准

- [ ] AC1：V0 归档 hash 与已确认值一致；隔离正式扫描成功生成
      `v0_generated_corrected` PostgreSQL dump、manifest 和 canonical snapshot，且
      manifest 记录代码/corpus/schema 身份及聚合计数。
- [ ] AC2：smoke 使用独立 PostgreSQL、临时数据目录和显式 opt-in；当前数据库、当前
      数据目录及真实音乐均未被修改，扫描前后资产树总摘要完全一致。
- [ ] AC3：首次真实扫描对所有发现输入给出持久化结果或分类诊断，无 panic、hang、
      无界读取、静默丢失或绝对路径泄露。
- [ ] AC4：第二次扫描在输入不变时不复制可见 Release/Medium/Track，来源身份、候选归属
      和 current evidence 稳定。
- [ ] AC5：V0 与当前实现基于同一 corpus 摘要完成逐项语义对照，并输出按层级、字段和
      差异类别汇总的脱敏报告；V0 corrected output 与当前已实现合同无未解释差异。
- [ ] AC6：所有当前回归都有最小回归测试和根因修复；最终完整 smoke 与相关自动化门禁
      通过，所有非回归差异都有明确处置。
- [ ] AC7：提交内容不包含真实文件清单、绝对音乐路径、媒体/CUE/封面内容、凭据、V0
      数据库/dump、临时 volume 或敏感逐项 diff；本地保留的成功 dump 权限受限且被 Git
      忽略。

## 范围外

- 修改、整理、移动、删除、转码或回写真实音乐、标签、CUE 与封面。
- 修改或迁移 historical golden；恢复 V0 的 `cmd/golden`、SQLite 运行时、GraphQL、
  Redis、Meilisearch、AI worker 或旧 PostgreSQL schema 作为当前产品依赖。V0 依赖只在
  隔离 smoke project 内临时运行，不成为当前 ROOMusic runtime。
- 使用或重置现有 ROOMusic 开发/生产数据库和数据目录。
- 为达到人工 reviewed 值而顺带实现 metadata overlay、confirm/review/revert、Release
  merge/split、外部 provider、Music Steward、播放或文件执行能力。
- 把真实资产 smoke 加入默认 `go test ./...` 或 CI。

## 风险与延期

- 当前真实资产可能与历史 UAT corpus 不同；历史数量只能用于 sanity check，不能替代
  V0 与当前实现针对同一当前 corpus 的逐项比较。
- V0 SQLite、PostgreSQL dataset 和备份当前均未找到；旧 review notes 因而不可恢复，
  但用户已确认它们不是本次 corrected behavior baseline 的必要输入。
- 全库两次扫描与摘要会产生较长只读 I/O；实施时应持续汇报进度，但不得为了提速跳过
  只读证明或重扫验收。
- V0 是历史大 runtime，镜像构建或迁移可能受工具链/依赖漂移影响；必须修复隔离运行
  适配而不改 V0 scanner 业务规则，否则基准身份失效并返回规划。

## 规划决策记录

- 路线：human selection。
- 选择者：用户。
- 已选结果：固定 V0 代码的生成结果已包含人工修正，以其针对同一当前资产重新生成的
  `v0_generated_corrected` 数据库输出作为本次权威行为基准；旧数据库只作可选审计。
- 选择依据：用户明确确认 V0 生成逻辑已经固化人工修正；关键 V0 代码与固定归档逐文件
  一致，且旧 SQLite/PostgreSQL 数据当前不可用。
- 多版本规则：若发现多个历史或重建数据库，仍由用户按非敏感盘点结果选择，不自动取
  最新版本。
- 阶段状态：方案选择已完成，实施仍需用户审核本轮最终规划摘要后另行明确批准。
