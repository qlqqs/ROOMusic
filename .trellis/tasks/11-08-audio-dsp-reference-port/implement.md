# 执行清单：音频算法对标（父任务）

设计：[design.md](./design.md)（跨子项架构）。子项的执行清单：[dsp-pipeline](../archive/2026-11/11-08-dsp-pipeline/implement.md)、[gapless-crossfade](../archive/2026-11/11-08-gapless-crossfade/implement.md)、[dither](../archive/2026-11/11-08-dither/implement.md)；第二、三批的子项另行设计。
播放区线的执行清单：[zones implement.md](../archive/2026-11/11-08-server-playback-zones/implement.md)（下称“zones 清单”），本文的时间点都对齐它的 B1/B2/B3/F2/F3/E。

## 0. 开工前

- [x] 主会话裁决 design.md §8 的端口补充（7 条），与 zones B2 的端口一起定稿；采纳的写进 zones 的 design.md §14 与合同（由主会话改，本线不改 zones 的文件）。
- [x] 用户裁决 design.md §12（CUE 虚拟轨是否第一批顺带），结果写进本 prd“已裁决”。
- [ ] iOS 交付小验证先做（dsp-pipeline 清单 P-a），结果写回 dsp-pipeline 的 prd 与 design-delivery.md §3；若命中“交主会话裁决”的几行，先裁决再开 render 骨架。
- [ ] 分支：第一批全部合入 zones 的集成分支 `playback-baseline`。本线用一个独立 worktree（`ROOMusic-dsp`），每个子步骤一个分支，从 `playback-baseline` 切出、做完合回；纯包在 `playback-baseline` 建立之前可以先从 `main` 切出，合入时再并到 `playback-baseline`。
- [ ] 测试库：渲染会话的测试不需要数据库；只有派生退役（P10）的启动核对可能用到隔离库，用独占项目名 `roomusic-test-dsp`（与 zones 的 `roomusic-test-zones` 分开）。
- [ ] 分工（2026-11-09 用户改定）：后端实现 `codex/gpt-6-astra`（high）；前端实现 Opus（high）；测试由独立的 `codex/gpt-6-astra`（high）测试 agent 编写与执行，实现 agent 不写新测试。
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

## 5. 第二、三批执行安排（2026-11-09）

本节是第一批 `main e29b65d / v0.5.0` 之后的执行依据，取代 §4 及父 design 中“接入可任意并行”的旧安排；历史第一批清单不回写。用户裁决：纯包并行，**骨架先合入，再按 SRC → 降混 → PEQ → 卷积 → DSD 串行接入**；PCM→DSD 只做库，往返依赖 DSD。接口以[骨架 design](../archive/2026-11/11-09-dsp-batch2-skeleton/design.md)及修订后的子设计为准。

### 5.1 子任务、工作树与资源

| 子项 | 可并行的纯包/基础 | 接入前置 | 后端实现 / 独立测试 | 工作树；分支建议 | 测试项目 | 迁移 |
| --- | --- | --- | --- | --- | --- | --- |
| [公共骨架](../archive/2026-11/11-09-dsp-batch2-skeleton/implement.md) | refdata 基础小提交 | 第一批 main | gpt-6-astra / gpt-6-astra | ROOMusic-dsp-skeleton；dsp/batch2-skeleton | roomusic-test-dsp-skeleton | 无 |
| [SRC](../archive/2026-11/11-08-sample-rate-conversion/implement.md) | fft、src、2s 生成器 | 骨架 | gpt-6-astra / gpt-6-astra | ROOMusic-dsp-src；dsp/src-pure → dsp/src-integration | roomusic-test-src | 0045 |
| [降混](../archive/2026-11/11-08-downmix/implement.md) | downmix 矩阵/处理器 | SRC 接入合 main | gpt-6.1-sol / gpt-6.1-sol | ROOMusic-dsp-downmix；dsp/downmix-pure → dsp/downmix-integration | roomusic-test-downmix | 0046 |
| [PEQ](../archive/2026-11/11-08-peq/implement.md) | peq；OPRA/导入纯解析准备 | 降混接入合 main | gpt-6-astra / gpt-6-astra | ROOMusic-dsp-peq；dsp/peq-pure → dsp/peq-integration | roomusic-test-peq | 0047（含预设表） |
| [卷积](../archive/2026-11/11-08-convolution/implement.md) | convolution（等 fft）、cfg；IR 重采样等 src | PEQ 接入合 main | gpt-6-astra / gpt-6-astra | ROOMusic-dsp-convolution；dsp/convolution-pure → dsp/convolution-integration | roomusic-test-convolution | 0048 IR 表、0049 设置 |
| [DSD](../archive/2026-11/11-08-dsd-playback/implement.md) | dsd/生成器；中性容器解析；组合等 src/peq | 卷积接入合 main；扫描⑩已满足 | gpt-6-astra / gpt-6-astra | ROOMusic-dsp-dsd；dsp/dsd-pure → dsp/dsd-integration | roomusic-test-dsd | 无 |
| [PCM→DSD](../archive/2026-11/11-08-pcm-to-dsd/implement.md) | sdm/生成器；往返等 dsd | 无产品接入；最终合入在 DSD 后 | gpt-6.1-sol / gpt-6.1-sol | ROOMusic-dsp-sdm；dsp/sdm-pure | roomusic-test-sdm（纯包无需DB） | 无 |

