# M5 ②：收藏

## 目标

“我的音乐”的“收藏”页签 `/me/favorites`：成员在专辑页、各处曲目行和播放器上一键收藏专辑或曲目，之后在收藏页找到并播放。父任务见 [M5](../11-04-classical-m5/prd.md)。

## 决定

规划路线：用户逐项裁决（qlqq，2026-11-04，经 AskUserQuestion）；同日用户要求推荐项按使用方便与人性化来定、不按实现难度，入口一题按此标准重选。其余为主会话按同一标准定的默认，列在“默认”里，随最终规划摘要一并批准。

最终规划批准：路线 (a) 人工批准最终规划摘要；批准人 qlqq，2026-11-04 回复“批准……直接开始执行”；理由：五项的裁决、默认、合同与并行方案（三条线、各自隔离库 `roomusic-test-m5p/m5f/m5s`、文件锁排队、合入时迁移改号）均已在摘要中列出并获同意。

**用户裁决**：
1. **专辑、曲目、艺人都能收藏**。专辑按整张专辑收（release group，不分版本）；曲目按曲目身份；艺人收藏由 ⑤ 接入（本项的收藏页先只有“专辑”“曲目”两栏，⑤ 加“艺人”一栏）。
2. **入口**：专辑详情头部一个心形；**每一行曲目都有心形**（专辑、历史、收藏、歌单、搜索结果、艺人页），已收藏为实心，点一下切换；底部播放器栏给当前曲目一个心形。

**默认**：
- 收藏页内用“专辑 / 曲目”切换（写进地址，如 `/me/favorites?tab=tracks&page=N`）。都按收藏时间倒序。专辑栏是封面网格，与专辑库的卡片一致；曲目栏与历史页同样的行（播放、心形、“更多”：下一首播放、加入队列、添加到歌单…）。
- 曲目栏顶部有“播放全部”：按当前顺序放入全部可播放的收藏曲目，跳过缺失的；超过队列上限 10,000 首时取前 10,000 首；提示加入、跳过与省略的数量。
- 取消收藏不弹确认。在收藏页里取消后，这一行留在原处、心形变空心，离开或刷新后才消失，误点可以马上点回来。
- 缺失曲目灰色、标“文件缺失”、不能播放，但可以取消收藏，误点后也能再收藏回来；文件回来后自动恢复。专辑暂时没有可见版本时灰色显示、标“暂时不可用”，可以取消收藏。
- 心形是切换按钮（`aria-pressed`），读屏名称是“收藏《标题》”；切换失败时心形回到原状态，并在旁边提示一句。
- 心形状态在各页面一致：一处切换，其他已打开的列表随之更新（同一查询键前缀失效）。

## 现状与约束

- 曲目身份：`tracks.id` 在改名、同根移动时保留；同路径内容变了会换新 ID（旧曲目变缺失）；跨根移动换新 ID（`backend/internal/catalog/ingest/identity_plan.go:72-119,264-317`，`.trellis/spec/backend/library-scan-contracts.md:65-81`）。曲目可以在保留 ID 的同时换到别的发行（`backend/internal/catalog/track_placement.go:15-16`），收藏引用曲目、读取时换算当前发行。曲目、发行、专辑都不会被物理删除。
- 专辑身份：release group 扫描不改，治理操作（改归组、认领）会让发行换组；没有可见成员的组读取返回 `ErrReleaseGroupNotFound`（`backend/internal/catalog/groups.go:19`）。
- catalog 公开读取：`LoadTrackSummaries` 含缺失曲目、最多 1,000 个（`backend/internal/catalog/track_summary.go:14,57`）；历史页的行映射 `playerTrackOfHistoryItem` 可复用（`frontend/src/app/routes/history-playback.tsx:15-36`）。
- 原型：专辑详情头部有心形收藏（`frontend-desgin/ROOMusic 原型.dc.html` 的 `toggleFav`）；设计探索 §06 收藏分“专辑 / 曲目”。设计规则：按曲目身份去重、不跨用户继承、旧本地数据不迁移（`frontend/DESIGN-components.md:87,92`）。
- 个人数据：只做归属授权，不进共享治理，不接 `operations.Run`（`docs/architecture/adr/0003-governed-operations.md:52,228`）；写路由先同源再鉴权，路径和请求体不带用户 ID（`.trellis/spec/backend/listening-contracts.md` §4）。
- 前端挂点：专辑头部与曲目行操作由 `frontend/src/app/routes/album-detail-page.tsx:39-40` 的插槽传入；历史行操作在 `history-playback.tsx:51-87`；播放器栏 `frontend/src/features/player/components/player-bar.tsx:14-27`。player 不能导入 listening 的界面，心形要由 app 层以插槽传入。共享 UI 没有图标切换按钮，现有做法是 `Button` 加 `aria-pressed`（`player-controls.tsx:36`），图标用 lucide-react 的 `Heart`。没有全局 toast，反馈写在操作旁的 `role="status"`。

## 验收

