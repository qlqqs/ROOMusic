# 修复专辑扫描解析与候选闭环

## Goal

恢复一个可验证的只读扫描闭环：真实的 AAC/ALAC M4A、单/多文件 CUE、普通与
多碟目录、根目录散落文件都能被解析为有稳定来源身份的 Release Graph；重扫不
复制或遗留可见空壳；REST 和前端能展示结果、音频事实、最小证据和需要检查的
状态。所有源音乐、标签、CUE 和目录保持只读。

## 背景与已确认事实

- 当前工作树在 `feat/album-scan-organization-core`，最近提交把占位的 organizer、
  M4A、CUE、候选持久化和 evidence 接到了现有 Core 0，但父任务已归档且 AC1--AC12
  未真正验收。
- `backend/cmd/roomusic/audio_parser.go:43-60` 读取整个 M4A，并将大于 16 MiB 的
  文件报为 `M4A metadata too large`；`parseM4AAtoms` 从 `data[8:]` 搜索容器，且
  UTF-8 `©nam` 不匹配标准 atom 的单字节 `0xA9nam`。真实资产只读复现中，一个
  ALAC 专辑的 10/10 文件失败，资产统计显示 84 个 M4A 中 72 个超过 16 MiB。
- `backend/cmd/roomusic/scanner.go:251-291` 的 CUE 路径只使用第一个 `FILE`，丢弃
  `ReferencedFile`、`IndexFrames`、时长和音频事实；未持久化 CATALOG、ISRC、sheet
  元数据，也没有按每个引用做 containment 和真实分轨去重。
- `backend/cmd/roomusic/organizer.go:121-243` 在同目录冲突时只修改 candidate kind，
  不会按 album/album artist 拆 observation；平票不拆候选。scanner 在
  `scanner.go:311` 没有填充 `AlbumArtist`，`audio_parser.go:623` 会把目录名回退成
  Album，根目录无标签文件可能被错误合并。
- `backend/cmd/roomusic/scanner.go:315-328,343-361,364-446` 在 candidate 确定前
  保存封面、任一解析失败便阻止整棵 root 的有效 observations 提交、重复追加
  `release_grouping_evidence`，也不清理 candidate 变化留下的旧 Medium/Release
  空壳；扫描结果仍可能在内存 slice 中无界增长。
- `backend/cmd/roomusic/application.go:343-364,470-520` 的列表/详情缺少完整
  attention 语义和管理员 evidence endpoint；普通用户的详情 evidence 被脱敏后仍
  返回 200；`frontend/src/main.tsx:110-125,315-318` 固定第一页，未提供
  `attention=required`、诊断聚合和完整 evidence 视图；`frontend/src/api.ts:187`
  只校验非空字符串，未约束 confidence/action 枚举。
- `backend/migrations/0010_album_organization.sql` 没有 current grouping evidence
  的唯一/替换约束，也没有完整的 cue source、candidate 生命周期或 staging 结构。
- 已有 Core 0 合同要求 PostgreSQL 是唯一业务 authority、版本化 REST、只读音乐根、
  只有 succeeded 扫描执行 missing、后端拥有权限和路径安全。`../ROOMusic-V0` 只
  用于抽取已验证的字段语义和行为样例，不作为依赖或运行时模板。

## 需求

### R1. 统一来源观察与真实格式解析

1. 以统一的 typed observation 作为 parser -> organizer 边界，至少携带安全相对路径、
   album/album artist/artist/title、track/disc、source kind、duration、codec、
   sample rate、channels、bitrate、CUE 父来源/引用文件/index/start/end/ISRC、
   artwork 引用和来源/推断标记。
2. M4A 必须使用有界、可流式的 ISO-BMFF atom 读取；不因媒体 payload 大而拒绝，能
   读取标准 raw `0xA9nam`/`0xA9ART`/`0xA9alb` 等标签、AAC/ALAC codec、duration、
   sample rate、channels 和可得 bitrate/bit depth。损坏或越界 atom 返回分类诊断，
   不 panic、不读取整个文件到内存。
3. CUE 支持 UTF-8、UTF-8 BOM、UTF-16 BOM 及 GBK、Shift-JIS、Big5 等当前真实资产
   所需编码；支持多 `FILE`、sheet/track TITLE/PERFORMER、REM date/genre、CATALOG、
   ISRC、INDEX 01/后续 INDEX。每个引用文件单独校验相对 containment；越界、缺失或
   不安全引用只产生 bounded diagnostic。
