# 自动整理 E1：执行计划

分支 `feat/auto-curation-e1`（主工作目录）。前端由主代理实现；后端一处（`auto_fields`）交给 Codex，实现与测试分属不同代理，与前端并行。

## 顺序

```text
B 后端 auto_fields（sol 实现）──► BT 测试与样本（sol）──┐
F1 接口层、解码器、契约、替身（主代理） ────────────────┼──► F3 详情标签（需要 BT 的样本） ──► 冒烟、真实服务、截图 ──► 规范、提交
F2 设置页、操作记录筛选、概览（主代理） ───────────────┘
```

### B 后端（`gpt-6.2-sol`）

允许修改：`backend/internal/catalog/read.go`、`fields.go`、`comparison.go`、`catalog/httpapi/handlers.go`（`auto_fields`，design 第九节 4）；`backend/internal/curation/suppressions.go`（成对清除，第九节 2）。不写测试。
验证：`go build ./...`、`go vet ./internal/catalog/...`。

### BT 后端测试（`gpt-6.2-sol`）

只改测试与样本：集成用例（只有自动值、自动值与人工修正并存、`null` 模式、没有时省略、对比接口；成对标记一起清除、单条的不受影响）；样本 `releases/get-auto`；`ROOMUSIC_UPDATE_API_SAMPLES=1` 生成新样本并确认现有样本逐项不变。
验证：`ROOMUSIC_TEST_RUN="<新用例>|TestPostgreSQLAPIResponseSamples"`。

### F1–F3 前端（主代理）

1. `features/curation` 的 api、decoders、query-keys、hooks；契约测试移入十个样本；`src/test/curation-data.ts` 与 `api-mock.ts` 登记。
2. 设置栏与五个区段、范围撤销对话框；操作记录的筛选与依据；概览的提示与状态行。
3. 详情的 `auto_fields` 标签、编辑对话框的说明、歌词来源。
4. 页面级测试、单元测试、冒烟与真实服务用例；1440 与 390 截图复核。

## 验证命令（范围上限，`frontend/` 下）

```bash
mise exec -- npx vitest run src/features/curation src/features/governance src/features/catalog src/app/curation-settings.test.tsx src/app/routes src/test/api-contract.test.ts
mise exec -- npm run -s typecheck && mise exec -- npx eslint src e2e
mise exec -- npx playwright test e2e/curation.spec.ts e2e/governance.spec.ts
# 真实服务：仓库根目录 scripts/playback-e2e.sh（按脚本说明只跑 curation.real.ts）
```

## 回退点

- 前端改动只新增栏目与标签；没有自动整理数据时专辑详情与操作记录和之前一样。
- 后端只加省略空值的字段。

## 启动前的检查

- [ ] 规划子代理审阅三份文档，意见记录在 `prd.md`。
- [ ] `implement.jsonl`、`check.jsonl` 登记规范与调研。
- [ ] `task.py start`，建分支。
