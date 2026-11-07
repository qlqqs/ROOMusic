# 执行计划：音乐目录浏览器

接口合同与裁决见 [design.md](./design.md)（第 2 节合同，第 8 节裁决）。不需要数据库迁移。后端在父任务执行计划的第 6 步开始，前端在第 7 步，都在 ui-minimal 提交之后。后端、前端和样本在同一个提交里（契约测试要求每个样本都有契约）。

## 分工

| 工作 | 执行者 |
| --- | --- |
| 后端：`library/folders.go`、`library/httpapi/folders.go`、路由注册，以及 `go vet` 和 `TestListFolders` | Sol（`codex/gpt-6.1-sol`，high，full-access，单独一个后端实施 agent） |
| 前端：解码器、接口、查询键、`useFolderListing`、`FolderBrowser`、`AddRootForm`，以及 typecheck、lint | Opus（`claude/claude-opus-5-5`，high） |
| 接口样本录制、Go 单元与样本集成测试、前端 Vitest 和 Playwright、截图 | Sol（`codex/gpt-6.1-sol`，high，full-access） |

## 步骤

1. **后端**（后端实施 Sol）：
   - 按 design.md 第 2、3 节实现 `ListFolders` 和 handler：起点、子文件夹、“已添加”标记、2000 条上限、错误统一为 `400 invalid_library_path`；
   - 写 `folders_test.go`（第 5 节的 ①–④，测试里的上限用参数传入）。
2. **样本**（Sol）：
   - 在 `TestPostgreSQLAPIResponseSamples` 录制 `library-folders/start`、`library-folders/child`、`errors/invalid-library-folder`，录制前把允许目录的临时路径换成 `/music`；
   - 加一次普通成员调用，断言 403。
   - 只用隔离的 `roomusic-test` 项目。
3. **前端**（Opus）：
   - 按 design.md 第 4 节实现，截断提示和路径框说明按第 8 节裁决；
   - 新增目录成功后让 `library/folders` 查询失效；
   - `npm run build` 更新 web 产物。
4. **前端测试**（Sol）：
   - Vitest：`library-roots.test.tsx` 加两例，`api-contract.test.ts` 样本表加三项，`decoders.test.ts` 加列表层的正例和反例；
   - Playwright：`e2e/library.spec.ts` 加一例“浏览并选择后添加”，在 1440 和 375 宽度各截一张图；
   - `fake-library.ts`、`api-mock.ts` 加路由。
5. 主会话审阅后端、样本和前端的 diff，一起提交：`feat(library): 添加目录时浏览并选择文件夹`。

## 验证命令

```bash
cd backend && go vet ./internal/library/... && go test ./internal/library/ -run TestListFolders -count=1
ROOMUSIC_TEST_RUN=TestPostgreSQLAPIResponseSamples ./scripts/test-integration.sh
cd frontend && npm run typecheck && npx vitest run src/app/library-roots.test.tsx src/test/api-contract.test.ts src/features/library/api/decoders.test.ts
cd frontend && npx playwright test e2e/library.spec.ts -g "浏览"
```

不跑全量：改动只在 library 一个接口和添加目录表单里。

## 回滚点

纯新增接口和组件。回退这一个提交即可，手动输入路径的旧流程一直可用。
