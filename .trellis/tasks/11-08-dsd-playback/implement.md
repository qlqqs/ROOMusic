# 实施计划：DSD 入库与播放

设计见 [design.md](./design.md)。测试遵循仓库 `AGENTS.md` 的测试范围约束与全局“测试只做必要的”。

## 顺序

| 步 | 内容 | 执行 | 前置 |
| --- | --- | --- | --- |
| 1 | `parser/dsf.go`、`parser/dff.go`：§1.2 与 §1.3 的读取、DSF 四种补偿、`ErrUnsupportedEncoding`；两种指纹；扫描支持表与诊断分支 | 后端 GPT | 无（⑩ 已在 main） |
| 2 | 生成器 `dsp/dsd/internal/gen`（§3.2 的加权最小二乘，标准库 QR），生成并提交 `fir_gen.go`；`dsp/dsd`：§3.3 的查表实现、`Reset` 填 `0x69` | 后端 GPT | 无 |
| 3 | 后端测试（design §6 的 1–5、7），与 1、2 同批 | 测试 GPT | 1、2 |
| 4 | `playback/render` 内的 `dsdreader`（按字节读、DSF 位反转、定位）与 `DSDLayout`；`TrackSource` 填写布局 | 后端 GPT | 第一批合入 main |
| 5 | DSD 来源段：转换 → 后置低通（`peq.ButterworthLowPass`）→ SRC 降到 352.8 kHz；来源段适配器（增益区域按绝对帧号搬运、延迟补偿、`0x69` 冲刷）；`dsd_to_pcm` 信号路径步骤；无缝判断用来源段输出格式 | 后端 GPT | 4，SRC 纯包，PEQ 纯包 |
| 6 | 后台响度分析的 DSD 管道输入（design §5） | 后端 GPT | 2、4 |
| 7 | design §6 第 6 项（来源段适配器） | 测试 GPT | 5 |
| 8 | 前端：DSD 采样率与编码显示、`dsd_to_pcm` 步骤 | 前端 Opus（可并入同期其他前端 agent） | 1、5 |
| 9 | 前端测试（一个用例） | 测试 GPT | 8 |
| 10 | 验收：主会话审 diff 与测试输出；本机用真实 DSD64 与 DSD128 曲目各一首扫描、分析、在浏览器播放，看信号路径 | 主会话 | 全部 |

**上线约束**：步骤 1 可以先写、先测，但要与步骤 5 一起合入 main 上线，避免曲目能入库却放不了。

## 派活要点

- 后端 prompt：DSF 只做 design 列出的四种补偿，不加其他猜测性修复；FIR 只用 design §3.2 的频带与权重，系数来自入库的生成结果；后置低通不做成用户设置；区域搬运与延迟补偿按父设计 §3.2、§4 的规则，不另造一套。
- 前端 prompt 写进全局文案约束；只改显示，不加说明文字。
- 测试 prompt：样本在测试里生成（几 KB），不提交真实 DSD 文件；只写 design §6 的用例；dsd2pcm 的系数不进测试。

## 验证命令

```bash
cd backend
go generate ./internal/dsp/dsd/        # 只在改 FIR 方法时；输出要与入库文件一致
go test ./internal/library/parser/ ./internal/dsp/dsd/
go test ./internal/library/ -run 'TestScannerOnlyDiagnosesKnownUnsupportedAudioExtensions'   # 扩展这个用例的表
# 步骤 5、6 之后：
go test ./internal/playback/render/... ./internal/playback/analysis/
```

```bash
cd frontend
npx vitest run <曲目格式显示的测试文件> src/features/player/components/signal-path.test.tsx
npm run typecheck
```

## 回滚点

- 步骤 1 回退即恢复“DSF/DFF 记为候选格式、不入库”的现状；已入库的 DSD 曲目随下一次扫描标为缺失（与删除扩展名同理）。
- 步骤 2 是新包。
- 步骤 4–6 只影响 DSD 源，其他格式路径不变。

## 回报要求

改动文件列表；测试命令与结果；`fir_gen.go` 注释里三项规格的实测值；真实曲目的扫描、分析（响度与峰值）与播放结果，信号路径截图；DSD64 立体声来源段的处理速度（倍实时）。
