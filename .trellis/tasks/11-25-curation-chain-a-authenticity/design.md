# 整理链 A 设计

审阅稿。对应父任务 R1、R2、R17 与 V3。分两次交付。第一段让本地重算、发行识别和各资料主题可以独立失效；第二段才启用新的取值函数和四类发行。分类在后台整理关闭、发行从未整理时也可查询。

## 1. 现有版本的使用面

`backend/internal/curation/program/worker.go` 定义 `RuleVersion = 1`、`StoredRuleVersion = RuleVersion*100 + textnorm.Version = 101`。下表为生产代码中全部直接引用及其下游用途。除标明目录的文件外，本节的程序文件均位于 `backend/internal/curation/program/`。

| 使用处 | 现在的作用 | 拆分后 |
| --- | --- | --- |
| `worker.go:250`、`enrich.go:300`、`track_fields.go:159` | 轮末推进、匹配写入后发现、旧匹配回填时发现同一发行的并入项，把版本传入 `release_merge_discovery.go` | 并入项保留原版本和双方证据合同，单独发现，不随外部匹配版本失效 |
| `pass.go:159` | 所有主题共用的 `needsWork` 首先比较版本和摘要，然后检查到期及声纹 | 按第 3 节判断 |
| `pass.go:208` | 给缺少操作后态的旧封面对应，从同版本匹配结果取组号 | 核对已匹配结果中的发行 ID 与现有对应相同，再使用组号；换版本不能使组号消失 |
| `pass.go:298,377` | 保存执行版本；媒体写后更新同版本发行的整份摘要。第 492 行还把状态版本转存到匹配待定项 | 写主题自己的状态；媒体只更新缺口及自身摘要 |
| `unit.go:33` | 所有程序写入的 UUIDv5 名称含主题、101、代次、步骤，命名空间为目标 ID | 见下文幂等键 |
| `model_matcher.go:280` | 模型匹配成功也保存相同版本的发行工作状态 | 保存当前外部匹配版本与写后摘要 |
| `artist_media_pass.go:288,340` | 艺人资料工作状态与艺人身份待定项也写 101 | 保留这两个主题的输入和版本语义 |
| `backend/internal/app/app.go:399` | 注入 `curation/httpapi/handlers.go`，`curation/progress.go` 返回该数，SQL 只计同版本发行 | 原 `progress.rule_version` 继续表示当前外部匹配版本。已沿用的匹配仍计入进度 |

间接使用还包括 `curation/work_states.go` 的批读、保存、待定项 upsert，`pending_items.go` 的公共列与认领、锁定，以及 `track_fields.go:234` 更新回填标记时用版本、摘要、代次和时间做并发检查。整份 `curation.ReleaseDigest` 还用于并入双方的摘要、模型预览、应用、已采纳提案的恢复；这些调用需要各自的写入校验。

测试中的直接引用位于 `backend/internal/curation/program/{b3_work_test,pending_refresh_integration_test,pending_shelved_test}.go`，以及 `backend/internal/app/` 下的 `curation_release_merge_discovery_integration_test.go`、`curation_program_http_integration_test.go`、`curation_program_fixture_integration_test.go`、`curation_b3_program_integration_test.go`、`curation_track_program_integration_test.go`、`curation_program_worker_integration_test.go`、`artist_web_search_integration_test.go`、`curation_model_matcher_integration_test.go`。兼容期保留旧常量名，值固定为 101，供旧夹具及旧键使用；新执行路径显式选择版本。

### 版本与存储

| 版本 | 第一段初值 | 记录位置和升级语义 |
| --- | --- | --- |
| `LocalRuleVersion` | 1，第二段升为 2 | 发行最近一次本地投影使用的版本。可见的未处理发行直接计算。它控制真实性与分类 |
| `MatchRuleVersion` | 101，沿用现行外部匹配语义 | 发行结论和发行／录音匹配待定项各记录实际使用的版本。今后手动递增，修改影响比较的 `textnorm` 规则时随外部规则显式升级 |
| `PatrolPromptVersion` | 1 | A 提供独立版本值。D 写巡检产物时随产物记录实际版本；历史发行没有巡检记录。提示词变化只作用于新巡检及用户选的“重新巡检”，不会使任何工作状态到期 |

