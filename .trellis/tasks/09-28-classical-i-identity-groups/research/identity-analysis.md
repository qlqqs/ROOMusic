# 阶段 I 规划分析

> 2026-09-30，代码基线 `8b3a65f`（B0 完成后）。本文记录规划期核实的事实与推导，结论以 PRD 与 design.md 为准。
> 公开仓库：只描述机制，不含真实路径、文件清单或媒体内容。

## 一、核实的现状

### 1. 扫描流水线

| 环节 | 事实 | 位置 |
| --- | --- | --- |
| root 顺序 | 按 `path,id` 串行扫描每个 active root | `backend/internal/library/scanner.go:93`–`106`、`:206`–`221` |
| 遍历与暂存 | 每个音频文件解析后写入 `scan_observations`（JSONB；内嵌封面每个 scope 只保留一份）；CUE 虚拟轨在解析 sheet 时生成 | `scanner.go:260`–`409`、`:564`–`604` |
| 写入 | 遍历未被取消且没有致命错误时才写入（`scanner.go:410`）；root 不完整（如解析失败）也写入有效候选；按 scope 分页读取、organize，逐候选短事务写入 | `scanner.go:410`–`428`、`:632`–`683` |
| 终态 | `finalizeScan` 在单事务里锁定扫描行；只有 `succeeded` 才调用 `catalog.MarkMissingSources`，按 `observed_at < started_at` 置 missing | `scanner.go:168`–`204`；`backend/internal/catalog/persist.go:215`–`222` |
| 不完整条件 | 遍历或权限错误、坏链接、非普通文件、CUE 无法解析或引用不安全、音频解析失败、不支持的音频格式、暂存/写入/封面失败、取消 | `scanner.go:260`–`432` |
| 真实库 | 2026-09-03 冒烟：两次扫描均 `succeeded`，各约 5 秒；CUE 缺失引用或缺 INDEX 只写诊断，不令扫描不完整 | 归档任务 `09-03-real-library-smoke-v0-comparison` 的 `research/smoke-result.md` |

### 2. 身份与约束

| 对象 | 现状 | 位置 |
| --- | --- | --- |
| 物理 Track | `root:physical:v1:<规范相对路径>` | `backend/internal/catalog/identity.go:21` |
| CUE Track | `root:cue:v1:<sheet>:<父来源>:<轨号>:<INDEX 01 帧>` | `identity.go:19` |
| Track 查找 | 新身份、0010 旧身份，以及同 root 同路径兜底 | `persist.go:77`–`81`、`identity.go:24`–`49` |
| Release 查找 | 按 `(source_root_id, candidate_anchor)`；未命中则新建 Release 和一个空 ReleaseGroup | `persist.go:40`–`47` |
| 锚点 | root + `v2` + 种类 + 范围 + 分区；散落和同目录拆分候选的分区取组内最小相对路径 | `backend/internal/catalog/organizer.go:979`–`982`、`:922`–`935` |
| 唯一约束 | `tracks` 的 `UNIQUE(source_root_id, relative_path)`；`tracks_source_identity_uq`；`releases_candidate_anchor_uq` | `backend/migrations/0002_core_slice.sql:60`；`0010_album_organization.sql:13`–`14` |
| ReleaseGroup | 只有 `id`、`created_at`，一个 Release 一个组 | `0003_catalog_observations.sql` |
| CUE 与整轨并存 | 同一父文件有多条虚拟轨时隐藏整轨，只有一条时保留整轨 | `organizer.go:937`–`965` |

### 3. 解析器能提供的指纹依据

| 格式 | 当前读取范围 | 与标签无关的指纹候选 | 额外读盘 |
| --- | --- | --- | --- |
| FLAC | 全部元数据块；STREAMINFO 已解析，但第 18–33 字节的音频 MD5 未读取 | MD5（非零时）加总样本数、采样率、声道、位深；MD5 为零时回退为音频帧字节的哈希 | MD5 无；回退需读整个文件 |
| M4A（ALAC/AAC） | 只遍历原子、读取 `moov`，跳过 `mdat` | 全部 `mdat` 负载的哈希加编码参数（备选：`stsz` 样本大小表的哈希，不额外读盘，但只是结构指纹） | 整个音频数据 |
| MP3 | ID3v2 及其后 64 KiB | 去掉首尾标签（ID3v2、ID3v1、APEv2、Lyrics3）后的帧数据哈希 | 整个文件 |
| OGG Vorbis/Opus | `os.ReadFile` 已读入整个文件 | 除注释包以外的包数据哈希（按包而不是按页计算，注释长度变化会改变分页） | 无 |
| WAV | 跳过 `data` 块 | `data` 块哈希加 `fmt` 参数 | 整个 data 块 |
| CUE 虚拟轨 | 父文件的解析结果加区间 | 父文件指纹加 `INDEX 01` 起点与终点 | 同父文件 |

位置：`backend/internal/library/parser/parser.go` 的 `:1513`–`1577`（FLAC）、`:116`–`185`（M4A）、`:1624`–`1657` 与 `:1722`（MP3）、
`:1309`–`1374`（OGG）、`:1378`–`1459`（WAV）。真实库构成（2026-09-27 只读统计）：flac 199、m4a 84（抽检均为 ALAC）、
cue 8、mp3 1，约 7.6 GB。

### 4. 读取与路由

- `/releases` 列表与详情只含至少有一条 present 曲目的 Release（`backend/internal/catalog/read.go:28`–`34`）；
  排序 `LOWER(title), LOWER(artist), id`（`read.go:146`）；搜索覆盖标题、艺人、专辑艺人与 present 曲目标题。
