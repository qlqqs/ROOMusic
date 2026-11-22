# Research: E1 前端现状（自动整理界面第一版）

- **Query**: 调研 `frontend/src/` 现状，为 E1（设置“自动整理”、整理记录、发行详情的自动补全标识、首次扫描后的一次性提示）给出带 file:line 的事实
- **Scope**: internal
- **Date**: 2026-11-22

路径默认相对 `frontend/src/`；`spec/` 指 `.trellis/spec/`。只记事实，不写方案。

---

## 1. 设置页的结构

### 1.1 栏目清单 `app/routes/settings-sections.ts`

- `SettingsSection` 类型：`to` 是固定字面量联合（七个地址），`label`、`icon`（lucide）、`adminOnly`、`group?: "管理"`、`wide?`（settings-sections.ts:4-14）。新增栏目要同时扩展 `to` 的联合类型。
- 栏目数组 `settingsSections`：账号、播放、Agent（不分组），扫描目录（`wide: true`）、成员、操作记录、备份（`group: "管理"`, `adminOnly: true`）（settings-sections.ts:16-24）。
- `visibleSettingsSections(role)` 按角色过滤（:27-29）；`settingsSectionAt(pathname)` 用前缀匹配决定当前栏目（扫描详情算在扫描目录下）（:32-34）；`groupSettingsSections` 按相邻同组分段（:37-45）。
- 测试 `app/routes/settings-sections.test.ts:5-15` 断言管理员“固定顺序的七栏”，:17-24 断言成员三栏、:23 `toHaveLength(7)`。`app/settings.test.tsx:38` 的用例名也写着“七栏”。新增栏目会让这两处断言变化。

### 1.2 布局 `app/routes/settings-layout.tsx`

- 组件注释（:7-12）：≥1024px 左侧分栏，更窄时胶囊标签行横滑、两端渐隐；布局不渲染 h1，每栏自带 `Page`；权限由各栏路由的 AdminGate 把关，布局只决定列出哪些栏。
- 分栏用 `Link` + `aria-current`，`search={{}}`、`activeOptions={{ includeSearch: false }}`（:34）；内容区 `data-wide` 由 `settingsSectionAt(...).wide` 决定（:16、:45-47）。
- `useScrollEdges`：横滑标签的 `data-fade-start/end` 与切栏时把当前项滚到中间（:56-85）。
- 版式约定（spec/frontend/interface-layout-guidelines.md:101-102）：分栏 176px；内容最宽 720px（扫描目录与扫描详情 820px）。

### 1.3 路由注册 `app/router.tsx`

- 管理页与播放设置页 `lazyRouteComponent` 按需加载（router.tsx:28-41，`MembersPage`:37、`OperationsPage`:38、`BackupsPage`:39）。
- `settingsRoute`（path `/settings`，组件 `SettingsLayout`）:351；`/settings` 首页重定向到 `/settings/account`（:353-359）。
- 管理栏的统一写法是 `<AdminGate><LazyPage><XxxPage/></LazyPage></AdminGate>`：成员 :424-434、操作记录 :437-453、备份 :455-465、扫描目录 :385-404。
- 带地址参数的栏用 `validateSearch`：扫描目录 `scan_page`（:397-404），操作记录 `parseOperationsSearch`（:448-453）。
- 子路由统一挂在 `settingsRoute.addChildren([...])`（:487）。
- 旧地址重定向（`/operations` → `/settings/operations` 等）在 :105-111。
- `LazyPage`（app/routes/lazy-page.tsx:29-35）：Suspense 的占位是 `<Skeleton label="正在加载页面" />`，代码块加载失败时显示 `Notice tone="error"` 和“刷新页面”（:14-21）。

### 1.4 AdminGate `app/routes/admin-gate.tsx`

- `viewer.role === "admin"` 时渲染子内容，否则渲染 `<ForbiddenPage />`，子内容不渲染，管理接口不会被调用（admin-gate.tsx:5-11）。

### 1.5 两个最接近的管理页

**备份（`/settings/backups`）**：服务端开关、状态与即改即存的写法最完整。

