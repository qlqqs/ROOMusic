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

- [x] 集成测试辅助代码提供可取消的扫描上下文并在清理时等待 worker；新增 `TestPostgreSQLAPIResponseSamples`（design.md 第 9 节的流程、规范化、比较与
      安全断言），生成 `backend/internal/app/testdata/api-samples/`。
- [x] `features/auth`、`members`、`library`、`catalog` 的 `api/` decoder 与 DTO；`src/test/api-samples.ts` 与契约测试；
      各 decoder 的反例测试。
- [x] 验证：`gofmt -l .`、`go vet ./...`、`go test ./internal/app/... -count=1`；
      `ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh` 生成样本后，再连续两次 `./scripts/test-integration.sh`
      确认比较模式通过且样本稳定；前端 `npm run lint`、`npm run typecheck`、`npm run test`。
- [x] 人工检查样本：无绝对路径、密码、Cookie，内容与后端合同一致。
- [x] 提交：`test(app): 用真实响应生成 API 样本，前端 decoder 以样本校验`。

## 第 3 步：认证、外壳与概览（首个可用界面）

- [x] `src/main.tsx`、`app/providers.tsx`、`app/router.tsx`、外壳（侧栏、窄屏导航、身份区）、无权限 / 不存在 / 服务不可用页面。
- [x] 会话控制器：启动、初始化后自动登录、登录跳回、退出、401 清理、代次守卫、跨标签页广播与重新可见核对。
- [x] 概览四个区块及其查询 hook。
- [x] CI 前端作业删除入口检测与占位页分支，测试命令改为 `npm run test`。
- [x] 验证：`npm run lint`、`npm run typecheck`、`npm run test`（23 个文件 342 个测试）、`npm run build`；`go build ./...`（内嵌新产物）；
      连续两次构建的 119 个产物文件逐字节一致（CI 漂移检查可行）。
      计划写的是“vite 开发服务连隔离后端”，实际改为更严格的做法：用**生产构建产物**连隔离后端（项目的 `compose.test.yaml`
      PostgreSQL + 临时目录 + 本地编译的后端二进制，同源托管内嵌产物），在真实 Chromium 中走完：全新库进入初始化页 →
      口令规则 → 创建并自动登录 → 刷新 → 已登录访问 `/login`、`/setup` 回概览 → 退出 → 错误密码 → 登录 → 另一标签页退出本页跟随 →
      清 Cookie 后重新可见触发核对，回到登录页并提示“登录已失效”；会话 Cookie 为 `HttpOnly; SameSite=Lax`；没有第三方请求。
      用样本假后端另检 375 / 768 / 1024 / 1280 / 1440 五个宽度无横向溢出，9 个页面状态（登录、初始化、概览、扫描运行中、
      非管理员、不存在、服务不可用、窄屏菜单）axe（wcag2a/2aa/21a/21aa/22aa）零违规。验证用的脚本与资源都在 `/tmp`，不入库，用后已清理。
- [x] 提交：`feat(frontend): 认证、会话生命周期、应用外壳与概览`（含首次内嵌产物与字体文件）。

## 第 4 步：扫描目录与扫描详情

- [x] 目录列表、新增、停用确认、恢复、409 与结果未知（同键重试）的处理；目录操作记录。
- [x] 活动扫描卡片、开始扫描、扫描详情的轮询与停止、取消、诊断汇总与明细；路由 `/library`、`/library/scans/$scanId`，
      导航“资料库 / 扫描目录”（仅管理员），概览里的入口链接。
