# 技术设计：真实音乐库只读 Smoke 与 V0 生成基准对照

## 1. 基准定义

本任务不再等待历史 `golden.sqlite`。用户已确认固定 V0 代码的生成结果已经包含此前
人工修正，因此基准定义为：

```text
V0 代码身份 + 当前 corpus 身份 + V0 production scanner 输出
  = v0_generated_corrected
```

- V0 代码身份固定为 `ROOMusic-migration.tar.gz`，执行前核对 SHA-256
  `fe25388328698b26991ea3b59a14406a155eb92d578a9be2a68d67d331ecf97d`。
- 归档只解包到权限受限的临时目录，不修改归档或旁边的 `ROOMusic-V0` 目录。
- V0 与当前实现必须读取同一份、同一摘要的当前真实资产。历史 UAT 数量只是 sanity
  check，不因历史 corpus 与当前 corpus 数量不同而否定本轮基准。
- 历史 SQLite/PostgreSQL 若日后出现，只作审计候选；多个候选必须先停下交由用户选择。

## 2. 交付边界

实现分为三个相互独立但由一个显式入口编排的部分：

| 部分 | 责任 | 不负责 |
| --- | --- | --- |
| Smoke 编排脚本 | opt-in、前置检查、隔离服务、正式 REST 扫描、超时/信号清理、产物权限 | scanner 业务规则、直接 SQL 修正 |
| 对照工具 | corpus 总摘要、V0/current canonical export、稳定匹配、确定性 diff、脱敏报告 | 启动业务服务、修改真实资产 |
| 缺陷修复 | 用脱敏合成 fixture 固化回归，在 parser/organizer/persistence/REST owner 修复 | 对真实音乐或 V0 数据做特例补丁 |

预计新增一个 `scripts/real-library-smoke.sh` 显式入口，以及位于当前 Go module 内的窄
`roomusic-smoke` 开发工具。Compose/Docker 定义只服务于 smoke，不改变生产
`compose.yaml` 的依赖关系。真实扫描不进入默认 `go test ./...` 或 CI。

## 3. 总体数据流

```text
显式 opt-in + 路径/工具/端口检查
  -> 真实资产前置树摘要（详细清单仅在 0700 临时目录）
  -> 校验并解包固定 V0 归档
  -> 隔离 V0 PostgreSQL/Redis/必要服务 + /music:ro
  -> V0 migration/setup/login/library-path/scan/poll
  -> V0 canonical snapshot + 权限受限的 catalog dump/manifest
  -> 隔离当前 PostgreSQL + 当前应用 + /music:ro
  -> 当前 migration/setup/library-root/首次 scan/poll
  -> current snapshot A
  -> 当前第二次 scan/poll -> current snapshot B
  -> A/B 幂等 diff + V0/current 语义 diff
  -> 真实资产后置树摘要
  -> 脱敏汇总/差异分类
  -> 清理容器、网络、volume、临时目录
```

任一阶段失败都不得继续生成“通过”报告。只有 V0 扫描完整成功、dump 完整且 manifest
落盘成功时，才允许保留本地 V0 基准文件。

## 4. 隔离运行合同

### 4.1 共同前置条件

- 入口同时要求显式 opt-in 和显式真实音乐根参数；空值、符号链接根、非目录、`/`、
  工作区根或与任一 data/database 目录重叠时 fail closed。
- 不读取现有 `.env`/`.env.dev`。脚本生成临时凭据、随机 Compose project 名和空闲
  loopback 端口，所有敏感临时文件为 `0600`，目录为 `0700`。
- 应用容器仅以 `:ro` 挂载真实音乐；V0/current 的数据库、data、cache、构建和报告目录
  与现有项目完全分离。通过 Compose inspect 再次确认 mount 的 `RW=false` 后才触发扫描。
- AI、provider、定时扫描和非必要网络能力显式关闭。V0 运行所需 Redis/Meilisearch
  即使启动，也只能位于随机隔离 project 网络中，不能复用现有服务或 volume。
- trap 处理成功、失败、取消和信号路径；使用精确 project 名执行 `down --volumes`，
  禁止对默认 `roomusic` project 或当前 volume 执行清理。

### 4.2 V0 正式路径

V0 通过归档自带 Dockerfile 与 migration 启动 production app，调用正式接口：

1. `/api/setup` 创建一次性管理员；
2. `/api/auth/login` 获取只存在于临时文件/内存的 token；
3. `/api/library-paths` 注册容器内只读根；
4. `/api/scan/trigger` 触发任务；
5. `/api/scan/progress` 和持久化 scan state 共同确认完整终态。

不得绕过 scanner 直接调用 assembler 或向 catalog 表填充结果。为重放生产生成逻辑而
启动 V0 runtime 是测试工具行为，不会把 Redis、Meilisearch、GraphQL、AI 或旧 schema
引入当前 ROOMusic。

### 4.3 当前 ROOMusic 正式路径

当前应用使用独立 PostgreSQL 18 和临时 `ROOMUSIC_DATA_DIR`，配置唯一
`ROOMUSIC_ALLOWED_LIBRARY_ROOTS`。通过当前 `/api/v1` 的 setup/session、library root、
scan start/status、diagnostics、Release list/detail/evidence 完成端到端验收。首次扫描和
输入不变的第二次扫描必须各自进入完整成功终态；非成功终态禁止做稳定性结论。