- 路由文件只组合：`app/routes/backups-page.tsx:5-12` 是 `<Page title="备份">` 加三个 feature 区段 `BackupStatusSection`、`BackupRetentionSection`、`BackupFilesSection`；三者由 `features/system/index.ts` 导出。
- 文件组织：`features/system/{api/decoders.ts, api/endpoints.ts, hooks/use-*.ts, components/*-section.tsx + .module.css, components/backup-messages.ts, query-keys.ts, index.ts}`。
- 区段骨架（features/system/components/backup-status-section.tsx:19-65）：`SettingsGroup title="状态"`、`action`（标题行右侧的主按钮）、`footer`（错误与提示），卡片里是 `QueryContent` + `SettingRow label=… plainLabel`（只读值）。
- 加载与错误：`QueryContent query={overview} loading={<Skeleton label="正在加载备份状态" lines={2} />}`（:47）；读取失败由 QueryContent 出 `FailureNotice` 和“重试”；写失败 `FailureNotice context="write"` 加重试（:41-43）；`409 backup_busy` 用 `isHttpFailure(failureOf(err), 409, "backup_busy")` 区分成普通提示（:27、:40）。
- 轮询：`useBackupOverview` 的 `refetchInterval` 只在 `status.running` 时返回 2000ms（features/system/hooks/use-backup-overview.ts:7、:16）。
- 即改即存（features/system/components/backup-retention-section.tsx）：`SettingsGroup status={<p role="status">…已保存</p>}`（:52-63）；会删除文件时先由服务端回答，再弹 `ConfirmDialog`（:231-247）；保存失败恢复原值并显示 `FailureNotice`（:230）。
- mutation 后的失效：`useStartBackup` 的 `onSettled` 先检查 `scope.isCurrent()` 再 `invalidateQueries(systemKeys.backups(generation))`（features/system/hooks/use-start-backup.ts:11-17）。

**成员（`/settings/members`）**：列表加对话框。

- `app/routes/members-page.tsx:5-10`：`<Page title="成员"><MemberManagement /></Page>`。
- `features/members/components/member-management.tsx:22-80`：`SettingsGroup title="成员列表" action={<Button variant="primary" …>新建成员</Button>}`，卡片里先是结果提示 `MemberCommandNotice`，再是 `QueryContent` + `MemberTable`；停用与撤销会话走确认对话框，命令状态由 `useMemberCommands` 持有。

**Agent 栏的服务端开关（与“总开关”最像）**：`features/steward/settings/members-section.tsx:19-42`

```tsx
<SettingRow label="允许普通成员使用 Agent" description="…">
  {({ controlId, descriptionId }) => (
    <Switch id={controlId} aria-describedby={descriptionId}
      checked={setEnabled.isPending ? setEnabled.variables : data.members_enabled}
      disabled={setEnabled.isPending}
      onChange={(checked) => setEnabled.mutate(checked)} />)}
</SettingRow>
{setEnabled.isError ? <FailureNotice error={setEnabled.error} context="write" title="成员开关没有改成" /> : null}
```

对应的 mutation：成功时 `setQueryData` 写入响应，失败时 `invalidateQueries`（features/steward/hooks/use-agent-settings.ts:94-102）。

**扫描目录的“每天自动扫描”开关**：`features/library/components/scan-schedule-control.tsx:20-55`。保存中显示 `save.variables`，失败时回到服务器的值，并出 `Notice tone="error"` 和“重试”。hook 在 features/library/hooks/use-scan-schedule.ts:21-34：`scope: { id: "scan-schedule" }` 让连续改动按顺序发送，`onSuccess` 先 `cancelQueries` 再 `setQueryData`。

**对 E1 的含义**：设置栏目的新增点有三处：settings-sections.ts 的类型与数组、router.tsx 的 `AdminGate + LazyPage + addChildren`，以及“七栏”的两处测试断言。服务端开关已有两种写法：members-section 是乐观显示 `variables`，scan-schedule 是带 mutation scope、失败回滚。页面骨架通常是“路由文件 `Page` + feature 的 `SettingsGroup` 区段”。

---

## 2. 操作记录页与 `features/governance`

### 2.1 页面与视图

- `app/routes/operations-page.tsx:12-19`：`<Page title="操作记录" description="只有最近一次修改可以撤销。">`，内容是 `OperationsView`。命令来自 `useReleaseCommands({ refresh: useReleaseDataRefresh(), onMoved: stayOnPage })`（:13-14）。
- `features/governance/components/operations-view.tsx:63-123`：`useOperations` 取一页；`useClampedPage` 处理页码越界，用替换历史的方式跳到末页（:71-77）；`SettingsGroup` 的标题随筛选变化（:86）；`footer` 里是“共 N 条记录”和 `Pagination`，`renderLink` 生成带 `page` 的 `Link`（:88-104）；空态是一句 `<p>`，不是 `EmptyState`（:111）；`RevertOperationDialog` 在区块外（:120）。
- 筛选说明 `FilterNote`（:33-56）：“只显示…的记录。查看全部记录”。

### 2.2 筛选现状：只有目标筛选，没有 `source/provider/batch_id`

