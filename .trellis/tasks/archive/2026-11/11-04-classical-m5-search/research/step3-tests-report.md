# 步骤 3：④ 搜索测试最终报告

日期：2026-11-04。工作区：`m5-search-artists`。本次在产品负责人修复 D1–D6 后复验；**本测试会话仅修改测试/替身与验收记录，没有修改产品代码或规范，没有 commit、stash、reset、checkout**。构建照约定更新内嵌 web 产物。

## 最终结论

**本步指定的相关自动化测试及质量门禁全部通过。**

| 检查 | 最终结果 |
| --- | --- |
| 原 9 个 Vitest 失败所在两个文件 | 93/93 通过，包含原 9 个失败用例 |
| 报告汇总 Vitest 集合 | 20 文件，592/592 通过，0 失败、0 跳过 |
| 原 2 个 Playwright 失败（1024/1440） | 2/2 通过，且包含新增 D6 断言 |
| 完整搜索 Playwright 文件 | 9/9 通过，六宽度 320/375/390/768/1024/1440 |
| axe、纯键盘与截图 | 六宽度建议/结果 WCAG A/AA 无违规；1440/390 纯键盘搜索播放通过；四图已重新生成并打开复核 |
| lint、typecheck、build、diff 检查 | 全部通过 |
| 真实服务 | 沿用上轮 2/2：繁简、全半角各搜索并原生媒体实播；**本轮未重跑** |

原失败没有改成预期失败、没有跳过或放宽断言。原始失败报告与日志保留在 `research/step3-search-tests/initial-report.md` 及原有压缩日志，不把历史失败写成从未发生。93 项、2 项定点集是最终汇总的子集，不能重复相加。

## 本轮改动与 D3 审查

本轮修改两个测试文件以及本报告/证据：

- `frontend/e2e/support/search.ts`：为专辑、曲目建议添加长作曲署名/厂牌说明；结果分页登记前经过 `decodeAlbumResults` / `decodeTrackResults`，防止替身绕过 D3 校验。
- `frontend/e2e/search.spec.ts`：对专辑、曲目两种长建议分别量标题高度÷行高 ≤2、副行/匹配说明 ≤1；同时验证原始标题行盒 >2（夹具确实会溢出）、副行/小字确实横向溢出且用省略号、完整可访问名称、两项建议浮层高度 ≤320px；结果页曲目标题/艺人/匹配说明仍自然多行，未受建议截断规则影响。
- `e2e/support/api-mock.ts` 只转发显式登记响应，不自动生成搜索分页；**无需修改**。
- `e2e/support/search.ts` 的两栏都是 page=1、page_size=12/20、total=1、eq、has_more=false，符合 D3，不盲改合法值。
- Vitest 的正常替身也符合 D3；eq 999/1000 的末页、gte 第99页空页均合法。非法组合仅保留在 decoder 拒绝测试里。

D3 依据：eq 必须满足 `has_more === (page * page_size < total)`；gte 的 total=1000，页结束位置 ≤1000 时 has_more 不得为 false。

## D1–D6 验收闭环

| 编号 | 本次证据 |
| --- | --- |
| D1 字节上限 | query 201字节、original 4098字节非法响应被拒绝，原2项通过 |
| D2 可用类型 | 重复类型与 unavailable 艺人类型被拒绝，原2项通过 |
| D3 分页组合 | 原4个矛盾组合被拒绝；全部真实样本往返、现有合法分页与替身通过 |
| D4 epoch | 新epoch等待期间不显示旧结果；同代翻页仍保留占位；响应迁键不重发、迟到隔离、外壳自动发布且播放位置不变均通过 |
| D5 时长折行 | 1024/1440 原失败改为通过；六宽度“3:04”的纯文本行盒均为一行 |
| D6 建议紧凑显示 | 六宽度两种建议均验证2/1/1行、实际截断/省略号、完整可访问名称、紧凑浮层与结果页不受影响 |

未发现新的产品缺陷。Vitest 汇总日志仍有既有 jsdom `HTMLMediaElement.pause/load` 未实现提示，不是测试失败，也不冒充真实播放验证。

## 实际命令与结果（本轮）

以下 npm/npx 命令在 `frontend/` 执行；浏览器命令明确持共享锁并在锁内加载环境。

