# 自动整理 E2：界面设计

## 布局

“自动整理”页（`app/routes/curation-page.tsx`）各组依次：开关、进度、**模型判断**（新）、**例外**（新）、外部源、批次、不再提出。例外排在外部源之前，因为它要人处理。两组都沿用 `SettingsGroup` + `SettingRow`、`DataTable`、`ConfirmDialog`、`Pagination`、`QueryContent`，不新造组件与颜色。

### 模型判断（`components/curation-model-section.tsx`）

| 行 | 内容 |
| --- | --- |
| 状态 | `modelStateText[state]`；`unavailable` 时接原因文案（`modelUnavailableText`），`not_configured`/`key_needs_reentry`/`tool_unsupported` 且当前管理员是开启者时附“去 Agent 设置”链接 |
| 整理用的模型 | 只读时“与对话模型相同”或模型名 + “更改”；编辑时文本框 + 保存/取消（照 `source-key-row` 的行内写法，`SettingRow nested` 不用）。U11 提示放在这一行的 description |
| 每日上限 | 只读“每天 1,000 万 token”/“不限” + “更改”；编辑时数字框（万 token，整数 1–100 000 000）+ “不限”复选框 + 保存/取消 |
| 今天 | `输入 a · 输出 b，共 c`；有上限时前面放进度条（照“已处理”的 meter），`limit_reached` 时 description 写“到了上限，明天继续” |
| 待定项 | `等模型 a · 处理中 b · 等你处理 c · 搁置 d`（全 0 时“没有”） |
| 后台的会话 | `fetchCurationConversations(1, 3)`：每个一行链接（标题、时间、项数摘要）到 `/steward/$conversationId`；没有时这一行不出现 |

从未打开过（`enabled_at === null`）时整组只一行“打开总开关并配置管家的模型接口之后，程序拿不准的由模型判断。”

### 例外（`components/curation-exceptions-section.tsx`）

`DataTable` 列：时间（行头）、发行（标题 · 艺人，链接 `/settings/operations?release=`）、提出的修改、审查、操作。

- 提出的修改：`exceptionChangesText(exception)` 输出若干行：
  - `resolve_release_match`：“对应到 MusicBrainz 发行 <id>”（链接 `https://musicbrainz.org/release/<id>`，`rel="noreferrer"`）、“曲目对应 n 首”、字段行“标题：A → B”、归组行“归入已有专辑”/“建立专辑对应”。
  - `correct_release`：字段行“标题：A → B”（空值写“（空）”）。
  - 字段名用 `releaseFieldLabels` 的同一份表（从 steward 导出或在 curation-text 里复制一份——选择：在 `shared` 里没有，复制会漂移；从 `features/steward` 导出 `releaseFieldLabels`）。
- 审查：“没通过”/“转给你确认”（`source=policy` 写“按规则转给你确认”）+ 理由列表（不可信文本）+ 风险（高/中/低）。
- 操作：采纳、不采纳（各带 VisuallyHidden 的发行名）；`conversation_id` 非空且会话属于当前用户时（从会话列表无法判断归属；改为：当前管理员是开启者时显示“查看过程”，否则不显示）。

确认框：
- 采纳：标题“采纳这项修改？”；正文列出 `exceptionChangesText`，加“记为你的人工确认；字段修正写进人工修正。”；确认“采纳”。
- 不采纳：标题“不采纳这项修改？”；正文“以后程序与模型都不再提出这个值，可以在‘不再提出’里清除。”；确认“不采纳”。
- 错误：`exception_stale`、`exception_decided` 用专门文案（`describeFailure` 的 messages 参数），其余 `FailureNotice`；这两种错误同时让列表失效。

### 会话页（`features/steward`）

`ConversationDTO.kind === "curation"` 时：模式菜单不渲染；标题前加 `Badge`“自动整理”（中性色）；输入框占位提示改为“接着对话纠正：这里的消息按你本人的对话处理”。

## 数据与状态

