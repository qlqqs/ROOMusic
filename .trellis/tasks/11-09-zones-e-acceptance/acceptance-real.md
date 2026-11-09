# E 第 1、3 项：真实服务验收记录

日期：2026-11-09。工作分支：`playback-baseline`。

## 边界

- 只修改真实服务验收测试、其隔离启动/重启脚本及本记录，不修改业务代码、不提交 Git。
- 单次隔离 PostgreSQL 与服务实例，重启复用本轮数据库、音乐目录与数据目录；不接管已有容器。
- 只用默认 Chromium；手机为独立浏览器上下文，不代表 iOS 真机验收。
- 远程输出和标签页接手前先点击目标页面，真实媒体解码和时间推进不被替换；自动化“出声”证据为原生媒体就绪、未暂停/静音且时间推进，不代表物理扬声器听测。
- 未修改其他会话负责的组件、信号路径样式或 `.trellis/spec/`。

## 派生退役核对

启动前在本轮数据目录预置 `media-cache/old.flac`、`media-cache/nested/partial.tmp`、`media-cache/index.json`；HTTP 就绪后断言整个目录已删除，服务日志的 `playback.derivation.cache_removed` 恰有一条。验收结束再次检查，防止重启重复记录不存在的缓存删除。

## 旧用例

`frontend/e2e/real/playback.real.ts` 已由 F2 提交 `c0ec62b2d4241e0111bb47758c1866ce49b23f4b` 删除，本轮无需重复删除。

## 未纳入本轮

- iPhone 锁屏连续跨两首、无缝、格式边界/HLS、锁屏元数据与解锁状态：E 第 2 项真机手工核对。
- 手机省流量有损档真机播放：E 第 2 项。
- 协议真实 handler 样本与 spec：E 第 4 项，另一个 agent 负责；本轮不声称完成。

## PRD 验收与用例对应

| PRD 验收 | 用例/步骤（均在 `playback-zones.real.ts`） |
| --- | --- |
| 手机控制电脑 | 用例 2 第一步：手机选择电脑，起播、真实进度推进、暂停/继续、下一首、拖动定位；手机不出声 |
| 转移 | 用例 2 第二步：电脑转手机，队列条目及当前项保留、原位置继续、电脑停播且源队列为空 |
| 离线 | 用例 2 第三步：转回电脑并关闭页面，设备离线、播放区暂停，保留队列位置后从手机接手 |
| 重启 | 用例 3：两个播放区分别播放/暂停且定位到非零位置，脚本真正重启服务；不刷新页面，检查新输出会话、队列/位置与播放意图恢复 |
| 多标签页 | 用例 1：同一浏览器两页只一页出声，关闭输出页后另一页换输出会话并从保留位置继续 |
| 隔离 | 用例 4：两个成员互不可见；跨成员队列读取、控制、转移、设备写入、音频读取返回 404 |
| 迁移 | 用例 5：预置旧 sessionStorage，导入队列、当前项、模式及位置；显示本机队列、刷新不重复导入、从原位置播放 |
| ALAC、歌词、计一次播放 | 用例 6：ALAC 来源经 FLAC 渲染流实际推进；歌词高亮、Media Session；过半并到曲尾只计一次，seek 跨过长曲不额外计次 |
| 电平化、无缝 | 用例 7：真实分析非零增益与信号路径一致；同专辑两首 12 秒素材同流连续解码，分段边界、曲名切换、无中途 ended |
| 5 秒交叉渐变 | 用例 8：两张不同专辑、各 20 秒（满足有效时长 > 3×5 秒）；设置页打开 5 秒渐变，分段在约 15 秒开始，跨边界同流连续播放 |
| 页面点击前提 | 输出页通过可见标题点击解锁；多标签页接手目标也先点击 |
| 锁屏、省流量真机、协议 | 不计入本轮自动化通过，见上一节分工 |

## 执行结果

### 首次执行

```bash
source /home/qlqq/.local/share/playwright-deps/env.sh
scripts/playback-e2e.sh e2e/real/playback-zones.real.ts
```

- 脚本总耗时 **194 秒**；Playwright 约 **2.8 分钟**。
- 多标签页通过（5.5 秒）；旧缓存目录删除与日志恰一次通过（退出清理时也检查）。
- 远程用例在测试前置失败：单一空播放区不显示播放器，却从播放器打开改名。这不是业务验收失败；改为公开 PATCH 设置测试设备名。
- 主动中止剩余运行以免重复命中此前置；重启用例被中断，另 5 例未运行。未执行真正服务重启。
- 第一轮日志：`/tmp/zones-e-acceptance-20261109.log`；trace 备份：`/tmp/zones-e-acceptance-first-results-20261109/`。
- 后续只执行未通过的 7 项，不重跑已通过多标签页；重启同样使用脚本的新隔离实例。


