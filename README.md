# ROOMusic

ROOMusic 正在从最小可运行版本重新构建。当前仓库只建立本地开发环境，不迁移 V0 中已经膨胀的应用容器、AI worker、operator、release overlay 或业务配置。

## 软件目标

ROOMusic 的长期目标是面向个人、家庭和小型私有群体的自托管音乐库管家。系统以 `ReleaseGroup -> Release -> Medium -> Track` 为核心理解发行版本，而不是只把音乐呈现为文件夹列表。后续能力可以覆盖浏览、搜索、播放、metadata 整理和受控 Agent 操作，但必须建立在稳定身份、来源可解释、后端权限、事务、操作记录和恢复边界之上。

当前 Core 0 先交付最小可用闭环：初始化和登录、允许目录内的只读扫描、保守的 Release Graph、Web 浏览、基础搜索，以及目录配置的 Change Set/Operation Journal。Redis、Meilisearch、GraphQL、播放、文件写入和可运行的 Agent runtime 都不是 Core 0 的强制依赖。

长期只保留一个产品 Agent：`Music Steward`。`Assistant`、`Steward`、`Operator` 是三种审批模式：Assistant 的危险操作由用户批准，Steward 由独立 Review Subagent 审查，Operator 由管理员显式进入后跳过审批直接执行。三种模式仍统一经过后端注册工具、权限和范围校验、结构化日志、Operation Journal 及适用的恢复机制；Operator 不等于无限制 shell、任意 SQL 或任意宿主机文件访问。

运行日志不承担“业务 Git”的职责。可恢复变更由 `Change Set + Operation Journal + Checkpoint + Reversible Executor` 描述；Core 0 只实现目录配置的窄闭环，不引入完整 Event Sourcing 或文件级恢复系统。

## 环境组成

- Go 1.25.10
- Node.js 24.16.0
- PostgreSQL 18
- Redis 8
- Meilisearch 1.45.0

Go 与 Node.js 在宿主机运行；PostgreSQL、Redis 和 Meilisearch 由 Docker Compose 承载。三个服务的端口只绑定到 `127.0.0.1`。

## 首次使用

安装工具链：

```bash
mise install
```

如需重建本地配置：

```bash
cp .env.example .env
```

启动依赖：

```bash
mise run env-up
```

检查环境：

```bash
mise run env-check
```

查看日志或停止依赖：

```bash
mise run env-logs
mise run env-down
```

## 迁移边界

本次只迁移可复用的开发工具链和基础数据服务配置。以下 V0 内容不会自动迁移：

- PostgreSQL、Redis、Meilisearch 的旧数据卷
- `.env` 中的生产密码、Token 或 API key
- `app`、`worker-ai`、`operator` 等应用服务
- AI provider、MusicBrainz、认证、扫描和发布配置

后续每引入一个实际能力，再同步增加对应依赖与环境变量，避免配置先于功能膨胀。
