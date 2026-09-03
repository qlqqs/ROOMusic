# 实施计划：真实音乐库只读 Smoke 与 V0 对照收口

## 启动前门槛

- [ ] 用户已审核最新 `prd.md`、`design.md` 和本文件，并在该摘要之后明确批准实施。
- [ ] `implement.jsonl` 与 `check.jsonl` 已包含真实规范/研究上下文，`task.py validate` 通过。
- [ ] 执行 `task.py start` 后才允许修改产品/脚本代码或启动真实音乐扫描。
- [ ] 记录工作树、固定 V0 归档 hash、当前 Docker project/volume 只读盘点；不得停止、
      重启、迁移或查询现有业务服务。

## 阶段 1：实现安全前置检查和可测试的 canonical 合同

- [ ] 定义版本化 snapshot、稳定来源/Release/Medium/Track 键、字段映射、diff 类别和
      脱敏 report 类型；确定性排序，忽略 runtime UUID/时间但不隐式规范化业务值。
- [ ] 实现真实资产树摘要的流式读取、前后比较和只输出聚合结果；详细条目仅写权限受限
      临时目录，所有错误使用安全 path hint。
- [ ] 实现 baseline manifest/inventory/selection：一个候选可显式使用；多个候选只输出
      非敏感身份后停止，必须等待用户选择。
- [ ] 增加 `.roomusic-smoke/` 或等价本地产物目录的 Git 忽略规则和权限检查；dump、raw
      snapshot、凭据不得被 Git 跟踪。
- [ ] 用合成 snapshot/临时目录测试 UUID 无关匹配、CUE identity、字段 exact diff、
      多基准停止、corpus mutation 检出、报告脱敏和确定性输出。
- [ ] 阶段门槛：相关 Go 单元测试、`go vet`、`bash -n` 通过；测试不得访问根 `music/`。

## 阶段 2：实现隔离 Smoke 编排

- [ ] 新增唯一显式入口，要求 opt-in、音乐根和 V0 归档参数；拒绝危险/重叠/symlink 根，
      不加载 `.env`/`.env.dev`，生成随机 project、loopback 端口和一次性凭据。
- [ ] 为 V0 和当前应用提供 smoke-only Compose/Docker 定义；真实音乐仅 `:ro`，数据库、
      Redis/必要 V0 服务、data 和网络全部独立，AI/provider/定时任务关闭。
- [ ] 启动前用 Compose inspect 断言 music mount `RW=false`，并验证 project/volume 名不与
      当前 `roomusic`、`roomusic-test` 或已存在资源冲突。
- [ ] 实现健康检查、REST setup/login/root/scan trigger、bounded poll、超时与 scan 终态
      检查；当前扫描通过 `/api/v1`，V0 通过其正式 `/api` 边界。
- [ ] 实现成功、错误、SIGINT/SIGTERM 清理测试；清理只接受脚本生成且已验证的精确
      project 名，默认删除失败产物和全部临时 volume。
- [ ] 阶段门槛：shell 语法、smoke Compose config、无真实资产的 dry-run/preflight 和
      合成小库隔离演练通过；现有容器/volume 状态前后相同。

## 阶段 3：建立 V0 corrected 基准

- [ ] 执行前重新计算 V0 归档 SHA-256；不一致立即停止并报告，不改用旁边源码目录。
- [ ] 生成扫描前 corpus 总摘要，然后只解包到 `0700` 临时目录，构建固定 V0 image，
      启动独立 PostgreSQL 18、Redis 和 V0 正式运行所需最小服务。
- [ ] 通过正式 migration/setup/login/library path/scan 路径完成 V0 全库扫描并确认完整
      终态；记录安全聚合、耗时和关键 ZARD Box leaf、WEB/CD、CUE、散落文件、多碟场景。
- [ ] 通过显式表 allowlist 导出 V0 catalog dump，生成 canonical snapshot 和 manifest；
      文件设为 `0600`，计算内容 hash，不保存 token/密码或 auth/AI 空表。
- [ ] 将历史 UAT 的 110 Release、112 Medium、466 Track、466 file evidence 和 631 字段
      合同作为 sanity check；数量变化先判定当前 corpus 与历史 corpus 漂移，不修改 V0。
