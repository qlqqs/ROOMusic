# M4 问题处理：技术设计

需求见 [prd.md](prd.md)，执行计划见 [implement.md](implement.md)。

## 1. 边界与能力归属

- 后端 `catalog` 拥有问题的定义与读取（它已经拥有“需要关注”的 SQL 与证据），以及“核对”的执行器和撤销（实现 `operations.Kind`）；
  写入只经 `operations.Run`。`operations` 只增加一个类型常量，不知道问题的业务规则。扫描（`library`、`catalog/persist*.go`）**不改**。
- 前端问题页放在 `governance`：它已经有编辑发行信息、命令 hook、撤销提示与操作记录，问题页主要由这些部件组成；
  不新建 `curation`，否则只会为同一组部件多出一层公开入口。“catalog 从不导入 governance”的规则不变。
- 路由 `/issues` 只有管理员（`AdminGate`），导航“资料库”分组加一项。

## 2. 问题与依据

三类问题沿用现在“需要关注”的算法（`backend/internal/catalog/read.go:39-62`），只看可见版本。每个可以核对的问题有一份**依据**：
扫描结论里决定“情况是否变了”的那部分。依据由 SQL 在一处构造（Go 常量），读取、写入与“需要关注”共用，三处的判断因此一致。

| 问题（`issue`） | 什么时候存在 | 依据（JSONB） | 核对仍有效的条件 |
| --- | --- | --- | --- |
| `grouping` | `release_grouping_evidence.reason` 非空 | `{reason, source_refs}`（来源路径已排序、最多 100 条，`organizer.go:673-691`） | 核对时的依据与现在的相等 |
| `source_type`、`media_type` | 该字段的决定是 `uncertain_apply` | `{reason, value, candidates}`（扫描选出的值与候选，候选最多 20 个，`organizer.go:138-140`） | 同上 |
| `missing_tracks` | 有 `source_status = 'missing'` 的曲目 | `{tracks: {<曲目 ID>: <最后一次看到的时间，Unix 微秒>}}` | **逐首**：这首在依据里，且最后一次看到的时间没变 |

- 缺失曲目逐首判断：曲目回来时扫描更新它的 `observed_at`（`persist_track.go:80`），再缺失时时间已经不同；新缺失的曲目不在依据里。
  所以“需要关注”只多出这些曲目，核对过的仍然不计。时间统一用 `(EXTRACT(EPOCH FROM observed_at) * 1000000)::bigint`，写入与比较是同一个表达式。
- 其余字段（标题、专辑艺人、年份、流派、厂牌、编号、版本说明、条码）不能核对，沿用 M3 的修正（含“确认扫描值”）。
- **依据令牌**：读取时把每个可核对问题的依据（JSONB 的文本输出，PostgreSQL 对同一个值的输出唯一）取 SHA-256，作为 `basis` 返回；
  核对请求带回它，执行器锁住发行之后重新计算，不同就 `409 issue_changed`。扫描不改版本号，没有令牌的话，页面打开与提交之间的一次扫描
  会让管理员核对一个自己没看到的情况。

## 3. 数据模型（迁移 0019）

```sql
CREATE TABLE release_issue_reviews (
    release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    issue TEXT NOT NULL,
    basis JSONB NOT NULL,
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    PRIMARY KEY (release_id, issue),
    CONSTRAINT release_issue_reviews_issue_ck CHECK (issue IN ('grouping', 'missing_tracks', 'source_type', 'media_type')),
    CONSTRAINT release_issue_reviews_basis_ck CHECK (jsonb_typeof(basis) = 'object')
);
-- operations_type_ck 加入 'release_issue_review'：与 0018 相同，删掉同名约束再加。目标类型仍是 release，不改。
```

- 每个版本每类问题至多一行，表示“谁在什么时候按哪份依据核对过”；核对人与时间来自 `operation_id` 指向的操作记录。
- **扫描从不读写这张表**：重扫之后核对行还在，是否仍有效由读取时比较依据决定（结构保证，与修正表相同）。
- 过期的行（依据不再相等）不删除：详情据此说明“核对之后情况变了”；再次核对时覆盖。发行从不删除，`ON DELETE CASCADE` 只是保险。

## 4. “需要关注”拆成三类