- [x] 验证：`npm run lint`、`typecheck`、`test`（28 个文件 416 个测试，其中新增 `src/app/library-roots.test.tsx` 27 个与
      `library-scans.test.tsx` 33 个页面级测试，覆盖 409 / 404 / 401 / `idempotency_conflict`、请求丢失与响应丢失后的同键重试、
      假定时器下的轮询与终态停止、页面隐藏暂停、取消、诊断、非管理员不请求管理接口）、`npm run build`、`go build ./...`。
      用生产构建连隔离后端（`compose.test.yaml` PostgreSQL + 临时目录里的合成素材：一个损坏的 `.flac`、一个损坏的 `.cue`、
      一个悬空符号链接）在真实 Chromium 中走完：路径被拒绝 → 添加三个目录 → 重复添加 → 停用确认（Esc 关闭、焦点回到触发按钮）→
      停用 / 恢复 → 开始扫描 → 详情页轮询到真实的 `incomplete` 终态与三类诊断（中文类别名，路径只显示相对路径）→ 返回后可再次开始；
      五个宽度无横向溢出，`/library` axe 零违规，没有第三方请求。运行中、取消请求中、失败等状态用样本桩在真实页面上截图检查。
      验证用的脚本与资源都在 `/tmp`，不入库，用后已清理。
- [x] 提交：`feat(frontend): 目录与扫描管理`。

## 第 5 步：成员管理

- [x] 成员列表、新建、停用、重新启用、撤销会话；针对自己的提示与 401 流程；`last_admin`；结果未知时刷新核对。
      路由 `/members`，导航“资料库 / 成员”（仅管理员），概览的成员区块有“管理成员”入口。
- [x] 验证：`npm run lint`、`typecheck`、`test`（29 个文件 443 个测试，其中新增 `src/app/members.test.tsx` 27 个页面级测试）、
      `npm run build`、`go build ./...`。用生产构建连隔离后端在真实 Chromium 中走完：概览入口 → 空用户名提示 → 新建成员 →
      重复用户名 → 成员登录（看不到管理导航，直接访问成员页显示需要管理员权限）→ 管理员撤销其会话后对方刷新即回到登录页 →
      停用后对方无法登录 → 重新启用后可以登录 → 停用自己（最后一个管理员）被 409 拒绝且会话保留 → 撤销自己的会话回到登录页；
      五个宽度无横向溢出，`/members` axe 零违规，没有第三方请求。验证脚本与资源在 `/tmp`，不入库，用后已清理。
- [x] 提交：`feat(frontend): 成员管理`。

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

## 实施中的发现（第 7 步据此同步文档）

- 路由：TanStack Router 把 `validateSearch` 的返回值合并到原始查询串上（`match.search = { ...原始, ...校验结果 }`），校验函数只省略键时
  不合法的原值仍留在 `search` 里并被 `beforeLoad` 读到；必须显式返回 `undefined`（`/login` 的 `redirect` 已修，有回归测试）。写进前端规范的路由一节。
- 数字：Cormorant Garamond 默认是旧式数字（“1”像罗马数字 I，“3”下沉），`tabular-nums` 单独使用不改变这一点；统计值与后续的表格、时长用
  `lining-nums tabular-nums`，标题里的数字保持字体默认。DESIGN.md“数字”一行据此改写。
- 测试：jsdom 没有 `window.scrollTo`，路由滚动恢复会在每次导航后刷出“Not implemented”，已在 `src/test/setup.ts` 桩住。
- 浏览器：未登录时 `GET /auth/me` 返回 401 是合同行为，浏览器控制台会记一条资源错误；第 6 步的控制台断言要排除这类预期的资源状态。
  本机缺 Chromium 的系统库（`libatk` 等）且无 root 时，可 `apt-get download` 后 `dpkg -x` 到 `/tmp`，用 `LD_LIBRARY_PATH` 运行；CI 用
  `npx playwright install --with-deps chromium`。Playwright 1.63 要求 chromium 修订 1243，本机缓存是 1234，第 6 步需要 `npx playwright install chromium`。
- 产物：构建后 CSS 约 118 KB（gzip 43 KB），主要是 Noto Serif SC 约 120 个 `unicode-range` 分片的 `@font-face`，字体文件按需下载；
  JS 约 337 KB（gzip 108 KB）。内嵌目录共 119 个文件、约 6.8 MB。