程序层分别声明三个版本。封面、歌词、艺人资料、艺人身份和并入继续使用既有主题版本 101。分类接口可展示三个版本，现行进度接口的 rule_version 表示外部匹配版本。

迁移 `0086_curation_rule_versions.sql` 为工作状态增加可空的 local_rule_version、match_rule_version、identity_digest、gap_digest、requery_match_version，仅发行主题填写。为发行与录音匹配待定项增加 match_rule_version、identity_digest。保留原列和数据，down 只移除新增列。工作状态和待定项的 input_digest 继续使用现有整份发行指纹。模型预览、采纳、确认恢复、并入证据继续用同一函数核对。程序写入键沿用“目标／主题／单元实际规则版本／代次／步骤”，第一段匹配版本为 101。

## 2. 哪些变化需要重新识别

识别摘要直接取原始输入。目录取证把匹配消费的来源字符串、观察 inferred 位和人工修正存在位交给该投影。新增取证字段用 Go 的 `json:"-"` 与现有整份指纹隔离。分类、真实性解释、搜索条件、提示词、规则版本、缺口均不参与识别摘要。

| 输入 | 用途 |
| --- | --- |
| 候选种类、发行标题、实际选用的专辑艺人或发行艺人 | 确定模式、文字搜索与比名。保留采用值的 selected_source 和人工修正事实。单曲按录音使用曲名和艺人 |
| 条码、转换 MCN 前的目录号、厂牌、媒体类型及各自原始来源 | 条码与目录查询、厂牌佐证和 CD 介质核对。保留转换前输入，避免解释规则改变摘要 |
| 各介质位置、每首在库曲的 ID、碟位、曲序、曲序来源与 inferred 位、取整秒时长 | 确定曲目数、抽样顺序、录音配对和结构佐证 |
| 单曲的标题、艺人、source_kind、观察 inferred 位与修正存在位 | 录音搜索和核对。专辑逐曲文字供补全与模型取证，当前外部识别按曲序、编号和时长配对 |
| 标签 mb_release、mb_release_group、mb_recording、mb_track、mb_artist、mb_album_artist、mb_disc、isrc，及 CUE ISRC | 编号查询与冲突检查。mb_work、acoustid 的“有无其它标识”在第二段分类投影中补充 |
| CUE 的来源类型、根、相对镜像与表路径、起止帧 | 原始光盘目录重建材料。非 CUE 音轨无需这些路径 |
| 发行、录音现有对应和相应 external_link 抑制标记 | 冲突校验，撤销后重新判定。按事务写后的实际对应保存识别摘要 |
| 本地归组及其操作来源、外部组号、年份、流派、版次、自动层有效文本、封面与歌词 | 这些输入用于写入校验、归组、补全或资料主题，当前均不参与发行识别 |

缺口摘要含发行标题和艺人的缺口、专辑艺人／年份／厂牌／目录号／条码是否可补、可写字段的修正与自动占用，以及字段抑制；逐曲含 ID、标题与艺人缺口、修正与自动占用、字段抑制、本地／外部歌词存在位；封面含本地行、外部行与受管文件存在位。第一段按当前占位与推断标记计算，第二段切换到统一真实性。媒体主题继续检查各自的参数、抑制和来源变化。

## 3. 首次升级、重复运行与离线重判

| 条件 | 动作 |
| --- | --- |
| 无工作状态或识别摘要发生实际变化 | 走现有普通识别，保存结论时重新取证，用整份指纹做写入校验 |
| 已有状态，identity_digest 为 NULL | 只比较现有整份发行指纹与旧 input_digest。相同则在数据库内补入版本与新摘要作为基线，保留结论、计数、代次；不同则按普通变化处理 |
| 只有本地版本、缺口或整份指纹变化，识别摘要相同 | 只更新本地投影与整份指纹。旧行首次建立基线的当轮仍按 result 标记维护已匹配的曲目字段、艺人对应和发行并入。回填完成时与新基线一同提交，暂时失败则保留重试状态。新的占位解释影响该发行下一次因独立原因发生的识别或 C 的外部规则升级；不清除回填完成标记，不调用 finder |
| 只有外部版本变化，现有结论 matched | 保留对应和结论，登记新的匹配版本 |
| 只有外部版本变化，尚待判断或查无结果 | 读取相应 release_match／recording_match 待定项，按下述离线入口处理。在数据库内保存该版本的结果 |
| try_later 到期；声纹从不可用变为可用 | 继续现有退避、重试与重开条件。普通查询资格不能越过尚未到期的退避 |
| 只有巡检提示词升级 | 保留已有巡检成果，D 提供手动重巡检 |

