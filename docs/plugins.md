# 数据源插件开发

插件提供自动整理所需的封面、歌词、艺人图片或简介。宿主按安装顺序询问启用的插件，先尝试内置来源。每次调用都会创建实例，插件可通过宿主访问管理员确认的上游。一个可构建的实现见[示例歌词源](../backend/plugins/examples/lyrics-demo/README.md)。

## 清单与打包

zip 根目录放两个普通文件 `manifest.json` 和 `plugin.wasm`。压缩包、解压内容总量各限 32 MiB，清单限 64 KiB。以下清单声明一个通过请求头接收密钥的上游。

```json
{
  "id": "lyrics-store",
  "name": "歌词资料库",
  "author": "资料库维护者",
  "version": "1.0.0",
  "abi": 1,
  "upstreams": [
    {"id":"main","base_url":"https://lyrics.example.org/api","min_interval_ms":1000}
  ],
  "keys": [
    {"name":"api_key","optional":false,"upstream":"main","inject":{"in":"header","name":"X-Api-Key"}}
  ]
}
```

插件标识匹配 `[a-z0-9-]{3,40}`，上游标识匹配 `[a-z0-9-]{1,40}`，密钥名称匹配 `[a-z_]{1,32}`。每个标识在对应列表内唯一。`base_url` 是 HTTP(S) 基础地址，可带目录前缀。请求间隔以毫秒计，允许 0 至 86400000。`inject.in` 支持 `header` 和 `query`。密钥值由管理员在源设置中填写。

用仓库固定的 Go 工具链构建 WASI reactor，导出函数使用 `//go:wasmexport`。在示例目录内构建时，使用 `GOOS=wasip1 GOARCH=wasm mise exec -- go build -buildmode=c-shared -o plugin.wasm .`。打包命令为 `python3 -m zipfile -c plugin.zip manifest.json plugin.wasm`。

## ABI 1

每个导出函数形式为 `() -> i32`。用 Extism PDK 读入 UTF-8 JSON、写出 UTF-8 JSON 并返回退出码。0 表示成功，1 表示没有匹配，2 表示稍后再试。其他退出码及 SetError 表示调用失败。成功返回的 JSON 最多 256 KiB。

| 导出函数 | 输入字段 | 成功输出字段 |
| --- | --- | --- |
| `describe` | `{}` | `{"capabilities":["lyrics","artist_bio"]}`，与实际能力导出一致 |
| `cover_art` | `kind` 为 `release` 或 `release_group`，以及 `mbid,title,artist` | `url`、可选 `mime` |
| `lyrics` | `title,artist,album,duration_seconds`，时长为整数 | `kind,text,source_url,external_id`，种类为 `synced` 或 `plain` |
| `artist_image` | `mbid,name,wikidata`，未知条目号为空 | `url,source_url,external_id`，可选 `author,license,license_url` |
| `artist_bio` | `mbid,name,lang`，第一版依次尝试 `zh` 与 `en` | `text,lang,source_url,external_id`，可选 `license,license_url` |

`artist_image` 和 `artist_bio` 也会按名字调用。此时 `mbid` 是空字符串，`name` 为本地艺人名，图片查询的 `wikidata` 同样为空。插件可用名字查询自身目录，无法确认时返回 1。

提供至少一种能力。`external_id` 是用于撤销和抑制的稳定条目标识，最长 128 字节。URL 使用 HTTP(S)，最长 2048 字节。图片下载地址须属于已声明的上游基础路径，宿主下载并校验图片。简介经宿主去 HTML、取首段并限制为 4000 字。歌词文本最多 64 KiB、2000 行，同步歌词使用 LRC 时间戳。

## 宿主函数

两个函数均从 `extism:host/user` 导入。参数指向通过 PDK 分配的 Extism 内存，调用方释放自己分配的输入以及收到的返回块。

- `upstream_fetch(i64) -> i64` 接收一个 JSON 对象，例如 `{"upstream":"main","method":"GET","path":"songs","query":{"title":"潮汐信箱"},"accept":"application/json"}`。只支持 GET。相对路径追加到上游基础目录，查询参数使用字符串映射。可省略方法、查询和 Accept。预检期间仅能查询自身能力。
- 成功出站返回 `{"status":200,"headers":{"content-type":"application/json"},"body_base64":"e30="}`。原始响应体上限为 4 MiB。请求器按上游排队、重试和缓存，并逐次注入密钥。仅回传 Content-Type。宿主失败返回含 `error` 的 JSON，例如 `{"error":"unknown_upstream"}`。插件先检查错误和状态，再解码响应体。
- `log(i64,i64)` 的参数为日志级别和 UTF-8 消息的内存偏移，例如 `info` 与 `已找到歌词`。单次最多 50 条，消息限 1024 字节，日志带插件标识。

## 管理接口

以下路径要求管理员会话，写请求遵循同源校验。接口使用示例见[响应样本](../backend/internal/app/testdata/api-samples/plugins)。

| 方法与路径 | 请求 | 响应 |
| --- | --- | --- |
| `POST /api/v1/admin/plugins/preview` | 原始 zip 或 multipart 的 `file` 字段 | 200，含 `manifest,capabilities,sha256,preview_token` |
| `POST /api/v1/admin/plugins` | `{"preview_token":"预检返回值"}` | 201，已安装的插件，默认 `enabled:false` |
| `GET /api/v1/admin/plugins` | 无 | 200，`{"items":[…]}`，按安装时间排序 |
| `PUT /api/v1/admin/curation/sources/plugin:<id>` | `{"enabled":true}` | 200，源状态、能力、密钥配置状态 |
| `PUT /api/v1/admin/curation/sources/plugin:<id>/keys/<name>` | JSON 对象，`key` 为管理员输入的密钥 | 200，源状态 |
| `DELETE /api/v1/admin/plugins/<id>` | 无 | 204 |

预检令牌 15 分钟有效，成功确认后消耗。相同标识的包经确认后替换旧版，开关恢复为关闭，并清除旧密钥。安装目录为数据目录下的 `plugins/<id>/`。启动及每次调用前校验清单和模块，变化后停用并返回 `disabled_reason:"hash_changed"`。重新上传和确认后可再次启用。删除插件后，管理员仍可按来源 `plugin:<id>` 撤销其整理记录。
