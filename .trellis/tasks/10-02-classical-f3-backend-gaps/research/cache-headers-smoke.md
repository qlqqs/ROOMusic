# 缓存头冒烟记录（F3 第 3 步）

> 日期：2026-10-02。目的：在真实二进制上核对 F3 第 3 步的缓存头，补足 Go 单元测试与集成测试覆盖不到的一环——
> 内嵌的前端产物经过真实的 HTTP 栈。只用合成数据，不读取 `music/` 与 `.env`，不碰用户自己的 `roomusic` compose 项目。

## 环境

- 二进制：`go build ./cmd/roomusic`，工作树是第 3 步的代码；内嵌的是已提交的前端产物（`assets/` 下 114 个文件）。
- 数据库：`roomusic-test` compose 项目的 PostgreSQL 18 里的临时库 `f3_cache_smoke`，冒烟结束后删除；服务器日志中没有 ERROR 级别记录。
- 监听 `127.0.0.1:18480`（不占用默认的 8080）；数据目录与音乐根是 `/tmp` 下的临时目录，结束后删除，服务器按 PID 停止。
- 数据：用合成凭据初始化的管理员；一个发行版本与一张 3×2 的合成 PNG 封面（按内容 SHA-256 命名放进数据目录，并写入 `release_artworks`）。

## 结果

`<hash>` 与 `<资源 ID>` 分别是构建产物文件名里的哈希段与封面的存储键（内容哈希加 `.png`）。

| 请求 | 状态 | `Cache-Control` | 说明 |
| --- | ---: | --- | --- |
| `HEAD /` | 200 | `no-cache` | `text/html`，没有 `ETag` |
| `HEAD /albums/example`（SPA 回落） | 200 | `no-cache` | 同入口页 |
| `HEAD /assets/index-<hash>.js` | 200 | `public, max-age=31536000, immutable` | `text/javascript` |
| `HEAD /assets/<字体>-<hash>.woff2` | 200 | `public, max-age=31536000, immutable` | `font/woff2` |
| `HEAD /assets/does-not-exist-AbC12345.js` | 404 | 无 | `text/plain`，没有回落到入口页 |
| `GET /assets/`、`GET /assets` | 404 | 无 | 不列目录 |
| `GET /licenses/` | 200 | `no-cache` | 回落到入口页（`text/html`），不列目录 |
| `HEAD /licenses/README.txt` | 200 | `no-cache` | `text/plain` |
| `HEAD /index.html` | 301 | `no-cache` | `Location: ./`，`http.FileServer` 既有的重定向 |
| `GET /api/v1/auth/me`（未登录） | 401 | `no-store` | `application/json` |
| `GET /api/v1/auth/me`（已登录） | 200 | `no-store` | |
| `GET /api/v1/missing` | 404 | `no-store` | 兜底的 JSON 错误信封 |
| `GET /healthz` | 200 | `no-store` | |
| 封面，未登录，带或不带匹配的 `If-None-Match` | 401 | `no-store` | 带着匹配的 ETag 也不会得到 304 |
| 封面，已登录 | 200 | `private, no-cache` | `image/png`，`ETag: "<资源 ID>"` |
| 封面，已登录，`If-None-Match` 为强标签、弱标签、列表或 `*` | 304 | `private, no-cache` | 同一个 `ETag`，没有响应体 |
| 封面，已登录，`If-None-Match: "stale.png"` | 200 | `private, no-cache` | 标签不匹配，返回完整响应 |
| 未知封面，已登录，带 `If-None-Match` | 404 | `no-store` | 不是 304 |

## 备注

- Go 把响应头名规范化为 `Etag`；HTTP 头名不区分大小写，与 `ETag` 等价。
- 用 `curl` 看 HEAD 响应必须写 `-I`。`-X HEAD` 会让 curl 一直等一个不会到来的响应体，请求挂起（本次冒烟踩到一次，已按 PID 清理）。
- 浏览器端的缓存行为（`private, no-cache` 的重新验证、退出登录后的封面）没有在这里验证：Playwright 跑在 `vite preview` 上，不经过 Go 服务。
  这一环由响应头合同加集成测试保证（`TestPostgreSQLArtworkCachingUsesETagAndRevalidation`）；M1b 的真实接口冒烟会带着封面再走一遍。
