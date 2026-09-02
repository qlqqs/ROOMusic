# V0 自动整理能力恢复执行计划

## 启动前门槛

- [ ] 用户明确批准最新 PRD、技术设计和本执行计划。
- [ ] 批准后运行 `task.py start`，再按 `trellis-before-dev`/实施工作流读取相关规范。
- [ ] 记录当前数据库迁移版本、工作树和可用的 PostgreSQL 18 集成测试环境。
- [ ] 确认 parser 依赖的许可证、纯 Go、有界读取、M4A 标签与 AAC/ALAC 能力；不因
  V0 曾使用某库而直接继承。
- [ ] 不读取或扫描仓库根 `music/`；测试只使用小型合成 fixture。

## 实施原则

- 按“纯规则 -> parser -> schema/repository -> scan integration -> REST -> UI”的依赖
  顺序推进，每一阶段都有可独立回归的测试。
- 先在当前 `backend/cmd/roomusic` 过渡单体内建立窄合同，不同时进行全仓 package
  搬迁。
- 只迁移 V0 的行为、字段语义和代表性测试案例；不复制 GraphQL、asynq、搜索、AI、
  overlay 或旧数据库代码。
- 每次只运行与当阶段直接相关的最小测试；跨层收口后再运行受影响模块的完整门禁。

## 阶段 1：纯 organizer 与稳定规则

1. 定义 `SourceObservation`、`CandidateAnchor`、`FieldDecision`、`CatalogCandidate`、
   Release/Medium/Track/Credit 等内部类型和枚举。
2. 实现不依赖文件系统/数据库的规范化、来源优先级、多数决、稳定 tie-break 和
   attention reason 生成。
3. 实现普通目录、same-dir split、loose album/unknown、严格多碟、Box leaf 归组。
4. 实现一 Release 一初始 ReleaseGroup、Medium/Track 稳定排序和 credits 确定性拆分。
5. 从 V0 行为重新制作 table-driven cases，覆盖遍历顺序置换、冲突、误合并和缺字段；
   不复制依赖旧模型的大型实现。

阶段门槛：纯 Go 单元测试证明相同 observations 的任意输入顺序产生完全相同候选、
decisions 和 attention。

## 阶段 2：parser、M4A、CUE 与必要 log

1. 把现有 FLAC/MP3/OGG/Opus/WAV parser 适配到统一 `SourceObservation`，保持已通过
   的解析与 filename fallback 行为。
2. 引入通过启动前评估的 M4A adapter，覆盖 tag、AAC/ALAC、duration、sample rate、
   channels、bitrate 和可得 bit depth；缺失事实保持空。
3. 拆出 CUE parser，支持多 `FILE`、固定编码策略、sheet/track 字段、CATALOG、ISRC、
   INDEX offset/duration 和每个引用的 containment。
4. 实现整轨、真实分轨和多文件 CUE 的显式来源关系与去重规则。
5. 增加窄 EAC/XLD log parser，只输出明确 source/media 证据和诊断。
6. 使用 Go test builder 或合法的小型 fixture 覆盖格式头、标签、CJK CUE、越界引用、
   损坏输入和超长字段；禁止使用真实用户资产。

阶段门槛：parser 单元/fixture 测试通过，fuzz 或表驱动 malformed case 不 panic、不
越界读取、不泄露绝对路径。

## 阶段 3：迁移与 Catalog persistence

1. 新增连续编号 migration，增加 scan staging、candidate anchor、Release/Medium/
   Track 字段、credits、label/genre、field decisions 和 grouping evidence。
2. backfill legacy Release candidate anchor、Track source identity 和现有 display 值；
   迁移后 legacy catalog 在未重扫时仍可查询。
3. 在 backfill 完成后替换 `releases_source_directory` 唯一索引；增加 candidate、
   Medium position、物理/CUE source identity、decision 枚举和 FK 约束。
4. 实现 scan staging 的有界批写、按 scope 流式读取和终态/恢复清理。
5. 实现 candidate 短事务 upsert：复用 Track ID、原子重挂 Medium、替换当前 decisions/
   credits/关系，并把空父实体留作诊断但从普通查询隐藏。
6. 适配 artwork，使其在 candidate 确定后绑定正确 Release，不再按直属目录猜测。
7. 增加 PostgreSQL 18 集成测试：fresh migration、legacy backfill、约束、事务回滚、
   candidate 幂等、source 移动归属、CUE identity、空壳隐藏和 staging 清理。

回退点：migration 不提供自动 down；运行前保留数据库备份。若 repository 阶段失败，
不得让旧 scanner 在已允许同目录多候选的 schema 上继续写入，使用 forward-fix 后再
恢复扫描。

## 阶段 4：扫描协调集成与重扫

1. 将 `scanRoot` 从“发现即写 Release”改为“发现/解析 -> staging -> organizer ->
   candidate store”，删除旧直属目录归组的策略所有权。
2. 保留 active roots、advisory lock、持久取消轮询、应用生命周期 context、异常恢复
   和现有 scan terminal state。
3. 确保 parser/catalog/staging 失败形成 bounded diagnostic 并使 run incomplete；有效
   候选仍可提交。
4. 只在全局 `succeeded` 的 finalize 事务执行 missing；failed/canceled/incomplete/
   offline/permission 均不执行负向对账。
5. 验证标签、碟号、single/split 和 leaf/multidisc 变化后 Track ID 稳定、当前归属更新，
   普通查询没有仍可见的旧空壳或重复 CUE 曲目。
