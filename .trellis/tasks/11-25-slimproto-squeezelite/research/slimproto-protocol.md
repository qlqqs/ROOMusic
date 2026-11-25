# Research: SlimProto 协议（单台 Squeezelite 播放器）

- **Query**: ROOMusic 实现 SlimProto 服务端、单台 Squeezelite 托管输出所需的协议事实（发现、握手、帧、strm、取流、STAT、无缝、控制、DSD、其他命令、心跳断线、部署差异、端口）
- **Scope**: internal（本地克隆的一手源码，只读）
- **Date**: 2026-11-25

## 来源与约定

- 源码位于 `temp/research-slimproto/`，下文 `sq/` 指 `temp/research-slimproto/squeezelite/`，`lms/` 指 `temp/research-slimproto/slimserver/`。
- Squeezelite 版本：`2.0.0-1613`（`sq/squeezelite.h:27-34`），单提交浅克隆，看不到历史。
- LMS（Lyrion Music Server）为 Perl 源码，只用来看服务端在协议上的实际做法。
- 只记协议事实和字段表，不复制代码。行号指本次克隆。
- 字节序：除特别说明外，多字节字段都是网络字节序（大端）。
- 文中“播放器”指 Squeezelite，“服务器”指 SlimProto 服务端。

---

## 1. 发现

### 结论

- 播放器向 `255.255.255.255:3483` 发一个 UDP 包，内容只有 1 字节 `e`（TLV 格式的请求，后面不带任何 TLV）。
- 播放器收到第一个应答后，**不看应答内容**，只取应答的**源 IP** 当服务器地址。端口不从应答里取，固定 3483；用了 `-s host:port` 时用指定端口。
- 所以服务器的最小应答是：从 TCP 服务所在的那个 IP，向请求的源地址和端口回任意一个 UDP 包。LMS 的写法是回一个 `E` 开头的 TLV 应答；对于只带 `e` 的请求，LMS 回的就是单字节 `E`。
- 没收到应答时，播放器每 5 秒重发一次，一直重发。
- 用 `-s` 指定服务器、并且地址能解析时，**跳过发现**。没用 `-s` 时，连续连接失败超过 5 次会重新发现。

### 播放器侧

| 事实 | 来源 |
|---|---|
| 请求内容是 1 字节 `e`，发往广播地址 UDP 3483，套接字开了 SO_BROADCAST | `sq/slimproto.c:783-805` |
| `poll` 等 5000 ms；用 `recvfrom` 读最多 10 字节，只记录源地址 | `sq/slimproto.c:807-812` |
| 循环到拿到非 0 的地址为止 | `sq/slimproto.c:818` |
| 启动时先解析 `-s`；解析出 IP 就不调用发现；端口没给时默认 3483 | `sq/slimproto.c:844-854` |
| 没用 `-s` 时，连续连接失败超过 5 次就重新发现 | `sq/slimproto.c:932-935` |
| `-s` 用 `host[:port]` 格式，冒号后面是端口 | `sq/utils.c:342-366`、`sq/main.c:67` |
| 发现循环里如果带着 `-s` 的值，每轮都会再解析一次；能解析就覆盖应答的源 IP | `sq/slimproto.c:814-816` |

### 服务器侧（LMS 的做法，供参照）

| 请求首字节 | LMS 的处理 | 应答 | 来源 |
|---|---|---|---|
| `d`（旧格式，18 字节，带 deviceid、revision、MAC） | `gotDiscoveryRequest` | `D` + 17 字节主机名（ISO-8859-1，最多 16 字节，用 0 补齐）；SLIMP3 回 `D` + 17 个 0 | `lms/Slim/Networking/UDP.pm:111-121`、`lms/Slim/Networking/Discovery.pm:40-57,81-110` |
| `e`（TLV 请求） | `gotTLVRequest` | `E` 后接若干 TLV | `lms/Slim/Networking/UDP.pm:127-131`、`lms/Slim/Networking/Discovery.pm:160-215` |
| `E`（别的服务器发出的 TLV 应答） | 服务器之间互相发现 | — | `lms/Slim/Networking/UDP.pm:123-125` |
| `h`（SLIMP3 hello） | `sayHello` | `h` + 17 个 0 | `lms/Slim/Networking/Discovery.pm:67-73` |

TLV 格式：T 4 字节 ASCII，L 1 字节无符号，V 0～255 字节。只对请求里出现的 T 作答，值超过 255 字节就截断，整包超过 1450 字节不发（`lms/Slim/Networking/Discovery.pm:165-210`）。

| T | 含义（LMS 的应答值） |
|---|---|
| `NAME` | 服务器全名 |
| `IPAD` | 服务器 IP 字符串（设置了才回） |
| `JSON` | HTTP 端口（字符串） |
| `VERS` | 服务器版本（对旧版 Radio 可能回伪版本 `7.999.999`） |
| `UUID` | 服务器 UUID |
| `JVID` | 只记日志，不回 |

来源：`lms/Slim/Networking/Discovery.pm:113-127`。Squeezelite 不发任何 TLV，也不解析应答，所以这些 TLV 对它都没有作用。

LMS 的 UDP 套接字绑 3483，开了 `ReuseAddr` 和 `Broadcast`（`lms/Slim/Networking/UDP.pm:25,34-40`）。

---

## 2. 连接与握手

### 结论

- 播放器用 TCP 连服务器的 3483 端口（或 `-s` 指定的端口），连接超时 5 秒。连上后**立即发 HELO**，服务器不需要先说话。
- HELO 里的 `deviceid` 固定为 12（squeezeplay），`revision` 为 0，MAC 是设备身份。
- 能力串是逗号分隔的文本：`键=值` 是属性，单独的小写短词是编解码。
- 播放器连上就算可用，协议上没有必须回给它的“握手应答”。但是有几条命令实际上需要服务器发：
  - **`audg`：必需。** 播放器内部增益初始为 0（全局结构体零初始化），不收到 `audg` 就一直静音。
  - **`strm t`：必需（作心跳）。** 播放器超过约 35 秒收不到服务器的任何消息就断开重连。
  - **`aude 1,1`：推荐。** 播放器默认就是开着的；只有之前收到过 `aude 0`（关机）时，才需要它来恢复。
  - **`setd` id 0（不带数据）：推荐。** 用来查询播放器名称。只有播放器用 `-n` 或 `-N` 设过名字，才会回 `SETD`。
  - **`vers`：不需要。** Squeezelite 不处理它，只记一条 “unhandled” 警告。LMS 对 revision 不超过 39 的设备也不发 `vers`。
  - **`strm q`：可选。** 用来让播放器进入确定的停止状态；LMS 只在“伪重连”且服务器认为该播放器处于停止状态时才发。

### HELO 布局

整帧 = `"HELO"`（4 字节）+ 长度（u32，载荷字节数）+ 载荷。载荷偏移如下：

| 偏移 | 长度 | 字段 | Squeezelite 填的值 | 来源 |
|---|---|---|---|---|
| 0 | 1 | deviceid | 12（squeezeplay） | `sq/slimproto.h:31-42`、`sq/slimproto.c:142` |
| 1 | 1 | revision | 0 | `sq/slimproto.c:141` |
| 2 | 6 | MAC | 网卡 MAC 或 `-m` 指定的值 | `sq/slimproto.c:146` |
| 8 | 16 | UUID | 全 0 | `sq/slimproto.c:138`（memset） |
| 24 | 2 | wlan_channellist | 首次连接为 `0x0000`，重连为 `0x4000` | `sq/slimproto.c:143` |
| 26 | 4 | bytes_received 高 32 位 | 当前音频流已收字节数 | `sq/slimproto.c:144` |
| 30 | 4 | bytes_received 低 32 位 | 同上 | `sq/slimproto.c:145` |
| 34 | 2 | lang | 全 0 | — |
| 36 | n | 能力串 | ASCII 文本，不带结尾 NUL | `sq/slimproto.c:150-155` |

- `wlan_channellist` 的位：`0x8000` 表示带位图显示屏，`0x4000` 表示重连，低 14 位是信道列表（`lms/Slim/Networking/Slimproto.pm:972-974`）。
- LMS 按载荷长度判断有没有 UUID：不少于 36 字节就按带 UUID 的格式解析；超过 36 字节的部分是能力串（`lms/Slim/Networking/Slimproto.pm:957-988`）。
- LMS 的 deviceid 表：`2 squeezebox, 3 softsqueeze, 4 squeezebox2, 5 transporter, 6 softsqueeze3, 7 receiver, 8 squeezeslave, 9 controller, 10 boom, 11 softboom, 12 squeezeplay`（`lms/Slim/Networking/Slimproto.pm:28`）。12 和 9 都用 SqueezePlay 类处理（`:1103-1106`）。遇到未知 deviceid 时，回一帧 `dsco` 后关闭连接（`:1029-1036,1113-1121`）。
- 客户端 ID 就是 MAC 字符串（`lms/Slim/Networking/Slimproto.pm:990-991`）。

