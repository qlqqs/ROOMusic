# 外部接口 mock 服务器

父任务 `11-24-m8-devices-artists-plugins`，决定 W4。技术设计见 `design.md`，执行顺序见 `implement.md`。

## 目标

做一个可以单独运行的假服务，按外部 API 的接口返回响应，开发与试用实例接上它以后不连真实服务、不发外部请求，
并能按需切到超时、503、限流、坏数据、半途断开等情况，看程序在外部服务出问题时的表现。

用户价值：试用自动整理、管家、联网搜索时不依赖网络和真实密钥；M6 没有条件试用的 SearXNG、Tavily 有了可用的端点；
外部服务出故障时的行为可以随时复现，不必等真的出故障。

## 现状（仓库证据）

- 四个外部源的客户端都有 `BaseURL` 选项（`backend/internal/sources/{musicbrainz,coverartarchive,lrclib,acoustid}/client.go`），
  但 `backend/internal/app/app.go:179-191` 组装时没有设置，用的是写死的官方地址；没有环境变量能改。
- 外部源请求一律用 `egress.PublicOnly`（`backend/internal/sources/request.go:290`），本机、局域网、Docker 网络里的地址都会被拒绝。
  Cover Art Archive 只允许跟随到官方主机的重定向（`coverartarchive/client.go:59-62`）。
- 模型端点由用户填写：管理员用 `egress.Unrestricted`，成员用放行名单（`backend/internal/steward/httpapi/outbound.go:21-31`），
  管理员现在就能把端点填成本机地址。三种协议都用流式 SSE：Responses、Chat Completions（`provider/openai.go:66`）、Messages（`provider/messages.go:93`）。
- 联网搜索：SearXNG 的地址由用户填写、用端点客户端（`steward/web/search.go:61`）；Tavily 的地址写死为
  `https://api.tavily.com/search`（`search.go:19`），用 `PublicOnly`（`search.go:83`）。
- 开发环境的后端在宿主机上运行，`compose.dev.yaml` 里只有 PostgreSQL；`Makefile` 有 `dev` 等目标。
- 现有的假服务都在各包的 `_test.go` 里（`httptest.NewServer`），不能单独运行，也不能给开发实例用。

## 需求

- R1 独立程序 `roomusic-upstream-mock`：一个 HTTP 服务，按路径前缀模拟 MusicBrainz、Cover Art Archive、LRCLIB、AcoustID、
  OpenAI 兼容的模型端点（Responses、Chat Completions、模型列表）、Anthropic Messages 端点、SearXNG、Tavily。
- R2 内置一套合成数据（虚构的艺人、发行、录音、ISRC、封面图、同步歌词、指纹对应），覆盖华语与欧美、多碟、单曲；
  可以用 `-data <目录>` 换成自己的数据。不使用真实曲库，也不复制真实的外部数据。
- R3 情景：每个服务可以单独切换为 `ok`、`not_found`、`unavailable`（503 加 `Retry-After`）、`rate_limited`（429/503 按该服务的实际做法）、
  `timeout`（一直不回，直到客户端放弃）、`slow`（延迟后正常返回）、`malformed`（坏 JSON 或坏图片）、`drop`（响应写到一半断开；
  模型端点是流到一半断开）。启动参数可以设初始情景，运行中用控制接口切换。
- R4 控制接口（`/_mock/` 下）：查看与修改各服务的情景、查看最近收到的请求（方法、路径、查询、时间，不记录密钥的值）、重置。
- R5 模型端点：三种协议都按流式返回；能通过后端的端点检测（包括工具调用能力）；按脚本回复——默认回复一段固定文字，
  脚本可以按最后一条消息或工具结果的内容给出文字或工具调用，让管家对话、自动整理的模型层与独立审查都能走通。
- R6 后端开关：环境变量 `ROOMUSIC_UPSTREAM_MOCK_URL`（绝对的 http/https 地址）。设了以后：四个外部源与 Tavily 改指 mock 的对应前缀，
  出站策略只额外放行这一个主机和端口；没设时行为与现在逐项相同。设了时启动日志写一条警告，写明所有外部源都指向这个地址。
  模型端点与 SearXNG 仍由用户在设置里填 mock 的地址，不受这个变量影响。
- R7 运行方式：`make mock-upstream` 在宿主机启动（监听地址与端口可配）；`docs/development-environment.md` 写明怎么让开发实例接上它、怎么切换情景。

## 验收

- A1 设了 `ROOMUSIC_UPSTREAM_MOCK_URL` 的后端：打开自动整理后，扫描带合成数据 MBID 的曲库，发行被匹配、封面与歌词补上、
  AcoustID 按指纹命中；mock 的请求记录里能看到这些请求；没有发往官方地址的请求。
- A2 没设这个变量时：外部源仍指向官方地址，`PublicOnly` 不变，既有测试结论不变。
- A3 各情景下后端的表现与 `sources-contracts.md` 一致：503 与限流按 `Retry-After` 退避后再试、断网与超时记为“稍后再试”、坏数据不写入；
  由自动化测试覆盖（mock 以进程内方式启动）。
- A4 模型端点：后端检测在三种协议下都通过；按脚本完成一次带工具调用的对话；`drop` 情景下运行以错误结束，不留半截写入。
- A5 SearXNG 与 Tavily：管家联网搜索两种都能返回 mock 的结果。
- A6 控制接口能切换情景并立即生效；请求记录不含密钥的值。
- A7 mock 程序自身的测试覆盖每个服务的路由、每种情景与控制接口。

## 不做

- 艺人资料来源（Wikidata、Commons、维基百科、Fanart.tv、TheAudioDB）：由 `11-24-m8-artist-info` 在本程序里加路由。
- 网页读取（任意网址）不模拟，仍走 `PublicOnly`。
- 生成与合成数据匹配的音频文件；真实声学指纹。
- 前端改动；生产镜像不带 mock 程序。

## 规划决定

路线：人工选择，qlqq 于 2026-11-24 预先授权“规划完直接实现”（父任务 W1）；以下默认值由主代理取定，交付时列出。

| 编号 | 决定 | 理由 |
| --- | --- | --- |
| K1 | 一个程序、按路径前缀分服务，而不是每个服务一个端口 | 只要一个地址就能接上；控制接口与请求记录集中 |
| K2 | 后端只认一个变量 `ROOMUSIC_UPSTREAM_MOCK_URL`，所有环境都可用，启动时警告 | 试用实例按生产配置运行（V12 就是），只在开发环境可用会挡住试用 |
| K3 | 只放行 mock 这一个主机和端口，其他地址仍按原策略 | 不因为接了 mock 就放开内网访问 |
| K4 | 合成数据是虚构内容 | 测试只用合成数据；不复制外部服务的真实数据 |
| K5 | 程序放在 `backend/cmd/roomusic-upstream-mock`，实现放在 `backend/internal/upstreammock` | 与现有 `cmd/roomusic-smoke` 同样的组织；复用后端的 Go 工具链 |