### 第二次执行：剩余七项

```bash
source /home/qlqq/.local/share/playwright-deps/env.sh
scripts/playback-e2e.sh e2e/real/playback-zones.real.ts --grep-invert '同一浏览器两个标签页'
```

- 脚本 **118 秒**；Playwright **3 通过、4 失败**，约 1.5 分钟。
- 通过：手机远程控制/转移/离线（12.7 秒）、成员隔离（3.0 秒）、旧队列迁移（3.6 秒）。
- 真实重启失败（39.5 秒），见问题 1。
- ALAC 用例（25.1 秒）前两个步骤通过：真实渲染、歌词、Media Session、过半及曲尾仅一条历史、页面历史显示。第三步的长曲起播停在 loading，见问题 3；尚未执行到 seek 175 秒后的负向计次断言。
- 电平化与渐变在设置页的 `.check()` 失败：控件等服务端确认才更新，Playwright `.check()` 在点击后立即判定状态。改为 `click()` 后以 `toBeChecked()` 等待可见状态，不改业务、不用固定延时。
- 日志 `/tmp/zones-e-acceptance-20261109-remaining.log`；完整 trace 备份 `/tmp/zones-e-acceptance-second-results-20261109/`。

### 第三次执行：仅两个尚未验到渲染的设置用例

```bash
source /home/qlqq/.local/share/playwright-deps/env.sh
scripts/playback-e2e.sh e2e/real/playback-zones.real.ts --grep '本机电平化|不同专辑 5 秒'
```

- 脚本 **94 秒**；两例失败。
- 电平化（31.7 秒）：独立启动后分析仍为 `pending`，未到电平化断言。`backend/internal/playback/analysis_worker.go:19` 的真实空闲轮询周期为一分钟，原 30 秒状态等待不足；按真实周期修正该状态等待，不替换分析结果。
- 交叉渐变（28.7 秒）：真实媒体超过 16 秒、仍是同一流、页面已显示第二首；系统 Media Session 仍为第一首，见问题 2。trace 中第二首 `stream_start_seconds=14.999909297052154`、`track_start_seconds=0`，证实 20 秒素材的 5 秒重叠。后续将边界断言移到 Media Session 断言之前，保留系统信息的失败断言；不重跑此业务失败。
- 日志 `/tmp/zones-e-acceptance-20261109-dsp.log`；trace 备份 `/tmp/zones-e-acceptance-third-results-20261109/`。

## 初次验收发现的问题（历史证据）

以下三项保留初次失败的现象与分析；已分别由 `c571188`（问题 1、2）和 `ca26b33`（问题 3）修复，并在下方“修复后定向复验”中通过。原失败断言未放宽，等待时长未增加。

### 1. 重启后输出不重新认领，设备持续离线

复现：两浏览器打开并点击；电脑播放长曲并定位 30 秒，手机暂停在 42 秒；保留数据库重启服务，不刷新、不再点击。

trace 证据（UTC）：07:16:35.700 电脑位置 31.8、手机 42；07:16:36.089 至 07:17:10.738 两设备始终 `online:false`、`output_session_id:null`；07:16:36.832 两页带旧会话重连 SSE 返回 200，之后无新 claim/report。位置确实持久化，但自动恢复不成立，队列/恢复后全部断言未到达。

高置信原因：

- `backend/internal/zones/service.go:166–170` 将重启加载的区恢复为暂停、无输出会话。
- `backend/internal/zones/output_lifecycle.go:138–147` 将旧会话 SSE 退化为普通订阅，不报 `output_unknown`。
- `frontend/src/features/player/zones/controller.ts:386–419` 直接应用快照并对齐，未识别输出会话已失效。
- `frontend/src/features/player/output/output.ts:193–199、266–276` 收到 `stream:null` 即卸载媒体；pause/timeupdate 无法继续上报（`:345–348、369–371`）。
- 重新认领只在 report 返回 `404 output_unknown` 时发生（`:429–444`）。没有报告就不会触发恢复，暂停设备也缺此触发。

对应重启断言保持失败；不能以延长等待代替修复。

### 2. 同一连续流换曲后系统媒体信息仍停在第一首

