# 实施计划：真实音乐库只读 Smoke 与 V0 对照收口

## 启动前门槛

- [x] V0 基准路线已从失败 production 库截取改为 standalone scanner + SQLite；用户已在
      最新规划摘要之后明确回复“批准”。
- [x] `implement.jsonl` 与 `check.jsonl` 已包含真实规范/研究上下文，`task.py validate` 通过。
- [x] 已执行 `task.py start`，任务位于 `in_progress`。
- [x] 已记录工作树、固定 V0 归档 hash、当前 Docker project/volume 只读盘点；不得停止、
      重启、迁移或查询现有业务服务。

## 阶段 1：实现安全前置检查和可测试的 canonical 合同

- [x] 定义版本化 snapshot、稳定来源/Release/Medium/Track 键、字段映射、diff 类别和
      脱敏 report 类型；确定性排序，忽略 runtime UUID/时间但不隐式规范化业务值。
- [x] 实现真实资产树摘要的流式读取、前后比较和只输出聚合结果；详细条目仅写权限受限
      临时目录，所有错误使用安全 path hint。
- [x] 实现 baseline manifest/inventory/selection：一个候选可显式使用；多个候选只输出
      非敏感身份后停止，必须等待用户选择。
- [x] 增加 `.roomusic-smoke/` 或等价本地产物目录的 Git 忽略规则和权限检查；dump、raw
      snapshot、凭据不得被 Git 跟踪。
- [x] 用合成 snapshot/临时目录测试 UUID 无关匹配、CUE identity、字段 exact diff、
      多基准停止、corpus mutation 检出、报告脱敏和确定性输出。
- [x] 阶段门槛：相关 Go 单元测试、`go vet`、`bash -n` 通过；测试不得访问根 `music/`。

## 阶段 2：实现隔离 Smoke 编排

- [x] 新增唯一显式入口，要求 opt-in、音乐根和 V0 归档参数；拒绝危险/重叠/symlink 根，
      不加载 `.env`/`.env.dev`，生成随机 project、loopback 端口和一次性凭据。
- [x] 已为原 V0 runtime 和当前应用提供 smoke-only Compose/Docker 定义；新路线保留当前
      正式服务定义，把 V0 部分收窄为无 PostgreSQL/Redis 的 standalone exporter。
- [x] 启动前用 Compose inspect 断言 music mount `RW=false`，并验证 project/volume 名不与
      当前 `roomusic`、`roomusic-test` 或已存在资源冲突。
- [x] 当前扫描的健康检查、REST setup/login/root/scan trigger、bounded poll、超时与
      scan 终态检查已经实现；V0 runtime 路径改由 standalone exporter 成功/失败退出码取代。
- [x] 实现成功、错误、SIGINT/SIGTERM 清理测试；清理只接受脚本生成且已验证的精确
      project 名，默认删除失败产物和全部临时 volume。
- [x] 原阶段门槛已通过：shell 语法、smoke Compose config、无真实资产的 preflight 和
      合成小库隔离演练通过；现有容器/volume 状态前后相同。

## 阶段 3：建立 V0 Release Graph 基准

- [x] 执行前重新计算 V0 归档 SHA-256；不一致立即停止并报告，不改用旁边源码目录。
- [x] 新增最小 V0 adapter：只调用
      `Walk -> ParseTags/ParseCueSheet -> FileEvidence -> BuildReleaseCandidates ->
      AssembleReleaseCandidate`，输出确定排序的 corpus-relative 临时 rows；不得复制或修改
      scanner 规则，任一解析、验证或组装错误 fail closed。
- [x] 生成扫描前 corpus 总摘要，只解包到 `0700` 临时目录，将 adapter 复制到临时 `cmd/`
      并分别计算 archive/adapter hash；构建 exporter image，运行时使用 `/music:ro`、
      `/output:rw` 和 `network=none`，不启动 V0 PostgreSQL、Redis、Meilisearch 或 REST。
