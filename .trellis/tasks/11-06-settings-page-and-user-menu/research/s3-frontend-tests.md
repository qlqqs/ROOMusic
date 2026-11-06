# S3 前端测试结果（2026-11-06）

## 首轮范围与结论（截图审阅调整前）

- 只修改前端测试及测试辅助代码；未修改产品代码、`backend/`、`frontend/e2e/real/`，未提交 git。
- 生产构建输出到 `/tmp/roomusic-s3-dist`，没有重建 `backend/cmd/roomusic/web`。
- 最终 Vitest：**23 文件通过，975 通过、0 失败、1 个原有条件跳过**。
- 最终模拟后端 Playwright：**347 通过、0 失败**；播放器菜单与检查工具回归：**12 通过、0 失败**。
- `npm run typecheck && npm run lint`、`git diff --check -- frontend` 通过。
- 按任务要求仅运行受影响文件，没有运行前端或后端全量测试。

## 测试文件清单

以下路径相对 `frontend/`。新增 5 个 Vitest 文件；其余 18 个为更新或追加。

- `src/app/account-password.test.tsx`
- `src/app/albums.test.tsx`
- `src/app/app.test.tsx`
- `src/app/artist-governance-step5.test.tsx`
- `src/app/claim-flow.test.tsx`
- `src/app/claims.test.tsx`
- `src/app/default-version.test.tsx`
- `src/app/issues.test.tsx`
- `src/app/library-roots.test.tsx`
- `src/app/library-scans.test.tsx`
- `src/app/login-throttle.test.tsx`
- `src/app/members.test.tsx`
- `src/app/operations.test.tsx`
- `src/app/routes/settings-sections.test.ts`（新增）
- `src/app/scan-feedback.test.tsx`
- `src/app/settings.test.tsx`（新增）
- `src/app/shell/nav-items.test.ts`
- `src/app/system-backups.test.tsx`
- `src/app/user-menu.test.tsx`（新增）
- `src/features/system/api/decoders.test.ts`（新增）
- `src/shared/ui/menu-button.test.tsx`
- `src/test/api-contract.test.ts`
- `src/test/system-data.test.ts`（新增）

模拟后端 Playwright 更新 11 个文件：

- `e2e/a11y.spec.ts`
- `e2e/auth.spec.ts`
- `e2e/claims.spec.ts`
- `e2e/default-version.spec.ts`
- `e2e/governance.spec.ts`
- `e2e/keyboard.spec.ts`
- `e2e/library.spec.ts`
- `e2e/members.spec.ts`
- `e2e/responsive.spec.ts`
- `e2e/scan-feedback.spec.ts`
- `e2e/system-backups.spec.ts`

测试辅助代码：

- `e2e/support/account-password.ts`：账号页的新入口与结果态场景。
- `e2e/support/checks.ts`：可见文字真实行数检查，排除图标与仅供读屏的文字。
- `e2e/support/settings.ts`（新增）：用户菜单与设置页面场景，供浏览器、响应式和 axe 共用。
- 复用实现 agent 已补齐的 `src/test/system-data.ts`、`fake-system.ts` 与 `e2e/support/api-mock.ts` 备份替身；S3 未修改这些文件的产品逻辑。

## 首轮最终验证命令与结果

以下测试命令在 `frontend/` 执行。

### Vitest 合跑

```bash
npx vitest run src/app/account-password.test.tsx src/app/albums.test.tsx src/app/app.test.tsx src/app/artist-governance-step5.test.tsx src/app/claim-flow.test.tsx src/app/claims.test.tsx src/app/default-version.test.tsx src/app/issues.test.tsx src/app/library-roots.test.tsx src/app/library-scans.test.tsx src/app/login-throttle.test.tsx src/app/members.test.tsx src/app/operations.test.tsx src/app/routes/settings-sections.test.ts src/app/scan-feedback.test.tsx src/app/settings.test.tsx src/app/shell/nav-items.test.ts src/app/system-backups.test.tsx src/app/user-menu.test.tsx src/features/system/api/decoders.test.ts src/shared/ui/menu-button.test.tsx src/test/api-contract.test.ts src/test/system-data.test.ts  --maxWorkers=2
```