离线重判输入为当前目录证据和已保存候选、原结论与冲突。发行候选有方法、外部编号、标题、艺人、曲数与发行组编号等，历史候选缺总命中数、逐曲时长和完整结构。第一段提供无网络依赖的入口，沿用现行约束。原候选均受抑制或与已有对应冲突时保留原结论和待定状态，由 C 定义后续终止规则；需要新材料的目标登记 requery_match_version，留给普通遍历。在下次普通查询尝试后按其结果保存，来源禁用时仍保留资格。C 在此入口接入新的离线判定规则。普通 finder 在缓存未命中时会出网，离线入口不调用它。

离线维护不抢占 in_progress 或等待人工的项。只补新列不会重开 shelved；正常重判时，按识别输入或候选的实际变化决定是否重开。旧待定项有同一整份指纹时，可随对应状态建立新基线。工作状态与待定项在一个事务内更新。其它 kind、封面、歌词、艺人资料各自沿用现有摘要、退避和状态机。

## 4. 字段真实性及其消费者

取值的事实来源是 `release_field_decisions.selected_source` 和 `track_observations.source_kind/inferred`，人工修正以修正行的模式和实际取值为准。建议在 `backend/internal/catalog/metadata/curation_authenticity.go` 放纯函数，供 catalog 证据投影和需要过滤整理署名的 catalog 内部代码调用；在 `catalog/curation_evidence.go` 完成层选择、批量查来源和装配。一次返回同一选中值的值与 `authenticity`（`real/inferred/placeholder`）。输入为字段语义、选中值、来源记录（区分缺行／空来源）、观察的 inferred 位以及是否存在人工设置／清空。空值另有存在性语义。显式设空视为缺值、禁止补写；非空人工设值为真实。专辑艺人为空时选择实际发行署名及该署名自己的来源。人工层、扫描层和仅供歌词／分类使用的有效层均显式区分。

| 实际写出的来源 | 类型与适用字段 |
| --- | --- |
| `tag` | organizer 的标签决定；非空标签值为真实。冲突／多数决定另保留已有置信度 |
| `flac_vorbis_comment`、`mp3_id3v2`、`m4a`、`ogg_vorbis_comment`、`opus_tags`、`wav_info`、`aiff_id3`、`ape_apev2`、`wavpack_apev2`、`dsf_id3`、`dff_diin` | parser 的物理观察来源；ingest 没有逐字段覆盖时透传。有值且非推断为真实 |
| `cue_sheet`、`cue_track`、`cue_virtual` | CUE 文档或曲目明确给值、虚拟轨继承的非推断标签为真实；字段有 fallback 来源时优先按字段取。CUE 的 MCN、ISRC 继续按原语义读取 |
| `rip_log` | 抓轨日志实证，organizer 给 `source_type/media_type` 写的 CD 为真实 |
| `filename_fallback`、`folder_fallback`、`folder` | 推断。分别是文件名拆出的曲序／碟号／标题／艺人、目录名兜底、结构化目录名解析出的专辑名、专辑艺人、年份、目录号等。后者有时观察 inferred 位为假，来源仍是目录 |
| `album_title`、`folder_structure`、`path_order_fallback` | 推断，分别为从标题推出碟号、从目录结构推出碟号、按路径顺序分配曲号 |
| `compilation` | organizer 生成的“群星”是合辑展示署名，对艺人名取占位，同时保留合辑事实。明确的逐曲署名仍逐曲判断；C 决定如何按 Various Artists 身份查询 |
| `display_fallback`、`default_fallback` | 占位，分别含“未知艺术家”和默认曲号／碟号 1 等常量。位置用于结构时仍须保留“未经标签确认”位 |
| `''` 或无来源行 | `field_missing_v1` 能实际写出空来源和空选值，随后发行显示“未知专辑”。空值、缺来源且命中相应字段的“未知专辑”“未知艺术家”为占位；其它非空历史值沿用原有读法，有推断事实则为推断。明确标签或人工真名恰好同字时保留真实 |