- 地址参数只有 `page`、`release`、`group`、`artist`（features/governance/operations-search.ts:5、:11-20），router.tsx:437-446 只把这四个传给页面。
- `OperationListParams` 也只有 `releaseId`、`groupId`、`artistId`（features/governance/api/endpoints.ts:105-115）。`fetchOperations` 只设置 `release_id`、`group_id`、`artist_id`（:118-124），**没有 `source`、`provider`、`batch_id` 参数**。
- 查询键 `governanceKeys.operationPage(generation, { releaseId, groupId, artistId, page, pageSize })`（features/governance/query-keys.ts:12-13），键里没有这三项。
- `useOperations`：`OPERATION_PAGE_SIZE = 20`（use-operations.ts:7），`keepPreviousData`、`staleTime: 0`（:27-36）。
- 后端合同里的新参数：`GET /api/v1/operations` 增加 `source`（只接受 `curation`）、`provider`、`batch_id`，与目标筛选是“并且”（spec/backend/curation-contracts.md:146）。前端的目标筛选之间是并集（endpoints.ts:110-113 的注释）。
- 测试替身 `src/test/governance-data.ts:572-587` 的 `respondToOperations` 只处理 `release_id`、`group_id`，不处理 `artist_id`、`source`、`provider`、`batch_id`。Playwright 的受控后端复用它（e2e/support/api-mock.ts:490）。

### 2.3 解码层已认识自动整理的记录

- `OPERATION_TYPES` 已含 `release_auto_correction`、`release_external_artwork`、`track_external_lyrics`、`external_link`（features/governance/api/decoders.ts:198-213）。
- 各自的 `changes` 解码器：自动补全 :230-240，外部封面 :243-253，外部歌词 :254-261，外部对应 :264-279；按 kind 分派在 :350-366。
- `origin`：`source` 是 `chat | mcp | curation`，另有可选的 `provider_kind`、`provider`、`basis`（≤500 字节）（:371-380）。
- 每条记录带可选的 `batch_id`（:385-386）。样本里撤销记录的 `origin` 是 null，也没有 `batch_id`（operations/auto-list.json 等）。

### 2.4 列表与 `change-summary.ts` 的用法

- `OperationList`（features/governance/components/operation-list.tsx:104-133）：`DataTable` 有四到五列：时间（行标题）、操作者、对象（`showRelease`）、改动、操作。
  - `ActorCell` 在用户名下放 `StatusTag`，文字来自 `operationOriginLabel(item.origin)`（:73-85）。
  - `ChangeCell` 是 `operationTypeLabel(item)` 加 `changeLines(item)` 的逐行列表（:87-98）。
  - 操作列：`notRevertibleReason(item)` 不为 null 时写原因，否则是“撤销”按钮，按钮里的 `VisuallyHidden` 写时间与类型（:112-129）。
- `change-summary.ts` 已覆盖自动整理：
  - `operationOriginLabel` 对 curation 写“自动整理 · {provider}”（change-summary.ts:9-16，:12）。**`origin.basis` 没有被任何界面代码读取**（全仓 grep `basis` 只命中 decoders.ts:375）。
  - `sourceName`：musicbrainz、coverartarchive、lrclib 的显示名（:19-24）。
  - `operationTypeLabel` 的四个类型名及其撤销名（:39-46、:61-64）。
  - `linesOf` 写外部封面、歌词、对应和自动补全的改动行，`linkMethodLabels` 给五种依据的中文名（:112、:170-197）。
  - `notRevertibleReason` 写自动类不能撤销的原因（:230-235）。
  - `revertLines` 是撤销预览（:216-218）。

### 2.5 单条撤销

- `RevertOperationDialog`（features/governance/components/revert-operation-dialog.tsx:60-112）：`ConfirmDialog` 的标题按类型取，含四种自动类（:43-53）；正文是“撤销 {用户} 在 {时间} 做的…会改回：”加 `revertLines` 列表（:97-108）；结果不确定时按钮变成“重试撤销”，沿用同一个键（:76、:85-88）。
- 打开与关闭由 `useRevertFlow` 管，撤销后把焦点带到区块标题（features/governance/hooks/use-revert-flow.ts:10-35）。
- 请求是 `revertOperation(operationId, idempotencyKey)`，没有请求体（endpoints.ts:127-129）；幂等键由 `useReleaseCommands` 的 `submit` 用 `newIdempotencyKey()` 生成，`retry` 沿用（features/governance/hooks/use-release-commands.ts:194-215）。
- 撤销后的失效：`onSettled` 让 `governanceKeys.all(generation)` 失效，再调 app 传入的 `refresh()`（use-release-commands.ts:160）。`refresh` 是 `useReleaseDataRefresh`：目录、listening、扫描报告、搜索、`playerKeys.allLyrics`（app/routes/use-release-refresh.ts:16-31）。

### 2.6 按批与按来源撤销

前端没有 `POST /api/v1/admin/curation/reverts` 的请求函数、解码器或界面（grep `curation` 在 `src/` 只命中 governance 的解码器、change-summary 和 api-contract.test.ts）。

**对 E1 的含义**：自动整理记录的解码、类型名、改动行、不能撤销的原因和单条撤销对话框都已经有了。还没有的是：`source/provider/batch_id` 筛选（地址参数、请求参数、查询键、测试替身四处都没有）、`basis` 的展示，以及批次与范围撤销的接口层。

