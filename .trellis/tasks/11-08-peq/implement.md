# 实施计划：参数均衡与耳机校正

设计：[design.md](./design.md)；共用接口：[骨架](../11-09-dsp-batch2-skeleton/design.md)；跨项合入门禁：[父 implement §5](../11-08-audio-dsp-reference-port/implement.md)。本文是待审规划，不等于授权启动实现。

后端实现与独立测试均用 `codex/gpt-6-astra`（high），由不同 agent 执行；前端由主会话直接写。工作树建议 `ROOMusic-dsp-peq`，纯包分支 `dsp/peq-pure`，接入分支 `dsp/peq-integration`（SDM只需pure）；测试项目 `roomusic-test-peq`。不改他人分支/测试库；参考稿只引章节号，本机数据只读。

## 1. 纯包（现在可开工）

1. dsp/peq：design §1–§3 的系数、固定 Q 级联、多通道 IIR、ButterworthLowPass；UI 只允1–6阶，供DSD调用的纯函数另支持8阶。
2. 独立测试覆盖每类滤波器必要频点、冲激、声道选择、分块等价；架类转折点是 G/2，不沿用旧稿的 G 断言。
3. OPRA 裁剪/导入纯解析可并行准备，但不先接 zones/HTTP/迁移。离线工具 `backend/cmd/opra-update` 只写 backend 数据，嵌入 SOURCE.md/LICENSE；标识由主会话放 frontend。

## 2. 接入（骨架合入后，按顺序）

1. 等骨架 → SRC → 降混合 main；0047 同时加 peq 文档和成员预设表。zones 拥有文档编解码，dspsettings 拥有资源，禁止 zones→dspsettings。
2. 资源 API、OPRA/导入/曲线、预设鉴权与 app 装配；新目录逐个登记规则。设置错误统一 invalid_input，无字段路径；导入专用错误在自己的 httpapi 映射。
3. render 按③后的格式构建 IIR，放 Spec.User；启用恒等设置也算处理。此时无卷积，后续由 convolution.before_peq 排序。
4. 主会话写编辑/预览/OPRA/导入/预设界面；150ms 只去抖预览，松手/确认才 set_settings，一次动作一次提交。OPRA 必需署名保留，其他文案不加防御说明。
5. 独立测试补小 OPRA 映射、真实嵌入可加载、导入、成员预设隔离、松手提交及信号显示。更新 recorder/fake；按 2026-11-09 最新裁定，只执行本轮限定的局部验证，全套 e2e 留到发版前一次。

## 3. 最小验证集与命令

只验 design 指定的实际改动，不写假想场景或全量兜底。以下 go 命令从 backend 运行；真实渲染测试先在仓库根 `export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"`。本机参考测试若跳过，须单列未完成对照，不能写“指标通过”。

```bash
go test ./internal/dsp/peq -count=1
# 接入后：
go test ./internal/dspsettings/... ./internal/zones/... ./internal/playback/render -count=1
go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1
```

在独立项目 `roomusic-test-peq` 的临时数据库中只执行以下定向命令（沿用集成脚本的全局锁、随机端口和退出清理，不运行其全包扫描）：

```bash
go test ./internal/platform/postgres -run '^TestPostgreSQLMigrationRollbackRoundTrip$/^0047_playback_zone_peq.sql$' -count=1 -v
ROOMUSIC_UPDATE_API_SAMPLES=1 go test ./internal/app -run '^TestPostgreSQL(Zones|DSP)APIResponseSamples$' -count=1 -v
```

迁移号 **0047**，带 down；新行为测试由独立测试 agent 维护；本轮只增加用户明确要求的 recorder 采集入口，不另写单元/集成行为用例。实际执行时列出匹配到的用例，不把零匹配/跳过当通过。独立 project 仍受脚本全局锁约束，本机数据库和 real e2e 串行。

```bash
(cd frontend && npx vitest run src/features/player/zones/decoders.test.ts src/features/player/components/signal-path.test.tsx)
(cd frontend && npm run typecheck)
```

新增本项表单/管理/上传交互测试由测试 agent 命名并在交接时追加**精确文件路径**的 vitest 命令；上述既有两文件不能代替新增交互验收。前端主会话在本子项合入前构建一次，产物随本项提交以满足 CI 漂移检查，不由各并行后端分支分别重建。

2026-11-09 最新用户裁定覆盖父 §5 的旧合入门禁：全套 real/mock e2e 仅发版前跑一次，本次不执行；日常不跑全仓 go test ./...。运行 `git diff --check`。生成器只在方法/系数变动时运行，CI检查自主产物性质，不重算昂贵优化。

## 4. 回滚点

默认关闭可停用节点但不删除资源。回退需恢复设置/HTTP/UI并降0047（先备份，预设会被删）；OPRA 数据回退到上一个自主裁剪提交。已被DSD调用的 ButterworthLowPass 不得单独删除。

## 5. 交接回报

文件与接口变化；真实执行命令/结果/跳过项；最小验收对应证据；迁移升级→回退→再升级结果（有迁移时）；信号路径/界面截图（有界面时）；尚未完成的本机对照与最终iPhone验收。不得把本计划命令列表当作执行记录。各子项实现收尾再更新所属 spec，本次只起草任务文档。

PEQ 另回报OPRA上游与裁剪文件摘要、压缩大小/条目数、嵌入许可证和首次加载常驻量；导入/套用截图含作者链接。
