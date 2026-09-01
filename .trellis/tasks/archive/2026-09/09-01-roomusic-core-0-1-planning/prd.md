# ROOMusic Core 0.1 版本规划

## Goal

在 Core 0 稳定闭环之上，提供无需特殊反代改造的访问方式、可控的开发数据库重置，以及前后端自动热更新的快捷开发工作流。

## Background

- 当前写接口通过 `Origin` 与请求 `Host` 严格匹配；TLS 终止在反代时容易因外部域名与内部 Host 不一致而返回 `origin_forbidden`。
- `setup_state` 记录一次性初始化状态；当前数据库已初始化后，前端只显示登录入口。
- Go 服务默认提供内嵌前端生产构建；Vite 仅在手动运行 `npm run dev` 时提供热更新。

## Requirements

1. 反代兼容：继续严格校验来源，但新增启动时配置的公网 URL（例如 `ROOMUSIC_PUBLIC_URL=https://music.qlqqs.com:8888`）；来源必须与该 URL 完全匹配，不依赖内部反代 Host。
2. 开发重置：提供仅 dev 环境可用的数据库重置命令或脚本，清理业务表并恢复 `setup_required=true`；执行前必须明确警告并要求显式确认。
3. 快捷开发：提供一条开发命令同时启动 PostgreSQL（必要时）和后端、Vite 前端，API 代理、Cookie 和跨源开发配置可用，源码修改自动生效，无需手动构建或复制静态资源。
4. 生产边界：开发代理配置、重置接口和不安全 Cookie 行为不得在 production 环境启用。

## Acceptance Criteria

- [ ] 配置 `ROOMUSIC_PUBLIC_URL` 与 `ROOMUSIC_SECURE_COOKIES=true` 后，登录、目录注册和扫描可通过标准反代完成；不匹配来源仍返回 `origin_forbidden`。
- [ ] 执行 dev reset 后，`GET /api/v1/setup/status` 返回 `setup_required: true`，业务数据清理范围和恢复方式有测试覆盖。
- [ ] 一条开发命令启动依赖和两个服务；修改 React 或 Go 源码后分别自动刷新/重启，API 请求和登录会话可用。
- [ ] production 配置拒绝 dev reset，并要求安全 Cookie 与显式可信来源策略。

## Out of Scope

- 不在本阶段引入通用工作流引擎、Redis 队列或完整 Event Sourcing。
- 不在生产环境提供无确认的数据库清空能力。

## Key Decisions

- 保留严格来源匹配，不接受完全关闭校验。
- 通过 `ROOMUSIC_PUBLIC_URL` 和 `ROOMUSIC_SECURE_COOKIES` 在启动时完成反代/TLS 配置。

## Open Questions

- 无。
