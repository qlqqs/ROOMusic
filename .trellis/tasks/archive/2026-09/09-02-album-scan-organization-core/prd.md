# V0 自动整理能力恢复

## 目标与用户价值

在当前 ROOMusic 的只读扫描、安全协调、PostgreSQL 与 REST 骨架上，恢复 V0 已验证的自动整理母线。一次扫描应把本地音频、标签、目录名、CUE 和必要的 rip log 证据稳定整理为可直接浏览的 `ReleaseGroup -> Release -> Medium -> Track` 图谱，而不是仅登记文件或按直属目录机械建专辑。

系统采用 coverage-first：能够形成可解释默认值时直接整理；中低置信结果也进入当前图谱，但明确标记为 `uncertain_apply`。用户可以查看结果及最小证据，不能在本任务中编辑数据库 metadata、改变图谱拓扑或修改真实音乐文件。

## 当前基础与约束

- 当前版已经具备持久化扫描状态、PostgreSQL 跨进程单任务、取消、异常恢复、只读路径 containment、仅完整成功扫描执行 `missing` 对账、最小 Release Graph、封面和基础浏览。
- 当前 scanner 仍在遍历单文件时直接写 catalog，并以直属目录作为唯一 Release 锚点；这不能表达 V0 的候选归组、多碟、Box、散落文件、字段多数决或 CUE 去重。
- 当前架构是模块化单体：Library/Scanner 拥有文件发现和 parser，Catalog 拥有归组、Release Graph 与 provenance，PostgreSQL 是唯一必需业务 authority，产品 API 使用版本化 REST。
- `../ROOMusic-V0` 是行为与字段语义的历史证据，不是运行时依赖。当前架构、安全和已经落地的扫描协调合同优先。
- 真实音乐资产当前已记录包含 `.flac`、`.m4a`、CUE 和封面；自动化测试不得依赖或扫描真实 `music/`。
- “整理”不改名、移动、复制或删除文件，不回写音频标签或 CUE。

## 最合适的恢复范围

自动整理、最小当前 evidence 与嵌入现有页面的只读 attention 入口共同构成一个 MVP，
不再删除其中任一环。继续裁掉 evidence 或可见入口，会让 coverage-first 应用的中低
置信结果无法解释和验收；范围控制改为删除完整确认/修正/issue 治理与独立工作台。

### R1. 自动整理流水线

建立清晰且可独立测试的数据流：

```text
只读遍历
  -> parser observations
  -> Release candidate grouping
  -> coverage-first field decisions
  -> Release Graph assembly
  -> candidate transaction persistence
  -> REST read projection
```

- 遍历/parser 只产出规范化来源观察和原始字段证据。
- Catalog organizer 独占候选归组、字段决定、Medium/Track 形状和保守合并规则。
- PostgreSQL adapter 独占 SQL、短事务、候选 upsert 与重扫对账。
- 保留现有 scan run、advisory lock、取消、异常恢复和 complete-success-only missing 合同，不恢复 V0 的 asynq、lease/generation/fencing 运行时。

### R2. 本次输入覆盖

- 保留现有 FLAC、MP3、OGG/Vorbis、Opus、WAV。
- 增加真实资产需要的 M4A 容器，识别 AAC/ALAC codec，并读取可用标签与音频事实。
- 恢复 V0 的多 `FILE` CUE、UTF-8/UTF-16 及常见 CJK 编码（至少 GBK、Shift-JIS、Big5）、sheet/track 标题与表演者、REM date/genre、CATALOG、ISRC、INDEX offset/duration 和引用路径安全。
- 识别必要的 EAC/XLD rip log 证据，仅用于明确的 CD source/media 判断和诊断；不恢复完整质量 badge 产品。
- APE、DSD/DSF/DFF、WMA、AIFF、WavPack、MKA、独立 AAC 等其余 V0 格式作为后续 parser adapter 扩展，不阻塞本次 organizer 架构。

### R3. V0 核心整理规则