4. 保留 FLAC、MP3、OGG/Vorbis、Opus、WAV 的已有行为；不把音频规格反推 source/media
   或发行语义，缺失字段保持空。EAC/XLD log 只输出明确的 CD source/media 证据和
   诊断，不恢复质量 badge 平台。

### R2. 确定性 candidate organizer

1. tag 优先，folder 只补缺，filename 仅用于明确的 title/artist fallback；每个决定
   保存 source、confidence、action、rule id、有限 candidates/reason。
2. 普通目录、多碟目录、Box leaf、同目录 split、根目录 loose album 和 loose
   unknown 都由 organizer 产生稳定 candidate anchor。anchor 不包含会随重扫变化的
   title；同一输入任意遍历顺序必须得到相同 candidate、Medium、Track、decision。
3. 同候选的小比例 album/album artist 冲突使用稳定多数决并保留少数派 evidence；无
   明显多数或明确冲突时按 album + album artist 拆 candidate，平票使用字典序稳定
   规则。多碟只有在 Disc/CD/Disk 结构、相同 album 证据、碟号从 1 连续且不重复时
   合并；Box 父目录不制造假 Release。
4. 无 album evidence 的散落文件各自形成“未知专辑”候选；禁止按弱标题、艺术家、
   年份或目录相似度跨目录 hard merge。每个本地 Release 初始拥有独立 ReleaseGroup。
5. CUE 整轨生成虚拟 Track；CUE 与明确真实分轨共存时仅在同一候选和明确父来源关系
   内优先真实分轨，保留 CUE evidence，不按标题/时长模糊删除；多 `FILE` 不得全局
   过滤真实分轨。

### R3. 候选持久化、封面和重扫生命周期

1. 扫描发现/解析、bounded staging、organizer 和 candidate 短事务写入分离；解析失败
   不阻止同一 root 其它有效候选提交，但 run 标记 incomplete 并记录诊断。
2. 按稳定 anchor upsert Release/ReleaseGroup/Medium；按 `root + normalized relative
   path` 复用物理 Track ID，按 CUE + 父来源 + track/index 复用虚拟 Track ID。candidate
   变化时更新当前归属，旧无 present Track 的父实体从普通列表隐藏但保留供诊断。
3. 每次重扫替换当前 field decisions、grouping evidence、credits 和 CUE 关系，不能
   无限追加同一 Release 的 evidence。数据库约束必须表达 anchor、medium position、
   source identity 和 decision 枚举的唯一性/合法性。
4. candidate 确定且 Release ID 已知后才绑定封面；封面关系、hash、MIME、尺寸和受管
   存储 key 一致，不留下由失败 candidate 产生的孤立记录或文件。
5. 只有最终状态 `succeeded` 执行 missing 对账；failed、canceled、incomplete、root
   offline 或权限错误都保持未访问来源 present。取消、异常恢复、advisory lock 和
   增量可见语义不回归。

### R4. REST 与只读前端闭环

1. Release 列表只展示至少一个 present Track，提供稳定分页/搜索、year、source/media、
   Medium/Track 数和由 `uncertain_apply`、grouping inconsistency、可见 missing
   来源去重得到的 `attention_count`；支持 allowlisted `attention=required`。
2. Release 详情返回核心本地字段、Medium/Track、duration/codec/sample rate/channels/
   bitrate、credits、封面和不泄露绝对路径的 evidence summary。管理员专用
   `GET /api/v1/releases/{id}/evidence` 才返回 bounded candidates、reason code 和
   安全相对来源标识；普通用户访问返回 403。
3. scan status/diagnostics 增加 bounded 聚合计数并保留 succeeded/incomplete/failed/
   canceled、轮询、取消、错误 envelope、request id 和权限合同。
4. 前端 API boundary 对所有新增 DTO 严格 runtime decode，枚举和 nullable 语义穷尽；
   列表把 `attention=required` 放入 URL，详情和管理员区展示 evidence/diagnostics
   及可恢复空、错误、权限状态。UI 只提供“修正外部源文件或标签后重扫”的提示，不
   提供 metadata 编辑、confirm、merge/split 或文件操作控件。

### R5. 回归与安全

1. 新增合成小型 fixture 和 table-driven malformed tests，禁止自动化测试扫描或写入
   根目录 `music/`；真实资产只能显式、只读 smoke，且不作为 CI 通过条件。
2. 后端、数据库、REST、前端测试分别覆盖代表性成功路径和危险失败路径；测试结果
   能映射到 AC1--AC12，不以 go test 全绿替代行为验收。
