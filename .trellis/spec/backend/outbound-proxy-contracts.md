# 全局出站代理合同

## 适用范围

来源、模型、管家网页和匿名统计通过 `egress.NewClient` 使用同一份实例配置。局域网设备使用各自的传输。进程启动先读取代理设置，再启动会发起外部请求的服务。

## 设置与接口

迁移 `0095_outbound_proxy.sql` 创建并初始化单行 `outbound_proxy_settings`。初始版本为 1，代理关闭，类型为 `http`，主机和用户名为空，端口为 `null`。回退删除该表。

三个接口均需要管理员会话。PUT 与 POST 还检查同源。

| 方法 | 路径 | 请求和成功响应 |
| --- | --- | --- |
| GET | `/api/v1/admin/network/proxy` | 返回 `{enabled,scheme,host,port,username,password_set,version}` |
| PUT | `/api/v1/admin/network/proxy` | 接收 `enabled,scheme,host,port,username,version` 和可选 `password`。返回保存后的 GET 结构 |
| POST | `/api/v1/admin/network/proxy/test` | 接收草稿的 `scheme,host,port,username` 和可选 `password`。固定 GET `https://musicbrainz.org/`，10 秒超时。收到目标响应头即结束，包括重定向。返回 `{ok,code,latency_ms}`，成功时 `code` 为 `null` |

`password` 省略时沿用原密码，空字符串表示清除，新值经 `platform/secret` 加密，关联数据为用户标识 `instance` 和用途 `outbound_proxy`。测试草稿使用单独的传输，已存设置和活动连接池只在保存时更新。读取设置只暴露 `password_set`。

## 请求与连接

`egress.SetProxy(*ProxyConfig)` 发布不可变快照，`nil` 关闭代理。每个请求以及重定向的每一跳均选择路由。按配置快照分隔代理连接池，切换时关闭已登记客户端的空闲连接，短命客户端通过弱引用登记。

| 目标 | 路由 |
| --- | --- |
| 已配置上游模拟服务的主机和有效端口 | 按原策略直连 |
| 非公网 IP 字面量，或本地解析结果全部为非公网地址 | 按原策略直连，通过检查后使用本次解析的地址拨号 |
| 公网地址，或含公网 IP 的解析结果 | 目标通过策略检查后，经代理连接 |
| 本地解析失败 | `Unrestricted` 将域名交给代理，其他策略返回本地解析错误 |

`PublicOnly` 拒绝含禁用地址的整组结果，`PublicOrAllowlist` 的放行仍按目标主机和有效端口匹配。HTTP 目标用 HTTP 代理的绝对 URI，HTTPS 用 CONNECT。SOCKS5 的连接命令保留目标域名。直连与代理都不读取环境代理变量。

## 校验与错误

| 条件 | 结果 |
| --- | --- |
| 开启或测试时缺主机或端口，类型不在 `http/socks5`，端口超出 1–65535，保存缺版本 | 400 |
| 提交的版本过期 | 409 `version_conflict` |
| 代理连接失败、握手无法建立 | 测试结果 `proxy_unreachable` |
| HTTP 407 或 SOCKS5 认证被拒 | 测试结果 `proxy_auth_failed` |
| 代理报告目标不可达，例如 CONNECT 502，或目标 TLS 失败 | 测试结果 `target_unreachable` |
| 超时 | 测试结果 `timeout` |

来源层将 `CodeProxyUnavailable` 和 `CodeProxyAuth` 交给已有网络错误重试及 `source_unavailable` 退避。日志只记固定错误码，诊断不收集代理草稿、密码、认证头或含凭据的地址。

## 案例与验收

- 已创建的客户端先直连，保存新代理后下一次公网请求命中新代理。关闭后恢复直连。
- 本地代理监听回环，网页目标域名解析为公网地址时允许连接该代理。若网页目标本身解析为回环，返回 `CodeAddressNotAllowed`，代理计数保持零。
- 对 HTTP 和 SOCKS5 分别用有认证和无认证的合成代理验收。核实 CONNECT 目标和 SOCKS5 域名字段，检查 TLS 隧道中的目标请求未携带代理认证头。
- 在隔离数据库核对密码密文、留空、清除、冲突、重启加载和草稿测试。整理队列遇到代理拒绝时应延后，并保留待整理状态。
- 把代理拨号地址送入 `PublicOnly` 会错误拒绝局域网代理。地址策略须检查原请求目标，代理连接由单独的传输建立。