---

## 3. API 层约定

### 3.1 请求函数 `shared/api/request.ts`

- `ApiPath` 是 `` `/api/v1/${string}` ``（request.ts:5）。`ApiRequestSpec` 有 `method`、`path`、`body`、`idempotencyKey`、`signal`、`keepalive`、`session`（:7-17）。
- `apiRequest(spec, decode)` 的请求固定 `credentials: "same-origin"`、`cache: "no-store"`、`X-Request-ID`；有 body 时设 `Content-Type: application/json`；有幂等键时设 `Idempotency-Key`（:56-68）。返回 `{ status, data }`（:20）；204 时解码器拿到 null（:75）。
- 失败一律抛 `ApiError`：网络失败是 `network`，中止是 `aborted`（:77-80）；非 2xx 先用 `decodeErrorEnvelope` 解出 `{error:{code,message}, request_id}` 再抛 `http`（:89-109）；成功响应解码失败是 `invalid_response`（:111-115）。401 `unauthorized` 会通知会话边界（:96-98）。
- 错误信封解码器：shared/api/envelope.ts:5-13（`code` 匹配 `^[a-z0-9_]+$`）。
- 公开入口 `shared/api/index.ts:3`：`export * as decode from "./decode"`，feature 里写成 `import { decode as d } from "../../../shared/api"`。另有 `failureOf`、`isHttpFailure`、`isOutcomeUncertain`、`describeFailure`（:5-6）。
- 幂等键 `newIdempotencyKey()` 生成 UUID v4（shared/lib/ids.ts:18-27）；`isUuid`（:31）用于路由参数校验。

### 3.2 解码器 `d`（shared/api/decode.ts）

- 原语：`object`、`optional`、`nullable`、`string({min,max,pattern})`、`boolean`、`integer`、`number`、`timestamp`、`uuid`、`literal`、`oneOf`、`array(item, {max, min?})`（必须给上限，`unbounded` 显式表示没有上限），以及 `defineDecoder`、`Infer`、`DecodeError`（decode.ts:7-153）。
- feature 的写法（features/system/api/decoders.ts:47-51）：`export const decodeX = d.defineDecoder(xDecoder); export type XDTO = d.Infer<typeof xDecoder>;`。governance 的导出在 decoders.ts:660-695。
- 未知枚举整体拒绝（例如 decoders.ts:365 的未知 kind）；后端新增类型时，样本契约测试先失败。

### 3.3 endpoints 的写法

- 每个接口一个具名函数，带中文注释，返回 `Promise<ApiResponse<DTO>>`（features/system/api/endpoints.ts:17-53）。
- 写接口带 `idempotencyKey` 参数（governance endpoints.ts:58-59 的说明：写接口都要求幂等键与同源请求，ID 先用 `isUuid` 校验）。
- 查询串用 `URLSearchParams` 拼（endpoints.ts:119-123）。

### 3.4 Query keys 与失效

- 查询键都以 `["viewer", 代次, feature, …]` 开头，用 `viewerQueryKey(generation, ...parts)` 构造（features/auth/session/session-context.tsx:56-58）；组件不手写查询键（spec/frontend/state-management.md:54-55）。
- 写法示例：`systemKeys`（features/system/query-keys.ts:1-6）、`governanceKeys`（features/governance/query-keys.ts:4-38）。`governanceKeys.all` 是前缀，修改后整体失效（:6）。
- 全局默认：`staleTime: 30_000`，重试策略 `shouldRetryQuery`（app/runtime.ts:37）。
- hook 的一般写法是 `useViewerScope()` 取 `generation`，`useQuery({ queryKey, queryFn: ({signal}) => fetchX(signal), select: (r) => r.data })`（features/library/hooks/use-scan-schedule.ts:8-15）。
- mutation 后的三种写法：
  - `setQueryData` 写入响应：use-scan-schedule.ts:28-32、use-agent-settings.ts:99；
  - `invalidateQueries`：use-start-backup.ts:16；
  - 先 `scope.isCurrent()` 防旧会话：use-start-backup.ts:16、use-scan-schedule.ts:29。

### 3.5 `src/test/api-contract.test.ts`

