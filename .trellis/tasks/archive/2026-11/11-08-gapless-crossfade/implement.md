# 执行清单：无缝换轨与淡化曲线

设计：[design.md](./design.md)。顺序、分支与跨子项验收见[父任务 implement.md](../../../11-08-audio-dsp-reference-port/implement.md)；渲染会话骨架见 [dsp-pipeline implement.md](../11-08-dsp-pipeline/implement.md) 的 P-c。

```bash
export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"
(cd backend && go test ./internal/dsp/fade/... ./internal/playback/render/... -count=1)
(cd backend && go test ./internal/app -run TestPackageDependencyDirections -count=1)
```

## G-1 纯包 `dsp/fade`（与 dsp-pipeline P-b 同期，不依赖端口）

1. [x] 后端 GPT：`FadeIn`、`FadeOut`、`AlignDown8`、`Mix`（按窗口内起始帧与窗口长度分段混音）、常量 `Warmup` 与注释（design.md §3.3）。不导入任何包。
2. [x] 测试 agent：design.md §6 的“曲线与截断”一条（几个点的数值、对齐、分三次 `Mix` 的抽查）。

回滚点：G-1 提交（可与 P-b 一起合入）。

## G-2 接入渲染会话（dsp-pipeline P-c 合入之后；与 D、P-e、P-d 并行）

1. [ ] 后端 GPT：在 `playback/render` 的 `player.read` 挂点上实现：
   - 取下一首时的判定（design.md §1：格式边界、淡化、无缝、间隔），格式边界与 `not_ready` 的立即事件，准备失败的 `track_failed` 与重取（连续 5 首停止）；
   - 短读换轨与递归补读、等待下一首最多 5 秒（§2）；
   - 交叉淡化的条件、截断点、窗口混音、`track_started` 与分段（§3）；
   - 关闭无缝时 0.5 秒静音与 `gap`（§4）；
   - `NextChanged` 的四种状态（§5）。
   去掉 P-c 里的最小换轨实现。
2. [ ] 测试 agent：design.md §6 的其余各条（逐位拼接 MD5、格式边界、淡化条件表、超时、`NextChanged` 两条、`gap`）。

验证：上面的命令（只本包）。风险文件：`playback/render/player.go`、`session.go`。回滚点：G-2 提交。

## 完成标准

- G-1、G-2 测试通过，经主会话验收合入 `playback-baseline`。
- prd 验收 1–4 由 G-2 的测试覆盖；“现有交叉渐变不能在新播放基线里消失”由 E 的“不退化”核对一次（真实服务、两张不同专辑的曲目、开 5 秒交叉渐变）。
