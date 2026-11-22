# 自动整理 E1：技术设计

依据：后端合同 `.trellis/spec/backend/curation-contracts.md` 第 7、11、14、15 节；前端规范 `frontend/DESIGN.md`、`DESIGN-components.md`、
`.trellis/spec/frontend/interface-layout-guidelines.md`、`state-management.md`、`quality-guidelines.md`、`quality-verification-layers.md`、`governance-components.md`；现状见 `research/frontend-current.md`。

## 一、目录与依赖

新 feature `frontend/src/features/curation/`（规范已预留，调研 §6）：

```text
features/curation/
├── api/decoders.ts      # 十个样本的解码器（往返无损）
├── api/endpoints.ts     # settings、sources、progress、batches、reverts、suppressions
├── query-keys.ts        # curationKeys（viewerQueryKey 前缀 "curation"）
├── hooks/               # use-curation-settings、use-curation-sources、use-curation-progress、use-curation-batches、use-scope-revert、use-suppressions
├── components/          # 各区段与对话框（下表）
├── first-scan-hint.ts   # 一次性提示的显示条件与本地存储（纯函数 + try/catch）
└── index.ts
```

- `features/curation` 可以导入 `features/governance` 的 `sourceName`、`operationOriginLabel`（只读的展示函数）；`governance` 不导入 `curation`。
- 路由文件 `app/routes/curation-page.tsx` 只组合区段；`settings-sections.ts` 增加 `{ to: "/settings/curation", label: "自动整理", icon: Sparkles, adminOnly: true, group: "管理" }`，排在“操作记录”之前；
  `router.tsx` 照管理栏的写法 `AdminGate + LazyPage`。两处“七栏”断言改成八栏。

## 二、接口层

| 函数 | 接口 | 说明 |
| --- | --- | --- |
| `fetchCurationSettings` / `updateCurationSettings({enabled?, paused?})` | `GET/PUT /admin/curation/settings` | PUT 返回新设置 |
| `fetchCurationSources` / `setCurationSource(id, enabled)` | `GET /sources`、`PUT /sources/{id}` | |
| `fetchCurationProgress` | `GET /progress` | |
| `fetchCurationBatches(page)` | `GET /batches?page=&page_size=20` | |
| `revertCurationScope(scope, cursor, key)` | `POST /reverts` | 需要幂等键 |
| `fetchSuppressions(page)` / `clearSuppression(id)` | `GET /suppressions`、`DELETE /suppressions/{id}` | DELETE 是 204 |

- 解码器与样本一一对应，十个样本从 `unconsumedContracts` 移到 `successContracts`，默认往返无损。`inactive_reason`、`state`、`tier`、`target_type`、`subject` 等枚举用 `oneOf`，未知值整体拒绝
  （后端加新值时契约测试先失败）。`suppressions.value` 是任意 JSON，按不可信文本显示，解码为 `unknown` 的受限形状（照 governance 对 `origin` 的做法）。
- 查询键：`curationKeys.all(gen)`、`settings`、`sources`、`progress`、`batches(page)`、`suppressions(page)`。写入后：设置与源的 PUT 用 `setQueryData` 写回响应，并让 `progress`、`sources` 失效；
  范围撤销完成后让 `curationKeys.all` 与 `governanceKeys.all` 失效，并调 app 的 `useReleaseDataRefresh()`。
- 开关的 mutation 用 `scope: { id: "curation-settings" }`（连续切换按顺序发送，照扫描计划开关的做法），失败时回到服务器的值。

## 三、页面：`/settings/curation`

`<Page title="自动整理" description="打开之后，后台按缺口把曲库与外部资料对上，自动补封面、歌词与空着的信息；每一项都能撤销。">`，五个 `SettingsGroup`：

### 3.1 开关（`CurationSwitchSection`）

| 行 | 内容 |
| --- | --- |
| 自动整理 | `Switch`。从关到开先弹 `ConfirmDialog`：标题“打开自动整理？”，正文列出会发送给外部的内容（MusicBrainz：外部编号、条码、ISRC、厂牌与编号、标题与艺人、由曲目时长推出的光盘目录；Cover Art Archive：外部编号；LRCLIB：曲目标题、艺人、专辑名、时长），并写明“不发送音频、文件路径与这台服务器的信息；之后不再逐项询问”。关闭不确认 |
| 状态（只读） | 开着且生效：“由 {用户名} 在 {时间} 打开”；`inactive_reason=enabler_unavailable`：“打开它的管理员已不可用，后台已停止”；关着：不显示这一行 |
| 暂停 | 只在总开关开着时出现（从属项随开关出现，不用禁用态）。说明“暂停后台的匹配与下载，已写入的内容不变” |