### 能力串

Squeezelite 按顺序拼三段（`sq/slimproto.c:122-156,883-895,944-962`）：

1. 基础段：编译时带 SSL 才有 `CanHTTPS=1,`；其后是 `Model=squeezelite,AccuratePlayPoints=1,HasDigitalOut=1,HasPolarityInversion=1,Balance=1,Firmware=<版本>`。
2. 固定段：`,ModelName=<-M 的值，默认 SqueezeLite>,MaxSampleRate=<数值>`，之后每个已注册的编解码依次加 `,<types>`。
3. 可变段：播放器和服务器在同一台主机上时（本端地址等于对端地址）加 `,loc`；收到带编组号的 `serv` 后，下次连接时加 `,SyncgroupID=<10 位数字>`。

示例（全功能构建、默认参数）：

```
CanHTTPS=1,Model=squeezelite,AccuratePlayPoints=1,HasDigitalOut=1,HasPolarityInversion=1,Balance=1,Firmware=2.0.0-1613,ModelName=SqueezeLite,MaxSampleRate=384000,dsf,dff,alc,aac,ogg,ops,ogf,flc,aif,pcm,mp3
```

编解码标记与 `strm` 格式码的对应：

| 能力串里的标记 | 解码器 | strm 格式码 | 来源 |
|---|---|---|---|
| `dsf,dff` | DSD | `d` | `sq/dsd.c:888-897` |
| `alc` | ALAC（libalac 或 ffmpeg） | `l` | `sq/alac.c:542-547`、`sq/ffmpeg.c:789-793` |
| `wma` 等 | ffmpeg 版 WMA（仅带 FFMPEG 的构建） | `w` | `sq/decode.c:165-169` |
| `aac` | faad | `a` | `sq/faad.c:646-651` |
| `ogg` | vorbis | `o` | `sq/vorbis.c:355-360` |
| `ops` | opus（可选） | `u` | `sq/opus.c:300-305` |
| `ogf,flc` | FLAC（`ogf` 指 Ogg 封装的 FLAC） | `f` | `sq/flac.c:385-390` |
| `aif,pcm`（带 `-W` 时是 `wav,aif,pcm`） | PCM | `p` | `sq/pcm.c:455-488` |
| `mp3` | mad 或 mpg123 | `m` | `sq/mad.c:400-405`、`sq/mpg.c:249-254` |

- 默认注册顺序：dsd、alac、aac、ogg、ops、flac、pcm、mp3（`sq/decode.c:155-200`）。用 `-c` 指定时按 `-c` 的顺序；`-e` 用来排除（`sq/main.c:86-97`）。是否带 DSD、ALAC、FFMPEG、OPUS、SSL、RESAMPLE 取决于构建选项（`sq/Makefile:7-31,66-82`）。
- LMS 的解析规则：匹配 `^[a-z][a-z0-9]{1,4}$` 的项算编解码，其余按 `键=值` 解析，没有 `=` 的项值为 1（`lms/Slim/Player/SqueezePlay.pm:166-202`）。按这条规则，`loc` 也会被当成一个“格式”。

LMS 认识的能力键（`lms/Slim/Player/SqueezePlay.pm:80-101`）：

| 键 | 含义 | Squeezelite 是否发 |
|---|---|---|
| `Model` | 型号（`squeezelite`） | 发 |
| `ModelName` | 显示用型号名（`-M` 可改） | 发 |
| `MaxSampleRate` | 最高采样率（Hz） | 发 |
| `AccuratePlayPoints` | 播放点精确，LMS 据此决定同步时是否需要加权播放点 | 发，值为 1 |
| `Firmware` | 版本字符串 | 发 |
| `HasDigitalOut` | 有数字输出 | 发，值为 1 |
| `HasPreAmp`、`HasDisableDac` | 前级、禁用 DAC | 不发 |
| `HasPolarityInversion` | 支持极性反转 | 发，值为 1 |
| `Balance` | 支持左右平衡 | 发，值为 1 |
| `CanHTTPS` | 能直连 HTTPS | 带 SSL 的构建才发 |
| `SyncgroupID` | 切换服务器时带过去的同步编组号 | 仅在 `serv` 之后发 |
| `Rhap`、`Rtmp`、`Spdirect`、`Proxy` | 其他（不涉及本任务） | 不发 |

- **能力串里没有表示“固定音量”的键。** 固定音量是服务器侧的设置，通过 `audg` 的 adjust 字节下发（见第 8 节）。`HasDigitalOut=1` 在 Squeezelite 里写死，不代表真的接了数字输出。
- DSD 能力只通过 `dsf,dff` 两个格式标记表达。DoP 还是原生 DSD 输出由播放器的 `-D` 参数决定，不写进能力串（见第 9 节）。

### LMS 在 HELO 之后发了什么（按顺序，供参照）

1. 收到 HELO 就取消“5 秒内没收到 HELO 就关连接”的定时器（`lms/Slim/Networking/Slimproto.pm:953`）。
2. `reconnect`：只在 revision 大于 39 时发 `vers`，对 Squeezelite 不发（`lms/Slim/Player/Squeezebox.pm:62-65`）。属于伪重连（重连位为 0、客户端已知）时，按 `bytes_received` 接着播；服务器认为该播放器处于停止状态时发 `strm q`（`:100-115`）。
3. 发 `setd` id 0（查询名称）和 `setd` id 4（查询 disableDac）（`lms/Slim/Player/Squeezebox2.pm:73-79`）。
4. 发 `aude`（按服务器记录的开关机状态）和 `audg`（当前音量）（`lms/Slim/Networking/Slimproto.pm:1264-1266`）。
5. 每 5 秒发一次 `strm t`（`lms/Slim/Networking/Slimproto.pm:199-241`、`lms/Slim/Player/Squeezebox2.pm:383-385`）。

---

## 3. 报文帧

### 结论

| 方向 | 帧头 | 长度字段的含义 | 上限 |
|---|---|---|---|
| 服务器→播放器 | 2 字节长度（u16 大端）+ 4 字节命令字 + 载荷 | 长度 = 4（命令字）+ 载荷长度，**不含**这 2 字节本身 | 播放器缓冲 4096 字节；长度超过 4096 时播放器判定致命错误并断开 |
| 播放器→服务器 | 4 字节命令字 + 4 字节长度（u32 大端）+ 载荷 | 长度 = 载荷长度，**不含**前 8 字节 | LMS 不设上限，按长度读够再处理 |

| 事实 | 来源 |
|---|---|
| 播放器先读 2 字节作长度，再读这么多字节，得到命令字加载荷 | `sq/slimproto.c:563-599`（长度 `:589`，上限 `:591-594`） |
| 播放器的接收缓冲 `MAXBUF` 为 4096 | `sq/slimproto.c:34` |
| 播放器按前 4 字节查命令表，查不到只记一条警告，不会断开 | `sq/slimproto.c:517-539` |
| LMS 发帧：`pack('n', len+4) . type . data` | `lms/Slim/Player/Squeezebox.pm:1150-1174` |
| LMS 收帧：前 4 字节命令字，`N` 长度，处理半包 | `lms/Slim/Networking/Slimproto.pm:362-444` |
| 播放器发帧时，`length = sizeof(struct) - 8 + 变长部分` | `sq/slimproto.c:140,177,211,224,237,252` |

- 收到 HELO 之前就来了别的命令字时，LMS 关闭连接（`lms/Slim/Networking/Slimproto.pm:433-443`）。
- 服务器发给播放器的单帧不能超过 4096（含命令字），`strm s` 带的 HTTP 请求因此也受这个限制，见第 4 节。

---

## 4. `strm`

### 结论

`strm` 的载荷固定 24 字节，后面可以跟变长数据，只有 `s` 命令用到。命令字决定各字段怎么用。

### 固定头（载荷偏移，不含 `strm` 四字节）

