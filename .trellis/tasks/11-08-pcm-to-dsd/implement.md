# 实施计划：PCM→DSD 调制

设计见 [design.md](./design.md)。测试遵循仓库 `AGENTS.md` 的测试范围约束与全局“测试只做必要的”。首版只做后端库与测试，不接输出（prd M5）。

## 顺序

| 步 | 内容 | 执行 | 前置 |
| --- | --- | --- | --- |
| 1 | 生成器 `backend/internal/dsp/sdm/internal/gen`：Legendre 根与 g、Butterworth 初值与 H∞ 二分、极点参数化的 Nelder–Mead（§2.3）、约束网格与稳定性选择（§2.4）、a 的 QR 最小二乘与线性核对（§2.5） | 后端 GPT | 无 |
| 2 | 跑生成器，提交 `coeffs_gen.go`（16 组，带 §2.6 的注释）；生成结果与 design §3.6 的原型指标逐组比对，带内噪声相差超过 0.3 dB 的要说明原因 | 后端 GPT | 1 |
| 3 | `sdm` 包：结构、钳位、量化、MSB 先打包、交错输出、校验字与 `0x69`、失稳复位（§1、§3） | 后端 GPT | 2 |
| 4 | CI 测试（§6.1 的 1–5） | 测试 GPT | 3，DSD 子任务的 `dsd.Converter` |
| 5 | 本机对照（§6.2），每组余量写进回报 | 测试 GPT | 3 |
| 6 | 验收：主会话审 diff、生成注释里的指标与对照汇总 | 主会话 | 全部 |

步骤 1–3 不依赖其他子任务；步骤 4 的往返测试要等 `dsd.Converter` 合入。

## 派活要点

- 后端 prompt：生成方法只用 design §2 列出的；生成器只用标准库；不在运行时生成系数；7 阶按 3 个谐振器实现（§5.2）。附录 G 与参考稿正文的系数不进代码、注释或测试数据。
- 测试 prompt：只写 §6 的用例；本机对照用 `refdata.Dir(t)`，以附录各段标题为准，不用正文 §6.7 的两组；失败信息不打印参考系数。

## 验证命令

```bash
cd backend
go generate ./internal/dsp/sdm/        # 只在改生成方法时跑；结果要与入库文件一致
go test ./internal/dsp/sdm/
go test -tags refcompare -run TestReferenceSDM ./internal/dsp/sdm/   # 只在本机
```

## 回滚点

新包，没有调用方；删除即回滚。

## 回报要求

改动文件列表；`coeffs_gen.go` 里 16 组的指标（标准阶选中的 l 与 Clans 胜出的起点，H∞、‖h‖₁、带内 NTF、稳定上限）与生成耗时；测试命令与结果；本机对照每组的余量（带内噪声、SNR、稳定上限）。
