# 执行清单：音频算法对标（父任务）

设计：[design.md](./design.md)（跨子项架构）。子项的执行清单：[dsp-pipeline](../11-08-dsp-pipeline/implement.md)、[gapless-crossfade](../11-08-gapless-crossfade/implement.md)、[dither](../11-08-dither/implement.md)；第二、三批的子项另行设计。
播放区线的执行清单：[zones implement.md](../11-08-server-playback-zones/implement.md)（下称“zones 清单”），本文的时间点都对齐它的 B1/B2/B3/F2/F3/E。

## 0. 开工前

- [x] 主会话裁决 design.md §8 的端口补充（7 条），与 zones B2 的端口一起定稿；采纳的写进 zones 的 design.md §14 与合同（由主会话改，本线不改 zones 的文件）。
- [x] 用户裁决 design.md §12（CUE 虚拟轨是否第一批顺带），结果写进本 prd“已裁决”。
- [ ] iOS 交付小验证先做（dsp-pipeline 清单 P-a），结果写回 dsp-pipeline 的 prd 与 design-delivery.md §3；若命中“交主会话裁决”的几行，先裁决再开 render 骨架。
- [ ] 分支：第一批全部合入 zones 的集成分支 `playback-baseline`。本线用一个独立 worktree（`ROOMusic-dsp`），每个子步骤一个分支，从 `playback-baseline` 切出、做完合回；纯包在 `playback-baseline` 建立之前可以先从 `main` 切出，合入时再并到 `playback-baseline`。
- [ ] 测试库：渲染会话的测试不需要数据库；只有派生退役（P10）的启动核对可能用到隔离库，用独占项目名 `roomusic-test-dsp`（与 zones 的 `roomusic-test-zones` 分开）。
- [ ] 分工（2026-11-09 用户改定）：后端实现 `codex/gpt-6-astra`（high）；前端实现 Opus（high）；测试由 `codex/gpt-6.1-sol`（high）测试 agent 编写与执行，实现 agent 不写新测试。
- [ ] 派活 prompt 写明：测试只做必要的；界面文案不写防御性说明和多余说明；参考稿只引章节号、不摘录、不写系数；纯包不导入项目内其他包。

## 1. 第一批的顺序与依赖

```text
时间 ─────────────────────────────────────────────────────────────────────────────────────────►
zones:  B1 ───────► B2（端口定稿）──► B3 ──────────────► 联调 ──► F3 ──────────────► E
                                                         ▲        │
本线:   P-a iOS 验证（用户真机）                           │        │
        纯包 dsp/chain、leveling、fade、dither ─┐          │        │
                                              ├─► P-c render 骨架 ─┤
                                              │   （B2 之后）      ├─► G 接入（gapless-crossfade）
                                              │                   ├─► D 接入（dither）
                                              │                   ├─► P-e Headroom、信号路径（后端）
                                              │                   └─► P-d 派生退役
                                              │                              P-f 前端（F3 之后）──► E
```

| 步骤 | 前置 | 并行关系 |
| --- | --- | --- |
| P-a iOS 交付小验证 | 无 | 与 zones B1、纯包同时开始 |
| P-b 纯包：`dsp/chain`、`dsp/leveling`（dsp-pipeline）、`dsp/fade`（gapless-crossfade）、`dsp/dither`（dither） | 无 | 四个包互不依赖（`chain` 只在装配时引用 `dither`，可先写接口），可以一个 GPT 子 agent 顺序做完，或两个并行 |
| P-c render 骨架：解码、预缓冲、渲染循环、事件队列、电平化施加、FLAC/WAV/Opus/AAC 编码、协商、`app.OpenRenderer` | zones B2 的端口、P-b、P-a 的结论 | 与 zones B3 并行 |
| G 换轨与淡化接入（gapless-crossfade） | P-c | 与 D、P-e、P-d 并行 |
| D 量化接入（dither） | P-c | 同上 |
| P-e Headroom 设置、削波、处理速度、信号路径（后端） | P-c、zones B2 的 `set_settings` | 同上 |
| P-d 派生退役与格式表 | P-c | 同上 |
| 联调（zones F2 换真实渲染器） | zones B3 + P-c 能出一条可播放的 FLAC 流 | — |
| P-f 前端：信号路径、Headroom 设置 | zones F3、P-e | — |
| E 第一批验收（zones 清单 E，测试 agent） | 以上全部合入 `playback-baseline` | — |