3. 不引入 Redis、Meilisearch、GraphQL、V0 SQLite、运行时 Agent、文件变更或新的
   metadata 治理/overlay 生命周期。

## 验收标准

- [x] AC1：普通单碟合成 fixture 从 tag/folder 证据生成 1 个 ReleaseGroup、1 个
      Release、1 个 Medium 和稳定排序的 Tracks。
- [x] AC2：严格的 Disc 1/Disc 2 合并为同一 Release 的两个 Medium；不连续碟号、
      album 冲突和 Box 父目录保持 leaf candidates。
- [x] AC3：同目录少数冲突产生确定性多数决和 attention；无明显多数拆成稳定候选；
      打乱 observation 顺序不改变结果。
- [x] AC4：根目录/散落文件按明确 album evidence 归组；无 album 的单文件各自形成
      “未知专辑”；弱相似不跨目录合并。
- [x] AC5：单/多文件、UTF/CJK CUE 形成虚拟 Track，保存父来源、INDEX offset/duration、
      CATALOG/ISRC；真实分轨共存无重复，越界引用产生诊断。
- [x] AC6：FLAC、MP3、OGG、Opus、WAV、M4A(AAC/ALAC) 的小型合成 fixture 可解析；
      大于 16 MiB 且 atom 位于文件后部的 M4A 不被误判，真实 `music/` 不进入自动测试。
- [x] AC7：tag/folder/log/audio 字段优先级符合 R1/R2；音频事实不推断发行语义，
      缺失字段不伪造。
- [x] AC8：Release/Track 核心字段、credits、音频事实和最小 evidence 按 R1--R3
      持久化；高置信为 `auto_apply`，中低置信为 `uncertain_apply`。
- [x] AC9：重复扫描不复制 present Release/Medium/Track；标签、碟号或 candidate 变化
      更新当前图谱；失败/取消/incomplete 不执行 missing，旧空壳不出现在普通列表。
- [x] AC10：列表、详情、`attention=required`、管理员 evidence 和 diagnostics 从
      REST 到前端严格解码并可浏览；普通用户读取管理员 evidence 得到 403 且无路径泄露。
- [x] AC11：界面无 metadata/归组写入入口，所有音乐目录及封面来源访问保持只读。
- [x] AC12：身份、目录 containment、symlink 防逃逸、扫描协调/取消、封面资源、生产
      静态资源和既有 REST 必填字段不回归。

## 范围外

- 数据库内人工 metadata 编辑、confirm/overlay/pin/review/revert、issue lifecycle。
- Release/ReleaseGroup merge/split、Track 人工重挂接、跨目录版本治理和全库原子
  generation/snapshot。
- Music Steward、模型/外部 provider、AI ledger/memory、Redis/asynq、Meilisearch、
  GraphQL、播放、转码和文件/目录改名移动复制删除。
- APE、DSD/DSF/DFF、WMA、AIFF、WavPack、MKA 等未被当前真实资产事实要求的 parser
  扩展，以及完整 rip quality badge、歌词、track artwork、外部封面和 V0 全量 evidence。

## 关键决策与风险

- 保留当前 Core 0 模块化单体，在现有 package 内建立窄的 observation/organizer/
  candidate store 合同；不复制 V0 运行时、依赖或数据库。
- M4A 采用标准库有界 atom reader，避免按媒体总大小读取；如实现阶段发现某个
  标签变体需要第三方库，必须先验证许可证、内存上界和行为 fixture，再单独记录决定。
- 使用 scan staging + 候选短事务控制大目录内存和数据库锁持有时间；staging 清理与
  取消是显式生命周期。
- candidate anchor 稳定但允许明确 split/merge 产生新 Release；Track 来源身份优先，
  旧无 present 父实体保留诊断而不污染浏览。
- schema 只新增 forward migration；迁移前备份，失败采用 forward-fix，不提供破坏性
  down migration。

## 规划决策记录

- 路线：human selection。
- 选择者：用户（2026-09-03 明确回复“批准实施”）。
- 选定结果：批准本次最终规划摘要并进入实现阶段；任务已通过 `task.py start` 切换为
  `in_progress`。
- 理由：本任务覆盖多个相互依赖的跨层缺陷，必须先锁定可观察验收和安全边界，避免
  再次用编译/门禁通过替代真实行为完成。

## Open Questions

无阻塞的产品问题。第三方 M4A 依赖、staging 批大小和 SQL 查询细节属于实现阶段的
技术验证，不能改变本 PRD 的可观察行为；若验证结果影响行为，将回到规划阶段更新
本文件后再实现。
