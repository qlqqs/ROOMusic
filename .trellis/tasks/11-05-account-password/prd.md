# 账户与密码

## 目标

补上 2026-11-05 操作闭环核对发现的断点：

- 成员能自己改密码。
- 管理员能替成员重置密码。
- 管理员自己忘了密码时，能在服务器上用命令恢复，不用直接改数据库。
- 艺人候选点了“以后不再提示”之后能找回来。

路线图里成员一行原来只有创建、停用和撤销会话（`docs/architecture/core-1-roadmap.md:34`）。

## 现状（已核对）

- **身份接口**：只有初始化、登录、退出、查看者、成员列表、新建成员、停用/启用和撤销会话（`backend/internal/identity/httpapi/handlers.go:51-61`），没有任何修改或重置密码的接口。`PATCH /users/{id}` 只接受 `disabled` 一个字段（`:255`）。
- **只有一个管理员**：新建成员的角色固定为 `user`（`:231`），初始化完成后就关闭（`:105-107`），而且最后一个管理员不能停用（`:302-306`）。
- **密码规则**：12–72 位可打印 ASCII，统一由 `identity.ValidPassword` 校验，哈希用 bcrypt（`backend/internal/identity/guard.go:128-160`）。登录时不按新规则检查，旧账号照常能登。合同见 `.trellis/spec/backend/identity-contracts.md`。
- **会话**：库里只存令牌哈希，有效期 24 小时。停用成员时，在同一个事务里撤销他的全部会话（`handlers.go:337-342`）。
- **进程入口**：`backend/cmd/roomusic/main.go` 没有子命令，配置全部来自环境变量，例如 `ROOMUSIC_DATABASE_URL`（`backend/internal/platform/config/config.go:58`）。
- **前端**：侧栏身份区只有用户名、角色和“退出登录”（`frontend/src/app/shell/identity-panel.tsx`）；成员页的操作只有停用、重新启用和撤销会话（`frontend/src/features/members/components/member-table.tsx`）。
- **成员写操作**：不带幂等键，也不写操作记录（G6 / R4，`identity-contracts.md` §3），本任务照旧。
- **艺人候选忽略**：
  - 忽略记录在 `artist_candidate_ignores` 表里（`backend/migrations/0026_artist_catalog.sql:103`）。
  - 候选列表用 `NOT EXISTS` 把忽略过的排除掉，也没有参数能列出它们（`backend/internal/catalog/governance/artist_candidates.go:30-33`、`backend/internal/catalog/httpapi/artist_governance.go:236-251`）。
  - 恢复接口已经有了：`POST /artists/{id}/candidate-ignore`，传 `ignored:false`。但前端只在刚忽略后的那条提示里给了恢复入口（`frontend/src/features/governance/components/artist-command-notice.tsx:64`、`frontend/src/features/governance/components/artist-candidates-view.tsx:120-124`）。
  - 这类操作不能撤销（`backend/internal/catalog/governance/artist_describe.go:199-202`），按设计要靠反向命令恢复。

## 关键决定（qlqq，2026-11-05）

- K1 **管理员重置的方式**：管理员在成员页直接输入一个新密码，告诉成员；成员登录后随时可以自己改。不做临时密码，也不强制首次登录改密码。
- K2 **改密码后的其他登录**：
  - 自己改密码：当前设备保持登录，其他设备退出。
  - 管理员重置、命令行重置：这个账号在所有设备上都退出。
- K3 **范围**：艺人候选“已忽略”的查看与恢复并入本任务。
- K4 **恢复命令**：新增 `scripts/reset-password.sh <用户名>`，与 `scripts/prod.sh` 一样读取 `.env`，管理员只需一条命令。
- K5 **已忽略的排序**：最近忽略的在前。
- K6 **真实服务用例**：只覆盖 A1、A2；A4 由 Go 集成测试（真实扫描、真实接口与样本）加模拟 Playwright 覆盖，因为真实用例的合成素材产生不了艺人候选，验收记录里写明。
- 最终规划路线：(a) 人工批准最终规划摘要。qlqq 于 2026-11-05 回复“启动任务”批准；测试分工按同日要求改由 GPT-6.1-Sol 编写和执行。

## 需求

| 编号 | 需求 |
| --- | --- |
| A1 | **成员自助改密码**。<br>侧栏身份区加入“修改密码”入口，要求输入当前密码，新密码输入两次。新密码沿用 `identity.ValidPassword` 的规则。<br>当前密码错误时明确提示，不改任何东西。<br>改完以后，本人的其他会话全部撤销，当前会话保留（K2）。 |
| A2 | **管理员重置成员密码**。<br>成员页每个其他成员的那一行加“重置密码”，输入新密码两次后确认。规则同 A1。<br>重置后该成员的全部会话撤销（K2）。停用的成员也可以重置，重置后仍是停用状态。<br>管理员自己那一行不提供重置，改自己的密码走 A1。 |
| A3 | **命令行恢复**。<br>同一个程序加子命令 `roomusic reset-password <用户名>`，读取和服务端相同的 `ROOMUSIC_DATABASE_URL`，只连库，不跑迁移，不启动服务。由 `scripts/reset-password.sh` 包装（K4）。在终端里隐藏输入、输入两次；不在终端时从标准输入读一行。规则同 A1。<br>成功后该账号的全部会话撤销（K2）。<br>用户不存在、密码不合规或数据库不可用时，退出码非零并给出一句说明；输出里不出现密码和数据库地址。 |
| A4 | **艺人候选“已忽略”**。<br>“可能是同一位”页可以切换查看已忽略的候选，并逐条恢复提示，最近忽略的在前（K5）。恢复后这一条回到待处理列表。<br>两位艺人已经合并、不再构成候选的，不出现在已忽略列表里。 |
| A5 | **同步合同与文档**。<br>身份合同、艺人治理合同、路线图成员一行和运维说明（README）里写清 A1–A4，包括命令行用法。 |

