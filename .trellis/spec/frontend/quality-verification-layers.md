# 前端各阶段的验证分层

从[前端质量规范](./quality-guidelines.md)移出：每个阶段的测试按“共用的接口行为 → 页面级 → 浏览器冒烟 → 真实服务”分层，并记录各层发现过的问题。通用的门禁与检查仍在质量规范。

## M2a 播放验证分层

播放器改动先运行最小 Vitest：`npx vitest run src/features/player src/test/api-contract.test.ts`；
涉及共享外壳/导航/专辑接线时影响所有页面，按获批计划运行前端全量 Vitest 与 Playwright。
API 全样本清单必须注册播放（含派生）与歌词的全部成功样本和错误样本，局部 decoder 测试不能替代样本完整性断言。

- 组件与路由模拟：底栏状态、未知时长、键盘菜单与队列、长标题、五宽375/768/1024/1280/1440、axe。
- 歌词（M2b）：Vitest 覆盖解码器（形态约束）、分组与当前组定位的纯函数、面板各状态、跟随与暂停跟随、键盘与点击跳转、减少动态效果、组内进度事件不重绘；
  Playwright 路由模拟覆盖同步、纯文本、没有歌词三种形态的五种宽度（布局、不溢出、axe）与跟随、暂停跟随、键盘、读取失败重试。
  平滑滚动要等它到位：用轮询判断，轮询函数里不放会抛错的断言；时间很短的状态（如 2 秒素材里只当前半秒的一行）不能靠轮询观察，用 MutationObserver 记录序列。
- 真实服务：`LD_LIBRARY_PATH=/tmp/chromelibs/root/usr/lib/x86_64-linux-gnu ./scripts/playback-e2e.sh`（本机缺系统库时）。
  独立临时合成媒体、真实Go、`roomusic-test` PostgreSQL与Chromium，由`playwright.real.config.ts`收集
  `e2e/real/*.real.ts`，不能被既有mock `*.spec.ts`混收集。
  输出使用`coverage/playback-real/{results,report}`独立目录，不放mock默认`test-results`下，避免并行运行时被另一配置清空。
  `.gitignore` 精确忽略 `/frontend/coverage/playback-real/`；根 `/coverage/` 不覆盖这个嵌套目录，切换输出位置必须用 `git check-ignore` 实证。
- 隔离脚本拒绝接管已存在的测试容器/数据卷，Compose `--env-file /dev/null`与临时Vite `envDir:false`不读本机.env。
  白名单复制后端源码到临时树，前端输出同一临时树，不覆盖工作区内嵌产物；结束按精确PID与本轮Compose资源清理。
  不与`test-integration.sh`并行争用`roomusic-test`。
- 音频真实时间、seek与自然结束必须观察原生媒体事件，不替换播放行为假装实播。
  长期撤销用180秒合成PCM与有背压的真实TCP读取，服务端慢速流测试独立证明复核；短素材通常已缓冲，不能代替。
- 三组手工：macOS Safari、Windows Chrome或Edge、任一OS Firefox，记录版本与格式/定位结果。
  未执行、失败、实际通过分别记录；CI配置已加入不等于远端CI已运行通过。

新增配置须纳入`tsconfig.json`，避免工具配置没有常规类型门禁；私有路径/源音频不得进入报告。

## M3 受治理操作验证分层

最小 Vitest：`npx vitest run src/features/governance src/app/release-corrections.test.tsx src/app/release-grouping.test.tsx src/app/operations.test.tsx src/shared/ui/disclosure.test.tsx src/test/api-contract.test.ts`；
改到专辑详情（`catalog`）、路由或外壳时再加 `src/app/album-detail.test.tsx` 与 `src/app/app.test.tsx`。

- **共用的接口行为**：`src/test/governance-data.ts` 实现修正、归组、撤销与操作记录的后端行为（预期版本、幂等重放与同键异义、`no_changes`、`same_group`、`already_alone`、
  `revert_conflict`、撤销资格），修改反映到共用的专辑数据（列表、组详情、版本详情）；Vitest 的 `fake-governance.ts` 与 Playwright 的受控后端共用它，两层对同一份行为断言。
  它只依赖解码器与样本读取（见[目录结构](./directory-structure.md)）。样本契约：`corrections/`、`group-changes/`、`operations/` 与新增错误码的样本都要有 decoder 并往返无损。
- **页面级**：`src/app/release-corrections.test.tsx`、`release-grouping.test.tsx`、`operations.test.tsx` 覆盖入口与权限（成员没有入口也不请求）、请求内容（版本号与幂等键）、
  各错误码的文案与对话框去向、结果不确定后同一个键重试、撤销后的刷新与焦点、页码越界规范化；用 `respondToCorrect` 等函数直接预置后端状态。