| 偏移 | 长度 | 字段 | 含义 | 来源 |
|---|---|---|---|---|
| 0 | 1 | command | `s`/`p`/`u`/`q`/`t`/`f`/`a` | `sq/slimproto.h:108-128` |
| 1 | 1 | autostart | ASCII `'0'`～`'3'`，见下 | `sq/slimproto.c:355` |
| 2 | 1 | format | 格式码：`p`/`f`/`d`/`o`/`u`/`a`/`l`/`m`/`w`/`?` | `sq/slimproto.c:362-370` |
| 3 | 1 | pcm_sample_size | ASCII，见 PCM 表；FLAC 填 `'o'` 表示 Ogg 封装 | `sq/pcm.c:441`、`sq/flac.c:294` |
| 4 | 1 | pcm_sample_rate | ASCII 码表，见下 | `sq/pcm.c:65-67,442` |
| 5 | 1 | pcm_channels | `'1'` 或 `'2'` | `sq/pcm.c:443` |
| 6 | 1 | pcm_endianness | `'0'` 大端，`'1'` 小端 | `sq/pcm.c:444` |
| 7 | 1 | threshold | 开始解码前流缓冲至少要攒的 KB 数（u8，最大 255） | `sq/slimproto.c:378`（`*1024`）、`sq/stream.c:733-736` |
| 8 | 1 | spdif_enable | Squeezelite 不用 | — |
| 9 | 1 | transition_period | 渐变时长，单位秒（u8） | `sq/slimproto.c:386` |
| 10 | 1 | transition_type | ASCII：`'0'` 无，`'1'` 交叉渐变，`'2'` 渐入，`'3'` 渐出，`'4'` 渐入加渐出 | `sq/slimproto.c:385`、`sq/squeezelite.h:640` |
| 11 | 1 | flags | 位定义见下 | `sq/slimproto.c:376,387-388` |
| 12 | 1 | output_threshold | 输出缓冲攒够这么多才开始出声，单位 0.1 秒 | `sq/output.c:63`、`sq/decode.c:266-270` |
| 13 | 1 | slaves | LMS 填同步从机流数，Squeezelite 不用 | `lms/Slim/Player/Squeezebox.pm:1052` |
| 14 | 4 | replay_gain | 按命令复用，见下 | `sq/slimproto.c:285,308,324,334,384` |
| 18 | 2 | server_port | 取流 HTTP 端口（网络序） | `sq/slimproto.c:349` |
| 20 | 4 | server_ip | 取流 IPv4（网络序）；0 表示用 SlimProto 连接的服务器 IP | `sq/slimproto.c:348-350` |
| 24 | n | request | 仅 `s`：原样发给取流服务器的 HTTP 请求 | `sq/slimproto.c:346-347` |

LMS 的打包格式是 `aaaaaaaCCCaCCCNnN`，正好 24 字节：前 7 个字段按字符打包，`transition_type` 按字符打包，其余按数值打包（`lms/Slim/Player/Squeezebox.pm:1038-1058,1106-1126`）。

### 命令字语义

| 命令 | 播放器的动作 | 播放器回的 STAT | replay_gain 字段的用法 | 来源 |
|---|---|---|---|---|
| `s` 开流 | 先回 STMf；按格式打开解码器；连接取流服务器并发出请求；再回 STMc；记下输出阈值、回放增益、渐变、极性和声道设置 | STMf，然后 STMc | 16.16 定点的回放增益，0 表示不调 | `sq/slimproto.c:344-392` |
| `p` 暂停 | 间隔为 0：输出进入停止状态，**缓冲保留**，回 STMp；间隔非 0：插入这么多毫秒的静音后继续（同步微调用），不回 STAT | STMp（仅间隔为 0 时） | 毫秒间隔 | `sq/slimproto.c:306-321`、`sq/output.c:86-96` |
| `u` 继续 | 时间值为 0：立即出声；非 0：等播放器本地时钟到这个 jiffies 值再出声（超过当前时间 10 秒以上的值视为无效，立即开始） | STMr | 播放器 jiffies（毫秒） | `sq/slimproto.c:332-343`、`sq/output.c:99-109` |
| `q` 停止 | 清空解码器和输出缓冲，断开取流，`frames_played` 归零，清空流缓冲 | STMf | 不用 | `sq/slimproto.c:287-294` |
| `f` 冲刷 | 解码器停止；把输出缓冲里“还没开始播”的下一首截掉（截到 track_start 处），**正在播的部分保留**；断开取流；清空流缓冲 | 真的断开了连接或截掉了数据才回 STMf | 不用 | `sq/slimproto.c:295-305`、`sq/output.c:455-466` |
| `t` 状态 | 立即回一个 STMt，把 replay_gain 字段原样放进 `server_timestamp` | STMt | 服务器时间戳（LMS 现在填 0） | `sq/slimproto.c:284-286`、`lms/Slim/Player/Squeezebox.pm:1099-1101` |
| `a` 跳过 | 丢掉输出缓冲里这么多毫秒的数据，不出声（同步微调用） | 无 | 毫秒间隔 | `sq/slimproto.c:322-331`、`sq/output.c:70-83` |

### autostart

| 值 | 含义 | 来源 |
|---|---|---|
| `'0'` | 流缓冲到阈值后开始解码，回 **STMl**，输出不自动开始，要等服务器发 `strm u`（同步开播或暂停中开流时用） | `sq/slimproto.c:673-676` |
| `'1'` | 流缓冲到阈值后开始解码，输出进入缓冲状态，攒够 output_threshold 就自动出声 | `sq/slimproto.c:677-680,713-715`、`sq/output.c:63-67` |
| `'2'` / `'3'` | 与 `'0'`/`'1'` 相同，但收到 HTTP 头后要等服务器发 `cont` 才开始缓冲（直连第三方流、让服务器先看响应头时用） | `sq/slimproto.c:364-368,399-415`、`sq/stream.c:637` |

格式码为 `?` 时必须用 autostart 2 或 3，服务器看完 RESP 后用 `codc` 告诉播放器格式（`sq/slimproto.c:362-370,417-422`、`sq/slimproto.h:171-179`）。

### flags

| 位 | 含义 | 来源 |
|---|---|---|
| `0x03` | 两位都置 1 时反转极性 | `sq/slimproto.c:387`；LMS 填 `polarityInversion` 设置（`lms/Slim/Player/Squeezebox.pm:899`） |
| `0x0C` | 右移 2 位后是输出声道：1 只出左声道，2 只出右声道，3 左右合并 | `sq/slimproto.c:388`、`sq/squeezelite.h:642-643`、`lms/Slim/Player/Squeezebox.pm:904-908` |
| `0x20` | 取流走 TLS（端口是 443 时也会自动走 TLS） | `sq/slimproto.c:376`、`sq/stream.c:900-904` |
| `0x40` | LMS：重连标志，Squeezelite 不用 | `lms/Slim/Player/Squeezebox.pm:897` |
| `0x80` | LMS：循环播放，Squeezelite 不用 | `lms/Slim/Player/Squeezebox.pm:898` |

### PCM 字段编码

| 字段 | 编码 | 来源 |
|---|---|---|
| sample_size | `'0'` 8 位，`'1'` 16 位，`'2'` 24 位，`'3'` 32 位 | `sq/pcm.c:441`、`lms/Slim/Player/Squeezebox.pm:1135-1148` |
| sample_rate | `'0'` 11025，`'1'` 22050，`'2'` 32000，`'3'` 44100，`'4'` 48000，`'5'` 8000，`'6'` 12000，`'7'` 16000，`'8'` 24000，`'9'` 96000，`':'` 88200，`';'` 176400，`'<'` 192000，`'='` 352800，`'>'` 384000，`'?'` 不用，`'@'` 705600，`'A'` 768000，`'B'` 1411200，`'C'` 1536000 | `sq/pcm.c:65-67`；LMS 的表只到 `'A'`（`lms/Slim/Player/SqueezePlay.pm:236-266`） |
| channels | `'1'` 或 `'2'`；其他声道数会报 “unsupported channels” | `sq/pcm.c:443,415-416` |
| endianness | `'0'` 大端，`'1'` 小端 | `sq/pcm.c:444` |

- **PCM 流必须是不带文件头的裸样本。** 只有启动时用了 `-W`，或者流来自本地文件，播放器才会解析 WAV/AIFF 头（`sq/pcm.c:192-194`、`sq/main.c:538-540`）。不满足这两条时，WAV 头会被当成样本播出去。
- 带 `-W` 时，播放器用头里的参数覆盖 `strm` 里的参数（`sq/pcm.c:145-167`）。
- 24 位 PCM 里检测到 DoP 标记时，按 DoP 处理（`sq/pcm.c:218-234`）。

### FLAC 的采样率字段