- 路由表由 `backend/internal/app/routes_test.go` 固定，新路由必须加入。
- 已有只读真实库测试的先例：`backend/internal/library/parser/real_music_readonly_test.go`（设置
  `ROOMUSIC_REAL_MUSIC_ROOT` 才运行，默认跳过）。

## 二、推导结论

1. **旧曲目的指纹必须提前保存。** 配对时旧文件已不在原处，无法再计算。所以每条曲目在首次出现或文件变化时计算
   一次并存入 `tracks`；“只在候选配对时计算”只适用于新出现的一侧。以“文件大小 + 修改时间”判断文件未变，
   未变时复用已存指纹，常规重扫不增加读盘；只有首次扫描（含迁移后的第一次）和文件变化时读取音频数据。
2. **重绑定无法与扫描最终状态原子绑定。** 候选按短事务逐个提交（避免在大库上长时间持有事务），写入阶段仍可能
   被取消、停机或出错。因此“完整”只能指遍历证据完整：写入开始前确认遍历完整且未收到取消；逐候选提交的
   重绑定都基于完整证据，取消后已提交的不回滚，没写到的候选保持原状。
3. **不完整扫描会“吃掉”移动。** 现在不完整扫描也写入有效候选。若它把移动后的文件写成新曲目，下一次完整扫描
   时这些文件已有身份匹配，不再算“本轮新出现”，旧曲目就再也配不上对。要在中间夹了一次不完整扫描时仍保住
   ID，不完整遍历中可能属于移动或同路径换内容的 scope 要暂缓写入，留给下一次完整遍历。以 scope 为单位暂缓，
   避免只扣掉 CUE 父子关系中的一方而改变 organizer 的去重结果。
4. **按 root 还是按整轮判断完整。** 配对只在同一 root 内进行，所需证据只来自该 root 的遍历。按整轮判断时，
   任何一个 root 不可用（例如 NAS 离线）都会阻断其他 root 的改名处理，并让那些移动一直暂缓；按 root 判断只看
   本 root。两种方式都不改变 missing 对账规则（仍要求整轮 `succeeded`）。
5. **配对放在 organizer 之后。** 只对最终保留的观察计算指纹和配对：不会与被隐藏的观察配对，也不必为每次都被
   隐藏的 CUE 整轨镜像（常见为大 WAV）计算指纹。
6. **成员关系匹配需要全 root 视图。** 同一 Release 的曲目可能分到不同 scope 的候选（例如把专辑的一部分移到另一个
   目录）。“重叠更高者优先”要求写入前看到全部竞争者，现有逐 scope 流式写入要改为“先规划、后写入”两遍。
   规划结果写在暂存行上，由 SQL 完成全 root 的配对与分配，内存保持有界。
7. **暂存表归 catalog 更顺。** 身份解析要联结暂存观察和曲目表；暂存载荷本就是 `catalog.SourceObservation`，由
   organizer 消费。把 `scan_observations` 的读写（暂存、按 scope 读取、清理、CUE scope 对齐）移入 catalog；
   library 负责遍历、解析、读文件（指纹、抓轨日志、文件夹封面）和扫描生命周期，两边不再读对方的私有表。
8. **约束需要调整。** 锚点降为定位信息后，`releases_candidate_anchor_uq` 会阻止新 Release 使用同一位置（如同目录
   内容整体替换），应取消唯一性；同路径换内容要为新内容建新曲目，`UNIQUE(source_root_id, relative_path)` 应改为
   只约束仍有来源身份的行。
9. **旧身份兼容可以删除。** 0010 旧身份与同路径兜底按“开发阶段不做兼容”移除，测试
   `TestPostgreSQLFirstRepairScanReusesLegacyCueTrackIdentity` 随之删除；0011 迁移本身及其重放测试不动。

## 三、成员关系匹配的边界

规则草案：候选 C 的全部曲目（含本轮新出现的）中，超过一半当前属于同一个既有 Release R 时，C 倾向 R；多个候选
倾向同一 R 时，重叠数多者得；平局时先选锚点与 R 当前锚点相同者，再按锚点字符串升序。落选或没有倾向的候选
新建 Release（及新组）。

| 场景 | 结果 |
| --- | --- |
| 未变化重扫、删文件、目录改名（经重绑定） | 全部命中原 Release |
| 10 轨专辑新增 1 个文件 | 10/11，沿用 |
| 2 轨专辑新增 3 个文件 | 2/5 不过半，新建（保守；原 Release 失去全部曲目后不可见） |
| 1 轨发行新增 1 个文件 | 1/2 不过半，新建 |
| 目录拆成两个目录各 5 轨 | 平局，留在原位置的一半沿用 |
| 单轨发行被移入含 10 个新文件的目录 | 1/11，新建，新专辑不会继承旧发行的 ID |

## 四、用户裁决

- 2026-09-30，qlqq 选择“按 root 判断”：一个 root 遍历完整即在该 root 内重绑定，不受其他 root 不可用影响
  （第二节第 4 点）。

## 五、在设计中确定的技术选择（不需裁决）

- 各格式指纹方案（第一节第 3 点），M4A 取 `mdat` 负载哈希；指纹计算失败按解析失败处理。
- 指纹缓存键为“文件大小 + 修改时间”。
- 诊断沿用 `scan_diagnostics`（每次扫描上限 100 条），新增歧义、暂缓、换内容三类。
- 按组接口的形状、排序、搜索与 attention 语义与 `/releases` 对齐。
