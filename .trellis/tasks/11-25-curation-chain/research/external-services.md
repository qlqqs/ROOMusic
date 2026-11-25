# 外部服务：限流规则、接口能力与 Roon 的做法（2026-11-25）

## 1. MusicBrainz 限流

来源：<https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting>、<https://musicbrainz.org/doc/MusicBrainz_API>。

- 每个来源 IP 平均每秒 1 个请求，超过后该 IP 的全部请求返回 503，直到速率降下来。搜索与按编号读取同样计数。
- 限的是 IP。同一网络里的其它 MusicBrainz 客户端共用这个额度。
- 全站上限平均每秒 300 个；匿名 User-Agent 另有限制。
- Cover Art Archive 文档写明“目前没有限流规则”，图片由 archive.org 提供：<https://musicbrainz.org/doc/Cover_Art_Archive/API>。
- AcoustID 每秒不超过 3 个请求：<https://acoustid.org/webservice>。
- 自建镜像：官方推荐 Docker 方式，完整数据库需要 60 GB 以上磁盘：<https://musicbrainz.org/doc/MusicBrainz_Server/Setup>。

## 2. MusicBrainz 接口能力

- 发行读取的 `inc` 可组合：`recordings`、`artist-credits`、`labels`、`release-groups`、`media`、`discids`、`isrcs`。
- `/discid/<id>` 的 `inc` 与发行读取相同，可以一次带回曲目；带 `toc` 时支持模糊查询。
- `/isrc/<isrc>` 的 `inc` 与录音读取相同。
- 浏览请求每页最多 100 个；发行浏览另有“整页不超过 500 首曲目”的限制。
- 搜索每页最多 100 个。发行搜索结果带标题、艺人署名、发行组、日期、国家、条码、厂牌与目录号、总曲目数、各介质的曲目数；**不带曲目时长**。
- 搜索用 Lucene 语法。用 OR 合并多个字段的写法，文档里没有例子，实现时要实测。

## 3. 实测（2026-11-25，公开艺人名，二十多个请求，通用 User-Agent）

| 请求 | 结果 |
| --- | --- |
| 搜艺人 `artist:"周杰伦"` | 1 个请求，唯一命中（MB 名称为“周杰倫”） |
| 搜艺人 `artist:"Jay Chou"` | 0 个结果 |
| 搜艺人 `alias:"Jay Chou"` 或不带字段的 `"Jay Chou"` | 唯一命中周杰倫 |
| 不带引号搜错字“周杰论” | 第一条是周杰倫，但共返回 568 条，分数不可靠 |
| 取该艺人的专辑与 EP 发行组 | 1 个请求，返回全部 39 个 |
| 搜发行 `release:"范特西" AND artist:"周杰伦"` | 1 个请求，15 个版本，全部属于同一个发行组，曲目数为 10、16、23 等 |
| 搜发行错字“范特系” | 0 个结果 |
| 多产艺人的专辑与 EP 发行组数 | Miles Davis 688，Johann Sebastian Bach 6186，Various Artists 约 27.6 万 |

- 按 1.1 到 1.5 秒的间隔发请求，仍有三到四成返回 503（响应头 `Retry-After: 1`）。这次用的是通用 User-Agent，MusicBrainz 对通用标识另有限制，所以这个比例不能当作程序的实际速度；程序有自己的标识（`backend/internal/sources/request.go:205`）。实际成功速度由子任务 M 用程序的标识重测。

由此得出的请求数：

- 只认到发行组一级：按专辑查每张 1 个请求（搜索结果已带发行组与艺人编号）。
- 按艺人查每个艺人 2 个请求（搜索加一页作品列表），只有本地专辑 3 张以上的艺人才省；多产艺人与合辑不可用。
- 要曲目级信息（时长、曲名）时每张多 1 个读取请求。

## 4. Roon 的做法

Roon 官方没有公开算法，以下来自官方帮助页、员工的论坛回复与第三方工具厂商的说明。

- 先在本地按目录结构与标签把文件聚成专辑，再识别。用于聚类与识别的标签：专辑、艺人、专辑艺人、曲名、曲序、碟号。
- 识别时每张专辑向 Roon 自己的在线库查询。最重要的信号是曲目位置加时长（TOC），其次是专辑名、专辑艺人、曲名；已有的 MusicBrainz 编号通常直接采信。
- 时长按秒比较，论坛版主提到的容差是正负 4 秒。曲目少的单曲与 EP 更保守。有矛盾（例如碟数不符）就不自动识别。
- 达不到置信门槛的专辑保持未识别，显示文件标签；用户可以用“识别专辑”向导手动选。
- 按字段选择优先 Roon 还是优先文件，优先的一方为空时用另一方。
- 数据来自 TiVo（AllMusic）与 MusicBrainz 合并的自有库，没有模型参与。

来源：

- <https://help.roonlabs.com/portal/en/kb/articles/file-tag-best-practice>
- <https://help.roonlabs.com/portal/en/kb/articles/import-settings>
- <https://help.roonlabs.com/portal/en/kb/articles/faq-why-didn-t-roon-find-any-metadata-for-my-album>
- <https://community.roonlabs.com/t/album-identification-inhibited-by-missing-track-duration/52336>
- <https://community.roonlabs.com/t/identifying-albums-with-perfect-metadata-need-improvements/196663>
- <https://www.blisshq.com/music-library-management-blog/2022/05/25/ultimate-guide-importing-library-roon/>
- <https://community.jthink.net/t/why-roon-can-t-identify-some-albums-and-how-to-fix-it/13995>

对本任务的启示：用曲目结构做判定；未识别是正常的最终状态；Roon 能每张都查是因为查的是自己的库，我们受 MusicBrainz 限流约束，不能照搬“每张都立即查”。

## 5. 用户提供的设计稿

《音乐库识别主链与元数据取舍 · 逻辑链设计稿》讲本地扫描一段（入集、类型、身份、字段来源层、取舍算子、专辑归属、专辑级定案、写回），明确不涉及外部匹配与模型。

采纳的原则：

| 原则 | 用在哪里 |
| --- | --- |
| 推断值与占位值必须带持久化标记，且全链可识别 | 本地分类、外部搜索的输入、交给模型的证据 |
| 结构证据优先于字符串证据 | 外部匹配用曲目数与时长判定；模型的选择由后端做结构复核 |
| 归属与字段值是两条轴 | 外部对应只回答“是哪张专辑、归哪组”，字段另按缺口补 |
| 分层覆盖，不合并，不投票 | 自动层内部固定先后 |
| 每一环写明输入、输出、失败去向 | 设计文档的写法 |
| 记录由哪一级规则定案，版本常量控制重算 | 规则版本拆分 |
| 回归数据集 | 度量子任务 |
| 保守失败优先于激进错并 | 高风险修改必须有结构或外部佐证 |

不采纳的：

- “文件是唯一事实源，用户改的值会被重扫覆盖”：本项目有人工层，重扫不覆盖。
- “外部标识不做检验”：本项目有模型参与，写入前必须核对。
- “系统没有立场判断标签错了”：模型纠错是本项目的目标；保留它的另一半，即纠正写在自动层、不改文件、可撤销。
- 同目录归组用曲号连续性优先于专辑名：属于扫描归组的改动，不在本任务范围，记作后续可评估项。
