<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/logo-dark.svg">
    <img src="docs/brand/logo.svg" alt="ROOMusic" width="88">
  </picture>
</p>

<h1 align="center">ROOMusic</h1>

<p align="center">
  自托管私人音乐库，支持多房间同步播放、服务器端音效、DLNA / AirPlay / Cast 投放、Subsonic 客户端和 AI Agent。
</p>

<p align="center">
  <a href="https://github.com/qlqqs/ROOMusic/releases/latest"><img alt="最新版本" src="https://img.shields.io/github/v/release/qlqqs/ROOMusic"></a>
  <a href="https://github.com/qlqqs/ROOMusic/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/qlqqs/ROOMusic/actions/workflows/ci.yml/badge.svg?branch=main"></a>
  <a href="LICENSE"><img alt="许可证 AGPL-3.0" src="https://img.shields.io/badge/license-AGPL--3.0-blue"></a>
</p>

<p align="center">
  <a href="#快速开始">快速开始</a> ·
  <a href="docs/install-nas.md">在 NAS 上安装</a> ·
  <a href="deploy/production/README.md">部署说明</a> ·
  <a href="docs/subsonic-clients.md">Subsonic 客户端</a> ·
  <a href="https://github.com/qlqqs/ROOMusic/releases">更新说明</a>
</p>

## 有什么不同

- **按专辑和版本理解音乐**：曲库按 `专辑 → 版本 → 碟 → 曲目` 组织，而不是列出文件夹。同一张专辑的原版、重制版、豪华版归在一起，可以并排对比曲目，管理员选定默认版本。
- **音效在服务器上处理**：参数均衡、卷积、采样率转换都在服务器上完成。浏览器、局域网音箱和手机客户端走同一条信号路径，每一步都能在网页上看到。
- **家里的音箱直接用**：自动发现 UPnP/DLNA、Google Cast 和 AirPlay 2 设备。Squeezelite 播放器以原采样率无损播放，几台编成一组，在不同房间同步出声。
- **整理随时可以撤销**：手动修正、自动补全、管家做的修改都有记录，每一项都能撤销，重新扫描也不会覆盖。音乐目录只读挂载，从不改动音乐文件。

## 功能

### 听歌

- 无缝播放、交叉渐变、音量均衡；ALAC、DSD 和 CUE 分轨由服务器解码，在浏览器里直接播放
- 歌词逐行同步，来自内嵌标签、同名 `.lrc` 文件或自动整理
- 搜索不区分繁简和写法
- 每位成员自己的音效：参数均衡（可导入耳机均衡曲线）、卷积、采样率转换与 Headroom
- 在一个页面上遥控另一台设备的播放
- 网页可以装到手机主屏幕，锁屏界面显示正在播放并能控制

### 播放到哪里

| 设备或客户端 | 连接方式 | 说明 |
| --- | --- | --- |
| 浏览器、手机主屏幕 | 网页 | 电脑、iPhone、Android 都能装成应用 |
| 功放、电视、播放软件 | UPnP/DLNA | 自动发现，音效照常生效 |
| Chromecast、Nest 音箱 | Google Cast | 自动发现 |
| HomePod、Apple TV、Mac | AirPlay 2 | 自动发现 |
| piCorePlayer、moOde 等 Squeezelite 播放器 | SlimProto | 原采样率无损，最高 768 kHz；多台编组同步，服务器逐帧补偿时钟漂移，偏差约 1 ms |
| Symfonium、Feishin、Amperfy 等 | OpenSubsonic | 收藏、评分、歌单、歌词、听歌记录与网页互通；点唱机模式遥控播放区。见[设置方法](docs/subsonic-clients.md) |
| Claude Code 等 MCP 客户端 | MCP | 用访问令牌查曲库、放歌。见[接入说明](docs/steward-mcp.md) |

局域网设备由管理员打开，可以设置成员之间是否共享设备。

### 整理

- 修正发行信息、调整归组、合并或拆分艺人，在艺人页上传图片、编辑简介或指定 MusicBrainz / Wikidata 条目
- 自动整理：从 MusicBrainz 等开放资料库补封面、歌词、空着的信息和艺人图片与简介，拿不准的交给模型判断
- 开放资料库查不到的艺人交给“网络搜索”：模型联网认人，或整理出带引用网页的图片与简介
- 安装第三方数据源插件，插件在沙箱里运行，见[插件开发说明](docs/plugins.md)
- 管家：接入你自己的模型服务，用对话查曲库、放歌、整理资料；修改前可以先确认，也可以交给另一次模型调用审查

### 家庭与管理

- 一位管理员整理曲库、添加成员、重置成员密码；成员各自有播放历史、收藏、歌单和 1–5 星评分
- 只读扫描音乐目录，可以定时扫描
- 每天自动备份数据库，升级和降级前也会备份；网页上可以立即备份、下载，设置保留几天或几份
- 管理员自己忘了密码，在服务器上用命令重置

### 支持的格式

FLAC、ALAC 与 AAC（.m4a）、MP3、Ogg Vorbis、Opus、WAV、AIFF、APE、WavPack、DSD（DSF/DFF），以及 CUE 分轨。

