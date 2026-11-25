# 自动层实施报告

发行和曲目原先都在重扫后继续显示旧自动值。修改前的两个真实重扫用例验证了这一点。0088 在两张自动表各增加写入方级别和原始扫描字段的 JSONB 依据。旧行保留，缺少依据时跳过作废比较。四级取值、旧行的级别判定、事务锁和摘要见[设计](./design.md)。

## 改动文件

| 目录 | 文件 |
| --- | --- |
| 本任务 | `design.md`、`report.md` |
| `backend/migrations/` | `0088_auto_value_basis.sql`、`0088_auto_value_basis.down.sql` |
| `backend/internal/catalog/` | `fields.go`、`curation_evidence.go`、`internal/releasesql/release.go` |
| `backend/internal/catalog/governance/` | `auto_values.go`、`auto_revert.go`、`auto_invalidation.go`、`corrections.go`、`track_auto_values.go`、`track_auto_revert.go`、`track_corrections.go` |
| `backend/internal/curation/` | `writer.go`、`auto_invalidation.go`、`model_api.go`、`stewardtools/correct.go` |
| `backend/internal/curation/program/` | `enrich.go`、`enrich_media.go`、`model_matcher.go`、`pass.go`、`track_fields.go`、`unit.go` |
| `backend/internal/app/` | `curation_model.go` |

## 前端操作记录

`type` 沿用 `release_auto_correction` 或 `track_auto_correction`，撤销记录为 `revert`。新增两种 `changes.kind`。以下为曲目作废的形状，发行作废使用 `release_auto_invalidation` 并省略 `track`。

```json
{
  "kind": "track_auto_invalidation",
  "track": {"id": "<曲目 UUID>", "title": "新标签曲名"},
  "fields": [{
    "field": "title",
    "before": {
      "auto": {"mode": "set", "value": "自动曲名", "operation_id": "<原写入操作 UUID>", "writer_level": 3, "scan_basis": "原曲名"},
      "effective": "自动曲名",
      "scan_value": "新标签曲名"
    },
    "after": {"auto": null, "effective": "新标签曲名", "scan_value": "新标签曲名"}
  }]
}
```

显式设空的自动意图使用 `mode:"null"`，省略 `value`。人工覆盖存在时两侧的 `effective` 都是人工值。撤销作废沿用同一个 `changes.kind`，恢复原操作归属，并用确认时的扫描值作为新依据。

## 验证

- `cd backend && mise exec -- go build ./... && mise exec -- go vet ./...` 通过。
- `cd backend && mise exec -- go test ./internal/curation/... ./internal/catalog/...` 通过。需要数据库的用例另用隔离脚本运行。
- 集成入口均为 `ROOMUSIC_TEST_RUN='<表达式>' mise exec -- ./scripts/test-integration.sh`，通过本机 Docker 组执行。
- 修改前用 `^TestPostgreSQLCuration(Rescan|TrackAutoSurvivesRescan)$` 验证两项旧行为，均通过。
- 实施后用 `^TestPostgreSQL(Curation(FieldPriority|RevertOrder|BatchRevert|ProgramTrackFields|Suppression|ModelApply|ProgramRescanAndChanges)|TrackAuto(Apply|Revert|BatchRevert|ProviderRevert))$` 和 `^TestPostgreSQLCurationModelCorrect(Release|Track)AutoLayer$` 验证 13 个顶层用例，均通过。
- 隔离数据库中用合成发行、曲目和自动行模拟扫描字段更新。整理程序读取证据前的有效值均是自动标题，读取后均变为新扫描标题。真实操作列表返回两种作废记录、旧依据和新扫描值；两次 HTTP 撤销均为 200。恢复后逐格查到原级别和新依据，作废及撤销后的抑制标记都为 0。
- 另用 `^TestPostgreSQL(CurationRevertOrder|TrackAutoRevert|CurationGapAndIdentityChanges)$` 检查展示及摘要。两项撤销通过，一项失败，详情见下。

## 失败与验收分工

`TestPostgreSQLCurationGapAndIdentityChanges` 在 `backend/internal/app/curation_versions_integration_test.go:148` 期望初次无候选时为 `no_match`，实际为 `unlisted`，原因为 `model_unconfigured`。在本次改动前的提交 `a1fefd49` 独立重跑该用例，复现相同失败。该断言早于三个缺口子用例，符合子任务 C 合同第 23.5 节记录的状态变化。现有断言保留。