- [ ] 若 inventory 出现多个可用 V0 数据库，停止本阶段并向用户提供非敏感盘点，得到
      选择后才继续。

## 阶段 4：执行当前首次扫描与重扫

- [ ] 从当前工作树构建 smoke image，连接全新隔离 PostgreSQL 18 和临时 data，配置唯一
      allowed root；通过正式 setup/session、root 注册和 scan REST 执行首次全库扫描。
- [ ] 等待持久化终态，采集 Release/Medium/Track/source/diagnostic/attention 聚合和
      canonical snapshot A；任何 incomplete/failed/canceled 都不得进入“通过”比较。
- [ ] 在输入不变时执行第二次完整扫描，采集 snapshot B，验证来源 identity、candidate、
      Medium 归属、current evidence 和 REST 投影稳定，无重复/可见空壳。
- [ ] 通过管理员 REST 分页抽查全部或确定性样本的 list/detail/evidence/diagnostics，并与
      当前 PostgreSQL canonical 聚合一致。
- [ ] 生成扫描后 corpus 总摘要；与前置摘要不一致则整轮作废，先报告资产变化，不进入
      缺陷修复。

## 阶段 5：V0/current 对照与问题收口

- [ ] 比较 V0 snapshot、current A/B：先来源集合，再 Release graph 结构，最后字段、
      decisions/evidence/diagnostics；生成确定性 raw diff 和仅含 hash ID 的本地索引。
- [ ] 将每项差异归入 `current_regression`、`schema_mapping_gap`、`capability_gap`、
      `historical_corpus_drift` 或 `intentional_contract_difference`，禁止遗留 `unclassified`。
- [ ] 每个 current regression 先建立脱敏最小 fixture/数据库测试，再修复 parser、
      organizer、persistence 或 REST owner；不得增加真实资产名/hash 特例。
- [ ] 每轮修复优先运行直接相关的最小测试；涉及 SQL 执行 PostgreSQL 18 集成测试，涉及
      并发/重扫执行适用的 race test，再做安全的定向 smoke。
- [ ] 需要 overlay、AI/provider、拓扑治理、文件写入或产品合同变化时停止扩张，更新规划
      并由用户决定是否创建独立子任务。

## 阶段 6：最终完整复验与报告

- [ ] 从空临时数据库重新执行：前置摘要、V0 corrected scan/export、当前首次 scan、当前
      重扫、V0/current compare、REST 验证和后置摘要；不得复用修复过程中的脏数据库。
- [ ] 确认当前 A/B 无幂等差异、当前已实现合同相对 V0 无未解释差异、所有非回归差异
      均有证据和处置，真实资产摘要前后一致。
- [ ] 写入中文脱敏 `research/smoke-result.md`，仅记录身份 hash、聚合计数、终态、耗时、
      差异分类数量、修复/延期和实际命令；检查 Git 不包含数据库、dump 或私有清单。
- [ ] 调用 `trellis-check` 做规范、只读安全、隔离、清理、映射、幂等、错误分类和跨层
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

- V0 image 因依赖/工具链漂移无法构建：只允许修复 smoke build 适配，不修改 scanner
  业务代码；若必须改业务代码，基准身份失效并返回规划。
- V0 migration/scan 非完整终态：删除失败 dump 与临时 project，保留脱敏错误类别；不得
  以部分数据库作为基准。
- corpus 前后摘要不一致：整轮结果作废，不把变化解释成 current regression。
- 多个 baseline：不覆盖、不自动选；盘点后等待用户明确选择。
- 当前修复引入 migration：只新增 forward migration，并使用空 PostgreSQL 18 验证；
  不接触现有开发库，也不执行 destructive down/reset。
- 任何清理目标无法证明属于本轮随机 project：停止清理并报告，禁止扩大目标范围。

## 完成前检查

- [ ] `prd.md` 已完成 convergence pass，无阻塞问题或重复临时结论。
- [ ] `design.md` 覆盖基准身份、隔离、只读证明、数据流、映射、差异和回退。
- [ ] `implement.jsonl` 与 `check.jsonl` 均有真实上下文，`task.py validate` 通过。
- [ ] 最新最终规划摘要已提交用户审核；只有用户在摘要之后明确批准才运行
      `task.py start`。