6. 保持扫描中候选级增量可见，并在测试中明确 incomplete 后未访问旧来源仍为 present。

阶段门槛：scanner 单元和 PostgreSQL 集成测试覆盖成功、部分失败、取消、重复扫描、
候选形状变化和 complete-only missing。

## 阶段 5：REST 查询与权限

1. 扩展 Release list query/presenter：present-only、稳定分页/搜索、核心摘要、数量、
   `attention_count` 和 `attention=required`。
2. 扩展 Release detail 的 Medium/Track、audio facts、credits、label/genre 和安全 decision
   summary；使用 bounded query/batch，避免 N+1。
3. 增加管理员 evidence endpoint；验证管理员角色，限制 candidates/source refs 数量，
   只返回安全相对标识。
4. 扩展 scan status 的聚合计数并复用现有 diagnostics endpoint；保持错误 envelope、
   request ID 和现有 scan wire 枚举。
5. 增加 handler/query 测试：筛选、分页、malformed input、普通用户/管理员差异、无路径
   泄露、legacy evidence 缺失和数据库错误。

阶段门槛：REST DTO 与数据库投影逐字段对齐，普通用户无法读取管理员 evidence，旧
必填字段和 endpoint 路径保持兼容。

## 阶段 6：前端最小闭环

1. 在 `frontend/src/api.ts` 扩展 DTO 和 runtime decoders，枚举与 nullable/optional
   语义保持穷尽；不使用 raw cast。
2. Release 列表增加 attention badge 和 URL 驱动的 `attention=required` 筛选，保留
   loading/empty/error/retry 状态。
3. Release 详情显示核心字段、Medium/Track、credits/audio facts 和安全 evidence；
   管理员按需读取详细冲突 evidence。
4. 管理区接入 scan diagnostics 与聚合计数，终态明确区分 succeeded、incomplete、
   failed、canceled。
5. 所有问题入口只读，统一提示修改外部源后重扫；搜索 UI 中不得出现 metadata 编辑、
   confirm、merge/split 或文件操作控件。
6. 仅在组件职责确实分离时从当前 `main.tsx` 提取小组件；不引入状态库、缓存库或 UI
   框架。

阶段门槛：decoder 和组件行为测试覆盖正常/不确定/硬诊断/权限/空状态，键盘与窄屏
基本可用，普通 UI 不显示敏感来源路径。

## 阶段 7：文档、跨层回归与审查

1. 更新当前 schema、scan reconciliation、REST 和相关 Trellis spec，记录 organizer、
   candidate identity、evidence、CUE 和增量可见合同。
2. 对 PRD 的 AC1--AC12 建立测试映射；确认每条都有纯规则、adapter、数据库、REST 或
   UI 中最靠近责任边界的证据。
3. 运行受影响模块完整门禁、迁移集成门禁和 race 检查；构建前端生成资产并确认没有
   未提交漂移。
4. 使用 `trellis-check` 做规范、跨层数据流、权限、只读安全、重扫和未来 seam 审查。
5. 只提交本任务相关文件；完成实现、检查、规范更新和提交后再归档任务。

## 计划验证命令

开发中的最小测试按具体 package/test name 运行。跨层收口至少执行：

```bash
cd backend && gofmt -l .
cd backend && go test ./cmd/roomusic -count=1
cd backend && go vet ./...
cd backend && go build ./...
cd frontend && npm run lint
cd frontend && npm run typecheck
cd frontend && npm run test
```

迁移、并发和生产投影相关变更追加：

```bash
./scripts/test-integration.sh
cd backend && go test -race ./cmd/roomusic -count=1
cd frontend && npm run build
python3 ./.trellis/scripts/task.py validate .trellis/tasks/09-02-album-scan-organization-core
git diff --check
```

若本地 PostgreSQL 集成环境不可用，不能把普通测试中的 skip 当作通过；最终验收前必须
通过 `./scripts/test-integration.sh` 或等价 PostgreSQL 18 证据。不会运行或扫描真实
`music/`。

## 风险与控制

- **规则迁移漂移**：先用 V0 行为 case 锁定输入输出，再写 adapter；不逐文件复制旧
  runtime。
- **大库内存/事务**：scan staging 有界批写、按 scope 读取、candidate 短事务；测试
  staging 清理和取消。
- **候选 identity 变化**：普通目录 anchor 不含 metadata；实质 split/merge 允许新
  Release，但 Track ID 必须复用且旧空壳隐藏。
- **CUE 误去重**：只依据显式 CUE 父子关系，同候选处理；禁止标题/时长模糊删除。
- **迁移回退**：先备份，migration 只前进；同目录多候选写入后不回滚旧二进制。
- **evidence 膨胀/泄露**：current decision replace、candidate 数量上限、相对标识和
  role-specific DTO；不存 raw dump/绝对路径。
- **范围回涨**：发现 overlay、人工 confirm、topology、AI、搜索引擎或额外格式需求时
  建后续任务，不在本任务顺带实现。

## 启动前最终检查

- [ ] `prd.md` 没有阻塞问题，目标、范围外和 AC 均明确。
- [ ] `design.md` 覆盖模块、identity、事务、迁移、权限、回退和后续 seam。
- [ ] `implement.jsonl`、`check.jsonl` 含真实 spec/research 上下文并通过 validate。
- [ ] 最新最终规划摘要已呈现，且用户在其后的消息中明确批准实现。
