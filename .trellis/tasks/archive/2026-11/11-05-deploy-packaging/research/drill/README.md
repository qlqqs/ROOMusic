# 上线演练记录

日期：2026-11-05（UTC）。执行者：测试 agent。范围：`implement.md` 第 3 步、`design.md` §10.1；不执行正式启动。

## 边界与状态

- 开始时：`main`，HEAD `263d75709b2b2fe8fc0ed79e8f7ef824eb3dba47`，工作区干净。
- 仅使用 Compose 项目 `roomusic-drill`、子网 `10.203.51.0/24`、应用端口 `18090`、临时 TLS `127.0.0.1:18443`。
- 开发容器/卷只读核对；不读取 `.env`、`.env.dev`，不创建 `.env.prod`，不启动正式项目，不提交 Git。
- 凭据只保存在权限受限的 `/tmp/roomusic-drill/`；记录不得含密码、Cookie 值、令牌或密钥。截图遮挡真实音乐内容及不必要元数据。
- 部署或应用问题立即停止；用户授权后，测试 agent 自编检查脚本的错误可修正、注明后继续。第 8.3 步必须保留实例并暂停。
- 当前状态：**用户于 2026-11-05 决定结束演练，第 9–12 步跳过；第 13 步清理、第 14 步开发库核对均通过。演练资源已移除，记录保留。第 8.3 步按实际路由器链路收口：真实对端为 `192.168.50.1`，无需 host 网络；另一台局域网设备直连仍未实测，不冒充已验证。下文历次暂停与等待均为历史记录，以末尾小结为准。**

## 结果索引

| 步骤 | 状态 | 证据 |
| --- | --- | --- |
| 0 开发库基线 | 通过 | `step-00.py`、`step-00.log`、`step-00-baseline.json` |
| 1 构建与启动 | 通过 | `step-01-build.log`、`step-01-start.log` |
| 1b main/分支/脏工作区隔离 | 修正测试断言后重跑通过 | `step-01b-rerun.log`（首次错误记录仍保留） |
| 2 镜像与自检 | 通过 | `step-02.log` |
| 3 初始化管理员 | 通过 | `step-03-rerun.log`、`step-03-setup.png`、`step-03-created.png` |
| 4 添加目录与扫描 | 通过 | `step-04.log`、`step-04-scan.png` |
| 5 FLAC/ALAC 播放 | 通过 | `step-05.log`、`step-05-flac.png`、`step-05-alac.png` |
| 6 临时 TLS | 通过 | `step-06.log` |
| 7 双来源与拒绝未知来源 | 通过 | `step-07.log`、`step-07-origin.log`、`step-07-*-favorite.png`、`step-07-*-logout.png` |
| 8.1–8.2 限速与来源隔离 | 通过 | `step-08.log`、`step-08-http-throttled.png`、`step-08-https-logged-in.png` |
| 8.3 真实对端来源 | **路由器链路通过：对端/source 为 `192.168.50.1`，不需要 host 网络；A 仅宿主机替代** | `step-08-real-ab-result.json`、`step-08-local-a.log`、`step-08-local-a.png`、`capture/step-08-real-request-headers.jsonl` |
| 9–12 重启、备份恢复、封面卷 | 用户决定跳过（2026-11-05） | 本文末尾小结；不得记为通过 |
| 13 清理 | 通过 | `step-13.py`、`step-13-cleanup.log`、`step-13-result.json` |
| 14 开发库只读核对 | 通过，全部与基线一致 | `step-14.py`、`step-14.log`、`step-14-result.json` |

## 命令约定

从仓库根目录执行；以下 `$D` 表示 Bash 数组 `D` 的展开 `"${D[@]}"`：

```bash
D=(docker compose -p roomusic-drill -f deploy/production/compose.yaml --env-file /tmp/roomusic-drill/drill.env)
```

不执行全量 Go/前端/集成测试：本轮是有明确步骤的部署演练，不修改应用代码，按任务要求仅验证相关路径。

## 步骤 0：通过

命令：`python3 research/drill/step-00.py`（实际路径见日志）。状态 running，迁移最大版本 12，用户 1，专辑 68；启动时间与卷创建时间见 `step-00-baseline.json`。环境无 ROOMUSIC/COMPOSE 残留。

## 步骤 1：通过

实际命令：

```bash
BUILDKIT_PROGRESS=plain /usr/bin/time -f '构建耗时：%e 秒' scripts/production.sh build-image --prefix roomusic-drill
docker image inspect -f '{{join .RepoTags ","}} size_bytes={{.Size}} revision={{index .Config.Labels "org.opencontainers.image.revision"}}' roomusic-drill-app:current roomusic-drill-tools:current
"${D[@]}" config --quiet
"${D[@]}" up -d --wait
"${D[@]}" ps
```