- [x] 已通过正式 migration/setup/login/library path/scan 路径完成 V0 全库运行并读取双重
      终态；真实结果为 REST `done`、PostgreSQL `error/failed`，且错误精确为
      `invalid_artifact_status=7`、`unresolved_quality_badge=1`。该结果只作为 production
      缺陷研究证据，不再作为基准来源或运行门禁。
- [x] 将 adapter rows 写入版本化 normalized `v0-reference.sqlite`，覆盖 manifest、Release、
      Medium、Track、物理 File、credits 和显式可比 graph evidence；验证 FK、唯一键、图
      闭合、顺序、相对路径和文本卫生，再从 SQLite 导出 canonical JSON。
- [x] 校验 SQLite 与 canonical JSON 的行数、稳定键和内容 hash round-trip；生成
      `v0_release_graph_generated_corrected` manifest，记录 `generation_mode=standalone_scanner`、
      `baseline_scope=release_graph_only`、`degraded=false` 与 excluded evidence。
- [x] 增加合成测试，覆盖 adapter mapping、解析/组装失败、路径泄漏、SQLite FK/重复/顺序
      拒绝、round-trip 漂移和确定性输出；不得保留部分 baseline。
- [x] inventory 未发现多个可用 V0 数据库；多基准停止并等待用户选择的保护分支已有
      合成测试，本轮未触发人工选择。

## 阶段 4：执行当前首次扫描与重扫

- [x] 从当前工作树构建 smoke image，连接全新隔离 PostgreSQL 18 和临时 data，配置唯一
      allowed root；通过正式 setup/session、root 注册和 scan REST 执行首次全库扫描。
- [x] 等待持久化终态，采集 Release/Medium/Track/source/diagnostic/attention 聚合和
      canonical snapshot A；任何 incomplete/failed/canceled 都不得进入“通过”比较。
- [x] 在输入不变时执行第二次完整扫描，采集 snapshot B，验证来源 identity、candidate、
      Medium 归属、current evidence 和 REST 投影稳定，无重复/可见空壳。
- [x] 通过管理员 REST 分页抽查全部或确定性样本的 list/detail/evidence/diagnostics，并与
      当前 PostgreSQL canonical 聚合一致。
- [x] 生成扫描后 corpus 总摘要；与前置摘要不一致则整轮作废，先报告资产变化，不进入
      缺陷修复。

## 阶段 5：V0/current 对照与问题收口

- [x] 比较 V0 snapshot、current A/B：先来源集合，再 Release graph 结构和图字段；V0
      standalone 不比较 local evidence、quality badge 或 diagnostics。current A/B 仍比较自身的
      decisions/evidence/diagnostics，并生成确定性 raw diff 和仅含 hash ID 的本地索引。
- [x] 将每项差异归入 `current_regression`、`schema_mapping_gap`、`capability_gap`、
      `historical_corpus_drift` 或 `intentional_contract_difference`，禁止遗留 `unclassified`。
- [x] 每个 current regression 先建立脱敏最小 fixture/数据库测试，再修复 parser、
      organizer、persistence 或 REST owner；不得增加真实资产名/hash 特例。
- [x] 每轮修复优先运行直接相关的最小测试；涉及 SQL 执行 PostgreSQL 18 集成测试，涉及
      并发/重扫执行适用的 race test，再做安全的定向 smoke。
- [x] 本轮未出现需要 overlay、AI/provider、拓扑治理、文件写入或产品合同变化的回归；
      49 项 capability gap 保持在已批准范围外，没有扩张任务权限。

## 阶段 6：最终完整复验与报告

- [x] 重新执行：前置摘要、V0 standalone graph baseline/SQLite export、从空数据库执行
      当前首次 scan、
      当前重扫、V0/current compare、REST 验证和后置摘要；不得复用修复过程中的脏数据库。