```bash
npx vitest run src/features/search/api/decoders.test.ts \
  src/features/search/hooks/use-search-results.test.tsx \
  --reporter=dot --reporter=json \
  --outputFile=/tmp/m5s-recheck-vitest-targeted.json
# 93/93通过；原9项失败均恢复。

npx vitest run src/features/search src/features/catalog/api/catalog-state.test.ts \
  src/app/search-results.test.tsx src/app/search-playback.test.tsx \
  src/app/shell/global-search-shortcut.test.tsx src/app/shell/catalog-epoch-sync.test.tsx \
  src/test/search-data.test.ts src/test/api-contract.test.ts src/app/app.test.tsx \
  src/app/release-grouping.test.tsx src/app/claim-flow.test.tsx src/app/albums.test.tsx \
  src/app/album-detail.test.tsx src/app/recent-plays.test.tsx \
  --reporter=dot --reporter=json --outputFile=/tmp/m5s-recheck-vitest-summary.json
# 20文件，592/592通过，0失败、0跳过。

flock /tmp/roomusic-m5-e2e.lock bash -lc '
  source /home/qlqq/.local/share/playwright-deps/env.sh
  cd /home/qlqq/.paseo/worktrees/2i6geckb/m5-search-artists/frontend
  npx playwright test e2e/search.spec.ts --grep "1024px|1440px：搜索框" --workers=2
'
# 原两个失败：2/2通过，包含D6。

flock /tmp/roomusic-m5-e2e.lock bash -lc '
  source /home/qlqq/.local/share/playwright-deps/env.sh
  cd /home/qlqq/.paseo/worktrees/2i6geckb/m5-search-artists/frontend
  npx playwright test e2e/search.spec.ts --workers=2
'
# 9/9通过；为让1440结果截图同时看到两节与时长，专辑匹配说明长夹具由20次重复缩到6次，
# 仍明确断言实际截断，并在此调整后复跑整个文件9/9通过。

npm run lint
npm run typecheck
npm run build
git diff --check
# 均通过。
```

Vitest 子会话使用 `set -o pipefail` 与 `tee` 留存命令输出，退出码均为0；主会话同样留存浏览器及完整门禁日志。没有运行全量前端测试或 Go 测试，范围为指定汇总集。无需重新 `npm ci`，沿用上轮同一锁文件已安装的依赖。

## 四张截图

- `frontend/test-results/m5-search/search-1440-suggestions.png`
- `frontend/test-results/m5-search/search-1440-results.png`
- `frontend/test-results/m5-search/search-390-suggestions.png`
- `frontend/test-results/m5-search/search-390-results.png`

截图为本轮重新生成，目录受 Git 忽略。1440：侧栏216px内输入框、380px建议浮层；390：窄屏菜单输入框与收缩到视口内的浮层。建议标题两行、副行与说明一行且省略，浮层明显紧凑；结果页保持原有多行文案，时长一行。后续 Playwright 会清空 test-results，需长期存证时先复制附件。

## 验证范围与未覆盖项

- 本轮产品修复只涉及前端解码/占位/样式，没有改变规范化后端、搜索端点或播放链路；全部48份真实搜索目录样本（含两status及错误/旧路由）往返通过，受控浏览器的搜索播放也通过。因此依照用户要求**不重跑真实服务**，沿用上轮繁简/全半角各命中且实播2/2证据，不将其计为本轮执行。
- S1其余规范化、S2字段/修正投影、S4规模性能与后台并发沿用步骤1证据；不重复造规模库或运行数据库测试。
- ⑤艺人、收藏/加入歌单及跳专辑后定位高亮仍为后续步骤，不属于本次④。
- 读屏为DOM/live region与可访问名称自动验证，未人工NVDA/VoiceOver实听；IME为组合事件模拟；浏览器仅Chromium，不声称Safari/Firefox已覆盖。

## 本步骤累计测试文件清单


以下均相对 `frontend/`，共 22 个测试/基础设施文件：

| 文件 | 改动 |
| --- | --- |
| `src/test/api-contract.test.ts` | 登记 48 份搜索目录样本，使用对应搜索/catalog/旧路由/错误 decoder 完整往返 |
| `src/test/fake-api.ts` | 已登录默认 status；`installFakeSearch` 显式登记替身 |
| `src/test/search-data.ts` | 搜索请求键补默认值并排序，未登记 404；真实样本 decoder factories |
| `src/test/search-data.test.ts` | 默认状态、未登记 404、请求键与显式响应 |
| `e2e/support/api-mock.ts` | 已鉴权默认 status、`ApiMock.search` |
| `src/features/search/api/decoders.test.ts` | 真实样本、必传字段、上限、枚举与非法组合；原 8 失败已通过 |
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
| `src/features/search/hooks/use-search-results.test.tsx` | epoch 迁键无重复、迟到隔离、同代翻页；原跨代占位失败已通过 |
| `src/app/search-playback.test.tsx` | 真 PlayerEngine + FakeAudio：播放、下一首、加入、不可播说明、10,000 满队列不丢项 |
| `e2e/support/search.ts` | 显式搜索样本/长文案与合成 FLAC 路由，不替换浏览器播放行为 |
| `e2e/search.spec.ts` | 六宽度、文字行盒/位置、axe、键盘、曲目建议不播放、截图与时长回归 |
| `e2e/real/search.real.ts` | 隔离合成 WAV、真实扫描/目录就绪、繁简/全半角命中与实播 |

已有请求数断言在默认 status 接入后没有失败，因此没有无依据修改请求数。


## 证据目录

`research/step3-search-tests/summary.json` 为最终统计；`initial-summary.json`/`initial-report.md` 保留上轮失败结论。`recheck-*.log.gz` 为本轮定点、汇总、Playwright及门禁输出；原 `real.log.gz` 是上轮真实服务证据。未提交或归档任务。
