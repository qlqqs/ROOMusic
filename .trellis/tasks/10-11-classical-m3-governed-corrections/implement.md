# M3 执行计划

需求见 [prd.md](prd.md)，设计见 [design.md](design.md)。每一步单独提交到本地 `main`（中文提交信息，不推送）；
改动前端源码的提交同时重新构建并提交 `backend/cmd/roomusic/web`；提交前用 `git checkout-index -a --prefix=<临时目录>/` 导出暂存区，
在完整文件树上跑该步的验证，`git diff --cached --check` 干净，不用 `--amend`。数据库测试只用 `roomusic-test` Compose 项目
（`--env-file /dev/null`、绝对路径的 compose 文件、空闲端口、随机库名，结束 `down -v`），不碰用户自己的 `roomusic` 项目；
自动化只用合成素材，不读真实 `music/`。

## 步骤

- [x] **1. 迁移、操作记录与有效值视图（无行为变化）**
  - 迁移 0017：`operations`、`releases.edit_revision`、`release_corrections`、视图 `release_effective`（design §2–3）。
  - 新包 `internal/operations`：`Run`（advisory lock、重放、冲突、执行器、记录、提交）、`Problem`（错误到状态码与错误码）、`Fingerprint`、`Load`、`List`；依赖规则测试加入新包。`Kind`（撤销与展示）接口随第一个使用者（第 4 步）定义，不提前造没有调用方的抽象。
  - catalog 读取切换到 `release_effective releases`；“需要关注”排除有修正的字段；专辑艺人署名按修正替换。
  - 门禁：没有修正时输出不变——现有 catalog、按组、播放历史与接口样本测试不改一行即通过；`operations` 用测试执行器覆盖重放、同键异义（含跨操作类型）、
    同键并发只执行一次、执行器错误回滚、记录写入失败（触发器）回滚。
- [x] **2. 字段修正接口**
  - catalog 的修正执行器（逆执行器随撤销一起放在第 4 步，那里才有调用方与测试）；`GET/PATCH /api/v1/releases/{id}/corrections`；详情加 `revision` 与 `corrected_fields`；接口样本。
  - 测试：无修正/设值/设空/清除四种状态转换；标题与专辑艺人设空被拒；非法值；`no_changes`；“确认扫描值”消除该字段的需要关注；
    列表、详情、搜索、排序、按组读取与最近播放显示同一有效值（含专辑艺人带动展示艺人与排序）；**合成素材真实重扫后修正仍生效、扫描值与证据更新、清除后显示新扫描值**；
    非管理员 403、跨源 403、未登录 401 且无副作用；同键重放、同键异义 409、过期版本 409；发行不可见 404。
- [x] **3. 人工归组接口**
  - catalog 的归组执行器（逆执行器随撤销放在第 4 步）；`POST /api/v1/releases/{id}/group`（`join`、`detach`）；接口样本与前端结果解码器。
  - 测试：归入后组的成员数、代表版本与 attention 之和正确，原组无可见版本时从列表消失、详情 404；移出得到新组；`same_group`、`already_alone`、`group_not_found`；
    **归组后真实重扫，归组保持**；并发：两个请求同时移动涉及同一组时不死锁、结果一致；权限与幂等同第 2 步。
- [x] **4. 撤销与操作记录接口**
  - 新包 `operations/httpapi`：`POST /api/v1/operations/{id}/revert`、`GET /api/v1/operations`；`operations.Kind` 与 `RevertExecutor`（先锁原记录再读取，使并发撤销串行）；catalog 实现两个 `Kind`（逆操作与批量描述），app 组装；身份包提供批量用户名查询；接口样本与前端解码器。
  - 测试：撤销修正恢复前一个意图（无修正、设值、设空分别验证，清除修正后再撤销读到的是当前扫描值）；撤销归组回到原组、原地址恢复；
    之后有修改 `revert_conflict`、重复撤销 `already_reverted`、撤销撤销 `not_revertible`、目标不可见 404；撤销的幂等；
    记录列表倒序分页、按发行筛选、`revertible` 正确、只有管理员可读、不含路径与密钥。
- [x] **5. 前端：编辑发行信息**
  - `features/governance` 的解码器、接口与钩子；专辑详情头部“编辑发行信息”对话框（三选一、扫描值与不确定提示、确认扫描值、只提交改动）；
    证据区标出“人工修正”；成功提示带“撤销”；409 与结果不确定的处理；`test/fake-api.ts` 与 `e2e/support/api-mock.ts` 的路由。
  - 测试：Vitest（对话框状态、请求内容、冲突、不确定后同键重试、撤销、普通成员没有入口）；Playwright（五种宽度、axe、纯键盘完成修改与撤销）。
- [x] **6. 前端：调整归组**
  - “归入其他专辑…”（搜索、预览、确认）与“移出为独立专辑”；成功后跳到新组并选中该版本。
  - 测试：Vitest（搜索排除当前组、预览内容、错误码文案、跳转）；Playwright（五种宽度、axe、键盘）。