- **浏览器冒烟**：`e2e/governance.spec.ts`（编辑、归组、撤销与操作记录的流程，五种宽度、axe、纯键盘、Esc）与 `responsive.spec.ts` 里操作记录与“修改记录”的页面状态
  （含超长改动内容）。页面上有多个折叠区块时，`summary` 的定位必须带上文字（“来源与证据”“修改记录”），不能再用无条件的 `locator("summary")`。
  对话框要检查**自己**没有横向滚动（`scrollWidth - clientWidth`），页面级的溢出检查看不到它。
- **真实服务**：`e2e/real/governance.real.ts`，经 `scripts/playback-e2e.sh` 与播放用例同一个服务运行：素材是用例自己生成的合成 WAV（INFO 标签带专辑，放在 `library/governance`），
  结束后删除并重扫；不依赖文件先后顺序（两种顺序都验证过）。覆盖页面改标题 → 改文件标签并重扫 → 扫描值更新而修正不动 → 撤销后回到现在的扫描值；
  归组 → 重扫保持 → 在“修改记录”里撤销后分开；成员没有入口且接口 `403`。脚本拒绝接管已有的 `roomusic-test` 容器，先拆掉自己的测试库再运行。
- **变异检查（实践，不入库）**：对新增逻辑逐项临时改坏（可撤销判断、焦点兜底、`staleTime`、地址规范化、同键重试、后端“扫描写入时顺手清掉修正”），确认用例会失败再还原；
  存活的变异必须逐个判断是否等价（例如页面与视图两道 `isAdmin` 把关，去掉一道行为不变）。

## M4 默认版本与版本对比验证分层

最小 Vitest：`npx vitest run src/app/default-version.test.tsx src/app/version-comparison.test.tsx src/features/catalog src/features/governance src/test/api-contract.test.ts`；
改到归组或详情的地址规范化时再加 `src/app/release-grouping.test.tsx` 与 `src/app/album-detail.test.tsx`。

- **共用的接口行为**：`catalog-data.ts` 增加版本对比（`respondToGroupComparison`），`governance-data.ts` 增加专辑的版本号与默认版本、专辑目标的记录与撤销、
  按专辑筛选的并集（`respondToGroupDefault` 等）；Vitest 的替身与 Playwright 的受控后端共用。样本契约覆盖 `group-defaults/`、`release-groups/comparison.json`、
  `get-default.json`、`operations/list-by-group.json`、`reverted-default.json` 与 `not_in_group` 等错误样本。
- **页面级**：`default-version.test.tsx`（入口与权限、请求体与幂等键、页面停在刚才的版本、提示里撤销、409 与结果不确定、移出默认版本的说明、
  “修改记录”与操作记录页的专辑级记录与组筛选）；`version-comparison.test.tsx`（标签只在生效中的默认版本上、表格的行与差异读屏文字、空行隐藏、人工修正、
  格式文字、列头切换后焦点回到入口、读取失败、只剩一个版本、超过 100 个版本）。
- **地址规范化暂缓**要有确定性用例：命令完成前让组详情的刷新挂起，断言地址没有被旧数据规范化、刷新完成后页面仍在这个版本；先写用例复现 M3 归组落到别的版本的现象，再修。
- **浏览器冒烟**：`e2e/default-version.spec.ts`（设定、改回、撤销、对比；纯键盘与 Esc；五种宽度下对比对话框在视野内、不超过 90vh、内部滚动、
  自己没有横向滚动、窄于 768px 时堆叠、axe 与最小字号；差异下划线在真实浏览器里确实画出来）。
- **真实服务**：`e2e/real/default-version.real.ts`：两张合成 WAV（16 bit / 44.1 kHz 与 24 bit / 96 kHz，`syntheticWav` 的 `format` 参数），年份用修正接口设定，
  再用归组接口放进同一个组；覆盖页面设定 → 列表与打开专辑都先显示它 → 真实重扫后保持 → 对比标出格式与年份的差异 → 在“修改记录”里撤销；成员只读、写接口 `403`；
  结束删除素材并重扫。反向验证：临时让扫描清空默认版本，“重扫后保持”用例随即失败。

## M4 问题处理验证分层

最小 Vitest：`npx vitest run src/app/issues.test.tsx src/app/shell src/features/governance src/test/api-contract.test.ts`。

