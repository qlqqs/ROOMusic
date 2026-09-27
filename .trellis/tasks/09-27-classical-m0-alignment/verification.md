# M0 文档验证记录

## 验证边界

日期：2026-09-27。本轮只有文档、规范与任务记录变更；不运行 Go/npm/数据库全量测试，
不启动 UI、不扫描真实音乐、不生成截图，也不将静态检查写成浏览器/媒体兼容或许可验收。
M0 批准不等于四类 ADR、Core 1 截止范围或后续业务实施已获批准。

## 需求到产物

| 需求 | 交付证据 | 检查点 |
| --- | --- | --- |
| R1 | `frontend/DESIGN.md` | 原型来源、颜色/字体/间距/布局、组件/状态、可访问性与落地差异；来源值与新提案分开 |
| R2 | `docs/architecture/core-1-roadmap.md`、产品目标与 README | 逐页面现有 API/缺口/阶段；M1 保留已有管理写操作，不假装已有播放或精细扫描进度 |
| R3 | frontend 八份规范、共享索引/工程原则/产品目标、backend 索引/运行合同、架构摘要 | 删除“当前前端源码已存在”和旧视觉/演示例外等误导；保留解码、安全及队列语义 |
| R4 | `docs/architecture/adr/0001`–`0004` 对应四份草案 | 现状/推荐/替代/验收/回退/待批准；外部事实对应官方资料，不写成技术已采纳 |
| R5 | 路线的旧任务承接表、播放器/状态规范 | 旧任务 `in_progress` 及历史批准记录不改；不将历史模拟能力当作本轮实现 |
| R6 | 父任务 PRD/设计/计划、路线阶段表 | M1 与 B0 独立，M4 依赖 M3；较小交付方案与 Core 1 截止范围仍待确认 |

## 已运行的最小检查

```bash
python3 .trellis/scripts/task.py validate .trellis/tasks/09-27-classical-core-1
python3 .trellis/scripts/task.py validate .trellis/tasks/09-27-classical-m0-alignment
python3 .trellis/scripts/task.py current --source
python3 .trellis/scripts/task.py list
git diff --check
git diff --exit-code HEAD -- backend/cmd backend/migrations frontend-desgin frontend/package.json frontend/package-lock.json .github scripts .trellis/tasks/09-04-music-client-experience
python3 /tmp/roomusic-m0-validate.py
```

临时校验器位于本次工作区 `/tmp`，不是仓库工具，不作为未来会话必须存在的依赖。
其检查：从 `git diff --name-only HEAD` 与 `git ls-files --others --exclude-standard` 合并全部
变更文件，验证文档白名单、UTF-8 文本/末尾换行/行尾空白、冲突标记、相对 Markdown 链接、
JSON/JSONL 及上下文目标、四份 ADR 草案状态、父子关系，以及父 `planning` / M0 `in_progress`。
受保护目录由上方 `git diff --exit-code` 额外验证。

初次完整集合检查通过，独立全范围审查结论见下节。新增未跟踪文件也纳入
检查，不能只靠 `git diff --check` 的已跟踪文件结果。

## 实施代理核对

- 设计指南代理：只新增 `frontend/DESIGN.md`，核对原型的 32 个颜色值、6 个间距、
  10 个布局值和 3 个字体族；来源差异明确记录。对比度是静态计算，不是浏览器验证。
- 元数据/操作 ADR 代理：只新增 `0002` 与 `0003`，核对扫描身份、证据替换、目录幂等与
  恢复源码；文档链接/围栏/空白检查通过，未运行所引用业务测试。
- 前端/媒体 ADR 代理：核对官方技术资料与当前配置；官方资料不能证明项目已部署、兼容或合规。

## 新发现与未做事项

- 目录停用/恢复幂等指纹缺少路由 root ID，存在跨目标重放风险；静态依据已写入 ADR 0003、
  路线和运行合同技术债。本轮未动态复现、未修复，不夹带进 B0 行为保持重构。
- 技术选型、浏览器/codec 矩阵、身份演进、风险/权限、回滚范围与 Core 1 截止范围仍待评审。
- 未安装/升级任何依赖，未修改业务代码、迁移、CI/脚本、原型或真实媒体；未提交或归档。

## 独立审查与最终结果

- 独立审查者：Confucius（`trellis-check`）。结论：R1–R6 已覆盖，无阻塞 M0 文档验收的问题。
- 已修正文档：扫描 DTO 补齐当前 `diagnostics` 摘要并明确非进度；修正封面权限和 CI 条件
  构建描述；删除前端示例的虚构扫描计数字段；区分 scanner 当前直接写图与未来接口边界。
- 公共文档改用稳定路线/架构入口，避免依赖活跃任务路径；修正 QueryClient 外部资料地址及
  FFmpeg 失效锚点。后续任务归档仍需校验父任务和 JSONL 的内部引用，不在本轮提前归档。
- ADR 0004 的 Web Audio 依据采用固定的 2021-06-17 Recommendation，而非滚动草案。
- 已独立核对原型颜色、间距、字体覆盖、布局和六组静态对比度，以及当前扫描七字段。
- 目录幂等目标绑定、封面 HTTP 缓存和 API fallback 等业务风险保持未修复状态，不冒充
  本轮完成；媒体/性能/安全动态测试和发行许可验收均尚未执行。

审查额外执行：

```bash
python3 .trellis/scripts/get_context.py --mode packages
python3 /tmp/roomusic-m0-review-check.py
git diff --exit-code HEAD -- backend frontend-desgin frontend/package.json frontend/package-lock.json .github scripts .trellis/tasks/09-04-music-client-experience .mise.toml compose.yaml compose.test.yaml
```

`review-check.py` 同样是本次 `/tmp` 临时检查器，检查原型/文档 token、静态对比度、
Markdown 围栏、公共入口不引用活跃任务目录，以及扫描 DTO 与当前源码一致。
审查会话依据派发任务路径加载上下文；它没有修改主会话的任务指针。

Lint、TypeCheck 与 Go/npm/数据库全量测试不适用于本轮纯文档变更，均未运行。
文档验收通过后尚未提交；M0 保持 `in_progress` 等待提交批准，父任务保持 `planning`。

## 主会话最终重跑

2026-09-27，审批/进度记录补齐后再次通过两任务 `task.py validate`、两个临时文档检查器、
`git diff --check` 与受保护路径零差异检查：共 36 个变更文档/任务文件、177 个本地链接、
四份草案；原型数值/字体/对比度、扫描七字段、JSON/JSONL 及父子关系均通过。
主会话 `task.py current --source` 仍指向 M0。未运行全量业务测试，原因同上；未创建分支、
未提交、未推送、未归档，等待用户对提交计划的一次性确认。

## Git 提交批准

2026-09-27，用户回复“提交”，明确批准 `commit-plan.md` 的文件范围、工作提交和 M0
收尾。已创建 `docs/classical-m0-alignment` 分支并同步任务元数据；此前“等待确认”的
记录属于批准前的历史状态。四份 ADR、Core 1 范围仍待批准；不推送，不归档旧任务。

批准记录与分支更新后，提交前再次运行两任务 `task.py validate`、两个临时文档检查器、
`git diff --check` 与受保护目录零差异检查，全部通过；仍为 36 个文件和 177 个本地链接。
