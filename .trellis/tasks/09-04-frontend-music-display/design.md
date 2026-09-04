# 技术设计：前端音乐库展示

## 1. 设计边界

本任务属于前端 `catalog` 展示能力，后端只做支持列表封面的最小 REST 投影扩展。PostgreSQL、扫描器、认证授权、目录只读规则和 Operation Journal 不改变；不新增迁移、队列服务、搜索引擎或音频服务。

按当前执行决定，后端投影已拆为独立子任务
`.trellis/tasks/09-04-release-list-artwork-backend`，先完成并验证；本父任务本轮不启动
前端代码，以下前端设计作为后续阶段的保留方案。

当前代码仍是过渡单体，因此采用增量边界：`App` 继续负责会话、管理员操作和跨区状态协调；Release/Medium/Track 的纯展示组件和格式化逻辑移入 `features/catalog`，不为了目录对称性一次性搬迁所有 API 文件。

## 2. 信息架构

桌面工作台的关系如下：

```text
┌──────────────┬──────────────────────────────────┬──────────────┐
│ 导航/账户     │ 媒体库工具栏 + Release 网格/列表 │ 队列/演示播放器 │
│              │                                  │              │
│ 媒体库       │ 选中 Release → 右侧详情抽屉      │ 当前曲目       │
│ 播放队列     │                                  │ 队列操作       │
│ 管理中心     │                                  │              │
└──────────────┴──────────────────────────────────┴──────────────┘
```

详情抽屉打开时覆盖内容区右侧并带遮罩，不覆盖全局导航和底部播放器；窄屏改为全宽面板，列表上下文仍可通过“关闭详情”返回。核心数据顺序永远是 Release → Medium → Track，证据和管理员诊断放在折叠区域。

## 3. 组件与文件职责

建议新增以下 feature-local 文件；若实现阶段发现某个组件只有单一简单消费者，可以保留在同一 feature 文件中，但不得把所有逻辑重新堆回一个带大量条件的 `App`：

```text
frontend/src/features/catalog/
├── components/
│   ├── catalog-toolbar.tsx        # 搜索、筛选、刷新、网格/列表、分页意图
│   ├── release-cover.tsx          # loading/absent/broken/ready 封面状态
│   ├── release-card.tsx           # 网格卡片与列表行的共同语义
│   ├── release-detail-drawer.tsx  # 详情面板、焦点与 Medium disclosure
│   ├── medium-section.tsx         # 单碟展开/收起
│   └── track-row.tsx              # 曲目展示和播放意图
├── model/
│   ├── display.ts                 # DTO → view model、时长/标签格式化、队列展开
│   └── display.test.ts            # 纯函数边界测试
└── index.ts                       # 需要时只导出公开组件/类型
```

`frontend/src/main.tsx` 保留 `App` 组合、会话和 API effect；`frontend/src/api.ts` 仍是当前过渡期的 DTO/decoder owner，新增字段时同步更新测试。`frontend/src/release_filters.ts` 继续是 URL 状态 owner，扩展选中 Release 的读写；不要引入 router 或全局 store。

## 4. REST 与类型数据流

### 4.1 列表封面摘要

后端 `releaseSummaryDTO` 增加：

```ts
artwork: {
  resource_id: string;
  mime: "image/jpeg" | "image/png" | "image/gif" | "image/webp";
  width: number;
  height: number;
} | null;
```

`GET /api/v1/releases` 在同一条分页查询中投影现有 `release_artworks` 关系；实现可以使用受索引保护的关联/相关子查询，但禁止前端按每张卡片再请求 Release 详情（N+1）。`GET /api/v1/releases/{id}` 继续复用相同结构。缺失封面必须序列化为 `null`，不能省略或伪造资源 ID。

数据流：

```text
PostgreSQL release_artworks
  -> catalog REST summary/detail
  -> decodeReleaseList/decodeReleaseDetail
  -> catalog display view model
  -> ReleaseCover(resource_id)
  -> /api/v1/artworks/{encoded-resource-id}
```

封面 URL 只由 `resource_id` 拼接，且继续通过 `requestApi`/同源 cookie 的浏览器请求获得鉴权；组件不接收原始文件路径。`ReleaseCover` 自己维护图片加载失败状态，失败不改变 Release 列表的 server state。

### 4.2 URL 与本地状态

| 状态 | Owner | 说明 |
| --- | --- | --- |
| `q`、`attention`、`page` | `release_filters.ts` + History API | 查询可分享、刷新可复现；查询/筛选变化将 page 重置为 1。 |
| `release` | `release_filters.ts` + App | 当前详情的 opaque ID；前进/后退和深链接可恢复，不把标题当身份。 |
| 网格/列表模式 | Catalog workspace local state | 纯展示偏好，不改变服务端查询，不写 storage。 |
| 列表/详情请求状态 | App 或 catalog hook | 使用 AbortController 和 generation/请求 key 防止旧响应覆盖新状态。 |
| Medium 展开状态 | 详情组件 local state | 默认第一碟展开；切换 Release 时重置。 |
| 演示队列 | App/player local state | `DemoQueueItem` 携带 Release/Track 上下文；登出清空，不持久化。 |

`release_filters.ts` 的读写函数只接受允许的查询键；`release` 做非空、有界字符串校验，不能把 URL 中的任意值当作 HTML 或文件路径。打开/关闭详情时保留 `q`、`attention`、`page`，仅增删 `release`。

## 5. 列表、详情与队列交互

### 5.1 列表