- [x] 确认当前 A/B 无幂等差异、当前已实现合同相对 V0 无未解释差异、所有非回归差异
      均有证据和处置，真实资产摘要前后一致。
- [x] 写入中文脱敏 `research/smoke-result.md`，仅记录身份 hash、聚合计数、终态、耗时、
      差异分类数量、修复/延期和实际命令；检查 Git 不包含数据库、dump 或私有清单。
- [x] 调用 `trellis-check` 做规范、只读安全、隔离、清理、映射、幂等、错误分类和跨层
      检查；修复发现的问题后重跑受影响验证。

## 计划验证命令

实施时按实际改动选择最小集合，并在最终记录中列出真实执行结果：

```bash
bash -n scripts/*.sh
docker compose -f <smoke-compose> config --quiet
(cd backend && test -z "$(gofmt -l .)")
(cd backend && go test ./cmd/roomusic-smoke ./cmd/roomusic -count=1)
(cd backend && go vet ./... && go build ./...)
./scripts/test-integration.sh
ROOMUSIC_REAL_LIBRARY_SMOKE=1 ./scripts/real-library-smoke.sh \
  --music-root <显式路径> --v0-archive <固定归档>
python3 ./.trellis/scripts/task.py validate \
  .trellis/tasks/09-03-real-library-smoke-v0-comparison
git diff --check
```

若回归修复影响范围超过上述窄集合，再补对应 backend 全量测试或 race 门禁；真实音乐
Smoke 不加入默认测试或 CI。

## 风险与回滚点

- V0 exporter image 因依赖/工具链漂移无法构建：只允许修复 smoke build 适配，不修改 scanner
  业务代码；若必须改业务代码，基准身份失效并返回规划。
- V0 standalone 生成、SQLite 校验或 round-trip 任一步失败：删除部分产物并停止，不得回退
  到 production 失败库或按历史计数拼装基准。
- corpus 前后摘要不一致：整轮结果作废，不把变化解释成 current regression。
- 多个 baseline：不覆盖、不自动选；盘点后等待用户明确选择。
- 当前修复引入 migration：只新增 forward migration，并使用空 PostgreSQL 18 验证；
  不接触现有开发库，也不执行 destructive down/reset。
- 任何清理目标无法证明属于本轮随机 project：停止清理并报告，禁止扩大目标范围。

## 完成前检查

- [x] `prd.md` 已完成 convergence pass，无阻塞问题或重复临时结论。
- [x] `design.md` 覆盖基准身份、隔离、只读证明、数据流、映射、差异和回退。
- [x] `implement.jsonl` 与 `check.jsonl` 均有真实上下文，`task.py validate` 通过。
- [x] 最新 standalone 路线规划摘要已提交用户审核，用户已在摘要之后明确批准恢复
      Phase 2 实施。

## 最终验证记录

- 后端：`go test ./... -count=1`、`go test -race ./... -count=1`、`go vet ./...`、
  `go build ./...` 均通过。
- PostgreSQL 18：`./scripts/test-integration.sh` 通过，并清理专用容器、volume 与 network。
- 前端：`npm run lint`、`npm run typecheck`、`npm run test`、`npm run build` 均通过。
- 工具与部署：`PYTHONDONTWRITEBYTECODE=1 python3 -m unittest
  scripts/v0_reference_sqlite_test.py`、`bash -n scripts/*.sh`、主/测试/Smoke 三份 Compose
  配置校验均通过；主 Compose 使用非真实占位必填值，不读取 `.env`。
- 真实验收：最终从空 PostgreSQL 执行 V0 standalone、current 首扫与重扫，得到
  current A/B 差异 0、`current_regression=0`、未知分类 0，corpus 前后摘要一致。
- 收尾：`task.py validate` 与 `git diff --check` 通过；提交前未发现数据库、dump、媒体、
  凭据、绝对真实路径或临时 Smoke Docker 资源进入 Git 变更。