- LMS 开 FLAC 流时，四个 PCM 字段都填 `'?'`；只有 Ogg 封装的 FLAC 在 sample_size 填 `'o'`（`lms/Slim/Player/Squeezebox.pm:635-656`）。
- 播放器的 FLAC 解码器只看 sample_size 是不是 `'o'`，其余字段不看（`sq/flac.c:281-312`）。
- 采样率取自**每条流第一帧的帧头**（`sq/flac.c:174-205`），所以 FLAC 是自描述的。
- 位深支持 8、16、20、24、32；声道只取前两个，单声道复制到两边（`sq/flac.c:171-172,227-254`）。
- 输出采样率只在一条流开头的 new_stream 时设定一次。同一条 FLAC 流中途换采样率时，播放器不会跟着切换（据 `sq/flac.c:174-205` 推断）。
- LMS 对不低于 88.2 kHz 的 FLAC 把 output_threshold 设为 20，即 2 秒（`lms/Slim/Player/Squeezebox.pm:644-649`）。

### 最高采样率由什么决定

- `MaxSampleRate` = `-Z` 的值，或者输出设备支持的最高采样率 `supported_rates[0]`（`sq/slimproto.c:883-888`）。
- 不带重采样的构建里，`-Z` 只能把上限调低；带 RESAMPLE 的构建里，`-Z` 可以任意设，由重采样兜住（`sq/slimproto.c:884-888`、`sq/main.c:150`）。
- `supported_rates` 的来源：打开设备时按 `TEST_RATES`（1536000 到 8000）逐个探测；或者用 `-r` 指定（最大值、区间或列表）（`sq/output.c:393-404`、`sq/main.c:478-524`、`sq/squeezelite.h:645`）。
- `-o -`（输出到 stdout）且没给 `-r` 时，默认只支持 44100（`sq/output_stdout.c:152-155`）。
- 能力串只报最高采样率，不报完整列表。LMS 只拿这个上限去判断要不要降采样：上限是 48k 的整数倍、源是 44.1k 的整数倍时，会换算到 44.1k 那一族（`lms/Slim/Player/CapabilitiesHelper.pm:12-36`）。

### 长度上限

- 帧长 ≤ 4096，所以 HTTP 请求 ≤ 4096 − 4 − 24 = 4068 字节（`sq/slimproto.c:34,591`）。
- 播放器还检查请求长度不超过 `MAX_HEADER - 1`（4095），超过就丢弃这条 `strm s`，只回过 STMf（`sq/slimproto.c:357-361`、`sq/squeezelite.h:269`）。

---

## 5. 取流

### 结论

- 播放器收到 `strm s` 后，在 SlimProto 线程里**同步**连接 `server_ip:server_port`，连接超时 10 秒。连上后把 `strm` 尾部的字节**原样**发出去。
- 请求串由服务器自己拼，必须以 `\r\n\r\n` 结尾。LMS 用的是 `GET /stream.mp3?player=<MAC> HTTP/1.0\r\n` + 可选的 `Authorization` + `\r\n`。
- 播放器**不解析状态行，也不解析响应头**，只读到连续 4 个 CR/LF 字符为止（即 `\r\n\r\n`），然后把整段响应头用 `RESP` 原样转给服务器。头部超过 4095 字节就断开。
- 之后收到的字节全部当音频数据。服务器**关闭连接**就是流结束（正常结束）。
  - 不需要 `Content-Length`。
  - 播放器**不处理分块传输编码（chunked）**，必须直接发原始字节流。
  - 用 HTTP/1.0 或 `Connection: close`。
  - 只有服务器在 autostart ≥ 2 时用 `cont` 给出 metaint，播放器才处理 ICY 元数据。
- 连接失败时，播放器把流状态记为“不可达”，在下一个 100 ms 周期里发 `DSCO` 原因 3。
- Squeezelite 不发 STMe 和 STMh。连上时发 STMc（在 `strm s` 处理末尾），收到响应头时发 RESP，连接结束时发 DSCO。

### 细节

| 事实 | 来源 |
|---|---|
| 从请求里的 `Host:` 取主机名，只用作 TLS 的 SNI | `sq/stream.c:893-898` |
| 连接超时 10 秒；设 TCP 接收缓冲；非阻塞 | `sq/stream.c:235-253` |
| 失败时 `stream.state = DISCONNECT`，原因 `UNREACHABLE` = 3 | `sq/stream.c:906-911`、`sq/squeezelite.h:547` |
| 连上后先清空流缓冲，再发请求 | `sq/stream.c:914-933`、`sq/stream.c:174-207,576-582` |
| 逐字节读响应头；遇到 CR 或 LF 计数，连续 4 个即头部结束 | `sq/stream.c:587-646`（`:632-639`） |
| 响应头超过 `MAX_HEADER-1` 就断开，原因 `LOCAL_DISCONNECT` = 1 | `sq/stream.c:627-630` |
| 头部结束后：autostart < 2 进入缓冲；autostart ≥ 2 等 `cont` | `sq/stream.c:637` |
| 已收字节超过 threshold 后进入 `STREAMING_HTTP`，解码器开始工作 | `sq/stream.c:733-736`、`sq/slimproto.c:670-682` |
| `recv` 返回 0 时为 `DISCONNECT_OK`；读出错时为 `REMOTE_DISCONNECT` = 2 | `sq/stream.c:709-719` |
| 第一次读头部就被关闭、且没走 TLS 时，会用 TLS 重试一次 | `sq/stream.c:599-617` |
| 有 metaint 时，每隔 metaint 字节读 1 字节长度 × 16 的元数据，再用 META 转给服务器 | `sq/stream.c:648-696` |
| 流缓冲默认 2 MB；`-b` 可改 | `sq/squeezelite.h:265`、`sq/main.c:85` |
| RESP 只发一次，内容是收到的原始响应头 | `sq/slimproto.c:654-660,219-230` |
| DSCO 在 `stream.state == DISCONNECT` 时发，载荷 1 字节原因码 | `sq/slimproto.c:649-653,206-217` |

LMS 给播放器的 `/stream.mp3` 响应：带 Content-Type、`Connection: close`。只有请求里带了 ICY 元数据请求头，才加 `icy-metaint`；LMS 给播放器拼的请求里不带（`lms/Slim/Web/HTTP.pm:1180-1208`）。

DSCO 原因码（`lms/Slim/Networking/Slimproto.pm:604-610`、`sq/squeezelite.h:547`）：0 正常关闭，1 本地重置，2 远端重置，3 不可达，4 超时。

---

## 6. STAT

### 结论

STAT 的载荷固定 53 字节，LMS 也按 53 字节解析。事件码是载荷前 4 个字节。Squeezelite 实际会发的事件有：`STMc STMd STMf STMl STMn STMo STMp STMr STMs STMt STMu`。

### 布局（载荷偏移，不含 `STAT` 和长度共 8 字节）

| 偏移 | 长度 | 字段 | Squeezelite 填的值 | 来源 |
|---|---|---|---|---|
| 0 | 4 | event | 事件码，4 个 ASCII 字符 | `sq/slimproto.h:45-65` |
| 4 | 1 | num_crlf | 0 | — |
| 5 | 1 | mas_initialized | 0 | — |
| 6 | 1 | mas_mode | 0 | — |
| 7 | 4 | stream_buffer_size | 流缓冲总大小（字节） | `sq/slimproto.c:182` |
| 11 | 4 | stream_buffer_fullness | 流缓冲已用（字节，未解码数据） | `sq/slimproto.c:181` |
| 15 | 4 | bytes_received_H | 当前流已收字节，高 32 位 | `sq/slimproto.c:183` |
| 19 | 4 | bytes_received_L | 低 32 位 | `sq/slimproto.c:184` |
| 23 | 2 | signal_strength | 0xFFFF（有线） | `sq/slimproto.c:185` |
| 25 | 4 | jiffies | 播放器单调时钟毫秒（u32，约 49.7 天回绕） | `sq/slimproto.c:186`、`sq/utils.c:103-115` |
| 29 | 4 | output_buffer_size | 输出缓冲总大小（字节，每帧 8 字节） | `sq/slimproto.c:187`、`sq/squeezelite.h:266,450` |
| 33 | 4 | output_buffer_fullness | 输出缓冲已用（已解码、未出声） | `sq/slimproto.c:188` |
| 37 | 4 | elapsed_seconds | 已播时间（秒） | `sq/slimproto.c:189` |
| 41 | 2 | voltage | 0 | — |
| 43 | 4 | elapsed_milliseconds | 已播时间（毫秒） | `sq/slimproto.c:191` |
| 47 | 4 | server_timestamp | 原样带回 `strm t` 的 replay_gain 字段，其他事件填 0 | `sq/slimproto.c:192` |
| 51 | 2 | error_code | 0 | — |

