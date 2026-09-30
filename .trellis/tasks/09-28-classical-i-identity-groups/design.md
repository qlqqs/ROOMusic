# 阶段 I 技术设计

需求与验收见 [prd.md](prd.md)，核实事实与推导见 [research/identity-analysis.md](research/identity-analysis.md)。
本文的行号基于 `8b3a65f`。

## 1. 边界与归属

| 包 | 本任务的改动 |
| --- | --- |
| `library/parser` | 新增与标签无关的内容指纹：FLAC 与 OGG 在解析时顺带算出，其余格式按需读盘计算 |
| `library` | 遍历时记录来源文件的大小与修改时间；编排“遍历 → 规划 → 写入 → 终态”；实现 catalog 的指纹端口；记录新诊断 |
| `catalog` | 接管 `scan_observations` 的读写；新增身份规划（匹配、指纹复用、分类、配对或暂缓）与 Release 分配；`PersistCandidate` 改为按计划写入；删除旧身份兼容；新增按组读取投影 |
| `catalog/httpapi` | 注册 `GET /api/v1/release-groups` 与 `GET /api/v1/release-groups/{id}` |
| `migrations` | 新增 `0013`（曲目指纹与暂存规划列）与 `0014`（锚点改为定位信息） |

- **暂存表改归 catalog**。身份规划要联结暂存观察与曲目表，暂存载荷本就是 `catalog.SourceObservation`、由 organizer
  消费。暂存、按 scope 读取、清理、CUE scope 对齐四项读写从 `library/scanner.go:564`–`630`、`:794`–`811`
  移到 `catalog/staging.go`。路径安全校验仍由 library 在暂存前完成（`createTrackSourceIdentity`，`scanner.go:42`）。
- **catalog 不导入 library 或 parser**。规划需要读文件时，通过 catalog 定义的窄端口向 library 取值：

  ```go
  // Fingerprinter 返回 root 内某个来源文件与标签无关的内容指纹。空串表示该格式不提供指纹；
  // 返回错误表示文件内容无法完整读取。
  type Fingerprinter interface {
  	Fingerprint(ctx context.Context, relativePath string) (string, error)
  }
  ```

  library 的实现先在注册 root 内重新解析路径并校验 containment 与普通文件，再调用 parser。
- `internal/app/dependency_rules_test.go` 的规则表不需要改动。

## 2. 数据模型

### 迁移 0013：曲目指纹与暂存规划列

`tracks`：

| 列 / 约束 | 说明 |
| --- | --- |
| `content_fingerprint TEXT` | 来源文件的内容指纹（CUE 虚拟轨存父文件指纹）；`NULL` 表示未计算。CHECK 非空且不超过 256 字节 |
| `source_size BIGINT`、`source_modified_at TIMESTAMPTZ` | 计算指纹时来源文件的大小与修改时间（CUE 虚拟轨为父文件），用于判断文件是否未变 |
| 删除 `UNIQUE(source_root_id, relative_path)` | 0002 的表级约束，默认名 `tracks_source_root_id_relative_path_key`，实施时用 `pg_constraint` 核对 |
| `tracks_bound_source_path_uq` | 部分唯一索引 `(source_root_id, relative_path) WHERE source_identity IS NOT NULL`：内容被替换的旧曲目失去来源身份后，可以与新曲目同路径并存 |
| `tracks_root_fingerprint_index` | `(source_root_id, content_fingerprint) WHERE content_fingerprint IS NOT NULL`，供配对使用 |

`scan_observations` 新增的规划列：`source_identity`、`fingerprint_path`（要计算指纹的文件，物理轨为自身，CUE 虚拟轨
为父文件）、`source_size`、`source_modified_at`、`content_fingerprint`、`cue_index_frames`、`cue_end_frames`、`candidate_key`、
`matched_track_id`、`planned_track_id`、`content_changed BOOLEAN NOT NULL DEFAULT FALSE`、`withheld BOOLEAN NOT NULL DEFAULT FALSE`，
以及 `(scan_run_id, root_id, source_identity)` 与 `(scan_run_id, root_id, candidate_key)` 两个索引。暂存行随扫描清理，不设外键。

