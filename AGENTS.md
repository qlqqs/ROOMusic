<!-- TRELLIS:START -->
# Trellis Instructions

These instructions are for AI assistants working in this project.

This project is managed by Trellis. The working knowledge you need lives under `.trellis/`:

- `.trellis/workflow.md` — development phases, when to create tasks, skill routing
- `.trellis/spec/` — package- and layer-scoped coding guidelines (read before writing code in a given layer)
- `.trellis/workspace/` — per-developer journals and session traces
- `.trellis/tasks/` — active and archived tasks (PRDs, research, jsonl context)

If a Trellis command is available on your platform (e.g. `/trellis:finish-work`, `/trellis:continue`), prefer it over manual steps. Not every platform exposes every command.

If you're using Codex or another agent-capable tool, additional project-scoped helpers may live in:
- `.agents/skills/` — reusable Trellis skills
- `.codex/agents/` — optional custom subagents

Managed by Trellis. Edits outside this block are preserved; edits inside may be overwritten by a future `trellis update`.

<!-- TRELLIS:END -->

## 项目文档语言约束

- 新增或修改的项目文档统一使用简体中文。
- 适用范围包括 README、架构文档、ADR、API 文档、Trellis 任务文档、说明性注释和运维文档。
- 代码标识符、文件名、命令、协议名称、产品名称和必须保留的原文引用可以使用英文。
- 与当前任务无关的已有英文内容不需要翻译，除非用户明确要求。

## 测试范围约束

- 验证修改时优先运行与改动直接相关的最小测试集。
- 除非用户明确要求、修改影响范围无法局部覆盖，或局部验证失败需要进一步定位，否则不要运行全量测试。
- 在最终说明中列出实际执行的验证命令；如果未运行全量测试，简要说明原因。