LMS 的解包格式是 `a4CCCNNNNnNNNNnNNn`；长度不是 53 或 57 时，把 error_code 当作 0（`lms/Slim/Networking/Slimproto.pm:751-777`）。

### elapsed 的算法（播放器侧）

- 有采样率、并且 `frames_played > device_frames` 时：`ms = (frames_played − device_frames) × 1000 / 当前采样率 + (现在 − 上次测量时刻)`（`sq/slimproto.c:163-166`）。
  - `frames_played` 从**本条流开始出声**时归零（`sq/output.c:154`），所以 elapsed 是“当前这条流”的已播时长，不是整个队列的。
  - `device_frames` 是声卡里还没出声的帧数（ALSA 的 delay），已经扣掉，所以 elapsed 对应实际出声的位置（`sq/output_alsa.c:831-851`）。
- 否则如果已经开始播放：`ms = 现在 − 本条流开始出声的时刻`（`sq/slimproto.c:167-169`）。
- 暂停时设备仍在输出静音，`frames_played` 不再增加，elapsed 停住不动（`sq/output.c:112-115,280-283`）。

### 事件码

| 事件 | 含义 | Squeezelite 何时发 | 来源 |
|---|---|---|---|
| STMa | 自动开始（旧固件） | 不发 | `lms/Slim/Networking/Slimproto.pm:733` |
| STMc | 正在连接取流 | `strm s` 处理末尾（连接成功或失败之后） | `sq/slimproto.c:380` |
| STMd | 解码完成，可以接下一首 | 流已结束、解码器把缓冲读完（DECODE_COMPLETE）时，同时断开取流套接字 | `sq/slimproto.c:683-690`、`sq/pcm.c:207-211`、`sq/flac.c:149` |
| STMe | 连接建立（旧固件） | 不发 | — |
| STMf | 已关闭或冲刷 | `strm q`、`strm f`（有东西被冲掉时）、`strm s` 开头 | `sq/slimproto.c:292,300-302,357` |
| STMh | 收到 HTTP 头（旧固件） | 不发，改发 RESP | — |
| STMl | 缓冲已满，等待开播 | 仅 autostart 0：解码器就绪并开始解码时 | `sq/slimproto.c:673-676` |
| STMn | 解码器不支持或解码出错 | DECODE_ERROR | `sq/slimproto.c:685` |
| STMo | 输出欠载（流仍在传输） | 正在出声、输出缓冲为空、流仍处于 `STREAMING_HTTP`（每条流只报一次） | `sq/slimproto.c:725-729` |
| STMp | 已暂停 | `strm p` 且间隔为 0 | `sq/slimproto.c:318` |
| STMr | 已继续 | `strm u` | `sq/slimproto.c:341` |
| STMs | 新的一条流开始出声 | 输出读到 track_start 标记、真正开始播新流时 | `sq/output.c:126-165`、`sq/slimproto.c:702-706` |
| STMt | 心跳或定时 | 出声期间约每秒一次，另外每收到 `strm t` 回一次 | `sq/slimproto.c:734-737,285` |
| STMu | 欠载，播放结束 | 正在出声、输出缓冲为空、流已断开（≤ DISCONNECT）、解码器已停：说明全部播完；输出转为停止 | `sq/slimproto.c:716-724` |
| STMz | LMS 内部用的伪事件，表示 DSCO 正常结束 | — | `lms/Slim/Networking/Slimproto.pm:747` |

同一个 100 ms 周期内的发送顺序：DSCO、STMs、STMd、STMt、STMl、STMu、STMo、STMn、RESP、META（`sq/slimproto.c:754-763`）。

### 服务器怎么用这些值（LMS）

| 用途 | 做法 | 来源 |
|---|---|---|
| 心跳 | 每收到一个 STAT 就刷新心跳时间 | `lms/Slim/Networking/Slimproto.pm:708-709` |
| 时钟对齐 | 用 `jiffies/1000` 和服务器收到 STAT 的时刻之差估计 jiffiesEpoch，取历史最小偏移，缓慢修正 | `lms/Slim/Player/Player.pm:932-985`；换算 `jiffiesToTimestamp = epoch + jiffies/1000 − 包延迟`（`:987-996`） |
| 播放位置 | 取 elapsed_ms/1000（ms 比 s 小时说明发生了回绕，改用 s 加上 ms 的余数）；正在播时再加上“现在 − jiffies 对应的服务器时刻”外推；最后加上本曲的起始偏移（跳转时的 seek 偏移） | `lms/Slim/Player/Squeezebox2.pm:434-462`、`lms/Slim/Player/StreamingController.pm:1719-1743` |
| 曲目开始 | STMs → `playerTrackStarted` → 状态 Playing，切换“正在播放” | `lms/Slim/Player/Squeezebox2.pm:170-171`、`lms/Slim/Player/StreamingController.pm:331-399,2250-2266` |
| 流解码完 | STMd → readyToStream → 发下一条 `strm s` | `lms/Slim/Player/Squeezebox2.pm:150-152`、`lms/Slim/Player/StreamingController.pm:2268-2284` |
| 播放结束 | STMu → `playerStopped` | `lms/Slim/Player/Squeezebox2.pm:161-163`、`lms/Slim/Player/StreamingController.pm:2223-2248` |
| 中途欠载 | STMo → `OutputUnderrun` → 播放中则 `_Rebuffer`：发 `strm p` 加重新缓冲，攒够后再继续 | `lms/Slim/Player/Squeezebox2.pm:172-173`、`lms/Slim/Player/StreamingController.pm:231-237,1659-1671` |
| 缓冲就绪 | STMl → BufferReady → `_StartIfReady` → `strm u` | `lms/Slim/Player/Squeezebox2.pm:158-160`、`lms/Slim/Player/StreamingController.pm:189-195,1403-1427,1519-1534` |
| 不支持的格式 | STMn → `playerStreamingFailed` | `lms/Slim/Player/Squeezebox2.pm:153-157` |
| 取流正常结束 | DSCO 原因 0 → `EoS` → EndOfStream；缓冲中遇到它就立即开始播（处理很短的曲目） | `lms/Slim/Networking/Slimproto.pm:676-678`、`lms/Slim/Player/StreamingController.pm:210-216` |
| 取流异常结束 | DSCO 原因非 0 → 已在播则按出错重试，否则判开流失败 | `lms/Slim/Networking/Slimproto.pm:627-675` |
| 忽略过期事件 | 第一个 STMc 之前的事件一律忽略（STMc 设置 streamStartTimestamp） | `lms/Slim/Player/Squeezebox2.pm:143-147` |
| 缓冲占用 | `stream_buffer_size` 记为 bufferSize，`fullness` 记为当前占用；输出缓冲占用另外保存 | `lms/Slim/Networking/Slimproto.pm:794-802` |

---

## 7. 无缝

### 结论

- **时序**：播放器回 STMd 时，旧流的数据已经全部从流缓冲解码进了输出缓冲，但输出缓冲里还有几秒没播。服务器这时发下一条 `strm s`。播放器把新流解码出的数据接在旧数据**后面**，并在交界处打上 track_start 标记。输出读到这个标记时切换参数并发 STMs。两段之间不插静音，所以是无缝的。
- **必须等 STMd 再发下一条 `strm s`。** `strm s` 会清空流缓冲、重开解码器（`sq/stream.c:914`、`sq/decode.c:282-283`）。提前发的话，旧流还没解码的数据会丢掉，已经进了输出缓冲的部分照常播完。
- `strm s` 不会清空输出缓冲（与 `q`/`f` 不同），所以正在播的内容不会中断（`sq/slimproto.c:344-392`）。
- **一条流只有在服务器关闭 HTTP 连接后才会产生 STMd。** 长流不结束，就不会有 STMd。
- **采样率不同的两条流**（`sq/output.c:126-165`）：
  - 到交界处时，如果 `-r` 设了切换延迟，就在交界前后各插一半的静音。
  - 输出后端发现采样率变了，就按新采样率重开设备（ALSA 线程比较 `alsa.rate` 和 `current_sample_rate`，见 `sq/output_alsa.c:682-716`）。
  - 结果是有短暂的间隙（设备重开，外加可选的延迟），但播放器**不断开**，不需要服务器介入。
  - 采样率不同时，交叉渐变自动关闭（`sq/output.c:326-331`）。
  - PCM 与 DSD/DoP 之间切换时，用 `-D` 里设的延迟（`sq/output.c:135-139`）。