### 迁移 0014：锚点改为定位信息

- 删除 `releases_candidate_anchor_uq`（`0010_album_organization.sql:13`）。`candidate_anchor` 此后只记录最近一次的位置，
  允许一个被放弃的旧 Release 与新 Release 同锚点。
- `scan_observations` 增加 `planned_release_id UUID`。

两个迁移都只新增或放宽约束，不改写已有数据，也不修改旧迁移。

## 3. 内容指纹

格式为 `<种类>-v1:<值>`，种类与版本写进字符串，比较时自然要求种类、版本一致。

| 格式 | 种类 | 值 | 计算时机 |
| --- | --- | --- | --- |
| FLAC（MD5 非零） | `flac-md5` | STREAMINFO 第 18–33 字节的 MD5，加总样本数、采样率、声道、位深 | 解析时（`parser.go:1556`） |
| FLAC（MD5 为零） | `flac-frames` | 最后一个元数据块之后全部字节的 SHA-256 | 按需读盘 |
| M4A | `m4a-mdat` | 按文件顺序拼接全部 `mdat` 负载的 SHA-256（兼容 64 位长度与“到文件尾”的原子） | 按需读盘 |
| MP3 | `mp3-frames` | 去掉开头 ID3v2（含 footer）与结尾 ID3v1、APEv2、Lyrics3v2 后的字节的 SHA-256 | 按需读盘 |
| OGG Vorbis / Opus | `ogg-audio` | 头部包之后全部页的段数据（不含页头）的 SHA-256；注释包变长只改变头部分页，不影响音频页 | 解析时（文件已整体读入，`parser.go:1310`） |
| WAV | `wav-data` | `fmt ` 块与全部 `data` 块负载的 SHA-256 | 按需读盘 |

- 解析器在 `AudioObservation` 上带出解析时就能得到的指纹；`parser.ContentFingerprint(ctx, path)` 负责按需计算。
  计算以流式读取，每读一块检查 context，不把整文件读入内存。
- **CUE 虚拟轨**的 `content_fingerprint` 取父文件指纹，配对键另加 `cue_index_frames` 与 `cue_end_frames`。
- **复用**：保留下来的观察匹配到在库曲目，且大小与修改时间（截断到微秒，与 PostgreSQL 精度一致）都相同、已存指纹
  非空时，直接复用已存指纹。常规重扫不增加读盘；迁移后的第一次扫描会为非 FLAC 文件与 MD5 为零的 FLAC
  各读一次音频数据。
- **失败**：按需计算失败时，记 `fingerprint_failure` 诊断，该文件所在 scope 本轮暂缓写入，root 记为不完整。
- 已知不影响正确性的边界：`mp3gain` 之类改写帧增益的工具、FLAC 文件尾附加的非标准 ID3v1、M4A 中的章节文本轨，
  都会被视为内容变化，走“换内容”处理（新身份），不会错误沿用。

## 4. 扫描流程

```text
遍历（现有逻辑；暂存时额外写入来源身份、大小、修改时间与免费指纹）
  → 规划 catalog.PlanStagedRoot（仅当遍历未被取消且无致命错误，与现在的写入门槛相同，scanner.go:410）
      a. 逐 scope 读取暂存（不含封面字节）并 organize，为保留下来的观察写入 candidate_key
      b. 按来源身份匹配在库曲目，写 matched_track_id
      c. 复用未变文件的已存指纹；其余经 Fingerprinter 分页计算
      d. 分类：同内容匹配 / 换内容 / 未匹配
      e. 遍历完整 → 配对重绑定；不完整 → 暂缓可疑 scope
      f. Release 分配（第 6 节）
  → 写入：逐 scope、逐候选短事务，按计划写；跳过暂缓的 scope
  → 终态：不变。只有整轮 succeeded 才执行 missing 对账（persist.go:215）
```

