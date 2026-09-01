# Core 0 扫描格式与 CUE 扩展技术设计

## 边界与模块

扩展继续归属 `backend/cmd/roomusic` 的 parser/scanner/catalog 边界。parser
只返回内部 `audioObservation`/虚拟 track 结构，第三方库类型不得穿透；scanner
负责只读文件访问、允许根 containment 和诊断，catalog 负责事务写入与来源身份。

## 数据流与身份

`WalkDir -> extension/CUE 分类 -> parser -> observation -> saveObservation`。
OGG、Opus、WAV 复用现有字段来源和 filename fallback。CUE 先解析声明，再验证
单一 `FILE` 的 realpath containment 与 `INDEX 01` 帧；每个虚拟 track 以
`root_id + cue_relative_path + track_number + referenced_file` 生成确定性来源键，
避免重扫重复实体。CUE 诊断不会参与负向 missing 对账。

## 兼容与迁移

优先复用现有 `tracks.relative_path` 和 observation 表；若需要区分虚拟来源，
增加明确的 source kind/virtual offset 字段与唯一约束，迁移必须可重复执行。
既有 FLAC/MP3 来源键和 DTO 不变。多碟目录只在可证明的目录/碟号规则下新增
Medium，否则保持现有独立 Release 行为并记录诊断。

## 失败与回退

格式 magic、标签损坏、编码错误、越界引用和不支持语法均记录 bounded
diagnostic 并继续扫描其他文件；单文件失败不改变既有来源状态。实现可先只
增加 parser 与 fixture，再接入 scanner/catalog，任何迁移失败阻止服务 ready。
