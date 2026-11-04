# Frontend Directory Structure

## 现状与意图

2026-10-01 的 M1a 已按下面的结构重建 `frontend/src/`。目录只为获批页面和真实消费者建立，不预建空文件夹；
M2a 已建立 `player`，M3 已建立 `governance`；`curation`、通用 `operations`（Change Set 与审批）、`artists`、`personal`、`steward` 随对应获批阶段建立，
基础 Release 搜索由 `catalog` 拥有，不为它新增搜索服务。阶段命名和依赖见 [Core 1 路线](../../../docs/architecture/core-1-roadmap.md)。
设计变量集中在 `styles/`，不复制设计工具的运行时。

## 实际布局

```text
frontend/
├── e2e/                            # Playwright 冒烟；受控后端在 e2e/support/api-mock.ts，响应取自后端样本，专辑的长内容与多版本数据在 e2e/support/albums-data.ts
├── playwright.config.ts            # 路由模拟
├── playwright.real.config.ts       # 隔离真实Go服务与原生音频；脚本启动
├── public/licenses/                # 三套字体的 OFL 文本，构建时复制到产物根目录
└── src/
    ├── main.tsx
    ├── app/
    │   ├── providers.tsx           # QueryClient、会话、Router 的装配
    │   ├── runtime.ts              # createAppRuntime：缓存、会话控制器、路由与清理的唯一接线
    │   ├── router.tsx              # 路由树与守卫
    │   ├── shell/                  # 外壳、侧栏、窄屏导航、身份区、导航项
    │   ├── routes/                 # 页面组合与整页状态（登录、无权限、不存在、服务不可用）；跨 feature 的组合（如历史页的播放动作 history-playback.tsx）
    │   └── *.test.tsx              # 页面级测试：经应用运行时与替身后端验证整页行为
    ├── features/
    │   ├── auth/                   # 初始化、登录、退出、会话生命周期与跨标签页同步
    │   ├── members/                # 成员列表与管理（管理其他账号，生命周期与 auth 不同）
    │   ├── library/                # 目录、目录操作记录、扫描记录、扫描详情与诊断
    │   ├── player/                 # 播放器：api、engine（含计次跟踪器、歌词分组与定位）、provider、能力查询、底栏、歌词面板与正在播放页
    │   ├── listening/              # 播放历史：播放上报器（带幂等键、有限重试、会话结束作废）、概览的“最近播放”区块与“我的音乐”的历史页签（M5 ①）
    │   ├── governance/             # 受治理操作（M3，管理员）：编辑发行信息、调整归组、设定默认版本（M4）、撤销、操作记录页与专辑详情的“修改记录”
    │   └── catalog/                # 专辑：列表（M1b）、详情（M1b）、封面、证据展示、默认版本标签与版本对比（M4），以及概览的计数（M1a）
    ├── shared/
    │   ├── api/                    # 请求、错误分类、解码原语、错误信封、重试策略、失败文案
    │   ├── ui/                     # 无策略的表现组件
    │   └── lib/                    # 与领域无关的小机制：幂等键与请求 ID、时间、数字、类名、页面标题、页码解析与越界规范化
    ├── styles/                     # tokens.css、base.css、fonts.ts
    └── test/                       # 测试共用基础设施：渲染、替身后端、样本读取、受控通道；专辑数据与接口行为（catalog-data.ts）、受治理操作的接口行为（governance-data.ts）
```

feature 只建需要的子目录；文件名统一小写短横线，组件以 PascalCase 导出，样式模块与组件同目录（`*.module.css`）：

```text
features/library/
├── api/                            # DTO、decoder、endpoint（decoders.ts、endpoints.ts）
├── components/                     # 组件与它们的样式、文案映射（root-messages.ts）
├── hooks/                          # 查询与命令 hook
├── query-keys.ts                   # 带查看者代次的查询键
├── scan-status.ts                  # 纯函数的视图投影与文案映射放在 feature 根目录的小模块
├── index.ts                        # 窄公开入口：只导出 app 组合页面所需的内容
└── *.test.ts(x)
```

测试与被测单元同目录。跨页面的整页行为（路由、会话、替身后端）放在 `app/*.test.tsx`；测试共用的替身后端与读取
后端样本的代码放在 `src/test/`，它们依赖 vitest，不被 Playwright 复用。浏览器冒烟放在 `frontend/e2e/`。
例外是只依赖解码器与样本读取的纯模块（M1b 的 `catalog-data.ts`：专辑数据与列表、组详情、版本详情、完整证据的接口行为，M4 起还有版本对比；M3 的 `governance-data.ts`：
修正、归组、撤销与操作记录的接口行为，含预期版本、幂等重放与各种 409，M4 起还有专辑的版本号与默认版本、专辑目标的记录与撤销、按专辑筛选的并集，
以及问题与核对：`seedIssues` 预置扫描结论、`scanIssues` 模拟依据变了的重扫，依据令牌、过期、`issue_changed` 与“需要关注”的重算）：
Vitest 的替身（`fake-catalog.ts`、`fake-governance.ts`）与 Playwright 的受控后端共用它们，让两层对同一份“后端行为”断言；Playwright 运行在 Node 里，
所以 e2e 只能导入这类纯模块（不能导入 vitest、`*.module.css` 或会连带导入它们的应用代码；各 feature 的 `api/decoders.ts` 只依赖 `shared/api`，真实服务用例可以直接导入）。
一个 feature 的解码器要复用另一个的解码器时，直接取对方的 `api/decoders` 模块（governance 取 catalog 的版本摘要解码器）：公开入口会连带导入组件与样式。

