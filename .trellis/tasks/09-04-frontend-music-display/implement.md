# 实施计划：前端音乐库展示

## 执行前门禁

- [ ] 用户明确批准本任务最新规划；批准前不得运行 `task.py start` 或修改产品代码。
- [ ] 执行 `python3 ./.trellis/scripts/get_context.py --mode phase --step 2.1 --platform codex`，再次读取实施阶段约定。
- [ ] 使用 `trellis-before-dev` 读取并确认共享指南、前端层指南和 Core 0 运行合同；保持当前无新 UI/状态/生产服务依赖的决定。
- [ ] 检查工作区已有用户改动，避免覆盖与本任务无关的 `.claude/hooks/__pycache__/` 等生成文件。

## 有序实现清单

### 0. 当前阶段拆分（先后端）

- [ ] 先启动并完成子任务 `.trellis/tasks/09-04-release-list-artwork-backend`，只修改后端
  catalog 投影、后端测试和 Core 0 合同。
- [ ] 子任务验收/归档前不修改 `frontend/src/**`、前端 decoder、样式、Vite 生成资产或
  演示队列；这些清单项保留到后续前端子任务。

### 1. 建立 catalog 展示模型与回归基线

- [ ] 在 `frontend/src/features/catalog/model/`（或同等 feature-local 位置）建立 Release/Track 展示模型和纯 formatter；实现时长、来源/codec、艺术家回退、封面 fallback、Medium/Track 展开顺序和有界演示队列投影。
- [ ] 先运行现有前端定向测试，记录基线；不改变现有 `release_filters.ts` 的 `q`/`attention`/`page` 语义。
- [ ] 为 URL 增加可选 `release` 读写和 malformed/越界回退测试，确认其它查询参数保持不变。

### 2. 扩展后端列表摘要封面合同（无迁移）

责任文件：`backend/cmd/roomusic/catalog_api.go`、相关 catalog REST 测试。

- [ ] 将 `releaseSummaryDTO` 增加 nullable `artwork`，使用已有白名单 MIME、受控 resource ID、正宽高。
- [ ] 修改列表/详情共用投影与扫描逻辑，使无封面显式返回 `null`，有封面返回与详情一致的结构；查询必须保持分页、搜索、attention、present-only 和稳定排序。
- [ ] 通过单条 SQL 的索引关联/相关查询取得封面，不在 HTTP handler 中逐 Release 请求详情。
- [ ] 增加后端测试：有封面、无封面、资源标识不安全、列表与详情形状一致，以及原有未授权/不存在行为不变。
- [ ] 若查询计划或数据库错误路径变化，保持稳定 `database_*` 错误 envelope 和 request ID。

### 3. 更新前端 DTO/decoder 与封面边界

责任文件：`frontend/src/api.ts`、`frontend/src/api.test.ts`。

- [ ] 让 `ReleaseSummaryDTO` 和 `decodeReleaseSummary` 严格解码 `artwork: ReleaseArtworkDTO | null`；复用详情的 MIME/数字边界，不使用 raw cast。
- [ ] 覆盖 null、完整 artwork、缺字段、未知 MIME、非法尺寸、超长 resource ID 等 malformed 输入。
- [ ] 为 `ReleaseCover` 实现 `absent/loading/ready/broken` 状态、受控 URL 编码、可访问 alt/fallback 和重试意图；组件不接收或拼接主机绝对路径。

### 4. 抽取并实现发行列表工作区

责任文件：`frontend/src/main.tsx`、`frontend/src/features/catalog/components/*`、`frontend/src/styles.css`。

- [ ] 将工具栏、封面、卡片/列表行、详情、Medium、Track 视图按职责抽取；`App` 只保留会话、请求协调和跨区回调。
- [ ] 添加搜索提交/清除、attention、刷新、网格/列表、上一页/下一页按钮；每个按钮显示 pending/disabled/aria 状态。
- [ ] 网格和列表共用同一 Release view model；卡片使用 stable Release ID，不嵌套交互按钮。
- [ ] 实现初始 loading skeleton、刷新保留旧结果、无扫描/无结果/attention 空状态、局部错误和重试按钮。
- [ ] 扫描进入 `succeeded` 后只触发一次列表刷新；`failed`、`canceled`、`incomplete` 显示不同的安全文案，不伪造完整更新。

### 5. 实现详情抽屉与 URL/焦点生命周期

- [ ] 打开卡片时写入 `release` 参数并请求详情；请求期间保留列表，旧详情响应不得覆盖新选择。
- [ ] 实现桌面右侧抽屉、移动全宽面板、遮罩、关闭按钮、Escape、Tab 循环、打开/关闭焦点回收和 `aria-labelledby`/`aria-modal`。
- [ ] 添加“播放首曲”“播放全部”“全部展开/收起”、单 Medium disclosure、逐 Track 播放和管理员完整 evidence 按钮；按钮只发出意图，不直接 fetch。
- [ ] 详情按 Release → Medium → Track 固定层级显示既有字段；空值安全显示“未记录”，不显示绝对路径。
- [ ] 处理 detail 401/403/404/503：会话恢复、权限提示、失效选择清理和面板内重试分别可见。

