# V0 参考与当前缺口

## 使用边界

`../ROOMusic-V0` 是历史行为证据，不是当前项目依赖。只抽取字段语义、规则和测试
样例；当前 Core 0 的 PostgreSQL、版本化 REST、只读根、后端权限和 complete-only
missing 合同优先。

## 可复用的行为参考

- `internal/scanner/grouping.go`：按 album/album artist 组织候选、字段多数决、folder
  evidence、rip-log source/media 证据的行为样例。
- `internal/scanner/assembler.go`：多碟 Medium、CUE 虚拟 Track、真实分轨优先、父来源
  和音频事实传递的行为样例。
- `internal/scanner/cueparser.go` 与 `cueparser_test.go`：多 `FILE`、INDEX、编码和
  CUE 结构测试样例。
- `internal/scanner/audioprops.go`、`tagparser.go`：音频事实、标签字段和 fallback 语义。
- `internal/scanner/reconciliation.go` 与 `docs/scan-reconciliation.md`：成功扫描才执行
  负向对账的状态边界。

## 当前项目需要重新实现的部分

- 当前实现只有 `audioObservation`/`sourceObservation` 的过渡字段；V0 的大型 scanner、
  GraphQL、旧数据库和 runtime 不应迁移。
- V0 的证据/artist/label 模型必须缩减到本任务的 bounded current decisions、grouping
  evidence 和安全 REST 投影，不能引入人工治理或 AI ledger。
- V0 的文件系统操作、队列、generation/fencing 方案不自动继承；只保留当前 scanner
  的 advisory lock、取消和 PostgreSQL authority。

## 本任务的验证结论

- 真实 M4A 失败是 parser 的有界读取和 atom 定位缺陷，不是 smoke test 误报；合成测试
  必须覆盖大文件和 moov 后置布局。
- 当前 CUE scanner 只取首个 `FILE`，因此必须先修复 observation contract，再修复
  organizer 去重和 persistence，不能只在 UI 增加字段。
- 当前 candidate evidence 是 append-only 且 artwork 在 Release 确定前保存，必须以
  current replace 和 candidate-aware artwork link 作为数据库/文件 adapter 验收点。
