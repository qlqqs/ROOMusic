# 设计

路径相对 `backend/`。

## 存储

- 迁移 `0095_outbound_proxy`：新建单行表 `outbound_proxy_settings`，字段如下：
  - `id smallint PRIMARY KEY CHECK (id=1)`；
  - `enabled bool`；
  - `scheme text CHECK (scheme IN ('http','socks5'))`；
  - `host text`、`port int CHECK 1..65535`；
  - `username text`；
  - `password_ciphertext bytea`，可空；
  - `version bigint`、`updated_at`、`updated_by`。
- 密码用 `platform/secret` 加密，`purpose` 写 `outbound_proxy`，`userID` 写实例常量。表里只存密文。

## 出站客户端

- `platform/egress` 新增进程级代理配置：
  - 用 `atomic.Pointer` 保存，由 `egress.SetProxy(*ProxyConfig)` 设置，传 `nil` 表示关闭；
  - `egress` 不依赖设置包，由 app 层在启动和保存设置时调用；
  - `Options` 不变，调用点不需要改动。
- `policyTransport.RoundTrip` 决定本次请求是否经代理：
  1. 没有配置代理，或者目标是上游模拟服务的主机，就直连；
  2. 目标是 IP 字面量：非公网则直连；公网则经代理；
  3. 目标是主机名：先在本地解析。全部为非公网地址时直连（这时仍按原策略检查）；否则经代理。
  4. 解析失败时，按原策略的公网检查规则决定：`Unrestricted` 直接交给代理去解析；其它策略仍按现有规则报错。
- 经代理的请求：
  - 目标策略检查在 `RoundTrip` 里用本地解析结果完成，对应 PRD R4；
  - 拨号阶段不再检查代理本身的地址；
  - 用请求上下文标记“本次经代理”，传给拨号。
- **HTTP 代理**：`http.Transport.Proxy` 读取上下文标记，返回带 userinfo 的代理 URL。Transport 自己完成 CONNECT 和 `Proxy-Authorization`。拨号函数遇到“连接代理”时跳过目标策略检查。
- **SOCKS5**：拨号函数用 `golang.org/x/net/proxy` 的 SOCKS5 拨号器（仓库已依赖 `x/net`），把主机名直接交给代理（远端解析）。
- 代理配置变化时，递增代次并关闭已登记 Transport 的空闲连接。登记表用弱引用或在 `NewClient` 返回的客户端上登记，避免泄漏。
- 错误分类：新增 `CodeProxyUnavailable`（拨号或 CONNECT 失败）和 `CodeProxyAuth`（407 或 SOCKS 认证失败）。`sources/request.go` 把这两类按暂时网络错误计入现有退避，对应合同 25.3。

## 接口（管理员）

| 方法与路径 | 说明 |
| --- | --- |
| `GET /api/v1/admin/network/proxy` | `{enabled, scheme, host, port, username, password_set, version}` |
| `PUT /api/v1/admin/network/proxy` | 请求体同上，另加可选的 `password`：省略表示不改，`""` 表示清除。必须带 `version` 做乐观锁，冲突返回 409。开启时主机和端口必填，缺了返回 400 |
| `POST /api/v1/admin/network/proxy/test` | 请求体为待测配置；`password` 省略时用已存的密码。返回 `{ok, code, latency_ms}`，`code` 取 `proxy_unreachable`、`proxy_auth_failed`、`target_unreachable`、`timeout` 之一。测试目标固定为 `https://musicbrainz.org/`，超时 10 秒，不跟随重定向，只看是否拿到响应 |

- 保存成功后，app 层调用 `egress.SetProxy`。启动时从库里读取并设置。
- 诊断导出与日志不含密码，代理地址也不带 userinfo。

## 前端（主代理实现）

- 在设置“管理”分组里新增“网络”栏目，路由 `/settings/network`，图标用 `Network`。
- 表单内容：
  - 开关；
  - 类型分段选择（HTTP、SOCKS5）；
  - 主机与端口；
  - 用户名；
  - 密码：已设置时显示占位并提供清除按钮。
- 底部两个按钮：
  - 测试：用图标按钮，结果在行内用图标和文字提示；
  - 保存。
- 解码器、请求、查询键沿用 `features/*/api` 的现有写法，并有单元测试。

## 风险

- **DNS 重绑定**：本地检查和代理端解析可能不一致。这只影响 `PublicOnly` 下的成员网页读取，与现在直连时的检查-使用间隙同类，记为已知局限。
- **局域网代理**：代理地址本身是局域网地址时属于正常情况，拨号到代理不做目标检查。