- A1 隔离：用户 A、用户 B、管理员互相读不到、改不了对方的收藏；未登录 401，写请求跨源 403。
- A2 切换：收藏与取消都是幂等的（重复收藏不产生第二条，重复取消不报错）；收藏不存在的曲目或专辑返回 404。
- A3 心形：专辑头部、各处曲目行、播放器栏的心形与服务端一致；一处切换后其他已打开的页面随之更新。
- A4 收藏页：两栏按收藏时间倒序、分页不重不漏；收藏页里取消后这一行留到离开为止；缺失曲目与不可用专辑按“默认”显示；曲目重扫后换了发行，显示当前所在的发行。
- A5 播放：行内播放、下一首、加入队列与历史页一致；“播放全部”提示加入与跳过数量，队列满时提示、不截断。
- A6 质量：接口样本与前端解码器往返；五种宽度不溢出、axe 无违规、纯键盘能完成收藏、取消与播放；Playwright 与真实服务用例通过（真实服务：收藏一首后刷新仍在、取消后消失）；1440 与 375 截图复核。

## 范围外

收藏的文件夹或标签、收藏数统计与推荐、导入导出、他人可见的收藏、按版本收藏。

## 设计与执行

见 [design.md](design.md) 与 [implement.md](implement.md)，与 ③ 歌单同一条实现线。

## 实施结果

2026-11-04 完成；提交：`825239c`（后端）、`f5d40fa`（前端与测试）、`d871caa`（同步 M5 之前遗留的过期文案断言）。验证基准为 `git archive f5d40fa` 的全新导出树。

- A1：`backend/internal/app/personal_api_integration_test.go`、`backend/internal/personal/favorites_integration_test.go` 验证本人隔离、401/403；指定 personal/catalog/app/httpserver/listening 范围 `go test -race ... -count=1` **1046 项通过（348 个顶层；集成612项），0 FAIL / 0 SKIP**。
- A2：`favorites_integration_test.go` 与 `personal_api_integration_test.go` 的幂等切换、404、页边界随上述 race 通过；`frontend/src/features/personal/favorite-interactions.test.tsx` **20/20**。
- A3：心形交互 **20/20**、`sync.test.tsx` **8/8**；覆盖双位置不确定门禁、失败后重读成功解锁与广播/聚焦同步；`frontend/test-results/m5-personal-screenshots/{1440,375}/{player-heart,favorite-reread-failed}.png`（本地、未入库，主会话已复核）。
- A4：`frontend/src/app/favorites.test.tsx` **10/10**；保留层、无多余刷新按钮、换页/栏/页签与缺失态通过；真实 `e2e/real/personal.real.ts` 的收藏刷新/取消持久化 **1/1**。
- A5：`src/app/routes/personal-playback.test.tsx` **38/38**、`track-actions.test.tsx` **4/4**；“播放全部”替换队列（即使旧队列满），missing/unsupported/omitted 分开计数；追加容量不足仍整批拒绝。
- A6：decoder **309/309**、全局样本 **220/220**；`e2e/personal.spec.ts` **29/29**（五宽、axe、键盘、拖动及失败态布局），真实 personal **2/2**；截图 `frontend/test-results/m5-personal-screenshots/{1440,375}/favorites-{albums,tracks}.png`（本地、未入库，主会话已复核）。

性能：沿用提交前正常真实形状320个非 race HTTP 样本验收，本轮未复测；当时全部达到既定 p95 门槛，收藏两栏首屏均低于300ms、队列低于2s；详见[性能实测](research/m5-personal-performance.md)。本次 race 不替代性能实测。

主会话裁决已落地：页签顺序为“收藏 / 歌单 / 历史”，`/me` 默认收藏；时长用“1 小时 20 分”等口语写法，部分未知显示向下取整的“以上”，全部未知只显示曲目数；加入歌单省略零计数，全部已存在用简短提示，混合时保留两组数量；仅取消、无新增行时不加页内“刷新”按钮，离开/换页/换栏/重载清掉保留行；不确定写入后同目标所有心形保持忙碌，直到之后发出的一次读取成功，重读失败则显示“没能确认是否已收藏。”与“重新读取”，不能再次切换。

导出树门禁：`npm ci`、lint、typecheck、build 与同范围 `go vet` 通过；重建 web 与提交原始副本 `diff -r` 完全一致。全量 Vitest 2419 PASS / 5 FAIL、全量模拟 Playwright 279 PASS / 4 FAIL、全量真实服务 45 PASS / 2 FAIL，均 0 SKIP；11 个失败都来自 M5 之前 `6efc902` 改了文案却没同步的断言（各重复 20 轮稳定失败，与本线无关）。`d871caa` 只改测试、把断言改为当前文案并保持原意，之后这 11 项所在的 5 个 Vitest 文件、`a11y`/`library`/`members` 三个浏览器 spec 与 `e2e/real/playback.real.ts` 整文件重跑全部通过（两条 ALAC 完整执行到拖动、seek 与播完），lint、typecheck 通过。

已知风险：极端单发行 10 万轨形状有偏慢的历史证据，未承诺当前实现同口径已达标；未做真实读屏或 Firefox/Safari 手工验收。另有既有问题：密码提示“至少 12 个字符”而实际按 12 字节校验（4 个汉字可通过），来自 `6efc902`，留独立修正。
