# 推广准备：匿名统计与补齐短板

## 目标

服务端以 AGPL-3.0-only 完全开源，收入来自手机端与模型 API（2026-11-24 qlqq 确定）。推广前需要两件事：

1. 能数出“30 天内活跃的实例数”与各功能的使用比例，用来判断手机端与模型服务值不值得做；
2. 补齐会挡住新用户的短板：手机上不能装成应用、安装门槛高、只能投放到 UPnP 设备。

## 背景事实（来自仓库与本机，2026-11-24）

- 服务端从不向项目方发送数据；出站规则由 ADR 0005、ADR 0006 约束，新增出站目的地要补 ADR。
- 界面里没有源代码链接；AGPL 要求修改后作为网络服务提供的版本向用户提供源码。
- `frontend/public/` 只有 `licenses/`，没有 Web App Manifest，手机浏览器不能“添加到主屏幕”成独立应用。
- 安装需要 Docker、Compose v2、Python 3 与 sudo 运行的首次安装脚本；镜像只发布在 ghcr.io，有 amd64 与 arm64。
- 生产容器：`network_mode: host`、uid 1000、只读根文件系统、`cap_drop: [ALL]`、`no-new-privileges`。
- 局域网投放只有 UPnP/DLNA（服务器托管的输出端，见 `.trellis/spec/backend/upnp-device-contracts.md`）。
- 本机有公网 IPv4（`147.135.108.57`），80/443 由 1Panel 管理的 OpenResty 容器占用；`roomusic.org` 的 DNS 在 Cloudflare，尚无记录。
- 本机另有 `new-api`（带额度与计费的模型网关），以后做模型 API 可复用，不在本任务范围。

## 调研

- `research/casting-protocols.md`：Chromecast 用 Default Media Receiver 拉流，可复用局域网设备的流地址；部分 Nest/三星设备长 FLAC 流会断，需按型号回退 MP3（固定的 ffmpeg 有 libmp3lame）。
  AirPlay 2 是推流，要新的输出类型；PTP 要绑定 UDP 319/320；HomePod 暂停时要持续发静音；成熟实现多为 GPL。
- `research/nas-install.md`：首次安装脚本可去掉、不需要初始化容器；各家 NAS 首个用户 uid 不同（群晖 1026:100、绿联 1000:10、Unraid 99:100、TrueNAS 568:568）；
  群晖 Compose 只有 v2.20.1；QNAP 与 TrueNAS 只能粘贴单文件且不读 `.env`；NAS 自带的镜像加速只对 Docker Hub 有效。

## 子任务

| 顺序 | 子任务 | 交付 |
| --- | --- | --- |
| 1 | `11-24-instance-insights` | 匿名心跳（服务端）、统计端程序与公开汇总页、管理员告知与开关、界面里的源代码链接、ADR 补充 |
| 2 | `11-24-installable-web-app` | 手机网页可安装：Manifest、图标、主屏幕打开、锁屏控制与后台播放的核对 |
| 3 | `11-24-nas-install` | 不依赖首次安装脚本的 Compose、`PUID`/`PGID`、首启生成数据库密码、单文件版本、分平台说明、Unraid 模板、加发 Docker Hub |
| 4 | `11-24-chromecast-output` | Chromecast / Google Cast 投放，作为服务器托管的输出端 |
| 5 | `11-24-airplay-output` | 完整 AirPlay 2 投放（配对、加密、PTP 对时），对时放在独立的小容器 |

依赖：5 在 3 之后（对时容器写进 3 改写后的 Compose）；4、5 复用局域网设备的发现、占用与输出端框架，5 在 4 之后以便共用投放层的整理。

## 已定的决定（qlqq 选定）

| 日期 | 决定 |
| --- | --- |
| 2026-11-24 | 范围为上表五项；原生手机 App 与车机（CarPlay、Android Auto）只能由原生 App 提供，另起私有项目，不在本仓库 |
| 2026-11-24 | 心跳默认开启；管理员首次登录时显示说明，列出发送的全部字段，可一键关闭；统计汇总公开 |
| 2026-11-24 | 统计端部署在本机，域名用 `roomusic.org`；统计端程序开源在本仓库 |
| 2026-11-24 | 改为：统计端程序与汇总页移到同级的门户项目 `roomusic-site`（以后放门户页面），本仓库只保留心跳客户端（子任务 `11-24-insights-site-split`） |
| 2026-11-24 | AirPlay 直接做完整 AirPlay 2；参考成熟开源实现自己写：可以读 GPL 源码理解协议与行为，但不翻译、不照抄，先记笔记再按本项目结构编写，保留 CONTRIBUTING 里另行授权的余地（qlqq 修正：原先只允许看文档，过严） |
| 2026-11-24 | 不放弃容器加固：主程序保持非 root、只读根文件系统、去掉全部权限；PTP 对时放在独立小容器，只给绑定低端口的权限，经本地 socket 把时间交给主程序；NAS 的 uid 差异用 `PUID`/`PGID` 解决 |
| 2026-11-24 | 镜像加发 Docker Hub，文档默认用 Docker Hub 地址 |

其余按“优先使用和操作舒适”由主代理取默认值，写在各子任务的 PRD 里，交付时列出。

## 跨子任务验收

- 五个子任务各自验收并合入本地 `main`；每项的 PRD 记录实际运行的验证。
- 新出站目的地（统计端）写入 ADR；投放协议的出站只到局域网地址。
- 主程序容器加固不回退：`compose.prod.yaml` 里仍是 `cap_drop: [ALL]`、`no-new-privileges`、只读根文件系统。
- 用生产镜像演练：从零安装（不运行首次安装脚本）、从 0.8.0 升级，数据保留、能登录。
- README 与部署说明同步：安装方式、心跳说明与关闭方法、投放设备类型。

## 需要 qlqq 操作或授权的事（不阻塞开发）

- 在 Cloudflare 给 `roomusic.org` 加统计端的 DNS 记录。
- 授权在 1Panel 的 OpenResty 里新增统计端站点，并在本机启动统计端容器。
- 注册 Docker Hub 账号，设置仓库 secret（访问令牌）与镜像名。
- 真实设备试用：手机安装、各家 NAS、Chromecast、AirPlay 设备。

## 不做

- 原生手机 App、车机、离线下载。
- 统计端不记录 IP 地址，不收集曲目、艺人、文件名等曲库内容。
- TrueNAS 官方应用目录（其模板要求权限初始化容器，与项目规则冲突）；只写安装说明。
- 多房间同步播放（AirPlay 2 的多设备同步）。

## 规划决定的选择方式

人工选择：范围与上表决定由 qlqq 在 2026-11-24 的对话中逐项选定；按其长期约定（规划完直接实现、取推荐默认值、交付时列出），不再单独等待规划批准。
