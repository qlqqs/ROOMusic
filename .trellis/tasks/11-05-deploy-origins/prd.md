# 来源清单与按来源设置会话 Cookie

父任务：[部署上线](../11-05-deploy-launch/prd.md)（决定 D1、D5 见父任务）。

## 目标

同一个实例同时接受局域网 `http://192.168.50.206:<端口>` 和外部反代的 `https://<域名>`：两种入口都能登录、写操作和退出，
其他来源的写请求仍被拒绝。

## 现状

- `backend/internal/platform/config/config.go:62` 读取 `ROOMUSIC_SECURE_COOKIES`、`ROOMUSIC_PUBLIC_URL`；`:71` 生产模式要求 `SECURE_COOKIES=true`。
- `backend/internal/identity/guard.go:91-123` `RequireSameOrigin`：协议由 `secureCookies` 决定只能是一种；设了 `publicURL` 只认它的主机，否则比较 `request.Host`。
- `backend/internal/identity/guard.go:127`、`:176` 会话 Cookie 的 `Secure` 取全局开关。
- 开发时 Vite（`frontend/vite.config.dev.ts:6-12`）把 `/api` 代理到 8080，不改 Host，`Origin` 是 `http://localhost:5173`；`ROOMUSIC_PUBLIC_URL` 还用于 Vite 的 `allowedHosts`。
- 前端已有 `origin_forbidden` / `origin_required` 的提示（`frontend/src/shared/api/failure-text.ts:21-22`）。

## 需求

| 编号 | 需求 |
| --- | --- |
| O1 | 新增 `ROOMUSIC_ALLOWED_ORIGINS`：逗号分隔的完整来源（`scheme://host[:port]`，scheme 只能是 http 或 https）。启动时校验格式，格式错误拒绝启动；生产模式必须至少有一项 |
| O2 | 配置了清单时，写请求的 `Origin` 必须与清单中某一项完全一致（按 URL 规范比较：协议与主机不区分大小写，默认端口等价），不再看 `request.Host`，不读 `X-Forwarded-*` |
| O3 | 登录成功时，会话 Cookie 的 `Secure` 由这次请求的 `Origin` 决定：https 来源带 `Secure`，http 来源不带；退出时清除 Cookie 用同样规则。其他属性（`HttpOnly`、`SameSite=Lax`、`Path=/`、有效期）不变 |
| O4 | 去掉“生产必须 `ROOMUSIC_SECURE_COOKIES=true`”的硬性要求，改为 O1 的“生产必须配置清单”。`ROOMUSIC_SECURE_COOKIES` 与 `ROOMUSIC_PUBLIC_URL` 在后端的去留、与清单同时出现时的规则由设计确定，但必须只有一种解释并写进合同 |
| O5 | 没有配置清单的非生产环境（开发、测试）行为与现在逐项一致：现有开发流程、Vite 代理、集成测试和 Playwright 不需要改配置 |
| O6 | 被拒绝的来源仍返回现有的 `403 origin_forbidden` / `origin_required`，错误信封不变 |
| O7 | 更新 `.env.example`、README 的配置说明、[身份与成员合同](../../spec/backend/identity-contracts.md) 与 [Core 0 当前运行合同](../../spec/backend/core0-runtime-contracts.md)；`scripts/prod.sh` 的 `SECURE_COOKIES` 检查跟着调整 |

## 验收

- [ ] A1 配置 `http://192.168.50.206:8080,https://music.example.com` 时：两个来源的登录、写操作、退出都成功；http 登录的 `Set-Cookie` 没有 `Secure`，https 登录的有。
- [ ] A2 同一配置下，`http://music.example.com`、`https://192.168.50.206:8080`、其他主机、带路径或用户信息的 `Origin`、缺少 `Origin` 的写请求都被拒绝且不写库。
- [ ] A3 生产模式不配清单或清单格式错误时拒绝启动，错误信息指出配置项；只配 http 来源也能启动。
- [ ] A4 不配清单的开发环境下，现有 `RequireSameOrigin` 与配置的单元测试结论不变；旧模式三条路径（协议开关、`PUBLIC_URL` 主机、`request.Host`）各有正反例。
  （不实际运行 `./scripts/dev.sh`：它会启动用户自己的 `roomusic` 开发库；现有集成测试都在旧模式下运行，已覆盖开发配置。）
- [ ] A5 后端相关单元测试与 identity 相关的 PostgreSQL 集成测试通过；接口 JSON 样本不变。

## 规划裁决

- 设计稿（[design.md](./design.md)）的取舍：有清单时 `ROOMUSIC_SECURE_COOKIES` 出现即拒绝启动，`ROOMUSIC_PUBLIC_URL` 必须在清单内（仍给 Vite 用）；Cookie 由 `SetSessionCookie` / `ClearSessionCookie` 读请求的 `Origin` 决定。主会话 2026-11-05 采纳。

## 范围外

- 前端界面改动（预计不需要）；CORS（同源部署，不跨域）；`__Host-` 前缀或改 Cookie 名。