- **“遍历完整”按 root 判断**（2026-09-30 用户裁决）：该 root 遍历结束时 `complete` 仍为真，且规划中没有指纹失败。
  其他 root 不可用不影响本 root 的重绑定；整轮状态与 missing 对账规则不变。
- 写入阶段仍可能被取消或出错。重绑定随所在候选的事务提交，依据的是完整的遍历证据；取消后已提交的不回滚，
  未写到的候选保持原状。
- 规划只做 SQL 与读文件，不持有长事务；organize 在规划和写入中各跑一次，规则是确定性的，两次结果一致。
  写入时若候选的观察缺少规划记录，按 `catalog_write_failure` 处理并令 root 不完整，不猜测身份。

## 5. 身份规则

### 5.1 分类（只看 organize 后保留下来的观察）

| 条件 | 分类 | `planned_track_id` |
| --- | --- | --- |
| 有同身份的在库曲目，且两边指纹至少一边未知或相等 | 同内容匹配 | 该曲目 |
| 有同身份的在库曲目，两边指纹都已知且不同 | 换内容（`content_changed`） | 暂空，待配对 |
| 没有同身份的在库曲目 | 未匹配 | 暂空，待配对 |

被 organizer 隐藏的观察（例如多轨 CUE 的整轨镜像）不参与配对，也不计算指纹；它原有的曲目按现有规则在成功扫描中
置为 missing。旧身份兼容（`persist.go:77`–`81`、`identity.go:24`–`49`）删除，曲目只按当前来源身份匹配。

### 5.2 消失集合与配对

- **消失集合**：本 root 中状态为 present、已有指纹，且没有被任何保留观察计划使用的曲目（包括换内容的原曲目，以及
  此前被腾出身份但仍为 present 的曲目）。已经是 missing 的曲目不参与，符合“本轮将被置为缺失”的批准范围。
- **配对键**：`(content_fingerprint, cue_index_frames, cue_end_frames)`，NULL 按相等处理。
- **遍历完整时**：同一键在未匹配或换内容的观察中恰好一条、在消失集合中也恰好一条，才重绑定——该观察的
  `planned_track_id` 设为那条曲目。两侧任一侧多于一条且另一侧非空时不重绑定，逐键记 `identity_ambiguous` 诊断。
  配对后仍为空的换内容观察建新曲目，记 `content_replaced` 诊断。
- **遍历不完整时**：不配对。凡是含有“键落在消失集合中的未匹配观察”或“换内容观察”的 scope，整个 scope 标记
  `withheld`，本轮不写入，记 `identity_deferred` 诊断；其余 scope 照常写入。以 scope 为单位是为了不只扣掉 CUE
  父子关系中的一方而改变 organizer 的去重结果。这样一次不完整扫描不会把移动后的文件写成新曲目，下一次完整遍历
  仍能配对。

### 5.3 诊断

写入现有 `scan_diagnostics`，沿用每次扫描最多 100 条的上限，管理员经 `GET /api/v1/scans/{id}/diagnostics` 查看：

| kind | relative_path | message |
| --- | --- | --- |
| `identity_ambiguous` | 该键下按路径排序的第一个新来源 | 多个来源的音频内容相同，没有沿用原身份 |
| `content_replaced` | 被替换的路径 | 同一路径的音频内容已变化，已作为新曲目 |
| `identity_deferred` | 该 scope 中第一个可疑来源 | 扫描不完整，疑似移动或内容变化的来源留待完整扫描处理 |
| `fingerprint_failure` | 读取失败的文件 | 无法读取音频内容以计算指纹 |

