# G 实施

分支 `feat/auto-curation-g`。

1. 派发 1（astra）：撤销（R1、R2）——`catalog/governance/{artist_revert.go,artist_describe.go,artist_merge.go,artist_state.go}`、`app/app.go`、迁移 0066、艺人样本、规范 artist/governance。另派测试代理。
2. 派发 2（astra）：对应与自动合并（R3、R4）——`catalog/curation_artist_credits.go`、`curation/{external_link.go,artist_merge.go}`、`curation/program/{artist_links.go,enrich.go,pass.go}`、`governance/artist_merge.go`（`CurationMergeExecutor`）、`app/app.go`、规范 curation 第 19 节。另派测试代理。
3. 派发 3（sol）：管家工具与策略第 3 版（R5）——第 6 节文件与规范。测试代理同上一并。
4. 主代理：前端（R6、M8）。
最后：后端全量单元、集成全量串行一次；前端全量 Vitest、相关 Playwright。