## 5. 真实资产只读证明

对照工具在扫描前后生成同一版本的树摘要：

- 逐项输入包括规范化相对路径、文件类型、size、mode、mtime 和流式内容 SHA-256；
- 详细条目只写入本轮权限受限临时目录，不进入日志、任务文档或 Git；
- 对条目稳定排序后生成 corpus 总 SHA-256，并只在最终报告保存文件数、总字节数和总
  摘要；
- 前后总摘要或聚合值不同，本轮立即判为 `corpus_changed_during_run`，所有 V0/current
  diff 作废；
- 扫描日志、错误和命令回显不得包含宿主绝对路径、完整文件名清单、tags、CUE/封面内容。

容器只读挂载是主要物理保护，前后摘要是独立验收证据；二者缺一不可。

## 6. Canonical snapshot 合同

V0 和当前 schema 分别由独立 adapter 读取，再映射到同一个版本化、确定排序的模型。
adapter 使用显式列和只读事务，不共享两套数据库的内部 ID。

### 6.1 数据集身份

- `snapshot_version`
- `implementation`：`v0_generated_corrected` 或 `current`
- `code_hash`、migration/schema 摘要、corpus 总摘要
- scan run 终态、开始/结束时间和安全聚合计数

时间只作审计，不参加语义相等判断。

### 6.2 稳定实体

- 物理来源键：root-independent 的规范化相对来源摘要；
- CUE 虚拟来源键：sheet 摘要、父来源摘要、track number、INDEX 01；
- Release 键：其 present 物理/虚拟来源集合摘要，并辅以 candidate kind/目录 scope；
- Medium 键：Release 键与 position；
- Track 键：稳定来源键，不使用 UUID、自增 ID 或遍历顺序。

路径只删除根前缀和统一分隔符，不做大小写、Unicode 或空白的隐式“纠正”，避免掩盖
真实 parser 差异。需要写入长期报告的来源标识使用本轮 keyed digest 或无语义序号，
详细映射只留在临时目录。

### 6.3 比较字段

- Release/Medium/Track/File 层级、归组、多碟、Box leaf 和 CUE virtual 关系；
- title、artist、album artist、year、source/media、edition、label、catalog、genre；
- credits、audio/CUE facts、current field decision、confidence/action、关键 evidence；
- scan diagnostics、attention 和管理员 REST 投影聚合。

只有两端确有等价语义的字段执行 exact compare。schema adapter 必须显式声明不可比字段，
不得通过删除、lowercase 或空值合并让差异消失。

## 7. 差异分类和处置

比较器先做结构化 raw diff，再要求每项进入以下互斥类别：

| 类别 | 判定 | 处置 |
| --- | --- | --- |
| `current_regression` | 当前已承诺能力与 V0 corrected 输出不同 | 必须最小回归测试、根因修复、定向与完整复验 |
| `schema_mapping_gap` | 两端已有等价值但 adapter 尚未正确映射 | 修正 adapter 和 synthetic mapping test |
| `capability_gap` | 仅 V0 后续 overlay/provider/AI/topology 能力拥有该语义 | 记录证据；需要扩围时返回规划 |
| `historical_corpus_drift` | 只存在于历史 UAT 数量与当前 corpus 的差异 | 记录，不影响同一当前 corpus 对照 |
| `intentional_contract_difference` | 当前已批准合同明确不同于 V0 | 引用 PRD/spec，由用户接受后关闭 |

分类不是比较器的猜测默认值。除机械可证明的 mapping/historical drift 外，所有非回归
结论都必须带代码、schema 或用户批准证据。最终不得存在 `unclassified`。

## 8. 本地基准与报告

- 成功的 V0 catalog dump、canonical snapshot 和 manifest 保存到新增的 Git 忽略本地
  目录，文件权限 `0600`；manifest 不包含凭据、宿主绝对路径或逐文件 metadata。
- 基准名包含 V0 code hash 和 corpus 总摘要。发现同一身份但内容 hash 不同时 fail
  closed；存在多个不同基准时只盘点并等待用户选择。
- 任务内只记录脱敏 `smoke-result.md`：运行身份、聚合计数、耗时、终态、前后 corpus
  总摘要是否一致、差异分类数量、修复/延期和实际命令，不记录原始数据库或逐项私有数据。
- 默认失败路径删除 dump 与 raw snapshot；可复现调试依赖脱敏 fixture，不依赖保留失败
  数据库。

## 9. 修复循环与回退

1. 对 `current_regression` 只提取最小结构事实，构造无真实内容的合成音频/CUE/SQL fixture。
2. 先让回归测试失败，再在真实 owner 修复；禁止按来源 hash、私有文件名或具体专辑写
   特例。
3. 运行与修改直接相关的最小测试和 PostgreSQL 集成测试，再执行对应 hash ID 的定向
   smoke；所有问题关闭后重新从空数据库执行完整 V0、当前首次/重扫和对照。
4. 若修复需要改变已批准产品合同、引入 overlay/AI/provider/topology 或文件写入，停止
   本任务并返回规划；不因“让 diff 归零”而扩大权限。

V0 归档和基准生成逻辑永不在本任务中修改。当前修复可按 Git patch 回退；数据库均为
临时或可重建，本任务不提供 destructive down migration，也不接触现有业务数据库。