模型 provider 都是 `codex`、reasoning 都是 high；实现与测试是独立 agent。**所有前端由主会话写**，不沿用旧稿 Opus 前端派工。此处是分支/项目命名建议，不表示本起草会话已创建工作树或启动 agent。

迁移名及内容见骨架 §8：0045_sample_rate、0046_downmix、0047_peq（表+列）、0048_dsp_ir_files、0049_convolution，实际完整文件名以该表为准。未合入分支不得自行重占编号；如 main 被其他任务占号，由主会话统一顺延未应用编号。

### 5.2 依赖图与合入方式

```text
现在可并行准备（互不依赖第一批接入）
  骨架基础 refdata ───────────────→ 各纯包本机对照测试
  SRC：fft → src ─────────┬──────→ 卷积纯引擎/IR重采样
  降混：downmix           │
  PEQ：peq ───────────────┼──────→ DSD 来源组合
  DSD：dsd ───────────────┼──────→ SDM 往返测试
  SDM：sdm/生成器         │
                         ↓（审阅共享纯包小提交，禁止各线重复实现）
main：第一批 → 公共骨架 → SRC → 降混 → PEQ → 卷积 → DSD → PCM→DSD库
           每个接入点：最小测试 + 主会话前端 + 独立检查 + 两组完整e2e → 合main
                                                                       ↓
                                                          v0.6.0 → 八号机 → iPhone
```

- “纯包现在可开工”不等于所有测试毫无依赖；卷积等 FFT、DSD 组合等 SRC/PEQ、SDM 往返等 Converter。纯包主功能各线先做，依赖提交审阅后定向取用，记提交号，后续合入避免重复 cherry-pick。
- 后端工作树只改自己负责的纯包；共享 `render`、zones、migration、app 规则与 API 样本在接入轮次由当前子项拥有。纯包必要的 packageRules 增量由主会话整合，不能每线重写整张 map。
- 每项接入前同步最新 main，不能拿第一批的 decoder.go 复盖已经接好的 SRC/降混。没有新的长期 integration 总分支；各项各自验收、各自合 main。
- API 样本按骨架 §7 的 recorder 所有权最少刷新，不能全目录机械替换。`backend/cmd/roomusic/web` **每个含前端变化的子项合 main 前由主会话最终构建一次**；所谓“最后一次重建”是该子项收口时，不是拖到整个 v0.6.0 才做，否则中途 main 的 CI 漂移检查会失败。
- 独立测试项目避免相互误删；现有 test-integration.sh 与 playback-e2e.sh 仍共用全局 flock，本机两类重测试串行调度，不删除他人的锁、不绕过它。纯 Go 算法测试可并行。

### 5.3 每项合入 main 前检查

1. 只改当前子项范围；主会话核对 K1–K7 接口、实际输入/输出格式、开启/关闭信号、Exact、输入提交污染、资源归属及取消（按本项涉及的检查，不强迫纯 SDM做无关存储测试）。
2. 独立测试 agent 执行子 implement 的最小集；迁移用隔离库升级→回退→再升级；有对照数据的本机测试交指标与余量，缺数据跳过明确记录，不冒充达标。源码/文档/镜像无参考原文与参考系数。
3. 前端主会话完成 model/decoder/controller/fake 与界面；仅受影响 Vitest、typecheck，构建产物同步。新信号 kind 能实际显示；PEQ 拖动不持续重建流。
4. **吸取第一批教训：每项合入前，在整合完的分支按 CI 入口整组跑一次 real e2e 与 mock e2e，不能用新用例或 playback-zones 单项代替。** 此项来自第一批合入的教训（子任务各自的最小测试集漏掉了跨文件干扰和过时断言）；其余日常仍最小测试，不为它另造全套场景。