这里的来源指上述两列；`tracks.source_kind` 标记物理／虚拟轨，`lyrics_source`、作品或人员贡献的 `source` 属于各自的数据合同。模型整理的值在自动层记录 `origin=model`，向分类和受限查询呈现为推断；保留“模型整理”的依据。对已存在的曲目自动补全，读取有效标题／艺人与来源供歌词使用，不能把自动值回灌为匹配自身的标签证据。将来 D 的规范名只替换查询文字，原输入的真实性仍随项传递。

- 发行与在库曲目按字段提供来源：发行标题、艺人、专辑艺人、年份、厂牌、目录号、条码、媒体类型、版次、流派；曲目标题、艺人、曲／碟号及已观察的其它字段使用各自的来源。已读到的有效标签编号为真实；读出的音频时长为测量事实，未知时长保持缺失。发行头查询目前只取四个字段的来源，要扩至实际投影。曲目查询返回原始来源、修正行和自动行存在性，再在 Go 侧一次推导。标题／艺人 Placeholder 为对应类别等于 placeholder；`TitleInferred` 仅为 inferred，补标题用两者并集。结构校验中的 `PositionInferred` 同时涵盖推断和占位位置。现有数字 1 不能单凭数值判断。
- 外部查询在构造请求前按字段提取可用文本。MB 专辑和单曲的必填标题、艺人有一项被排除就跳过该文字步骤；可选厂牌／歌词专辑名／封面插件标题和署名仅传可用项。LRCLIB 精确与搜索共同使用有效标题和有效艺人，只要必填值为占位就不调用。依赖艺人名的资料请求与模型上下文沿用有来源的署名投影，防止旧艺人图中生成的“未知艺术家”重新进入名称查询。编号查询保留原有顺序。查询证据保留实际使用字段的类别，导出 `uses_inferred`；含推断或模型整理值时 C 只能接受曲目数相等且时长吻合的结构判定，不能按发行组唯一认定。A 期间文字搜索仍只产生候选。
- `PendingEvidence` 保留原有的标题、署名、标识、每碟曲数和轨道数组，在发行与每条轨旁附加按字段名的 `authenticity`，例如曲目含 `"authenticity":{"title":"inferred","artist":"placeholder","position":"real"}`；组合条码按各值保留来源。模型可以看见占位及其原因，构造外部查询时仍须过滤。旧项若与当前本地证据匹配，从同快照补出标记。刷新旧项时，能按现有整份指纹复现原签名才关联当前来源；过期输入走该项既有的 stale／重新取证路径。艺人身份项引用的发行／曲名样本、并入预览展示的曲名也随值标明其来源，保留原字段形状，模型工具直接消费。目录路径、CUE 路径、原始文件名不随这些标记发给模型。

## 5. 四类发行与查询

分类是 `catalog` 在同一份证据上计算的纯函数。只看可见发行及 present 轨。专辑使用发行标题和实际采用的专辑署名；`loose_unknown` 的唯一在库曲是散放单曲，使用曲名和曲目艺人判断身份。异常的多曲 `loose_unknown` 保留现有结构冲突处理，不能凭任意一曲建立录音对应。四类按下列顺序取首个命中，使“信息不足”优先于普通缺口。

