# 运行合同拆分执行计划

> 需求见 [prd.md](prd.md)，去向见 [design.md](design.md)。直接提交到 `main`，不推送。在 F4 归档之后进行。

## 启动前

- [x] F4 已归档。
- [x] 用户批准最终规划摘要后运行 `python3 ./.trellis/scripts/task.py start .trellis/tasks/10-03-split-runtime-contracts`。

## 步骤

- [x] 按 design.md 的去向表新建四个专题文件，并改写总览：
  - 移动时原样复制行
  - 只按 S2 允许的三类改动编辑
- [x] `catalog-rest-contracts.md` 加入发行字段、列表/详情/evidence 与测试条目。
- [x] 改入站链接与 `backend/index.md`、`frontend/index.md` 的表格。
- [x] 运行无损校验脚本，结果写进本文件的“校验记录”。
- [x] 校验：

  ```bash
  wc -c .trellis/spec/backend/*.md                       # 每个都 ≤ 24,000
  python3 /tmp/split-check.py                            # 无损校验
  python3 /tmp/link-check.py <改动的文件>                  # 相对链接与锚点
  git diff --check
  ```

- [x] PRD 补“实施结果与验收对照”，包括发现的过时内容清单。
- [ ] 提交：`docs(spec): 按主题拆分后端运行合同`。
- [ ] 归档：`--skip-branch-validation`，然后修正归档后的相对链接。

## 校验记录

2026-10-03 实施。拆分用一次性脚本 `/tmp/split.py`（按 F4 之后原文件的行号搬行，每个范围校验首行与末行），校验用 `/tmp/split-check.py`
（不入库）。下面是结果。

**大小**（字节）：

| 文件 | 拆分前 | 拆分后 |
| --- | --- | --- |
| `core0-runtime-contracts.md`（总览） | 61,682 | 20,449 |
| `library-scan-contracts.md`（新） | — | 23,096 |
| `scan-coordination-contracts.md`（新） | — | 9,694 |
| `identity-contracts.md`（新） | — | 7,372 |
| `real-library-smoke-contracts.md`（新） | — | 3,451 |
| `catalog-rest-contracts.md` | 9,916 | 11,362 |

`find .trellis/spec -name '*.md' -size +24000c` 为空。最大的 `library-scan-contracts.md` 已到 23,096，离 24,000 只剩约 900 字节：
下一次给扫描、封面或身份规则加内容时，需要先把“封面”或“曲目与 Release 身份”按主题再拆出去（`backend/index.md` 的大小规则已写明）。

**无损校验**：把原文（`git show HEAD:`）每个非空、非标题、非表格分隔行规范化空白后作多重集，与五个文件加 `catalog-rest-contracts.md` 相对 HEAD 新增的行比较。
原文 628 行，新文件 650 行。原文里“缺少”的 9 行与新文件“多出”的 31 行，逐条对应如下，没有别的差异：

| 类别（S2 允许的改动） | 原文行（缺少） | 新文件（多出） |
| --- | --- | --- |
| 把“见上文/见下文”改成指向新文件的链接 | §1 第 15–16 行的“合同见下文……各错误矩阵”；技术债里“见上文扫描场景”“见上文‘身份与成员接口’”“见上文‘封面来源与文件夹封面发现’”“见上文‘数据库与扫描’”（跨两行） | 同样位置的 6 行，把引用换成相应新文件的链接 |
| 把混合了多个主题的列表项按主题拆开 | §5 的“错误”项（3 行） | 3 个新项：总览（`role` 缺失）、目录与扫描流程（root 原始路径、unsupported、FIFO、按标题删 CUE 轨）、身份与成员（忽略 `Exec` 错误） |
| 新写的导语、专题表与表头 | — | 总览的“专题文件”说明与 5 行表；身份、目录与扫描流程、真实库 Smoke 各 2–3 行范围导语；扫描协调 2 行来源说明；主矩阵表头 `| 条件 | 行为 |` 在 4 个文件里各一份（比原文多 3 份） |

没有丢失、重复或改写的内容行。标题与表格分隔行不参与比较：标题按主题重排（场景一节的标题上提一级，`#### Wrong/Correct` 改为 `###`），属于结构调整。
并入扫描协调的三处内容：扫描状态枚举（接口签名）、`canceled` 终态与 worker context 条目（合同）、扫描记录的测试条目（必需测试）。

**入站链接**（共 6 处）：改了 4 处——`backend/index.md`（层级表）、`frontend/index.md`（表格）、`frontend/type-safety.md`（扫描响应 → 扫描协调合同）、
`backend/database-guidelines.md`（曲目身份 → 目录与扫描流程合同）；不用改的 2 处——`backend/catalog-rest-contracts.md` 指“HTTP 缓存”，仍留在总览；
`docs/architecture/core-1-roadmap.md` 泛指运行合同，总览里有专题文件表，保持不变。`python3 /tmp/link-check.py`：99 个链接，0 个问题（含锚点）；`git diff --check` 通过。
