# 实施计划：PCM→DSD 调制

设计：[design.md](./design.md)；共用接口：[骨架](../11-09-dsp-batch2-skeleton/design.md)；跨项合入门禁：[父 implement §5](../11-08-audio-dsp-reference-port/implement.md)。本文是待审规划，不等于授权启动实现。

后端实现与独立测试均用 `codex/gpt-6.1-sol`（high），由不同 agent 执行；前端由主会话直接写。工作树建议 `ROOMusic-dsp-sdm`，纯包分支 `dsp/sdm-pure`，接入分支 `dsp/sdm-integration`（SDM只需pure）；测试项目 `roomusic-test-sdm`。不改他人分支/测试库；参考稿只引章节号，本机数据只读。

## 1. 纯包（现在可开工）

1. 生成器 dsp/sdm/internal/gen：按 design §2 的自主方法、16组系数与数值核验，输出 coeffs_gen.go；两级目录同时登记规则。
2. dsp/sdm：结构、0.5缩放/钳位、MSB先、交错输出、完整性0x69与失稳复位。核心实现现在可开工，不等骨架或DSD渲染接入。
3. 独立测试先做频谱/稳定性/校验/系数性质；本机对照通过refdata读取，缺失明确跳过，结果只写指标与余量，不写参考系数。

## 2. 接入（骨架合入后，按顺序）

1. 本项**没有产品接入**：不改 render/zones/HTTP/UI、不启用SRC dsd策略、不加迁移。此段只管依赖汇合与合入，不能误解为必须往共享链插⑦。
2. 往返验证等待DSD纯包（可先取小提交，不必等DSD播放器）；将SDM交错 []byte 拆成每声道 [][]byte 后交Converter，补偿/丢弃其启动瞬态再测频谱。
3. 正式合入排序在DSD之后：基于最新main复核规则/生成物/测试，父 §5合入前real/mock整组门禁由主会话执行一次，不为纯库额外编写新e2e。完成后随其他子项进入v0.6.0，不另做输出开关。

## 3. 最小验证集与命令

只验 design 指定的实际改动，不写假想场景或全量兜底。以下 go 命令从 backend 运行；真实渲染测试先在仓库根 `export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"`。本机参考测试若跳过，须单列未完成对照，不能写“指标通过”。

```bash
go generate ./internal/dsp/sdm # 仅修改生成方法时运行
go test ./internal/dsp/sdm -count=1
go test -tags refcompare -run TestReferenceSDM ./internal/dsp/sdm -count=1
go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1
```

合入前额外执行父 §5 的整组 real e2e 与 mock e2e，禁止只跑单个新用例代替；日常不跑全仓 go test ./...。运行 `git diff --check`。生成器只在方法/系数变动时运行，CI检查自主产物性质，不重算昂贵优化。

## 4. 回滚点

没有生产调用方，整包与生成器可独立回退；不牵涉DB或前端。生成物回退须连同方法说明与实测指标，不能仅换系数。

## 5. 交接回报

文件与接口变化；真实执行命令/结果/跳过项；最小验收对应证据；迁移升级→回退→再升级结果（有迁移时）；信号路径/界面截图（有界面时）；尚未完成的本机对照与最终iPhone验收。不得把本计划命令列表当作执行记录。各子项实现收尾再更新所属 spec，本次只起草任务文档。

SDM 另回报16组自主系数的指标、生成耗时及本机噪声/SNR/稳定上限余量；与design §3.6原型带内噪声差超过0.3 dB时说明原因。