### 6. 完善本地演示队列/播放器

- [ ] 将 `nowPlaying` 扩展为有界 `DemoQueueState`，支持播放首曲、播放全部、逐曲播放、上一首、下一首、移除当前项、清空队列和暂停/继续。
- [ ] 队列项携带 Release 上下文和 Track stable ID；切换列表/刷新不丢队列，登出清空，不写 localStorage/URL/服务端。
- [ ] 固定底栏显示封面缩略图、曲目、艺术家、序号和“演示模式/未连接音频服务”；边界按钮禁用并有辅助文本。
- [ ] 移除或明确标注没有后端行为的假进度、音量、下载控件。

### 7. 响应式样式与可访问性验证

- [ ] 重写/整理 `frontend/src/styles.css` 中相关规则，保留深色工作台、青绿色主色、珊瑚色警告和 ≤8px 圆角。
- [ ] 在桌面、`768px` 断点和 `375px` 窄屏检查网格/列表、详情、队列、固定播放器和管理区无溢出/遮挡。
- [ ] 为图标按钮提供可见文字或 `aria-label`，为状态提供 live region，检查焦点、标题层级、列表语义和 reduced-motion。

### 8. 测试与合同同步

- [ ] 增加 formatter/view-model 单测：时长、未知字段、艺术家回退、排序/展开顺序、队列边界和封面 fallback。
- [ ] 增加 catalog 组件/交互测试（按现有 runner 能力覆盖 loading/empty/error/ready、键盘打开关闭、按钮名称和恢复路径）；若必须引入 DOM 测试环境，只增加最小 dev 依赖并更新 lockfile，不引入 UI 库。
- [ ] 更新 API decoder、URL filter 和既有测试 fixture；搜索所有 `ReleaseSummaryDTO` 消费者，确保新 artwork 字段无遗漏。
- [ ] 更新 `.trellis/spec/backend/core0-runtime-contracts.md` 或对应 API 文档，记录列表 artwork 摘要和仍未实现的真实播放边界；文档使用简体中文。

### 9. 构建、生成资产与最终检查

- [ ] 运行前端 lint/typecheck/test/build，检查 Vite 输出和 Go 内嵌资源变更。
- [ ] 运行后端 catalog 定向测试、`gofmt`、`go vet`、`go build`；若定向失败再扩大到必要的后端测试。
- [ ] 执行 `git diff --check`，审查无路径/token 泄露、无数据库迁移漂移、无意外依赖和无用户无关文件修改。
- [ ] 运行 `trellis-check`，修复其报告的问题后再进入 finish/commit；不得在本阶段执行 `task.py archive`。

## 推荐验证命令

```bash
cd frontend
npm run test -- src/api.test.ts src/release_filters.test.ts
npm run typecheck
npm run lint
npm run test
npm run build

cd ../backend
go test ./cmd/roomusic -run 'Release|Catalog|Artwork' -count=1
gofmt -l .
go vet ./...
go build ./...

cd ..
git diff --check
git diff -- backend/cmd/roomusic/web
```

若新增组件测试需要浏览器/DOM runner，先确认仓库已有可用环境；没有时记录选型和依赖变化，再运行对应定向测试。除非局部验证失败或后端跨模块影响无法覆盖，不默认运行全量集成 Smoke。

## 风险文件与回滚点

| 阶段 | 高风险文件/行为 | 回滚点 |
| --- | --- | --- |
| API 投影 | `catalog_api.go` 的共享 SQL projection/Scan 顺序 | 恢复 summary artwork 字段前的投影与 scan；数据库关系不动。 |
| DTO | `api.ts` 与所有 Release fixture | 恢复 decoder/type 后重新生成前端构建；不保留不匹配的半发布字段。 |
| 详情生命周期 | `main.tsx`、URL、焦点/AbortController | 先关闭抽屉/URL 扩展，保留原 inline detail 作为最小可用回退。 |
| 队列 | 本地 state 与底栏 | 删除新增队列操作，保留现有单曲演示；不触及音频后端。 |
| 构建资产 | `backend/cmd/roomusic/web` 生成文件 | 使用同一源代码重新 build，审查后再提交生成资产，禁止手工编辑。 |

## 完成门槛

- 所有 PRD 验收标准有对应代码或测试证据。
- 相关前端/后端门禁通过，生成资产无漂移。
- `trellis-check` 通过，必要的运行合同/规范已更新。
- 用户在最终回复中能看到实际执行的验证命令、未运行全量测试的原因，以及仍延期的真实播放能力。