- 回放增益在交界处切换成新流的值（交叉渐变时等渐变完成再切换）（`sq/output.c:161-163,203-212`）。
- 播放器发 STMs 时，elapsed 从 0 重新计。服务器要把它对应到新流的起点（`sq/output.c:154-157`）。
- LMS 的流程：STMd → `ReadyToStream` → Playing+Streaming 状态下 `_NextIfMore` → 取下一曲 → `_Stream` → `strm s`（`lms/Slim/Player/StreamingController.pm:217-223,1003-1015,1144-1357`）。新流的 STMs 到达后才把“正在播放”换成下一曲（`:196-202,331-399`）。

---

## 8. 暂停、继续、停止、跳转、音量

### 结论

| 操作 | 服务器发 | 播放器回 | 说明 |
|---|---|---|---|
| 暂停 | `strm p`，replay_gain 为 0 | STMp | 缓冲保留，取流连接不断。流缓冲满了之后播放器不再读 TCP，服务器那边的写操作会被反压阻塞。LMS 先把音量渐弱到 0 再发 `p`（`lms/Slim/Player/StreamingController.pm:1552-1586`） |
| 继续 | `strm u`，replay_gain 为 0（立即） | STMr | LMS 先发 `audg` 把音量设为 0，再发 `u`，然后渐强（`lms/Slim/Player/StreamingController.pm:1620-1656`） |
| 停止 | `strm q` | STMf | 清空一切（`lms/Slim/Player/Squeezebox.pm:206-216`） |
| 跳转 | `strm q`，然后新的 `strm s`（服务器自己从新位置开流） | STMf，然后 STMf、STMc… | 播放器的 elapsed 从 0 开始；位置 = seek 偏移 + elapsed（`lms/Slim/Player/StreamingController.pm:1092-1142,1729-1733`） |
| 取消预取的下一首 | `strm f` | STMf（如果真有东西被冲掉） | 正在播的部分保留（`lms/Slim/Player/StreamingController.pm:990-1001`、`lms/Slim/Player/Squeezebox2.pm:387-396`） |
| 音量 | `audg` | 无 | 见下 |
| 开关机 | `aude` | 无 | 见下 |

### `audg` 布局（载荷偏移）

| 偏移 | 长度 | 字段 | 含义 | 来源 |
|---|---|---|---|---|
| 0 | 4 | old_gainL | 旧式增益（1.7 定点，0～128），Squeezelite 不用 | `sq/slimproto.h:138-147`、`lms/Slim/Player/Squeezebox2.pm:182-211` |
| 4 | 4 | old_gainR | 同上 | — |
| 8 | 1 | adjust | LMS 的 `digitalVolumeControl`：1 表示按下面的增益调；0 表示**固定音量**，播放器按 1.0 输出 | `sq/slimproto.c:447`、`lms/Slim/Player/Squeezebox2.pm:297-300` |
| 9 | 1 | preamp | 前级，Squeezelite 不用 | `lms/Slim/Player/Squeezebox2.pm:302` |
| 10 | 4 | gainL | 新式增益，16.16 定点，`0x10000` = 1.0 = 0 dB，0 = 静音 | `sq/squeezelite.h:448`、`sq/output_pack.c:33-38` |
| 14 | 4 | gainR | 同上 | — |
| 18 | 4 | sequence | 可选的序号（LMS 对 SqueezePlay 类会带），Squeezelite 不用 | `lms/Slim/Player/Squeezebox2.pm:308-317` |

- 播放器的处理：`adjust` 非 0 时用 gainL/gainR；否则用 `FIXED_ONE`（`sq/slimproto.c:440-448`）。
- 软件音量：直接乘到样本上（`sq/output.c:54-57`、`sq/output_alsa.c:232-238`）。
- 用 `-V <control>` 时，改为把增益换算成 dB 去设 ALSA 硬件混音器，内部增益保持 1.0（`sq/output_alsa.c:239-250`）。
- DSD 或 DoP 输出时，增益强制为 1.0（`sq/output.c:172-176`）。
- **内部增益初始为 0**：`struct outputstate output` 是全局变量，ALSA 和 PortAudio 的初始化不给增益赋值；stdout 输出初始化时还会把它清零（`sq/output.c:28`、`sq/output_stdout.c:139`）。所以至少要发一次 `audg`。
- LMS 的音量曲线：音量 0～100 先换成 dB，再换成 16.16。SqueezePlay 类的总范围是 −74 dB，分段点在 25、分段比例 0.5；音量为 0 或负数时增益为 0（`lms/Slim/Player/SqueezePlay.pm:219-228`、`lms/Slim/Player/Squeezebox2.pm:213-228,241-321`）。
- 左右平衡：LMS 按 balance 设置分别缩放左右增益（`lms/Slim/Player/Squeezebox2.pm:305-307`）。

### `aude` 布局

| 偏移 | 长度 | 字段 | 含义 |
|---|---|---|---|
| 0 | 1 | enable_spdif | 0：输出进入关闭状态（相当于关机，ALSA 设备关闭）；1：从关闭恢复到停止状态（仅在没用 `-C` 空闲关闭时） |
| 1 | 1 | enable_dac | Squeezelite 不用 |

来源：`sq/slimproto.h:131-135`、`sq/slimproto.c:424-438`；LMS 两字节填同一个值（`lms/Slim/Player/Squeezebox2.pm:900-907`）。

- 输出处于关闭状态时，`strm s`（autostart 1）照样能把输出拉起来（`sq/slimproto.c:713-715`）。所以 `aude 0` 只是关闭当前输出，并不阻止下次开播。

---

## 9. DSD

### 结论（只记事实）

- **输入**：格式码 `d`，能力串里的标记是 `dsf,dff`（构建时带 `-DDSD` 才有）。流里必须带完整的 DSF（`DSD ` 开头）或 DSDIFF（`FRM8` 开头）容器头。播放器自己解析容器，**不看** `strm` 的 PCM 字段（`sq/dsd.c:99-205,857-871`）。
  - LMS 把 sample_size 填 0 表示 dsf、1 表示 dff，其余填 `'?'`（`lms/Slim/Player/Squeezebox.pm:733-740`）。
  - DSF 只支持原始 DSD 格式（format 0）（`sq/dsd.c:160-163`）。
  - 声道只处理单声道和立体声（`sq/dsd.c:306-470`）。
- **解码开始时，容器头必须已经全部在流缓冲里。** 头部不完整时，`_read_header` 返回 0，`dsd_decode` 会把它当错误处理（`r < 1`），于是发 STMn（`sq/dsd.c:205,754-759`）。threshold 足够大时，这一点自然满足。
- **输出**，由启动参数 `-D [delay][:format]` 决定（`sq/main.c:571-588`；默认是 PCM，`:333`）：

| 输出方式 | `-D` 写法 | 输出采样率（相对 DSD 位率） | 来源 |
|---|---|---|---|
| 转成 PCM（没用 `-D`） | — | 位率 / 8，例如 DSD64 → 352.8 kHz，再按设备能力处理 | `sq/dsd.c:816-819` |
| DoP | `-D` 或 `:dop`，另有 `dop24`、`dop24_3` | 位率 / 16 | `sq/dsd.c:794-805` |
| 原生 U8 | `:u8` | 位率 / 8 | `sq/dsd.c:790-793` |
| 原生 U16 LE/BE | `:u16le`、`:u16be` | 位率 / 16 | `sq/dsd.c:782-789` |
| 原生 U32 LE/BE | `:u32le`、`:u32be` | 位率 / 32 | `sq/dsd.c:774-781` |

- 原生 DSD 和 DoP 写法只在 ALSA 构建里可用；非 ALSA 构建只支持 DoP（`sq/main.c:127-133`）。
- 换算后的采样率超过设备最高采样率时，自动改成转 PCM 输出（`sq/dsd.c:811-814`）。
- **另一条路**：服务器把 DSD 封成 DoP，放进 24 位 FLAC 或 24 位 PCM 发过去。播放器检测到连续 32 帧带 0x05/0xFA 标记时，按 DoP 输出（`sq/dop.c:36-60`、`sq/flac.c:186-193`、`sq/pcm.c:218-229`）。这条检测在 DSD 构建里始终开启，与 `-D` 无关；输出格式选 DoP，或者是 `-D` 指定的 DoP 变体。
- DSD 或 DoP 输出时，不做音量调节，不做渐变（`sq/output.c:172-176`、`sq/dsd.c:822`）。
- 能力串不说明播放器用的是 DoP 还是原生输出，服务器无从得知。

---

## 10. 其他服务器→播放器命令

Squeezelite 只处理 7 个命令：`strm cont codc aude audg setd serv`（`sq/slimproto.c:517-526`）。其他命令只记一条 “unhandled” 警告，不断开。

