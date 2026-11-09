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
