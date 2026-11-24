# 统计端迁到门户项目

## 目标

统计端只由项目方部署，用户的实例用不到。把统计端程序、公开汇总页与部署文件迁到新项目 `roomusic-site`（项目门户的源码仓库，目前只有统计页，首页、安装文档等以后再加）。本仓库只保留实例一侧：心跳的生成、发送、管理员接口与界面。

## 已定的决定（qlqq 选定，2026-11-24）

- 门户项目放在本机 `~/workspace/roomusic-site`（与本仓库同级），单独的 Git 仓库。
- 先只放统计页，目录给以后的门户页面预留位置。
- 推翻父任务“统计端程序开源在本仓库”的决定：统计端改为在门户项目里开源。

## 范围

### roomusic-site（新项目）

```
roomusic-site/
├── insights/          Go 模块：统计端 roomusic-insights 与汇总页
│   ├── cmd/roomusic-insights/
│   ├── internal/heartbeat/   心跳格式的解码与校验（从本仓库复制，统计端自己维护）
│   ├── internal/collector/
│   └── testdata/heartbeats/  与 ROOMusic 对齐的心跳样本
├── site/              预留：roomusic.org 门户页面（首页、安装、文档）
├── deploy/insights/   Compose、Dockerfile、部署说明
└── docs/              项目说明
```

- 统计端行为不变：接口路径、限速、去重、7/30 天汇总、汇总页、90 天清理、原子写、不记录 IP；部署方式不变（`127.0.0.1:18110`、非 root、只读根文件系统、`cap_drop: [ALL]`）。
- 心跳格式不再引用 ROOMusic 的包（Go 不允许跨模块引用 `internal`）；统计端复制解码与校验，以 `testdata/heartbeats/` 的样本作为两边的约定。
- 许可与 ROOMusic 相同（AGPL-3.0）。

### ROOMusic（本仓库）

- 删除 `backend/cmd/roomusic-insights`、`backend/internal/insights/collector`、`deploy/insights`。
- `internal/insights/schema` 保留（实例生成心跳、mock 校验都用它）。
- 依赖规则测试去掉 `insights/collector`。
- 文档与规范改指向门户项目：`docs/insights.md`、ADR 0008、`.trellis/spec/backend/insights-contracts.md`、父任务的决定表。
- 心跳格式的改动顺序写进合同：统计端先上线能接收新版本，实例再发新版本。

## 不做

- 不改心跳字段、默认地址（仍是 `https://insights.roomusic.org/api/v1/heartbeat`）、管理员接口与界面。
- 不部署统计端，不配置 DNS 与反向代理。
- 不给门户项目建远端仓库、不推送。

## 验收

- `roomusic-site/insights`：`go vet ./...`、`go test ./...` 通过；样本测试覆盖 schema 1 与 2 的心跳能被接收、汇总正确。
- `deploy/insights` 的 `docker compose config` 通过，镜像能构建（构建后删除本任务产生的镜像）。
- ROOMusic：`go build ./...`、`go vet ./...`、`go test ./internal/insights/... ./internal/app/ -run 'Dependency'` 通过；仓库内不再有指向已删除目录的引用。
