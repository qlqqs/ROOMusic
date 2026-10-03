# M1b 技术设计

> 需求、决策与验收见 [prd.md](prd.md)，执行顺序见 [implement.md](implement.md)。本文只写实现边界、数据流、合同和取舍。
> 前置：F3 已交付 `shared/ui` 的 `Pagination`、封面 `ETag`/304 与查看者 ID。
> 本文保留规划时的设计；实施中与它不同的地方（版本导航的标签、没有“专辑”小标题、`Page.compactTitle`、用焦点管理做介质跳转等）
> 逐条列在 [prd.md 的“与设计的差异”](prd.md#与设计的差异)。

## 1. 边界

- 只改前端与后端的样本测试；不新增后端接口、参数或行为。
- 专辑能力全部归 `features/catalog`；页面组合与 URL 校验在 `app/routes` 与 `app/router.tsx`；导航在 `app/shell/nav-items.ts`；
  概览链接改 `features/catalog/components/library-summary.tsx`。
- 跨 feature 只经各自 `index.ts`；`shared/ui` 组件不发请求、不判断角色、不依赖路由。

```text
frontend/src/features/catalog/
  api/decoders.ts          组详情、发行版本详情、证据的 decoder（列表 decoder 已有）
  api/endpoints.ts         列表加 q；fetchReleaseGroup、fetchRelease、fetchReleaseEvidence；artworkPath(resourceId)
  query-keys.ts            catalogKeys（新）
  album-search.ts          列表与详情 URL 参数的校验与规范化（纯函数）
  labels.ts                证据、来源、介质类型等词表的中文标签；未知值原样显示
  format.ts                时长、格式摘要、介质标题等显示格式
  hooks/                   use-album-page、use-album、use-release、use-release-evidence
  components/
    cover.tsx              四态封面（列表与详情共用）
    album-filters.tsx      搜索表单与“只看需要关注”
    album-grid.tsx         卡片网格、结果数与分页
    album-card.tsx
    album-header.tsx       组头
    version-nav.tsx        版本导航（多版本时）
    release-media.tsx      介质分段与曲目行
    release-facts.tsx      侧栏“此版本”与“制作人员”
    evidence-section.tsx   证据摘要与管理员完整证据
  index.ts                 导出 AlbumListView、AlbumDetailView、LibrarySummary
frontend/src/app/routes/albums-page.tsx、album-detail-page.tsx
```

## 2. 路由与 URL

- `albumsRoute`：`/albums`，`validateSearch` 返回 `{ q?: string; attention?: "required"; page?: number }`。
  - `q`：trim 后非空且不超过 200 个 UTF-8 字节（与后端上限一致），否则 `undefined`。
  - `attention`：只有 `"required"` 保留。
  - `page`：≥2 的安全整数，否则 `undefined`（第 1 页不写）。
  - 每个不合法的值都**显式**返回 `undefined`（路由会把校验结果合并到原始查询串，见 state-management 规范）。
- `albumDetailRoute`：`/albums/$groupId`，`validateSearch` 返回 `{ release?: string }`（UUID 才保留）。`groupId` 不是 UUID 时不发请求，
  直接显示“专辑不存在”（沿用扫描详情的做法）。
- 页码规范化：响应 `total > 0` 且 `page > 末页` 时 `navigate({ search: {...search, page: 末页或 undefined}, replace: true })`；`total = 0` 时保留第一页空态。
- 版本规范化：组详情返回后，`release` 不在 `releases` 里时 `replace` 去掉该参数（含组只剩一个版本的情况）。
- 返回列表：卡片链接带 `state: { albumList: search }`；详情页读 `location.state.albumList`，经同一个 `validateSearch` 校验后生成“返回专辑列表”
  链接，没有时链接到 `/albums`。浏览器后退不依赖它。
- 查询参数改变（搜索提交、清除、切换筛选）时把 `page` 置为 `undefined`；翻页只改 `page`。

## 3. 数据流与查询

| 查询 | 键 | 请求 | 说明 |
| --- | --- | --- | --- |
| 列表 | `catalogKeys.page(gen, {q, attention, page})` | `GET /release-groups?page_size=48&…` | `placeholderData` 保留上一页，翻页与筛选时旧结果保持可见并标明正在刷新 |
| 组详情 | `catalogKeys.group(gen, groupId)` | `GET /release-groups/{id}` | 404 → “专辑不存在” |
| 发行版本详情 | `catalogKeys.release(gen, releaseId)` | `GET /releases/{id}` | 选中的版本：`release` 参数或代表版本；404 → A4 的提示 |
| 完整证据 | `catalogKeys.evidence(gen, releaseId)` | `GET /releases/{id}/evidence` | 仅管理员且展开后 `enabled`；普通用户不创建这个查询 |

- 键都以 `viewerQueryKey(generation, "catalog", …)` 构造，会话变化自动隔离；`staleTime`、重试与失焦刷新沿用 M1a 的全局默认。
- 组详情与发行版本详情是串行依赖（先知道选哪个版本）；组详情里已有代表版本的摘要，头部可先渲染，曲目区单独显示加载态。
- 概览的 `useLibraryTotals` 不变。

## 4. Decoder 与上限

- 复用已有的 `releaseSummaryDecoder`、`artworkDecoder`。
- 组详情：组摘要字段加 `releases: array(releaseSummary, {min: 1, max: 100})`。
- 发行版本详情：摘要字段加
  - `candidate_kind: string({max: 64})`，`genre`/`catalog_number`/`edition`/`label`/`barcode`：`nullable(string)`；
  - `media: array(medium, {max: 256})`，medium = `{id: uuid, position: integer≥0, title: string, tracks: array(track, {max: 10000})}`；
  - track = `{id, title, artist, position, source, source_kind, duration_seconds: nullable(number≥0), codec, bit_depth, sample_rate, channels,
    bitrate（均 nullable）, cue: nullable({index_frames, end_frames, start_seconds, end_seconds, isrc}), credits: array({role, name}, {max: 100})}`；
  - 跨介质的曲目总数另做一次 ≤10000 的检查；`credits: array({role, name}, {max: 100})`；`evidence: array({field, source, confidence, action, rule_id}, {max: 100})`。
- 完整证据：`{release_id: uuid, fields: array({field, source, confidence, action, rule_id, value: nullable(string), candidates: array(string, {max: 20}),
  reason_code: nullable(string)}, {max: 100}), grouping: nullable({candidate_kind, rule_id, source_refs: array(string, {max: 100}),
  reason_codes: array(string)}), truncated: boolean}`。字段名以实际样本为准，样本与 decoder 一起提交。
- 字符串字段除 ID 外不限定词表：`source`、`confidence`、`action`、`rule_id`、`candidate_kind`、`source_type`、`media_type` 都按“已知值给中文标签，
  未知值原样显示”处理（`labels.ts`），后端新增取值不会让页面解码失败。

## 5. 组件要点

- **Cover**：`{ artwork, title, variant: "card" | "detail" }`。
  - `artwork === null` → 文字版面：`--color-neutral-900` 底、`--color-neutral-100` 字，排标题（标题字体，超出行数截断），确定性、不随机配色。
  - 有封面 → `<img src={artworkPath(id)} width height loading="lazy" decoding="async">`；`onLoad` 前显示同尺寸静态底色，`onError` 后换成文字版面并显示
    “封面无法加载”。卡片中 `alt=""`（标题已在链接名里），详情页 `alt="《标题》封面"`。
  - 详情页失败后提供“重新加载封面”按钮：递增 `key` 重新挂载图片；Playwright 断言确实发出了第二次请求。
  - 裱边：`6px` surface 边框加 `1px` divider 外线，保持正方形（`aspect-ratio: 1`，`object-fit: contain`，不裁切真实封面）。不加滤镜。
- **AlbumCard**：整张卡片是一个 `Link`（可访问名称包含标题与艺人），内部没有按钮。标题最多 3 行（`line-clamp`），文字完整保留在 DOM 中；
  长串用 `overflow-wrap: anywhere`。标签用 `StatusTag`。
- **AlbumGrid**：`grid-template-columns: repeat(auto-fill, minmax(160px, 1fr))`；结果数（“共 N 张专辑”/“找到 N 张”）用 `aria-live="polite"`
  只在提交后更新；分页用 F3 的 `Pagination`。
- **AlbumFilters**：`<form role="search">`，`TextField` 加“搜索”按钮；“清除”只在有 `q` 时出现；复选框“只看需要关注”。
- **AlbumHeader**：小标题“专辑”、`h1` 标题（超过 80 个字符时降一级字号，完整显示并换行）、专辑艺人、年份与流派、计数与总时长。
- **VersionNav**：`<nav aria-label="版本">` 里的链接，当前版本 `aria-current="true"`；标签文字依次取 版本说明 → 厂牌编号 → 年份 → “版本 N” 中第一个非空值，
  再附年份；`release_count > releases.length` 时注明“只列出前 100 个版本”。
- **ReleaseMedia**：每个介质一个 `<section aria-labelledby>`，标题为“第 N 张”加介质标题（介质标题与默认值相同时不重复）；多于一张时顶部有
  “跳到第 N 张”的页内链接；曲目用有序列表，行内为序号、标题、曲目艺人（与专辑艺人不同时）、署名（“角色：姓名”）、时长与 CUE 标记；
  每段 `content-visibility: auto` 加 `contain-intrinsic-size`。
- **ReleaseFacts**：定义列表（`<dl>`）；空值的项不显示；全部为空时显示“暂无更多发行信息”。
- **EvidenceSection**：`<details>`/`<summary>`“来源与证据”，默认折叠；摘要表格用 `DataTable`；管理员的“查看完整证据”按钮在展开后才出现，
  点击后启用证据查询，结果按字段列出值、候选与原因码，分组证据单列；`truncated` 时提示“内容过长，已截断”。
- **样式**：沿用 `tokens.css`，新增样式用 CSS Modules 与容器查询；详情页主体为“曲目 + 侧栏”两列，容器窄于约 720px 时侧栏落到下方。

## 6. 错误与边界状态

| 情况 | 呈现 |
| --- | --- |
| 列表首次加载 / 刷新中 | 同结构占位 / 保留旧结果并标明刷新中 |
| 资料库没有专辑 | 管理员：“还没有专辑，添加音乐目录并完成扫描后会出现在这里”，链接到扫描目录；普通用户：等待管理员扫描的说明 |
| 筛选无结果 | “没有符合条件的专辑”，“清除筛选”按钮 |
| 401 / 403 / 503 / 无效响应 | 沿用 M1a 的 `QueryContent` 与 `FailureNotice`：401 进入会话恢复，其他给出可重试的说明 |
| 组 404 或 ID 不合法 | “专辑不存在”，链接回列表 |
| 所选版本 404 | “这个版本已不可见（可能刚被重新扫描）”，链接到代表版本 |
| 证据 403/404/503 | 证据区域内的局部提示，不影响页面其他部分 |

## 7. 测试

- **接口样本**（`backend/internal/app/api_samples_integration_test.go`）：在现有样本流程之后追加：
  - 在测试根目录写两份合成 FLAC（`DISCNUMBER=1/2`，`LABEL`、`CATALOGNUMBER`、`BARCODE`、`COMPOSER`、`CONDUCTOR`、`PERFORMER` 等），扫描后记录
    `releases/get`（双碟）、`releases/get-single`（既有单曲专辑）、`releases/evidence`、`release-groups/get`；
  - 直接写库把一个 Release 的 `group_id` 指到另一组，记录 `release-groups/get-multiple`；
  - 记录 `errors/not-found-release-group`、`errors/not-found-release`。
  - 已有样本若因新素材而变化，逐个核对 diff 后提交；不使用真实媒体。
- **Vitest**：
  - decoder：正例为样本，反例覆盖越界数组、错误类型与缺失字段；`album-search.ts` 的每个规范化分支；`labels.ts` 的未知值。
  - 新的有状态替身 `src/test/fake-catalog.ts`：由样本生成 120 个组以覆盖分页，支持 `q`、`attention` 与页码；组详情、发行版本详情与证据来自样本，
    可按测试注入 404/403/503、长标题与多介质数据。
  - 页面测试：列表四种状态、搜索提交与清除、筛选、翻页与超出末页规范化、返回列表恢复参数；详情单版本/多版本/版本 404/组 404、
    普通用户不请求证据接口、管理员按需请求、封面四态（触发 `load`/`error` 事件）；1000 首曲目的发行版本可渲染。
- **Playwright**（`vite preview` 加受控后端）：
  - `api-mock.ts` 增加 catalog 路由与封面路由（测试代码生成的 1×1 PNG 与一个返回 404 的资源）；数据含超过 1000 字的标题、超长艺人名、
    无空格长串、中日英混排、12 张介质、两版本的组。
  - 新增 `albums.spec.ts`：搜索/筛选/翻页/详情/版本切换/证据/返回列表的主路径，封面失败后重新加载发出第二次请求。
  - `a11y.spec.ts`、`responsive.spec.ts` 加入：列表（有封面）、空库、无结果、单版本详情、多版本多介质详情、证据展开（管理员）、长内容列表与详情。
  - 字体回退：在 `responsive.spec.ts` 中对长内容的列表与详情再跑一遍，`page.route("**/*.woff2", route => route.abort())`，断言无横向溢出与最小字号。
  - `keyboard.spec.ts`：A9 的键盘路径。
- **真实接口冒烟**：沿用 M1a 的临时脚本方式（`roomusic-test` compose、合成 FLAC 与生成的 PNG 封面），走登录 → 列表 → 搜索 → 详情 → 证据，
  结果与截图记入 `research/real-api-smoke.md`，截图留在 `/tmp`，不入库。

## 8. 取舍

- **介质逐段列出而不是分段切换**：任意张数都能完整呈现，可整页查找；代价是多碟专辑页面更长，用页内跳转链接缓解。
- **搜索显式提交**：不做输入即搜，换来可预期的请求与历史记录；代价是多按一次回车。
- **不做缩略图**：保持 M1b 纯前端；风险见 PRD。
- **版本导航用链接而不是 ARIA tabs**：选择会写进 URL，链接语义与前进后退一致，也不需要方向键的焦点管理。

## 9. 兼容与回退

- 开发阶段不做兼容：旧 `?release=` hash 链接不支持（M1a 已声明）。
- 只有前端与测试改动，每步一个提交，可单独 `git revert`；没有迁移与数据回退。