- 样本来自 `backend/internal/app/testdata/api-samples`（src/test/api-samples.ts:6），格式是 `{status, body}`。
- `successContracts`（api-contract.test.ts:139 起）把样本名映射到 `{ status, decode, preservesWireShape? }`。自动整理相关的已登记项：`corrections/get-auto`（:513）、`operations/auto-list`/`auto-reverted`（:514-515）、`external-*`（:517-518）、`link-*`（:519-520），以及 `tracks/lyrics-online`（:399）。
- `unconsumedContracts`（:837-851）：注释写“后端已有接口、前端还没有消费的样本……界面在子任务 E1 才做；那时把它们移到 successContracts 并配上解码器”。列出的十个是 `curation/batch`、`batches`、`progress`、`scope-reverted`、`settings-disabled`、`settings-enabled`、`settings-paused`、`source-updated`、`sources`、`suppressions`。
- 双向核对（:1004-1016）：每个样本必须在某一类契约里，每个契约名必须有样本。`successContracts` 里的样本要被 decoder 完整解码，默认还要求 `toEqual(sample.body)` 往返无损（:1018-1025）。所以移过去时，解码器必须保留样本的每个字段（除非标 `preservesWireShape: false`）。
- 错误样本 `errorContracts`（:619 起），已有 `errors/revert-conflict`（:788）。错误样本目录里没有 `curation_disabled`、`batch_closed`、`curation_paused` 的样本（只有 `scan-running`、`revert-conflict`、`already-reverted`、`not-revertible`）。
- 样本形状（backend/.../api-samples/curation/）：
  - settings：`{active, enabled, enabled_at, enabled_by:{id,username}|null, inactive_reason, paused}`；
  - sources：`{items:[{id,name,capabilities[],enabled,active,inactive_reason}]}`；
  - batches：`{items:[{id,tier,opened_at,closed_at,operations,reverted}], pagination}`；
  - progress：`{state, rule_version, releases:{total,processed,matched,uncertain,no_match,try_later}, pending:{waiting_model}, batch|null, last_pass_finished_at}`，样本里 `state="unavailable"`；
  - scope-reverted：`{reverted, skipped[], next_cursor}`；
  - suppressions：`{items:[{id,target_type,target_id,subject,value_hash,value,operation_id,created_by,created_at}], pagination}`，其中 `created_by` 是 UUID 字符串，不是对象。

**对 E1 的含义**：新接口的路径是 endpoints（具名函数、可选幂等键）→ decoders（`d` 原语，往返无损）→ query-keys（`viewerQueryKey`）→ hooks（查询、带失效的 mutation）。十个 curation 样本从 `unconsumedContracts` 移到 `successContracts` 时，必须有往返无损的解码器。

---

## 4. 发行详情上的字段展示与修正入口

- 详情解码器的 `corrected_fields` 只列人工修正的字段名（features/catalog/api/decoders.ts:249；对比视图 :133）。合同说面向成员的接口不变，`corrected_fields` 仍只列人工修正（spec/backend/curation-contracts.md:149）。**发行详情的响应里没有自动层的字段标记。**
- 用 `isFieldCorrected(correctedFields, field)` 判断，专辑艺人的修正同时覆盖“艺人”（features/catalog/format.ts:73-75）。
- 标签组件 `ManualCorrectionTag`：`StatusTag tone="accent"`，`PencilLine` 图标，文字“人工修正”（features/catalog/components/manual-correction-tag.tsx:5-10）。它出现在四处：
  - 页头概况行：features/catalog/components/album-header.tsx:87、:126-132；
  - 侧栏发行信息：features/catalog/components/release-facts.tsx:39-42；
  - 证据区：features/catalog/components/evidence-section.tsx:16、:155-163；
  - 版本对比：features/catalog/components/comparison-table.tsx:45、features/catalog/comparison.ts:107。
- 修正面板 `GET /api/v1/releases/{id}/corrections`：`fetchCorrections`（governance endpoints.ts:62-64）。每个字段的 `auto` 已经解码：`{mode, value?, operation_id, provider_kind, provider}` 或 null（governance decoders.ts:30-48）。样本 `corrections/get-auto` 已经往返（api-contract.test.ts:513）。
- **编辑对话框不显示 `auto`**：`EditReleaseDialog` 的 `FieldEditor` 写“扫描值”（features/governance/components/edit-release-dialog.tsx:52-53）；只有 `field.correction !== null` 时才写“当前显示 = effective_value”（:60-63）；legend 的标签只看 `correction`（:44-48）；三选一是“使用扫描值 / 自定义 / 留空”（:29）。草稿模型 `field-draft.ts:67-71` 也只看 `scan_value` 和 `correction`。全仓只有 change-summary.ts:106、:193-194 读了 `.auto`，那里描述的是操作记录的前后态。
- 详情底部的“修改记录”：`ReleaseHistory` 是一个 Disclosure，展开后取这个版本和这张专辑最近 5 条，共用 `OperationList`，自动整理的记录也会带“自动整理 · provider”标签（features/governance/components/release-history.tsx:18-39、:54-66）。由 app 组合进页面（app/routes/album-detail-page.tsx:156）。
- 封面来源：`artworkDecoder` 只有 `resource_id/mime/width/height`，没有来源字段（features/catalog/api/decoders.ts:19-24）。合同说摘要里的 `artwork` **没有来源字段**，哪些是自动补的要从操作记录看（curation-contracts.md:282）。
- 歌词来源：`source` 解码为 `embedded | external | online`，注释写明“面板不显示来源”（features/player/api/decoders.ts:38-39）；lyrics-panel.tsx 没有读 `source`。

