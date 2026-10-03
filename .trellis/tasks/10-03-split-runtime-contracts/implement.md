# 运行合同拆分执行计划

> 需求见 [prd.md](prd.md)，去向见 [design.md](design.md)。直接提交到 `main`，不推送。在 F4 归档之后进行。

## 启动前

- [ ] F4 已归档。
- [ ] 用户批准最终规划摘要后运行 `python3 ./.trellis/scripts/task.py start .trellis/tasks/10-03-split-runtime-contracts`。

## 步骤

- [ ] 按 design.md 的去向表新建四个专题文件，并改写总览：
  - 移动时原样复制行
  - 只按 S2 允许的三类改动编辑
- [ ] `catalog-rest-contracts.md` 加入发行字段、列表/详情/evidence 与测试条目。
- [ ] 改入站链接与 `backend/index.md`、`frontend/index.md` 的表格。
- [ ] 运行无损校验脚本，结果写进本文件的“校验记录”。
- [ ] 校验：

  ```bash
  wc -c .trellis/spec/backend/*.md                       # 每个都 ≤ 24,000
  python3 /tmp/split-check.py                            # 无损校验
  python3 /tmp/link-check.py <改动的文件>                  # 相对链接与锚点
  git diff --check
  ```

- [ ] PRD 补“实施结果与验收对照”，包括发现的过时内容清单。
- [ ] 提交：`docs(spec): 按主题拆分后端运行合同`。
- [ ] 归档：`--skip-branch-validation`，然后修正归档后的相对链接。

## 校验记录

（实施时填写）
