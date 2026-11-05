# 步骤 3：④ 搜索测试交付报告

日期：2026-11-04。工作区：`m5-search-artists`。仅测试、测试基础设施及本报告；不包含⑤艺人。没有提交、stash、reset、checkout，没有改动产品代码或前端规范。已有前端实现与规范改动原样保留，生产构建按约定重建 `backend/cmd/roomusic/web`。

## 结论

**测试已补齐，但④质量验收未通过，不应提交为全绿。**

- 最终相关 Vitest：20 文件、592 项，**583 通过、9 失败**。48 份 `search/**` 样本（其中包含两个 catalog status、错误及旧路由样本）全部往返通过；全 API 样本登记检查通过。
- 最终 Playwright：9 项，**7 通过、2 失败**。六宽度 320/375/390/768/1024/1440 均无横向溢出，建议/结果的 WCAG A/AA axe 无违规；1440 与 390 纯键盘搜索并原生媒体播放通过。两个失败来自 1024、1440 下曲目时长折行，非 axe 或溢出失败。
- 真实服务：**2/2 通过**，简体 `爱与乐章` 搜到繁体 `愛與樂章`，全角 `ＡＢＣ１２３` 搜到半角 `ABC123`；每次也断言另一首不命中及无匹配查询为空；两者均从结果页播放，观察原生 `playing` 事件及时间增长。扫描后等待 `/catalog/status` 的 `search_mode=normalized`。
- `npm run lint`、`npm run typecheck`、`npm run build` 通过；`git diff --check` 通过。
- 未运行全量 Vitest、全量 Playwright 或 Go 测试：本次只新增测试与替身，使用新增搜索及直接受影响页面的最小集合；后端性能、扫描、投影与数据库合同沿用步骤 1 的报告，不重复冒充本轮验证。

## 文件清单

以下均相对 `frontend/`，共 22 个测试/基础设施文件：

| 文件 | 改动 |
| --- | --- |
| `src/test/api-contract.test.ts` | 登记 48 份搜索目录样本，使用对应搜索/catalog/旧路由/错误 decoder 完整往返 |
| `src/test/fake-api.ts` | 已登录默认 status；`installFakeSearch` 显式登记替身 |
| `src/test/search-data.ts` | 搜索请求键补默认值并排序，未登记 404；真实样本 decoder factories |
| `src/test/search-data.test.ts` | 默认状态、未登记 404、请求键与显式响应 |
| `e2e/support/api-mock.ts` | 已鉴权默认 status、`ApiMock.search` |
| `src/features/search/api/decoders.test.ts` | 真实样本、必传字段、上限、枚举与非法组合；保留 8 失败 |
| `src/features/search/api/endpoints.test.ts` | 编码、取消信号、21 页、无效响应与 503 |
| `src/features/catalog/api/catalog-state.test.ts` | 状态样本往返与非法状态字段 |
| `src/app/release-grouping.test.tsx` | 空提示改为“请输入搜索词。” |
| `src/app/claim-flow.test.tsx` | 同上 |
| `src/features/search/components/global-search-box.test.tsx` | 250ms、防抖重启、单字、IME、按键、200 字节、空/错态、迟到响应、稳定后播报 |
| `src/features/search/hooks/use-search-suggestions.test.tsx` | 取消、占位/新词、卸载、空与失败 |
| `src/features/search/match-note.test.ts` | 字段说明；行内已显示原文不重复 |
| `src/app/shell/catalog-epoch-sync.test.tsx` | 外壳实际状态接口三秒轮询、隐藏暂停/前台重读、发布停轮询与目录刷新；搜索词/页码/播放位置保持 |
| `src/app/shell/global-search-shortcut.test.tsx` | 宽/窄框与菜单、可编辑元素、修饰键/IME/已消费事件、对话框 |
| `src/app/search-results.test.tsx` | 两栏独立加载/失败/重试，12/20，独立翻页、eq/gte、21/51页、越界、换词与地址恢复 |
| `src/features/search/search-params.test.ts` | URL 校验、字节边界、页码与换词 |
| `src/features/search/hooks/use-search-results.test.tsx` | epoch 迁键无重复、迟到隔离、同代翻页；保留跨代占位失败 |
| `src/app/search-playback.test.tsx` | 真 PlayerEngine + FakeAudio：播放、下一首、加入、不可播说明、10,000 满队列不丢项 |
| `e2e/support/search.ts` | 显式搜索样本/长文案与合成 FLAC 路由，不替换浏览器播放行为 |
| `e2e/search.spec.ts` | 六宽度、文字行盒/位置、axe、键盘、曲目建议不播放、截图与时长回归 |
| `e2e/real/search.real.ts` | 隔离合成 WAV、真实扫描/目录就绪、繁简/全半角命中与实播 |

