# 完善音乐库前端展示

## 规划状态

- 父任务：`.trellis/tasks/09-04-frontend-music-display`（保持 `planning`）。
- 前置依赖：后端子任务 `.trellis/tasks/archive/2026-09/09-04-release-list-artwork-backend` 已完成并归档，`GET /api/v1/releases` 摘要已含 nullable `artwork`。
- 规划决策路线：人工选择（Human selection）。选择者为用户（qlqq）；用户已在本轮明确要求“完成规划里面的前端部分”，视为对前端子任务最新规划的批准，允许执行 `task.py start`。
- 权威规划文档：父任务的 `prd.md`、`design.md`、`implement.md`。本子任务只实现其中前端部分，不改变后端 REST 语义。

## 目标与用户价值

扫描完成后，用户能在首屏通过真实封面、标题、艺术家、年份、介质和曲目数量浏览发行版本，用搜索/筛选缩小范围，并在 `Release -> Medium -> Track` 的详情抽屉中检查元数据；播放控件明确标注为本地演示状态。

## 范围内需求

### R1. catalog 展示模型与纯 formatter

- 在 `frontend/src/features/catalog/model/` 建立 DTO → view model 与纯格式化函数：`formatDuration`、`formatAudioFacts`、`formatReleaseLabel`（Album Artist → Artist → “艺术家未知”）、`flattenReleaseTracks`、`coverFallbackLabel`，以及封面状态 `absent | loading | ready | broken`。
- formatter 无副作用：非法/null 时长显示“未记录”，不输出 `undefined`、原始 JSON 或绝对路径。

### R2. DTO/decoder 与封面边界

- `frontend/src/api.ts` 的 `ReleaseSummaryDTO`/`decodeReleaseSummary` 严格解码 additive 的 `artwork: ReleaseArtworkDTO | null`，复用详情的 MIME/正整数/受控 resource ID 校验，不用 raw cast；同步更新 `api.test.ts` 的 null、完整、缺字段、未知 MIME、非法尺寸、超长 resource ID 用例。
- `ReleaseCover` 组件只由 `resource_id` 拼接 `/api/v1/artworks/{encoded-resource-id}`，经同源 cookie 鉴权；组件自行维护 loading/broken，`onError` 只影响当前卡片，允许重试清除 broken。

### R3. 列表工作区与工具栏

- 按父任务 `design.md` 第 3 节抽取 `features/catalog/components/`：catalog-toolbar、release-cover、release-card、release-detail-drawer、medium-section、track-row；`App` 只保留会话、请求协调和跨区回调。
- 工具栏：搜索提交（回车等效）、清除（仅有已提交查询时显示）、attention 切换（`aria-pressed`）、网格/列表切换（本地 UI 状态）、刷新、分页；每个控件有 pending/disabled/aria 状态。
- 网格与列表共用同一 view model，卡片用 stable Release ID，不嵌套交互按钮。
- 初始 loading skeleton；刷新保留旧结果并显示 stale-refreshing 标识；空状态区分无扫描数据/查询无结果/attention 无结果/后端错误，各自只提供可恢复按钮；局部错误有重试。
- 扫描 `succeeded` 后只触发一次列表刷新；`failed`/`canceled`/`incomplete` 显示互不混淆的安全文案，不伪造“库已完整更新”。

### R4. URL 状态与详情抽屉

- `release_filters.ts` 扩展可选 `release` 参数读写（opaque ID），`q`/`attention`/`page` 语义不变；查询或筛选变化重置 page 为 1；前进/后退与深链接可恢复；附 malformed/越界回退测试。
- 打开卡片先写 URL 再请求详情；请求期间保留列表并显示 skeleton；旧详情响应不得覆盖新选择。
- 抽屉：`role="dialog"`、`aria-modal="true"`、`aria-labelledby`；打开后焦点移到关闭按钮，Escape/关闭退出，关闭后焦点回到原卡片，Tab 在抽屉内循环；桌面右侧固定上限宽度 + 遮罩，移动端全宽面板。
- Medium 为 `button + region` disclosure；“全部展开/收起”只改本地展示；evidence 摘要默认收起；管理员“查看完整证据”走既有 endpoint，普通用户不渲染。
- 详情错误：401 走会话恢复、403 权限提示、404 清除 `release` 参数并提示、503 面板内重试。