### 3.2 进度（`CurationProgressSection`）

- 状态文字：`running` 正在整理、`waiting_for_scan` 等扫描结束、`idle` 已处理完，等新的内容、`paused` 已暂停、`disabled` 未打开、`unavailable` 后台整理没有运行。
- 进度条：`processed / total`（复用 `StatusRow` 的 meter 写法，或在 curation 里做一个 `role="progressbar"` 的细条，不进 shared/ui）。
- 计数行：对上 N、拿不准 N、没对上 N、稍后再试 N；“N 项等模型判断”（拿不准的交给模型层，没有配置时停在那里；D 上线前固定这样写）。
- 当前批次：开始时间与已写的记录数，链接到按批次筛选的操作记录。
- 轮询：`state` 是 `running` 或 `waiting_for_scan` 时 `refetchInterval` 10 秒，其余不轮询；页面切到后台时不轮询（TanStack 默认）。

### 3.3 外部源（`CurationSourcesSection`）

- 每个源一行：名字、能力（发行与录音与艺人 / 封面 / 歌词）、`Switch`；`active=false` 时在说明里写原因（`curation_disabled` 总开关没开、`enabler_unavailable`、`source_disabled` 已关闭）。
- 行尾 `MenuButton`：“撤销来自 {源} 的全部自动修改” → 范围撤销对话框（3.6）。

### 3.4 批次（`CurationBatchesSection`）

- `DataTable`：开始时间（行标题）、层（程序 / 模型）、改动数、已撤销、操作（“查看改动”链接到 `/settings/operations?batch=<id>`；“撤销这一批”按钮，全部已撤销时写“已全部撤销”）。
- 正在进行的批次（`closed_at` 为空）写“进行中”。分页 20 条，空态“还没有自动整理的记录”。

### 3.5 不再提出（`SuppressionsSection`）

- 说明“撤销过的自动修改不会再自动出现。清除一条，后台可以再次写入同一个值。”
- `DataTable`：时间、对象（发行 / 曲目 / 专辑，显示类型即可，不另查名字）、类别（字段名用 `correctionFieldLabels`；`artwork`、`artwork_source` 封面；`lyrics`、`lyrics_source` 歌词；`external_link` 外部对应；`group_join` 归组）、值（`value` 的展示：字段值用 `displayValue`，其余显示 `source` 与 `external_id`；不可信文本，`overflow-wrap:anywhere`）、操作“清除”（`ConfirmDialog`）。

### 3.6 范围撤销对话框（`ScopeRevertDialog`）

- 打开时生成幂等键；确认后循环 `POST /reverts {scope, cursor}`，每次带同一个键，直到 `next_cursor` 为 null；累加 `reverted` 与 `skipped`。
- 进行中按钮是忙碌态，不能关闭；完成后就近 `Notice`：“撤销了 N 条，跳过 M 条”，跳过的按 `code` 归类写原因（`revert_conflict` 之后又被改写、`already_reverted` 已撤销等，沿用 governance 的文案）。
- 网络失败或结果不确定：保留键与当前游标，按钮变成“继续撤销”。

## 四、操作记录的筛选与依据

- `operations-search.ts` 增加 `source`（只接受 `curation`）、`provider`（`^[a-z0-9._:-]{1,64}$`）、`batch`（UUID）；`OperationListParams` 与 `fetchOperations` 加 `source`、`provider`、`batch_id`；查询键带上它们。
- `FilterNote`：批次“只显示这一批自动整理的记录”；来源“只显示来自 {源名} 的自动整理记录”；两者与目标筛选同时存在时都写出。“查看全部记录”清掉全部筛选。
- `ChangeCell`：`origin.source=curation` 且有 `basis` 时，在改动行下加一行次要文字“依据：{basis}”（不可信文本，`overflow-wrap:anywhere`）。
- 测试替身 `respondToOperations` 支持这三个参数（Vitest 与 Playwright 共用）。

## 五、专辑详情

- 后端（Codex）：`catalog` 的发行详情与版本对比 DTO 增加 `auto_fields []string`（`json:"auto_fields,omitempty"`）：没有修正行、有自动值的字段名；专辑艺人的规则与 `corrected_fields` 相同
  （有效值取自自动层时“艺人”也算）。没有自动补全时省略，现有样本不变；新增一个样本 `releases/detail-auto`（或在现有自动整理样本里覆盖）。