- tag 是首选来源；folder name 只补缺，不覆盖已有 tag。
- 支持 V0 已验证的 DIC、JP-PT、EAC、WEB-DL 和简单目录模式；目录解析结果必须携带规则来源。
- 同一候选中的少量 album/album artist 冲突采用确定性多数决，保留少数派；没有明显多数的明确 album 分歧才拆成多个候选。平票使用稳定排序，结果不依赖遍历顺序。
- 多碟合并必须同时满足 Disc/CD/Disk 结构、相同 album 证据和从 1 连续且不重复的碟号；证据不足时保留 leaf releases。
- Box/collection 父目录不制造假 Release；leaf releases 保持独立，并保存安全的 parent collection evidence。
- 根目录或目录内散落文件有 album evidence 时按 album/album artist 归组；无 album evidence 的单文件形成独立“未知专辑”Release，并从文件名进行确定性标题/艺术家兜底。
- 每个本地 Release 初始拥有独立 ReleaseGroup；不按标题、艺术家、年份或目录相似度跨目录 hard merge。
- CUE 整轨生成虚拟 Track；CUE 与真实分轨共存时优先真实分轨，CUE 保留为证据而不重复展示；多文件 CUE 不得误删全部真实分轨。
- source/media/release type、label、catalog、barcode、edition 等只从明确 tag、folder 或 rip-log 证据获得；音频规格不得反推发行来源、版本或厂牌。
- 艺术家 credit 可以按 V0 的确定性分隔规则 coverage-first 结构化；原始 display credit 必须保留，固定组合或歧义分隔使用 `uncertain_apply`，不做跨语言身份合并。
- 缺失字段保持空；除“未知专辑”等明确业务兜底外，不为完整观感凭空补值。

### R4. 本地图谱与字段语义

恢复自动整理真正使用、且后续 metadata/AI/playback 会消费的本地语义：

- Release：title、album artist、date/year、release type、source/media type、provider、edition、label、catalog number、barcode、parent collection、candidate kind。
- Medium：position、title、format、track count。
- Track：title、position、source kind、CUE parent/index/start/end、duration、codec、bit depth、sample rate、channels、bitrate、Hi-Res 标记、ISRC。
- Credit：release/track 的 album artist、performer、composer、conductor、producer 等本地结构化 credit，并保留 credited/display name。
- Genre/label 使用本地规范化关系或等价结构化投影，但不实现 MusicBrainz identity、跨语言 alias 合并或实体治理。
- 本地 Release/Track 使用当前 UUID；物理 Track 来源继续以 library root + 规范化相对路径作为稳定身份。CUE 虚拟 Track 使用包含 CUE、父来源和 track index/offset 的确定性来源身份。
- 同一普通目录重扫后 metadata 变化应更新既有本地 Release，不因 title 改变复制 Release；同目录明确拆候选、根目录 album 归组和 loose single 使用可解释的稳定 candidate anchor。

不恢复 V0 的 `mb_id`、`enrichment_status`、`source_priority`、任意 `custom_fields` 或为未来 provider 预建的空模型；这些在真实 provider/overlay 任务中添加。

### R5. 最小 evidence 与问题语义

字段决定只保存当前整理和后续扩展确实需要的最小合同：

- entity/field、selected value、selected source；
- `confidence=high|medium|low`；
- `action=auto_apply|uncertain_apply`；
- 稳定 `rule_id`；
- 仅冲突或不确定时保存必要 raw candidates 和 reason code；
- 关联最新 scan run 与观察时间。

归组证据保存 candidate kind、参与来源的安全相对标识、结构/tag 依据、parent collection 和 inconsistency reason。禁止保存完整 raw tag dump、绝对路径或为 AI 预构造的大 payload。

本任务不实现 `confirmation_status` 状态机、持久化 correction path、独立 issue 表或 open/resolved/ignored 生命周期：

- 当前 catalog 待检查项从 `uncertain_apply`、grouping inconsistency 和 missing 状态派生。
- parse/format/permission/CUE safety/catalog write 等硬错误继续使用按 scan run 保存的 bounded diagnostics。
- `uncertain_apply` 不使扫描失败；无法读取或无法安全处理的来源才进入硬诊断。
- 统一用户提示是“修正外部源文件或标签后重扫”，不提供 ROOMusic 内 metadata 写入。

### R6. 重扫与可见性

- 候选按稳定顺序在短事务中 upsert；遍历/解析期间不持有长事务。
- 同路径 Track 保持 ID；标签、碟号或候选变化时更新字段与归属，不留下仍可见的旧 Medium/Release 空壳。
- 完整成功扫描后才把未观察来源标为 `missing`；失败、取消、离线、权限错误或不完整扫描不得执行负向对账。
- Release 列表默认只显示至少有一个 present Track 的当前图谱；历史 missing/空壳数据可供管理员诊断但不污染普通浏览。
- 修正外部文件后重扫应替换当前派生字段决定和归组结果；本任务没有 overlay 优先级问题。

### R7. REST 与前端最小闭环

- 扩展 Release 列表：title、album artist、year、source/media、Medium/Track 数量、attention count，并支持稳定分页和一个 `attention=required` 筛选。
- 扩展 Release 详情：完整 Medium/Track 形状、核心本地字段、音频事实、credits 与安全 evidence summary。
- 管理员可以查看冲突候选、安全相对来源和 scan diagnostics；普通用户只能看到不泄露路径的值、来源类别和不确定标记。
- 不建设独立大型问题工作台。现有 Release 列表提供“需要检查”入口，Release 详情展示 evidence 摘要，管理员扫描区展示硬诊断与聚合计数。
- 保留现有扫描启动、刷新后恢复、轮询、取消和终态展示；不恢复 V0 的逐目录状态、扫描历史中心或实时推送。
- 所有 REST payload 在前端 API 边界严格解码；不引入 GraphQL、状态库、query/cache 或新的 UI 框架。

