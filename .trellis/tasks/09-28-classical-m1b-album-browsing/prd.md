# M1b：专辑浏览

> 状态：planning，未启动（前置的 M1a 已于 2026-10-01 完成）。2026-09-28 由[路线修订任务](../archive/2026-09/09-28-core-1-roadmap-revision/design.md)（第二部分）创建，
> 本文是 PRD 初稿；启动前需按 Trellis 流程完成规划并获批准。父任务见 [Classical 重设计 / Core 1](../09-27-classical-core-1/prd.md)。

## 目标与用户价值

使用阶段 I 的按组只读接口交付专辑列表与详情，界面从第一天起就是“组 → 版本”的最终形态；在 M3 人工归组之前，
每组只有一个版本，页面自然退化显示，后续不用返工。

## 依据

- [Core 1 路线](../../../docs/architecture/core-1-roadmap.md) 的 M1b 行与能力矩阵。
- [frontend/DESIGN.md](../../../frontend/DESIGN.md) 的专辑页设计与落地差异表。
- [ADR 0002](../../../docs/architecture/adr/0002-metadata-overlays.md) 第六节：Core 1 只允许人工确认归组。

## 范围

- **专辑列表**：按组卡片、封面四态、搜索、待关注筛选、分页、可恢复的 URL 状态。
- **专辑详情**：组头、版本标签（单版本时退化）、Medium 分段、曲目与署名、厂牌/编号/条码、管理员可见的证据。
- 播放、收藏、版本对比、Steward 等原型入口隐藏或明确禁用。

## 前置依赖

阶段 I 的 `/api/v1/release-groups` 接口；M1a（2026-10-01 完成）的外壳、路由、数据层与 decoder 约定。M1a 已交付、M1b 直接沿用的部分：

- 应用外壳与导航（`app/shell/nav-items.ts` 加入“专辑”入口）、代码定义的路由与守卫（`app/router.tsx`，路由不使用 loader）、会话代次与
  `["viewer", 代次, …]` 查询键、请求层 `apiRequest` 与解码原语（`shared/api`）、`shared/ui` 组件（`QueryContent`、`DataTable`、`Notice`、
  `Page`、`Skeleton` 等）、Classical 变量与字体（`styles/`）。
- `features/catalog` 已有 `GET /release-groups` 列表的 decoder 与端点，当前只用于概览计数（每次请求一项，取 `pagination.total`）；M1b 在此扩展列表、
  筛选、分页与详情。
- 测试基础设施：`src/test/` 的替身后端与样本读取、`e2e/support/api-mock.ts` 的受控后端、`e2e/checks.spec.ts` 保证的检查（五种宽度、axe、最小字号）。
  新页面把状态加进 `e2e/a11y.spec.ts` 与 `e2e/responsive.spec.ts` 的清单即可获得这些检查。

M1a 留下、M1b 需要处理的事项：

- **接口样本**：`backend/internal/app/testdata/api-samples/release-groups/` 只有列表样本（空、一项、一项带封面）。按组详情、版本、Medium/Track、署名、
  管理员证据与对应错误码需要在 `TestPostgreSQLAPIResponseSamples` 的固定流程里补样本，再写 decoder；否则契约测试无法守住这些响应。
- **封面缓存**：封面响应头是 `private, max-age=86400`，会话切换后浏览器 HTTP 缓存可能复用旧图；显示封面时评估（ADR 0001 第 6 项），M1a 没有改后端。
- **字体与长标题**：中日英混排、长人名、超过 1000 字的标题与字体未加载时的回退，M1a 没有逐项验证（DESIGN 第 10 节“M1b 与后续界面验收”）。
- **URL 形状**：M1a 的 URL 是 `/`、`/setup`、`/login?redirect=`、`/library`、`/library/scans/$scanId`、`/members`；专辑列表与详情的路径、搜索参数与版本标签的 URL 状态
  在 M1b 规划时确定。`validateSearch` 必须对不合法的值显式返回 `undefined`（路由会把校验结果合并到原始查询串）。
- **表格与长列表**：`DataTable` 按容器宽度堆叠，适合管理表格；专辑列表是卡片还是表格由 M1b 决定，列表分页必须有界。

## 验收标准（初稿）

- 与 M1a 相同的质量门禁；另覆盖超长标题（超过 1000 字）、缺封面、单版本、多 Medium、无权查看证据等场景。
- 不暗示多个版本已被聚合，也不因标题相似展示“同一专辑”。

## 验证方式

`npm run lint`、`npm run typecheck`、`npm run test`、`npm run build`（含内嵌资产漂移）；`npm run test:e2e`（五种宽度、axe、键盘路径）；JSON 样本覆盖按组接口；
`./scripts/test-integration.sh`（样本比较）。

## 风险

阶段 I 的接口延期会阻塞本任务；大列表性能；封面加载与缓存。

## 范围外

版本对比与默认版本（M4）；人工归组（M3）；搜索规范化与别名（M5）；播放（M2a）。

## 待规划事项

- 列表排序与分页交互。
- 详情页 URL 与版本标签的 URL 状态。
- 证据展示在不同权限下的呈现方式。