- 前端：解码器 `auto_fields: d.optional(...)`；`AutoFilledTag`（`StatusTag tone="outline"`，`Sparkles`，“自动补全”）出现在 `ManualCorrectionTag` 出现的四处，判断函数 `isFieldAutoFilled` 照 `isFieldCorrected`。
- 编辑对话框：没有人工修正、修正状态里有 `auto` 时，在字段下写“当前显示的是自动补全的值：{值}（来自 {源名}）”；三选一的“使用扫描值”改名为“不修改（用扫描或自动补全的值）”。
- 歌词面板：`source="online"` 时在面板底部写“歌词来自网络”（来源名不在歌词接口里，不写源名）。

## 六、一次性提示与概览

- 条件（纯函数 `shouldShowFirstScanHint`）：管理员 ∧ 最近一次扫描记录里有 `status=succeeded` ∧ 设置 `enabled=false` 且 `enabled_at=null`（从未打开过）∧ 本地没有“不再提示”。
- 本地存储键 `roomusic.curation.first-scan-hint.<userId>`，读写都包在 try/catch 里，读不到当作没有关闭（提示照常出现，关闭后本次会话内不再出现）。
- 位置：概览主栏顶部的 `Notice tone="info"`，文字“曲库扫描好了。打开自动整理，可以自动补全封面、歌词与空着的信息。”，动作“前往设置”“不再提示”。
- “资料库状态”加一行 `CurationSummary`（管理员）：标题“自动整理”，值写状态文字与“已处理 N / M”，链接到设置；未打开时写“未打开”。

## 七、测试

| 层 | 内容 |
| --- | --- |
| 契约 | 十个 `curation/*` 样本往返；`auto_fields` 的新样本；`settings-sections` 八栏 |
| 共用替身 | `src/test/curation-data.ts`：有状态的设置、源、进度、批次、范围撤销（可设置分几段返回、某段失败）、标记；`api-mock.ts` 与 Vitest 替身都用它 |
| 单元 | `first-scan-hint.ts` 的条件与存储失败；进度文案；标记类别与值的展示；范围撤销的循环（分段、重试同键、跳过归类） |
| 页面级 | `src/app/curation-settings.test.tsx`：打开要确认、关闭不确认、暂停随开关出现、失败回滚；进度轮询只在工作时；源开关；按批与按来源撤销的分段与结果；清除标记；操作记录的三种筛选与依据；专辑详情的标签；概览提示的出现与关闭 |
| 冒烟 | `e2e/curation.spec.ts`：1440、375、390 三宽；设置页与确认框、范围撤销对话框、操作记录筛选；无横向溢出、axe、最小字号 |
| 真实服务 | `e2e/real/curation.real.ts`：总开关保持关闭（不让后台向真实外部服务发请求），读设置、进度、源，切换一个源与暂停，刷新后保持 |
| 后端 | `auto_fields` 的集成用例：只有自动值、有人工修正、专辑艺人联动、没有时省略；现有样本不变 |

## 八、取舍

| 取舍 | 选择 | 理由 |
| --- | --- | --- |
| 批次明细单独做页还是复用操作记录 | 复用（按批次筛选） | 操作记录已有改动行、撤销对话框、不能撤销的原因；单独做一页会重复 |
| 一次性提示的关闭存哪里 | 浏览器本地 | 只是一个查看者的便利；服务器已经有“打开过”这一事实，打开过之后由它决定 |
| 自动补全的标记从哪里来 | 后端在详情里给 `auto_fields` | 成员也要看到；修正状态接口只给管理员，前端拼不出 |
| 进度轮询 | 只在工作时，10 秒 | 后台按外部源每秒 1 次推进，更密的轮询没有信息量 |

## 九、审阅后的修订（2026-11-22，规划子代理审阅；与前文冲突时以本节为准）

1. **进度轮询**：`refetchInterval` 同时看设置与进度。进度是 `running`/`waiting_for_scan` 时 10 秒；设置与进度不一致（设置开着且未暂停而进度是 `disabled`/`paused`，或设置关闭/暂停而进度是 `running`）时也 10 秒，直到对上
   （后台循环最长 1 分钟才发现开关变化，`program/worker.go`）；开着时的 `idle` 60 秒；其余不轮询。`idle` 时写“上一轮完成于 {时间}”。
2. **成对的标记**：后端 `DELETE /suppressions/{id}` 清除 `artwork`/`artwork_source`、`lyrics`/`lyrics_source` 中的一条时，一并删除同一目标、同一 `operation_id` 的另一半（合同第 7 节同步）。
   前端列表隐藏 `*_source` 行。说明文字照旧（清除后可以再写）。