M1b 的 `features/catalog/` 实际布局（只列与 library 不同的部分）：

```text
features/catalog/
├── album-search.ts                 # 列表与详情的 URL 参数校验与规范化（纯函数）、从 history state 取返回列表的参数
├── labels.ts                       # 署名与证据词表的中文名，未知值原样显示
├── format.ts                       # 卡片摘要、时长、介质标题、署名文字、发行信息条目
├── comparison.ts                   # 版本对比表的行、差异与格式文字（M4，纯函数）
├── api/                            # decoders.ts（含样本往返与上限）、endpoints.ts（列表、组、版本、证据、对比、封面地址）
├── hooks/                          # use-album-page、use-album、use-release、use-release-evidence、use-release-comparison（M4）、use-library-totals
└── components/                     # cover、album-card/grid/filters/list-view、album-detail-view、album-header、version-nav、release-media/facts、evidence-section、library-summary、
                                    # default-version-tag、version-comparison-dialog、comparison-table（M4）
```

M3 的 `features/governance/` 实际布局（只列与 library 不同的部分）：

```text
features/governance/
├── labels.ts、field-draft.ts       # 可修正字段的中文名与取值显示；编辑对话框的草稿模型（只记动过的字段，规划要提交的修改与校验）
├── change-summary.ts               # 操作记录的类型名、改动逐行描述、撤销前的“会改回什么”预览、不能撤销的原因（纯函数）
├── operations-search.ts            # 操作记录页的 URL 参数校验与规范化（纯函数）
├── issues-search.ts、issue-text.ts # 问题页的 URL 参数（status、category、page、release）；类别名与每行的数目文字（M4，纯函数）
├── claims-search.ts、claim-text.ts # 待认领页的 URL 参数（page、release、target）；留着的修改、位置、理由与预览每项的文字（M4 第三项，纯函数）
├── api/                            # decoders.ts（含样本往返）、endpoints.ts（修正、归组、默认版本、问题与核对、认领、撤销、操作记录）
├── hooks/                          # use-release-commands（命令状态机）、use-release-corrections、use-operations、use-revert-flow（撤销对话框的开合与焦点兜底）、
│                                   # use-issues（问题列表与版本的问题）、use-claims（待认领列表、详情、预览与提示）、use-container-wide（问题页与待认领页的两栏与顺序导航）
└── components/                     # edit-release-dialog、join-album-dialog、detach-album-dialog、release-admin-actions、release-command-notice、
                                    # operation-list、revert-operation-dialog、operations-view（操作记录页内容）、release-history（专辑详情的“修改记录”）、
                                    # issues-view、issue-list、release-issues-detail、issue-actions（元数据问题页，M4）、
                                    # claims-view、claim-list、claim-detail、claim-target-picker、claim-preview（待认领页，M4 第三项）
```

## 归属

- `app` 组合路由、顶层 provider、会话启动与外壳，不吸收 feature 策略。`runtime.ts` 是缓存、会话与路由之间唯一的接线点。
- `auth` 拥有初始化与登录表单、会话控制器、退出、当前身份的展示与角色标签；服务器仍是认证权威。管理**其他**账号不属于
  `auth`，由 `members` 拥有。
- `members` 拥有成员列表、新建、停用、重新启用与撤销会话。
- `library` 拥有目录与扫描管理、目录操作记录、扫描记录（F3）、扫描详情与诊断的展示。
- `catalog` 拥有 Release/ReleaseGroup 的列表与详情、Medium/Track 展示、来源与封面展示（M1b 实现），以及概览里的专辑数。
- `player` 拥有单一媒体生命周期、队列/模式/恢复、浏览器能力与播放界面；只经 catalog 公开合同读发行，
  不反向导入 app，也不把播放器全局状态放到 shared。进度与离散快照分开订阅。