**对 E1 的含义**：所有成员可读的发行详情没有“哪些字段是自动补的”这个信号，`corrected_fields` 只覆盖人工修正。管理员可用的数据源是修正状态的 `auto`（已解码但未显示）和操作记录。封面在读取接口里没有来源；歌词有 `online`，但界面不显示。

---

## 5. 扫描完成的信号与一次性提示

- 活动扫描：`useActiveScan` 读 `GET` 活动扫描，只在有扫描时每 5s 轮询（features/library/hooks/use-active-scan.ts:9、:16-24）。活动扫描的 ID 变化或消失时，让扫描记录失效（:26-34），并调用 `useScanCatalogRefresh`（:35）。
- `useScanCatalogRefresh`（features/library/hooks/use-scan-catalog-refresh.ts:19-39）：观察到的“运行中”扫描结束、消失或被换掉时，调用 `refreshCatalog()`。第一次读到时只记下来、不触发。它只能观察到本页打开期间结束的扫描。
- 消费处：概览的 `ScanSummary`（features/library/components/scan-summary.tsx:15-68，用在 app/routes/overview-page.tsx:37）和扫描目录页的 `ScanSection`（features/library/components/scan-section.tsx:14-40）。
- 扫描记录 `useScanHistory(page)` 不轮询，由上面的失效带动（features/library/hooks/use-scan-history.ts:13-21）；`ScanDTO` 有 `status`、`trigger`、`finished_at`（features/library/api/decoders.ts:181-185）。
- 目录代次：`useCatalogEpochSync` 在已登录外壳里读目录状态，`epoch` 变大时让相关缓存失效（app/shell/use-catalog-epoch-sync.ts:11-40）。
- 类似的后台进度：`useAnalysisProgress` 只在 `running/waiting_for_scan` 时 60s 轮询（features/library/hooks/use-analysis-progress.ts:7-16）；`AnalysisSummary` 是概览“资料库状态”里的一行（features/library/components/analysis-summary.tsx:12-30）。概览的状态表组合在 app/routes/overview-page.tsx:36-42。
- Toast 和一次性提示：
  - **没有全局 toast**。`Notice` 的注释写“结果就近显示在操作区，不使用全局 toast”（shared/ui/notice.tsx:24-27），组件目录与治理约定也这样写（spec/frontend/component-catalog.md:13、spec/frontend/governance-components.md:6）。
  - 没有“横幅”或“只提示一次”的组件或模式：grep `toast|banner|onboard|seen|一次性` 没有相关命中。
  - `localStorage` 和 `sessionStorage` 只用于播放器：设备 ID、控制目标、音量、旧队列导入（features/player/zones/device.ts:1、control-target.ts:1、engine/persistence.ts:2-3、player-provider.tsx:16-38）。没有界面提示“已关闭”的存储。
  - 现有的“关闭”都是命令结果提示的 `mutation.reset()`（例如 features/members/hooks/use-member-commands.ts:95），不持久化。
- 质量规范禁止在浏览器存储里放令牌（spec/frontend/quality-guidelines.md 的 Forbidden Patterns），没有禁止放界面偏好。

**对 E1 的含义**：现有的“扫描结束”信号是轮询活动扫描，并在本页观察到运行中变为结束时触发，没有“首次扫描完成”的服务端标志。没有 toast、横幅或已关闭状态的存储模式可以直接复用，现有约定是就近的 `Notice`。

---

## 6. 设计规范要点