3. **编辑对话框**：只在 `field.auto !== null` 时，把“使用扫描值”的文字换成“使用自动补全的值”，并显示“当前显示：{值}（来自 {源名}）”；有自动值时不出现“确认扫描值”。其他情况保持原样。
4. **`auto_fields` 的后端位置**：`catalog/read.go`（`ReleaseHeader`、`LoadReleaseHeader`，用已有的 `fields.go` `LoadAutoValues`）、`catalog/httpapi/handlers.go`（详情 DTO 组装）、`catalog/comparison.go`（对比接口）。
   后端只列字段名（与 `corrected_fields` 一样，设空的自动值也列入），专辑艺人联动“艺人”是前端规则。样本 `releases/get-auto`，录在 `curation_samples_integration_test.go` 的写入之后、撤销之前。合同第 7 节“面向成员的接口不变”一句同步修改。
5. **标签色调**：`StatusTag tone="neutral"` 加 `Sparkles`。证据区的说明句补上自动补全的写法。
6. **歌词来源**：不在 E1 做（player 规范写明面板不显示来源）。
7. **测试替身**：扩展 `src/test/governance-data.ts`（四种自动类型、`batchId`、`origin` 预置、自动类的可撤销规则、`artist_id`/`source`/`provider`/`batch_id` 筛选）；`curation-data.ts` 的批次计数由这份操作数据推出，范围撤销复用同一套撤销逻辑。
   `fake-api.ts` 与 `e2e/support/api-mock.ts` 登记 curation 的默认响应：默认进度 `releases.total=0`（现有的管理员概览测试不出现一次性提示）。同步改 `e2e/auth.spec.ts` 的栏目名单、`settings.test.tsx` 的 `managementApis`（成员不请求 `/admin/curation/*`）。
8. **分页**：`/settings/curation` 的 `validateSearch` 带 `batch_page`、`marker_page`（照 `scan_page`）；每页 20；页码越界时 `useClampedPage` 回到末页。批次与标记列表 `staleTime: 0`；进度里的 `batch.id` 变化或消失时让批次列表失效。
9. **真实服务用例**：打开总开关的确认框后取消，断言没有 PUT、仍是关闭；关掉一个源、刷新后仍关闭，最后恢复；进度 `expect.poll` 到“未打开”；批次与标记空态。
10. **范围撤销**：第一次调用不带 `cursor`；`skipped` 按 `operation_id` 去重；`code` 按 `^[a-z0-9_]+$` 解码，未知码有兜底文案，临时性失败提示可以再试；`next_cursor` 没有前进时停止。
    按批撤销完成后重读 `GET /batches/{id}`，写“这一批 N 条，已撤销 M 条”；按来源中途重试过时写“至少撤销了 N 条”。撤销前写出数量（按批 `operations - reverted`；按来源用 `GET /operations?provider=&page_size=1` 的总数），
    并写明“撤销之后同一个值不会再自动写入，可以在‘不再提出’里清除”；源还开着时提示“后台还会继续用它写入新内容，可以先关闭这个源”；批次进行中时说明撤销开始之后新写的不包括在内。进行中礼貌播报“已撤销 N 条…”。
11. **一次性提示的条件**改为：管理员 ∧ `progress.releases.total > 0` ∧ 设置 `enabled_at=null` ∧ 本地没有关闭（不再读扫描记录）。
12. **入口**：外部源那一行的菜单先“查看来自 {源} 的改动”（`?provider=`），再撤销；批次区底部“查看全部自动修改”（`?source=curation`）。批次行的操作收进行尾菜单；模型层上线前不显示“层”这一列。
13. **外部源的不生效原因**：总开关关着或暂停时在分组层面写一行说明，各行只写自己的 `source_disabled`。
14. **文案**：待定项写“N 项拿不准，暂时留着”；从未打开时进度区收成一行；“由谁在何时打开”并进总开关那一行的说明；确认框只列当前开着的源并补“这台服务器会直接连接这些网站”；关闭的说明“关闭后不再向外部发请求，已写入的内容保留，可以按来源撤销”。
15. **不再提出列表**：空态；目标是发行的行链接到 `/settings/operations?release=<id>`；`group_join` 的值写“归入另一张专辑”；DELETE 的 404 当作已清除。
16. **依赖方向**：`useReleaseDataRefresh` 由 `app/routes/curation-page.tsx` 传入；`sourceName` 从 governance 的 `index.ts` 导出；`operationOriginLabel` 改用 `sourceName`（“自动整理 · MusicBrainz”）。
17. **要同步的规范**：`curation-contracts.md` 第 7 节、`interface-layout-guidelines.md`（管理栏目）、`DESIGN-components.md` 与 `governance-components.md` 的编辑对话框、`state-management.md`（轮询清单）、
    `directory-structure.md`（feature 清单）、`component-catalog.md`（新标签）。
