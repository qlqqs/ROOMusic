# 执行计划

分支 `ui-review-followups`，工作目录为仓库根目录。

## 后端（Codex，gpt-6.2-sol，实现与测试分开）

1. 实现代理：design §1、§2 后端、§3；只改 `backend/internal/catalog/ingest/`、`backend/internal/catalog/artists_collections.go`、`backend/internal/catalog/httpapi/artist_reads.go`、`backend/internal/library/parser/`（含 version.go）、`.trellis/spec/backend/` 相关文档；可运行既有测试，不写新测试。
2. 测试代理：补测试——organizer 混合目录无专辑标签曲目标题为“未知专辑”、整目录无标签仍归组；m4a AAC 无位深、ALAC 位深不变；`appears_on` 的集合、roles、q、分页、非法值（store 层集成测试 + httpapi）。
3. 验证：`go test ./internal/catalog/ingest/... ./internal/library/parser/...`；集成测试用 `ROOMUSIC_TEST_RUN` 收窄到艺人专辑与归组相关用例。

## 前端（主代理）

4. §4 音量条、§5 管家标签、§2 前端栏目；更新对应单元测试与 e2e（player、responsive、artists 相关）。
5. 验证：`npm run typecheck`、`npm run lint`、受影响的 vitest 文件、受影响的 Playwright 用例。

## 收尾

6. 更新 `frontend/DESIGN-components.md`/前端规范中播放栏音量的描述；提交、合并回本地 main、归档任务。不推送。
