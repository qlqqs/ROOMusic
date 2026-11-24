# 项目开发与测试环境

本文记录 ROOMusic 的开发与测试约定。本机环境说明见 `~/.codex/AGENT.md`；通用工作约定见项目根目录的 `AGENTS.md`。

## 工具链

Go 与 Node 版本由仓库根目录的 `.mise.toml` 固定。进入项目后执行：

```bash
mise trust
mise install
mise exec -- go version
mise exec -- node --version
```

## 工作目录

以下路径均相对于当前项目工作目录：

- `temp/dev/`：本项目开发数据。
- `temp/`：临时文件、合成测试素材与每次测试的部署目录。
- `temp/` 整体加入 Git 忽略规则；临时文件按日期归档到 `temp/archive/YYYY-MM-DD/`。
- `.trellis/tasks/archive/`：Trellis 归档任务只留本机，加入 Git 忽略规则。`task.py archive` 移动任务后，因为归档目录被忽略，会提示并跳过自动提交；
  这时手动提交源任务目录的删除（以及被改写的父子任务 `task.json`）：`git add -A .trellis/tasks/<任务目录>` 后提交。
  spec 与文档引用归档文档时写成不带链接的路径文字，并注明“本机归档”。

## 浏览器测试与截图

Playwright 只用 Chromium。本机缺 Chromium 的系统库（`libatk` 等）时，`apt-get download` 后 `dpkg -x` 解到 `temp/` 下，
运行时用 `LD_LIBRARY_PATH` 指向解出的库目录；对回环地址不走代理（`NO_PROXY=127.0.0.1,localhost`）。

截图与文字显示核对需要中文字体，否则中文显示为方块。不用 sudo 的装法：

```bash
apt-get download fonts-noto-cjk
dpkg -x fonts-noto-cjk_*.deb fonts-noto-cjk
mkdir -p ~/.local/share/fonts/noto-cjk ~/.config/fontconfig/conf.d
cp fonts-noto-cjk/usr/share/fonts/opentype/noto/NotoSansCJK-{Regular,Bold}.ttc ~/.local/share/fonts/noto-cjk/
cp fonts-noto-cjk/usr/share/fontconfig/conf.avail/70-fonts-noto-cjk.conf ~/.config/fontconfig/conf.d/
```

第二个文件按页面语言选字形：页面是 `lang="zh-CN"`，所以用简体字形，不会落到日文字形。前端字体栈里有 `"Noto Sans CJK SC"`，
截图看到的就是它；真实设备上用的是苹方、微软雅黑等系统字体，截图核对不能代替真实设备上的查看。

## Docker 隔离约定

开发项目名使用 `roomusic-compose-dev`；每次集成测试使用 `roomusic-compose-test-` 加随机后缀；生产使用独立项目名。不同项目各自使用网络及目录，开发和测试由 Compose 生成容器名；生产使用可配置的固定容器名。

应用端口（Vite、Go 后端）绑定 `0.0.0.0`，便于局域网设备访问；开发和测试的 PostgreSQL 只绑定 `127.0.0.1`。开发使用独立端口，测试由脚本选取空闲端口。开发、测试与生产的 PostgreSQL 使用同一个钉死镜像。测试容器限制 CPU 与内存，数据库集成和端到端测试串行运行。

仅清理当前测试项目及其临时目录，不执行全局 prune。测试仅使用合成数据，不访问现有实例的配置、凭据或数据。

## 外部接口 mock

在仓库根目录运行 `make mock-upstream`，默认监听 `127.0.0.1:18090`。另一个终端运行：

```bash
ROOMUSIC_UPSTREAM_MOCK_URL=http://127.0.0.1:18090 mise exec -- make dev
```

后端启动时会警告并列出 mock 地址。MusicBrainz、Cover Art Archive、LRCLIB、AcoustID、Tavily，以及五个艺人资料源 Wikidata、Wikimedia Commons、维基百科、Fanart.tv、TheAudioDB 都会连接该地址。AcoustID 的应用密钥和用户密钥、Fanart.tv 项目密钥（个人密钥可选）、TheAudioDB 密钥及 Tavily 搜索密钥按设置页要求填写虚构值即可。模型与 SearXNG 的地址分别在管家设置中填写：

| 用途 | 基础地址 |
| --- | --- |
| OpenAI 兼容（自动检测优先选 Responses） | `http://127.0.0.1:18090/openai/v1` |
| 单独试用 Chat Completions 的探测回退 | `http://127.0.0.1:18090/openai/chat/v1` |
| Anthropic Messages | `http://127.0.0.1:18090/anthropic` |
| SearXNG | `http://127.0.0.1:18090/searxng` |

默认模型是 `mock-model` 与 `mock-reviewer`。管理员可以使用回环端点，成员的模型和 SearXNG 端点沿用实例的成员放行名单。

### 切换情景与查看请求

```bash
mock=http://127.0.0.1:18090
curl "$mock/_mock/scenarios"
curl -X PUT "$mock/_mock/scenarios/musicbrainz" --json '{"scenario":"unavailable","retry_after":5}'
curl -i "$mock/musicbrainz/ws/2/release/30000000-0000-4000-8000-000000000001"
curl "$mock/_mock/requests?service=musicbrainz&limit=10"
curl -X POST "$mock/_mock/reset"
```

环境里设了 `http_proxy` 等代理变量时，对回环地址的请求也会走代理，`curl` 加 `--noproxy '*'` 或设置 `NO_PROXY=127.0.0.1,localhost`。