- **共用的接口行为**：`governance-data.ts` 增加问题与核对（`seedIssues`、`scanIssues`、`respondToIssues`、`respondToReleaseIssues`、`respondToIssueReview`）：依据令牌由内容算出，
  内容变了令牌就变，过期、`issue_changed` 与“需要关注”的重算与后端一致。样本契约覆盖 `release-issues/`、`issue-reviews/`、`operations/list-issue-reviews.json` 等。
- **页面级**：`issues.test.tsx`（入口与权限、列表与筛选的数字与地址、空态与错误态、详情三节、窄与宽容器、核对与列表和数字一起变化、提示里撤销、
  撤销取消核对恢复原来的核对人、`issue_changed`、过期、结果不确定、编辑发行信息、“查看问题”、操作记录的名称与改动行、从记录撤销时的提示、核对之后的焦点）。
- **浏览器冒烟**：`e2e/issues.spec.ts`（主流程、`issue_changed`、编辑发行信息、“查看问题”、成员没有入口；五种宽度下 1280/1440 两栏、更窄顺序导航、不溢出、axe 与最小字号；纯键盘）。
- **真实服务**：`e2e/real/issues.real.ts`：同一目录三种专辑标签（3/2/2，没有过半数）的合成 WAV 拆成三个版本，删一个文件造出缺失曲目；页面核对 → 原样重扫保持 →
  加一个文件后归组冲突重新提醒 → 取消核对并在“修改记录”里撤销；成员 `403`；结束删除素材并重扫。
- 这一项的浏览器与真实服务用例各发现一个替身测试漏掉的问题（核对之后焦点掉回页面顶部；从记录撤销核对时提示写成字段修改），都已补 Vitest 回归用例。

## M4 认领验证分层

最小 Vitest：`npx vitest run src/app/claims.test.tsx src/app/claim-flow.test.tsx src/features/governance src/test/api-contract.test.ts`。

- **共用的接口行为**：`governance-data.ts` 增加认领（待认领列表与详情、推荐、预览与令牌、认领的执行、撤销、记录列表按来源筛选）；`claim-data.ts` 与 `claims-setup.ts` 提供测试用的旧版本与新版本。
  令牌由计划的内容算出，所以别人改了专辑的默认版本、新版本又被修改，令牌与版本号随之变化，`claim_changed` 与 `revision_conflict` 和后端一致。样本契约覆盖 `release-claims/`、`operations/*-claims*.json`、`errors/claim-changed.json` 等。
- **页面级**（59 个）：`claims.test.tsx`（入口与权限、空态与错误态、列表与详情、窄与宽容器）、`claim-flow.test.tsx`（推荐与搜索的选择、预览每一类的文字、认领、`claim_changed` 与 `revision_conflict` 后重新读取、
  结果不确定时用同一个键重试、提交后焦点落在提示上、撤销后页面回到原来的专辑、提交后列表与专辑数据一起刷新、专辑详情的提示）、`claim-text` 与 `claims-search` 的纯函数测试。
- **浏览器冒烟**：`e2e/claims.spec.ts`（9 个：主流程与提示里的“撤销”、预览之后别人改了新版本、搜索其他版本、专辑详情的提示、操作记录、成员没有入口；五种宽度的两栏与顺序导航、不溢出、字号、axe；纯键盘选择、认领与撤销）。
- **真实服务**：`e2e/real/claims.real.ts`（6 个，串行）：同一目录三种专辑标签的合成 WAV 拆成三个版本，用接口在其中一个上做好四类人工状态，把它的三首换成另一份音频（同路径，改修改时间）并重扫，
  扫描新建发行、旧版本不可见；页面在“待认领”里按推荐认领，检查专辑页、默认版本与“需要关注”；原样重扫后四类保持、操作记录不被扫描改动；撤销后旧版本又在待认领里，可以再认领；成员 `403`；
  结束删除素材并重扫（带着人工修改的新版本因此成为待认领，一直留在列表里）。
- 这一项的真实服务用例发现了一个替身测试漏掉的缺陷：操作记录里来源版本的名字取了旧版本现在的有效标题，而认领会把它的修正清走，记录里的名字就和用户认领时看到的不同；
  已改成取记录里的名字，并补了后端回归断言。
- 反向验证（实际执行并还原）：真实服务用例里，让认领不清除旧版本的修正、让迁移的核对依据被改坏、让页面提交的令牌被丢掉、让页面把旧版本号写成新版本的，“在页面上认领”随即失败；后端 `TestPostgreSQLClaimDescribe` 里，让来源标题取现在的有效标题、让认领记录取认领之后的标题，都会失败。