复现：两张不同专辑各 20 秒，打开 5 秒交叉渐变；页面切到 `Zone Fade Second` 后，`navigator.mediaSession.metadata.title` 仍为 `Zone Fade First`。第五轮的同专辑 12 秒无缝换曲也出现相同问题（页面 `Zone Second`、系统信息 `Zone First`）。

原因：`frontend/src/features/player/output/output.ts:193–204` 对相同 seq 的当前流不更新 slot 中的流对象；`:507–513` 的 `segment()` 读取的是旧 `active.stream.segments`；`:516–527` 又优先以该旧分段刷新 Media Session，覆盖了当前播放区曲目。后端已经发布第二首分段（约 15 秒），页面播放区状态也已经切换。

保留失败断言。本条 Chromium 证据不能替代 iPhone 锁屏手工验收，但说明系统信息存在不退化问题。

### 3. 关闭电平化后，长静音素材起播停在加载中

复现：用例 6 完整播放 ALAC、看到历史仅一条；打开播放历史页面并点击标题，设备已重新认领；以公开命令播放 180 秒 `Slow Revocation`（纯静音），电平化 `off`。

证据：07:17:33.011 新输出会话认领 200，07:17:33.155 `play_tracks` 返回 `intent:play`、`status:loading`，随后 FLAC 流请求 200；10 秒后页面仍“正在加载”、进度 0，原生媒体没有进入 readyState≥2 的播放状态。没有 `autoplay_blocked` 报告，也没有继续时间上报。ALAC/歌词/一次计数本身已通过，失败在附加的长曲负向计次场景起播前。

待实现方定位具体卡点。静态上疑似与无处理纯静音的高压缩 FLAC 和有限预渲染窗口有关：`backend/internal/playback/render/encode.go:32–60`、`session.go:299–304`（最多提前 20 秒）；默认 auto 模式对缺分析素材会施加 -5 dB 并抖动，而 off 保持精确静音，输出数据量不同。此机制仅为排查方向，未用额外实验确认，不将其当作已证实根因。对应失败断言保留。

## 初次验收验证汇总

第四次仅补跑电平化：

```bash
source /home/qlqq/.local/share/playwright-deps/env.sh
scripts/playback-e2e.sh e2e/real/playback-zones.real.ts --grep '本机电平化'
```

- 脚本 **104 秒**；分析在 75 秒界限内仍为 `pending`，未到渲染断言；随后发现原脚本生成的无关格式矩阵占用分析队列，收窄到本文件实际使用的六首，再做最后一次该项补跑。
- 后台分析除空闲一分钟外，每首还有至少 2 秒低优先级间隔（`backend/internal/playback/analysis_worker.go:141–145`），隔离脚本原有格式素材也在排队。第二轮较晚执行此用例时分析已完成，第三、四轮只跑子集反而没有前面场景提供的分析时间。因此第四轮属于验收前置/耗时限制，**不能据此认定电平化实现有缺陷**；第五轮收窄素材后已验到渲染。
- 第四轮日志 `/tmp/zones-e-acceptance-20261109-leveling.log`；trace 备份 `/tmp/zones-e-acceptance-fourth-results-20261109/`。

第五次仍只执行同一条 `--grep '本机电平化'` 命令：`playback-e2e.sh` 在单独运行本文件时不再复制旧格式矩阵/CUE，仅生成本次所需六首；分析状态等待覆盖一分钟空闲周期及六首的低优先级间隔，仍为状态轮询，不使用固定 sleep。

- 脚本 **116 秒**；电平化和无缝主体断言全部执行通过：真实分析增益 **+7.8 dB**、信号路径一致；第二首分段起点 **12 秒**、曲内起点 **0**，同一 src/seq、只有一个媒体、跨边界没有 ended/error。
- 用例最终失败于同一个系统信息问题：页面已是 `Zone Second`，Media Session 仍为 `Zone First`。断言保留，问题 2 同样影响无缝，不再重跑。
- 日志 `/tmp/zones-e-acceptance-20261109-leveling-focused.log`；最终 trace 在 `frontend/coverage/playback-real/results/`。

初次验收五轮脚本报告耗时合计 **626 秒（10 分 26 秒）**，包括构建、隔离库创建与清理。没有重跑已通过用例；重启、长静音起播、交叉渐变系统信息的业务失败也未重跑。后续四轮均只选择同一文件的未通过/未覆盖子集，用于修正测试前置、异步控件等待及分析素材范围。无全量测试、无多浏览器矩阵。

