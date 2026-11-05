# 在线回填 503 修复：独立增量检查

- 日期：2026-11-04；检查角色：trellis-check，未创建子代理。
- 范围：graph 范围化、0028 迁移、maintenance 身份阶段、在线写入回归、正式规模诊断隔离。
- 方法：只读源码与任务合同；仅执行下列纯 vet/build。未连接数据库、未执行测试或 benchmark，未修改生产、其他 owner 测试、前端或 spec。
- 本报告不覆盖 `check-report.md` 的旧失败记录，也不是性能通过声明。

## 一、结论与剩余问题

**发现一项 P2 正确性缺口，已反馈主控；除该项外，本次增量静态未发现新的阻塞问题。** 新代码把历史全库 profile 补齐移出扫描事务，方向与已取证根因一致；但“显式身份数量有界”不证明查询成本或 CATP 持有时间达标。

### P2：ready 后新建、被 M3 遮盖的 scan 身份可能永久缺 profile

审查时的代码路径：

1. `artistgraph/facts.go` 仍解析并保存 scan 层发行署名，即使该发行已有 `album_artist` 修正。
2. `artistgraph/names.go` 的 `nameResolver.ensure` 为首次出现的规范化键创建 `artists`、key、name。
3. `artistgraph/refresh.go` 的 `refreshEffective` 正确排除被 correction 遮盖的发行 scan 层；但 `Refresh` 的 profile 集合只有变更前后有效署名身份，未包含上述本次新建却无有效署名的身份。
4. 目录已 ready 时，`maintenance/artist_worker.go` 直接返回，独立 profiles 清单不会再发现它；重启同版本也不应强制重建。
5. `catalog/artists_reads.go:LoadArtist` 能解析该真实 ID，但摘要 inner join profile 后漏项，返回 `ErrQuery: missing artist profile`，而不是合同要求的安全空身份摘要 `available=false`。早先即时反馈曾推测为 404，已更正为此实际错误路径。

建议仅收集**本次 facts 新建身份**并加入同事务显式刷新集合，不恢复全库 missing-profile 查询。应补 ready 后“已有 M3 覆盖→重扫产生全新原始发行署名”的测试，断言新身份 profile/搜索名字/代次完整、scan 层仍不进入有效署名，且无关孤立身份不被顺带刷新。此次检查未自行修改或执行该测试。

## 二、静态覆盖矩阵

| 范围 | 已核查内容 | 结论及限制 |
| --- | --- | --- |
| 在线 Refresh | 空输入直接返回；只收集指定发行旧/新有效身份；失去最后署名的旧身份仍刷新；事实、有效署名、profile、搜索名字、候选及发行 marker 使用同一传入事务 | 原全库无 profile 兜底已删除；上节新建隐含身份边界未闭合 |
| 主名和 ownedNames | 请求身份自身路由及指向它的一跳来源；binding 优先；作用域有效署名可共享名字；present subject/name 跨角色去重票；票数降序、原文 C 排序、name ID；无票回退；人工主名及合并来源人工名保留 | 未见算法语义降级；`profiles_scope_integration_test.go` 用旧集合定义的双向 EXCEPT 校验路由等价，另断言 seed、scope、redirect、manual |
| 查询成本 | ownedNames 从显式身份沿 initial/binding/redirect 索引找名字；popular/fallback 集合聚合代替逐身份重复计算 | 查询形状收窄，不等于计划或耗时证明；热门身份仍可展开大量署名/名字，须原规模实测 |
| 0028 | profile 新增非负 applied_generation；两张清单允许 artist 类型；无全库派生重算，无版本强升 | 既有 ready 不因默认 marker=0 被撤销；本轮只静态检查，未执行迁移 |
| 身份清单 | 初始快照发现全部 artists（含孤立种子）；独立 artist subject_kind；release 阶段优先；profile 独立批次限额及适应值 | 对已有孤立身份不依赖发行署名；初始分母不被修复阶段改写 |
| 批次/锁序 | CATM 专用连接选主；批次在同连接事务取 CATP 后重读目标和 marker；派生/marker/进度一起提交；失败或取消回滚；提交后让出 50ms | 未发现新增反向拿 OPER/发行锁路径；没有在扫描或发布事务重新规范化全库 |
| 发布缺口 | release 与 artist 双 marker 缺口检查；发现缺口先回滚发布事务再建修复清单；发布要求 profiles 阶段存在；收藏解析和 ready 同事务 | 能捕获初始清单之后、UUID 排序更小的新身份；发布仍涉及全体缺口验证及既有收藏映射，不应凭静态审查宣称是低成本事务 |
| 兼容/续跑测试 | `artist_profiles_integration_test.go`：独立小批、释放 CATP、在线已完成跳过、重启续跑、迟到 UUID、旧 sealed build、ready 零 marker、下一代刷新、失败/取消回滚、零搜索行 | 测试断言与目标一致；本检查未执行，不把文件存在写成 PASS |
| 新在线 app 回归 | 20k 身份/24k 名字且未预填 profile；真实 worker 已提交搜索批次处暂停，不持 CATP；在线请求前后仍有 pending 且至少 19k profile 缺口 | 能捕捉扫描顺带全库补 profile 的原失败类别；不能代替 worker 实际繁忙时的竞争/规模性能验证 |
| 真扫描完成 | POST 202 后 `WaitForScans`、scan_runs=succeeded、真实 present 轨入库及有效 track/composer 署名；整个流程受 5 秒 context/耗时断言约束 | 不是只断言受理；未设置真实用户音乐路径 |
| M3 完成与续跑 | PATCH 200 且 5 秒内、有效 album_artist 图立即变化、legacy 搜索立即可见；放行 worker 后 normalized/ready、全身份同代无缺口、别名与真实参与关系可搜 | 5 秒为工程回归上限，不是合同原有 SLA；本检查未执行该回归 |
| 规模语义 | 100k present+10k missing，20k 参与身份、4k 不同 key 别名、每轨 2 艺人/4 credit、50k 热门倾斜的结构断言保留；三栏并发、5 次预热/30 次测量、原阈值保留 | 没有用局部回归或选择性测试冒充完整规模验收 |
| 诊断隔离 | 成功在线阶段后先 `stopOnline`：取消并等待采样 goroutine，关闭独立 monitor pool，关闭详细 trace，再 AnalyzeSchema、EXPLAIN、预热和正式测量；失败清理前 checkpoint 保留未结束事务 | 静态顺序正确；原请求 45 秒预算未被延长；普通请求数据库计时保留 |

