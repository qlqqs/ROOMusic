# 播放区数据与队列合同（B1）

## 1. 范围

`zones/queue` 拥有队列纯函数，`zones` 拥有三张播放区表与存储操作。B1 不包含 Service、命令、HTTP、输出端或渲染器。
设计见[播放区父任务](../../tasks/archive/2026-11/11-08-server-playback-zones/design.md)。运行时按单进程设计；后续 Service 在播放区锁内计算新状态，事务提交后才替换内存。

## 2. 数据与入口

迁移 `0043_playback_zones.sql`（及 `.down.sql`）新增 `playback_devices`、`playback_zones`、`playback_queue_entries`。
设备与播放区一对一，复合外键保证同属用户，删除设备级联删除区与条目；曲目引用不级联。
播放区包含电平化、交叉渐变、`gapless=true`、`quality=lossless`；迁移 `0044_playback_zone_headroom.sql` 增加 `headroom JSONB NOT NULL`，默认 `{enabled:false,db:-3,clip_indicator:false}`，同名 `.down.sql` 仅删除该列（回退会丢失 Headroom 设置）。
条目位置非负，`UNIQUE(zone_id,position) DEFERRABLE INITIALLY DEFERRED` 允许事务内腾位和交换。

`LoadUser(ctx, db, userID)` 在显式只读 REPEATABLE READ 事务中加载设备、播放区与条目，设备按 `created_at,id` 稳定排序；队列按位置排序。
`NewStore(tx *sql.Tx) *Store` 只绑定调用方事务。写入方法如下：

```go
func (s *Store) InsertEntries(ctx context.Context, userID, zoneID, afterEntryID string, entries []queue.Entry) error
func (s *Store) DeleteEntry(ctx context.Context, userID, zoneID, entryID string) (bool, error)
func (s *Store) SwapEntries(ctx context.Context, userID, zoneID, firstEntryID, secondEntryID string) (bool, error)
func (s *Store) ReplaceEntries(ctx context.Context, userID, zoneID string, entries []queue.Entry) error
func (s *Store) UpdateZone(ctx context.Context, userID string, zone PersistedZone) error
```

`LoadUser` 返回 `(UserState, error)`，`UserState` 含 `Devices []PersistedDevice`、`Zones []PersistedZone`；`PersistedZone.Queue` 为 `queue.State`。
空插入锚点表示队首，空替换表示清空；版本号由调用方计算，`UpdateZone` 不写 `Queue.Entries`。

`PlaybackSettings.Headroom` 与其他设置同事务写入，注册默认值、读取和 DTO／渲染端口使用同一份设置。`db` 必须有限、在 −30…0 内且为 0.1 的步进；存储读取遇到非对象、缺失/null 字段、类型不符、未知字段或非法 `db` 时整项回退默认值，不因坏设置阻断播放区加载，也不在只读加载中修复数据库。JSONB 列不额外添加字段形状或范围 CHECK，校验由 zones 拥有。

## 3. 行为约束

- 队列复用 `queue.State`、`queue.Entry`，只存条目 ID 与曲目 ID。显示元数据由后续读取侧经 catalog 摘要取得，不移植前端 `patchTrackMetadata`。
- 队列上限 10,000，历史上限 500；随机数与条目 ID 由调用方注入。保持 `queue.ts` 的四模式、增删移动、前进后退语义，函数不修改输入切片。
- `PeekNatural(state, env, isSkipped) string` 预选下一项；`AdvanceTo(state, entryID, isSkipped) Step` 消费预选，不再次抽签，历史与随机袋更新与 `Advance` 共用逻辑。失效预选返回空 `EntryID` 与原状态，调用方再预选；当前项或模式改变时调用方丢弃缓存。
- 所有存储读写带用户条件。插入批量腾位，删除不重排，交换由调用方传相邻 ID，替换整段删除后用 `unnest` 批量插入；条目变化与播放区行更新必须同事务。
- `ROOMUSIC_TEST_COMPOSE_PROJECT` 默认为 `roomusic-test`，只允许 `^roomusic-test(-[a-z0-9]+)*$`；集成脚本的资源检查、启动、清理与日志使用同一项目名，保留与播放 E2E 的共享互斥锁。

## 4. 校验与错误

| 条件 | 合同 |
| --- | --- |
| 加入后超过 10,000 | 整批拒绝，不截断、不生成部分条目 |
| 替换超过 10,000 | 返回原队列（沿用前端） |
| 条目已不存在 | 队列移除、移动、跳转无操作 |
| 预选项被移除 | `AdvanceTo` 不前进；调用方重新 `PeekNatural`，不将旧渲染事件静默替换为另一首 |
| 存储删除／交换无本人目标 | 返回 `false`，不触及其他用户数据 |
| 存储插入／替换／更新无本人目标 | 返回 `ErrNotFound` |
| SQL、扫描、事务提交失败 | 返回错误并保留原因，不发布部分读取或内存新状态 |
| 不安全的 Compose 项目名 | 在创建资源前退出 |

## 5. 案例

正常：同一曲目两次加入获得不同条目 ID；删除其中一个不影响另一个。
基线：空队列、无历史、无当前项保持前端既有语义；单曲循环自然结束重播当前项，手动下一首按顺序。
错误：条目写入成功后播放区更新失败时仍提交，造成当前条目与队列不一致；必须回滚整个调用方事务。

## 6. 验证边界