成功的重绑定不写诊断；每个 root 规划结束时记一条 INFO 日志 `library.scan.identity_resolved`，只含计数
（同内容匹配、重绑定、歧义键、换内容、暂缓 scope、计算与复用的指纹数、失败数）和 `scan_run_id`、
`library_root_id`，不含路径。

## 6. Release 分配

在 5.2 之后、写入之前，对未暂缓的全部候选一次性计算：

1. 候选 C 的总数 = 其保留观察数；与既有 Release R 的重叠 = 其中 `planned_track_id` 当前属于 R 的条数
   （`tracks.medium_id → media.release_id`）。
2. 重叠超过总数一半时，C 倾向 R。严格过半保证每个候选至多倾向一个 Release。
3. 多个候选倾向同一 R 时：重叠多者得；平局时 `candidate_key` 等于 R 当前锚点者优先（留在原位置的一方）；
   再平局按 `candidate_key` 字符串升序。
4. 胜者的全部暂存行写入 `planned_release_id = R`；落选或没有倾向的候选留空，写入时新建 Release 与新组。

候选键即 `CandidateIdentity(rootID, anchor)`，与 `releases.candidate_anchor` 同一格式。锚点不再用于查找，只在写入时
更新为最近一次的位置。

## 7. 按计划写入

`PersistCandidate(ctx, db, scanID, rootID, candidate)` 签名不变，事务内的步骤：

1. 按 `candidate_key` 读取本候选的暂存行（`planned_track_id`、`planned_release_id`、指纹、大小、修改时间）。
2. Release：有 `planned_release_id` 时更新字段、`candidate_anchor`、`candidate_kind`、`relative_directory`；否则新建
   Release 与 ReleaseGroup（与现在相同，每个新 Release 一个新组）。
3. Medium：按 `(release_id, position)` 查找或新建（不变）。
4. 每条曲目：
   - 若另一条曲目仍持有本观察的来源身份（换内容的原曲目，或交换中尚未改名的一方），先腾出：把它的
     `source_identity` 置空；它若没有在本轮计划中被别的观察使用，同时置为 missing（它的内容已不在该路径）。
   - 有 `planned_track_id` 时更新该曲目：Medium、位置、字段、相对路径、来源身份、指纹、大小、修改时间，
     `source_status='present'`，`observed_at=NOW()`；否则插入新曲目。
   - 替换逐字段观察与署名（不变）。
5. 删除本 Release 的空 Medium，替换字段决定、Release 署名与分组证据（不变）。封面仍在事务提交后处理。

“先腾出再占用”在同一候选内和跨候选的交换中都成立，与处理顺序无关：先处理的一方把对方的身份置空，后处理的
一方占用时该身份已经空出。被腾出、但计划由后续候选接手的曲目保持 present，扫描若在中途取消，下一次完整遍历
仍能把它放进消失集合重新配对。

## 8. 按组只读接口

### 合同

- `GET /api/v1/release-groups?q=&attention=required&page=&page_size=`：参数、默认值、上限与错误码与 `/releases` 相同
  （`invalid_pagination`、`invalid_query`、`invalid_attention`）。
- `GET /api/v1/release-groups/{id}`：非 UUID 返回 `400 invalid_id`；组不存在或没有可见成员返回 `404 not_found`。
- 都要求登录；读取失败返回与 `/releases` 相同的 `503` 错误信封。

```json
{
  "items": [
    { "id": "组 ID", "release_count": 1, "attention_count": 0, "representative": { "…": "ReleaseSummary" } }
  ],
  "pagination": { "page": 1, "page_size": 50, "total": 68 }
}
```

详情在组摘要上加 `releases`：按代表版本规则排序的成员 `ReleaseSummary`，最多 100 条；`release_count` 是完整数量。

### 语义

- **可见性**：成员 Release 可见的条件与 `/releases` 相同（至少一条 present 曲目，`read.go:28`）。组至少有一个可见
  成员才出现；`release_count` 只数可见成员。