- 构建提交：`263d75709b2b2fe8fc0ed79e8f7ef824eb3dba47`，与 `main` 一致；只从提交导出，记录产生的未跟踪文件不进入镜像。
- 耗时：26.73 秒（包含构建缓存复用，不是无缓存构建）。tools 上下文 10.43 kB，app 上下文 14.53 MB；与实施记录约 14.49 MB 接近，但不是严格的“几 MB”。没有调用 Node。
- app 大小 71,658,714 字节；tools 大小 111,970,423 字节；两者 revision 正确。
- 配置校验退出 0；app、postgres、backup 全部 healthy；应用仅发布 `0.0.0.0:18090`，数据库没有发布宿主机端口。
- 完整输出：`step-01-build.log`、`step-01-start.log`。

## 步骤 1b：首次测试脚本断言失败（历史记录，后已获准修正并重跑）

执行命令：

```bash
set -euo pipefail
bash .trellis/tasks/11-05-deploy-packaging/research/drill/step-01b.sh 2>&1 | tee .trellis/tasks/11-05-deploy-packaging/research/drill/step-01b.log
```

脚本按设计在 `/tmp/roomusic-drill/clone` 创建 `drill-side`，仅在此临时 clone 提交 `LABEL drill.marker=branch`，再加未提交的 `drill.marker=dirty`。
主仓库未提交、未切分支，仍是原始 `main`/HEAD。

默认构建成功，读取输出为：

```text
命令：docker image inspect（revision、drill.marker）
263d75709b2b2fe8fc0ed79e8f7ef824eb3dba47
```

失败断言（实际退出 1）：

```bash
test "$(docker image inspect -f '{{index .Config.Labels "drill.marker"}}' roomusic-drill-c6-app:current)" = '<no value>'
```

### 判断

这是**本次测试脚本的错误预期**，不能报告成产品缺陷：当前 Docker 模板查询缺失标签返回空串，设计要求也是“marker 为空”，脚本却误期待字面量 `<no value>`。
停止后只读检查 `.Config.Labels` 已确认 `drill.marker` 键不存在，revision 与 `main` 一致。
因此默认构建排除其他分支和未提交改动的这部分证据符合预期，但整个 1b 尚未通过：显式 `--ref drill-side`、不存在 ref 的退出 2 与临时资源清理均未执行。

遵守“任何一步失败就停下”的约束，没有修复测试脚本并重跑，没有修改应用、部署文件或任务规划。
建议主会话允许把上述断言改为检查空串或键不存在后，从 1b 的剩余检查继续。

### 首次停止时的现场与后续（历史状态，当前状态见结果索引）

- `roomusic-drill` 的 app、postgres、backup 保持 healthy；未初始化管理员、未扫描真实音乐。
- `roomusic-drill-c6-app/tools` 的 `current` 与 `263d75709b2b` 标签保留；临时 clone 保留在 `/tmp/roomusic-drill/clone`。
- `/tmp/roomusic-drill/drill.env` 与管理员凭据文件均为 600；此目录为 700，未将内容复制进记录。
- 第 2–14 步未执行，TLS 尚未启动，没有截图（尚未进入浏览器步骤）。
- **尚未到达 8.3，本次不需要用户使用手机/另一台电脑输错密码。**
- 恢复演练时仍必须在 8.3 再次暂停；本次未执行第 14 步，不冒充最终开发库核对通过。
- 没有对开发容器或开发卷执行任何写入、停止、重启、删除操作；没有执行任何 down、删卷或删镜像命令；没有创建正式生产配置或实例。
- 停止后的状态证据：`step-01b-stopped.log`。

## 续跑授权与检查脚本修正

用户明确允许把缺失标签按空串判断，并重跑 1b；后续仅测试脚本自身错误可以注明后修正继续，部署/应用问题仍需停止。
`step-01b.sh` 将 `<no value>` 的错误预期改为空串；临时目录删除不使用强制参数。清除本轮拥有的 `/tmp/roomusic-drill/clone` 后从临时 clone 创建重新执行；不改变主仓库分支、HEAD 或部署代码。原始失败记录保留，续跑日志为 `step-01b-rerun.log`。

## 步骤 1b 续跑：通过

命令：`bash .trellis/tasks/11-05-deploy-packaging/research/drill/step-01b.sh`，输出见 `step-01b-rerun.log`。

