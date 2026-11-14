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


## 工作方式

- 未获明确授权前，只讨论和检查，不修改代码或系统配置。
- 已授权范围内自主完成工作，不反复询问同一事项。
- 修改前先读取项目规范、本机环境说明和现有配置。
- 不覆盖其他任务的未提交改动。
- 项目文档和新增说明性注释使用简体中文。
- 不添加防御性说明，不为不存在的场景设计兜底。
- 只对实际输入边界、依赖和已知失败情况做必要校验。

## 工具链管理

- Go、Node 等开发工具统一使用 mise 管理。
- mise 和工具版本安装在当前用户目录，不通过 apt 安装开发工具链。
- 优先使用仓库已有的 mise 配置，按项目固定版本。
- 不替换系统工具、不修改系统级默认版本。
- 必须增加系统依赖时，先说明具体依赖、用途和系统改动，再取得授权。

## Docker 使用

- 默认复用本机现有 Docker daemon，不另建 rootless daemon。
- 开发、测试、生产使用不同的 Compose 项目名。
- 避免固定 container_name，由 Compose 生成隔离名称。
- 每个项目使用独立网络和数据目录。
- 开发和测试端口绑定 0.0.0.0，使用独立端口避免冲突。

## 代码与数据隔离

- 非简单修改使用独立任务分支；需要并行或工作区已有其他改动时，使用独立工作目录。
- 开发数据放在项目空间的temp/dev，加入 Git 忽略规则，不提交、不推送。
- 临时文件统一放在项目空间的 temp 目录，加入 Git 忽略规则，不提交、不推送。
- temp/archive作为临时文件的归档目录，按照日期命名子文件夹归档，加入 Git 忽略规则，不提交、不推送。
- 每次集成测试使用独立临时目录和唯一 Compose 项目名。
- 测试使用合成数据，不读取现有实例的数据、配置或凭据。
- 未经明确授权，不启动、停止、升级、迁移或删除现有实例。

## 凭据使用

- 按本机环境说明定位凭据文件，仅访问当前任务需要的条目。
- 不在回复、日志、命令参数或项目文件中暴露密码。
- 需要使用 sudo 密码时，通过标准输入传递。
- 不将凭据提交到 Git。

## 验证与清理

- 优先运行与改动直接相关的最小测试集。
- 复用已有测试结果，除非有改动代码，否则不进行重复测试。
- 只有影响范围无法局部覆盖、局部失败需要定位，或用户明确要求时，才扩大测试范围。
- 部署改动验证 Compose 配置及相关容器启动流程。
- 只清理本任务创建的容器、网络和临时目录。
- 不执行全局 Docker prune，不删除其他项目的资源。
- 未执行、失败或跳过的验证必须如实记录，不能报告为通过。
- 完成后说明实际改动、执行的验证命令及尚未完成的事项。