每个服务可独立选择 `ok`、`not_found`、`unavailable`、`rate_limited`、`timeout`、`slow`、`malformed`、`drop`。限流和不可用的 `retry_after` 单位为秒，默认 2；`slow` 的 `delay_ms` 单位为毫秒，默认 3000。请求记录按最新在前返回，最多保留 500 条，`status: 0` 表示尚未发出响应头。重置清空记录并将全部情景恢复为 `ok`。后端命中已有响应缓存时不会发出请求，试验故障可选一个尚未查询的发行或使用新的开发数据目录。

启动时也可指定端口和初始情景：

```bash
make mock-upstream MOCK_ADDR=127.0.0.1:18091 MOCK_ARGS='-scenario lrclib=slow -scenario tavily=rate_limited'
```

### 合成数据与模型脚本

`backend/internal/upstreammock/testdata/catalog.json` 列出所有固定 ID、碟片曲序、时长、歌词和指纹。例如发行《潮线以北》的单碟版 MBID 为 `30000000-0000-4000-8000-000000000001`，第一首《潮汐信箱》时长 180 秒、ISRC 为 `QMZ9X2400001`。指纹 `mock-tide` 命中该录音，`mock-ambiguous` 有两个候选，`mock-empty` 返回空结果。这些指纹用于接口调用。试用扫描可给合成曲库打上相应 MBID。

艺人资料样本的 MBID 前缀均为 `10000000-0000-4000-8000-`。库中已有艺人 MBID 对应且开启自动整理后，可用以下样本核对图片与简介的回退。

| MBID 尾段 | 艺人 | mock 提供的资料 |
| --- | --- | --- |
| `000000000001` | 纸屿（海岸二重奏） | Fanart.tv 两张不同赞数的头像、TheAudioDB 图片及中英文简介、Wikidata Q900001 的 Commons 图片和中英文维基百科条目 |
| `000000000002` | Hollow Meridian | 仅 TheAudioDB 图片及中英文简介，中文带脚注标记 |
| `000000000003` | 纸屿（独奏者） | 所有资料源均为空 |
| `000000000004` | 盐风小组 | 仅 Commons 图片，MusicBrainz 无链接，Wikidata 的 P434 反查指向 Q900004 |

五个源的基础路径分别为 `/wikidata`、`/commons`、`/wikipedia`、`/fanarttv` 和 `/theaudiodb`。图片请求也留在 mock 服务。用 `/_mock/scenarios/fanarttv` 切换故障后，可查看该艺人的图片是否从下一来源补上。已有图片可在整理记录中撤销；相同来源条目的抑制标记会一并生效。TheAudioDB 路径中的密钥在 `/_mock/requests` 中显示为 `***`。

将 `backend/internal/upstreammock/testdata/` 复制到 `temp/mock-data/`，编辑 `catalog.json` 以及其中引用的图片。示例脚本可保存为 `temp/mock-script.json`：

```json
{
  "models": ["mock-model", "mock-reviewer"],
  "rules": [
    {
      "when": {"last_user_contains": "试查发行", "tool_available": "search_music_metadata"},
      "reply": {"tool_calls": [{"name": "search_music_metadata", "arguments": {"entity": "release", "query": "潮线以北", "limit": 1}}]}
    },
    {
      "when": {"tool_result_contains": "潮线以北"},
      "reply": {"text": "找到了《潮线以北》。"}
    }
  ]
}
```

`when` 中的条件同时成立时命中，按文件顺序取首条；`model` 可限定模型名。运行目标会切换到 `backend/`，所以自定义参数写成：

```bash
make mock-upstream MOCK_ARGS='-data ../temp/mock-data -script ../temp/mock-script.json'
```

### Compose 试用实例

在与试用容器同架构的 Linux 宿主机上，用项目工具链构建独立二进制：

```bash
mkdir -p temp
(cd backend && CGO_ENABLED=0 mise exec -- go build -o ../temp/roomusic-upstream-mock ./cmd/roomusic-upstream-mock)
```

如果试用 Compose 文件在项目根目录，可在试用项目中加入以下服务。应用与 mock 加入同一个项目网络，应用服务的环境变量设为 `ROOMUSIC_UPSTREAM_MOCK_URL=http://upstream-mock:18090`。模型和 SearXNG 设置中的主机也使用 `upstream-mock:18090`。

```yaml
services:
  upstream-mock:
    image: alpine:3.23@sha256:f3d55900449227ae9e237ede521e7b665d53a253b42065ba90b9db9275e198ad
    volumes:
      - ./temp/roomusic-upstream-mock:/mock:ro
    entrypoint: ["/mock", "-addr", "0.0.0.0:18090"]
```

## 开发用 UPnP 假渲染器

在宿主机上启动一个渲染器，终端会输出带局域网 IPv4 和实际端口的 HTTP 基址。

```bash
cd backend
mise exec -- go run ./cmd/roomusic-fake-renderer -name 客厅 -addr 0.0.0.0:0
```

在开发实例打开管理员设置里的“局域网设备”，即可发现“客厅”。同机网卡禁用组播回送时，在输出的基址后加 `/description.xml`，用完整描述地址手动添加。另一终端可用 `-name 书房 -rel-time zero` 启动第二台，模拟持续播放时 `GetPositionInfo` 的 `RelTime` 一直为零。多网卡环境可在 `-addr` 中写要公告的局域网 IPv4。

播放时用 `curl --noproxy '*' "$base/_fake/state"` 查看当前 URI、播放状态、音量、位置、拉取字节数、采样率和位深，其中 `base` 取终端输出的地址。按 Ctrl-C 会发送下线公告并结束拉流。