- hooks（`use-curation.ts`）：`useCurationExceptions(page)`（键 `exceptionPage`，`placeholderData` 保留上一页）、`useAcceptException()`、`useDeclineException()`（成功后失效 exceptions、progress、batches；不采纳另失效 suppressions；同时调 app 的 `refresh` 让目录与详情更新）、`useCurationConversations()`（键 `conversations`，进度刷新时一起失效）。设置的修改复用 `useUpdateCurationSettings`（类型已扩展），成功后失效 progress。
- 路由：`/settings/curation` 的 search 加 `exception_page`（照 `marker_page`）。
- U11：从 `features/steward` 导出 `useStewardModels()` 返回 `{ model, reviewModel } | undefined`（读本人设置，已有查询），route 层算出 `sameReviewModel` 传给模型组。
- 测试替身：`test/curation-data.ts` 加 `exceptions`、`conversations`、`usage` 与模型状态；`fake-curation.ts` 实现 exceptions 三个接口、conversations、settings 的 model/daily_token_limit；`e2e/support/api-mock.ts` 同步。

## 审阅（规划子代理 Plan，2026-11-23；全部采纳，与上文冲突时以本节为准）

必须改：
1. 字段名不从 steward 取（steward 不对外提供私有模块，`directory-structure.md:139-140,150`）：用 governance 的 `correctionFieldLabels` 与 `displayValue`（`curation-text.ts:1` 已在用）。
2. 自动整理会话列表的端点与解码器放在 `features/curation/api`（路径是 `/admin/curation/*`），D 时放在 steward 的 `fetchCurationConversations`、`decodeCurationConversationPage` 移过来，契约测试改引用。
3. `preview` 解码为 `{changes:[{field, before, after}]}`（值用已有 `jsonValue`）。匹配预览：`external_link` 有前值时写“替换现有对应”；`track_links` 写“曲目对应 a → b 首”；`group_external_link` = “建立专辑对应”，`group` = “归入已有专辑”；`year` 数字、`genre` 数组交给 `displayValue`；`kind=recording_match` 链接 `musicbrainz.org/recording/`；`input.external_id` 先校验 UUID 再拼链接，`target="_blank" rel="noopener noreferrer"`。
4. 采纳的错误码全覆盖：409 `exception_stale`、`exception_decided`（含两人同时采纳的后到者）、`curation_disabled`（暂停、扫描中也是它）、`source_disabled`、`nothing_to_apply`；503 `source_unavailable`；400 `invalid_id`；404 `not_found`（按“已经被处理了”刷新）。
5. `source_unavailable`：多步匹配可能已写了一部分，例外停在 `accepting`（待处理列表只列 `open`），所以保留确认框与“重试”（同一人可重入），不立刻刷新；网络失败按 `onSettled` 刷新。
6. 失效：采纳、不采纳都失效 `governanceKeys.all`（照 `use-scope-revert.ts:75-77`）；`query-keys.ts` 补 exceptions、conversations；`useUpdateCurationSettings` 的入参类型真的扩展。
7. 会话页：`kind=curation` 时除模式菜单外，`onShiftTab` 切模式也要禁用；后台方案卡只读（不给确认、重新审查），另给 `curation_plan` 配文案以防万一；不改输入框 placeholder（它是 aria-label），长提示放 `hint`；打开 curation 会话不记进面板（`setDockConversation`）。

应该改（采纳）：
- 上限与模型两行即改即存，照 `backup-retention-section.tsx` 的“不限”数字框（回车或失焦提交，勾“不限”立即提交，失败恢复服务器值）；不用“更改/保存/取消”。
- 上限显示不假设整万：是整万时写“1,000 万”，否则写原 token 数；编辑框单位万，非整万时编辑框显示原值换算后的小数并禁止四舍五入（选择：非整万时编辑框改用 token 单位）。
- 模型名按 UTF-8 字节 ≤200 校验，提交前去首尾空格。
- U11 判断：有效整理模型 = `settings.model || 本人对话模型`（`progress.model.model` 在暂停、关闭、上限时为空串）；当前管理员不是开启者时不提示。
- “查看过程”只在当前管理员是开启者时显示；会话 404 时页面已有“这个对话不存在或已经删除”。
- 轮询：模型 `running`、`waiting_review` 时进度也按 10 秒刷新；例外与会话在进度里 `waiting_human`、`in_progress` 数变化时重新读取。
- 标签用 `StatusTag tone="neutral"`（没有 Badge）。
- “进度”组的“待定”行删掉（由模型判断组的“待定项”替代，旧文案过时）。
- 替身：`test/fake-steward.ts`、`e2e/support/steward.ts` 详情带 `kind`、改模式 409、会话列表路由；`respondToUpdateCurationSettings` 认 model 与上限。
- 规范：`interface-layout-guidelines.md` 的卡片数改为七张并写两组；`state-management.md` 补例外与会话的失效与轮询。