### R5. 本地演示队列

- `DemoQueueState`：`items`（携带 releaseId/releaseTitle/releaseArtist + TrackDTO）、`currentIndex`、`isPlaying`；支持播放首曲、播放全部、逐曲播放、上一首/下一首（边界禁用）、移除当前项（确定性修正索引）、清空队列、暂停/继续。
- 列表切换/刷新不丢队列；登出清空；不写 localStorage/URL/服务端。
- 固定底栏显示封面缩略图、曲目、艺术家、序号和“演示模式，未连接音频服务”；不渲染假进度/音量/下载控件；不遮挡内容末尾。

### R6. 导航、样式与可访问性

- “媒体库”回列表并显示结果总数；“播放队列”滚动/聚焦队列区，空状态可恢复；管理员可见“管理中心”，普通用户不渲染管理入口；“退出登录”清理选中详情与浏览状态。
- 样式延续深色工作台、青绿主色、珊瑚警告、≤8px 圆角、CSS 变量；网格 `minmax` 自适应；`375px` 窄屏无横向溢出/遮挡；`prefers-reduced-motion` 关闭动画；所有交互元素有可见 `:focus-visible`。

### R7. 测试与构建

- formatter/view-model 单测覆盖有效、空值、malformed、队列边界、封面 fallback。
- 组件/交互测试按现有 runner 能力覆盖 loading/empty/error/ready、键盘打开关闭与恢复路径；若需 DOM 环境只增加最小 dev 依赖并更新 lockfile，不引入 UI 库。
- `npm run lint`、`npm run typecheck`、`npm run test`、`npm run build` 通过；Vite 输出到 `backend/cmd/roomusic/web`，生成资产与源构建一致。
- 更新 `.trellis/spec/backend/core0-runtime-contracts.md`（若需补充前端消费说明）或相应文档，明确真实播放仍延期；文档使用简体中文。

## 验收标准

继承父任务 `prd.md` 验收标准 1–5、7–10 中属于前端的部分：

1. 首屏工作台搜索、清除、刷新、attention 筛选、网格/列表切换、结果计数、分页均可操作。
2. 有封面的 Release 在网格和列表中显示真实受鉴权封面；无封面/加载失败稳定回退，不影响其它卡片。
3. 详情抽屉层级可读；关闭、Escape、浏览器前进/后退不留过期详情。
4. 演示队列全部操作在本地状态中产生正确可见变化，并明确标注未连接音频服务。
5. 搜索、attention、分页、刷新和扫描终态联动保持现有 REST 语义；旧查询不覆盖新查询；详情/封面失败有局部重试。
6. 扫描五态文案与动作互不混淆。
7. 普通用户不渲染管理员 evidence/管理按钮；401/403/404/503 不泄露绝对路径、token 或内部错误。
8. 桌面与 `375px` 窄屏无遮挡/溢出/焦点丢失；键盘可完成核心操作。
9. decoder、formatter、URL 状态和组件测试覆盖有效、空值、malformed、越界和失败恢复路径。
10. 前端 lint/typecheck/test/build 与生成资产一致性检查通过；后端仅在不受影响的前提下做定向回归确认。

## 明确范围外

- 真实音频流、`<audio>`、播放历史、跨会话队列、音量控制。
- 独立艺术家/曲目页、歌词、收藏、评分、推荐。
- 修改后端 REST 语义、数据库迁移、扫描/权限规则（仅在前端 decoder 消费既有 additive 字段）。
- 引入 router、全局状态库、UI 组件库、PWA。

## 关键风险与回滚

- `main.tsx` 单体拆分是高风险区：先建 model/纯函数与测试基线，再逐组件抽取；回滚点为保留原 inline 渲染。
- URL `release` 参数与详情 AbortController/焦点生命周期耦合：异常时先关闭抽屉与 URL 扩展，保留最小可用详情。
- 生成资产 `backend/cmd/roomusic/web` 必须由同一源码重新 build，禁止手工编辑；审查后再提交。
