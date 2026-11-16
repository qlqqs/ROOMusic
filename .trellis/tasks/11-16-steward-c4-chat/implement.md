# 管家 C4 执行计划

前置：用户批准本任务的规划摘要后 `task.py start`，在分支 `steward-c4-chat` 上做。
后端两处改动交 Codex 子代理（跨运行时与接口，`gpt-6-astra` high），测试交另一个子代理；子代理不提交 Git。前端交 Opus 5.5（high）子代理实现（用户 2026-11-16 指定），主代理审阅与验收。

## 步骤

- [x] 1 后端：名字进系统提示词；继续接口（路由、前置检查、错误码 `nothing_to_continue`）。
- [x] 2 后端测试（另一个子代理）与样本 `continue-nothing.json`；规范第 4、7、10 节同步。
- [x] 3 前端依赖：加 `react-markdown`、`remark-gfm`，核对版本与新增的间接依赖，记进执行记录。
- [x] 4 前端数据层：解码器、接口、SSE 解析、归并函数、运行 store，及其单元测试；契约测试改用解码器。
- [x] 5 对话界面：回合、过程条目、Markdown 回答、结束说明、输入框与模型菜单、会话列表、未配置引导。
- [x] 6 外壳：侧栏入口与面板、整页路由、标签栏第五项、成员开关关闭时隐藏。
  - 评审点：做完后截图给用户确认布局，再继续第 7 步。
- [x] 7 设置页 Agent 栏目：名字、模型与检测、令牌并入、管理员的成员段；`/settings/tokens` 重定向。
- [x] 8 组件测试、端到端冒烟、390px 与 1024px 的浏览器核对。
- [x] 9 文档：前端规范补管家一节；`frontend/DESIGN.md` 的 Steward 条目；父任务执行计划勾选。

## 验证

- 后端：`mise exec -- go test ./internal/steward/runtime/... ./internal/steward/httpapi/... ./internal/app/...`，后两个用 `-run`
  收窄到继续接口、系统提示词、路由与样本的用例。C2、C3 已通过且未改动的用例不重跑。
- 前端：`tsc --noEmit`、改动文件的 lint、新增与受影响的测试文件（`steward*`、`settings*`、`tokens`、`nav-items`、`tab-bar`、`api-contract`）。
- 端到端只跑新增的冒烟用例与改过的 `auth.spec.ts`。
- 集成测试用独立临时目录、唯一 Compose 项目名、合成数据和本地假模型端点，不调用真实模型。

## 风险点

- `app-shell` 的栅格是全站共用的：加面板列时不改现有断点下没有面板时的布局，用现有的外壳测试核对。
- `settings-sections.ts` 的路由类型与 `e2e/auth.spec.ts` 的栏目清单要同步改。
- `package.json`、锁文件是共享文件，只新增两个依赖；现有依赖版本被连带改动时停下来确认。
- 标签栏从四项变五项：核对 390px 与 320px 下不换行。

## 启动前检查

- [x] 用户批准本任务的最终规划摘要（2026-11-16）。
- [x] `implement.jsonl`、`check.jsonl` 已有条目。

## 执行记录

- 2026-11-16 步骤 1–2：后端实现与测试由两个 Codex 子代理完成；测试代理汇报收窄后的 `go test`（名字提示词、继续接口、路由与样本）通过，主代理未重跑。
- 2026-11-16 步骤 3：`react-markdown` 10.1.0、`remark-gfm` 4.0.1（精确版本）。锁文件新增 96 个包（2 个直接、94 个间接），已有包版本未变；
  6 个已有包去掉 `dev` 标记（`@types/estree`、`@types/react`、`csstype`、`debug`、`dequal`、`ms`）。
- 2026-11-16 步骤 4–6：Opus 5.5（high）子代理实现。它汇报 `tsc --noEmit`、改动文件 lint、19 个相关测试文件（847 通过、1 个原有跳过）通过。
- 2026-11-16 主代理核对：生产构建里 Markdown 依赖原先落进首屏的 `vendor` 块；在 `vite.config.ts` 加 `markdown` 分组后重新构建，
  `markdown` 块 164 kB 只由对话界面引用，`vendor` 为 44 kB；改后 `tsc --noEmit` 通过。
- 2026-11-16 布局评审截图（真实界面、合成数据、受控接口）在 `temp/steward-design/real/`，待用户确认后做第 7 步。
- 与设计的出入（子代理决定，待用户过目）：设置读取成功才显示入口（设计只写 403 时隐藏）；展开到整页时收起面板；
  删除正在回答的会话时确认框改为“停止回答”；会话列表只读最近 100 个；入口图标用 lucide `Loader`。
- 2026-11-16 独立审查（trellis-check 子代理，只读）：未发现密钥泄露、跨用户读取或 Markdown 注入；后端继续接口无问题；
  前端 6 个确认缺陷与若干疑点，共 12 项交回前端子代理修复并补测试。它汇报 `tsc --noEmit`、改动文件 lint 通过，
  `src/features/steward`（47 个用例）与 `steward.test.tsx`、`tab-bar.test.tsx`、`use-conversations.test.ts`（56 个用例）通过；
  `app-shell.tsx` 加了 Esc 监听，其余外壳测试文件未重跑。未处理：模型菜单条数上限、`SseParser` 的 `\r\n` 跨块、名字只过滤换行与「」。
- 2026-11-16 用户确认布局后做步骤 7–8（前端子代理）：设置页 Agent 栏目、`/settings/tokens` 重定向、组件测试与 `e2e/steward.spec.ts`。
  它汇报 `tsc --noEmit`、改动文件 lint、10 个测试文件 129 个用例通过。主代理在 Playwright 容器里运行
  `e2e/steward.spec.ts`（3 条）与 `e2e/auth.spec.ts`（22 条），全部通过；A11 的 390px 对话由冒烟用例覆盖，1024px 由评审截图核对。
  之后主代理给检测结果区补了内边距（`settings.module.css` 的 `.probe`），纯样式，未重跑测试。
- 成员开关关闭时成员保留令牌一组：用户 2026-11-16 同意保留（PRD D6）。
- 未做的验证：A2（运行中切页面不中断）与 A9（模型回答名字）没有在真实浏览器或真实模型上核对，前者由组件测试覆盖，后者由后端测试核对提示词；
  全量前端测试、后端 `-race` 未运行；没有接触真实模型端点。
- 2026-11-16 步骤 9：前端子代理新增 `.trellis/spec/frontend/steward-guidelines.md`，更新规范索引、目录结构与 `frontend/DESIGN.md`；主代理更正 `frontend/DESIGN-components.md` 的 Steward 一行。
- 2026-11-16 第二轮独立审查（设置页与上轮修复，只读）：未发现密钥进浏览器存储、URL、日志或界面文案；2 个确认缺陷
  （改接口类型后检测结果消失、失败检测报告残留）、1 个按时序推断的疑点（会话过期时草稿留给下一个登录用户）与若干小项，共 12 项交前端子代理修复。
  它汇报 `tsc --noEmit`、改动文件 lint、`agent-settings`、`steward`、`src/app/shell` 共 7 个文件 104 个用例通过。
  主代理修后运行：`e2e/steward.spec.ts` 3 条通过；`src/features/steward/run` 2 个文件 20 个用例通过（替身改过）。`e2e/auth.spec.ts` 沿用修复前的结果。