- `listening` 拥有播放上报（幂等键、有限重试、会话结束作废）、概览的“最近播放”区块（读取、卡片、“全部历史”链接、清除确认）与“我的音乐”的历史页签内容
  （M5 ①，`HistoryView`：按曲目汇总的列表、分页、缺失行、移除与清除确认，以及“播放全部”候选的读取 `useHistoryQueue`）；经 catalog 公开合同取封面与专辑艺人的显示规则，
  不导入 player：`player` 导入它的上报器，概览页（app 层）组合区块。历史页的播放动作由 app 组合：`HistoryView` 经插槽 `renderPlayAll` 与
  `renderTrackActions(item, requestRemove)` 接收 `app/routes/history-playback.tsx` 里的“播放全部”与行内播放/“更多”菜单，菜单里的“从历史中移除”调用插槽给的
  `requestRemove`（确认对话框仍归 listening）。`HistoryItem → PlayerTrack` 的映射（`playerTrackOfHistoryItem`）也在 app：player 只经 catalog 公开合同读发行，不认识历史条目。
  “我的音乐”的布局与页签（`app/routes/me-layout.tsx`）在 app，后续页签（收藏、歌单）沿用同一布局。
- `governance`（M3）拥有管理员对发行的受治理写操作的界面与命令状态：编辑发行信息、调整归组、撤销、操作记录页与专辑详情的“修改记录”；
  M4 起还有专辑的默认版本（设定、改回自动选择与撤销）与“元数据问题”页（`/issues`：问题列表与详情、核对、取消核对与撤销，专辑详情的“查看问题”经现有插槽）与“待认领”页（`/claims`：把已不可见的旧版本上的修改认领给新版本，专辑详情的“可能接替了……”提示经同一个插槽）。
  只读的默认版本标签与版本对比（所有成员可见）在 catalog；问题页用的证据字段与原因码的中文名由 catalog 公开。
  它导入 catalog 的公开合同（封面、专辑艺人的显示规则、专辑搜索与专辑详情的读取），**catalog 从不导入 governance**：专辑详情里的管理员区块由 app 经插槽
  （`renderAdminActions`、`renderAdminSections`）传给 catalog 的详情视图；写操作之后刷新什么缓存、版本换了专辑组之后导航到哪里，由 app 提供给命令 hook，
  governance 不知道别的 feature 的查询键，也不依赖路由的页面。
- `search` 拥有查询与筛选的 URL 状态和结果展示（尚未建立）。
- 通用 `operations` 拥有 Change Set 与审批的状态及受支持的恢复界面，不是通用 toast（尚未建立；M3 的操作记录与撤销界面在 `governance`）。
- `shared/api` 拥有同源请求、凭据、请求关联、响应体处理、错误信封、重试策略与解码原语；feature 的端点与 DTO 留在 feature。
  `fetch` 只在 `shared/api/request.ts` 中调用，由 ESLint 规则保证。
- `shared/ui` 放至少两处使用的、无策略的表现组件：不发请求、不判断角色、不引用 feature（M3 把折叠面板抽成 `Disclosure`，“来源与证据”与“修改记录”共用）。

## Import Rules

- `app` may compose feature public surfaces.
- Features may import `shared` and a deliberately published contract from
  another feature. They do not import another feature's private component, hook,
  API endpoint, or state.
- `shared` never imports `features` or `app`.
- Feature-to-feature cycles are forbidden. Move orchestration to `app` or
  publish a smaller typed contract.
- Avoid deep relative imports across boundaries. A public entry is narrow; it is
  not a barrel that re-exports the whole feature.

## Naming

- React components and their exported types use `PascalCase`; hooks start with
  `use`; functions/values use `camelCase`.
- 文件名统一小写短横线（`root-table.tsx`、`use-scan.ts`）；路由与 feature 目录名使用产品词汇，不用后端表名。
- Wire types end in `DTO` when distinction from a view/domain model matters.
  Runtime decoders use verbs such as `decodeReleaseDetail`.
- Tests use `*.test.ts` or `*.test.tsx` beside the owner. Do not create a
  repository-wide `__tests__` mirror tree.

## Example Placement

A Release page route composes `ReleaseDetail` and its feature hook. The hook
calls an endpoint function in `catalog/api/endpoints.ts`, which uses the shared
HTTP transport and a decoder from `catalog/api/decoders.ts`. A reusable skeleton
may move to `shared/ui` only when another implemented feature needs the same
presentation contract（`DataTable` 就是这样在目录、操作记录、诊断与成员列表都需要时才进入 `shared/ui` 的）。

The page never imports a PostgreSQL-shaped row, exposes an absolute artwork path,
or combines a scan mutation into catalog display merely because both mention a
Release.

## Production Boundary

The frontend build output is embedded or served by the Go application in
production. Do not add a production Node server, cross-origin authentication
scheme, or CORS dependency for Core 0. Local development may use a dev-server
proxy to the Go REST API.

## Anti-Patterns

- Global `components`, `hooks`, `services`, or `types` buckets containing
  unrelated feature policy.
- One `api.ts` file with every endpoint and DTO.
- Importing private feature state to avoid defining a narrow contract.
- A generic `utils.ts` that accumulates path, permission, date, and Release
  rules.
- Creating player, Agent chat, plugin marketplace, or Meilisearch client folders
  before those phases are approved.

This structure mirrors backend capability ownership in
[Modular Design](../guides/modular-design.md) and the current
[Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
