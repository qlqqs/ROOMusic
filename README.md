# ROOMusic

ROOMusic 正在从最小可运行版本重新构建。当前仓库只建立本地开发环境，不迁移 V0 中已经膨胀的应用容器、AI worker、operator、release overlay 或业务配置。

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
