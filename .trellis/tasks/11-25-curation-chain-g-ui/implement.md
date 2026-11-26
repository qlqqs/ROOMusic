# 整理链 G：实施

工作目录与分支：Orca 工作目录，分支 `task/curation-g-ui`，基线 `main`。纯前端加用户文档，后端接口已具备（分类计数 A、操作记录按来源筛选、重新巡检 D、设置与进度的新字段 E、队列进度 B）。

1. Claude 子代理实现前端与单元测试、更新受影响的端到端用例与用户文档。
2. 主代理审阅改动、跑相关的前端测试、提交、合回 `main`。

验证：`cd frontend && mise exec -- npm run -s typecheck`、`npx eslint` 改动文件、`npx vitest run` 整理与治理相关目录、受影响的端到端用例。