- “联调具备条件”与 zones 清单一致：B3 合入且 P-c 能出可播放的无损流。G、D 不阻塞联调（P-c 自带最小换轨：格式相同就接、不淡化；量化先用直通 + 无抖动取整，D 接入后替换）。
- 每个子步骤完成后由主会话验收再合入 `playback-baseline`；合入顺序：P-b → P-c → G、D、P-e、P-d（任意顺序，冲突时后合的同步）→ P-f。

## 2. 跨子项验收（第一批，对应父 prd 与各子 prd）

| 验收 | 在哪里验证 |
| --- | --- |
| 关掉所有处理、只开无缝时渲染流 PCM 与源逐位相同 | dsp-pipeline 会话测试（MD5）＋ gapless-crossfade 拼接测试（MD5）；E 再用真实服务跑一次 FLAC 与 ALAC |
| 电平化增益与现有前端规则一致 | `dsp/leveling` 移植用例；E 的“不退化”用例听感核对一次 |
| 无缝、交叉淡化、间隔、格式边界、超时 | gapless-crossfade 测试；E 的“不退化”里无缝与交叉渐变各一条 |
| 开电平化后输出带抖动、无处理时不抖动 | dither 单元测试 + 会话测试一条 |
| Headroom、削波指示、处理速度、信号路径 | dsp-pipeline 会话测试；P-f 的界面测试 |
| iPhone Safari 与桌面浏览器能播、锁屏连续 | P-a 真机验证；E 的 iOS 真机手工核对（zones 清单 E 第 2 条） |
| 定位、改处理设置后 1 秒内听到新结果 | E（真实服务，量从命令到新流 `playing` 的时间） |
| 派生缓存退役 | dsp-pipeline P-d 测试；E 的升级核对（zones 清单 E 第 3 条） |
| 现有能力不退化（各种格式、电平化、交叉渐变、歌词、锁屏、计次） | E（zones 清单 E 第 1 条的“不退化”） |

## 3. 合入与回滚

- 第一批随 `playback-baseline` 一次合入 main（zones 清单“合入与收尾”）；本线的迁移（Headroom 列）与 zones 的迁移一起按 main 当时的最大编号顺延。
- 回滚点就是那个合入提交（zones design.md §8）。本线没有单独的回滚路径：渲染会话是唯一的交付方式。
- spec（E 阶段，与 zones 一起）：新增 `.trellis/spec/backend/render-contracts.md`（渲染会话的端口实现约束、ffmpeg 命令与超时、协商规则、事件分类、信号路径结构、iOS 结论；≤ 24 KB），改写 `playback-derivation-contracts.md` 的派生部分（删除或并入），`audio-analysis-contracts.md` 加一句“电平化增益的选择在 `dsp/leveling`”。

## 4. 第二、三批的启动条件

- 第一批合入 main 之后再开第二批，每项一条独立分支、独立合入 main（不再用集成分支）。
- 建议顺序：采样率转换先做（替换第一批的 ffmpeg 兼容性转换，卷积与 DSD 都依赖它），降混、PEQ 与它并行；卷积在采样率转换之后；DSD 在采样率转换与扫描对标 ⑩ 之后；第三批 PCM→DSD 在 DSD 之后。
- 每项沿用父 design.md §3.1 的处理器形状与 §9 的对照验证做法；处理设置的迁移各自加在 `playback_zones` 上。