- `CatalogToolbar` 发出 `onSubmitSearch`、`onClearSearch`、`onToggleAttention`、`onRefresh`、`onChangeView`、`onChangePage` 等意图；不直接调用 `fetch`。
- 网格和列表只接收同一个 Release summary/view model 数组；列表项使用 `release.id` key，卡片按钮的 accessible name 由标题和艺术家组成。
- 加载初次数据时显示固定尺寸 skeleton；刷新时保留旧项并在工具栏/结果区显示 stale-refreshing 标识。
- 空状态按原因区分：无扫描数据、查询无结果、attention 无结果、后端错误；每种状态只提供能恢复该状态的按钮。

### 5.2 详情抽屉

- 选中卡片先写入 `release` URL，再请求详情；请求期间抽屉显示标题/封面 skeleton，不清空主列表。
- 使用 `role="dialog"`、`aria-modal="true"` 和由标题关联的 `aria-labelledby`。打开后把焦点移到关闭按钮；Escape 或关闭按钮退出；关闭后焦点返回原卡片。Tab 在抽屉内部循环，不能落到被遮罩的背景控件。
- 详情请求按 Release ID 取消/忽略过期响应。404 清除 `release` 参数并显示可恢复提示；401 交由会话恢复，403 显示无权访问，503 提供面板内重试。
- Medium 是 `button + region` disclosure，稳定 ID 作为关联；`全部展开/收起` 只改变本地展示，不修改服务端。
- Evidence 摘要默认收起或放在详情末端；管理员“查看完整证据”继续走既有 endpoint，普通用户不渲染该操作且仍处理后端 403。

### 5.3 演示队列

使用以下本地语义模型（具体类型可放在 `main.tsx` 或 catalog 的公开模型中）：

```ts
type DemoQueueItem = {
  releaseId: string;
  releaseTitle: string;
  releaseArtist: string;
  track: TrackDTO;
};
type DemoQueueState = { items: DemoQueueItem[]; currentIndex: number | null; isPlaying: boolean };
```

“播放首曲/播放全部/曲目播放”只更新该状态；上一首/下一首在边界处禁用；移除当前项后按确定性规则修正索引；清空后恢复空状态。播放器必须显示“演示模式，未连接音频服务”，不渲染会让用户以为可拖动或调音量的假控件。

## 6. 显示格式与降级策略

`model/display.ts` 负责无副作用的格式化：

- `formatDuration(seconds)`：非负有限值转成 `mm:ss`，null/非法值为“未记录”。
- `formatAudioFacts(track)`：只拼接存在的 codec、采样率、声道、位深、bitrate、CUE 起点；不输出 `undefined`、原始 JSON 或绝对路径。
- `formatReleaseLabel(release)`：Album Artist 优先，缺失回退 Artist，再缺失为“艺术家未知”。
- `flattenReleaseTracks(detail)`：保留服务端 Medium/Track 顺序，生成播放全部所需上下文，不改变身份。
- `coverFallbackLabel(title)`：使用标题首个可显示字符或音乐符号；不使用数组索引产生身份。

封面状态采用明确的有限状态：`absent | loading | ready | broken`。图片 `onError` 只把当前组件转为 `broken`；如果同一 resource ID 重试，应允许重新加载并清除 broken 状态。

## 7. 错误、权限与扫描联动

列表、详情、封面、evidence 各自拥有错误显示区域；全局 toast 只用于跨区操作完成提示。`ApiRequestError.code` 用于分类，不比较本地化 message。扫描状态由后端 `ScanStatusDTO` 驱动：

| 后端状态 | UI 语义 | 可用动作 |
| --- | --- | --- |
| `running` | 扫描进行中，结果可能继续变化 | 管理员停止扫描；普通用户只读状态。 |
| `succeeded` | 扫描完成，触发一次列表刷新 | 刷新、浏览。 |
| `failed` | 扫描失败，旧结果仍可能存在 | 重试扫描/查看诊断（管理员）。 |
| `canceled` | 扫描已取消，结果不宣称完整 | 重新扫描。 |
| `incomplete` | 扫描未完成，结果可能不完整 | 查看诊断、修正源后重扫。 |

只有后端报告 `succeeded` 才显示“结果已更新”；前端不根据计数或轮询次数推断完整性。

## 8. 样式与响应式设计

- 延续现有深色中性背景、青绿色主操作色、珊瑚色警告色和不超过 8px 的圆角；通过 CSS 变量集中颜色，避免新增样式框架。
- 列表网格使用 `minmax` 自适应列；列表行在窄屏折叠次要字段，标题/艺术家可换行或省略，不依赖横向滚动。
- 详情抽屉桌面宽度保持可读的固定上限，内容区域内部滚动；移动端宽度 100%，底部播放器预留空间。
- `prefers-reduced-motion` 下关闭抽屉/骨架动画；所有 `button`、`input`、`a` 保持可见 `:focus-visible`。

## 9. 兼容、回滚与运维

- 不新增数据库迁移；列表 DTO 的 artwork 是 additive 字段，后端和前端需同批发布。若旧后端短暂缺失该字段，前端应按明确版本策略报 malformed，而不是猜测封面。
- 回滚顺序：先回退前端列表封面/详情 UI，再回退后端 summary 投影；既有详情 artwork endpoint、数据库关系和扫描数据不删除。
- Vite build 仍输出到 `backend/cmd/roomusic/web`，生成文件必须和源构建一致；不启动生产 Node 服务。
- 需要更新 `.trellis/spec/backend/core0-runtime-contracts.md` 中的列表 DTO 说明（若该合同当前没有 artwork 摘要），并在最终任务日志记录新增字段和未做的播放边界。
