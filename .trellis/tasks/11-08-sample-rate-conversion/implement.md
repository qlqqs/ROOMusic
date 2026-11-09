# 实施计划：采样率转换（SRC）

设计见 [design.md](./design.md)。测试遵循仓库 `AGENTS.md` 的测试范围约束与全局“测试只做必要的”：只测改动直接影响的行为，不跑全量，不加演练和兜底。

## 顺序

| 步 | 内容 | 执行 | 前置 |
| --- | --- | --- | --- |
| 1 | `backend/internal/dsp/internal/fft`：基 2 复数 FFT、实数输入打包、逆变换（不缩放） | 后端 GPT | 无 |
| 2 | `scripts/dspgen/src2s.py`（design §2.3 的线性规划），生成并提交 `backend/internal/dsp/src/kernels2s_gen.go` | 后端 GPT | 无 |
| 3 | `dsp/src` 核：Kaiser 窗 sinc（§2.2）、读入 2s 半核、倒谱最小相位（§2.4）、进程内缓存 | 后端 GPT | 1、2 |
| 4 | 多相重采样器：单级、降采样复用原型、多级串联、`Latency`/`Reset`、`ResampleAll`（§1、§3） | 后端 GPT | 3 |
| 5 | 规划器 `Choose`：五种策略、四步回退、分级表（§4） | 后端 GPT | 4 |
| 6 | CI 测试（§8.1）与本机对照测试（§8.2，`refcompare` 标签） | 测试 GPT | 1–5 |
| 7 | 本机跑一次对照，把每族最差余量贴进回报 | 测试 GPT | 6 |
| 8 | 迁移 `sample_rate jsonb`（编号接在 Headroom 迁移之后，带 `.down.sql`）；`dspsettings` 的编解码；`zones` 的 `set_settings` 键；链 ② 节点；`sample_rate` 信号路径步骤；删除 §6.3 的 ffmpeg 参数 | 后端 GPT | 第一批合入 main |
| 9 | 前端两项设置与 `sample_rate` 步骤显示 | 前端 Opus（新开一个 agent，做完即结束） | 8 的接口 |
| 10 | 验收：主会话审 diff 与测试输出；本机放一首 44.1 kHz 曲目在 Opus 档和一首 192 kHz 曲目在 iOS 上，看信号路径 | 主会话 | 1–9 |

步骤 1–7 是纯包，与第一批并行；卷积与 DSD 子任务在步骤 5 合入后即可开工。

## 派活要点

- 后端 prompt：只实现 design 里的方法与参数，不试别的窗函数或设计法；2s 只能来自入库的生成结果，运行时不求解线性规划；不为假想输入写兜底分支。
- 前端 prompt 写明全局文案约束：不写防御性说明、不写多余说明；两项设置都不配说明文字。
- 测试 prompt：只写 §8.1 列出的测试；本机对照只在本机跑一次，结果写进回报，不提交数据、不进 CI；失败信息不打印参考系数。

## 验证命令

```bash
uv run --with numpy==2.5.4 --with scipy==1.18.1 scripts/dspgen/src2s.py   # 只在改 2s 方法时；输出要与入库文件一致
cd backend
go test ./internal/dsp/internal/fft/ ./internal/dsp/src/
go test -tags refcompare -run TestReferenceKernels ./internal/dsp/src/   # 只在本机，数据目录不存在会跳过
# 步骤 8 之后：只跑受影响包
go test ./internal/dsp/chain/ ./internal/dspsettings/... ./internal/zones/... ./internal/playback/render/...
```

```bash
cd frontend
npx vitest run src/features/player/components/signal-path.test.tsx <设置表单的测试文件>
npm run typecheck
```

## 回滚点

- 步骤 1–7 只新增纯包与脚本，回滚即删除。
- 步骤 8 的迁移带 `.down.sql`；回退时同时恢复解码、编码命令里的 ffmpeg 转换参数（第一批的行为）。
- 步骤 9 只增设置项，可单独回退。

## 回报要求

改动文件列表；执行的测试命令与结果；`kernels2s_gen.go` 注释里 6 组的阻带、通带、−6 dB 点；本机对照每族最差余量（阻带、通带、群延迟）；最大最小相位核（1280/147 precise）的 Go 生成耗时与峰值内存。
