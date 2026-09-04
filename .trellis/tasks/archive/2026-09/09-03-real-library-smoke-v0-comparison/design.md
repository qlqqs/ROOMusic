# 技术设计：真实音乐库只读 Smoke 与 V0 生成基准对照

## 1. 基准定义

本任务不再等待历史 `golden.sqlite`，也不从已知失败的 V0 production PostgreSQL 截取
部分数据。用户已确认固定 V0 scanner 的生成结果已经包含此前人工修正；代码又证明
production worker、现存真实语料测试和历史 golden generator 在图生成阶段共用同一组
核心 API。因此基准定义为：

```text
V0 scanner 代码身份 + smoke adapter 身份 + 当前 corpus 身份
  + standalone 核心调用链完整成功 + normalized SQLite 校验成功
  = v0_release_graph_generated_corrected
```

- V0 代码身份固定为 `ROOMusic-migration.tar.gz`，执行前核对 SHA-256
  `fe25388328698b26991ea3b59a14406a155eb92d578a9be2a68d67d331ecf97d`。
- 归档只解包到权限受限的临时目录，不修改归档或旁边的 `ROOMusic-V0` 目录。
- adapter 源码由本任务维护，只复制到临时解包副本的 `cmd/` 下构建；它可以调用 V0
  `internal/scanner`，但不能修改或替换 scanner 文件。manifest 分别记录 archive 与
  adapter hash。
- V0 与当前实现必须读取同一份、同一摘要的当前真实资产。历史
  `110 Release/112 Medium/407 Track/466 File` 只作口径明确的 sanity evidence，不覆盖
  同一 corpus 的实际逐项输出。
- 基准范围从设计上就是 Release Graph-only，成功时记录 `degraded=false`；不生成或导出
  V0 local evidence、quality badge、scan diagnostics、REST/queue 状态，也不需要接受任何
  V0 production 失败。
- 历史 SQLite/PostgreSQL 若日后出现，只作审计候选；多个候选必须先停下交由用户选择。

## 2. 交付边界

实现分为三个相互独立但由一个显式入口编排的部分：

| 部分 | 责任 | 不负责 |
| --- | --- | --- |
| Smoke 编排脚本 | opt-in、前置检查、隔离 V0 exporter 与当前正式 REST 扫描、超时/信号清理、产物权限 | scanner 业务规则、直接 SQL 修正 |
| V0 adapter | 调用固定 V0 scanner 的公开核心 API，输出严格版本化的临时 graph rows | 复制算法、保存 local evidence、启动 V0 runtime |
| 对照工具 | corpus 总摘要、SQLite/canonical export、稳定匹配、确定性 diff、脱敏报告 | 修改真实资产、推断或忽略未知差异 |
| 缺陷修复 | 用脱敏合成 fixture 固化回归，在 parser/organizer/persistence/REST owner 修复 | 对真实音乐或 V0 数据做特例补丁 |

预计新增一个 `scripts/real-library-smoke.sh` 显式入口，以及位于当前 Go module 内的窄
`roomusic-smoke` 开发工具。Compose/Docker 定义只服务于 smoke，不改变生产
`compose.yaml` 的依赖关系。真实扫描不进入默认 `go test ./...` 或 CI。

## 3. 总体数据流

```text
显式 opt-in + 路径/工具/端口检查
  -> 真实资产前置树摘要（详细清单仅在 0700 临时目录）
  -> 校验并解包固定 V0 归档
  -> 注入并 hash smoke-owned V0 adapter
  -> 隔离 exporter 容器 + /music:ro + /output:rw + runtime network=none
  -> Walk/parse/FileEvidence/BuildReleaseCandidates/AssembleReleaseCandidate
  -> 临时 graph rows -> normalized v0-reference.sqlite
  -> SQLite graph/路径/FK/唯一性校验 -> canonical V0 snapshot/manifest
  -> 隔离当前 PostgreSQL + 当前应用 + /music:ro
  -> 当前 migration/setup/library-root/首次 scan/poll
  -> current snapshot A
  -> 当前第二次 scan/poll -> current snapshot B
  -> A/B 幂等 diff + V0/current 语义 diff
  -> 真实资产后置树摘要
  -> 脱敏汇总/差异分类
  -> 清理容器、网络、volume、临时目录
```

任一 walk、解析、CUE 路径验证、组装、序列化或 SQLite 完整性失败都不得生成基准或启动
current。V0 adapter 不提供“部分成功”或 known-error 放行分支；只有 graph 全量生成、
SQLite 校验和 canonical round-trip 全部成功后，才允许保留本地基准文件。

## 4. 隔离运行合同

### 4.1 共同前置条件

- 入口同时要求显式 opt-in 和显式真实音乐根参数；空值、符号链接根、非目录、`/`、
  工作区根或与任一 data/database 目录重叠时 fail closed。
- 不读取现有 `.env`/`.env.dev`。脚本生成临时凭据、随机 Compose project 名和空闲
  loopback 端口，所有敏感临时文件为 `0600`，目录为 `0700`。