结果：23 文件通过；975 通过，0 失败，1 跳过；耗时 115.28 秒。日志 `/tmp/s3-vitest-final.log`。

跳过的是已有 `src/test/api-contract.test.ts` 中仓外 10000 项艺人样本用例：没有设置 `ROOMUSIC_ARTIST_LARGE_SAMPLES_DIR`。未新增 skip，也未改动原 skipIf。备份新样本均正常运行并通过：`backup-deleted`、`backups-deleted`、`retention-confirmation-required`、`retention-saved`、`retention-saved-unlimited`，以及重录的 Overview 与新错误样本。

### 生产产物

```bash
npx vite build --outDir /tmp/roomusic-s3-dist --emptyOutDir
npx vite preview --outDir /tmp/roomusic-s3-dist --host 127.0.0.1 --port 4173 --strictPort
```

构建成功；最后一次构建已包含实现 agent 对用户按钮可访问名称的修复，日志 `/tmp/s3-build-final.log`。Playwright 复用该生产 preview，因此没有触发配置里默认写入后端嵌入目录的构建命令。测试完成后关闭本轮 preview。

### 模拟后端 Playwright 合跑

```bash
source /home/qlqq/.local/share/playwright-deps/env.sh
npx playwright test e2e/{auth,system-backups,keyboard,library,scan-feedback,members,governance,claims,default-version,a11y,responsive}.spec.ts --workers=4 --output=test-results/s3-final
```

结果：347 通过，0 失败，耗时 2.8 分钟。日志 `/tmp/s3-playwright-final.log`。

### 播放器菜单与检查工具回归

```bash
source /home/qlqq/.local/share/playwright-deps/env.sh
npx playwright test e2e/player.spec.ts e2e/checks.spec.ts --grep '空/有队列|不可播放曲目|纯键盘|检查工具自检' --workers=2 --output=/tmp/roomusic-s3-menu-regression
```

结果：12 通过，0 失败（播放器 7、检查自检 5）；耗时 42.7 秒。日志 `/tmp/s3-menu-regression-final.log`。播放器原用例未修改，覆盖五宽模式菜单与曲目“更多”的鼠标、键盘和禁用行为。

### 静态检查

```bash
npm run typecheck && npm run lint
# 仓库根目录
 git diff --check -- frontend
```

结果：均返回 0，无类型、lint 或空白错误。另核对 `git status --short -- frontend/e2e/real backend/cmd/roomusic/web` 无输出。

## 发现的问题与主会话裁决

1. 用户按钮原来把用户名与角色连读为 `curator管理员`：已报告并由前端实现 agent 修复，S3 未修产品；最终精确断言 `curator 管理员`，退出中精确断言 `curator 正在退出…`。
2. 旧地址 `%20` 规范化为 `+`：主会话接受编码等价。pathname、hash 逐字比较，查询用 `URLSearchParams.entries()` 逐项比较键、值、顺序，保留 replace / 后退 / 前进断言。
3. 重复查询键会被路由合并成 JSON 数组字符串：主会话明确不纳入本任务，已移除手工重复键用例。保留单值未知参数与全部业务参数；用例旁已写“重复键会被路由合并为数组，不在保证范围内。”。
4. 只读审查发现批删忙碌态、清理后的界面文件身份断言不足：已追加批删挂起请求、忙碌/防重复/取消与 Esc 约束，以及按下载 href 验证保留的具体文件。

**没有剩余的范围内产品缺陷。** 真实服务测试的旧交互迁移仍按计划留给 S4，不宣称已通过 A10 的真实服务部分。

## 调试阶段记录（不是最终结果）

