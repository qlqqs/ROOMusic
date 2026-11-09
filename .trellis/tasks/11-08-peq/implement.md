# 实施计划：参数均衡与耳机校正

设计见 [design.md](./design.md)。测试遵循仓库 `AGENTS.md` 的测试范围约束与全局“测试只做必要的”。

## 顺序

| 步 | 内容 | 执行 | 前置 |
| --- | --- | --- | --- |
| 1 | `backend/internal/dsp/peq`：§1 系数设计、§2 固定 Q 级联、§3 多通道 IIR、`ButterworthLowPass` | 后端 GPT | 无 |
| 2 | `dspsettings`：PEQ 文档类型与校验、换算成 `[]Section`、频响曲线计算 | 后端 GPT | 1 |
| 3 | OPRA：`opra-update` 命令（下载、裁剪、压缩、`SOURCE.md`、标识图片），提交一次生成结果；内存加载与搜索；映射 | 后端 GPT | 2 |
| 4 | 文本导入解析 | 后端 GPT | 2 |
| 5 | 迁移：`dsp_peq_presets` 表、`playback_zones.peq` 列（编号接在 Headroom 迁移之后，带 `.down.sql`）；预设仓储与接口；§8 全部接口 | 后端 GPT | 2–4 |
| 6 | 接入：`set_settings` 的 `peq` 键（`zones` 调 `dspsettings` 校验）；⑤ PEQ 节点（顺序看 `convolution_before_peq`）；信号路径 `peq` 步骤 | 后端 GPT | 第一批合入 main |
| 7 | 后端测试（design §11 的 1–7） | 测试 GPT | 1–6 |
| 8 | 前端：编辑面板、频响曲线、OPRA 面板与署名、粘贴导入、预设；信号路径 PEQ 节点名 | 前端 Opus（一个 agent 一次做完） | 5、6 的接口 |
| 9 | 前端测试（design §11 末段的三个用例） | 测试 GPT | 8 |
| 10 | 验收：主会话审 diff、测试输出与界面截图（按 `/home/qlqq/workspace/AGENTS.md` 的 Playwright 环境截图） | 主会话 | 全部 |

步骤 1–5 不依赖第一批，可以先做。

## 派活要点

- 后端 prompt：公式只用 design §1、§2 列出的；不加 design 之外的滤波器类型或导入格式；OPRA 更新命令只在本机手动跑一次，生成物提交，CI 不联网。
- 前端 prompt 写进全局文案约束：不写防御性说明、不写多余说明；必要说明一句日常的话。OPRA 署名按 design §5.4，作者比 OPRA 标识醒目。图标按钮无边框、紧凑尺寸（用户的界面偏好）。
- 测试 prompt：只写 design §11 列出的用例；不为假想输入补测试。

## 验证命令

```bash
cd backend
go test ./internal/dsp/peq/ ./internal/dspsettings/...
# 步骤 6 之后再加受影响的包，例如
go test ./internal/dsp/chain/ ./internal/zones/... ./internal/playback/render/...
```

```bash
cd frontend
npx vitest run <PEQ 编辑面板与 OPRA 面板的测试文件>
npm run typecheck
```

## 回滚点

- 步骤 1–4 是新包，删除即回滚。
- 步骤 5 迁移带 `.down.sql`（删表、删列）。
- 步骤 6 的节点在 `peq.enabled = false`（默认）时不插入，关掉即回到无 PEQ 的链路。
- OPRA 数据是单个嵌入文件，回退到上一版提交即可。

## 回报要求

改动文件列表与大小（含 OPRA 压缩包大小与条目数）；测试命令与结果；导入一段真实 AutoEq 文本、套用一条 OPRA 配置后的信号路径截图。