- 色彩（frontend/DESIGN.md:75-111）：单一金色强调。`--color-accent` 只用于描边、细线和进度；文字和链接用 `accent-700`；次要文字 `neutral-700`；主按钮是透明底加描边。不能未经审阅扩出红绿状态色。缺失、冲突、失败必须有文字和图标，不只靠颜色。
- 间距：`--space-1/2/3/4/6/8` = 4/8/12/16/24/32px，组件只用这组 token（DESIGN.md:139）；圆角 `--radius-sm/md/lg` 是 2/4/7px（:143）。
- 响应式：<768px 页面内边距 16px，控件最小高 44px，底部标签栏（DESIGN.md:197-199）；表格按**容器**宽度 <640px 堆叠（:201、shared/ui/data-table.tsx:39-43）。
- 设置页：分组卡片，每行“名称与说明 | 控件”右对齐；从属项随开关出现或消失，不用禁用态（spec/frontend/interface-layout-guidelines.md:16；DESIGN-components.md“界面整理”一节）。主操作在卡片标题旁（interface-layout-guidelines.md:107）。
- 数据与操作状态的通用要求（DESIGN-components.md:76-93）：首次加载的占位保留结构；区分空、筛选无结果、无权限；409 保留草稿；写入中防止重复提交。
- 治理组件：结果就近显示（`ReleaseCommandNotice`），不用 toast；长内容在表格和对话框里 `overflow-wrap: anywhere`，操作列和时间不换行（spec/frontend/governance-components.md:6、:14、:23-24）。
- 390 宽：规范里没有“390 宽”的专门规则。验收宽度是五种：375/768/1024/1280/1440（e2e/support/checks.ts:5；spec/frontend/quality-guidelines.md 的“响应式与无障碍”行）。DESIGN-acceptance.md:135 记了“桌面 1440 与手机 390 已逐项核对”；390 也出现在若干 e2e 用例里（第 8 节）。
- 测试要求：
  - 层次（spec/frontend/quality-guidelines.md“已采用的测试层次”）：单元与组件和被测代码同目录；`api-contract.test.ts` 做往返；页面级测试在 `src/app/*.test.tsx`，经 `renderApp` 用有状态替身；Playwright 对生产构建做冒烟，用受控后端，五种宽度、axe、最小字号 12px，≥768 时窗口不滚动。
  - 阶段分层模板（spec/frontend/quality-verification-layers.md:33-50）：最小 Vitest 命令 → 共用的接口行为（`src/test/*-data.ts`，Vitest 与 Playwright 共用）→ 页面级 → 浏览器冒烟 → 真实服务。
  - 目录归属：`curation` feature 写着“随对应获批阶段建立”（spec/frontend/directory-structure.md:6）。e2e 只能导入纯模块（decoders 与 `*-data.ts`），不能导入带 CSS 的应用代码（:63-68）。

**对 E1 的含义**：视觉上只能用金色加中性色和文字图标表达状态，设置用卡片加行，表格按容器堆叠，结果就近显示。验收宽度沿用五种（另有 390 的若干用例），每个阶段按“共用接口行为 → 页面级 → 冒烟”分层。新 feature 目录 `features/curation` 在规范里已经预留。

---

## 7. 通用组件（`shared/ui`，入口 shared/ui/index.ts:1-31）

| 需要 | 现有组件 | 位置与用法 |
| --- | --- | --- |
| 开关 | `Switch` | shared/ui/switch.tsx:13-15：`role="switch"` 的原生 checkbox，`checked` 加 `onChange(checked)`；名称由 `SettingRow` 的 label 经 `id={ids.controlId}` 给，或者直接给 `aria-label`。用例见 features/player/components/playback-settings-form.tsx:89、scan-schedule-control.tsx:44、members-section.tsx:28 |
| 设置卡片与行 | `SettingsGroup`、`SettingRow` | shared/ui/settings-group.tsx:37-69：有 `title/status/action/headingRef/footer`，卡片宽 <640 时窄；:90-114：有 `label/description/nested/plainLabel`，children 可以是 `(ids)=>…` |
| 确认对话框 | `ConfirmDialog` | shared/ui/confirm-dialog.tsx:7-22：`open/title/confirmLabel/pending/error/onConfirm/onCancel`，初始焦点在“取消” |
| 普通对话框 | `ModalDialog` | shared/ui/modal-dialog.tsx:6-24：原生 `<dialog>`，`titleId`、`initialFocus`、`onRequestClose`；用例见 members-section.tsx:160-179 |
| 表格 | `DataTable` + `Column` | shared/ui/data-table.tsx:5-43：`caption/columns/rows/rowKey`，列选项有 `rowHeader/numeric/hideLabelWhenStacked/truncate/width/secondary`；空列表由调用方处理 |
| 标签/徽标 | `StatusTag` | shared/ui/status-tag.tsx:4-11：`tone` 是 `neutral|accent|outline`，可带 `icon` |
| 进度 | **没有共用组件** | 扫描进度是 library 私有的：features/library/components/scan-progress.tsx（`role="progressbar"`，数值来自服务端）和 scan-summary.tsx:40-56 的细进度条；状态行可用 `StatusRow` 的 `meter` 插槽（shared/ui/status-table.tsx:20-39） |
| Toast | **不存在**（见第 5 节） | 结果提示用 `Notice`（shared/ui/notice.tsx:5-28，`tone`：info/success/warning/error，有 `action`）和 `FailureNotice`（shared/ui/failure-notice.tsx:18-29，`context="write"`、`messages`） |
| 分页 | `Pagination` | shared/ui/pagination.tsx:12-27：`label/page/pageCount/renderLink(page, props)`，用例见 operations-view.tsx:94-101 |
| 加载/错误 | `QueryContent`、`Skeleton` | shared/ui/query-content.tsx:24-38；skeleton.tsx:11 |
| 空状态 | `EmptyState` | shared/ui/empty-state.tsx:4-16：`icon/title/children/action` |
| 状态表 | `StatusTable`、`StatusRow`、`PendingStatusRow` | shared/ui/status-table.tsx:8、:39、:65（概览“资料库状态”） |
| 折叠 | `Disclosure` | shared/ui/disclosure.tsx:20（“修改记录”） |
| 其他 | `Segmented`、`Panel`、`Page`、`MenuButton`、`FilterChip`、`FilterField`、`VisuallyHidden`、`Button`（primary/secondary/icon，`busy`） | index.ts:2-31 |

