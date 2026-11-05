# A 阶段四接口裁决：独立静态检查

- 日期：2026-11-04；角色：trellis-check，未创建子代理。
- 依据：联合设计 `11-04-classical-m5-search/design.md` §15.2a 的 I1/I2/I4/I6、既有艺人/安全摘要/治理合同及本轮授权。
- 范围：只读检查后端实现与新增测试/样本生成器，执行纯 vet/build；不运行 DB、普通测试、race 或 benchmark，不开展 B 性能工作，不修改生产、其他 owner 文件或前端。

## 一、结论

**当前四项实现未发现新的确定性生产语义缺陷；A 的运行验收和样本落盘仍未由本检查判定通过。**

检查途中发现两个 app 文件重复声明 `TestArtistAdjudicationSampleOwnership`，已即时反馈主控。主控删除自己新建的独立文件，保留样本 owner 测试；复查只剩一个声明，并已加入 `artists` 父 scope 负例。此临时编译冲突已闭合，随后 vet/build 均退出 0；没有留下未修的编译阻塞。

## 二、合同覆盖矩阵

| 项目 | 独立静态证据 | 结论 |
| --- | --- | --- |
| I1 当前可恢复身份 | `governance/merged_sources.go` 只选当前 redirect 指向 canonical 目标的旧身份；读取前检查艺人 ready 和目标存在；旧 ID 子路由解析 canonical；返回当前旧身份名字/revision | 不从同名字符串或操作列表展示摘要猜身份；已恢复身份自动退出列表 |
| I1 操作来源 | `mergedSourceHistory.origin` 沿持久 before/after 类型化身份、redirect_operation_id 回溯；扁平化时 revision 不变则追溯旧来源，真正主动合并时 revision+1 且 before 无 redirect 才停止 | B→A→C 时 B 保留首次合并来源，A 使用后次事件；恢复再合并从新事件开始；不依赖易变的 changes 文本 |
| I1 名字键 | 原合并 `before.Bindings/Assignments` 的键去重；再用当前 binding 优先于 initial、再解析 redirect 的全局归属过滤 | 只含该次合并搬入且当前仍可按 aliases 拆回的键；已转给第三人的键排除；空集合显式 `[]`；scope-only 合并的键仍从 assignments 留证 |
| I1 安全与坏证据 | 管理员鉴权先于 ID 和 DB；有循环、快照版本、类型、目标、身份修订关系及证据大小检查；任何查询/证据错误整体失败，不伪装完整空列表 | 401/403、400、整理 503+Retry-After、未知 404 和依赖 503 分支清晰；只读不触发治理写入 |
| I2 any 与默认 | `artists_collections.go` 默认只取 album_artist，Roles 为 nil 且 omitempty；any 复用 artistParticipationCTE，发行级展开全部轨、轨级仅取该轨，present 参与按组去重 | 不让 missing 署名借同组/同发行的其他 present 轨变成可见参与；any 的已选组必有非空角色集合，七项角色稳定去重排序 |
| I2 分组、q 与代表 | credited_release 从实际参与版本选；代表版本从全组可见版本按既有默认版本规则选；q 按组内任一可见发行有效标题匹配，并转义 `%/_/~`；计数与分页同快照 | 不因参与过滤或命中非代表标题偷换代表版本；默认无 participation/q 的专辑艺人范围及 DTO 保留 |
| I2 参数 | participation 仅允许省略或 any；显式空值/重复值拒绝；q 复用既有 200 字节、UTF-8、控制字符规则且拒绝重复；分页沿旧上限 | 未新增不受限参数或拼接用户 SQL |
| I4 请求安全 | `httpapi/track_summaries.go` 先同源再登录校验；严格唯一 JSON 字段、UUID 校验、小写去重后 1–1000；1MiB 请求体上限 | 普通成员可读；非法 ID 不借内部函数“跳过非法输入”变成部分成功；重复大小写不占额外名额 |
| I4 响应与 refs | 直接复用 LoadTrackSummaries，按首次输入顺序重排；未知省略，missing 保留；安全 DTO 不含路径/root/指纹；refs 与状态检查在同一 SQL 快照内 | 艺人未 ready 时三类 refs 返回必传空数组且摘要仍可用；真实查询失败整体安全 503，不吞成未知 ID |
| I6 角色并集 | ListArtists 的 album_artist 条件扩为 release_artist OR album_artist；release_artist 参数仍只匹配本身；EXISTS 筛身份后计数/分页 | 双角色同人不重复，missing-only 不混入；返回 roles 词表没有折叠或重命名，前端显示合并不在本轮范围 |
| JIT wrapper | I1 通过导出的薄包装 ReadWithLocalJITOff 复用原 helper；I2/I6 经 artistSnapshot 复用同 helper | 没有第二套 SET LOCAL/事务配置实现；RR、只读、错误回滚、成功提交由原 helper 负责。I4 按裁决直接复用既有单 SQL 批量摘要入口，未擅自复制查询或引入 B 性能改动 |
| 路由和样本 ownership | 生产 handlers.Register 与 app routes 测试包含 merged-sources、POST tracks/summaries；ownsSample 优先判断 artists/adjudication 子目录 | 默认、artist-increment、artists 父 scope 均不能清理或比较本次子目录；新 scope 不能接管旧 artists/detail |

