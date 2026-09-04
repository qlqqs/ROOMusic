# Paseo、Claude Code 与 Codex 用户迁移调研

## 调研范围

本调研用于把 Ubuntu 24.04 主机上的独立 Paseo daemon 从 `root` 迁移到 `qlqq`
用户，并判断哪些配置需要复制、哪些运行时状态应重新生成。调研时间为 2026-09-04。
所有本机检查只记录路径、版本、状态和配置键；密码、Token、API key、真实音乐文件名、
Agent 提示词和会话正文均未写入本文。

## 当前拓扑

- Paseo 是通过 npm 安装的 standalone daemon，不是桌面应用托管 daemon，也不在
  Docker 内运行。
- systemd 系统服务以 UID `0` 运行，`PASEO_HOME=/root/.paseo`，监听
  `0.0.0.0:6767`，启用 daemon 自带 Web UI，禁用 relay。
- `paseo daemon status --json` 报告 CLI/daemon 均为 `0.6.1`，当前 owner 为 root，
  Claude Code 和 Codex 的解析路径均位于 `/root`。
- `paseo provider diagnostic` 报告 Claude Code `2.1.251` 和 Codex CLI `0.151.0`
  均为 Ready；Claude 使用当前 root 用户的登录状态，Codex 使用自定义 provider 配置
  与 systemd 注入的环境变量。
- 用户确认其他 Agent 已停止。`paseo ls --json` 复核到 37 个 `idle`、12 个 `closed`
  和 1 个 `running`；最后一个是执行本调研的当前对话。切换本身会中断该会话，必须由
  Paseo cgroup 外的独立 systemd 一次性任务完成。
- qlqq 当前没有用户进程，systemd user manager 可访问但未启用 linger；它尚未加入
  `docker` 组。

## 官方文档结论

### Paseo

1. Paseo 默认把配置和本地状态放在 `~/.paseo`；可通过 `PASEO_HOME` 或
   `paseo daemon start --home` 选择独立目录。同一台主机可用不同 `PASEO_HOME` 运行
   隔离 daemon，因此可在 `127.0.0.1:6768` 预验收 qlqq 环境，而不触碰 root daemon。
2. 配置优先级为默认值、`config.json`、环境变量、CLI 参数；监听地址、密码和 Web UI
   等启动参数会覆盖配置文件。最终 user service 必须显式固定 HOME、PASEO_HOME、PATH
   和监听参数，避免依赖交互式 shell。
3. Paseo 不捆绑 Claude Code 或 Codex。它直接解析 daemon 用户 PATH 中的 CLI，并使用
   该用户已有的登录状态。官方故障排查建议用 `paseo provider diagnostic <provider>`
   对比 resolved path、daemon PATH 和版本。
4. Claude Code 应先在运行 Paseo 的同一用户下安装并登录；登录过期后应在该用户下
   重新认证，再创建新 Paseo 会话。
5. `/api/health` 是无需 daemon 密码的存活探针，适合 systemd 切换任务做健康检查；
   其他 HTTP/WebSocket 调用仍需密码。

### Codex

1. Codex CLI 支持 ChatGPT 登录和 API key 登录；`codex login status` 可只读确认当前
   认证方法。
2. 登录缓存可能保存在 `~/.codex/auth.json` 或系统凭据存储中。官方明确要求把
   `auth.json` 当作密码处理，不提交、不粘贴、不输出。
3. 当前 root 环境没有 `~/.codex/auth.json`，实际使用 systemd 提供的 API 环境变量和
   `~/.codex/config.toml` 自定义 provider。因此迁移应复制配置、重写可信项目路径，
   并把必要环境变量放入 qlqq 专用 `0600` service env；不应复制 Codex 的 SQLite、
   WAL、锁、shell snapshot、历史和运行队列。

## 版本与供应链基线

项目 `.mise.toml` 固定 Go `1.25.10` 和 Node.js `24.16.0`。当前 root 环境还提供以下
npm 包版本；2026-09-04 已通过 npm registry 确认版本仍可获取并记录其 `dist.integrity`
用于实施时校验，但本文不重复粘贴完整值：

| 工具 | npm 包 | 固定版本 |
| --- | --- | --- |
| Paseo | `@getpaseo/cli` | `0.6.1` |
| Claude Code | `@anthropic-ai/claude-code` | `2.1.251` |
| Codex CLI | `@openai/codex` | `0.151.0` |
| GitHub Copilot CLI | `@github/copilot` | `1.0.82` |
| Trellis | `@mindfoldhq/trellis` | `0.6.16` |

mise 使用 root 当前版本 `2026.8.16` 作为迁移基线。官方安装脚本支持
`MISE_VERSION` 与 `MISE_INSTALL_PATH`，并从发布页获取 SHA-256 清单进行校验，因此可在
qlqq 下做版本固定的独立安装，而无需复制 root 的 mise 目录。

## 迁移结论

- qlqq 使用独立 mise/Node/Go/npm 全局目录，不能把 root NVM 或 root mise 加入 PATH。
- qlqq 新建自己的 `~/.paseo`。只迁移经验证的 `config.json` 设置和必要凭据，然后通过
  CLI 注册 `/home/qlqq/workspace/ROOMusic`；不复制 root PID、日志、Agent 状态和旧项目/
  工作区注册表。历史会话仍完整保留在 `/root/.paseo`，作为回滚与审计资料。
- 使用同一个 qlqq `PASEO_HOME` 先在 `127.0.0.1:6768` 运行 preview service；验证完成
  并停止 preview 后，最终 service 再用同一 home 接管 `0.0.0.0:6767`。两个 qlqq daemon
  不得同时写同一 home。
- qlqq service 使用绝对二进制路径与显式 PATH。交互 shell 同时把
  `~/.local/bin` 和 mise shims 加入 PATH，保证新登录 shell 与 daemon 的解析结果一致。
- Claude 的非会话设置与认证文件可以在同一主机内以 `0600` 安全复制；若
  `claude auth status` 或 provider diagnostic 失败，必须由 qlqq 重新登录，不能回退到
  root 二进制或 root HOME。
- Codex 只迁移 `config.toml`、需要的 skills/plugin 配置和 qlqq service env；不迁移
  活跃运行时数据库。项目级 `.codex/` 随仓库复制。
- 迁移后的 daemon server ID 会重新生成。直接 Web UI 仍使用原监听地址和密码；已保存
  旧 server ID 的客户端可能需要删除旧主机并重新连接。root server identity 保留在
  root home，回滚时不会丢失。

## 参考资料

- [Paseo Getting started](https://paseo.sh/docs.md)
- [Paseo Configuration](https://paseo.sh/docs/configuration.md)
- [Paseo Troubleshooting](https://paseo.sh/docs/troubleshooting.md)
- [Paseo Claude Code](https://paseo.sh/docs/claude-code.md)
- [Paseo Codex](https://paseo.sh/docs/codex.md)
- [Paseo CLI](https://paseo.sh/docs/cli.md)
- [OpenAI Codex Authentication](https://learn.chatgpt.com/docs/auth.md)
