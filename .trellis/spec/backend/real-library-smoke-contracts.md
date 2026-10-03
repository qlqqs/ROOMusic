# 真实库 Smoke 与 V0 对照合同

## 1. 范围与触发

当代码涉及真实音乐资料库的显式、只读验收（`scripts/real-library-smoke.sh`），或 V0 与 current 的对照比较器时，遵守本合同。
它不进入默认测试或 CI。真实媒体的访问边界与披露规则见 [真实音乐资产](../guides/real-music-assets.md)。

## 2. 接口签名

- 真实音乐显式验收：`ROOMUSIC_REAL_LIBRARY_SMOKE=1 ./scripts/real-library-smoke.sh
  --music-root <绝对目录> --v0-archive <固定归档>`；不得进入默认测试或 CI。

## 3. 合同

### 真实 Smoke 环境与 corpus 摘要

- 真实 Smoke 使用随机 Compose project、空 PostgreSQL 18 volume、临时数据目录和
  `/music:ro`；V0 只从固定 hash 归档注入独立 adapter，运行时网络为 `none`，先写
  normalized SQLite 再导出 snapshot v2。前后 corpus 摘要、SQLite round-trip、current
  首扫/重扫、REST 投影、零 `current_regression`、零未知分类和分类计数闭合都是成功
  门禁；本地产物只能写入权限受限且 Git 忽略的 `.roomusic-smoke/`。成功后可保留 V0
  SQLite/canonical 及经过稳定键、字段白名单与脱敏约束的 current A/B snapshot、comparison
  和 manifest；原始 SQL dump、逐项路径、凭据、Cookie 与 exporter rows 必须删除。
- 任务内 `research/smoke-result.md` 只发布脱敏聚合。并发成功运行各自保留独立审计目录，
  聚合报告采用“最后一个完整成功运行胜出”的同目录原子 rename，不允许并发直接覆盖写入。
- corpus 摘要对每个节点纳入规范相对路径、节点类型、mode 和 mtime；普通文件另外纳入
  size 与流式内容 SHA-256，目录节点也必须参与总摘要。遍历不跟随 symlink，前后任一
  节点事实变化都会使本轮对照失效。

## 4. 校验与错误矩阵

| 条件 | 行为 |
| --- | --- |
| V0 codec 为 `aac`、current 为 `alac`，且 V0 缺少 current 的 5 类音频事实 | 只允许 `duration_ms`、`sample_rate`、`channels`、`bitrate`、`bit_depth` 归为窄 `intentional_contract_difference` |
| canonical 对照出现未知字段差异 | 保留为 `current_regression`/未分类失败；不得用空值或通用 ignore 放行 |
| V0/current 报告出现 `current_regression`、未知分类或分类计数与差异项不闭合 | 整轮 Smoke 非零退出，不发布成功报告或保留该轮审计产物 |

## 5. 正确、基准与错误案例

- 基准：V0 的组装前 `grouping_medium_count=1` 与双方最终两个 Medium 并不代表图回归；
  只有在 Medium keys 完全一致且 current 值等于最终图计数时，才允许归为窄
  `schema_mapping_gap`。
- 基准：V0 将 ALAC 报为 `aac` 且没有音频事实、current 将同一来源报为 `alac` 并补齐
  5 类音频事实时，差异可以按窄规则接受；同样的空值差异若发生在 catalog、CUE ISRC
  或其它字段，仍是 `current_regression`。

## 6. 必需测试

- 真实 Smoke 比较器：ALAC current-only composer 只有在 V0 `aac`/current `alac` 且
  其它非 performer credits 完全保持时才是合同差异；current-only 音频事实只允许
  `duration_ms`、`sample_rate`、`channels`、`bitrate`、`bit_depth` 这 5 类；stale Medium
  count 只有图 keys 相同才是映射差异；未知 catalog/CUE ISRC 以及每条窄分类的相邻反例
  必须仍落入 `current_regression`。