- 备份最初局部 Vitest：30 通过、10 失败。原因是样本的秒级时间在界面格式化到分钟后同名，以及删除名称的空白格式；测试改为用下载 href 定位备份行后通过，未改产品。
- 后端样本重录后备份、decoder、契约测试：先 547 通过、1 原有跳过；补充规则同步和替身规则测试后 557 通过、1 原有跳过，最终合跑见上。
- 迁移后的原 15 文件：380 通过、0 失败；新增与迁移合跑一度 411 通过、6 失败，均为后来裁定不在范围内的重复键断言。
- 裁决前 23 文件合跑：968 通过、6 失败、1 原有跳过。移除重复键、补齐批删忙碌态后最终 975 通过、1 原有跳过。
- 浏览器初轮核心：95 通过、13 失败；修正测试入口、名称和单行度量后核心 42 通过。响应式/axe 初轮 234 通过、4 失败（375 隐藏身份区选择器、临时重建资产切换），修正辅助场景并在稳定产物上合跑后 347 通过；追加批删忙碌态后再次 347 通过。
- 曾在仓库根目录误执行 `npx vitest run src/app/system-backups.test.tsx`（40 失败，未加载前端 jsdom 配置）与 Playwright 命令（收集错误）；已返回 `frontend/` 使用项目锁定依赖复跑，未改项目依赖，根目录误生成的 `test-results/` 已清理。

## 验收映射

| 验收 | 前端自动化证据 |
| --- | --- |
| A1 | `app/system-backups.test.tsx` 单删确认、成功刷新、焦点与 404；`e2e/system-backups.spec.ts` 1440/375 单删。服务器文件实际删除与再删 404 由 S2 Go 集成测试证明。 |
| A1b | 同两文件的全选/半选、取消、批删忙碌、防重复、部分失败仅重试剩余、missing 成功、轮询裁剪选择；375 工具行可用。 |
| A1c | `app/system-backups.test.tsx` 初值/不限/范围错误/保存确认及取消/confirm 请求/实际数量/失败重试/规则损坏/最新三份 href；`test/system-data.test.ts` 两上限与最新一份；`features/system/api/decoders.test.ts` 新联合与字段范围；浏览器清理流程。真实重启/恢复后的持久性由 S2 负责。 |
| A3 | 单删取消不请求、503 错误留框可重试或取消、404 无错误刷新；Vitest 与 Playwright 均覆盖。 |
| A4 | `app/settings.test.tsx` 五栏/一栏、全部管理地址被 AdminGate 拦截且不发管理接口、默认跳账号；`app/routes/settings-sections.test.ts` 栏目顺序与地址；响应式五宽。 |
| A5 | `app/settings.test.tsx` 五旧地址查询/hash 等价保留、历史替换与前进后退、匿名登录回跳新地址；迁移后的 library/members/operations/governance/catalog 等原测试继续检查站内新 href。 |
| A6 | `app/shell/nav-items.test.ts` 精确分组与项目数组、艺人不可用时隐藏候选；`settings.test.tsx` 与 `auth.spec.ts` 主导航无四个配置入口。 |
| A7 | `app/user-menu.test.tsx` 头像/精确名称/菜单项/当前样式/键盘全流程/退出失败与重试；`shared/ui/menu-button.test.tsx` 上方位置、图标不入名称、dialog 内 portal；`auth.spec.ts` 真实浏览器几何和键盘。 |
| A8 | `app/user-menu.test.tsx` 从概览及当前账号页选设置后都关闭对话框；`auth.spec.ts` 375；`a11y.spec.ts`、`responsive.spec.ts` 在窄屏 dialog 内展开菜单。 |

## 布局与截图

- `e2e/system-backups.spec.ts` 在 1440/375 检查下载/删除同行、工具行同行、数字框/单位/不限同行、单行文字、无页面或确认框横向溢出、确认框不出界、axe 无 serious/critical。
- `responsive.spec.ts` 的设置场景覆盖 375/768/1024/1280/1440；`a11y.spec.ts` 补 1440/375。
- 截图共 14 张，位于 `frontend/test-results/settings-screenshots/`（忽略的本地测试产物）：设置账号、设置备份、备份删除确认框、备份批量选择、备份批量删除确认框、自动清理确认框、用户菜单，各有 1440 与 375。
- 已抽查账号 375、备份 375、用户菜单 1440/375、清理确认框 375：未见新增出界、错位或操作文字折行；最终人工审阅仍交主会话/用户。


## 截图审阅后补充验证（2026-11-06 14:04 UTC）

### 本轮改动边界