## 快速开始

需要 Docker 与 Docker Compose v2。用群晖、飞牛、绿联、威联通、TrueNAS、Unraid 等 NAS 的 Docker 界面安装，
见[在 NAS 上安装](docs/install-nas.md)，不需要命令行。

命令行安装：

```bash
mkdir roomusic && cd roomusic
curl -fL -o compose.yaml https://raw.githubusercontent.com/qlqqs/ROOMusic/main/compose.prod.yaml
curl -fL -o .env https://raw.githubusercontent.com/qlqqs/ROOMusic/main/.env.prod.example
chmod 600 .env
```

编辑 `.env`，填写音乐目录；读取音乐的用户不是 uid 1000 时，同时填写它的用户与组 ID：

```ini
ROOMUSIC_MUSIC_DIR=/path/to/music     # 音乐目录的绝对路径
# PUID=1000
# PGID=1000
```

启动：

```bash
docker compose up -d --wait
```

打开 `http://<主机>:8090`，创建管理员，在“扫描目录”添加 `/music`，扫描完成后就可以听歌了。

数据库密码在首次启动时自动生成。数据都在部署目录里的 `secrets/`、`postgres/`、`data/` 和 `backups/`，由 Docker 自动创建。
默认的固定容器名是 `roomusic-app`、`roomusic-postgres` 和 `roomusic-ptp`（为 AirPlay 2 对时），可以在 `.env` 中修改。

**装到手机主屏幕**：iPhone、iPad 用 Safari 打开，点分享按钮，选“添加到主屏幕”（主屏幕窗口和 Safari 不共享登录，第一次要重新登录）；
Android 用 Chrome 菜单里的“安装应用”；电脑上点 Chrome、Edge 地址栏右侧的安装按钮。

### 升级

```bash
docker compose pull && docker compose up -d --wait
```

默认跟随 `latest`。要固定或降级版本，把 `.env` 里的 `ROOMUSIC_VERSION` 改成具体版本（不带 `v`），再执行同样的命令。
数据库结构变化前会自动备份；遇到无法自动回退的版本，应用不会启动，日志会给出恢复备份的命令。

端口、反向代理、备份恢复、排查等见[部署说明](deploy/production/README.md)。

## 匿名统计

ROOMusic 默认每天向项目发送一次匿名统计：版本、曲库规模的分档、用到了哪些功能。不含曲目、文件名、账号或 IP。
可以在“设置 › 匿名统计”里关闭，或者首次启动前在 `.env` 里设置 `ROOMUSIC_INSIGHTS_DISABLED=true`，一次都不发送。
发送的字段见[匿名统计说明](docs/insights.md)。

## 文档

| 想做的事 | 看这里 |
| --- | --- |
| 在 NAS 上用图形界面安装 | [在 NAS 上安装](docs/install-nas.md) |
| 配置、反向代理、备份恢复、排查 | [部署说明](deploy/production/README.md) |
| 用手机或桌面的 Subsonic 客户端听歌 | [用 Subsonic 客户端连接](docs/subsonic-clients.md) |
| 让 Claude Code 等 MCP 客户端连进来 | [用 MCP 客户端连接](docs/steward-mcp.md) |
| 写一个数据源插件 | [插件开发说明](docs/plugins.md) |
| 了解匿名统计发送了什么 | [匿名统计说明](docs/insights.md) |
| 查看每个版本的变化 | [Releases](https://github.com/qlqqs/ROOMusic/releases) |

## 参与开发

欢迎提交问题和改动；较大的改动请先开 issue 说明想解决的问题。提交要求和贡献者许可协议见[参与贡献](CONTRIBUTING.md)。

后端是 Go，前端是 React 与 TypeScript，数据库是 PostgreSQL 18。工具链由 [mise](https://mise.jdx.dev/) 管理：

```bash
mise install
cp .env.dev.example .env.dev   # 填写本地 PostgreSQL 密码
make dev                       # PostgreSQL + Go 后端（8080）+ Vite（5173）
```

测试、外部接口 mock、局域网设备的假接收端和发布流程见[开发与测试环境](docs/development-environment.md)；
长期规则见[架构决策](docs/architecture/architecture-decisions.md)，界面设计见[前端设计依据](frontend/DESIGN.md)。

## 致谢

自动整理的资料来自 [MusicBrainz](https://musicbrainz.org/)、Cover Art Archive、[LRCLIB](https://lrclib.net/)、[AcoustID](https://acoustid.org/)、
Wikidata、Wikimedia Commons、维基百科、Fanart.tv 和 TheAudioDB；音频解码与转换使用 [FFmpeg](https://ffmpeg.org/)。
随附的第三方程序与数据见[第三方组件与许可](THIRD-PARTY-NOTICES.md)。

## 许可证

Copyright (C) 2026 qlqqs

ROOMusic 以 [GNU Affero 通用公共许可证第 3 版](LICENSE)（AGPL-3.0-only）发布：可以自由使用、修改和分发；
分发修改版，或者把修改版作为网络服务提供给他人使用时，需要以同一许可公开修改后的源码。