独立测试 agent 移植 `frontend/src/features/player/engine/queue.test.ts` 的全部队列用例，另补历史 500 边界、输入切片不变、随机预选消费一致与预选删除重选。
存储集成重点：用户隔离、位置有空洞时插入/交换、整段替换、UUID 数组与 nullable 字段往返、操作组合回滚、升级与回退。
实现阶段不启动数据库，不能把静态检查当作迁移或事务已验证。迁移数量现由嵌入文件动态计算，`migration_version_integration_test.go` 已无硬编码迁移号。

## 7. 错误与正确做法

错误：队列纯包导入 catalog、持久化显示快照，或每个 Store 方法独立开关事务。
正确：纯包只依赖标准库与注入函数；存储只保存身份，复用调用方事务，使队列与播放区更新原子提交。


## 8. SRC 设置文档（0045）

### 范围与入口

迁移 `0045_playback_zone_sample_rate.sql/.down.sql` 增删 `playback_zones.sample_rate JSONB NOT NULL`。zones 拥有 `SampleRateSettings`、`DefaultSampleRateDocument()`、`NewSampleRateDocument(SampleRateSettings)` 与 `ParseSampleRateDocument([]byte)`；渲染使用 `DecodeSettingsDocument[SampleRateSettings]` 取独占临时配置，不反向引入 DSP 依赖。

### 请求、默认与比较

`settings.sample_rate` 总是完整对象：`{strategy,custom_rate,filter}`。默认 `{"strategy":"none","custom_rate":null,"filter":"linear_precise"}`。
策略为 `none|max_sample_rate|power_of_2|custom|dsd`；滤波器为 `linear_precise|minimum_precise|linear_smooth|minimum_smooth`。
`custom_rate` 为 null 或 44.1/48 kHz 两族 ×1/2/4/8/16；`custom + null` 规范化为 `none`，DSD 暂不启用输出。

`SettingsPatch` 只替换给出的完整文档；RegisterDevice 和 set_settings 共用校验。共享设置保存 `SettingsDocument` 的规范 JSON 字符串，`EqualSettings` 逐值比较，不比较 `*int` 地址；对象键序、空白、等值数字拼写不触发重建。改值沿用 300 ms 去抖。

### 校验与错误

| 条件 | 行为 |
| --- | --- |
| 未知键、缺字段、非法策略/滤波器/自定义率，文档本身 null | `400 invalid_input`，不带字段路径 |
| 存储文档形状或值损坏 | 仅 sample_rate 回退默认；每次加载记一条 `zones.settings.invalid`，只含 zone_id/key/reason，不写回库 |
| 查询或队列损坏 | 保留既有错误，不冒充默认值 |
| 回退 0045 | 删除新列及其中设置；再升级恢复默认 |

### 案例与验证

正常：提交 custom/96000/minimum_smooth，存取及 API 返回相同规范对象。基线：未给设置使用默认，两个不同指针指向 96000 不触发重建。错误：非标准自定义率不接受，坏持久值不阻断其它设置。

最小验证复用文档表测、同值不重建和坏值回退集成用例；只刷新 `TestPostgreSQLZonesAPIResponseSamples` 所属金样，新增 `set-settings-sample-rate` 非默认请求/响应；迁移执行升级→回退→再升级并比较 schema。

错误做法：在 DTO 留可变指针并用地址判断设置变化，或直接封装任意 RawMessage。正确做法：先按明确类型校验、规范化，再保存不可变文档，由 zones 单一拥有规则。


## 9. 降混设置文档（0046）

### 范围与入口

迁移 `0046_playback_zone_downmix.sql/.down.sql` 增删 `playback_zones.downmix JSONB NOT NULL DEFAULT '{"enabled":true}'`。zones 拥有 `DownmixSettings{Enabled bool}`、`DefaultDownmixDocument()`、`NewDownmixDocument(DownmixSettings)`、`ParseDownmixDocument([]byte)`；render 使用 `DecodeSettingsDocument[DownmixSettings]`。

### 请求、默认与比较

`settings.downmix` 为完整对象 `{enabled:boolean}`，默认开启。注册与 `set_settings` 复用统一白名单及规范文档入口；patch 仅替换给出的键，不递归合并；`EqualSettings` 比较规范值。读写列、Scan 和参数同步增加 downmix；注册设备沿用数据库默认值，随后统一 UpdateZone 写入规范设置。

### 校验与错误

| 条件 | 行为 |
| --- | --- |
| 文档不是对象、未知键、缺 enabled、null 或非布尔值 | `400 invalid_input`，无字段路径 |
| 持久值形状或类型损坏 | 仅 downmix 回退默认 true，每次加载记录 `zones.settings.invalid` 的 zone_id/key/reason；不记原文，不写回库 |
| SQL 查询或队列损坏 | 保留既有错误，不伪装成 DSP 默认 |
| 回退 0046 | 删除列及设置；再升级恢复默认开启 |

### 案例与验证

正常：`set_settings` 提交 `{"downmix":{"enabled":false}}` 后返回关闭文档，重建沿用 300 ms 去抖。基准：未给出键使用默认 true，同值不会触发重建。错误：`{"downmix":{}}`、`{"downmix":{"enabled":null}}` 均拒绝。

独立测试应覆盖完整文档/同值比较、坏值默认及存储往返；API recorder 在末尾独立播放区采集 `set-settings-downmix` 非默认请求/响应，仅刷新自身拥有的金样。迁移往返验证须在后续获准的隔离数据库中执行，不能把未运行记为通过。

错误做法：新增 boolean 列或为 downmix 再造 HTTP/SQL 注册框架。正确做法：按 sample_rate 的文档模式接入固定读写列与公共校验入口。
