# 扫描校验矩阵、案例与必需测试

从[目录与扫描流程合同](./library-scan-contracts.md)拆出：§4 校验与错误矩阵、§5 案例、§6 必需测试；章节编号沿用原合同。

## 4. 校验与错误矩阵

| 条件 | 行为 |
| --- | --- |
| 同一幂等键用于另一个目录，或同一目录的不同请求参数 | `409 idempotency_conflict`；不执行、不写记录，也不返回前一个请求的结果 |
| 同一幂等键的并发请求（新增、停用、恢复、改路径及排除项增删） | 串行处理，只执行一次，其余请求回放首次记录的状态码和响应 |
| 停用/恢复/改路径的路由目录 ID 不是 UUID 形式 | `400 invalid_id`，不进入幂等流程，不写操作记录 |
| 重复注册 disabled root | 返回 `status=disabled` 与真实 revision，不隐式恢复 |
| 改路径未登录 / 非管理员 / 来源不符 | `401` / `403` / 来源拒绝；无副作用，鉴权先于资源读取 |
| 改路径缺幂等键、缺 path 或 `expected_revision < 1` | `400 invalid_input`；不写记录 |
| 改路径越界、不存在、非目录或任一层为符号链接 | `400 invalid_library_path` / “目录不在允许的音乐根目录内”；不进入幂等流程，不泄露允许根或系统错误 |
| 改路径时本进程或其他进程持有扫描锁 | `409 scan_running` / “正在扫描，稍后再试”；不修改、不写记录 |
| 改路径目录不存在 / revision 不符 | `404 not_found` / `409 revision_conflict`；不修改、不写记录 |
| 新路径已被其他根登记（含停用根），或并发新增触发 `23505` 且约束为 `library_roots_path_key` | `409 path_already_registered` / “这个文件夹已经添加过了”；回滚，不把其他唯一约束错误误判为路径冲突 |
| 改路径为当前规范路径，且 revision 相符、取得扫描锁 | `200` 安全六字段 DTO，revision 不变，写成功记录 |
| 改路径数据库失败 / 操作记录写入失败 | `503 database_unavailable` / `500 operation_failed`；整个事务回滚 |
| 停用后改路径导致版本不再紧邻停用记录 | 恢复取最近成功 disable 的 active 前态；仍校验当前 disabled 与请求版本，保留新路径 |
| unsupported、音频解析失败、CUE 失败、子目录读不了 | 见完整性矩阵；诊断并保住失败来源，其余来源正常对账 |
| 隐藏项、内置名单和用户排除 | 不诊断、不保住；成功后按消失处理 |
| 候选为零且已有 present 来源，无目录读取失败或排除命中 | `root_empty`，不规划不写入、不做 missing |
| CUE 引用目标不存在或 Track 缺少 `INDEX 01` | 写 `cue_reference` 诊断并跳过不可用虚拟 Track；不单独令扫描 incomplete，也不伪造父来源/offset |
| 初始或终态 staging 清理失败 | root 不完整并记录 `staging_write_failure`；旧 observation 不得混入本次候选 |
| 媒体节点不是 regular file | 不调用 parser，记录 `non_regular_file` 并保住该文件，root 仍完整 |
| CUE 显式父来源跨 sheet 目录但仍在注册根内 | 对齐到父来源 scope，保留稳定虚拟身份并按明确关系去重 |
| rip log 无明确签名、签名晚于 64 KiB 或为 symlink | 不产生 CD 语义，不读取超出预算的内容 |
| 明确 rip log 与 `folder` 补充值并存 | 选定 CD 并把 decision provenance 提升为 `rip_log`；不得保留重复字段决定 |
| 明确 rip log 与 tag/CUE/音频规格决定冲突 | 保留明确来源，不允许抓轨日志覆盖 |
| 艺人/credit 含换行或 NUL | 先切段再清理，不能把两个名字粘连；固定组合与拉丁姓名保护按第3节[标签多值、署名与合辑](./library-scan-tags.md)执行 |
| TIT1 没有乐章标签且解析不出作品编号 | 忽略分组值，仍可回退到 WORK |
| 非古典扫描流派且没有 WORK | 不从标题推断作品；有 WORK 不受流派限制 |
| 标签作品只有一轨 | 保存作品供搜索，work_index 为 NULL，REST work 为 null |
| 乐章号非正数或超出 INTEGER | 不保存序号，不使合法作品名失效 |
| 作品列违反非空、长度、正数或关联 CHECK | 候选事务回滚，按既有 catalog_write_failure 记录；不吞错 |
| TIPL/IPLS/TMCL/INVOLVEDPEOPLE 的 NUL 奇数个值 | 只丢最后落单项，已成对的署名保留 |
| 未知工种或乐器 | TIPL/IPLS/INVOLVEDPEOPLE 归制作人、TMCL/PERSONNEL 归演奏并保留细分；未知普通标签键忽略 |
| 演奏括号含未知非拉丁文字 | 整个原文作为姓名，不把别名猜成乐器 |
| 歌词头部首个非署名行 | 停止提取，之后的署名形状行不读取；歌词原文不变 |
| 同角色同时有标签与歌词署名 | 全部采用标签来源，不合并该角色的歌词署名 |
| 专辑艺人高优先级为空或多值 | 回退到首个非空级，展开该级全部值，不采用最后写入值 |
| TXXX 描述/值编码无效、缺终止符，或帧压缩/加密 | 跳过该帧，不使整个文件解析失败；证据超预算仍按既有规则失败 |
| 无真实专辑艺人，合辑标志恰好半数 | 不因标志判合辑；必须严格过半，目录观察不计票 |
| 有真实专辑艺人且合辑标志为真 | 保留真实专辑艺人，不改发行决定 |
| 本版年份存在且原始年份更早 | 保留本版年份，不覆盖 |
| 相同 album 的部分 Track 缺少 AlbumArtist，且其它 Track 只有一个明确值 | 保持同一候选，明确 AlbumArtist 优先于 Track Artist |
| 命名 folder artwork 非普通文件、为空、超限或格式损坏，且所有查找目录里没有别的可用封面 | `artwork_unreadable`，root 仍完整，保留已有关系，不解释为封面删除 |
| 前一个目录里的命名封面无效，后一个目录里有可用封面 | 用后一个目录的封面，不报错 |
| 查找目录不存在 | 跳过该目录，不报错 |
| 读查找目录失败（非“不存在”，如权限） | 记为错误并继续找下一个目录；全部找完仍没有可用封面才 `artwork_unreadable`；root 仍完整 |
| 发行目录与曲目目录都有封面 | 先按四轮优先级，再按目录顺序；两者均命中精确名单时发行目录胜出，曲目目录精确名优先于发行目录附属子目录封面 |
| 候选带内嵌封面，目录里也有文件夹封面 | 内嵌封面，`source_type=embedded` |
| 文件名只是大小写不同（`Cover.JPG`、`FOLDER.png`） | 与全小写名同样被发现；优先级不受大小写影响 |
| content-addressed 受管文件与其 hash 内容不符 | 写临时文件并原子替换，随后才确认数据库关系 |
| 受管封面查询或旧文件清理失败 | 返回分类错误并令扫描不完整，不静默吞错 |
| 单个 candidate/封面写入失败 | 已提交候选保持可见，记录分类诊断并使扫描不完整，禁止 `missing` |
| 同一路径的音频内容指纹变化 | 新内容建新曲目（新 UUID）；原曲目失去来源身份，没有配对到别处时置为 `missing`；记 `content_replaced` |
| 遍历完整的 root 中，无身份匹配的观察与本轮消失的曲目指纹相同且各恰好一条 | 沿用原曲目 UUID 并更新路径；任一侧多于一条时不重绑定，逐键记 `identity_ambiguous` |
| 遍历不完整或被取消时出现疑似移动或换内容的来源 | 不重绑定、不因此新建身份、不置 `missing`；所在 scope 暂缓写入并记 `identity_deferred`，其余 scope 照常写入 |
| 来源文件无法完整读取以计算指纹 | 记 `fingerprint_failure`，该 scope 暂缓写入，root 不完整，本轮不配对 |
| 规划失败，或候选的观察没有规划 | 记 `catalog_write_failure`，root 不完整；不写入、不猜测身份 |
| 候选已规划沿用的曲目严格过半属于同一 Release | 沿用该 Release UUID；不过半（如 2 轨加 3 轨、1 轨加 1 轨）则新建 Release；格式版本可加入已有组，其他情况新建 ReleaseGroup |
| 扫描成功且所有 root 完整 | 允许负向 `missing` 对账 |
| 非音频附件 | 忽略，不改变扫描完整性 |
| 并发流水线 apply 失败或取消 | 停止提交；取消丢弃未应用结果并等待 worker 退出，root 不完整，不做 missing |
| 缓存表读写失败、缓存内容不能解码 | WARN，按未命中解析，不改变 root 完整性 |
| 大小、微秒 mtime 或解析器版本变化 | 重新解析；完整模式即使相同也重新解析 |
| 延迟内嵌封面读取失败 | `artwork_unreadable`，继续原文件夹封面查找 |
| 同名/同大小/同时间的移动来源任一侧不唯一，或 full 模式 | 不借用指纹，照常计算或按原来源规则复用 |