## 明确范围外

- 数据库内人工 metadata 编辑、confirm、overlay、pin、review、revert 和操作历史。
- Track 人工重挂接、Release/ReleaseGroup merge/split、跨目录版本归组和其他 topology governance。
- Music Steward、模型调用、Review Subagent、AI ledger/memory、外部 metadata provider。
- 文件/目录改名、移动、复制、删除，音频标签/CUE 回写及任意 shell/SQL 工具。
- Redis/asynq、Meilisearch、GraphQL、动态插件运行时。
- 完整 rip quality badge、歌词/歌词冲突、track artwork、外部封面和 V0 local-evidence 全套表。
- V0 golden SQLite、真实 `music/` CI、全库 raw tag 保存。
- 详细逐目录进度、全扫描历史工作台、跨扫描人工 issue 生命周期。
- APE/DSD/WMA/AIFF/WavPack/MKA 等未被当前真实资产事实要求的 parser 扩展。

## 验收标准

- [ ] AC1：普通单碟专辑从标签和目录证据生成一个 ReleaseGroup、一个 Release、一个 Medium 和稳定排序的 Tracks。
- [ ] AC2：严格证据成立的 Disc 1/Disc 2 生成一个 Release 的两个 Medium；不连续、album 冲突或 Box leaf 不被错误合并。
- [ ] AC3：同目录小比例冲突使用确定性多数决并产生 attention；无明显多数时稳定拆候选；改变遍历顺序不改变结果。
- [ ] AC4：根目录/目录散落文件按 album evidence 归组，无 album 的单文件形成“未知专辑”；弱相似不跨目录 hard merge。
- [ ] AC5：单/多文件、UTF/常见 CJK CUE 正确形成虚拟 Track、父来源、offset/duration、CATALOG/ISRC；与真实分轨共存时无重复曲目，越界引用被拒绝并诊断。
- [ ] AC6：FLAC、MP3、OGG、Opus、WAV、M4A(AAC/ALAC) 的小型合成 fixture 可解析；真实 `music/` 不进入自动化测试。
- [ ] AC7：tag/folder/log/audio 的字段优先级符合 R3；音频规格不反推发行语义，缺失字段不伪造。
- [ ] AC8：Release/Track 核心字段、credits、音频事实与最小 evidence 按 R4/R5 持久化；高置信为 `auto_apply`，中低置信为 `uncertain_apply`。
- [ ] AC9：重复扫描不复制 present Release/Medium/Track；标签、碟号、候选变化更新当前图谱；失败/取消/incomplete 不执行 missing。
- [ ] AC10：Release 列表、详情和 `attention=required` 从 REST 到前端严格解码并可浏览；管理员能查看安全证据/诊断，普通用户看不到敏感路径。
- [ ] AC11：界面没有 metadata/归组写入入口；所有音乐目录访问保持只读。
- [ ] AC12：现有身份、目录安全、扫描协调、取消、封面和生产静态资源合同不回归。

## 风险与后续扩展

- V0 规则不能整包复制；需按当前安全边界、相对路径、REST 和 PostgreSQL schema 重新实现并用行为 fixture 固化。
- 当前过渡单体会在 organizer 引入第二个明确策略所有者，允许只拆出真实的 Library parser/Catalog organizer/persistence 边界；不进行全仓目录重构。
- 将来 metadata overlay 应以稳定 entity ID + field key 覆盖 scanner base decision，并通过 Operations 处理 revision/idempotency/revert；本任务不提前实现。
- 将来 AI/外部 provider 只消费安全 evidence projection并产生 candidate/overlay，不直接改 scanner base truth。
- 将来 topology governance 使用本任务保存的 candidate/grouping evidence，但必须独立设计影响范围、revision 与逆操作。
- 将来 search 可索引当前 REST/catalog projection；PostgreSQL 继续是 authority。
- 将来 playback 可消费 Track 的物理/CUE source 字段；本任务不实现流媒体接口。

## 规划决策记录

- 路线：human selection。
- 选择者：用户于 2026-09-02 明确回复“批准”。
- 最终候选：恢复 V0 自动整理行为、本地核心字段/关系、实际资产格式、最小 evidence 和派生只读问题入口；不恢复治理、AI、文件操作或 V0 运行时。
- 选择结果：批准上述最终候选进入实现。
- 理由：用户认可这是当前架构内最小但不残缺的整理闭环；它同时为 overlay、AI、search、topology 与 playback 保留稳定的实体、字段和证据接口，而不为尚未实现的能力预建平台。