- 后端静态文件（M1a 不改，记入路线遗留项）：`httpserver.Frontend` 不设缓存头，带哈希文件名的资源与较大的字体每次都要重新协商；
  对 `/assets/`、`/licenses/` 这类目录路径会返回目录列表。
- 第 4 步与设计的差异：
  - 开始扫描返回 200（已有扫描在运行）时不新建也不跳转，留在扫描目录页提示“已有扫描在运行”并链接到那个扫描；只有 202（新建）才进入详情页。
    PRD R6 写作“开始扫描后进入该扫描的详情页；返回 200 时提示”，这样理解：用户本来就在能看到活动扫描摘要与详情链接的页面上。
  - 活动扫描（`/scans/active`）在确有扫描时每 5 秒刷新一次（页面隐藏时暂停），没有活动扫描时不轮询；设计只写了详情页轮询。
    否则扫描目录页与概览在扫描结束后会一直显示“运行中”，也不会重新出现“开始扫描”。
  - 表格以容器宽度（CSS 容器查询，小于 640px）而不是视口宽度决定是否堆叠成“标签：值”，因为带侧栏时 768px 视口里内容区只有约 500px；
    不产生任何横向滚动（局部滚动区需要可聚焦，会引入额外的无障碍负担）。`DataTable` 进入 `shared/ui`（目录、操作记录、诊断、后续成员页共用）。
  - `shared/api` 新增 `isOutcomeUncertain`：网络失败、无效响应与 5xx 视为“结果不确定”，4xx 是明确的拒绝；目录写操作据此决定能否用同一个幂等键重试，
    第 5 步的成员写操作据此决定是否刷新核对。
  - 测试发现并修复的缺陷：诊断类别中文名表用对象字面量查找时，类别 `constructor` 会取到对象原型上的方法，改为 `Map`。
  - 测试里的目录与扫描后端替身（`src/test/fake-library.ts`）按后端源码实现幂等重放、`expected_revision` 与 409、新增已登记路径返回已有目录，
    并可注入“请求丢失 / 响应丢失”，种子数据取自真实样本。它依赖 vitest，不能被 Playwright 直接复用；第 6 步的 `e2e/support/api-mock.ts` 需要同样的行为（幂等、revision、扫描状态机），按同一份行为定义另写，响应同样取自样本。
- 第 5 步的发现与取舍：
  - 成员写操作没有幂等键，所以界面没有“重试”：结果不确定时刷新列表、说明“请核对成员的当前状态，再决定是否重新操作”，对话框在命令结束时关闭，
    由页面上的提示说明结果；列表之外的核对手段是用户名唯一（响应丢失后重新提交会得到“用户名已存在”，这本身就是确认）。
  - 对自己的停用或撤销会话成功后，后端已撤销当前会话，随后的列表刷新得到 401，由会话边界回到登录页（提示“登录已失效”）；对话框里先提示“你会被立即退出登录”。
    停用最后一个管理员由服务器以 409 `last_admin` 拒绝，界面不用本地的角色判断去预先禁用按钮，只在管理员目标的对话框里先说明这一点。
  - 已停用的成员只提供“重新启用”：停用时其会话已被撤销，没有会话可撤。新建表单的账号字段用 `autocomplete="off"` / `new-password`，避免浏览器把当前管理员的凭据填进来。
  - `roleLabels` 提到 `features/auth` 的公共导出（身份区与成员列表共用）；测试替身的后注册处理器（含正则路径）覆盖先注册的。
  - 后端缺口（第 7 步记入 design.md 第 13 节）：`createUser` 把任何插入失败都映射成 409 `username_taken`；`invalid_input` 的文案写“12 个字符”而规则按字节；
    `PATCH /users/{id}` 与撤销会话对格式不对的 ID 返回 503（UUID 转换错误）而不是 400；`/users` 的读取失败会被静默跳过（design.md 已记）。