从仓库根执行（project 替换为本项表中的名称；以下以 SRC 示例）：

```bash
test -f /home/qlqq/.local/share/playwright-deps/env.sh && source /home/qlqq/.local/share/playwright-deps/env.sh
(cd frontend && npm ci && npm run typecheck && npm run build)
# 将本次应有的构建产物纳入待合入变更，再按 CI 检查漂移；不得忽略未跟踪产物。
# 干净的待合入提交上重建后执行：
git diff --exit-code -- backend/cmd/roomusic/web
test -z "$(git ls-files --others --exclude-standard -- backend/cmd/roomusic/web)"
(cd frontend && npm run test:e2e)  # mock 整组；不用 grep/单文件过滤
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-src ./scripts/playback-e2e.sh  # real 整组；无路径参数
git diff --check
```

浏览器使用本机已配置的 Chromium 依赖；环境缺失先按本机 AGENTS 排查，不能宣称已通过。失败保留实际报告路径/日志，定位到受影响行为后最小复跑，修复完再完整跑被失败影响的该组。PR 正常 CI 按 `.github/workflows/ci.yml` 执行自身门禁，不将 CI 全仓 go test 命令复制成每一步本地任务。

5. 主会话审阅、按子项更新 spec 并提交/合入（此轮设计 agent 不做）；记录 merge SHA、最小集和两组 e2e 的实际结果。关所有新处理仍通过第一批逐位测试；每次 main 处于可运行状态。

### 5.4 v0.6.0、部署与最后真机验收

- [ ] 骨架及五个接入子项、PCM→DSD 纯库均验收合 main；不提前启用 DAC/DoP/PCM→DSD 输出。汇总版本说明、OPRA 来源/许可与数据版本、自生成系数指标。
- [ ] 按现有发版流程与 `deploy/production/README.md` 做 v0.6.0 发布检查和镜像构建；确认迁移号连续、内嵌前端无漂移、发布 CI（包括完整 real/mock）通过。此计划不包含自动推 tag/生产操作授权。
- [ ] 部署八号机前备份数据库（含 IR bytea、PEQ预设及设备选择），按生产部署文档升级、记录原镜像/新镜像摘要、schema 与健康结果；只在用户授权的部署窗口操作。失败按已留存镜像与迁移回退/备份流程处置，不能把原IR当缓存删除。
- [ ] 部署后先做服务健康与桌面最小播放检查，再进行**最后一轮 iPhone 真机验收**：无损/有损、SRC/降混/PEQ/卷积的信号与设置、DSD64/128及CUE相邻分轨、定位/歌词/锁屏连续/计次；借真实使用路径合并核对，不给每个算法重复整套手机测试。
- [ ] iPhone 结果由主会话/用户记录；本机Chromium不替代真机。若未完成，父任务不标全部验收完成。v0.6.0 同批部署及最终真机顺序按用户2026-11-09裁决执行。

## 进度核对（2026-11-14）

本节是当前状态，优先于上方未逐项回填的历史执行清单。

- [x] 公共骨架、SRC、降混、PEQ、卷积、DSD 播放及 PCM→DSD 纯库已合入 main，7 个子任务本次归档；连同第一批，10 个算法子任务均已交付归档。
- [x] v0.6.0 已于 2026-11-09 发布，提交 `c3a27b79`；[CI](https://github.com/qlqqs/ROOMusic/actions/runs/42693220952) 与 [Release](https://github.com/qlqqs/ROOMusic/actions/runs/42695792805) 成功。
- [ ] 核实 v0.6.0 八号机部署，补录备份、镜像、schema 与健康结果。现有日志仅明确记录 v0.5.0 部署，不据此推断当前生产版本。
- [ ] 补齐最终 iPhone 真机验收结果（范围见 §5.4）；未找到完成证据，不标为通过。

父任务保持 `in_progress`；生产与真机验收收齐后再归档。旧逐首播放接口仍保留，不作为本轮已完成事项。
