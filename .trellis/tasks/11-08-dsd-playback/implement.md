# 实施计划：DSD 入库与播放

设计：[design.md](./design.md)；共用接口：[骨架](../11-09-dsp-batch2-skeleton/design.md)；跨项合入门禁：[父 implement §5](../11-08-audio-dsp-reference-port/implement.md)。本文是待审规划，不等于授权启动实现。

后端实现与独立测试均用 `codex/gpt-6-astra`（high），由不同 agent 执行；前端由主会话直接写。工作树建议 `ROOMusic-dsp-dsd`，纯包分支 `dsp/dsd-pure`，接入分支 `dsp/dsd-integration`（SDM只需pure）；测试项目 `roomusic-test-dsd`。不改他人分支/测试库；参考稿只引章节号，本机数据只读。

## 1. 纯包（现在可开工）

1. dsp/dsd/internal/gen 与自主 fir_gen.go；dsp/dsd 的96抽头/查表/静音历史/增益；登记两个包目录。独立测试做 FIR 规格、查表对直接卷积、已知信号。
2. 可先写 dsdio 中性容器读取、parser 的DSF/DFF标签和指纹、小样本测试；新增 ErrUnsupportedEncoding 哨兵明确归 parser。此段代码先在分支验证，不单独放开生产扫描准入。
3. dsdpcm 组合需要 SRC/PEQ 纯包提交，不能用 render 内部 dsdreader 让分析无法复用；模型的生成/转换纯包不等这些接线。

## 2. 接入（骨架合入后，按顺序）

1. 等骨架 → SRC → 降混 → PEQ → 卷积合 main；扫描⑩已在main。无迁移号，不能为来源枚举自行占号。
2. parser 分派/缓存版本/指纹、scan_pipeline 的 DST 分类、普通与 CUE 父音频准入；formats 的 container、sniff、renderFormatTable 普通与 cue_virtual 四行、内置 decoder 门控全部接齐。
3. OpenForRender 重读负载偏移与位序，RenderSource→app→Track 传递DSDLayout；CUE绝对索引直接转有效PCM帧，validateTrack 不再用PCM上限拒绝DSD比特率。
4. DSD填充协程写原byteRing，复用decoder.read电平化；实际进程预留按来源/交付区分（DSD→WAV为0）。有效来源格式比较、输入/输出时钟、signal dsd_to_pcm。
5. CUE相邻轨共享父生产者/滤波历史，段EOF不冲刷；read限在段末，非连续跳转重开并前滚。测试证明拼接等于连续转换，不只是“听起来无缝”。
6. analysis.go 的 stdin 改 io.Reader 加原始PCM输入描述；analysis_worker 对普通DSD与CUE父嗅探分支；DSD流已截取不再 -ss/-t；取消关闭生产者。
7. 主会话加DSD格式与信号显示，测试agent只补design §6 的准入、CUE、流式分析/取消和来源接线；最小真实DSD64/128扫描→分析→播放在本机验证。所有准入/播放/分析同批合入，父 §5完整real/mock门禁，iPhone留发版部署后。

## 3. 最小验证集与命令

只验 design 指定的实际改动，不写假想场景或全量兜底。以下 go 命令从 backend 运行；真实渲染测试先在仓库根 `export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"`。本机参考测试若跳过，须单列未完成对照，不能写“指标通过”。

```bash
go generate ./internal/dsp/dsd # 仅修改生成方法时运行
go test ./internal/dsp/dsd ./internal/library/parser -count=1
# 接入后：
go test ./internal/playback/dsdio ./internal/playback/dsdpcm ./internal/playback/render ./internal/playback/analysis -count=1
go test ./internal/playback -run 'Test.*(DSD|DFF|DSF|CUE|Render)' -count=1
go test ./internal/library -run 'TestScannerOnlyDiagnosesKnownUnsupportedAudioExtensions|Test.*DSD|Test.*DST' -count=1
go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1
```

```bash
(cd frontend && npx vitest run src/features/player/zones/decoders.test.ts src/features/player/components/signal-path.test.tsx)
(cd frontend && npm run typecheck)
```

新增本项表单/管理/上传交互测试由测试 agent 命名并在交接时追加**精确文件路径**的 vitest 命令；上述既有两文件不能代替新增交互验收。前端主会话在本子项合入前构建一次，产物随本项提交以满足 CI 漂移检查，不由各并行后端分支分别重建。

合入前额外执行父 §5 的整组 real e2e 与 mock e2e，禁止只跑单个新用例代替；日常不跑全仓 go test ./...。运行 `git diff --check`。生成器只在方法/系数变动时运行，CI检查自主产物性质，不重算昂贵优化。

## 4. 回滚点

DSD准入、格式表、转换与分析应整组回退，不能留下“看得见放不了”的新准入。已有DSD记录不会因代码回退自动消失，不宣称下一次扫描必然标missing；暂停新增准入并由主会话决定恢复新版本或正常重扫，不删除成员数据。纯dsp/dsd被SDM测试依赖后保留。

## 5. 交接回报

文件与接口变化；真实执行命令/结果/跳过项；最小验收对应证据；迁移升级→回退→再升级结果（有迁移时）；信号路径/界面截图（有界面时）；尚未完成的本机对照与最终iPhone验收。不得把本计划命令列表当作执行记录。各子项实现收尾再更新所属 spec，本次只起草任务文档。

DSD 另回报自主FIR三项规格、真实DSD64/128的扫描/分析/播放结果与倍实时速度；CUE连续状态用测试证据而非听感代替。