## 修复后定向复验（2026-11-09）

复验基线：当前 HEAD `ca26b33`，已包含 `c571188`。本次仅更新验收记录，不改业务代码、测试代码、断言或等待时长，不提交 Git。

先加载本机 Playwright 依赖，然后用一次 `--grep`、一次隔离服务运行选中用例 1、3、6、7、8：

```bash
source /home/qlqq/.local/share/playwright-deps/env.sh
scripts/playback-e2e.sh e2e/real/playback-zones.real.ts --grep '同一浏览器两个标签页|真实服务重启|本机 ALAC|本机电平化|不同专辑 5 秒'
```

| 原用例编号 | 复验范围 | 结果 | 用例耗时 |
| --- | --- | --- | --- |
| 1 | 多标签页只一页出声、关闭输出页后接手 | 通过 | 5.5 秒 |
| 3 | 真实重启、两区队列/位置保留、播放区自动恢复而暂停区保持暂停 | 通过 | 7.8 秒 |
| 6 | ALAC、歌词、Media Session、一次计次、长静音起播及 seek 不虚增计次 | 通过 | 23.5 秒 |
| 7 | 真实分析增益、信号路径、同流无缝、系统媒体信息随曲目更新 | 通过 | 15.4 秒 |
| 8 | 两专辑 5 秒渐变、同流分段边界、系统媒体信息随曲目更新 | 通过 | 17.8 秒 |

- **5 passed，退出码 0**；Playwright 报告 **1.2 分钟**，脚本总耗时 **97 秒（1 分 37 秒）**，含构建、隔离服务/数据库启动与清理。
- 同次启动的旧 `media-cache` 删除核对通过，真实重启后删除日志仍恰一条。
- 用例 2（远程/转移/离线）、4（隔离）、5（迁移）未重跑，沿用初次通过结果。未运行全量测试或其他浏览器项目。
- 一次通过后结束，无重试或追加测试。没有新失败；初次发现的三个问题均通过原有行为断言复验。
- 执行日志：`/tmp/zones-e-recheck-c571188-ca26b33.log`；HTML 报告：`frontend/coverage/playback-real/report/index.html`。成功用例按现有配置不保留失败 trace。
- 本次脚本自带 `tsc --noEmit`、Vite 生产构建、临时树 `go build` 均通过；既存 PostCSS `from` 警告仍出现。隔离容器及数据卷已由脚本清理。

## 最终状态（以本次复验更新）

| 项目 | 最终状态 |
| --- | --- |
| 多标签页 | 本次复验通过 |
| 手机控制电脑、转移、离线 | 初次验收通过，本次未重跑 |
| 成员隔离 | 初次验收通过，本次未重跑 |
| sessionStorage 迁移 | 初次验收通过，本次未重跑 |
| 派生缓存退役 | 通过；每次启动同轮检查，真实重启后仍恰一条删除日志 |
| 服务重启 | 本次复验通过；问题 1 已修复 |
| ALAC、歌词、一次播放 | 本次完整通过，含长曲起播与负向计次；问题 3 已修复 |
| 电平化、同专辑无缝 | 本次完整通过，含 Media Session 换曲；问题 2 已修复 |
| 5 秒交叉渐变 | 本次完整通过，含约 15 秒第二分段与 Media Session 换曲；问题 2 已修复 |
| iPhone 锁屏、省流量、协议 spec | 本轮范围外，未声称通过 |

初次验收执行的静态验证（本次未重复执行）：

```bash
bash -n scripts/playback-e2e.sh
python3 -c 'from pathlib import Path; p = Path("scripts/playback-e2e-server.py"); compile(p.read_text(), str(p), "exec")'
cd frontend && npm exec eslint -- e2e/real/playback-zones.real.ts
git diff --check -- scripts/playback-e2e.sh scripts/playback-e2e-server.py frontend/e2e/real/playback-zones.real.ts .trellis/tasks/11-09-zones-e-acceptance/acceptance-real.md
```

指定脚本各轮自带的 `tsc --noEmit`、Vite 生产构建、临时树 `go build` 均通过。前端构建有既存 PostCSS `from` 警告，不影响构建。本轮没有改构建资产；原工作区中其他 agent 的改动保持原样。未运行全量测试，遵照用户只验这份真实服务用例的约束。

本轮负责的 E 第 1、3 项已具备通过证据（本次复验五例，另外三例沿用初次通过结果）。本任务 E 还包含真机与 spec 分工，**不据此标记整项 E 完成、不归档、不提交 Git**。
