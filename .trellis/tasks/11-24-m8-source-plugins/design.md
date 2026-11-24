# 设计：第三方数据源插件

## 组成

```
backend/internal/plugins/                 （不依赖 curation、catalog、数据库之外的业务包）
  runtime/        Extism 适配层：编译、实例、宿主函数注册、内存与超时、并发信号量、编译缓存
  manifest.go     清单解析与校验
  package.go      zip 包解析（只认 manifest.json 与 plugin.wasm，大小上限 32 MiB）、SHA-256
  store.go        安装目录 <数据目录>/plugins/<id>/ 与数据库表 source_plugins
  source.go       把一个插件包装成 sources 的声明与能力接口（CoverFinder、LyricsFinder、ArtistImageFinder、ArtistBioFinder）
  fetch.go        upstream_fetch：按清单的上游拼地址，复用 sources.Requester（限速、缓存、UA、PublicOnly、mock 替换、密钥注入）
  httpapi/        管理接口：上传预检、确认安装、列表、启用与停用、删除
backend/plugins/examples/lyrics-demo/     示例插件（Go，GOOS=wasip1 + extism go-pdk），README 与清单
docs/plugins.md                            插件开发说明
docs/architecture/adr/0007-source-plugins.md
```

## 清单

```json
{
  "id": "lyrics-demo",                 // [a-z0-9-]{3,40}，安装后不变
  "name": "示例歌词源",
  "version": "1.0.0",
  "author": "ROOMusic",
  "abi": 1,
  "upstreams": [
    { "id": "main", "base_url": "https://lyrics.example.org/api", "min_interval_ms": 1000 }
  ],
  "keys": [
    { "name": "api_key", "optional": false, "inject": { "in": "header", "name": "X-Api-Key" }, "upstream": "main" }
  ]
}
```

能力不写在清单里，按插件导出的函数识别：`cover_art`、`lyrics`、`artist_image`、`artist_bio`。另有必需的 `describe`（返回能力列表，与导出核对一致）。

## 调用约定（ABI 1）

- 输入输出都是 JSON；退出码 0 成功、1 没找到、2 稍后再试（对应 `ErrTryLater`）、其他为失败。错误信息用 Extism 的 SetError。
- 函数与入参：
  - `cover_art({kind, mbid, title, artist})` → `{url, mime?}`
  - `lyrics({title, artist, album, duration_seconds})` → `{kind: "synced"|"plain", text, source_url, external_id}`
  - `artist_image({mbid, name, wikidata?})` → `{url, source_url, author?, license?, license_url?, external_id}`
  - `artist_bio({mbid, name, lang})` → `{text, lang, source_url, license?, license_url?, external_id}`
- 宿主校验：字段长度、URL 必须 http(s)、简介去 HTML 截断 4000 字、歌词行数上限；不合格按失败处理并记日志。

## 宿主函数

| 名称 | 说明 |
| --- | --- |
| `upstream_fetch(json) -> json` | 入参 `{upstream, path, query, accept}`；只允许 GET；宿主拼 `base_url + path`，注入密钥，走 Requester；返回 `{status, headers(白名单), body_base64}`，响应体上限 4 MiB；错误以 JSON 返回，不 panic |
| `log(level, msg)` | 每次调用最多 50 条、每条 1 KiB，转 slog（带插件标识） |

Extism 的 manifest 不设 `AllowedHosts`、`AllowedPaths`；WASI 只给时钟与随机数；回归测试锁住“内置 HTTP 全部拒绝”。

## 与自动整理的连接

- 启用的插件注册为外部源：声明 `ID = "plugin:" + id`、`Name = 清单 name`、能力来自 `describe`、`Keys` 来自清单、`MinInterval` 取上游最小值、`DefaultEnabled = false`。
  设置页与密钥存储沿用现有机制（`curation_source_settings`、`curation_source_keys`）。
- 各能力的取用顺序：内置来源在前，插件按安装时间在后。程序层的封面、歌词、艺人图片与简介流程在内置来源都没有结果时再问插件。
- 来源记为 `plugin:<id>`，按来源撤销、抑制标记、发给外部的数据说明（显示清单里的上游地址）都按源处理。

## 安装与完整性

- `POST /api/v1/admin/plugins/preview`（上传 zip）→ 预检：解析清单、编译模块、调用 `describe`，返回清单内容、能力、上游与密钥、哈希；包暂存在数据目录的临时区。
- `POST /api/v1/admin/plugins`（带预检令牌）→ 确认安装：移到 `plugins/<id>/`，写表；同 id 已存在时是升级，旧版本停用直至确认。
- 启动与每次调用前核对文件哈希；不一致 → 停用并记原因 `hash_changed`，设置页提示重新确认。
- `DELETE /api/v1/admin/plugins/{id}` → 停用并删除文件；已写入的内容不自动撤销，界面引导用按来源撤销。

## 生产环境

- 编译缓存放 `<数据目录>/plugins/.cache`（只读根文件系统下数据目录可写）；编译器模式不可用时 wazero 退回解释器，启动日志写明模式。
- 生产 Compose 验证：用示例插件在测试项目里跑一次 A6。

## 前端

- 设置 › 自动整理 新增“插件”卡片：上传按钮 → 确认页（名称、作者、版本、能力、要访问的地址、需要的密钥、哈希前 12 位）→ 安装；列表（状态、停用原因、删除）。
- 启用后在“外部源”卡片里像内置来源一样显示（开关、密钥行、来自它的改动），名称后标“插件”。

## 回退

- 迁移只新增表；关掉或删除插件即停止。适配层之外不引用 Extism 类型，便于以后替换运行时。