已有请求数断言在默认 status 接入后没有失败，因此没有无依据修改请求数。

## 产品缺陷（均保留普通失败断言）

| 编号 | 复现与实际结果 | 预期/定位 |
| --- | --- | --- |
| D1 | `search/api/decoders.test.ts`：67 个“爱”（201 字节）的 query、1366 个“爱”（4098 字节）的 match.original 被接受；2 项失败 | 按 UTF-8 字节上限 200/4096 拒绝；当前 decoder 只限 JS 字符长度 |
| D2 | 同文件：`available_types=[albums,albums]` 或 unavailable 状态带 artists 均被接受；2 项失败 | 可用类型唯一且与④能力状态一致 |
| D3 | 同文件：eq total0 + has_more=true；eq total100 首页 + false；eq total1000 第20页 + true；gte1000 首页 + false（page_size50），均被接受；4 项失败 | 同快照的计数与是否存在下一页不能矛盾；不把 gte 当精确末页 |
| D4 | `use-search-results.test.tsx`：“epoch 上升时不把旧结果当成新代次的翻页占位显示”；新 epoch 请求等待时仍有“旧规则命中”；1 项失败 | `use-search-results.ts` 的 `keepPreviousData` 应只在合适的同代条件下保留，旧代不得显示 |
| D5 | `e2e/search.spec.ts`：1024、1440 长标题结果的“3:04”实际占两行，1440 截图为“3:0 / 4”；2 项失败 | 时长数值应保持一行。文字 Range 仅量文本节点，排除 SVG 图标；等待字体就绪后仍复现，未改 CSS |

D1–D4 不挡住其他验证；D5 通过截图复核发现，再补可复现断言。没有 `test.fails`、skip 或为了全绿放宽断言。

## 实际验证命令与结果

除明确 `cd` 外，下列 npm/npx 命令均在 `frontend/` 执行。

### 安装与子任务最小集

```bash
npm ci
# 通过；251 packages，审计 0 vulnerabilities。

npx vitest run src/test/api-contract.test.ts src/features/search/api/decoders.test.ts src/features/catalog/api/catalog-state.test.ts --reporter=dot
# 290 通过、8 失败；真实样本全部通过。

npx vitest run src/test/search-data.test.ts src/app/app.test.tsx src/app/release-grouping.test.tsx src/app/claim-flow.test.tsx --reporter=dot
# 80 通过。

npx vitest run src/app/albums.test.tsx src/app/album-detail.test.tsx src/app/recent-plays.test.tsx --reporter=dot
# 104 通过。

npx vitest run src/features/search/api/endpoints.test.ts --reporter=dot
# 5 通过。
npx vitest run src/test/search-data.test.ts --reporter=dot
# 4 通过（与前组重复，不叠加统计）。

npx vitest run src/features/search/components/global-search-box.test.tsx src/features/search/hooks/use-search-suggestions.test.tsx src/features/search/match-note.test.ts src/app/shell/global-search-shortcut.test.tsx
# 54 通过。

npx vitest run src/app/search-results.test.tsx src/app/search-playback.test.tsx src/features/search/hooks/use-search-results.test.tsx src/features/search/search-params.test.ts
# 40 通过、1 失败。
```

### 汇总复跑（最终统计，不与以上重复相加）

```bash
npx vitest run src/features/search src/features/catalog/api/catalog-state.test.ts \
  src/app/search-results.test.tsx src/app/search-playback.test.tsx \
  src/app/shell/global-search-shortcut.test.tsx src/app/shell/catalog-epoch-sync.test.tsx \
  src/test/search-data.test.ts \
  src/test/api-contract.test.ts src/app/app.test.tsx src/app/release-grouping.test.tsx \
  src/app/claim-flow.test.tsx src/app/albums.test.tsx src/app/album-detail.test.tsx \
  src/app/recent-plays.test.tsx --reporter=dot --reporter=json \
  --outputFile=/tmp/m5s-search-vitest.json
# 20 文件、592 项：583 通过，9 失败。
```

### 浏览器与真实服务（整条命令持共享锁）

```bash
flock /tmp/roomusic-m5-e2e.lock bash -lc '
  source /home/qlqq/.local/share/playwright-deps/env.sh
  cd /home/qlqq/.paseo/worktrees/2i6geckb/m5-search-artists/frontend
  npx playwright test e2e/search.spec.ts --workers=2
'
# 最终 7 通过、2 失败（D5），不是全绿。

flock /tmp/roomusic-m5-e2e.lock bash -lc '
  source /home/qlqq/.local/share/playwright-deps/env.sh
  cd /home/qlqq/.paseo/worktrees/2i6geckb/m5-search-artists
  scripts/playback-e2e.sh search.real.ts
'
# 2 通过，退出成功；本轮 roomusic-test 容器、网络、数据卷全部清理。
```

