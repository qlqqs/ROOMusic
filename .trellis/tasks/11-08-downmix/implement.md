# 实施计划：多声道降混

设计见 [design.md](./design.md)。测试遵循仓库 `AGENTS.md` 的测试范围约束与全局“测试只做必要的”。

## 顺序

| 步 | 内容 | 执行 | 前置 |
| --- | --- | --- | --- |
| 1 | `backend/internal/dsp/downmix`：掩码与布局名、默认布局、`MaskFromFFmpegLayout`、`Build`、`Target`、处理器 | 后端 GPT | 无 |
| 2 | 后端测试（design §7 的 1–4） | 测试 GPT | 1 |
| 3 | 解码层探测源布局填 `Format.Layout`；迁移 `playback_zones.downmix`（编号接在 Headroom 迁移之后，带 `.down.sql`）；`set_settings` 键；链 ③ 节点；`downmix` 信号路径步骤；解码命令删去 `-ac`（design §5） | 后端 GPT | 第一批合入 main |
| 4 | 前端：“多声道降为双声道”开关；信号路径降混节点 | 前端 Opus（可并入同期其他设置项的前端 agent） | 3 的接口 |
| 5 | 前端测试（design §7 末段两个用例） | 测试 GPT | 4 |
| 6 | 验收：主会话审 diff 与测试输出；用一首 5.1 曲目在浏览器里播放，看信号路径 | 主会话 | 全部 |

## 派活要点

- 后端 prompt：矩阵只用 design §2.1 的构建规则，不为个别布局写特例；不加行和归一；处理器形状按父设计 §3.1。
- 前端 prompt 写进全局文案约束：开关不配说明。
- 测试 prompt：正弦素材在测试里生成，不提交音频文件。

## 验证命令

```bash
cd backend
go test ./internal/dsp/downmix/
# 步骤 3 之后：
go test ./internal/dsp/chain/ ./internal/zones/... ./internal/playback/render/...
```

```bash
cd frontend
npx vitest run src/features/player/components/signal-path.test.tsx <设置表单的测试文件>
npm run typecheck
```

## 回滚点

- 步骤 1 是新包。
- 步骤 3 迁移带 `.down.sql`；回退时恢复解码命令的 `-ac`（第一批行为）。把开关关掉就是参考稿的“取前两声道”行为。

## 回报要求

改动文件列表；测试命令与结果；5.1 曲目播放时的信号路径截图。
