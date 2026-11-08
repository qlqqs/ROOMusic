# ⑬ 搜索忽略标点

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，T 线第 4 项。参考设计 24.5.2 章（通用归一化的二次拆解思路）。

## 目标

搜索时标点不再挡路：`acdc` 能搜到 `AC/DC`，`guns n roses` 能搜到 `Guns N' Roses`，直撇号和弯撇号互相能搜到。

## 现状（`B/` = `backend/internal/`）

- 规范化 `Text`：NFKC → casefold → 去拉丁附加符 → 片假名转平假名 → 逐字繁转简 → 空白折叠，不删任何标点（`B/catalog/textnorm/textnorm.go:43-54`，`.trellis/spec/backend/search-contracts.md:15`）。
- 投影表 `catalog_search_values` 存 `normalized_value`、`short_grams` 等（`backend/migrations/0025:64-93`）；艺人名另有 `artist_search_names`（`0026:76`）。匹配：`short_grams @> ARRAY[gram] AND normalized_value LIKE '%'||key||'%'`，排名为完全相同 → 开头相同 → 包含（`B/catalog/search_ranked_sql.go:10-19`）。
- 版本：`textnorm.Version=1`，`searchindex.ProjectionVersion=1`；版本不一致时后台按清单回填，发布前只读旧路径（`B/catalog/maintenance/initialize.go:29-37`，`search-contracts.md:21,37-45`）。
- 按代码推断：`acdc` 搜不到 `AC/DC`；`guns n roses` 搜不到 `Guns N' Roses`；`'` 与 `’` 互搜不命中。

## 需求

- **R1 去标点匹配键**：在现有规范化值之外，为每个投影值生成一个去掉标点与符号（Unicode P*、S* 类，含中文标点如 `·`、`《》`、`、`）的匹配键，空白按现规则折叠；查询同样生成两种键，任一命中即算匹配。去掉标点后为空的值不生成第二键。
- **R2 撇号统一**：`’`、`‘`、`ʼ`、`＇` 等撇号与 `'` 在规范化时统一。
- **R3 排名**：原规范化值命中的排名不变；只靠去标点键命中的，排在同等级原值命中之后。
- **R4 回填**：升规范化或投影版本，经现有后台回填流程重建投影，回填完成前沿用旧路径。
- **R5 合同同步**：更新 `search-contracts.md`。

## 验收

- 单测：键生成对照表（`AC/DC`、`Guns N' Roses`、`周杰伦·`、`《夜曲》`、全标点值）；查询命中与排名顺序。
- 回填：迁移或版本升级后投影重建完成、搜索结果正确（集成测试）。

## 不做

- 拼写纠正、分词、拼音检索（拼音目前只用于首字母分组）。

## 已裁决

- 2026-11-08 主会话裁决（设计审阅）：R2 的“统一撇号”放进去标点键里做，不改名字键算法（`textnorm.Version` 不变，避免人工意图与艺人收藏失联）；两种撇号互相能搜到，写法不同的排在写法完全相同的之后。

## 依赖

T 线第 4 项，在 ⑧ 之后（⑧ 往搜索投影里加作品字段，本项随后统一升版本回填一次）。