| 顺序与返回键 | 精确条件 |
| --- | --- |
| 1 带外部编号 `tagged_id` | 专辑至少一首在库曲有标签 `mb_release`，散放单曲有标签 `mb_recording`。冲突标签仍归此类，冲突由匹配程序处理。数据库里已有对应或只有发行组标签均不能充当此处的标签 |
| 2 信息不足 `insufficient` | 标题和艺人都为占位（包括无值），并且没有可用条码／MCN、目录号、曲目 ISRC／CUE ISRC、任何已记录的外部编号。后者涵盖全部轨标识种类和当前发行、组、轨的外部对应；可重建 CUE 目录也能产生可查的 disc ID。声纹服务是否开启不影响分类，只影响这类发行可否进入外部识别；文件／目录仍有可整理线索时供 D 本地整理 |
| 3 有缺口 `has_gaps` | 以上均未命中，以下至少一项成立：标题非真实，艺人非真实，任一在库曲标题非真实，年份无值，没有本地封面 |
| 4 本地完整 `local_complete` | 其余发行，即标题、艺人、全部在库曲标题真实，年份有值且存在本地封面。外部歌词、厂牌、目录号、逐曲艺人缺失本身不改变此分类 |

缺口排序键取 `(标题和艺人中非真实的个数, 非真实曲名数, 缺本地封面位, 缺年份位)`，逐项降序，任一高位差异优先于所有低位。数量只作同档细分；两张完全同键的发行沿用加入时间、发行 ID 倒序。本地完整一类另向 B 提供“主署名艺人尚缺外部对应”位（按有效本地艺人身份批量核对 MB 对应），为真的优先。A 只提供分类、排序键和计数；调度顺序仍由后续 B 接入。

拟增管理员只读接口 `GET /api/v1/admin/curation/classifications`，与 `/progress` 共用 `h.admin`、`h.respond` 和可见性口径。示例为同一只读快照的计数：

```json
{
  "versions": {"local_rule": 2, "external_match_rule": 101, "patrol_prompt": 1},
  "releases": {
    "total": 10,
    "tagged_id": 2,
    "has_gaps": 4,
    "local_complete": 3,
    "insufficient": 1
  }
}
```

所有计数为非负整数，空库返回五个零，四类之和等于 total。A 不建立需等待后台首轮才能读取的分类表。catalog 用可取消的只读一致快照，按现有 `PageCurationReleases` 键集分页，成页读取分类所需字段和来源，复用同一个分类函数逐页累加；计数不需读取文件、歌词正文、抑制详情、艺人优先位或调用任何外部能力。选择独立 GET，使显示整理状态的既有高频 `/progress` 轮询不承担全库分类成本。B 在扫描结束维护可排序的物化投影时，可复用分类函数及本接口合同。

## 6. 行为变化、实施边界与验证交接

| 交付／触发 | 可观察变化 |
| --- | --- |
| 第一段，版本拆分，外部匹配仍为 101 | 封面、歌词、年份等缺口独变不会再触发 MB 重新识别；真实输入变化仍可触发。旧待定项、媒体退避、旧写入键可继续使用。单独升级外部规则才触发离线候选重判，单独升级提示词不会重做 |
| 第二段，`LocalRuleVersion 1 → 2` | `display_fallback` 纳入占位。M 中 poor-05 的 3 首“未知艺术家”和 poor-06 的 3 首分别成为可补曲目艺人；沿用按正确发行编号取得的逐曲配对，正常匹配一次即可用其署名补为“雾谷二重奏”和“雪杉组合”，不额外查录音。已匹配的旧库在纯本地升级时立即能看见真实缺口；历史 result 一般仅有 ID 和完成标记，若无已保存且经配对核实的署名，留到正常的有效匹配／回填机会再补，不能因 `track_fields=done` 清标后再联网一次 |
| 同一本地升级 | 文件／目录解析出的文字标推断，模型可辨认；合辑展示署名与默认值退出文字搜索，明确的同名真实标签保留。歌词使用有效层，已有人工或合法自动补全的艺人仍可查歌词。分类计数随证据重读生效 |
| 后续子任务 C、D、B | C 首次改变接受候选的条件时单独升级 `MatchRuleVersion`，负责 V3 的曲数相等、时长通过与截断判断；D 负责规范名、巡检产物和手动重巡检；B 消费排序键并更换调度。M 的错误编号错连、同名／截断错连和错字修正不作为 A 的修复成果 |

### 第一段：可独立合入

