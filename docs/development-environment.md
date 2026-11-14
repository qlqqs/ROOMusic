# 本机开发与测试环境

## 工具链

mise 位于 `/home/qlqq/.local/bin/mise`，Go 与 Node 按 `.mise.toml` 固定版本安装在用户目录。进入项目后执行：

```bash
mise trust
mise install
mise exec -- go version
mise exec -- node --version
```

交互式 Bash 已在用户 `.bashrc` 末尾启用 mise。不通过 apt 安装开发工具链。

## 工作目录

本任务分支为 `chore/compose-auto-init`，工作目录为 `/home/qlqq/workspace/ROOMusic-compose-auto-init`。主工作区的未提交归档改动保留原样。

- `temp/dev/`：本项目开发数据。
- `temp/`：临时文件、合成测试素材与每次测试的部署目录。
- `temp/` 整体加入 Git 忽略规则；临时文件按日期归档到 `temp/archive/YYYY-MM-DD/`。

## Docker 隔离约定

复用本机 Docker daemon。开发项目名使用 `roomusic-compose-dev`；每次集成测试使用 `roomusic-compose-test-` 加随机后缀。不同项目各自使用网络及目录，不设置固定容器名。

开发和测试端口绑定 `0.0.0.0`；开发使用独立端口，测试由 Docker 分配可用端口。测试容器限制 CPU 与内存，数据库集成和端到端测试串行运行。

仅清理当前测试项目及其临时目录，不执行全局 prune。测试仅使用合成数据，不访问现有实例的配置、凭据或数据。

## 当前验证状态

Go 1.25.10、Node 24.22.1 已安装。`qlqq` 已加入 docker 组，Docker 29.9.2 与 Compose v5.6.1 可访问。已有会话使用 `sg docker -c '命令'`；重新登录后直接使用 docker。

2026-11-14 已通过独立临时 Compose 项目验证目录挂载、独立网络、`0.0.0.0` 动态端口、0.5 CPU 和 128 MiB 内存限制，并清理该测试容器、网络及部署目录。未操作现有实例。

实际验证命令：

```bash
sg docker -c 'docker version --format "{{.Server.Version}}"'
sg docker -c 'docker compose version'
sg docker -c 'python3 temp/archive/2026-11-14/environment-smoke/check.py'
```

临时验证脚本保存在忽略的 `temp/archive/2026-11-14/environment-smoke/` 中。此验证确认本机隔离能力；生产自动初始化与数据库备份恢复的验证随部署改造执行。