`releaseAttentionExpression` 拆成三个具名常量再相加；问题列表与详情用同样的常量，所以各类数目之和等于 `attention_count`：

- `releaseOpenFieldIssues`：现有条件（`uncertain_apply`、没有修正），再排除有仍然有效核对的字段（只有来源类型、介质类型可能有）。
- `releaseOpenGroupingIssue`：有原因，且没有依据相等的 `grouping` 核对时为 1。
- `releaseOpenMissingTracks`：缺失曲目中没被核对覆盖的，按现有方式去重计数。

没有核对行时三者之和与现在逐项相同（第 1 步的门禁：接口样本不变）。组的计数（`groups.go:30-31`）、列表的 `attention=required`
与概览随之变化，不另写规则。核对表很小，子查询都走主键。

另有对应的三个“已核对”常量（被仍有效核对压下的字段数、归组 0/1、被覆盖的缺失曲目数），只给问题页用。

## 5. 核对与取消核对

`POST /api/v1/releases/{id}/issue-review`（管理员、同源、`Idempotency-Key`）：

- 请求：`{expected_revision, issue, action: "review", basis}` 或 `{expected_revision, issue, action: "unreview"}`。检查顺序同修正：
  同源 403 → 401/403 → 400 `invalid_id`、`invalid_input`（幂等键、`issue` 不是四种之一、`action` 不对、`review` 缺 `basis`）→ `operations.Run`。
- 指纹：`{type: release_issue_review, release_id, expected_revision, issue, action, basis}`。
- 执行器：`lockVisibleRelease`（`corrections.go:367`，不可见 `404 not_found`）→ 版本号不等 `409 revision_conflict` →
  - `review`：问题现在不存在，或现在的依据令牌不等于请求的 `basis`，`409 issue_changed`；这个问题现在没有未核对的部分 `400 no_changes`；
    否则写入当前依据（覆盖过期的行）。
  - `unreview`：没有核对行 `400 no_changes`（过期的行也可以取消）；否则删除。
  - 版本号加一；前后状态是 `{issue, review: {basis, operation_id} | null}`。
- 响应：`{release_id, issue, reviewed, revision, operation_id}`。
- 与修正、归组共用发行的 `edit_revision`，只锁发行行，不锁组行，不会与归组、默认版本形成环形等待。
  扫描的 `PersistCandidate` 先更新发行行再重写证据（`persist.go:29-128`），所以执行器锁住发行之后读到的依据不会是同一版本扫描写到一半的样子；
  `MarkMissingSources`（`persist.go:243`）不锁发行行，它提交后新标为缺失的曲目不在依据里，照常计入，不会被误盖住。
- 取消核对是普通的新操作（预期版本是当前版本号），任何时候都能做，不受“只能撤销最近一次修改”的限制。

## 6. 撤销与操作记录

`IssueReviewOperations()` 实现 `operations.Kind`，在 `backend/internal/app/app.go:77` 注册：

- **Revert**：锁发行；版本号不等于原记录的 `result_revision` 是 `409 revert_conflict`；把核对行恢复成前态：有前态就写回当时的依据与
  `operation_id`（核对人仍是原来的人），没有就删除。恢复的核对现在过期也照样恢复（恢复的是意图，有效性在读取时判断）。版本号加一。
- **Describe**：复用 `loadReleaseInfos`（`revert.go:234`）；改动摘要 `{kind: "release_issue_review", issue, reviewed, track_count}`
  （`track_count` 只在缺失曲目时有，取依据里的曲目数）；`revertible` = 版本可见 ∧ 版本号等于记录的结果版本。
- 前端的记录名称是“核对问题”，改动行如“归组冲突：标记为已核对”“缺失曲目（3 首）：取消核对”；撤销记录是“撤销核对”。

## 7. 读取接口

### 7.1 问题列表 `GET /api/v1/release-issues?status=&category=&page=&page_size=`（管理员）

- `status`：`open`（默认，有待处理部分的版本）或 `reviewed`（有被仍有效核对压下部分的版本）；`category`：`fields`、`grouping`、`missing_tracks`，
  不给是全部；非法值 `400 invalid_filter`。分页同其他列表（默认 50、最大 100，`400 invalid_pagination`）。只列可见版本，
  按有效标题、艺人、ID 排序（与专辑列表一致）。