## 三、实际执行的验证

工作目录 `backend`：

```bash
go vet ./internal/catalog/... ./internal/library/... ./internal/operations/... ./internal/personal/... ./internal/app ./internal/platform/postgres/...
go build ./internal/catalog/... ./internal/library/... ./internal/operations/... ./internal/personal/... ./internal/app ./internal/platform/postgres/...
```

两条命令顺序执行，整体退出码 0，无错误输出。此结果只覆盖当时落盘代码的静态检查/生产编译，不等同新测试运行、race、SQL执行或性能通过；后续 owner 修改须另行验证。

## 四、性能与完整验收边界

- 历史完整规模 `scale-full-20261104T200228Z`：FAIL，73.972s 回填阶段 M3 correction 503，未到 p95/EXPLAIN；历史失败仍保留。
- 主控根因证据 `repro-online-20261104T201219Z`：扫描持 CATP 约 47 秒，主名 SQL 的身份参数 20002 项、执行约 45.7 秒；M3 等待约 44.997 秒触发 45 秒测试 deadline；worker 也是等待方。此处依据主控已落盘根因报告，不声称本检查重跑复现。
- 本次静态修复审查不能关闭性能门禁。仍须主控在修正正确性缺口后汇总实际回归/race、原规模在线扫描/M3/播放、逐批及 CATP 时长、正式 p95/EXPLAIN。profileLimit 最大 100 只限制身份数，不限制单身份关联量，更不是 100–200ms 达标证据。
- 三个真实用户音乐 opt-in 仍因安全约束未运行；本检查未设置用户库路径。此前样本、旧 app 断言及 AnalyzeSchema allowlist 审计保留在旧报告，本次没有重复执行；前端、嵌入资源、浏览器、视觉/无障碍不属于本次增量范围。

## 五、P2 修复后的定点复核（2026-11-04，追加，不替换历史）

**当前结论：第一节 P2 在本次修复范围内关闭。** 上文保留的是修复前发现，不再表示当前代码仍有该缺口。本次只复核 Kepler 交付的 `names.go`、`facts.go`、`refresh.go` 和 `masked_scan_integration_test.go`，未发现新的确定性问题。

- `nameResolver` 仅在本次成功插入新 `artists` 行后记录 `createdArtistIDs`；既有键命中、同次名字缓存命中不会扩展成全库身份集合。记录先于 key/name 插入不造成半提交：后续任一步出错均返回错误，`Refresh` 不继续执行派生，仍由原事务统一回滚。
- `refreshFacts` 返回此次解析新建的身份，错误继续向上传递；原始署名保留、correction 遮盖、去重和事实 upsert 逻辑未改变。
- `Refresh` 对 `old ∪ current ∪ created` 去重后刷新 profile/搜索名字及目标代次。因此被 M3 遮盖、从未进入有效署名的新身份也在同事务获得完整派生；没有恢复全库无 profile 查询，没有新开事务或改变 CATP 锁序，也不写治理意图。
- `TestPostgreSQLArtistGraphReadyMaskedScanCreatesProfile` 在 ready 条件下引入新原始发行署名，断言两个 scan 角色事实保留、有效署名仍为零、profile 与搜索名字均为第 7 代，并检查无关孤立身份没有被补齐。最后真实提交事务，再从数据库通过 `catalog.NewStore(...).LoadArtist` 读取，断言新 ID/名字正确、`available=false`、专辑及曲目计数均为零，直接覆盖原先 `missing artist profile` 错误路径。
- 该定点测试调用真实 graph 和 catalog 读取，但以 SQL 构造重扫后的事实输入及 ready 前提，不应将它单独描述为 scanner/HTTP 全链路测试；已有真实扫描回归仍承担那部分覆盖。

### 本次验证与外部结果的区分

本检查仅执行（工作目录 `backend`）：

```bash
go vet ./internal/catalog/internal/artistgraph
go build ./internal/catalog/internal/artistgraph
```

退出码 0。**我未运行 DB、race、普通测试或性能基准。** 主控提供的 Kepler 最小 4 项 race PASS / 0 SKIP，以及 maintenance 独占全包 race PASS / 30 tests / 0 SKIP，作为外部运行结果记录，不冒称本检查亲自执行；此前并发失败日志继续保留。

完整影响 race 由主控执行中，本次未核验其最终结果；正式原规模性能验收仍未由本报告判定通过。P2 关闭仅表示此正确性缺口的代码和针对性断言已闭合，不覆盖或撤销第四节历史性能失败。