- 默认 `main` 构建 revision 为 `263d75709b2b2fe8fc0ed79e8f7ef824eb3dba47`，marker 为空；复用已有合格镜像。
- 显式 `--ref drill-side` revision 为临时 clone 的 `e030d13ab5f9d7b02f808aaa8feb97c9a2984fb7`，marker 为 `branch`，未带入未提交的 `dirty`。
- `--ref no-such-ref` 如预期提示“找不到提交”，退出 2。
- 按标签删除前逐项检查仅为 `roomusic-drill-c6-app/tools:*`，未使用强制删除或镜像 ID；临时 clone 已删除。
- 主仓库始终停留原 `main` / HEAD，不提交主仓库改动。

## 步骤 2：通过

实际执行（独立镜像检查额外加 `--network none`，避免引入自动分配子网）：

```bash
docker run --rm --network none --entrypoint /usr/local/bin/ffmpeg roomusic-drill-app:current -hide_banner -version
"${D[@]}" exec -T app id
"${D[@]}" exec -T app id -u
"${D[@]}" logs --no-color app | grep '"event":"playback.derivation.ready"'
docker run --rm --network none --entrypoint ls roomusic-drill-tools:current /opt/roomusic
docker run --rm --network none -e TZ=Asia/Shanghai --entrypoint date roomusic-drill-tools:current
```

ffmpeg 首行为 7.1.1，应用 uid/gid 均为 1000，日志有 `playback.derivation.ready`，tools 有 `backup.sh`、`restore.sh`，上海时区显示 CST。

## 浏览器执行约定与环境问题

脚本均位于本目录，没有放进 `frontend/`。使用已安装的项目 Playwright 模块，不新增依赖、不修改系统库。每次浏览器运行均在同一 shell 先加载共享库：

```bash
source /home/qlqq/.local/share/playwright-deps/env.sh
node .trellis/tasks/11-05-deploy-packaging/research/drill/step-03.cjs
node .trellis/tasks/11-05-deploy-packaging/research/drill/step-04.cjs
node .trellis/tasks/11-05-deploy-packaging/research/drill/step-05.cjs
node .trellis/tasks/11-05-deploy-packaging/research/drill/step-07.cjs
node .trellis/tasks/11-05-deploy-packaging/research/drill/step-08.cjs
```

以上逐步执行，各自日志独立，并非一条命令无条件跑完。另对 5/7/8 的脚本执行了 `node --check` 语法预检。

第 3 步第一次运行在导航阶段超时（`step-03.log`）；`curl --noproxy '*'` 和 Playwright 的 API 请求均返回 200，但 Chromium 导航被继承的宿主机代理转发。
无凭据网络诊断确认是浏览器检查环境问题，不是应用/部署问题。仅在 `browser-common.cjs` 启动 Chromium 子进程时清掉继承的 `*_proxy` 变量，保留其他环境和已加载的动态库路径。
诊断中曾试用 `direct://` 上下文代理，得到 `ERR_PROXY_CONNECTION_FAILED`，随即移除；最终方案不设置新代理、不修改宿主机代理或部署配置。
然后第 3 步重跑通过，见 `step-03-rerun.log`。诊断原始网络日志留在 `/tmp/roomusic-drill/`，不提交。

## 步骤 3–4：通过

- 空库 `setup_required=true`，页面初始化管理员成功：`POST /setup/admin = 201`，`GET /auth/me = 200`。
- 用真实页面添加 `/music`：201；开始扫描：202；扫描约 20.131 秒后 `succeeded`、报告 `ready`。
- 共 68 个发行、68 个专辑组；报告显示发现/处理 292 个文件、新增 284 条曲目。
- 诊断汇总：100 条 `cue_reference`（页面提示“至少 100 条”，可能截断）。扫描仍完整成功；未展开/保存真实文件明细，不把“扫描成功”写成“无诊断”。未修改真实音乐文件。
- 会话、目录与曲目选择标识只存 `/tmp/roomusic-drill/`，不记录密码、Cookie 或可识别媒体列表。

## 步骤 5：通过

通过专辑页真实按钮启动播放，透明观测原生 `HTMLMediaElement`：仍调用原生 `play()`，不伪造网络、解码、事件或时间，不录音、不复制媒体。

| 源编码 | 预检 | 音频响应 | 真实媒体时间 | 结果 |
| --- | --- | --- | --- | --- |
| FLAC | 200 / original | 206 / audio/flac | 0 → 2.862251 秒 | 正在播放，无媒体错误 |
| ALAC | 200 / derived | 206 / audio/flac | 0.012008 → 2.847186 秒 | 正在播放，无媒体错误 |

同时断言媒体推进不超过真实墙钟时间的容许误差；ALAC 正在播放页服务端栏包含“无损转换”。两首播放后均暂停。
截图在编码前由 Playwright 遮罩主内容、播放器身份区域与图片，保留控制按钮、格式、进度和播放状态，不保存未遮罩原图。