| 命令 | 无屏 Squeezelite | 布局与说明 | 来源 |
|---|---|---|---|
| `strm` | 必需 | 见第 4 节 | — |
| `audg` | 必需 | 见第 8 节 | — |
| `aude` | 推荐 | 见第 8 节 | — |
| `setd` | 推荐（只用 id 0） | 载荷 `id(1) + 数据`。id 0 不带数据：查询名称，有名称才回 SETD；id 0 带数据：设置名称（最多 64 字节），回 SETD 确认，用了 `-N` 时还会写进文件。其他 id 忽略，不回应 | `sq/slimproto.c:450-478`、`lms/Slim/Player/Squeezebox2.pm:910-1037` |
| `cont` | 仅 autostart ≥ 2 时需要 | 载荷 `metaint(u32) + loop(u8) + guid 数(u16) + guid…`；播放器只用 metaint，收到后开始缓冲 | `sq/slimproto.c:399-415`、`lms/Slim/Player/Squeezebox2.pm:714-725` |
| `codc` | 仅格式码为 `?` 时需要 | 载荷 `format, sample_size, sample_rate, channels, endianness`（各 1 字节） | `sq/slimproto.h:171-179`、`sq/slimproto.c:417-422` |
| `serv` | 可选 | 载荷 `IPv4(u32)`，可再跟 10 位 ASCII 编组号。播放器断开后连新 IP，端口不变；IP 为 0.0.0.1 表示 mysqueezebox.com；新服务器连不上就退回原服务器 | `sq/slimproto.c:483-510,909-930`、`lms/Slim/Control/Commands.pm:300-335` |
| `vers` | 可忽略 | 播放器不处理 | — |
| `grfe` `grfb` `grfd` `grfs` `grfg` `visu` `bdac` `i2cc` `ledc` `knoa` `rhap` `updn` `ureq` `dsco` 等 | 可忽略 | 显示、固件升级、硬件相关，Squeezelite 都不处理 | — |

---

## 11. 播放器→服务器的其他报文

| 命令 | Squeezelite 是否发 | 服务器要不要处理 | 布局与说明 | 来源 |
|---|---|---|---|---|
| `HELO` | 发 | 必须 | 见第 2 节 | — |
| `STAT` | 发 | 必须 | 见第 6 节 | — |
| `DSCO` | 发 | 必须 | 1 字节原因码，表示取流连接结束；0 是正常结束 | `sq/slimproto.h:68-72` |
| `RESP` | 发 | 建议接收：可以忽略，也可以用来记录取流已建立 | 原始 HTTP 响应头；LMS 收到后把“连接中”标志清掉 | `sq/slimproto.c:219-230`、`lms/Slim/Networking/Slimproto.pm:573-588` |
| `META` | 发（仅在有 ICY 或 Ogg 元数据时） | 可以忽略 | ICY 元数据文本，或 `"Ogg"` + N 组（u16 长度 + 注释） | `sq/slimproto.c:232-243`、`sq/stream.c:401-424` |
| `SETD` | 发 | 建议处理 id 0（播放器名称） | `id(1)` + 以 NUL 结尾的名称 | `sq/slimproto.c:245-258` |
| `IR  ` | 仅 IR 构建并用了 `-i`（lirc）时发 | 可选 | `jiffies(u32) format(u8) bits(u8) code(u32)`；LMS 当作遥控按键 | `sq/slimproto.h:96-105`、`sq/slimproto.c:260-275` |
| `BYE!` `BUTN` `KNOB` `ANIC` `BODY` `UREQ` `ALSS` `RAWI` `DBUG` | 不发 | 不需要 | 硬件 Squeezebox 用 | `lms/Slim/Networking/Slimproto.pm:52-72` |

- **Squeezelite 没有“设备端暂停或继续”的报文。** 它本身没有按键，暂停只能由服务器发起。用 lirc 遥控时，会发 `IR  ` 红外码（服务器按 Squeezebox 遥控码表解释）。除此之外，播放器侧暂停、继续的来源在源码中没有找到。

---

## 12. 心跳与断线

### 结论

| 项 | 值 | 来源 |
|---|---|---|
| LMS 发 `strm t` 的间隔 | 5 秒（`check_all_clients` 定时器，对每个客户端发 requestStatus） | `lms/Slim/Networking/Slimproto.pm:40,199-241`、`lms/Slim/Player/Squeezebox2.pm:383-385` |
| LMS 判掉线 | 15 秒（3 个检查周期）没收到 STAT 就关连接；检测到时钟跳变的那一轮不判 | `lms/Slim/Networking/Slimproto.pm:217-232` |
| LMS 等 HELO | accept 后 5 秒内没收到 HELO 就关连接 | `lms/Slim/Networking/Slimproto.pm:193-196` |
| LMS 忘掉客户端 | 断开 300 秒后忘掉 | `lms/Slim/Networking/Slimproto.pm:38,288-295` |
| 播放器主动发 STMt | 出声期间约每 1 秒一次 | `sq/slimproto.c:734-737` |
| 播放器判服务器掉线 | `wait_readwake` 每次等 1 秒，连续 35 次超时（约 35 秒）就断开。注释写的是“服务器每 5 秒来一条消息，mysb.com 是 30 秒，所以 35 秒超时”。只要有事件就把计数清零，内部线程的唤醒事件也算 | `sq/slimproto.c:559-614` |
| 播放器重连 | 关闭套接字，等 100 ms 后重连；连接超时 5 秒，失败后睡 5 秒再试；没用 `-s` 时，连续失败超过 5 次就重新发现 | `sq/slimproto.c:907-978` |
| 重连时的 HELO | `wlan_channellist` 带 `0x4000`，并带上当前流的 `bytes_received` | `sq/slimproto.c:143-145,968-970` |

- **SlimProto 控制连接断开不影响音频。** 取流在独立的 HTTP 连接和线程里，解码和输出也是独立线程。重连期间，已经缓冲的音频照常播放，HTTP 流也会继续下载（`sq/slimproto.c:966-977` 只关控制套接字）。
- LMS 收到重连时的处理：
  - 重连位为 1：只更新套接字，状态照旧。
  - 重连位为 0 但客户端已知（“伪重连”）：如果它是唯一的活动播放器，就按 `bytes_received` 恢复（`playerReconnect`）；服务器认为它处于停止状态时发 `strm q`。
  - 来源：`lms/Slim/Player/Squeezebox.pm:79-117`。
- 同一个 MAC 发来新的 HELO 时，LMS 关闭旧连接、换用新连接（`lms/Slim/Networking/Slimproto.pm:1184-1196`）。
- 播放器进程被杀掉时，TCP 连接通常由内核关闭，服务器读到 EOF 就能马上发现。只有网络断开或断电时，才要靠心跳超时来判断。

---

## 13. 常见部署的版本差异与兼容（只记源码能看到的）

| 事实 | 来源 |
|---|---|
| `Firmware` 是 `VERSION`，编译时可以用 `CUSTOM_VERSION` 加后缀。LMS 在 Firmware 里看到 `-pCP` 时，给 ModelName 加上 `-pCP`，可以据此识别 piCorePlayer 构建 | `sq/squeezelite.h:27-34`、`lms/Slim/Player/SqueezePlay.pm:107-118` |
| `ModelName` 可以用 `-M` 任意设置；`Model` 固定为 `squeezelite` | `sq/main.c:103,475-477`、`sq/slimproto.c:123,883` |
| 编解码列表随构建选项和 `-c`/`-e` 参数变化：DSD、ALAC、FFMPEG（wma/alc）、OPUS、SSL（CanHTTPS）、faad、mad/mpg123 都可能缺；`-W` 时 PCM 的标记变成 `wav,aif,pcm` | `sq/decode.c:155-200`、`sq/Makefile:7-82`、`sq/pcm.c:455-488` |
| `MaxSampleRate` 随设备探测、`-r`、`-Z`、是否带 RESAMPLE 而变 | `sq/slimproto.c:883-888` |
| PCM 采样率码 `'B'`（1411200）、`'C'`（1536000）只在这一版的表里出现；LMS 的表只到 `'A'`（768000） | `sq/pcm.c:65-67`、`lms/Slim/Player/SqueezePlay.pm:240-261` |
| deviceid 固定为 12，revision 固定为 0 | `sq/slimproto.c:141-142` |
| STAT 发 53 字节；LMS 还兼容 57 字节（多 4 字节垃圾）和更短的旧格式 | `lms/Slim/Networking/Slimproto.pm:772-777` |
| `strm s` 的格式码 `?` 加 `codc`、以及 autostart 2/3 加 `cont`，是 Squeezelite 对协议的扩展 | `sq/slimproto.c:364-366`、`sq/slimproto.h:171` |
| 本机 LocalPlayer 扩展：`server_ip = 127.0.0.1` 且 `server_port = 3483` 时，请求串被当成本地文件路径。服务器不应该用这个组合 | `sq/slimproto.c:36-42,371-374` |