## 5. 案例

T 线标签、署名与作品的补充验证见[目录与扫描流程验证与示例](./library-scan-verification.md)。以下保留目录、文件与归组验证。

- 基准：重复 POST 已停用目录返回 disabled；管理员随后使用 restore 端点显式恢复。
- 正确：NAS 新位置包含同样的相对路径与音频，改路径后 root ID 不变，下一次成功扫描沿用 Track/Release UUID，个人数据与修正不迁移。
- 基准：用新幂等键提交当前路径且 revision 相符，返回 `200`、revision 不变，收据仍可回放。
- 正确：revision 4 的目录停用到 5、改路径到 6，再按 `expected_revision=6` 恢复到 7；使用最近成功停用的 active 前态，路径保持新位置。
- 错误：把停用根的已登记路径视为空闲，或让扫描锁冲突的失败请求占用幂等键。
- 正确：多碟来源按 `disc_number=1/2` 形成两个 Medium，重扫保持 Track ID。
- 正确：`sheets/album.cue` 引用 `../media/image.flac` 时，只要两者仍在同一注册根内，
  虚拟轨会与显式父来源对齐；每轨 provenance 独立，track PERFORMER 不污染相邻轨。
- 基准：历史 CUE 指向已不存在的原始 WAV，或个别 Track 没有 `INDEX 01` 时，保留
  `cue_reference` 诊断并跳过这些虚拟 Track；同目录其它有效物理来源仍可完成扫描。