## 步骤 6：通过

```bash
docker run -d --name roomusic-drill-tls --network host \
  -v /tmp/roomusic-drill/Caddyfile:/etc/caddy/Caddyfile:ro caddy:2-alpine
curl --noproxy '*' -k --silent --show-error --fail --retry 15 --retry-delay 1 --retry-connrefused https://localhost:18443/readyz
ss -lnt '( sport = :18443 )'
```

Caddyfile 严格采用设计配置（仅 `bind 127.0.0.1`，上游 `127.0.0.1:18090`，`tls internal`）；没有将 TLS 端口暴露到局域网。
镜像首次下载；启动就绪轮询第一次连接被拒、重试后立即返回 `{"status":"ready"}`，不是持续故障。容器 running，实际监听 `127.0.0.1:18443`。
临时证书/密钥不导出、不写记录。

## 步骤 7：通过（X2、X3 的来源部分）

两个入口分别使用全新无会话浏览器，经页面登录、各收藏一张不同专辑、退出，均返回 200。
收藏回执 `favorited=true`、按钮 `aria-pressed=true`；退出后 Cookie 不存在。HTTPS 上游通过 Caddy，Playwright 仅对此临时证书忽略校验。

| 来源 | Cookie Secure | HttpOnly | SameSite | Path |
| --- | --- | --- | --- | --- |
| http://192.168.50.206:18090 | false | true | Lax | / |
| https://localhost:18443 | true | true | Lax | / |

未记录 Cookie 值。收藏截图只截心形按钮，退出截图遮挡密码输入框。
未知来源使用无凭据写请求实测：

```bash
curl --noproxy '*' --silent --show-error --max-time 10 \
  -o /tmp/roomusic-drill/evil-origin.json -w '%{http_code}' \
  -X POST -H 'Origin: http://evil.example' -H 'Content-Type: application/json' \
  --data '{}' http://192.168.50.206:18090/api/v1/auth/login
```

实际 403，错误码 `origin_forbidden`，提示“请求来源不受信任”。证据 `step-07-origin.log`。

## 步骤 8.1–8.2：通过（X3 的限速部分）

- HTTP 同用户名 `drill-admin` 连错五次：每次 401 / `invalid_credentials`；第六次 429 / `too_many_attempts`，`Retry-After: 60`。
- 页面精确显示“登录失败次数太多，请过几分钟再试。”，截图 `step-08-http-throttled.png`。
- HTTP 锁仍有效时，经 HTTPS 故意错一次以取得来源（401），随后同一用户名正确登录 200、Secure Cookie 正常。
- HTTPS 成功后，再经 HTTP 用正确密码仍为 429 / `Retry-After: 59`：证明成功只清本组合、不清另一来源。
- 应用日志 `identity.login.throttled` 的 HTTP source 为 **192.168.50.206**；TLS 探针 `identity.login.failed` source 为 **10.203.51.1**。记录通过请求 ID 与探针关联，不读取转发头推断来源。
- 仅按字段白名单记录登录事件，不记录请求体、Cookie、密码。完整状态码与日志证据见 `step-08.log`。

## 步骤 8.3：强制暂停，等待用户

暂停时间：**2026-11-05 15:22:20 UTC**（后续从此时间之后核对登录失败日志）。

请用户：

1. 用连接同一局域网的手机或另一台电脑（不是本机）打开 **http://192.168.50.206:18090**；不要走 TLS 入口、蜂窝网络、VPN 或代理。
2. 用户名填 **drill-admin**，密码随意填一个故意错误的值，点击“登录”**一次**。不需要知道正确密码，也不要把输入的密码回传。
3. 告知操作完成、页面提示，最好附该设备的局域网 IPv4 地址和操作时间。

收到反馈与继续指令后，先查 `identity.login.failed`：source 必须等于设备的 `192.168.50.x`，且不是宿主机 `.206` 或网关 `10.203.51.1`；之后再执行第 9–14 步。

### 保留的运行现场

- app、postgres、backup 全部 healthy，TLS running；`step-08-pause-status.log` 有只读状态。
- 仅演练的 `roomusic-drill_app-data`、`roomusic-drill_postgres-data` 和固定子网；c6 标签/clone 已清理。
- 管理员已初始化，68 张专辑已扫描，两个专辑收藏用于后续备份恢复验证；HTTPS 已登录状态保存在 `/tmp/roomusic-drill/https-state.json`（600），供重启恢复步骤使用。
- 没有执行第 9–14 步，没有触碰开发容器/卷的运行状态和数据，没有创建 `.env.prod` 或正式实例，没有提交主仓库 Git。
- 暂停不代表 X4/X5 或清理/最终开发库核对通过；这些仍待续跑。第 12 步封面结论未验证，因此没有修改部署说明。

