# 拆分后端运行合同

> 状态：规划中（2026-10-03 创建）。只改规范文档，不改代码。
> 来源：M1b 归档时记下的遗留项。用户 2026-10-03 要求与 [F4](../10-03-classical-f4-artwork-discovery/prd.md) 一并先解决。
> 技术设计（各段去向）见 [design.md](design.md)，执行顺序与校验见 [implement.md](implement.md)。

## 目标与用户价值

把 `.trellis/spec/backend/core0-runtime-contracts.md` 按主题拆成几个文件，每个都能被 Trellis 完整注入上下文。
做某一类后端工作时，只读对应主题，不必通读 58 KB。

## 已核实事实（2026-10-03）

- 文件 58,647 字节、713 行，是 `.trellis/spec` 下唯一超过 20 KB 的文件。
- Trellis 给子代理注入上下文时，单个文件的上限是 32,768 字节，超出部分会被截断，只留一句“去读全文”的提示
  （`.claude/hooks/inject-subagent-context.py:177` `DEFAULT_MAX_FILE_BYTES`、`:287`；`.trellis/scripts/common/config.py:176`）。
  第 32 KB 以后的内容会被截掉，包括扫描协调场景和后续技术债。
- 文件内容混了六个主题：
  - 平台层：启动、请求与响应、HTTP 缓存、迁移执行器、环境、CI、HTTP 完成日志
  - 身份与成员
  - 目录与扫描流水线：目录操作幂等、暂存、曲目与 Release 身份、CUE、抓轨日志、封面
  - 扫描协调：已是独立的“场景”一节
  - 真实库 Smoke 与 V0 对照
  - 发行 REST 的少量字段：本应在 [Catalog REST 合同](../../spec/backend/catalog-rest-contracts.md) 里

  各主题的错误矩阵、案例、必需测试、示例都混在同一张表或同一个列表里。
- 仓库里（不含归档任务）链接它的只有 6 处，都不带锚点：
  - `backend/index.md`
  - `backend/catalog-rest-contracts.md:50`（指“HTTP 缓存”）
  - `backend/database-guidelines.md:93`（指曲目身份规则）
  - `frontend/index.md:52`
  - `frontend/type-safety.md:65`（指扫描响应）
  - `docs/architecture/core-1-roadmap.md:62`

  文件内部用“见上文‘身份与成员接口’”这类文字互相引用。

## 需求

- **S1 按主题拆分**：保留 `core0-runtime-contracts.md` 这个文件名，改为总览加平台层合同，并放一张专题文件表与后续技术债。
  新建以下文件：
  - `identity-contracts.md`
  - `library-scan-contracts.md`
  - `scan-coordination-contracts.md`
  - `real-library-smoke-contracts.md`

  发行 REST 的字段与测试条目移入已有的 `catalog-rest-contracts.md`。每个专题文件沿用现有的七节结构：
  范围与触发、签名、合同、校验与错误矩阵、案例、必需测试、示例。没有内容的节省略。
- **S2 无损**：原文每一条合同、矩阵行、案例、测试条目和代码示例都必须出现在某个新位置，且只出现一次。允许的改动只有三类：
  - 把“见上文……”改成指向新文件的链接
  - 把混合了多个主题的列表项按主题拆开
  - 新写的“范围与触发”导语

  内容不改写、不删减，过时的说法也照搬，发现的过时内容记在 PRD 里另行处理。
- **S3 大小**：拆分后每个文件不超过 24 KB，给后续增长留余量；硬上限是 32 KiB。
  `backend/index.md` 写明这条规则：接近 24 KB 时按主题再拆。
- **S4 链接**：上面 6 处入站链接按主题改指新文件。`backend/index.md` 的层级表列出新文件，`frontend/index.md` 的表格同步。
  归档任务里的历史链接不改，文件名保留，所以它们仍然有效。

## 验收标准

| # | 标准 | 对应 |
| --- | --- | --- |
| B1 | 无损校验脚本通过：原文所有非标题内容行（规范化空白后），都能在新文件集合中找到，且只出现一次；对不上的行逐条列在 implement.md 里，并说明属于 S2 允许的哪类改动 | S2 |
| B2 | `wc -c` 显示每个 `.trellis/spec/**/*.md` 都不超过 24,000 字节 | S3 |
| B3 | 改动文件里的相对链接（含锚点）全部能解析；`git diff --check` 通过 | S4 |
| B4 | `backend/index.md` 列出全部专题文件，并写明大小规则；没有遗漏任何入站链接 | S3、S4 |
| B5 | 只改 `.trellis/spec/` 与 `docs/architecture/core-1-roadmap.md`，以及本任务目录；不改代码 | 范围 |

## 不做

- 改写、合并或删除合同内容，包括已划掉的技术债条目，以及“具体新路由形式尚待批准”这类已过时的说法。
  过时内容只记录，另行处理。
- 修改归档任务里的链接。
- 拆分其他规范文件（都在 20 KB 以下）。

## 与 F4 的顺序

先做 F4。F4 修改的封面规则、错误矩阵、测试条目与技术债，都在拆分之前写进原文件；拆分时随“目录与扫描”一起移走。
这样 F4 的提交只包含规则变化，拆分的提交只包含移动。
