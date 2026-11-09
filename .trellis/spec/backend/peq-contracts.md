# 参数均衡后端合同

## 1. 范围与边界

`zones` 拥有播放区 PEQ 不可变文档；`dspsettings` 拥有成员预设、离线 OPRA 目录、导入与频响；`playback/render` 拥有文档到 IIR 的转换。`zones` 不得导入 `dspsettings`，`dsp/chain` 不导入具体 PEQ 算法。

## 2. 签名与存储

- `zones.NewPEQDocument(PEQSettings)`、`ParsePEQDocument([]byte)`、`DefaultPEQDocument()` 返回 `SettingsDocument`；读取用 `DecodeSettingsDocument[PEQSettings]`，每次独占解码。
- `PlaybackSettings.PEQ SettingsDocument`、`SettingsPatch.PEQ *SettingsDocument`；HTTP `set_settings` 的 `peq` 必须是完整对象。
- 0047 为 `playback_zones` 增加 `peq jsonb NOT NULL DEFAULT '{"enabled":false,"preamp_db":0,"bands":[]}'`，同时增加 `dsp_peq_presets(id,user_id,name,peq,created_at,updated_at)`；成员外键级联删除，`(user_id,name)` 唯一，名称长度 1–64。
- `dspsettings.NewService(db)` 与 `dspsettings/httpapi.New(guard, service).Register(mux)` 由 app 装配；新包在 `dependency_rules_test.go` 登记。

## 3. 请求与响应

均在 `/api/v1/me/dsp/`，使用当前成员鉴权和既有 Origin 守卫。

| 接口 | 合同 |
| --- | --- |
| GET `opra/search?q=` | `{items:[{product_id,vendor,name,subtype,eq_count}]}`；空查询空数组，最多 30 项 |
| GET `opra/products/{id}` | `{product:{product_id,vendor,name,subtype},eqs:[{eq_id,author,details,link,peq}]}` |
| POST `peq/import` | `{text}` → `{peq,unrecognized_lines:[int]}`，行号从 1 开始 |
| POST `peq/response` | `{peq,sample_rate?}` → `{curves:[{channels,points:[[hz,db]]}]}`；默认 48000，显式值为 8000–768000 整数 |
| GET `peq-presets` | `{items:[preset]}`，按名称排序 |
| POST `peq-presets` | `{name,peq}` → 201 preset |
| PATCH `peq-presets/{id}` | `{name?,peq?}` → 200 preset |
| DELETE `peq-presets/{id}` | 204，无响应体 |

preset 为 `{preset_id,name,peq,created_at,updated_at}`，不是 `id`。所有预设读写都限定当前成员，每人最多 200 个；配额检查与写入使用成员级事务锁。

PEQ 为 `{enabled,preamp_db,bands,source?}`。段包含 `type,freq_hz,channels,enabled`，类型参数：峰值/架类必须 `gain_db,q`；低通/高通必须 `slope_db`，仅斜率 12 可额外给 `q`；带通/陷波/全通必须 `q`。不接受类型无效的参数。`channels` 是 `"all"` 或非负整数数组（可空），排序去重。来源不可为 null，只能省略或为以下完整对象：

- OPRA：`{kind:"opra",eq_id,vendor,product,author,details,link}`，字符串可空但不能省略字段。
- 预设：`{kind:"preset",preset_id,name}`。
- 导入：`{kind:"import"}`。

曲线为 20 Hz–20 kHz 对数 256 点，前级计入每条。始终保留 `channels:"all"` 曲线，仅含 all 段；显式声道叠加 all 段和自己的段，按等效启用段集合分组。整体关闭时不施加均衡。

## 4. 校验与错误矩阵

| 条件 | 结果 |
| --- | --- |
| 段数 >32；频率不在 10–40000；增益/前级不在 −30…30；Q 不在 0.1–30；斜率不在 6/12/18/24/30/36；对象不完整或字段不适用 | 400 `invalid_input`，不带字段路径 |
| 导入无可用 Filter | 400 `peq_import_empty` |
| 同成员同名预设 | 409 `peq_preset_name_taken` |
| 超过 200 个预设 | 409 `peq_preset_limit` |
| 预设不存在/属于他人；OPRA 产品不存在 | 404 `not_found` |
| 播放区持久化 PEQ 损坏 | 只将 PEQ 回退默认，记录一次 `zones.settings.invalid` WARN，不把原文写日志，不自动写库 |

预设名称与 source 字符串不得含 PostgreSQL text/jsonb 无法保存的 NUL 字符，入口返回 `invalid_input`，不把它留到写库后变成 500。

错误 envelope 沿用 `{error:{code,message},request_id}`。OPRA 运行期不联网，首次请求惰性解析内嵌数据并常驻；映射按设计钳位、最多保留前 32 段，保留署名。

## 5. 正常、基线和错误场景

- 基线：默认关闭，不插 PEQ 节点，不改变 Exact。
- 正常：以 SRC 与矩阵降混后的真实格式设计系数；在 Tail 声道选择之前把单个 IIR 放入 `Spec.User`。
- 恒等：`enabled=true` 即使无匹配段且前级为零，仍以 `chain.Wrap(...,true)` 算处理。
- 信号：`kind:peq`、`quality:enhanced`、`compat:false`，params 只有 `{label,bands}`；bands 按启用且至少匹配一个流声道的文档段计数，不按 IIR 节数或声道数计数。label 为“产品 · 作者”、预设名或“参数均衡”。
- 错误：引用他人预设 ID 的 CRUD 返回 404；播放区 source 只是复制的快照，不为它增加运行期预设查询。

## 6. 验证接点

现有最小集：`dsp/peq`、`dspsettings/...`、`zones/...`、`playback/render` 单元测试，app 的 `TestPackageDependencyDirections`，受影响包 vet 与 `go build ./...`。迁移只定向运行 0047 的升级→回退→再升级；recorder 只采集 DSP 与 playback-zones 所属样本。独立测试任务补接口隔离、等值不重建、解码切片独占与真实格式上的 PEQ 组合。全套 e2e 仅发版前执行，不在本项合入前重复运行。

## 7. 错误写法与正确写法

- 错：在可比较共享设置中保存 `[]PEQBand`；对：保存规范 JSON 的不可变 `SettingsDocument`，调用时独占解码。
- 错：以最终立体声出口设计 PEQ 后再放到六声道链；对：以③后、Tail 前格式设计和展开声道。
- 错：只因段数为零就认为无处理；对：只看总开关是否启用。
- 错：手改整个金样目录；对：由 recorder 的 `dsp` / `playback-zones` 所有权边界生成。