I1 的归属过滤刻意检查可直接 aliases 拆回的全局路由；第三人的作用域不会因提示而被强抢。恢复执行仍需现有 preview/basis/revision 校验，本接口只是只读提示，不绕开写入保护。

## 三、新增测试断言审查

以下表示**已读取测试代码**，不是本检查运行 PASS：

- **I1**：`merged_sources_test.go`、`merged_sources_integration_test.go` 覆盖链式扁平化原来源、恢复后再合并的新来源、同身份再次合并只提示新搬入键、当前键移走后空数组、scope-only 键、误导 changes 不影响证据、坏证据拒绝、读取不改变意图/operations；HTTP 覆盖权限/整理/未知/故障。JIT 回归在同一 PID 检查事务内 off/RR/read-only、成功/业务错误/配置失败后恢复，未用换连接掩盖会话泄漏。
- **I2/I6**：`artists_i2_i6_integration_test.go`、`httpapi/artist_i2_i6_integration_test.go` 分离发行独有/专辑独有/双角色/曲目独有，验证并集去重和分页；any 覆盖全部七角色、多版本组、非参与默认代表、字面通配符、非代表标题、missing 不借其他 present 轨、旧默认无 roles、参数边界及整理状态。未见通过放宽原断言迁就新实现。
- **I4**：`track_summaries_test.go`、`track_summaries_integration_test.go` 覆盖去重后 0/1000/1001、1001 原始输入但含重复、大小写/首次顺序、未知/缺失、null/错形状/重复及转义字段、同源/鉴权先于解码、原文/安全音频事实、整理和 ready refs、整批查询故障安全失败。
- **真实 app 样本生成器**：新增 `adjudication_samples_{integration,merged,albums,tracks}_test.go` 通过实际 app handler 取响应，复用原脱敏器；I1 用接口返回的 ID/revision/name_key_ids 真正 preview+restore；I4 在真实 merge 后立即重读 refs；I2 在参与过滤前后对默认响应完整比较，未手写 golden 或伪造治理响应。

## 四、已执行与尚未验证

本次实际执行（`backend` 工作目录）：

```bash
go vet ./internal/catalog/... ./internal/app
go build ./internal/catalog/... ./internal/app
```

`VET_EXIT=0`，`BUILD_EXIT=0`。这些检查不执行测试函数，也不连接数据库。没有启动全量测试或 DB 任务，以免与 owner 独占验证冲突。

主控告知 **I1 独占 race PASS / 0 SKIP**；本报告将其标为主控提供结果，未独立读取运行日志。I2/I6 正由 owner 占用 DB 验证；本检查不预判其最终结果，I4 与 app 样本运行结果亦待主控汇总。

审查时 `adjudicationSampleNames` 为 **35 个不重复必需样本**，目标目录实际文件 **0/35**；样本 owner 正在编写/生成。因此目前只确认生成逻辑与 ownership，**未确认样本已落盘、UPDATE 成功、无 UPDATE 逐字比较或完整性验收**。后续须核对实际 35 项、状态/正文与未越界改写旧样本，不能用历史 96+1 样本通过代替本轮增量。

本轮不评估 B 查询计划/吞吐/时延，不以小测或 vet 代替正式规模性能；既有性能失败报告不被本检查撤销。前端、浏览器、decoder 往返、队列元数据刷新交互、视觉和无障碍不在本次授权中，完全未修改。三个真实用户音乐 opt-in 仍未由本检查运行，未设置用户库路径。

## 五、最终增量复核：读取 cap 与 35 个样本

本节更新前四节的阶段性状态，不覆盖检查历史。

### I1 非合同读取上限

主控指出的三项隐形拒绝门槛，已逐项确认从当前源码移除：