## 验收

- V1（A1）：
  - 当前密码对时改成功：当前页面不掉线，同一账号在另一个浏览器的会话失效，旧密码不能再登录，新密码能登录。
  - 当前密码错时有明确提示，密码和会话都不变。
  - 新密码 11 位、73 位、含中文时被拒绝，不写库。
- V2（A2）：
  - 管理员重置某成员后，该成员已有的会话全部失效，新密码能登录。
  - 普通成员调用重置接口返回 403；非 UUID 返回 400，未知用户返回 404。
  - 管理员自己那一行没有“重置密码”。
- V3（A3）：
  - 在隔离测试库上，用命令行重置管理员密码后，旧会话失效，新密码能登录。
  - 用户不存在、密码不合规时退出码非零，数据不变。
  - 输出不含密码和数据库地址。
- V4（A4）：
  - 忽略一条候选后，它出现在“已忽略”里；恢复后回到待处理列表，再次刷新也保持。
  - 两位艺人合并后，这一对既不在待处理里，也不在已忽略里。
- V5（质量）：
  - 后端：身份与艺人候选的 PostgreSQL 集成测试，以及命令行子命令的测试。
  - 前端：Vitest 与接口样本往返；涉及页面在五种宽度下不溢出，axe 无违规；Playwright 覆盖改密码、重置和已忽略恢复。
  - 收尾时跑一次真实服务用例（新增改密码与重置，A4 不做，见 K6），并复核截图。
  - 界面文案按全局文案规则来，不写防御性和多余的说明。

## 范围外

- 邮件或短信找回、临时密码与强制改密码、登录失败次数限制、两步验证。
- 成员删除与改角色、`editor` 角色、成员写操作的幂等键与操作记录。
- 其他零碎缺口：添加目录时显示允许的目录、清理 `GET /releases`。

## 设计与执行

跨了 identity、catalog 治理、进程入口和前端，按 R5 另写 [design.md](design.md) 与 [implement.md](implement.md)。不需要数据库迁移。

## 实施结果

2026-11-05 完成。分工：后端 GPT-6-Astra，前端 Claude Opus 5.5，测试编写与执行 GPT-6.1-Sol，主会话规划、验收与提交。

| 提交 | 内容 |
| --- | --- |
| `552f875` | 规划文档 |
| `da4297e` | 后端：改密码、重置、`reset-password` 子命令与脚本、已忽略候选 |
| `54b65e6` | 后端测试 32 个与接口样本 8 个 |
| `fea965d` | 前端：修改密码、重置成员密码、候选“待处理 / 已忽略” |
| `811ba7c` | 修正：两个状态链接同时是 `aria-current`（3b 的 Playwright 发现） |
| `ca0d1e7` | 前端测试：Vitest、样本往返、Playwright、`account.real.ts` |
| `41f8c21` | A5：身份、艺人、目录结构、会话与治理规范，路线图成员一行，README“忘记密码” |
| `eae3877`、`0ef1816` | 修正：改密码成功结果移进对话框（截图复核发现侧栏提示挤成两行、把“退出登录”推出 1440×900 视口）及回归测试 |

与设计不同的地方（验收时裁定）：

- 命令行的 30 秒超时只从读完密码后开始算，人输入密码不计时；`scripts/reset-password.sh` 先编译到临时目录再运行，透传退出码（`go run` 会把 2 改成 1）。
- 文案按全局规则精简：改密码成功“密码已修改。”，结果不确定“不确定改好了没有，可以退出后用新密码登录试试。”，重置说明“对方需要用新密码重新登录。”，重置成功“已重置“{用户名}”的密码。”。
- 带输入框的两个对话框初始焦点在标题上；改密码成功后焦点在“完成”上。窄屏菜单里按 Esc 只关最上层对话框（`shared/ui/modal-dialog.tsx`）。

验收对照：

- V1–V3：`identity_api_integration_test.go` 的 `ChangeOwnPassword*`、`AdminResetPassword*`，`ResetPasswordCommand*` 与 `cmd/roomusic/reset_password_test.go`，`ConnectDoesNotMigrate`；真实服务用例 `account.real.ts` 2/2（A1 当前页不掉线、另一浏览器 401、旧密码失效；A2 全部会话 401、新密码能登录、自己那一行没有入口、普通成员 403）。
- V4：`ArtistIgnoredCandidates{ListAndRestore,ExcludeMergedPairs,OrderAndPaging}` 与艺人样本；模拟 Playwright 的纯键盘忽略→已忽略恢复→刷新保持。按 K6 不做真实服务用例：真实用例的合成素材产生不了艺人候选。
- V5：Vitest 相关八文件 629 通过、1 跳过（既有仓外大样本未配置）；模拟 Playwright 五文件首轮 212 例（修正后失败项重跑通过），成功态修正后账户子集 87/87（五宽、axe 违规为空）。
- 收尾：对 `ca0d1e7` 的导出树 `npm ci` 与构建，内嵌资产 124 个文件逐字节一致；真实服务用例 10 个文件 55/55（`--retries=0`，155 秒）。`eae3877` 只改了一个前端文件，之后在工作树上跑相关 Vitest、账户 Playwright 子集与 `account.real.ts`（2/2），没有再跑整套。
- 截图复核：1440 与 375 下身份区、两个对话框、对话框内成功、成员表、候选两个视图（`frontend/test-results/account-screenshots/`，不提交）。
