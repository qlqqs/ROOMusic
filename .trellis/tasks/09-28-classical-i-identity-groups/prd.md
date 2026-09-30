# 阶段 I：身份稳定与 ReleaseGroup 只读

> 状态：planning，未启动。2026-09-28 由[路线修订任务](../archive/2026-09/09-28-core-1-roadmap-revision/design.md)（第二部分）创建，本文是 PRD 初稿；
> 启动前需按 Trellis 流程完成规划并获批准。父任务见 [Classical 重设计 / Core 1](../09-27-classical-core-1/prd.md)。

## 目标与用户价值

让 Track、Release 的 UUID 在重扫、增删文件、同一 root 内改名或移动后保持不变，使后续的修正（M3）、
播放历史（P）、收藏与歌单（M5）不会因为整理文件夹而与实体脱钩；并提供按组读取的接口供 M1b 使用。

## 依据

- [ADR 0002](../../../docs/architecture/adr/0002-metadata-overlays.md) 的决策记录（D2、D6）与第四、六、九节。
- [Core 1 路线](../../../docs/architecture/core-1-roadmap.md) 的 I 行。
- 现状：曲目身份取决于相对路径（`internal/catalog/identity.go:21`），拆分候选的锚点取组内最小路径（`internal/catalog/organizer.go:922`），改名、移动或增删文件都可能换 ID。

## 范围

1. **音频指纹**：新增迁移，为 `tracks` 增加指纹及其种类/版本列。FLAC 取 STREAMINFO 的音频 MD5（非零时）加总样本数、采样率、声道、位深（`internal/library/parser/parser.go:1556` 已解析该块）；其他格式（ALAC/M4A、MP3、OGG/Opus、WAV）的指纹在本任务研究后确定，要求与标签无关、只在候选配对时计算；CUE 虚拟轨 = 父文件指纹 + 区间。
2. **同 root 保守重绑定**：只在完整成功的扫描中进行；候选集 = 本轮没有身份匹配的新观察 × 本轮将被置为缺失的旧曲目；只有唯一 1:1 指纹相等才重绑定（更新相对路径与来源身份，保留 UUID）；否则保持分离、记录诊断、留待人工认领；取消、失败、不完整的扫描一律不重绑定。
3. **Release 成员关系匹配**：候选先解析出所含曲目的 UUID（含重绑定结果），复用“包含其多数曲目、且没有被重叠更高的候选占用”的既有 Release；锚点降为定位信息；平局规则确定且可测试。
4. **ReleaseGroup 只读接口**：`GET /api/v1/release-groups`（分页、`q`、`attention`、present-only 语义与 `/releases` 对齐；组摘要 = 代表版本字段 + 版本数 + 封面）与 `GET /api/v1/release-groups/{id}`（组摘要 + 成员 Release 摘要）。代表版本：默认版本（M4 前为空）→ 最早年份 → 最早创建。Core 1 不做自动跨版本归组（D6）。

## 前置依赖

B0 的 catalog 部分（`internal/catalog`，已随 B0 于 2026-09-30 完成）。M1b、M3、P 依赖本任务。

## 验收标准（初稿）

- 使用合成 fixture 的 PostgreSQL 集成测试矩阵全部通过：未变化重扫；新增排序更靠前的文件；删除最小路径文件；目录改名；同 root 内移动文件；两份音频相同的文件（不重绑定）；取消或不完整扫描（不重绑定、不置缺失）；跨 root 移动（新身份）；同路径换内容（指纹不同时不静默沿用原身份）。
- `/releases` 的既有行为不变；没有人工归组时，按组接口每组一个版本，与 Release 列表一一对应。
- `.trellis/spec/backend/catalog-rest-contracts.md` 增补按组接口合同；迁移只新增，不改旧迁移。

## 验证方式

`go test ./... -count=1`、`go test -race ./... -count=1`、`./scripts/test-integration.sh`。可选一次显式、只读的真实库冒烟
（`scripts/real-library-smoke.sh`），对比重扫前后的 ID，输出不含路径与曲目清单。

## 风险

锚点语义变化需要兼容旧数据（参照 0011 的保守升级方式）；指纹计算成本（只对候选配对计算）；CUE 边界；大库扫描时间。

## 范围外

人工归组及任何写操作（M3）；默认版本与版本对比（M4）；自动强证据归组；外部元数据源；前端页面（M1b）。

## 待规划事项

- 非 FLAC 格式的指纹方案与成本评估。
- 指纹列的版本化与回填策略（旧数据在首次扫描时计算）。
- 按组接口的分页与搜索语义细节。
- 是否为未认领的重绑定候选提供管理员可见的诊断。
