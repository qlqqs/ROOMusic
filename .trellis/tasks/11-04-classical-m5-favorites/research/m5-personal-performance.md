# M5 本人收藏与歌单后端性能验证

日期：2026-11-04。本文前半部分为提交前追加验收的**当前实现结果**；后半部分保留首轮极端形状证据，不能将两轮不同实现、统计状态和计时口径直接作性能倍率比较。对应[设计 §10](../design.md#10-性能预期与必须验证的-explain)。

## 本轮结论：真实形状全部达标

真实 app、会话鉴权、本机 TCP、客户端读完响应体，非 race；每阶段每场景20个有效样本、另一次预热，nearest-rank p95。两阶段共320个有效场景样本，另16次预热。服务端 handler 从进入完整 app 到 ServeHTTP 返回；TCP 从发请求到读完并关闭 body，均不含客户端 JSON 解码。详情组合为两个真实请求串行累计，避免只报一个便宜端点。

下表单位为 ms；斜线前后分别为**空辅助表未 ANALYZE / ANALYZE 后**。TCP 和服务端分别验收，全部通过。

| 场景 | 门槛 | 完整TCP p95 | 服务端 p95 | 结论 |
| --- | ---: | ---: | ---: | --- |
| 歌单列表20×10,000 | 1,000 | 841.08 / 863.15 | 840.53 / 862.77 | 通过 |
| 收藏20,000，播放前10,000可用轨 | 2,000 | 642.92 / 660.75 | 625.43 / 634.10 | 通过 |
| 歌单10,000，播放全部可用轨 | 2,000 | 558.01 / 540.25 | 545.32 / 527.33 | 通过 |
| 收藏曲目首50项 | 300 | 14.23 / 14.26 | 13.70 / 13.78 | 通过 |
| 收藏专辑首50项 | 300 | 15.49 / 14.12 | 14.98 / 13.83 | 通过 |
| 歌单摘要 | 300 | 52.85 / 49.08 | 52.57 / 48.72 | 通过 |
| 歌单items首50项 | 300 | 11.61 / 14.40 | 11.04 / 13.92 | 通过 |
| 歌单摘要+items串行组合 | 300 | 60.91 / 62.95 | 60.12 / 62.00 | 通过 |

收藏队列响应体5,622,108字节，歌单队列最多5,342,266字节；Go JSON解码单独记录于原始数据，不等同浏览器 decoder/DOM。未清操作系统或数据库缓存，没有声称冷缓存、并发负载或远程网络已达标。共享机器上的本次测量通过既定门槛，不是跨硬件 SLA 保证。

### 真实形状与统计状态

- 独立夹具 [personal-realistic-scale.sql](../../../../backend/internal/app/testdata/personal-realistic-scale.sql)：100,000轨、10,000发行、8,000组，每发行恰好10轨，5,000 missing；不依赖 S 线夹具。
- 本人20,000收藏，1,000 missing；20张歌单各10,000轨/500 missing。每张歌单跨10,000发行、8,000组，不用所有卡片重复同一小集合。相邻歌单环形重叠5,000轨，20张并集恰好100,000轨，每轨出现两次。
- 另一用户100,000曲目收藏、8,000专辑收藏、20张歌单，保留owner干扰。页/队列核对总量、顺序、missing、omitted与完整统计；未缩小夹具以过门槛。
- 核心表两阶段均有统计；修正、问题核对、字段决定、归组依据、封面等辅助表为空。第一阶段检查 `reltuples=-1`、无手动或自动 ANALYZE；第二阶段仅由测试准备器 ANALYZE 后检查 `reltuples=0`。每阶段开始和结束均核对。
- PostgreSQL18.6，Go1.25.10，12逻辑CPU，shared_buffers=128MB、work_mem=4MB，会话默认 `jit=on`。所有 `enable_*`、优化器参数、会话和全局 JIT 均保持默认；仅 personal 延迟敏感只读事务 BEGIN 后执行 `SET LOCAL jit = off`，业务事务不 ANALYZE。

### 保留的优化与边界

1. catalog 公开 `LoadTrackCollectionStats`：至多10,000不同有效UUID，只从owner的tracks表返回匹配/缺失/未知时长计数及正有限时长之和，不传完整事实行或所有组。空输入不查库，未知身份由匹配计数暴露；NULL、NaN、±Infinity和非正时长仍为未知，SUM溢出失败。personal不读catalog私表。
2. 卡片按既有最多10,000引用批次完整聚合。封面仅补前四个不同组的位置事实，32起指数窗口、单批上限10,000，四组齐全后停止该部分投影；missing或无封面的组也占位。统计不随封面提前停止，撤销超限成员仍全量统计；跨批顺序、计数和RR快照保持。
3. 按获批规则，读事务局部关闭JIT，去掉空辅助表误估触发的编译成本。SET失败保留错误链、回滚且不继续业务；集成验证实际Service入口在同一PID上事务内off、提交/回滚后恢复默认，写事务不执行SET。规则已写入[数据库规范](../../../spec/backend/database-guidelines.md#延迟敏感只读事务与-jit)，供F/S线引用。
4. 不引入跨请求缓存、派生持久统计、额外Go依赖或200,000输入公开API；HTTP路径/字段/状态码和golden合同不变。

### 试验与证据归属

- 最初2样本探索的 `before.json` 未入库，本机保存在 `/home/qlqq/.local/share/roomusic-evidence/m5-personal/realistic-performance/before.json`：卡片约1,728/1,766ms，不充当正式p95证据。
- 聚合中同时求全部组排序的试验约2.3–2.4s；20万引用一次聚合产生临时落盘；20万引用窗口去重facts的20样本失败门槛记录仍约1.10–1.12s，本机保存在 `/home/qlqq/.local/share/roomusic-evidence/m5-personal/realistic-performance/page-facts-200k-failed-sla.json`，未入库。这些方案均撤回，不在最终生产代码；其余probe JSON也移至该仓库外目录，仅为诊断记录。
- [final.json](realistic-performance/final.json) 为唯一当前正式HTTP验收：`valid=true`、`mode=performance`、`samples_per_scenario=20`、`sla_gate=true`，包含每轮原始样本、夹具计数、统计前后、环境参数和响应大小。
- 当前28份catalog实际SQL计划的结论见[Catalog报告](m5-personal-catalog-explain.md)。原始计划未入库，本机保存在 `/home/qlqq/.local/share/roomusic-evidence/m5-personal/realistic-performance/catalog-explain/realistic-cards/`，可用该报告中的命令重新生成当前实现的计划。默认JIT下新统计热测10.71/10.42ms、32项位置事实0.25/0.33ms，无temp落盘；计划不是HTTP门槛替代品。

### 本轮复现命令

在本线独占启动器创建的 `roomusic-test-m5p` 随机隔离库环境中，从worktree根执行；不得复制旧URL或接管其他实现线容器。性能采样与race分开，race不设置报告目录，以免覆盖本轮非race数字。

```bash
source /tmp/roomusic-m5p-test-env
ROOMUSIC_PERSONAL_REALISTIC_PERF=1 ROOMUSIC_PERSONAL_REALISTIC_SLA=1 ROOMUSIC_PERSONAL_REALISTIC_DIR="$PWD/.trellis/tasks/11-04-classical-m5-favorites/research/realistic-performance" ROOMUSIC_PERSONAL_REALISTIC_REPORT=final.json go -C backend test ./internal/app -run '^TestPostgreSQLPersonalRealisticHTTPPerformance$' -count=1 -v
```

本轮回归、样本SHA核对与清理状态统一见[交付报告](m5-personal-backend-handoff.md)；不以旧轮验证冒充新轮结果。

## 首轮历史记录：极端形状风险，非当前HTTP复测

**以下原始记录属于上一轮实现。** 单发行100,000轨/80%missing、5样本、服务+JSON口径：卡片p95 3.408s、2万收藏队列10.956s、万首歌单队列5.574s、十万收藏队列27.610s，未满足本次正常形状门槛。按用户要求保留风险，不追该极端目标；不能据此断言优化后仍为相同耗时，也不能拿下面的“无SLA”或“完整HTTP另测”覆盖上面的本轮验收。

日期：2026-11-04。范围：design.md §10 的 P 线后端；艺人 canonical 聚合由 S 线实现，不在本报告中。

### 环境与方法

- 仅 `roomusic-test-m5p` 隔离 Compose 项目、随机库名和空闲端口；不连接业务库，不操作其他实现线容器。
- PostgreSQL 18.6；合成目录 100,000 曲目均在一个发行，20% present、80% missing，约1/7未知时长。两用户干扰，收藏同时间；本人1,000歌单、其中20张各10,000首；另测20张各10,002首；1,000不可用组。
- 性能结果为非 race，每个服务＋JSON编码场景5次，p50取中位数、p95取本组最大样本。样本量小，不是SLA；共享主机仍有其他实现线负载。
- `EXPLAIN (ANALYZE, BUFFERS, WAL, FORMAT JSON)` 运行实际应用查询；写查询置于回滚事务；不关JIT、不禁seqscan。44份个人读写计划在同一JSON中。
- “first/warm”分别为准备并ANALYZE后的首次和重复测量，没有清操作系统/数据库缓存，不声称是真正冷缓存。峰值为5ms采样的Go heap增长，不是进程RSS或完整峰值；allocated是单请求累计分配，不能混称常驻内存。

### 请求级结果

服务用例计时包含JSON编码，不含HTTP鉴权/网络；完整HTTP另测。

| 场景 | p50 ms | p95 ms | JSON字节 | 最大累计分配 MiB | 最大采样heap增长 MiB | facts/summary/group批次 |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| cards-20x10000 | 3331.96 | 3408.44 | 7,894 | 225.75 | 16.35 | 20/0/1 |
| favorites-20000-queue | 10513.35 | 10956.35 | 2,216,725 | 41.10 | 14.65 | 2/4/0 |
| playlist-10000-queue | 5443.06 | 5574.01 | 1,108,665 | 21.09 | 13.37 | 1/2/0 |
| favorites-100000-queue | 27105.31 | 27609.56 | 5,541,588 | 152.50 | 19.75 | 10/10/0 |
| cards-20x10002 | 3380.06 | 3462.28 | 7,894 | 225.75 | 12.68 | 21/0/1 |

- 20张满额卡片完整统计200,000引用；超限场景统计200,040引用、每张over_limit_count=2。未加载完整TrackSummary；只有一个当前组，实际group批次1。
- 除catalog批次外，满额列表个人SQL为1次COUNT、1次状态页、21次引用批次（含边界空批），合计44条SQL，不计BEGIN/COMMIT；超限为1+1+21+21+1=45条。计数由端口spy和批次控制流核对，不把它冒称数据库网络包数量。
- 100,000收藏队列：total=100000、missing=80000、eligible=20000、returned=10000、omitted=10000，JSON约5.54MB。只保留候选窗口，但轻量统计仍遍历完整集合。
- 单一发行100,000轨是极端数据形状；缺失核对和完整摘要成本明显，不能用轻量facts延迟代替整队列耗时。本次已ANALYZE关系表、未补齐空的元数据辅助表统计；catalog复测定位单批2,495ms中JIT约2,074ms，补齐这些统计后单批约399ms（不是本表队列已重测后的数字）。诊断原因及catalog计划见同目录[Catalog 执行计划报告](m5-personal-catalog-explain.md)。

#### 完整HTTP

真实app＋不跳过会话认证＋本机TCP，单发行10,000首present：5次；p50 **353.46ms**、p95 **398.64ms**，响应 **5,400,390字节**。Go JSON解析p50 101.25ms、p95 122.86ms；**这不是前端decoder时间**。浏览器解码、渲染和DOM数量属于步骤3/4，本轮未执行。

### EXPLAIN 热测摘要

| 实际查询 | 执行 ms | 输出行/估算行 | shared hit/read | 临时写块 |
| --- | ---: | --- | --- | ---: |
| favorites-deep-100000 | 33.672 | 50/10 | 1839/0 | 0 |
| favorites-first | 0.085 | 50/50 | 4/0 | 0 |
| favorites-keyset-older-time | 3.664 | 10000/10000 | 75/0 | 0 |
| favorites-keyset-same-time | 3.377 | 10000/10000 | 75/0 | 0 |
| favorites-middle | 2.357 | 50/50 | 75/0 | 0 |
| favorites-total | 2.988 | 1/1 | 124/0 | 0 |
| lookup-10000 | 3.652 | 10000/2172 | 64/0 | 0 |
| lookup-albums-1000 | 0.352 | 1000/632 | 10/0 | 0 |
| playlist-card-batch | 5.341 | 10000/10000 | 264/0 | 0 |
| playlist-existing | 18.982 | 10000/9752 | 2322/0 | 0 |
| playlist-items | 1.839 | 51/51 | 108/0 | 0 |
| playlist-list | 0.126 | 20/20 | 3/0 | 0 |
| playlist-lock | 0.124 | 1/1 | 5/0 | 0 |
| playlist-membership | 2.713 | 1000/891 | 3013/0 | 0 |
| playlist-neighbor | 0.046 | 1/1 | 4/0 | 0 |
| playlist-queue-batch | 2.963 | 5000/1 | 143/0 | 0 |
| playlist-quota | 0.205 | 1/1 | 27/0 | 0 |
| playlist-update-one | 0.150 | 0/0 | 30/0 | 0 |
| receipt-replay | 0.025 | 1/1 | 1/0 | 0 |
| undo-original-index | 1.505 | 1/1 | 106/0 | 0 |
| undo-predecessor | 0.051 | 1/43 | 4/0 | 0 |
| undo-token | 0.022 | 1/1 | 1/0 | 0 |

- 收藏首/中/深页使用user/created索引；deep OFFSET仍随偏移增长，不宣称O(page_size)。精确total走本人PK范围，仍O(F_user)。
- 收藏时间相同的keyset以两条独立范围seek实现，同时间ID范围和更早时间范围均命中时间索引，热测无过滤掉前10,000项的重复工作。
- items分页、卡片批次、队列tuple续批、邻居走order复合索引；反查1,000歌单走user/track/playlist索引与歌单主键；正常移动仅更新一个成员行。
- 小型配额表/undo/收据上优化器可能选seqscan；例如2,000歌单中精确过滤本人1,000，语义有owner约束。未强制索引以伪造结果。成功移除收据507字节，远小于64KiB。
- 计划JSON保留所有节点的估算/实际行、loops、buffer、WAL、排序/JIT/临时I/O；只省略超过600字节的巨大合成UUID字面量，保留查询结构。

### 排序重整的根因与修复

初始新表无统计时，两侧owner筛选都误估1行，Nested Loop将窗口子查询重跑10,000次、比较99,990,000对，10,000项重整约110秒；ANALYZE后约228ms。单纯物化只能消除重复窗口，仍可能错选owner/order索引逐项扫全歌单。最终按owner先物化合法主键集合，再只按全局主键更新该集合；owner授权仍在来源CTE，不向未知成员写入。没有设置planner开关或在业务事务里强制ANALYZE。新回归同时测fresh/analyzed，断言不会产生超线性重复过滤；普通排序/撤销身份、时间、revision和跨owner测试仍须通过。

| 重整阶段 | 完整耗时 ms（含恢复唯一约束） |
| --- | ---: |
| analyzed | 247.279 |
| fresh | 297.264 |

### 收据运维与保留

收据保留至用户物理删除，不设置TTL、不随歌单删除。容量诊断只需聚合，不输出个人请求/响应内容；下列是供数据库运维执行的只读监测，不新增管理API：

```sql
SELECT count(*) AS receipt_count,
       avg(pg_column_size(response_body)) AS average_body_bytes,
       max(pg_column_size(response_body)) AS maximum_body_bytes,
       pg_total_relation_size('personal_playlist_requests') AS total_relation_bytes
FROM personal_playlist_requests;
```

按固定间隔记录聚合增长、查询延迟、表/索引/TOAST体量；常规VACUUM/ANALYZE不改变活用户幂等键的保留承诺。未来需要冷热归档时必须完整保留user+key、指纹、status/body，并在同一PKEY锁下透明读取。

### 可复现命令与证据

在本线启动器提供的隔离环境中执行（不得复用来源不明的URL）：

```bash
source /tmp/roomusic-m5p-test-env
export ROOMUSIC_PERSONAL_EXPLAIN_DIR="$PWD/.trellis/tasks/11-04-classical-m5-favorites/research/personal-explain"
cd backend
go test ./internal/personal -run '^TestPostgreSQLPersonalPerformance$' -count=1 -v
go test ./internal/personal -run '^TestPostgreSQLPersonalOrderRebalanceExplain$' -count=1 -v
go test ./internal/app -run '^TestPostgreSQLPersonalHTTPPerformance$' -count=1 -v
```

证据目录：`.trellis/tasks/11-04-classical-m5-favorites/research/personal-explain/`：`personal-performance.json`、`http-performance.json`、`order-rebalance.json`及修复前`order-rebalance-before.json`。普通race门禁不设置输出目录，仅一次请求验证各规模正确性，不覆盖五次非race结果。

### 限制与待主会话评估

- 无外部路径/字段/状态码偏差；没有新增产品决策。内部改进包括静态路由子mux、叶子模板完成日志、UUID排序列限定、两段keyset、catalog批量计数、重整主键驱动。
- 尚未测真正冷缓存、前端decoder/DOM和浏览器交互；这些不能从Go测试推断。前端步骤3/4另验收。
- 20张满额卡片和极端大量missing队列仍有显著成本；本设计未给毫秒SLA，主会话应据报告决定交互门槛，不能直接宣称全部性能达标。

### 缺陷复盘：无统计信息下的重整退化

#### 1. 根因分类

- **E 隐含假设 + D 测试缺口**：以为带owner过滤的UPDATE必然使用主键定位；小规模正确性用例和ANALYZE后计划没有暴露新表的错误基数估计。真实fresh计划将两侧各10,000行都估为1行，实际重复比较近一亿次。

#### 2. 修复尝试与证据

1. 原窗口子查询被重复执行10,000次，完整重整约110秒。
2. 仅加MATERIALIZED令窗口只算一次，但目标仍选择owner/order索引，每项过滤9,999项，约23.6秒；这只解决了一半问题。
3. 来源CTE先限定owner并物化合法主键，目标按全局主键定位，fresh约297ms、analyzed约247ms。计划证明消除了重复范围过滤，未以关闭Nested Loop、关闭JIT或强制业务ANALYZE规避。

#### 3. 防止复发

| 优先级 | 机制 | 具体动作 | 状态 |
| --- | --- | --- | --- |
| P0 | 计划回归 | 10,000项fresh/analyzed均执行真实EXPLAIN，约束重复过滤工作量；写查询回滚 | 已完成 |
| P0 | 业务回归 | 10,001项超限恢复触发重整，核对所有原身份/时间、顺序和仅一次revision | 已完成 |
| P0 | 权限边界 | 只从本人歌单CTE取得可写主键，未知目标无法进入更新集合 | 已完成 |
| P1 | 合同 | personal-contracts记录来源授权、物化与主键驱动，不复制错误owner索引连接 | 已完成 |
| P1 | 测试可信度 | FK/唯一约束断言准确SQLSTATE，不能将无类型参数的42P18误当约束拒绝 | 已完成 |

#### 4. 系统性检查范围

同类风险包括输出 `id::text` 后ORDER BY裸别名、混向复合键的OR游标、批量摘要估算偏差触发JIT。本轮在新personal查询与所消费catalog摘要内核对并修复已有证据的问题；不扩大到无关能力重构。保留极端missing摘要成本作为明确风险，而不是用微型夹具或强制planner设置制造绿色结果。

#### 5. 知识固化

已更新 `.trellis/spec/backend/personal-contracts.md`、`database-guidelines.md`，保留修复前后原始执行计划及回归。没有自动创建新任务或改变主会话批准的外部合同。