- 基准：只有 EAC/XLD 明确签名位于 `.log` 前 64 KiB 时补充缺失的 CD 语义；无签名
  日志保持未知。
- 正确：目录名先补了 WEB/CD 等来源值，但同 candidate 存在明确 EAC/XLD 日志时，
  抓轨规则可以替换 `folder` 决定；tag 或 CUE 的明确来源仍保持不变。
- 正确：`Artist Beta / Artist Alpha` 作为 Track artist 被拆分后以稳定顺序展示；已登记的
  别名不再改写事实显示名，固定组合名不会因包含 `/`、`,` 或 `&` 被误拆。
- 正确：一张专辑只有一轨提供 `AlbumArtist`、其余轨只提供各自 Artist 时仍形成一个
  candidate，Release artist 与 credit 使用明确 `AlbumArtist`；缺失轨不伪造自己的 tag。
- 正确：已存在的 content-addressed 封面文件被破坏后，完整重扫或候选重写时按期望字节修复；旧 key
  清理失败返回 `artwork_failure`，不报告成功。
- 正确：CUE 专辑目录里的 `cover.png` 被绑定；严格多碟专辑父目录里的 `Cover.JPG` 被绑定；Box 叶子目录没有封面时，
  Box 父目录里的封面不会绑给它；封面后来才放进目录，下一次扫描即绑定。
- 正确：unsupported 文件诊断并保住其已有曲目，扫描 succeeded，真正删除的其它曲目照常 missing。
- 错误：把 root 原始路径放入列表 JSON；把失败来源当消失用于移动配对；把 FIFO 交给 parser；按标题删除 CUE 轨。

## 6. 必需测试

- ⑤ 单元：Ordered 混排/窗口/Flush 复用/首错/取消退出与 Map 输入顺序，race；配置默认 4、边界 1/16、非法 0/17/abc；OGG/Opus 新旧观察逐字段和指纹一致，大文件内存不随大小增长，截断仍失败。
- ⑤ PostgreSQL：并发 1/4 对比缓存命中与解析计数、暂存顺序、诊断（含 100 条截断）、HoldSource 和反馈；覆盖 CUE 在物理父文件前后、多个 sheet 共父与跨目录父音频、缓存冷热/full、取消丢弃在途结果且 worker 退出。测试另由测试 agent 实施，不把无数据库时的 SKIP 当作通过。

