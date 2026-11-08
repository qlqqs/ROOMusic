# 目录与扫描流程验证与示例

本文件承接[目录与扫描流程合同](./library-scan-contracts.md)第5–7节，修改扫描规则与验收时共同阅读。第7节的前五组示例与文末“② 文件夹名规则验证”“③ 文件名与同目录归组验证”从 library-scan-contracts.md 拆出。

## 5. 正确、基准与错误案例

- 基准：重复 POST 已停用目录返回 disabled；管理员随后使用 restore 端点显式恢复。
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
- 正确：已存在的 content-addressed 封面文件被破坏后，重扫按期望字节修复；旧 key
  清理失败返回 `artwork_failure`，不报告成功。
- 正确：CUE 专辑目录里的 `cover.png` 被绑定；严格多碟专辑父目录里的 `Cover.JPG` 被绑定；Box 叶子目录没有封面时，
  Box 父目录里的封面不会绑给它；封面后来才放进目录，下一次扫描即绑定。
- 错误：把 root 原始路径放入列表 JSON；unsupported 文件仍让扫描 succeeded 并执行 missing；把 FIFO 交给 parser；按标题删除 CUE 轨。

- 正确：`PERFORMER=测试演奏者 (violin)` 变成 performer / 测试演奏者 / violin，`WRITER=测试作者` 同时产出作词与作曲。
- 正确：`PERFORMER=A (violin); B (cello)` 先按明确分隔符切段，再逐段拆乐器和名字，得到 A / violin、B / cello；` / `、换行等同理。`Simon & Garfunkel (guitar)` 保留一个组合名，`Bach, Johann (piano)` 保留一个姓名；括号内 `cello, conductor` 仍按原规则解析。`甲 (violin、指挥)` 与 `甲（violin、指挥）` 都得到 conductor / 甲 和 performer / 甲 / violin，不能在第1步按顿号切坏。
- 基准：旧署名迁移后 detail=NULL、source=tag；同一演奏者多种乐器保留多条曲目署名，但艺人事实只保留一个同轨同角色姓名。
- 正确：歌词 `编曲/混音：测试作者` 同时产出 arranger 与 producer（detail=混音）；`吉他：测试演奏者@测试组合` 的名字只取测试演奏者。
- 错误：跳过歌词头部的未知“协力”行后继续读后续署名，或把 `测试艺人（测试别名）` 拆成乐器。

### 古典作品与乐章案例

- 正确：扫描流派 Classical，首曲完整作品名加 I.、后续 II./III. 时同组；中文全角冒号与“第N乐章”、阿拉伯序号同样处理。
- 基准：Pop 的 `Live: Intro / Live: Song A` 不拆；单轨 WORK 保存作品但无 index；无作品时重扫六列全 NULL。
- 错误：只见 TIT1 普通分组就保存作品、人工修正流派后重算，或相隔曲目的同名乐章被当作相邻重复。

## 6. 必需测试

- 后端单元：终态映射、unsupported 音频候选识别、来源身份规范化和路径 containment。
- parser/organizer 单元：M4A/WAV 容器有效性、CUE 多 FILE 与跨目录 containment、
  missing reference/missing `INDEX 01` 非阻断跳过、`unsafe`/`unchecked` 引用阻断、
  sheet/track performer provenance、逐轨 map 隔离、inferred track/disc fallback、
  明确标签优先、缺失 AlbumArtist 兼容与 AlbumArtist 决策优先级、艺人/credit 多分隔符、换行/NUL、拉丁姓名与团体词、
  专辑艺人分级多值、TXXX 编码、合辑真假及严格过半、真实专辑艺人优先、原始年份回退、
  官方别名/固定组合名，rip log 64 KiB 预算、签名和仅覆盖 `folder` 的优先级，封面目录顺序（`ArtworkDirectories`：普通、CUE、
  CUE 跨目录、严格多碟含碟号 10、Box 叶子、根目录散曲、去重与按曲序）与内嵌封面选取（`EmbeddedArtwork`），
  文件夹封面发现（大小写变体、全小写优先、名字优先级不受大小写影响、多目录顺序、前一个目录无效时用后一个、目录不存在跳过、
  读目录失败报告、符号链接/无效/读取失败），以及各格式音频内容指纹（同音频不同标签
  相同、音频变化不同、FLAC MD5 为零的回退、MP3 首尾标签、OGG 注释包分页、读取中取消）。
- 署名角色：映射键唯一、目标合法、固定角色/细分/名字排序与三元组去重；Vorbis/TXXX/M4A新键、ID3 TEXT、TIPL/IPLS/TMCL成对及落单项；
  演奏括号的乐器、角色、未知拉丁细分与中文别名整串保留；歌词前30行、LRC标签、空行、全角空白、双语/多子标签、词曲双产出、
  工种原写法、名字去@后缀、首个未知行截断和同角色标签优先；歌词内容不变。