- **没能从源码看出的**：moOde 用哪个版本、带哪些构建选项；旧版（如 1.8/1.9）的能力串和命令有哪些差异。这次的克隆是单提交，没有历史，无法比较旧版本。piCorePlayer 和 moOde 也没有在源码里出现。上面列出的“随构建变化”的项，就是服务器必须按能力串动态处理的部分。

---

## 14. 端口占用

### 结论

- **TCP**：播放器可以用 `-s host:port` 连到 3483 以外的端口（`sq/utils.c:347-352`、`sq/slimproto.c:844-854`）。`serv` 切换服务器时端口保持不变（`sq/slimproto.c:909-912`）。
- **发现应答不能告知另一个端口**：
  - 播放器只取应答的源 IP，端口用 3483 或 `-s` 指定的值（`sq/slimproto.c:807-822,852-854`）。
  - 应答内容不被解析（只读 10 字节，丢弃）。
  - LMS 的 TLV 里也没有 SlimProto 端口字段（`JSON` 是 HTTP 端口）。
- 同一主机上已经有 LMS 时：
  - TCP 3483 被 LMS 占着，ROOMusic 的 SlimProto 只能监听别的端口，播放器必须手动配置 `-s <主机>:<端口>`。
  - 即使 ROOMusic 也绑上 UDP 3483 并且回应，播放器收到应答后连的仍是该 IP 的 TCP 3483，也就是 LMS。
  - LMS 绑 UDP 3483 时开了 `ReuseAddr`（`lms/Slim/Networking/UDP.pm:34-40`）。在 Linux 上，两个都开了 SO_REUSEADDR 的 UDP 套接字可以绑同一端口，广播包两边都能收到，这一点属于系统行为，源码里没有。但这对 Squeezelite 没有意义，因为它只取第一个应答的源 IP，端口固定。
- 不同主机上有两个服务器（都在 3483）时，播放器连的是**最先应答**的那个（`sq/slimproto.c:807-812`，只读一次）。
- 取流端口与 3483 无关：`strm s` 的 `server_port`、`server_ip` 可以是 ROOMusic 的任意 HTTP 端口（`sq/slimproto.h:125-126`）。

---

## 实现要点清单（服务端最小必需行为，按开发顺序）

1. **帧编解码**
   - 收：4 字节命令字 + u32 长度 + 载荷，处理半包。
   - 发：u16 长度（= 4 + 载荷长度）+ 4 字节命令字 + 载荷。单帧不超过 4096 字节。
2. **UDP 发现**
   - 监听 UDP 3483。收到首字节为 `e` 的包，就从 SlimProto TCP 所在的 IP 回一个 `E`；首字节为 `d` 时回 `D` + 17 字节名称。
   - 只应答允许的来源地址（PRD S9）。
3. **TCP 监听与 HELO**
   - accept 后 5 秒内要收到 HELO，否则关闭连接。
   - 解析 deviceid、MAC（设备键）、重连位（`0x4000`）、bytes_received、能力串：编解码标记，加上 `Model`、`ModelName`、`MaxSampleRate`、`Firmware`、`AccuratePlayPoints`、`CanHTTPS`、`Balance`、`SyncgroupID`。
   - 同一个 MAC 再次 HELO 时，替换旧连接。
4. **HELO 之后的初始化**
   - 发 `aude 1,1`、`audg`（adjust=1，增益按设备音量；固定音量时 adjust=0）、`setd` id 0（查询名称）。
   - 需要确定的停止状态时发 `strm q`。
5. **心跳**
   - 每约 5 秒发 `strm t`（replay_gain 填 0）。这是必需的：播放器约 35 秒收不到消息就断开。
   - 每收到一个 STAT 刷新心跳时间。约 15 秒没有 STAT，或读到 EOF、出错，就判离线。
6. **STAT 解析与时钟**
   - 解析 53 字节载荷。
   - 记录 jiffies 与服务器时刻的偏移（取最小值，作为 epoch）。
   - 位置 = 本条流的起始偏移 + elapsed_ms/1000；正在播时，加上“现在 − jiffies 对应的服务器时刻”外推。
7. **取流 HTTP 端点**
   - 接受播放器按 `strm s` 请求串发来的 GET，用令牌或 MAC 识别设备。
   - 回 `HTTP/1.0 200 OK` 或带 `Connection: close` 的响应头，以 `\r\n\r\n` 结尾。不用 chunked，不要求 Content-Length。
   - 之后直接写音频字节，流结束时关闭连接。
   - 播放器暂停时，写操作会被 TCP 反压阻塞，服务器要能容忍长时间阻塞。
8. **开流 `strm s`**
   - command `s`，autostart `'1'`（单台），threshold 按码率设（LMS 默认 255 KB）。
   - FLAC：格式码 `f`，PCM 字段全填 `'?'`，高采样率时 output_threshold 设 20。
   - PCM：格式码 `p`，按码表填 size/rate/channels/endianness，发**裸 PCM，不带 WAV 头**。
   - transition_type `'0'`，flags 0，replay_gain 0（或 16.16 定点），server_port 填 HTTP 端口，server_ip 填 0（即 SlimProto 服务器 IP）或明确的 IPv4，请求串附在后面（≤ 4068 字节）。
   - 只用能力串里声明过的格式。未声明的格式播放器找不到解码器，也不会回 STMn。
   - 采样率不超过 MaxSampleRate。
9. **状态机对接**
   - STMc：开流已受理。
   - RESP 或 DSCO：取流连接已建立或已结束（DSCO 原因非 0 时按出错处理）。
   - STMs：本条流开始出声，此时切换“正在播放”，elapsed 归零。
   - STMd：本条流解码完。有接续流时，**这时**才发下一条 `strm s`。
   - STMu：全部播完，转为停止。
   - STMo：中途欠载（可以先暂停，攒够缓冲后再 `u`，或者只记录）。
   - STMn：解码失败。
10. **控制命令**
    - 暂停：`strm p`（0）→ STMp。
    - 继续：`strm u`（0）→ STMr。
    - 停止：`strm q` → STMf。
    - 跳转：`strm q` + 从新位置开流的 `strm s`，位置加上 seek 偏移。
    - 下一首、上一首：同跳转，先停再开流。
    - 取消已经预取的接续流：`strm f`。
    - 音量：`audg`，16.16 定点；静音发增益 0。
11. **长流与接续流**
    - 同格式的曲目由服务器拼进一条 HTTP 长流（播放器只看到一条流，只有一次 STMs）。
    - 格式或采样率变化时，服务器先结束当前 HTTP 响应，等 STMd 后再发新的 `strm s`。播放器在交界处切换采样率，连接不断。
    - 同一条 FLAC 流中途不能换采样率，换就必须开新流。
12. **断线与重连**
    - 控制连接断开时，设备离线，释放输出（PRD R6）。
    - 播放器重连发 HELO 时，看重连位和 bytes_received：重连位为 1 说明音频可能还在播，状态照旧。
13. **端口占用**
    - TCP 3483 被占用时，按 PRD S10 标记 `port_in_use`。
    - 如果改用备用端口，只能让用户在播放器上配置 `-s 主机:端口`，发现机制帮不上忙。

## Caveats / Not Found

- 旧版本 Squeezelite 与这一版的差异无法确认（浅克隆没有历史）。piCorePlayer 和 moOde 的构建选项也不在源码里。
- Squeezelite 没有播放器侧暂停或继续的上行报文，只有 lirc 构建会发 `IR  ` 红外码。PRD S11 的“设备端暂停或继续”，对纯 Squeezelite 来说多半没有事件来源，或者要靠解析 IR 码。
- A1 用“无声卡输出”测试时要注意两点：
  - `-o -` 把音频写到 stdout，没有实时节奏，管道读多快它就播多快；默认上限只有 44100，需要用 `-r` 声明采样率（`sq/output_stdout.c:103-121,152-155`）。
  - 用 ALSA 的 null 设备时，是否按实时节奏消耗数据，源码里看不出来，未核实。
- 同一条 FLAC 长流中途不能换采样率，这是根据 `sq/flac.c:174-205` 只在 new_stream 时设定输出采样率推断的，没有实测。
- DSD 容器头不完整时会发 STMn，这是根据 `sq/dsd.c:205,754-759` 的返回值推断的，没有实测。
- Linux 上两个进程同绑 UDP 3483 时广播包怎么分发，属于系统行为，源码里没有，本文没有核实。