- 产品实现 agent 将账号页改为普通 `<dl>` 信息，并让窄屏菜单的用户区贴底；本轮只验证这些已完成的产品修改，不修改产品文件。
- 本轮新增断言只落在 `frontend/e2e/responsive.spec.ts`；账号信息、菜单和修改密码的既有 Vitest / Playwright 定位均继续通过，无须调整定位或弱化断言。
- `stat-list.module.css` 恢复统计组件默认字号后，账号页不再使用统计组件，测试以页面语义而非旧 CSS / StatList 结构定位。
- 报告更新为本节；未修改 `backend/`、`frontend/e2e/real/`，未提交 git。

### 新增回归断言

1. `expectAccountValueFontSize`：在 `main` 内核对两个 term（用户名、角色）及 definition 的数量和完整内容，确认可见且字号可测；每个值的字号不超过 `body` 正文字号的 1.2 倍。该检查随“设置账号”在全部五种宽度运行。
2. `expectDrawerUserAtBottom`：375px / 1000px 高的菜单打开场景，先确认对话框无需纵向滚动，再测量用户按钮实际底边到对话框底边的距离，要求在 0–24px；不依赖 `.drawerIdentity` 的实现样式值。
3. 两个自检用例仅向当前浏览器页面注入临时 CSS：分别将用户名、角色恢复为 32px，要求对应字号断言失败；取消用户区的自动上边距，要求菜单展开、折叠两种状态的贴底断言失败。移除临时 CSS 后重新检查通过，不改动任何产品文件。
4. 只读审查确认检查没有确定的空转或断言语义弱化问题。

### 本轮命令与结果

以下命令均在 `frontend/` 执行，未跑全量。

```bash
npx vitest run src/app/{settings,user-menu,app,account-password}.test.tsx src/shared/ui/menu-button.test.tsx --maxWorkers=2
```

**5 个文件通过，74 通过、0 失败、0 跳过**，耗时 15.02 秒。日志 `/tmp/s3-visual-review-vitest-final.log`。覆盖指定四个 app 测试以及共享菜单在对话框内的回归。

```bash
npx vite build --outDir /tmp/roomusic-s3-review-dist --emptyOutDir
npx vite preview --outDir /tmp/roomusic-s3-review-dist --host 127.0.0.1 --port 4173 --strictPort
```

新生产构建成功，输出为独立临时目录，不重建后端嵌入资源。日志 `/tmp/s3-visual-review-build.log`。浏览器测试复用本轮 4173 preview；结束后关闭本轮 preview。

```bash
source /home/qlqq/.local/share/playwright-deps/env.sh
unset CI
npx playwright test e2e/{auth,responsive,a11y,keyboard}.spec.ts --workers=4 --output=test-results/s3-visual-review
```

**270 通过、0 失败、0 跳过**，耗时 1.5 分钟，包含新增两个断言自检。日志 `/tmp/roomusic-s3-visual-review.log`。

```bash
source /home/qlqq/.local/share/playwright-deps/env.sh
unset CI
npx playwright test e2e/responsive.spec.ts --grep '设置样式回归断言自检' --workers=4 --output=test-results/s3-visual-review-selfcheck-final
```

**2 通过、0 失败**。补跑验证最终的临时 CSS 清理代码版本；此前门禁发现 `Node.remove()` 类型不合法，已只在测试中改用 `parentNode?.removeChild(element)`，未修改产品代码。

```bash
npm run typecheck && npm run lint
# 仓库根目录
 git diff --check -- frontend
```

**均返回 0**。初次类型检查发现的上述两个测试类型错误已经修正；最终检查干净。

### 截图更新与复核

- `frontend/test-results/settings-screenshots/` 下 14 张截图全部重新生成；逐个对比本轮前后的 `mtime`，14/14 均更新，生成时间为 **2026-11-06 14:03:14–14:03:21 UTC**。
- 截图集合不变：设置账号、设置备份、备份删除确认框、备份批量选择、备份批量删除确认框、自动清理确认框、用户菜单，每种场景均有 1440 与 375。
- 已抽查新截图“设置账号-375.png”“用户菜单-375.png”：账号值恢复正文尺寸，用户按钮贴近抽屉底部且展开菜单位于按钮上方，没有新增出界或错位。
- 本轮没有发现产品缺陷。最终 14 张图片继续留在上述目录供主会话和用户审阅。