- 署名集成：同曲同人两种乐器仅一条 performer 艺人事实，重扫替换 detail/source，只有 detail 变化也须记扫描更新（派生 position 不计）；0038 的 rollbackFixtures 覆盖新角色、细分重复与级联删除，
  升级→回退→再升级结构与数据往返；真实 handler 样本必须含 detail（发行级为null）和新角色，由测试 agent 在隔离库验证。
- PostgreSQL 集成（配置 `ROOMUSIC_TEST_DATABASE_URL` 时）：用户禁用与 session
  原子性、最后管理员/并发保护、未知用户 404、目录幂等与真实状态、扫描 incomplete
  禁止 missing、多 Medium 与 Track 身份稳定；另需覆盖 staging 清理、候选短事务
  回滚/部分提交、跨目录 CUE 父来源对齐、regular-file 拒绝、身份规划（改名/移动/互换/相同音频、不完整与取消、换内容、跨 root、CUE、
  指纹复用与失败）与 Release 成员关系匹配、
  current evidence/track credits 替换、Track artist canonical 展示、受管封面内容重校验、
  旧 key 清理错误传播和绑定失败清理，封面发现（CUE 含重扫、严格多碟父目录、父目录与碟目录都有、回退到碟目录、
  Box 叶子不取父目录、内嵌优先）。

### 古典作品与乐章验证

- classical：design §5 编号表、参考22.7.3段切分表、22.7.9样例与22.12第1–14条；参考 C′ 的非相邻重复分裂与任务相邻语义冲突，按任务设计，不加可配置白名单的测试专用接口。
- parser：Vorbis、ID3、M4A 的作品/乐章读取、各级空值与标签顺序、TIT1保护、n/m及16位整数；scanner验证物理/CUE观察传递，ingest覆盖标签优先、古典白名单、连续无标签段、乐章号回退断组、相邻重复与Promenade、稳定排序及单曲不分组。
- 隔离 PostgreSQL：0039往返（rollbackFixtures含作品轨与work_title值）、重扫替换与清空、无作品列的历史曲目、WORK与编号搜索传播、API样本的work对象/null。由测试agent补齐；未启动数据库时不得宣称集成通过。

## 7. 错误与正确示例

### 并发缓存的错误与正确示例

错误：worker 直接写缓存或暂存；CUE 的后续物理条目在早先父音频仍在途时直接查缓存，导致命中数取决于调度。

正确：worker 只返回解析结果，主 goroutine 按序应用；同路径在途依赖先 Flush 再查缓存，无依赖的文件继续使用有界窗口并行。

### 增量扫描的错误与正确示例

基准：空缓存的第一次扫描解析并写缓存与候选状态；第二次读取同路径同大小同微秒 mtime 同版本的文件时命中解析缓存，候选签名与当前归属也符合时仅 touch，报告零变化。只命中解析缓存而附属文件变化时仍须重写候选。

正确：标签编辑保留 mtime 时，管理员发送 `{"mode":"full"}` 强制重新解析；缓存照常更新，同来源的音频指纹仍可按旧规则复用。

错误：缓存 SQL 失败令 root 不完整；把无封面字节的缓存观察当成从来没有内嵌封面；full 模式借用改名来源指纹。

正确：缓存失败 WARN 后解析，MIME 标记传递为 `ArtworkDeferred` 并在保存封面时现读，只有完整增量遍历才借用指纹。

### 改路径与恢复的错误与正确示例

错误：查到 `scan_runs` 没有 running 就直接更新路径；恢复查询限定停用记录的 `result_revision` 必须等于当前 revision；把前后态直接当作响应。

正确：先幂等回放检查，再持同键扫描锁修改路径并写安全收据；恢复按最近成功 disable 验证 active 前态，不限定版本紧邻。

### CUE 诊断错误与正确示例

错误：把所有 CUE diagnostic 都提升为 root incomplete；这会让缺失的历史 sidecar
引用阻断同目录中已经确定的物理音频，也无法完成真实库重扫。

```go
for range document.Diagnostics {
	complete = false
}
```

正确：诊断始终保留；unsafe/unchecked 引用保住本 sheet，根仍完整；missing 是确定不存在，跳过对应虚拟 Track，成功后允许正常 missing 对账。

```go
if cueReferenceNeedsHold(reference.Status) {
    // 整张 sheet 只登记、计跳过一次；失败时不写该 sheet 的虚拟轨。
    return hold(ingest.HeldCueSheet, sheetRelativePath)
}
if reference.Status != "present" {
    continue
}
```

### 抓轨证据优先级错误与正确示例

错误：只看最终字符串是否为空，会把先到达的目录补充值误当作权威值。