浏览器初轮：曾被并行新增测试的两个 TS 错误阻止启动；测试自身修复收窄后的 SessionState 与 DOMRectList iterator 后通过类型检查。随后初版断言误用精确“播放”和“查看全部结果”（实际可访问名带曲名/查询词），8 失败、1 通过；修正定位后，文字行数函数误把 SVG 图标也算一行，6 失败、3 通过。改为只量文本节点后 9/9 通过。截图发现 D5 并新增断言后，最终是上述 7/2；旧 9/9 不代表最终版本通过。

### 静态与构建门禁

```bash
npm run lint
npm run typecheck
npm run build
git diff --check
```

均通过。子任务分别对其上表负责的测试文件运行了 `npx eslint <文件列表>`，通过；曾发现测试直接调用 fetch，改成生产 endpoint 调用后通过。第一次在仓库根误运行 `npm run typecheck` 得到 ENOENT（根无 package.json），已切到 frontend 后执行；不算产品缺陷。各子任务 typecheck 的临时在编错误均已消除。

### 复核后追加的防假阳性验证

```bash
npx vitest run src/features/search/components/global-search-box.test.tsx
npx eslint src/features/search/components/global-search-box.test.tsx
# 17 通过；专辑/曲目建议使用不同目标；两类建议的作曲说明与行内去重。

npx vitest run src/app/search-results.test.tsx
npx eslint src/app/search-results.test.tsx
# 16 通过；不同q/page使用不同ID/标题，前后退/换词实际内容也变化，三类说明及标题去重。

npx vitest run src/app/shell/catalog-epoch-sync.test.tsx --reporter=dot
# 2 通过；真实外壳装配/状态接口驱动，不是仅直接改Query缓存。
```

真实服务和浏览器也按上面的持锁命令复跑，结果仍为实播2/2、浏览器7/2；曲目建议增加具体release参数与详情加载断言。首次补外壳测试时误在仓库根运行该 npx 命令，临时 npm 缓存下载 Vitest 5.1.0、未加载 frontend 的 jsdom 配置，报 document 未定义；没有修改 package.json/lockfile，此轮不计验收。改到 frontend 后使用锁定的 Vitest 4.1.12；随后修正合成 visibilitychange 事件为可冒泡（Query 在 window 上监听），最终2/2。全部追加项均已纳入最终592项统计。

## 截图与视觉复核

最终截图归档在（被 Git 忽略的本地验收产物）：

- `frontend/test-results/m5-search/search-1440-suggestions.png`：216px 侧栏内输入框、380px 建议浮层。
- `frontend/test-results/m5-search/search-1440-results.png`：专辑/曲目两节与侧栏框，能看到 D5。
- `frontend/test-results/m5-search/search-390-suggestions.png`：窄屏菜单搜索及按视口收缩的建议浮层。
- `frontend/test-results/m5-search/search-390-results.png`：窄屏结果页框、专辑与曲目两节。

390 下侧栏按产品设计隐藏，用菜单提供等价搜索，没有伪造一个不存在的窄屏侧栏。四张截图均实际打开复核；建议长文案可换行、浮层不被侧栏/菜单裁掉、操作文字一行；宽屏时长折行单独列为 D5。

原始各用例目录同在 `frontend/test-results/`，失败目录带 `trace.zip` 与 `error-context.md`。后续 Playwright 会清空该目录，若主会话需要长存截图，应先复制到验收附件。

## 未覆盖或尚未通过

- D1–D5 尚未修复，相关拒绝规则、旧 epoch 不显示、宽屏时长布局**未通过**，留给产品实现负责人；其他证据不抵消这些失败。
- ⑤艺人、收藏/添加歌单、建议跳专辑后的定位高亮属于后续步骤，不属于本次④测试。
- S1 的其余双向规范化、S2 全字段/修正投影与 S4 十万轨性能/回填并发沿用步骤 1 后端证据。本轮真实服务仅新增用户要求的繁简和全半角，不重复运行规模库或 Go 数据库测试。
- 读屏仅验证 DOM live region 稳定后的播报变化，没有人工 NVDA/VoiceOver 实听；IME 用组合事件模拟，不冒充真实操作系统输入法手测。
- 浏览器只按现有配置使用 Chromium，不声称 Safari/Firefox 已覆盖。

详细统计与日志位于 `research/step3-search-tests/`：`summary.json`、最终 Vitest/Playwright/实播压缩日志及测试开发阶段失败日志。实播后按 Compose 标签确认 `roomusic-test` 无残留容器/卷；未连接或操作用户的 `roomusic-postgres-1` 或 `roomusic` 数据库。
