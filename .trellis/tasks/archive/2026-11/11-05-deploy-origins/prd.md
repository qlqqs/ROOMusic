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
| O1 | 新增 `ROOMUSIC_ALLOWED_ORIGINS`：逗号分隔的完整来源（`scheme://host[:port]`，scheme 只能是 http 或 https）。启动时校验格式，格式错误拒绝启动；~~生产模式必须至少有一项~~（已由 O8 取代：生产可以不配） |
| O2 | 配置了清单时，写请求的 `Origin` 必须与清单中某一项完全一致（按 URL 规范比较：协议与主机不区分大小写，默认端口等价），不再看 `request.Host`，不读 `X-Forwarded-*` |
| O3 | 登录成功时，会话 Cookie 的 `Secure` 由这次请求的 `Origin` 决定：https 来源带 `Secure`，http 来源不带；退出时清除 Cookie 用同样规则。其他属性（`HttpOnly`、`SameSite=Lax`、`Path=/`、有效期）不变 |
| O4 | 去掉“生产必须 `ROOMUSIC_SECURE_COOKIES=true`”的硬性要求，改为 O1 的“生产必须配置清单”（后者已由 O8 取代）。`ROOMUSIC_SECURE_COOKIES` 与 `ROOMUSIC_PUBLIC_URL` 在后端的去留、与清单同时出现时的规则由设计确定，但必须只有一种解释并写进合同 |
| O5 | 没有配置清单的非生产环境（开发、测试）行为与现在逐项一致：现有开发流程、Vite 代理、集成测试和 Playwright 不需要改配置 |
| O6 | 被拒绝的来源仍返回现有的 `403 origin_forbidden` / `origin_required`，错误信封不变 |
| O7 | 更新 `.env.example`、README 的配置说明、[身份与成员合同](../../../../spec/backend/identity-contracts.md) 与 [Core 0 当前运行合同](../../../../spec/backend/core0-runtime-contracts.md)；`scripts/prod.sh` 的 `SECURE_COOKIES` 检查跟着调整 |
| O8 | （D8，2026-11-05 增补）生产不配清单时改走“主机一致模式”，不再拒绝启动：写请求的 `Origin` 与请求 `Host` 规范化后一致就放行，协议不限；Cookie 的 `Secure` 跟随 `Origin` 协议；仍不读 `X-Forwarded-*` 与 `Forwarded`。清单保留为可选配置，配了就按清单。各种配置组合只有一种行为（见 design §9.1），O5 不变。生产 Compose 与模板中清单改为可选，部署说明要求反代保留浏览器的 Host 与 Origin |

## 验收

- [x] A1 配置 `http://192.168.50.206:8080,https://music.example.com` 时：两个来源的登录、写操作、退出都成功；http 登录的 `Set-Cookie` 没有 `Secure`，https 登录的有。
- [x] A2 同一配置下，`http://music.example.com`、`https://192.168.50.206:8080`、其他主机、带路径或用户信息的 `Origin`、缺少 `Origin` 的写请求都被拒绝且不写库。
- [x] A3 清单格式错误时拒绝启动，错误信息指出配置项；只配 http 来源也能启动。（原“生产不配清单拒绝启动”已由 O8 / A6 取代。）
- [x] A4 不配清单的开发环境下，现有 `RequireSameOrigin` 与配置的单元测试结论不变；旧模式三条路径（协议开关、`PUBLIC_URL` 主机、`request.Host`）各有正反例。
  （不实际运行 `./scripts/dev.sh`：它会启动用户自己的 `roomusic` 开发库；现有集成测试都在旧模式下运行，已覆盖开发配置。）
- [x] A5 后端相关单元测试与 identity 相关的 PostgreSQL 集成测试通过；接口 JSON 样本不变。

- [x] A6 生产不配清单时：`http://192.168.50.206:18090`（Host `192.168.50.206:18090`）与 `https://music.qlqqs.com:8888`（Host `music.qlqqs.com:8888`）
  两个来源的登录、写操作、退出都成功，http 的 `Set-Cookie` 没有 `Secure`、https 的有；Origin 与 Host 不一致、`Origin: null`、缺少 Host 时
  `403 origin_forbidden` 且不写库，缺少 Origin 时 `403 origin_required`；带 `X-Forwarded-Host`、`X-Forwarded-Proto` 或 `Forwarded` 不改变结论；
  生产同时设置 `ROOMUSIC_SECURE_COOKIES` 或 `ROOMUSIC_PUBLIC_URL` 时拒绝启动；`docker compose ... --env-file .env.prod.example config --quiet` 仍通过。

## 规划裁决

- 设计稿（[design.md](./design.md)）的取舍：有清单时 `ROOMUSIC_SECURE_COOKIES` 出现即拒绝启动，`ROOMUSIC_PUBLIC_URL` 必须在清单内（仍给 Vite 用）；Cookie 由 `SetSessionCookie` / `ClearSessionCookie` 读请求的 `Origin` 决定。主会话 2026-11-05 采纳。
- 增补（O8，design §9）：模式由 `config` 一次算出（`legacy` / `allow-list` / `same-host`）；生产不配清单时出现 `ROOMUSIC_SECURE_COOKIES` 或 `ROOMUSIC_PUBLIC_URL` 即拒绝启动；拒绝来源时写 `identity.origin.rejected` 诊断日志（§9.5）；packaging design 的相关段落一并改为“不填清单”。主会话 2026-11-05 采纳。

## 实施结果（2026-11-05）

- 提交：`db4b4f3`（清单模式与按来源设置 Cookie）、`06450d0`（主机一致模式，D8 增补）、`a931fb5`（登记 `platform/weborigin` 的包依赖规则，收尾门禁发现）。
- A1、A2：`identity_api_integration_test.go` 的 `TestPostgreSQLAllowedOrigins*`；A6：`TestPostgreSQLSameHost*`（两个来源登录、改密码、退出，Secure 区分；Host 不一致、`Origin: null`、缺 Host 均 403 且不写库；转发头不改变结论），隔离库 PASS 非 SKIP。
- A3、A4：`config_test.go` 的 `TestLoadOriginModeMatrix`、`TestLoadOriginValidationOrder` 覆盖 design §9.1 全表；旧模式既有用例未改。
- A5：接口 JSON 样本未变；来源合同拆为 [identity-origin-contracts.md](../../../../spec/backend/identity-origin-contracts.md)。
- 生产实测：正式实例按主机一致模式运行，Host 一致的登录请求得 401（进入密码核对），Origin 与 Host 不一致得 `403 origin_forbidden` 并记录 `identity.origin.rejected mode=same-host`（[正式启动记录](../11-05-deploy-packaging/research/launch.md)）。
- 审阅补充：`OriginMode` 零值按旧模式装配（design §9.2），直接构造 `config.Server{}` 的测试夹具不必改；清单模式的集成夹具显式写模式。

## 范围外

- 前端界面改动（预计不需要）；CORS（同源部署，不跨域）；`__Host-` 前缀或改 Cookie 名。