- 每项：`{release: <版本摘要>, group_id, edition, open: {fields, grouping, missing_tracks}, reviewed: {fields, grouping, missing_tracks}}`；
  `open` 三项之和等于 `release.attention_count`。缺失曲目部分核对时，这个版本同时出现在两种状态里。
- `summary`：`{open: {all, fields, grouping, missing_tracks}, reviewed: {…}}`，即筛选项上的数字，一次查询（`COUNT(*) FILTER`）得到。

### 7.2 版本的问题 `GET /api/v1/releases/{id}/issues`（管理员；不存在或不可见 `404 not_found`）

```text
{release_id, group_id, revision,
 fields: [{field, value, reason_code, candidates, reviewable, basis|null, status, review|null}],
 grouping: null | {reason_codes, source_refs, truncated, siblings: [{release_id, group_id, title}], basis, status, review},
 missing_tracks: null | {count, tracks: [{id, title, medium_position, position, source, last_seen_at, reviewed}], truncated, basis, status, review}}
```

- `status` 是 `open` 或 `reviewed`；`review` 是 `{operation_id, reviewed_at, reviewer: {id, username}}`，有核对行就给。
  有核对行但 `status` 是 `open`，就是“核对之后情况变了”（缺失曲目是“核对之后又有曲目缺失”）。
- 字段只列现在仍算问题或已核对的；已修正的字段不列（它们在“修改记录”里）。`reviewable` 只对来源类型、介质类型为真，其余字段由“编辑发行信息”处理。
- `siblings`：同一扫描目录（`source_root_id` 与 `relative_directory` 相同）的其他可见版本，最多 20 个，帮助判断是不是该归到一张专辑。
- 缺失曲目最多列 1000 首（`truncated`），依据始终覆盖全部缺失曲目。路径经 `SafeRelativeSourceRef`（`read.go:614`）处理，
  与证据的来源路径一致，不返回绝对路径。

## 8. 前端（governance）

- `api/`：三个接口的 DTO、解码器与端点。`query-keys.ts` 增加 `issues`（列表全部页）、`issuePage`、`releaseIssues`，都在 `governanceKeys.all` 之下，
  命令结束时整体失效（沿用 `useReleaseCommands` 的 `onSettled`），再由 app 提供的刷新更新目录的计数。
- `issues-search.ts`：`/issues?status=&category=&page=&release=`；`status=open`、全部类型、第 1 页时省略；非法值显式写成 `undefined`
  （同 `operations-search.ts`）。
- `IssuesView`：容器足够宽时两栏（列表 `--layout-issue-list-width: 380px`，详情最大 `860px`）；容器窄于约 900px 时顺序导航：
  地址有 `release` 只显示详情与“返回问题列表”，没有只显示列表。列表项是链接（当前项带 `aria-current`），与版本导航相同。
  处理完、离开当前筛选的版本仍保持选中，详情说明“这个版本已经没有待处理的问题”，页面不跳走。
- 详情分“字段 / 归组 / 缺失曲目”三节：
  - 字段表（字段、当前值、原因、候选）与一个“编辑发行信息”（复用 `EditReleaseDialog`）；来源类型、介质类型的行内有“标记为已核对”。
  - 归组：说明原因，列出涉及的文件与同一目录的其他版本（链接到专辑）。
  - 缺失曲目表（碟、曲序、标题、路径、最后一次看到的时间）与“标记为已核对”。
  - 已核对显示“已核对 · 用户名 · 时间”与“取消核对”；过期另有说明。不弹确认对话框（随时可撤销、不丢数据），结果提示就近出现并带“撤销”。
- 命令：`ReleaseIntent` 增加 `{kind: "review", release, revision, issue, action, basis?}`，`RevertSubject` 增加 `"review"`。
  `409 issue_changed` 的说明是“扫描刚刚改变了这个问题，已重新读取，请看过之后再核对”。
- 入口：导航“资料库”加“元数据问题”（只有管理员，`nav-items.ts`）；专辑详情的管理员操作区在当前版本需要关注时加“查看问题”
  （经现有插槽，catalog 不改）。
- 操作记录与“修改记录”：`change-summary.ts` 增加这一类的名称与改动行。