## 8.3 首次用户反馈：真实反代清单外拒绝，符合设计

用户确认：首次不是通过局域网 URL，而是在另一台电脑的 Chrome 中打开 **`https://music.qlqqs.com:8888`**；该用户自有反代将请求转发到演练实例。
页面显示“请用正确的网址打开页面后重试。”，请求 ID `web-5ae0d67867afbafe`。

只读核对 app 日志：

```text
2026-11-05T16:01:17.792009872Z
POST /api/v1/auth/login
request_id=web-5ae0d67867afbafe
status=403
```

用户报告错误码为 `origin_forbidden`。当时清单只有 `http://192.168.50.206:18090,https://localhost:18443`，不包含用户实际打开的 HTTPS 来源。
**结论：这是“真实外部反代保留 Origin、清单外来源被拒绝”的联合证据，符合设计，不是应用故障。**
需要区分证据边界：当时尚未抓取原始头部，此结论依据用户确认网址、请求 ID 对应的 403 和清单配置；不伪称已有该次请求的原始 Origin 记录。
此次请求在 Origin 守卫处拒绝，未进入登录核对/限速，因此没有 `identity.login.failed` 的来源字段，**不能用它验收 8.3**。
结构化证据见 `step-08-external-rejected.json`。

## 真实反代重试准备：已完成，等待用户

按用户本次明确授权，**仅修改 `/tmp/roomusic-drill/drill.env` 的 `ROOMUSIC_ALLOWED_ORIGINS` 一行**，追加 `,https://music.qlqqs.com:8888`；其他字节保持不变，文件权限仍为 600。
执行前断言项目名为 `roomusic-drill`，执行：

```bash
docker compose -p roomusic-drill -f deploy/production/compose.yaml --env-file /tmp/roomusic-drill/drill.env config --quiet
docker compose -p roomusic-drill -f deploy/production/compose.yaml --env-file /tmp/roomusic-drill/drill.env up -d --wait app
```

结果：app 被重建且 healthy；演练 postgres、backup 的容器 ID 和启动时间均未改变。未重新构建镜像，未修改应用、部署文件或正式配置。
运行中 app 的清单已通过 `docker exec roomusic-drill-app-1 printenv ROOMUSIC_ALLOWED_ORIGINS` 单项核对（未读取/输出其他 env）：

```text
http://192.168.50.206:18090,https://localhost:18443,https://music.qlqqs.com:8888
```

app 重建会清空进程内登录限速，但会话与音乐资料库仍在独立数据库中。原有 Caddy 模拟证据保留；本轮用户 B 将验证真实外部反代，不要求用户知道或输入正确密码。

### 白名单头部监听

虽然登录失败日志会给出 TCP 来源，但不会记录 Origin；为了按用户要求同时核对 A/B 的 Origin，准备了等价的被动只读捕获。
辅助容器 `roomusic-drill-capture` 共享 **更新后** app 的网络命名空间，用已有 `python:3.13-alpine` 镜像运行本目录 `capture-login-headers.py`；不安装软件、不改应用容器。

- 只用原始套接字被动接收 `eth0` 的 IPv4 TCP 8080 请求，不发送包、不改网络配置、不启用混杂模式。
- 套接字创建后放弃 root，按 uid/gid 1000 写证据；容器根文件系统只读，没有应用/数据库卷挂载。
- **只保存 `POST /api/v1/auth/login` 的 Origin、Host、Referer、User-Agent，以及 TCP 对端/服务端地址和采集时间。**
- Cookie、Authorization 和其他头部不进入输出；按 Content-Length 跳过请求体，不保存 raw packet/pcap，不打印请求体。
- 已用内存合成请求验证分段头部（逐个字节边界）、同包 body、持久连接、多次请求；随机请求体内容与 Cookie 均未进入结果，见自检命令。
- 证据文件 `capture/step-08-real-request-headers.jsonl`，属主 1000、权限 600；就绪时间 **2026-11-05 16:06:56.835001 UTC**。
- 仅捕获接下来的两条登录请求后自动退出；最长等待 24 小时。辅助容器 `--rm`，退出自动删除容器，但保留白名单证据文件。
- 命令与就绪证据：`step-08-capture-ready.log`。没有开启此前建议的完整 tcpdump 输出，也没有落盘任何原始包。

实际局部验证：

```bash
python3 .trellis/tasks/11-05-deploy-packaging/research/drill/capture-login-headers.py --selftest
git diff --check
```

### 请用户执行 A/B（各一次）

用户名均为 **`drill-admin`**，每次故意输入错误密码，不需要正确密码，不要发送输入的密码：