1. 来源查询无 `LIMIT 10001`，也不再因来源数超过 10000 返回错误。
2. 历史缓存不再因达到 10000 个操作拒绝新证据。
3. 不再累计 before/after 字节并以 64MiB 为限返回错误。

`origin` 每轮检查请求 context；操作缓存、循环检测、类型化结构/目标/redirect/修订关系的损坏 fail-closed 保留。`TestArtistMergedSourcesHistoryHasNoAggregateCap` 经 history.evidence 路径读取 10001 份、每份约 7KB 的证据，确实跨越旧数量及累计 64MiB 两个门槛，而非只测声明常量。它不是 10001 个来源的真实 HTTP/DB 集成验证；本检查未执行该测试。

**尚待 owner 处理的 P2：逐份序列化字节的 8MiB 读取拒绝仍存在。** 审查时 `decodeMergedSourceEvidence` 仍有 `len(record.Before/After)>8<<20`。这不能直接等同于既有 `boundedArtistState`：后者检查 `json.Marshal(state)` 的紧凑字节，operations 写入转换为 JSONB，读取 `scanRecord` 直接保留数据库返回的 JSON 字节，未恢复为原紧凑表示。边界内合法写入可能因序列化表示变化超过读取上限而新报 503。已反馈主控，建议移除这项读取字节门槛；不应以“安全检查”代替合同依据。本节更正第二节对“证据大小检查”的笼统认可。

单份 before 中名字键数量校验对应原 MergeExecutor 的每次合并键数上限；当前归属 SQL 每 1000 个键分批会完整遍历全部键，并不是接口总数限制。两者与任意限制来源总数/累计历史大小不同。

### 样本当前已落盘

已用纯 Python 文件解析确认：必需清单 **35 个、无重复**，实际目录 **35 个、无缺失、无多余**；全部为 `{status,body}` JSON 信封。此次文件审计还验证：

- merged-sources 从空到合并来源、恢复后空、键已移走后 `name_key_ids=[]`；权限/整理/未知/非法状态分别落盘。
- any 专辑 roles 非空且无重复，默认 DTO 无 roles，读取 any 前后默认样本完全相同；q 和分页场景独立存放。
- I6 样本 album_artist 并集为两人，release_artist 单角色为一人。
- 批量曲目样本 4 个唯一 ID，其中 2 个 missing；整理样本的曲目及发行两类 refs 均为必传空数组；合并后刷新样本已存在。
- 35 个正文递归检查未出现 relative_path/source_root_id/password_hash/source_path/content_hash 私有键。

主控报告这 35 个样本已真实 UPDATE 生成并完成无 UPDATE race 比较，PASS / 0 SKIP；这是主控提供的运行结果，本检查只确认落盘及上述文件内容，未亲自运行或读取该轮完整日志。综合 I1/I2/I4/I6＋原读取＋样本 race 仍由主控执行，不提前声称通过。

**截至本节：三项非合同累计 cap 已闭合，样本落盘缺口已闭合；逐份 8MiB 读取门槛问题待 owner 反馈。** 我没有运行 DB、测试或 B 性能任务，也未修改 owner 文件或前端。

## 六、逐份 8MiB 读取限制修复复核（最终定点追加）

**第五节剩余 P2 现已关闭；当前 A 范围没有本检查尚未关闭的确定性问题。** 已重新读取最新文件，`decodeMergedSourceEvidence` 不再对 before/after 原始读取字节长度设 8MiB 拒绝门槛；没有恢复之前已移除的来源数量、历史条数或累计字节 cap。类型化解码、快照版本/身份/键合法性校验、context 取消、缓存及循环检测仍在，未以移除任意读取预算为由放开损坏证据。

新增 `TestArtistMergedSourcesEvidenceAllowsReadbackWhitespace` 先通过原 `boundedArtistState` 检查并序列化合法前后态，分别仅扩展 before 或 after 的 JSON 空白至超过 8MiB，使用 `json.Compact` 核对内容与原紧凑字节完全相同，再对解析结果做完整 `reflect.DeepEqual`。它针对性证明读取不再因等价 JSON 的表示长度拒绝数据；这是纯解码回归，不冒称实际 PostgreSQL JSONB 往返测试，也没有放宽原治理写入预算。

主控提供该纯测试 race PASS；本次我仅执行文件读取和文本检索，**未运行测试、DB、性能或再次编译**。最终 A composite race 正由主控安排，结果尚未在本报告核验。35 项样本文件审计及前述 vet/build 结果保留；A 静态问题闭合不等于 B 性能或整项产品验收通过。