## 9. 与扫描、认领的关系

- 扫描不改核对表与版本号；依据不变的重扫之后核对保持，依据变了就重新计入（第 2 节）。
- 成员关系不连续而新建的发行（M3 的 G8，见[受治理操作合同](../../../../spec/backend/governance-contracts.md)第 7 节）是新版本，没有核对，问题重新出现；旧版本的核对随旧版本留存（不可见）。迁移属于认领（M4 第三项）。
- 全部文件都找不到的版本不可见，不在问题页，也不计入“需要关注”（现状不变）。

## 10. 错误码

新增 `409 issue_changed`、`400 invalid_filter`；沿用 `invalid_id`、`invalid_input`、`invalid_pagination`、`not_found`、`revision_conflict`、
`no_changes`、`idempotency_conflict`、`revert_conflict`、`already_reverted`、`not_revertible`，以及同源与权限的 401/403。

## 11. 迁移、兼容与回退

- 0019 只加表并放宽操作类型约束；没有核对时所有既有接口的输出不变（接口样本门禁）。
- 回退：关闭写入路由，核对行保留；要让“需要关注”回到原来的算法，回退第 1 步的查询改动即可，表可以留着。不写 down 迁移。

## 12. 取舍

- **依据存 JSONB，不另建曲目子表**：四类核对共用一张表、一个执行器和一种撤销（写回前态那一行）；代价是缺失曲目要按 JSON 键逐首查找，
  并用一个不常见的时间表达式。
- **共用发行的版本号**：撤销规则仍是一句话；代价是核对之后，之前的字段修正要先撤销这次核对才能撤销（也可以直接再改）。
- **依据令牌**：多一个字段和一次比较，换来“核对的就是看到的”。
- **按版本列出**（K4）与原型“一行一个问题”不同，记入 DESIGN 的落地差异。
- **依据比较会有少量多余的提醒**：候选值按出现次数排序，次数变化导致顺序变化也算“情况变了”；文件改名也会重新提醒（K2 已接受）。
- **来源路径最多 100 条**：超出部分的变化发现不了，与证据的上限一致。

## 13. 已知限制

- 成员看不到谁核对过，也没有“已核对”标记；证据摘要照常显示扫描结论（“不确定，已暂时采用”）。
- 不做严重程度、置信度、建议值、“交给 Steward”与外部参照（原型的这些依赖外部来源或 M6）。
- 不能批量核对；问题页没有搜索；导航上不显示数量。

## 14. 实施中与设计的差异（2026-10-17）

- **撤销取消核对的加锁顺序**（§6）：写回的核对行引用一条更早的操作记录，提交时的外键检查要给它加 KEY SHARE 锁；撤销那条更早的记录会先 `FOR UPDATE` 锁它、
  再锁发行行，两个撤销交叉时会死锁（测试里 PostgreSQL 报 `40P01`）。实现改为前态有核对行时先 `SELECT … FOR KEY SHARE` 锁那条记录、再锁发行行。
- **版本的问题接口多了 `release` 与 `edition`**（§7.2）：页面标题不能依赖列表（从“查看问题”或同一目录的其他版本进来时，这个版本可能不在当前页），
  所以详情带上版本摘要与版本说明，不另发请求。
- **接口样本规范化依据令牌**：令牌是摘要，依据里有 UUID 与时间，每次运行都不同；样本测试把 64 位十六进制的值规范化为顺序编号。
- **governance 的解码器直接取 catalog 的解码器模块**（§8）：经 catalog 公开入口会连带导入组件与样式，Playwright（Node）里的受控后端加载不了。
- **筛选项与核对按钮的可访问名称用 `aria-label` 写全**（§8）：看不见的后缀单独成段时，Chromium 会在全角冒号前插入空格。
- **核对之后的焦点**（§8）：被点的按钮换成另一个时焦点掉回页面顶部，改为落在这一节的标题上（Playwright 纯键盘用例发现）。
- **从记录撤销核对时的提示**：撤销对话框按改动类型选 `review`，原来误用了字段修改的文案（真实服务用例发现）。
- **1024px 视口是顺序导航**（§8）：带侧栏时内容区不到 900px，与设计的“按容器宽度切换”一致，只是比预想的更早切换。