- **A**：在那台电脑直接打开 `http://192.168.50.206:18090`，输错一次。预期 401 / `invalid_credentials`；捕获 Origin 应为该 HTTP 来源，app 日志 source 应是这台电脑的真实 `192.168.50.x`，不能是网关 `10.203.51.1` 或宿主机 `.206`。
- **B**：打开 `https://music.qlqqs.com:8888`，输错一次。预期从 403 变为 401 / `invalid_credentials`；捕获 Origin 应为该真实 HTTPS 来源，app 日志 source 为反代建立后端连接时的 TCP 来源地址。

请用户回报两次提示/请求 ID，最好附 A 设备局域网 IPv4。收到“用户已完成”后，先将白名单头部与 app 的 HTTP 状态及登录失败来源日志关联，再判定 8.3；若实际应用/部署结果不符，仍按约束停止，不自行修改。
只有核对通过才继续第 9–14 步。继续后在清理演练项目前确认辅助监听已结束（或仅停止该辅助容器），避免共享旧网络命名空间造成清理残留。
本轮未向真实外部网址发送正确凭据，不要求补做真实反代的正确登录/收藏/退出；模拟 HTTPS 的完整 Cookie 验证仍保留为第 7 步证据。

## 用户 B 重试与本机 A 替代：核对结果

用户反馈 B 页面“用户名或密码错误。”，请求 ID **`web-757521fca87d5c8b`**；同时明确无法从另一台设备执行 A，要求 agent 直接尝试。
agent 使用宿主机无头 Chromium 实际执行 A 的错误密码登录，未使用其他设备、未伪造源地址。

| 用例 | UTC 时间 | 捕获的 Origin | TCP 对端地址 | 应用失败日志 source | HTTP |
| --- | --- | --- | --- | --- | --- |
| B：用户从真实外部反代访问 | 2026-11-05 16:09:20 | `https://music.qlqqs.com:8888` | `192.168.50.1:37128` | `192.168.50.1` | 401 |
| A 替代：agent 宿主机直连 | 2026-11-05 16:10:43 | `http://192.168.50.206:18090` | `192.168.50.206:57474` | `192.168.50.206` | 401 |

- B 的 Host 为 `music.qlqqs.com:8888`，Referer 为真实 HTTPS `/login`；User-Agent 为用户 Windows Chrome。原始指定头部白名单见 `capture/step-08-real-request-headers.jsonl`。
- B 的应用日志 ID 对应用户报告，`identity.login.failed` 与 HTTP 完成日志均有记录；不再被 Origin 拒绝，已正确进入密码核对。**真实反代正确保留 Origin、清单追加生效，B 通过。**
- B 的 `192.168.50.1` 是应用实际看到的后端连接源；未进一步确认它是否为反代物理主机或经过路由/NAT 后的地址，不擅自据此推断反代拓扑。
- A 替代的请求 ID 为 **`web-626c558e38d99d4f`**，返回 401 / `invalid_credentials`，页面同样显示“用户名或密码错误。”；TCP 与失败日志一致，未退化为 Docker 网关 `10.203.51.1`。
- **A 替代只能证明宿主机直连。source 恰为宿主机 `.206`，不能伪称另一台局域网设备的 IP 验收通过。** B 来自非宿主机的 `.1` 可证明此反代后端连接源被保留，但不替代原计划对用户设备直连的验收。

实际命令：

```bash
node --check .trellis/tasks/11-05-deploy-packaging/research/drill/step-08-local-a.cjs
source /home/qlqq/.local/share/playwright-deps/env.sh
node .trellis/tasks/11-05-deploy-packaging/research/drill/step-08-local-a.cjs
# 只读提取 docker logs，自指定就绪时间起按上述两个 request_id 过滤，输出字段白名单。
git diff --check
```

完整关联证据：`step-08-real-ab-result.json`；A 的命令输出/截图：`step-08-local-a.log`、`step-08-local-a.png`。
捕获已收到两条登录请求并自动退出，`roomusic-drill-capture` 已自动删除；证据文件保留，没有原始包、密码、请求体或 Cookie。
目前没有发现新的应用/部署故障。**第 9–14 步仍未执行；需明确接受跨设备局域网验证缺口后再继续，不将未验证项记为通过。**

## 最新暂停约束：只报告头部，不继续第 9 步