- 扫描容器仅以 `:ro` 挂载真实音乐；V0 exporter/current 的 data、database、cache、构建
  和报告目录与现有项目完全分离。通过容器 inspect 再次确认 mount 的 `RW=false` 后才
  触发扫描。
- V0 exporter 不启动 Redis、Meilisearch、PostgreSQL、REST、AI 或 provider，运行时网络
  设为 `none`。构建阶段可使用用户明确允许的 `.bashrc` 代理下载已锁定 Go 依赖，代理值
  不写入镜像层、manifest 或运行环境。
- trap 处理成功、失败、取消和信号路径；使用精确 project 名执行 `down --volumes`，
  禁止对默认 `roomusic` project 或当前 volume 执行清理。

### 4.2 V0 standalone 路径

adapter 复刻的是历史 `cmd/golden generate-candidate` 的职责边界，而不是 production
runtime：

1. `scanner.Walk` 在唯一 allowlisted `/music` 根生成 `WalkResult`；
2. 对发现的 CUE 和音频分别调用 `ParseCueSheet`/`ValidateCueFiles` 与 `ParseTags`，构造
   `FileEvidence`；
3. 调用 `BuildReleaseCandidates`，随后为每个 candidate 注入 tags、Cues 和 FileEvidence；
4. 调用 `AssembleReleaseCandidate` 得到 Release/Medium/Track/File、credits 与 grouping
   结果；
5. adapter 只输出 corpus-relative、确定排序的临时 NDJSON；当前 smoke writer 将其写入
   normalized SQLite，校验后再导出 canonical JSON。

步骤 1—4 与现存 `TestRealMusicCorpusRoonReleaseGraph` 同源，也与 production handler 在
`SaveReleaseWithObservationsFenced` 之前的图生成路径同源。步骤 5 属于可审计适配，不改变
V0 业务规则。adapter 对解析错误 fail closed；不会进入 production 独有的 artwork、lyrics、
quality badge、local-evidence persistence、queue 或 reconciliation 收尾。

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

V0 standalone rows 先进入 normalized SQLite，当前 schema 则由独立只读 PostgreSQL
adapter 导出；两端再映射到同一个版本化、确定排序的模型，不共享内部 ID。

### 6.1 数据集身份

- `snapshot_version`
- `implementation`：`v0_release_graph_generated_corrected` 或 `current`
- `code_hash`、adapter hash、SQLite/current migration schema 摘要、corpus 总摘要
- `generation_mode`、开始/结束时间和安全聚合计数
- `baseline_scope=release_graph_only`、`degraded=false` 和 excluded evidence scope

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
- credits，以及 Release Graph 中已有的 audio/CUE facts；
- V0 standalone 基准只比较 adapter 明确导出的 grouping/field facts；V0 local evidence、
  quality badge、scan diagnostics/attention 以及 production runtime 状态始终排除；
- current A/B 仍独立比较 current evidence、diagnostics、attention 和管理员 REST 投影。

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

- 校验通过的 normalized `v0-reference.sqlite`、V0 canonical、字段白名单约束的 current
  A/B canonical、comparison 和 manifest 保存到 Git 忽略本地目录，文件权限 `0600`。
  SQLite 只包含 Release Graph allowlist，不含 local-evidence/quality-badge/scan-error 表；
  审计产物不包含凭据、宿主绝对路径或原始 SQL dump，失败运行不得保留这些产物。
- 基准名包含 V0 code hash 和 corpus 总摘要。发现同一身份但内容 hash 不同时 fail
  closed；存在多个不同基准时只盘点并等待用户选择。
- 任务内只记录脱敏 `smoke-result.md`：运行身份、聚合计数、耗时、终态、前后 corpus
  总摘要是否一致、差异分类数量、修复/延期和实际命令，不记录原始数据库或逐项私有数据。
- 并发成功运行各自保存在独立身份目录；任务内聚合报告按“最后一个完整成功运行胜出”
  原子发布，禁止直接覆盖写入造成截断或混合内容。
- 默认失败路径删除 SQLite、raw rows 与 snapshot；可复现调试依赖脱敏 fixture，不依赖
  production 失败数据库。

## 9. 修复循环与回退

1. 对 `current_regression` 只提取最小结构事实，构造无真实内容的合成音频/CUE/SQL fixture。
2. 先让回归测试失败，再在真实 owner 修复；禁止按来源 hash、私有文件名或具体专辑写
   特例。
3. 运行与修改直接相关的最小测试和 PostgreSQL 集成测试，再执行对应 hash ID 的定向
   smoke；所有问题关闭后重新生成 V0 standalone 基准，并从空当前数据库执行首次/重扫
   和对照。
4. 若修复需要改变已批准产品合同、引入 overlay/AI/provider/topology 或文件写入，停止
   本任务并返回规划；不因“让 diff 归零”而扩大权限。

V0 归档和 scanner 业务逻辑永不在本任务中修改；adapter 可以整体删除回退，已知 V0
production evidence 缺陷另行跟踪。当前修复可按 Git patch 回退；SQLite 与当前测试库均
为临时或可重建，本任务不提供 destructive down migration，也不接触现有业务数据库。
