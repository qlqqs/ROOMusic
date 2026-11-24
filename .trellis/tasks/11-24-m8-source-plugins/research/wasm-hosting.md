# 插件宿主方案调研（2026-11-24）

## 候选
| 方案 | 隔离 | 状态 | 许可 |
|---|---|---|---|
| Extism go-sdk v1.7.1 + wazero v1.12.0（进程内 Wasm） | 沙箱：网络、文件、环境默认都没有，只能经宿主函数 | go-sdk 2025-05 后停更（#102 http_request 失败会 panic 并逐步坏掉实例；不能替换 HTTP 客户端）；Extism 核心与 PDK 仍在更新；Navidrome v0.60 起生产使用此组合 | BSD-3 / Apache-2.0 |
| 直接用 wazero + 自定 ABI | 同上 | wazero 活跃（最低 Go 1.25） | Apache-2.0 |
| hashicorp/go-plugin（子进程 gRPC） | 子进程与宿主同 OS 权限，能自由联网读文件 | 活跃 | MPL-2.0 |
| knqyf263/go-plugin（wazero + protobuf） | 沙箱 | 只能用 Go 写插件 | MIT |

## 要点
- Extism 内置 `http_request` 无法注销：不设 AllowedHosts 时 Go 实现全部拒绝（文档对 null 的说法与实现不一致，要用回归测试锁住）；它不防 SSRF、重定向不复核。
- wazero 没有 fuel，CPU 只能靠 `WithCloseOnContextDone` + 超时；内存默认每实例 4 GB，须设 `MaxPages` 并限并发。
- 编译器模式需要可执行匿名 mmap；v1.12 起被 seccomp 拒绝时退回解释器。生产容器（read_only、cap_drop ALL）下要实测。编译缓存可用内存或数据卷目录。
- PDK：Rust、Go（TinyGo/wasip1）正式；JS（QuickJS）可用；Python、C# 实验性。
- Navidrome 的做法：`.ndp` = zip(manifest.json + plugin.wasm)；能力按导出函数识别；只给 `host.HTTPSend`（10 秒、5 次重定向逐跳复核、10 MB、拨号时拦私有地址）；每次调用新建实例、30 秒超时；无签名，库里记 SHA-256 只用于检测变化，变化后先禁用。
- Music Assistant 社区目录：索引带内容哈希、可远程封禁、默认关闭。
- 签名可选：minisign（Ed25519，go-minisign MIT）。

## 推荐
Extism go-sdk + wazero，全部 extism 类型封在适配层里（以后可换 fork 或直接 wazero）；关掉 AllowedHosts/AllowedPaths；WASI 只给时钟与随机数；
限内存、超时、每插件并发；编译一次、每次调用新实例；JSON 调用约定与退出码；唯一出网通道 `upstream_fetch`（插件按 manifest 声明的上游 ID 传 path 与 query，
宿主拼地址并复用 sources.Requester 的限速、缓存、mock 替换、出站策略）；图片只返回 URL，宿主下载与校验；包为 zip，安装时记 SHA-256，哈希变了就禁用并重新审批。
来源网址见调研代理原始汇报（extism/go-sdk、wazero、navidrome plugins、music-assistant community catalog 等）。