- **代表版本**：可见成员中年份最早（无年份排最后）→ 创建最早 → ID 最小。M4 引入默认版本后排在最前。
- **搜索**：任一可见成员命中 `/releases` 的搜索条件，组即命中。
- **attention**：`attention_count` 为可见成员之和；`attention=required` 取其大于 0 的组。
- **排序**：代表版本的 `LOWER(title)`、`LOWER(artist)`、代表版本 ID。每组一个版本时，结果与 `/releases` 的顺序、
  总数逐项对应。
- 实现放在 `catalog/groups.go`：可见 Release 的 CTE 上用窗口函数求代表版本、成员数、attention 之和与搜索命中，
  投影复用 `releaseSummaryProjection` 与封面 `LEFT JOIN`，不逐组查询。

## 9. 兼容、迁移与回退

- **不兼容旧身份**：0010 旧身份与同路径兜底直接删除（开发阶段不做兼容）。仍带旧身份的曲目会在下一次扫描中
  被视为未匹配，按常规得到新身份，旧行在成功扫描中置为 missing。
- **迁移后第一次扫描**：所有曲目都没有已存指纹，因此不会发生配对；它为全部保留观察补齐指纹（非 FLAC 读一次
  音频数据），之后的扫描才开始重绑定。
- **回退**：各步骤单独提交，可以逐个回退代码。0013、0014 只放宽约束、增加列，保留在库中无害；但回退到 0014
  之前的代码时，旧代码按锚点 `QueryRow` 可能遇到同锚点的多个 Release，需要前向修复而不是删除数据。不写 down
  migration。

## 10. 已知限制与取舍

| 情况 | 结果 | 理由 |
| --- | --- | --- |
| 某个 root 一直不完整（例如有一个始终损坏的文件） | 该 root 的移动一直暂缓，旧条目保留 | 不完整的证据不能用来判断移动；修好文件后下一次完整扫描即处理 |
| 原地替换音频（如重新抓轨） | 新曲目 ID；若整张专辑都被替换，发行也会新建 | 同路径不能证明内容相同，按批准规则不沿用 |
| 新增文件多于原有文件（如 2 轨加 3 轨） | 新建 Release，原 Release 失去全部曲目后不可见 | 严格过半是“包含其多数曲目”的保守读法，避免新专辑继承旧发行的 ID |
| 同内容的多个文件 | 永不重绑定，逐键记诊断 | 批准规则要求唯一 1:1；人工认领属于 M3 及以后 |
| 写入阶段被取消 | 已提交的重绑定保留 | 逐候选短事务；依据的遍历证据完整 |
| 跨 root 移动 | 新身份，旧来源在成功扫描中置为 missing | 批准规则：跨 root 需单独审阅的映射操作 |

## 11. 测试策略

- **parser 单元测试**：每种格式“同音频、不同标签 → 指纹相同”“音频改变 → 指纹不同”；FLAC MD5 为零的回退；MP3 首尾
  各类标签；OGG 注释包跨页；M4A 多个 `mdat` 与 `moov` 在后；WAV 的 LIST 块；读取中途取消。
- **PostgreSQL 集成（真实临时目录中的合成文件，走完整扫描流程）**：见 [implement.md](implement.md) 第 3、4 步的矩阵。
  用可计数的 Fingerprinter 验证未变文件不重复计算。
- **REST 集成**：按组列表与 `/releases` 的总数、顺序、代表版本逐项对应；搜索、attention、分页、present-only；多成员组的
  代表版本规则与计数（直接插入测试数据）；401、400、404；路由表测试加入两个新路由。
- **只读真实库核对**（需在规划批准时一并授权）：设置 `ROOMUSIC_REAL_MUSIC_ROOT` 才运行的测试，对真实库计算指纹，
  只输出按种类的计数、失败数、MD5 为零的 FLAC 数、重复指纹数、读取字节数与耗时，不输出路径或文件名。