**对 E1 的含义**：开关、卡片、确认框、表格、标签、分页、空状态和加载错误都有现成组件。进度条和 toast 不在 `shared/ui`：进度只有 library 私有的实现和 `StatusRow.meter` 插槽，toast 按约定不用。

---

## 8. Playwright 冒烟

- 配置：frontend/playwright.config.ts:9-29。`testDir: "e2e"`，只跑 Chromium（Desktop Chrome），locale 是 zh-CN；`webServer` 先 `npm run build` 再 `vite preview` 到 127.0.0.1:4173，本机复用已在运行的服务。真实服务用例另用 `playwright.real.config.ts`，收集 `e2e/real/*.real.ts`。
- 运行：`cd frontend && npm run test:e2e`（package.json:16，等于 `playwright test`）；单文件可以 `npx playwright test e2e/system-backups.spec.ts`。
- 夹具（frontend/e2e/support/fixtures.ts:9-37）：`backend.start({ signedInAs: "curator" | "listener" })` 安装受控后端，结束时断言没有第三方请求、没有脚本或控制台错误。
- 受控后端：frontend/e2e/support/api-mock.ts，拦截 `**/api/v1/**`（:605）；`guarded(method, path, requireAdmin, handler)` 处理 401/403（:251-256）；未登记的路径返回 `errors/not-found-api`（:602）；`mock.calls` 和 `callsTo` 记录请求（含 `idempotencyKey`）（:618）。操作记录走 `respondToOperations(mock.governance, search)`（:490）。**没有任何 `/admin/curation/*` 路由。**
- 检查函数（frontend/e2e/support/checks.ts）：`widths = [375, 768, 1024, 1280, 1440]`（:5）；`expectNoHorizontalOverflow`（:8）；`expectNoDocumentScroll`（:25）；`expectNoSeriousViolations`（axe WCAG 2.1 A/AA，:37）；`expectMinimumFontSize`（:45）；`expectVisibleFocusRing`（:62）；`expectSingleTextLine`（:75）。
- 范例：
  - 桌面与手机两宽：frontend/e2e/system-backups.spec.ts:5-53 对 `[1440, 375]` 循环，`page.setViewportSize`、`backend.start`、`page.goto("/settings/backups")`，然后检查标题、表格行数、单行文字、不溢出和 axe，再点“立即备份”看忙碌态，并断言 `mock.callsTo("POST", …)` 只有一次。
  - 390 宽：同一文件 :77-92 用 `{ width: 390, height: 844 }` 验证行尾菜单靠近视口底边时仍保持打开；frontend/e2e/shell.spec.ts:53-54 用 `test.describe("390px 窄屏外壳") + test.use({ viewport: { width: 390, height: 844 } })`。
  - 五宽循环：frontend/e2e/governance.spec.ts:499-516（操作记录页）和 :518-540（撤销对话框：在视野内，对话框自身 `scrollWidth - clientWidth <= 0`）。
- 进入设置的辅助函数：frontend/e2e/support/settings.ts:31-41 的 `openSettings`，宽屏点齿轮，窄屏经“我的”页进入。

**对 E1 的含义**：冒烟用例跑在生产构建加受控后端上，curation 接口需要先在 api-mock.ts 登记，并在 `src/test/` 里补一个纯行为模块供 Vitest 和 Playwright 共用。宽度检查沿用 `widths` 或 `[1440, 375]` 加 390 的写法。

---

## 不存在 / 没找到

- 前端没有任何 `/api/v1/admin/curation/*` 的请求函数、解码器、查询键、hook 或界面，也没有 `features/curation` 目录。
- 操作记录没有 `source`、`provider`、`batch_id` 筛选（地址参数、请求参数、查询键、测试替身 `respondToOperations` 都没有）。
- `origin.basis` 已解码，但界面不显示。
- `EditReleaseDialog` 不显示修正状态里的 `auto`。
- 发行详情（`corrected_fields`）没有自动补全字段的标记；封面摘要没有来源；歌词的 `online` 已解码，但面板不显示。
- 没有全局 toast、横幅或“只提示一次”的组件，也没有持久化“已关闭”的存储（`localStorage` 只给播放器用）。
- `shared/ui` 没有通用的 Progress 组件。
- 没有“首次扫描完成”的前端信号，只有“本页观察到运行中扫描结束”。
- `src/test/` 和 `e2e/support/` 没有 curation 的替身行为；错误样本里没有 `curation_disabled`、`batch_closed`、`curation_paused`。
- 规范里没有“390 宽”的专门规则，验收宽度是五种（375/768/1024/1280/1440）。
