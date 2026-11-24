# 设计：补到新证据后重开搁置的待定项

## 现状

`backend/internal/curation/work_states.go` 的 `UpsertPendingItem` 在冲突时只在下面两种情况更新：

- 项处于 `waiting_model` 或 `in_progress`；
- 项处于 `shelved`，且 `input_digest` 变化。

搁置项摘要不变时整条 `UPDATE` 都不执行，候选与冲突停在搁置时的样子。`input_digest` 来自发行的目录证据（`curation.ReleaseDigest`），
密钥、源开关、外部数据变化引起的重做不会改变它。

调用方有两处：`program/pass.go` 的 `savePending`（`release_match`、`recording_match`）、`release_merge_discovery.go`（`release_merge`）。

## 决定

搁置项在下面任一条件成立时重开（回到 `waiting_model`，`attempts` 清零，搁置理由清空，与现在摘要变化时的处理相同）：

1. `input_digest` 变化（不变）；
2. `candidates` 与搁置项保存的不同（`jsonb` 比较，`IS DISTINCT FROM`）；
3. `conflicts` 与搁置项保存的不同。

只改这一条 SQL 的 `WHERE`，不加列、不加迁移。

理由：

- 模型看到的就是 `evidence`、`candidates`、`conflicts` 三样，`evidence` 由目录证据得出，已被摘要覆盖；另两样正是指纹、密钥、外部数据带来的变化。
- 重开后保存的就是新的候选与冲突，下一轮同样的结果不再重开，满足“同一组候选只重开一次”。候选来回变化时每次都重开，这本身是证据变了，可以接受。
- `jsonb` 比较按值，不受键顺序影响；候选列表由程序按固定规则排序生成（`releaseCandidates`、`recordingCandidates`），同样的外部结果得到同样的列表。实现时核对排序是否稳定，不稳定就在生成处排序，不在比较处处理。

### 不受影响的状态

- `waiting_human`（例外）：仍不更新，人还没处理的例外内容不变。
- `resolved`（含人工不采纳）：不在冲突索引内，行为不变。不采纳后不再提出由“不再提出”记录保证，本任务不碰。
- `release_merge`：`release_merge_discovery.go` 在摘要变化时已经先把旧项结束、再建新项；候选只有一个来源发行，比较同样适用，不另做处理。

## 规范

`.trellis/spec/backend/` 里 curation 合同中描述待定项“只在输入摘要变化时重开”的地方同步改写（实现时按 `grep -rn "shelved" .trellis/spec/backend` 找到原文）。

## 回退

单条 SQL 改动，回退即恢复原 `WHERE`。