用户补充决定：未来生产改为浏览器来源主机与请求 Host 一致时放行，不再强制来源清单；此行为由其他 agent 单独实现。本测试 agent 不修改应用、部署或演练配置，不把该未来行为当作当前已实现合同。
本轮 A/B 已有白名单抓包，尤其 B 的 `Host` 确为 `music.qlqqs.com:8888`，与 Origin 的主机及端口一致。该观察只针对本次已捕获请求，不推断其他反代或其他配置也保留 Host。
当前仅单独报告 A/B 的 Origin、Host、TCP 对端、HTTP 状态与失败日志 source；A 仍明确为宿主机替代，不冒充另一台局域网设备。**回报后停止，第 9–14 步必须等用户/主会话再次确认；即使接受 A 的覆盖缺口也不自动继续。**


## 演练结束小结（2026-11-05）

用户决定不再继续演练，以后遇到问题再反馈。本轮仅完成清理和开发库只读核对，不启动正式环境，不实现或验证其他 agent 正在处理的来源策略改动。以上暂停、等待续跑和结构化 A/B 证据中的当时结论均保留为历史记录，以本节及顶部结果索引为最终状态。

### 第 0–8 步结果

| 步骤 | 最终结果 |
| --- | --- |
| 0 | 开发库只读基线采集通过，原始基线保留。 |
| 1、1b | 从 main 提交导出构建并启动成功；main、其他分支、未提交改动的构建隔离验证通过。1b 首次脚本的缺失标签断言经授权修正后通过。 |
| 2 | ffmpeg 7.1.1、uid/gid 1000、派生自检与 tools/时区检查通过。 |
| 3 | 管理员初始化成功，201；未在记录中保存密码。 |
| 4 | `/music` 只读扫描成功：68 个发行、68 个专辑组、284 条曲目、292 个文件；100 条 `cue_reference` 诊断（可能截断），不等于无诊断。 |
| 5 | FLAC 直出、ALAC 无损派生均真实播放，206 / audio/flac，媒体进度推进约 2.8 秒。 |
| 6–7 | 临时 TLS、本机 HTTP/HTTPS 两入口登录、收藏、退出通过；Cookie 的 Secure 跟随来源，未知 Origin 返回 403。 |
| 8.1–8.2 | 连错五次 401、第六次 429；不同 TCP 来源限速隔离验证通过。 |
| 8.3 | 按实测的路由器转发链路收口，结论及限制见下。 |

### 第 8.3 步结论

**经路由器转来的请求能看到真实对端 `192.168.50.1`，不需要 host 网络。** 当前 bridge + IPv4 发布端口保留了本次连接的 TCP 对端；这不是最终浏览器设备的地址，也不从转发头推断设备地址。

真实外部反代 B 的请求 `web-757521fca87d5c8b`：Origin 为 `https://music.qlqqs.com:8888`，Host 为 `music.qlqqs.com:8888`，TCP 对端为 `192.168.50.1:37128`，应用 `identity.login.failed.source` 为 `192.168.50.1`，返回 401。Origin 与 Host 均被真实链路保留。
首次真实反代来源未在清单内时返回 403，是符合当时设计的正确拒绝；用户授权只追加临时清单并重建演练 app 后，B 进入登录核对并返回 401。原始头部、来源与状态码关联证据：`capture/step-08-real-request-headers.jsonl`、`step-08-external-rejected.json`、`step-08-real-ab-result.json`。

用户没有条件从另一台设备直连 A，按其要求由 agent 宿主机替代：Origin/Host 为 `http://192.168.50.206:18090` / `192.168.50.206:18090`，TCP 对端 `192.168.50.206:57474`，source 为 `192.168.50.206`，返回 401。**另一台局域网设备直连这一覆盖项仍未实测**；不把 A 替代测试记成跨设备直连通过。无需 host 网络的结论限定于本次已实测链路。

### 第 9–12 步：用户决定跳过（2026-11-05）

- 第 9 步重启恢复：用户决定跳过（2026-11-05）。
- 第 10 步手动备份及失败路径演练：用户决定跳过（2026-11-05）。
- 第 11 步恢复至临时库及演练库核对：用户决定跳过（2026-11-05）。
- 第 12 步封面卷丢失后重新扫描：用户决定跳过（2026-11-05）。

这些步骤均未执行，不标为通过，不声称重启、恢复或封面可重建已验收。`deploy/production/README.md` 本测试 agent 从未修改，也没有写入第 12 步封面卷结论。清理前该文件 diff 为空；最终检查时发现来源策略并行任务正在更新此文件的来源/反代说明，本 agent 不还原其他 agent 的改动。封面卷相关段落单独与 HEAD 对比，仍保持“尚待上线演练确认”的未验证表述。

### 第 13 步：清理通过

实际执行入口与关键命令（所有命令、输出见 `step-13-cleanup.log`）：