- `backend/migrations/0086_curation_rule_versions.sql` 及对应 down 文件；`backend/internal/curation/work_states.go`、`pending_items.go`、`progress.go`、`release_merge_evidence.go`、`release_merge_discovery.go`：扩展列映射、兼容读写、局部版本推进与事务比较。
- `backend/internal/catalog/curation_evidence.go` 的原始来源投影，`backend/internal/curation/program/{worker,digest,pass,unit,track_fields,enrich,artist_media_pass,model_matcher,match}.go`：分离识别／缺口／写入校验和主题版本，适配各执行入口、回填、确认恢复和幂等键。纯候选重判可放在同包 `pending_rejudge.go`。模型 `release_match/recording_match` upsert 单独按新识别摘要和实际候选判断是否重开 shelved，完整提案签名的媒体位变化只使原预览需复核。在 `backend/internal/curation/httpapi/handlers.go` 和 `backend/internal/app/app.go` 接入进度所需版本。
- 合入标准是单独部署后仍使用旧来源解释与旧匹配规则，旧库升级不集体重查。用旧 101 夹具覆盖 release、artwork、lyrics、artist_media 和不同状态的待定项；比较首次与第二次遍历的来源计数、进度、退避、代次与已开始操作的回放，再独立修改识别字段和缺口字段测分支。

### 第二段：真实性与分类

- `backend/internal/catalog/metadata/curation_authenticity.go`、`catalog/curation_evidence.go`、拟增 `catalog/curation_classification.go`（此处及以下 catalog、curation 路径均相对 `backend/internal/`）：来源与层、标志派生、缺口排序和批量只读投影。核对 `catalog/internal/artistgraph/facts.go` 对展示占位生成署名的路径，整理端对已有库也按来源过滤，无需先重建艺人图。scanner、parser 和 ingest 的现有落库与文件名解析作为测试输入。
- `curation/program/{match,match_lookup,enrich_media,track_fields,artist_media_pass,artist_media,model_matcher}.go`、`curation/release_merge_evidence.go`、`curation/stewardtools/tools.go`：构造带来源的模型证据和可查询字段，复用现有补全及写入保护。`curation/progress.go`、`curation/httpapi/handlers.go` 与 app 装配接分类计数接口。同步提升本地版本。
- 测试代理先用合成文件通过真实 scanner → ingest → catalog 路径固定 poor-05、poor-06 的失败，再验标签真名、路径推断、占位过滤、人工设空、模型证据、四类互斥、排序键和计数可见性。用 M 两种模型模式对照相同的 92 个样本，检查这 6 首艺人、错连／错并／错改和程序可定样本的请求增量。基线 oracle 为 78/92、12/0/2；first_candidate 为 59/92、97/0/4。这些是改动前的结果。
- 现有断言重点交接：`TestSubjectDigests` 的“每字段都改变发行摘要”与 `TestB3DigestBaseline` 的发行常量继续约束整份指纹；测试代理另覆盖识别投影；`TestPostgreSQLCurationProgramRescanAndChanges` 当前要求已匹配项降旧版本后多查一次并增加 attempts；`TestPostgreSQLCurationShelvedReopensOnNewCandidates` 的 rejudge 夹具直接改旧版本后期待重新搜索。相应断言应改为新列、离线判定及真正改变候选后的行为。`TestPostgreSQLCurationModelPlanAfterClaimRefresh` 的歌词变化只更新缺口与完整指纹，原会话仍可重新预览，识别代次不增加。复核 `TestPostgreSQLCurationEvidenceProjection`、`TestB3FingerprintNeedsWork`、`TestPostgreSQLCurationPendingUpsert`、`TestPostgreSQLCurationCProgress`、`TestPostgreSQLCurationTrackFieldsBackfillOnce`、`TestPostgreSQLCurationLyricsUsesEffectiveArtist` 和旧模型确认恢复用例，保留仍适用的断言。
- 实施后的最小验证在 `backend/` 用 `mise exec -- go test ./internal/curation/... ./internal/catalog/...`；隔离集成用 `ROOMUSIC_TEST_RUN` 收窄目标并执行 `./scripts/test-integration.sh`，度量单独选 `TestPostgreSQLCurationMetrics`。第一段验证后单独审阅，第二段及 M 度量在后续交付。
