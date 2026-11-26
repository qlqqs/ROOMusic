# 模型巡检与本地整理

## 入库身份与显示名

扫描在 `backend/internal/catalog/ingest/artist_projection.go` 的 persistArtistEvidence 保存署名证据，`backend/internal/catalog/ingest/persist.go` 在提交候选的同一事务调用 artistgraph.Refresh 刷新投影。`backend/internal/catalog/internal/artistgraph/names.go` 的 nameResolver.ensure 对原文调用 textnorm.Text，按规范化版本和键哈希查询 artist_name_keys，同时比较完整键。只有新键才创建 artists 和 initial_artist_id。同键的各个原文单独保存在 artist_names。因此，同名的不同人默认归入同一个本地身份，这是已知局限。现有人工绑定与作用域路由继续决定具体署名归属。

artistgraph/profiles.go 的 ownedNames 经默认路由、人工绑定、重定向和有效署名枚举合并双方的原文，并为搜索生成全部名字。显示名沿用可见发行及曲目的去重计票与人工覆盖顺序，合并平票时接入外部主名称。

## 巡检链路

在已有模型调度的首档生产稳定库内名字的分块任务。艺人名附专辑样本，各艺人的发行名列表独立分块。持久化输入标识、规范身份、置信度、模型、提示词版本和时间，以当前名字与已处理输入比较增量。手动重新巡检使已有输入重新取得资格。模型与提示词版本只用于追溯。名录数据经 JSON 编码，沿用可行性提示词与提交结果工具。

规范身份按原文和拉丁写法分开保存并规范化，跨块匹配，low 跳过。候选交 QueueVerification，由 E 复核。程序查询接入 CanonicalNames 和 NamesPatrolled。巡检存储、后台工具与管理员路由分别放在 curation 及 app 现有边界，表与约束使用增量迁移。

## 本地整理

从扫描来源判定逐字段资格，按目录给出文件名、目录名、现有标签和待整理字段。提交端重读输入，仅接受规范化后出现在单个本地片段内的字段值。沿用受治理写入、自动层级别 1 及已有版本保护。曲序与碟号没有提交入口。模型整理值作为推断的检索输入，沿用时长要求。