```bash
python3 .trellis/tasks/11-05-deploy-packaging/research/drill/step-13.py
# 脚本先核对归一化项目名、卷/容器归属，再执行：
docker compose -p roomusic-drill -f deploy/production/compose.yaml --env-file /tmp/roomusic-drill/drill.env down -v
docker stop roomusic-drill-tls
docker rm roomusic-drill-tls
docker image rm roomusic-drill-app:263d75709b2b roomusic-drill-app:current roomusic-drill-tools:263d75709b2b roomusic-drill-tools:current
rm -r -- /tmp/roomusic-drill
```

- 抓包在捕获两次请求后已自动退出，辅助容器已自动删除；无活动抓包。
- `down -v` 仅针对已核实的 `roomusic-drill`，三个服务、两个命名卷、专属网络及 backup 独占匿名卷均删除。TLS 容器没有匿名卷，停止后正常删除。
- `roomusic-drill-*` 镜像按标签删除，无强制删除、无全局 prune，不删除共享基础镜像。
- 临时 clone 在 1b 已清理，本步再次确认不存在；`/tmp/roomusic-drill/` 已删除，包含临时凭据、会话、诊断与备份。仓库 `research/drill/` 记录全部保留。
- 最终 `docker ps -a`、`docker volume ls`、`docker network ls`、`docker image ls` 检查演练相关残留均为 0；18090、18443 无监听。
- 删除临时凭据前，仅在内存读取演练数据库密码、管理员密码和保存的会话值，对 49 个记录/脚本/截图文件检查均未发现这些值，未输出秘密或散列。抓包仅保存指定头部与 TCP 地址，无请求体、Cookie 或原始包；截图已在编码前遮罩密码框。
- 首次清理预检的自编脚本错误地要求所有卷都带项目名前缀，未考虑 tools 继承 PostgreSQL 镜像的匿名卷，**在任何删除操作前**断言失败。按用户既有授权修正检查：仅允许 backup 的 PostgreSQL 数据路径匿名卷，核实无其他容器使用，清理后再确认删除；重跑通过。首轮日志保留为 `step-13-precheck-initial.log`。不是部署或应用故障，未改部署或应用代码。

结构化结果：`step-13-result.json`；脚本：`step-13.py`。

### 第 14 步：开发库只读核对通过

实际执行 `python3 .trellis/tasks/11-05-deploy-packaging/research/drill/step-14.py`，重复第 0 步允许的状态、卷创建时间和三项 SQL 聚合查询。只白名单提取容器中的 `POSTGRES_USER` / `POSTGRES_DB`，不读取完整环境，不读取 `.env` / `.env.dev`。

| 对比项 | 第 0 步基线 | 第 14 步核对 | 结果 |
| --- | --- | --- | --- |
| 容器状态 | running | running | 不变 |
| 启动时间 | `2026-10-26T03:13:45.497579973Z` | `2026-10-26T03:13:45.497579973Z` | 不变 |
| 卷创建时间 | `2026-09-01T09:54:51Z` | `2026-09-01T09:54:51Z` | 不变 |
| 迁移最大版本 | 12 | 12 | 不变 |
| users 行数 | 1 | 1 | 不变 |
| releases 行数 | 68 | 68 | 不变 |

采集时间：基线 `2026-11-05T15:08:06.767962+00:00`，核对 `2026-11-05T16:26:13.265319+00:00`。证据：原 `step-00-baseline.json`、新增 `step-14-result.json`、`step-14.log` 与 `step-14.py`。
本 agent 未对开发容器、卷或 Compose 项目执行停止、重启、删除或业务写操作；核对项全部一致。未创建 `.env.prod`、未启动 `roomusic-prod`，未提交 Git。

### 收尾验证与并行改动边界

- 实际执行新增 `step-13.py`、`step-14.py`，两个脚本的内存语法检查，以及抓包白名单结构检查；清理后资源/端口和开发库结果均通过。
- 实际执行 `git diff --check`；对本次 README 和新增脚本另行检查末尾空白（因为新记录尚未跟踪）。首次发现 README 第 81 行有历史末尾空格，已只去除空格后重查通过；首轮结果保留于 `step-13-14-final-check-initial.log`。
- 最终检查原先用整份部署 README 与 HEAD 无 diff 作为门禁，但在清理期间其他 agent 已开始修改来源/反代说明，该假设不再成立。未还原、未修改其文件；将门禁改为只核对本任务关心的封面卷段落未变。并行 diff 的当时输出保留于 `step-13-14-concurrent-doc-check.log`，最终有效验证见 `step-13-14-final-check.log`。不将其他 agent 尚未完成的策略变更作为本轮应用验证结果。
- 不运行全量 Go、前端或集成测试：本轮仅清理演练资源、执行三项只读聚合并写演练记录，没有改动部署或应用代码。不提交 Git。
