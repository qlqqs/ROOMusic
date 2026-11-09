# 实施计划：卷积校正

设计见 [design.md](./design.md)。测试遵循仓库 `AGENTS.md` 的测试范围约束与全局“测试只做必要的”。

## 顺序

| 步 | 内容 | 执行 | 前置 |
| --- | --- | --- | --- |
| 1 | `backend/internal/dsp/convolution`：几何、分段 OLA、矩阵路由、延时线、`Latency` | 后端 GPT | SRC 的 `dsp/internal/fft` |
| 2 | `dspsettings/irload`：顶层格式、zip、`.pcm`/`.dbl`、cfg 记号流解析、多 cfg 选择、ffmpeg 解码读 WAV 头 | 后端 GPT | 无 |
| 3 | 声道匹配（§3）、延时换算、重采样与代数和归一（§4）、固有延迟；进程内缓存 4 份 | 后端 GPT | 1、2，SRC 的 `ResampleAll` |
| 4 | 迁移：`dsp_ir_files` 表，`playback_zones` 三列与复合外键（编号接在 Headroom 迁移之后，带 `.down.sql`）；仓储与 §6 接口；上传时完整解析一次 | 后端 GPT | 3 |
| 5 | 接入：`set_settings` 的 `convolution` 键与 IR 归属核对；⑤ 卷积节点（`chain.Wrap` 补偿延迟）与 `before_peq` 顺序；信号路径 `convolution` 步骤 | 后端 GPT | 第一批合入 main |
| 6 | 后端测试（design §9 的 1–7） | 测试 GPT | 1–5 |
| 7 | 前端：卷积开关与 IR 下拉、⑤ 两项的顺序交换、IR 管理面板（上传、改名、删除）、信号路径卷积步骤 | 前端 Opus（若与 PEQ 前端同时开工，合给同一个 agent 一次做完） | 4、5 的接口 |
| 8 | 前端测试（design §9 末段两个用例） | 测试 GPT | 7 |
| 9 | 验收：主会话审 diff、测试输出与界面截图 | 主会话 | 全部 |

## 派活要点

- 后端 prompt：cfg 文法、“声道.权重”的含义、多 cfg 的选择顺序都按 design §2.2；不支持文件列表模式；不把 zip 解压到磁盘。V7：`Latency()` 报峰值位置，补偿交给 `chain.Wrap`，卷积器不截断 IR、不自己丢样本。
- 前端 prompt 写进全局文案约束；上传失败只显示一句原因。
- 测试 prompt：测试素材（小 zip、cfg、IR）在测试里生成，不提交大文件；只写 design §9 的用例。

## 验证命令

```bash
cd backend
go test ./internal/dsp/convolution/ ./internal/dspsettings/...
# 步骤 5 之后再加：
go test ./internal/dsp/chain/ ./internal/zones/... ./internal/playback/render/...
```

```bash
cd frontend
npx vitest run <卷积设置与 IR 管理面板的测试文件>
npm run typecheck
```

## 回滚点

- 步骤 1–3 是新包，删除即回滚。
- 步骤 4 迁移带 `.down.sql`（删外键、三列与表）。
- 步骤 5 的节点只在开关打开且选了 IR 时插入，默认关闭。

## 回报要求

改动文件列表；测试命令与结果；手头有真实的房间校正 zip（含 cfg）时，在本机装载的结果（路径数、抽头、重采样前后代数和、申报的延迟）。
