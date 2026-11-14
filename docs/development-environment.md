# 项目开发与测试环境

本文记录 ROOMusic 的开发与测试约定。本机环境说明见 `~/.codex/AGENT.md`；通用工作约定见项目根目录的 `AGENTS.md`。

## 工具链

Go 与 Node 版本由仓库根目录的 `.mise.toml` 固定。进入项目后执行：

```bash
mise trust
mise install
mise exec -- go version
mise exec -- node --version
```

## 工作目录

以下路径均相对于当前项目工作目录：

- `temp/dev/`：本项目开发数据。
- `temp/`：临时文件、合成测试素材与每次测试的部署目录。
- `temp/` 整体加入 Git 忽略规则；临时文件按日期归档到 `temp/archive/YYYY-MM-DD/`。

## Docker 隔离约定

开发项目名使用 `roomusic-compose-dev`；每次集成测试使用 `roomusic-compose-test-` 加随机后缀；生产使用独立项目名。不同项目各自使用网络及目录，不设置固定容器名。

开发和测试端口绑定 `0.0.0.0`；开发使用独立端口，测试由 Docker 分配可用端口。测试容器限制 CPU 与内存，数据库集成和端到端测试串行运行。

仅清理当前测试项目及其临时目录，不执行全局 prune。测试仅使用合成数据，不访问现有实例的配置、凭据或数据。