- ④ 单元：当前支持格式/未知扩展名的缓存版本、deferred 封面选取与暂存去重；HTTP 空请求体默认增量、两种合法模式、未知字段/缺 mode/非法 mode 拒绝，所有角色/入口包含 `mode`。
- ④ PostgreSQL：连续扫描未变文件命中、mtime/size/格式版本变化重新解析、full 绕过读取但仍写缓存；CUE sheet 每轮读取且父音频共用缓存；超 2000 条目录的逐条查询、seen 批量刷新、删除/过滤清理与取消/walk_error 不清理；缓存故障不改变完整性。
  deferred 候选重写仍取内嵌封面；目录改名借用且 Track UUID 不变，任一侧多文件歧义不借用、保住范围不被借用，full 不借用。0041 升级/下行/再升级覆盖两表一列及历史模式默认值。
- ④ 候选跳过：成员、版本、CUE、封面四轮候选与抓轨日志每类输入使签名失效，无关文件不影响；未变候选零写入/零反馈变化/零 deferred 读取，治理改归属后重写，升归组版本全部重写，touch 不改变音频分析输入快照。另验封面共享列举保持命中即停/首错顺序/过滤；被跳过版本仍为新增同组版本提供组 ID，重复扫描不误触发 group moves/收藏转移；完整遍历才清候选状态，取消与 walk_error 不清，touch 失败阻断 missing，状态表故障只降级。

- 改路径 PostgreSQL/HTTP：成功保持 root ID/status/excluded_paths、revision 仅在路径变化时加一；成功响应及回放严格六字段，响应/操作记录列表不含绝对路径，前后态仅库内可见。
  覆盖同键回放、同键异义（含目标/规范路径/版本变化）、同键并发只执行一次、相同路径不增版本仍写收据、revision 冲突、未知/非法 ID、缺参数/键与鉴权无副作用。
  已登记新路径（含 disabled）、越界/不存在/非目录/符号链接应返回对应安全错误；独立连接持有扫描锁时拒绝且不写记录，释放后同键可重试。
  “停用 → 改路径 → 恢复”成功并保留新路径；复制同内容到新位置后改路径重扫，曲目/Release UUID 不变、missing 为零。
- 0035 迁移往返：含真实 `relocate` 成功记录的夹具执行升级 → 下行 → 再升级，确认下行删除新类型收据并恢复五类约束，根路径与 revision 保持改后值；不把结构回退当作目录改路径撤销。
- 后端单元：终态映射、同一过滤覆盖预计数与遍历、系统名大小写整段匹配且不检查注册根自身、用户排除子树、unsupported 候选识别、来源身份与 containment；CUE NFC/NFD/大小写唯一匹配和歧义停止回退。
- parser/organizer 单元：M4A/WAV 容器有效性、CUE 多 FILE 与跨目录 containment、
  missing reference/missing `INDEX 01` 非阻断跳过、`unsafe`/`unchecked` 引用保住 sheet、
  sheet/track performer provenance、逐轨 map 隔离、inferred track/disc fallback、
  明确标签优先、缺失 AlbumArtist 兼容与 AlbumArtist 决策优先级、艺人/credit 多分隔符、
  官方别名/固定组合名，rip log 64 KiB 预算、签名和仅覆盖 `folder` 的优先级，封面目录顺序（`ArtworkDirectories`：普通、CUE、
  CUE 跨目录、严格多碟含碟号 10、Box 叶子、根目录散曲、去重与按曲序）与内嵌封面选取（`EmbeddedArtwork`），
  文件夹封面发现（大小写变体、全小写优先、名字优先级不受大小写影响、多目录顺序、前一个目录无效时用后一个、目录不存在跳过、
  读目录失败报告、符号链接/无效/读取失败），以及各格式音频内容指纹（同音频不同标签
  相同、音频变化不同、FLAC MD5 为零的回退、MP3 首尾标签、OGG 注释包分页、读取中取消）。
- PostgreSQL 集成（配置 `ROOMUSIC_TEST_DATABASE_URL` 时）：用户禁用与 session
  原子性、最后管理员/并发保护、未知用户 404、目录幂等与真实状态、扫描 incomplete
  禁止 missing、文件/目录/CUE 保住与反抢 UUID、空根保护和排除命中例外、排除接口幂等与上限、多 Medium 与 Track 身份稳定；另需覆盖 staging 清理、候选短事务
  回滚/部分提交、跨目录 CUE 父来源对齐、regular-file 拒绝、身份规划（改名/移动/互换/相同音频、不完整与取消、换内容、跨 root、CUE、
  指纹复用与失败）与 Release 成员关系匹配、
  current evidence/track credits 替换、Track artist canonical 展示、受管封面内容重校验、
  旧 key 清理错误传播和绑定失败清理，封面发现（CUE 含重扫、严格多碟父目录、父目录与碟目录都有、回退到碟目录、
  Box 叶子不取父目录、内嵌优先）。