- [x] **7. 前端：操作记录**
  - 导航“资料库”下的“操作记录”页（管理员，分页表格、撤销）；专辑详情（管理员）的“修改记录”（最近 5 条与“全部记录”链接）。
  - 测试：Vitest（列表、分页、撤销按钮只在可撤销时出现、撤销后刷新、普通成员看不到导航与页面）；Playwright（五种宽度、axe、键盘）。
  - 实施时的取舍：折叠面板抽成 `shared/ui` 的 `Disclosure`，“来源与证据”与“修改记录”共用（折叠时不渲染内容，内容里的请求在展开后才发出）；
    “修改记录”里的撤销另用一个页面持有的命令状态，结果提示放在折叠区块外（撤销归组后页面换组、详情重新挂载，提示仍在）；
    操作记录的查询 `staleTime` 为 0（每次打开都重读，别的管理员的修改也能看到）；撤销成功后“撤销”按钮所在行已变，焦点由 `useRevertFlow` 带到区块标题；
    撤销记录的名称写明撤销的种类（“撤销修改”“撤销归组调整”），撤销前的对话框把前后互换写出“会改回什么”；既有 e2e 里单独的 `summary` 定位改为按“来源与证据”限定。
- [x] **8. 真实服务用例**
  - `frontend/e2e/real/` 新增用例（Go、隔离 PostgreSQL、Chromium、合成素材）：页面上修改标题 → 重扫 → 仍是修正值；把两张合成专辑归到一起 → 一个组两个版本 → 撤销 → 分开。
  - 实施时的取舍：素材是用例自己生成的合成 WAV（INFO 标签带专辑，放在 `library/governance` 下，结束后删除并重扫），不依赖播放用例文件的先后顺序
    （两种顺序都验证过）；除两条主流程外另加成员的 403 与无入口检查；标题改过后只改文件的专辑标签再重扫，同时验证“扫描值更新而修正不动”，
    撤销后回到的是现在的扫描值（撤销恢复的是“没有修正”的意图）。已用“扫描写入时顺手清掉修正”的临时改动验证用例会失败，随即还原。
- [ ] **9. 文档同步**
  - 新规范 `.trellis/spec/backend/governance-contracts.md`（操作记录、修正、归组、撤销、错误码、测试；不超过 24,000 字节）并登记到后端索引；
    更新 Catalog REST 合同（有效值投影、`revision`、`corrected_fields`）、数据库规范（共同写入流程）、目录结构（两个新包、技术债）、运行合同（技术债与完成日志）、
    身份合同（成员写操作的去向，G6）、前端规范（状态、组件、目录、质量）、`frontend/DESIGN.md`、ADR 0002/0003 的决策记录与状态、路线图、产品目标、父任务 PRD。
- [ ] **10. 收尾**
  - 用合成数据截取编辑对话框、归组对话框、操作记录页与专辑详情“修改记录”的宽屏与窄屏，请用户确认布局（G9）；
    PRD 写实施结果与验收对照；归档（`--skip-branch-validation`），单独提交相对链接修正，写 journal，更新记忆。

## 验证命令（按步骤取最小集合）

- 后端：`gofmt -l .`、`go vet ./...`；在隔离 PostgreSQL 上
  `go test -count=1 ./internal/operations/... ./internal/catalog/... ./internal/app/...`，新包加 `-race`；
  扫描相关用例另跑 `go test -count=1 ./internal/library/...`；`ROOMUSIC_TEST_FFMPEG` 只在涉及派生的包需要。
- 前端：`npm run lint`、`npm run typecheck`、`npx vitest run <相关目录>`；提交前全量 `npm test`、`npm run build` 并核对内嵌产物、`npm run test:e2e`
  （需要 `LD_LIBRARY_PATH=/tmp/chromelibs/root/usr/lib/x86_64-linux-gnu`）。
- 真实服务：先拆掉自己的 `roomusic-test` 测试库，再 `./scripts/playback-e2e.sh`。
- 文档：Markdown 相对链接检查、规范文件不超过 24,000 字节、`python3 ./.trellis/scripts/task.py validate`。

## 风险与回滚点

- **读取切换到视图（第 1 步）** 牵动全部 catalog 读取：以“无修正时输出不变”为门禁，失败就回退这一步，不带着差异进入第 2 步。
- **扫描写入路径不改**：如果实现中发现必须改 `persist.go`，先停下说明原因，不扩大范围。
- **并发与锁顺序（第 3 步）**：发行行先锁，组行按 UUID 排序再锁；死锁测试失败不得用重试掩盖。
- 每步独立提交，任何一步可以单独 revert；迁移 0017 一旦提交只向前修复，不改写。

## 启动前

- 规划文档（本目录三份文件）先单独提交，再运行 `python3 ./.trellis/scripts/task.py start`。