四级覆盖矩阵、V11 的新增回归用例和 M 度量属于测试代理。前端增加上述两种展示类型、合同和 ADR 更新属于主代理。本次没有提交 Git。

## 测试代理补充

| 用例 | 验收覆盖 |
| --- | --- |
| `TestPostgreSQLCurationGapAndIdentityChanges` | 零候选且模型未配置时是 `unlisted / model_unconfigured`。C 增加了去掉曲目数的放宽查询，本夹具每轮识别有两次发行搜索。保留封面、歌词和年份只改变缺口与完整摘要，以及标题、条码和曲目时长变化各触发一轮识别的断言。 |
| `TestPostgreSQL自动值随标签重扫作废且可撤销` | V11。用同一合成 FLAC 重写发行与曲目标题标签并真实重扫，读取整理证据后从普通成员的发行详情看到新标题。核对两种可撤销操作的原值、原依据、新扫描值和曲目标识。管理员撤销后恢复原自动写入归属和级别，以新标签作为依据。作废和撤销均无“不再提出”标记。 |
| `TestPostgreSQL自动写入四级先后矩阵` | 对发行与曲目各覆盖四个同级替换及六对不同级别。逐对验证高级替换低级、低级全被拒返回 409 `nothing_to_apply`、可写字段与被拒字段混合时部分提交。核对逐字段 `lower_priority` 与保留的实际写入归属。 |
| `TestPostgreSQL没有扫描依据的旧自动值保留` | 模拟升级前自动行的 SQL NULL 级别和依据，在真实重扫并读取整理证据后，两层自动值及其归属都保留，无作废记录。 |
| `TestPostgreSQLAPIResponseSamples` | 用真实操作列表 handler 扩充 `operations/link-list`，加入两个自动值作废记录和一个带 `release_group_match` 的外部对应。前端全局样本契约已登记该样本。录制同时同步了 C 的整理进度样本和当前迁移版本 88。 |

现有撤销用例 `TestPostgreSQLCurationBatchRevert`、`TestPostgreSQLCurationBatchRevertKeepsManualEdit`、`TestPostgreSQLCurationProviderCursor`、`TestPostgreSQLTrackAutoBatchRevert`、`TestPostgreSQLTrackAutoProviderRevert` 均通过。

### 执行命令与结果

```bash
(cd backend && mise exec -- go test ./internal/curation/... ./internal/catalog/...)

ROOMUSIC_UPDATE_API_SAMPLES=1 \
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-f-contract \
ROOMUSIC_TEST_RUN='^TestPostgreSQL(Curation(GapAndIdentityChanges|BatchRevert|BatchRevertKeepsManualEdit|ProviderCursor)|TrackAuto(BatchRevert|ProviderRevert)|自动值随标签重扫作废且可撤销|没有扫描依据的旧自动值保留|自动写入四级先后矩阵|APIResponseSamples)$' \
sg docker -c 'TMPDIR="$PWD/temp/f-contract" mise exec -- ./scripts/test-integration.sh'

ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-f-contract-recheck \
ROOMUSIC_TEST_RUN='^TestPostgreSQL(CurationGapAndIdentityChanges|APIResponseSamples)$' \
sg docker -c 'TMPDIR="$PWD/temp/f-contract-recheck" mise exec -- ./scripts/test-integration.sh'

(cd frontend && mise exec -- npx vitest run src/features/governance)
```

Go 包测试通过。集成首轮录制了样本，10 个顶层用例中 9 个通过，摘要用例失败在原有的单轮搜索次数断言。修正为每轮两次发行搜索后，摘要用例和 API 样本逐字复核均通过。前端治理测试 9 个文件、217 项通过，临时依赖链接已删除。

两种 M 度量均达到验收门槛。四个隔离 Compose 项目均已确认无残留容器、卷或网络。[度量报告](./research/metrics-2026-11-25.md)

### 样本契约问题

- `API 样本契约 > 成功样本 operations/link-list 被 decoder 完整解码` 会比较解码结果与真实样本。两个作废分支的自动值解码都遗漏 `writer_level`、`scan_basis`，会把这两个键丢掉。组级对应的 `object_type` 已在解码器枚举内。
- `API 样本契约 > 成功样本 curation/progress 被 decoder 完整解码` 也存在 C 留下的解码漂移。进度解码器遗漏 `releases.no_usable_candidates`、`releases.unlisted`。
- 上述问题由比对真实 JSON、解码器字段和契约断言发现。全局契约位于 `frontend/src/test/api-contract.test.ts`，超出本次指定的 Vitest 运行路径。
