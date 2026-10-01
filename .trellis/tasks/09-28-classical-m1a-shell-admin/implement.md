# M1a 执行计划

需求见 [prd.md](prd.md)，设计见 [design.md](design.md)。每一步单独提交到 `main`，不推送；提交信息末尾带约定的
Co-Authored-By 行。改动前端源码的提交都重新构建并一起提交 `backend/cmd/roomusic/web`。

## 启动前

- [ ] 用户批准最终规划摘要。
- [ ] 工作区干净；`python3 ./.trellis/scripts/task.py start .trellis/tasks/09-28-classical-m1a-shell-admin`。
- [ ] 提交规划文档：`docs(task): 规划 M1a 外壳与管理界面`。

## 第 1 步：依赖与脚手架（无应用入口）

- [x] `package.json`：全部直接依赖改为 design.md 第 2 节的精确版本，构建工具移入 devDependencies；脚本 `test` 改为
      `vitest run`，新增 `test:e2e`。用 `npm install` 更新 lock，再用 `npm ci` 确认可重复安装。
- [x] 复核 TypeScript 6 与 TanStack Router、`exactOptionalPropertyTypes` 的兼容性；不兼容时按 prd.md 风险条处理并记录。
      结果（2026-10-01）：TypeScript 6.0.3 + Router 1.170.40 + Query 5.104.0 在 `strict`、`noUncheckedIndexedAccess`、
      `exactOptionalPropertyTypes` 下用代码定义路由（根上下文、无路径布局的守卫、`validateSearch`、`$scanId` 参数）通过类型检查，
      无需放宽任何选项；注意无路径布局的 `id` 会进入子路由 ID，参数 hook 用路由对象的 `useParams()`。
- [x] `tsconfig.json`（新增严格选项、包含 `src`、`e2e` 与配置文件）、`eslint.config.js`（react-hooks 推荐规则、Node 环境文件）、
      `vite.config.ts`（Vitest：jsdom、setup 文件、只收集 `src/**/*.test.ts(x)`）、`.gitignore`。
- [x] `src/styles/`：tokens、base、fonts；`public/licenses/` 放三套字体的 OFL 文本与来源说明。
- [x] `shared/api`（请求层、错误分类、解码原语、错误信封 decoder）、`shared/lib`（请求 ID、幂等键、时间格式化）、
      `shared/ui` 基础组件，均带单元测试。
- [x] 验证：`npm ci`、`npm run lint`、`npm run typecheck`、`npm run test`。
- [x] 提交：`build(frontend): 精确锁定依赖并建立 Classical 脚手架与请求层`。

## 第 2 步：接口样本与全部 decoder

- [ ] 集成测试辅助代码允许传入扫描上下文；新增 `TestPostgreSQLAPIResponseSamples`（design.md 第 9 节的流程、规范化、比较与
      安全断言），生成 `backend/internal/app/testdata/api-samples/`。
- [ ] `features/auth`、`members`、`library`、`catalog` 的 `api/` decoder 与 DTO；`src/test/api-samples.ts` 与契约测试；
      各 decoder 的反例测试。
- [ ] 验证：`gofmt -l .`、`go vet ./...`、`go test ./internal/app/... -count=1`；
      `ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh` 生成样本后，再连续两次 `./scripts/test-integration.sh`
      确认比较模式通过且样本稳定；前端 `npm run lint`、`npm run typecheck`、`npm run test`。
- [ ] 人工检查样本：无绝对路径、密码、Cookie，内容与后端合同一致。
- [ ] 提交：`test(app): 用真实响应生成 API 样本，前端 decoder 以样本校验`。

## 第 3 步：认证、外壳与概览（首个可用界面）

- [ ] `src/main.tsx`、`app/providers.tsx`、`app/router.tsx`、外壳（侧栏、窄屏导航、身份区）、无权限 / 不存在 / 服务不可用页面。
- [ ] 会话控制器：启动、初始化后自动登录、登录跳回、退出、401 清理、代次守卫、跨标签页广播与重新可见核对。
- [ ] 概览四个区块及其查询 hook。
- [ ] CI 前端作业删除入口检测与占位页分支，测试命令改为 `npm run test`。
- [ ] 验证：`npm run lint`、`npm run typecheck`、`npm run test`、`npm run build`；`go build ./...`（内嵌新产物）；
      本地 `vite` 开发服务连隔离后端手动走一遍登录与退出。
- [ ] 提交：`feat(frontend): 认证、会话生命周期、应用外壳与概览`（含首次内嵌产物与字体文件）。

## 第 4 步：扫描目录与扫描详情

- [ ] 目录列表、新增、停用确认、恢复、409 与结果未知（同键重试）的处理；目录操作记录。
- [ ] 活动扫描卡片、开始扫描、扫描详情的轮询与停止、取消、诊断汇总与明细。
- [ ] 验证：同第 3 步的前端命令与 `go build ./...`。
- [ ] 提交：`feat(frontend): 目录与扫描管理`。

## 第 5 步：成员管理

- [ ] 成员列表、新建、停用、重新启用、撤销会话；针对自己的提示与 401 流程；`last_admin`；结果未知时刷新核对。
- [ ] 验证：同第 3 步。
- [ ] 提交：`feat(frontend): 成员管理`。

## 第 6 步：浏览器冒烟、无障碍与真实接口冒烟

- [ ] `playwright.config.ts`、`e2e/support/api-mock.ts` 与各场景；五种宽度的横向溢出断言、axe 扫描、键盘路径、
      无第三方请求断言；按发现的问题修整样式与语义。
- [ ] CI 前端作业增加安装 Chromium 与 `npm run test:e2e`；Makefile 增加 `frontend-e2e`、`frontend-install` 改用 `npm ci`。
- [ ] 真实接口冒烟（design.md 第 10 节）：隔离 PostgreSQL 与临时目录，走 A2 主路径；命令与结果写入
      `research/real-api-smoke.md`，用后清理容器、目录与临时文件。
- [ ] 验证：`npx playwright install chromium`、`npm run test:e2e`，以及第 3 步的前端命令。
- [ ] 提交：`test(frontend): Playwright 冒烟、无障碍与响应式检查`。

## 第 7 步：文档同步与收尾

- [ ] 前端规范（index、directory-structure、state-management、type-safety、quality-guidelines、hook-guidelines、
      component-guidelines）、后端 `core0-runtime-contracts.md` 的 CI 与样本说明、ADR 0001 决策记录（第 4、5 项及字体、测试工具）、
      DESIGN.md（字体、字号、断点与第 10 节状态）、README、Core 1 路线与架构图、父任务 prd/implement、M1b PRD。
- [ ] 本任务 prd.md 的实施结果与验收对照；如实施偏离设计，同步 design.md。
- [ ] 最终验证：按 CI 顺序执行前端全部命令（含漂移检查与 e2e）；`./scripts/test-integration.sh`；`git diff --check`；
      检查改动文档的相对链接。
- [ ] 提交：`docs: 同步 M1a 的前端规范、ADR 与路线`；随后归档任务并修正归档后的相对链接。

## 风险文件与回退点

- `frontend/package.json`、`package-lock.json`：依赖组合不可用时整体回退第 1 步，不单独降某个包。
- `backend/cmd/roomusic/web/`：只由构建生成，不手改；回退源码时一并回退产物。
- `.github/workflows/ci.yml`：第 3、6 步修改，回退时与对应源码一起回退。
- 后端只改测试与 testdata（第 2 步），回退不影响运行；没有数据库迁移。
- 每步提交后都是可用状态，按逆序 `git revert` 即可回到上一步。
