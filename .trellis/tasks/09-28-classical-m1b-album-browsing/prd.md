# M1b：专辑浏览

> 状态：planning，未启动。2026-09-28 由[路线修订任务](../archive/2026-09/09-28-core-1-roadmap-revision/design.md)（第二部分）创建，本文是 PRD 初稿；
> 启动前需按 Trellis 流程完成规划并获批准。父任务见 [Classical 重设计 / Core 1](../09-27-classical-core-1/prd.md)。

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

阶段 I 的 `/api/v1/release-groups` 接口；M1a 的外壳、路由、数据层与 decoder 约定。

## 验收标准（初稿）

- 与 M1a 相同的质量门禁；另覆盖超长标题（超过 1000 字）、缺封面、单版本、多 Medium、无权查看证据等场景。
- 不暗示多个版本已被聚合，也不因标题相似展示“同一专辑”。

## 验证方式

`npm run lint`、`npm run typecheck`、`npm run test -- --run`、`npm run build`；三种宽度的 Playwright 冒烟；JSON 样本覆盖按组接口。

## 风险

阶段 I 的接口延期会阻塞本任务；大列表性能；封面加载与缓存。

## 范围外

版本对比与默认版本（M4）；人工归组（M3）；搜索规范化与别名（M5）；播放（M2a）。

## 待规划事项

- 列表排序与分页交互。
- 详情页 URL 与版本标签的 URL 状态。
- 证据展示在不同权限下的呈现方式。
