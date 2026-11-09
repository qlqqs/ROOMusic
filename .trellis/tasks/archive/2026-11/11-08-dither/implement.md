# 执行清单：位深转换与 TPDF 抖动

设计：[design.md](./design.md)。顺序、分支与跨子项验收见[父任务 implement.md](../../../11-08-audio-dsp-reference-port/implement.md)；渲染会话骨架见 [dsp-pipeline implement.md](../11-08-dsp-pipeline/implement.md) 的 P-c。

```bash
export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"
(cd backend && go test ./internal/dsp/dither/... -count=1)
(cd backend && go test ./internal/playback/render/... -run 'Dither|Leveling' -count=1)   # D-2 的会话用例
(cd backend && go test ./internal/app -run TestPackageDependencyDirections -count=1)
```

## D-1 纯包 `dsp/dither`（与 dsp-pipeline P-b 同期，不依赖端口）

1. [x] 后端 GPT：`LFSR`（x³¹ + x²⁸ + 1，每次取值推进 31 步）、`Uniform`、`Triangular`、`Quantizer`（`pack16`/`pack24`、直通、TPDF 取整、钳位与削波数）、`Reset`（design.md §2、§5）。不导入任何包；稳态不分配。
2. [x] 测试 agent：design.md §7 的单元测试五条（周期、分布、直通、抖动线性化、钳位与削波）。

回滚点：D-1 提交（可与 P-b 一起合入）。

## D-2 接入渲染会话（dsp-pipeline P-c 合入之后；与 G-2、P-e、P-d 并行）

1. [ ] 后端 GPT：链出口按块算“是否直通”（`Exact && 0 < SourceBits ≤ bits`）并调用 `Quantize`，字节交给 FLAC 编码器或 WAV 写出器；削波数交给 P-e 的削波统计（P-e 未合入时先只累计）；
       信号路径在“这一首会被抖动”时加 `dither` 步骤（design.md §6）。删除 P-c 里的临时取整。
2. [ ] 测试 agent：design.md §7 末尾的会话用例一条（电平化开时输出带抖动、逐样本相差不超过 1 LSB）。

验证：上面的命令（只相关用例）。风险文件：`dsp/chain` 的出口、`playback/render/session.go`。回滚点：D-2 提交。

## 完成标准

- D-1、D-2 测试通过，经主会话验收合入 `playback-baseline`。
- prd 验收：周期与分布由 D-1 覆盖；“没开处理时逐位相同”由 dsp-pipeline 的 MD5 用例覆盖；“开了电平化后带抖动”与“钳位并计入削波”由 D-1、D-2 覆盖。