```go
if candidate.SourceType != "" {
	return
}
candidate.SourceType = "CD"
```

正确：检查选定 decision 的 provenance；仅替换 `folder`，tag/CUE 等其它来源不变。

```go
if decision.Source == "folder" {
	candidate.SourceType = "CD"
	replaceDecision("source_type", ripLogDecision)
}
```

停用目录拒绝新的播放请求，已建立传输不因此切断，专辑仍可浏览；具体语义见[播放合同](./playback-contracts.md)。

### 署名细分错误与正确示例

错误：用 `role+name` 去重曲目署名会丢乐器；直接把每个细分写成艺人事实会让一次 INSERT 的 ON CONFLICT 重复命中。

正确：曲目署名按 `role+name+detail` 保留，进入艺人图时按 `track+role+name` 去重；歌词署名按整个角色服从标签来源，不只去掉同名项。

署名拆分回归须同时覆盖括号外多人、括号内多角色、固定组合与“姓, 名”：同一分隔符在姓名列表与乐器列表中属于不同层级，不能把全串切分或全串套括号正则当成等价操作。

### 古典作品错误与正确示例

错误：在 organizer 内据有效流派切标题，或给每个带 WORK 的单轨都返回 REST work 对象。

正确：写入入口基于扫描候选调用 `AssignWorks`，单轨标签仍可入搜索；读取仅在 `work_index IS NOT NULL` 时构造 `ReleaseTrackWork`。标题推断仅处理连续无 WORK 段，不能跨标签轨串组。

## ② 文件夹名规则验证

- 解析单元：覆盖数字、英文数词、规范罗马数字、括号与总碟数、首个无效标记后继续查找；纯数字、中文碟号、介质标签与唱片编号不得误认。专辑名从非开头标记处截断，剥空不改，观察原文不丢失。
- 归组单元：独占/不独占、父为注册根、单碟归父、Prefix 冲突、标签冲突、无标签连续多碟、缺号仅全目录有标签可合、附加碟及重复编号；断言候选种类/锚点、碟号/碟名、标题兜底及非合成候选的标签→CUE→专辑名→目录优先级。
- 封面单元：原目录精确名单必须优先于原目录 front 和附属子目录；附属目录大小写、名字字节序、拒绝符号链接；坏图后仍可用其它目录的有效封面，全部失败才报第一个错误。
- 隔离 PostgreSQL：CUE 对齐后独占标记看到最终 scope，父目录直接音频、旁边专辑和碟目录内更深层音频均阻止独占；保存/去掉碟名后介质标题恢复 `Medium`。
- 收藏集成：两个旧叶子合成新发行后曲目 UUID 保持，旧组腾空才转移；其它版本仍有曲目（含 missing）时不转；按接收发行计数，平局按创建时间/ID。覆盖目标已收藏、多来源合流、保留时间/保存名称、仅受影响用户 revision 加一、重复扫描不再转移、回调失败仅 Warn 且事务回滚。
- 正确：独占 `A/CD1` 锚在 `A`，旁边另有专辑时锚回碟目录；`CD1/CD2/CD1 (Live)` 的介质为 1、2、3，第三碟名为 `Live`。
- 错误：未对齐 CUE 就算独占，或按旧 Release 而非旧组是否腾空来转收藏；正确：最终 scope 判断独占、整组无任何曲目才转移。

以上是必需断言，不表示已执行；本项实现会话不新增测试，数据库验证由专门测试会话执行。

## ③ 文件名与同目录归组验证

- 单元断言：五级编号、零值、三/四位目录多数与降级、年份保护、标题清洗及艺人拆分；标签/CUE不变；自然序数字段、前导零与长数字；同介质文件名冲突整批补位及碟号优先级。
- 归组断言：连续、缺轨、重轨、无第 1 碟、允许缺碟、单曲；仅在原本会拆时连续性合并；两格式各连续且重复才切、单曲双格式可切、不完整子集不切，锚点与 `VersionGroup` 稳定。
- 隔离 PostgreSQL 断言：首扫双格式同组、旧混合发行一个沿用一个新建同组、重复扫描稳定；两个已有版本被人工分组后扫描不搬组、不改默认版本或组 revision；文件名 inferred 艺人进入图，普通占位艺人不进入。数据库验证由测试会话执行，本实现会话不启动数据库。
- 正确：无标签合成文件名 `01 - 示例艺人 - 示例曲.flac` 得轨 1、艺人与干净标题；同目录 flac 与 mp3 各自 1..2 得两个同组版本；单一格式原有正确归组保持不变。
- 错误：把 `2046` 拆为碟轨、用自然序补出的 1..n 证明不同专辑能合并、把沿用发行移进新选组；正确：四位先验首位与目录多数、连续性只用标签/文件名证据、只为新建发行选组。
